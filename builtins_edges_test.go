package mzs

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// §12.1 is the table every kind answers, so its edges are the ones a one-liner reaches
// first: `is` with each type name, output that fails to write, an argument of the wrong
// type, and the two numeric answers — an empty sequence and an overflow — that have to
// be decided rather than left to Go.

// `is(x, name)` is the whole §7.2 vocabulary plus the two internal kinds a script can
// still see. A name that is not in the list is an argument error, not false, so a
// pasted `x.is("Integer")` fails loudly (§12.1).
func TestIsEveryTypeName(t *testing.T) {
	c := stdCtx(optsWithRand())
	re, err := c.Regex("а", "")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}
	fn := Fn("f", 0, func(c *Ctx, args []Value) (Value, error) { return Nil(), nil })
	when := timeOf(time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC))

	values := map[string]Value{
		"nil": Nil(), "bool": Bool(true), "int": Int(1), "float": Float(1.5),
		"string": Str("да"), "regex": re, "array": Array(Int(1)),
		"dict": Dict(Str("k"), Int(1)), "function": fn, "time": when,
	}
	names := []string{"nil", "bool", "int", "float", "string", "regex", "array",
		"dict", "function", "range", "time"}

	for name, v := range values {
		t.Run(name, func(t *testing.T) {
			// Its own name answers true…
			got, err := stdBuiltin(t, c, "is", v, Str(name))
			if err != nil {
				t.Fatalf("is(%s, %q) error = %v", v.Inspect(), name, err)
			}
			if !got.Bool() {
				t.Errorf("is(%s, %q) = false; want true", v.Inspect(), name)
			}
			// …and every other name answers false, except that a range is also an
			// array (§12.10), which is the one overlap in the table.
			for _, other := range names {
				if other == name {
					continue
				}
				got, err := stdBuiltin(t, c, "is", v, Str(other))
				if err != nil {
					t.Fatalf("is(%s, %q) error = %v", v.Inspect(), other, err)
				}
				if got.Bool() {
					t.Errorf("is(%s, %q) = true; only %q should answer", v.Inspect(), other, name)
				}
			}
		})
	}

	t.Run("a range is an array and a range", func(t *testing.T) {
		r := rangeOf(1, 3, false)
		for _, name := range []string{"array", "range"} {
			got, err := stdBuiltin(t, c, "is", r, Str(name))
			if err != nil || !got.Bool() {
				t.Errorf("is(1..3, %q) = %s, %v; want true", name, got.Inspect(), err)
			}
		}
		got, _ := stdBuiltin(t, c, "type", r)
		if got.Str() != "range" {
			t.Errorf(`type(1..3) = %q; want "range"`, got.Str())
		}
	})

	t.Run("an unknown name is an argument error", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "is", Int(1), Str("Integer")); err == nil {
			t.Error(`is(1, "Integer") was accepted; a name outside §7.2 is an error`)
		}
	})
	t.Run("the name must be a string", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "is", Int(1), Int(1)); err == nil {
			t.Error("is(1, 1) was accepted; want a type error")
		}
	})
}

// failingWriter is a host Stdout that refuses everything, which is what a closed pipe
// looks like: `mzs -e 'println(x)' | head -1`.
type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) { return 0, w.err }

// Output goes through Options.Stdout, and a write that fails has to become a script
// error rather than being dropped: the program is otherwise told its output arrived.
func TestOutputReportsAFailingWriter(t *testing.T) {
	boom := errors.New("pipe closed")
	opts := DefaultOptions()
	opts.Stdout = failingWriter{boom}
	c := stdCtx(opts)

	for _, tt := range []struct {
		fn   string
		args []Value
	}{
		{"print", []Value{Str("привет")}},
		{"println", []Value{Str("привет")}},
		{"println", []Value{Array(Str("а"), Str("б"))}}, // an array prints one per line
		{"println", nil}, // the bare newline
		{"debug", []Value{Int(1)}},
	} {
		t.Run(tt.fn, func(t *testing.T) {
			_, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err == nil {
				t.Fatalf("%s swallowed a write failure", tt.fn)
			}
			if !strings.Contains(err.Error(), "write failed") {
				t.Errorf("%s error = %v; want it to name the failed write", tt.fn, err)
			}
		})
	}

	t.Run("debug with no arguments writes nothing and returns nil", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "debug")
		if err != nil || !got.IsNil() {
			t.Errorf("debug() = %s, %v; want nil, nil", got.Inspect(), err)
		}
	})
}

// The conversions of §12.1 that have somewhere to fail.
func TestCoreBuiltinArgumentErrors(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		fn   string
		args []Value
	}{
		{"tap needs a function", "tap", []Value{Int(1), Int(2)}},
		{"pipe needs a function", "pipe", []Value{Int(1), Int(2)}},
		{"regex needs a string pattern", "regex", []Value{Int(1)}},
		{"regex needs string flags", "regex", []Value{Str("а"), Int(1)}},
		{"regex reports a pattern that will not compile", "regex", []Value{Str("(")}},
		{"sum needs a sequence", "sum", []Value{Int(1)}},
		{"sort needs a sequence", "sort", []Value{Int(1)}},
		{"sort needs a function comparator", "sort", []Value{Array(Int(1)), Int(2)}},
		{"abs needs a number", "abs", []Value{Str("1")}},
		{"round needs a number", "round", []Value{Str("1")}},
		{"round needs numeric digits", "round", []Value{Float(1.5), Str("2")}},
		{"ceil needs a number", "ceil", []Value{Str("1")}},
		{"floor needs a number", "floor", []Value{Str("1")}},
		{"min refuses values that do not compare", "min", []Value{Int(1), Str("а")}},
		{"max refuses values that do not compare", "max", []Value{Int(1), Str("а")}},
		{"dict needs pairs", "dict", []Value{Array(Int(1))}},
		{"dict needs a hashable key", "dict", []Value{Array(Array(Array(Int(1)), Int(2)))}},
		{"array of a huge range is capped", "array", []Value{rangeOf(1, 1<<40, false)}},
		{"format needs a layout string", "format", []Value{Int(1)}},
		{"assert needs a string message", "assert", []Value{Bool(false), Int(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err == nil {
				t.Fatalf("%s accepted %v; want an error", tt.fn, tt.args)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s returned %T (%v); want an *Error", tt.fn, err, err)
			}
		})
	}
}

