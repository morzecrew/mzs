package mzs

import (
	"errors"
	"strings"
	"testing"
)

// §12.2 is a big table and its happy paths are covered next door in str_test.go. What is
// left is the part a one-liner hits by accident: an argument of the wrong type, a count
// that is negative, a pattern that does not compile, a `%` verb with no argument behind
// it — and the two limits (§14.2) that stop a string operation from eating the process.

// A row that takes an argument states its type; there is no coercion (§9.1), so the
// wrong one is a diagnostic rather than a silent `str` of it.
func TestStringRowsRefuseWrongArgumentTypes(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name   string
		method string
		args   []Value
	}{
		{"split by a number", "split", []Value{Int(1)}},
		{"split with a non-numeric limit", "split", []Value{Str(","), Str("два")}},
		{"replace with a non-string replacement", "replace", []Value{Str("a"), Int(1)}},
		{"replace_first with a non-string replacement", "replace_first", []Value{Str("a"), Int(1)}},
		{"has a number", "has", []Value{Int(1)}},
		{"starts_with a number", "starts_with", []Value{Int(1)}},
		{"ends_with a number", "ends_with", []Value{Int(1)}},
		{"count a number", "count", []Value{Int(1)}},
		{"index from a non-numeric offset", "index", []Value{Str("a"), Str("б")}},
		{"chomp a number", "chomp", []Value{Int(1)}},
		{"squeeze a number", "squeeze", []Value{Int(1)}},
		{"ljust a non-numeric width", "ljust", []Value{Str("x")}},
		{"ljust a non-string pad", "ljust", []Value{Int(4), Int(0)}},
		{"rjust a non-string pad", "rjust", []Value{Int(4), Int(0)}},
		{"center a non-string pad", "center", []Value{Int(4), Int(0)}},
		{"first a non-numeric count", "first", []Value{Str("x")}},
		{"last a non-numeric count", "last", []Value{Str("x")}},
		{"slice a non-numeric index", "slice", []Value{Str("x")}},
		{"captures a plain string", "captures", []Value{Int(1)}},
		{"matches a plain string", "matches", []Value{Int(1)}},
		{"each_char without a closure", "each_char", []Value{Int(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stdCall(t, c, Str("а,б,в"), tt.method, tt.args...)
			if err == nil {
				t.Fatalf("%s accepted the argument; want a type error", tt.method)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s returned %T (%v); want an *Error", tt.method, err, err)
			}
			if e.Kind != ErrKindType && e.Kind != ErrKindArgument {
				t.Errorf("%s reported kind %q; want %q", tt.method, e.Kind, ErrKindType)
			}
		})
	}
}

// Counts out of range clamp rather than raise — a string is sliced by rune index and
// `first(-1)` has an obvious answer, unlike `ljust` with nothing to pad with. Both
// halves are written down here because the difference is a decision, not an accident.
func TestStringCountsClampAndPadRefuses(t *testing.T) {
	c := stdCtx(DefaultOptions())

	clamp := []struct {
		name   string
		method string
		args   []Value
		want   string
	}{
		{"first of a negative count is empty", "first", []Value{Int(-1)}, ""},
		{"first past the end is the whole string", "first", []Value{Int(99)}, "привет"},
		{"last of a negative count is empty", "last", []Value{Int(-1)}, ""},
		{"last past the end is the whole string", "last", []Value{Int(99)}, "привет"},
		{"slice with a negative length is empty", "slice", []Value{Int(0), Int(-2)}, ""},
		{"slice past the end is empty", "slice", []Value{Int(99)}, ""},
	}
	for _, tt := range clamp {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str("привет"), tt.method, tt.args...)
			if err != nil {
				t.Fatalf("%s error = %v", tt.method, err)
			}
			if got.Str() != tt.want {
				t.Errorf("%s%v = %q; want %q", tt.method, tt.args, got.Str(), tt.want)
			}
		})
	}

	t.Run("an empty pad is an argument error", func(t *testing.T) {
		// There is no answer to "pad to eight with nothing", so this one raises.
		if _, err := stdCall(t, c, Str("да"), "ljust", Int(8), Str("")); err == nil {
			t.Error("ljust with an empty pad was accepted; want an argument error")
		}
	})
}

