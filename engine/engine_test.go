package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mzs"
)

// Every case below is ported from morzebot-backend-v2's own suites —
// tests/eval_test.go and pkg/engine/eval/eval_test.go — with the expressions run
// through the codemod of §19.2 and the call shape updated to
// Bool(ctx, expr, vars). There is no legacy path to pin beside them: values are
// bound, never substituted (§10), so the quoted-variable forms the ruby runner
// needed ('$__sent') are gone from the corpus and from this file.

// TestBoolCondition ports tests/eval_test.go's TestBoolCondition: the condition
// dialect the production scripts are actually written in, migrated.
func TestBoolCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		vars map[string]string
		want bool
	}{
		{"equal match", `$__sent == "да"`, map[string]string{"$__sent": "да"}, true},
		{"equal mismatch", `$__sent == "да"`, map[string]string{"$__sent": "нет"}, false},
		{"numeric compares as string", `$__sent == "1"`, map[string]string{"$__sent": "1"}, true},
		{"lower then trim", `$__sent.lower.trim == "on-prem"`, map[string]string{"$__sent": "  On-Prem "}, true},
		{"unicode fold and trim", `$__sent.lower.trim == "оператор"`, map[string]string{"$__sent": "  ОПЕРАТОР "}, true},
		{"or combinator true branch", `$__sent == "btn_1" || $t == "1"`, map[string]string{"$__sent": "x", "$t": "1"}, true},
		{"or combinator both false", `$__sent == "btn_1" || $t == "1"`, map[string]string{"$__sent": "x", "$t": "2"}, false},
		{"and combinator", `$__sent == "a" && $b == "c"`, map[string]string{"$__sent": "a", "$b": "c"}, true},
		{"substring", `$__sent.has("lo")`, map[string]string{"$__sent": "hello"}, true},
		{"regex case-insensitive", `$__sent.lower ~ /при/i`, map[string]string{"$__sent": "ПРИВЕТ"}, true},
		{"regex word boundary", `$__sent.lower ~ /\bменю\b|главное меню/i`, map[string]string{"$__sent": "Меню"}, true},
		{"negated regex", `$__sent.lower !~ /отмена/i`, map[string]string{"$__sent": "да"}, true},
		{"negation", `!($__sent == "да")`, map[string]string{"$__sent": "нет"}, true},
		{"explicit int conversion", `$bot_check_attempts.int >= 2`, map[string]string{"$bot_check_attempts": "3"}, true},
		{"explicit int conversion below", `$bot_check_attempts.int >= 2`, map[string]string{"$bot_check_attempts": "0"}, false},
		{"membership", `["да", "ага", "конечно"].has($__sent.lower.trim)`, map[string]string{"$__sent": " Ага "}, true},
		{"unbound global is falsy", `$not_existed`, nil, false},
		{"zero is truthy", `0`, nil, true},
		{"match one-liner", `match $__sent.lower.trim { in ["да", "ага"] -> true; else -> false }`, map[string]string{"$__sent": " АГА "}, true},
	}

	eng := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := eng.Bool(context.Background(), tt.expr, tt.vars)
			if err != nil {
				t.Fatalf("Bool(%q) %v error: %v", tt.expr, tt.vars, err)
			}
			if got != tt.want {
				t.Errorf("Bool(%q) %v = %v, want %v", tt.expr, tt.vars, got, tt.want)
			}
		})
	}
}

// TestBoolMalformedExpression ports TestBoolMalformedExpression and
// TestConditionErrorIsReported: a broken condition must surface an error, which
// condition.go reads as "no match".
func TestBoolMalformedExpression(t *testing.T) {
	t.Parallel()

	got, err := Bool(context.Background(), `"a" == == "b"`, nil)
	if err == nil {
		t.Fatal("expected an error for a malformed condition, got nil")
	}
	if got {
		t.Error("Bool = true alongside an error; an error must never match")
	}
}

// TestBoolEmptyExpression ports the empty-input guard, which used to exist so no
// ruby process was spawned for nothing and now keeps the same contract. An empty
// field is the one input String must not hand back verbatim: an empty bubble is
// never sent (§13.6).
func TestBoolEmptyExpression(t *testing.T) {
	t.Parallel()

	if _, err := Bool(context.Background(), "   ", nil); err == nil {
		t.Fatal("expected an error for an empty expression, got nil")
	}
	if _, err := String(context.Background(), "", nil); !errors.Is(err, ErrNilResult) {
		t.Fatal("an empty expression must report as producing no value")
	}
}

