// Package lineedit is the terminal line editor behind the mzs REPL: raw-mode input with
// the arrow keys, an in-memory history, the emacs editing keys every shell already
// answers to, and live colouring of the line while it is being typed.
//
// The package knows nothing about mzs. What a line *means* arrives as two function
// values — a Highlighter that paints runes and a Completer that proposes words — so the
// editor can be driven by a scripted terminal in a test with no interpreter in sight,
// and the colours of §3 stay in one place (internal/highlight).
//
// Everything here degrades instead of failing. FileTerminal says no when either end is
// not a console, MakeRaw returns an error the caller can fall back from, and a terminal
// that will not say how wide it is gets 80 columns. A REPL that cannot enter raw mode
// must still read lines, so the caller keeps its plain reader for exactly that case.
//
// Only one physical line is edited at a time. That is not a limitation the REPL has to
// work around: an unclosed bracket already reads the next line at its own `...>` prompt
// (§5.6), so the editor never has to own a rectangle of text and the whole cursor
// arithmetic below stays one-dimensional — one row, scrolled sideways when the line
// outgrows the window.
package lineedit

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// ErrInterrupted reports a Ctrl-C. The line is abandoned; the session is not, which is
// why this is a distinct error from io.EOF — Ctrl-C returns to the prompt and Ctrl-D on
// an empty line ends the session.
var ErrInterrupted = errors.New("lineedit: interrupted")

// Terminal is the console an Editor drives. It is an interface rather than an *os.File
// so that the tests can supply a scripted keyboard and collect what was drawn.
type Terminal interface {
	// Read delivers the keys as raw bytes, undecoded and unechoed.
	io.Reader
	// Write draws. The editor writes only CR, LF and CSI sequences every terminal
	// emulator has answered to since the VT100.
	io.Writer
	// MakeRaw switches the terminal to raw mode and returns the undo. The editor calls
	// it around a single ReadLine, so a program that runs between two lines still sees
	// the terminal the way the shell left it — Ctrl-C included.
	MakeRaw() (restore func() error, err error)
	// Width is the terminal's column count, or 0 when it cannot be determined.
	Width() int
}

// Highlighter returns one SGR parameter string per rune of src — "31" for red, "1;34"
// for bold blue, "" for whatever the terminal draws by default. It must return exactly
// len(src) entries; an implementation that cannot is ignored rather than trusted.
type Highlighter func(src []rune) []string

// Completer proposes replacements for the word ending at pos. It returns the rune index
// where that word starts together with the candidates, each of which must begin with
// line[start:pos] — the editor replaces the range rather than appending to it.
type Completer func(line []rune, pos int) (start int, candidates []string)

// DefaultMaxHistory is how many lines an Editor remembers when MaxHistory is 0.
const DefaultMaxHistory = 500

// maxHighlight bounds the line the highlighter is asked about. Colouring is a full lex
// of the buffer on every keystroke, which is nothing for a REPL line and not worth doing
// for a pasted megabyte; past this the line is drawn plain and still edits normally.
const maxHighlight = 4096

// Editor reads lines from a Terminal. The zero value is not usable; call New.
type Editor struct {
	// Highlight paints the line as it is typed. nil draws it in the terminal's default
	// colour, which is also what happens when the user has asked for no colour at all.
	Highlight Highlighter
	// Complete answers the Tab key. nil makes Tab do nothing, which is better than
	// inserting a tab into a line the lexer would then have to explain.
	Complete Completer
	// MaxHistory caps the remembered lines; 0 means DefaultMaxHistory.
	MaxHistory int

	term Terminal
	in   *bufio.Reader
	hist []string
}

// New returns an Editor drawing on t.
func New(t Terminal) *Editor {
	return &Editor{term: t, in: bufio.NewReaderSize(t, 1024)}
}

// History returns the remembered lines, oldest first.
func (e *Editor) History() []string { return append([]string(nil), e.hist...) }

