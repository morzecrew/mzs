package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"mzs/internal/lineedit"
)

// scriptedLines is a lineSource written down in advance: each entry is either a line the
// user typed or the error a key produced, which is how Ctrl-C can be tested without a
// terminal to press it on.
type scriptedLines struct {
	steps   []scriptedLine
	i       int
	prompts []string
}

type scriptedLine struct {
	line string
	err  error
}

func (s *scriptedLines) read(prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if s.i >= len(s.steps) {
		return "", errInterrupted // never reached: every script ends in :q
	}
	step := s.steps[s.i]
	s.i++
	return step.line, step.err
}

func replRun(t *testing.T, argv []string, steps ...scriptedLine) (out, errOut string, prompts []string) {
	t.Helper()
	code, out, errOut, prompts := replCode(t, argv, steps...)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	return out, errOut, prompts
}

// replCode is replRun for the sessions that end on purpose with a status of their own:
// `exit(code)`, and the second Ctrl-C.
func replCode(t *testing.T, argv []string, steps ...scriptedLine) (code int, out, errOut string, prompts []string) {
	t.Helper()
	cfg, _, _, err := parseArgs(argv)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var stdout, stderr bytes.Buffer
	lines := &scriptedLines{steps: steps}
	session := &replSession{prompt: "mzs> ", contPrompt: "...> "}
	code = replLoop(cfg, &stdout, &stderr, lines, session)
	return code, stdout.String(), stderr.String(), lines.prompts
}

// TestREPLCommands drives the dot-commands, which are the session's whole control panel.
func TestREPLCommands(t *testing.T) {
	t.Parallel()

	t.Run("help prints the help", func(t *testing.T) {
		t.Parallel()
		out, _, _ := replRun(t, []string{"--repl"},
			scriptedLine{line: ".help"},
			scriptedLine{line: ":q"},
		)
		if !strings.Contains(out, ".clear   forget every line of this session") {
			t.Errorf("stdout = %q, want the help text", out)
		}
	})

	t.Run("clear forgets the bindings", func(t *testing.T) {
		t.Parallel()
		out, errOut, _ := replRun(t, []string{"--repl"},
			scriptedLine{line: "a = 1"},
			scriptedLine{line: ".clear"},
			scriptedLine{line: "a"},
			scriptedLine{line: ".exit"},
		)
		if !strings.Contains(out, "session cleared") {
			t.Errorf("stdout = %q, want the confirmation", out)
		}
		if !strings.Contains(errOut, "undefined variable 'a'") {
			t.Errorf("stderr = %q, want the binding to be gone", errOut)
		}
	})

	t.Run("vars prints the bound $variables", func(t *testing.T) {
		t.Parallel()
		out, _, _ := replRun(t, []string{"--repl", "-v", "name=Ivan", "-v", "n=3"},
			scriptedLine{line: ".vars"},
			scriptedLine{line: ".quit"},
		)
		for _, want := range []string{`$n = "3"`, `$name = "Ivan"`} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout = %q, want it to list %s", out, want)
			}
		}
		if strings.Index(out, "$n =") > strings.Index(out, "$name =") {
			t.Errorf("stdout = %q, want the names sorted", out)
		}
	})

	t.Run("a blank line is not a line", func(t *testing.T) {
		t.Parallel()
		out, errOut, prompts := replRun(t, []string{"--repl"},
			scriptedLine{line: "   "},
			scriptedLine{line: ":q"},
		)
		if errOut != "" {
			t.Errorf("stderr = %q, want nothing", errOut)
		}
		if _, body, _ := strings.Cut(out, "\n"); body != "" {
			t.Errorf("stdout after the banner = %q, want nothing", body)
		}
		if len(prompts) != 2 || prompts[1] != "mzs> " {
			t.Errorf("prompts = %q, want the main prompt again", prompts)
		}
	})
}

// TestREPLEndOfInput: the session ends on the reader's EOF — Ctrl-D on a terminal, the
// end of the file on a pipe — with a newline so the shell prompt starts on its own row.
func TestREPLEndOfInput(t *testing.T) {
	t.Parallel()

	out, errOut, _ := replRun(t, []string{"--repl"},
		scriptedLine{line: "1 + 1"},
		scriptedLine{err: io.EOF},
	)
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
	if !strings.HasSuffix(out, "2\n\n") {
		t.Errorf("stdout = %q, want the value and then a bare newline", out)
	}
}

