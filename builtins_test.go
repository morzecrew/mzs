package mzs

import (
	"bytes"
	"math/rand"
	"testing"
	"time"
)

// Everything in §12.1 is a **free function**. Under UFCS (D18) that is already both
// spellings — `len(x)` and `x.len` reach the same row — so these tests drive the builtin
// table directly, and only the rows §12.9 really gives a kind of its own (bool's
// `&`/`|`/`^`, a function's `call`/`arity`) go through LookupMethod.

func TestBuiltinConversions(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{"len of a Cyrillic string counts runes", "len", []Value{Str("привет")}, Int(6)},
		{"len of an array", "len", []Value{Array(Int(1), Int(2))}, Int(2)},
		{"len of a dict", "len", []Value{Dict(Str("a"), Int(1))}, Int(1)},
		{"len of a range", "len", []Value{rangeOf(1, 3, false)}, Int(3)},
		{"len of nil is zero", "len", []Value{Nil()}, Int(0)},
		{"empty of an empty array", "empty", []Value{Array()}, Bool(true)},
		{"empty of nil", "empty", []Value{Nil()}, Bool(true)},
		{"type of an int", "type", []Value{Int(1)}, Str("int")},
		{"type of a dict", "type", []Value{Dict()}, Str("dict")},
		{"type of a range", "type", []Value{rangeOf(1, 3, false)}, Str("range")},
		{"type of a function", "type", []Value{Fn("f", 0, nil)}, Str("function")},
		{"is names the kind", "is", []Value{Int(1), Str("int")}, Bool(true)},
		{"is on a mismatch", "is", []Value{Int(1), Str("string")}, Bool(false)},
		{"is treats a range as an array", "is", []Value{rangeOf(1, 3, false), Str("array")}, Bool(true)},
		{"is still distinguishes a range", "is", []Value{rangeOf(1, 3, false), Str("range")}, Bool(true)},
		{"int of a messy string", "int", []Value{Str("12abc")}, Int(12)},
		{"int of an empty string", "int", []Value{Str("")}, Int(0)},
		{"float", "float", []Value{Str("1.5")}, Float(1.5)},
		{"str of an int", "str", []Value{Int(1)}, Str("1")},
		{"str of a float keeps the dot", "str", []Value{Float(2)}, Str("2.0")},
		{"str of an array is JSON", "str", []Value{Array(Int(1), Int(2))}, Str("[1,2]")},
		{"bool of an empty string is true", "bool", []Value{Str("")}, Bool(true)},
		{"bool of zero is true", "bool", []Value{Int(0)}, Bool(true)},
		{"bool of nil is false", "bool", []Value{Nil()}, Bool(false)},
		{"array of nil is empty", "array", []Value{Nil()}, Array()},
		{"array of a scalar wraps it", "array", []Value{Int(1)}, Array(Int(1))},
		{"array of a range materialises", "array", []Value{rangeOf(1, 3, false)},
			Array(Int(1), Int(2), Int(3))},
		{"array of a dict is its pairs", "array", []Value{Dict(Str("a"), Int(1))},
			Array(Array(Str("a"), Int(1)))},
		{"dict from pairs", "dict", []Value{Array(Array(Str("a"), Int(1)))}, Dict(Str("a"), Int(1))},
		{"dict of a dict is itself", "dict", []Value{Dict(Str("a"), Int(1))}, Dict(Str("a"), Int(1))},
		{"json of a dict", "json", []Value{Dict(Str("a"), Int(1))}, Str(`{"a":1}`)},
		{"json of nil is null", "json", []Value{Nil()}, Str("null")},
		{"inspect quotes a string", "inspect", []Value{Str("a")}, Str(`"a"`)},
		{"inspect spells nil out", "inspect", []Value{Nil()}, Str("nil")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tt.fn, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s = %s; want %s", tt.fn, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	t.Run("is rejects an unknown type name", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "is", Int(1), Str("Integer")); err == nil {
			t.Errorf(`is(1, "Integer"): want an argument error, got none`)
		}
	})
	t.Run("dict refuses a value with no defensible key", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "dict", Str("a")); err == nil {
			t.Errorf(`dict("a"): want a type error, got none`)
		}
	})
}

