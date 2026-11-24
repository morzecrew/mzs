package mzs

import (
	"context"
	"errors"
	"time"

	"mzs/internal/rx"
)

// stepCheckInterval is how often the step counter also checks the clock and the
// context (§14.1). The check lives inside the node loop, so `while true { }` and a
// pathological regex are both interrupted mid-loop, not merely between statements.
// There is no goroutine and no timer per Run — just one deadline comparison.
const stepCheckInterval = 1024

// step charges n interpreter steps and, every stepCheckInterval, verifies the context,
// the wall clock and the budget in that order. It is the only place a Run can be
// interrupted, and the errors it returns are never catchable by `try` (§8.11).
func (rs *runState) step(n int64) error {
	// The counter is shared by every task of the Run (§8.14) and, like everything else
	// in runShared, is only ever touched by the goroutine holding the lock.
	rs.sh.steps += n
	rs.tick += int(n)
	if rs.tick < stepCheckInterval {
		return nil
	}
	rs.tick = 0
	return rs.checkLimits()
}

// checkLimits runs the three checks unconditionally. The evaluator calls it directly
// at loop back-edges so a tight loop cannot outrun the sampling interval.
func (rs *runState) checkLimits() error {
	// A task has a context of its own (§8.14), so this is also where `t.cancel` lands:
	// the task stops at its next step, and the message says the script ended it rather
	// than blaming the host's context. Either way it is a limit error and uncatchable —
	// a cancelled task that could `try` its way out of the cancel would not be one.
	if rs.ctx != nil {
		if err := rs.ctx.Err(); err != nil {
			return rs.canceledError()
		}
	}
	if rs.hasDL && time.Now().After(rs.deadline) {
		return rs.limitErr(ErrTimeout, "execution timed out after %s", rs.opts.Timeout)
	}
	if rs.limit >= 0 && rs.sh.steps > rs.limit {
		return rs.limitErr(ErrBudget, "step budget exceeded (%d steps)", rs.limit)
	}
	return rs.stepCharge()
}

// hostContext is Ctx.Context: the Run's context with the Run's deadline on it. The
// derived context is kept while neither moves, so a host function asking for it in a
// loop makes one timer, not one per call. A task's state starts as a copy of its
// parent's, with a context of its own, so it makes its own and leaves the parent's be;
// the Run's end ends them all.
func (rs *runState) hostContext() context.Context {
	if rs.ctx == nil {
		return context.Background()
	}
	if !rs.hasDL {
		return rs.ctx
	}
	if rs.hostCtx != nil && rs.hostParent == rs.ctx && rs.hostDL.Equal(rs.deadline) {
		return rs.hostCtx
	}
	if rs.hostCtx != nil && rs.hostParent == rs.ctx {
		// The same context with a later deadline: http.serve began another request,
		// and what the last one handed its host calls is over with it.
		rs.hostCancel()
	}
	rs.hostCtx, rs.hostCancel = context.WithDeadlineCause(rs.ctx, rs.deadline, ErrTimeout)
	rs.hostParent, rs.hostDL = rs.ctx, rs.deadline
	return rs.hostCtx
}

// afterHost is what a call site does when a host function returns. A limit Blocking
// found broken wins over whatever the function answered; so does one the function ran
// into itself, when it returns the context's error — a script must not be able to `try`
// its way past the deadline because the host said "context deadline exceeded" in its
// own words.
func (rs *runState) afterHost(v Value, err error) (Value, error) {
	if p := rs.pending; p != nil {
		rs.pending = nil
		return Nil(), p
	}
	// A function that waited on the context without Blocking is caught here too, as
	// soon as it returns rather than some steps later.
	if rs.hostCtx != nil && rs.hostCtx.Err() != nil {
		if lerr := rs.checkLimits(); lerr != nil {
			return Nil(), lerr
		}
	}
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		if e := asError(err); e.Kind != ErrKindLimit {
			if lerr := rs.checkLimits(); lerr != nil {
				return Nil(), lerr
			}
		}
	}
	return v, err
}

// enter charges one level of call depth. Recursion is bounded so the Go stack can
// never overflow (§14.2).
func (rs *runState) enter(fn string, line, col int) error {
	rs.depth++
	if rs.depth > rs.maxDepth {
		rs.depth--
		return limitErrorf(ErrDepth, "max call depth exceeded (%d)", rs.maxDepth)
	}
	if fn == "" {
		fn = "{}"
	}
	rs.stack = append(rs.stack, Frame{Fn: fn, File: rs.file, Line: line, Col: col})
	return nil
}

