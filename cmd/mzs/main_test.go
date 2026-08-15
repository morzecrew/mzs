package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exec runs the CLI exactly as main does, with stdin supplied by the caller. A
// bytes.Reader is never a terminal, so the REPL branch stays out of the way.
func exec(argv []string, stdin string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(argv, &out, &errOut, strings.NewReader(stdin))
	return code, out.String(), errOut.String()
}

func TestCLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		argv     []string
		stdin    string
		wantCode int
		wantOut  string   // exact stdout, when non-empty
		contains []string // substrings stdout must contain
		errHas   []string // substrings stderr must contain
		errLacks []string // substrings stderr must NOT contain
	}{
		{
			name:    "one-liner prints its value",
			argv:    []string{"-e", "1 + 2"},
			wantOut: "3\n",
		},
		{
			name:    "repeated -e is joined with newlines",
			argv:    []string{"-e", "a = 40", "-e", "a + 2"},
			wantOut: "42\n",
		},
		{
			name:    "unicode round-trips",
			argv:    []string{"-e", `"привет ёж".upper`},
			wantOut: "ПРИВЕТ ЁЖ\n",
		},
		{
			name:    "json prints the value as JSON instead of str",
			argv:    []string{"--json", "-e", "(0..3).map { it * 2 }"},
			wantOut: "[0,2,4,6]\n",
		},
		{
			name:    "json of a dict keeps insertion order",
			argv:    []string{"--json", "-e", `{b: 1, a: 2}`},
			wantOut: `{"b":1,"a":2}` + "\n",
		},
		{
			name:    "str is the default rendering: a dict strs as JSON (§12.7)",
			argv:    []string{"-e", `{b: 1, a: "два"}`},
			wantOut: `{"b":1,"a":"два"}` + "\n",
		},
		{
			name:    "an implied print skips a nil result",
			argv:    []string{"-e", `println("hi")`},
			wantOut: "hi\n",
		},
		{
			name:    "an explicit -p prints the nil too",
			argv:    []string{"-p", "-e", `println("hi")`},
			wantOut: "hi\n\n",
		},
		{
			name:    "a $var is bound as a string",
			argv:    []string{"-v", "__sent=  ОПЕРАТОР ", "-e", `$__sent.lower.trim == "оператор"`},
			wantOut: "true\n",
		},
		{
			name:    "vars json keeps types",
			argv:    []string{"--vars", `{"n": 2}`, "-e", "$n + 1"},
			wantOut: "3\n",
		},
		{
			name:    "var name may carry its dollar",
			argv:    []string{"-v", "$who=мир", "-e", `"привет, " + $who`},
			wantOut: "привет, мир\n",
		},
		{
			name:    "an unbound $var is nil, not an error",
			argv:    []string{"-e", "$nope == nil"},
			wantOut: "true\n",
		},
		{
			name:     "bool mode is silent and exits 0 when truthy",
			argv:     []string{"--bool", "-e", "1 == 1"},
			wantCode: 0,
			wantOut:  "",
		},
		{
			name:     "bool mode exits 1 when falsy",
			argv:     []string{"--bool", "-e", "1 == 2"},
			wantCode: 1,
			wantOut:  "",
		},
		{
			name:     "syntax error points at the offending rune",
			argv:     []string{"-e", "1 +"},
			wantCode: 1,
			errHas:   []string{"-e:1:4: syntax:", "  1 +\n", "^"},
		},
		{
			name:     "unknown flag is a usage error",
			argv:     []string{"--nope"},
			wantCode: 2,
			errHas:   []string{"unknown flag"},
		},
		{
			name:     "compat is gone: -c is just an unknown flag",
			argv:     []string{"-c", "-e", "1"},
			wantCode: 2,
			errHas:   []string{"unknown flag -c"},
		},
		{
			name:     "-e with a file is a usage error",
			argv:     []string{"-e", "1", "script.mzs"},
			wantCode: 2,
			errHas:   []string{"mutually exclusive"},
		},
		{
			name:     "-v without an equals sign is a usage error",
			argv:     []string{"-v", "novalue", "-e", "1"},
			wantCode: 2,
			errHas:   []string{"name=value"},
		},
		{
			name:     "version is 0.1.0",
			argv:     []string{"--version"},
			contains: []string{"mzs 0.1.0"},
		},
		{
			name:     "help lists the flags and no compat mode",
			argv:     []string{"--help"},
			contains: []string{"Usage:", "--json", "--time", "Exit codes"},
		},
		{
			name:     "check compiles without running",
			argv:     []string{"--check", "-e", `println("not printed")`},
			contains: []string{"ok"},
		},
		{
			name:     "check reports a compile failure",
			argv:     []string{"--check", "-e", "1 +"},
			wantCode: 1,
			errHas:   []string{"syntax"},
		},
		{
			name:     "check reports the double-backslash regex gotcha",
			argv:     []string{"--check", "-e", `$s ~ /\\bеда/`},
			contains: []string{"ok"},
			errHas:   []string{"matches a literal backslash"},
		},
		{
			name:     "check reports an unused closure parameter",
			argv:     []string{"--check", "-e", "{ (n) -> 1 }"},
			contains: []string{"ok"},
			errHas:   []string{"never used"},
		},
		{
			// `_` is the conventional name for a parameter you do not use (§3.4, §4.1),
			// so the spelling the spec recommends must not be the noisy one.
			name:     "check stays quiet about an unused '_' parameter",
			argv:     []string{"--check", "-e", "{ (_, v) -> v }"},
			contains: []string{"ok"},
			errLacks: []string{"never used"},
		},
		{
			name:     "check reports an assignment used as a condition",
			argv:     []string{"--check", "-e", "if x = 1 { 2 }"},
			contains: []string{"ok"},
			errHas:   []string{"'=' assigns"},
		},
		{
			name:     "tokens dump",
			argv:     []string{"--tokens", "-e", "a ~ /x/i"},
			contains: []string{"IDENT(a)", "REGEX(x)"},
		},
		{
			name:     "ast dump",
			argv:     []string{"--ast", "-e", "$a + 2"},
			contains: []string{"Program", "Binary +", "Global $a"},
		},
		{
			name:    "stdin is run when it is not a terminal",
			argv:    nil,
			stdin:   `println("from stdin")`,
			wantOut: "from stdin\n",
		},
		{
			name:    "explicit dash reads stdin",
			argv:    []string{"-"},
			stdin:   `println(1 + 1)`,
			wantOut: "2\n",
		},
		{
			name:     "a script does not print its value unless asked",
			argv:     nil,
			stdin:    `1 + 2`,
			wantCode: 0,
			wantOut:  "",
		},
		{
			name:    "-p prints a script's value",
			argv:    []string{"-p"},
			stdin:   `1 + 2`,
			wantOut: "3\n",
		},
		{
			name:     "--no-print silences a one-liner",
			argv:     []string{"--no-print", "-e", "1 + 2"},
			wantCode: 0,
			wantOut:  "",
		},
		{
			// §12.1: `exit` is the program naming its own status. It is not a failure, so
			// nothing is printed for it, and nothing after it runs.
			name:     "exit sets the status and prints nothing",
			argv:     []string{"-e", `println("done"); exit(2); println("never")`},
			wantCode: 2,
			wantOut:  "done\n",
			errLacks: []string{"exit", "error"},
		},
		{
			name:     "exit with no argument is zero",
			argv:     []string{"--no-print", "-e", "exit()"},
			wantCode: 0,
			wantOut:  "",
		},
		{
			name:     "one line asking to exit ends the whole -n run",
			argv:     []string{"-n", "--no-print", "-e", `println($_); exit(4) if $_ == "b"`},
			stdin:    "a\nb\nc\n",
			wantCode: 4,
			wantOut:  "a\nb\n",
		},
		{
			name:     "an exit code is a status, and 256 is not one",
			argv:     []string{"-e", "exit(256)"},
			wantCode: 1,
			errHas:   []string{"argument", "between 0 and 255"},
		},
		{
			name:     "runaway loop hits the step budget and exits 3",
			argv:     []string{"--steps", "10000", "-e", "while true { }"},
			wantCode: 3,
			errHas:   []string{"limit", "step budget"},
		},
		{
			name:     "runaway loop hits the timeout and exits 3",
			argv:     []string{"-t", "0.05", "--steps", "0", "-e", "x = 0; while true { x += 1 }"},
			wantCode: 3,
			errHas:   []string{"limit", "timed out"},
		},
		{
			name:    "stats go to stderr, not stdout",
			argv:    []string{"--stats", "-e", "(1..10).sum"},
			wantOut: "55\n",
			errHas:  []string{"steps"},
		},
		{
			name:    "seeded rand is reproducible",
			argv:    []string{"--rand=7", "-e", "rand(100) == rand(100) || true"},
			wantOut: "true\n",
		},
		{
			name:     "rand stays off without the flag",
			argv:     []string{"-e", "rand(100)"},
			wantCode: 1,
			errHas:   []string{"undefined function 'rand'"},
		},
		{
			name:    "--time enables the lowercase date module",
			argv:    []string{"--time", "-e", "include date\ndate.today.year > 2000"},
			wantOut: "true\n",
		},
		{
			name:    "--time enables the lowercase time module",
			argv:    []string{"--time", "-e", "include time\n" + `time.parse("2024-03-01").strftime("%d.%m.%Y")`},
			wantOut: "01.03.2024\n",
		},
		{
			name:     "the time module stays off without the flag",
			argv:     []string{"-e", "include date"},
			wantCode: 1,
			errHas:   []string{"date"},
		},
		{
			name:    "-n runs the program once per line, with the line in $_",
			argv:    []string{"-n", "-e", `$_.split(" ")[0]`},
			stdin:   "GET /a 200\nPOST /b 404\n",
			wantOut: "GET\nPOST\n",
		},
		{
			name:    "-n skips a nil the way -e always has",
			argv:    []string{"-n", "-e", `if $_.starts_with("a") { $_.upper }`},
			stdin:   "abc\nzzz\nade\n",
			wantOut: "ABC\nADE\n",
		},
		{
			name:    "-p under -n prints the nils too, as the blank line nil strs to",
			argv:    []string{"-n", "-p", "-e", `if $_ == "a" { 1 }`},
			stdin:   "a\nb\n",
			wantOut: "1\n\n",
		},
		{
			name:    "$vars carry from line to line, locals do not",
			argv:    []string{"-n", "-e", `$n = ($n ?? 0) + 1`},
			stdin:   "a\nb\nc\n",
			wantOut: "1\n2\n3\n",
		},
		{
			name:    "a line with no terminator is still a line",
			argv:    []string{"-n", "-e", "$_.upper"},
			stdin:   "a\nb",
			wantOut: "A\nB\n",
		},
		{
			name:    "CRLF reads like LF",
			argv:    []string{"-n", "-e", "$_.len"},
			stdin:   "abc\r\nde\r\n",
			wantOut: "3\n2\n",
		},
		{
			name:     "empty input runs the program zero times",
			argv:     []string{"-n", "-e", `println("never")`},
			stdin:    "",
			wantCode: 0,
			wantOut:  "",
		},
		{
			name:     "--bool under -n is grep's question: any line",
			argv:     []string{"-n", "--bool", "-e", "$_ ~ /ERROR/"},
			stdin:    "ok\nERROR here\nok\n",
			wantCode: 0,
			wantOut:  "",
		},
		{
			name:     "--bool under -n exits 1 when no line answered yes",
			argv:     []string{"-n", "--bool", "-e", "$_ ~ /ERROR/"},
			stdin:    "ok\nfine\n",
			wantCode: 1,
			wantOut:  "",
		},
		{
			name:     "every line runs even after --bool has its answer",
			argv:     []string{"-n", "--bool", "-e", `println("seen ${$_}"); true`},
			stdin:    "a\nb\n",
			wantCode: 0,
			wantOut:  "seen a\nseen b\n",
		},
		{
			name:     "a raise names the input line as well as the program position",
			argv:     []string{"-n", "-e", `if $_ == "2" { raise("bad row") }; $_`},
			stdin:    "1\n2\n3\n",
			wantCode: 1,
			wantOut:  "1\n",
			errHas:   []string{"input line 2", "raise: bad row"},
		},
		{
			name:     "the timeout is per line, and a line that runs away still exits 3",
			argv:     []string{"-n", "-t", "0.05", "--steps", "0", "-e", "while true { }"},
			stdin:    "a\n",
			wantCode: 3,
			errHas:   []string{"input line 1", "timed out"},
		},
		{
			name:     "-n with the program on stdin has nothing left to read",
			argv:     []string{"-n"},
			stdin:    `println("hi")`,
			wantCode: 2,
			errHas:   []string{"-n has nothing to read", "--in"},
		},
		{
			name:     "-n and --repl want the same reader",
			argv:     []string{"-n", "--repl", "-e", "1"},
			wantCode: 2,
			errHas:   []string{"pick one"},
		},
		{
			name:     "--no-io takes the filesystem back",
			argv:     []string{"--no-io", "-e", "include io"},
			wantCode: 1,
			errHas:   []string{"module 'io' needs a filesystem"},
		},
		{
			name:    "--io is the CLI's default, spelled out",
			argv:    []string{"--io", "-e", `include io; io.lines.len`},
			stdin:   "a\nb\n",
			wantOut: "2\n",
		},
		{
			name:    "under -n the CLI owns stdin, so the module sees none of it",
			argv:    []string{"-n", "-e", `include io; "${$_}:${io.stdin.len}"`},
			stdin:   "a\nb\n",
			wantOut: "a:0\nb:0\n",
		},
		{
			name:     "--no-io does not take -n away: the CLI reads the lines, not the script",
			argv:     []string{"--no-io", "-n", "-e", "$_.upper"},
			stdin:    "ab\n",
			wantCode: 0,
			wantOut:  "AB\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec(tt.argv, tt.stdin)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, tt.wantCode, errOut)
			}
			if tt.wantOut != "" || (tt.wantOut == "" && tt.contains == nil && tt.errHas == nil) {
				if out != tt.wantOut {
					t.Errorf("stdout = %q, want %q", out, tt.wantOut)
				}
			}
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("stdout = %q, want it to contain %q", out, want)
				}
			}
			for _, want := range tt.errHas {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}
			for _, bad := range tt.errLacks {
				if strings.Contains(errOut, bad) {
					t.Errorf("stderr = %q, want it not to contain %q", errOut, bad)
				}
			}
		})
	}
}

