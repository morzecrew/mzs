package mzs

import (
	"context"
	"errors"
	"testing"
)

// RFC 0002: dict patterns on the left of `=`, in `match` arms, nested in array patterns,
// and as `for` loop variables.

func TestDictTargets(t *testing.T) {
	spreadCases(t, [][2]string{
		{`{name, age: a} = {name: "Ann", age: 41}; [name, a]`, `["Ann",41]`},
		// A pattern is open: keys it does not name are ignored.
		{`{a} = {a: 1, b: 2, c: 3}; a`, `1`},
		{`{"content-type": ct} = {"content-type": "text/plain"}; ct`, `"text/plain"`},
		// `**rest` collects what no key names, as a new dict in the source's order.
		{`{b, **rest} = {a: 1, b: 2, c: 3}; [b, rest]`, `[2,{"a":1,"c":3}]`},
		{`{**all} = {a: 1}; all`, `{"a":1}`},
		{`{a, **rest} = {a: 1}; rest`, `{}`},
		// Nesting, both ways round.
		{`{data: {reason}} = {data: {reason: "late"}}; reason`, `"late"`},
		{`{pos: [x, y]} = {pos: [3, 4]}; x * y`, `12`},
		{`[id, {name}] = [7, {name: "x"}]; [id, name]`, `[7,"x"]`},
		{`id, {name} = [7, {name: "x"}]; [id, name]`, `[7,"x"]`},
		{`{a: [h, *t]} = {a: [1, 2, 3]}; [h, t]`, `[1,[2,3]]`},
		// The targets an array pattern takes: a $var and an index.
		{`{a: $g} = {a: 5}; $g`, `5`},
		{`xs = [0, 0]; {a: xs[0], b: xs[1]} = {a: 1, b: 2}; xs`, `[1,2]`},
		// Every value is read before the first is written, so a target writing into the
		// dict being taken apart cannot change what a later key reads — at any depth.
		{`d = {a: 1, b: 2}; {a: d["b"], b: x} = d; [d, x]`, `[{"a":1,"b":1},2]`},
		{`d = {a: {x: 1}, b: {y: 2}}; {a: {x: d["b"]["y"]}, b: {y: got}} = d; [d, got]`, `[{"a":{"x":1},"b":{"y":1}},2]`},
		{`xs = [[1], [2]]; [[xs[1][0]], [got]] = xs; [xs, got]`, `[[[1],[1]],2]`},
		{`xs = [1, 2]; xs[1], xs[0] = xs; xs`, `[2,1]`},
		// A shape that does not fit writes nothing, not the targets to its left.
		{`a = 0; try ([a, [b, c]] = [1, 2]) else nil; a`, `0`},
		{`a = 0; try ({a, z} = {a: 1}) else nil; a`, `0`},
		// The value of the assignment is the right side, and the assignment is an
		// expression wherever one may stand, as the bracketed array form is.
		{`d = {a: 1}; ({a} = d) == d`, `true`},
		{`fn f(x) { x }; d = {a: 1}; [f({a} = d), a]`, `[{"a":1},1]`},
		{`fn f(x) { x }; f([p, {q}] = [1, {q: 2}]); [p, q]`, `[1,2]`},
		{`xs = [({a} := {a: 3}), a]; xs`, `[{"a":3},3]`},
		// A record is a dict, and its rest is a plain one, as `{**p}` is.
		{`record P(x, y); {x, **r} = P(1, 2); [x, r, type(r)]`, `[1,{"y":2},"dict"]`},
		// `=` writes an outer binding and `:=` declares a new one, as for one name.
		{`a = 0; fn f() { {a} = {a: 9} }; f(); a`, `9`},
		{`a = 0; fn f() { {a} := {a: 9} }; f(); a`, `0`},
		// The rest is a copy: writing to it cannot reach the source.
		{`d = {a: 1, b: 2}; {a, **r} = d; r["z"] = 0; d`, `{"a":1,"b":2}`},
	})
}

func TestDictTargetErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		{`{a, b} = [1, 2]`, ErrKindType, "cannot destructure array with a dict pattern"},
		{`{a} = nil`, ErrKindType, "cannot destructure nil with a dict pattern"},
		{`{a} = {}`, ErrKindKey, `destructuring: key not found: "a"`},
		{`{a: {b}} = {a: {c: 1}}`, ErrKindKey, `key not found: "b"`},
		{`{a: {b}} = {a: 1}`, ErrKindType, "cannot destructure int with a dict pattern"},
		{`{a: [x, y]} = {a: [1]}`, ErrKindIndex, "destructuring expects 2 values, got 1"},
		{`d = {a: 1}; {a: 1} = d`, ErrKindSyntax, "a destructuring target binds"},
		{`d = {}; {} = d`, ErrKindSyntax, "an empty dict pattern binds nothing"},
		{`d = {a: 1}; {**} = d`, ErrKindSyntax, "'**' and a name"},
		{`d = {a: 1}; {**r, a} = d`, ErrKindSyntax, "comes last"},
		{`d = {a: 1}; {a, a: b} = d`, ErrKindSyntax, `key "a" is named twice`},
		{`d = {a: 1}; {**r, **s} = d`, ErrKindSyntax, "comes last"},
		{`d = {a: 1}; {a: in [1]} = d`, ErrKindSyntax, "'in' tests membership"},
		{`d = {a: 1}; {a} += d`, ErrKindSyntax, "destructuring assigns with '=' or ':='"},
		{`d = {a: 1}; k = "a"; {"${k}": v} = d`, ErrKindSyntax, "a name or a plain string"},
		{`d = {a: 1}; {1: v} = d`, ErrKindSyntax, "expected a key in a dict pattern"},
		// A write that fails fails at its target; the shape had already fit.
		{`s = 5; {a: s[0]} = {a: 1}`, ErrKindType, "cannot assign to an index of int"},
	})
}

func TestDictPatternsInMatch(t *testing.T) {
	route := `fn kind(ev) {
  match ev {
    {kind: "paid", total: t} if t > 0 -> "credit ${t}"
    {kind: "refund", data: {reason}} -> "refund: ${reason}"
    {kind: /^test\./} -> "test"
    {age: in 18..65} -> "adult"
    {age} -> "age ${age}"
    {} -> "a dict"
    else -> "not a dict"
  }
}
`
	spreadCases(t, [][2]string{
		{route + `kind({kind: "paid", total: 5})`, `"credit 5"`},
		// A guard that fails moves on to the next arm, with the bindings dropped.
		{route + `kind({kind: "paid", total: 0})`, `"a dict"`},
		{route + `kind({kind: "refund", data: {reason: "late", by: 1}})`, `"refund: late"`},
		{route + `kind({kind: "test.unit"})`, `"test"`},
		{route + `kind({age: 30})`, `"adult"`},
		{route + `kind({age: 3})`, `"age 3"`},
		{route + `kind({})`, `"a dict"`},
		{route + `kind([1])`, `"not a dict"`},
		{route + `kind(nil)`, `"not a dict"`},
		// A missing key is no match, not an error.
		{`s = {a: 1}; match s { {b} -> b; else -> "none" }`, `"none"`},
		// `{age: 18..65}` compares with a range; membership is `in`.
		{`s = {age: 30}; match s { {age: 18..65} -> "eq"; else -> "no" }`, `"no"`},
		{`s = {age: 18..65}; match s { {age: 18..65} -> "eq"; else -> "no" }`, `"eq"`},
		{`s = {tag: "b"}; match s { {tag: in ["a", "b"]} -> "yes"; else -> "no" }`, `"yes"`},
		{`allowed = ["a", "b"]; s = {tag: "c"}; match s { {tag: in allowed} -> "yes"; else -> "no" }`, `"no"`},
		// Rest, named and anonymous.
		{`s = {a: 1, b: 2}; match s { {a, **rest} -> [a, rest] }`, `[1,{"b":2}]`},
		{`s = {a: 1, b: 2}; match s { {a, **} -> a }`, `1`},
		// Inside an array pattern, and an array pattern inside a dict pattern.
		{`match [1, {x: 2}] { [a, {x}] -> a + x }`, `3`},
		{`s = {p: [1, 2]}; match s { {p: [a, b]} -> a + b }`, `3`},
		{`s = {p: [1, 2, 3]}; match s { {p: [a, b]} -> "two"; {p: [h, *t]} -> t }`, `[2,3]`},
		// A later value may name what an earlier key bound.
		{`s = {lo: 1, hi: 1}; match s { {lo, hi: lo} -> "flat"; else -> "slope" }`, `"flat"`},
		// A name in parentheses is a value to compare, not a binder.
		{`r = 1; s = {k: 1}; match s { {k: (r)} -> "one"; else -> "other" }`, `"one"`},
		{`r = 1; s = {k: 2}; match s { {k: (r)} -> "one"; else -> "other" }`, `"other"`},
		// A record is a dict, so a dict pattern takes one apart.
		{`record M(amount, cur); match M(5, "RUB") { {cur: "RUB", amount} -> amount }`, `5`},
		// A brace that is not the whole pattern is the operand it always was.
		{`match 1 { {a: 1}.len -> "len one"; else -> "no" }`, `"len one"`},
		{`s = {a: 1}; match s { ({a: 1}) -> "equal"; else -> "no" }`, `"equal"`},
		{`s = {a: 1, b: 2}; match s { ({a: 1}) -> "equal"; else -> "no" }`, `"no"`},
	})
}

func TestDictPatternArmErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		{`d = {a: 1}; match { {a: 1} -> 1 }`, ErrKindSyntax, "a dict pattern needs a subject"},
		{`d = {a: 1}; match d { {a: 1}, {b: 2} -> 1 }`, ErrKindSyntax, "a dict pattern must be the only pattern in its arm"},
		{`d = {a: 1}; match d { {a} -> a; {a, a: b} -> b }`, ErrKindSyntax, `key "a" is named twice`},
		// A comparison inside a pattern still raises the errors it raises anywhere.
		{`d = {a: 1}; match d { {a: in 5} -> 1 }`, ErrKindType, "the right side of 'in' must have members"},
		{`d = {a: 1}; match d { {a: in raise("no list")} -> 1 }`, ErrKindRaise, "no list"},
		{`d = {a: 1}; match d { {a: raise("no value")} -> 1 }`, ErrKindRaise, "no value"},
	})
}

func TestDictPatternsInFor(t *testing.T) {
	spreadCases(t, [][2]string{
		{`out = []; for [i, r] in [[1, "a"], [2, "b"]] { out.push("${i}${r}") }; out`, `["1a","2b"]`},
		{`out = []; for {name} in [{name: "x"}, {name: "y", z: 1}] { out.push(name) }; out`, `["x","y"]`},
		{`out = []; for k, {total} in {a: {total: 1}, b: {total: 2}} { out.push("${k}${total}") }; out`, `["a1","b2"]`},
		{`out = []; for [k, v] in {a: 1} { out.push(k + v.str) }; out`, `["a1"]`},
		{`out = []; for [h, *t] in [[1, 2, 3]] { out.push(t) }; out`, `[[2,3]]`},
		{`out = []; for {id, **rest} in [{id: 1, x: 2}] { out.push(rest) }; out`, `[{"x":2}]`},
		{`out = []; for [id, {tags: [first, *more]}] in [[1, {tags: ["a", "b"]}]] { out.push(first) }; out`, `["a"]`},
		// Each iteration binds afresh, so a closure keeps its own item.
		{`fs = []; for {n} in [{n: 1}, {n: 2}] { fs.push({ n }) }; fs.map { it() }`, `[1,2]`},
		// The loop's names stay in the loop.
		{`name = "outer"; for {name} in [{name: "inner"}] { }; name`, `"outer"`},
		// A pattern reads each item from a seq too.
		{`out = []; for {v} in [{v: 1}, {v: 2}].seq { out.push(v) }; out`, `[1,2]`},
	})
	spreadErrors(t, [][3]string{
		{`for {name} in [{x: 1}] { }`, ErrKindKey, `key not found: "name"`},
		{`for [a, b] in [[1]] { }`, ErrKindIndex, "destructuring expects 2 values, got 1"},
		{`for {a} in [1] { }`, ErrKindType, "cannot destructure int with a dict pattern"},
		{`for k, {a} in {x: 1} { }`, ErrKindType, "cannot destructure int with a dict pattern"},
		{`xs = [0]; for [xs[0]] in [[1]] { }`, ErrKindSyntax, "a loop pattern binds names"},
		{`for {a: $g} in [{a: 1}] { }`, ErrKindSyntax, "a loop pattern binds names"},
		{`for 1 in [1] { }`, ErrKindSyntax, "expected a loop variable"},
		{`for [a].x in [[1]] { }`, ErrKindSyntax, "expected a loop variable"},
		{`for [a: 1] in [1] { }`, ErrKindSyntax, "a dict is written {a: 1}"},
	})
}