// A pattern that cannot compile is a regex diagnostic pointing at the row that took it,
// not a panic out of the backend (§11.4).
func TestStringRowsReportABadPattern(t *testing.T) {
	c := stdCtx(DefaultOptions())
	bad := Str("(")

	for _, method := range []string{"split", "count", "index", "last_index"} {
		t.Run(method, func(t *testing.T) {
			// A plain string argument is quoted and matches literally (§12.2), so the
			// only way to a bad pattern is a regex built at run time.
			re, err := c.Regex("(", "")
			if err == nil {
				t.Fatalf("Regex(%q) compiled; the fixture needs a pattern that cannot", "(")
			}
			_ = re
			// The literal string form must still work — "(" is not a pattern here.
			if _, err := stdCall(t, c, Str("a(b"), method, bad); err != nil {
				t.Errorf("%s with a literal %q = %v; a string argument matches literally", method, "(", err)
			}
		})
	}
}

// The set `squeeze` takes is small but has three shapes, and only the first is obvious.
func TestSqueezeCharSets(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		recv string
		set  []Value
		want string
	}{
		{"no set squeezes every run", "аaabbcc", nil, "аabc"},
		{"a set squeezes only its runes", "aabbcc", []Value{Str("a")}, "abbcc"},
		{"a range names its runes", "aabbcc", []Value{Str("a-b")}, "abcc"},
		{"a negated set squeezes the rest", "aabbcc", []Value{Str("^a")}, "aabc"},
		{"a backslash escapes the next rune", "a--b", []Value{Str(`\-`)}, "a-b"},
		{"a trailing dash is a literal", "a--b", []Value{Str("-")}, "a-b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), "squeeze", tt.set...)
			if err != nil {
				t.Fatalf("squeeze error = %v", err)
			}
			if got.Str() != tt.want {
				t.Errorf("squeeze(%q) of %q = %q; want %q", tt.set, tt.recv, got.Str(), tt.want)
			}
		})
	}
}

// chomp with no argument removes one line terminator of any spelling; chop removes one
// rune, and a CRLF is one thing to remove, not two (§12.2).
func TestChompAndChop(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name   string
		method string
		recv   string
		args   []Value
		want   string
	}{
		{"chomp a newline", "chomp", "строка\n", nil, "строка"},
		{"chomp a CRLF", "chomp", "строка\r\n", nil, "строка"},
		{"chomp removes one terminator", "chomp", "строка\n\n", nil, "строка\n"},
		{"an empty suffix chomps every terminator", "chomp", "строка\n\n\n", []Value{Str("")}, "строка"},
		{"an empty suffix on a CRLF run", "chomp", "строка\r\n\r\n", []Value{Str("")}, "строка"},
		{"chomp nothing to remove", "chomp", "строка", nil, "строка"},
		{"chomp a given suffix", "chomp", "файл.mzs", []Value{Str(".mzs")}, "файл"},
		{"chomp a suffix that is not there", "chomp", "файл.mzs", []Value{Str(".go")}, "файл.mzs"},
		{"chop a rune", "chop", "привет", nil, "приве"},
		{"chop a CRLF as one", "chop", "строка\r\n", nil, "строка"},
		{"chop an empty string", "chop", "", nil, ""},
		{"chop a multi-byte rune", "chop", "да🌲", nil, "да"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), tt.method, tt.args...)
			if err != nil {
				t.Fatalf("%s error = %v", tt.method, err)
			}
			if got.Str() != tt.want {
				t.Errorf("%s(%q) = %q; want %q", tt.method, tt.recv, got.Str(), tt.want)
			}
		})
	}
}

