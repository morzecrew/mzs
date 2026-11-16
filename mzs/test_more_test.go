package mzs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// RFC 0014: the members a test file reaches for after `case` and `eq`, and the options a
// runner sets. As in test_test.go, what is asserted is what reaches Result.Tests and the
// report, since that is all a runner can see.

// failure runs one case and answers what it failed with, or "" when it passed.
func failure(t *testing.T, body string) string {
	t.Helper()
	res, _, err := runTests(t, "include test\ninclude json\ntest.case(\"c\") { "+body+" }\n", Options{})
	if err != nil {
		t.Fatalf("%s: the file failed: %v", body, err)
	}
	if len(res.Tests) != 1 {
		t.Fatalf("%s: Tests = %+v, want one case", body, res.Tests)
	}
	if res.Tests[0].Err == nil {
		return ""
	}
	return res.Tests[0].Err.Error()
}

func TestNe(t *testing.T) {
	if got := failure(t, `test.ne(1, 2); test.ne("a", nil)`); got != "" {
		t.Errorf("different values failed: %s", got)
	}
	if got := failure(t, `test.ne(1, 1.0)`); !strings.Contains(got, "expected a value other than 1.0") {
		t.Errorf("equal values: %q", got)
	}
}

func TestNear(t *testing.T) {
	for _, pass := range []string{
		`test.near(0.1 + 0.2, 0.3)`,
		`test.near(1, 1.05, 0.1)`,
		`test.near(3, 3, 0)`,
		`test.near(1.0 / 0.0, 1.0 / 0.0)`, // equal values are near whatever eps is
	} {
		if got := failure(t, pass); got != "" {
			t.Errorf("%s failed: %s", pass, got)
		}
	}
	for body, want := range map[string]string{
		`test.near(0.1, 0.2, 0.01)`:       "expected 0.2 within 0.01, got 0.1",
		`test.near(0.0 / 0.0, 0.0 / 0.0)`: "expected NaN within 0.000000001, got NaN", // NaN is near nothing
		`test.near(1, 2)`:                 "expected 2 within",
	} {
		if got := failure(t, body); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", body, got, want)
		}
	}
	for body, want := range map[string]string{
		`test.near(1, 1, -1)`:                            "tolerance of 0 or more",
		`test.near("1", 1)`:                              "expects a number, got string",
		`include decimal; test.near(decimal.of("1"), 1)`: "expects a number",
	} {
		if got := failure(t, body); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", body, got, want)
		}
	}
}

func TestRaises(t *testing.T) {
	for _, pass := range []string{
		// The error dict comes back, so the test goes on to it.
		`e = test.raises("json") { json.parse("{") }; test.eq(e["kind"], "json"); assert(e["message"].len > 0)`,
		`e = test.raises { raise({message: "short", kind: "billing", data: {short_by: 30}}) }; test.eq(e["data"]["short_by"], 30)`,
		`test.eq(test.raises("billing") { raise("x", "billing") }["kind"], "billing")`,
		`test.raises(nil) { 1 / 0 }`,
	} {
		if got := failure(t, pass); got != "" {
			t.Errorf("%s failed: %s", pass, got)
		}
	}
	for body, want := range map[string]string{
		`test.raises { 42 }`:            "expected an error, got 42",
		`test.raises("json") { 1 / 0 }`: `expected kind "json", got "zero-division": divided by 0`,
		`test.raises(1) { 1 / 0 }`:      "expects a string",
		`test.raises("json")`:           "needs a block",
	} {
		if got := failure(t, body); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", body, got, want)
		}
	}
}

// raises catches what `try` catches and nothing more: a budget inside it ends the Run.
func TestRaisesDoesNotCatchALimit(t *testing.T) {
	_, _, err := runTests(t, `
include test
test.case("spins") { test.raises { while true { } } }
`, Options{StepBudget: 10_000})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want the step budget", err)
	}
}

