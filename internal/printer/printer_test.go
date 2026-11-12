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
		{"a rest target keeps its star", "a,*rest=xs", "a, *rest = xs\n"},
		{"a rest in brackets", "[ *init , last ] = xs", "[*init, last] = xs\n"},
		{"a splat at a call", "f( *xs , **opts )", "f(*xs, **opts)\n"},
		{"a spread in an array", "x = [ *a, 1, *( 2..3 ) ]", "x = [*a, 1, *(2..3)]\n"},
		{"a spread in a dict", "x = { **d , k : 1 }", "x = {**d, k: 1}\n"},
		{"an anonymous rest in an arm", "match x { [h, * ] -> h }", "match x { [h, *] -> h }\n"},
		{"a rest that starts its line", "h = 1\n*init, last = xs", "h = 1\n*init, last = xs\n"},
		{"a dict pattern on the left", "{ name , age :a, \"x-y\" : q , ** rest } = user", "{name, age: a, \"x-y\": q, **rest} = user\n"},
		{"a dict pattern inside an array one", "[ id, { name } ] = row", "[id, {name}] = row\n"},
		{"a dict pattern in an arm", "match e { { kind : \"paid\" , total:t } if t>0 -> t }", "match e { {kind: \"paid\", total: t} if t > 0 -> t }\n"},
		{"membership and an anonymous rest", "match e { {age: in 18..65 , ** } -> 1 }", "match e { {age: in 18..65, **} -> 1 }\n"},
		{"a pattern as a loop variable", "for k , { total } in by { k }", "for k, {total} in by { k }\n"},
		{"an array pattern as a loop variable", "for [ i , r ] in pairs { i }", "for [i, r] in pairs { i }\n"},
		{"a record pattern is written as one", "record M(a)\nmatch m { M{ a } -> a; M{} -> 0 }", "record M(a)\nmatch m { M {a} -> a; M {} -> 0 }\n"},
		{"a call that reads both ways keeps its closure spelling", "match x { choose{it} -> 1 }", "match x { choose { it } -> 1 }\n"},
		{"an empty closure that reads both ways is written as the call", "match x { choose{} -> 1 }", "match x {\n    choose {\n    } -> 1\n}\n"},
		{"a record pattern with more keys", "record M(a, b)\nmatch m { M{a,b:1,**r} -> a }", "record M(a, b)\nmatch m { M {a, b: 1, **r} -> a }\n"},
		{"a comment inside a dict pattern stays inside", "{a, # first\n  b} = d", "{a, # first\n  b} = d\n"},
		{"a comment inside an array pattern stays inside", "[a, # first\n  b] = xs", "[a, # first\n  b] = xs\n"},
		{"a comment inside a loop pattern stays inside", "for {k, # note\n  v} in xs { k }", "for {k, # note\n  v} in xs {\n    k\n}\n"},
		{"a long spread breaks one element to a line",
			"x = [*first_long_array_name_that_is_longer, *second_long_array_name_that_is_longer, *third_one_too]",
			"x = [\n    *first_long_array_name_that_is_longer,\n    *second_long_array_name_that_is_longer,\n    *third_one_too,\n]\n"},
		{"a long dict spread breaks too",
			"y = {**first_long_dict_name_that_is_longer, **second_long_dict_name_that_is_longer, keyname: 12345}",
			"y = {\n    **first_long_dict_name_that_is_longer,\n    **second_long_dict_name_that_is_longer,\n    keyname: 12345,\n}\n"},
		{"a long call with splats breaks",
			"z = compute(*first_long_argument_list_longer_still, **first_long_keyword_options_longer_still, a = 1)",
			"z = compute(\n    *first_long_argument_list_longer_still,\n    **first_long_keyword_options_longer_still,\n    a = 1,\n)\n"},
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

// U+00A0 is whitespace in code (§3.2) and *text* inside a heredoc body: `<<~` sheds
// spaces and tabs and nothing else. A body indented with it therefore cannot be moved
// left — the characters are the string — and the formatter leaves it exactly where it
// was rather than deleting a character from the program's data.
func TestHeredocDoesNotShedANonBreakingSpace(t *testing.T) {
	const nbsp = "\u00a0"
	body := strings.Repeat(nbsp, 12) + "line one"
	src := "if x {\n" +
		strings.Repeat(nbsp, 8) + "s = <<~T\n" +
		body + "\n" +
		strings.Repeat(nbsp, 8) + "T\n" +
		"}\n"
	got, err := Format("t.mzs", src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(got, body+"\n") {
		t.Errorf("the body line changed:\n%q", got)
	}
	twice, err := Format("t.mzs", got)
	if err != nil || twice != got {
		t.Errorf("not idempotent: err=%v\n%q\n%q", err, got, twice)
	}
}

// A heredoc that can afford the move is moved: the body's lines and its terminator shift
// with the code around them, and `<<~` sheds the same amount either way.
func TestHeredocMovesWithItsCode(t *testing.T) {
	src := "if x {\n" +
		"      s = <<~T\n" +
		"        line one\n" +
		"      T\n" +
		"}\n"
	want := "if x {\n" +
		"    s = <<~T\n" +
		"      line one\n" +
		"    T\n" +
		"}\n"
	got, err := Format("t.mzs", src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// A comment inside a bracket keeps its place: the broken form has a line for every element
// and one more before the closing bracket. A comment anywhere else — between an operator
// and its operand, say — does not decide the layout; it is written out wherever the layout
// allows and is never lost, which is the whole of the promise.
func TestACommentWithNoSlotIsKept(t *testing.T) {
	for _, src := range []string{
		"while a && # why\n    b {\n    step()\n}\n",
		"x = f(\n    a, # first\n    b,\n)\n",
		"if a || # why\n    b {\n    step()\n}\n",
		"xs = [\n    1,\n    2,\n    # a last word\n]\n",
		"d = {\n    # about a\n    a: 1,\n}\n",
	} {
		got, err := Format("t.mzs", src)
		if err != nil {
			t.Fatalf("Format(%q): %v", src, err)
		}
		if strings.Count(got, "#") != strings.Count(src, "#") {
			t.Errorf("a comment was dropped from %q:\n%s", src, got)
		}
		twice, err := Format("t.mzs", got)
		if err != nil || twice != got {
			t.Errorf("not idempotent: err=%v\n%q\n%q", err, got, twice)
		}
	}
}

// A comment on a heredoc's own line travels with the statement and is written once.
func TestACommentBesideAHeredocIsKeptOnce(t *testing.T) {
	src := "s = <<~T # the tag's own line\n  body\nT\n"
	got, err := Format("t.mzs", src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if n := strings.Count(got, "# the tag's own line"); n != 1 {
		t.Errorf("the comment appears %d times:\n%s", n, got)
	}
}

// A condition too long for one line breaks after its operator, which §3.10 lets a line end
// on. `while` and `if` take the same path and both are worth pinning.
func TestALongConditionBreaks(t *testing.T) {
	long := strings.Repeat("a", 50)
	for _, kw := range []string{"if", "while"} {
		src := kw + " " + long + " && " + long + " {\n    step()\n}\n"
		got, err := Format("t.mzs", src)
		if err != nil {
			t.Fatalf("Format: %v", err)
		}
		if !strings.Contains(got, "&&\n") {
			t.Errorf("%s: the condition did not break after its operator:\n%s", kw, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if len([]rune(line)) > Width {
				t.Errorf("%s: a line is %d columns wide:\n%s", kw, len([]rune(line)), got)
			}
		}
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
