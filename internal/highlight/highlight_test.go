package highlight

import (
	"strings"
	"testing"
)

// colorAt is the colour of the first rune of the first occurrence of sub.
func colorAt(t *testing.T, src, sub string) string {
	t.Helper()
	i := strings.Index(src, sub)
	if i < 0 {
		t.Fatalf("%q does not occur in %q", sub, src)
	}
	return Colors([]rune(src))[len([]rune(src[:i]))]
}

func TestColors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		sub  string
		want string
	}{
		{"a keyword", `fn f(a) { a }`, "fn", Keyword},
		{"true is a keyword too", `x = true`, "true", Keyword},
		{"the name being called", `double(21)`, "double", Call},
		{"every link of a chain", `s.lower.trim`, "trim", Call},
		{"a safe-navigation link", `s?.lower`, "lower", Call},
		{"a plain name is plain", `xs.map { it * 2 }`, "it", ""},
		{"an integer", `1 + 2`, "1", Number},
		{"a float", `1.5 + 2`, "1.5", Number},
		{"a string", `"привет"`, `"`, String},
		{"the text inside it", `"привет"`, `привет`, String},
		{"a single-quoted string", `'raw'`, `raw`, String},
		{"a regex", `s ~ /оператор/i`, "/о", Regex},
		{"a global", `$name.lower`, "$name", Global},
		{"a global inside a string", `"hi $name"`, "$name", Global},
		{"an interpolation opener", `"n = ${1 + 2}"`, "${", Global},
		{"the expression inside it", `"n = ${1 + 2}"`, "1", Number},
		{"a comment", `1 + 2 # add them`, "#", Comment},
		{"a whole-line comment", `# nothing here`, "nothing", Comment},
		{"an operator is left alone", `1 + 2`, "+", ""},
		{"a '#' inside a string is text", `"a # b"`, "#", String},
		{"a '#' inside a regex is not a comment", `/a#b/`, "#", Regex},
		{"a division is not a regex", `x = a / 2`, "/", ""},
		{"a bracket that closes nothing", `1 + 2)`, ")", Mismatch},
		{"a bracket closing the wrong thing", `[1, 2}`, "}", Mismatch},
		{"a matched bracket is left alone", `(1 + 2)`, ")", ""},
		{"a matched square bracket too", `[1, 2][0]`, "]", ""},
		{"a matched brace too", `xs.map { it }`, "}", ""},
		{"an unclosed bracket is not an error here", `[1, 2,`, "[", ""},
		{"the '}' of an interpolation is not a block brace", `"${x}"`, "}", Global},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := colorAt(t, tc.src, tc.sub); got != tc.want {
				t.Errorf("Colors(%q)[%q] = %q, want %q", tc.src, tc.sub, got, tc.want)
			}
		})
	}
}

// TestColorsIsAlwaysIndexable is the contract the line editor leans on: one entry per
// rune, for every input, including the half-typed and the outright broken — which is what
// a line looks like most of the time it is being coloured.
func TestColorsIsAlwaysIndexable(t *testing.T) {
	t.Parallel()

	srcs := []string{
		"",
		" ",
		`"unterminated`,
		`'unterminated`,
		`/unterminated`,
		`"${`,
		"fn f(a, b) {",
		"1 +",
		"@reserved",
		"«»‽",
		"\ufeffx = 1",
		"привет = \"мир\" # комментарий",
		"\U0001f600 = 1",
		strings.Repeat("a.b(1) # c\n", 50),
	}
	for _, src := range srcs {
		rs := []rune(src)
		got := Colors(rs)
		if len(got) != len(rs) {
			t.Errorf("Colors(%q) returned %d entries for %d runes", src, len(got), len(rs))
		}
	}
}

// TestBOMDoesNotShiftTheColours pins the one place the lexer's offsets and the caller's
// indices can disagree: a byte-order mark is dropped before the lexer starts counting.
func TestBOMDoesNotShiftTheColours(t *testing.T) {
	t.Parallel()

	src := []rune("\ufefffn f")
	got := Colors(src)
	if got[1] != Keyword || got[2] != Keyword {
		t.Errorf("Colors(%q) = %q, want the keyword coloured after the BOM", string(src), got)
	}
}

// TestCommentRunsToTheEndOfItsLine only: a session line is one line, but .src replays
// several at once and the second must not be swallowed by the first one's comment.
func TestCommentRunsToTheEndOfItsLine(t *testing.T) {
	t.Parallel()

	src := []rune("a # note\nb = 1")
	got := Colors(src)
	if c := got[2]; c != Comment {
		t.Errorf("the '#' is %q, want %q", c, Comment)
	}
	if c := got[len(src)-1]; c != Number {
		t.Errorf("the line after the comment is %q, want %q", c, Number)
	}
}