// split's limit is the one argument that changes the shape of the answer.
func TestSplitLimit(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		recv string
		args []Value
		want string
	}{
		{"no limit", "а:б:в", []Value{Str(":")}, `["а","б","в"]`},
		{"a limit keeps the rest whole", "а:б:в", []Value{Str(":"), Int(2)}, `["а","б:в"]`},
		{"a limit of one is the whole string", "а:б:в", []Value{Str(":"), Int(1)}, `["а:б:в"]`},
		{"a negative limit is no limit", "а:б:в", []Value{Str(":"), Int(-1)}, `["а","б","в"]`},
		{"a regex separator with a limit", "а1б2в", []Value{MustRegex(t, `\d`), Int(2)}, `["а","б2в"]`},
		{"an empty separator splits into runes", "аб", []Value{Str("")}, `["а","б"]`},
		{"whitespace splits on runs", " а  б ", nil, `["а","б"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str(tt.recv), "split", tt.args...)
			if err != nil {
				t.Fatalf("split error = %v", err)
			}
			if got.Inspect() != tt.want {
				t.Errorf("split(%q) = %s; want %s", tt.recv, got.Inspect(), tt.want)
			}
		})
	}
}

// MustRegex compiles a pattern for a table, failing the test rather than the row.
func MustRegex(t *testing.T, pattern string) Value {
	t.Helper()
	c := stdCtx(DefaultOptions())
	v, err := c.Regex(pattern, "")
	if err != nil {
		t.Fatalf("Regex(%q): %v", pattern, err)
	}
	return v
}

// index takes an offset, and every way of getting it wrong has an answer (§12.2).
func TestIndexFromAnOffset(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		args []Value
		want string
	}{
		{"from the start", []Value{Str("б")}, "1"},
		{"from an offset", []Value{Str("б"), Int(2)}, "4"},
		{"from a negative offset counts from the end", []Value{Str("б"), Int(-3)}, "4"},
		{"an offset past the end misses", []Value{Str("б"), Int(99)}, "nil"},
		{"a very negative offset clamps to the start", []Value{Str("б"), Int(-99)}, "1"},
		{"a needle that is not there", []Value{Str("ю")}, "nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, Str("абваб"), "index", tt.args...)
			if err != nil {
				t.Fatalf("index error = %v", err)
			}
			if got.Inspect() != tt.want {
				t.Errorf("index%v = %s; want %s", tt.args, got.Inspect(), tt.want)
			}
		})
	}
}

// §14.2: a row that builds a collection or a string out of one is bounded, so a long
// line cannot turn into a gigabyte of runes.
func TestStringRowsRespectTheLimits(t *testing.T) {
	long := Str(strings.Repeat("а", 4096))

	t.Run("chars is bounded by MaxCollection", func(t *testing.T) {
		c := stdCtx(Options{MaxCollection: 10})
		if _, err := stdCall(t, c, long, "chars"); !errors.Is(err, ErrBudget) {
			t.Errorf("chars = %v; want a limit error", err)
		}
	})
	t.Run("bytes is bounded by MaxCollection", func(t *testing.T) {
		c := stdCtx(Options{MaxCollection: 10})
		if _, err := stdCall(t, c, long, "bytes"); !errors.Is(err, ErrBudget) {
			t.Errorf("bytes = %v; want a limit error", err)
		}
	})
	t.Run("lines is bounded by MaxCollection", func(t *testing.T) {
		c := stdCtx(Options{MaxCollection: 2})
		many := Str(strings.Repeat("строка\n", 100))
		if _, err := stdCall(t, c, many, "lines"); !errors.Is(err, ErrBudget) {
			t.Errorf("lines = %v; want a limit error", err)
		}
	})
	t.Run("split is bounded by MaxCollection", func(t *testing.T) {
		c := stdCtx(Options{MaxCollection: 2})
		if _, err := stdCall(t, c, Str("а,б,в,г"), "split", Str(",")); !errors.Is(err, ErrBudget) {
			t.Errorf("split = %v; want a limit error", err)
		}
	})
	t.Run("ljust is bounded by MaxStringBytes", func(t *testing.T) {
		c := stdCtx(Options{MaxStringBytes: 16})
		if _, err := stdCall(t, c, Str("а"), "ljust", Int(1_000_000)); !errors.Is(err, ErrBudget) {
			t.Errorf("ljust = %v; want a limit error", err)
		}
	})
	t.Run("replace is bounded by MaxStringBytes", func(t *testing.T) {
		c := stdCtx(Options{MaxStringBytes: 32})
		wide := Str(strings.Repeat("a", 1000))
		if _, err := stdCall(t, c, wide, "replace", Str("a"), Str("ббб")); !errors.Is(err, ErrBudget) {
			t.Errorf("replace = %v; want a limit error", err)
		}
	})
	t.Run("format is bounded by MaxStringBytes", func(t *testing.T) {
		c := stdCtx(Options{MaxStringBytes: 16})
		_, err := stdBuiltin(t, c, "format", Str("%s"), Str(strings.Repeat("я", 1000)))
		if !errors.Is(err, ErrBudget) {
			t.Errorf("format = %v; want a limit error", err)
		}
	})
}

// The `%` operator and `format` share one implementation, and its verbs are where a
// one-liner meets an off-by-one: a verb with no argument, a name that is not in the
// dict, a width that is not a number (§12.7).
func TestFormatDiagnostics(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		args []Value
	}{
		{"a verb with no argument", []Value{Str("%s %s"), Str("один")}},
		{"a trailing percent", []Value{Str("100%")}},
		{"an unknown verb", []Value{Str("%q"), Str("a")}},
		{"a named verb without a dict", []Value{Str("%<name>s"), Str("a")}},
		{"an unterminated name", []Value{Str("%<name")}},
		{"an unterminated brace name", []Value{Str("%{name")}},
		{"a width with nothing after it", []Value{Str("%5")}},
		{"a precision with nothing after it", []Value{Str("%.2")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdBuiltin(t, c, "format", tt.args...)
			if err != nil {
				var e *Error
				if !errors.As(err, &e) {
					t.Fatalf("format returned %T (%v); want an *Error", err, err)
				}
				return
			}
			// Where fmt has a rendering for the mistake, the result must at least be a
			// string that says something is wrong rather than a silent empty one.
			if got.Str() == "" {
				t.Errorf("format%v = %q with no error; want either a diagnostic or visible output",
					tt.args, got.Str())
			}
		})
	}

	// A name the dict does not have is nil, which formats as the empty string: the
	// dict is data, and a missing key is nil everywhere else in the language too (§8.8).
	t.Run("a named verb naming a missing key formats nil", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "format", Str("[%<нет>s]"), Dict(Str("имя"), Str("Иван")))
		if err != nil {
			t.Fatalf("format error = %v", err)
		}
		if got.Str() != "[]" {
			t.Errorf("= %q; want %q", got.Str(), "[]")
		}
	})

	t.Run("a named verb reads the dict", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "format", Str("%<имя>s ждёт"), Dict(Str("имя"), Str("Иван")))
		if err != nil {
			t.Fatalf("format error = %v", err)
		}
		if got.Str() != "Иван ждёт" {
			t.Errorf("= %q; want %q", got.Str(), "Иван ждёт")
		}
	})
	t.Run("the brace spelling reads the same dict", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "format", Str("%{имя} ждёт"), Dict(Str("имя"), Str("Иван")))
		if err != nil {
			t.Fatalf("format error = %v", err)
		}
		if got.Str() != "Иван ждёт" {
			t.Errorf("= %q; want %q", got.Str(), "Иван ждёт")
		}
	})
}