// TestREPLPrintsOnlyTheNewOutput guards the replay trick from the other side: the session
// re-runs every line that worked, and the output of those lines must not be printed
// again. A `say` from line one stays a single "hi" however many lines follow it.
func TestREPLPrintsOnlyTheNewOutput(t *testing.T) {
	t.Parallel()

	out, _, _ := replRun(t, []string{"--repl"},
		scriptedLine{line: `println("hi")`},
		scriptedLine{line: `println("there")`},
		scriptedLine{line: "1"},
		scriptedLine{line: ":q"},
	)
	if n := strings.Count(out, "hi"); n != 1 {
		t.Errorf("stdout = %q, want \"hi\" once, got %d", out, n)
	}
	if n := strings.Count(out, "there"); n != 1 {
		t.Errorf("stdout = %q, want \"there\" once, got %d", out, n)
	}
}

// TestREPLRuntimeErrorKeepsTheSession: a line that compiles and then fails reports and is
// dropped — the next line must not inherit it through the replay.
func TestREPLRuntimeErrorKeepsTheSession(t *testing.T) {
	t.Parallel()

	out, errOut, _ := replRun(t, []string{"--repl"},
		scriptedLine{line: "a = 2"},
		scriptedLine{line: "1 / 0"},
		scriptedLine{line: "a * 21"},
		scriptedLine{line: ":q"},
	)
	if !strings.Contains(errOut, "zero-division") {
		t.Errorf("stderr = %q, want the runtime error", errOut)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("stdout = %q, want the session to carry on", out)
	}
	if strings.Count(errOut, "zero-division") != 1 {
		t.Errorf("stderr = %q, want the failed line dropped from the replay", errOut)
	}
}

// TestScannerSourceEndsAndFails: the plain reader's two exits. EOF is the end of a
// session; a line it cannot hold is an error, and either way the loop stops asking.
func TestScannerSourceEndsAndFails(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	src := &scannerSource{sc: bufio.NewScanner(strings.NewReader("")), out: &out}
	if _, err := src.read("mzs> "); !errors.Is(err, io.EOF) {
		t.Errorf("read at the end = %v, want io.EOF", err)
	}
	if out.String() != "mzs> " {
		t.Errorf("the scanner drew %q, want it to print the prompt", out.String())
	}

	sc := bufio.NewScanner(strings.NewReader(strings.Repeat("x", 100) + "\n"))
	sc.Buffer(make([]byte, 0, 8), 8)
	if _, err := (&scannerSource{sc: sc, out: &out}).read("mzs> "); err == nil || errors.Is(err, io.EOF) {
		t.Errorf("read of an over-long line = %v, want the scanner's error", err)
	}
}

func TestColorOK(t *testing.T) {
	tests := []struct {
		name     string
		noColor  string
		term     string
		setColor bool
		want     bool
	}{
		{name: "a normal terminal", term: "xterm-256color", want: true},
		{name: "NO_COLOR at any value", noColor: "", setColor: true, term: "xterm", want: false},
		{name: "NO_COLOR set to something", noColor: "1", setColor: true, term: "xterm", want: false},
		{name: "a dumb terminal", term: "dumb", want: false},
		{name: "no TERM at all", term: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, "NO_COLOR")
			if tc.setColor {
				t.Setenv("NO_COLOR", tc.noColor)
			}
			t.Setenv("TERM", tc.term)
			if got := colorOK(); got != tc.want {
				t.Errorf("colorOK() = %v, want %v", got, tc.want)
			}
		})
	}
}

// unsetEnv is t.Setenv's missing other half: it removes a variable for the duration of
// the test and puts back whatever the environment had.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	old, had := os.LookupEnv(name)
	if !had {
		return
	}
	os.Unsetenv(name)
	t.Cleanup(func() { os.Setenv(name, old) })
}

