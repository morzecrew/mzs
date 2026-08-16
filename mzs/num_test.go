package mzs

import (
	"errors"
	"math"
	"testing"
)

func TestNumberConversions(t *testing.T) {
	tests := []struct {
		name string
		recv Value
		meth string
		args []Value
		want Value
	}{
		{"int of an int", Int(42), "int", nil, Int(42)},
		{"int of a float truncates toward zero", Float(-1.9), "int", nil, Int(-1)},
		{"float of an int", Int(3), "float", nil, Float(3)},
		{"str of an int", Int(42), "str", nil, Str("42")},
		{"str in base 2", Int(5), "str", []Value{Int(2)}, Str("101")},
		{"str in base 16", Int(255), "str", []Value{Int(16)}, Str("ff")},
		{"str of a float always carries a dot", Float(2), "str", nil, Str("2.0")},
		{"str of a float round trips", Float(1.5), "str", nil, Str("1.5")},
		{"json of an int", Int(7), "json", nil, Str("7")},
		{"chr of a Cyrillic codepoint", Int(0x44f), "chr", nil, Str("я")},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, tt.recv, tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%s.%s: unexpected error %v", tt.recv.Inspect(), tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s.%s = %s; want %s", tt.recv.Inspect(), tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	t.Run("a base needs an int receiver", func(t *testing.T) {
		if _, err := stdCall(t, c, Float(1.5), "str", Int(2)); err == nil {
			t.Errorf("1.5.str(2): want an argument error, got none")
		}
	})
	t.Run("an out-of-range base is an error", func(t *testing.T) {
		if _, err := stdCall(t, c, Int(5), "str", Int(1)); err == nil {
			t.Errorf("5.str(1): want an argument error, got none")
		}
	})
	t.Run("chr outside the Unicode range is an error", func(t *testing.T) {
		if _, err := stdCall(t, c, Int(0x110000), "chr"); err == nil {
			t.Errorf("chr: want an argument error, got none")
		}
	})
}

// Rounding keeps D9's int/float split visible: digits <= 0 yields an Int, digits > 0
// a Float, and the half case goes away from zero.
func TestNumberRounding(t *testing.T) {
	tests := []struct {
		name string
		recv Value
		meth string
		args []Value
		want Value
	}{
		{"round to an int", Float(1.5), "round", nil, Int(2)},
		{"round half away from zero", Float(-1.5), "round", nil, Int(-2)},
		{"round to two digits", Float(1.256), "round", []Value{Int(2)}, Float(1.26)},
		{"round the awkward decimal", Float(2.675), "round", []Value{Int(2)}, Float(2.68)},
		{"round an int is itself", Int(7), "round", nil, Int(7)},
		{"round an int to tens", Int(1234), "round", []Value{Int(-2)}, Int(1200)},
		{"round an int up to tens", Int(1250), "round", []Value{Int(-2)}, Int(1300)},
		{"round a float to tens", Float(2549.6), "round", []Value{Int(-2)}, Int(2500)},
		{"ceil an int to tens is exact", Int(2500), "ceil", []Value{Int(-2)}, Int(2500)},
		{"ceil an int up to tens", Int(2501), "ceil", []Value{Int(-2)}, Int(2600)},
		{"floor an int to tens", Int(2599), "floor", []Value{Int(-2)}, Int(2500)},
		{"ceil", Float(1.2), "ceil", nil, Int(2)},
		{"ceil with digits", Float(1.231), "ceil", []Value{Int(2)}, Float(1.24)},
		{"floor", Float(1.8), "floor", nil, Int(1)},
		{"floor of a negative", Float(-1.2), "floor", nil, Int(-2)},
		{"floor with digits", Float(1.239), "floor", []Value{Int(2)}, Float(1.23)},
		{"abs of an int stays an int", Int(-2), "abs", nil, Int(2)},
		{"abs of a float stays a float", Float(-2.5), "abs", nil, Float(2.5)},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, tt.recv, tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%s.%s: unexpected error %v", tt.recv.Inspect(), tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s.%s(%v) = %s; want %s", tt.recv.Inspect(), tt.meth, tt.args,
					got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

func TestNumberPredicatesAndArithmetic(t *testing.T) {
	tests := []struct {
		name string
		recv Value
		meth string
		args []Value
		want Value
	}{
		{"zero", Int(0), "zero", nil, Bool(true)},
		{"zero on a float", Float(0), "zero", nil, Bool(true)},
		{"positive", Int(1), "positive", nil, Bool(true)},
		{"negative", Float(-0.5), "negative", nil, Bool(true)},
		{"even", Int(4), "even", nil, Bool(true)},
		{"odd", Int(4), "odd", nil, Bool(false)},
		{"clamp below", Int(0), "clamp", []Value{Int(1), Int(3)}, Int(1)},
		{"clamp above", Int(5), "clamp", []Value{Int(1), Int(3)}, Int(3)},
		{"clamp inside", Int(2), "clamp", []Value{Int(1), Int(3)}, Int(2)},
		{"pow keeps ints", Int(2), "pow", []Value{Int(10)}, Int(1024)},
		{"pow promotes on a float exponent", Int(4), "pow", []Value{Float(0.5)}, Float(2)},
		{"pow with a negative exponent is a float", Int(2), "pow", []Value{Int(-1)}, Float(0.5)},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, tt.recv, tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%s.%s: unexpected error %v", tt.recv.Inspect(), tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s.%s = %s; want %s", tt.recv.Inspect(), tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	t.Run("even is an Int-only row", func(t *testing.T) {
		if HasMethod(KFloat, "even") {
			t.Errorf("even is registered for float; §12.5 makes it Int-only")
		}
		if HasMethod(KFloat, "odd") {
			t.Errorf("odd is registered for float; §12.5 makes it Int-only")
		}
	})
	t.Run("clamp rejects an inverted range", func(t *testing.T) {
		if _, err := stdCall(t, c, Int(1), "clamp", Int(3), Int(1)); err == nil {
			t.Errorf("clamp(3, 1): want an error, got none")
		}
	})
	t.Run("clamp rejects a non-numeric bound", func(t *testing.T) {
		if _, err := stdCall(t, c, Int(1), "clamp", Str("1"), Int(3)); err == nil {
			t.Errorf(`clamp("1", 3): want a type error, got none`)
		}
	})
}

func TestNumberIteration(t *testing.T) {
	c := stdCtx(DefaultOptions())

	tests := []struct {
		name string
		recv Value
		meth string
		args []Value
		want Value
	}{
		{"times without a closure materialises", Int(3), "times", nil,
			Array(Int(0), Int(1), Int(2))},
		{"zero times is empty", Int(0), "times", nil, Array()},
		{"a negative count is empty", Int(-2), "times", nil, Array()},
		{"upto", Int(1), "upto", []Value{Int(3)}, Array(Int(1), Int(2), Int(3))},
		{"upto a smaller bound is empty", Int(3), "upto", []Value{Int(1)}, Array()},
		{"downto", Int(3), "downto", []Value{Int(1)}, Array(Int(3), Int(2), Int(1))},
		{"step", Int(0), "step", []Value{Int(6), Int(2)},
			Array(Int(0), Int(2), Int(4), Int(6))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, c, tt.recv, tt.meth, tt.args...)
			if err != nil {
				t.Fatalf("%s.%s: unexpected error %v", tt.recv.Inspect(), tt.meth, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("%s.%s = %s; want %s", tt.recv.Inspect(), tt.meth, got.Inspect(), tt.want.Inspect())
			}
		})
	}

	t.Run("times with a closure iterates and returns the receiver", func(t *testing.T) {
		var seen []int64
		block := stdBlock(func(args []Value) (Value, error) {
			seen = append(seen, args[0].Int())
			return Nil(), nil
		})
		got, err := stdCall(t, c, Int(3), "times", block)
		if err != nil {
			t.Fatalf("times: %v", err)
		}
		if len(seen) != 3 || seen[0] != 0 || seen[2] != 2 {
			t.Errorf("times visited %v; want [0 1 2]", seen)
		}
		if !stdSame(got, Int(3)) {
			t.Errorf("times with a closure = %s; want the receiver 3", got.Inspect())
		}
	})

	t.Run("upto reads its bound past the trailing closure", func(t *testing.T) {
		n := 0
		block := stdBlock(func(args []Value) (Value, error) { n++; return Nil(), nil })
		got, err := stdCall(t, c, Int(1), "upto", Int(3), block)
		if err != nil {
			t.Fatalf("upto: %v", err)
		}
		if n != 3 || !stdSame(got, Int(1)) {
			t.Errorf("upto called the closure %d times and returned %s; want 3 and the receiver",
				n, got.Inspect())
		}
	})

	t.Run("break out of times is the value of the call", func(t *testing.T) {
		block := stdBlock(func(args []Value) (Value, error) { return Nil(), breakSignal(Str("hit")) })
		got, err := stdCall(t, c, Int(5), "times", block)
		if err != nil {
			t.Fatalf("times: %v", err)
		}
		if !stdSame(got, Str("hit")) {
			t.Errorf("break inside times = %s; want \"hit\"", got.Inspect())
		}
	})

	t.Run("step with a zero step is an error", func(t *testing.T) {
		if _, err := stdCall(t, c, Int(0), "step", Int(6), Int(0)); err == nil {
			t.Errorf("step(6, 0): want an error, got none")
		}
	})
}

// A duration is Int seconds and exists only when the time module is installed, so a
// script with no clock sees exactly the same thing as if the row were absent (§12.5).
func TestNumberDurations(t *testing.T) {
	tests := []struct {
		name string
		meth string
		want int64
	}{
		{"seconds", "seconds", 7},
		{"minutes", "minutes", 7 * 60},
		{"hours", "hours", 7 * 3600},
		{"days", "days", 7 * 86400},
		{"weeks", "weeks", 7 * 7 * 86400},
	}

	withTime := stdCtx(Options{EnableTime: true})
	without := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdCall(t, withTime, Int(7), tt.meth)
			if err != nil {
				t.Fatalf("7.%s: unexpected error %v", tt.meth, err)
			}
			if !stdSame(got, Int(tt.want)) {
				t.Errorf("7.%s = %s; want %d seconds", tt.meth, got.Inspect(), tt.want)
			}
			if _, err := stdCall(t, without, Int(7), tt.meth); err == nil {
				t.Errorf("7.%s without the time module: want an undefined-method error", tt.meth)
			}
		})
	}
}

