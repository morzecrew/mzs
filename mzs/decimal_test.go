package mzs

import (
	"strings"
	"testing"
)

// SPEC §12.15, driven through the front end: a decimal is a value the language already
// has (§7.8), so what has to be pinned is not only the arithmetic but the value model
// around it — that `==` is the numeric question, that `<` says so instead of guessing,
// and that `+` no longer answers with the right-hand operand.

// TestDecimalOf is the one way in: text, an Int, or a decimal that is already one.
func TestDecimalOf(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"a price", `include decimal; decimal.of("1500.35").json`, `{"units":150035,"scale":2}`},
		{"a whole number keeps scale 0", `include decimal; decimal.of("42").json`, `{"units":42,"scale":0}`},
		{"an int is exact and needs no text", `include decimal; decimal.of(42).json`, `{"units":42,"scale":0}`},
		{"trailing zeros are shed on the way in", `include decimal; decimal.of("1.50").json`, `{"units":15,"scale":1}`},
		{"and all of them", `include decimal; decimal.of("2.000").json`, `{"units":2,"scale":0}`},
		{"zero has one form", `include decimal; decimal.of("-0.00").json`, `{"units":0,"scale":0}`},
		{"a sign", `include decimal; decimal.str(decimal.of("-0.5"))`, "-0.5"},
		{"a leading plus", `include decimal; decimal.str(decimal.of("+3.25"))`, "3.25"},
		{"no integer part", `include decimal; decimal.str(decimal.of(".5"))`, "0.5"},
		{"no fraction after the dot", `include decimal; decimal.str(decimal.of("5."))`, "5"},
		{"surrounding blanks", `include decimal; decimal.str(decimal.of("  1.5  "))`, "1.5"},
		{"eighteen places is the cap and fits",
			`include decimal; decimal.str(decimal.of("0.000000000000000001"))`, "0.000000000000000001"},
		{"of a decimal is the same decimal", `include decimal; decimal.of(decimal.of("1.5")) == decimal.of("1.5")`, "true"},
		{"a dict that came back from json is one too",
			`include decimal; include json; decimal.str(decimal.of(json.parse("{\"units\":150035,\"scale\":2}")), 2)`, "1500.35"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestDecimalOfRefuses pins the diagnostics of the way in. Each one names the fix,
// because each one is a mistake with exactly one correct next line (§17).
func TestDecimalOfRefuses(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, kind, msg string }{
		{"a float has already lost the digits", `include decimal; decimal.of(1500.35)`,
			ErrKindType, `decimal.of("1500.35")`},
		{"and says so even when it looks exact", `include decimal; decimal.of(1.0)`,
			ErrKindType, "a float has already lost the exact digits"},
		{"a grouped price is not a decimal", `include decimal; decimal.of("1 500,35")`,
			ErrKindDecimal, "digits, one dot and an optional sign"},
		{"an exponent is not one either", `include decimal; decimal.of("1.5e3")`,
			ErrKindDecimal, "cannot read"},
		{"nor is a bare sign", `include decimal; decimal.of("-")`, ErrKindDecimal, "cannot read"},
		{"nor is the empty string", `include decimal; decimal.of("")`, ErrKindDecimal, "cannot read"},
		{"nineteen places is past the cap", `include decimal; decimal.of("0.0000000000000000001")`,
			ErrKindDecimal, "19 decimal places and a decimal holds 18"},
		{"a plain dict is not a decimal", `include decimal; decimal.of({a: 1})`,
			ErrKindType, "build one with decimal.of"},
		{"and neither is a broken one", `include decimal; decimal.of({units: "x", scale: 2})`,
			ErrKindType, "build one with decimal.of"},
		{"a scale out of range is refused", `include decimal; decimal.of({units: 1, scale: 40})`,
			ErrKindType, "0..18 places"},
		{"nil is not a number", `include decimal; decimal.of(nil)`, ErrKindType, "got nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q; want %q (%s)", tt.src, e.Kind, tt.kind, e.Msg)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s = %q; want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestDecimalArithmetic is the point of the module: the sums a float gets wrong.
func TestDecimalArithmetic(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"the sum a float cannot do", `include decimal
decimal.str(decimal.plus(decimal.of("0.1"), decimal.of("0.2")))`, "0.3"},
		{"and the float that cannot", `(0.1 + 0.2).str`, "0.30000000000000004"},
		{"plus is variadic", `include decimal
decimal.str(decimal.plus(decimal.of("0.1"), decimal.of("0.2"), decimal.of("0.3")))`, "0.6"},
		{"an int mixes in without a conversion", `include decimal
decimal.str(decimal.plus(decimal.of("0.5"), 2))`, "2.5"},
		{"different scales align", `include decimal
decimal.str(decimal.plus(decimal.of("1.005"), decimal.of("2")))`, "3.005"},
		{"minus", `include decimal; decimal.str(decimal.minus(decimal.of("1500.35"), decimal.of("0.35")))`, "1500"},
		{"minus goes negative", `include decimal; decimal.str(decimal.minus(decimal.of("1"), decimal.of("2.5")))`, "-1.5"},
		{"times adds the places", `include decimal; decimal.str(decimal.times(decimal.of("1.05"), decimal.of("3")))`, "3.15"},
		{"times sheds what was only zeros", `include decimal
decimal.str(decimal.times(decimal.of("0.5"), decimal.of("0.2")))`, "0.1"},
		{"times is variadic too", `include decimal
decimal.str(decimal.times(decimal.of("2"), decimal.of("3"), decimal.of("4")))`, "24"},
		{"vat, the whole reason", `include decimal
price = decimal.of("1500.35")
decimal.str(decimal.plus(price, decimal.times(price, decimal.of("0.20"))), 2)`, "1800.42"},
		{"neg", `include decimal; decimal.str(decimal.neg(decimal.of("1.5")))`, "-1.5"},
		{"neg of zero is zero", `include decimal; decimal.str(decimal.neg(decimal.of(0)))`, "0"},
		{"abs", `include decimal; decimal.str(decimal.abs(decimal.of("-1.5")))`, "1.5"},
		{"sum of an array", `include decimal
decimal.str(decimal.sum(["0.1", "0.2", "0.3"].map { decimal.of(it) }))`, "0.6"},
		{"sum of nothing is zero", `include decimal; decimal.str(decimal.sum([]))`, "0"},
		{"sum takes ints and a range", `include decimal; decimal.str(decimal.sum((1..4)))`, "10"},
		{"cmp orders by value and not by digits", `include decimal
[decimal.cmp(decimal.of("9"), decimal.of("10")),
 decimal.cmp(decimal.of("1.50"), decimal.of("1.5")),
 decimal.cmp(decimal.of("2"), decimal.of("1.999"))].json`, "[-1,0,1]"},
		{"and cmp is what sort wants", `include decimal
["9.5", "10", "0.75"].map { decimal.of(it) }.sort { (a, b) -> decimal.cmp(a, b) }.map { decimal.str(it) }.json`,
			`["0.75","9.5","10"]`},
		{"float is the way out, and lossy", `include decimal; decimal.float(decimal.of("0.1")).str`, "0.1"},
		{"int truncates toward zero", `include decimal
[decimal.int(decimal.of("2.9")), decimal.int(decimal.of("-2.9"))].json`, "[2,-2]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestDecimalOperandRefusals is the second half of every row: an operand of the wrong kind
// is refused wherever it stands, not only in the first position.
func TestDecimalOperandRefusals(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, kind string }{
		{"plus, second operand", `include decimal; decimal.plus(decimal.of(1), 1.5)`, ErrKindType},
		{"plus, third operand", `include decimal; decimal.plus(decimal.of(1), 2, "3")`, ErrKindType},
		{"minus, first operand", `include decimal; decimal.minus("1", decimal.of(1))`, ErrKindType},
		{"minus, second operand", `include decimal; decimal.minus(decimal.of(1), nil)`, ErrKindType},
		{"times, second operand", `include decimal; decimal.times(decimal.of(1), [1])`, ErrKindType},
		{"div, first operand", `include decimal; decimal.div(1.5, decimal.of(1))`, ErrKindType},
		{"div, second operand", `include decimal; decimal.div(decimal.of(1), 1.5)`, ErrKindType},
		{"div, a mode that is not a string", `include decimal; decimal.div(decimal.of(1), decimal.of(2), 2, 7)`, ErrKindType},
		{"cmp, second operand", `include decimal; decimal.cmp(decimal.of(1), "1")`, ErrKindType},
		{"neg", `include decimal; decimal.neg("1")`, ErrKindType},
		{"abs", `include decimal; decimal.abs(true)`, ErrKindType},
		{"float", `include decimal; decimal.float("1")`, ErrKindType},
		{"int", `include decimal; decimal.int(1.5)`, ErrKindType},
		{"round, first operand", `include decimal; decimal.round("1", 2)`, ErrKindType},
		{"round, places that is not a number", `include decimal; decimal.round(decimal.of(1), "2")`, ErrKindType},
		{"str, first operand", `include decimal; decimal.str("1")`, ErrKindType},
		{"sum of something that is not an array", `include decimal; decimal.sum(1)`, ErrKindType},
		{"sum of an array with a stranger in it", `include decimal; decimal.sum([decimal.of(1), "2"])`, ErrKindType},
		{"split, first operand", `include decimal; decimal.split("10", 2, 2)`, ErrKindType},
		{"split, ways that is not a number", `include decimal; decimal.split(decimal.of(10), "2", 2)`, ErrKindType},
		{"split, places past the cap", `include decimal; decimal.split(decimal.of(10), 2, 19)`, ErrKindArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q; want %q (%s)", tt.src, e.Kind, tt.kind, e.Msg)
			}
		})
	}

	t.Run("and the arity of a member is checked by the module", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.minus(decimal.of(1))`, nil)
		if e.Kind != ErrKindArgument || !strings.Contains(e.Msg, "decimal.minus expects 2 argument(s), got 1") {
			t.Errorf("error = %s: %s; want the arity error", e.Kind, e.Msg)
		}
	})
}

// TestDecimalDiv is the row with a decision in it: without `places` the answer must be
// exact, and where there is no exact answer the module says so instead of picking a
// precision nobody asked for.
func TestDecimalDiv(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"an exact quotient needs no places", `include decimal
decimal.str(decimal.div(decimal.of("10"), decimal.of("4")))`, "2.5"},
		{"a denominator of 8 terminates", `include decimal
decimal.str(decimal.div(decimal.of(1), decimal.of(8)))`, "0.125"},
		{"so does 1/1024", `include decimal; decimal.str(decimal.div(decimal.of(1), decimal.of(1024)))`,
			"0.0009765625"},
		{"places round", `include decimal
decimal.str(decimal.div(decimal.of(1), decimal.of(3), 4), 4)`, "0.3333"},
		{"and round away from zero by default", `include decimal
decimal.str(decimal.div(decimal.of(2), decimal.of(3), 2), 2)`, "0.67"},
		{"half_even is available here too", `include decimal
decimal.str(decimal.div(decimal.of(5), decimal.of(2), 0, "half_even"))`, "2"},
		{"negative places round to hundreds", `include decimal
decimal.str(decimal.div(decimal.of("2500"), decimal.of(1), -3))`, "3000"},
		{"a negative divisor keeps the sign", `include decimal
decimal.str(decimal.div(decimal.of("1"), decimal.of("-4")))`, "-0.25"},
		{"zero over anything is zero", `include decimal
decimal.str(decimal.div(decimal.of(0), decimal.of(3)))`, "0"},
		{"nil places is the same as none", `include decimal
decimal.str(decimal.div(decimal.of(1), decimal.of(4), nil))`, "0.25"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("a third has no decimal form and the message names the fix", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.div(decimal.of(1), decimal.of(3))`, nil)
		if e.Kind != ErrKindDecimal {
			t.Errorf("kind = %q; want %q", e.Kind, ErrKindDecimal)
		}
		if !strings.Contains(e.Msg, "decimal.div(a, b, 2)") {
			t.Errorf("message = %q; want the fix in it", e.Msg)
		}
	})

	t.Run("and neither does a seventh", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.div(decimal.of(1), decimal.of(7))`, nil)
		if e.Kind != ErrKindDecimal {
			t.Errorf("kind = %q; want %q", e.Kind, ErrKindDecimal)
		}
	})

	t.Run("dividing by zero is the error dividing by zero always was", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.div(decimal.of(1), decimal.of(0))`, nil)
		if e.Kind != ErrKindZeroDiv {
			t.Errorf("kind = %q; want %q", e.Kind, ErrKindZeroDiv)
		}
	})
}