// A closure that raises inside tap or pipe takes the whole call down with it, rather
// than the call returning the receiver as if nothing happened.
func TestTapAndPipePropagate(t *testing.T) {
	c := stdCtx(DefaultOptions())
	boom := Fn("boom", 1, func(c *Ctx, args []Value) (Value, error) {
		return Nil(), c.Errorf("boom")
	})

	for _, fn := range []string{"tap", "pipe"} {
		t.Run(fn, func(t *testing.T) {
			if _, err := stdBuiltin(t, c, fn, Int(1), boom); err == nil {
				t.Errorf("%s swallowed the error its closure raised", fn)
			}
		})
	}

	t.Run("tap returns the receiver, pipe the closure's value", func(t *testing.T) {
		double := Fn("double", 1, func(c *Ctx, args []Value) (Value, error) {
			return Int(args[0].Int() * 2), nil
		})
		got, err := stdBuiltin(t, c, "tap", Int(21), double)
		if err != nil || got.Int() != 21 {
			t.Errorf("tap = %s, %v; want 21", got.Inspect(), err)
		}
		got, err = stdBuiltin(t, c, "pipe", Int(21), double)
		if err != nil || got.Int() != 42 {
			t.Errorf("pipe = %s, %v; want 42", got.Inspect(), err)
		}
	})
}

// D9 reaches `sum` too: it stays in Int until an operand or an overflow forces Float,
// and an empty sequence is 0 rather than nil.
func TestSumPromotesOnOverflow(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		in   Value
		want Value
	}{
		{"empty", Array(), Int(0)},
		{"ints stay int", Array(Int(2), Int(3)), Int(5)},
		{"a float makes it float", Array(Int(2), Float(0.5)), Float(2.5)},
		{"positive overflow promotes", Array(Int(math.MaxInt64), Int(math.MaxInt64)),
			Float(2 * float64(math.MaxInt64))},
		{"negative overflow promotes", Array(Int(math.MinInt64), Int(math.MinInt64)),
			Float(2 * float64(math.MinInt64))},
		{"a range sums without materialising twice", rangeOf(1, 4, false), Int(10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdBuiltin(t, c, "sum", tt.in)
			if err != nil {
				t.Fatalf("sum error = %v", err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("sum(%s) = %s; want %s", tt.in.Inspect(), got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

// `rand` has three shapes and each answers differently (§12.1).
func TestRandShapes(t *testing.T) {
	c := stdCtx(optsWithRand())

	t.Run("no argument is a float in [0,1)", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "rand")
		if err != nil || got.Kind() != KFloat || got.Float() < 0 || got.Float() >= 1 {
			t.Errorf("rand() = %s, %v; want a float in [0,1)", got.Inspect(), err)
		}
	})
	t.Run("zero is the float shape too", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "rand", Int(0))
		if err != nil || got.Kind() != KFloat {
			t.Errorf("rand(0) = %s, %v; want a float", got.Inspect(), err)
		}
	})
	t.Run("a bound gives an int below it", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			got, err := stdBuiltin(t, c, "rand", Int(10))
			if err != nil || got.Kind() != KInt || got.Int() < 0 || got.Int() >= 10 {
				t.Fatalf("rand(10) = %s, %v; want an int in [0,10)", got.Inspect(), err)
			}
		}
	})
	t.Run("a negative bound is its magnitude", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "rand", Int(-10))
		if err != nil || got.Kind() != KInt || got.Int() < 0 || got.Int() >= 10 {
			t.Errorf("rand(-10) = %s, %v; want an int in [0,10)", got.Inspect(), err)
		}
	})
	t.Run("a non-numeric bound is an error", func(t *testing.T) {
		if _, err := stdBuiltin(t, c, "rand", Str("10")); err == nil {
			t.Error(`rand("10") was accepted; want a type error`)
		}
	})
	t.Run("uuid has the shape of a uuid", func(t *testing.T) {
		got, err := stdBuiltin(t, c, "uuid")
		if err != nil {
			t.Fatalf("uuid error = %v", err)
		}
		s := got.Str()
		if len(s) != 36 {
			t.Fatalf("uuid = %q; want 36 characters", s)
		}
		for i, r := range s {
			switch i {
			case 8, 13, 18, 23:
				if r != '-' {
					t.Errorf("uuid = %q; want '-' at %d", s, i)
				}
			default:
				if !strings.ContainsRune("0123456789abcdef", r) {
					t.Errorf("uuid = %q; %q at %d is not a hex digit", s, r, i)
				}
			}
		}
		// RFC 4122: the version nibble is 4 and the variant nibble is one of 8-b.
		if s[14] != '4' {
			t.Errorf("uuid = %q; want version 4 at index 14", s)
		}
		if !strings.ContainsRune("89ab", rune(s[19])) {
			t.Errorf("uuid = %q; want the variant nibble at 19 to be one of 8, 9, a, b", s)
		}
	})
}
