package printer

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mzs/internal/ast"
	"mzs/internal/lexer"
	"mzs/internal/parser"
)

var update = flag.Bool("update", false, "rewrite the .golden files from the current output")

// TestGolden is the formatter's shape: one `.in` file per family of rules, one `.golden`
// file holding what it must come out as. `go test ./internal/printer -update` rewrites
// them, and the diff in review is the whole point of the exercise.
func TestGolden(t *testing.T) {
	ins, err := filepath.Glob("testdata/fmt/*.in")
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) == 0 {
		t.Fatal("no testdata/fmt/*.in files")
	}
	for _, in := range ins {
		t.Run(filepath.Base(in), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Format(in, string(src))
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			golden := strings.TrimSuffix(in, ".in") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("formatting %s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s",
					in, got, want)
			}
		})
	}
}

// TestGoldenIsFormatted is the fixed point: a `.golden` file is already what the
// formatter produces, so formatting it again changes nothing. It is idempotency stated
// on the files a reader looks at.
func TestGoldenIsFormatted(t *testing.T) {
	goldens, _ := filepath.Glob("testdata/fmt/*.golden")
	for _, g := range goldens {
		src, err := os.ReadFile(g)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Format(g, string(src))
		if err != nil {
			t.Fatalf("%s: %v", g, err)
		}
		if out != string(src) {
			t.Errorf("%s is not its own formatting", g)
		}
	}
}

// TestIdempotentOverExamples is the invariant the whole design is arranged around, run
// over every program the repository ships: formatting twice is formatting once.
//
// Format checks on every call that the tree and the comments came through unchanged, so
// this test is also, for free, the assertion that no example is altered by `mzs fmt`.
func TestIdempotentOverExamples(t *testing.T) {
	files := exampleFiles(t)
	if len(files) < 40 {
		t.Fatalf("found %d examples, expected the whole corpus", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			once, err := Format(f, string(src))
			if err != nil {
				t.Fatalf("first pass: %v", err)
			}
			twice, err := Format(f, once)
			if err != nil {
				t.Fatalf("second pass: %v", err)
			}
			if once != twice {
				t.Errorf("fmt(fmt(x)) != fmt(x)\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
		})
	}
}

// TestCommentsSurviveTheExamples states the half of the promise an AST comparison cannot
// make. Format refuses rather than drops, so a failure here is a refusal — but the count
// is worth asserting on the real corpus, where the comments outnumber the code.
func TestCommentsSurviveTheExamples(t *testing.T) {
	for _, f := range exampleFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Format(f, string(src))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		_, before, _, _ := lexer.Scan(f, string(src))
		_, after, _, _ := lexer.Scan(f, out)
		if len(before) != len(after) {
			t.Errorf("%s: %d comments in, %d out", f, len(before), len(after))
		}
	}
}

// TestTreeSurvivesTheExamples is the other half, spelled out here rather than left to the
// check inside Format: the formatted text parses to the same tree.
func TestTreeSurvivesTheExamples(t *testing.T) {
	for _, f := range exampleFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Format(f, string(src))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		before, err := parser.Parse(f, string(src))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		after, err := parser.Parse(f, out)
		if err != nil {
			t.Fatalf("%s reformatted: %v", f, err)
		}
		if ast.Dump(before) != ast.Dump(after) {
			t.Errorf("%s: the tree changed", f)
		}
	}
}

func exampleFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.mzs"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"an empty file stays empty", "", ""},
		{"whitespace alone is an empty file", "\n\n  \n", ""},
		{"a missing final newline is added", "x = 1", "x = 1\n"},
		{"runs of blank lines become one", "x = 1\n\n\n\ny = 2\n", "x = 1\n\ny = 2\n"},
		{"a leading blank line goes", "\n\nx = 1\n", "x = 1\n"},
		{"operators get their spaces", "x=1+2", "x = 1 + 2\n"},
		{"a comma is followed by one space", "f( 1,2 )", "f(1, 2)\n"},
		{"unary minus keeps none", "x = -y", "x = -y\n"},
		{"two minus signs keep one", "x = - -y", "x = - -y\n"},
		{"a range has no spaces", "x = 1 .. 10", "x = 1..10\n"},
		{"an exclusive range keeps its <", "x = 1..<10", "x = 1..<10\n"},
		{"a method with no arguments drops its parens", "x = s.upper()", "x = s.upper\n"},
		{"a method with named arguments keeps them", "x = s.f(a = 1)", "x = s.f(a = 1)\n"},
		{"a trailing closure stays outside", "x = xs.map { it }", "x = xs.map { it }\n"},
		{"a closure argument stays inside", "x = f({ it })", "x = f({ it })\n"},
		{"a dict key keeps its own spelling", `x = {"a": 1, b: 2}`, "x = {\"a\": 1, b: 2}\n"},
		{"a literal key keeps its arrow", `x = {1 -> "a"}`, "x = {1 -> \"a\"}\n"},
		{"a computed key keeps its parens", "x = {(k): 1}", "x = {(k): 1}\n"},
		{"an integer keeps its base", "x = 0xff", "x = 0xff\n"},
		{"a float keeps its zeros", "x = 1.50", "x = 1.50\n"},
		{"a raw string gains no escapes", `x = 'a\nb'`, "x = 'a\\nb'\n"},
		{"the if modifier stays a modifier", "println(1) if x", "println(1) if x\n"},
		{"the while modifier stays a modifier", "x -= 1 while x", "x -= 1 while x\n"},
		{"a one-line body stays on its line", "fn f(a) { a }", "fn f(a) { a }\n"},
		{"a body written across lines stays across them", "fn f(a) {\n a\n}", "fn f(a) {\n    a\n}\n"},
		{"indentation is four spaces", "if x {\n  x\n}", "if x {\n    x\n}\n"},
		{"a trailing comment stays on its line", "x = 1 # why\n", "x = 1  # why\n"},
		{"a comment above stays above", "# why\nx = 1\n", "# why\nx = 1\n"},
		{"parentheses are kept", "x = (1 + 2) * 3", "x = (1 + 2) * 3\n"},
		{"an empty array is one token pair", "x = []", "x = []\n"},
		{"an empty dict is one token pair", "x = {}", "x = {}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format("t.mzs", tc.in)
			if err != nil {
				t.Fatalf("Format(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Format(%q) =\n%q\nwant\n%q", tc.in, got, tc.want)
			}
		})
	}
}

// A file written on Windows comes out with one kind of line ending, and a heredoc body
// inside it comes out with the same one: the lexer drops the carriage return either way
// (§3.2), so the text the program holds does not change.
func TestFormatNormalisesLineEndings(t *testing.T) {
	src := "x = 1\r\ns = <<~T\r\n  body\r\nT\r\ny = 2\r\n"
	got, err := Format("t.mzs", src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("a carriage return survived:\n%q", got)
	}
	want := "x = 1\ns = <<~T\n  body\nT\ny = 2\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestFormatRefusesWhatDoesNotParse(t *testing.T) {
	for _, src := range []string{"x = (1 +", "fn f( {", "x = 'unterminated"} {
		if _, err := Format("t.mzs", src); err == nil {
			t.Errorf("Format(%q) = no error, want one", src)
		}
	}
}

// A line only breaks where breaking helps. A single argument longer than the page is not
// improved by moving it down a line and the closing paren down another.
func TestLongSingleArgumentStaysPut(t *testing.T) {
	src := `println("` + strings.Repeat("x", 120) + `")` + "\n"
	got, err := Format("t.mzs", src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got\n%s\nwant it unchanged", got)
	}
}