// TestDecimalRound pins both modes and the half that tells them apart. The default is
// half_up because that is what `round` already does to a number (§12.5).
func TestDecimalRound(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"half goes away from zero by default", `include decimal
decimal.str(decimal.round(decimal.of("2.665"), 2), 2)`, "2.67"},
		{"half_even keeps the even quotient", `include decimal
decimal.str(decimal.round(decimal.of("2.665"), 2, "half_even"), 2)`, "2.66"},
		{"and rounds up from an odd one", `include decimal
decimal.str(decimal.round(decimal.of("2.675"), 2, "half_even"), 2)`, "2.68"},
		{"below the half stays put", `include decimal
decimal.str(decimal.round(decimal.of("2.664"), 2), 2)`, "2.66"},
		{"a negative half goes away from zero as well", `include decimal
decimal.str(decimal.round(decimal.of("-2.5"), 0))`, "-3"},
		{"half_even on a negative", `include decimal
decimal.str(decimal.round(decimal.of("-2.5"), 0, "half_even"))`, "-2"},
		{"negative places round to hundreds", `include decimal
decimal.str(decimal.round(decimal.of("1250"), -2))`, "1300"},
		{"rounding up is exact, not floating", `include decimal
decimal.str(decimal.round(decimal.of("1.005"), 2), 2)`, "1.01"},
		{"the float it beats", `1.005.round(2).str`, "1.01"},
		{"rounding to more places changes nothing", `include decimal
decimal.str(decimal.round(decimal.of("1.5"), 6))`, "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("an unknown mode names the two there are", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.round(decimal.of(1), 2, "ceil")`, nil)
		if e.Kind != ErrKindArgument || !strings.Contains(e.Msg, `"half_up"`) ||
			!strings.Contains(e.Msg, `"half_even"`) {
			t.Errorf("error = %s: %s; want an argument error naming both modes", e.Kind, e.Msg)
		}
	})

	t.Run("places past the cap are refused", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.round(decimal.of(1), 19)`, nil)
		if e.Kind != ErrKindArgument || !strings.Contains(e.Msg, "-18..18") {
			t.Errorf("error = %s: %s; want an argument error naming the range", e.Kind, e.Msg)
		}
	})
}

// TestDecimalStr is the other half of the module's job: a number is one thing and how
// many places it is shown at is another.
func TestDecimalStr(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"canonical by default", `include decimal; decimal.str(decimal.of("1500.30"))`, "1500.3"},
		{"places pad", `include decimal; decimal.str(decimal.of("1500.30"), 2)`, "1500.30"},
		{"and a whole number pads too", `include decimal; decimal.str(decimal.of("10"), 2)`, "10.00"},
		{"places round", `include decimal; decimal.str(decimal.of("1.005"), 2)`, "1.01"},
		{"zero places is a whole number", `include decimal; decimal.str(decimal.of("1.5"), 0)`, "2"},
		{"a fraction keeps its leading zero", `include decimal; decimal.str(decimal.of("0.05"), 2)`, "0.05"},
		{"a negative fraction too", `include decimal; decimal.str(decimal.of("-0.05"), 2)`, "-0.05"},
		{"nil places is the same as none", `include decimal; decimal.str(decimal.of("1.50"), nil)`, "1.5"},
		{"it is a string and interpolates", `include decimal
"итого: ${decimal.str(decimal.of("1500.35"), 2)} ₽"`, "итого: 1500.35 ₽"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("negative places are for rounding, not for printing", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.str(decimal.of(1), -2)`, nil)
		if e.Kind != ErrKindArgument || !strings.Contains(e.Msg, "0..18") {
			t.Errorf("error = %s: %s; want an argument error naming the range", e.Kind, e.Msg)
		}
	})
}

