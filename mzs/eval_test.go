package mzs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The evaluator's contract with SPEC §8, driven through the real front end: a semantics
// test that builds ast trees by hand pins the evaluator to the parser's current output
// instead of to the language, and the language is what §8 defines.

// evInterp is the evaluator under test: every capability off, every limit at its
// documented default (§13.2).
func evInterp() *Interp { return New(Options{}) }

// evOK evaluates src and fails the test if it raises.
func evOK(t *testing.T, in *Interp, src string, vars map[string]Value) Value {
	t.Helper()
	v, err := in.Eval(context.Background(), src, vars)
	if err != nil {
		t.Fatalf("Eval(%s): %v", src, err)
	}
	return v
}

// evErr evaluates src and fails the test unless it raises an *Error.
func evErr(t *testing.T, in *Interp, src string, vars map[string]Value) *Error {
	t.Helper()
	v, err := in.Eval(context.Background(), src, vars)
	if err == nil {
		t.Fatalf("Eval(%s) = %s, want an error", src, v.Inspect())
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("Eval(%s) error is %T (%v), want *Error", src, err, err)
	}
	return e
}

// evStr evaluates src and renders the result with str (§12.7), which is how every table
// below states its expectation.
func evStr(t *testing.T, in *Interp, src string) string {
	t.Helper()
	return evOK(t, in, src, nil).Str()
}

// TestProgramResult is §8.1: the value of a program is the value of the last statement
// executed, and a top-level return ends it.
func TestProgramResult(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"the last of several statements", `1; 2; 3`, "3"},
		{"an assignment has the assigned value", `x = 5`, "5"},
		{"an empty program is nil", ``, ""},
		{"only comments is nil", "# nothing\n", ""},
		{"a block's value is its last statement", `if true { 1; 2 }`, "2"},
		{"an if with no else and a false test is nil", `if false { 1 }`, ""},
		{"a top-level return ends the program", `return 4; 5`, "4"},
		{"a while loop with no break is nil", `i = 0; while i < 3 { i += 1 }`, ""},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestScopes is §8.2, and E16 is the rule that changed: every `{ … }` is a closure and
