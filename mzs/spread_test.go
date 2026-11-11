package mzs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// RFC 0001: rest elements, splats at a call, and spreads in array and dict literals.

func spreadEval(t *testing.T, o Options, src string) (Value, error) {
	t.Helper()
	return New(o).Eval(context.Background(), src, nil)
}

// spreadCases runs src and compares the result's inspect form.
func spreadCases(t *testing.T, cases [][2]string) {
	t.Helper()
	for _, c := range cases {
		v, err := spreadEval(t, DefaultOptions(), c[0])
		if err != nil {
			t.Errorf("%s: %v", c[0], err)
			continue
		}
		if got := v.Inspect(); got != c[1] {
			t.Errorf("%s = %s, want %s", c[0], got, c[1])
		}
	}
}

// spreadErrors runs src and wants an error of kind with msg in it.
func spreadErrors(t *testing.T, cases [][3]string) {
	t.Helper()
	for _, c := range cases {
		_, err := spreadEval(t, DefaultOptions(), c[0])
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: error = %v, want an *Error", c[0], err)
			continue
		}
		if e.Kind != c[1] || !strings.Contains(e.Msg, c[2]) {
			t.Errorf("%s: error = %s: %s, want %s: …%s…", c[0], e.Kind, e.Msg, c[1], c[2])
		}
	}
}

func TestRestTargets(t *testing.T) {
	spreadCases(t, [][2]string{
		{`h, *t = [1, 2, 3]; [h, t]`, `[1,[2,3]]`},
		{`*i, l = [1, 2, 3]; [i, l]`, `[[1,2],3]`},
		{`a, *m, z = [1, 2, 3, 4, 5]; [a, m, z]`, `[1,[2,3,4],5]`},
		{`a, *m, z = [1, 2]; [a, m, z]`, `[1,[],2]`},
		{`h, *t = [1]; [h, t]`, `[1,[]]`},
		{`[*all] = [1, 2]; all`, `[1,2]`},
		{`a, *m, z = 1..5; [a, m, z]`, `[1,[2,3,4],5]`},
		{`[a, [b, *c], *d] = [1, [2, 3, 4], 5, 6]; [a, b, c, d]`, `[1,2,[3,4],[5,6]]`},
		// The rest is a new array: writing to it cannot reach the one taken apart.
		{`xs = [1, 2, 3]; h, *t = xs; t.push(9); [xs, t]`, `[[1,2,3],[2,3,9]]`},
		// `=` writes an outer binding, `:=` declares a new one, exactly as for one name.
		{`t = 0; fn f() { h, *t = [1, 2]; t }; [f(), t]`, `[[2],[2]]`},
		{`t = 0; fn f() { h, *t := [1, 2]; t }; [f(), t]`, `[[2],0]`},
		// The value of a destructuring is its right side.
		{`(h, *t = [1, 2])`, `[1,2]`},
	})
	spreadErrors(t, [][3]string{
		{`a, *m, z = [1]`, ErrKindIndex, "destructuring expects at least 2 values, got 1"},
		{`a, b = [1, 2, 3]`, ErrKindIndex, "destructuring expects 2 values, got 3"},
		{`h, *t = 5`, ErrKindType, "cannot destructure int"},
	})
}

// A `*` that starts a line begins a target list rather than continuing the line before;
// anywhere else a line starting with `*` still continues (§3.10).
func TestRestStartsALine(t *testing.T) {
	spreadCases(t, [][2]string{
		{"h = 1\n*i, l = [1, 2, 3]\n[h, i, l]", `[1,[1,2],3]`},
		{"x = 2\ny = x\n  * 3\ny", `6`},
		{"fn f(a, c = 0) { [a, c] }\nf(2\n  * 3, c = 1)", `[6,1]`},
		{"[1].map { (x) ->\n  y = x\n  *a, b = [y, 2]\n  a\n}", `[[1]]`},
		// A break after a comma was never a statement boundary: the list goes on.
		{"a,\n*rest = [1, 2, 3]\n[a, rest]", `[1,[2,3]]`},
		{"h = 0\na,\n  *rest = [1, 2]\n[h, a, rest]", `[0,1,[2]]`},
	})
}

