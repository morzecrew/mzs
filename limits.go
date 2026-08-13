package mzs

import "time"

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
	if rs.ctx != nil {
		if err := rs.ctx.Err(); err != nil {
			return limitErrorf(ErrCanceled, "canceled")
		}
	}
	if rs.hasDL && time.Now().After(rs.deadline) {
		return limitErrorf(ErrTimeout, "execution timed out after %s", rs.opts.Timeout)
	}
	if rs.limit >= 0 && rs.sh.steps > rs.limit {
		return limitErrorf(ErrBudget, "step budget exceeded (%d steps)", rs.limit)
	}
	return nil
}

// enter charges one level of call depth. Recursion is bounded so the Go stack can
// never overflow (§14.2).
func (rs *runState) enter(fn string, line, col int) error {
	rs.depth++
	if rs.depth > rs.maxDepth {
		rs.depth--
		return limitErrorf(ErrDepth, "max call depth exceeded (%d)", rs.maxDepth)
	}
	rs.stack = append(rs.stack, Frame{Fn: fn, Line: line, Col: col})
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
		return limitErrorf(ErrBudget, "collection too large: %d elements exceeds the limit of %d",
			n, rs.opts.MaxCollection)
	}
	return nil
}

// checkString bounds string construction — `*`, `+`, join, replace, interpolation.
func (rs *runState) checkString(n int) error {
	if n > rs.opts.MaxStringBytes {
		return limitErrorf(ErrBudget, "string too large: %d bytes exceeds the limit of %d",
			n, rs.opts.MaxStringBytes)
	}
	return nil
}
