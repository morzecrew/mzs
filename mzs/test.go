package mzs

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
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
	RegisterModuleFunc("test", "ne", 2, 2, testNe)
	RegisterModuleFunc("test", "near", 2, 3, testNear)
	RegisterModuleFunc("test", "raises", 0, 2, testRaises)
	RegisterModuleFunc("test", "skip", 1, 1, testSkip)
	RegisterModuleFunc("test", "group", 1, 2, testGroup)
}

// The three values of TestCase.Status (§12.18).
const (
	TestPass = "pass"
	TestFail = "fail"
	TestSkip = "skip"
)

// ErrFailFast is what a Run under Options.Test.FailFast ends with once a case has
// failed (§12.18). The failure itself is in Result.Tests; this only says why the Run
// stopped there.
var ErrFailFast = errors.New("mzs: a test case failed under fail-fast")

// TestCase is one `test.case` of a Run, in the order the cases ran (§12.18). Err is nil
// when the case passed or was skipped, and holds what it raised when it failed.
//
// An entry with an empty Name is the rest of a file skipped: `test.skip` called outside
// any case, at Line and Col.
type TestCase struct {
	Name    string
	Err     error
	Elapsed time.Duration
	// Status is TestPass, TestFail or TestSkip.
	Status string
	// Group is the `test.group` names the case was declared in, outermost first.
	Group []string
	// Reason is what `test.skip` said, for a skipped case.
	Reason string
	// Line and Col are where `test.case` was called.
	Line, Col int
	// Output is what the case printed, under Options.Test.Capture.
	Output string
}

// Passed reports whether the case passed. A skipped case did not fail, and counts as
// passed: skips do not make a run red.
func (t TestCase) Passed() bool { return t.Status != TestFail && t.Err == nil }

// FullName is the case's name under its groups, as a report prints it and as
// Options.Test.Filter sees it: `rounding › half up`.
func (t TestCase) FullName() string { return testFullName(t.Group, t.Name) }

func testFullName(group []string, name string) string {
	if len(group) == 0 {
		return name
	}
	return strings.Join(append(slices.Clip(group), name), " › ")
}

// testSignal is how `test.skip` and fail-fast leave the code they are in. It is not an
// error a script can catch (§8.11): a skip that a `try` swallowed would be a case that
// went on running after it said it would not, and a fail-fast that a `try` swallowed would
// not stop anything. An `ensure` does run on the way out of a skip, which is an ordinary
// way out of a block; under fail-fast the Run ends, so it does not.
type testSignal struct {
	skip      bool
	reason    string
	line, col int
}

func (s *testSignal) Error() string {
	if s.skip {
		return "test.skip: " + s.reason
	}
	return ErrFailFast.Error()
}

func (s *testSignal) Unwrap() error {
	if s.skip {
		return nil
	}
	return ErrFailFast
}

// testSignalOf finds the signal in an error chain.
func testSignalOf(err error) (*testSignal, bool) {
	var s *testSignal
	if err != nil && errors.As(err, &s) {
		return s, true
	}
	return nil, false
}

// isTestSkip reports a `test.skip` travelling out of a block.
func isTestSkip(err error) bool {
	s, ok := testSignalOf(err)
	return ok && s.skip
}

// fileSkipped is the entry a `test.skip` outside every case leaves in Result.Tests: the
// rest of the file did not run, and the report says so instead of reading as a file that
// ended with fewer cases (§12.18).
func fileSkipped(s *testSignal) TestCase {
	return TestCase{Status: TestSkip, Reason: s.reason, Line: s.line, Col: s.col}
}

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
	o := c.rs.opts.Test
	group := slices.Clone(c.rs.testGroups)
	if o.Filter != nil && !o.Filter(testFullName(group, name)) {
		// Not run, not counted: a filter narrows the file to the cases asked for, and a
		// case left out has neither passed nor failed.
		return Nil(), nil
	}
	tc := TestCase{Name: name, Group: group, Line: c.pos.Line, Col: c.pos.Col}

	var out, prev *strings.Builder
	if o.Capture {
		prev, out = c.rs.sh.testOut, &strings.Builder{}
		c.rs.sh.testOut = out
	}
	outer := c.rs.frames()
	c.rs.testDepth++
	start := time.Now()
	_, rerr := c.CallClosure()
	tc.Elapsed = time.Since(start)
	c.rs.testDepth--
	if o.Capture {
		c.rs.sh.testOut = prev
		tc.Output = out.String()
	}

	if s, ok := testSignalOf(rerr); ok {
		if !s.skip {
			// A case nested in this one failed under fail-fast: it is recorded already,
			// and this one has not finished.
			return Nil(), rerr
		}
		tc.Status, tc.Reason = TestSkip, s.reason
		c.rs.sh.tests = append(c.rs.sh.tests, tc)
		reportLine(c, "skip "+tc.FullName()+"  ("+s.reason+")")
		reportOutput(c, tc, false)
		return Bool(true), nil
	}
	if _, ok := catchableError(rerr); rerr != nil && !ok {
		// A timeout, a budget or `exit` is the Run ending, not a case failing (§8.11).
		// Recording it as a failure would report a red test where there is a stopped
		// process, and swallowing it would hide the stop.
		return Nil(), rerr
	}
	tc.Err = rerr
	if rerr != nil {
		tc.Status = TestFail
		c.rs.sh.tests = append(c.rs.sh.tests, tc)
		reportLine(c, "FAIL "+tc.FullName())
		reportLine(c, "     "+errorLine(rerr))
		for _, line := range FormatTrace(caseFrames(rerr, outer)) {
			reportLine(c, "       "+line)
		}
		reportOutput(c, tc, true)
		if o.FailFast {
			return Nil(), &testSignal{}
		}
		return Bool(false), nil
	}
	tc.Status = TestPass
	c.rs.sh.tests = append(c.rs.sh.tests, tc)
	reportLine(c, "ok   "+tc.FullName())
	reportOutput(c, tc, false)
	return Bool(true), nil
}