// therefore a scope, so `=` reaches out to an existing binding but a name first created
// inside a block does not survive it.
func TestScopes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		err  string
	}{
		{name: "assignment reaches the outer binding", src: `x = 0; if true { x = 1 }; x`, want: "1"},
		{name: "a name born in a block does not escape", src: `if true { y = 1 }; y`,
			err: "undefined variable 'y'"},
		{name: "a name born in a loop body does not escape", src: `while false { z = 1 }; z`,
			err: "undefined variable 'z'"},
		{name: "a name born in a match arm does not escape", src: `match 1 { 1 -> { w = 1 } }; w`,
			err: "undefined variable 'w'"},
		{name: "walrus shadows in the current scope", src: `x = 1; if true { x := 2 }; x`, want: "1"},
		{name: "walrus at the same level rebinds", src: `x := 1; x := 2; x`, want: "2"},
		{name: "a closure sees the enclosing binding", src: `x = 1; f = { x + it }; f.call(1)`, want: "2"},
		{name: "a closure captures by reference", src: `x = 1; f = { x }; x = 9; f.call(0)`, want: "9"},
		{name: "a parameter shadows an outer name", src: `x = 1; f = { (x) -> x }; f.call(7)`, want: "7"},
		{name: "top-level fn declarations are hoisted", src: `f(1, 2); fn f(a, b) { a + b }; f(2, 3)`, want: "5"},
		{name: "a global is a separate namespace", src: `sent = "local"; $sent = "global"; sent`, want: "local"},
		{name: "a global never resolves through the chain", src: `sent = "local"; $sent`, want: ""},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err != "" {
				if got := evErr(t, in, tt.src, nil).Msg; !strings.Contains(got, tt.err) {
					t.Errorf("%s error = %q, want it to contain %q", tt.src, got, tt.err)
				}
				return
			}
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestArithmetic is the table of §8.3.
func TestArithmetic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"int addition", `2 + 3`, "5"},
		{"int division truncates toward zero", `7 / 2`, "3"},
		{"negative int division truncates toward zero", `(0 - 7) / 2`, "-3"},
		{"modulo takes the sign of the divisor", `-7 % 3`, "2"},
		{"modulo of a negative divisor", `7 % -3`, "-2"},
		{"overflow promotes to float", `9223372036854775807 + 1`, "9223372036854776000.0"},
		{"a float operand makes it float arithmetic", `1 + 2.0`, "3.0"},
		{"float division by zero is IEEE", `1.0 / 0.0`, "Infinity"},
		{"int power stays int", `2 ** 10`, "1024"},
		{"a negative exponent gives a float", `2 ** -1`, "0.5"},
		{"power is right associative", `2 ** 3 ** 2`, "512"},
		{"unary minus", `-(2 ** 2)`, "-4"},
		{"string concatenation", `"a" + "b"`, "ab"},
		{"array concatenation", `([1, 2] + [3]).json`, "[1,2,3]"},
		{"dict merge, right side wins", `({a: 1, b: 1} + {b: 2}).json`, `{"a":1,"b":2}`},
		{"string repetition", `"ab" * 3`, "ababab"},
		{"array repetition", `([1] * 3).json`, "[1,1,1]"},
		{"format with an array", `"%s-%d" % ["a", 1]`, "a-1"},
		{"format with one value", `"%.2f" % 3.14159`, "3.14"},
		{"array difference keeps order", `([3, 1, 2, 1] - [1]).json`, "[3,2]"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestNoCoercionInArithmetic is §9.1 seen from the evaluator: the coercion mode of the
// earlier draft is gone, so every mixed operand pair is an error naming both kinds and
// the fix is an explicit `.int`.
func TestNoCoercionInArithmetic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind string
		msg  string
	}{
		{"string plus int", `"2" + 1`, ErrKindType, "cannot add int to string"},
		{"int plus string", `1 + "2"`, ErrKindType, "cannot add"},
		{"string compared with int", `x = "3"; x >= 2`, ErrKindType, "cannot compare string with int"},
		{"nil compared with int", `$n >= 2`, ErrKindType, "cannot compare nil with int"},
		{"negating a string", `-"a"`, ErrKindType, "cannot negate string"},
		{"int division by zero", `1 / 0`, ErrKindZeroDiv, "divided by 0"},
		{"modulo by zero", `1 % 0`, ErrKindZeroDiv, "divided by 0"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q, want %q", tt.src, e.Kind, tt.kind)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}

	// The documented fix always works and never raises (§9.1, §12.7).
	for _, src := range []string{`"2".int + 1`, `"".int + 1200`, `"abc".int + 1`, `"3".int >= 2`} {
		t.Run(src, func(t *testing.T) {
			if _, err := in.Eval(context.Background(), src, nil); err != nil {
				t.Errorf("Eval(%s): %v", src, err)
			}
		})
	}
}

// TestEqualityAndOrdering covers §7.4 and §7.5 as the evaluator sees them: no coercion,
// no error for a cross-kind `==`, and an error for a cross-kind `<`.
func TestEqualityAndOrdering(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"int equals float", `1 == 1.0`, "true"},
		{"int does not equal its text", `1 == "1"`, "false"},
		{"nil equals nil", `nil == nil`, "true"},
		{"nil equals nothing else", `nil == false`, "false"},
		{"strings are byte exact", `"Да" == "да"`, "false"},
		{"arrays compare deeply", `[1, [2]] == [1, [2]]`, "true"},
		{"array order matters", `[1, 2] == [2, 1]`, "false"},
		{"dict insertion order does not matter", `{a: 1, b: 2} == {b: 2, a: 1}`, "true"},
		{"functions compare by identity", `f = { it }; g = { it }; f == f && !(f == g)`, "true"},
		{"spaceship orders numbers", `5 <=> 3`, "1"},
		{"spaceship orders strings", `"a" <=> "b"`, "-1"},
		{"spaceship of incomparable operands is nil", `"a" <=> 1`, ""},
		{"bools order false first", `false < true`, "true"},
		{"arrays order element-wise", `[1, 2] < [1, 3]`, "true"},
		{"not equal is the negation", `1 != 2`, "true"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestMatchOperators is §8.4. `~` is the operator ~136 of the 272 production conditions
// use, and D5 makes it a Bool: the index moved to `.index`.
func TestMatchOperators(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"match is a bool, not an index", `"привет" ~ /привет/`, "true"},
		{"a match at index 0 is truthy", `("привет" ~ /привет/) == true`, "true"},
		{"no match is false", `"что?" ~ /эхо|echo/i`, "false"},
		{"the regex may be on the left", `/вет/ ~ "привет"`, "true"},
		{"negated match", `"да" !~ /отмена/i`, "true"},
		{"negated match of a hit", `"да" !~ /да/`, "false"},
		{"nil never matches", `nil ~ /x/`, "false"},
		{"nil never matches, negated", `nil !~ /x/`, "true"},
		{"index is the rune index", `"привет".index(/вет/)`, "3"},
		{"index of no match is nil", `"привет".index(/zzz/)`, ""},
		{"captures returns the whole match first", `"a-b".captures(/(\w)-(\w)/).json`, `["a-b","a","b"]`},
		{"captures of no match is nil", `"abc".captures(/z/)`, ""},
		// §8.4: "named groups are also reachable as m["name"]".
		{"named groups are reachable by name", `"abc".captures(/(?<g>b)/)["g"]`, "b"},
		{"matches returns every match", `"a1b2".matches(/\d/).json`, `["1","2"]`},
		{"matches with groups returns group arrays", `"a-b".matches(/(\w)-(\w)/).json`, `[["a","b"]]`},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestInOperator is §8.5's membership operator. It is the `in` of a `match` arm written
// infix, so it asks the same question the same way — by dispatching `has` — and every
// kind that answers one answers the other (I6).
func TestInOperator(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"an int inside a range", `5 in 1..20`, "true"},
		{"an int outside a range", `99 in 1..20`, "false"},
		{"an exclusive range excludes its end", `20 in 1..<20`, "false"},
		{"an element of an array", `2 in [1, 2, 3]`, "true"},
		{"a missing element of an array", `9 in [1, 2, 3]`, "false"},
		{"a key of a dict", `"k" in {k: 1}`, "true"},
		{"a value is not a key", `1 in {k: 1}`, "false"},
		{"a substring of a string", `"вет" in "привет"`, "true"},
		{"in is a condition", `a = 5; if a in 1..20 { "yes" } else { "no" }`, "yes"},
		{"in is an ordinary value", `ok = 5 in 1..20; ok`, "true"},
		{"in inside a closure", `[1, 5, 9].filter { it in 2..8 }.json`, "[5]"},
		{"the result is a Bool, not the receiver", `("вет" in "привет") == true`, "true"},
		// §5.1: the range is the operand of `in`, and `&&` is looser than both.
		{"the range binds tighter than in", `5 in 1..20 && 5 > 3`, "true"},
		{"a user function may answer in", `fn has(box, x) { box == x }; 5 in 5`, "true"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestInOperatorErrors: a right operand with no members gets the operator's own message
// rather than the `undefined method 'has'` the dispatch underneath it would produce
// (§5.6) — the source never wrote `has`.
func TestInOperatorErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
	}{
		{"an int has no members", `1 in 5`, "the right side of 'in' must have members"},
		{"nil has no members", `1 in nil`, "the right side of 'in' must have members"},
		{"the kind is named", `1 in 5`, "got int"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != ErrKindType {
				t.Errorf("%s kind = %q, want %q", tt.src, e.Kind, ErrKindType)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestNamedArguments is §8.7's parameter binding. A name reaches the parameter it spells
// wherever the call goes — a plain `fn`, a closure, a UFCS method call, a module member
// and an `async fn` are all the same binding code.
func TestNamedArguments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a name skips a defaulted parameter", `fn f(a, b = 2, c = 3) { "${a}${b}${c}" }; f(1, c = 5)`, "125"},
		{"names may be given in any order", `fn f(a, b, c) { "${a}${b}${c}" }; f(c = 3, a = 1, b = 2)`, "123"},
		{"a name beats a default", `fn f(a = 1) { a }; f(a = 9)`, "9"},
		{"every argument may be named", `fn f(a, b) { a - b }; f(b = 1, a = 9)`, "8"},
		{"a default sees a parameter a name filled", `fn f(a = 1, b = a * 2) { b }; f(a = 5)`, "10"},
		// A default is evaluated at each call, not once when the declaration is read, so
		// a collection default cannot accumulate across calls (§8.7).
		{"a default is fresh on every call", `fn f(xs = []) { xs.push(1); xs.len }; f(); f(); f()`, "1"},
		{"a rest function still binds its own names", `fn f(a, b = 2, *rest) { "${a}${b}${rest.len}" }; f(1, b = 9)`, "190"},
		{"a closure binds by name too", `g = { (x, y) -> x - y }; g(y = 1, x = 9)`, "8"},
		{"an anonymous fn binds by name", `g = fn(x, y = 2) { x * y }; g(x = 5)`, "10"},
		{"ufcs passes the receiver and keeps the name", `fn area(w, h = 2) { w * h }; 3.area(h = 4)`, "12"},
		{"an async fn binds by name", `async fn f(a, b = 2) { a + b }; f(1, b = 40).await`, "41"},
		{"the value is an ordinary expression", `fn f(a) { a }; n = 2; f(a = n * 3)`, "6"},
		// §8.7: `f(x = 5)` names a parameter, so an assignment in argument position is
		// written with its own parentheses.
		{"a parenthesised assignment is still an assignment", `fn f(a) { a }; x = 0; f((x = 5)) + x`, "10"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestNamedArgumentErrors covers the four mistakes only the callee's parameter list can
// catch. The parser owns the other two — a repeated name and a positional argument after
// a named one — because the call site alone decides those (§5.6).
func TestNamedArgumentErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
	}{
		{"no such parameter", `fn f(a, b = 2) { a }; f(1, z = 3)`, "f has no parameter named 'z'"},
		{"the hint lists the parameters", `fn f(a, b = 2) { a }; f(1, z = 3)`, "it takes 'a' and 'b'"},
		{"a parameter given twice", `fn f(a, b = 2) { a }; f(1, a = 3)`, "got two values for parameter 'a'"},
		{"a parameter left without a value", `fn f(a, b) { a }; f(b = 3)`, "missing a value for parameter 'a'"},
		{"a rest parameter is not nameable", `fn f(a, *rest) { a }; f(1, rest = 3)`, "cannot be given by name"},
		{"a builtin takes no names", `print(len = "x")`, "takes its arguments by position"},
		{"a stdlib method takes no names", `[1, 2].map(f = { it })`, "takes its arguments by position"},
		// `defined` takes a name rather than a value (§12.1), so it owns its own message.
		{"defined takes a name", `defined(x = 1)`, "takes a name to test, not a named argument"},
		// A stdlib row is reached through its first argument's kind, so a call that gives
		// only names has no receiver to dispatch on. The compile pass says so, rather than
		// letting it fall through to `undefined function 'filter'` (§6.3).
		{"a named-only call to a stdlib row", `filter(xs = [1])`, "filter takes its arguments by position"},
		{"a named-only call names the argument", `filter(xs = [1])`, "'xs = …' has no parameter to bind"},
		// The hint lists what the callee answers to, so it has to survive a parameter
		// list with nothing in it and one whose last entry is `*rest`.
		{"no parameters, so no hint", `fn f() { 1 }; f(x = 1)`, "f has no parameter named 'x'"},
		{"a rest parameter appears in the hint", `fn f(a, *rest) { a }; f(1, z = 2)`, "it takes 'a' and '*rest'"},
		// §12.8: a module's own `fn` binds by name, but a stdlib row reached through the
		// module answers by position like every other row.
		{"a module's stdlib row takes no names", `include json; json.len(x = 1)`, "json.len takes its arguments by position"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != ErrKindArgument {
				t.Errorf("%s kind = %q, want %q", tt.src, e.Kind, ErrKindArgument)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestNamedArgumentsEvaluateInOrder: positions before names is the source order, since a
// positional argument may not follow a named one (§8.7).
func TestNamedArgumentsEvaluateInOrder(t *testing.T) {
	var sb strings.Builder
	in := New(Options{Stdout: &sb})
	const src = `fn f(a, b, c) { 0 }; f(println("1"), c = println("3"), b = println("2"))`
	if _, err := in.Eval(context.Background(), src, nil); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := sb.String(); got != "1\n3\n2\n" {
		t.Errorf("side effects ran in order %q, want %q", got, "1\n3\n2\n")
	}
}

// TestMatchOperatorTypeErrors pins the other half of §8.4: `~` does not coerce, and `==`
// against a regex literal is rejected at compile time (D5, §6.3).
func TestMatchOperatorTypeErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind string
		msg  string
	}{
		{"a number is not a subject", `1 ~ /1/`, ErrKindType, "cannot match against int"},
		{"equality against a regex literal", `s = "x"; s == /re/`, ErrKindSyntax,
			"'==' with a regex operand: use '~' to match"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q, want %q", tt.src, e.Kind, tt.kind)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestLogicalOperators is §8.5. `&&` and `||` return an operand rather than a Bool, and
// `??` fires on nil only — `false ?? x` is `false` while `false || x` is `x`.
func TestLogicalOperators(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"and returns the falsy left operand", `nil && "b"`, ""},
		{"and returns the right operand", `"a" && "b"`, "b"},
		{"or returns the truthy left operand", `"a" || "b"`, "a"},
		{"or returns the right operand", `false || "b"`, "b"},
		{"or sees zero as truthy", `0 || "b"`, "0"},
		{"coalesce fires on nil", `nil ?? "b"`, "b"},
		{"coalesce does not fire on false", `false ?? "b"`, "false"},
		{"coalesce is left associative", `nil ?? false ?? "c"`, "false"},
		{"bang always returns a bool", `!0`, "false"},
		{"bang of nil", `!nil`, "true"},
		{"bang binds tighter than equality", `x = 1; y = 1; !x == y`, "false"},
		{"or-assign fires on falsy", `a = false; a ||= 7; a`, "7"},
		{"and-assign fires on truthy", `a = 1; a &&= 7; a`, "7"},
		{"and-assign skips a falsy target", `a = nil; a &&= 7; a`, ""},
		{"coalesce-assign fires on nil only", `a = false; a ??= 7; a`, "false"},
		{"coalesce-assign on an undefined local", `b ??= 7; b`, "7"},
		{"or-assign on an undefined local", `c ||= 7; c`, "7"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestShortCircuitDoesNotEvaluate pins that the skipped side is never touched: the
// counter is a global so the skipped write would be visible if it happened.
func TestShortCircuitDoesNotEvaluate(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"and skips the right side", `$n = 0; false && ($n = 1); $n.str`, "0"},
		{"or skips the right side", `$n = 0; true || ($n = 1); $n.str`, "0"},
		{"coalesce skips the right side", `$n = 0; false ?? ($n = 1); $n.str`, "0"},
		{"a ternary evaluates one arm", `$n = 0; true ? 1 : ($n = 1); $n.str`, "0"},
		{"or-assign skips the right side", `$n = 0; a = 1; a ||= ($n = 1); $n.str`, "0"},
		{"safe navigation skips the arguments", `$n = 0; nil?.get($n = 1); $n.str`, "0"},
		// §8.7: the whole postfix chain is skipped, so a trailer further along it
		// never evaluates its arguments either.
		{"safe navigation skips the arguments further along the chain",
			`$n = 0; x = nil; x?.get("a").get($n = 1); $n.str`, "0"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q; the skipped side must not run", tt.src, got, tt.want)
			}
		})
	}
}

// TestRanges is §8.6: a lazy, Array-like iterable over Int endpoints.
func TestRanges(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"inclusive", `(1..3).array.json`, "[1,2,3]"},
		{"exclusive", `(1..<4).array.json`, "[1,2,3]"},
		{"a descending range is empty", `(5..1).array.json`, "[]"},
		{"len", `(0..6).len`, "7"},
		{"has", `(1..10).has(5)`, "true"},
		{"sum", `(1..100).sum`, "5050"},
		{"map", `(0..2).map { it * 2 }.json`, "[0,2,4]"},
		{"filter", `(1..10).filter { it % 3 == 0 }.json`, "[3,6,9]"},
		{"step", `(0..4).step(2).array.json`, "[0,2,4]"},
		{"each_slice", `(0..6).map { it }.each_slice(2).array.json`, "[[0,1],[2,3],[4,5],[6]]"},
		{"reverse", `(1..3).reverse.array.json`, "[3,2,1]"},
		{"reduce", `(1..3).reduce(0) { (acc, x) -> acc + x }`, "6"},
		{"first and last", `(1..9).first.str + "-" + (1..9).last.str`, "1-9"},
		{"a range reports its own type", `type(1..2)`, "range"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	// Materialising more than MaxCollection is a limit error, not an OOM (§8.6).
	small := New(Options{MaxCollection: 16})
	if _, err := small.Eval(context.Background(), `(1..1000).array`, nil); !errors.Is(err, ErrBudget) {
		t.Errorf("materialising an oversized range: %v, want a limit error", err)
	}
}

// TestCalls is §8.7: strict left-to-right argument evaluation, UFCS dispatch, `?.`
// short-circuiting the whole chain, and calling a non-function.
func TestCalls(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a declared function", `fn add(a, b) { a + b }; add(1, 2)`, "3"},
		{"ufcs on a user function", `fn shout(s) { s.upper + "!" }; "да".shout`, "ДА!"},
		{"ufcs passes the receiver first", `fn pair(a, b) { a + "/" + b }; "l".pair("r")`, "l/r"},
		{"a stdlib method wins over a user function", `fn len(x) { "mine" }; "abc".len`, "3"},
		{"rest parameters", `fn f(a, *rest) { rest.json }; f(1, 2, 3)`, "[2,3]"},
		{"default parameters", `fn f(a, b = 2) { a + b }; f(1)`, "3"},
		{"a named argument binds its parameter", `fn f(a, b = 2, c = 3) { "${a}${b}${c}" }; f(1, c = 5)`, "125"},
		{"safe navigation on nil is nil", `nil?.lower`, ""},
		// §8.7: "if recv is nil the whole postfix chain is nil" — every trailer after
		// the `?.`, not only the one it introduces.
		{"safe navigation short-circuits the chain", `x = nil; x?.lower.trim.len`, ""},
		{"safe navigation on a value still calls", `"A"?.lower`, "a"},
		{"a trailing closure is the last argument", `[1, 2].map { it * 2 }.json`, "[2,4]"},
		{"a function value passed by name", `double = { it * 2 }; [1, 2].map(double).json`, "[2,4]"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestArgumentsEvaluateLeftToRight pins the order §8.7 fixes, including the trailing
// closure, which is constructed in argument position and therefore last.
func TestArgumentsEvaluateLeftToRight(t *testing.T) {
	var sb strings.Builder
	in := New(Options{Stdout: &sb})
	const src = `fn f(a, b, c) { 0 }; f(println("1"), println("2"), println("3"))`
	if _, err := in.Eval(context.Background(), src, nil); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := sb.String(); got != "1\n2\n3\n" {
		t.Errorf("side effects ran in order %q, want %q", got, "1\n2\n3\n")
	}
}

// TestCallErrors is the third branch of the §8.7 dispatch table plus the non-function
// call. A dict never dispatches `.` to its own keys, which is what keeps UFCS
// unambiguous.
func TestCallErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind string
		msg  string
	}{
		{"calling a number", `5(1)`, ErrKindType, "not a function: int"},
		{"an unknown method", `"a".нетакого`, ErrKindName, "undefined method"},
		{"a dict key is not a method", `d = {a: 1}; d.a`, ErrKindName, "undefined method 'a'"},
		{"an unresolved bare identifier", `нетакой`, ErrKindName, "undefined variable"},
		{"too many arguments", `fn f(a) { a }; f(1, 2, 3)`, ErrKindArgument, "argument"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q, want %q", tt.src, e.Kind, tt.kind)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestIndexing is the table of §8.8. Indexing nil is an error on purpose: a silently-nil
// chain hides the real failure, and `.dig` is the nil-safe path.
func TestIndexing(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a rune of a string", `"привет"[1]`, "р"},
		{"a negative string index", `"привет"[-1]`, "т"},
		{"a string index out of range is nil", `"abc"[9]`, ""},
		{"a substring by rune count", `"привет"[1, 3]`, "рив"},
		{"an array element", `[1, 2, 3][1]`, "2"},
		{"a negative array index", `[1, 2, 3][-1]`, "3"},
		{"an array index out of range is nil", `[1, 2, 3][9]`, ""},
		{"a sub-array", `[1, 2, 3][0, 2].json`, "[1,2]"},
		{"a dict value", `{a: 1}["a"]`, "1"},
		{"a missing dict key is nil", `{a: 1}["b"]`, ""},
		{"array assignment", `x = [1]; x[0] = 9; x.json`, "[9]"},
		{"array assignment extends with nils", `x = [1]; x[3] = 9; x.json`, "[1,null,null,9]"},
		{"dict assignment appends at the end", `d = {a: 1}; d["b"] = 2; d.json`, `{"a":1,"b":2}`},
		{"compound assignment evaluates the target once",
			`$n = 0; fn k() { $n = $n + 1; 0 }; x = [1]; x[k()] += 10; x.json + "/" + $n.str`, "[11]/1"},
		{"dig walks a nil-safe path", "include json\n" + `json.parse("{\"a\":{\"b\":1}}").dig("a", "b")`, "1"},
		{"dig off the end is nil", "include json\n" + `json.parse("[]").dig(0, "b")`, ""},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestIndexingErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
	}{
		{"indexing nil", `x = nil; x[0]`, "cannot index nil"},
		{"assigning into nil", `x = nil; x[0] = 1`, "cannot assign to an index of nil"},
		{"a string is immutable", `x = "abc"; x[0] = "z"`, "cannot assign to an index of string"},
		{"indexing a regex", `/re/[0]`, "cannot index regex"},
		{"indexing a function", `fn f() { 1 }; f[0]`, "cannot index function"},
		{"a dict has no two-argument form", `{a: 1}["a", 2]`, "dict"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evErr(t, in, tt.src, nil).Msg; !strings.Contains(got, tt.msg) {
				t.Errorf("%s message = %q, want it to contain %q", tt.src, got, tt.msg)
			}
		})
	}
}

// TestClosuresAndIt is §8.9: a closure with no parameter list declares `it`, and an
// explicit parameter shadows it.
func TestClosuresAndIt(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"implicit it", `[1, 2, 3].map { it * 2 }.json`, "[2,4,6]"},
		{"an explicit parameter means the same", `[1, 2, 3].map { (x) -> x * 2 }.json`, "[2,4,6]"},
		{"the two forms agree", `[1, 2, 3].map { it * 2 } == [1, 2, 3].map { (x) -> x * 2 }`, "true"},
		{"an explicit parameter shadows it", `[1, 2].map { (it) -> it + 1 }.json`, "[2,3]"},
		{"two parameters", `[1, 2, 3].reduce(0) { (acc, x) -> acc + x }`, "6"},
		{"a dict block takes key and value", `{a: 1, b: 2}.map { (k, v) -> "${k}=${v}" }.join("&")`, "a=1&b=2"},
		{"a closure is a value", `f = { it + 1 }; f.call(1)`, "2"},
		{"arity of an implicit closure", `f = { it }; f.arity`, "1"},
		{"a closure closes over a local", `fn adder(n) { { it + n } }; adder(10).call(1)`, "11"},
		{"it is not visible outside a closure", `[1].map { it }; 1`, "1"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	if got := evErr(t, in, `it`, nil).Msg; !strings.Contains(got, "undefined variable 'it'") {
		t.Errorf("bare `it` = %q, want an undefined-variable error", got)
	}
}

// TestAnonymousFn is §4.1 and §7.7 on the nameless `fn`: it is a value, and it is a
// function rather than a closure in the two ways a program can tell — its arity is
// checked, and a `return` inside it returns from it.
func TestAnonymousFn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a value with parameters", `f = fn(a, b) { a + b }; f(2, 3)`, "5"},
		{"called where it stands", `fn(x) { x * 3 }(5)`, "15"},
		{"passed to a library function", `[1, 2, 3].map(fn(x) { x * 2 }).json`, "[2,4,6]"},
		{"return leaves it", `f = fn(x) { if x > 0 { return "+" }; "-" }; f(1) + f(-1)`, "+-"},
		{"it closes over a local", `n = 10; f = fn(x) { x + n }; f(1)`, "11"},
		{"it is a value in a dict", `ops = {add: fn(a, b) { a + b }}; ops["add"](1, 2)`, "3"},
		{"nested", `mk = fn(n) { fn(x) { x * n } }; mk(3)(5)`, "15"},
		{"arity", `fn(a, b) { a }.arity`, "2"},
		{"it has no name", `fn(a) { a }.str`, "#<fn>"},
		// §4.1: the arrow form is the same function with the keyword left out, so every
		// row above holds for it too.
		{"the arrow form", `f = (a, b) -> { a + b }; f(2, 3)`, "5"},
		{"the arrow form takes no parameters", `f = () -> { 42 }; f()`, "42"},
		{"the arrow form is called where it stands", `(x) -> { x * 3 }(5)`, "15"},
		{"the arrow form returns from itself",
			`f = (x) -> { if x > 0 { return "+" }; "-" }; f(1) + f(-1)`, "+-"},
		{"the two forms are the same function", `((x) -> { x + 1 })(1) == fn(x) { x + 1 }(1)`, "true"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	// A closure is lenient about its arguments because a library decides how many to
	// pass (§7.7); a `fn` is an interface and is checked, named or not.
	if got := evErr(t, in, `f = fn(a, b) { a }; f(1)`, nil).Msg; !strings.Contains(got, "expects 2 argument") {
		t.Errorf("anonymous fn arity = %q, want the argument-count error", got)
	}
	// Nothing is declared: the value is the only way to reach it.
	if got := evErr(t, in, "fn(a) { a }\nf(1)", nil).Msg; !strings.Contains(got, "undefined function 'f'") {
		t.Errorf("anonymous fn binding = %q, want it to bind nothing", got)
	}
	// …so a statement that only writes one throws it away, which is §17's warning — the
	// same one a closure literal in that position has always had.
	discarded := []struct{ src, want string }{
		{"fn(a) { a }\n1", "anonymous 'fn' in statement position"},
		{"async fn() { 1 }\n1", "anonymous 'fn' in statement position"},
		{"{ it }\n1", "closure literal in statement position"},
	}
	for _, tt := range discarded {
		prog, err := in.Compile("t", tt.src)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.src, err)
		}
		warns := prog.Warnings()
		if len(warns) != 1 || !strings.Contains(warns[0].Msg, tt.want) {
			t.Errorf("Compile(%q) warnings = %v, want one saying %q", tt.src, warns, tt.want)
		}
	}
	// The last statement of a block is its value, so the same literal there is received.
	if prog, err := in.Compile("t", "1\nfn(a) { a }"); err != nil {
		t.Fatalf("Compile: %v", err)
	} else if w := prog.Warnings(); len(w) != 0 {
		t.Errorf("warnings on a final anonymous fn = %v, want none", w)
	}
}

// TestExit is §12.1's `exit`: the program says it is done and names a status. It is not
// a failure, so nothing catches it and nothing after it runs — and it never touches the
// process, because a Run inside a bot has no business ending one.
func TestExit(t *testing.T) {
	in := evInterp()

	tests := []struct {
		name string
		src  string
		code int
	}{
		{"a status of its own", `exit(3)`, 3},
		{"no argument is zero", `exit()`, 0},
		{"nothing after it runs", `println("a"); exit(1); raise("never")`, 1},
		{"from inside a function", `fn f() { exit(2) }; f(); 9`, 2},
		{"from inside a closure", `[1, 2].each { exit(4) }`, 4},
		{"try does not catch it", `try exit(5) else "caught"`, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := in.Eval(context.Background(), tt.src, nil)
			code, ok := ExitCode(err)
			if !ok {
				t.Fatalf("Eval(%s) = %s, %v; want an exit", tt.src, v.Inspect(), err)
			}
			if code != tt.code {
				t.Errorf("Eval(%s) exit code = %d; want %d", tt.src, code, tt.code)
			}
		})
	}

	// A host function may end the Run the same way, by returning an error that wraps the
	// sentinel. It named no status, so the status is 0.
	hosted := New(Options{})
	hosted.Register("stop", 0, func(c *Ctx, args []Value) (Value, error) {
		return Nil(), fmt.Errorf("the host is done: %w", ErrExit)
	})
	_, err := hosted.Eval(context.Background(), `try stop() else "caught"`, nil)
	if code, ok := ExitCode(err); !ok || code != 0 {
		t.Errorf("a host ErrExit = %d, %v (%v); want an uncatchable exit with status 0", code, ok, err)
	}

	// ExitCode answers for an exit and for nothing else, which is what lets a host tell
	// "the program chose to stop" from "the program broke".
	if _, err := in.Eval(context.Background(), `raise("boom")`, nil); err == nil {
		t.Error("raise returned no error")
	} else if _, ok := ExitCode(err); ok {
		t.Error("ExitCode claimed a raise was an exit")
	}
	if _, ok := ExitCode(nil); ok {
		t.Error("ExitCode claimed a nil error was an exit")
	}

	// The code is a status, so it is bounded and it is an integer.
	for _, src := range []string{`exit(256)`, `exit(-1)`, `exit("x")`} {
		err := evErr(t, in, src, nil)
		if _, ok := ExitCode(err); ok {
			t.Errorf("Eval(%s) exited; want the argument refused", src)
		}
		if err.Kind != ErrKindArgument && err.Kind != ErrKindType {
			t.Errorf("Eval(%s) kind = %q; want argument or type", src, err.Kind)
		}
	}
}

// TestReturnBreakNext is §8.10. All three are sentinel values on the eval chain, never
// Go panics, so `break` out of a closure ends the call the closure was passed to.
func TestReturnBreakNext(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"return leaves the named function", `fn f() { return 1; 2 }; f()`, "1"},
		{"return from inside a loop", `fn f() { while true { return 3 } }; f()`, "3"},
		{"return from inside a closure leaves the function",
			`fn f() { [1, 2, 3].each { return 9 }; 0 }; f()`, "9"},
		{"break ends a loop with a value", `while true { break 7 }`, "7"},
		{"break ends the call a closure was passed to", `[1, 2, 3].each { break 1 }`, "1"},
		{"break with no value", `i = 0; while true { i += 1; break }; i`, "1"},
		{"next ends one closure invocation", `[1, 2, 3].map { next it * 2 }.json`, "[2,4,6]"},
		{"next ends one loop iteration",
			`s = 0; i = 0; while i < 5 { i += 1; next if i % 2 == 0; s += i }; s`, "9"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestTryElse is §8.11. `try X else Y` is the whole error-handling story; the closure
// form binds a dict describing the failure.
func TestTryElse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"the happy path is the value of X", `try 1 else 2`, "1"},
		{"a raise is caught", `try raise("boom") else "-"`, "-"},
		{"a runtime error is caught", `try (1 / 0) else "-"`, "-"},
		{"the closure form binds a message", `try (1 / 0) else (e) -> e["message"]`, "divided by 0"},
		{"the closure form binds a kind", `try raise("boom") else (e) -> e["kind"]`, "raise"},
		{"the closure form binds a line", `try (1 / 0) else (e) -> e["line"]`, "1"},
		{"raise carries a dict payload", `try raise({code: 5}) else (e) -> e["data"]["code"]`, "5"},
		{"a group guards several statements", `try (x = 1; raise("e"); x) else "-"`, "-"},
		{"try is right associative", `try (try raise("a") else raise("b")) else "outer"`, "outer"},
		{"a caught error does not poison what follows", `v = try (1 / 0) else 0; v + 1`, "1"},

		// The braced form (§8.11). It is the same evaluation with the same errors; what
		// the braces add is a statement list and a scope.
		{"a block guards several statements", `try { x = 1; raise("e"); x } else { "-" }`, "-"},
		{"a block's value is its last statement", `try { 1; 2 } else 0`, "2"},
		{"a block binds the error without an arrow", `try { 1 / 0 } else (e) { e["message"] }`, "divided by 0"},
		{"a block binds the error with one", `try { 1 / 0 } else (e) -> { e["kind"] }`, "zero-division"},
		{"a dict in operand position is still a dict", `try {a: 1} else 0`, `{"a":1}`},
		{"the binder ends at the brace, dict or not", `try raise("x") else (e) {m: e["message"]}`, `{"m":"x"}`},
		{"the same fallback with the arrow", `try raise("x") else (e) -> {m: e["message"]}`, `{"m":"x"}`},
		{"the empty dict is still the empty dict", `try {} else 0`, "{}"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestTryEnsure is the release half of §8.11. An `ensure` runs on every way out of the
// body that leaves the Run alive, it never changes the value, and it never catches: a
// `try … ensure` with no `else` releases and lets the error go on unwinding.
func TestTryEnsure(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"it runs on the happy path",
			`$log = ""; try { 1 } ensure { $log += "r" }; $log`, "r"},
		{"the value is the body's, not the ensure's",
			`try { 1 } ensure { 99 }`, "1"},
		{"it runs before the error is handed on",
			`$log = ""; try (try { raise("x") } ensure { $log += "r" }) else "-"; $log`, "r"},
		{"it runs after the else that caught",
			`$log = ""; try { raise("x") } else { $log += "e" } ensure { $log += "r" }; $log`, "er"},
		{"it runs on a return out of the body",
			`$log = ""; fn f() { try { return 1 } ensure { $log += "r" } }; f(); $log`, "r"},
		{"it runs on a break out of the body",
			`$log = ""; for i in 1..3 { try { break } ensure { $log += "r" } }; $log`, "r"},
		{"a return out of the body is still a return",
			`fn f() { try { return "early" } ensure { 0 }; "late" }; f()`, "early"},
		{"the ensure's own failure replaces what was pending",
			`try (try { raise("first") } ensure { raise("second") }) else (e) -> e["message"]`, "second"},
		{"so does a control signal of its own",
			`for i in 1..5 { try { i } ensure { break "stopped at ${i}" } }`, "stopped at 1"},
		{"a return from the ensure wins too",
			`fn f() { try { "body" } ensure { return "released" }; "after" }; f()`, "released"},
		{"an else and an ensure are independent clauses",
			`$log = ""; v = try { 1 } else { 2 } ensure { $log += "r" }; "${v}${$log}"`, "1r"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, evInterp(), tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	// With no `else`, the error is not caught: the ensure runs and the error still
	// reaches the host.
	e := evErr(t, evInterp(), `try { raise("boom") } ensure { 1 }`, nil)
	if e.Msg != "boom" {
		t.Errorf("error = %q, want the body's error to survive the ensure", e.Msg)
	}
}

// TestEnsureDoesNotOutliveALimit is §14.1 read from the `ensure` side: what ends the Run
// ends it, and script code does not get one more turn. An `ensure` that ran here would be
// a way to spend time and steps past the point the host said stop.
func TestEnsureDoesNotOutliveALimit(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		src  string
		want error
	}{
		{"the step budget", Options{StepBudget: 5_000, Timeout: -1},
			`try { while true { } } ensure { $ran = true }`, ErrBudget},
		{"the deadline", Options{StepBudget: -1, Timeout: 30 * time.Millisecond},
			`try { while true { } } ensure { $ran = true }`, ErrTimeout},
		{"exit is not a failure and is not caught either", Options{},
			`try { exit(3) } ensure { $ran = true }`, ErrExit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(tt.opts)
			p, err := in.Compile("ensure", tt.src)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			res, err := in.RunResult(context.Background(), p, nil)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if v, ok := res.Globals["$ran"]; ok && v.Truthy() {
				t.Errorf("the ensure ran; a limit ends the Run (§14.1)")
			}
		})
	}
}

// TestTryDoesNotCatchLimits is the other half of §8.11: a timeout, a budget, a depth
// limit and a cancellation are unrecoverable and must reach the host.
func TestTryDoesNotCatchLimits(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		src  string
		want error
	}{
		{"the step budget", Options{StepBudget: 5_000, Timeout: -1},
			`try (while true { }) else "swallowed"`, ErrBudget},
		{"the deadline", Options{StepBudget: -1, Timeout: 30 * time.Millisecond},
			`try (while true { }) else "swallowed"`, ErrTimeout},
		{"the depth limit", Options{MaxDepth: 32},
			`fn rec() { rec() }; try rec() else "swallowed"`, ErrDepth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(tt.opts)
			v, err := in.Eval(context.Background(), tt.src, nil)
			if !errors.Is(err, tt.want) {
				t.Fatalf("= (%s, %v), want %v", v.Inspect(), err, tt.want)
			}
		})
	}
}

// TestStringInterpolation is §8.12, the only place a conversion is implicit.
func TestStringInterpolation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a global by name", `"Ваш адрес $__sent?"`, "Ваш адрес Ленина 1?"},
		{"an unbound global renders empty", `"[$nope]"`, "[]"},
		{"a braced expression", `"${1 + 2}"`, "3"},
		{"a local needs braces", `addr = "Ленина 1"; "Ваш адрес ${addr}?"`, "Ваш адрес Ленина 1?"},
		{"nil renders empty", `"[${nil}]"`, "[]"},
		{"a bool", `"${true}"`, "true"},
		{"an int", `"${42}"`, "42"},
		{"a float keeps its point", `"${2.0}"`, "2.0"},
		{"a float round-trips", `"${1.5}"`, "1.5"},
		{"an array renders as json", `"${[1, 2]}"`, "[1,2]"},
		{"a dict renders as json", `"${{a: 1}}"`, `{"a":1}`},
		{"a regex renders as a literal", `"${/re/i}"`, "/re/i"},
		{"a function renders as a name", `fn g() { 1 }; "${g}"`, "#<fn g>"},
		{"single quotes do not interpolate", `'$__sent ${1}'`, `$__sent ${1}`},
	}

	in := evInterp()
	vars := map[string]Value{"$__sent": Str("Ленина 1")}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evOK(t, in, tt.src, vars).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestHeredoc is §3.7's third string form evaluated: one shape, `<<~TAG`, whose body is
