package mzs

import (
	"math"
	"math/bits"
	"strconv"
)

// Number methods (SPEC §12.5) for both Int and Float, plus the arithmetic helpers the
// evaluator shares with them.
//
// D9 governs throughout: Int is int64, Float is float64, and an Int operation that
// would overflow promotes to Float rather than wrapping. `7 / 2` is 3; `7.0 / 2` is
// 3.5; `2 ** 10` is an Int and `2 ** 0.5` is a Float.
//
// §12.5 is the complete list of number methods. There are no aliases (D17) and nothing
// beyond the table: `to_i` is `int`, `to_int` is gone, and the predicates lost their `?`
// because there is no such suffix in the language (§3.4).

// registerNum installs a row on both numeric kinds, which is what "number" means in
// every §12.5 receiver column.
func registerNum(ms ...Method) {
	RegisterMethods(KInt, ms...)
	RegisterMethods(KFloat, ms...)
}

// ---------------------------------------------------------------------------
// Shared arithmetic
// ---------------------------------------------------------------------------
//
// The int64 overflow primitives (mulOverflow, intPow, addNum) live in ops.go, which
// owns `+`, `*` and `**`; the rows below reuse them so `2.pow(10)` and `2 ** 10` can
// never disagree.

// numPow is `pow`: Int ** non-negative Int stays an Int unless it overflows; a negative
// exponent or any Float operand gives a Float (§8.3).
func numPow(a, b Value) Value {
	if a.Kind() == KInt && b.Kind() == KInt && b.n >= 0 {
		if v, ok := intPow(a.n, b.n); ok {
			return v
		}
	}
	return Float(math.Pow(a.Float(), b.Float()))
}

// numAbs keeps the receiver's kind, except that the one int64 with no positive twin
// promotes to Float rather than staying negative.
func numAbs(v Value) Value {
	if v.Kind() == KInt {
		n := v.n
		switch {
		case n == math.MinInt64:
			return Float(-float64(n))
		case n < 0:
			return Int(-n)
		}
		return Int(n)
	}
	return Float(math.Abs(v.Float()))
}

// roundHalfUp rounds half away from zero (§12.1): round the scaled value, then correct
// for the case where the exact binary value sits precisely on the .5 boundary of the
// printed decimal. Without the correction 2.675.round(2) would give 2.67.
func roundHalfUp(x, s float64) float64 {
	xs := x * s
	f := math.Round(xs)
	if s == 1.0 {
		return f
	}
	if x > 0 {
		if (f+0.5)/s <= x {
			f++
		}
	} else {
		if (f-0.5)/s >= x {
			f--
		}
	}
	return f
}

// pow10 is 10**d as a float, for the rounding scale.
func pow10(d int) float64 { return math.Pow(10, float64(d)) }

// intScale is 10**d as an int64, reporting overflow; it backs `round(-n)` on an Int.
func intScale(d int) (int64, bool) {
	if d < 0 || d > 18 {
		return 0, false
	}
	p := int64(1)
	for i := 0; i < d; i++ {
		p *= 10
	}
	return p, true
}

// floatToNum converts a rounded float back to the kind §12.5 promises: `1.5.round` is
// an Int, `1.256.round(2)` is a Float. A magnitude no int64 can hold stays a Float
// rather than wrapping.
func floatToNum(f float64, digits int) Value {
	if digits > 0 {
		return Float(f)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) >= 9.2233720368547758e18 {
		return Float(f)
	}
	return Int(int64(f))
}

// intRound rounds an integer to a negative number of digits — 1250.round(-2) is 1300 —
// in exact integer arithmetic, because 10**-2 is not representable as a float and
// scaling by it turns 2500 into 2499.
func intRound(n int64, digits int) Value {
	if digits >= 0 {
		return Int(n)
	}
	p, ok := intScale(-digits)
	if !ok {
		return Int(0)
	}
	q, rem := n/p, n%p
	switch {
	case rem*2 >= p:
		q++
	case rem*2 <= -p:
		q--
	}
	r, ok := mulOverflow(q, p)
	if !ok {
		return Int(n)
	}
	return Int(r)
}

// numRound is `round(digits)` for both kinds, half away from zero (§12.1, §12.5).
func numRound(v Value, digits int) Value {
	if v.Kind() == KInt {
		return intRound(v.n, digits)
	}
	f := v.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Float(f)
	}
	if digits <= 0 {
		// Divide by an exactly representable 10**-digits rather than multiplying by
		// its unrepresentable reciprocal, and round once: rounding to a whole number
		// first and to hundreds afterwards would turn 2549.6 into 2600.
		p, ok := intScale(-digits)
		if !ok {
			return Int(0)
		}
		return floatToNum(math.Round(f/float64(p))*float64(p), 0)
	}
	s := pow10(digits)
	return Float(roundHalfUp(f, s) / s)
}

