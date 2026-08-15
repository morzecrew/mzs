package mzs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mzs/internal/token"
)

// Async functions and tasks (§8.14).
//
//	async fn fetch(u) { http.get(u)["body"] }
//	a = fetch("https://…"); b = fetch("https://…")   # both requests are on their way
//	[a.await, b.await]                                # …and this is where they are read
//
// Calling an `async fn` starts its body on a goroutine and returns a **task** instead of
// a value. `t.await` waits for that task and yields what the body produced — or raises,
// at the await site, whatever the body raised. That is the whole surface: `async fn`,
// `await`, and `done` for asking without waiting.
//
// The concurrency is real (one goroutine per task) and the safety of §14 is unchanged,
// because **one goroutine of a Run evaluates mzs code at a time**. Every task holds the
// Run's lock while it runs and releases it exactly where it could make no progress
// anyway:
//
//   - at `await`, waiting for another task;
//   - inside a blocking host call — the http client is the one in the box (§12.11);
//   - when it finishes.
//
// So two tasks never touch one Array, one Dict or one $var at the same time: a script
// cannot write a data race even deliberately (A6), and the interpreter needs no locks
// anywhere else. What overlaps is the *waiting*, which is the point — two `http.get`
// calls in two tasks are on the wire together.
//
// Nothing here is preemptive: a task that only computes runs to its end once it holds
// the lock, and a switch happens at the three points above and nowhere else. Which
// *runnable* task takes the lock at one of those points is not specified, though — two
// tasks that both `say` may print in either order. Concurrency is what was asked for by
// writing `async`; everything outside a task is as ordered as it ever was.
//
// Everything a Run started, the Run ends: RunResult cancels and joins every task before
// it returns, so no goroutine outlives the call that made it (A5) and the host reads
// Result.Globals with nothing still writing to it.

// runShared is the half of a Run that all of its tasks see. Every field is read and
// written under gil, which is the whole synchronisation story of this package.
type runShared struct {
	gil sync.Mutex

	// steps is the Run's budget counter. Tasks spend from it too: what the Run may do is
	// bounded by §14.1 no matter how many goroutines it does it on.
	steps int64

	wg   sync.WaitGroup
	live int // tasks started and not yet finished, against Options.MaxTasks
	// all is every task this Run started, so the failures nobody awaited can be reported
	// once at the end instead of vanishing.
	all []*task

	// cancel ends the context every task derives from. RunResult calls it before joining,
	// so a task blocked on the network or on another task stops instead of holding the
	// Run open.
	cancel context.CancelFunc

	// stdin is Options.Stdin drained once and kept for the rest of the Run (§12.13). It
	// lives here rather than on runState because a reader can only be drained once and a
	// task runs on a copy: `io.stdin` in the main program and in a task must be the same
	// string, not one string and one empty.
	stdin     string
	stdinRead bool
}

// task is what an async call returns: a goroutine, the value it will produce, and the
// channel that says it has. val, err and fin are written once by the task's goroutine
// under the lock and read by awaiters under the same lock; done is the ordinary
// happens-before edge for anything that waits without holding it.
type task struct {
	name string
	done chan struct{}
	val  Value
	err  error
	fin  bool
	read bool // true once something awaited it, which is what makes a failure someone's
}

// render is `str` of a task. It says what the reader wants to know — has it finished —
// and never blocks to find out.
func (t *task) render() string {
	if t == nil {
		return "#<task>"
	}
	state := "running"
	if t.fin {
		state = "done"
	}
	if t.name == "" {
		return "#<task " + state + ">"
	}
	return "#<task " + t.name + " " + state + ">"
}

// ---------------------------------------------------------------------------
// The lock
// ---------------------------------------------------------------------------

func (rs *runState) lock()   { rs.sh.gil.Lock() }
func (rs *runState) unlock() { rs.sh.gil.Unlock() }

// blocking runs f with the Run's lock released. A caller that is about to wait on
// something outside the interpreter — a socket, a timer — can make no progress until it
// returns, so another task may as well use the interpreter meanwhile. It is the only way
// a host function may block without stalling every task of the Run.
//
// f must not touch Values, globals or anything else the interpreter owns: outside the
// lock, that is exactly the data race this design exists to prevent.
func (c *Ctx) blocking(f func()) {
	c.rs.unlock()
	defer c.rs.lock()
	f()
}

