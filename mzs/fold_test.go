package mzs

import (
	"strings"
	"testing"
)

// §12.2's two Unicode rows: `casefold`, the comparison key that `lower` is not, and
// `width`, the display columns that `len` is not — and the three padding rows that were
// counting the wrong thing until `width` existed.

// TestCasefold is the table of everything folding does that lowercasing does not.
func TestCasefold(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"ascii is already folded", "hello", "hello"},
		{"empty", "", ""},
		{"case is removed", "HeLLo", "hello"},
		{"cyrillic", "ПРИВЕТ Ёж", "привет ёж"},
		{"sharp s becomes two letters", "Straße", "strasse"},
		{"capital sharp s too", "STRAẞE", "strasse"},
		{"final sigma and capital sigma meet", "ΟΔΟΣ", "οδοσ"},
		{"the other spelling of the same word", "Οδος", "οδοσ"},
		{"a tonos is a letter, not a case", "Οδός", "οδόσ"},
		{"the kelvin sign is a k", "K", "k"},
		{"the long s is an s", "ſ", "s"},
		{"the micro sign is a mu", "µ", "μ"},
		{"a ligature is its letters", "ﬃ", "ffi"},
		{"cherokee folds to its capitals", "ꭰ", "Ꭰ"},
		{"and a cherokee capital stays one", "Ꭰ", "Ꭰ"},
		{"the dotted capital I keeps its dot", "İ", "i\u0307"},
		{"the dotless i is left alone", "ı", "ı"},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.in), "casefold")
			if err != nil {
				t.Fatalf("%q.casefold: %v", tt.in, err)
			}
			if got.Str() != tt.want {
				t.Errorf("%q.casefold = %q; want %q", tt.in, got.Str(), tt.want)
			}
		})
	}
}

// TestCasefoldComparesWhatLowerCannot is the reason the row exists, written the way a
// script writes it.
func TestCasefoldComparesWhatLowerCannot(t *testing.T) {
	c := stdCtx(DefaultOptions())
	fold := func(s string) string {
		t.Helper()
		v, err := stdCall(t, c, Str(s), "casefold")
		if err != nil {
			t.Fatalf("%q.casefold: %v", s, err)
		}
		return v.Str()
	}
	if fold("Straße") != fold("STRASSE") {
		t.Error("casefold did not match the two spellings of Straße")
	}
	if strings.ToLower("Straße") == strings.ToLower("STRASSE") {
		t.Error("lower matched them too, so the row would have nothing to add")
	}
}

// TestCasefoldIsBounded pins that a fold is charged like any other string that gets
// built: it may be longer than what it folded (§14.1).
func TestCasefoldIsBounded(t *testing.T) {
	o := DefaultOptions()
	o.MaxStringBytes = 16
	c := stdCtx(o)
	if _, err := stdCall(t, c, Str(strings.Repeat("ß", 32)), "casefold"); err == nil {
		t.Error("a fold past MaxStringBytes was accepted; want a limit error")
	}
}

// TestFoldLenMatchesFoldString is the join between the two halves of that bound: the size
// is checked before the string exists, so the count and the build have to agree on every
// shape of input — including bytes that are not UTF-8 at all, which `crypto.unbase64` and a
// binary `io.read` hand back (§12.16).
func TestFoldLenMatchesFoldString(t *testing.T) {
	inputs := []string{
		"", "hello", "HeLLo", "ПРИВЕТ Ёж", "Straße", "STRAẞE", "ΟΔΟΣ", "Οδός",
		"K", "ſ", "µ", "ﬃ", "ꭰ", "Ꭰ", "İ", "ı", "\u0390\u0390\u0390",
		"a\xffb", "\xff\xfe", "ß\xffß", "e\u0301", "\U0001f642 ok",
		strings.Repeat("ß", 100), strings.Repeat("a", 100),
	}
	for _, in := range inputs {
		folded := foldString(in)
		if got, want := foldLen(in), len(folded); got != want {
			t.Errorf("foldLen(%q) = %d; foldString produced %d bytes (%q)", in, got, want, folded)
		}
	}
}

