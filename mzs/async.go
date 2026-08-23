package mzs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"mzs/internal/ast"
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
// tasks that both `println` may print in either order. Concurrency is what was asked for by
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
	// stdinRd is that same reader buffered, which is what `io.lines` pulls a line at a
	// time (§12.14) and what `input` pulls one of (§12.1); stdinTakenBy records that
	// something has. The whole text and a line at a time are two ways of asking one
	// reader for its bytes, so the Run has to remember which way was used: after the
	// lines have been streamed there is no whole text left to hand back, and `io.stdin`
	// says so rather than answering "".
	stdinRd *bufio.Reader
	// stdinTakenBy is the member that took the reader — "io.lines" or "input" — and is
	// empty until one of them has. It is a name rather than a flag because it is what the
	// later `io.stdin` diagnostic says: which member has the bytes is the one thing that
	// answer needs, and a script that mixes the two spellings deserves to be told which
	// of them it was.
	stdinTakenBy string
	// stdinAt is how far `input` has read into the cached text (§12.1). It only ever
	// moves when `io.stdin` was drained first: consecutive `input()` calls must answer
	// consecutive lines, and a cursor on the shared half of the Run is what a seq of its
	// own cannot be.
	stdinAt int
	// stdinLineBad records that a line overran MaxStringBytes. The reader is then no
	// longer positioned at the start of a line, and the rest of the one that overran would
	// come back as a line of its own — a fragment presented as data — so every later pull
	// reports the same failure instead. bufio.Scanner ends a scan the same way.
	stdinLineBad bool

	// records is every shape *name* a `record` declaration has used in this Run (§7.8).
	// It answers one question — `x.is("Money")`, which has to tell a name nobody
	// declared from one this value simply is not — and a name is all that takes, because
	// `is` answers exactly what `type` answers. It is here for the same reason stdin is:
	// a task runs on a *copy* of runState, so a declaration inside one must land in the
	// half the whole Run shares. It is created on the first declaration and stays nil in
	// the Runs that declare none, which is the common case and pays nothing.
	records map[string]bool

	// fallbackRecords gives one identity to a declaration the compile pass never saw, so
	// that the hoist and the declaration itself agree on it. It stays nil for every
	// program Compile produced, which fills ast.RecordDecl.Type for all of them.
	fallbackRecords map[*ast.RecordDecl]*RecordType
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

	// stop ends the task's own context, and canceled records that the *script* asked for
	// it — `t.cancel`, or the losers of `tasks.race` (§12.12). The two are separate
	// because the context is also cancelled when the Run ends, and those two endings say
	// different things at an await: one is the script's decision, the other is the Run's
	// limit. Both fields are written under the Run's lock, like everything else here.
	stop     context.CancelFunc
	canceled bool
}

// label names a task in a diagnostic. An `async fn` value has no name to give (§4.1), so
// the anonymous case says what it can rather than quoting an empty string.
func (t *task) label() string {
	if t == nil || t.name == "" {
		return "the task"
	}
	return "task '" + t.name + "'"
}

// cancelTask is `t.cancel` and the losing half of `tasks.race`: the task is told to stop
// and its context ends, which aborts the request it may be waiting on. It stops at its
// next step or its next wait — nothing here is preemptive (§8.14) — and a task that has
// already finished keeps the value it produced.
func (t *task) cancelTask() {
	if t == nil || t.fin || t.canceled {
		return
	}
	t.canceled = true
	if t.stop != nil {
		t.stop()
	}
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
	_, err := rs.waitLimited(ch, 0, false)
	return err
}

