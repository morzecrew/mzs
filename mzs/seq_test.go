package mzs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// SPEC §12.14, driven through the front end: what a seq answers is a question about the
// language, and the rows only mean anything through dispatch (`s.map` is a KSeq row,
// `s.sort` is not one and must say so).

// TestSeqSources is the three ways a sequence starts (§12.1 `seq`).
func TestSeqSources(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"an array", `[1, 2, 3].seq.array.json`, "[1,2,3]"},
		{"a range", `(1..4).seq.array.json`, "[1,2,3,4]"},
		{"an exclusive range", `(1..<4).seq.array.json`, "[1,2,3]"},
		{"a descending range is empty", `(4..1).seq.array.json`, "[]"},
		{"a generator, ended by nil", `seq { (i) -> if i < 3 { i * i } }.array.json`, "[0,1,4]"},
		{"a seq is already one", `s = (1..2).seq; (s.seq == s)`, "true"},
		{"the prefix spelling", `seq([1, 2]).array.json`, "[1,2]"},
		{"a range keeps its endpoints, not its elements", `(1..9_000_000_000).seq.first`, "1"},
		{"and its last element is reachable", `(1..3).seq.drop(2).array.json`, "[3]"},
		{"a scalar has no elements to hand out", `try seq(5) else "no"`, "no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// Both ends of the int64 range are reachable from a script, and the arithmetic that walks
// one has to say so. An exclusive range ending at the smallest int has nothing below it,
// and the `hi--` that finds the last element wrapped it to the largest one instead — the
// empty range became the widest one there is, and its eager twin asked `make` for a
// negative length and panicked (A7).
func TestSeqRangeEndpointsDoNotWrap(t *testing.T) {
	t.Parallel()
	in := evInterp()
	const minInt = "(0 - 9223372036854775807 - 1)"
	const maxInt = "9223372036854775807"

	tests := []struct{ name, src, want string }{
		{"an exclusive range ending at the smallest int is empty",
			"(0..<" + minInt + ").seq.len", "0"},
		{"and so is its eager twin, which used to panic in make",
			"(0..<" + minInt + ").array.len", "0"},
		{"len agrees with both", "(0..<" + minInt + ").len", "0"},
		{"the widest range there is counts up to the cap, not into a negative",
			"(" + minInt + ".." + maxInt + ").len", "2147483647"},
		{"a seq ending at the largest int does not wrap past it",
			"(" + maxInt + ".." + maxInt + ").seq.array.json", "[9223372036854775807]"},
		{"nor does its exclusive form", "(" + maxInt + "..<" + maxInt + ").seq.len", "0"},
		{"the smallest int is still an element when it is the endpoint",
			"(" + minInt + ".." + minInt + ").seq.len", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// `first(0)` asks for no elements and must therefore take none. seqRun pulls before it
// asks, so the row has to answer before the traversal starts — otherwise a line comes off
// `io.lines`, or a generator advances, to produce an empty array.
func TestSeqFirstZeroPullsNothing(t *testing.T) {
	t.Parallel()
	in := evInterp()

	src := `n = 0
		s = seq { n = n + 1; if n <= 4 { n } }
		[s.first(0), s.first(2), s.first(2)].json`
	if got := evStr(t, in, src); got != "[[],[1,2],[3,4]]" {
		t.Errorf("first(0) = %s; want [[],[1,2],[3,4]] — nothing consumed by the empty ask", got)
	}
	if got := evStr(t, in, `(1..5).seq.take(0).array.json`); got != "[]" {
		t.Errorf("take(0) = %s; want []", got)
	}
}

// TestSeqIsLazy is the feature itself, and it is measured rather than asserted: the
// generator counts its own calls, so the count *is* the number of elements the chain
// pulled. A materialising `map` would answer 100 here, and every number below would be
// the length of the source instead of the length of the answer.
func TestSeqIsLazy(t *testing.T) {
	in := evInterp()

	tests := []struct {
		name  string
		chain string
		pulls string
	}{
		{"take stops the source", `s.take(3).array`, "3"},
		{"first pulls one", `s.first`, "1"},
		{"find stops at the hit", `s.find { it == 2 }`, "3"},
		{"any stops at the hit", `s.any { it == 0 }`, "1"},
		{"all stops at the miss", `s.all { it < 0 }`, "1"},
		{"has stops at the hit", `s.has(1)`, "2"},
		{"empty pulls one", `s.empty`, "1"},
		{"take_while stops after the refusal", `s.take_while { it < 2 }.array`, "3"},
		{"a filtered take pulls only what it needs", `s.filter { it % 2 == 0 }.take(2).array`, "3"},
		{"a chain of lazy rows pulls nothing on its own", `s.map { it * 2 }.filter { true }.drop(1)`, "0"},
		{"len is a terminal and pulls everything", `s.len`, "101"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "pulls = 0\n" +
				"s = seq { (i) -> pulls = pulls + 1; if i < 100 { i } }\n" +
				tt.chain + "\npulls"
			if got := evStr(t, in, src); got != tt.pulls {
				t.Errorf("%s pulled %s element(s); want %s", tt.chain, got, tt.pulls)
			}
		})
	}
}

// TestSeqRows walks every row, lazy and terminal, over a source small enough to read.
func TestSeqRows(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"map", `(1..3).seq.map { it * 10 }.array.json`, "[10,20,30]"},
		{"filter", `(1..6).seq.filter { it % 2 == 0 }.array.json`, "[2,4,6]"},
		{"reject", `(1..4).seq.reject { it % 2 == 0 }.array.json`, "[1,3]"},
		{"flat_map over arrays", `(1..3).seq.flat_map { [it, -it] }.array.json`, "[1,-1,2,-2,3,-3]"},
		{"flat_map leaves a scalar alone", `(1..2).seq.flat_map { it }.array.json`, "[1,2]"},
		{"flat_map over a range", `(1..3).seq.flat_map { (1..it) }.array.json`, "[1,1,2,1,2,3]"},
		{"flat_map over a seq", `(1..2).seq.flat_map { (1..it).seq }.array.json`, "[1,1,2]"},
		{"take", `(1..9).seq.take(2).array.json`, "[1,2]"},
		{"take more than there is", `(1..2).seq.take(9).array.json`, "[1,2]"},
		{"take(0)", `(1..2).seq.take(0).array.json`, "[]"},
		{"take_while", `(1..9).seq.take_while { it < 4 }.array.json`, "[1,2,3]"},
		{"drop", `(1..5).seq.drop(3).array.json`, "[4,5]"},
		{"drop more than there is", `(1..2).seq.drop(9).array.json`, "[]"},
		{"drop_while", `(1..5).seq.drop_while { it < 4 }.array.json`, "[4,5]"},
		{"drop_while stops dropping for good", `[1, 9, 1].seq.drop_while { it < 5 }.array.json`, "[9,1]"},

		{"each returns the receiver", `type((1..2).seq.each { it })`, "seq"},
		{"each_with_index", `out = []; (10..11).seq.each_with_index { (x, i) -> out.push([i, x]) }; out.json`,
			"[[0,10],[1,11]]"},
		{"reduce", `(1..4).seq.reduce { (a, b) -> a + b }`, "10"},
		{"reduce with an initial value", `(1..3).seq.reduce(10) { (a, b) -> a + b }`, "16"},
		{"reduce of empty is nil", `inspect((4..1).seq.reduce { (a, b) -> a + b })`, "nil"},
		{"len", `(1..7).seq.len`, "7"},
		{"empty", `[(1..2).seq.empty, (4..1).seq.empty].json`, "[false,true]"},
		{"count", `(1..4).seq.count`, "4"},
		{"count a value", `[1, 2, 2].seq.count(2)`, "2"},
		{"count with a closure", `(1..4).seq.count { it % 2 == 0 }`, "2"},
		{"has", `[(1..3).seq.has(2), (1..3).seq.has(9)].json`, "[true,false]"},
		{"in asks has", `2 in (1..3).seq`, "true"},
		{"first", `(5..9).seq.first`, "5"},
		{"first of empty is nil", `inspect((4..1).seq.first)`, "nil"},
		{"first n", `(5..9).seq.first(2).json`, "[5,6]"},
		{"find", `(1..9).seq.find { it > 3 }`, "4"},
		{"find misses", `inspect((1..3).seq.find { it > 9 })`, "nil"},
		{"any", `(1..3).seq.any { it == 2 }`, "true"},
		{"any without a closure tests truthiness", `[nil, false].seq.any`, "false"},
		{"all", `(1..3).seq.all { it > 0 }`, "true"},
		{"none", `(1..3).seq.none { it > 9 }`, "true"},
		{"sum", `(1..4).seq.sum`, "10"},
		{"sum of empty is zero", `(4..1).seq.sum`, "0"},
		{"sum with a closure", `["aa", "b"].seq.sum { it.len }`, "3"},
		{"min", `[3, 1, 2].seq.min`, "1"},
		{"max", `[3, 1, 2].seq.max`, "3"},
		{"min of empty is nil", `inspect((4..1).seq.min)`, "nil"},
		{"min with a comparator", `["aaa", "b"].seq.min { (a, b) -> a.len <=> b.len }`, `b`},
		{"join", `(1..3).seq.join("-")`, "1-2-3"},
		{"join with no separator", `(1..3).seq.join`, "123"},
		{"array", `(1..3).seq.array.json`, "[1,2,3]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// A seq is a recipe and not a cursor: a terminal opens the source again, so the same
// chain answers the same thing twice. Where the source has state of its own, a second run
// sees what that state left — which is the same rule, said about a stateful source.
func TestSeqIsRerunnable(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"a range source answers twice", `s = (1..3).seq.map { it * 2 }; [s.array, s.array].json`,
			"[[2,4,6],[2,4,6]]"},
		{"an array source sees the array it has now",
			`xs = [1]; s = xs.seq; a = s.len; xs.push(2); [a, s.len].json`, "[1,2]"},
		{"a pure generator answers twice", `s = seq { (i) -> if i < 3 { i } }; [s.array, s.array].json`,
			"[[0,1,2],[0,1,2]]"},
		{"a stateful generator carries on where it stopped",
			`n = 0; s = seq { n = n + 1; if n <= 4 { n } }; [s.take(2).array, s.take(2).array].json`,
			"[[1,2],[3,4]]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// §7.2, §7.4, §7.6: a seq is a kind of its own and answers `is("array")` with **false**,
// unlike a Range. It is the difference between "can be materialised under a cap" and
// "refuses to be", and host code that accepts one must not silently accept the other.
func TestSeqIsNotAnArray(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"type", `type((1..2).seq)`, "seq"},
		{"is seq", `(1..2).seq.is("seq")`, "true"},
		{"is not an array", `(1..2).seq.is("array")`, "false"},
		{"a range still is one", `(1..2).is("array")`, "true"},
		{"an array is not a seq", `[1].is("seq")`, "false"},
		{"str says what it is without running it", `str((1..2).seq)`, "#<seq>"},
		{"equality is identity", `s = (1..2).seq; [s == s, s == (1..2).seq, s == [1, 2]].json`,
			"[true,false,false]"},
		{"and it is not ordered", `inspect((1..2).seq <=> (1..2).seq)`, "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}

	// A dict key must be hashable (§7.6), and a seq has no value to hash until it is run.
	if e := evErr(t, in, `d = {}; d.set((1..2).seq, 1)`, nil); !strings.Contains(e.Msg, "hashable") {
		t.Errorf("a seq as a dict key = %q; want the hashable-key diagnostic", e.Msg)
	}

	// And no JSON form, which is a raise rather than the `null` a function encodes as:
	// a sequence *has* a document form and it is the one `.array` produces (§12.14).
	e := evErr(t, in, `include json; {items: (1..2).seq}.json`, nil)
	if !strings.Contains(e.Msg, ".array") {
		t.Errorf("json of a seq = %q; want it to name the fix", e.Msg)
	}
}

// The same answer has to reach the host, or the two halves of §12.14 disagree: the script
// is told to materialise while `encoding/json`, an http response body (§12.11) and the
// CLI's --json quietly write `null` in place of the data. MarshalJSON is where all three
// meet, so it is where the refusal lives — and it reaches inside a collection, because
// that is where a forgotten `.array` actually hides.
func TestSeqHasNoJSONFormForTheHostEither(t *testing.T) {
	t.Parallel()
	in := New(Options{})

	for _, src := range []string{
		`(1..3).seq`,
		`{items: (1..3).seq}`,
		`[[(1..3).seq]]`,
		`{a: {b: [1, (1..3).seq]}}`,
	} {
		v, err := in.Eval(context.Background(), src, nil)
		if err != nil {
			t.Fatalf("Eval(%s): %v", src, err)
		}
		b, err := v.MarshalJSON()
		if err == nil {
			t.Errorf("MarshalJSON(%s) = %s; want the refusal the script gets", src, b)
			continue
		}
		if !strings.Contains(err.Error(), ".array") {
			t.Errorf("MarshalJSON(%s) = %v; want it to name the fix", src, err)
		}
	}

	// Everything else still encodes, including the kinds that legitimately have no JSON
	// spelling of their own.
	for _, tt := range []struct{ src, want string }{
		{`{items: (1..3).seq.array}`, `{"items":[1,2,3]}`},
		{`[1, "a", nil, 1..3]`, `[1,"a",null,[1,2,3]]`},
		{`{f: { it }}`, `{"f":null}`},
	} {
		v := evOK(t, in, tt.src, nil)
		b, err := v.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(%s): %v", tt.src, err)
		}
		if string(b) != tt.want {
			t.Errorf("MarshalJSON(%s) = %s; want %s", tt.src, b, tt.want)
		}
	}

	// A self-referential value must not send the walk down the Go stack (A7).
	v := evOK(t, in, `a = []; a.push(a); a`, nil)
	if _, err := v.MarshalJSON(); err != nil {
		t.Errorf("MarshalJSON of a cyclic array = %v; the depth cap answers rather than raising", err)
	}
}

// §12.14: everything an array does that needs the whole sequence at once is reached by
// materialising first. The diagnostic has to say so — silently buffering would be the one
// thing a seq exists not to do.
func TestSeqRefusesTheRowsThatNeedEverything(t *testing.T) {
	in := evInterp()

	// What the materialised form answers, so the second half of each case can fail.
	materialised := map[string]string{
		"sort": "array", "reverse": "array", "uniq": "array",
		"tally": "dict", "group_by": "dict", "last": "int",
	}
	for name, want := range materialised {
		t.Run(name, func(t *testing.T) {
			suffix := ""
			if name == "group_by" {
				suffix = " { it }"
			}
			e := evErr(t, in, "(1..3).seq."+name+suffix, nil)
			if !strings.Contains(e.Msg, "seq") {
				t.Errorf("(1..3).seq.%s = %q; want a diagnostic naming the receiver kind", name, e.Msg)
			}
			// The array is one row away, and it is the same name.
			if got := evStr(t, in, "type((1..3).seq.array."+name+suffix+")"); got != want {
				t.Errorf("type((1..3).seq.array.%s) = %s; want %s", name, got, want)
			}
		})
	}
}

// UFCS is one namespace (§12): a row of §12.14 answers `f(s)` as well as `s.f`. It has to
// be the row in both spellings, because the builtin of the same name reads a value that
// is already there and a seq has nothing until it is pulled.
func TestSeqRowsUnderUFCS(t *testing.T) {
	in := evInterp()

	tests := []struct{ src, want string }{
		{`len((1..4).seq)`, "4"},
		{`empty((4..1).seq)`, "true"},
		{`sum((1..4).seq)`, "10"},
		{`min([3, 1].seq)`, "1"},
		{`max([3, 1].seq)`, "3"},
		{`array((1..2).seq).json`, "[1,2]"},
		{`map((1..2).seq, { it * 2 }).array.json`, "[2,4]"},
		{`filter((1..4).seq, { it > 2 }).array.json`, "[3,4]"},
		{`count((1..4).seq)`, "4"},
	}
	for _, tt := range tests {
		if got := evStr(t, in, tt.src); got != tt.want {
			t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
		}
	}
}

// `for x in s` pulls rather than materialising, which is what makes a loop over a log the
// same shape as a loop over an array (§8.2). `break` and `next` mean what they always do.
func TestSeqInAForLoop(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"iterates", `out = []; for x in (1..3).seq { out.push(x) }; out.json`, "[1,2,3]"},
		{"through a lazy chain", `out = []; for x in ((1..4).seq.map { it * 2 }) { out.push(x) }; out.json`,
			"[2,4,6,8]"},
		{"break stops the pull", `n = 0; s = seq { (i) -> n = n + 1; i }; for x in s { break x }; n`, "1"},
		{"break carries its value", `for x in (1..9).seq { break x * 10 }`, "10"},
		{"next skips", `out = []; for x in (1..4).seq { next if x % 2 == 0; out.push(x) }; out.json`, "[1,3]"},
		{"an endless source is fine when the body breaks",
			`out = []; for x in (seq { (i) -> i }) { break if x > 2; out.push(x) }; out.json`, "[0,1,2]"},
		{"two variables destructure the item",
			`out = []; for k, v in [["a", 1]].seq { out.push(k + v.str) }; out.json`, `["a1"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// §14.1 is the reason the step charge lives in the source rather than in the terminals:
// an endless sequence is a `while true` and must end the same way. The predicate below
// never fires, so the loop that runs away is *inside* `filter` and never returns to the
// terminal that started it.
func TestSeqEndlessChainsHitTheLimits(t *testing.T) {
	t.Parallel()
	in := New(Options{StepBudget: 50_000})

	for _, src := range []string{
		`seq { (i) -> i }.filter { false }.first`,
		`seq { (i) -> i }.len`,
		`seq { (i) -> i }.take_while { true }.array`,
		`seq { (i) -> i }.drop(9_000_000_000).first`,
		`n = 0; for x in (seq { (i) -> i }) { n = n + 1 }; n`,
	} {
		t.Run(src, func(t *testing.T) {
			_, err := in.Eval(context.Background(), src, nil)
			if err == nil {
				t.Fatalf("%s finished; an endless sequence must end on the budget", src)
			}
			if !errorsIsBudget(err) {
				t.Errorf("%s = %v; want the step budget (§14.1)", src, err)
			}
		})
	}

	// And the deadline reaches it too, with the budget out of the way.
	slow := New(Options{StepBudget: -1, Timeout: 50 * time.Millisecond})
	if _, err := slow.Eval(context.Background(), `seq { (i) -> i }.len`, nil); err == nil {
		t.Fatal("an endless sequence outlived the deadline")
	}
}

// §14.2: materialising is where a lazy pipeline becomes memory, so `array` is the row
// that charges MaxCollection — and the only one that has to.
func TestSeqMaterialisationIsCapped(t *testing.T) {
	t.Parallel()
	in := New(Options{MaxCollection: 100, StepBudget: 1_000_000})

	if _, err := in.Eval(context.Background(), `(1..1000).seq.array`, nil); err == nil {
		t.Fatal("array built 1000 elements under a cap of 100")
	}
	// The same source counted rather than kept is not capped: nothing was materialised.
	if v, err := in.Eval(context.Background(), `(1..1000).seq.len`, nil); err != nil || v.Str() != "1000" {
		t.Fatalf("len = %v, %v; counting materialises nothing", v.Str(), err)
	}
}

// The closure contract of §8.10 travels through a lazy chain unchanged: a raise inside a
// stage surfaces at the terminal that pulled it, and `break` is the value of the whole
// call the way it is for an array row.
func TestSeqClosureControlFlow(t *testing.T) {
	in := evInterp()

	if e := evErr(t, in, `(1..3).seq.map { raise("боль") }.array`, nil); !strings.Contains(e.Msg, "боль") {
		t.Errorf("a raise inside map = %q; want it to reach the terminal", e.Msg)
	}
	if e := evErr(t, in, `try (1..3).seq.filter { raise("боль") }.first else raise("caught")`, nil); !strings.Contains(e.Msg, "caught") {
		t.Errorf("a raise inside filter = %q; want it catchable at the terminal", e.Msg)
	}
	if got := evStr(t, in, `(1..9).seq.each { break it * 3 }`); got != "3" {
		t.Errorf("break inside each = %s; want 3 — the value of the whole call (§8.10)", got)
	}
	if got := evStr(t, in, `fn f() { (1..9).seq.each { return it }; 0 }; f()`); got != "1" {
		t.Errorf("return inside each = %s; want it to leave the function", got)
	}
}

// A lazy row checks its closure where it is written, not where it is pulled: a stage that
// is never pulled must still refuse `s.map(3)`.
func TestSeqLazyRowsCheckTheirArgumentsEagerly(t *testing.T) {
	in := evInterp()

	for _, src := range []string{
		`(1..3).seq.map(3)`,
		`(1..3).seq.filter("x")`,
		`(1..3).seq.take(-1)`,
		`(1..3).seq.take_while(nil)`,
	} {
		if e := evErr(t, in, src, nil); e.Kind != ErrKindType && e.Kind != ErrKindArgument {
			t.Errorf("%s reported kind %q; want a type or argument error", src, e.Kind)
		}
	}
}

// ---------------------------------------------------------------------------
// Every row, one property at a time
// ---------------------------------------------------------------------------

var (
	seqNever  = colBlock(func(c *Ctx, args []Value) (Value, error) { return Bool(false), nil })
	seqAlways = colBlock(func(c *Ctx, args []Value) (Value, error) { return Bool(true), nil })
)

// seqRowArgs is a working argument list for every §12.14 row, so a row added tomorrow is
// covered the moment it is registered — and a row missing from this table fails the test
// rather than being skipped.
var seqRowArgs = map[string][]Value{
	"map": {colIdentity}, "filter": {colIsEven}, "reject": {colIsEven},
	"flat_map": {colIdentity}, "take_while": {colIsEven}, "drop_while": {colIsEven},
	"take": {Int(2)}, "drop": {Int(2)},
	"each": {colIdentity}, "each_with_index": {colIdentity}, "reduce": {colSumPair},
	"count": {colIsEven},
	// The short-circuiting rows are given the answer that is never there, so that they
	// walk the whole sequence and the budget test below means something.
	"find": {seqNever}, "has": {Int(-1)},
	"any": {seqNever}, "all": {seqAlways}, "none": {seqNever},
	"array": nil, "len": nil, "empty": nil, "first": nil,
	"sum": nil, "min": nil, "max": nil, "join": {Str("-")},
}

func TestSeqRowArgsTableIsComplete(t *testing.T) {
	for _, name := range MethodNames(KSeq) {
		if _, ok := seqRowArgs[name]; !ok {
			t.Errorf("no arguments listed for the %q row; add it to seqRowArgs so the "+
				"property tests below cover it", name)
		}
	}
}

// A7 in the small: a receiver of the wrong kind is a diagnostic, never a panic. Only a
// host can produce this — dispatch inside a script finds the row by the receiver's kind —
// but LookupMethod is exported (§13), so the guard has to hold anyway.
func TestSeqRowsRefuseANonSeqReceiver(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	for _, name := range MethodNames(KSeq) {
		t.Run(name, func(t *testing.T) {
			_, err := colInvoke(c, KSeq, name, colInts(1, 2, 3), seqRowArgs[name]...)
			if err == nil {
				t.Fatalf("%q accepted an array receiver; want a type error", name)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%q returned %T (%v); every failure is an *Error (§17)", name, err, err)
			}
			if e.Kind != ErrKindType && e.Kind != ErrKindArgument {
				t.Errorf("%q reported kind %q; a wrong receiver is %q", name, e.Kind, ErrKindType)
			}
		})
	}
}

// Every terminal row walks the sequence and must therefore charge for it (§14.1); every
// lazy row returns without pulling anything and must therefore charge nothing. The split
// *is* the feature, so it is asserted rather than described.
func TestSeqRowsAndTheStepBudget(t *testing.T) {
	lazy := map[string]bool{
		"map": true, "filter": true, "reject": true, "flat_map": true,
		"take": true, "drop": true, "take_while": true, "drop_while": true,
	}
	// first/empty pull one element and stop, which one step pays for.
	oneElement := map[string]bool{"first": true, "empty": true}

	opts := DefaultOptions()
	opts.StepBudget = 1
	const n = 4 * stepCheckInterval

	for _, name := range MethodNames(KSeq) {
		t.Run(name, func(t *testing.T) {
			c := colCtx(t, opts)
			recv := seqOf(seqOfRange(&Range{Lo: 1, Hi: int64(n)}))
			_, err := colInvoke(c, KSeq, name, recv, seqRowArgs[name]...)
			if lazy[name] || oneElement[name] {
				if err != nil {
					t.Errorf("%q = %v; a row that pulls at most one element must answer "+
						"on a budget of one step", name, err)
				}
				return
			}
			if !errors.Is(err, ErrBudget) {
				t.Errorf("%q walked %d elements on a budget of one step and returned %v; "+
					"a terminal charges the budget (§14.1)", name, n, err)
			}
		})
	}
}

// A closure is script code, and script code raises. A lazy row hands the failure to
// whatever pulls it and a terminal hands it to its caller, so `s.map { raise("…") }.array`
// lands where it was written (§8.11) — and a lazy row that swallowed it would lose the
// error entirely, there being nobody else to give it to.
func TestSeqRowsPropagateFromTheirClosure(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	ran := 0
	for _, name := range MethodNames(KSeq) {
		args := make([]Value, len(seqRowArgs[name]))
		copy(args, seqRowArgs[name])
		takesClosure := false
		for i, a := range args {
			if a.Kind() == KFunc {
				args[i] = colBoom
				takesClosure = true
			}
		}
		if !takesClosure {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) {
			recv := seqOf(seqOfArray(colInts(1, 2, 3)))
			out, err := colInvoke(c, KSeq, name, recv, args...)
			if err == nil && out.Kind() == KSeq {
				// A lazy row raises where it is pulled, which is the whole point of it.
				_, err = colInvoke(c, KSeq, "array", out)
			}
			if err == nil {
				t.Fatalf("%q swallowed the error its closure raised", name)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%q returned %T (%v); want the *Error the closure raised", name, err, err)
			}
			if e.Msg != "boom" {
				t.Errorf("%q reported %q; want the closure's own message", name, e.Msg)
			}
		})
	}
	if ran < 14 {
		t.Fatalf("only %d rows took a closure; the table has lost its arguments", ran)
	}
}

// A seq is a recipe rather than a cursor, so two tasks may walk the same one: each
// terminal opens its own traversal, and §8.14's one-evaluator-at-a-time rule does the
// rest. This is the shape the race detector is here for.
func TestSeqAcrossTasks(t *testing.T) {
	t.Parallel()
	in := New(Options{Timeout: 5 * time.Second})

	src := `
		async fn count(s) { s.len }
		s = (1..300).seq.filter { it % 3 == 0 }
		a = count(s)
		b = count(s)
		[a.await, b.await].json
	`
	v, err := in.Eval(context.Background(), src, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "[100,100]" {
		t.Errorf("two tasks over one seq = %s; want [100,100]", v.Str())
	}
}

// errorsIsBudget reports whether err is the step-budget limit of §14.1.
func errorsIsBudget(err error) bool { return errors.Is(err, ErrBudget) }
