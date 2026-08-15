package mzs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// §8.14: `async fn` starts a task, `await` reads it, and the Run owns every goroutine it
// made. These tests pin the four things that make the feature safe rather than merely
// present: one evaluator at a time, one budget, one deadline, and no goroutine left over.

func asyncOpts() Options {
	o := DefaultOptions()
	o.Timeout = 5 * time.Second
	return o
}

func asyncEval(t *testing.T, src string) (Value, error) {
	t.Helper()
	return New(asyncOpts()).Eval(context.Background(), src, nil)
}

// asyncMustEval fails the test when the script does not run.
func asyncMustEval(t *testing.T, src string) Value {
	t.Helper()
	v, err := asyncEval(t, src)
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return v
}

// TestAsyncBasics is the whole surface in one table: a task is a value, `await` is what
// turns it into the value the body produced, and a second `await` is the same answer.
func TestAsyncBasics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a call returns a task", "async fn f() { 1 }\ntype(f())", "task"},
		{"await yields the body's value", "async fn f(n) { n * 2 }\nf(21).await", "42"},
		{"await twice is the same value", "async fn f() { 7 }\nt = f()\nt.await + t.await", "14"},
		{"await is also a prefix call (UFCS)", "async fn f() { 7 }\nawait(f())", "7"},
		{"done before and after", "async fn f() { 1 }\nt = f()\n[t.done, t.await, t.done].json",
			`[false,1,true]`},
		{"a task closes over its scope", "x = 5\nasync fn f() { x + 1 }\nf().await", "6"},
		{"arguments are bound at the call", "async fn f(a, b) { a - b }\nf(10, 4).await", "6"},
		{"a default argument runs in the caller", "async fn f(a, b = a * 2) { b }\nf(3).await", "6"},
		{"return ends the task", "async fn f() { return 1; 2 }\nf().await", "1"},
		{"a task may start a task", "async fn inner() { 20 }\nasync fn outer() { inner().await + 1 }\nouter().await", "21"},
		{"many tasks fan out", "async fn f(n) { n * n }\n(1..4).map { f(it) }.map { it.await }.json", "[1,4,9,16]"},
		{"str of a task says what it is", "async fn f() { 1 }\nt = f()\nt.await\nstr(t)", "#<task f done>"},
		{"a task is not JSON data", "async fn f() { 1 }\n{a: f()}.json", `{"a":null}`},
		{"identity, not equality of results", "async fn f() { 1 }\nt = f()\n[t == t, t == f()].json", "[true,false]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := asyncMustEval(t, tt.src).Str(); got != tt.want {
				t.Fatalf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestAsyncIsPositional pins that `async` bought no keyword: it means something only
// directly before `fn`, so every program that used it as a name still runs (§3.5).
func TestAsyncIsPositional(t *testing.T) {
	t.Parallel()

	if v := asyncMustEval(t, "async = 41\nasync + 1"); v.Str() != "42" {
		t.Fatalf("async as a variable = %s; want 42", v.Str())
	}
	if v := asyncMustEval(t, "fn async(x) { x }\nasync(7)"); v.Str() != "7" {
		t.Fatalf("async as a function = %s; want 7", v.Str())
	}
	for _, kw := range Keywords() {
		if kw == "async" {
			t.Fatal("'async' reached the keyword table; §3.5 has sixteen entries")
		}
	}
}

// TestAsyncAnonymous pins §8.14 on the anonymous form: `async fn(…) { … }` is a value, so
// the modifier is read in expression position too and the task it starts is an ordinary
// one.
func TestAsyncAnonymous(t *testing.T) {
	t.Parallel()

	if v := asyncMustEval(t, "f = async fn(x) { x * 2 }\nf(21).await"); v.Str() != "42" {
		t.Fatalf("anonymous async fn = %s; want 42", v.Str())
	}
	// The task is started by the call, not by the literal, exactly as the named form is.
	if v := asyncMustEval(t, "fs = [async fn() { 1 }, async fn() { 2 }]\nfs.map { it().await }.sum"); v.Str() != "3" {
		t.Fatalf("array of anonymous async fns = %s; want 3", v.Str())
	}
}

// TestAsyncErrors pins where a failure surfaces: at the await, catchable there like any
// other error, and never at the call that started the task.
func TestAsyncErrors(t *testing.T) {
	t.Parallel()

	v := asyncMustEval(t, `async fn boom() { raise("нет") }
try boom().await else "поймали"`)
	if v.Str() != "поймали" {
		t.Fatalf("try around await = %q; want it to catch", v.Str())
	}

	// Starting the task is not where it fails.
	if _, err := asyncEval(t, `async fn boom() { raise("нет") }
t = boom()
"дошли"`); err != nil {
		t.Fatalf("starting a failing task ended the Run: %v", err)
	}

	_, err := asyncEval(t, "async fn boom() { raise(\"нет\") }\nboom().await")
	if err == nil {
		t.Fatal("await of a failed task returned no error")
	}
	if !strings.Contains(err.Error(), "нет") {
		t.Fatalf("error = %v; want the task's own message", err)
	}
}

// TestAsyncUnawaitedFailureIsReported pins that a task nobody waited for still runs, and
// that its failure is written to Stderr instead of vanishing.
func TestAsyncUnawaitedFailureIsReported(t *testing.T) {
	t.Parallel()

	var out, errOut strings.Builder
	o := asyncOpts()
	o.Stdout, o.Stderr = &out, &errOut
	in := New(o)

	if _, err := in.Eval(context.Background(), `async fn late() { println("побежала"); raise("упала") }
late()
"конец"`, nil); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out.String() != "побежала\n" {
		t.Fatalf("stdout = %q; want the unawaited task to have run", out.String())
	}
	if !strings.Contains(errOut.String(), "never awaited") {
		t.Fatalf("stderr = %q; want the dropped failure reported", errOut.String())
	}
}

// TestAsyncSelfAwaitIsDiagnosed pins the one deadlock the evaluator can see coming.
func TestAsyncSelfAwaitIsDiagnosed(t *testing.T) {
	t.Parallel()

	_, err := asyncEval(t, "async fn selfie() { $t.await }\n$t = selfie()\n$t.await")
	if err == nil || !strings.Contains(err.Error(), "cannot await itself") {
		t.Fatalf("error = %v; want the self-await diagnostic", err)
	}
}

// TestAsyncSharesTheBudget pins §14.1 under tasks: the steps a task spends are the Run's
// steps, and the limit is not catchable wherever it is reached.
func TestAsyncSharesTheBudget(t *testing.T) {
	t.Parallel()

	o := asyncOpts()
	o.StepBudget = 20_000
	in := New(o)
	_, err := in.Eval(context.Background(), `async fn spin() { i = 0; while i < 100000 { i += 1 }; i }
try spin().await else "поймали"`, nil)
	if err == nil {
		t.Fatal("a task outspent the Run's budget without an error")
	}
	if !strings.Contains(err.Error(), "step budget") {
		t.Fatalf("error = %v; want the budget limit", err)
	}
}

// TestAsyncDeadlineEndsAWait pins that waiting is bounded: two tasks waiting for each
// other end on the Run's clock, not never.
func TestAsyncDeadlineEndsAWait(t *testing.T) {
	t.Parallel()

	o := asyncOpts()
	o.Timeout = 300 * time.Millisecond
	in := New(o)

	start := time.Now()
	_, err := in.Eval(context.Background(), `async fn a() { $b.await }
async fn b() { $a.await }
$a = a(); $b = b()
$a.await`, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v; want a timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the Run took %s; the deadline was 300ms", d)
	}
}

// TestAsyncContextCancelEndsTheRun pins that a host that cancels gets its goroutines
// back, whatever the tasks are doing.
func TestAsyncContextCancelEndsTheRun(t *testing.T) {
	t.Parallel()

	o := asyncOpts()
	o.Timeout = -1 // only the context may end this Run
	in := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := in.Eval(ctx, `async fn a() { $b.await }
async fn b() { $a.await }
$a = a(); $b = b()
$a.await`, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled Run returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the Run")
	}
}

// TestAsyncMaxTasks pins the goroutine bound, and that a host may turn tasks off.
func TestAsyncMaxTasks(t *testing.T) {
	t.Parallel()

	o := asyncOpts()
	o.MaxTasks = 4
	_, err := New(o).Eval(context.Background(), "async fn f() { 1 }\n(1..10).map { f() }", nil)
	if err == nil || !strings.Contains(err.Error(), "too many tasks") {
		t.Fatalf("error = %v; want the MaxTasks limit", err)
	}

	o.MaxTasks = -1
	_, err = New(o).Eval(context.Background(), "async fn f() { 1 }\nf()", nil)
	if err == nil || !strings.Contains(err.Error(), "tasks are disabled") {
		t.Fatalf("error = %v; want tasks to be off", err)
	}

	// Finished tasks free their slot: the bound is on what is running, not on what ran.
	o.MaxTasks = 2
	v, err := New(o).Eval(context.Background(), "async fn f(n) { n }\n(1..10).map { f(it).await }.sum", nil)
	if err != nil {
		t.Fatalf("sequential tasks under MaxTasks=2: %v", err)
	}
	if v.Str() != "55" {
		t.Fatalf("sum = %s; want 55", v.Str())
	}
}

// TestAsyncNoGoroutineLeak pins A5: every task the Run started has ended by the time
// RunResult returns, awaited or not.
func TestAsyncNoGoroutineLeak(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	running := 0
	o := asyncOpts()
	in := New(o)
	in.Register("__enter", 0, func(c *Ctx, _ []Value) (Value, error) {
		mu.Lock()
		running++
		mu.Unlock()
		return Nil(), nil
	})
	in.Register("__leave", 0, func(c *Ctx, _ []Value) (Value, error) {
		mu.Lock()
		running--
		mu.Unlock()
		return Nil(), nil
	})

	if _, err := in.Eval(context.Background(), `async fn work(n) { __enter(); i = 0; while i < 50 { i += 1 }; __leave(); n }
xs = (1..8).map { work(it) }
xs[0].await`, nil); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if running != 0 {
		t.Fatalf("%d task(s) still running after the Run returned", running)
	}
}

// TestAsyncOneEvaluatorAtATime is the safety property the whole design rests on: no two
// tasks evaluate at the same moment, so the Values they share need no locks of their own
// (A6). The host function is called from inside every task and would see an overlap if
// there ever were one.
func TestAsyncOneEvaluatorAtATime(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	inside, maxInside := 0, 0
	o := asyncOpts()
	in := New(o)
	in.Register("__tick", 0, func(c *Ctx, _ []Value) (Value, error) {
		mu.Lock()
		inside++
		if inside > maxInside {
			maxInside = inside
		}
		n := inside
		mu.Unlock()
		if n > 1 {
			return Nil(), c.Errorf("two tasks evaluated at once")
		}
		mu.Lock()
		inside--
		mu.Unlock()
		return Nil(), nil
	})

	if _, err := in.Eval(context.Background(), `async fn work() { i = 0; while i < 200 { __tick(); i += 1 }; i }
ts = (1..8).map { work() }
ts.map { it.await }.sum`, nil); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxInside > 1 {
		t.Fatalf("%d tasks evaluated at once; want 1", maxInside)
	}
}

// TestAsyncSharedStateIsNotRaced runs many tasks over one array and one $var. Under
// -race this is the test that would fail if the lock were ever dropped in the wrong
// place; the sum is what says nothing was lost.
func TestAsyncSharedStateIsNotRaced(t *testing.T) {
	t.Parallel()

	v := asyncMustEval(t, `xs = []
async fn add(n) { xs.push(n); $total = ($total ?? 0).int + n; n }
(1..20).map { add(it) }.map { it.await }
[xs.len, xs.sum, $total.int].json`)
	if v.Str() != "[20,210,210]" {
		t.Fatalf("shared state = %s; want [20,210,210]", v.Str())
	}
}

// TestAsyncHTTPRunsInParallel is the reason the feature exists: the requests of two
// tasks are on the wire together, so N slow calls cost about one of them.
func TestAsyncHTTPRunsInParallel(t *testing.T) {
	t.Parallel()

	const delay = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	o := asyncOpts()
	o.Timeout = 10 * time.Second
	in := New(o)

	src := fmt.Sprintf(`include http
async fn get(i) { http.get("%s/${i}")["body"] }
(1..5).map { get(it) }.map { it.await }.join(",")`, srv.URL)

	start := time.Now()
	v, err := in.Eval(context.Background(), src, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "ok,ok,ok,ok,ok" {
		t.Fatalf("bodies = %q; want five ok", v.Str())
	}
	// Sequentially this is 5×200ms. Anything under half of that can only be overlap.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("five %s requests took %s; they did not overlap", delay, elapsed)
	}
}

// TestAsyncBlockingReleasesTheLock pins the other half of the same rule for a host
// function: while it waits inside Ctx.Blocking, the other tasks of the Run keep going.
func TestAsyncBlockingReleasesTheLock(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var once sync.Once
	in := New(asyncOpts())
	in.Register("__wait", 0, func(c *Ctx, _ []Value) (Value, error) {
		c.Blocking(func() { <-release })
		return Nil(), nil
	})
	in.Register("__release", 0, func(c *Ctx, _ []Value) (Value, error) {
		once.Do(func() { close(release) })
		return Nil(), nil
	})

	// The first task parks inside a blocking host call; the second one only finishes
	// because it runs while the first is parked, and then lets it go.
	v, err := in.Eval(context.Background(), `async fn blocked() { __wait(); "разблокировали" }
async fn other() { __release(); "прошла" }
a = blocked()
b = other()
[a.await, b.await].json`, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != `["разблокировали","прошла"]` {
		t.Fatalf("got %s; want both tasks to have finished", v.Str())
	}
}

// TestAsyncWithServe pins that the two blocking things a Run can do compose: a task
// started before `http.serve` runs while the server waits for a connection, and the
// handler reads it. Without the release around that wait this test hangs.
func TestAsyncWithServe(t *testing.T) {
	t.Parallel()

	s := startServer(t, `include http
async fn work() { 21 * 2 }
t = work()
http.serve(":0", {
  "GET /":     { (req) -> "ответ ${t.await}" },
  "GET /stop": { (req) -> http.stop(); "пока" },
}, { (u) -> __ready(u) })`, netOpts())

	if code, body, _ := do(t, "GET", s.url+"/", ""); code != 200 || body != "ответ 42" {
		t.Fatalf("GET / = %d %q; want 200 and the task's value", code, body)
	}
	do(t, "GET", s.url+"/stop", "")
	if err := s.wait(t); err != nil {
		t.Fatalf("the server Run ended with %v", err)
	}
}

// TestAsyncModuleExport pins that a script module may export an async function and that
// the importer gets tasks from it (§12.8).
func TestAsyncModuleExport(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./work.mzs": "export async fn double(n) { n * 2 }",
	})
	v, err := evalMod(t, in, "include w from \"./work.mzs\"\nw.double(21).await")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "42" {
		t.Fatalf("got %s; want 42", v.Str())
	}
}

// TestAsyncDiagnostics covers the shapes that are not it. `await` is a method of one
// kind only, so a receiver that is not a task is the ordinary §5.6 message, produced
// where the receiver exists — at run time, since its kind is not known before that.
func TestAsyncDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     string
		want    string
		compile bool
	}{
		{"await on a value that is not a task", "5.await", "undefined method 'await' for int", false},
		{"done on a value that is not a task", "x = 5\nx.done", "undefined method 'done' for int", false},
		{"an exported async fn needs a name", "export async fn (a) { a }", "'export' needs a name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var err error
			if tt.compile {
				_, err = New(asyncOpts()).Compile("t", tt.src)
			} else {
				_, err = asyncEval(t, tt.src)
			}
			if err == nil {
				t.Fatalf("%s: no error", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want it to mention %q", err, tt.want)
			}
		})
	}
}