func TestBuiltinNumeric(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{"sum of an array", "sum", []Value{Array(Int(1), Int(2))}, Int(3)},
		{"sum of an empty array is zero", "sum", []Value{Array()}, Int(0)},
		{"sum promotes on a float", "sum", []Value{Array(Int(1), Float(0.5))}, Float(1.5)},
		{"sum of a range", "sum", []Value{rangeOf(1, 4, false)}, Int(10)},
		{"min of varargs", "min", []Value{Int(3), Int(1), Int(2)}, Int(1)},
		{"max of an array", "max", []Value{Array(Int(3), Int(1), Int(2))}, Int(3)},
		{"min of strings is lexicographic", "min", []Value{Str("b"), Str("a")}, Str("a")},
		{"min of an empty array is nil", "min", []Value{Array()}, Nil()},
		{"abs", "abs", []Value{Int(-2)}, Int(2)},
		{"round to an int", "round", []Value{Float(1.5)}, Int(2)},
		{"round with digits", "round", []Value{Float(1.256), Int(2)}, Float(1.26)},
		{"ceil", "ceil", []Value{Float(1.2)}, Int(2)},
		{"floor", "floor", []Value{Float(1.8)}, Int(1)},
		{"range with one bound", "range", []Value{Int(3)}, Array(Int(0), Int(1), Int(2))},
		{"range with two bounds", "range", []Value{Int(0), Int(3)},
			Array(Int(0), Int(1), Int(2))},
		{"range with a step", "range", []Value{Int(0), Int(6), Int(2)},
			Array(Int(0), Int(2), Int(4))},
		{"a descending range", "range", []Value{Int(3), Int(0), Int(-1)},
			Array(Int(3), Int(2), Int(1))},
		{"an empty range", "range", []Value{Int(0)}, Array()},
		{"sort", "sort", []Value{Array(Int(3), Int(1), Int(2))},
			Array(Int(1), Int(2), Int(3))},
		{"sort strings", "sort", []Value{Array(Str("b"), Str("a"))},
			Array(Str("a"), Str("b"))},
		{"format", "format", []Value{Str("%.2f"), Float(1.256)}, Str("1.26")},
		{"format with a Cyrillic argument", "format", []Value{Str("%s!"), Str("да")}, Str("да!")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tt.fn, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s = %s; want %s", tt.fn, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	// §12.1: the closure `sort` takes is a comparator returning a `<=>`-style int, not a
	// key extractor. One name, one meaning (D17).
	t.Run("sort with a comparator closure", func(t *testing.T) {
		block := stdBlock(func(args []Value) (Value, error) {
			return Int(args[1].Int() - args[0].Int()), nil
		})
		got, err := stdBuiltin(t, c, "sort", Array(Int(1), Int(3), Int(2)), block)
		if err != nil {
			t.Fatalf("sort: %v", err)
		}
		if !stdSame(got, Array(Int(3), Int(2), Int(1))) {
			t.Errorf("sort with a descending comparator = %s; want [3,2,1]", got.Inspect())
		}
	})

	t.Run("sum takes a sequence, not varargs", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "sum", Int(1), Int(2)); err == nil {
			t.Errorf("sum(1, 2): want an argument error, got none")
		}
	})
	t.Run("range with a zero step is an error", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "range", Int(0), Int(3), Int(0)); err == nil {
			t.Errorf("range(0,3,0): want an error, got none")
		}
	})
}