// waitLimited is waitFor with a limit of the caller's own on top of the Run's: `sleep`
// waits for nothing with one, `t.timeout(d)` waits for a task with one. It answers
// whether ch fired — false with a nil error is the caller's limit running out, which is
// not a failure of the Run and is the caller's to name.
//
// The lock is released for the wait and taken back before anything is decided, because
// what the outcome *means* is read off interpreter state — whether this task was
// cancelled — and that may not be touched while another task owns the interpreter.
func (rs *runState) waitLimited(ch <-chan struct{}, limit time.Duration, hasLimit bool) (bool, error) {
	const (
		fired = iota
		expired
		stopped
		late
	)
	outcome := fired
	func() {
		rs.unlock()
		defer rs.lock()

		var deadline <-chan time.Time
		if rs.hasDL {
			timer := time.NewTimer(time.Until(rs.deadline))
			defer timer.Stop()
			deadline = timer.C
		}
		var expiry <-chan time.Time
		if hasLimit {
			timer := time.NewTimer(limit)
			defer timer.Stop()
			expiry = timer.C
		}
		var canceled <-chan struct{}
		if rs.ctx != nil {
			canceled = rs.ctx.Done()
		}

		select {
		case <-ch:
			outcome = fired
		case <-expiry:
			outcome = expired
		case <-canceled:
			outcome = stopped
		case <-deadline:
			outcome = late
		}
	}()

	switch outcome {
	case expired:
		return false, nil
	case stopped:
		return false, rs.canceledError()
	case late:
		return false, limitErrorf(ErrTimeout, "execution timed out after %s", rs.opts.Timeout)
	}
	return true, nil
}

// waitAny blocks until one of chs is closed and answers which. It is what `tasks.race`
// needs and what a written select cannot spell: how many tasks are being waited for is
// the script's decision, not the compiler's.
func (rs *runState) waitAny(chs []<-chan struct{}) (int, error) {
	// reflect.Select refuses more cases than this, and a panic where a limit belongs is
	// not an answer. It is unreachable with any sane Options.MaxTasks — every task here
	// is a running one — and says what it counted rather than truncating the array.
	const maxCases = 65534
	if len(chs) > maxCases {
		return -1, limitErrorf(ErrBudget, "too many tasks to wait for at once: %d", len(chs))
	}

	// The outcome is decided outside the lock and named inside it, exactly as in
	// waitLimited and for the same reason.
	const (
		stopped = -1
		late    = -2
	)
	chosen := late
	func() {
		rs.unlock()
		defer rs.lock()

		cases := make([]reflect.SelectCase, 0, len(chs)+2)
		for _, ch := range chs {
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)})
		}
		ctxCase := -1
		if rs.ctx != nil {
			ctxCase = len(cases)
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(rs.ctx.Done())})
		}
		if rs.hasDL {
			timer := time.NewTimer(time.Until(rs.deadline))
			defer timer.Stop()
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer.C)})
		}

		switch i, _, _ := reflect.Select(cases); {
		case i < len(chs):
			chosen = i
		case i == ctxCase:
			chosen = stopped
		default:
			chosen = late
		}
	}()

	switch chosen {
	case stopped:
		return -1, rs.canceledError()
	case late:
		return -1, limitErrorf(ErrTimeout, "execution timed out after %s", rs.opts.Timeout)
	}
	return chosen, nil
}

