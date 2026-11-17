package mzs

import (
	"context"
	"strings"
	"testing"
)

// RFC 0016, part 1: the dict rows arrays already had, and the array rows for statistics,
// reshaping and running folds. Each case is a script and the Inspect of its value.

func evalRow(t *testing.T, src string) (string, error) {
	t.Helper()
	v, err := New(Options{}).Eval(context.Background(), src, nil)
	if err != nil {
		return "", err
	}
	return v.Inspect(), nil
}

func TestDictCollectionRows(t *testing.T) {
	t.Parallel()

	const d = `d = {a: 1, b: 2, c: 3}; `
	cases := []struct{ src, want string }{
		{d + `d.reduce(0) { (acc, _, v) -> acc + v }`, `6`},
		{d + `d.reduce("") { (acc, k, _) -> acc + k }`, `"abc"`},
		{`{}.reduce(7) { (acc, _, _) -> acc + 1 }`, `7`},
		{d + `d.count { (_, v) -> v > 1 }`, `2`},
		{`{}.count { (_, _) -> true }`, `0`},
		{d + `d.none { (_, v) -> v > 5 }`, `true`},
		{d + `d.none { (k, _) -> k == "b" }`, `false`},
		{`{}.none { (_, _) -> true }`, `true`},
		{d + `d.sum { (_, v) -> v * 2 }`, `12`},
		{d + `d.sum { (_, v) -> v / 2.0 }`, `3.0`},
		{`{}.sum { (_, v) -> v }`, `0`},
		{d + `d.min_by { (_, v) -> -v }`, `["c",3]`},
		{d + `d.max_by { (_, v) -> v }`, `["c",3]`},
		{`{a: 1, b: 1}.max_by { (_, v) -> v }`, `["a",1]`},
		{`{}.min_by { (_, v) -> v }`, `nil`},
		{d + `d.group_by { (_, v) -> v % 2 == 0 ? "even" : "odd" }`, `{"odd":{"a":1,"c":3},"even":{"b":2}}`},
		{d + `d.partition { (_, v) -> v > 1 }`, `[{"b":2,"c":3},{"a":1}]`},
		{`{}.partition { (_, _) -> true }`, `[{},{}]`},
		{d + `d.map_values { it * 10 }`, `{"a":10,"b":20,"c":30}`},
		{d + `d.pick("c", "a", "z")`, `{"a":1,"c":3}`},
		{d + `d.pick()`, `{}`},
		{d + `d.omit("a", "z")`, `{"b":2,"c":3}`},
		{d + `d.omit()`, `{"a":1,"b":2,"c":3}`},
		{`{1 -> "x", "1" -> "y"}.pick(1).values`, `["x"]`},
	}
	for _, tc := range cases {
		got, err := evalRow(t, tc.src)
		if err != nil || got != tc.want {
			t.Errorf("%s = %s, %v; want %s", tc.src, got, err, tc.want)
		}
	}
}

// map_values keeps the shape, so a record stays the record it was; pick and omit change
// the shape, so they give a plain dict. d.map { … }.dict is the contrast the row exists for.
func TestMapValuesKeepsARecord(t *testing.T) {
	t.Parallel()

	src := `record Money(amount, currency = "EUR")
m = Money(5)
[type(m.map_values { it }), type(m.map { (k, v) -> [k, v] }.dict), type(m.pick("amount")), type(m.omit("amount")), m.map_values { it }.amount]`
	got, err := evalRow(t, src)
	if err != nil || got != `["Money","dict","dict","dict",5]` {
		t.Errorf("got %s, %v", got, err)
	}
}

// A dict's closure sees the entries as they were when the row started: writing to the
// dict inside it does not change what this walk visits.
func TestDictRowsWalkASnapshot(t *testing.T) {
	t.Parallel()

	got, err := evalRow(t, `d = {a: 1}; n = d.count { (k, _) -> d["z"] = 1; true }; [n, d.len]`)
	if err != nil || got != `[1,2]` {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestDictRowErrors(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`{a: 1}.reduce { (acc, _, _) -> acc }`, "expects at least 2 argument(s)"},
		{`{a: 1}.sum { (_, _) -> "x" }`, "sum expects numbers, got int and string"},
		{`{a: 1, b: "x"}.min_by { (_, v) -> v }`, "comparison of string with int failed"},
		{`{a: 1}.pick([1])`, "pick:"},
		{`{a: 1}.group_by { (_, _) -> [1] }`, "group_by: the closure returned array, which cannot be a dict key"},
	}
	for _, tc := range cases {
		_, err := evalRow(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.src, err, tc.want)
		}
	}
}

