package mzs

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The limits of §14 have to bound what one Run of an untrusted script can cost the host
// that embeds it. These are the holes an audit found with a proof of concept each, and
// the PoCs themselves: before the fixes, each one ran past every limit it was given
// without an error.

// untrustedRun compiles and runs src, and reports how long the Run took.
func untrustedRun(t *testing.T, in *Interp, ctx context.Context, src string) (Result, error, time.Duration) {
	t.Helper()
	p, err := in.Compile("untrusted", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	res, err := in.RunResult(ctx, p, nil)
	return res, err, time.Since(start)
}

// liveHeap is the heap still in use after the collector has run twice.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// wantKind fails unless err is an *Error of the kind and every sentinel given.
func wantKind(t *testing.T, err error, kind string, sentinels ...error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v (%T); want an *Error of kind %q", err, err, kind)
	}
	if e.Kind != kind {
		t.Errorf("Kind = %q (%v); want %q", e.Kind, err, kind)
	}
	for _, s := range sentinels {
		if !errors.Is(err, s) {
			t.Errorf("errors.Is(%v, %v) = false", err, s)
		}
	}
	return e
}

// ---------------------------------------------------------------------------
// 1. Patterns compiled at run time
// ---------------------------------------------------------------------------

// The PoC: 256 patterns of 200 KB each, compiled in 3 600 steps with nothing charged to
// the memory budget, left about 2.7 GB in the Interp's regex cache after the Run. A
// pattern built at run time is bounded by MaxRegexBytes now, and refused before any of it
// is compiled.
func TestRuntimePatternIsBounded(t *testing.T) {
	in := New(Options{Timeout: 10 * time.Second})
	before := liveHeap()
	_, err, took := untrustedRun(t, in, nil, `base = "a" * 200000
i = 0
while i < 256 { r = regex(base + i.str); i += 1 }`)
	e := wantKind(t, err, ErrKindRegex, ErrRegexLimit, ErrBudget)
	if !strings.Contains(e.Msg, "pattern too long: 200001 bytes exceeds the limit of 4096") {
		t.Errorf("message = %q; want it to name the pattern's size and the limit", e.Msg)
	}
	if took > 2*time.Second {
		t.Errorf("the Run took %s; the cap refuses the first pattern at once", took)
	}
	if in.rxc.Len() != 0 {
		t.Errorf("the regex cache holds %d patterns; a refused pattern is never compiled", in.rxc.Len())
	}
	if grew := int64(liveHeap()) - int64(before); grew > 32<<20 {
		t.Errorf("the heap grew by %d MB after the Run; want nothing retained", grew>>20)
	}

	// The cap is a catchable regex error: it ends the call, not the Run.
	v := evOK(t, in, `try regex("a" * 5000) else (e) -> e["kind"]`, nil)
	if v.Str() != ErrKindRegex {
		t.Errorf("try regex(a * 5000) = %v; want the regex kind", v)
	}
	if v := evOK(t, in, `regex("a" * 4096).source.len`, nil); v.Int() != 4096 {
		t.Errorf("a pattern at the cap = %v; want it compiled", v)
	}

	// A host that raises the cap still has a bounded cache: what the Run compiled is
	// charged to it, and the patterns too large to cache are not kept after it.
	in = New(Options{Timeout: 60 * time.Second, StepBudget: -1, MaxRegexBytes: 1 << 20})
	before = liveHeap()
	res, err, _ := untrustedRun(t, in, nil, `base = "a" * 100000
i = 0
while i < 32 { r = regex(base + i.str); i += 1 }`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Steps < 32*100000 {
		t.Errorf("Steps = %d; compiling 32 patterns of 100 KB should cost at least a step per byte", res.Steps)
	}
	if res.PeakMemory == 0 {
		t.Errorf("PeakMemory = 0; a compiled pattern is charged to the memory budget")
	}
	if c := in.rxc.Cost(); c > regexCacheBytes {
		t.Errorf("the regex cache holds %d bytes; want at most %d", c, regexCacheBytes)
	}
	// Before the fix this retained about 32 × 5.5 MB.
	if grew := int64(liveHeap()) - int64(before); grew > 48<<20 {
		t.Errorf("the heap grew by %d MB after the Run; want the cache bounded to a few MB", grew>>20)
	}
}

// Patterns at the cap fill the cache up to its byte bound and no further, however many
// a Run compiles.
func TestRegexCacheIsBoundedInBytes(t *testing.T) {
	in := New(Options{Timeout: 60 * time.Second, StepBudget: -1})
	_, err, _ := untrustedRun(t, in, nil, `base = "a" * 4000
i = 0
while i < 300 { r = regex(base + i.str); i += 1 }`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := in.rxc.Cost(); c == 0 || c > regexCacheBytes {
		t.Errorf("the regex cache holds %d bytes; want some, and at most %d", c, regexCacheBytes)
	}
	if n := in.rxc.Len(); n >= 300 {
		t.Errorf("the regex cache holds %d patterns; the byte bound should have evicted most", n)
	}
	// A short pattern is cached, so a hot dialogue still compiles it once.
	evOK(t, in, `regex("^hi")`, nil)
	if _, ok := in.rxc.Get("^hi\x00"); !ok {
		t.Errorf("a short runtime pattern was not cached")
	}
}

// What a compiled pattern holds counts against MaxMemory while the Run holds it.
func TestCompiledPatternsCountAgainstMemory(t *testing.T) {
	in := New(Options{MaxMemory: 4 << 20, Timeout: 30 * time.Second, StepBudget: -1})
	_, err, _ := untrustedRun(t, in, nil, `xs = []; base = "a" * 4000; i = 0
while i < 1000 { xs.push(regex(base + i.str)); i += 1 }`)
	wantKind(t, err, ErrKindLimit, ErrMemory)

	// A short pattern that unrolls into a long program weighs what it unrolled to.
	in = New(Options{MaxMemory: 4 << 20, Timeout: 30 * time.Second, StepBudget: -1})
	_, err, _ = untrustedRun(t, in, nil, `xs = []; i = 0
while i < 1000 { xs.push(regex("(?:x${i}){1000}")); i += 1 }`)
	wantKind(t, err, ErrKindLimit, ErrMemory)
}

// A string pattern matches literally, and replace finds it without compiling anything —
// as split, count and index already did.
func TestLiteralReplaceCompilesNothing(t *testing.T) {
	in := New(Options{})
	tests := []struct{ src, want string }{
		{`"abc".replace("", "-")`, "-a-b-c-"},
		{`"abc".replace_first("", "-")`, "-abc"},
		{`"aaa".replace("a", "[\\0]")`, "[a][a][a]"},
		{`"aaa".replace("a", "[\\&|\\1|\\k<x>|\\\\]")`, `[a|||\][a|||\][a|||\]`},
		{`"a.b.c".replace(".", "")`, "abc"},
		{`"a.b.c".replace_first(".", "")`, "ab.c"},
		{`"привет мир".replace("и", "И")`, "прИвет мИр"},
		{`"при".replace("", "·")`, "·п·р·и·"},
		{`"a-b-c".replace("-") { it + it }`, "a--b--c"},
		{`"a-b".replace("-") { (m, g) -> "${m}${g}" }`, "a-b"},
		{`"none".replace("x", "y")`, "none"},
		{`"(a)".replace("(a)", "b")`, "b"},
	}
	for _, tt := range tests {
		if got := evOK(t, in, tt.src, nil); got.Str() != tt.want {
			t.Errorf("%s = %q; want %q", tt.src, got.Str(), tt.want)
		}
	}
	evOK(t, in, `base = "x" * 3000; i = 0; while i < 50 { "y".replace(base + i.str, "z"); i += 1 }`, nil)
	if n := in.rxc.Len(); n != 0 {
		t.Errorf("the regex cache holds %d patterns after string replaces; want none", n)
	}
}

// A replacement is bounded as it is built: 2 000 matches of a 1 MB template are 2 GB,
// which used to be allocated in full and only then refused, in 14 steps.
func TestReplaceIsBoundedWhileBuilt(t *testing.T) {
	in := New(Options{Timeout: 10 * time.Second})
	for _, src := range []string{
		`s = "a" * 2000; r = "b" * 1000000; s.replace(/a/, r)`,
		`s = "a" * 2000; r = "b" * 1000000; s.replace(/(?=a)a/, r)`,
		`s = "a" * 2000; r = "b" * 1000000; s.replace("a", r)`,
		`s = "a" * 2000; r = "b" * 1000000; s.replace(/a/) { r }`,
		`s = "a" * 2000; r = "b" * 1000000; s.replace("a") { r }`,
		`s = "a" * 4000000; s.replace(/a+/, "\\0" * 1000)`,
	} {
		before := liveHeap()
		_, err, took := untrustedRun(t, in, nil, src)
		e := wantKind(t, err, ErrKindLimit, ErrBudget)
		if !strings.Contains(e.Msg, "string too large") {
			t.Errorf("%s: message = %q; want the string cap", src, e.Msg)
		}
		if took > 3*time.Second {
			t.Errorf("%s took %s", src, took)
		}
		if grew := int64(liveHeap()) - int64(before); grew > 64<<20 {
			t.Errorf("%s: the heap grew by %d MB", src, grew>>20)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Matching
// ---------------------------------------------------------------------------

// The PoC: each match of the lookahead scans the rest of the subject, and every match
// had a fresh budget, so `"a" * 40000` took 11 s in 19 steps without an error — and
// `"a" * 199000` minutes. One call is one budget now.
func TestRegexCallIsOneBudget(t *testing.T) {
	in := New(Options{})
	_, err, took := untrustedRun(t, in, nil, `s = "a" * 40000; r = s.replace(/a(?=[^z]*z|)/, "b"); r.len`)
	e := wantKind(t, err, ErrKindRegex, ErrRegexLimit, ErrBudget)
	if !strings.Contains(e.Msg, "regex step budget exceeded (200000 steps)") {
		t.Errorf("message = %q; want the regex budget named", e.Msg)
	}
	if took > 2*time.Second {
		t.Errorf("the Run took %s; the budget stops it in milliseconds", took)
	}
	if !e.Catchable() {
		t.Errorf("a regex budget overrun is catchable (§11.2), Catchable() = false")
	}
	v := evOK(t, in, `s = "a" * 40000; try s.replace(/a(?=[^z]*z|)/, "b") else (e) -> e["kind"]`, nil)
	if v.Str() != ErrKindRegex {
		t.Errorf("try … else = %v; want the regex error caught", v)
	}

	// Every row that matches spends one budget per call.
	for _, src := range []string{
		`s ~ /a(?=[^z]*z|)z/`,
		`s.matches(/a(?=[^z]*z|)/)`,
		`/a(?=[^z]*z|)/.matches(s)`,
		`s.count(/a(?=[^z]*z|)/)`,
		`s.split(/a(?=[^z]*z|)/)`,
		`s.last_index(/a(?=[^z]*z|)/)`,
		`s.replace(/a(?=[^z]*z|)/) { "b" }`,
		`s.replace_first(/a(?=[^z]*b)/, "b")`,
		`match s { /a(?=[^z]*z|)z/ -> 1; else -> 0 }`,
	} {
		_, err, took := untrustedRun(t, in, nil, `s = "a" * 40000; `+src)
		wantKind(t, err, ErrKindRegex, ErrRegexLimit, ErrBudget)
		if took > 2*time.Second {
			t.Errorf("%s took %s", src, took)
		}
	}
}

// Backtracking is charged to the Run, so a host that turns the regex budget off is still
// protected by StepBudget, and one that turns both off by Timeout and its context: the
// matcher checks them as it searches.
func TestRegexWorkIsChargedToTheRun(t *testing.T) {
	const poc = `s = "a" * 199000; r = s.replace(/a(?=[^z]*z|)/, "b"); r.len`

	// A budget well under the default keeps this quick under the race detector, which
	// slows the matcher down twenty times; the default is spent the same way.
	const budget = 200_000
	in := New(Options{RegexSteps: 1 << 40, StepBudget: budget, Timeout: 30 * time.Second})
	res, err, took := untrustedRun(t, in, nil, poc)
	e := wantKind(t, err, ErrKindLimit, ErrBudget)
	if e.Catchable() {
		t.Errorf("the Run's step budget is not catchable")
	}
	if res.Steps < budget || took > 5*time.Second {
		t.Errorf("Steps = %d in %s; want the budget spent, quickly", res.Steps, took)
	}

	in = New(Options{RegexSteps: 1 << 40, StepBudget: -1, Timeout: 300 * time.Millisecond})
	_, err, took = untrustedRun(t, in, nil, poc)
	wantKind(t, err, ErrKindLimit, ErrTimeout)
	if took > 3*time.Second {
		t.Errorf("Timeout 300ms, the Run took %s", took)
	}

	in = New(Options{RegexSteps: 1 << 40, StepBudget: -1, Timeout: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err, took = untrustedRun(t, in, ctx, poc)
	wantKind(t, err, ErrKindLimit, ErrCanceled, ErrTimeout, context.DeadlineExceeded)
	if took > 3*time.Second {
		t.Errorf("a context of 300ms, the Run took %s", took)
	}

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err, took = untrustedRun(t, in, ctx, poc)
	wantKind(t, err, ErrKindLimit, ErrCanceled, context.Canceled)
	if errors.Is(err, ErrTimeout) {
		t.Errorf("a cancel is not a timeout: %v", err)
	}
	if took > 3*time.Second {
		t.Errorf("cancelled after 100ms, the Run took %s", took)
	}
}

// A small regex costs what it cost: the meter's remainder is dropped, so a condition
// matching a short message is not charged extra steps.
func TestShortMatchCostsNoExtraSteps(t *testing.T) {
	in := New(Options{})
	a, _, _ := untrustedRun(t, in, nil, `"меню" ~ /\bменю\b/i`)
	b, _, _ := untrustedRun(t, in, nil, `"меню" == "меню"`)
	if a.Steps != b.Steps {
		t.Errorf("a short match took %d steps, a comparison %d; want the same", a.Steps, b.Steps)
	}
}

// ---------------------------------------------------------------------------
// 3. Host calls
// ---------------------------------------------------------------------------

// The PoC: a host function that waits in Ctx.Blocking on Ctx.Context() was not
// interrupted by Timeout, because the deadline was not on that context, and nothing
// checked it when the wait returned — five 300 ms calls under a 200 ms Timeout ran for
// 1.5 s and returned no error.
func TestHostContextCarriesTheDeadline(t *testing.T) {
	var cause error
	var deadline time.Time
	in := New(Options{Timeout: 200 * time.Millisecond})
	in.Register("slow", 0, func(c *Ctx, args []Value) (Value, error) {
		ctx := c.Context()
		deadline, _ = ctx.Deadline()
		var err error
		c.Blocking(func() {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				cause = context.Cause(ctx)
			case <-time.After(300 * time.Millisecond):
			}
		})
		return Nil(), err
	})
	start := time.Now()
	_, err, took := untrustedRun(t, in, nil, `i = 0; while i < 5 { slow(); i += 1 }`)
	wantKind(t, err, ErrKindLimit, ErrTimeout)
	if took > time.Second {
		t.Errorf("Timeout 200ms, the Run took %s", took)
	}
	if deadline.IsZero() || deadline.After(start.Add(250*time.Millisecond)) {
		t.Errorf("Ctx.Context().Deadline() = %v; want the Run's, 200ms after %v", deadline, start)
	}
	if !errors.Is(cause, ErrTimeout) {
		t.Errorf("context.Cause = %v; want ErrTimeout", cause)
	}

	// A `try` around the call does not get past it: the host answered with the
	// context's error, and the Run's deadline is what that means.
	_, err, _ = untrustedRun(t, in, nil, `try slow() else "caught"`)
	wantKind(t, err, ErrKindLimit, ErrTimeout)
}

// A host call that ignores the context is not interrupted — nothing can do that — but
// the Run ends as soon as it returns: no statement runs after a wait past the deadline.
func TestBlockingChecksLimitsWhenItReturns(t *testing.T) {
	var out bytes.Buffer
	in := New(Options{Timeout: 100 * time.Millisecond, Stdout: &out})
	in.Register("stubborn", 0, func(c *Ctx, args []Value) (Value, error) {
		c.Blocking(func() { time.Sleep(250 * time.Millisecond) })
		return Str("done"), nil
	})
	_, err, took := untrustedRun(t, in, nil, `x = try stubborn() else "caught"; print("after ${x}")`)
	wantKind(t, err, ErrKindLimit, ErrTimeout)
	if out.Len() != 0 {
		t.Errorf("the script went on after the wait: %q", out.String())
	}
	if took > time.Second {
		t.Errorf("the Run took %s", took)
	}

	// A wait inside the deadline changes nothing.
	in = New(Options{Timeout: time.Second, Stdout: &out})
	in.Register("quick", 0, func(c *Ctx, args []Value) (Value, error) {
		c.Blocking(func() { time.Sleep(10 * time.Millisecond) })
		return Str("done"), nil
	})
	if v := evOK(t, in, `quick()`, nil); v.Str() != "done" {
		t.Errorf("quick() = %v; want done", v)
	}
}

// A host function that waits on the context without Blocking is caught when it returns.
func TestHostWaitWithoutBlocking(t *testing.T) {
	in := New(Options{Timeout: 100 * time.Millisecond})
	in.Register("wait", 0, func(c *Ctx, args []Value) (Value, error) {
		<-c.Context().Done()
		return Nil(), nil
	})
	var out bytes.Buffer
	in.opts.Stdout = &out
	_, err, took := untrustedRun(t, in, nil, `wait(); print("after")`)
	wantKind(t, err, ErrKindLimit, ErrTimeout)
	if out.Len() != 0 || took > time.Second {
		t.Errorf("printed %q, took %s; want the Run to end when wait returned", out.String(), took)
	}

	// With the deadline turned off, the context has none.
	in = New(Options{Timeout: -1})
	in.Register("dl", 0, func(c *Ctx, args []Value) (Value, error) {
		_, ok := c.Context().Deadline()
		return Bool(ok), nil
	})
	if v := evOK(t, in, `dl()`, nil); v.Bool() {
		t.Errorf("Timeout -1, Ctx.Context() has a deadline")
	}
}

// ---------------------------------------------------------------------------
// 4. Every limit says which it is
// ---------------------------------------------------------------------------

func TestEveryLimitHasItsSentinel(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel2()
	time.Sleep(5 * time.Millisecond)

	tests := []struct {
		name      string
		opts      Options
		ctx       context.Context
		src       string
		kind      string
		want      []error
		catchable bool
	}{
		{"timeout", Options{Timeout: 20 * time.Millisecond, StepBudget: -1}, nil,
			`while true { }`, ErrKindLimit, []error{ErrTimeout}, false},
		{"step budget", Options{StepBudget: 1000}, nil,
			`while true { }`, ErrKindLimit, []error{ErrBudget}, false},
		{"memory", Options{MaxMemory: 1 << 20}, nil,
			`xs = []; while true { xs.push("x" * 1000 + xs.len.str) }`, ErrKindLimit, []error{ErrMemory}, false},
		{"depth", Options{MaxDepth: 20}, nil,
			`fn f(n) { f(n + 1) }; f(0)`, ErrKindLimit, []error{ErrDepth}, false},
		{"cancelled context", Options{}, canceled,
			`while true { }`, ErrKindLimit, []error{ErrCanceled, context.Canceled}, false},
		{"expired context", Options{}, expired,
			`while true { }`, ErrKindLimit, []error{ErrCanceled, ErrTimeout, context.DeadlineExceeded}, false},
		{"regex budget", Options{}, nil,
			`("a" * 40) ~ /\b(a+)+b/`, ErrKindRegex, []error{ErrRegexLimit, ErrBudget}, true},
		{"regex pattern size", Options{}, nil,
			`regex("a" * 5000)`, ErrKindRegex, []error{ErrRegexLimit, ErrBudget}, true},
		{"string cap in a replace", Options{}, nil,
			`("a" * 100).replace(/a/, "b" * 100000)`, ErrKindLimit, []error{ErrBudget}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err, _ := untrustedRun(t, New(tt.opts), tt.ctx, tt.src)
			e := wantKind(t, err, tt.kind, tt.want...)
			if e.Catchable() != tt.catchable {
				t.Errorf("Catchable() = %v; want %v", e.Catchable(), tt.catchable)
			}
			if !tt.catchable && errors.Is(err, ErrRegexLimit) {
				t.Errorf("a limit of the Run is not ErrRegexLimit: %v", err)
			}
		})
	}
}
