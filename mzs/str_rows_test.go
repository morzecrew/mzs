package mzs

import (
	"context"
	"strings"
	"testing"
)

// RFC 0016, part 3: the string rows. Each case is a script and the Inspect of its value.

func TestStringRowsOfRFC0016(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		// trim with a cutset: a set of runes, not a prefix or a suffix.
		{`"xxhixx".trim("x")`, `"hi"`},
		{`"-=a=-".trim("=-")`, `"a"`},
		{`"--a-b--".trim_start("-")`, `"a-b--"`},
		{`"--a-b--".trim_end("-")`, `"--a-b"`},
		{`"ёёдаёё".trim("ё")`, `"да"`},
		{`"abc".trim("")`, `"abc"`},
		{`"  hi  ".trim(nil)`, `"hi"`},
		{`"  hi  ".trim`, `"hi"`},
		{`" x ".trim("x")`, `" x "`},

		// take and drop count runes.
		{`"привет".take(2)`, `"пр"`},
		{`"привет".drop(2)`, `"ивет"`},
		{`"ab".take(0)`, `""`},
		{`"ab".drop(0)`, `"ab"`},
		{`"ab".take(9)`, `"ab"`},
		{`"ab".drop(9)`, `""`},
		{`"".take(1)`, `""`},
		{`s = "привет"; s.take(3) + s.drop(3) == s`, `true`},

		// truncate counts display columns, and the tail counts toward them.
		{`"hello world".truncate(8)`, `"hello w…"`},
		{`"hello world".truncate(8, "...")`, `"hello..."`},
		{`"hello".truncate(5)`, `"hello"`},
		{`"hello".truncate(4, "")`, `"hell"`},
		{`"日本語テキスト".truncate(7)`, `"日本語…"`},
		{`"日本語テキスト".truncate(7).width`, `7`},
		{`"日本語".truncate(4)`, `"日…"`},
		{`"e\u0301e\u0301e\u0301".truncate(2)`, "\"e\u0301…\""}, // the mark stays with its letter

		// wrap reflows the text as one paragraph, by display columns.
		{`"the quick brown fox jumps".wrap(10)`, `["the quick","brown fox","jumps"]`},
		{`"a\nb  c\n\nd".wrap(3)`, `["a b","c d"]`},
		{`"one two".wrap(7)`, `["one two"]`},
		{`"one two".wrap(6)`, `["one","two"]`},
		{`"supercalifragilistic is long".wrap(5)`, `["supercalifragilistic","is","long"]`},
		{`"日本 語 テキスト".wrap(5)`, `["日本","語","テキスト"]`},
		{`"   ".wrap(5)`, `[]`},
		{`"".wrap(5)`, `[]`},

		// indent prefixes every line with something on it and keeps line endings.
		{`"a\nb".indent("  ")`, `"  a\n  b"`},
		{`"a\n\n  \nb\r\nc\n".indent("> ")`, `"> a\n\n  \n> b\r\n> c\n"`},
		{`"".indent("> ")`, `""`},
		{`"a".indent("")`, `"a"`},
	}
	for _, tt := range cases {
		got, err := evalRow(t, tt.src)
		if err != nil {
			t.Errorf("%s: %v", tt.src, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
		}
	}
}

func TestStringRowErrorsOfRFC0016(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`"ab".trim(1)`, "trim expects a string"},
		{`"ab".trim_start([])`, "trim_start expects a string"},
		{`"ab".take(-1)`, "take expects a non-negative count, got -1"},
		{`"ab".drop(-2)`, "drop expects a non-negative count, got -2"},
		{`"ab".take("1")`, "take expects an integer"},
		{`"ab".truncate(-1)`, "truncate expects a non-negative width, got -1"},
		{`"abc".truncate(0)`, `the tail "…" is wider than the width 0`},
		{`"abc".truncate(1, "...")`, `the tail "..." is wider than the width 1`},
		{`"ab".truncate(1, 2)`, "truncate expects a string"},
		{`"ab".wrap(0)`, "wrap expects a positive width, got 0"},
		{`"ab".wrap("3")`, "wrap expects an integer"},
		{`"ab".indent(2)`, "indent expects a string"},
	}
	for _, tt := range cases {
		_, err := evalRow(t, tt.src)
		if err == nil {
			t.Errorf("%s: want an error containing %q, got none", tt.src, tt.want)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %q; want it to contain %q", tt.src, err.Error(), tt.want)
		}
	}
}

// The rows that build a string or an array charge the caps that every builder does.
func TestStringRowsOfRFC0016AreCapped(t *testing.T) {
	t.Parallel()
	in := New(Options{MaxStringBytes: 100, MaxCollection: 3})
	for _, src := range []string{
		`("a\n" * 40).indent(">>>")`,
		`"a b c d".wrap(1)`,
	} {
		if _, err := in.Eval(context.Background(), src, nil); err == nil {
			t.Errorf("%s ran past a cap; want a limit error", src)
		}
	}
	for src, want := range map[string]string{
		`("a\n" * 10).indent(">")`: strings.Repeat(">a\n", 10),
		`"a b c".wrap(1).len`:      "3",
		`("x" * 90).truncate(5)`:   "xxxx…",
	} {
		v, err := in.Eval(context.Background(), src, nil)
		if err != nil || v.Str() != want {
			t.Errorf("%s = %q, %v; want %q", src, v.Str(), err, want)
		}
	}
}
