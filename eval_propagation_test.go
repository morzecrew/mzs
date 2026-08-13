package mzs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// §8.11 says an error unwinds to the nearest `try` and, failing that, out of the Run.
// What that means in practice is one property repeated across the grammar: **every**
// position where a subexpression is evaluated has to hand the error upward unchanged —
// same kind, same message, same position — instead of swallowing it, replacing it with
// its own, or turning it into a value.
//
// The table below puts one `raise("boom")` in each such position. A row that fails is
// either a construct that dropped an error on the floor or one that rewrote it, and
// both are the kind of bug that only shows up in someone's production script.

func TestErrorsPropagateFromEveryPosition(t *testing.T) {
	tests := []struct {
		name string
		src  string
		col  int // where the reported error must point; 0 means "at the raise itself"
	}{
		{"an array element", `[1, raise("boom"), 3]`, 0},
		{"a dict key", `{"${raise("boom")}": 1}`, 0},
		{"a dict value", `{k: raise("boom")}`, 0},
		{"a range's upper bound", `1..raise("boom")`, 0},
		{"a range's lower bound", `raise("boom")..3`, 0},
		{"an index", `[1,2][raise("boom")]`, 0},
		{"the length of a two-argument index", `[1,2][0, raise("boom")]`, 0},
		{"the iterable of a for", `for x in raise("boom") { x }`, 0},
		{"the body of a for", `for x in [1] { raise("boom") }`, 0},
		{"the condition of a while", `while raise("boom") { 1 }`, 0},
		{"the body of a while", `while true { raise("boom") }`, 0},
		{"the subject of a match", `match raise("boom") { else -> 1 }`, 0},
		{"a match pattern", `match 1 { raise("boom") -> 1; else -> 2 }`, 0},
		{"an `in` pattern", `match 1 { in raise("boom") -> 1; else -> 2 }`, 0},
		{"a match guard", `match 1 { 1 if raise("boom") -> 1; else -> 2 }`, 0},
		{"an interpolation", `"${raise("boom")}"`, 0},
		{"the left of &&", `raise("boom") && true`, 0},
		{"the right of &&", `true && raise("boom")`, 0},
		{"the condition of a ternary", `raise("boom") ? 1 : 2`, 0},
		{"the then of a ternary", `true ? raise("boom") : 2`, 0},
		{"the else of a ternary", `false ? 1 : raise("boom")`, 0},
		{"the condition of an if", `if raise("boom") { 1 }`, 0},
		{"the body of an if", `if true { raise("boom") }`, 0},
		{"the value of an assignment", `x = raise("boom")`, 0},
		{"the right of a destructuring", `a, b = raise("boom")`, 0},
		{"an element of a destructured array", `[a, b] = [raise("boom"), 2]`, 0},
		{"an index being assigned to", `d = {k: 1}; d[raise("boom")] = 2`, 0},
		{"the value being assigned to an index", `d = {k: 1}; d["k"] = raise("boom")`, 0},
		{"a global's value", `$g = raise("boom")`, 0},
		{"a call argument", `fn f(a) { a }; f(raise("boom"))`, 0},
		{"a function body", `fn f(a) { raise("boom") }; f(1)`, 0},
		{"a default argument", `fn f(a = raise("boom")) { a }; f()`, 0},
		{"a closure's body", `x = { () -> raise("boom") }; x()`, 0},
		{"a method receiver", `raise("boom").len`, 0},
		{"a method argument", `[1].each_slice(raise("boom"))`, 0},
		{"a module member's argument", `include json; json.parse(raise("boom"))`, 0},
		{"a closure passed to a stdlib row", `[1].map { raise("boom") }`, 0},
		{"the operand of a unary minus", `-raise("boom")`, 0},
		{"the operand of a not", `!raise("boom")`, 0},
		{"the left of a binary operator", `raise("boom") + 1`, 0},
		{"the right of a binary operator", `1 + raise("boom")`, 0},
		{"a statement inside a group", `(1; raise("boom"))`, 0},
		{"the value of a return", `fn f() { return raise("boom") }; f()`, 0},
		{"the value of a break", `while true { break raise("boom") }`, 0},
		{"the value of a next", `while true { next raise("boom") }`, 0},
		{"the fallback of a try", `try raise("x") else raise("boom")`, 0},
		// The handler builds a new error, so the position is the re-raise, not the
		// raise it came from. Reporting the original would point at a line the second
		// failure did not come from.
		{"a re-raise from the handler", `try raise("boom") else (e) -> raise(e["message"])`, 31},
	}

	// Limits off: this is about the error that the program raises, and a deadline or a
	// budget firing first would prove nothing (§14.1 errors are not catchable anyway).
	in := New(Options{Timeout: 0, StepBudget: -1})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := in.Eval(context.Background(), tt.src, nil)
			if err == nil {
				t.Fatalf("%s: no error; the raise was swallowed", tt.src)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s: error is %T (%v), want *Error", tt.src, err, err)
			}
			if e.Msg != "boom" || e.Kind != ErrKindRaise {
				t.Errorf("%s: error = %s: %q; want %s: %q",
					tt.src, e.Kind, e.Msg, ErrKindRaise, "boom")
			}
			// The position must be the raise's own, not merely some non-zero one: an
			// error rebuilt on the way out would still have a position, just the
			// wrong one. Every program here is a single line, so the column of the
			// `raise` in the source is the whole expectation.
			wantCol := tt.col
			if wantCol == 0 {
				wantCol = strings.Index(tt.src, `raise("boom")`) + 1
			}
			if e.Line != 1 || e.Col != wantCol {
				t.Errorf("%s: error reported at %d:%d; want 1:%d, where the raise is",
					tt.src, e.Line, e.Col, wantCol)
			}
		})
	}
}