// TestReferenceOneLiners is the §15 table, verbatim. Every row is a command a
// reader is invited to paste into a shell, so each one has to produce exactly the
// bytes promised — an extra blank line is a broken promise.
func TestReferenceOneLiners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: `println("hi")`,
			argv: []string{"-e", `println("hi")`},
			want: "hi\n",
		},
		{
			name: "lower trim against a literal",
			argv: []string{"-v", "__sent=  ОПЕРАТОР ", "-e", `$__sent.lower.trim == "оператор"`},
			want: "true\n",
		},
		{
			name: "match operator against a case-insensitive regex",
			argv: []string{"-e", `s = $__sent.lower; s ~ /привет|hello/i`, "--vars", `{"__sent":"Привет!"}`},
			want: "true\n",
		},
		{
			name: "map with it, each_slice, array, --json",
			argv: []string{"-e", "(0..6).map { it * 2 }.each_slice(2).array", "--json"},
			want: "[[0,2],[4,6],[8,10],[12]]\n",
		},
		{
			name: "fn declaration and call in one line",
			argv: []string{"-e", "fn f(a, b) { a += b; return a }; f(1, 2)"},
			want: "3\n",
		},
		{
			name: "match with an in-array pattern",
			argv: []string{"-e", `match $__sent.lower.trim { in ["да","ага"] -> 1; else -> 0 }`, "-v", "__sent=Ага"},
			want: "1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec(tt.argv, "")
			if code != exitOK {
				t.Fatalf("exit = %d, stderr: %s", code, errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q, want %q", out, tt.want)
			}
		})
	}
}