// TestDecimalValueModel is what the dict form buys and what it costs (§7.4, §7.5, §7.8).
func TestDecimalValueModel(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"type names the shape", `include decimal; type(decimal.of("1.5"))`, "Decimal"},
		{"is answers by that name", `include decimal; decimal.of("1.5").is("Decimal")`, "true"},
		{"and it never stopped being a dict", `include decimal; decimal.of("1.5").is("dict")`, "true"},
		{"a plain dict is not one", `include decimal; decimal.of("1"); {units: 1, scale: 0}.is("Decimal")`, "false"},
		{"equality is the numeric question", `include decimal; decimal.of("1.50") == decimal.of("1.5")`, "true"},
		{"and it separates what differs", `include decimal; decimal.of("1.5") == decimal.of("1.6")`, "false"},
		{"a decimal against an int is not a number comparison",
			`include decimal; decimal.of("1") == 1`, "false"},
		{"json is the dict, which is why str is a row", `include decimal; decimal.of("1.5").json`,
			`{"units":15,"scale":1}`},
		{"the label is not content", `include decimal; decimal.of("1.5") == {units: 15, scale: 1}`, "true"},
		{"and a copy keeps it", `include decimal; type(decimal.of("1.5").dup)`, "Decimal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("ordering says so rather than guessing", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.of("9") < decimal.of("10")`, nil)
		if e.Kind != ErrKindType || !strings.Contains(e.Msg, "cannot compare dict with dict") {
			t.Errorf("error = %s: %s; want a type error", e.Kind, e.Msg)
		}
	})

	t.Run("a decimal is not a dict key", func(t *testing.T) {
		e := evErr(t, in, `include decimal; {(decimal.of("1.5")): true}`, nil)
		if !strings.Contains(e.Msg, "hashable") {
			t.Errorf("error = %s: %s; want the hashable-key diagnostic", e.Kind, e.Msg)
		}
	})
}