// the lines below the tag with their common indentation shed.
func TestHeredoc(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"the body is the lines below", "<<~T\n  a\n  b\nT\n", "a\nb\n"},
		{"the common indent is shed", "<<~T\n    a\n      b\nT\n", "a\n  b\n"},
		{"a blank line has no say in the indent", "<<~T\n\n  a\nT\n", "\na\n"},
		{"the terminator may be indented with the body", "  <<~T\n    a\n    T\n", "a\n"},
		{"an empty body is the empty string", "<<~T\nT\n", ""},
		{"it interpolates like a double-quoted string", "n = 2\n<<~T\n  n=${n} $__sent\nT\n", "n=2 here\n"},
		{"it takes the same escapes", "<<~T\n  a\\tb\nT\n", "a\tb\n"},
		{"a quote is ordinary text", "<<~T\n  say \"hi\"\nT\n", "say \"hi\"\n"},
		{"a hash is ordinary text", "<<~T\n  # not a comment\nT\n", "# not a comment\n"},
		{"the raw form takes neither escapes nor interpolation", "<<~'T'\n  ${x} $y \\n\nT\n", "${x} $y \\n\n"},
		{"a trailer applies to the string", "<<~T.trim.upper\n  hi\nT\n", "HI"},
		{"the rest of the tag's line is read after the body",
			"[<<~T, \"z\"].join(\"|\")\n  a\nT\n", "a\n|z"},
		{"two on one line take their bodies in order",
			"[<<~A, <<~B].join(\"\")\n  one\nA\n  two\nB\n", "one\ntwo\n"},
		{"the statement after it is the next line of source",
			"x = <<~T\n  a\nT\ny = \"${x}!\"\ny", "a\n!"},
	}

	in := evInterp()
	vars := map[string]Value{"$__sent": Str("here")}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evOK(t, in, tt.src, vars).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestRecords is §7.8: a `record` names a shape over the dict mzs already has, and the
