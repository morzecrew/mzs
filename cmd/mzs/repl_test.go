package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
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
	cfg, _, _, err := parseArgs(argv)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var stdout, stderr bytes.Buffer
	lines := &scriptedLines{steps: steps}
	session := &replSession{prompt: "mzs> ", contPrompt: "...> "}
	if code := replLoop(cfg, &stdout, &stderr, lines, session); code != exitOK {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	return stdout.String(), stderr.String(), lines.prompts
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
	session := &replSession{history: []string{`greeting = "hi"`, "fn double(n) { n * 2 }"}}
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
		{"a builtin", "sa", "say", 0},
		{"a keyword", "matc", "match", 0},
		{"a module", "js", "json", 0},
		{"a name the session bound", "greet", "greeting", 0},
		{"a function the session defined", "doub", "double", 0},
		{"a $var from the command line", "$na", "$name", 0},
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