// The bit rows of §12.5 are functions, not operators, and they are pure int64: D9's
// promotion rule stops at their door, so `shl` truncates to 64 bits where `*` would have
// gone to Float.
func TestBitOperations(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{"band", "band", []Value{Int(0b1100), Int(0b1010)}, Int(0b1000)},
		{"bor", "bor", []Value{Int(0b1100), Int(0b1010)}, Int(0b1110)},
		{"bxor", "bxor", []Value{Int(0b1100), Int(0b1010)}, Int(0b0110)},
		{"band with a mask", "band", []Value{Int(0x1234), Int(0xff)}, Int(0x34)},
		{"bnot of zero is all ones", "bnot", []Value{Int(0)}, Int(-1)},
		{"bnot is its own inverse", "bnot", []Value{Int(^int64(42))}, Int(42)},
		{"shl", "shl", []Value{Int(1), Int(10)}, Int(1024)},
		{"shl of zero bits", "shl", []Value{Int(7), Int(0)}, Int(7)},
		{"shl truncates to 64 bits instead of promoting", "shl", []Value{Int(1), Int(64)}, Int(0)},
		{"shl into the sign bit", "shl", []Value{Int(1), Int(63)}, Int(math.MinInt64)},
		{"shl drops the bits it shifts out", "shl", []Value{Int(math.MaxInt64), Int(1)}, Int(-2)},
		{"shr", "shr", []Value{Int(1024), Int(10)}, Int(1)},
		{"shr is arithmetic", "shr", []Value{Int(-8), Int(1)}, Int(-4)},
		{"shr of a negative saturates at -1", "shr", []Value{Int(-8), Int(64)}, Int(-1)},
		{"shr of a positive saturates at 0", "shr", []Value{Int(8), Int(64)}, Int(0)},
		{"popcount", "popcount", []Value{Int(255)}, Int(8)},
		{"popcount of zero", "popcount", []Value{Int(0)}, Int(0)},
		{"popcount counts two's complement bits", "popcount", []Value{Int(-1)}, Int(64)},
		{"bit that is set", "bit", []Value{Int(0b101), Int(2)}, Bool(true)},
		{"bit that is clear", "bit", []Value{Int(0b101), Int(1)}, Bool(false)},
		{"bit 63 of a negative number is the sign", "bit", []Value{Int(-1), Int(63)}, Bool(true)},
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
}