// numCeil and numFloor follow the same kind rules as numRound.
func numCeil(v Value, digits int) Value  { return numTrunc(v, digits, true) }
func numFloor(v Value, digits int) Value { return numTrunc(v, digits, false) }

func numTrunc(v Value, digits int, up bool) Value {
	if v.Kind() == KInt {
		return intTrunc(v.n, digits, up)
	}
	x := v.Float()
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return Float(x)
	}
	step := math.Floor
	if up {
		step = math.Ceil
	}
	if digits <= 0 {
		p, ok := intScale(-digits)
		if !ok {
			return Int(0)
		}
		return floatToNum(step(x/float64(p))*float64(p), 0)
	}
	s := pow10(digits)
	return Float(step(x*s) / s)
}

func intTrunc(n int64, digits int, up bool) Value {
	if digits >= 0 {
		return Int(n)
	}
	p, ok := intScale(-digits)
	if !ok {
		return Int(0)
	}
	q, rem := n/p, n%p
	if rem != 0 {
		if up && rem > 0 {
			q++
		}
		if !up && rem < 0 {
			q--
		}
	}
	r, ok := mulOverflow(q, p)
	if !ok {
		return Int(n)
	}
	return Int(r)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func init() {
	registerNum(
		// Conversions (§12.7). They are the same operations as the global `int`,
		// `float`, `str` and `json`, reachable both ways under UFCS (D18); the radix
		// argument is why `str` needs a row of its own here.
		Method{Name: "int", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Int(recv.Int()), nil
		}},
		Method{Name: "float", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Float(recv.Float()), nil
		}},
		Method{Name: "str", Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			if len(args) == 0 {
				return Str(recv.Str()), nil
			}
			base, err := argInt(c, args[0])
			if err != nil {
				return Nil(), err
			}
			if base < 2 || base > 36 {
				return Nil(), c.ArgErrorf("str: invalid base %d", base)
			}
			if recv.Kind() != KInt {
				return Nil(), c.ArgErrorf("str: a base needs an int receiver, got %s", recv.Kind())
			}
			return Str(strconv.FormatInt(recv.n, int(base))), nil
		}},
		Method{Name: "json", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Str(encodeJSON(recv, "")), nil
		}},

		Method{Name: "abs", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return numAbs(recv), nil
		}},
		Method{Name: "round", Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			d, err := optInt(c, args, 0, 0)
			if err != nil {
				return Nil(), err
			}
			return numRound(recv, int(d)), nil
		}},
		Method{Name: "ceil", Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			d, err := optInt(c, args, 0, 0)
			if err != nil {
				return Nil(), err
			}
			return numCeil(recv, int(d)), nil
		}},
		Method{Name: "floor", Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			d, err := optInt(c, args, 0, 0)
			if err != nil {
				return Nil(), err
			}
			return numFloor(recv, int(d)), nil
		}},
		Method{Name: "clamp", Min: 2, Max: 2, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			lo, err := argNum(c, args[0])
			if err != nil {
				return Nil(), err
			}
			hi, err := argNum(c, args[1])
			if err != nil {
				return Nil(), err
			}
			if cmp, ok := compare(lo, hi); ok && cmp > 0 {
				return Nil(), c.ArgErrorf("clamp: min %s is greater than max %s", lo.Str(), hi.Str())
			}
			if cmp, ok := compare(recv, lo); ok && cmp < 0 {
				return lo, nil
			}
			if cmp, ok := compare(recv, hi); ok && cmp > 0 {
				return hi, nil
			}
			return recv, nil
		}},

		Method{Name: "zero", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Bool(recv.Float() == 0), nil
		}},
		Method{Name: "positive", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Bool(recv.Float() > 0), nil
		}},
		Method{Name: "negative", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Bool(recv.Float() < 0), nil
		}},

		Method{Name: "pow", Min: 1, Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			e, err := argNum(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return numPow(recv, e), nil
		}},
		Method{Name: "chr", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			n := recv.Int()
			if n < 0 || n > 0x10FFFF {
				return Nil(), c.ArgErrorf("chr: %d is out of the Unicode range", n)
			}
			return Str(string(rune(n))), nil
		}},

		// The iterators take their closure as an ordinary trailing argument (§4.2), so
		// each row counts it in its Max.
		Method{Name: "times", Max: 1, Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return iterInts(c, 0, recv.Int()-1, 1, recv)
		}},
		Method{Name: "upto", Min: 1, Max: 2, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			n, err := numPosInt(c, args, 0)
			if err != nil {
				return Nil(), err
			}
			return iterInts(c, recv.Int(), n, 1, recv)
		}},
		Method{Name: "downto", Min: 1, Max: 2, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			n, err := numPosInt(c, args, 0)
			if err != nil {
				return Nil(), err
			}
			return iterInts(c, recv.Int(), n, -1, recv)
		}},
		Method{Name: "step", Min: 2, Max: 3, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			limit, err := numPosInt(c, args, 0)
			if err != nil {
				return Nil(), err
			}
			by, err := numPosInt(c, args, 1)
			if err != nil {
				return Nil(), err
			}
			if by == 0 {
				return Nil(), c.ArgErrorf("step: step cannot be 0")
			}
			return iterInts(c, recv.Int(), limit, by, recv)
		}},

		durationMethod("seconds", 1),
		durationMethod("minutes", 60),
		durationMethod("hours", 3600),
		durationMethod("days", 86400),
		durationMethod("weeks", 7*86400),
	)

	RegisterMethods(KInt,
		Method{Name: "even", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Bool(recv.n%2 == 0), nil
		}},
		Method{Name: "odd", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Bool(recv.n%2 != 0), nil
		}},
	)
}