// label it adds answers three questions and takes nothing away.
func TestRecords(t *testing.T) {
	const decl = `record Money(amount, currency = "RUB")` + "\n"

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a field is read with a dot", `Money(1500, "USD").amount`, "1500"},
		{"a default fills the field it names", `Money(700).currency`, "RUB"},
		{"type names the shape", `type(Money(1))`, "Money"},
		{"it is still a dict", `Money(1).is("dict")`, "true"},
		{"is answers the shape too", `Money(1).is("Money")`, "true"},
		{"and answers false for a plain dict", `{amount: 1}.is("Money")`, "false"},
		{"the entries are the fields, in order", `Money(1500, "USD").json`, `{"amount":1500,"currency":"USD"}`},
		{"keys are the field names", `Money(1).keys.json`, `["amount","currency"]`},
		{"a field may be given by name", `Money(currency = "EUR", amount = 3).json`,
			`{"amount":3,"currency":"EUR"}`},
		{"an index reads a field as well", `Money(1)["amount"]`, "1"},
		{"a dict row still works", `Money(1).dig("amount")`, "1"},
		{"equality ignores the label", `Money(1, "USD") == {amount: 1, currency: "USD"}`, "true"},
		{"so does hash", `hash(Money(1, "USD")) == hash({amount: 1, currency: "USD"})`, "true"},
		{"a match arm asks the shape", `match Money(9) { Money -> "money"; else -> "no" }`, "money"},
		{"and does not fire on a plain dict",
			`d = {amount: 9, currency: "RUB"}; match d { Money -> "money"; else -> "no" }`, "no"},
		{"nor on anything else", `match 42 { Money -> "money"; else -> "no" }`, "no"},
		{"dup keeps the shape", `type(Money(1).dup)`, "Money"},
		{"merge keeps it too, which is the with-update",
			`m = Money(1500); type(m.merge({amount: 2000})) + ":" + m.merge({amount: 2000}).amount.str`,
			"Money:2000"},
		{"filter drops it, because the shape may no longer hold",
			`type(Money(1).filter { (k, _) -> k == "amount" })`, "dict"},
		{"a set writes a field in place", `m = Money(1); m["amount"] = 2; "${type(m)}:${m.amount}"`, "Money:2"},
		{"the constructor is an ordinary value", `f = Money; f(5).amount`, "5"},
		{"a record may be declared below its use", `first.amount`, "1"},
		{"a field may be destructured out", `a, c = [Money(1).amount, Money(1).currency]; "${a}${c}"`, "1RUB"},
	}

	prelude := decl + "first = Money(1)\n"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, evInterp(), prelude+tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestRecordDiagnostics pins what a shape refuses. Every one of them is the diagnostic