// Remember adds a line to the history the arrow keys walk. Blank lines and an immediate
// repeat are dropped: neither is ever what Up was reaching for.
func (e *Editor) Remember(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if n := len(e.hist); n > 0 && e.hist[n-1] == line {
		return
	}
	e.hist = append(e.hist, line)
	max := e.MaxHistory
	if max <= 0 {
		max = DefaultMaxHistory
	}
	if len(e.hist) > max {
		e.hist = append([]string(nil), e.hist[len(e.hist)-max:]...)
	}
}

// ReadLine draws prompt, edits one line and returns it without its terminator. The
// prompt may carry SGR escapes; they are measured out of its width so the cursor still
// lands where the text does.
//
// It returns io.EOF when the input ends (Ctrl-D on an empty line, or a closed terminal)
// and ErrInterrupted on Ctrl-C. An accepted line is remembered.
func (e *Editor) ReadLine(prompt string) (string, error) {
	restore, err := e.term.MakeRaw()
	if err != nil {
		return "", err
	}
	defer restore()

	s := &session{
		e:      e,
		prompt: prompt,
		pw:     displayWidth([]rune(stripSGR(prompt))),
		idx:    len(e.hist),
	}
	line, err := s.run()
	if err != nil {
		return "", err
	}
	e.Remember(line)
	return line, nil
}

func (e *Editor) write(s string) { _, _ = io.WriteString(e.term, s) }

// session is one ReadLine: the buffer being edited, where the cursor is in it, and where
// the history cursor stands. idx == len(hist) means "the line being typed", whose text is
// parked in saved while the user walks back through older lines.
type session struct {
	e      *Editor
	prompt string
	pw     int
	buf    []rune
	pos    int
	idx    int
	saved  string
}

func (s *session) run() (string, error) {
	s.refresh()
	for {
		k, err := s.e.readKey()
		if err != nil {
			// The terminal went away mid-line. Whatever was typed was never accepted,
			// so this is the end of the input and not a line.
			return "", io.EOF
		}
		switch k.kind {
		case keyRune:
			s.insert(k.r)
		case keyEnter:
			// Park the cursor at the end first: on a line that had scrolled sideways
			// this redraws the tail, so what stays on the screen is what was accepted.
			s.pos = len(s.buf)
			s.refresh()
			s.e.write("\r\n")
			return string(s.buf), nil
		case keyInterrupt:
			s.e.write("^C\r\n")
			return "", ErrInterrupted
		case keyEOF:
			// Ctrl-D is two keys in one: end of input on an empty line, forward delete
			// on a line with something in it. Both are what every shell does.
			if len(s.buf) == 0 {
				return "", io.EOF
			}
			s.deleteAt()
		case keyBackspace:
			if s.pos > 0 {
				s.pos--
				s.buf = append(s.buf[:s.pos], s.buf[s.pos+1:]...)
			}
		case keyDelete:
			s.deleteAt()
		case keyLeft:
			if s.pos > 0 {
				s.pos--
			}
		case keyRight:
			if s.pos < len(s.buf) {
				s.pos++
			}
		case keyWordLeft:
			s.pos = wordStart(s.buf, s.pos)
		case keyWordRight:
			s.pos = wordEnd(s.buf, s.pos)
		case keyHome:
			s.pos = 0
		case keyEnd:
			s.pos = len(s.buf)
		case keyUp:
			s.history(-1)
		case keyDown:
			s.history(+1)
		case keyKillToEnd:
			s.buf = s.buf[:s.pos]
		case keyKillToStart:
			s.buf = append([]rune(nil), s.buf[s.pos:]...)
			s.pos = 0
		case keyKillWordLeft:
			start := wordStart(s.buf, s.pos)
			s.buf = append(s.buf[:start], s.buf[s.pos:]...)
			s.pos = start
		case keyKillWordRight:
			end := wordEnd(s.buf, s.pos)
			s.buf = append(s.buf[:s.pos], s.buf[end:]...)
		case keyTranspose:
			s.transpose()
		case keyClear:
			// Home the cursor and clear the screen; the line itself is redrawn below,
			// which is the whole point of Ctrl-L: keep typing, lose the mess.
			s.e.write("\x1b[H\x1b[2J")
		case keyTab:
			s.complete()
		case keyIgnore:
			continue
		}
		s.refresh()
	}
}