// leave undoes enter.
func (rs *runState) leave() {
	if rs.depth > 0 {
		rs.depth--
	}
	if n := len(rs.stack); n > 0 {
		rs.stack = rs.stack[:n-1]
	}
}

// limitErr builds a limit error with the call stack it was raised under. The helpers that
// raise these are called from deep inside a statement and their errors travel up
// through frames that are already unwinding, so the stack has to be taken here, at the
// point of failure, or it is empty by the time anything positions the error (§8.11).
func (rs *runState) limitErr(sentinel error, format string, a ...any) *Error {
	e := limitErrorf(sentinel, format, a...)
	e.Stack = rs.frames()
	return e
}

// frames returns the current call stack innermost first, for *Error.Stack.
func (rs *runState) frames() []Frame {
	out := make([]Frame, 0, len(rs.stack))
	for i := len(rs.stack) - 1; i >= 0; i-- {
		out = append(out, rs.stack[i])
	}
	return out
}

// checkCollection bounds any operation that materialises elements — array, map, `*`,
// flatten, range, split, matches, each_slice (§14.2). Exceeding it is a limit error and
// is not catchable.
func (rs *runState) checkCollection(n int) error {
	if n > rs.opts.MaxCollection {
		return rs.limitErr(ErrBudget, "collection too large: %d elements exceeds the limit of %d",
			n, rs.opts.MaxCollection)
	}
	return nil
}

// checkString bounds string construction — `*`, `+`, join, replace, interpolation.
func (rs *runState) checkString(n int) error {
	if n > rs.opts.MaxStringBytes {
		return rs.limitErr(ErrBudget, "string too large: %d bytes exceeds the limit of %d",
			n, rs.opts.MaxStringBytes)
	}
	return nil
}

// regexStepsPerStep is how many backtracking steps of a regex call cost one interpreter
// step (§11.2, §14.1). A backtracking step is a few nanoseconds and an interpreter step a
// few tens, so a pathological pattern spends the budget at about the rate a loop doing the
// same work would.
const regexStepsPerStep = 16

// regexMeter is the meter every regex call of this state is charged to: its work goes to
// the step budget, which checks the deadline and the context as it does for any other
// step, and what it builds is bounded by MaxStringBytes. It lives on the state so a match
// in a hot condition allocates nothing for it; a task runs on a copy of the state, which
// is why the owner is checked rather than assumed.
func (rs *runState) regexMeter() *rx.Meter {
	if rs.rxOwner != rs {
		rs.rxOwner = rs
		rs.rxMeter = rx.Meter{Charge: rs.regexCharge}
	}
	rs.rxMeter.MaxBytes = rs.opts.MaxStringBytes
	rs.rxAcc = 0
	return &rs.rxMeter
}

// regexCharge turns the backtracking steps a call reports into interpreter steps. The
// remainder is carried to the next report of the same call, so a call is charged what it
// did however the reports fall.
func (rs *runState) regexCharge(n int) error {
	rs.rxAcc += n
	steps := rs.rxAcc / regexStepsPerStep
	if steps == 0 {
		return nil
	}
	rs.rxAcc -= steps * regexStepsPerStep
	return rs.step(int64(steps))
}

// regexError names what a metered regex call failed with. A limit of the Run — the
// budget, the deadline, the context, the memory budget, all raised through regexCharge —
// passes through untouched. The call's own budget is a catchable `regex` error that
// still says ErrBudget to a host, and a replacement past MaxStringBytes is the same
// limit as any other string that would be too large.
func (rs *runState) regexError(err error) error {
	var e *Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e):
		return err
	case errors.Is(err, rx.ErrBudget):
		return wrapError(ErrKindRegex, sentinels{ErrRegexLimit, ErrBudget},
			"regex step budget exceeded (%d steps)", rs.opts.RegexSteps)
	case errors.Is(err, rx.ErrTooLarge):
		return rs.limitErr(ErrBudget, "string too large: the result exceeds the limit of %d bytes",
			rs.opts.MaxStringBytes)
	}
	return regexErrorf("%s", err.Error())
}