// Blocking is blocking, for host functions installed with Register. Wrap the wait — the
// request, the read — and nothing else.
func (c *Ctx) Blocking(f func()) { c.blocking(f) }

// waitFor blocks on ch with the lock released, honouring the host's context and the
// Run's deadline. Waiting is why the deadline of §14.1 still means something under
// tasks: a script that awaits something that can never arrive fails on time, the way a
// `while true` does, rather than never.
func (rs *runState) waitFor(ch <-chan struct{}) error {
	rs.unlock()
	defer rs.lock()

	var deadline <-chan time.Time
	if rs.hasDL {
		timer := time.NewTimer(time.Until(rs.deadline))
		defer timer.Stop()
		deadline = timer.C
	}
	var canceled <-chan struct{}
	if rs.ctx != nil {
		canceled = rs.ctx.Done()
	}

	select {
	case <-ch:
		return nil
	case <-canceled:
		return limitErrorf(ErrCanceled, "canceled")
	case <-deadline:
		return limitErrorf(ErrTimeout, "execution timed out after %s", rs.opts.Timeout)
	}
}

// fork is the per-task copy of the Run's state. What it shares is deliberate: the
// globals table, the module cache, the budget and the lock behind sh. What it does not
// share is just as deliberate: a task gets its own call stack and depth, so an error
// raised in it carries the frames of the task rather than of whatever the main program
// happened to be doing at the time.
func (rs *runState) fork(t *task) *runState {
	trs := *rs
	trs.task = t
	trs.stack = nil
	trs.depth = 0
	trs.tick = 0
	trs.exports = nil
	trs.loading = append([]string(nil), rs.loading...)
	return &trs
}

// joinTasks ends the Run's concurrency, and it is called on every exit from a Run — the
// panic path included — which is what makes "no goroutine outlives its Run" (A5) a
// property of the implementation rather than of the script.
//
// A Run is over when the program *and* its tasks are over: a task started and never
// awaited still runs, because `notify(user)` on the last line of a script is a use, not
// a mistake. That waiting is bounded by the same deadline and the same context as
// everything else (§14.1); what is still going when they run out is cancelled and joined.
func (rs *runState) joinTasks() {
	rs.drainTasks()
	if rs.sh.cancel != nil {
		rs.sh.cancel()
	}
	rs.unlock()
	rs.sh.wg.Wait()
	rs.lock()
	rs.reportUnawaited()
}

// drainTasks gives the tasks still running the rest of the Run's time to finish in. The
// lock is released while it waits, which is what lets them use it.
func (rs *runState) drainTasks() {
	if rs.sh.live == 0 {
		return
	}
	waited := make(chan struct{})
	go func() {
		rs.sh.wg.Wait()
		close(waited)
	}()
	// A limit here is not the Run's error: the program has already produced its value,
	// and the cancel below is what the timeout means.
	_ = rs.waitFor(waited)
}

// reportUnawaited writes the errors nobody asked for to Stderr. A task whose value was
// never awaited swallows its failure otherwise, and a script that fires one off for its
// effect deserves to learn that the effect did not happen. Limit errors are the one thing
// skipped: a cancelled task says the Run ended, not that the task was wrong.
func (rs *runState) reportUnawaited() {
	for _, t := range rs.sh.all {
		if t.err == nil || t.read {
			continue
		}
		if x := asError(t.err); x != nil && x.Kind == ErrKindLimit {
			continue
		}
		fmt.Fprintf(rs.opts.errOut(), "mzs: task '%s' failed and was never awaited: %v\n",
			t.name, t.err)
	}
}

// ---------------------------------------------------------------------------
// Starting and awaiting
// ---------------------------------------------------------------------------