func TestRestInMatch(t *testing.T) {
	spreadCases(t, [][2]string{
		{`match ["GET", "a", "b"] { ["GET", *path] -> path; else -> nil }`, `["a","b"]`},
		{`match ["POST", "a"] { ["GET", *path] -> path; else -> "other" }`, `"other"`},
		{`match [1, 2, 3] { [first, *] -> first }`, `1`},
		{`match [1] { [first, *rest] -> rest }`, `[]`},
		{`match [] { [first, *] -> first; else -> "empty" }`, `"empty"`},
		{`match [1, 2, 3, 4] { [a, *mid, z] -> [a, mid, z] }`, `[1,[2,3],4]`},
		{`match [1, [2, 3, 4]] { [a, [b, *c]] -> c }`, `[3,4]`},
		// The guard sees the rest, and an arm whose guard fails falls through.
		{`match [1, 2, 3] { [h, *t] if t.len > 5 -> "long"; [h, *t] -> t.len }`, `2`},
		{`match 1..4 { [h, *t] -> t }`, `[2,3,4]`},
		{`match "abc" { [h, *t] -> t; else -> "not an array" }`, `"not an array"`},
	})
}

func TestSplatAtACall(t *testing.T) {
	spreadCases(t, [][2]string{
		{`fn f(a, b, c) { [a, b, c] }; f(*[1, 2, 3])`, `[1,2,3]`},
		{`fn f(a, b, c) { [a, b, c] }; f(1, *[2], 3)`, `[1,2,3]`},
		{`fn f(a, b, c) { [a, b, c] }; f(*[1], *(2..3))`, `[1,2,3]`},
		{`fn f(*n) { n }; f(*[1, 2], 3, *[])`, `[1,2,3]`},
		{`fn f(a, b = 2, c = 3) { [a, b, c] }; f(1, **{c: 9})`, `[1,2,9]`},
		{`fn f(a, b = 2, c = 3) { [a, b, c] }; f(**{c: 9, a: 1})`, `[1,2,9]`},
		{`fn f(a, b = 2, c = 3) { [a, b, c] }; f(*[1], b = 5, **{c: 9})`, `[1,5,9]`},
		{`fn f(a, b = 2) { [a, b] }; f(1, **{})`, `[1,2]`},
		// Builtins, stdlib rows and closures take the expanded positions like any other.
		{`max(*[3, 9, 2])`, `9`},
		{`xs = [1]; xs.push(*[2, 3]); xs`, `[1,2,3]`},
		{`add = { (a, b) -> a + b }; add(*[1, 2])`, `3`},
		{`"%s-%s" % [*["a", "b"]]`, `"a-b"`},
	})
	// Left to right, positions before names, once each.
	spreadCases(t, [][2]string{
		{`log = []
fn note(x) { log.push(x); x }
fn f(a, b, c = 0) { 0 }
f(note(1), *note([2]), **note({c: 3}))
log`, `[1,[2],{"c":3}]`},
	})
	spreadErrors(t, [][3]string{
		{`fn f(a) { a }; f(*5)`, ErrKindType, "cannot splat int; '*' takes an array or a range"},
		{`fn f(a) { a }; f(*"ab")`, ErrKindType, "cannot splat string"},
		{`fn f(a) { a }; f(*(1..3).seq)`, ErrKindType, "cannot splat seq: it may be endless"},
		{`fn f(a) { a }; f(**[1])`, ErrKindType, "cannot splat array as named arguments; '**' takes a dict"},
		{`fn f(a) { a }; f(**{a -> 1, 2 -> 3})`, ErrKindType, "is not a string"},
		{`fn f(a, b) { a }; f(*[1, 2, 3])`, ErrKindArgument, "f expects 2 argument(s), got 3"},
		{`fn f(a) { a }; f(**{b: 1})`, ErrKindArgument, "f has no parameter named 'b'"},
		{`fn f(a, b) { a }; f(**{a: 1}, a = 2)`, ErrKindArgument, "argument 'a' is given twice"},
		{`fn f(a, b) { a }; f(**{a: 1}, **{a: 2})`, ErrKindArgument, "argument 'a' is given twice"},
		{`fn f(a, b) { a }; f(*[1], **{a: 2})`, ErrKindArgument, "got two values for parameter 'a'"},
		{`len(**{a: 1})`, ErrKindArgument, "takes its arguments by position"},
		{`xs = [1]; defined(*xs)`, ErrKindArgument, "defined takes a name to test, not a spread"},
	})
}