func TestArrayStatisticsAndFolds(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`[1, 2, 3, 4].mean`, `2.5`},
		{`[1, 2, 3].mean`, `2.0`},
		{`[].mean`, `nil`},
		// A sum that overflows does not make a mean that does.
		{`[1.7e308, 1.7e308].mean`, `1.7e+308`},
		// n quotients x/n can each round up and add past max; scaling by 2^-k cannot.
		{`m = 1.7976931348623157e308; [m, m, m].mean == m`, `true`},
		{`m = 1.7976931348623157e308; (0..<99).map { m }.mean == m`, `true`},
		// Large values that cancel leave a subnormal residue; the exact fallback keeps it.
		{`m = 1.7976931348623157e308; [m, m, -m, -m, 5e-323].mean == 1e-323`, `true`},
		{`m = 1.7976931348623157e308; [m, m, -m, 3.0].mean == m / 4 + 0.75`, `true`},
		{`[1.0e308, -1.0e308, 2.0].mean`, `0.6666666666666666`},
		{`(1..4).mean`, `2.5`},
		{`[3, 1, 2].median`, `2`},
		{`[4, 1, 3, 2].median`, `2.5`},
		{`[1.5].median`, `1.5`},
		{`[].median`, `nil`},
		// Sorted as numbers compare, not as float64s: past 2^53 those collapse.
		{`n = 2 ** 53; [n + 1, n, n + 2].median == n + 1`, `true`},
		// The two middles are halved before they are added, so they cannot overflow.
		{`[1.7e308, 1.7e308].median`, `1.7e+308`},
		{`[2, 3, 4].product`, `24`},
		{`[2, 0.5].product`, `1.0`},
		{`["a", "bb"].product { it.len }`, `2`},
		{`[].product`, `1`},
		{`[2 ** 40, 2 ** 40].product`, `1.2089258196146292e+24`},
		{`[[1, 2], [3, 4], [5, 6]].transpose`, `[[1,3,5],[2,4,6]]`},
		{`[].transpose`, `[]`},
		{`[[], []].transpose`, `[]`},
		{`[1, 2, 3, 4].rotate`, `[2,3,4,1]`},
		{`[1, 2, 3, 4].rotate(2)`, `[3,4,1,2]`},
		{`[1, 2, 3, 4].rotate(-1)`, `[4,1,2,3]`},
		{`[1, 2, 3].rotate(7)`, `[2,3,1]`},
		{`[].rotate(3)`, `[]`},
		{`xs = [1, 2]; ys = xs.rotate; [xs, ys]`, `[[1,2],[2,1]]`},
		{`[1, 2, 4, 5, 7].chunk_while { (a, b) -> b == a + 1 }`, `[[1,2],[4,5],[7]]`},
		{`[1].chunk_while { (_a, _b) -> false }`, `[[1]]`},
		{`[].chunk_while { (_a, _b) -> true }`, `[]`},
		{`[1, 2, 3, 4].accumulate { (a, b) -> a + b }`, `[1,3,6,10]`},
		{`[1, 2, 3].accumulate(10) { (a, b) -> a + b }`, `[11,13,16]`},
		{`[].accumulate(10) { (a, b) -> a + b }`, `[]`},
		{`xs = [5, 1, 4]; [xs.accumulate { (a, b) -> max(a, b) }.last, xs.reduce { (a, b) -> max(a, b) }]`, `[5,5]`},
		{`[1, 3, 5, 7, 9].bsearch { it <=> 7 }`, `3`},
		{`[1, 3, 5, 7, 9].bsearch { it <=> 1 }`, `0`},
		{`[1, 3].bsearch { it <=> 2 }`, `nil`},
		{`[].bsearch { it <=> 2 }`, `nil`},
		{`["ant", "bee", "cat"].bsearch { it <=> "cat" }`, `2`},
		// A range is probed in place: ten million elements is past MaxCollection.
		{`(1..10_000_000).bsearch { it <=> 7_777_777 }`, `7777776`},
		{`(1..<1).bsearch { it <=> 1 }`, `nil`},
	}
	for _, tc := range cases {
		got, err := evalRow(t, tc.src)
		if err != nil || got != tc.want {
			t.Errorf("%s = %s, %v; want %s", tc.src, got, err, tc.want)
		}
	}
}