// spawnTask starts f on its own goroutine and returns the task the caller holds. The
// goroutine cannot run before the caller releases the lock — at an `await`, at a
// blocking call, or by finishing — so starting a task interleaves nothing: the call
// returns a handle and the caller carries straight on.
func (e ev) spawnTask(f *Func, args []Value, named namedArgs, pos token.Pos) (Value, error) {
	rs := e.rs
	switch {
	case rs.opts.MaxTasks <= 0:
		return Nil(), e.at(limitErrorf(ErrBudget,
			"tasks are disabled: the host set MaxTasks to none, so '%s' cannot be started", f.Name), pos)
	case rs.sh.live >= rs.opts.MaxTasks:
		return Nil(), e.at(limitErrorf(ErrBudget,
			"too many tasks: %d already running (MaxTasks)", rs.sh.live), pos)
	}
	// The frame is built here, under the caller's lock, because a default argument
	// (`async fn f(x = g())`) is the *caller's* code and must not run beside it.
	env, err := e.bindParams(f, args, named)
	if err != nil {
		return Nil(), e.at(err, pos)
	}

	t := &task{name: f.Name, done: make(chan struct{})}
	trs := rs.fork(t)
	rs.sh.live++
	rs.sh.all = append(rs.sh.all, t)
	rs.sh.wg.Add(1)

	go func() {
		defer rs.sh.wg.Done()
		trs.lock()
		defer func() {
			// A7 reaches in here too: a panic in a task is the Run's error, not the
			// host's crash. It is stored like any other failure and raised at the await.
			if r := recover(); r != nil {
				t.err = internalError(trs.file, r, false)
			}
			t.fin = true
			trs.sh.live--
			trs.unlock()
			close(t.done)
		}()
		te := ev{rs: trs, cx: newCtx(trs), env: env}
		t.val, t.err = te.runTaskBody(f, pos)
	}()

	return taskOf(t), nil
}

// runTaskBody runs the body of an async function as the whole of its goroutine. It is
// invoke's second half with one difference: there is no caller left to unwind to, so
// `return`, `next` and a `break` that escaped all end the task with their value rather
// than travelling further (§8.10).
func (e ev) runTaskBody(f *Func, pos token.Pos) (Value, error) {
	if err := e.rs.enter(f.Name, pos.Line, pos.Col); err != nil {
		return Nil(), e.at(err, pos)
	}
	defer e.rs.leave()
	if f.Body == nil {
		return Nil(), nil
	}
	v, err := e.execStmts(f.Body.Stmts)
	if err == nil {
		return v, nil
	}
	if c, ok := ctrlOf(err); ok {
		return c.val, nil
	}
	return Nil(), err
}

// awaitTask waits for one task and yields its value here, at the await. The lock is
// released while waiting — that is what lets the awaited task run at all — and an error
// the body raised is raised again at this site, with the position and the frames of
// where it actually happened.
func (e ev) awaitTask(t *task, pos token.Pos) (Value, error) {
	if t == nil {
		return Nil(), e.at(typeErrorf("not a task"), pos)
	}
	if e.rs.task == t {
		return Nil(), e.at(newError(ErrKindRaise,
			"a task cannot await itself: '%s' would wait for its own result", t.name), pos)
	}
	t.read = true
	if !t.fin {
		if err := e.rs.waitFor(t.done); err != nil {
			return Nil(), e.at(err, pos)
		}
	}
	if t.err != nil {
		return Nil(), t.err
	}
	return t.val, nil
}

// ---------------------------------------------------------------------------
// The task table (§12.12)
// ---------------------------------------------------------------------------

func init() {
	RegisterMethods(KTask,
		Method{Name: "await", Min: 0, Max: 0, Fn: taskvAwait},
		Method{Name: "done", Min: 0, Max: 0, Fn: taskvDone},
	)
}

// taskvAwait is `t.await` — and, by UFCS, `await(t)`. Awaiting a finished task again is
// the same value again: a task is waited for once and holds its result afterwards.
func taskvAwait(c *Ctx, recv Value, _ []Value) (Value, error) {
	return ev{rs: c.rs, cx: c}.awaitTask(recv.task(), c.pos)
}

// taskvDone answers whether the task has finished, without waiting for it. It is the one
// question about a task that never blocks.
func taskvDone(_ *Ctx, recv Value, _ []Value) (Value, error) {
	t := recv.task()
	return Bool(t != nil && t.fin), nil
}
