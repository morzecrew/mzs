package mzs

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// RFC 0011: the memory budget of §14.2.

func memInterp(max int64) *Interp {
	o := DefaultOptions()
	o.MaxMemory = max
	o.Timeout = 20 * time.Second
	o.StepBudget = -1
	return New(o)
}

func memRun(t *testing.T, in *Interp, src string, vars map[string]Value) (Result, error) {
	t.Helper()
	p, err := in.Compile("mem", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return in.RunResult(context.Background(), p, vars)
}

func wantMemoryError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrMemory) {
		t.Fatalf("error = %v, want ErrMemory", err)
	}
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrKindLimit {
		t.Fatalf("error = %#v, want Kind %q", err, ErrKindLimit)
	}
	if !strings.Contains(e.Msg, "memory budget exceeded") {
		t.Fatalf("message = %q, want it to name the memory budget", e.Msg)
	}
}

// The program of the RFC's motivation: every option at its default, and the loop used
// to run until the clock stopped it with about 1.5 GB resident.
func TestMemoryBudgetStopsTheRFCProgram(t *testing.T) {
	in := New(Options{})
	_, err := in.Eval(context.Background(), `xs = []; s = "x" * 1000000; i = 0
while true { xs.push(s + i.str); i += 1 }`, nil)
	wantMemoryError(t, err)
	if !strings.Contains(err.Error(), "the limit is 64 MiB") {
		t.Errorf("error = %v, want the default 64 MiB budget in it", err)
	}
}