// canceledError says which cancellation ended a wait, because the two mean opposite
// things: a task the script cancelled is the script's own decision, and anything else is
// the Run's context ending under it (§14.1).
func (rs *runState) canceledError() error {
	if rs.task != nil && rs.task.canceled {
		return limitErrorf(ErrCanceled, "%s was canceled", rs.task.label())
	}
	return limitErrorf(ErrCanceled, "canceled")
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

	// The task is given a context of its own, derived from the Run's. Deriving it is what
	// keeps A5 true — the Run's end reaches every task — and having one of its own is what
	// `t.cancel` ends: the context travels into the blocking host calls the task makes
	// (§12.11), so cancelling a task aborts the request it is waiting on instead of only
	// the step after it.
	parent := rs.ctx
	if parent == nil {
		parent = context.Background()
	}
	tctx, stop := context.WithCancel(parent)

	t := &task{name: f.Name, done: make(chan struct{}), stop: stop}
	trs := rs.fork(t)
	trs.ctx = tctx
	rs.sh.live++
	rs.sh.all = append(rs.sh.all, t)
	rs.sh.wg.Add(1)

	go func() {
		defer rs.sh.wg.Done()
		defer stop()
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
		// A task cancelled before it ever held the interpreter has run no steps, so there
		// is no "next step" for the cancel to land at — it lands here instead. Without
		// this, a body short enough to finish inside one sampling interval (§14.1) would
		// hand back a value for a task the script had already stopped, and which of the
		// two happened would depend on how long the body was.
		if t.canceled {
			t.err = trs.canceledError()
			return
		}
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
		// A task the *script* cancelled is not the Run running out of anything: the
		// program made that decision and may want to handle it, so what the await raises
		// is an ordinary catchable error rather than the uncatchable limit the task
		// itself ended on (§8.11).
		if t.canceled && errors.Is(t.err, ErrCanceled) {
			return Nil(), e.at(newError(ErrKindCanceled, "%s was canceled", t.label()), pos)
		}
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
		Method{Name: "timeout", Min: 1, Max: 1, Fn: taskvTimeout},
		Method{Name: "cancel", Min: 0, Max: 0, Fn: taskvCancel},
	)
	// `sleep` is a §12.1 builtin and lives here rather than in builtins.go, because what
	// makes it correct is this file: it hands the interpreter back while it waits.
	RegisterBuiltin(Builtin{Name: "sleep", Min: 1, Max: 1, Fn: bSleep})
	// Registration order is `tasks.keys` order (§8.13).
	RegisterModuleFunc("tasks", "all", 1, 1, tasksAll)
	RegisterModuleFunc("tasks", "race", 1, 1, tasksRace)
}