func TestSpreadInArrays(t *testing.T) {
	spreadCases(t, [][2]string{
		{`a = [1, 2]; [*a, 3, *a]`, `[1,2,3,1,2]`},
		{`[*(1..3), *[]]`, `[1,2,3]`},
		{`[*[]]`, `[]`},
		{`a = [[1]]; b = [*a]; b.push(2); [a, b]`, `[[[1]],[[1],2]]`},
		{`a = [1]; "${[*a, 2]}"`, `"[1,2]"`},
		// A spread is a snapshot of its operand when its position is reached.
		{`a = [1, 2]; [*a, (a[0] = 9)]`, `[1,2,9]`},
		{`a = [1, 2]; [(a[0] = 9), *a]`, `[9,9,2]`},
		{`a = [1, 2]; fn f(*n) { n }; f(*a, (a[0] = 9))`, `[1,2,9]`},
	})
	spreadErrors(t, [][3]string{
		{`[*5]`, ErrKindType, "cannot splat int"},
		{`[*{a: 1}]`, ErrKindType, "cannot splat dict"},
		{`[*nil]`, ErrKindType, "cannot splat nil"},
	})
	// The limit is checked on what the literal lays out, not on what it says.
	o := DefaultOptions()
	o.MaxCollection = 4
	_, err := spreadEval(t, o, `a = [1, 2, 3]; [*a, *a]`)
	if !errors.Is(err, ErrBudget) {
		t.Errorf("[*a, *a] over MaxCollection = %v, want ErrBudget", err)
	}
	_, err = spreadEval(t, o, `fn f(*n) { n }; f(*(1..10))`)
	if !errors.Is(err, ErrBudget) {
		t.Errorf("a splat over MaxCollection = %v, want ErrBudget", err)
	}
}

func TestSpreadInDicts(t *testing.T) {
	spreadCases(t, [][2]string{
		{`d = {a: 1, b: 2}; {**d, c: 3}`, `{"a":1,"b":2,"c":3}`},
		// A later entry overwrites the value in the earlier entry's place (D11).
		{`d = {a: 1, b: 2}; {**d, a: 9}`, `{"a":9,"b":2}`},
		{`d = {a: 1, b: 2}; {a: 9, **d}`, `{"a":1,"b":2}`},
		{`{**{a: 1}, b: 2, **{a: 3, c: 4}}`, `{"a":3,"b":2,"c":4}`},
		{`{**{}}`, `{}`},
		{`{**{1 -> "x"}, k: 2}`, `{"1":"x","k":2}`},
		// A copy: changing the result leaves the source alone.
		{`d = {a: 1}; e = {**d}; e["b"] = 2; [d, e]`, `[{"a":1},{"a":1,"b":2}]`},
	})
	// A literal carries no record label, whatever its parts carried.
	spreadCases(t, [][2]string{
		{`record P(x, y)
p = P(1, 2)
q = {**p, y: 5}
[type(p), type(q), q]`, `["P","dict",{"x":1,"y":5}]`},
	})
	spreadErrors(t, [][3]string{
		{`{**[1]}`, ErrKindType, "cannot spread array into a dict; '**' takes a dict"},
		{`{a: 1, **nil}`, ErrKindType, "cannot spread nil into a dict"},
	})
}

// A bare `*` skips positions in a match pattern and nowhere else.
func TestBareStarOutsideAPattern(t *testing.T) {
	for _, src := range []string{`[1, *]`, `fn f(*n) { n }; f(*)`} {
		_, err := New(Options{}).Compile("t.mzs", src)
		if err == nil || !strings.Contains(err.Error(), "a bare '*' skips positions only in a match pattern") {
			t.Errorf("Compile(%s) = %v", src, err)
		}
	}
}

