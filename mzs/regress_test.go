// Regressions for defects found by adversarial review and by differential testing of
// the corpus against the runner mzs replaces. Each test here corresponds to a bug that
// shipped in an implementation pass and that the unit suites all missed; none of them is
// a restatement of §16, which corpus_test.go owns.
package mzs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mzs/mzs"
)

// regressInterp is deliberately generous with time: these tests are about what an
// evaluation does, not how fast, and a shared CI box must not turn a crash test into a
// flake.
func regressInterp() *mzs.Interp {
	return mzs.New(mzs.Options{Timeout: 5 * time.Second})
}

// evalStr runs src and returns its string form.
func evalStr(t *testing.T, src string) string {
	t.Helper()
	v, err := regressInterp().Eval(context.Background(), src, nil)
	if err != nil {
		t.Fatalf("Eval(%s): %v", src, err)
	}
	return v.Str()
}

// TestCyclicCollectionDoesNotKillHost pins A7 for self-referential data. Rendering or
// comparing a cycle used to recurse until the Go stack overflowed, which is fatal and
// cannot be recovered — a single `a.push(a)` in one bot author's script would have taken
// down the whole morzebot process, not just that evaluation.
func TestCyclicCollectionDoesNotKillHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{"self-referential array", `a = [1]; a.push(a); a.str`},
		{"self-referential dict", `m = {}; m["self"] = m; m.str`},
		{"mutually referential arrays", `a = []; b = []; a.push(b); b.push(a); a.str`},
		{"cycle compared to cycle", `a = [1]; a.push(a); b = [1]; b.push(b); (a == b).str`},
		{"cycle interpolated", `a = [1]; a.push(a); "v=${a}"`},
		{"cycle rendered as json", `a = [1]; a.push(a); a.json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Returning at all is the assertion: a stack overflow aborts the test
			// binary rather than failing the test. Refusing a cycle with a script error
			// is a fine answer; recursing until the host dies is not.
			v, err := regressInterp().Eval(context.Background(), tt.src, nil)
			if err == nil {
				if v.Str() == "" {
					t.Errorf("%s = %q, want a rendered value", tt.src, v.Str())
				}
				return
			}
			var e *mzs.Error
			if !errors.As(err, &e) {
				t.Fatalf("%s error is %T (%v), want a script *Error", tt.src, err, err)
			}
		})
	}
}

// TestFloatToIntSaturates covers the out-of-range float conversion. int64(f) is undefined
// in Go for out-of-range f and wrapped to MinInt64 on amd64, so `1e30.int` returned a
// large *negative* number and silently poisoned any arithmetic after it. mzs is int64-only
// and cannot promote to a bignum, so it saturates and keeps the sign.
func TestFloatToIntSaturates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"huge positive saturates to max", `(1e30).int`, "9223372036854775807"},
		{"huge negative saturates to min", `(0.0 - 1e30).int`, "-9223372036854775808"},
		{"positive infinity saturates", `(1.0 / 0.0).int`, "9223372036854775807"},
		{"nan is zero", `(0.0 / 0.0).int`, "0"},
		{"in-range truncates toward zero", `(3.7).int`, "3"},
		{"negative in-range truncates toward zero", `(0.0 - 3.7).int`, "-3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := evalStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestPosixClassesAreUnicode pins the POSIX bracket classes to Unicode semantics. Go's
// RE2 implements them as ASCII-only, so /[[:alpha:]]+/ matched "Привет" under the
// backtracking backend and not under RE2 — the two backends disagreed with each other,
// which is the one thing §11.2 may never allow.
func TestPosixClassesAreUnicode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		subject string
		want    bool
	}{
		{"alpha matches Cyrillic", `[[:alpha:]]+`, "Привет", true},
		{"alnum matches Cyrillic", `[[:alnum:]]+`, "Привет", true},
		{"upper matches a Cyrillic capital", `[[:upper:]]`, "Привет", true},
		{"lower matches Cyrillic lowercase", `[[:lower:]]`, "привет", true},
		{"digit does not match letters", `[[:digit:]]+`, "Привет", false},
		{"space does not match letters", `[[:space:]]`, "Привет", false},
		{"alpha still matches ASCII", `[[:alpha:]]+`, "hello", true},
		{"class combined with a range", `[[:alpha:]0-9]+`, "Привет", true},
		{"negated class", `[^[:alpha:]]`, "Привет!", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := `"` + tt.subject + `" ~ /` + tt.pattern + `/`
			if got := evalStr(t, src); got != boolStr(tt.want) {
				t.Errorf("%s = %s, want %v", src, got, tt.want)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestDeepRecursionIsCaught keeps the runaway-recursion guard honest: it must surface as
// ErrDepth, never as a Go stack overflow (A5, A7).
func TestDeepRecursionIsCaught(t *testing.T) {
	t.Parallel()

	_, err := regressInterp().Eval(context.Background(), `fn f(n) { f(n + 1) }; f(0)`, nil)
	if !errors.Is(err, mzs.ErrDepth) {
		t.Fatalf("unbounded recursion error = %v, want ErrDepth", err)
	}
}

// TestQuestionMarkForms is the lexer's hardest corner now that `x.empty?` is gone: four
// different tokens start with `?`, and `n>0?"pos":"neg"` has no whitespace to help.
// Both `$flag?"y":"n"` (the global absorbed the `?`) and `a??b` (parsed as `a ? ? b`)
// shipped broken once.
func TestQuestionMarkForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"ternary with no spaces", `n = 5; n>0?"pos":"neg"`, "pos"},
		{"ternary on a global with no spaces", `$flag?"y":"n"`, "n"},
		{"ternary spaced", `true ? "y" : "n"`, "y"},
		{"nil coalescing", `nil ?? "d"`, "d"},
		{"nil coalescing does not fire on false", `false ?? "d"`, "false"},
		{"nil coalescing is left associative", `nil ?? false ?? "c"`, "false"},
		{"safe navigation", `nil?.lower ?? "n"`, "n"},
		{"safe navigation chains", `d = {a: {}}; d?.get("a")?.get("b") ?? "none"`, "none"},
		{"coalescing assignment fires once", `x = nil; x ??= "set"; x ??= "again"; x`, "set"},
		{"ternary then coalescing", `(nil ? 1 : nil) ?? 2`, "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := evalStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestBoundValueNeverReachesTheParser is why the Translate pass of the earlier drafts is
// gone (§10). A value is bound, never substituted, so no value — however hostile — can
// change what the program means. Every string below broke the substituting runner.
func TestBoundValueNeverReachesTheParser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{"an apostrophe", `О'Брайен`},
		{"a double quote", `он сказал "да"`},
		{"a closing paren", `Elite Plus (350k)`},
		{"an emoji", `RU 🇷🇺`},
		{"a newline", "две\nстроки"},
		{"mzs source", `" + println("pwned") + "`},
		{"a comment", `да # и нет`},
		{"a regex literal", `/привет/i`},
		{"another global's name", `$other`},
		{"a backslash", `C:\новая\папка`},
	}

	in := regressInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vars := map[string]mzs.Value{"$__sent": mzs.Str(tt.value)}
			// The comparison is against the *bound* value, not against source text, so
			// the right-hand side comes in as a second global rather than as a literal.
			vars["$want"] = mzs.Str(tt.value)
			v, err := in.Eval(context.Background(), `$__sent == $want && $__sent.len == $want.len`, vars)
			if err != nil {
				t.Fatalf("Eval with %q: %v", tt.value, err)
			}
			if !v.Bool() {
				t.Errorf("a bound value of %q did not compare equal to itself", tt.value)
			}
		})
	}
}