// TestOneLinerDiagnostics walks the §5.6 table through the CLI. The contract is
// stronger than "an error happens": the message is fixed, the caret sits under the
// offending rune, and there is exactly one diagnostic — someone pasting Ruby gets a
// fix-it, not a cascade.
func TestOneLinerDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string // the message after "<file>:<line>:<col>: syntax: "
		col  int    // 1-based caret column
	}{
		{"unary minus and power", "-2 ** 2", "ambiguous: write -(2 ** 2) or (-2) ** 2", 1},
		{"range needs parens before a call", "0..5.map { it }", "ambiguous range: write (0..5).map", 2},
		{"chained range", "1..2..3", "range operator is non-associative", 5},
		{"equality against a regex", "s == /re/", "'==' with a regex operand: use '~' to match", 3},
		{"ruby match operator", "s =~ /re/", "'=~' is not an mzs operator; use '~'", 3},
		{"question mark in a method name", "x.empty?", "'?' is not part of an identifier; did you mean 'empty'?", 8},
		{"renamed method", "x.downcase", "undefined method 'downcase'; did you mean 'lower'?", 3},
		{"and", "a and b", "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", 3},
		{"or", "a or b", "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", 3},
		{"not", "not a", "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", 1},
		{"do and end", "if c do 1 end", "'do'/'end' are not mzs keywords; use braces: if c { … }", 6},
		{"elsif", "elsif c { 1 }", "'elsif' is not an mzs keyword; use 'else if'", 1},
		{"unless", "unless c { 1 }", "'unless' is not an mzs keyword; use 'if !(c)'", 1},
		{"until", "until c { 1 }", "'until' is not an mzs keyword; use 'while !(c)'", 1},
		{"loop", "loop { 1 }", "'loop' is not an mzs keyword; use 'while true { … }'", 1},
		{"def", "def f() { }", "'def' is not an mzs keyword; use 'fn'", 1},
		{"percent-w array", "%w[a b]", `'%w' is not mzs; write ["a", "b"]`, 1},
		{"symbol", ":name", `mzs has no symbols; write "name"`, 1},
		{"brace dict after a call", "f {a: 1}", "a dict after a call is written f({a: 1})", 3},
		{"brace dict in a body", "if c {a: 1}", "this '{' opens the if body; write { {a: 1} } for a dict", 6},
		{"hash rocket", "k => v", "'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure", 3},
		{"pipe closure params", "{ |x| x }", "closure parameters are parenthesised: { (x) -> … }", 3},
		{"ruby safe navigation", "x &. y", "'&.' is not an mzs operator; use '?.'", 3},
		{"rescue", "a rescue b", "'rescue' is not an mzs keyword; use 'try a else b'", 3},
		{"hash interpolation", `"#{x}"`, `string interpolation is "${x}"`, 2},
		{"three-dot range", "1...5", "'...' is not an mzs operator; use '..<'", 2},
		{"scope operator", "a::B", "'::' is not an mzs operator; use '.'", 2},
		{"bang after equals", `str =! "x"`, "unexpected '!' after '='; did you mean '!='?", 6},
		{"to_s", "x.to_s", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
		{"to_i", "x.to_i", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
		{"to_f", "x.to_f", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
		{"to_a", "x.to_a", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
		{"to_h", "x.to_h", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
		{"to_json", "x.to_json", "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec([]string{"-e", tt.src}, "")
			if code != exitError {
				t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", code, exitError, out, errOut)
			}
			lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("want one diagnostic (message, source, caret), got %d lines:\n%s", len(lines), errOut)
			}
			head := "-e:1:" + itoa(tt.col) + ": syntax: " + tt.want
			if lines[0] != head {
				t.Errorf("message = %q, want %q", lines[0], head)
			}
			if lines[1] != "  "+tt.src {
				t.Errorf("source line = %q, want %q", lines[1], "  "+tt.src)
			}
			if want := "  " + pad(tt.src, tt.col) + "^"; lines[2] != want {
				t.Errorf("caret line = %q, want %q", lines[2], want)
			}
		})
	}
}

// TestOneDiagnosticPerLine pins the collapse: a second complaint about a line the
// CLI has already explained is cascade, and cascade is what §5.6 exists to prevent.
// Errors on other lines still all show up.
func TestOneDiagnosticPerLine(t *testing.T) {
	t.Parallel()

	_, _, errOut := exec([]string{"-e", "a and b\nc or d"}, "")
	if got := strings.Count(errOut, "syntax:"); got != 2 {
		t.Errorf("two broken lines produced %d diagnostics, want 2:\n%s", got, errOut)
	}
	if !strings.Contains(errOut, "-e:1:3:") || !strings.Contains(errOut, "-e:2:3:") {
		t.Errorf("stderr = %q, want a diagnostic for each line", errOut)
	}
}

// TestExamples runs the shipped examples/ files the way the README tells a reader
// to run them. They are the only end-to-end programs in the repository, so a
// regression that the unit tables miss surfaces here.
func TestExamples(t *testing.T) {
	t.Parallel()

	path := func(name string) string { return filepath.Join("..", "..", "examples", name) }

	tests := []struct {
		name     string
		argv     []string
		wantCode int
		contains []string
		errHas   []string
	}{
		{
			name:     "01 values",
			argv:     []string{path("01_values_and_operators.mzs")},
			contains: []string{"9223372036854776000.0", `"привет".index(/при/)`},
		},
		{
			name:     "03 match",
			argv:     []string{path("03_match_dispatch.mzs")},
			contains: []string{"grades: A B C F", "no arm, no else: nil"},
		},
		{
			name:     "09 host variables, routed to the operator",
			argv:     []string{"-v", "__sent=  OPERATOR ", "-v", "price=1500", path("09_host_variables.mzs")},
			contains: []string{`"intent":"handoff_operator"`, `$normalized  "operator"`},
		},
		{
			name:     "09 host variables, the program value is the branch",
			argv:     []string{"-p", "-v", "__sent=yes", path("09_host_variables.mzs")},
			contains: []string{"true\n"},
		},
		{
			name:     "12 csv round trip",
			argv:     []string{path("12_csv_report.mzs")},
			contains: []string{`"Petrov, Ivan"`, "round trip preserves every field: true"},
		},
		{
			name:     "22 game of life",
			argv:     []string{path("22_game_of_life.mzs")},
			contains: []string{"blinker has period 2:                             true"},
		},
		{
			name:     "27 modules across three files",
			argv:     []string{path("27_modules_main.mzs")},
			contains: []string{"INVOICE A-1043", "undefined member 'rate' in module 'money'"},
		},
		{
			name:     "28 async tasks",
			argv:     []string{path("28_async_tasks.mzs")},
			contains: []string{"counter = 160", "cannot await itself"},
		},
		{
			name:     "29 scheduling needs --time",
			argv:     []string{"--time", path("29_time_scheduling.mzs")},
			contains: []string{"Thursday, 05 March 2026", "utilisation 45%"},
		},
		{
			name:     "29 scheduling without --time says which option is missing",
			argv:     []string{path("29_time_scheduling.mzs")},
			wantCode: exitError,
			errHas:   []string{"module 'time' needs a clock", "mzs --time"},
		},
		{
			name:     "30 http compiles clean with no flag at all",
			argv:     []string{"--check", path("30_http_service.mzs")},
			contains: []string{"ok"},
		},
		{
			// Compile only: running it would reach the network, which a test must not.
			name:     "31 api pipeline compiles clean",
			argv:     []string{"--check", path("31_api_pipeline.mzs")},
			contains: []string{"ok"},
		},
		{
			name:     "31 api pipeline rejects an unknown tag",
			argv:     []string{"-v", "tag=nope", path("31_api_pipeline.mzs")},
			wantCode: exitError,
			errHas:   []string{"tag must be one of", "front_page"},
		},
		{
			name:     "32 io: files, env and the catchable miss",
			argv:     []string{path("32_io_files.mzs")},
			contains: []string{"write       42 bytes", "parsed      3 rows, 210 minutes in total", "io.stdin  io.lines"},
		},
		{
			name:     "--net is gone and says so",
			argv:     []string{"--net", "--check", path("30_http_service.mzs")},
			wantCode: exitUsage,
			errHas:   []string{"--net is gone", "always available"},
		},
		{
			name:     "every example compiles clean",
			argv:     []string{"--check", path("01_values_and_operators.mzs")},
			contains: []string{"ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec(tt.argv, "")
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr: %s)", code, tt.wantCode, errOut)
			}
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("stdout = %q, want it to contain %q", out, want)
				}
			}
			for _, want := range tt.errHas {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}
		})
	}
}

