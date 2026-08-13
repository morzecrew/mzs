package mzs

import (
	"strings"
	"testing"
)

// §12.6 is the whole regex surface, and D17 allows exactly one name per operation, so
// the table is pinned as a set: no 1.0 spelling (`to_s`, `match?`, `match`, `names`) and
// no operator-as-method (`=~`, `===`) survives — matching is the `~` operator (D5).
func TestRegexMethodTable(t *testing.T) {
	want := []string{"captures", "flags", "index", "matches", "source", "str"}
	if got := MethodNames(KRegex); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MethodNames(KRegex) = %v; want %v", got, want)
	}
	for _, gone := range []string{"to_s", "match?", "match", "names", "=~", "===", "source?"} {
		if _, ok := methodTables[KRegex][gone]; ok {
			t.Errorf("regex answers %q; §12.6 and D17 say it must not", gone)
		}
	}
}

func TestRegexValueMethods(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		meth    string
		args    []Value
		want    Value
	}{
		{"source is the pattern text", `\bменю\b`, "i", "source", nil, Str(`\bменю\b`)},
		{"flags are canonical", `a`, "i", "flags", nil, Str("i")},
		{"str is the literal form", `a`, "i", "str", nil, Str("/a/i")},
		{"captures returns the groups", `(\d+)-(\d+)`, "", "captures", []Value{Str("12-34")},
			Array(Str("12-34"), Str("12"), Str("34"))},
		{"captures failing is nil", `\d`, "", "captures", []Value{Str("abc")}, Nil()},
		{"index is a rune index", `вет`, "", "index", []Value{Str("привет")}, Int(3)},
		{"index of no match is nil", `zz`, "", "index", []Value{Str("привет")}, Nil()},
		{"matches lists every match", `\d`, "", "matches", []Value{Str("a1b2")},
			Array(Str("1"), Str("2"))},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recv := regexArg(tt.pattern, tt.flags)
			got, err := stdCall(t, c, recv, tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("/%s/%s.%s: unexpected error %v", tt.pattern, tt.flags, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("/%s/%s.%s = %s; want %s", tt.pattern, tt.flags, tt.meth,
					got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

// The §16.2 patterns reach the stdlib through `.downcase =~ /…/i`, so the rune index
// and the Cyrillic folding are what the corpus actually observes.
func TestRegexCorpusIndices(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		pattern string
		want    Value
	}{
		{"row 21 greeting matches at zero", "привет",
			`привет|здравствуй|hello|\bhi\b`, Int(0)},
		{"row 22 farewell at a rune index", "ну пока",
			`пока|до свидан|\bbye\b|прощай`, Int(3)},
		{"row 23 no match is nil", "что?",
			`эхо|echo|тест|ping|пинг`, Nil()},
		{"row 25 bounded gap", "бесплатная консультация",
			`бесплатн.{0,14}(аудит|консультац)|free.?audit`, Int(0)},
		{"row 26 latin inside Cyrillic text", "нужна crm",
			`\bcrm\b|црм|клиентск.{0,8}баз`, Int(6)},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := regexArg(tt.pattern, "i")
			got, err := regexIndex(c, re.rx(), tt.subject)
			if err != nil {
				t.Fatalf("%q =~ /%s/i: unexpected error %v", tt.subject, tt.pattern, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q =~ /%s/i = %s; want %s", tt.subject, tt.pattern,
					got.Inspect(), tt.want.Inspect())
			}
		})
	}
}