// ---------------------------------------------------------------------------
// Bit operations (§12.5)
// ---------------------------------------------------------------------------
//
// These are functions, not operators, and that is the decision rather than a shortcut.
// `&` and `|` living one keystroke away from `&&` and `||` is the exact shape D16 says
// not to introduce, and `<<`/`>>` are reserved (§20). As free functions they get the
// method spelling for nothing under UFCS: `flags.band(0xff)` *is* `band(flags, 0xff)`,
// one implementation and one name (D17).
//
// Every row here is pure int64 arithmetic. D9's promotion rule does not reach it: a bit
// operation never produces a Float, `shl` truncates to 64 bits instead of promoting, and
// a Float argument is a type error rather than a silent truncation (§9.1).

func init() {
	RegisterBuiltins(
		Builtin{Name: "band", Min: 2, Max: 2, Fn: bitPair(func(a, b int64) int64 { return a & b })},
		Builtin{Name: "bor", Min: 2, Max: 2, Fn: bitPair(func(a, b int64) int64 { return a | b })},
		Builtin{Name: "bxor", Min: 2, Max: 2, Fn: bitPair(func(a, b int64) int64 { return a ^ b })},
		Builtin{Name: "bnot", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			a, err := argBits(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return Int(^a), nil
		}},
		Builtin{Name: "shl", Min: 2, Max: 2, Fn: bitShift(false)},
		Builtin{Name: "shr", Min: 2, Max: 2, Fn: bitShift(true)},
		Builtin{Name: "popcount", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			a, err := argBits(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return Int(int64(bits.OnesCount64(uint64(a)))), nil
		}},
		Builtin{Name: "bit", Min: 2, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			a, err := argBits(c, args[0])
			if err != nil {
				return Nil(), err
			}
			i, err := argBits(c, args[1])
			if err != nil {
				return Nil(), err
			}
			// 64 is not "false, the number has no such bit": for a negative receiver
			// every bit above 63 is a 1, so either answer would be a guess (I5, D16).
			if i < 0 || i > 63 {
				return Nil(), c.ArgErrorf("bit: index %d is outside 0..63", i)
			}
			return Bool(a&(int64(1)<<uint(i)) != 0), nil
		}},
	)
}

// argBits reads an argument that must be an Int. A Float is refused rather than
// truncated: `2.9.band(1)` has no defensible answer, and there is no coercion (§9.1).
func argBits(c *Ctx, v Value) (int64, error) {
	if v.Kind() == KInt {
		return v.n, nil
	}
	if v.Kind() == KFloat {
		return 0, c.TypeErrorf("%s expects an int, got float: bit operations do not round, write x.int", c.Name())
	}
	return 0, c.TypeErrorf("%s expects an int, got %s", c.Name(), v.TypeName())
}

