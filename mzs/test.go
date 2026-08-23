package mzs

import (
	"fmt"
	"time"
)

// The `test` module (§12.18): a runner for code written in mzs.
//
// `assert` has been in §12.1 from the beginning and was never enough on its own. A file
// full of assertions stops at the first one that fails, says nothing about the ones after
// it, and has no name to put in a report — so the whole corpus of this language lives in
// Go (`corpus_test.go`) and a script of your own had nothing to be tested with. That is a
// gap for a language with modules and `export`.
//
//	include test
//
//	test.case("нормализация") { assert("  ДА ".lower.trim == "да") }
//	test.case("деньги") { test.eq(decimal.times(decimal.of("10.00"), 3), decimal.of("30.00")) }
//
// **A case catches, the file keeps going.** `test.case` runs its closure and holds on to
// whatever it raised, so the second case runs after the first one fails and the report
// names both. What it does not catch is what `try` does not catch either (§8.11): a
// timeout, a step budget and `exit` end the file, because a case that ran out of time has
// not failed — it has stopped.
//
// **`eq` exists for its message.** `assert(a == b)` can only say "assertion failed";
// `test.eq(a, b)` says what it wanted and what it got, in the spelling `inspect` gives
// them (§12.1), which is the difference between a report and a puzzle.
//
// **The report is a value, not a print.** Every case is recorded on the Run and comes
// back in `Result.Tests` (§13.3), which is what `mzs test` counts and what an embedder
// reads to run its own scripts' tests. The lines on stdout are for the person watching.

func init() {
	RegisterModuleFunc("test", "case", 1, 2, testCase)
	RegisterModuleFunc("test", "eq", 2, 2, testEq)
}

// TestCase is one `test.case` of a Run, in the order the cases ran (§12.18). Err is nil
// when the case passed and holds what it raised when it did not.
type TestCase struct {
	Name    string
	Err     error
	Elapsed time.Duration
}

// Passed reports whether the case passed.
func (t TestCase) Passed() bool { return t.Err == nil }

// testCase runs one case. The block is the trailing `{ … }` of §4.2 — which the
// evaluator hands over as the last argument *and* as the Ctx's closure — so
// `test.case("name") { … }` reads as a heading over a body rather than as a call with a
// function in it.
func testCase(c *Ctx, args []Value) (Value, error) {
	name, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if !c.HasClosure() {
		return Nil(), c.ArgErrorf("test.case needs a block: test.case(%q) { … }", name)
	}
	start := time.Now()
	_, rerr := c.CallClosure()
	elapsed := time.Since(start)

	if _, ok := catchableError(rerr); rerr != nil && !ok {
		// A timeout, a budget or `exit` is the Run ending, not a case failing (§8.11).
		// Recording it as a failure would report a red test where there is a stopped
		// process, and swallowing it would hide the stop.
		return Nil(), rerr
	}
	c.rs.sh.tests = append(c.rs.sh.tests, TestCase{Name: name, Err: rerr, Elapsed: elapsed})
	if rerr != nil {
		writeLine(c, "FAIL "+name)
		writeLine(c, "     "+errorLine(rerr))
		return Bool(false), nil
	}
	writeLine(c, "ok   "+name)
	return Bool(true), nil
}

// testEq is the comparison whose failure is worth reading. Equality is the language's
// own (§7.4), so `1 == 1.0` passes here exactly as it does in a program: a test that
// disagreed with `==` would be testing something the program cannot observe.
func testEq(c *Ctx, args []Value) (Value, error) {
	if args[0].Equal(args[1]) {
		return Nil(), nil
	}
	return Nil(), c.position(raiseError(
		"expected "+args[1].Inspect()+", got "+args[0].Inspect(), Nil()))
}

// errorLine is what a failure says on one line: the message with the position it was
// raised at, which for `test.eq` is the line of the comparison itself.
func errorLine(err error) string {
	e := asError(err)
	if e == nil {
		return err.Error()
	}
	switch {
	case e.Line > 0 && e.File != "":
		return fmt.Sprintf("%s (%s:%d:%d)", e.Msg, e.File, e.Line, e.Col)
	case e.Line > 0:
		return fmt.Sprintf("%s (%d:%d)", e.Msg, e.Line, e.Col)
	}
	return e.Msg
}

func writeLine(c *Ctx, s string) {
	w := c.Out()
	if w == nil {
		return
	}
	_, _ = w.Write([]byte(s + "\n"))
}
