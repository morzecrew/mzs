package mzs

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// §8.3 is where the language is least like the ones it resembles: `/` on two ints is
// integer division, `%` takes the sign of the divisor, an overflowing Int becomes a
// Float instead of wrapping, and `+` refuses two kinds it cannot add rather than
// stringifying one of them (§9.1). Each of those is a decision, so each has a row here.
//
// The operators are driven through Eval, because the operand kinds are what the rules
// are about and a hand-built call would let the test pick them without the parser's
// agreement.

func opEval(t *testing.T, src string) (Value, error) {
	t.Helper()
	return New(Options{Timeout: 0, StepBudget: -1, EnableTime: true}).
		Eval(context.Background(), src, nil)
}

func opStr(t *testing.T, src string) string {
	t.Helper()
	v, err := opEval(t, src)
	if err != nil {
		t.Fatalf("Eval(%s) error = %v", src, err)
	}
	return v.Inspect()
}

func TestArithmeticOnEveryKindPair(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// Numbers (D9).
		{"int division truncates", `7 / 2`, "3"},
		{"a float operand makes it float", `7.0 / 2`, "3.5"},
		{"int overflow promotes on +", `9223372036854775807 + 1`, "9223372036854776000.0"},
		{"int overflow promotes on *", `9223372036854775807 * 2`, "18446744073709552000.0"},
		{"int overflow promotes on -", `-9223372036854775807 - 3`, "-9223372036854776000.0"},
		{"the most negative int is reachable by arithmetic", `-9223372036854775807 - 1`, "-9223372036854775808"},
		{"modulo takes the sign of the divisor", `-7 % 3`, "2"},
		{"modulo of a negative divisor", `7 % -3`, "-2"},
		{"float modulo", `7.5 % 2`, "1.5"},
		{"float modulo of a negative divisor", `7.5 % -2`, "-0.5"},
		{"power of two ints is an int", `2 ** 10`, "1024"},
		{"a fractional power is a float", `4 ** 0.5`, "2.0"},
		{"a negative power is a float", `2 ** -1`, "0.5"},
		{"unary minus on a float", `-1.5`, "-1.5"},
		{"unary plus keeps the number", `+1`, "1"},

		// Strings.
		{"string concatenation", `"при" + "вет"`, `"привет"`},
		{"string repetition", `"ха" * 3`, `"хахаха"`},
		{"string repetition by zero is empty", `"ха" * 0`, `""`},
		{"string modulo formats", `"%s-%d" % ["а", 1]`, `"а-1"`},
		{"string modulo with one argument", `"%s!" % "да"`, `"да!"`},
		{"string modulo with a number", `"%d!" % 1`, `"1!"`},

		// Collections.
		{"array concatenation", `[1] + [2]`, "[1,2]"},
		{"array difference removes every equal element", `[1,2,1,3] - [1]`, "[2,3]"},
		{"array repetition", `[1,2] * 2`, "[1,2,1,2]"},
		{"dict merge, later wins", `[a: 1, b: 2] + [b: 3]`, `{"a":1,"b":3}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// Time is the one kind whose arithmetic is not numeric: a number is seconds, and two
// times subtract into seconds (§12.8).
func TestTimeArithmetic(t *testing.T) {
	in := New(Options{Timeout: 0, StepBudget: -1, EnableTime: true,
		Now: func() time.Time { return time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC) }})

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"adding seconds moves the instant forward",
			`include time; (time.parse("2026-08-13 10:00:00") + 60).str`, "2026-08-13T10:01:00Z"},
		{"subtracting seconds moves it back",
			`include time; (time.parse("2026-08-13 10:00:00") - 60).str`, "2026-08-13T09:59:00Z"},
		{"two times subtract into seconds",
			`include time; time.parse("2026-08-13 10:01:00") - time.parse("2026-08-13 10:00:00")`, "60"},
		{"a duration from §12.5 is just seconds",
			`include time; (time.parse("2026-08-13 10:00:00") + 2.minutes).str`, "2026-08-13T10:02:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := in.Eval(context.Background(), tt.src, nil)
			if err != nil {
				t.Fatalf("Eval error = %v", err)
			}
			if v.Str() != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, v.Str(), tt.want)
			}
		})
	}

	t.Run("a time and a string do not add", func(t *testing.T) {
		_, err := in.Eval(context.Background(), `include time; time.parse("2026-08-13 10:00:00") + "1"`, nil)
		if err == nil {
			t.Error("adding a string to a time was accepted; want a type error")
		}
	})
}

// There is no coercion (§9.1): a pair of kinds an operator has no rule for is a type
// error naming both, not a silent `str` of one of them.
func TestArithmeticTypeErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string // a fragment of the message
	}{
		{`"2" + 1`, "cannot add int to string"},
		{`[1] + 1`, "cannot add int to array"},
		{`[a: 1] + 1`, "cannot add int to dict"},
		{`nil + 1`, "cannot add"},
		{`true + 1`, "cannot add"},
		{`1 + "2"`, "cannot add"},
		{`[1] - 1`, "cannot subtract int from array"},
		{`"a" - "b"`, "cannot subtract"},
		{`nil - 1`, "cannot subtract"},
		{`"a" * "b"`, "cannot multiply string by string"},
		{`[1] * "b"`, "cannot multiply array by string"},
		{`nil * 1`, "cannot multiply"},
		{`"a" / 2`, "cannot divide"},
		{`[1] % 2`, "cannot take"},
		{`"a" ** 2`, "cannot raise"},
		{`-"a"`, "cannot negate"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			_, err := opEval(t, tt.src)
			if err == nil {
				t.Fatalf("%s was accepted; want a type error", tt.src)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error is %T (%v); want an *Error", err, err)
			}
			if e.Kind != ErrKindType {
				t.Errorf("%s: kind = %q; want %q", tt.src, e.Kind, ErrKindType)
			}
			if !strings.Contains(e.Msg, tt.want) {
				t.Errorf("%s: message = %q; want it to contain %q", tt.src, e.Msg, tt.want)
			}
		})
	}
}

// Division by zero is the one arithmetic error mzs raises; float division follows IEEE
// and never raises (§8.3).
func TestDivisionByZero(t *testing.T) {
	for _, src := range []string{`1 / 0`, `1 % 0`} {
		t.Run(src, func(t *testing.T) {
			_, err := opEval(t, src)
			var e *Error
			if !errors.As(err, &e) || e.Kind != ErrKindZeroDiv {
				t.Errorf("%s = %v; want a zero-division error", src, err)
			}
		})
	}
	for _, tt := range []struct{ src, want string }{
		{`1.0 / 0`, "Infinity"},
		{`-1.0 / 0`, "-Infinity"},
		{`0.0 / 0`, "NaN"},
		{`1.0 % 0`, "NaN"},
	} {
		t.Run(tt.src, func(t *testing.T) {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// The int64 corners a wrapping implementation gets wrong. The most negative int64 has
// no literal — the lexer refuses 9223372036854775808 because the minus is an operator,
// not part of the number (§3.6) — so every row reaches it by arithmetic, which is also
// how a script would.
func TestInt64Corners(t *testing.T) {
	const min = `x = -9223372036854775807 - 1; `

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"arithmetic reaches it exactly", min + `x`, "-9223372036854775808"},
		{"divided by -1 it promotes", min + `x / -1`, "9223372036854776000.0"},
		{"modulo -1 is zero", min + `x % -1`, "0"},
		{"times -1 it promotes", min + `x * -1`, "9223372036854776000.0"},
		{"abs promotes rather than staying negative", min + `x.abs`, "9223372036854776000.0"},
		{"negating it promotes", min + `-x`, "9223372036854776000.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// §14.2 reaches the operators too: `*` and `+` are the two ways a script can ask for a
// gigabyte in one expression.
func TestOperatorsRespectTheLimits(t *testing.T) {
	tests := []struct {
		name string
		src  string
		opts Options
	}{
		{"string repetition is bounded", `"а" * 1000000`, Options{MaxStringBytes: 64}},
		{"string concatenation is bounded", `"а" * 32 + "б" * 32`, Options{MaxStringBytes: 32}},
		{"array repetition is bounded", `[1,2,3] * 1000`, Options{MaxCollection: 10}},
		{"array concatenation is bounded", `[1,2,3,4,5] + [1,2,3,4,5,6]`, Options{MaxCollection: 10}},
		{"interpolation is bounded", `s = "а" * 32; "${s}${s}"`, Options{MaxStringBytes: 40}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			opts.Timeout = 0
			opts.StepBudget = -1
			_, err := New(opts).Eval(context.Background(), tt.src, nil)
			if !errors.Is(err, ErrBudget) {
				t.Errorf("%s = %v; want a limit error", tt.src, err)
			}
		})
	}

	// A repetition count of zero or less is empty rather than an error: `"-" * n` in a
	// layout is written with an n that can legitimately reach zero.
	t.Run("a repetition count at or below zero is empty", func(t *testing.T) {
		for _, tt := range []struct{ src, want string }{
			{`"а" * 0`, `""`}, {`"а" * -1`, `""`}, {`[1] * 0`, "[]"}, {`[1] * -1`, "[]"},
		} {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		}
	})
}

// Ordering answers for the kinds that have an order and refuses the rest (§7.5), and
// `<=>` is the same comparison with a number for an answer.
func TestOrderingAndSpaceship(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`1 < 2`, "true"},
		{`2.5 <= 2.5`, "true"},
		{`"а" < "б"`, "true"},
		{`"б" > "а"`, "true"},
		{`1 <=> 2`, "-1"},
		{`2 <=> 1`, "1"},
		{`2 <=> 2`, "0"},
		{`"а" <=> "б"`, "-1"},
		{`1 < 2.5`, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	// Arrays order element by element, which is what makes `sort` work on them.
	for _, tt := range []struct{ src, want string }{
		{`[1] < [2]`, "true"},
		{`[1,2] <=> [1,3]`, "-1"},
		{`[1,2] <=> [1,2]`, "0"},
	} {
		t.Run(tt.src, func(t *testing.T) {
			if got := opStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	// `<` between kinds that have no order is a type error; `<=>` answers nil instead,
	// because it is the row `sort` hands a comparator and nil is "cannot say".
	for _, src := range []string{`1 < "а"`, `nil < 1`, `[1] < "а"`} {
		t.Run("incomparable: "+src, func(t *testing.T) {
			if _, err := opEval(t, src); err == nil {
				t.Errorf("%s was accepted; kinds that have no order are a type error", src)
			}
		})
	}
	if got := opStr(t, `1 <=> "а"`); got != "nil" {
		t.Errorf(`1 <=> "а" = %s; want nil`, got)
	}

	// Bools order as false < true, which is what lets `sort` put the failures first.
	for _, tt := range []struct{ src, want string }{
		{`false < true`, "true"},
		{`true <=> false`, "1"},
	} {
		if got := opStr(t, tt.src); got != tt.want {
			t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
		}
	}
}

// mulOverflow and intPow are shared by the operators and by §12.5's `pow`, so the two
// must never disagree about where int64 stops.
func TestOverflowHelpersAgreeWithTheOperators(t *testing.T) {
	if v, ok := mulOverflow(math.MaxInt64, 2); ok {
		t.Errorf("mulOverflow(MaxInt64, 2) = %d, true; want an overflow", v)
	}
	if v, ok := mulOverflow(3, 4); !ok || v != 12 {
		t.Errorf("mulOverflow(3, 4) = %d, %v; want 12, true", v, ok)
	}
	if _, ok := intPow(2, 63); ok {
		t.Error("intPow(2, 63) reported an int64 answer; 2**63 does not fit")
	}
	if v, ok := intPow(2, 10); !ok || v.Int() != 1024 {
		t.Errorf("intPow(2, 10) = %s, %v; want 1024, true", v.Inspect(), ok)
	}
	if got := opStr(t, `2 ** 63`); got != opStr(t, `(2 ** 62) * 2`) {
		t.Errorf("2 ** 63 and (2 ** 62) * 2 disagree: %s vs %s",
			opStr(t, `2 ** 63`), opStr(t, `(2 ** 62) * 2`))
	}
}
