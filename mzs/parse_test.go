package mzs

import (
	"context"
	"errors"
	"math"
	"testing"
)

// RFC 0008: parse_int and parse_float answer nil unless the whole string is a number.

func parseEval(t *testing.T, src string) (Value, error) {
	t.Helper()
	return New(DefaultOptions()).Eval(context.Background(), src, nil)
}

// Every spelling parse_int accepts, with and without a base, and every one it refuses:
// trailing text, misplaced separators, a prefix under an explicit base, overflow.
func TestParseInt(t *testing.T) {
	ok := []struct {
		src  string
		want int64
	}{
		{`"42".parse_int`, 42},
		{`parse_int("42")`, 42},
		{`"-17".parse_int`, -17},
		{`"+5".parse_int`, 5},
		{`"007".parse_int`, 7},
		{`" \t 12\n".parse_int`, 12},
		{`"1_000_000".parse_int`, 1000000},
		{`"9223372036854775807".parse_int`, math.MaxInt64},
		{`"-9223372036854775808".parse_int`, math.MinInt64},
		{`"ff".parse_int(16)`, 255},
		{`"FF".parse_int(16)`, 255},
		{`"-101".parse_int(2)`, -5},
		{`"zz".parse_int(36)`, 1295},
		{`"17".parse_int(8)`, 15},
		{`"0x1f".parse_int(0)`, 31},
		{`"0X1F".parse_int(0)`, 31},
		{`"-0x1f".parse_int(0)`, -31},
		{`"0o17".parse_int(0)`, 15},
		{`"0b1010".parse_int(0)`, 10},
		{`"0xff_ff".parse_int(0)`, 0xffff},
		{`"10".parse_int(0)`, 10},
		{`"0".parse_int(0)`, 0},
		// An explicit base takes no prefix, so in base 36 "0x1f" is four digits.
		{`"0x1f".parse_int(36)`, 42819},
	}
	for _, tt := range ok {
		v, err := parseEval(t, tt.src)
		if err != nil || v.Kind() != KInt || v.Int() != tt.want {
			t.Errorf("%s = %s, %v; want %d", tt.src, v.Inspect(), err, tt.want)
		}
	}

	refused := []string{
		`""`, `"   "`, `"abc"`, `"12abc"`, `"12 3"`, `"1.5"`, `"1e3"`, `"-"`, `"+"`, `"+-1"`,
		`"- 1"`, `"_1"`, `"1_"`, `"1__0"`, `"0x1f"`, `"٣"`, `"９"`,
		`"9223372036854775808"`, `"-9223372036854775809"`, `"99999999999999999999"`,
	}
	for _, s := range refused {
		src := s + ".parse_int"
		if v, err := parseEval(t, src); err != nil || v.Kind() != KNil {
			t.Errorf("%s = %s, %v; want nil", src, v.Inspect(), err)
		}
	}
	refusedBase := []string{
		`"2".parse_int(2)`, `"g".parse_int(16)`, `"0x1f".parse_int(16)`, `"0x".parse_int(0)`,
		`"0x_1f".parse_int(0)`, `"0b2".parse_int(0)`, `"0o8".parse_int(0)`, `"-0x".parse_int(0)`,
	}
	for _, src := range refusedBase {
		if v, err := parseEval(t, src); err != nil || v.Kind() != KNil {
			t.Errorf("%s = %s, %v; want nil", src, v.Inspect(), err)
		}
	}
}

// Every spelling parse_float accepts, and the ones a literal would not: a bare `.`, a
// separator in the exponent, inf, nan, hex and a value past float64.
func TestParseFloat(t *testing.T) {
	ok := []struct {
		src  string
		want float64
	}{
		{`"1.5".parse_float`, 1.5},
		{`parse_float("1.5")`, 1.5},
		{`"12".parse_float`, 12},
		{`"-0.25".parse_float`, -0.25},
		{`"+3.0".parse_float`, 3},
		{`" 2.5 ".parse_float`, 2.5},
		{`"1_000.25".parse_float`, 1000.25},
		{`"2e10".parse_float`, 2e10},
		{`"1.5E-3".parse_float`, 1.5e-3},
		{`"1e+2".parse_float`, 100},
		{`"1e-400".parse_float`, 0},
	}
	for _, tt := range ok {
		v, err := parseEval(t, tt.src)
		if err != nil || v.Kind() != KFloat || v.Float() != tt.want {
			t.Errorf("%s = %s, %v; want %v", tt.src, v.Inspect(), err, tt.want)
		}
	}
	if v, _ := parseEval(t, `"-0".parse_float`); v.Kind() != KFloat || !math.Signbit(v.Float()) {
		t.Errorf(`"-0".parse_float = %s, want -0.0`, v.Inspect())
	}

	refused := []string{
		`""`, `" "`, `"abc"`, `"1.5x"`, `"1.5.2"`, `".5"`, `"1."`, `"."`, `"-"`, `"e5"`, `"1e"`,
		`"1e+"`, `"1e1_0"`, `"1_.5"`, `"1._5"`, `"inf"`, `"-Inf"`, `"infinity"`, `"nan"`, `"NaN"`,
		`"0x1f"`, `"0x1p-2"`, `"1e400"`, `"-1e400"`, `"1,5"`,
	}
	for _, s := range refused {
		src := s + ".parse_float"
		if v, err := parseEval(t, src); err != nil || v.Kind() != KNil {
			t.Errorf("%s = %s, %v; want nil", src, v.Inspect(), err)
		}
	}
}

// A missing number is nil; a value that is not a string at all is a bug and raises.
func TestParseErrors(t *testing.T) {
	cases := []struct {
		src  string
		kind string
	}{
		{`parse_int(nil)`, ErrKindType},
		{`5.parse_int`, ErrKindType},
		{`parse_float(1.5)`, ErrKindType},
		{`parse_int("1", 1.5)`, ErrKindType},
		{`parse_int("1", "16")`, ErrKindType},
		{`parse_int("1", 1)`, ErrKindArgument},
		{`parse_int("1", 37)`, ErrKindArgument},
		{`parse_int("1", -2)`, ErrKindArgument},
	}
	for _, tt := range cases {
		_, err := parseEval(t, tt.src)
		var e *Error
		if !errors.As(err, &e) || e.Kind != tt.kind {
			t.Errorf("%s: error = %v, want kind %s", tt.src, err, tt.kind)
		}
	}

	// `?.` is the spelling for a value that may be absent, and `??` supplies the default.
	v, err := parseEval(t, `x = nil
[x?.parse_int, x?.parse_float, "12abc".parse_int ?? 0, "7".parse_int ?? 0]`)
	if err != nil || v.Inspect() != "[null,null,0,7]" {
		t.Errorf("composition = %s, %v", v.Inspect(), err)
	}
}

// The lenient conversions keep their meaning: parse_* is a second operation, not a mode.
func TestParseLeavesIntAlone(t *testing.T) {
	v, err := parseEval(t, `["12abc".int, "".int, "x".float.is("float"), "0x1f".int]`)
	if err != nil || v.Inspect() != "[12,0,true,0]" {
		t.Errorf("lenient conversions = %s, %v", v.Inspect(), err)
	}
}