// TestREPLInterruptKeepsTheSession: Ctrl-C throws away the line and nothing else. The
// variable bound before it is still bound after it.
func TestREPLInterruptKeepsTheSession(t *testing.T) {
	t.Parallel()

	out, errOut, _ := replRun(t, []string{"--repl"},
		scriptedLine{line: "a = 41"},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: "a + 1"},
		scriptedLine{line: ":q"},
	)
	if !strings.Contains(out, "42") {
		t.Errorf("stdout = %q, want the session to survive the interrupt", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

// TestREPLTwoInterruptsLeave: the second Ctrl-C in a row ends the session. It is the one
// way out that needs no line typed, which is the whole reason it exists — and the first
// press still says so, so nobody has to guess.
func TestREPLTwoInterruptsLeave(t *testing.T) {
	t.Parallel()

	code, out, errOut, _ := replCode(t, []string{"--repl"},
		scriptedLine{err: errInterrupted},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: ":q"}, // never reached
	)
	if code != exitOK {
		t.Errorf("exit = %d, want %d", code, exitOK)
	}
	if !strings.Contains(out, "press Ctrl-C again to leave") {
		t.Errorf("stdout = %q, want the first press to name the second", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

// TestREPLInterruptsMustBeConsecutive: a line between them starts the count again, so a
// Ctrl-C now and another one ten lines later leave the session alone.
func TestREPLInterruptsMustBeConsecutive(t *testing.T) {
	t.Parallel()

	out, _, _ := replRun(t, []string{"--repl"},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: "1 + 1"},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: "6 * 7"},
		scriptedLine{line: ":q"},
	)
	if !strings.Contains(out, "42") {
		t.Errorf("stdout = %q, want the session to survive both interrupts", out)
	}
}

// TestREPLContinuationInterruptCounts: the Ctrl-C that abandons a `...>` continuation is
// the same press as any other, so it and the next one leave together.
func TestREPLContinuationInterruptCounts(t *testing.T) {
	t.Parallel()

	code, _, _, _ := replCode(t, []string{"--repl"},
		scriptedLine{line: "fn f() {"},
		scriptedLine{err: errInterrupted},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: ":q"}, // never reached
	)
	if code != exitOK {
		t.Errorf("exit = %d, want %d", code, exitOK)
	}
}

// TestREPLExitBuiltin: `exit(code)` ends the session with the status it names — the same
// builtin a script uses, doing the same thing (§12.1).
func TestREPLExitBuiltin(t *testing.T) {
	t.Parallel()

	code, out, errOut, _ := replCode(t, []string{"--repl"},
		scriptedLine{line: `println("bye")`},
		scriptedLine{line: "exit(7)"},
		scriptedLine{line: ":q"}, // never reached
	)
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
	if !strings.Contains(out, "bye") {
		t.Errorf("stdout = %q, want the output before the exit", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want no diagnostic for an exit", errOut)
	}
}

// TestREPLInterruptAbortsAContinuation: an interrupted `fn f() {` leaves no diagnostic
// behind — unlike a blank line, which deliberately compiles the fragment so the user sees
// what was unfinished.
func TestREPLInterruptAbortsAContinuation(t *testing.T) {
	t.Parallel()

	out, errOut, prompts := replRun(t, []string{"--repl"},
		scriptedLine{line: "fn f() {"},
		scriptedLine{err: errInterrupted},
		scriptedLine{line: "7"},
		scriptedLine{line: ":q"},
	)
	if !strings.Contains(out, "7") {
		t.Errorf("stdout = %q, want the next line to run", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want the abandoned construct to be silent", errOut)
	}
	if len(prompts) < 2 || prompts[1] != "...> " {
		t.Errorf("prompts = %q, want the second one to be the continuation", prompts)
	}
}

// TestREPLBlankLineStillReportsTheFragment is the other half of the rule above: aborting
// with an empty line is a request for the error.
func TestREPLBlankLineStillReportsTheFragment(t *testing.T) {
	t.Parallel()

	_, errOut, _ := replRun(t, []string{"--repl"},
		scriptedLine{line: "[1, 2,"},
		scriptedLine{line: ""},
		scriptedLine{line: ":q"},
	)
	if !strings.Contains(errOut, "syntax") {
		t.Errorf("stderr = %q, want the syntax error", errOut)
	}
}

// TestNewLineSourceFallsBackToTheScanner: no console, no editor. This is what keeps
// `cat session.txt | mzs --repl` and every test in this package working.
func TestNewLineSourceFallsBackToTheScanner(t *testing.T) {
	t.Parallel()

	cfg, _, _, err := parseArgs([]string{"--repl"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	lines, session := newLineSource(cfg, &bytes.Buffer{}, strings.NewReader("1\n"))
	if _, ok := lines.(*scannerSource); !ok {
		t.Fatalf("lines = %T, want the plain scanner", lines)
	}
	if session.prompt != "mzs> " {
		t.Errorf("prompt = %q, want it uncoloured off a terminal", session.prompt)
	}
	line, err := lines.read(session.prompt)
	if err != nil || line != "1" {
		t.Errorf("read = %q, %v; want %q, nil", line, err, "1")
	}
}

// fakeTerm is a console the editor can be pointed at from here: it answers with the keys
// it was given, and it can be told to refuse raw mode, which is the case worth testing —
// the REPL must survive a terminal that is not the one it was promised.
type fakeTerm struct {
	keys   *strings.Reader
	out    bytes.Buffer
	rawErr error
}

func (t *fakeTerm) Read(p []byte) (int, error)  { return t.keys.Read(p) }
func (t *fakeTerm) Write(p []byte) (int, error) { return t.out.Write(p) }
func (t *fakeTerm) Width() int                  { return 80 }

func (t *fakeTerm) MakeRaw() (func() error, error) {
	if t.rawErr != nil {
		return nil, t.rawErr
	}
	return func() error { return nil }, nil
}

func editorOver(keys string, rawErr error, piped string) (*editorSource, *bytes.Buffer) {
	var out bytes.Buffer
	sc := bufio.NewScanner(strings.NewReader(piped))
	return &editorSource{
		ed:       lineedit.New(&fakeTerm{keys: strings.NewReader(keys), rawErr: rawErr}),
		fallback: &scannerSource{sc: sc, out: &out},
	}, &out
}

// TestEditorFallsBackToTheScanner: a console that will not go raw costs the arrow keys,
// not the session.
func TestEditorFallsBackToTheScanner(t *testing.T) {
	t.Parallel()

	src, out := editorOver("", errors.New("no raw mode here"), "1 + 1\n2 + 2\n")
	for i, want := range []string{"1 + 1", "2 + 2"} {
		line, err := src.read("mzs> ")
		if err != nil || line != want {
			t.Fatalf("line %d = %q, %v; want %q, nil", i, line, err, want)
		}
	}
	if !src.broken {
		t.Error("the editor was kept after it had failed")
	}
	if !strings.Contains(out.String(), "mzs> ") {
		t.Errorf("the fallback drew %q, want it to print the prompt itself", out.String())
	}
}

// TestEditorPassesOnInterruptAndEOF: neither of those is the console breaking, so neither
// gives up the editor.
func TestEditorPassesOnInterruptAndEOF(t *testing.T) {
	t.Parallel()

	src, _ := editorOver("oops\x03", nil, "")
	if _, err := src.read("mzs> "); !errors.Is(err, errInterrupted) {
		t.Errorf("ctrl-C = %v, want errInterrupted", err)
	}
	if src.broken {
		t.Error("ctrl-C gave up the editor")
	}
	if _, err := src.read("mzs> "); !errors.Is(err, io.EOF) {
		t.Errorf("the end of the keys = %v, want io.EOF", err)
	}
	if src.broken {
		t.Error("the end of the input gave up the editor")
	}
}

func TestREPLCompleter(t *testing.T) {
	t.Parallel()

	cfg, _, _, err := parseArgs([]string{"--repl", "-v", "name=Ivan"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	session := &replSession{history: []string{`greeting = "hi"`, "fn double(n) { n * 2 }", "$total = 5"}}
	complete := replCompleter(cfg, session)

	tests := []struct {
		name  string
		line  string
		want  string // a candidate that must be offered
		start int    // where the replacement begins
	}{
		{"a REPL command", ".he", ".help", 0},
		{"a command after blanks", "  .cl", ".clear", 2},
		{"a method after a dot", `"x".low`, "lower", 4},
		{"a builtin", "printl", "println", 0},
		{"a keyword", "matc", "match", 0},
		{"a module", "js", "json", 0},
		{"a name the session bound", "greet", "greeting", 0},
		{"a function the session defined", "doub", "double", 0},
		{"a $var from the command line", "$na", "$name", 0},
		{"a $var the session assigned", "$to", "$total", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line := []rune(tc.line)
			start, cands := complete(line, len(line))
			if start != tc.start {
				t.Errorf("start = %d, want %d", start, tc.start)
			}
			var found bool
			for _, c := range cands {
				if c == tc.want {
					found = true
				}
				if !strings.HasPrefix(c, string(line[start:])) {
					t.Errorf("candidate %q does not extend %q", c, string(line[start:]))
				}
			}
			if !found {
				t.Errorf("completing %q gave %q, want it to offer %q", tc.line, cands, tc.want)
			}
		})
	}
}

// TestREPLCompleterOffersNothingUseless keeps Tab quiet where it has nothing to say: on
// an empty word there is no prefix to complete and the whole vocabulary is not an answer.
func TestREPLCompleterOffersNothingUseless(t *testing.T) {
	t.Parallel()

	cfg, _, _, err := parseArgs([]string{"--repl"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	complete := replCompleter(cfg, &replSession{})
	for _, line := range []string{"", "1 + ", "zzz"} {
		rs := []rune(line)
		if _, cands := complete(rs, len(rs)); len(cands) != 0 {
			t.Errorf("completing %q gave %q, want nothing", line, cands)
		}
	}
}