// TestSingleQuotedGlobalIsLiteralText is the other half of §3.7. Production conditions
// are full of `'$name'` because the legacy engine substituted text before parsing; in mzs
// the single-quoted form is literal and the double-quoted one interpolates, and mixing
// them up is the single most likely migration error.
func TestSingleQuotedGlobalIsLiteralText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		vars map[string]mzs.Value
		want string
	}{
		{"single quotes are literal", `'$__sent'`, map[string]mzs.Value{"$__sent": mzs.Str("да")}, "$__sent"},
		{"double quotes interpolate", `"$__sent"`, map[string]mzs.Value{"$__sent": mzs.Str("да")}, "да"},
		{"an unbound global interpolates as empty", `"[$nope]"`, nil, "[]"},
		{"braced interpolation", `"${$__sent.upper}"`, map[string]mzs.Value{"$__sent": mzs.Str("да")}, "ДА"},
		{"a value that looks like a global stays text", `$__sent`,
			map[string]mzs.Value{"$__sent": mzs.Str("$other")}, "$other"},
	}

	in := regressInterp()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := in.Eval(context.Background(), tt.src, tt.vars)
			if err != nil {
				t.Fatalf("Eval(%s): %v", tt.src, err)
			}
			if got := v.Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestStringsAreRuneIndexed pins the one place a silent off-by-N hides: every position