// A brace or a bracket that is not a whole element is the operand it always was: what
// follows its close decides, before anything is consumed.
func TestABracketOperandIsNotAPattern(t *testing.T) {
	spreadCases(t, [][2]string{
		{`xs = [{a: 1}]; d = {a: 1}; match true { {a: 1} in xs -> "member"; else -> "no" }`, `"member"`},
		{`d = [1]; match [1, 2] { [1] + d -> "sum"; else -> "no" }`, `"no"`},
		{`d = [1, 1]; match [1, 1] { [1] + [1] -> "sum"; else -> "no" }`, `"sum"`},
	})
}

// A rest dict is charged like any other new collection (§14.2): the source fits the
// budget, and keeping several rests of it alive does not.
func TestDictRestIsCharged(t *testing.T) {
	o := DefaultOptions()
	o.MaxMemory = 1 << 20
	build := `d = {}; (1..5000).each { d[it.str] = it }; `
	if _, err := spreadEval(t, o, build+`d.len`); err != nil {
		t.Fatalf("the source alone: %v", err)
	}
	_, err := spreadEval(t, o, build+`keep = []; 20.times { {**r} = d; keep.push(r) }`)
	if !errors.Is(err, ErrMemory) {
		t.Errorf("twenty live rests under a 1 MiB budget: err = %v, want ErrMemory", err)
	}
}

// A rest reads every entry of its source, so it pays a step for each before anything is
// built, as `**d` does.
func TestDictRestChargesSteps(t *testing.T) {
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
	const setup = "d = {}\n(1..1000).each { d[\"k${it}\"] = it }\n"
	plain := steps(setup + "{k1} = d")
	rest := steps(setup + "{k1, **r} = d")
	if rest-plain < 1000 {
		t.Errorf("a rest of 1000 entries cost %d more steps, want at least 1000", rest-plain)
	}
}

// What an arm binds is a root while its guard runs: a guard that allocates sees the rest
// the pattern built, so the budget counts it. The control binds no rest and fits.
func TestArmBindingsAreRooted(t *testing.T) {
	o := DefaultOptions()
	o.MaxMemory = 1600 << 10
	const setup = "d = {}\n(1..3000).each { d[it.str] = it }\n"
	guard := ` if ("x" * 950000).len > 0 -> "fired"; else -> "no" }`
	if _, err := spreadEval(t, o, setup+`match d { {**}`+guard); err != nil {
		t.Fatalf("the control, with no rest bound: %v", err)
	}
	if _, err := spreadEval(t, o, setup+`match d { {**r}`+guard); !errors.Is(err, ErrMemory) {
		t.Errorf("a guard allocating beside a bound rest: err = %v, want ErrMemory", err)
	}
}