// The other half of §8.11: `try` catches what a script raised, and does **not** catch
// what the sandbox raised. A limit is not a failure the program is allowed to survive,
// or `ensure`-style cleanup would become a way to outlive the deadline.
func TestTryCatchesRaisesButNotLimits(t *testing.T) {
	t.Run("a raise is caught", func(t *testing.T) {
		in := New(Options{Timeout: 0, StepBudget: -1})
		v, err := in.Eval(context.Background(), `try raise("boom") else "caught"`, nil)
		if err != nil {
			t.Fatalf("Eval error = %v", err)
		}
		if v.Str() != "caught" {
			t.Errorf("= %q; want %q", v.Str(), "caught")
		}
	})

	t.Run("a step budget is not caught", func(t *testing.T) {
		in := New(Options{Timeout: 0, StepBudget: 1000})
		_, err := in.Eval(context.Background(), `try (while true { }) else "caught"`, nil)
		if !errors.Is(err, ErrBudget) {
			t.Errorf("error = %v; want ErrBudget to pass straight through the try", err)
		}
	})

	t.Run("a cancelled context is not caught", func(t *testing.T) {
		in := New(Options{Timeout: 0, StepBudget: -1})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := in.Eval(ctx, `try (while true { }) else "caught"`, nil)
		if !errors.Is(err, ErrCanceled) {
			t.Errorf("error = %v; want ErrCanceled to pass straight through the try", err)
		}
	})

	t.Run("the collection cap is not caught", func(t *testing.T) {
		in := New(Options{Timeout: 0, StepBudget: -1, MaxCollection: 10})
		_, err := in.Eval(context.Background(), `try (1..1000).array else "caught"`, nil)
		if err == nil {
			t.Fatal("the cap was caught by try; §14.2 limits are not catchable")
		}
		var e *Error
		if !errors.As(err, &e) || e.Kind != ErrKindLimit {
			t.Errorf("error = %v; want a limit error", err)
		}
	})
}

// A raise carries its data and its position all the way out, which is what makes
// `try … else (e) -> …` able to tell one failure from another (§8.11).
func TestRaisedErrorCarriesKindDataAndPosition(t *testing.T) {
	in := New(Options{Timeout: 0, StepBudget: -1})

	src := "x = 1\ny = 2\nraise({message: \"нет ключа\", code: 404})"
	_, err := in.Eval(context.Background(), src, nil)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error is %T (%v), want *Error", err, err)
	}
	if e.Msg != "нет ключа" {
		t.Errorf("Msg = %q; want the dict's message key", e.Msg)
	}
	if e.Line != 3 {
		t.Errorf("Line = %d; want 3", e.Line)
	}
	if got := e.Data.Get(Str("code")); got.Int() != 404 {
		t.Errorf("Data[code] = %s; want 404", got.Inspect())
	}
	if !strings.Contains(e.Error(), "нет ключа") {
		t.Errorf("Error() = %q; want it to carry the message", e.Error())
	}

	// The same error, seen from inside the script.
	v, err := in.Eval(context.Background(),
		`try raise({message: "нет ключа", code: 404}) else (e) -> "${e["kind"]}/${e["data"]["code"]}/${e["line"]}"`, nil)
	if err != nil {
		t.Fatalf("Eval error = %v", err)
	}
	if v.Str() != "raise/404/1" {
		t.Errorf("the handler saw %q; want %q", v.Str(), "raise/404/1")
	}
}