// A bit function refuses what it cannot answer exactly: there is no coercion (§9.1) and
// no guess about the bits above 63.
func TestBitOperationErrors(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		args []Value
		want string
	}{
		{"a float argument does not truncate", "band", []Value{Float(2.9), Int(1)},
			"band expects an int, got float: bit operations do not round, write x.int"},
		{"a float second argument", "bor", []Value{Int(1), Float(2.0)},
			"bor expects an int, got float: bit operations do not round, write x.int"},
		{"a string argument", "bxor", []Value{Str("1"), Int(1)},
			"bxor expects an int, got string"},
		{"a negative left shift", "shl", []Value{Int(1), Int(-1)},
			"shl: shift count -1 is negative; the other direction is shr"},
		{"a negative right shift", "shr", []Value{Int(1), Int(-1)},
			"shr: shift count -1 is negative; the other direction is shl"},
		{"a bit index past the width", "bit", []Value{Int(1), Int(64)},
			"bit: index 64 is outside 0..63"},
		{"a negative bit index", "bit", []Value{Int(1), Int(-1)},
			"bit: index -1 is outside 0..63"},
	}

	c := stdCtx(DefaultOptions())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stdBuiltin(t, c, tt.fn, tt.args...)
			if err == nil {
				t.Fatalf("%s: want an error, got none", tt.fn)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s error is %T (%v), want *Error", tt.fn, err, err)
			}
			if e.Msg != tt.want {
				t.Errorf("%s error = %q; want %q", tt.fn, e.Msg, tt.want)
			}
		})
	}
}

