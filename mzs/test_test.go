package mzs

import (
	"context"
	"strings"
	"testing"
	"time"
)

// SPEC §12.18. The module is a runner, so what is worth asserting is what it does to the
// Run around it: which cases were recorded, in what order, what the printed report says,
// and what it refuses to swallow.

// runTests compiles and runs src with a writer for the report, and hands back the Run's
// result.
func runTests(t *testing.T, src string, o Options) (Result, *strings.Builder, error) {
	t.Helper()
	var out strings.Builder
	o.Stdout = &out
	in := New(o)
	prog, err := in.Compile("t_test.mzs", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, rerr := in.RunResult(context.Background(), prog, nil)
	return res, &out, rerr
}

func TestCaseRecordsEveryCaseInOrder(t *testing.T) {
	res, out, err := runTests(t, `
include test
test.case("first")  { assert(true) }
test.case("second") { assert(false, "nope") }
test.case("third")  { test.eq(1 + 1, 2) }
`, Options{})
	if err != nil {
		t.Fatalf("the file itself failed: %v", err)
	}
	if len(res.Tests) != 3 {
		t.Fatalf("Result.Tests = %+v, want three cases", res.Tests)
	}
	for i, want := range []struct {
		name string
		pass bool
	}{{"first", true}, {"second", false}, {"third", true}} {
		if res.Tests[i].Name != want.name || res.Tests[i].Passed() != want.pass {
			t.Errorf("case %d = %q passed=%v, want %q passed=%v",
				i, res.Tests[i].Name, res.Tests[i].Passed(), want.name, want.pass)
		}
	}
	report := out.String()
	for _, want := range []string{"ok   first", "FAIL second", "nope", "ok   third"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}

// A failing case is caught, so the cases after it still run. That is the whole reason
// the module exists next to `assert`.
func TestCaseKeepsGoingAfterAFailure(t *testing.T) {
	res, _, err := runTests(t, `
include test
test.case("raises") { raise("boom") }
test.case("after")  { assert(true) }
`, Options{})
	if err != nil {
		t.Fatalf("a failing case ended the file: %v", err)
	}
	if len(res.Tests) != 2 || res.Tests[0].Passed() || !res.Tests[1].Passed() {
		t.Fatalf("Tests = %+v, want a failure and then a pass", res.Tests)
	}
	if !strings.Contains(res.Tests[0].Err.Error(), "boom") {
		t.Errorf("the failure lost its message: %v", res.Tests[0].Err)
	}
}

// `test.case` answers whether the case passed, so a file may branch on it.
func TestCaseIsAValue(t *testing.T) {
	in := New(Options{Stdout: &strings.Builder{}})
	if got := evStr(t, in, `include test; test.case("x") { assert(true) }`); got != "true" {
		t.Errorf("a passing case = %q, want true", got)
	}
	if got := evStr(t, in, `include test; test.case("x") { assert(false) }`); got != "false" {
		t.Errorf("a failing case = %q, want false", got)
	}
}

// What `try` cannot catch, a case cannot catch either (§8.11): a Run that ran out of time
// has not failed a test, it has stopped.
func TestCaseDoesNotSwallowALimit(t *testing.T) {
	_, _, err := runTests(t, `
include test
test.case("runs away") { while true { 1 } }
test.case("never runs") { assert(true) }
`, Options{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("the timeout was swallowed by the case")
	}
	if e := asError(err); e == nil || e.Kind != ErrKindLimit {
		t.Errorf("err = %v, want a limit", err)
	}
}

func TestCaseNeedsABlock(t *testing.T) {
	in := New(Options{Stdout: &strings.Builder{}})
	e := evErr(t, in, `include test; test.case("x")`, nil)
	if !strings.Contains(e.Msg, "needs a block") {
		t.Errorf("message = %q, want it to name the block", e.Msg)
	}
}

// The message is the reason `eq` exists: `assert(a == b)` can only say that something
// was false.
func TestEqSaysWhatItWantedAndWhatItGot(t *testing.T) {
	res, _, err := runTests(t, `
include test
test.case("shapes") { test.eq("Да".lower, "нет") }
`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Tests[0].Err.Error()
	if !strings.Contains(msg, `expected "нет"`) || !strings.Contains(msg, `got "да"`) {
		t.Errorf("message = %q, want both sides inspected", msg)
	}
}

// Equality is the language's own (§7.4): a test that disagreed with `==` would be testing
// something no program can observe.
func TestEqUsesTheLanguagesEquality(t *testing.T) {
	in := New(Options{Stdout: &strings.Builder{}})
	for _, src := range []string{
		`include test; test.eq(1, 1.0); "ok"`,
		`include test; test.eq([1, {a: 2}], [1, {a: 2}]); "ok"`,
		`include test; test.eq(nil, nil); "ok"`,
	} {
		if got := evStr(t, in, src); got != "ok" {
			t.Errorf("%s = %q, want it to pass", src, got)
		}
	}
	e := evErr(t, in, `include test; test.eq(1, "1")`, nil)
	if e.Kind != ErrKindRaise {
		t.Errorf("kind = %q, want %q", e.Kind, ErrKindRaise)
	}
}

// A program that never includes the module carries no report, which is what keeps
// Result.Tests free for the one case it means something in.
func TestResultHasNoTestsWithoutTheModule(t *testing.T) {
	res, _, err := runTests(t, `x = 1`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) != 0 {
		t.Errorf("Tests = %+v, want none", res.Tests)
	}
}

// The module needs nothing from the host: like `json` and `decimal`, the include is all
// of it (§14.3).
func TestModuleNeedsNoCapability(t *testing.T) {
	o := DefaultOptions()
	if _, ok := LookupModule("test", &o); !ok {
		t.Error("the test module is gated off under the default options")
	}
}