// the same mistake already had somewhere else: a record's constructor is bound by the
// parameter rules of §8.7, so there is no second set to learn.
func TestRecordDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind string
		msg  string
	}{
		{"a missing field is the arity error", `record M(a, b)` + "\n" + `M(1)`,
			ErrKindArgument, "M expects 2 argument(s), got 1"},
		{"a name that is not a field", `record M(a, b)` + "\n" + `M(1, c = 2)`,
			ErrKindArgument, "M has no parameter named 'c'; it takes 'a' and 'b'"},
		{"a misspelled field names the shape", `record M(amount)` + "\n" + `M(1).amont`,
			ErrKindName, "undefined method 'amont'; did you mean 'amount'?"},
		{"a field takes no arguments", `record M(amount)` + "\n" + `M(1).amount(2)`,
			ErrKindArgument, "'amount' is a field of M, so it takes no arguments, got 1"},
		{"an undeclared shape is not a type name", `record M(a)` + "\n" + `M(1).is("Nope")`,
			ErrKindArgument, `is: unknown type name "Nope"`},
		{"another shape's field is not this one's", "record A(alpha)\nrecord B(beta)\nA(1).beta",
			ErrKindName, "undefined method 'beta' for A"},
		{"and a near miss is still suggested", "record A(amount)\nrecord B(amounts)\nA(1).amounts",
			ErrKindName, "undefined method 'amounts' for A (did you mean 'amount'?)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, evInterp(), tt.src, nil)
			if e.Kind != tt.kind || e.Msg != tt.msg {
				t.Errorf("error = %s: %s, want %s: %s", e.Kind, e.Msg, tt.kind, tt.msg)
			}
		})
	}
}