// bitPair builds the two-argument rows, which differ only in the Go operator.
func bitPair(op func(a, b int64) int64) HostFunc {
	return func(c *Ctx, args []Value) (Value, error) {
		a, err := argBits(c, args[0])
		if err != nil {
			return Nil(), err
		}
		b, err := argBits(c, args[1])
		if err != nil {
			return Nil(), err
		}
		return Int(op(a, b)), nil
	}
}

// bitShift builds `shl` and `shr`. `shr` is arithmetic — the sign bit is copied in, so
// `shr(-8, 1)` is -4 and stays the integer division by two it looks like.
//
// A negative count is an error rather than a shift the other way: each direction has its
// own name, and `shl(x, -1)` is far more often a bug than an intention.
func bitShift(right bool) HostFunc {
	return func(c *Ctx, args []Value) (Value, error) {
		a, err := argBits(c, args[0])
		if err != nil {
			return Nil(), err
		}
		n, err := argBits(c, args[1])
		if err != nil {
			return Nil(), err
		}
		if n < 0 {
			return Nil(), c.ArgErrorf("%s: shift count %d is negative; the other direction is %s",
				c.Name(), n, otherShift(right))
		}
		// Everything is shifted out past 63 bits. Go defines this case, but spelling it
		// out keeps the code and the SPEC line side by side.
		if n > 63 {
			if right && a < 0 {
				return Int(-1), nil
			}
			return Int(0), nil
		}
		if right {
			return Int(a >> uint(n)), nil
		}
		return Int(a << uint(n)), nil
	}
}

func otherShift(right bool) string {
	if right {
		return "shl"
	}
	return "shr"
}

// numPos is the positional arguments with the trailing closure removed. A closure is an
// ordinary last argument (§4.2), so `5.upto(9) { … }` and `5.upto(9)` read their limit
// from the same place.
func numPos(c *Ctx, args []Value) []Value {
	if c.HasClosure() && len(args) > 0 {
		return args[:len(args)-1]
	}
	return args
}

// numPosInt reads the i-th positional argument as an integer and names the row when one
// is missing, which is what `5.step(10) { … }` — a step count swallowed by the closure —
// needs to hear.
func numPosInt(c *Ctx, args []Value, i int) (int64, error) {
	pos := numPos(c, args)
	if i >= len(pos) {
		return 0, c.ArgErrorf("%s expects at least %d argument(s), got %d", c.Name(), i+1, len(pos))
	}
	return argInt(c, pos[i])
}

// durationMethod builds `7.days` and friends. A duration is plain Int seconds — there
// is no eleventh kind — because §12.8 already defines `Time + Int` as adding seconds.
// The row is gated on the time module being installed, and a gated-off method must be
// indistinguishable from an absent one, hence the undefined-method error. It is spelled
// out rather than taken from undefinedMethodError because the row *is* registered: the
// suggestion pass would find this very name and offer it back.
func durationMethod(name string, secs int64) Method {
	return Method{Name: name, Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
		if !c.Options().EnableTime {
			return Nil(), c.ErrorfKind(ErrKindName, "undefined method '%s' for %s", name, recv.Kind())
		}
		n, ok := mulOverflow(recv.Int(), secs)
		if !ok {
			return Nil(), c.ArgErrorf("%s: duration overflows", name)
		}
		return Int(n), nil
	}}
}

// iterInts backs times/upto/downto/step. Without a closure it materialises the sequence,
// so `3.times` is [0,1,2] and `3.times.each { … }` works (§12.5); with one it iterates
// and returns the receiver, which keeps `1_000_000.times { }` from allocating a million
// values it would immediately discard.
func iterInts(c *Ctx, from, to, by int64, recv Value) (Value, error) {
	if by == 0 {
		return Nil(), c.ArgErrorf("%s: step cannot be 0", c.Name())
	}
	n := int64(0)
	if by > 0 && to >= from {
		n = (to-from)/by + 1
	} else if by < 0 && to <= from {
		n = (from-to)/(-by) + 1
	}
	closure := c.HasClosure()
	if !closure {
		if err := c.CheckCollection(int(min(n, int64(math.MaxInt32)))); err != nil {
			return Nil(), err
		}
	}
	var out []Value
	if !closure {
		out = make([]Value, 0, n)
	}
	for i, k := from, int64(0); k < n; i, k = i+by, k+1 {
		if err := c.Step(1); err != nil {
			return Nil(), err
		}
		if closure {
			if _, err := c.CallClosure(Int(i)); err != nil {
				return Nil(), err
			}
			continue
		}
		out = append(out, Int(i))
	}
	if closure {
		return recv, nil
	}
	return arrayOf(out), nil
}