// TestCasefoldKeepsBytesItCannotDecode pins the other half of that: text that is not valid
// UTF-8 is a key like any other, and folding it does not quietly replace what it could not
// read with U+FFFD.
func TestCasefoldKeepsBytesItCannotDecode(t *testing.T) {
	c := stdCtx(DefaultOptions())
	got, err := stdCall(t, c, Str("A\xffB"), "casefold")
	if err != nil {
		t.Fatalf("casefold: %v", err)
	}
	if got.Str() != "a\xffb" {
		t.Errorf("casefold = %q; want the invalid byte kept", got.Str())
	}
}

// TestStringWidth is what a terminal, a Markdown table and a fixed-width report all count,
// and what `len` — a rune count — is not.
func TestStringWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"ascii is one column a rune", "abc", 3},
		{"empty", "", 0},
		{"cyrillic is one column a rune", "привет", 6},
		{"CJK takes two", "日本", 4},
		{"an emoji takes two", "\U0001f642", 2},
		{"a combining mark takes none", "e\u0301", 1},
		{"and the precomposed spelling takes one", "é", 1},
		{"a control character takes none", "a\u0001b", 2},
		{"a zero width joiner takes none", "\u200d", 0},
		// A joined emoji sequence is counted rune by rune, so it is 2 + 0 + 2. Whether a
		// terminal draws it as one glyph or two is the terminal's decision — Unicode gives
		// a width to code points, not to grapheme clusters — and this is the same table the
		// REPL moves its cursor by, where guessing wrong costs two columns per emoji.
		{"a joined emoji sequence counts its emoji", "\U0001f469\u200d\U0001f3a8", 4},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.in), "width")
			if err != nil {
				t.Fatalf("%q.width: %v", tt.in, err)
			}
			if got.Int() != tt.want {
				t.Errorf("%q.width = %d; want %d", tt.in, got.Int(), tt.want)
			}
		})
	}
}

// TestJustifyCountsColumns is the fix `width` was needed for: a table padded by rune count
// is ragged wherever the data is not Latin.
func TestJustifyCountsColumns(t *testing.T) {
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want string
	}{
		{"CJK is padded by columns", "日本", "ljust", []Value{Int(6), Str(".")}, "日本.."},
		{"and so is an emoji", "\U0001f642", "rjust", []Value{Int(4), Str(".")}, "..\U0001f642"},
		{"a combining mark is not a column", "e\u0301", "ljust", []Value{Int(3), Str(".")},
			"e\u0301.."},
		{"center splits the remainder right", "日本", "center", []Value{Int(7), Str("-")},
			"-日本--"},
		{"a text already wide enough is itself", "日本", "ljust", []Value{Int(3)}, "日本"},
		{"a two column pad stops short rather than overrunning", "a", "ljust",
			[]Value{Int(4), Str("日")}, "a日"},
		{"ascii is what it always was", "ab", "ljust", []Value{Int(5), Str(".")}, "ab..."},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: %v", tt.recv, tt.meth, err)
			}
			if got.Str() != tt.want {
				t.Errorf("%q.%s = %q; want %q", tt.recv, tt.meth, got.Str(), tt.want)
			}
		})
	}
}

// TestJustifyRejectsPaddingWithNoWidth: padding that occupies no columns could never fill a
// field, and the alternative to the error is a loop that never ends.
func TestJustifyRejectsPaddingWithNoWidth(t *testing.T) {
	c := stdCtx(DefaultOptions())
	for _, pad := range []string{"", "\u0301", "\u200d\ufe0f"} {
		if _, err := stdCall(t, c, Str("да"), "ljust", Int(8), Str(pad)); err == nil {
			t.Errorf("ljust with %q for padding was accepted; want an argument error", pad)
		}
	}
}

// TestJustifyAlignsATable is the whole point in one assertion: two rows of a table, one
// Latin and one CJK, end in the same column.
func TestJustifyAlignsATable(t *testing.T) {
	c := stdCtx(DefaultOptions())
	widths := make([]int64, 0, 2)
	for _, name := range []string{"Ivan", "日本語"} {
		padded, err := stdCall(t, c, Str(name), "ljust", Int(10))
		if err != nil {
			t.Fatalf("%q.ljust: %v", name, err)
		}
		w, err := stdCall(t, c, padded, "width")
		if err != nil {
			t.Fatalf("%q.ljust.width: %v", name, err)
		}
		widths = append(widths, w.Int())
	}
	if widths[0] != widths[1] {
		t.Fatalf("padded widths %v differ; the table is ragged", widths)
	}
}