// Live, not cumulative: a loop that allocates and drops runs to the end under a budget
// it allocates many times over.
func TestMemoryBudgetReleasesGarbage(t *testing.T) {
	in := memInterp(4 << 20)
	res, err := memRun(t, in, `s = "x" * 500000
n = 0
for i in 0..200 { t = s + i.str; n += t.len }
n`, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Value.Int(); got <= 100_000_000 {
		t.Errorf("n = %d, want the whole loop to have run", got)
	}
	if res.PeakMemory == 0 || res.PeakMemory > 4<<20 {
		t.Errorf("PeakMemory = %d, want a walk to have run and stayed under the budget", res.PeakMemory)
	}
}

// Every place a value can be held while the script allocates more is a root. Each row
// keeps about 2 MB reachable only through one of them under a 1 MiB budget.
func TestMemoryBudgetSeesEveryRoot(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"a global", `$keep = []; for i in 0..40 { $keep.push("x" * 50000 + i.str) }`},
		{"a local", `keep = []; for i in 0..40 { keep.push("x" * 50000 + i.str) }`},
		{"a dict", `d = {}; for i in 0..40 { d[i.str] = "x" * 50000 + i.str }`},
		{"a closure's captured scope", `fns = []
for i in 0..40 { big = "x" * 50000 + i.str; fns.push({ big }) }`},
		{"the frames of a recursion", `fn f(n) { big = "x" * 50000 + n.str; n == 0 ? 0 : f(n - 1) + big.len }
f(40)`},
		{"an array literal being built", `fn big(i) { "x" * 50000 + i.str }
fn f(n) { n == 0 ? [] : [big(n), f(n - 1)] }
f(40)`},
		{"the arguments of a call in progress", `fn big(i) { "x" * 50000 + i.str }
fn f(a, n) { n == 0 ? a.len : f(big(n), n - 1) + a.len }
f("", 40)`},
		{"the left operand of a binary", `fn big(i) { "x" * 50000 + i.str }
fn f(n) { n == 0 ? "" : (big(n) == f(n - 1) ? "same" : "different") }
f(40)`},
		{"a map in progress", `(0..40).map { "x" * 50000 + it.str }`},
		{"a dict map in progress", `d = {}; for i in 0..40 { d[i.str] = i }
d.map { (k, v) -> "x" * 50000 + k }`},
		{"a group_by in progress", `(0..40).group_by { "x" * 50000 + it.str }`},
		{"a dict map_values in progress", `d = {}; for i in 0..40 { d[i.str] = i }
d.map_values { "x" * 50000 + it.str }`},
		{"an array min_by's best key", `[1, 2].min_by { "x" * 600000 + it.str }`},
		{"a dict max_by's best key", `{a: 1, b: 2}.max_by { (k, _) -> "x" * 600000 + k }`},
		{"an accumulate in progress", `(0..40).accumulate("") { (acc, x) -> "x" * 50000 + x.str }`},
		{"a seq being collected", `(0..40).seq.map { "x" * 50000 + it.str }.array`},
		{"a seq uniq's seen keys", `seq { (i) -> i < 40 ? i : nil }.uniq { "x" * 50000 + it.str }.len`},
		{"a seq uniq's seen keys under a for", `n = 0
for x in (seq { (i) -> i < 40 ? i : nil }.uniq { "x" * 50000 + it.str }) { n += 1 }`},
		{"a seq each_slice batch being filled", `(0..40).seq.map { "x" * 50000 + it.str }.each_slice(41).len`},
		{"a seq each_cons window", `(0..40).seq.map { "x" * 50000 + it.str }.each_cons(41).len`},
		{"a seq last window", `(0..40).seq.map { "x" * 50000 + it.str }.last(41).len`},
		{"a seq tally in progress", `(0..40).seq.map { "x" * 50000 + it.str }.tally`},
		{"a seq group_by in progress", `(0..40).seq.group_by { "x" * 50000 + it.str }`},
		{"a seq max_by's best key", `[1, 2].seq.max_by { "x" * 600000 + it.str }`},
		{"a flat_map's inner array", `(0..0).seq.flat_map { (_) -> ["a", "b" * 600000] }
.map { it.len < 100 ? ("z" * 500000).len : 0 }.sum`},
		{"a flat_map's inner array beside another flat_map under a zip",
			`(1..3).seq.flat_map { [it] }.zip((0..0).seq.flat_map { (_) -> [0, 0, "b" * 600000] })
.map { it[0] == 2 ? ("z" * 600000).len : 0 }.sum`},
		{"a wrap result being built", `x = ("abcdefghij " * 80000).wrap(5); x.len`},
		{"a zip row being built", `seq { (i) -> i < 1 ? "a" * 600000 : nil }
.zip(seq { (i) -> i < 1 ? "b" * 600000 : nil }).len`},
		{"a uniq beside a flat_map that releases its inner cursors",
			`(1..40).seq.flat_map { [it] }.zip((0..40).seq.map { "x" * 50000 + it.str }.uniq { it }).len`},
		{"a seq's source array", `keep = []
for i in 0..40 { keep.push(("x" * 50000 + i.str).chars.first(1).join + ("y" * 50000)) }
s = keep.seq; keep = nil
more = []; for i in 0..40 { more.push("z" * 50000 + i.str) }`},
		{"a task's own frames", `async fn work() { keep = []; for i in 0..40 { keep.push("x" * 50000 + i.str) }; keep.len }
work().await`},
		{"a task's result", `async fn work() { "x" * 2000000 }
t = work(); t.await
more = []; for i in 0..10 { more.push("z" * 50000 + i.str) }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := memRun(t, memInterp(1<<20), tt.src, nil)
			wantMemoryError(t, err)
		})
	}
}

// The state a lazy seq row keeps is held for the traversal that pulls it and no longer
// (RFC 0016 §3): the hold is released with the frame of the `for` or the terminal that
// pulled it, so eight traversals in one block, each keeping about 550 KB, run under a
// 1 MiB budget.
func TestMemorySeqStateEndsWithItsTraversal(t *testing.T) {
	loop := `for x in ((0..10).seq.map { "%s" * 50000 + it.str }.uniq { it }) { n += 1 }
`
	src := "n = 0\n"
	for _, c := range []string{"a", "b", "c", "d"} {
		src += strings.Replace(loop, "%s", c, 1)
	}
	src += `for k in ["e", "f", "g", "h"] { n += (0..10).seq.map { k * 50000 + it.str }.uniq { it }.len }
`
	// Inside one traversal too: each inner cursor of a flat_map, and each part of a
	// concat, releases what it kept when it ends.
	src += `n += (1..2000).seq.flat_map { (_) -> (1..20).seq.map { "x" * 1000 + it.str }.uniq }.len / 1000
n += (1..30).seq.map { "x" * 20000 + it.str }.uniq.concat((1..30).seq.map { "y" * 20000 + it.str }.uniq).len
n`
	res, err := memRun(t, memInterp(1<<20), src, nil)
	if err != nil {
		t.Fatalf("Run: %v; the keys of a finished traversal are not live", err)
	}
	if res.Value.Int() != 88+40+60 {
		t.Errorf("n = %v; want every loop to have run", res.Value)
	}
}

// What the host bound is not the script's: a value larger than the budget passed in
// through vars costs nothing, and the script may read it.
func TestMemoryBudgetIgnoresHostValues(t *testing.T) {
	in := memInterp(1 << 20)
	big := Str(strings.Repeat("x", 4<<20))
	res, err := memRun(t, in, `n = 0; for i in 0..300 { n += $big.len + ("y" * 10000 + i.str).len }; n`,
		map[string]Value{"big": big})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Value.Int() == 0 || res.PeakMemory == 0 {
		t.Errorf("value = %v, PeakMemory = %d; want the loop to have run and walked", res.Value, res.PeakMemory)
	}
}

// A limit is a limit: try does not catch it and ensure does not run (§8.11).
func TestMemoryBudgetIsUncatchable(t *testing.T) {
	in := memInterp(1 << 20)
	_, err := memRun(t, in, `$ran = false
try { keep = []; while true { keep.push("x" * 100000) } } else "swallowed" ensure { $ran = true }`, nil)
	wantMemoryError(t, err)
	res, _ := memRun(t, in, `$ran = false
try { keep = []; while true { keep.push("x" * 100000) } } ensure { $ran = true }`, nil)
	if res.Globals["$ran"].Truthy() {
		t.Errorf("ensure ran after the memory budget ended the Run")
	}
}

// D15: the walk depends on the script's values only, so the Run fails at the same step.
func TestMemoryBudgetIsDeterministic(t *testing.T) {
	in := memInterp(2 << 20)
	src := `keep = []; i = 0; while true { keep.push("x" * (1000 + i * 37 % 5000)); i += 1 }`
	var steps []int64
	for range 3 {
		res, err := memRun(t, in, src, nil)
		wantMemoryError(t, err)
		steps = append(steps, res.Steps)
	}
	if steps[0] != steps[1] || steps[1] != steps[2] {
		t.Errorf("steps at the failure = %v, want the same every run", steps)
	}
}

// A single allocation larger than what is left is refused before it is made.
func TestMemoryBudgetRefusesOneLargeAllocation(t *testing.T) {
	in := memInterp(1 << 20)
	_, err := memRun(t, in, `s = "x" * 2000000`, nil)
	wantMemoryError(t, err)
}

// A negative budget disables it; 0 is the default.
func TestMemoryBudgetOptions(t *testing.T) {
	if got := New(Options{}).opts.MaxMemory; got != DefaultMaxMemory {
		t.Errorf("default MaxMemory = %d, want %d", got, DefaultMaxMemory)
	}
	if got := DefaultOptions().MaxMemory; got != DefaultMaxMemory {
		t.Errorf("DefaultOptions().MaxMemory = %d, want %d", got, DefaultMaxMemory)
	}
	in := memInterp(-1)
	res, err := memRun(t, in, `keep = []; for i in 0..20 { keep.push("x" * 1000000 + i.str) }; keep.len`, nil)
	if err != nil {
		t.Fatalf("Run with the budget disabled: %v", err)
	}
	if res.Value.Int() != 21 || res.PeakMemory != 0 {
		t.Errorf("value = %v, PeakMemory = %d; want 21 and no walk", res.Value, res.PeakMemory)
	}
}

// A cycle is walked once.
func TestMemoryBudgetWalksCycles(t *testing.T) {
	in := memInterp(1 << 20)
	res, err := memRun(t, in, `d = {}; d["self"] = d; xs = [d]; xs.push(xs)
n = 0; for i in 0..200 { n += ("x" * 20000 + i.str).len }; n`, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PeakMemory == 0 {
		t.Errorf("PeakMemory = 0, want the loop to have walked")
	}
}

// Every kind of value is weighed, and none is free: a loop that keeps a few thousand of
// one kind alive ends on the budget, whatever the kind.
func TestMemoryBudgetWeighsEveryKind(t *testing.T) {
	for _, tt := range []struct{ name, make string }{
		{"a named match", `"ab".captures(/(?<x>a)(?<y>b)/)`},
		{"a regex", `regex("a" * 50 + i.str)`},
		{"a range", `0..i`},
		{"a closure", `{ i }`},
		{"a seq", `[i].seq`},
		{"a dict", `{k: i}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := memRun(t, memInterp(256<<10), `keep = []; i = 0
while true { keep.push(`+tt.make+`); i += 1 }`, nil)
			wantMemoryError(t, err)
		})
	}
}

