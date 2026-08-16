package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"mzs/internal/highlight"
	"mzs/internal/lexer"
	"mzs/internal/lineedit"
	"mzs/internal/token"
	"mzs/mzs"
)

// repl is the interactive front door. mzs has no way to hand a live Env back to a
// host — a *Program is immutable and every Run gets a fresh frame — so the session
// keeps its state the only way that is both correct and cheap: it replays the lines
// that already worked and prints only the output the replay had not produced
// before. Evaluation is deterministic (§8.13), so the replayed prefix writes the
// same bytes every time and the delta is exactly this line's output. The
// interpreter is rebuilt for each line to keep that true under --rand, whose
// generator would otherwise carry its position into the replay.
func repl(cfg *config, stdout, stderr io.Writer, stdin io.Reader) int {
	lines, session := newLineSource(cfg, stdout, stdin)
	return replLoop(cfg, stdout, stderr, lines, session)
}

// replLoop is the session itself, over whatever the lines come from. The reader is a
// parameter so that the loop can be driven by a test — and because there are two of
// them: a line editor when both ends are a terminal, and the plain scanner that has
// always been here for a pipe.
func replLoop(cfg *config, stdout, stderr io.Writer, lines lineSource, session *replSession) int {
	var sink bytes.Buffer

	fmt.Fprintf(stdout, "%s — .help for help, .exit to quit\n", version)

	shown := 0

	// One Ctrl-C abandons the line and nothing else: the session, its variables and its
	// history are all still there at the next prompt. Two in a row leave, because a
	// session that has stopped answering is the one case where there is no line left to
	// type `.exit` on — and the first press says so, so nobody has to guess.
	interrupts := 0
	notice := func() { fmt.Fprintln(stdout, "(press Ctrl-C again to leave, or .exit)") }

	for {
		line, err := lines.read(session.prompt)
		switch {
		case errors.Is(err, errInterrupted):
			if interrupts++; interrupts >= 2 {
				fmt.Fprintln(stdout)
				return exitOK
			}
			notice()
			continue
		case err != nil:
			fmt.Fprintln(stdout)
			return exitOK
		}
		// The two presses have to be consecutive, so any line that was actually read —
		// even an empty one, even `.help` — starts the count again.
		interrupts = 0
		switch strings.TrimSpace(line) {
		case "":
			continue
		case ".exit", ".quit", ":q":
			return exitOK
		case ".help":
			fmt.Fprint(stdout, replHelp)
			continue
		case ".clear":
			session.history, shown = nil, 0
			sink.Reset()
			fmt.Fprintln(stdout, "session cleared")
			continue
		case ".src":
			fmt.Fprintln(stdout, strings.Join(session.history, "\n"))
			continue
		case ".vars":
			for _, k := range sortedVarNames(cfg.vars) {
				fmt.Fprintf(stdout, "%s = %s\n", k, cfg.vars[k].Inspect())
			}
			continue
		}

		in := mzs.New(options(cfg, &sink, stderr, nil))
		prog, pending, full, err := compileMaybeContinued(in, session, line, lines)
		if err != nil {
			if errors.Is(err, errInterrupted) {
				// The line that opened the continuation reset the count, so this press is
				// always the first of its run: it abandons the construct, and the next one
				// at the prompt is what leaves.
				interrupts++
				notice()
				continue
			}
			reportErr(stderr, "repl", full, err)
			continue
		}

		sink.Reset()
		res, runErr := in.RunResult(context.Background(), prog, cfg.vars)
		produced := sink.String()
		if len(produced) > shown {
			fmt.Fprint(stdout, produced[shown:])
		}
		if code, ok := mzs.ExitCode(runErr); ok {
			// `exit(code)` is the session's own way out, with the status it asked for —
			// the same builtin a script uses, doing the same thing (§12.1).
			return code
		}
		if runErr != nil {
			reportErr(stderr, "repl", full, runErr)
			continue
		}

		session.history = append(session.history, pending)
		shown = len(produced)
		fmt.Fprintln(stdout, res.Value.Inspect())
	}
}

// compileMaybeContinued compiles history plus the new line, and keeps asking for
// more input while the line is still open — so `fn f(a, b) {` on its own line opens
// a definition instead of printing an error.
func compileMaybeContinued(in *mzs.Interp, session *replSession, line string, lines lineSource) (prog *mzs.Program, pending, full string, err error) {
	pending = line
	for incomplete(pending) {
		next, rerr := lines.read(session.contPrompt)
		if errors.Is(rerr, errInterrupted) {
			// Ctrl-C throws the whole construct away rather than compiling the half of
			// it that was typed: an aborted line should leave no diagnostic behind.
			return nil, "", "", errInterrupted
		}
		if rerr != nil || strings.TrimSpace(next) == "" {
			// A blank line aborts the continuation; compiling what there is turns
			// the abandoned construct into the error the user needs to see.
			break
		}
		pending += "\n" + next
	}
	full = strings.Join(append(append([]string{}, session.history...), pending), "\n")
	prog, err = in.Compile("repl", full)
	return prog, pending, full, err
}

