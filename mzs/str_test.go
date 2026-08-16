package mzs

import (
	"context"
	"strings"
	"testing"

	"mzs/internal/token"
)

// These tests drive the method tables the way the evaluator's dispatch site does —
// arity check, the trailing closure mirrored onto the Ctx, `break` turned into the value
// of the whole call — rather than through a parse. That is also the sharper test: it
// pins the §12 contract itself.

// stdCtx builds a Ctx outside a script run.
func stdCtx(opts Options) *Ctx {
	in := New(opts)
	return newCtx(newRunState(in, context.Background(), "test", nil))
}

// stdBlock wraps a Go function as a closure value. A method cannot tell it apart from a
// `{ (x) -> … }` written in a script, and must not try (§4.2).
func stdBlock(f func(args []Value) (Value, error)) Value {
	return Fn("block", -1, func(c *Ctx, args []Value) (Value, error) { return f(args) })
}

// trailingClosure is call.go's enterCall rule: a closure is an ordinary last argument
// (§4.2), and the Ctx offers it a second way for the rows that ask `HasClosure`.
func trailingClosure(args []Value) Value {
	if n := len(args); n > 0 && args[n-1].Kind() == KFunc {
		return args[n-1]
	}
	return Nil()
}

func stdCall(t *testing.T, c *Ctx, recv Value, name string, args ...Value) (Value, error) {
	t.Helper()
	m, ok := LookupMethod(recv.Kind(), name)
	if !ok {
		t.Fatalf("no method %q for %s", name, recv.Kind())
	}
	if err := m.CheckArity(len(args)); err != nil {
		return Nil(), err
	}
	restore := c.setCall(name, token.Pos{Line: 1, Col: 1}, args, trailingClosure(args))
	defer restore()
	v, err := m.Fn(c, recv, args)
	if bv, isBrk := isBreak(err); isBrk {
		return bv, nil
	}
	return v, err
}

func stdBuiltin(t *testing.T, c *Ctx, name string, args ...Value) (Value, error) {
	t.Helper()
	b, ok := LookupBuiltin(name)
	if !ok {
		t.Fatalf("no builtin %q", name)
	}
	opts := c.Options()
	if !b.Available(&opts) {
		t.Fatalf("builtin %q is not available under these options", name)
	}
	if err := b.CheckArity(len(args)); err != nil {
		return Nil(), err
	}
	restore := c.setCall(name, token.Pos{Line: 1, Col: 1}, args, trailingClosure(args))
	defer restore()
	v, err := b.Fn(c, args)
	if bv, isBrk := isBreak(err); isBrk {
		return bv, nil
	}
	return v, err
}

// stdSame is stricter than Value.Equal: it also pins the kind, so a row expecting
// Int(2) is not satisfied by Float(2) (D9 makes that distinction observable).
func stdSame(a, b Value) bool { return a.Kind() == b.Kind() && a.Equal(b) }

// regexArg compiles a literal pattern for a test table row.
func regexArg(pattern, flags string) Value {
	v, err := Regex(pattern, flags)
	if err != nil {
		panic(err)
	}
	return v
}

// ---------------------------------------------------------------------------