func (s *session) insert(r rune) {
	s.buf = append(s.buf, 0)
	copy(s.buf[s.pos+1:], s.buf[s.pos:])
	s.buf[s.pos] = r
	s.pos++
}

func (s *session) deleteAt() {
	if s.pos < len(s.buf) {
		s.buf = append(s.buf[:s.pos], s.buf[s.pos+1:]...)
	}
}

// transpose swaps the two runes around the cursor, which is Ctrl-T and the fastest fix
// for the commonest typo there is.
func (s *session) transpose() {
	if len(s.buf) < 2 {
		return
	}
	i := s.pos
	if i >= len(s.buf) {
		i = len(s.buf) - 1
	}
	if i == 0 {
		i = 1
	}
	s.buf[i-1], s.buf[i] = s.buf[i], s.buf[i-1]
	if s.pos < len(s.buf) {
		s.pos++
	}
}

// history walks the remembered lines. The line being typed is parked on the way out and
// restored on the way back, so Up followed by Down loses nothing.
func (s *session) history(step int) {
	if len(s.e.hist) == 0 {
		return
	}
	next := s.idx + step
	if next < 0 || next > len(s.e.hist) {
		return
	}
	if s.idx == len(s.e.hist) {
		s.saved = string(s.buf)
	}
	s.idx = next
	if next == len(s.e.hist) {
		s.buf = []rune(s.saved)
	} else {
		s.buf = []rune(s.e.hist[next])
	}
	s.pos = len(s.buf)
}

// complete answers Tab. One candidate is inserted; several are listed under the line
// after the longest prefix they share has been inserted, which is the readline bargain:
// Tab types as much as it can prove and then shows you the choice.
func (s *session) complete() {
	if s.e.Complete == nil {
		return
	}
	start, cands := s.e.Complete(append([]rune(nil), s.buf...), s.pos)
	if len(cands) == 0 || start < 0 || start > s.pos {
		return
	}
	word := string(s.buf[start:s.pos])
	prefix := commonPrefix(cands)
	if len(prefix) > len(word) {
		tail := append([]rune(nil), s.buf[s.pos:]...)
		s.buf = append(append(s.buf[:start], []rune(prefix)...), tail...)
		s.pos = start + len([]rune(prefix))
	}
	if len(cands) > 1 {
		s.e.write("\r\n" + listing(cands, s.cols()))
	}
}

// maxListed bounds what Tab prints. A completer with nothing to go on can have hundreds
// of answers — every method of every kind, when the word is still empty — and a screenful
// is a menu while ten screenfuls are a scroll back to where you were.
const maxListed = 100

func listing(cands []string, cols int) string {
	if len(cands) <= maxListed {
		return columns(cands, cols)
	}
	return columns(cands[:maxListed], cols) +
		"… and " + strconv.Itoa(len(cands)-maxListed) + " more\r\n"
}

// cols is the terminal's width, with a floor that keeps the arithmetic below sane on a
// terminal that reports something absurd.
func (s *session) cols() int {
	w := s.e.term.Width()
	if w <= 0 {
		w = 80
	}
	if w < 20 {
		w = 20
	}
	return w
}

// refresh redraws the line in place: carriage return, prompt, the visible slice of the
// buffer, erase to the right, carriage return again and one CSI to put the cursor back.
// Drawing the whole line every keystroke costs a few hundred bytes and is the only way
// colouring can work at all — a character typed in the middle of a string changes the
// colour of everything to its right.
func (s *session) refresh() {
	cols := s.cols()
	avail := cols - s.pw - 1 // one column spare: a full line makes terminals wrap
	if avail < 1 {
		avail = 1
	}
	start, end := s.window(avail)
	colors := s.colors()

	var b strings.Builder
	b.WriteString("\r")
	b.WriteString(s.prompt)
	cur := ""
	for i := start; i < end; i++ {
		c := ""
		if colors != nil {
			c = colors[i]
		}
		if c != cur {
			if c == "" {
				b.WriteString("\x1b[0m")
			} else {
				b.WriteString("\x1b[" + c + "m")
			}
			cur = c
		}
		b.WriteRune(s.buf[i])
	}
	if cur != "" {
		b.WriteString("\x1b[0m")
	}
	b.WriteString("\x1b[0K")
	b.WriteString("\r")
	if col := s.pw + displayWidth(s.buf[start:s.pos]); col > 0 {
		b.WriteString("\x1b[" + strconv.Itoa(col) + "C")
	}
	s.e.write(b.String())
}