// TestRecordPlusRecordIsRefused is the §8.3 rule the decimal form needs and every other
// shape wanted: `+` on dicts merges, and merging two values of one shape answers with the
// right-hand one — which is never what `price + vat` meant.
func TestRecordPlusRecordIsRefused(t *testing.T) {
	in := evInterp()

	t.Run("two decimals", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.of("1500.35") + decimal.of("20")`, nil)
		if e.Kind != ErrKindType {
			t.Fatalf("kind = %q; want %q", e.Kind, ErrKindType)
		}
		for _, want := range []string{"cannot add Decimal to Decimal", "decimal.plus"} {
			if !strings.Contains(e.Msg, want) {
				t.Errorf("message = %q; want it to contain %q", e.Msg, want)
			}
		}
	})

	t.Run("two records of one shape", func(t *testing.T) {
		e := evErr(t, in, `record M(a, b); M(1, 2) + M(3, 4)`, nil)
		if e.Kind != ErrKindType || !strings.Contains(e.Msg, "cannot add M to M") {
			t.Errorf("error = %s: %s; want a type error naming the shape", e.Kind, e.Msg)
		}
		if !strings.Contains(e.Msg, "'merge'") {
			t.Errorf("message = %q; want 'merge' in it", e.Msg)
		}
	})

	t.Run("two shapes that differ", func(t *testing.T) {
		e := evErr(t, in, `record M(a); record N(b); M(1) + N(2)`, nil)
		if !strings.Contains(e.Msg, "cannot add N to M") {
			t.Errorf("message = %q; want both shapes named", e.Msg)
		}
	})

	tests := []struct{ name, src, want string }{
		{"a with-update is still a with-update", `record M(a, b); (M(1, 2) + {b: 9}).json`, `{"a":1,"b":9}`},
		{"and it keeps the shape", `record M(a, b); type(M(1, 2) + {b: 9})`, "M"},
		{"a plain dict on the left merges as ever", `record M(a, b); ({z: 0} + M(1, 2)).json`,
			`{"z":0,"a":1,"b":2}`},
		{"two plain dicts are untouched", `({a: 1} + {b: 2}).json`, `{"a":1,"b":2}`},
		{"merge itself still merges two shapes", `record M(a, b); M(1, 2).merge(M(3, 4)).json`,
			`{"a":3,"b":4}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestDecimalSplit is the kopeck nobody may lose.
func TestDecimalSplit(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"ten three ways", `include decimal
decimal.split(decimal.of("10"), 3, 2).map { decimal.str(it, 2) }.json`, `["3.34","3.33","3.33"]`},
		{"and the parts add back up", `include decimal
decimal.str(decimal.sum(decimal.split(decimal.of("10"), 3, 2)), 2)`, "10.00"},
		{"an even split has no remainder", `include decimal
decimal.split(decimal.of("10"), 4, 2).map { decimal.str(it, 2) }.json`,
			`["2.50","2.50","2.50","2.50"]`},
		{"one way is the whole thing", `include decimal
decimal.split(decimal.of("1.05"), 1, 2).map { decimal.str(it, 2) }.json`, `["1.05"]`},
		{"a negative total splits negatively", `include decimal
decimal.split(decimal.of("-10"), 3, 2).map { decimal.str(it, 2) }.json`,
			`["-3.34","-3.33","-3.33"]`},
		{"and still adds back up", `include decimal
decimal.str(decimal.sum(decimal.split(decimal.of("-10"), 3, 2)), 2)`, "-10.00"},
		{"whole units", `include decimal
decimal.split(decimal.of("10"), 3, 0).map { decimal.str(it) }.json`, `["4","3","3"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("splitting into fewer places than the value has is refused", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.split(decimal.of("10.005"), 2, 2)`, nil)
		if e.Kind != ErrKindDecimal || !strings.Contains(e.Msg, "round it first") {
			t.Errorf("error = %s: %s; want a decimal error naming the fix", e.Kind, e.Msg)
		}
	})

	t.Run("zero ways is an argument error", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.split(decimal.of("10"), 0, 2)`, nil)
		if e.Kind != ErrKindArgument {
			t.Errorf("kind = %q; want %q (%s)", e.Kind, ErrKindArgument, e.Msg)
		}
	})

	t.Run("a split past MaxCollection is a limit, not a hang", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.split(decimal.of("10"), 100_000_000, 2)`, nil)
		if e.Kind != ErrKindLimit {
			t.Errorf("kind = %q; want %q (%s)", e.Kind, ErrKindLimit, e.Msg)
		}
	})
}