// reportOutput prints a case's captured output under its report line: always for a case
// that failed, which is when the output is evidence, and for every case under Verbose.
func reportOutput(c *Ctx, tc TestCase, failed bool) {
	o := c.rs.opts.Test
	if tc.Output == "" || !(failed || o.Verbose) {
		return
	}
	for _, line := range strings.Split(strings.TrimSuffix(tc.Output, "\n"), "\n") {
		reportLine(c, "     | "+line)
	}
}

// testGroup names the cases declared in its block: they are reported as
// `name › case`, and groups nest. It is naming and nothing else — no setup, no
// isolation, no catching: a failure in the block outside any case ends the file as it
// would without the group (§12.18).
func testGroup(c *Ctx, args []Value) (Value, error) {
	name, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if !c.HasClosure() {
		return Nil(), c.ArgErrorf("test.group needs a block: test.group(%q) { … }", name)
	}
	saved := c.rs.testGroups
	// Clip first, so the append never writes into a backing array a task started
	// earlier still reads its own groups from.
	c.rs.testGroups = append(slices.Clip(saved), name)
	_, rerr := c.CallClosure()
	c.rs.testGroups = saved
	if rerr != nil {
		return Nil(), rerr
	}
	return Nil(), nil
}

// testSkip ends the current case as skipped. Outside a case it ends the file, whose
// remaining cases are then not run: a test file that needs a capability the host did not
// give it says so once, at the top, instead of failing every case for the same reason.
func testSkip(c *Ctx, args []Value) (Value, error) {
	reason, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if c.rs.testDepth == 0 {
		reportLine(c, "skip (rest of file)  ("+reason+")")
	}
	return Nil(), &testSignal{skip: true, reason: reason, line: c.pos.Line, col: c.pos.Col}
}

// testNe is eq's negation, with the value it did not want in the message.
func testNe(c *Ctx, args []Value) (Value, error) {
	if !args[0].Equal(args[1]) {
		return Nil(), nil
	}
	return Nil(), c.position(raiseError("expected a value other than "+args[1].Inspect(), Nil()))
}

// testNear compares two numbers with a tolerance, because `0.1 + 0.2 == 0.3` is false
// in every language with IEEE floats and a test that wants the arithmetic right needs to
// say how right. Equal values are near whatever eps is — that is how infinity is near
// itself — and NaN is near nothing, itself included. A Decimal is not a number here:
// Decimal arithmetic is exact, so `test.eq` is the comparison for it.
func testNear(c *Ctx, args []Value) (Value, error) {
	a, err := argNum(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b, err := argNum(c, args[1])
	if err != nil {
		return Nil(), err
	}
	eps := Float(1e-9)
	if len(args) > 2 {
		if eps, err = argNum(c, args[2]); err != nil {
			return Nil(), err
		}
		if e := eps.Float(); math.IsNaN(e) || e < 0 {
			return Nil(), c.ArgErrorf("test.near wants a tolerance of 0 or more, got %s", eps.Inspect())
		}
	}
	x, y := a.Float(), b.Float()
	if x == y || math.Abs(x-y) <= eps.Float() {
		return Nil(), nil
	}
	return Nil(), c.position(raiseError(
		"expected "+b.Inspect()+" within "+eps.Inspect()+", got "+a.Inspect(), Nil()))
}

// testRaises runs its block and wants it to fail. It answers the error dict a `try`
// handler would bind (§8.11), so the test goes on to the message and the data instead of
// stopping at "it raised". It catches what `try` catches and nothing more: a limit or
// `exit` inside the block still ends the Run.
func testRaises(c *Ctx, args []Value) (Value, error) {
	if !c.HasClosure() {
		return Nil(), c.ArgErrorf(`test.raises needs a block: test.raises("kind") { … }`)
	}
	args = args[:len(args)-1] // the last argument is the block
	kind := ""
	if len(args) > 0 && !args[0].IsNil() {
		k, err := argStr(c, args[0])
		if err != nil {
			return Nil(), err
		}
		kind = k
	}
	v, rerr := c.CallClosure()
	if rerr == nil {
		return Nil(), c.position(raiseError("expected an error, got "+v.Inspect(), Nil()))
	}
	x, ok := catchableError(rerr)
	if !ok {
		return Nil(), rerr
	}
	if kind != "" && x.Kind != kind {
		return Nil(), c.position(raiseError(
			fmt.Sprintf("expected kind %q, got %q: %s", kind, x.Kind, x.Msg), Nil()))
	}
	return x.ErrorValue(), nil
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

// caseFrames is the part of a failure's stack that is inside the case: the frames the
// case's own block called, without the block itself, `test.case` and whatever called the
// file. outer is the stack test.case was called under, its own frame included; a stack
// that ends with outer and the block's frame is cut there. One that does not — the
// failure of an awaited task carries the task's own stack, which starts at the task — is
// all inside the case, and is shown whole (§12.18).
func caseFrames(err error, outer []Frame) []Frame {
	e := asError(err)
	if e == nil {
		return nil
	}
	s := e.Stack
	n := len(s) - len(outer) - 1
	if n < 0 || !slices.Equal(s[n+1:], outer) {
		return s
	}
	return s[:n]
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

// reportLine writes one line of the human report, unless the host asked for none.
func reportLine(c *Ctx, s string) {
	if c.rs.opts.Test.Quiet {
		return
	}
	w := c.Out()
	if w == nil {
		return
	}
	_, _ = w.Write([]byte(s + "\n"))
}