// window is the slice of the buffer that fits in avail columns with the cursor inside
// it. It walks back from the cursor first — the cursor must be visible, whatever else is
// — and then forward with whatever room is left. Walking back first is also what keeps a
// line that fits from ever being drawn scrolled: there the walk reaches the start of the
// buffer before it runs out of columns.
func (s *session) window(avail int) (start, end int) {
	used := 0
	start = s.pos
	for start > 0 {
		w := runeWidth(s.buf[start-1])
		if used+w > avail {
			break
		}
		used += w
		start--
	}
	end = s.pos
	for end < len(s.buf) {
		w := runeWidth(s.buf[end])
		if used+w > avail {
			break
		}
		used += w
		end++
	}
	return start, end
}

// colors is the per-rune palette for the current buffer, or nil for "draw it plain" —
// which is what a missing highlighter, an over-long line and a highlighter that returned
// the wrong number of entries all mean. A wrong length is a bug in the caller, and the
// editor's answer to it is to keep drawing rather than to index out of range.
func (s *session) colors() []string {
	if s.e.Highlight == nil || len(s.buf) == 0 || len(s.buf) > maxHighlight {
		return nil
	}
	c := s.e.Highlight(append([]rune(nil), s.buf...))
	if len(c) != len(s.buf) {
		return nil
	}
	return c
}

// wordStart is where the word to the left of pos begins: the separators before it are
// skipped first, so Ctrl-W on "a.map  " deletes the spaces and "map" together.
func wordStart(rs []rune, pos int) int {
	i := pos
	for i > 0 && !isWordRune(rs[i-1]) {
		i--
	}
	for i > 0 && isWordRune(rs[i-1]) {
		i--
	}
	return i
}

// wordEnd is the mirror of wordStart: past the separators to the right of pos, then past
// the word behind them.
func wordEnd(rs []rune, pos int) int {
	i := pos
	for i < len(rs) && !isWordRune(rs[i]) {
		i++
	}
	for i < len(rs) && isWordRune(rs[i]) {
		i++
	}
	return i
}

// isWordRune is the editor's idea of a word, which is the language's: §3.4 identifiers,
// the '$' of a global and the digits of a literal. A '.' is not one, so word motion stops
// at every link of a method chain — the place you actually want to be.
func isWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// columns lays the candidates out in even columns, the way ls does, and ends with a
// newline so the caller's redraw starts on a clean row.
func columns(items []string, width int) string {
	w := 0
	for _, it := range items {
		if n := displayWidth([]rune(it)); n > w {
			w = n
		}
	}
	w += 2
	per := width / w
	if per < 1 {
		per = 1
	}
	var b strings.Builder
	for i, it := range items {
		b.WriteString(it)
		if i == len(items)-1 || (i+1)%per == 0 {
			b.WriteString("\r\n")
			continue
		}
		b.WriteString(strings.Repeat(" ", w-displayWidth([]rune(it))))
	}
	return b.String()
}

// stripSGR removes the escape sequences from a prompt so its printable width can be
// measured. Only the sequences this package and its callers produce need to be
// recognised: CSI … final byte, and the two-byte escapes that carry no parameters.
func stripSGR(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != 0x1b {
			b.WriteRune(rs[i])
			continue
		}
		i++
		if i < len(rs) && rs[i] == '[' {
			for i++; i < len(rs) && !(rs[i] >= 0x40 && rs[i] <= 0x7e); i++ {
			}
		}
	}
	return b.String()
}