// print/say/debug write to Options.Stdout, never to the process stdout: a script running
// inside a bot has no console, and the host must be able to capture the output.
func TestBuiltinOutput(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want string
	}{
		{"print has no separator and no newline", "print",
			[]Value{Str("a"), Int(1)}, "a1"},
		{"print of a Cyrillic value", "print", []Value{Str("привет")}, "привет"},
		{"say appends a newline per argument", "say",
			[]Value{Str("a"), Str("b")}, "a\nb\n"},
		{"say of an array prints one element per line", "say",
			[]Value{Array(Int(1), Int(2))}, "1\n2\n"},
		{"say of a range prints its str form", "say",
			[]Value{rangeOf(1, 3, false)}, "1..3\n"},
		{"say with no arguments writes one newline", "say", nil, "\n"},
		{"debug writes the inspect form", "debug", []Value{Str("a")}, "\"a\"\n"},
		{"debug of nil spells it out", "debug", []Value{Nil()}, "nil\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := DefaultOptions()
			opts.Stdout = &buf
			c := stdCtx(opts)
			if _, err := stdBuiltin(t, c, tt.fn, tt.args...); err != nil {
				t.Fatalf("%s: unexpected error %v", tt.fn, err)
			}
			if buf.String() != tt.want {
				t.Errorf("%s wrote %q; want %q", tt.fn, buf.String(), tt.want)
			}
		})
	}

	t.Run("debug returns its first argument", func(t *testing.T) {
		c := stdCtx(DefaultOptions())
		got, err := stdBuiltin(t, c, "debug", Int(7), Int(8))
		if err != nil {
			t.Fatalf("debug: %v", err)
		}
		if !stdSame(got, Int(7)) {
			t.Errorf("debug(7, 8) = %s; want 7", got.Inspect())
		}
	})

	t.Run("output goes nowhere when the host installed no sink", func(t *testing.T) {
		c := stdCtx(DefaultOptions())
		if _, err := stdBuiltin(t, c, "print", Str("x")); err != nil {
			t.Errorf("print with no Stdout: unexpected error %v", err)
		}
	})
}

func TestBuiltinRaiseAndAssert(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name    string
		fn      string
		args    []Value
		wantErr bool
		wantMsg string
	}{
		{"raise with a message", "raise", []Value{Str("bad")}, true, "bad"},
		{"raise with a dict takes its message key", "raise",
			[]Value{Dict(Str("message"), Str("boom"))}, true, "boom"},
		{"assert on a truthy value passes", "assert", []Value{Int(0)}, false, ""},
		{"assert on nil raises", "assert", []Value{Nil()}, true, "assertion failed"},
		{"assert with a custom message", "assert",
			[]Value{Bool(false), Str("nope")}, true, "nope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s: err = %v; want an error = %v", tt.fn, err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			e := asError(err)
			if e.Msg != tt.wantMsg {
				t.Errorf("%s message = %q; want %q", tt.fn, e.Msg, tt.wantMsg)
			}
			if !e.Catchable() {
				t.Errorf("%s produced an uncatchable error; `try … else` must be able to see it", tt.fn)
			}
		})
	}

	t.Run("raise carries its dict as data", func(t *testing.T) {
		payload := Dict(Str("code"), Int(42))
		_, err := stdBuiltin(t, c, "raise", payload)
		e := asError(err)
		if !e.Data.Equal(payload) {
			t.Errorf("raise data = %s; want %s", e.Data.Inspect(), payload.Inspect())
		}
	})
}