// An operand that raises stops the literal or the call where it stands, with its own
// error.
func TestSpreadOperandErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		{`fn bad() { raise("no", "app") }; [1, *bad()]`, "app", "no"},
		{`fn bad() { raise("no", "app") }; [bad(), *[1]]`, "app", "no"},
		{`fn bad() { raise("no", "app") }; {**bad()}`, "app", "no"},
		{`fn bad() { raise("no", "app") }; fn f(*a) { a }; f(*bad())`, "app", "no"},
		{`fn bad() { raise("no", "app") }; fn f(*a) { a }; f(bad(), *[1])`, "app", "no"},
		{`fn bad() { raise("no", "app") }; fn f(a = 0) { a }; f(**bad())`, "app", "no"},
		{`fn bad() { raise("no", "app") }; fn f(a = 0) { a }; f(*[], a = bad())`, "app", "no"},
		{`fn bad() { raise("no", "app") }; h, *t = bad()`, "app", "no"},
	})
	// The limit holds while the operands are still being laid out, and for a rest.
	o := DefaultOptions()
	o.MaxCollection = 3
	for _, src := range []string{
		`[*(1..2), *(1..2)]`,
		`[*(1..9)]`,
		`h, *t = 1..9`,
		`match 1..9 { [h, *t] -> t }`,
		`{**{a: 1, b: 2}, **{c: 3, d: 4}}`,
		// The positions written after a splat count toward the list the callee gets.
		`fn f(*n) { n }; f(*[1, 2, 3], 4)`,
		// So do the entries a `**` spreads into named arguments.
		`fn f(a = 0) { a }; f(**{a: 1, b: 2, c: 3, d: 4})`,
		// And a written key that a spread has left no room for.
		`{**{a: 1, b: 2, c: 3}, d: 4}`,
	} {
		if _, err := spreadEval(t, o, src); !errors.Is(err, ErrBudget) {
			t.Errorf("%s under MaxCollection 3 = %v, want ErrBudget", src, err)
		}
	}
	// What overlaps costs no room: the limit is on the result, not on the operands.
	for _, src := range []string{
		`d = {a: 1, b: 2, c: 3}; {**d, **d}`,
		`d = {a: 1, b: 2, c: 3}; {**d, a: 9, **d}`,
		`{a: 0, **{a: 1, b: 2, c: 3}}`,
	} {
		v, err := spreadEval(t, o, src)
		if err != nil || v.Len() != 3 {
			t.Errorf("%s under MaxCollection 3 = %s, %v; want three entries", src, v.Inspect(), err)
		}
	}
}

// A spread of a large dict pays a step per entry, so the step budget sees it.
func TestNamedSpreadChargesSteps(t *testing.T) {
	steps := func(src string) int64 {
		t.Helper()
		in := New(DefaultOptions())
		p, err := in.Compile("t.mzs", src)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		r, err := in.RunResult(context.Background(), p, nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return r.Steps
	}
	const setup = "d = {}\n(1..1000).each { d[\"k${it}\"] = it }\nfn f(*a) { a }\n"
	plain := steps(setup + "f()")
	spread := steps(setup + "try f(**d) else nil")
	if spread-plain < 1000 {
		t.Errorf("f(**d) over 1000 entries cost %d more steps, want at least 1000", spread-plain)
	}
}

// A range spread is paid for before it is materialised, so MaxMemory stops it.
func TestRangeSpreadIsCharged(t *testing.T) {
	o := DefaultOptions()
	o.MaxMemory = 1 << 20
	for _, src := range []string{`[*(1..500000)]`, `fn f(*n) { n.len }; f(*(1..500000))`} {
		if _, err := spreadEval(t, o, src); !errors.Is(err, ErrMemory) {
			t.Errorf("%s under a 1 MiB budget = %v, want ErrMemory", src, err)
		}
	}
}