// TestRecordFieldShadowsAMethod is the one collision a shape can have with the standard
// library, and §17's answer to it: the field wins on that shape — that is what naming a
// shape is for — the warning says so once, and the operation is still there under the
// prefix spelling UFCS gives every row (D18).
func TestRecordFieldShadowsAMethod(t *testing.T) {
	in := evInterp()
	src := "record Row(len, name)\nr = Row(3, \"x\")\n\"${r.len}:${len(r)}\""
	if got := evStr(t, in, src); got != "3:2" {
		t.Errorf("= %q, want %q", got, "3:2")
	}
	prog, err := in.Compile("t", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := "record field 'len' shadows the method of that name: on a Row, '.len' reads the field — write len(m) for the method"
	warns := prog.Warnings()
	if len(warns) != 1 || warns[0].Msg != want {
		t.Fatalf("warnings = %v, want one saying %q", warns, want)
	}
}

// TestMatchExpression is §5.2–§5.5 evaluated: the replacement for the if/else if ladder
// that ~136 of the 272 production conditions are written as.
func TestMatchExpression(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a literal pattern", `match "да" { "да" -> 1; else -> 0 }`, "1"},
		{"a regex pattern matches", `match "ну пока" { /пока/ -> "bye"; else -> "?" }`, "bye"},
		{"a regex pattern that misses", `match "x" { /y/ -> 1; else -> 2 }`, "2"},
		{"in over an array", `match "ага" { in ["да", "ага"] -> "yes"; else -> "no" }`, "yes"},
		{"in over a range", `match 5 { in 1..10 -> "in"; else -> "out" }`, "in"},
		{"in over a string is a substring test", `match "ло" { in "hello, лоси" -> "yes"; else -> "no" }`, "yes"},
		{"several patterns in one arm", `match 2 { 1, 2 -> "a"; else -> "b" }`, "a"},
		{"a guard narrows an arm", `n = 5; match n { in 1..9 if n > 4 -> "hi"; in 1..9 -> "lo" }`, "hi"},
		{"the first matching arm wins", `match 5 { in 1..10 -> "a"; 5 -> "b"; else -> "c" }`, "a"},
		{"no matching arm and no else is nil", `match 99 { 1 -> "a" }`, ""},
		{"an else arm", `match 99 { 1 -> "a"; else -> "z" }`, "z"},
		{"an arm body may be a closure", `match 1 { 1 -> { 7 } }`, "7"},
		{"with no subject every pattern is a condition",
			`s = "оператор"; match { s.len > 500 -> "long"; s ~ /оператор/ -> "handoff"; else -> "?" }`, "handoff"},
		{"a subjectless match falls through to else", `match { false -> 1; else -> 2 }`, "2"},
		{"on one line", `match 1 { 1 -> "a"; 2 -> "b"; else -> "c" }`, "a"},
		{"an expression pattern compares with ==", `n = 3; match 3 { n -> "eq"; else -> "ne" }`, "eq"},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestMatchSubjectEvaluatedOnce is the §5.5 guarantee that makes a match ladder cheaper
// than the if/else if chain it replaces.
func TestMatchSubjectEvaluatedOnce(t *testing.T) {
	var sb strings.Builder
	in := New(Options{Stdout: &sb})
	const src = `match println("once") { "a" -> 1; "b" -> 2; nil -> 3; else -> 4 }`
	v := evOK(t, in, src, nil)
	if v.Int() != 3 {
		t.Errorf("= %s, want 3", v.Inspect())
	}
	if got := sb.String(); got != "once\n" {
		t.Errorf("stdout = %q, want %q", got, "once\n")
	}
}

// TestDestructuring is §8.15: one shape rule, three spellings — the assignment, the
// `match` arm and the two-variable `for` — and a mismatch that raises instead of
// quietly filling in nil.
func TestDestructuring(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		err  string
	}{
		{name: "a pair by name", src: `pair = [1, 2]; a, b = pair; "${a}${b}"`, want: "12"},
		{name: "three names", src: `a, b, c = [1, 2, 3]; a + b + c`, want: "6"},
		{name: "brackets on the left", src: `[a, b] = [1, 2]; a + b`, want: "3"},
		{name: "nested, recursively", src: `[a, [b, c]] = [1, [2, 3]]; "${a}${b}${c}"`, want: "123"},
		{name: "a range has positions too", src: `a, b = 1..2; a + b`, want: "3"},
		{name: "the value is the right side", src: `x = [a, b] = [1, 2]; str(x)`, want: "[1,2]"},
		{name: "the right side is evaluated once",
			src: `n = 0; f = { n += 1; [1, 2] }; a, b = f.call(0); n`, want: "1"},
		{name: "a swap needs no temporary", src: `a = 1; b = 2; a, b = [b, a]; "${a}${b}"`, want: "21"},
		{name: "an index is a target", src: `d = {x: 0, y: 0}; d["x"], d["y"] = [1, 2]; d["x"] + d["y"]`, want: "3"},
		{name: "a target may write into the array being taken apart",
			src: `xs = [1, 2]; xs[1], xs[0] = xs; str(xs)`, want: "[2,1]"},
		{name: "a $var is a target", src: `$a, $b = [1, 2]; $a + $b`, want: "3"},
		{name: "= writes the outer binding", src: `a = 0; b = 0; if true { a, b = [1, 2] }; a + b`, want: "3"},
		{name: ":= shadows in the current scope", src: `a = 0; if true { a, b := [1, 2] }; a`, want: "0"},
		{name: "a name born in a destructure is a binding", src: `a, b = [1, 2]; a`, want: "1"},

		{name: "too many values raises", src: `a, b = [1, 2, 3]`,
			err: "destructuring expects 2 values, got 3"},
		{name: "too few values raises", src: `a, b = [1]`,
			err: "destructuring expects 2 values, got 1"},
		{name: "a nested mismatch raises", src: `[a, [b, c]] = [1, [2]]`,
			err: "destructuring expects 2 values, got 1"},
		{name: "a right side that is not an array raises", src: `a, b = 1`,
			err: "cannot destructure int: the right side must be an array"},
		{name: "a dict is not positional", src: `a, b = {x: 1, y: 2}`,
			err: "cannot destructure dict: the right side must be an array"},

		{name: "an array pattern binds in a match arm",
			src: `match [1, 2] { [x, y] -> x + y; else -> 0 }`, want: "3"},
		{name: "the length picks the arm",
			src: `match [1] { [x, y] -> "two"; [x] -> "one"; [] -> "none"; else -> "?" }`, want: "one"},
		{name: "an empty pattern fires on an empty array",
			src: `match [] { [] -> "none"; else -> "?" }`, want: "none"},
		{name: "a subject of the wrong kind does not fire",
			src: `match "ab" { [x, y] -> "pair"; else -> "no" }`, want: "no"},
		{name: "a literal element compares",
			src: `match [0, 5] { [0, n] -> n; else -> -1 }`, want: "5"},
		{name: "a literal array still compares element for element",
			src: `match [1, 2] { [1, 2] -> "eq"; else -> "no" }`, want: "eq"},
		{name: "a regex element matches",
			src: `match ["ok", 7] { [/^o/, n] -> n; else -> 0 }`, want: "7"},
		{name: "patterns nest in a match arm too",
			src: `match [1, [2, 3]] { [x, [y, z]] -> x + y + z; else -> 0 }`, want: "6"},
		{name: "a guard sees the bindings",
			src: `match [3, 1] { [m, n] if m > n -> "desc"; [m, n] -> "asc" }`, want: "desc"},
		{name: "a range subject destructures",
			src: `match 1..2 { [a, b] -> a + b; else -> 0 }`, want: "3"},
		{name: "a binding does not escape the arm",
			src: `match [1, 2] { [x, y] -> x }; x`, err: "undefined variable 'x'"},

		{name: "for over a dict takes the pair apart",
			src: `out = ""; for k, v in {a: 1, b: 2} { out += "${k}${v}" }; out`, want: "a1b2"},
		{name: "for over pairs of its own", src: `n = 0; for a, b in [[1, 2], [3, 4]] { n += a * b }; n`, want: "14"},
		{name: "for over items that are not pairs raises", src: `for a, b in [1, 2] { a }`,
			err: "cannot destructure int: a two-variable 'for' takes an array of two per item"},
		{name: "for over items of the wrong length raises", src: `for a, b in [[1, 2, 3]] { a }`,
			err: "a two-variable 'for' expects 2 values per item, got 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh interpreter per row: several rows write $vars, and the globals
			// table is per-Run but the LRU of compiled programs is not.
			in := evInterp()
			if tt.err != "" {
				if got := evErr(t, in, tt.src, nil).Msg; !strings.Contains(got, tt.err) {
					t.Errorf("%s error = %q, want it to contain %q", tt.src, got, tt.err)
				}
				return
			}
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestGlobals is §10: a separate namespace, unbound reads as nil, writes come back to
// the host, and the table is per-Run.
func TestGlobals(t *testing.T) {
	in := evInterp()

	t.Run("an unbound global is nil", func(t *testing.T) {
		if v := evOK(t, in, `$not_existed`, nil); !v.IsNil() {
			t.Errorf("= %s, want nil", v.Inspect())
		}
	})

	t.Run("keys normalise with and without the dollar", func(t *testing.T) {
		for _, key := range []string{"$__sent", "__sent"} {
			v := evOK(t, in, `$__sent`, map[string]Value{key: Str("да")})
			if v.Str() != "да" {
				t.Errorf("bound as %q = %q, want %q", key, v.Str(), "да")
			}
		}
	})

	t.Run("writes are visible to the host", func(t *testing.T) {
		p, err := in.Compile("set_var", `$out = $in.int + 1; $created = "new"`)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		res, err := in.RunResult(context.Background(), p, map[string]Value{"$in": Str("41")})
		if err != nil {
			t.Fatalf("RunResult: %v", err)
		}
		if got := res.Globals["$out"]; got.Int() != 42 {
			t.Errorf("$out = %s, want 42", got.Inspect())
		}
		if got := res.Globals["$created"]; got.Str() != "new" {
			t.Errorf("$created = %s, want %q", got.Inspect(), "new")
		}
		if res.Steps <= 0 {
			t.Errorf("Steps = %d, want a positive count", res.Steps)
		}
	})

	t.Run("bindings do not leak between runs", func(t *testing.T) {
		p, err := in.Compile("iso", `$x = ($x ?? "unset").str; $x`)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		first, err := in.Run(context.Background(), p, map[string]Value{"$x": Str("one")})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		second, err := in.Run(context.Background(), p, nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if first.Str() != "one" || second.Str() != "unset" {
			t.Errorf("runs saw %q then %q, want %q then %q", first.Str(), second.Str(), "one", "unset")
		}
	})

	t.Run("a default global is overridden per run", func(t *testing.T) {
		d := New(Options{})
		d.SetGlobal("tenant", Str("default"))
		if got := evOK(t, d, `$tenant`, nil).Str(); got != "default" {
			t.Errorf("= %q, want %q", got, "default")
		}
		if got := evOK(t, d, `$tenant`, map[string]Value{"tenant": Str("acme")}).Str(); got != "acme" {
			t.Errorf("= %q, want %q", got, "acme")
		}
	})
}

// TestDeterminism is §8.13: the same source, vars and options give the same answer, and
// nothing that reads a clock or a random source exists unless the host installs it.
func TestDeterminism(t *testing.T) {
	in := evInterp()

	const src = `d = {z: 1, a: 2}; d["m"] = 3; d.keys.join(",") + "|" + d.json`
	first := evStr(t, in, src)
	for i := 0; i < 8; i++ {
		if got := evStr(t, in, src); got != first {
			t.Fatalf("run %d = %q, want %q", i, got, first)
		}
	}

	for _, src := range []string{`rand()`, `uuid()`, `now()`, `[1, 2].sample`} {
		t.Run(src, func(t *testing.T) {
			if _, err := in.Eval(context.Background(), src, nil); err == nil {
				t.Errorf("%s is available without the capability; §14.3 gates it", src)
			}
		})
	}
}

// TestLimits pins that every runaway program is interruptible, whichever guard fires
// first (§14.1).
func TestLimits(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{
			name: "the step budget interrupts an empty infinite loop",
			opts: Options{StepBudget: 10_000, Timeout: -1},
			want: ErrBudget,
		},
		{
			name: "the deadline interrupts an empty infinite loop",
			opts: Options{Timeout: 30 * time.Millisecond, StepBudget: -1},
			want: ErrTimeout,
		},
		{
			name: "cancellation interrupts an empty infinite loop",
			opts: Options{Timeout: -1, StepBudget: -1},
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 30*time.Millisecond)
			},
			want: ErrCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(tt.opts)
			p, err := in.Compile("loop", `while true { }`)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			ctx := context.Background()
			if tt.ctx != nil {
				c, cancel := tt.ctx()
				defer cancel()
				ctx = c
			}
			done := make(chan error, 1)
			go func() {
				_, err := in.Run(ctx, p, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, tt.want) {
					t.Errorf("= %v, want %v", err, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Run did not return; the loop was not interruptible")
			}
		})
	}
}

// TestMaxDepth proves unbounded recursion is a script error, never a Go stack overflow.
func TestMaxDepth(t *testing.T) {
	in := New(Options{MaxDepth: 32})
	if _, err := in.Eval(context.Background(), `fn rec() { rec() }; rec()`, nil); !errors.Is(err, ErrDepth) {
		t.Fatalf("= %v, want ErrDepth", err)
	}
}

// TestCollectionAndStringLimits keeps one operation from allocating the host out of
// memory (§14.2).
func TestCollectionAndStringLimits(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		src  string
	}{
		{"string repetition is capped", Options{MaxStringBytes: 64}, `"aaaaaaaa" * 1000`},
		{"array repetition is capped", Options{MaxCollection: 8}, `[1] * 100`},
		{"range materialisation is capped", Options{MaxCollection: 8}, `(1..100).array`},
		{"interpolation is capped", Options{MaxStringBytes: 64}, `s = "a" * 60; "${s}${s}"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(tt.opts)
			if _, err := in.Eval(context.Background(), tt.src, nil); !errors.Is(err, ErrBudget) {
				t.Errorf("%s = %v, want a limit error", tt.src, err)
			}
		})
	}
}

// TestHostFunctions is §13.4: what a host installs, how its errors surface, and that a
// fatal one is not catchable.
func TestHostFunctions(t *testing.T) {
	in := New(Options{})
	in.Register("twice", 1, func(c *Ctx, args []Value) (Value, error) {
		return Int(args[0].Int() * 2), nil
	})
	in.Register("fail", 0, func(c *Ctx, args []Value) (Value, error) {
		return Nil(), c.Errorf("host said no")
	})
	in.Register("fatal", 0, func(c *Ctx, args []Value) (Value, error) {
		return Nil(), wrapError(ErrKindRaise, ErrFatal, "unrecoverable")
	})
	in.Register("apply", 2, func(c *Ctx, args []Value) (Value, error) {
		return c.Call(args[0], args[1])
	})
	in.RegisterModule("cfg", map[string]Value{"name": Str("acme")})

	tests := []struct {
		name string
		src  string
		want string
		err  string
	}{
		{name: "a host function", src: `twice(21)`, want: "42"},
		{name: "ufcs reaches a host function", src: `21.twice`, want: "42"},
		{name: "a host error is a script error", src: `fail()`, err: "host said no"},
		{name: "a host error is catchable", src: `try fail() else "ok"`, want: "ok"},
		{name: "a fatal host error is not catchable", src: `try fatal() else "ok"`, err: "unrecoverable"},
		{name: "arity is checked", src: `twice(1, 2)`, err: "argument"},
		{name: "a host function may call back into a closure", src: `apply({ it + 1 }, 1)`, want: "2"},
		{name: "a module member", src: "include cfg\ncfg.name", want: "acme"},
		{name: "a host module still needs its include", src: `cfg.name`, err: "include cfg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := in.Eval(context.Background(), tt.src, nil)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Errorf("%s = (%s, %v), want an error containing %q", tt.src, v.Inspect(), err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", tt.src, err)
			}
			if got := v.Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	t.Run("Unregister narrows the surface", func(t *testing.T) {
		narrow := New(Options{})
		narrow.Unregister("println")
		if _, err := narrow.Eval(context.Background(), `println("x")`, nil); err == nil {
			t.Error("println is still reachable after Unregister")
		}
	})
}

// TestErrorsCarryPosition pins §17: every error names the file, the line and the column,
// and renders as `<file>:<line>:<col>: <kind>: <message>`.
func TestErrorsCarryPosition(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a runtime type error", "\n\n\"Hello \" + 1", `t.mzs:3:10: type: cannot add int to string`},
		{"division by zero", `1 / 0`, `t.mzs:1:3: zero-division: divided by 0`},
		{"a raise", `raise("boom")`, `t.mzs:1:1: raise: boom`},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := in.Compile("t.mzs", tt.src)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			_, err = in.Run(context.Background(), p, nil)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error is %T (%v), want *Error", err, err)
			}
			if got := e.Error(); got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompileRejectsUnresolvedNames is §9.3: there is no bareword-to-string shim, so a
// plain-text answer template must not compile as a program.
func TestCompileRejectsUnresolvedNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
		ok   bool
	}{
		{"a bare word", `Привет`, false},
		{"several bare words", `Стрижка c фейдом`, false},
		{"a name bound first", `Привет = 1; Привет`, true},
		{"a declared function", `fn f() { 1 }; f()`, true},
		{"a builtin", `len("a")`, true},
		{"a module", "include json\n" + `json.parse("1")`, true},
		{"a module without its include", `json.parse("1")`, false},
		{"an unbound global is fine", `$nope`, true},
	}

	in := evInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := in.Compile("t", tt.src)
			if tt.ok && err != nil {
				t.Errorf("Compile(%q): %v", tt.src, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("Compile(%q) succeeded; an unresolved identifier is an error", tt.src)
			}
		})
	}
}

// TestProgramCacheContract pins what Options.ProgramCache means, because the field is
// the one bound whose zero value could plausibly read either way. It follows the rest of
// Options: 0 is "unset, use the default", and turning the cache off takes a negative
// size. Compile is keyed on (name, src), so an enabled cache hands back the identical
// *Program and a disabled one compiles afresh — which is what makes the two observable.
func TestProgramCacheContract(t *testing.T) {
	compileTwice := func(t *testing.T, o Options) (*Program, *Program) {
		t.Helper()
		in := New(o)
		a, err := in.Compile("p", `1 + 1`)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		b, err := in.Compile("p", `1 + 1`)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		return a, b
	}

	t.Run("the zero value is the default, not disabled", func(t *testing.T) {
		if got := (Options{}).normalize().ProgramCache; got != DefaultProgramCache {
			t.Errorf("ProgramCache 0 normalized to %d; want the default %d", got, DefaultProgramCache)
		}
		if a, b := compileTwice(t, Options{}); a != b {
			t.Error("zero Options recompiled the same (name, src); want the cached *Program")
		}
	})

	t.Run("a negative size disables the cache", func(t *testing.T) {
		if got := (Options{ProgramCache: -1}).normalize().ProgramCache; got != 0 {
			t.Errorf("ProgramCache -1 normalized to %d; want 0", got)
		}
		if a, b := compileTwice(t, Options{ProgramCache: -1}); a == b {
			t.Error("ProgramCache -1 still cached; want a fresh *Program each Compile")
		}
	})

	t.Run("an explicit size is kept", func(t *testing.T) {
		if got := (Options{ProgramCache: 7}).normalize().ProgramCache; got != 7 {
			t.Errorf("ProgramCache 7 normalized to %d; want 7", got)
		}
	})
}