// Capabilities are declarative: a builtin the host did not enable must be invisible,
// not merely inert (§14.3, D15).
func TestBuiltinCapabilityGates(t *testing.T) {
	sandboxed := DefaultOptions()
	capable := DefaultOptions()
	capable.Rand = rand.New(rand.NewSource(1))
	capable.Now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	tests := []struct {
		name  string
		fn    string
		opts  Options
		avail bool
	}{
		{"rand is hidden without a source", "rand", sandboxed, false},
		{"rand appears with a source", "rand", capable, true},
		{"uuid is hidden without a source", "uuid", sandboxed, false},
		{"uuid appears with a source", "uuid", capable, true},
		{"now is hidden without a clock", "now", sandboxed, false},
		{"now appears with a clock", "now", capable, true},
		{"print is always available", "print", sandboxed, true},
		{"json is always available", "json", sandboxed, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, ok := LookupBuiltin(tt.fn)
			if !ok {
				t.Fatalf("builtin %q is not registered", tt.fn)
			}
			o := tt.opts.normalize()
			if got := b.Available(&o); got != tt.avail {
				t.Errorf("%s available = %v; want %v", tt.fn, got, tt.avail)
			}
		})
	}

	t.Run("rand and uuid are deterministic under a seeded source", func(t *testing.T) {
		c := stdCtx(capable)
		v, err := stdBuiltin(t, c, "rand", Int(10))
		if err != nil {
			t.Fatalf("rand: %v", err)
		}
		if v.Kind() != KInt || v.Int() < 0 || v.Int() >= 10 {
			t.Errorf("rand(10) = %s; want an int in [0,10)", v.Inspect())
		}
		u, err := stdBuiltin(t, c, "uuid")
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		s := u.Str()
		if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[14] != '4' || s[18] != '-' || s[23] != '-' {
			t.Errorf("uuid = %q; want the version-4 shape", s)
		}
	})

	t.Run("now returns the host clock", func(t *testing.T) {
		c := stdCtx(capable)
		v, err := stdBuiltin(t, c, "now")
		if err != nil {
			t.Fatalf("now: %v", err)
		}
		if v.Kind() != KTime || v.Time().Unix() != 1700000000 {
			t.Errorf("now = %s; want the injected clock", v.Inspect())
		}
	})
}

// §12.9: nil, bool and function receivers. The nil and bool conversions are the same
// free functions as everywhere else — no kind gets a private copy of `str` (D17).
func TestNilBoolAndFunctionReceivers(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{"nil str is empty", "str", []Value{Nil()}, Str("")},
		{"nil int is zero", "int", []Value{Nil()}, Int(0)},
		{"nil float is zero", "float", []Value{Nil()}, Float(0)},
		{"nil array is empty", "array", []Value{Nil()}, Array()},
		{"nil inspect spells it out", "inspect", []Value{Nil()}, Str("nil")},
		{"nil json is null", "json", []Value{Nil()}, Str("null")},
		{"bool str", "str", []Value{Bool(true)}, Str("true")},
		{"bool int", "int", []Value{Bool(true)}, Int(1)},
		{"bool json", "json", []Value{Bool(false)}, Str("false")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tt.fn, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s = %s; want %s", tt.fn, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	// §12.9: `&&`, `||` and `!` are the whole set. A row named `&` on bool would be a
	// second spelling of `&&` reachable only from Go — `&` is not a lexeme (§3.9) and
	// `.` is followed by an identifier — which is what D17 forbids. It is also the
	// reason the bit operations of §12.5 are functions rather than operators.
	for _, name := range []string{"&", "|", "^"} {
		t.Run("bool does not answer "+name, func(t *testing.T) {
			if HasMethod(KBool, name) {
				t.Errorf("bool answers %q; §12.9 leaves only && || !", name)
			}
		})
	}

	t.Run("a function value can be called", func(t *testing.T) {
		fn := Fn("double", 1, func(c *Ctx, args []Value) (Value, error) {
			return Int(args[0].Int() * 2), nil
		})
		got, err := stdCall(t, c, fn, "call", Int(21))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !stdSame(got, Int(42)) {
			t.Errorf("fn.call(21) = %s; want 42", got.Inspect())
		}
		arity, err := stdCall(t, c, fn, "arity")
		if err != nil || !stdSame(arity, Int(1)) {
			t.Errorf("fn.arity = %s, %v; want 1", arity.Inspect(), err)
		}
	})
}

func TestBuiltinDupTapAndPipe(t *testing.T) {
	c := stdCtx(DefaultOptions())

	t.Run("dup copies an array", func(t *testing.T) {
		src := Array(Int(1))
		got, err := stdBuiltin(t, c, "dup", src)
		if err != nil {
			t.Fatalf("dup: %v", err)
		}
		got.Append(Int(2))
		if src.Len() != 1 {
			t.Errorf("dup aliased the receiver: src is now %s", src.Inspect())
		}
	})

	t.Run("dup is shallow", func(t *testing.T) {
		inner := Array(Int(1))
		got, err := stdBuiltin(t, c, "dup", Array(inner))
		if err != nil {
			t.Fatalf("dup: %v", err)
		}
		got.Index(0).Append(Int(2))
		if inner.Len() != 2 {
			t.Errorf("dup deep-copied a nested array; §12.1 makes it shallow")
		}
	})

	t.Run("tap runs the closure and returns the receiver", func(t *testing.T) {
		seen := Nil()
		block := stdBlock(func(args []Value) (Value, error) { seen = args[0]; return Int(99), nil })
		got, err := stdBuiltin(t, c, "tap", Str("x"), block)
		if err != nil {
			t.Fatalf("tap: %v", err)
		}
		if !stdSame(got, Str("x")) || !stdSame(seen, Str("x")) {
			t.Errorf("tap = %s (saw %s); want the receiver both times", got.Inspect(), seen.Inspect())
		}
	})

	t.Run("pipe returns the closure's value", func(t *testing.T) {
		block := stdBlock(func(args []Value) (Value, error) { return Int(99), nil })
		got, err := stdBuiltin(t, c, "pipe", Str("x"), block)
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		if !stdSame(got, Int(99)) {
			t.Errorf("pipe = %s; want 99", got.Inspect())
		}
	})

	t.Run("hash is stable and discriminating", func(t *testing.T) {
		a, err := stdBuiltin(t, c, "hash", Str("привет"))
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		b, _ := stdBuiltin(t, c, "hash", Str("привет"))
		d, _ := stdBuiltin(t, c, "hash", Str("пока"))
		if !stdSame(a, b) {
			t.Errorf("hash is not stable: %s vs %s", a.Inspect(), b.Inspect())
		}
		if stdSame(a, d) {
			t.Errorf("hash collides on two different strings")
		}
	})

	t.Run("regex compiles at runtime", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "regex", Str(`\bменю`), Str("i"))
		if err != nil {
			t.Fatalf("regex: %v", err)
		}
		if got.Kind() != KRegex || got.Str() != `/\bменю/i` {
			t.Errorf("regex = %s; want /\\bменю/i", got.Inspect())
		}
		if _, err := stdBuiltin(t, c, "regex", Str("(")); err == nil {
			t.Errorf("regex(\"(\"): want a compile error, got none")
		}
	})
}