// TestCLIScriptFileAndArgv covers the file path and $ARGV, which only a real file
// exercises.
func TestCLIScriptFileAndArgv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "argv.mzs")
	if err := os.WriteFile(path, []byte(`println($ARGV.join(","))`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, out, errOut := exec([]string{path, "один", "два"}, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if out != "один,два\n" {
		t.Errorf("stdout = %q, want %q", out, "один,два\n")
	}
}

// TestCLIVarsFile covers --vars-file, the only flag that needs a file on disk.
func TestCLIVarsFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "vars.json")
	if err := os.WriteFile(path, []byte(`{"__sent":"Привет!","price":1500}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, out, errOut := exec([]string{"--vars-file", path, "-e", `[$__sent.lower ~ /привет/, $price + 100]`, "--json"}, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if out != "[true,1600]\n" {
		t.Errorf("stdout = %q, want %q", out, "[true,1600]\n")
	}
}

// TestCLIIOModule is §12.13 from the shell: the CLI is a host that hands over the real
// filesystem and the real environment, so the round trip a one-liner exists for — read,
// transform, write, list — works with no flag at all.
func TestCLIIOModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("привет\nмир\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(dir, "out.txt")

	tests := []struct {
		name string
		expr string
		want string
	}{
		{"read", fmt.Sprintf(`include io; io.read(%q).lines.len`, src), "2\n"},
		{"exists", fmt.Sprintf(`include io; io.exists(%q)`, src), "true\n"},
		{"a missing file does not exist", fmt.Sprintf(`include io; io.exists(%q)`, out), "false\n"},
		{"ls is sorted", fmt.Sprintf(`include io; io.ls(%q).join(",")`, dir), "in.txt\n"},
		{"env", `include io; io.env("MZS_NO_SUCH_VAR", "default")`, "default\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := exec([]string{"-e", tt.expr}, "")
			if code != 0 {
				t.Fatalf("exit = %d, stderr: %s", code, stderr)
			}
			if stdout != tt.want {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
		})
	}

	// Exactly one of two things can be piped in: the program or its data. With -e the
	// program came from the flag, so stdin is data — and without it stdin is still the
	// program, which is the behaviour every existing `cat s.mzs | mzs` depends on.
	t.Run("stdin is data when the program came from -e", func(t *testing.T) {
		code, stdout, stderr := exec([]string{"-e", `include io; io.lines.len`}, "a\nb\nc\n")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, stderr)
		}
		if stdout != "3\n" {
			t.Errorf("stdout = %q, want %q", stdout, "3\n")
		}
	})
	t.Run("stdin is still the program when nothing else supplied one", func(t *testing.T) {
		code, stdout, stderr := exec(nil, `include io; println("stdin has ${io.lines.len} lines of data")`)
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, stderr)
		}
		if stdout != "stdin has 0 lines of data\n" {
			t.Errorf("stdout = %q; want the piped script to run with no data behind it", stdout)
		}
	})

	// write and append are one sequence, not three independent rows.
	code, _, stderr := exec([]string{"--no-print", "-e", fmt.Sprintf(
		`include io; io.write(%q, io.read(%q).upper); io.append(%q, "!")`, out, src, out)}, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := string(b); got != "ПРИВЕТ\nМИР\n!" {
		t.Errorf("written file = %q; want the upper-cased source plus the appended byte", got)
	}
}

// TestCLIDataFromAFile is --in and the line modes over a real file: the two flags that
// only mean something when the program and its data come from different places.
func TestCLIDataFromAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := filepath.Join(dir, "rows.txt")
	if err := os.WriteFile(data, []byte("сеанс,60\nстрижка,30\nборода,15\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	prog := filepath.Join(dir, "first.mzs")
	if err := os.WriteFile(prog, []byte(`$_.split(",")[0]`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("--in feeds the io module", func(t *testing.T) {
		code, out, errOut := exec([]string{"--in", data, "-e", `include io; io.lines.len`}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "3\n" {
			t.Errorf("stdout = %q, want %q", out, "3\n")
		}
	})

	t.Run("--in feeds -n", func(t *testing.T) {
		code, out, errOut := exec([]string{"-n", "--in", data, "-e", `$_.split(",")[1].int`}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "60\n30\n15\n" {
			t.Errorf("stdout = %q, want %q", out, "60\n30\n15\n")
		}
	})

	// The whole point of --in: the program may still arrive on the pipe, because the
	// data no longer needs that reader.
	t.Run("--in frees stdin to be the program again", func(t *testing.T) {
		code, out, errOut := exec([]string{"--in", data, "-n"}, `$_.split(",")[0].upper`)
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q; a piped script prints only what it says to print", out)
		}
		code, out, errOut = exec([]string{"--in", data, "-l"}, `$_.split(",")[0].upper`)
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "СЕАНС\nСТРИЖКА\nБОРОДА\n" {
			t.Errorf("stdout = %q, want the three upper-cased names", out)
		}
	})

	// -n leaves a script file as silent as it is without it; -l is the flag that asks a
	// file for the value of every line.
	t.Run("-l prints where -n stays quiet", func(t *testing.T) {
		code, out, errOut := exec([]string{"-n", "--in", data, prog}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q, want a script file to print nothing on its own", out)
		}
		code, out, errOut = exec([]string{"-l", "--in", data, prog}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "сеанс\nстрижка\nборода\n" {
			t.Errorf("stdout = %q, want one name per line", out)
		}
	})

	t.Run("--no-print outranks -l", func(t *testing.T) {
		code, out, errOut := exec([]string{"-l", "--no-print", "--in", data, prog}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		if out != "" {
			t.Errorf("stdout = %q, want silence", out)
		}
	})

	t.Run("--json under -n prints one document per line", func(t *testing.T) {
		code, out, errOut := exec([]string{"-n", "--json", "--in", data, "-e", `$_.split(",")`}, "")
		if code != 0 {
			t.Fatalf("exit = %d, stderr: %s", code, errOut)
		}
		want := `["сеанс","60"]` + "\n" + `["стрижка","30"]` + "\n" + `["борода","15"]` + "\n"
		if out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	})

	t.Run("a missing --in file is a usage error", func(t *testing.T) {
		code, _, errOut := exec([]string{"--in", filepath.Join(dir, "nope.txt"), "-e", "1"}, "")
		if code != exitUsage {
			t.Errorf("exit = %d, want %d", code, exitUsage)
		}
		if !strings.Contains(errOut, "nope.txt") {
			t.Errorf("stderr = %q, want it to name the missing file", errOut)
		}
	})
}

// TestCLIMissingFile keeps a typo in a path a usage error rather than a stack trace.
func TestCLIMissingFile(t *testing.T) {
	t.Parallel()

	code, _, errOut := exec([]string{filepath.Join(t.TempDir(), "nope.mzs")}, "")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "nope.mzs") {
		t.Errorf("stderr = %q, want it to name the missing file", errOut)
	}
}

// TestREPL drives a scripted session: state carries between lines, an unclosed
// brace opens the continuation prompt, and the banner says which language this is.
func TestREPL(t *testing.T) {
	t.Parallel()

	session := strings.Join([]string{
		`s = "  ПРИВЕТ "`,
		`s.lower.trim`,
		`fn double(n) {`,
		`  n * 2`,
		`}`,
		`double(21)`,
		`(0..6).map { it * 2 }.each_slice(2).array`,
		`.src`,
		`:q`,
	}, "\n") + "\n"

	code, out, errOut := exec([]string{"--repl"}, session)
	if code != exitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{
		"mzs 0.1.0 — .help for help, .exit to quit\n",
		`"привет"`,
		"#<fn double>",
		"42",
		"[[0,2],[4,6],[8,10],[12]]",
		"...> ",
		"fn double(n) {\n  n * 2\n}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("session output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "compat") {
		t.Errorf("session output mentions compat: %q", out)
	}
}

// TestREPLReplayIsStableUnderRand guards the replay trick: the session re-runs its
// whole history for every line, so a generator that kept its position would make
// line N print the value line N-1 already showed.
func TestREPLReplayIsStableUnderRand(t *testing.T) {
	t.Parallel()

	code, out, errOut := exec([]string{"--repl", "--rand=7"}, "rand(100)\nrand(100)\nrand(100)\n:q\n")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	_, body, _ := strings.Cut(out, "\n") // drop the banner
	var got []string
	for _, line := range strings.Split(body, "mzs> ") {
		if v := strings.TrimSpace(line); v != "" {
			got = append(got, v)
		}
	}
	if len(got) != 3 {
		t.Fatalf("session produced %v, want three values (out: %q)", got, out)
	}
	if got[0] == got[1] || got[1] == got[2] {
		t.Errorf("session produced %v, want three draws from one sequence", got)
	}
}

// TestREPLHelpUsesNewSyntax keeps the help text from teaching 1.0 shapes.
func TestREPLHelpUsesNewSyntax(t *testing.T) {
	t.Parallel()

	for _, want := range []string{".exit", "{ it * 2 }", "fn f(a, b)", "match s {"} {
		if !strings.Contains(replHelp, want) {
			t.Errorf("replHelp is missing %q", want)
		}
	}
	for _, bad := range []string{"do", "end", "|n|", "downcase", "=~"} {
		if strings.Contains(replHelp, bad) {
			t.Errorf("replHelp still mentions %q", bad)
		}
	}
}

// TestIncomplete pins the continuation rule: mzs has no 'end', so an open bracket
// or an open literal is the only reason to keep reading.
func TestIncomplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"a complete expression", "1 + 2", false},
		{"a wrong but complete expression", "1 +", false},
		{"an open fn body", "fn f(a, b) {", true},
		{"a closed fn body", "fn f(a, b) { a + b }", false},
		{"an open call", "println(", true},
		{"an open array", "xs = [1, 2,", true},
		{"an open closure", "xs.map { (x) ->", true},
		{"a brace inside a string does not open anything", `s = "{"`, false},
		{"a brace inside a comment does not open anything", "1 + 2 # {", false},
		{"an unterminated string keeps reading", `s = "abc`, true},
		{"a stray closer does not keep reading", "}", false},
		{"an interpolation is not an open brace", `"${x}"`, false},
		{"a multi-line match", "match s {\n  in [\"да\"] -> 1", true},
		{"a finished multi-line match", "match s {\n  in [\"да\"] -> 1\n  else -> 0\n}", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := incomplete(tt.src); got != tt.want {
				t.Errorf("incomplete(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

// TestSourceLine pins the caret machinery against every line ending of §3.1.
func TestSourceLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		line int
		want string
		ok   bool
	}{
		{"first line", "a\nb\nc", 1, "a", true},
		{"middle line", "a\nb\nc", 2, "b", true},
		{"last line without a terminator", "a\nb\nc", 3, "c", true},
		{"crlf", "a\r\nb", 2, "b", true},
		{"cr only", "a\rb", 2, "b", true},
		{"past the end", "a", 9, "", false},
		{"line zero", "a", 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := sourceLine(tt.src, tt.line)
			if got != tt.want || ok != tt.ok {
				t.Errorf("sourceLine(%q, %d) = %q, %v; want %q, %v", tt.src, tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestPadCountsRunes is the Cyrillic case: a byte-counted caret would land in the
// middle of a two-byte rune and point at the wrong column.
func TestPadCountsRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		col  int
		want int // expected pad width in bytes, all spaces
	}{
		{"привет", 4, 3},
		{"abc", 1, 0},
		{"abc", 3, 2},
		{"\tx", 2, 1},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()
			if got := len(pad(tt.text, tt.col)); got != tt.want {
				t.Errorf("len(pad(%q, %d)) = %d, want %d", tt.text, tt.col, got, tt.want)
			}
		})
	}
}

// itoa keeps the diagnostic table readable without dragging strconv in for one call.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// TestCLIArgumentErrors is the other half of the flag table (§15): every flag that takes
// a value has three ways to be wrong — no value at all, a value of the wrong shape, and
// a file that is not there — and each must be exit code 2 with a message that names the
// flag, never a panic or a silent default.
func TestCLIArgumentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		argv   []string
		errHas string
	}{
		{"-e with no value", []string{"-e"}, "needs a value"},
		{"--in with no value", []string{"--in"}, "needs a value"},
		{"-v with no value", []string{"-v"}, "needs a value"},
		{"-v without an =", []string{"-v", "price", "-e", "1"}, "name=value"},
		{"--vars with no value", []string{"--vars"}, "needs a value"},
		{"--vars with broken json", []string{"--vars", "{oops", "-e", "1"}, "vars"},
		{"--vars with a non-object", []string{"--vars", "[1,2]", "-e", "1"}, "vars"},
		{"--vars-file that is not there", []string{"--vars-file", "нет-такого.json", "-e", "1"}, "нет-такого.json"},
		{"-t with no value", []string{"-t"}, "needs a value"},
		{"-t with a bad duration", []string{"-t", "вчера", "-e", "1"}, "вчера"},
		{"--steps with a non-number", []string{"--steps", "много", "-e", "1"}, "--steps"},
		{"--tasks with a non-number", []string{"--tasks", "много", "-e", "1"}, "--tasks"},
		{"--tasks with a negative count", []string{"--tasks", "-1", "-e", "1"}, "--tasks"},
		{"--rand with a bad seed", []string{"--rand=завтра", "-e", "1"}, "seed"},
		{"an unknown flag", []string{"--нетакого", "-e", "1"}, "нетакого"},
		{"-n with no data", []string{"-n"}, "--in"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec(tt.argv, "")
			if code != exitUsage {
				t.Errorf("exit = %d, want %d (stdout %q, stderr %q)", code, exitUsage, out, errOut)
			}
			if !strings.Contains(errOut, tt.errHas) {
				t.Errorf("stderr = %q; want it to mention %q", errOut, tt.errHas)
			}
		})
	}
}

// The flags that change how a run is set up rather than what it computes.
func TestCLIRunOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		argv     []string
		stdin    string
		wantCode int
		outHas   []string
		errHas   []string
	}{
		{
			name:   "--stats reports steps and elapsed time on stderr",
			argv:   []string{"--stats", "-e", "(1..100).sum"},
			outHas: []string{"5050"},
			errHas: []string{"steps"},
		},
		{
			// A constant fold would turn `1 + 2` into an Int before the dump, so the
			// expression has to be one the compile step cannot fold.
			name:   "--ast dumps the tree and stops",
			argv:   []string{"--ast", "-e", "x = 1; x + 2"},
			outHas: []string{"Program", "Binary +"},
		},
		{
			name:   "--tokens dumps the token stream and stops",
			argv:   []string{"--tokens", "-e", "1 + 2"},
			outHas: []string{"INT(1)", "EOF"},
		},
		{
			name:   "--check reports a regex the backtracking engine only approximates",
			argv:   []string{"--check", "-e", `"a" ~ /(?<=a)b/`},
			outHas: []string{"ok"},
		},
		{
			name:     "--timeout 0 removes the deadline",
			argv:     []string{"-t", "0", "-e", "1"},
			outHas:   []string{"1"},
			wantCode: exitOK,
		},
		{
			name:     "--steps 0 removes the budget",
			argv:     []string{"--steps", "0", "-e", "(1..1000).sum"},
			outHas:   []string{"500500"},
			wantCode: exitOK,
		},
		{
			name:     "--tasks 0 forbids async",
			argv:     []string{"--tasks", "0", "-e", "async fn f() { 1 }; f()"},
			wantCode: exitLimit,
			errHas:   []string{"tasks are disabled"},
		},
		{
			name:     "an exhausted budget is exit 3",
			argv:     []string{"--steps", "1000", "-e", "while true { }"},
			wantCode: exitLimit,
		},
		{
			name:     "a timeout is exit 3",
			argv:     []string{"-t", "50ms", "--steps", "0", "-e", "while true { }"},
			wantCode: exitLimit,
		},
		{
			name:     "--vars binds typed values from JSON",
			argv:     []string{"--vars", `{"qty": 3, "name": "гель"}`, "-e", `"${$name}: ${$qty * 2}"`},
			outHas:   []string{"гель: 6"},
			wantCode: exitOK,
		},
		{
			name:     "-v binds a string",
			argv:     []string{"-v", "price=1500", "-e", `$price.int + 1`},
			outHas:   []string{"1501"},
			wantCode: exitOK,
		},
		{
			name:     "--no-io withholds the module",
			argv:     []string{"--no-io", "-e", "include io; io.stdin"},
			wantCode: exitError,
			errHas:   []string{"io"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := exec(tt.argv, tt.stdin)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d (stdout %q, stderr %q)", code, tt.wantCode, out, errOut)
			}
			for _, want := range tt.outHas {
				if !strings.Contains(out, want) {
					t.Errorf("stdout = %q; want it to contain %q", out, want)
				}
			}
			for _, want := range tt.errHas {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q; want it to contain %q", errOut, want)
				}
			}
		})
	}
}