// TestDecimalLimits is the width of the value: the digits live in an Int, so the module
// has an edge, and the edge is an error rather than the silent promotion D9 gives Int.
func TestDecimalLimits(t *testing.T) {
	in := evInterp()

	t.Run("the largest int is a decimal", func(t *testing.T) {
		if got := evStr(t, in, `include decimal
decimal.str(decimal.of("9223372036854775807"))`); got != "9223372036854775807" {
			t.Errorf("= %s; want the largest int back", got)
		}
	})

	t.Run("one past it is an error and not a float", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.of("9223372036854775808")`, nil)
		if e.Kind != ErrKindDecimal || !strings.Contains(e.Msg, "does not fit") {
			t.Errorf("error = %s: %s; want a decimal error", e.Kind, e.Msg)
		}
	})

	t.Run("an overflowing product is an error, where Int would promote", func(t *testing.T) {
		e := evErr(t, in, `include decimal
decimal.times(decimal.of("9223372036854775807"), decimal.of(10))`, nil)
		if e.Kind != ErrKindDecimal {
			t.Errorf("kind = %q; want %q (%s)", e.Kind, ErrKindDecimal, e.Msg)
		}
	})

	t.Run("and the Int that does promote", func(t *testing.T) {
		if got := evStr(t, in, `type(9223372036854775807 * 10)`); got != "float" {
			t.Errorf("type = %s; want float — D9 promotes and that is the whole point", got)
		}
	})

	t.Run("a long string of digits is refused by its count, not by converting it", func(t *testing.T) {
		// big.Int's SetString is superlinear, and a string may be megabytes (§14.2): the
		// digits have to be counted before anything is parsed, or one call outlasts the
		// deadline that is supposed to bound the Run (§14.1).
		e := evErr(t, in, `include decimal; decimal.of("9" * 2_000_000)`, nil)
		if e.Kind != ErrKindDecimal || !strings.Contains(e.Msg, "2000000 digits before the dot") {
			t.Errorf("error = %s: %s; want the digit-count refusal", e.Kind, e.Msg)
		}
		if len(e.Msg) > 200 {
			t.Errorf("message is %d bytes; a diagnostic quotes back a person-sized excerpt", len(e.Msg))
		}
	})

	t.Run("leading zeros are free", func(t *testing.T) {
		if got := evStr(t, in, `include decimal
decimal.str(decimal.of("0" * 4_000 + "1.5"))`); got != "1.5" {
			t.Errorf("= %s; want 1.5", got)
		}
	})

	t.Run("a product needing more than 18 places is an error", func(t *testing.T) {
		e := evErr(t, in, `include decimal
decimal.times(decimal.of("0.0000000001"), decimal.of("0.0000000001"))`, nil)
		if e.Kind != ErrKindDecimal || !strings.Contains(e.Msg, "decimal places") {
			t.Errorf("error = %s: %s; want the places cap", e.Kind, e.Msg)
		}
	})

	t.Run("the whole part of the widest decimal is still an int", func(t *testing.T) {
		if got := evStr(t, in, `include decimal
decimal.int(decimal.of("9223372036854775807")).str`); got != "9223372036854775807" {
			t.Errorf("= %s; want the largest int", got)
		}
	})
}

// TestDecimalModuleShape pins what `include decimal` binds: a module like any other
// (§12.8), with no host capability behind it and no callable half.
func TestDecimalModuleShape(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"the members, in registration order", `include decimal; decimal.keys.json`,
			`["of","plus","minus","times","div","neg","abs","cmp","round","str","float","int","sum","split"]`},
		{"it is a dict", `include decimal; type(decimal)`, "dict"},
		{"and it is reachable with no option set", `defined(decimal)`, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	t.Run("a module is never callable", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal("1.5")`, nil)
		if !strings.Contains(e.Msg, "is a module, not a function") {
			t.Errorf("message = %q; want the module diagnostic", e.Msg)
		}
	})

	t.Run("a member it does not have is a name error", func(t *testing.T) {
		e := evErr(t, in, `include decimal; decimal.pow(decimal.of(2), 2)`, nil)
		if e.Kind != ErrKindName {
			t.Errorf("kind = %q; want %q (%s)", e.Kind, ErrKindName, e.Msg)
		}
	})

	t.Run("without the include the name is not there", func(t *testing.T) {
		e := evErr(t, in, `decimal.of("1.5")`, nil)
		if !strings.Contains(e.Msg, "include decimal") {
			t.Errorf("message = %q; want the include in it", e.Msg)
		}
	})
}
