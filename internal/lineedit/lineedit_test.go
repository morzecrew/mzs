package lineedit

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

// fakeTerm is a terminal made of a script: the keys are the bytes it hands out and the
// screen is everything the editor drew. Nothing here needs a console, which is the point
// of Terminal being an interface.
type fakeTerm struct {
	in       *bytes.Reader
	out      bytes.Buffer
	cols     int
	raws     int
	restores int
	rawErr   error
}

func newTerm(keys string, cols int) *fakeTerm {
	return &fakeTerm{in: bytes.NewReader([]byte(keys)), cols: cols}
}

func (t *fakeTerm) Read(p []byte) (int, error)  { return t.in.Read(p) }
func (t *fakeTerm) Write(p []byte) (int, error) { return t.out.Write(p) }
func (t *fakeTerm) Width() int                  { return t.cols }

func (t *fakeTerm) MakeRaw() (func() error, error) {
	if t.rawErr != nil {
		return nil, t.rawErr
	}
	t.raws++
	return func() error { t.restores++; return nil }, nil
}

// screen is what the last redraw left on the row: the last non-empty stretch between two
// carriage returns, with the escape sequences taken out. (A redraw ends by returning the
// carriage and stepping the cursor out to its column, so the final segment is empty.)
func (t *fakeTerm) screen() string {
	rows := strings.Split(strings.TrimSuffix(stripSGR(t.out.String()), "\r\n"), "\r")
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i] != "" {
			return rows[i]
		}
	}
	return ""
}

// The escape sequences the tests type. Written out because "\x1b[A" in the middle of a
// string of keys is unreadable.
const (
	up        = "\x1b[A"
	down      = "\x1b[B"
	right     = "\x1b[C"
	left      = "\x1b[D"
	ss3Up     = "\x1bOA"
	homeKey   = "\x1b[H"
	endKey    = "\x1b[F"
	delKey    = "\x1b[3~"
	ctrlRight = "\x1b[1;5C"
	ctrlLeft  = "\x1b[1;5D"
	backspace = "\x7f"
)

func read(t *testing.T, keys string, tweak func(*Editor)) (string, error, *fakeTerm) {
	t.Helper()
	term := newTerm(keys, 80)
	ed := New(term)
	if tweak != nil {
		tweak(ed)
	}
	line, err := ed.ReadLine("mzs> ")
	return line, err, term
}

func TestReadLineTypes(t *testing.T) {
	t.Parallel()

	line, err, term := read(t, "1 + 2\r", nil)
	if err != nil || line != "1 + 2" {
		t.Fatalf("ReadLine = %q, %v; want %q, nil", line, err, "1 + 2")
	}
	if term.raws != 1 || term.restores != 1 {
		t.Errorf("raw mode entered %d and left %d times; want 1 and 1", term.raws, term.restores)
	}
	if got := term.screen(); !strings.Contains(got, "mzs> 1 + 2") {
		t.Errorf("screen = %q, want the prompt and the line", got)
	}
}

// TestReadLineFallsBackWhenRawFails is the promise the REPL relies on: a terminal that
// will not go raw is an error the caller can recover from, not a lost line.
func TestReadLineFallsBackWhenRawFails(t *testing.T) {
	t.Parallel()

	term := newTerm("x\r", 80)
	term.rawErr = errors.New("nope")
	if _, err := New(term).ReadLine("mzs> "); err == nil {
		t.Fatal("ReadLine succeeded on a terminal that refused raw mode")
	}
}

func TestEditingKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys string
		want string
	}{
		{"left inserts in the middle", "ac" + left + "b\r", "abc"},
		{"right comes back", "ab" + left + left + right + "X\r", "aXb"},
		{"application cursor mode moves too", "ab" + "\x1bOD" + "X\r", "aXb"},
		{"backspace deletes behind", "abc" + backspace + "\r", "ab"},
		{"delete deletes ahead", "abc" + left + delKey + "\r", "ab"},
		{"ctrl-d deletes ahead on a non-empty line", "abc" + left + "\x04\r", "ab"},
		{"home and end", "bc" + homeKey + "a" + endKey + "d\r", "abcd"},
		{"ctrl-a and ctrl-e", "bc\x01a\x05d\r", "abcd"},
		{"ctrl-k kills to the end", "abcd" + left + left + "\x0b\r", "ab"},
		{"ctrl-u kills to the start", "abcd" + left + left + "\x15\r", "cd"},
		{"ctrl-w kills the word behind", "a.map xs\x17\r", "a.map "},
		{"ctrl-w skips the blanks first", "one two   \x17\r", "one "},
		{"alt-b and alt-f walk words", "one two" + "\x1bb" + "X" + "\x1bf" + "Y\r", "one XtwoY"},
		{"ctrl-arrows walk words too", "one two" + ctrlLeft + "X" + ctrlRight + "Y\r", "one XtwoY"},
		{"ctrl-t transposes", "ab\x14\r", "ba"},
		{"a lone escape is not text", "a\x1b\r", "a"},
		{"an unknown sequence is not text", "a\x1b[5~b\r", "ab"},
		{"utf-8 survives a backspace", "привет" + backspace + "\r", "приве"},
		{"a tab with no completer types nothing", "a\tb\r", "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, err, _ := read(t, tc.keys, nil)
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if line != tc.want {
				t.Errorf("line = %q, want %q", line, tc.want)
			}
		})
	}
}

// TestKeyDecoding covers the spellings the same key arrives in, and the sequences that
// must reach the buffer as nothing at all.
func TestKeyDecoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys string
		want string
	}{
		{"home and end as tildes", "bc\x1b[1~a\x1b[4~d\r", "abcd"},
		{"home and end as the other tildes", "bc\x1b[7~a\x1b[8~d\r", "abcd"},
		{"home and end in application mode", "bc\x1bOHa\x1bOFd\r", "abcd"},
		{"up on an empty history changes nothing", ss3Up + "a" + down + "b\r", "ab"},
		{"ctrl-p and ctrl-n are up and down", "a\x10\x0e\r", "a"},
		{"alt-d kills the word ahead", "one two" + ctrlLeft + "\x1bd\r", "one "},
		{"alt-backspace kills the word behind", "one two\x1b\x7f\r", "one "},
		{"a device report is not text", "a\x1b[?1;2cb\r", "ab"},
		{"an endless sequence stops being read", "a\x1b[" + strings.Repeat(";", 17) + "b\r", "ab"},
		{"ctrl-l redraws and keeps the line", "ab\x0c\r", "ab"},
		{"transpose at the end of the line", "abc\x14\r", "acb"},
		{"transpose needs two runes", "a\x14\r", "a"},
		{"backspace at the start does nothing", backspace + "a\r", "a"},
		{"delete at the end does nothing", "a" + delKey + "\r", "a"},
		{"right at the end does nothing", "a" + right + "b\r", "ab"},
		{"left at the start does nothing", "a" + left + left + "b\r", "ba"},
		{"a nul byte is not text", "a\x00b\r", "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, err, _ := read(t, tc.keys, nil)
			if err != nil {
				t.Fatalf("ReadLine: %v", err)
			}
			if line != tc.want {
				t.Errorf("line = %q, want %q", line, tc.want)
			}
		})
	}
}

func TestCtrlLClearsTheScreen(t *testing.T) {
	t.Parallel()

	_, _, term := read(t, "ab\x0c\r", nil)
	if !strings.Contains(term.out.String(), "\x1b[2J") {
		t.Errorf("drew %q, want the screen cleared", term.out.String())
	}
}

func TestInterruptAndEOF(t *testing.T) {
	t.Parallel()

	if _, err, term := read(t, "abc\x03", nil); !errors.Is(err, ErrInterrupted) {
		t.Errorf("ctrl-C on a typed line = %v, want ErrInterrupted", err)
	} else if !strings.Contains(term.out.String(), "^C") {
		t.Errorf("ctrl-C drew %q, want it to show ^C", term.out.String())
	}
	if _, err, _ := read(t, "\x04", nil); !errors.Is(err, io.EOF) {
		t.Errorf("ctrl-D on an empty line = %v, want io.EOF", err)
	}
	if _, err, _ := read(t, "abc", nil); !errors.Is(err, io.EOF) {
		t.Errorf("a closed terminal = %v, want io.EOF", err)
	}
}