// UFCS is not a second implementation (D18): the method spelling every example uses
// reaches the very same row.
func TestBitOperationsAreMethodsToo(t *testing.T) {
	v := evOK(t, evInterp(), `0xff.band(0x0f).shl(4).bor(1)`, nil)
	if !stdSame(v, Int(0xf1)) {
		t.Errorf("chained bit methods = %s; want 241", v.Inspect())
	}
}

// D17 for §12.5: the Ruby number spellings, the `?` predicates and the singular
// durations are gone, and no second name survives for anything that stayed.
func TestNumberHasNoOldNames(t *testing.T) {
	tests := []struct {
		old, use string
	}{
		{"to_i", "int"},
		{"to_int", "int"},
		{"to_f", "float"},
		{"to_s", "str"},
		{"to_json", "json"},
		{"truncate", "int"},
		{"integer", "is(\"int\")"},
		{"between", "clamp"},
		{"gcd", "—"},
		{"lcm", "—"},
		{"fdiv", "float division"},
		{"divmod", "/ and %"},
		{"nan", "—"},
		{"finite", "—"},
		{"infinite", "—"},
		{"succ", "+ 1"},
		{"day", "days"},
		{"hour", "hours"},
		{"minute", "minutes"},
		{"second", "seconds"},
		{"week", "weeks"},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			for _, k := range []Kind{KInt, KFloat} {
				if HasMethod(k, tt.old) {
					t.Errorf("%s answers %q; D17 allows only %q", k, tt.old, tt.use)
				}
			}
		})
	}
}