// incomplete reports whether the pending source is still open: a '(', '[' or '{'
// with no partner, or a string or regex literal the line ended in the middle of.
// mzs has no 'end' to wait for (§5.6), so the brackets are the whole story, and
// asking the lexer rather than counting bytes keeps a '}' inside a string or a
// comment from closing anything. Nothing else continues: `1 +` is a finished line
// that happens to be wrong, and saying so at once beats a prompt the user has to
// escape with a blank line.
func incomplete(src string) bool {
	toks, errs := lexer.Lex("repl", src)
	for _, e := range errs {
		if strings.Contains(e.Msg, "unterminated") {
			return true
		}
	}
	depth := 0
	for _, t := range toks {
		switch t.Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			depth--
		}
	}
	return depth > 0
}

// ---------------------------------------------------------------------------
// Where the lines come from
// ---------------------------------------------------------------------------

// errInterrupted is a Ctrl-C: this line is over, the session is not.
var errInterrupted = errors.New("interrupted")

// lineSource is one line of input at a time. Both implementations own the prompt —
// the scanner prints it, the editor draws it and redraws it on every keystroke — so
// the loop above never has to know which one it is talking to.
type lineSource interface {
	read(prompt string) (string, error)
}

// replSession is the state the two halves of the REPL share: the lines that already
// worked, and the two prompts, which carry colour only when the terminal will take it.
type replSession struct {
	history    []string
	prompt     string
	contPrompt string
}

// scannerSource is the REPL a pipe gets: no editing, no colour, one line per Scan.
type scannerSource struct {
	sc  *bufio.Scanner
	out io.Writer
}

func (s *scannerSource) read(prompt string) (string, error) {
	fmt.Fprint(s.out, prompt)
	if !s.sc.Scan() {
		if err := s.sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return s.sc.Text(), nil
}

// editorSource is the REPL a terminal gets: raw mode, the arrow keys, and the line
// coloured as it is typed. It keeps the plain scanner beside it: raw mode is only asked
// for when a line is wanted, so a console that turns out to refuse it does so with the
// session already running, and the answer to that is to read the line anyway.
type editorSource struct {
	ed       *lineedit.Editor
	fallback *scannerSource
	broken   bool
}

func (s *editorSource) read(prompt string) (string, error) {
	if !s.broken {
		line, err := s.ed.ReadLine(prompt)
		switch {
		case errors.Is(err, lineedit.ErrInterrupted):
			return "", errInterrupted
		case err == nil || errors.Is(err, io.EOF):
			return line, err
		}
		s.broken = true
	}
	return s.fallback.read(prompt)
}

// newLineSource picks between them. The editor needs three things at once — a console
// on stdin, a console on stdout and a terminal that will let us into raw mode — and it
// asks for all three before committing, because a REPL that cannot read a line is a
// worse outcome than a REPL without arrow keys. Everything it cannot get, it does
// without: no console means the scanner, no colour means a plain line.
func newLineSource(cfg *config, stdout io.Writer, stdin io.Reader) (lineSource, *replSession) {
	session := &replSession{prompt: "mzs> ", contPrompt: "...> "}

	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	plain := &scannerSource{sc: sc, out: stdout}

	in, inOK := stdin.(*os.File)
	out, outOK := stdout.(*os.File)
	if inOK && outOK {
		if term, ok := lineedit.FileTerminal(in, out); ok {
			ed := lineedit.New(term)
			ed.Complete = replCompleter(cfg, session)
			if colorOK() {
				ed.Highlight = highlight.Colors
				session.prompt = "\x1b[1;32mmzs>\x1b[0m "
				session.contPrompt = "\x1b[1;32m...>\x1b[0m "
			}
			return &editorSource{ed: ed, fallback: plain}, session
		}
	}
	return plain, session
}

// colorOK is the usual three questions: is it a terminal, does it claim to be one that
// can draw, and has the user asked for no colour at all. NO_COLOR is honoured for any
// value, which is what no-color.org asks for.
func colorOK() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	switch os.Getenv("TERM") {
	case "", "dumb":
		return false
	}
	return true
}