// TestHistory walks the arrow keys through three lines, including the one being typed:
// Up parks it and Down brings it back untouched.
func TestHistory(t *testing.T) {
	t.Parallel()

	term := newTerm(strings.Join([]string{
		"one\r",
		"two\r",
		up + up + "\r",           // back past "two" to "one"
		up + down + "half\r",     // up and straight back down: the parked line survives
		up + "X\r",               // editing a recalled line does not rewrite the history
		up + up + up + up + "\r", // four steps back through five remembered lines
	}, ""), 80)
	ed := New(term)

	var got []string
	for i := 0; i < 6; i++ {
		line, err := ed.ReadLine("> ")
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		got = append(got, line)
	}
	want := []string{"one", "two", "one", "half", "halfX", "two"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
	if h := ed.History(); len(h) != 6 {
		t.Errorf("history = %q, want six lines", h)
	}
}

// TestHistorySkipsBlanksAndRepeats keeps Up useful: it should never walk onto an empty
// line or the same line twice.
func TestHistorySkipsBlanksAndRepeats(t *testing.T) {
	t.Parallel()

	term := newTerm("a\r\r   \ra\rb\r", 80)
	ed := New(term)
	for i := 0; i < 5; i++ {
		if _, err := ed.ReadLine("> "); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	if got, want := strings.Join(ed.History(), ","), "a,b"; got != want {
		t.Errorf("history = %q, want %q", got, want)
	}
}

func TestMaxHistory(t *testing.T) {
	t.Parallel()

	term := newTerm("a\rb\rc\r", 80)
	ed := New(term)
	ed.MaxHistory = 2
	for i := 0; i < 3; i++ {
		if _, err := ed.ReadLine("> "); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	if got, want := strings.Join(ed.History(), ","), "b,c"; got != want {
		t.Errorf("history = %q, want %q", got, want)
	}
}

// TestHighlightIsDrawn checks the mechanics of colouring, not the palette: the SGR runs
// are emitted where the highlighter asked for them, and the text between them is intact.
func TestHighlightIsDrawn(t *testing.T) {
	t.Parallel()

	_, err, term := read(t, "ab\r", func(e *Editor) {
		e.Highlight = func(src []rune) []string {
			out := make([]string, len(src))
			for i := range out {
				if src[i] == 'a' {
					out[i] = "31"
				}
			}
			return out
		}
	})
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	drawn := term.out.String()
	if !strings.Contains(drawn, "\x1b[31ma") {
		t.Errorf("drew %q, want 'a' painted red", drawn)
	}
	if !strings.Contains(drawn, "\x1b[0mb") {
		t.Errorf("drew %q, want the colour reset before 'b'", drawn)
	}
	if got := term.screen(); !strings.Contains(got, "mzs> ab") {
		t.Errorf("screen = %q, want the plain text under the colour", got)
	}
}

// TestHighlightOfTheWrongLengthIsIgnored: a highlighter is a caller's function, and a
// buggy one must cost a colour, not an index out of range.
func TestHighlightOfTheWrongLengthIsIgnored(t *testing.T) {
	t.Parallel()

	line, err, term := read(t, "abc\r", func(e *Editor) {
		e.Highlight = func(src []rune) []string {
			c := make([]string, len(src)+1)
			for i := range c {
				c[i] = "31"
			}
			return c
		}
	})
	if err != nil || line != "abc" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	if strings.Contains(term.out.String(), "\x1b[31m") {
		t.Error("a highlighter with the wrong length was trusted")
	}
}

func TestCompletion(t *testing.T) {
	t.Parallel()

	complete := func(line []rune, pos int) (int, []string) {
		start := pos
		for start > 0 && line[start-1] != ' ' {
			start--
		}
		word := string(line[start:pos])
		var out []string
		for _, c := range []string{"each", "each_slice", "map", "select"} {
			if strings.HasPrefix(c, word) {
				out = append(out, c)
			}
		}
		return start, out
	}

	t.Run("one candidate is typed out", func(t *testing.T) {
		t.Parallel()
		line, err, _ := read(t, "ma\t(2)\r", func(e *Editor) { e.Complete = complete })
		if err != nil || line != "map(2)" {
			t.Errorf("line = %q, %v; want %q", line, err, "map(2)")
		}
	})
	t.Run("several candidates give their common prefix and a list", func(t *testing.T) {
		t.Parallel()
		line, err, term := read(t, "ea\t\r", func(e *Editor) { e.Complete = complete })
		if err != nil || line != "each" {
			t.Errorf("line = %q, %v; want %q", line, err, "each")
		}
		if drawn := term.out.String(); !strings.Contains(drawn, "each_slice") {
			t.Errorf("drew %q, want the candidates listed", drawn)
		}
	})
	t.Run("a long list is cut short", func(t *testing.T) {
		t.Parallel()
		many := make([]string, 250)
		for i := range many {
			many[i] = "m" + strconv.Itoa(i)
		}
		_, err, term := read(t, "m\t\r", func(e *Editor) {
			e.Complete = func(line []rune, pos int) (int, []string) { return 0, many }
		})
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		drawn := term.out.String()
		if !strings.Contains(drawn, "… and 150 more") {
			t.Errorf("drew %q, want the tail of the list summarised", drawn)
		}
		if strings.Contains(drawn, "m249") {
			t.Error("the whole list was printed")
		}
	})
	t.Run("no candidate changes nothing", func(t *testing.T) {
		t.Parallel()
		line, err, _ := read(t, "zz\t\r", func(e *Editor) { e.Complete = complete })
		if err != nil || line != "zz" {
			t.Errorf("line = %q, %v; want %q", line, err, "zz")
		}
	})
}

// TestScrollsSideways: a line longer than the terminal keeps the cursor on screen, which
// is the whole reason the editor draws a window rather than the buffer.
func TestScrollsSideways(t *testing.T) {
	t.Parallel()

	term := newTerm(strings.Repeat("x", 60)+"y\r", 30)
	ed := New(term)
	line, err := ed.ReadLine("mzs> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if len(line) != 61 {
		t.Fatalf("line is %d runes, want 61", len(line))
	}
	screen := term.screen()
	if !strings.HasSuffix(screen, "y") {
		t.Errorf("screen = %q, want it to end at the cursor", screen)
	}
	if w := displayWidth([]rune(screen)); w > 30 {
		t.Errorf("screen is %d columns wide, want no more than 30: %q", w, screen)
	}
}

// TestCursorColumnIgnoresPromptColour: the prompt may carry SGR escapes, and they take no
// columns — a cursor placed as if they did would sit inside the prompt.
func TestCursorColumnIgnoresPromptColour(t *testing.T) {
	t.Parallel()

	term := newTerm("ab\r", 80)
	if _, err := New(term).ReadLine("\x1b[1;32mmzs>\x1b[0m "); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	// "mzs> " is five columns, "ab" two: the cursor ends at column seven.
	if !strings.Contains(term.out.String(), "\x1b[7C") {
		t.Errorf("drew %q, want the cursor put at column 7", term.out.String())
	}
}

func TestDisplayWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"привет", 6},
		{"日本語", 6},
		{"é", 1},         // e + combining acute
		{"\U0001f600", 2}, // emoji
		{"a‍b", 2},        // zero-width joiner
		{"❤️", 1},         // a heart and its variation selector
		{"\x01", 0},       // a control byte is never drawn
	}
	for _, tc := range tests {
		if got := displayWidth([]rune(tc.s)); got != tc.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// TestColsHasAFloor: a terminal that reports nothing, or something absurd, must not turn
// the window arithmetic into a negative width.
func TestColsHasAFloor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ reported, want int }{{0, 80}, {-1, 80}, {5, 20}, {100, 100}} {
		s := &session{e: New(newTerm("", tc.reported))}
		if got := s.cols(); got != tc.want {
			t.Errorf("a terminal reporting %d columns gives %d, want %d", tc.reported, got, tc.want)
		}
	}
}

func TestCommonPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"map"}, "map"},
		{[]string{"each", "each_slice"}, "each"},
		{[]string{"each", "map"}, ""},
	}
	for _, tc := range tests {
		if got := commonPrefix(tc.in); got != tc.want {
			t.Errorf("commonPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripSGR(t *testing.T) {
	t.Parallel()

	if got, want := stripSGR("\x1b[1;32mmzs>\x1b[0m "), "mzs> "; got != want {
		t.Errorf("stripSGR = %q, want %q", got, want)
	}
}