// A seed makes the sequence reproducible, which is the whole reason --rand takes one
// (§8.13): two runs with the same seed agree, and the unseeded default does not have to.
func TestCLISeededRandIsReproducible(t *testing.T) {
	t.Parallel()

	const prog = "(1..5).map { rand(1000000) }.json"

	_, first, _ := exec([]string{"--rand=7", "-e", prog}, "")
	_, again, _ := exec([]string{"--rand=7", "-e", prog}, "")
	if first == "" || first != again {
		t.Errorf("two runs with seed 7 gave %q and %q; want the same sequence", first, again)
	}

	_, other, _ := exec([]string{"--rand=8", "-e", prog}, "")
	if other == first {
		t.Errorf("seeds 7 and 8 both gave %q; the seed is not reaching the source", first)
	}
}

// `--` ends the flags, so a script may be handed arguments that look like flags. It
// needs a file: a one-liner and a script file are mutually exclusive (§15).
func TestCLIEndOfFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := filepath.Join(dir, "argv.mzs")
	if err := os.WriteFile(script, []byte("$ARGV.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A script file does not print its value on its own — that is the -e shape — so
	// -p is what makes the run observable here (§15).
	code, out, errOut := exec([]string{"-p", script, "--", "--not-a-flag"}, "")
	if code != exitOK {
		t.Fatalf("exit = %d (stdout %q, stderr %q)", code, out, errOut)
	}
	if !strings.Contains(out, "--not-a-flag") {
		t.Errorf("stdout = %q; want the argument in $ARGV", out)
	}
}

// A script may only include a file under the root of the program that ran it (§12.8,
// §14.3): the CLI is the host here, and the path policy is the host's. The root is the
// script's own directory, and for a `-e` program it is the working directory — which is
// why this test cannot run in parallel: t.Chdir and t.Parallel are exclusive.
func TestCLIIncludeRootIsEnforced(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.mzs"), []byte("export fn one() { 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.mzs")
	if err := os.WriteFile(main, []byte(`include lib from "./lib.mzs"`+"\nlib.one()\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("a sibling include works", func(t *testing.T) {
		code, out, errOut := exec([]string{"-p", main}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stdout %q, stderr %q)", code, out, errOut)
		}
		if !strings.Contains(out, "1") {
			t.Errorf("stdout = %q; want the module's answer", out)
		}
	})

	// The escaping include has to name a module that would otherwise load, or a refusal
	// proves nothing: a missing or unparsable file fails for its own reasons, and the
	// test would pass even if the root policy were dropped entirely.
	t.Run("an include above the root is refused", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "outside.mzs"),
			[]byte("export fn two() { 2 }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(dir, filepath.Join(outside, "outside.mzs"))
		if err != nil {
			t.Fatal(err)
		}
		// Sanity: the path really does climb out of the root, so the refusal below is
		// the policy's doing and not a typo's.
		if !strings.HasPrefix(rel, "..") {
			t.Fatalf("the fixture path %q does not leave the root", rel)
		}

		escaping := filepath.Join(dir, "escape.mzs")
		src := `include x from "` + filepath.ToSlash(rel) + `"` + "\nx.two()\n"
		if err := os.WriteFile(escaping, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}

		code, _, errOut := exec([]string{escaping}, "")
		if code == exitOK {
			t.Fatal("an include outside the root was allowed")
		}
		if !strings.Contains(errOut, "outside the root directory") {
			t.Errorf("stderr = %q; want the root-policy diagnostic", errOut)
		}
	})

	// A `-e` program has no file of its own, so the working directory is its root.
	t.Run("a one-liner resolves from the working directory", func(t *testing.T) {
		t.Chdir(dir)
		code, out, errOut := exec([]string{"-e", `include lib from "./lib.mzs"; lib.one()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stdout %q, stderr %q)", code, out, errOut)
		}
		if !strings.Contains(out, "1") {
			t.Errorf("stdout = %q; want the module's answer", out)
		}
	})

	t.Run("a one-liner cannot climb out of the working directory either", func(t *testing.T) {
		t.Chdir(dir)
		code, _, errOut := exec([]string{"-e", `include x from "../../etc/passwd"`}, "")
		if code == exitOK {
			t.Fatal("a -e program included a file above its working directory")
		}
		if !strings.Contains(errOut, "outside the root directory") {
			t.Errorf("stderr = %q; want the root-policy diagnostic", errOut)
		}
	})
}