// replCompleter is the Tab key. It completes three things, in the order the line makes
// them possible: the dot-commands when the line *is* one, a method name after a '.', and
// otherwise a name that could stand on its own — a keyword, a builtin, a module, a $var
// from the command line, or anything the session itself has bound.
func replCompleter(cfg *config, session *replSession) lineedit.Completer {
	return func(line []rune, pos int) (int, []string) {
		start := pos
		for start > 0 && isNameRune(line[start-1]) {
			start--
		}
		word := string(line[start:pos])

		switch {
		case start > 0 && line[start-1] == '.':
			if isCommandLine(line, start) {
				return start - 1, matching(replCommands(), "."+word)
			}
			return start, matching(methodNames(), word)
		case word == "":
			return pos, nil
		case word[0] == '$':
			return start, matching(varNames(cfg, session), word)
		}
		return start, matching(globalNames(cfg, session), word)
	}
}

// isCommandLine reports whether the '.' just before start is the one that opens a REPL
// command rather than a method call: it is when nothing but blanks precedes it.
func isCommandLine(line []rune, start int) bool {
	for i := 0; i < start-1; i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return false
		}
	}
	return true
}

func isNameRune(r rune) bool {
	return r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r > 0x7f
}

func replCommands() []string {
	return []string{".clear", ".exit", ".help", ".quit", ".src", ".vars"}
}

// completionKinds is every receiver a method can be registered for, KAny — the universal
// table of §12.1 — included. It mirrors the evaluator's own dispatch list rather than
// counting up to the last Kind, so a table that is empty today still gets asked.
var completionKinds = []mzs.Kind{
	mzs.KNil, mzs.KBool, mzs.KInt, mzs.KFloat, mzs.KString, mzs.KRegex,
	mzs.KArray, mzs.KDict, mzs.KFunc, mzs.KTime, mzs.KRange, mzs.KTask, mzs.KAny,
}

// methodNames is every method of every kind, which is the best a completer can do: a
// receiver's kind is not known until the line runs (§6.3), so `"a".` and `[1].` are the
// same question here and the answer is the union.
func methodNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range completionKinds {
		for _, n := range mzs.MethodNames(k) {
			if !seen[n] {
				seen[n], out = true, append(out, n)
			}
		}
	}
	sortStrings(out)
	return out
}

// globalNames is everything that can start an expression: the keywords, the builtins,
// the modules this configuration actually installs, and the names the session has bound
// so far — the last of which is why the completer is built per session.
func globalNames(cfg *config, session *replSession) []string {
	o := options(cfg, io.Discard, io.Discard, nil)
	out := append(mzs.Keywords(), mzs.BuiltinNames()...)
	out = append(out, mzs.ModuleNames(&o)...)
	out = append(out, sessionNames(session)...)
	sortStrings(out)
	return dedup(out)
}

func varNames(cfg *config, session *replSession) []string {
	out := sortedVarNames(cfg.vars)
	for _, n := range sessionNames(session) {
		if strings.HasPrefix(n, "$") {
			out = append(out, n)
		}
	}
	sortStrings(out)
	return dedup(out)
}

// sessionNames are the identifiers the accumulated source mentions. Lexing it is both
// cheaper and more honest than tracking assignments: everything the user has typed and
// that still compiles is a name they may want to type again.
func sessionNames(session *replSession) []string {
	if len(session.history) == 0 {
		return nil
	}
	toks, _ := lexer.Lex("repl", strings.Join(session.history, "\n"))
	var out []string
	for _, t := range toks {
		switch t.Kind {
		case token.IDENT, token.GVAR:
			// A GVAR's Value keeps its '$' (§3.4), so both kinds are already spelled the
			// way the line will be typed again.
			out = append(out, t.Value)
		}
	}
	return out
}

func matching(names []string, prefix string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

func dedup(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// sortStrings is an insertion sort, like sortedVarNames below: these lists are read at
// the speed of a keystroke and the CLI has no other reason to import sort.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortedVarNames(vars map[string]mzs.Value) []string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sortStrings(names)
	return names
}

const replHelp = `  .help    this text
  .exit    leave (:q, exit(code), Ctrl-D and Ctrl-C twice work too)
  .clear   forget every line of this session
  .src     show the session's accumulated source
  .vars    show the bound $variables

  Locals and functions carry over between lines; $vars come from -v/--vars.
  A line with an unclosed '{', '(' or '[' keeps reading at the ...> prompt;
  a blank line aborts it.

  On a terminal the line is edited, not just read: ← → move, ↑ ↓ walk the
  history, Home/End and Ctrl-A/E jump, Ctrl-W and Ctrl-U erase, Tab
  completes a name, Ctrl-C throws the line away — twice in a row leaves —
  and what you type is coloured as you type it. NO_COLOR turns the colour
  off.

  mzs> s = "  ОПЕРАТОР ".lower.trim
  mzs> s ~ /оператор/i
  mzs> (0..6).map { it * 2 }.each_slice(2).array
  mzs> fn f(a, b) { a += b; return a }
  mzs> match s { in ["да", "ага"] -> 1; else -> 0 }
`
