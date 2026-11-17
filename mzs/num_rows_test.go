package mzs

import (
	"strings"
	"testing"
)

// RFC 0016, part 4: the number rows and the math members. Each case is a script and the
// Inspect of its value.

func TestNumberRowsOfRFC0016(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		// div is floored, the partner of %: div(a, b) * b + a % b == a.
		{`div(7, 3)`, `2`},
		{`div(-7, 3)`, `-3`},
		{`div(7, -3)`, `-3`},
		{`div(-7, -3)`, `2`},
		{`div(6, -3)`, `-2`},
		{`div(-6, 3)`, `-2`},
		{`[-7, 7].flat_map { (a) -> [-3, 3].map { (b) -> div(a, b) * b + a % b == a } }.all`, `true`},
		{`(-9223372036854775807 - 1).div(-1)`, `9223372036854776000.0`},
		{`div(-7.5, 2)`, `-4.0`},
		{`div(7, 2.0)`, `3.0`},
		{`div(7.5, 0)`, `Infinity`},
		{`div(7, 0.0)`, `Infinity`},
		{`7.div(2)`, `3`},
		{`div(1, -9223372036854775807 - 1)`, `-1`},
		{`b = -9223372036854775807 - 1; div(1, b) * b`, `9223372036854776000.0`}, // past int64: promotes

		// sign is an int whatever the receiver.
		{`sign(-5)`, `-1`},
		{`sign(0)`, `0`},
		{`sign(9)`, `1`},
		{`sign(-2.5)`, `-1`},
		{`sign(0.1)`, `1`},
		{`sign(-0.0)`, `0`},
		{`sign(-1.0 / 0)`, `-1`},

		// gcd and lcm are non-negative, of ints, and promote past MaxInt64.
		{`gcd(12, 18)`, `6`},
		{`gcd(-12, 18)`, `6`},
		{`gcd(12, -18)`, `6`},
		{`gcd(0, 5)`, `5`},
		{`gcd(0, 0)`, `0`},
		{`gcd(7, 13)`, `1`},
		{`lcm(4, 6)`, `12`},
		{`lcm(-4, 6)`, `12`},
		{`lcm(0, 6)`, `0`},
		{`lcm(0, 0)`, `0`},
		{`lcm(6, 0)`, `0`},
		{`lcm(21, 6)`, `42`},
		{`gcd(-9223372036854775807 - 1, 0)`, `9223372036854776000.0`},
		{`gcd(-9223372036854775807 - 1, 6)`, `2`},
		{`m = -9223372036854775807 - 1; gcd(m, m)`, `9223372036854776000.0`},
		{`lcm(9223372036854775807, 2)`, `18446744073709552000.0`},
		{`lcm(4611686018427387904, 2)`, `4611686018427387904`},
		{`lcm(9223372036854775807, 9223372036854775806)`, `8.507059173023462e+37`},

		// The IEEE tests: an int is never NaN or infinite.
		{`(0.0 / 0).is_nan`, `true`},
		{`1.5.is_nan`, `false`},
		{`1.is_nan`, `false`},
		{`(1.0 / 0).is_inf`, `true`},
		{`(-1.0 / 0).is_inf`, `true`},
		{`(0.0 / 0).is_inf`, `false`},
		{`9223372036854775807.is_inf`, `false`},

		// The math members.
		{`include math; math.inf`, `Infinity`},
		{`include math; -math.inf`, `-Infinity`},
		{`include math; math.inf == 1.0 / 0`, `true`},
		{`include math; math.nan.is_nan`, `true`},
		{`include math; math.nan == math.nan`, `false`},
		{`include math; math.asin(1) == math.pi / 2`, `true`},
		{`include math; math.acos(1)`, `0.0`},
		{`include math; math.asin(2).is_nan`, `true`},
		{`include math; [math.sinh(0), math.cosh(0), math.tanh(0)]`, `[0,1,0]`},
		{`include math; math.tanh(1000)`, `1.0`},
		{`include math; math.cosh(1000)`, `Infinity`},
		{`include math; math.keys.len`, `22`},
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

func TestNumberRowErrorsOfRFC0016(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`div(7, 0)`, "zero-division: divided by 0"},
		{`div(-7, 0)`, "zero-division: divided by 0"},
		{`div(7, "2")`, "div expects a number"},
		{`sign(0.0 / 0)`, "sign: NaN has no sign"},
		{`gcd(2.5, 1)`, "gcd expects ints, got float"},
		{`gcd(4, 2.0)`, "gcd expects ints, got float"},
		{`lcm(4, "2")`, "lcm expects ints, got string"},
		{`include math; math.asin("x")`, "math.asin expects a number"},
		{`include math; math.tanh()`, "math.tanh expects 1 argument(s), got 0"},
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