// argDuration reads the duration every waiting row takes. A duration in this language is
// a number of **seconds** and nothing else — that is what `5.minutes` already is under
// `include time` (§12.15) — so `sleep(0.25)`, `sleep(2)` and `sleep(5.minutes)` are one
// argument with one meaning, and there is no second spelling to learn.
func argDuration(c *Ctx, v Value) (time.Duration, error) {
	n, err := argNum(c, v)
	if err != nil {
		return 0, err
	}
	secs := n.Float()
	switch {
	case math.IsNaN(secs):
		return 0, c.ArgErrorf("%s: nan is not a duration", c.Name())
	case secs < 0:
		return 0, c.ArgErrorf("%s: a duration cannot be negative, got %s", c.Name(), n.Str())
	case secs >= float64(math.MaxInt64)/float64(time.Second):
		// Longer than a time.Duration can hold — three centuries — is not a number of
		// seconds anyone meant. It waits as long as it can, which the Run's deadline
		// will cut short long before it matters.
		return time.Duration(math.MaxInt64), nil
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// bSleep is `sleep(d)` (§12.1). The whole of its design is that it gives the interpreter
// *back* while it waits (D19): the tasks of §8.14 run during the pause, so a poll loop
// that sleeps between attempts does not stop the three requests that are on the wire.
//
// It is charged against the Run's clock like everything else and never extends it: a
// `sleep(5)` in a Run with a one second timeout ends that Run on time (§14.1).
func bSleep(c *Ctx, args []Value) (Value, error) {
	d, err := argDuration(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if _, err := c.rs.waitLimited(nil, d, true); err != nil {
		return Nil(), c.position(asError(err))
	}
	return Nil(), nil
}

// taskList reads the argument `tasks.all` and `tasks.race` take: an array of tasks and
// nothing else. The offending position is named, because such an array is usually the
// result of a `map` and which element is not a task is the whole of the answer.
func taskList(c *Ctx, v Value) ([]*task, error) {
	if v.Kind() != KArray {
		return nil, c.TypeErrorf("%s expects an array of tasks, got %s", c.Name(), v.Kind())
	}
	elems := v.Elems()
	out := make([]*task, 0, len(elems))
	for i, el := range elems {
		if el.Kind() != KTask {
			return nil, c.TypeErrorf("%s: element %d is %s, not a task", c.Name(), i, el.Kind())
		}
		out = append(out, el.task())
	}
	return out, nil
}

// tasksAll is `tasks.all(ts)`: every task waited for, and the results in the order the
// tasks were given — never in the order they happened to finish, which is not a fact
// about the program (§8.14). One task failing raises here, at the first failure in that
// same order.
func tasksAll(c *Ctx, args []Value) (Value, error) {
	ts, err := taskList(c, args[0])
	if err != nil {
		return Nil(), err
	}
	// The whole array counts as read, not only the prefix awaited before a failure:
	// `tasks.all` is a script taking responsibility for all of them, and a sibling's
	// failure printed to stderr afterwards would be a report about a task the program
	// did ask about (§8.14).
	for _, t := range ts {
		t.read = true
	}
	e := ev{rs: c.rs, cx: c}
	out := make([]Value, 0, len(ts))
	for _, t := range ts {
		v, err := e.awaitTask(t, c.pos)
		if err != nil {
			return Nil(), err
		}
		out = append(out, v)
	}
	return Array(out...), nil
}

// tasksRace is `tasks.race(ts)`: the value of the first task to finish, and a cancel for
// every other one. It is the reason the concurrency was added — ask three mirrors, keep
// the answer that arrives — and the cancel is what makes it honest: the losers stop
// instead of running on with nobody left to read them.
//
// A loser that had already failed is not reported to stderr either. Racing is a script
// saying it wants one answer out of many, and the ones it did not want are not mistakes.
func tasksRace(c *Ctx, args []Value) (Value, error) {
	ts, err := taskList(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if len(ts) == 0 {
		return Nil(), c.ArgErrorf("%s: an empty array has nothing to wait for", c.Name())
	}
	for _, t := range ts {
		if c.rs.task == t {
			return Nil(), c.position(newError(ErrKindRaise,
				"%s: %s cannot race itself: it would wait for its own result", c.Name(), t.label()))
		}
	}
	for _, t := range ts {
		t.read = true
	}

	// A task that has already finished wins without any waiting, and the earliest such
	// task in the array wins: the answer to a race whose result is already in must not
	// depend on which of the ready channels a select happened to pick.
	winner := -1
	for i, t := range ts {
		if t.fin {
			winner = i
			break
		}
	}
	if winner < 0 {
		chs := make([]<-chan struct{}, len(ts))
		for i, t := range ts {
			chs[i] = t.done
		}
		i, werr := c.rs.waitAny(chs)
		if werr != nil {
			return Nil(), c.position(asError(werr))
		}
		winner = i
	}
	for _, t := range ts {
		if t != ts[winner] {
			t.cancelTask()
		}
	}
	return ev{rs: c.rs, cx: c}.awaitTask(ts[winner], c.pos)
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

// taskvTimeout is `t.timeout(d)`: an await with a limit of the caller's own. What runs out
// is the *waiting* and not the work — the task carries on, because the script may still
// want it and `t.cancel` is where it says it does not — and the error is an ordinary
// catchable one of kind "timeout", since a limit the script chose is not the Run's (§8.11).
func taskvTimeout(c *Ctx, recv Value, args []Value) (Value, error) {
	t := recv.task()
	e := ev{rs: c.rs, cx: c}
	if t == nil || c.rs.task == t {
		// Not a task, or this very task: awaitTask already has the words for both.
		return e.awaitTask(t, c.pos)
	}
	d, err := argDuration(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if !t.fin {
		t.read = true
		ok, werr := c.rs.waitLimited(t.done, d, true)
		if werr != nil {
			return Nil(), c.position(asError(werr))
		}
		if !ok {
			// No `timeout:` prefix in front of a kind that is already the word: the kind
			// says which wait ran out, and the message says whose and how long it was.
			return Nil(), c.ErrorfKind(ErrKindTimeout, "%s did not finish within %s", t.label(), d)
		}
	}
	return e.awaitTask(t, c.pos)
}

// taskvCancel is `t.cancel`: stop this task. It never waits and never fails — cancelling a
// task that has already finished changes nothing, and cancelling one twice is the first
// cancel — so the answer is always nil and the effect lands at the task's next step or
// wait (§8.14).
func taskvCancel(_ *Ctx, recv Value, _ []Value) (Value, error) {
	recv.task().cancelTask()
	return Nil(), nil
}