// mzs reports or accepts is in runes, and the corpus is Cyrillic, so a byte index is
// wrong by a factor of two everywhere and still looks plausible on ASCII fixtures.
func TestStringsAreRuneIndexed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"len counts runes", `"привет".len`, "6"},
		{"index of a substring", `"привет".index("вет")`, "3"},
		{"index of a regex", `"привет".index(/вет/)`, "3"},
		{"a single rune", `"привет"[3]`, "в"},
		{"a negative index", `"привет"[-1]`, "т"},
		{"a substring by rune count", `"привет"[1, 3]`, "рив"},
		{"out of range is nil", `("привет"[99] ?? "-")`, "-"},
		{"bytes are still available", `"привет".bytes.len`, "12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := evalStr(t, tt.src); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestDictKeepsInsertionOrder pins §8.13. need_eval_buttons renders a dict straight into
// a Telegram payload, so a Go map's randomised iteration order would make the rendered
// keyboard differ between two runs of the same flow.
func TestDictKeepsInsertionOrder(t *testing.T) {
	t.Parallel()

	const src = `d = {z: 1, a: 2, m: 3}; d["b"] = 4; d.json + "|" + d.keys.join(",")`
	const want = `{"z":1,"a":2,"m":3,"b":4}|z,a,m,b`
	for i := 0; i < 8; i++ {
		if got := evalStr(t, src); got != want {
			t.Fatalf("run %d = %q, want %q", i, got, want)
		}
	}
}

// TestCompileErrorsAreBounded pins §17: the parser recovers at statement boundaries and
// reports at most ten diagnostics, with the real one first. An early recovery loop
// emitted a diagnostic per token to the end of the file, so a single unclosed brace
// buried the message that mattered under hundreds of others.
func TestCompileErrorsAreBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		first string
	}{
		{"a stray operator", `1 +`, "cascade.mzs:1:4: syntax: unexpected end of input"},
		{"an unclosed brace", `if true {`, "cascade.mzs:1:10: syntax: expected '}'"},
		{"an unclosed string", `x = "abc`, "cascade.mzs:1:5: syntax: unterminated string literal"},
		{"an unterminated regex", `x = /abc`, "cascade.mzs:1:5: syntax: unterminated regex literal"},
		{"the first typo is reported first", "a = 1 ...\nb = 2 ...",
			"cascade.mzs:1:7: syntax: '...' is not an mzs operator"},
		{"a file of nothing but typos", strings.Repeat("a = 1 ...\n", 50),
			"cascade.mzs:1:7: syntax: '...' is not an mzs operator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := regressInterp().Compile("cascade.mzs", tt.src)
			if err == nil {
				t.Fatalf("Compile(%q) = nil error, want a syntax error", tt.src)
			}
			lines := strings.Split(err.Error(), "\n")
			if len(lines) > 10 {
				t.Errorf("Compile(%q) reported %d diagnostics, want at most 10:\n%v",
					tt.src, len(lines), err)
			}
			if !strings.HasPrefix(lines[0], tt.first) {
				t.Errorf("Compile(%q) first diagnostic = %q, want it to start at %s",
					tt.src, lines[0], tt.first)
			}
		})
	}
}