// TestString ports tests/eval_test.go's TestString and TestNeedEvalString.
func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		vars map[string]string
		want string
	}{
		{"concat and upper", `"Hello " + $__sent.upper`, map[string]string{"$__sent": "world"}, "Hello WORLD"},
		{"trim", `$__sent.trim`, map[string]string{"$__sent": "  padded  "}, "padded"},
		{"integer arithmetic", `(2 + 3).str`, nil, "5"},
		{"interpolated global", `"Ваш адрес $__sent?"`, map[string]string{"$__sent": "Ленина 1"}, "Ваш адрес Ленина 1?"},
		{"interpolated expression", `"Итоговая цена: ${$price.int + 1200}"`, map[string]string{"$price": "800"}, "Итоговая цена: 2000"},
		{"unset price still converts", `"${$price.int}"`, nil, "0"},
		{"json fallback", "include json\n" + `json.parse($__webhook_res).dig(0, "generated_text") ?? "Упс"`,
			map[string]string{"$__webhook_res": `[{"generated_text":"ok"}]`}, "ok"},
		{"json fallback fires", "include json\n" + `json.parse($__webhook_res).dig(0, "generated_text") ?? "Упс"`,
			map[string]string{"$__webhook_res": `[]`}, "Упс"},
	}

	eng := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := eng.String(context.Background(), tt.expr, tt.vars)
			if err != nil {
				t.Fatalf("String(%q) error: %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

// TestStringFallsBackToSourceText pins §13.6 rule 5, which is where the bareword
// shim of the earlier draft went (§9.3): a plain-text answer is not a program, so a
// need_eval accidentally enabled on one sends the text instead of failing. A
// *runtime* error is a different thing and still an error.
func TestStringFallsBackToSourceText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
		fail bool
	}{
		{"single bare word", "Привет", "Привет", false},
		{"a whole sentence", "Привет, чем помочь?", "Привет, чем помочь?", false},
		{"text that looks like ruby", "'$__sent'.downcase", "'$__sent'.downcase", false},
		{"unterminated string", `"осталось`, `"осталось`, false},
		{"runtime type error is not text", `"2" + 1`, "", true},
	}

	eng := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := eng.String(context.Background(), tt.expr, nil)
			if tt.fail {
				if err == nil {
					t.Fatalf("String(%q) = %q, want an error", tt.expr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("String(%q) error: %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("String(%q) = %q, want the source text %q", tt.expr, got, tt.want)
			}
		})
	}
}

// TestNilResultIsAnError pins §13.6 rule 5's other half: an expression that
// compiles and yields nothing must not send an empty bubble.
func TestNilResultIsAnError(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"nil", "$not_existed"} {
		if _, err := String(context.Background(), expr, nil); !errors.Is(err, ErrNilResult) {
			t.Errorf("String(%q) error = %v, want ErrNilResult", expr, err)
		}
	}
}

// TestValueForButtons covers the need_eval_buttons path, which §19.3 switches from
// String to Value so the array of dicts never round-trips through a string.
func TestValueForButtons(t *testing.T) {
	t.Parallel()

	v, err := Value(context.Background(), `(0..2).map { {text: it.str, data: "var:date:${it}"} }`, nil)
	if err != nil {
		t.Fatalf("Value error: %v", err)
	}
	if v.Kind() != mzs.KArray || v.Len() != 3 {
		t.Fatalf("Value = %s, want an array of 3 dicts", v.Inspect())
	}
	first := v.Index(0)
	if got := first.Get(mzs.Str("text")).Str(); got != "0" {
		t.Errorf("buttons[0][text] = %q, want %q", got, "0")
	}
	if got := first.Get(mzs.Str("data")).Str(); got != "var:date:0" {
		t.Errorf("buttons[0][data] = %q, want %q", got, "var:date:0")
	}
}

// TestBoundValuesAreNeverParsed is the whole point of §10 and the disagreement
// class §19.5 expects during shadow mode: an apostrophe, a space or an emoji in a
// value turned the textual-substitution path into a syntax error and therefore into
// "no match". Bound values cannot reach the parser at all.
func TestBoundValuesAreNeverParsed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{"apostrophe", "О'Брайен"},
		{"spaces", "Стрижка c фейдом"},
		{"emoji", "EN 🇬🇧"},
		{"ampersand", "Orange & Lime"},
		{"parentheses", "Elite Plus (350k)"},
		{"quote", `он сказал "да"`},
		{"newline", "две\nстроки"},
		{"looks like code", `"] + say("pwned") + ["`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vars := map[string]string{"__sent": tt.value, "want": tt.value}
			got, err := Bool(context.Background(), `$__sent == $want`, vars)
			if err != nil {
				t.Fatalf("Bool error: %v", err)
			}
			if !got {
				t.Errorf("a bound %s value did not compare equal to itself", tt.name)
			}
		})
	}
}

// TestVarKeyNormalisation pins §13.6 rule 2: the host may spell a var with or
// without the leading '$'.
func TestVarKeyNormalisation(t *testing.T) {
	t.Parallel()

	for _, vars := range []map[string]string{
		{"__sent": "да"},
		{"$__sent": "да"},
	} {
		got, err := Bool(context.Background(), `$__sent == "да"`, vars)
		if err != nil {
			t.Fatalf("Bool error: %v", err)
		}
		if !got {
			t.Errorf("Bool with vars %v = false, want true", vars)
		}
	}
}