// What the host installed with SetGlobal is the baseline too, and what a loaded module
// holds is live.
func TestMemoryBudgetBaselineAndModules(t *testing.T) {
	in := memInterp(1 << 20)
	in.SetGlobal("catalog", Str(strings.Repeat("c", 4<<20)))
	res, err := memRun(t, in, `n = 0; for i in 0..300 { n += $catalog.len + ("y" * 10000 + i.str).len }; n`, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PeakMemory == 0 {
		t.Errorf("PeakMemory = 0, want the loop to have walked")
	}

	o := DefaultOptions()
	o.MaxMemory = 1 << 20
	o.ModuleLoader = func(from, path string) (string, string, error) {
		return path, `export big = "m" * 700000`, nil
	}
	in = New(o)
	_, err = memRun(t, in, `include lib from "./lib.mzs"
keep = []; for i in 0..20 { keep.push("x" * 30000 + i.str) }`, nil)
	wantMemoryError(t, err)
}

// The walk does not recurse: a chain as deep as a raised budget allows is measured, not
// a stack overflow that takes the host down with it.
func TestMemoryBudgetWalksDeepChains(t *testing.T) {
	// 16 MB of stack is far more than the interpreter needs for this program and far less
	// than a recursive walk of a 400,000-deep chain would take.
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	in := memInterp(512 << 20)
	res, err := memRun(t, in, `a = []; for i in 0..400000 { a = [a] }
d = {}; for i in 0..200000 { d = {link: d} }
f = { 0 }; for i in 0..200000 { g = f; f = { g() } }
n = 0; for i in 0..3000 { n += ("x" * 100000).len }; n`, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PeakMemory < 20<<20 {
		t.Errorf("PeakMemory = %d, want the chains counted", res.PeakMemory)
	}
}

// A task that has not started yet still holds its arguments: its frame is a root from
// the moment it is spawned, not from when its body first runs.
func TestMemoryBudgetSeesUnstartedTasks(t *testing.T) {
	in := memInterp(4 << 20)
	_, err := memRun(t, in, `async fn hold(s) { s.len }
ts = []
for i in 0..40 { ts.push(hold("x" * 200000 + i.str)) }`, nil)
	wantMemoryError(t, err)
}

func TestByteSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 512: "512 B", 1536: "1.50 KiB", 64 << 20: "64 MiB",
		1<<30 + 1<<29: "1536 MiB", 2 << 30: "2 GiB", 68_000_000: "64.85 MiB",
	} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// The A4 shape with and without the budget: a Run that never comes near it must not
// pay for it beyond the bookkeeping of the roots.
func BenchmarkMemoryBudget(b *testing.B) {
	for _, bb := range []struct {
		name string
		max  int64
	}{{"off", -1}, {"on", 0}} {
		o := DefaultOptions()
		o.MaxMemory = bb.max
		in := New(o)
		prog, err := in.Compile("bench", `$s.lower.trim == "оператор"`)
		if err != nil {
			b.Fatal(err)
		}
		vars := map[string]Value{"s": Str("  ОПЕРАТОР ")}
		b.Run(bb.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := in.Run(context.Background(), prog, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A piece cut from a string is a string of its own (RFC 0011): were it to share its
// source, the walk would charge eleven bytes for the megabyte they keep alive — which is
// exactly what the budget cannot see, so the test looks at the bytes themselves.
func TestMemoryBudgetCutsCopy(t *testing.T) {
	src := " " + strings.Repeat("a", 10) + "\n" + strings.Repeat("y", 1<<20) + " \r\n"
	lo := uintptr(unsafe.Pointer(unsafe.StringData(src)))
	hi := lo + uintptr(len(src))
	in := memInterp(0)
	for _, cut := range []string{
		`$s.split("\n").first`, `$s.split("\n", 2).first`, `$s.lines.first`, `$s.lines.last`,
		`$s.trim`, `$s.trim_start`, `$s.trim_end`, `$s.chomp`, `$s.chomp("\n")`, `$s.chomp("")`,
		`$s.chop`, `$s.captures(/(a+)/)[0]`, `$s.captures(/(a+)/)[1]`, `$s.matches(/a+/).first`,
		`$s.matches(/(a)(a)/).first.first`,
	} {
		v, err := in.Eval(context.Background(), cut, map[string]Value{"s": Str(src)})
		if err != nil {
			t.Fatalf("%s: %v", cut, err)
		}
		got := v.Str()
		if got == "" || len(got) == len(src) {
			t.Fatalf("%s = %q: the cut should be a proper piece", cut, got)
		}
		if p := uintptr(unsafe.Pointer(unsafe.StringData(got))); p >= lo && p < hi {
			t.Errorf("%s shares its source's bytes", cut)
		}
	}
}

// Moving the step counter, as http.serve does per request, keeps the memory charge in
// step: what was owed stays owed, and nothing already charged is charged again.
func TestSetStepsKeepsTheMemoryChargeOwed(t *testing.T) {
	sh := &runShared{steps: 500, memSteps: 300} // 200 steps not yet charged
	sh.setSteps(0)
	if owed := sh.steps - sh.memSteps; owed != 200 {
		t.Errorf("after a reset %d steps are owed, want 200", owed)
	}
	sh.steps += 50
	sh.setSteps(1000) // the Run's total, restored
	if owed := sh.steps - sh.memSteps; owed != 250 {
		t.Errorf("after a restore %d steps are owed, want 250", owed)
	}
}

// The live set is the Run's: a task that goes over MaxMemory ends the Run with
// ErrMemory whether or not the program awaits it (§14.2). Without that, a task nobody
// awaited would keep the limit to itself, and the Run would report success.
func TestMemoryBudgetEndsTheRunFromAnUnawaitedTask(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.MaxMemory = 1 << 20
	_, err := memRun(t, New(o), `async fn hog() { s = "x" * 3000000; s.len }
hog()
"done"`, nil)
	wantMemoryError(t, err)

	// A task the script cancelled ended on the cancel, not on the budget.
	_, err = memRun(t, New(o), `async fn hog() { s = "x" * 3000000; s.len }
t = hog()
t.cancel
"done"`, nil)
	if err != nil {
		t.Fatalf("a cancelled task failed the Run: %v", err)
	}

	// A wait that expired handed the caller a timeout, not the memory error that came
	// after it: the Run still ends with ErrMemory. The task is held at a gate that only
	// the timeout's handler opens, so the wait cannot see the task finish first.
	in := New(o)
	gate := make(chan struct{})
	in.Register("__gate", 0, func(c *Ctx, args []Value) (Value, error) {
		c.Blocking(func() {
			select {
			case <-gate:
			case <-time.After(5 * time.Second):
			}
		})
		return Nil(), nil
	})
	timedOut := false
	in.Register("__open", 0, func(c *Ctx, args []Value) (Value, error) {
		timedOut = true
		close(gate)
		return Nil(), nil
	})
	_, err = memRun(t, in, `async fn hog() { __gate(); s = "x" * 3000000; s.len }
t = hog()
try t.timeout(0.001) else (e) -> __open()
"done"`, nil)
	if !timedOut {
		t.Fatal("the wait did not time out")
	}
	wantMemoryError(t, err)
}