func TestArrayRowErrors(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`[1, "a"].mean`, "mean expects numbers, got string"},
		{`["a"].median`, "median expects numbers, got string"},
		{`[1, "a"].product`, "product expects numbers"},
		{`[[1], [2, 3]].transpose`, "row 1 has 2 elements and row 0 has 1; a ragged table has no transpose"},
		{`[1, 2].transpose`, "expects an array of arrays, got int at index 0"},
		{`[1].rotate("x")`, "rotate expects an int, got string"},
		{`[1, 2].bsearch { true }`, "the closure returns an int (negative, 0 or positive), got bool"},
		{`[1, 2].avg`, "did you mean 'mean'?"},
		{`[1, 2].average`, "did you mean 'mean'?"},
	}
	for _, tc := range cases {
		_, err := evalRow(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.src, err, tc.want)
		}
	}
}

// bsearch costs a step per probe, not per element, so a long sorted array is cheap to
// search; transpose is charged for every cell it copies.
func TestRowCharges(t *testing.T) {
	t.Parallel()

	in := New(Options{StepBudget: 2000})
	if _, err := in.Eval(context.Background(), `xs = (0..<1000).array; xs.bsearch { it <=> 999 }`, nil); err != nil {
		t.Errorf("bsearch over 1000 elements under 2000 steps: %v", err)
	}
	steps := func(src string) int64 {
		in := New(Options{})
		p, err := in.Compile("t", src)
		if err != nil {
			t.Fatal(err)
		}
		res, err := in.RunResult(context.Background(), p, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Steps
	}
	const table = `t = [(0..<500).array, (0..<500).array, (0..<500).array]; `
	if d := steps(table+`t.transpose`) - steps(table+`t`); d < 1500 {
		t.Errorf("transposing 1500 cells cost %d steps; want at least one per cell", d)
	}
}

// A closure that raises stops the row and the error reaches the script unchanged, from
// every row that calls one — the first entry it is given is enough.
func TestRowsPassAClosureErrorThrough(t *testing.T) {
	t.Parallel()

	const boom = `{ (*_) -> raise("boom") }`
	srcs := []string{
		`{a: 1}.reduce(0) ` + boom,
		`{a: 1}.count ` + boom,
		`{a: 1}.none ` + boom,
		`{a: 1}.sum ` + boom,
		`{a: 1}.min_by ` + boom,
		`{a: 1}.max_by ` + boom,
		`{a: 1}.group_by ` + boom,
		`{a: 1}.partition ` + boom,
		`{a: 1}.map_values ` + boom,
		`[1, 2].product ` + boom,
		`[1, 2].chunk_while ` + boom,
		`[1, 2].accumulate ` + boom,
		`[1, 2].accumulate(0) ` + boom,
		`[1, 2].bsearch ` + boom,
	}
	for _, src := range srcs {
		got, err := evalRow(t, `try `+src+` else (e) -> e["message"]`)
		if err != nil || got != `"boom"` {
			t.Errorf("%s: got %s, %v; want the closure's error", src, got, err)
		}
	}
}

// A row that is not given a dict says so, whichever row it is.
func TestDictRowsRefuseAnArrayThroughUFCS(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"reduce", "count", "none", "sum", "min_by", "max_by", "group_by", "partition", "map_values", "pick", "omit"} {
		c := colCtx(t, DefaultOptions())
		_, err := colInvoke(c, KDict, name, colInts(1, 2), dictRowArgs[name]...)
		if err == nil || !strings.Contains(err.Error(), "expects a dict") {
			t.Errorf("%s on an array: %v", name, err)
		}
	}
}