func TestSkipInACase(t *testing.T) {
	res, out, err := runTests(t, `
include test
r = test.case("later") { test.skip("not yet"); assert(false) }
assert(r == true, "a skip answers true")
test.case("ensure runs") { try { test.skip("x") } ensure { println("released") } }
test.case("try cannot swallow it") { try { test.skip("y") } else { assert(false, "caught") } }
test.case("after") { assert(true) }
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) != 4 {
		t.Fatalf("Tests = %+v", res.Tests)
	}
	for i, want := range []string{TestSkip, TestSkip, TestSkip, TestPass} {
		if res.Tests[i].Status != want || !res.Tests[i].Passed() {
			t.Errorf("case %d = %+v, want %s and not failed", i, res.Tests[i], want)
		}
	}
	if res.Tests[0].Reason != "not yet" {
		t.Errorf("reason = %q", res.Tests[0].Reason)
	}
	report := out.String()
	for _, want := range []string{"skip later  (not yet)", "released", "ok   after"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}

// Outside a case, a skip ends the file and is not a failure of it.
func TestSkipOutsideACaseSkipsTheRestOfTheFile(t *testing.T) {
	res, out, err := runTests(t, `
include test
test.case("first") { assert(true) }
test.skip("needs a database")
test.case("never") { assert(false) }
`, Options{})
	if err != nil {
		t.Fatalf("a skipped file is an error: %v", err)
	}
	if len(res.Tests) != 2 || !res.Tests[1].RestOfFile || res.Tests[1].Name != "" || res.Tests[1].Status != TestSkip ||
		res.Tests[1].Reason != "needs a database" || res.Tests[1].Line != 4 {
		t.Fatalf("Tests = %+v, want the case and then the skipped rest of the file", res.Tests)
	}
	if !strings.Contains(out.String(), "skip (rest of file)  (needs a database)") {
		t.Errorf("report:\n%s", out)
	}
}

func TestGroupNamesItsCases(t *testing.T) {
	res, out, err := runTests(t, `
include test
test.group("rounding") {
  test.case("half up") { assert(true) }
  test.group("odd") { test.case("bankers") { assert(false) } }
}
test.case("top") { assert(true) }
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rounding › half up", "rounding › odd › bankers", "top"}
	for i, w := range want {
		if got := res.Tests[i].FullName(); got != w {
			t.Errorf("case %d = %q, want %q", i, got, w)
		}
	}
	if g := res.Tests[2].Group; len(g) != 0 {
		t.Errorf("the group leaked past its block: %v", g)
	}
	if !strings.Contains(out.String(), "FAIL rounding › odd › bankers") {
		t.Errorf("report:\n%s", out)
	}
}

// A group is naming, not isolation: an error in it outside a case ends the file.
func TestGroupDoesNotCatch(t *testing.T) {
	_, _, err := runTests(t, `
include test
test.group("g") { raise("setup broke") }
`, Options{})
	if err == nil || !strings.Contains(err.Error(), "setup broke") {
		t.Fatalf("err = %v, want the group's own failure", err)
	}
}

func TestCaseRecordsWhereItIs(t *testing.T) {
	res, _, err := runTests(t, "include test\n\n  test.case(\"here\") { assert(true) }\n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Tests[0]; c.Line != 3 || c.Col != 8 || c.Status != TestPass {
		t.Errorf("case = %+v, want line 3 col 8 (the member name), passed", c)
	}
}

func TestFilterRunsOnlyTheCasesAskedFor(t *testing.T) {
	res, out, err := runTests(t, `
include test
$ran = []
test.group("money") { test.case("sum") { $ran.push("sum") } }
r = test.case("other") { $ran.push("other") }
assert(r == nil, "a case left out answers nil")
`, Options{Test: TestOptions{Filter: func(name string) bool { return strings.HasPrefix(name, "money › ") }}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) != 1 || res.Tests[0].FullName() != "money › sum" {
		t.Fatalf("Tests = %+v", res.Tests)
	}
	if got := res.Globals["$ran"].Inspect(); got != `["sum"]` {
		t.Errorf("ran = %s: a filtered-out case's block ran", got)
	}
	if strings.Contains(out.String(), "other") {
		t.Errorf("a filtered-out case was reported:\n%s", out)
	}
}

func TestFailFastStopsTheRun(t *testing.T) {
	res, _, err := runTests(t, `
include test
test.case("one") { assert(true) }
test.case("two") { try { assert(false, "red") } ensure { 1 } }
test.case("three") { assert(true) }
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("err = %v, want ErrFailFast", err)
	}
	if len(res.Tests) != 2 || res.Tests[1].Status != TestFail {
		t.Fatalf("Tests = %+v, want two cases, the second failed", res.Tests)
	}
	// Nothing catches the stop, not even a case around the failing one.
	_, _, err = runTests(t, `
include test
test.case("outer") { test.case("inner") { assert(false) }; assert(true) }
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("nested: err = %v", err)
	}
}

func TestCaptureQuietAndVerbose(t *testing.T) {
	src := `
include test
println("file level")
test.case("quiet pass") { println("pass output") }
test.case("loud fail") { println("evidence"); assert(false) }
`
	res, out, err := runTests(t, src, Options{Test: TestOptions{Capture: true}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tests[0].Output != "pass output\n" || res.Tests[1].Output != "evidence\n" {
		t.Errorf("Output = %q, %q", res.Tests[0].Output, res.Tests[1].Output)
	}
	report := out.String()
	if strings.Contains(report, "pass output") {
		t.Errorf("a passing case's output was printed without Verbose:\n%s", report)
	}
	for _, want := range []string{"file level", "FAIL loud fail", "     | evidence"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}

	_, out, _ = runTests(t, src, Options{Test: TestOptions{Capture: true, Verbose: true}})
	if !strings.Contains(out.String(), "ok   quiet pass\n     | pass output\n") {
		t.Errorf("Verbose report:\n%s", out)
	}

	res, out, _ = runTests(t, src, Options{Test: TestOptions{Quiet: true}})
	if got := out.String(); got != "file level\npass output\nevidence\n" {
		t.Errorf("Quiet left only the script's own output? got %q", got)
	}
	if len(res.Tests) != 2 {
		t.Errorf("Quiet lost the record: %+v", res.Tests)
	}
}

// Review of #29: two cases running in two tasks at once each keep their own output,
// whichever finishes first, and a task that outlives its case prints to the Stdout.
func TestCaptureFollowsTheTaskNotTheClock(t *testing.T) {
	res, out, err := runTests(t, `
include test
async fn a() { test.case("A") { println("a1"); sleep(0.03); println("a2") } }
async fn b() { test.case("B") { sleep(0.01); println("b1"); sleep(0.05); println("b2") } }
async fn late() { sleep(0.02); println("late") }
ta = a()
tb = b()
ta.await
tb.await
test.case("C") { $t = late() }
$t.await
`, Options{Test: TestOptions{Capture: true, Verbose: true}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range res.Tests {
		got[c.Name] = c.Output
	}
	if got["A"] != "a1\na2\n" || got["B"] != "b1\nb2\n" || got["C"] != "" {
		t.Errorf("Output = %q", got)
	}
	if !strings.Contains(out.String(), "late\n") {
		t.Errorf("what a task printed after its case ended was lost:\n%s", out)
	}
}

// Review of #29: a nested case's report is report, not the outer case's output.
func TestNestedReportIsNotCaptured(t *testing.T) {
	_, out, err := runTests(t, `
include test
test.case("outer") { test.case("inner") { println("evidence"); assert(false) } }
`, Options{Test: TestOptions{Capture: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FAIL inner", "     | evidence", "ok   outer"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

// Review of #29: a case that fails in a task under fail-fast stops the Run, even when
// nothing awaits the task.
func TestFailFastFromATask(t *testing.T) {
	res, _, err := runTests(t, `
include test
async fn bad() { test.case("in a task") { assert(false) } }
bad()
sleep(0.02)
test.case("after") { assert(true) }
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("err = %v, want ErrFailFast", err)
	}
	if len(res.Tests) != 1 || res.Tests[0].Name != "in a task" {
		t.Errorf("Tests = %+v, want only the failure", res.Tests)
	}
	_, _, err = runTests(t, `
include test
async fn bad() { test.case("in a task") { assert(false) } }
bad()
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("never awaited: err = %v, want ErrFailFast", err)
	}
}

// Review of #29: two ints are compared as ints, not as the one float they round to.
func TestNearComparesIntsExactly(t *testing.T) {
	for _, body := range []string{
		`test.near(9007199254740993, 9007199254740992, 0)`,
		// The distance is one more than the tolerance, and both round to 2^53 as floats.
		`test.near(9007199254740993, 0, 9007199254740992)`,
		`test.near(9007199254740993, 0, 9007199254740992.0)`,
		`test.near(-9223372036854775807, 9223372036854775807, 1.0e18)`,
	} {
		if got := failure(t, body); !strings.Contains(got, "within") {
			t.Errorf("%s passed: the distance exceeds the tolerance", body)
		}
	}
	for _, pass := range []string{
		`test.near(9007199254740993, 9007199254740992, 1)`,
		`test.near(-9223372036854775807, 9223372036854775807, 2.0e19)`,
		`test.near(9007199254740993, 0, 9007199254740993)`,
		`test.near(9007199254740993, 1, 9007199254740992)`,
		`test.near(5, 3, 2.5)`,
	} {
		if got := failure(t, pass); got != "" {
			t.Errorf("%s failed: %s", pass, got)
		}
	}
}

// Review of #29: a task still running when fail-fast stops the Run is drained, but it
// starts no case, and its being stopped is not reported as a failure.
func TestFailFastStopsADrainingTask(t *testing.T) {
	var errOut strings.Builder
	res, _, err := runTests(t, `
include test
async fn later() { sleep(0.02); test.case("from a draining task") { println("ran") } }
later()
test.case("fails") { assert(false) }
`, Options{Stderr: &errOut, Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("err = %v, want ErrFailFast", err)
	}
	if len(res.Tests) != 1 || res.Tests[0].Name != "fails" {
		t.Errorf("Tests = %+v, want only the failure", res.Tests)
	}
	if strings.Contains(errOut.String(), "never awaited") {
		t.Errorf("a task stopped by fail-fast was reported as failed:\n%s", errOut.String())
	}
}

// Review of #29: an empty kind is a kind asked for, which no error has.
func TestRaisesWithAnEmptyKind(t *testing.T) {
	if got := failure(t, `test.raises("") { 1 / 0 }`); !strings.Contains(got, `expected kind "", got "zero-division"`) {
		t.Errorf("raises(\"\") = %q", got)
	}
}

// Review of #29: the program that resumes from a wait after a task failed under
// fail-fast stops there, rather than running on to statements of its own.
func TestFailFastStopsTheProgramAtItsNextWait(t *testing.T) {
	_, out, err := runTests(t, `
include test
async fn bad() { test.case("worker") { assert(false) } }
bad()
sleep(0.02)
println("still running")
assert(false, "later error")
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("err = %v, want ErrFailFast and not the later error", err)
	}
	if strings.Contains(out.String(), "still running") {
		t.Errorf("the program ran on past its wait:\n%s", out)
	}
	// tasks.all and await stop the same way.
	_, _, err = runTests(t, `
include test
async fn bad() { test.case("worker") { assert(false) } }
async fn ok() { sleep(0.05); 1 }
t = ok()
bad()
t.await
assert(false, "later error")
`, Options{Test: TestOptions{FailFast: true}})
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("await: err = %v, want ErrFailFast", err)
	}
}

// Review of #29: a host call through Ctx.Blocking gives the lock up too, and the program
// returning from it does not run on after a task failed under fail-fast meanwhile.
func TestFailFastStopsTheProgramAfterABlockingCall(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	var out strings.Builder
	in := New(Options{Stdout: &out, Test: TestOptions{FailFast: true}})
	in.Register("__wait", 0, func(c *Ctx, _ []Value) (Value, error) {
		c.Blocking(func() { <-release })
		return Nil(), nil
	})
	in.Register("__release", 0, func(c *Ctx, _ []Value) (Value, error) {
		once.Do(func() { close(release) })
		return Nil(), nil
	})
	prog, err := in.Compile("t_test.mzs", `
include test
async fn bad() { test.case("worker") { __release(); assert(false) } }
bad()
__wait()
println("still running")
assert(false, "later error")
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = in.RunResult(context.Background(), prog, nil)
	if !errors.Is(err, ErrFailFast) {
		t.Fatalf("err = %v, want ErrFailFast and not the later error", err)
	}
	if strings.Contains(out.String(), "still running") {
		t.Errorf("the program ran on past the blocking call:\n%s", out.String())
	}
}