// RFC 0002, part 2: `Name {…}` in a pattern asks for the shape a `record` built, then
// takes the value apart as a dict pattern does.
func TestRecordPatterns(t *testing.T) {
	const decls = "record Money(amount, currency = \"RUB\")\nrecord Point(x, y)\n"
	describe := decls + `fn describe(v) {
  match v {
    Money {amount, currency: "EUR"} -> "eur ${amount}"
    Money {amount} if amount > 100 -> "big ${amount}"
    Money {} -> "money"
    Point {x, **rest} -> "x=${x} ${rest.inspect}"
    {amount} -> "dict ${amount}"
    else -> "other"
  }
}
`
	spreadCases(t, [][2]string{
		{describe + `describe(Money(5, "EUR"))`, `"eur 5"`},
		{describe + `describe(Money(500))`, `"big 500"`},
		{describe + `describe(Money(1))`, `"money"`},
		{describe + `describe(Point(1, 2))`, `"x=1 {\"y\":2}"`},
		// A plain dict with the same keys is not the shape.
		{describe + `describe({amount: 3, currency: "EUR"})`, `"dict 3"`},
		{describe + `describe(7)`, `"other"`},
		// Nested, both ways round.
		{decls + `match [Money(2)] { [Money {amount}] -> amount; else -> 0 }`, `2`},
		{decls + `s = {m: Point(3, 4)}; match s { {m: Point {y}} -> y; else -> 0 }`, `4`},
		{decls + `match Point(1, 2) { Money {} -> "money"; Point {} -> "point" }`, `"point"`},
		// A record declared below the match is still one: records hoist (§8.2).
		{`fn f(v) { match v { Late {a} -> a; else -> 0 } }; record Late(a); f(Late(9))`, `9`},
		// The shape asked is the constructor's, so a record of another name with the
		// same fields does not fire.
		{`record A(x); record B(x); match B(1) { A {x} -> "a"; else -> "not a" }`, `"not a"`},
		// A brace broken across lines, and a key written as a string.
		{decls + "match Point(1, 2) {\n  Point {\n    x\n  } -> x\n}", `1`},
		{decls + `match Money(7) { Money {"amount": a} -> a }`, `7`},
	})
}

// A name that is not a record keeps the reading `Name {…}` always had: a call with a
// trailing closure, compared as an arm pattern is.
func TestRecordPatternKeepsTheCallReading(t *testing.T) {
	const choose = "fn choose(f) { 1 }\n"
	spreadCases(t, [][2]string{
		{choose + `match 1 { choose {it} -> 7; else -> 0 }`, `7`},
		{choose + `match 1 { choose {} -> 7; else -> 0 }`, `7`},
		{choose + `match 1 { choose {}, 2 -> 9; else -> 0 }`, `9`},
		{choose + `match { choose {it} -> 10; else -> 0 }`, `10`},
		{choose + `match [1] { [choose {it}] -> 11; else -> 0 }`, `11`},
		{choose + `s = {k: 1}; match s { {k: choose {it}} -> 12; else -> 0 }`, `12`},
		{choose + `match 1 { choose { (x) -> x } -> 8; else -> 0 }`, `8`},
		// A binding that shadows a record is not one: the reading follows the binding.
		{`record Money(amount); fn f() { Money := { (_) -> 1 }; match 1 { Money {it} -> "call"; else -> "no" } }; f()`, `"call"`},
	})
}

func TestRecordPatternErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		{`record Money(amount, currency); m = Money(1, "x"); match m { Money {amout} -> 1 }`, ErrKindSyntax,
			"record Money has no field 'amout'; it has amount, currency"},
		{`record M(a); match M(1) { [M {b}] -> 1; else -> 0 }`, ErrKindSyntax, "record M has no field 'b'; it has a"},
		{`fn f(x) { x }; match 1 { f {a, b} -> 1; else -> 0 }`, ErrKindSyntax, "'f' is not a record"},
		{`fn f(x) { x }; match [1] { [f {a: 1}] -> 1; else -> 0 }`, ErrKindSyntax, "'f' is not a record"},
		{`record M(a); match M(1) { M {a}, 2 -> 1; else -> 0 }`, ErrKindSyntax, "a record pattern must be the only pattern in its arm"},
		{`record M(a); match { M {a} -> 1; else -> 0 }`, ErrKindSyntax, "a record pattern needs a subject"},
		{`record M(a); [M {a}] = [M(1)]`, ErrKindSyntax, "a record shape is a match pattern"},
		// A name bound nowhere is not a record either: the call reading, and its error.
		{`match 1 { nope {it} -> 1; else -> 0 }`, ErrKindName, "undefined function 'nope'"},
	})
}