// TestCacheKeyIsSourceOnly pins §13.6 rule 3. Values are bound rather than
// substituted, so the source text is the whole key: a dialogue that sends a
// different message every turn still compiles its condition exactly once.
func TestCacheKeyIsSourceOnly(t *testing.T) {
	t.Parallel()

	eng := New(Options{})
	for _, sent := range []string{"да", "нет", "может быть", "да"} {
		if _, err := eng.Bool(context.Background(), `$__sent == "да"`, map[string]string{"__sent": sent}); err != nil {
			t.Fatalf("Bool error: %v", err)
		}
	}
	if got := eng.cache.Len(); got != 1 {
		t.Errorf("cache holds %d programs after four evaluations of one condition, want 1", got)
	}
}

// TestTimeoutWrapsSentinel pins §13.6 rule 6: callers keep their existing "error
// means fall back" behaviour and can still tell a runaway apart. Which of the two
// limits fires first depends on the machine, and both are uncatchable.
func TestTimeoutWrapsSentinel(t *testing.T) {
	t.Parallel()

	eng := New(Options{Timeout: 200 * time.Millisecond})
	_, err := eng.Bool(context.Background(), "while true { }", nil)
	if !errors.Is(err, mzs.ErrTimeout) && !errors.Is(err, mzs.ErrBudget) {
		t.Fatalf("error = %v, want it to wrap ErrTimeout or ErrBudget", err)
	}
}

// TestContextCancellation makes sure the caller's context still governs, so a
// dropped webhook does not leave work running.
func TestContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	eng := New(Options{Timeout: 5 * time.Second})
	if _, err := eng.Bool(ctx, "i = 0; while i < 100000000 { i += 1 }; true", nil); err == nil {
		t.Fatal("expected a cancellation error")
	}
}

// TestStdoutIsCaptured proves say inside a script cannot reach the bot's own
// stdout unless the host asks for it.
func TestStdoutIsCaptured(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	eng := New(Options{Stdout: &out})
	if _, err := eng.String(context.Background(), `say("hi"); "done"`, nil); err != nil {
		t.Fatalf("String error: %v", err)
	}
	if out.String() != "hi\n" {
		t.Errorf("captured stdout = %q, want %q", out.String(), "hi\n")
	}

	quiet := New(Options{})
	if _, err := quiet.String(context.Background(), `say("hi"); "done"`, nil); err != nil {
		t.Fatalf("String error: %v", err)
	}
}

// TestCheckReportsWarnings covers the publish-time validator of §19.4: a literal
// double backslash in a regex is the corpus's most common silent bug (§11.5).
func TestCheckReportsWarnings(t *testing.T) {
	t.Parallel()

	eng := Default()
	warns, err := eng.Check(`$__sent.lower ~ /\\bеда\\b/i`)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(warns) == 0 {
		t.Error(`Check produced no warning for \\b (§11.5)`)
	}
	if _, err := eng.Check(`"a" == == "b"`); err == nil {
		t.Error("Check accepted a malformed condition")
	}
	if _, err := eng.Check("  "); !errors.Is(err, ErrNilResult) {
		t.Errorf("Check(\"  \") error = %v, want ErrEmpty", err)
	}
}

// TestConcurrentUse pins the concurrency contract: one Engine serves every
// dialogue, and two dialogues never see each other's variables.
func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	eng := New(Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 128)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := "да"
			if i%2 == 1 {
				want = "нет"
			}
			got, err := eng.String(context.Background(), `$__sent`, map[string]string{"__sent": want})
			if err != nil {
				errs <- err
				return
			}
			if got != want {
				errs <- errors.New("got " + got + ", want " + want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestNoPanicOnHostileInput is the engine-side half of A7: whatever a script author
// types into the web editor, and whatever ruby a half-finished migration leaves
// behind, the bot worker survives it.
func TestNoPanicOnHostileInput(t *testing.T) {
	t.Parallel()

	eng := New(Options{Timeout: 100 * time.Millisecond})
	for _, expr := range []string{
		"'", `"`, "/", "((((", "}}}}", "$", "${", "#{x}", "\x00", strings.Repeat("(", 500),
		"1 +", `== "x"`, "$__sent ==", "%w[a b]", "->", "fn", "if c do end", ":name",
		"{ |x| x }", "-2 ** 2", "1...5", "a::B", "x &. y", "a rescue b", "0..5.map { it }",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", expr, r)
				}
			}()
			_, _ = eng.Bool(context.Background(), expr, map[string]string{"__sent": "x"})
			_, _ = eng.String(context.Background(), expr, map[string]string{"__sent": "x"})
		}()
	}
}