func TestStringCaseAndTrim(t *testing.T) {
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want Value
	}{
		{"lower folds Cyrillic", "ПРИВЕТ ЁЖ", "lower", nil, Str("привет ёж")},
		{"upper folds Cyrillic", "да", "upper", nil, Str("ДА")},
		{"capitalize", "пРИВЕТ мир", "capitalize", nil, Str("Привет мир")},
		{"swapcase", "AbВг", "swapcase", nil, Str("aBвГ")},
		{"trim ascii space", "  ОПЕРАТОР ", "trim", nil, Str("ОПЕРАТОР")},
		{"trim NBSP", " да ", "trim", nil, Str("да")},
		{"trim zero width space", "\u200bда\ufeff", "trim", nil, Str("да")},
		{"trim tabs and newlines", "\t да \n", "trim", nil, Str("да")},
		{"trim_start keeps the tail", "  да  ", "trim_start", nil, Str("да  ")},
		{"trim_end keeps the head", "  да  ", "trim_end", nil, Str("  да")},
		{"chomp drops one newline", "да\n", "chomp", nil, Str("да")},
		{"chomp drops crlf", "да\r\n", "chomp", nil, Str("да")},
		{"chomp with a suffix", "да!", "chomp", []Value{Str("!")}, Str("да")},
		{"chop drops the last rune", "привет", "chop", nil, Str("приве")},
		{"chop on empty", "", "chop", nil, Str("")},
		{"squeeze runs", "aaabbc", "squeeze", nil, Str("abc")},
		{"squeeze a set", "aaabbc", "squeeze", []Value{Str("a")}, Str("abbc")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: unexpected error %v", tt.recv, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q.%s = %s; want %s", tt.recv, tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

func TestStringSizeAndPredicates(t *testing.T) {
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want Value
	}{
		{"len counts runes not bytes", "привет", "len", nil, Int(6)},
		{"len counts a flag as two runes", "🇷🇺", "len", nil, Int(2)},
		{"empty on empty", "", "empty", nil, Bool(true)},
		{"empty on blank", " ", "empty", nil, Bool(false)},
		{"blank on whitespace", "  \n", "blank", nil, Bool(true)},
		{"blank on text", " да ", "blank", nil, Bool(false)},
		{"has a substring", "hello", "has", []Value{Str("lo")}, Bool(true)},
		{"has misses", "hello", "has", []Value{Str("zz")}, Bool(false)},
		{"has Cyrillic", "нужна CRM", "has", []Value{Str("CRM")}, Bool(true)},
		{"starts_with a single prefix", "/start", "starts_with", []Value{Str("/")}, Bool(true)},
		{"starts_with any of several", "меню", "starts_with", []Value{Str("x"), Str("ме")}, Bool(true)},
		{"starts_with none", "меню", "starts_with", []Value{Str("x")}, Bool(false)},
		{"ends_with", "file.json", "ends_with", []Value{Str(".json")}, Bool(true)},
		{"count occurrences", "abcabc", "count", []Value{Str("bc")}, Int(2)},
		{"count of an empty needle is zero", "abc", "count", []Value{Str("")}, Int(0)},
		{"ord of a Cyrillic rune", "ё", "ord", nil, Int(0x451)},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: unexpected error %v", tt.recv, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q.%s = %s; want %s", tt.recv, tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

func TestStringConversions(t *testing.T) {
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want Value
	}{
		{"empty int is zero", "", "int", nil, Int(0)},
		{"leading digits", "800", "int", nil, Int(800)},
		{"trailing junk tolerated", "12abc", "int", nil, Int(12)},
		{"underscores allowed", "1_000", "int", nil, Int(1000)},
		{"signed with spaces", "  -37 ", "int", nil, Int(-37)},
		{"hex is base ten only", "0x1f", "int", nil, Int(0)},
		{"words are zero", "abc", "int", nil, Int(0)},
		{"float", "1.5кг", "float", nil, Float(1.5)},
		{"float of junk", "abc", "float", nil, Float(0)},
		{"str is identity", "да", "str", nil, Str("да")},
		{"json quotes", "a\"b", "json", nil, Str(`"a\"b"`)},
		{"reverse is rune-wise", "привет", "reverse", nil, Str("тевирп")},
		{"first default", "привет", "first", nil, Str("п")},
		{"first n", "привет", "first", []Value{Int(3)}, Str("при")},
		{"first beyond the end", "да", "first", []Value{Int(9)}, Str("да")},
		{"last n", "привет", "last", []Value{Int(2)}, Str("ет")},
		{"first_and_last", "привет", "first_and_last", nil, Str("пт")},
		{"slice one rune", "привет", "slice", []Value{Int(1)}, Str("р")},
		{"slice n runes", "привет", "slice", []Value{Int(1), Int(3)}, Str("рив")},
		{"slice from the end", "привет", "slice", []Value{Int(-2), Int(2)}, Str("ет")},
		{"slice out of range", "да", "slice", []Value{Int(9)}, Nil()},
		{"ljust pads with runes", "аб", "ljust", []Value{Int(5), Str("·")}, Str("аб···")},
		{"rjust", "аб", "rjust", []Value{Int(5), Str("·")}, Str("···аб")},
		{"center splits the remainder right", "аб", "center", []Value{Int(5), Str("·")}, Str("·аб··")},
		{"ljust no-op when wide enough", "абвгд", "ljust", []Value{Int(3)}, Str("абвгд")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: unexpected error %v", tt.recv, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q.%s = %s; want %s", tt.recv, tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

func TestStringCollections(t *testing.T) {
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want Value
	}{
		{"chars are one-rune strings", "аб", "chars", nil, Array(Str("а"), Str("б"))},
		{"bytes are integers", "é", "bytes", nil, Array(Int(0xc3), Int(0xa9))},
		{"lines drops the terminator", "a\nb\n", "lines", nil, Array(Str("a"), Str("b"))},
		{"split on a literal", "ivan:i@x.ru", "split", []Value{Str(":")},
			Array(Str("ivan"), Str("i@x.ru"))},
		{"split on whitespace runs", "Иван   Петров", "split", []Value{Str(" ")},
			Array(Str("Иван"), Str("Петров"))},
		{"split with no argument splits on whitespace", " a  b ", "split", nil,
			Array(Str("a"), Str("b"))},
		{"split keeps every field", "a,b,,", "split", []Value{Str(",")},
			Array(Str("a"), Str("b"), Str(""), Str(""))},
		{"a positive limit caps the fields", "a:b:c", "split", []Value{Str(":"), Int(2)},
			Array(Str("a"), Str("b:c"))},
		{"an empty separator splits into runes", "аб", "split", []Value{Str("")},
			Array(Str("а"), Str("б"))},
		{"split on a regex", "a1b22c", "split", []Value{regexArg("[0-9]+", "")},
			Array(Str("a"), Str("b"), Str("c"))},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: unexpected error %v", tt.recv, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q.%s = %s; want %s", tt.recv, tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

func TestStringRegexMethods(t *testing.T) {
	c := stdCtx(DefaultOptions())
	tests := []struct {
		name string
		recv string
		meth string
		args []Value
		want Value
	}{
		{"replace removes an apostrophe", "О'Брайен", "replace",
			[]Value{regexArg("'", ""), Str("")}, Str("ОБрайен")},
		{"replace with a literal string pattern", "a.b.c", "replace",
			[]Value{Str("."), Str("-")}, Str("a-b-c")},
		{"replace expands a group", "12-34", "replace",
			[]Value{regexArg(`(\d+)-(\d+)`, ""), Str(`\2-\1`)}, Str("34-12")},
		{"replace_first replaces only the first", "aaa", "replace_first",
			[]Value{Str("a"), Str("b")}, Str("baa")},
		{"index of a substring is a rune index", "привет мир", "index",
			[]Value{Str("мир")}, Int(7)},
		{"index missing is nil", "привет", "index", []Value{Str("zz")}, Nil()},
		{"index of a regex", "abc123", "index",
			[]Value{regexArg(`\d`, "")}, Int(3)},
		{"index honours the start offset", "abab", "index",
			[]Value{Str("ab"), Int(1)}, Int(2)},
		{"index at zero is still a hit", "оператор", "index",
			[]Value{regexArg("оператор", "i")}, Int(0)},
		{"last_index takes the last", "abab", "last_index", []Value{Str("ab")}, Int(2)},
		{"count with a regex", "a1b2c3", "count",
			[]Value{regexArg(`\d`, "")}, Int(3)},
		{"matches without groups", "a1b22", "matches",
			[]Value{regexArg(`\d+`, "")}, Array(Str("1"), Str("22"))},
		{"matches with groups yields group arrays", "Mon Tue", "matches",
			[]Value{regexArg(`(Mon|Tue)`, "")},
			Array(Array(Str("Mon")), Array(Str("Tue")))},
		{"captures returns the whole match then the groups", "2024-01", "captures",
			[]Value{regexArg(`(\d+)-(\d+)`, "")},
			Array(Str("2024-01"), Str("2024"), Str("01"))},
		{"captures failing is nil", "abc", "captures",
			[]Value{regexArg(`\d`, "")}, Nil()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%q.%s: unexpected error %v", tt.recv, tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%q.%s = %s; want %s", tt.recv, tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	// matches and captures are spelled `(re: regex)` in §12.2 — a string there is a
	// mistake, not a pattern to quote.
	t.Run("matches refuses a string pattern", func(t *testing.T) {
		if _, err := stdCall(t, c, Str("a1"), "matches", Str("1")); err == nil {
			t.Errorf("matches(\"1\"): want a type error, got none")
		}
	})
}

func TestStringClosureForms(t *testing.T) {
	c := stdCtx(DefaultOptions())

	t.Run("each_char iterates and returns the receiver", func(t *testing.T) {
		var seen []string
		block := stdBlock(func(args []Value) (Value, error) {
			seen = append(seen, args[0].Str())
			return Nil(), nil
		})
		got, err := stdCall(t, c, Str("абв"), "each_char", block)
		if err != nil {
			t.Fatalf("each_char: %v", err)
		}
		if strings.Join(seen, "") != "абв" {
			t.Errorf("each_char visited %v; want а б в", seen)
		}
		if !stdSame(got, Str("абв")) {
			t.Errorf("each_char = %s; want the receiver", got.Inspect())
		}
	})

	t.Run("replace with a closure uses the closure's result", func(t *testing.T) {
		block := stdBlock(func(args []Value) (Value, error) {
			return Str(strings.ToUpper(args[0].Str())), nil
		})
		got, err := stdCall(t, c, Str("a1b2"), "replace", regexArg("[ab]", ""), block)
		if err != nil {
			t.Fatalf("replace: %v", err)
		}
		if !stdSame(got, Str("A1B2")) {
			t.Errorf("replace with a closure = %s; want \"A1B2\"", got.Inspect())
		}
	})

	t.Run("replace passes the match then its groups", func(t *testing.T) {
		var got []string
		block := stdBlock(func(args []Value) (Value, error) {
			for _, a := range args {
				got = append(got, a.Str())
			}
			return Str("x"), nil
		})
		if _, err := stdCall(t, c, Str("12-34"), "replace", regexArg(`(\d+)-(\d+)`, ""), block); err != nil {
			t.Fatalf("replace: %v", err)
		}
		want := []string{"12-34", "12", "34"}
		if len(got) != len(want) || got[0] != want[0] || got[2] != want[2] {
			t.Errorf("the replace closure saw %v; want %v", got, want)
		}
	})

	t.Run("break out of each_char is the value of the call", func(t *testing.T) {
		block := stdBlock(func(args []Value) (Value, error) { return Nil(), breakSignal(Int(7)) })
		got, err := stdCall(t, c, Str("abc"), "each_char", block)
		if err != nil {
			t.Fatalf("each_char: %v", err)
		}
		if !stdSame(got, Int(7)) {
			t.Errorf("break inside each_char = %s; want 7", got.Inspect())
		}
	})

	t.Run("each_char without a closure reports it", func(t *testing.T) {
		if _, err := stdCall(t, c, Str("abc"), "each_char", Nil()); err == nil {
			t.Errorf("each_char without a closure: want an error, got none")
		}
	})
}

func TestStringFormat(t *testing.T) {
	tests := []struct {
		name   string
		format string
		args   []Value
		want   string
	}{
		{"string and decimal", "%s-%d", []Value{Str("a"), Int(1)}, "a-1"},
		{"percent literal", "100%%", nil, "100%"},
		{"fixed precision", "%.2f", []Value{Float(1.256)}, "1.26"},
		{"width and zero pad", "%05d", []Value{Int(42)}, "00042"},
		{"left align", "%-5s|", []Value{Str("ab")}, "ab   |"},
		{"i is d", "%i", []Value{Int(7)}, "7"},
		{"hex", "%x", []Value{Int(255)}, "ff"},
		{"hex with the sharp flag", "%#x", []Value{Int(255)}, "0xff"},
		{"octal", "%o", []Value{Int(8)}, "10"},
		{"binary", "%b", []Value{Int(5)}, "101"},
		{"general", "%g", []Value{Float(0.5)}, "0.5"},
		{"exponent", "%e", []Value{Float(1500)}, "1.500000e+03"},
		{"json verb", "%j", []Value{Array(Int(1), Int(2))}, "[1,2]"},
		{"char from a codepoint", "%c", []Value{Int(0x44f)}, "я"},
		{"char from a string", "%c", []Value{Str("яя")}, "я"},
		{"star width", "%*d", []Value{Int(4), Int(7)}, "   7"},
		{"named brace", "Привет %{who}", []Value{Dict(Str("who"), Str("мир"))}, "Привет мир"},
		{"named angle with a verb", "%<n>05d", []Value{Dict(Str("n"), Int(42))}, "00042"},
		{"Cyrillic passes through", "цена: %d ₽", []Value{Int(1500)}, "цена: 1500 ₽"},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatValues(c, tt.format, tt.args)
			if err != nil {
				t.Fatalf("format(%q): unexpected error %v", tt.format, err)
			}
			if got != tt.want {
				t.Errorf("format(%q) = %q; want %q", tt.format, got, tt.want)
			}
		})
	}

	t.Run("too few arguments is an error", func(t *testing.T) {
		if _, err := formatValues(c, "%s %s", []Value{Str("a")}); err == nil {
			t.Errorf("format with a missing argument: want an error, got none")
		}
	})
	t.Run("an unknown verb is an error", func(t *testing.T) {
		if _, err := formatValues(c, "%q", []Value{Str("a")}); err == nil {
			t.Errorf("format with an unknown verb: want an error, got none")
		}
	})
}

// §9.1: there is one semantics and no coercion, so a number where §12.2 asks for a
// string is a type error at every row that takes one.
func TestStringArgumentTypeErrors(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		meth string
		args []Value
	}{
		{"has with an int", "has", []Value{Int(1)}},
		{"has with an array", "has", []Value{Array(Int(1))}},
		{"starts_with with an int", "starts_with", []Value{Int(1)}},
		{"ends_with with nil", "ends_with", []Value{Nil()}},
		{"index with an array", "index", []Value{Array()}},
		{"chomp with an int suffix", "chomp", []Value{Int(1)}},
		{"ljust with a string width", "ljust", []Value{Str("5")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := stdCall(t, c, Str("abc"), tt.meth, tt.args...); err == nil {
				t.Errorf("%s: want a type error, got none", tt.meth)
			}
		})
	}
}

// D17: one operation, one name. The Ruby spellings the corpus used to carry are not
// registered under any kind, so §5.6's did-you-mean is the only thing they can produce.
func TestStringHasNoOldNames(t *testing.T) {
	tests := []struct {
		old, use string
	}{
		{"downcase", "lower"},
		{"upcase", "upper"},
		{"strip", "trim"},
		{"lstrip", "trim_start"},
		{"rstrip", "trim_end"},
		{"length", "len"},
		{"size", "len"},
		{"include", "has"},
		{"contains", "has"},
		{"start_with", "starts_with"},
		{"end_with", "ends_with"},
		{"gsub", "replace"},
		{"sub", "replace_first"},
		{"tr", "replace"},
		{"scan", "matches"},
		{"match", "captures"},
		{"rindex", "last_index"},
		{"present", "blank"},
		{"to_s", "str"},
		{"to_i", "int"},
		{"to_f", "float"},
		{"to_a", "chars"},
		{"to_json", "json"},
		{"to_sym", "str"},
		{"to_date", "date.parse"},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			if HasMethod(KString, tt.old) {
				t.Errorf("string answers %q; D17 allows only %q", tt.old, tt.use)
			}
		})
	}
}

// The §16.1 rows that are pure string work, evaluated through the method tables.
func TestCorpusStringRows(t *testing.T) {
	c := stdCtx(DefaultOptions())

	chain := func(t *testing.T, recv Value, steps ...any) Value {
		t.Helper()
		v := recv
		for i := 0; i < len(steps); i += 2 {
			name := steps[i].(string)
			args := steps[i+1].([]Value)
			out, err := stdCall(t, c, v, name, args...)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			v = out
		}
		return v
	}
	no := []Value{}

	tests := []struct {
		name string
		run  func(t *testing.T) Value
		want Value
	}{
		{"row 18 lower trim /Operator", func(t *testing.T) Value {
			return chain(t, Str(" /Operator "), "lower", no, "trim", no)
		}, Str("/operator")},
		{"row 19 lower trim оператор", func(t *testing.T) Value {
			return chain(t, Str("  ОПЕРАТОР "), "lower", no, "trim", no)
		}, Str("оператор")},
		{"row 20 keeps the question mark", func(t *testing.T) Value {
			return chain(t, Str("Сколько стоит?"), "lower", no, "trim", no)
		}, Str("сколько стоит?")},
		{"row 27 price int", func(t *testing.T) Value {
			return chain(t, Str("800"), "int", no)
		}, Int(800)},
		{"row 28 empty price int", func(t *testing.T) Value {
			return chain(t, Str(""), "int", no)
		}, Int(0)},
		{"row 31 split first field", func(t *testing.T) Value {
			return chain(t, Str("ivan:i@x.ru"), "split", []Value{Str(":")}).Index(0)
		}, Str("ivan")},
		{"row 32 split second field", func(t *testing.T) Value {
			return chain(t, Str("ivan:i@x.ru"), "split", []Value{Str(":")}).Index(1)
		}, Str("i@x.ru")},
		{"row 33 split on a space", func(t *testing.T) Value {
			return chain(t, Str("Иван Петров"), "split", []Value{Str(" ")}).Index(0)
		}, Str("Иван")},
		{"row 34 replace an apostrophe", func(t *testing.T) Value {
			return chain(t, Str("О'Брайен"), "replace", []Value{regexArg("'", ""), Str("")})
		}, Str("ОБрайен")},
		{"row 43 upper after a chain", func(t *testing.T) Value {
			return chain(t, Str("world"), "upper", no)
		}, Str("WORLD")},
		{"row 45 has", func(t *testing.T) Value {
			return chain(t, Str("hello"), "has", []Value{Str("lo")})
		}, Bool(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(t)
			if !stdSame(got, tt.want) {
				t.Errorf("= %s; want %s", got.Inspect(), tt.want.Inspect())
			}
		})
	}
}