// §12.1 is the complete list of global functions, and D17 forbids a second spelling of
// any of them. The `defined` row is a parser-level special form and is registered so the
// lookup and the §17 suggestions find it.
func TestBuiltinRoster(t *testing.T) {
	present := []string{
		"print", "say", "debug", "len", "empty", "type", "is", "str", "int", "float",
		"bool", "array", "dict", "json", "inspect", "hash", "dup", "tap", "pipe",
		"regex", "range", "sum", "min", "max", "abs", "round", "ceil", "floor", "sort",
		"format", "raise", "assert", "defined", "rand", "uuid", "now",
	}
	for _, name := range present {
		t.Run(name, func(t *testing.T) {
			if !HasBuiltin(name) {
				t.Errorf("§12.1 lists %q; it is not registered", name)
			}
		})
	}

	absent := []struct {
		old, use string
	}{
		{"puts", "say"},
		{"p", "debug"},
		{"sprintf", "format"},
		{"printf", "print(format(…))"},
		{"eval", "—"},
		{"lambda", "{ (x) -> … }"},
		{"proc", "{ (x) -> … }"},
		{"size", "len"},
		{"length", "len"},
		{"to_s", "str"},
		{"to_i", "int"},
		{"to_json", "json"},
		{"generate", "json"},
		{"nil?", "x == nil"},
		{"is_a", "is"},
		{"respond_to", "—"},
		{"class", "type"},
		{"freeze", "—"},
		{"then", "pipe"},
	}
	for _, tt := range absent {
		t.Run("no "+tt.old, func(t *testing.T) {
			if HasBuiltin(tt.old) {
				t.Errorf("%q is a builtin; D17 allows only %q", tt.old, tt.use)
			}
			if HasMethodAnyKind(tt.old) {
				t.Errorf("%q is a method of some kind; D17 allows only %q", tt.old, tt.use)
			}
		})
	}
}
