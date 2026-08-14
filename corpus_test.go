// The acceptance corpus of SPEC §16, transcribed verbatim. Every row is a real
// expression mined from production bot flows and then migrated by §19, so this file —
// not the unit suites — is the project's definition of done.
//
// Rows run through the public API of §13: Compile, then Run with the host's $vars bound
// as strings, because that is the only shape a value ever arrives in (§9.1). There is no
// compat mode and no textual substitution left to test: an expression means exactly what
// §1–§12 say it means.
package mzs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"mzs"
	"mzs/internal/rx"
)

// The five ways a corpus row is checked. The mode is part of the expectation: row 22a
// must be an Int and row 21 must be a Bool, so comparing rendered text would let a
// regression through (D5).
const (
	modeBool = "bool" // a condition block: the value must be a Bool
	modeInt  = "int"  // arithmetic and rune indices
	modeStr  = "str"  // a need_eval_* field
	modeJSON = "json" // need_eval_buttons: compared as JSON
	modeNil  = "nil"  // an unbound global (§9.2)
)

// corpusInterp is the interpreter every row runs on, with stdout wired to out so row 56
// can see what say wrote.
//
// The deadline is a runaway guard and not a performance budget, which is why it is ten
// seconds and not the default one. Two was not enough: the coverage job instruments every
// package and runs each package's binary alongside the others, and the heaviest example
// — 26_memoization, which calls fib naively up to n=22 — has already crossed two seconds
// of wall clock on a shared runner while computing exactly what it should. A deadline
// that close to the real time is a coin toss, and one that fires only on a genuine loop
// is the one worth keeping: an example stuck forever still fails here in ten seconds.
func corpusInterp(out io.Writer) *mzs.Interp {
	return mzs.New(mzs.Options{Timeout: 10 * time.Second, Stdout: out})
}

// bind lifts the host's string table into $vars. Keys are written without the `$` in the
// SPEC table, exactly as the bot engine stores them.
func bind(vars map[string]string) map[string]mzs.Value {
	if vars == nil {
		return nil
	}
	out := make(map[string]mzs.Value, len(vars))
	for k, v := range vars {
		out["$"+k] = mzs.Str(v)
	}
	return out
}

// nbsp is U+00A0. Messengers send it instead of a space and row 19 requires trim to
// remove it, which is why the fixture below is not written with plain blanks.
const nbsp = " "

// goRegexp compiles a pattern the way the RE2 backend must: `^`/`$` are line anchors and
// the corpus flag is always `i`.
func goRegexp(p string) (*regexp.Regexp, error) { return regexp.Compile("(?im)" + p) }

// TestCorpusConditions is SPEC §16.1, transcribed row for row.
func TestCorpusConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n      string
		name   string
		expr   string
		vars   map[string]string
		mode   string
		want   string
		stdout string
	}{
		{"01", "equality", `$__sent == "да"`, map[string]string{"__sent": "да"}, modeBool, "true", ""},
		{"02", "equality is case sensitive", `$__sent == "да"`, map[string]string{"__sent": "Да"}, modeBool, "false", ""},
		{"03", "equality нет", `$__sent == "нет"`, map[string]string{"__sent": "нет"}, modeBool, "true", ""},
		{"04", "numeric looking string", `$__sent == "1"`, map[string]string{"__sent": "1"}, modeBool, "true", ""},
		{"05", "two digit string", `$__sent == "10"`, map[string]string{"__sent": "10"}, modeBool, "true", ""},
		{"06", "slash command", `$__sent == "/start"`, map[string]string{"__sent": "/start"}, modeBool, "true", ""},
		{"07", "flag emoji value", `$__sent == "RU 🇷🇺"`, map[string]string{"__sent": "RU 🇷🇺"}, modeBool, "true", ""},
		{"08", "ampersand value", `$__sent == "Orange & Lime"`, map[string]string{"__sent": "Orange & Lime"}, modeBool, "true", ""},
		{"09", "parenthesised value", `$__sent == "Elite Plus (350k)"`, map[string]string{"__sent": "Elite Plus (350k)"}, modeBool, "true", ""},
		{"10", "multiword cyrillic value", `$__sent == "Стрижка c фейдом"`, map[string]string{"__sent": "Стрижка c фейдом"}, modeBool, "true", ""},
		{"11", "message type", `$__msg_type == "plain_text"`, map[string]string{"__msg_type": "plain_text"}, modeBool, "true", ""},
		{"12", "and combinator", `$__sent == "привет" && $__msg_type == "tg_buttons"`,
			map[string]string{"__sent": "привет", "__msg_type": "tg_buttons"}, modeBool, "true", ""},
		{"13", "or combinator", `$__sent == "btn_1" || $test == "1"`,
			map[string]string{"__sent": "x", "test": "1"}, modeBool, "true", ""},
		{"14", "int compare false", `$bot_check_attempts.int >= 2`, map[string]string{"bot_check_attempts": "0"}, modeBool, "false", ""},
		{"15", "int compare true", `$bot_check_attempts.int >= 2`, map[string]string{"bot_check_attempts": "3"}, modeBool, "true", ""},
		{"16", "tree emoji value", `$__sent == "🌲"`, map[string]string{"__sent": "🌲"}, modeBool, "true", ""},
		{"17", "operator flag", `$operator == "human"`, map[string]string{"operator": "human"}, modeBool, "true", ""},
		{"18", "lower trim slash command", `$__sent.lower.trim == "/operator"`,
			map[string]string{"__sent": " /Operator "}, modeBool, "true", ""},
		{"19", "unicode fold and unicode trim", `$__sent.lower.trim == "оператор"`,
			map[string]string{"__sent": " " + nbsp + "ОПЕРАТОР" + nbsp + " "}, modeBool, "true", ""},
		{"20", "lower trim question", `$__sent.lower.trim == "сколько стоит?"`,
			map[string]string{"__sent": "Сколько стоит?"}, modeBool, "true", ""},
		{"21", "greeting regex is a Bool", `$__sent.lower ~ /привет|здравствуй|hello|\bhi\b/i`,
			map[string]string{"__sent": "Привет"}, modeBool, "true", ""},
		{"22", "farewell regex", `$__sent.lower ~ /пока|до свидан|\bbye\b|прощай/i`,
			map[string]string{"__sent": "ну пока"}, modeBool, "true", ""},
		{"22a", "index is the rune index", `$__sent.lower.index(/пока|до свидан/i)`,
			map[string]string{"__sent": "ну пока"}, modeInt, "3", ""},
		{"23", "no match is false", `$__sent.lower ~ /эхо|echo|тест|ping|пинг/i`,
			map[string]string{"__sent": "что?"}, modeBool, "false", ""},
		{"24", "unicode word boundary", `$__sent.lower ~ /\bменю\b|главное меню/i`,
			map[string]string{"__sent": "Меню"}, modeBool, "true", ""},
		{"25", "bounded gap alternation", `$__sent.lower ~ /бесплатн.{0,14}(аудит|консультац)|free.?audit/i`,
			map[string]string{"__sent": "Бесплатная консультация"}, modeBool, "true", ""},
		{"26", "latin boundary in cyrillic text", `$__sent.lower ~ /\bcrm\b|црм|клиентск.{0,8}баз/i`,
			map[string]string{"__sent": "нужна CRM"}, modeBool, "true", ""},
		{"27", "int plus int", `$price.int + 1200`, map[string]string{"price": "800"}, modeInt, "2000", ""},
		{"28", "empty int never raises", `$price.int + 1200`, map[string]string{"price": ""}, modeInt, "1200", ""},
		{"29", "counter increment", `$bot_check_attempts.int + 1`, map[string]string{"bot_check_attempts": "2"}, modeInt, "3", ""},
		{"30", "int plus large int", `$__sent.int + 92304`, map[string]string{"__sent": "1"}, modeInt, "92305", ""},
		{"31", "split first field", `$__sent.split(":")[0]`, map[string]string{"__sent": "ivan:i@x.ru"}, modeStr, "ivan", ""},
		{"32", "split second field", `$__sent.split(":")[1]`, map[string]string{"__sent": "ivan:i@x.ru"}, modeStr, "i@x.ru", ""},
		{"33", "split on space cyrillic", `$__sent.split(" ")[0]`, map[string]string{"__sent": "Иван Петров"}, modeStr, "Иван", ""},
		{"34", "replace apostrophe", `$__sent.replace(/'/, "")`, map[string]string{"__sent": "О'Брайен"}, modeStr, "ОБрайен", ""},
		{"35", "interpolate a global", `"Ваш адрес $__sent?"`, map[string]string{"__sent": "Ленина 1"}, modeStr, "Ваш адрес Ленина 1?", ""},
		{"36", "interpolate an expression", `"Итоговая цена: ${$price.int}"`, map[string]string{"price": "1500"}, modeStr, "Итоговая цена: 1500", ""},
		{"37", "interpolate two globals", `"Вы записаны на $book_time на имя $user_name"`,
			map[string]string{"book_time": "14:00", "user_name": "Иван"}, modeStr, "Вы записаны на 14:00 на имя Иван", ""},
		{"38", "dig into parsed json", "include json\n" + `json.parse($__webhook_res).dig(0, "generated_text") ?? "Упс, что-то пошло не так..."`,
			map[string]string{"__webhook_res": `[{"generated_text":"ok"}]`}, modeStr, "ok", ""},
		{"39", "dig is nil safe and ?? fires", "include json\n" + `json.parse($__webhook_res).dig(0, "generated_text") ?? "Упс, что-то пошло не так..."`,
			map[string]string{"__webhook_res": `[]`}, modeStr, "Упс, что-то пошло не так...", ""},
		{"40", "grouped assignment and json membership", "include json\n" + `(t = $__sent != $time ? $__sent : $time; json.parse($times).has(t))`,
			map[string]string{"__sent": "14:00 - 14:30", "time": "15:00",
				"times": `["14:00 - 14:30", "14:30 - 15:00"]`}, modeBool, "true", ""},
		{"41", "ternary picks the new value", `date = $__sent != $date ? $__sent : $date`,
			map[string]string{"__sent": "12/03/25", "date": "11/03/25"}, modeStr, "12/03/25", ""},
		{"42", "negated equality", `!($__sent == "да")`, map[string]string{"__sent": "нет"}, modeBool, "true", ""},
		{"43", "concat upper", `"Hello " + $__sent.upper`, map[string]string{"__sent": "world"}, modeStr, "Hello WORLD", ""},
		{"44", "arithmetic then str", `(2 + 3).str`, nil, modeStr, "5", ""},
		{"45", "substring membership", `"hello".has("lo")`, nil, modeBool, "true", ""},
		{"46", "two conditions", `$__sent == "a" && $b == "c"`, map[string]string{"__sent": "a", "b": "c"}, modeBool, "true", ""},
		{"47", "negated equality again", `!($__sent == "да")`, map[string]string{"__sent": "нет"}, modeBool, "true", ""},
		{"48", "not match operator", `$__sent.lower !~ /отмена/i`, map[string]string{"__sent": "да"}, modeBool, "true", ""},
		{"49", "array membership", `["да", "ага", "конечно"].has($__sent.lower)`,
			map[string]string{"__sent": "Ага"}, modeBool, "true", ""},
		{"50", "times each returns the receiver", `3.times.each { it.str }`, nil, modeJSON, `[0,1,2]`, ""},
		{"51", "range map each_slice", `(0..6).map { it }.each_slice(2).array`, nil, modeJSON, `[[0,1],[2,3],[4,5],[6]]`, ""},
		{"52", "buttons payload round-trips", `(0..6).map { {text: it.str, data: "var:date:${it}"} }.each_slice(2).array`, nil, modeJSON,
			`[[{"text":"0","data":"var:date:0"},{"text":"1","data":"var:date:1"}],` +
				`[{"text":"2","data":"var:date:2"},{"text":"3","data":"var:date:3"}],` +
				`[{"text":"4","data":"var:date:4"},{"text":"5","data":"var:date:5"}],` +
				`[{"text":"6","data":"var:date:6"}]]`, ""},
		{"53", "an unbound global is nil", `$not_existed`, nil, modeNil, "", ""},
		{"54", "zero is a value and it is truthy", `0`, nil, modeInt, "0", ""},
		{"55", "validator fixture", `$sent == "лол"`, map[string]string{"sent": "лол"}, modeBool, "true", ""},
		{"56", "say writes and returns nil", `$sent == say("test")`, map[string]string{"sent": "x"}, modeBool, "false", "test\n"},
		{"57", "match with a subject", `match $__sent.lower.trim { in ["да","ага"] -> "yes"; /^нет/ -> "no"; else -> "?" }`,
			map[string]string{"__sent": " АГА "}, modeStr, "yes", ""},
		{"58", "match with no subject", `match { $__sent.len > 3 -> "long"; else -> "short" }`,
			map[string]string{"__sent": "да"}, modeStr, "short", ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("row%s_%s", tt.n, tt.name), func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			in := corpusInterp(&out)
			prog, err := in.Compile("cond#"+tt.n, tt.expr)
			if err != nil {
				t.Fatalf("Compile(%s): %v", tt.expr, err)
			}
			v, err := in.Run(context.Background(), prog, bind(tt.vars))
			if err != nil {
				t.Fatalf("Run(%s) with %v: %v", tt.expr, tt.vars, err)
			}

			switch tt.mode {
			case modeBool:
				// D5: a condition yields a Bool, never an index and never a string.
				if v.Kind() != mzs.KBool {
					t.Fatalf("%s = %s (%s), want a bool", tt.expr, v.Inspect(), v.Kind())
				}
				if fmt.Sprint(v.Bool()) != tt.want {
					t.Errorf("%s with %v = %v, want %s", tt.expr, tt.vars, v.Bool(), tt.want)
				}
			case modeInt:
				if v.Kind() != mzs.KInt {
					t.Fatalf("%s = %s (%s), want an int", tt.expr, v.Inspect(), v.Kind())
				}
				if got := fmt.Sprint(v.Int()); got != tt.want {
					t.Errorf("%s with %v = %s, want %s", tt.expr, tt.vars, got, tt.want)
				}
				// D6: everything except nil and false is truthy. Row 54 is in the table
				// to pin that 0 is one of them.
				if !v.Truthy() {
					t.Errorf("%s = %s is falsy; only nil and false are falsy", tt.expr, v.Inspect())
				}
			case modeStr:
				if got := v.Str(); got != tt.want {
					t.Errorf("%s with %v = %q, want %q", tt.expr, tt.vars, got, tt.want)
				}
			case modeJSON:
				b, err := v.MarshalJSON()
				if err != nil {
					t.Fatalf("MarshalJSON(%s): %v", tt.expr, err)
				}
				if string(b) != tt.want {
					t.Errorf("%s = %s, want %s", tt.expr, b, tt.want)
				}
			case modeNil:
				if !v.IsNil() {
					t.Errorf("%s = %s, want nil (§9.2)", tt.expr, v.Inspect())
				}
				if v.Truthy() {
					t.Errorf("%s is truthy; nil is falsy", tt.expr)
				}
			default:
				t.Fatalf("unknown mode %q", tt.mode)
			}

			if got := out.String(); got != tt.stdout {
				t.Errorf("%s wrote %q to stdout, want %q", tt.expr, got, tt.stdout)
			}
		})
	}
}

// TestCorpusRegexes is SPEC §16.2: every production pattern must compile, and none may
// fall back to a backend that cannot reproduce its documented semantics.
func TestCorpusRegexes(t *testing.T) {
	t.Parallel()

	patterns := []struct {
		src   string
		flags string
		// backend is asserted only where §16.2 names one: `(?!…)` selects the
		// backtracking engine because RE2 has no lookaround.
		backend string
	}{
		{`привет|здравствуй|hello|\bhi\b`, "i", ""},
		{`пока|до свидан|\bbye\b|прощай`, "i", ""},
		{`помощ|help|что умеешь|команд`, "i", ""},
		{`эхо|echo|тест|ping|пинг`, "i", ""},
		{`\bcrm\b|црм|клиентск.{0,8}баз|управлени.{0,10}клиент|от заявки до отгруз`, "i", ""},
		{`бесплатн.{0,14}(аудит|консультац|анализ|оценк|диагност)|бесплатно|free.?audit|пробн`, "i", ""},
		{`все темы|главное меню|\bменю\b|показать все|все раздел|остальные темы|другие раздел|в начало`, "i", ""},
		{`эта[пп]ы?|етап|\bкак\b.{0,16}(работа|внедр|происходит|устроен|стро)|процес|шаг[иове]?|сроки`, "i", ""},
		{`\bоператор|\boperator|\/operator|перевед.{0,12}оператор|переключ.{0,14}(на )?оператор`, "i", ""},
		{`ке[ийс]с|кэйс|\bcase\b|пример|портфолио|резул[ьъ]?тат|ваши проект|опыт работ|клиент`, "i", ""},
		{`классификац.{0,12}код|код.{0,12}(окпд|оквэд)`, "i", ""},
		{`^(?!❌ Отмена).*$`, "", "bt"},
		{`(Sun|Mon|Tue|Wed|Thu|Fri|Sat)`, "", ""},
		{`столов|кафе|\bеда\b|кормя|питани|обед|завтрак|ужин|повар|кухн|тр[её]хразов`, "i", ""},
	}

	for _, p := range patterns {
		t.Run(p.src, func(t *testing.T) {
			t.Parallel()
			r, err := rx.Compile(p.src, p.flags)
			if err != nil {
				t.Fatalf("Compile(/%s/%s): %v", p.src, p.flags, err)
			}
			if r.Approx() {
				t.Errorf("Compile(/%s/%s) is approximate; the documented semantics are not guaranteed",
					p.src, p.flags)
			}
			if p.backend != "" && r.Backend() != p.backend {
				t.Errorf("Compile(/%s/%s) backend = %q, want %q", p.src, p.flags, r.Backend(), p.backend)
			}
		})
	}
}

// TestCorpusRegexBehaviour pins the four behaviours §16.2 requires of the patterns
// above: `i` folds Cyrillic, `\b` is Unicode-aware, `^`/`$` are line anchors, and the
// index is a rune index.
func TestCorpusRegexBehaviour(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		flags   string
		input   string
		want    bool
		index   int // rune index of the first match; -1 when no match is expected
	}{
		{"i folds cyrillic", `привет`, "i", "ПРИВЕТ", true, 0},
		{"index is in runes not bytes", `вет`, "", "привет", true, 3},
		{"unicode boundary matches a whole word", `\bменю\b`, "i", "меню", true, 0},
		{"unicode boundary inside a phrase", `\bменю\b`, "i", "главное меню сейчас", true, 8},
		{"unicode boundary rejects a prefix", `\bменю\b`, "i", "меньше", false, -1},
		{"latin boundary in cyrillic text", `\bcrm\b`, "i", "нужна crm срочно", true, 6},
		{"escaped slash is a literal slash", `\/operator`, "i", "нажми /operator", true, 6},
		{"negative lookahead rejects", `^(?!❌ Отмена).*$`, "", "❌ Отмена", false, -1},
		{"negative lookahead accepts", `^(?!❌ Отмена).*$`, "", "Продолжить", true, 0},
		{"caret is a line anchor", `^два`, "", "один\nдва", true, 5},
		{"farewell rune index", `пока|до свидан|\bbye\b|прощай`, "i", "ну пока", true, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, err := rx.Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile(/%s/%s): %v", tt.pattern, tt.flags, err)
			}
			got, err := r.MatchErr(tt.input)
			if err != nil {
				t.Fatalf("MatchErr(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("/%s/%s ~ %q = %v, want %v", tt.pattern, tt.flags, tt.input, got, tt.want)
			}
			if !tt.want {
				return
			}
			start, _, ok, err := r.FindIndexErr(tt.input)
			if err != nil || !ok {
				t.Fatalf("FindIndexErr(%q) ok=%v err=%v", tt.input, ok, err)
			}
			if start != tt.index {
				t.Errorf("/%s/%s.index(%q) = %d, want %d", tt.pattern, tt.flags, tt.input, start, tt.index)
			}
		})
	}
}

// TestAuthorFiles is SPEC §16.3: the author's own files, migrated. They are fixtures,
// not teaching material, so they live in testdata/ and are pinned verbatim — the
// examples/ programs are checked by "the shipped examples run" below.
func TestAuthorFiles(t *testing.T) {
	t.Parallel()

	t.Run("main.mzs prints 3 and evaluates to 3", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		in := corpusInterp(&out)
		prog, err := in.Compile("main.mzs", readExample(t, "testdata/main.mzs"))
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		// The migrated file spells the word boundary `\b`, not the `\\b` of §11.5, so
		// it must compile clean.
		if w := prog.Warnings(); len(w) != 0 {
			t.Errorf("Warnings = %v, want none", w)
		}
		v, err := in.Run(context.Background(), prog, nil)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if v.Kind() != mzs.KInt || v.Int() != 3 {
			t.Errorf("program value = %s, want 3 (from f(1,2))", v.Inspect())
		}
		if out.String() != "3" {
			t.Errorf("stdout = %q, want %q", out.String(), "3")
		}
	})

	t.Run("test is never called and returns nil below the length guard", func(t *testing.T) {
		t.Parallel()
		in := corpusInterp(io.Discard)
		src := readExample(t, "testdata/main.mzs") + "\ntest(\"да\")\n"
		v, err := in.Eval(context.Background(), src, nil)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		if !v.IsNil() {
			t.Errorf("test(\"да\") = %s, want nil (the if has no else)", v.Inspect())
		}
	})

	t.Run("the \\\\b of §11.5 still warns", func(t *testing.T) {
		t.Parallel()
		in := corpusInterp(io.Discard)
		prog, err := in.Compile("gotcha.mzs", `"еда" ~ /\\bеда\\b/`)
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		w := prog.Warnings()
		if len(w) == 0 {
			t.Fatalf(`Warnings = none; \\b inside a regex literal must warn (§11.5)`)
		}
		if !strings.Contains(w[0].Msg, "matches a literal backslash") {
			t.Errorf("Warnings[0] = %q, want the literal-backslash diagnostic", w[0].Msg)
		}
	})

	t.Run("one.mzs reports the =! typo", func(t *testing.T) {
		t.Parallel()
		in := corpusInterp(io.Discard)
		_, err := in.Compile("one.mzs", readExample(t, "testdata/one.mzs"))
		if err == nil {
			t.Fatal(`Compile succeeded; str =! "x" must be a syntax error`)
		}
		const want = `one.mzs:3:6: syntax: unexpected '!' after '='; did you mean '!='?`
		if got := firstError(t, err).Error(); got != want {
			t.Errorf("Compile error = %q, want %q", got, want)
		}
	})

	t.Run("the s.md notes are valid mzs", func(t *testing.T) {
		t.Parallel()
		src := strings.Join([]string{
			`a := 1.2`,
			`b := "a"`,
			`c := 1`,
			`d := {a: 1}`,
			`e := [1, 2, "3"]`,
			`if $__sent.int > 5 { print("big") }`,
		}, "\n")
		var out bytes.Buffer
		in := corpusInterp(&out)
		if _, err := in.Eval(context.Background(), src, bind(map[string]string{"__sent": "9"})); err != nil {
			t.Fatalf("Eval: %v", err)
		}
		if out.String() != "big" {
			t.Errorf("stdout = %q, want %q", out.String(), "big")
		}
	})

	t.Run("the shipped examples run", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			file string
			vars map[string]string
			want string   // the program value, when the file ends in an expression
			out  []string // fragments stdout must contain
		}{
			{"values", "examples/01_values_and_operators.mzs", nil, "",
				[]string{"7 / 2", "3.5", "9223372036854776000.0", `"привет".index(/при/)`}},
			{"control flow", "examples/02_control_flow.mzs", nil, "",
				[]string{"stopped because it reached 1", "longest odd start under 300: 231"}},
			{"match", "examples/03_match_dispatch.mzs", nil, "",
				[]string{"moved permanently", "handoff", "express freight", "grades: A B C F"}},
			{"strings", "examples/04_strings_unicode.mzs", nil, "",
				[]string{"привет", "тевирп", "Иван Петров", "05.03.2026"}},
			{"arrays", "examples/05_arrays_pipeline.mzs", nil, "",
				[]string{"15 transactions", "1. ann", "big tickets"}},
			{"dicts", "examples/06_dicts_records.mzs", nil, "",
				[]string{"deep merge keeps them", `"port":443`, "headcount by dept"}},
			{"functions", "examples/07_functions_closures.mzs", nil, "",
				[]string{"quicksort: [1,2,3,4,7,8,9]", "mutual recursion", "DONE!"}},
			{"errors", "examples/08_errors_and_validation.mzs", nil, "",
				[]string{"zero-division", "declined (no_items)", "retry: succeeded on attempt 3"}},
			{"host variables", "examples/09_host_variables.mzs",
				map[string]string{"__sent": "  OPERATOR ", "price": "1500"}, "false",
				[]string{"handoff_operator", `"score":15`}},
			{"regex", "examples/10_regex_toolkit.mzs", nil, "",
				[]string{"Lee, Ann", "05/03/2026", "11 tokens"}},
			{"log parser", "examples/11_log_parser.mzs", nil, "",
				[]string{"parsed 29 entries", "POST /payment", "circuit-break"}},
			{"csv", "examples/12_csv_report.mzs", nil, "",
				[]string{`"Petrov, Ivan"`, "round trip preserves every field: true"}},
			{"word frequency", "examples/13_word_frequency.mzs", nil, "",
				[]string{"hapax legomena", "repeated bigrams", "concordance"}},
			{"layout", "examples/14_text_layout.mzs", nil, "",
				[]string{"INVOICE A-1043", "MORZE BARBERSHOP"}},
			{"json", "examples/15_json_shaping.mzs", nil, "",
				[]string{"round trip is lossless: true", "a cycle raises"}},
			{"intent router", "examples/16_intent_router.mzs", nil, "booking",
				[]string{"booking", `"time":"14:00"`}},
			{"state machine", "examples/17_state_machine.mzs", nil, "",
				[]string{"Booked! See you tomorrow at 14:30", "too many retries"}},
			{"orders", "examples/18_order_pipeline.mzs", nil, "",
				[]string{"4 accepted", "unknown sku", "★ repeat customer"}},
			{"inventory", "examples/19_inventory_ledger.mzs", nil, "",
				[]string{"balances agree with the FIFO lots: true", "reorder 12 units", "cost of sales"}},
			{"leaderboard", "examples/20_leaderboard.mzs", nil, "",
				[]string{"ordinal (1234) order: ann > bob > cleo", "head to head"}},
			{"matrix", "examples/21_matrix_ops.mzs", nil, "",
				[]string{"det(M)   = -13", "(A·B)ᵀ == Bᵀ·Aᵀ:       true"}},
			{"life", "examples/22_game_of_life.mzs", nil, "",
				[]string{"blinker has period 2:                             true"}},
			{"bfs", "examples/23_maze_bfs.mzs", nil, "",
				[]string{"shortest path: 66 steps", "path length equals the BFS distance:    true"}},
			{"fuzzy", "examples/24_fuzzy_search.mzs", nil, "",
				[]string{`did you mean "install"?`, "4 people"}},
			{"numerals", "examples/25_roman_numerals.mzs", nil, "",
				[]string{"MMMCMXCIX", "every one agrees", "1 234 567"}},
			{"memoization", "examples/26_memoization.mzs", nil, "",
				[]string{"cache hits", "4-entry FIFO cache", "1332×"}},
			{"async", "examples/28_async_tasks.mzs", nil, "",
				[]string{"counter = 160", "nested tasks: [20,30,40]", "cannot await itself"}},
			{"destructuring", "examples/33_destructuring.mzs", nil, "",
				[]string{"a=2 b=1", "push 2 | push 3 | add | dup | mul → [25]",
					"index: destructuring expects 2 values, got 3", "moved on"}},
			{"bits and bytes", "examples/34_bits_and_bytes.mzs", nil, "",
				[]string{`clear WRITE           → 0101  ["read","execute"]`,
					"192.168.1.7    in 192.168.1.0/24 → true",
					"crc32(\"123456789\") = cbf43926  ✓", "bits that differ      → 14 of 32",
					"type(shl(1, 63))    → int"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var out bytes.Buffer
				in := corpusInterp(&out)
				prog, err := in.Compile(tt.file, readExample(t, tt.file))
				if err != nil {
					t.Fatalf("Compile(%s): %v", tt.file, err)
				}
				v, err := in.Run(context.Background(), prog, bind(tt.vars))
				if err != nil {
					t.Fatalf("Run(%s): %v", tt.file, err)
				}
				if tt.want != "" && v.Str() != tt.want {
					t.Errorf("%s = %q, want %q", tt.file, v.Str(), tt.want)
				}
				for _, frag := range tt.out {
					if !strings.Contains(out.String(), frag) {
						t.Errorf("%s stdout does not contain %q:\n%s", tt.file, frag, out.String())
					}
				}
			})
		}
	})
}

// readExample reads one of the shipped programs of §16.3.
func readExample(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// firstError digs the first *mzs.Error out of what Compile returned. Compile joins
// several diagnostics when recovery found several (§13.5), and §5.6 asserts the first.
func firstError(t *testing.T, err error) *mzs.Error {
	t.Helper()
	var e *mzs.Error
	if !errors.As(err, &e) {
		t.Fatalf("error is %T (%v), want *mzs.Error", err, err)
	}
	return e
}

// ---------------------------------------------------------------------------
// §16.4 gotcha tests — one named test each, as the SPEC requires.
// ---------------------------------------------------------------------------

// evalCorpus evaluates src with the corpus interpreter and fails the test on error.
func evalCorpus(t *testing.T, src string, vars map[string]string) mzs.Value {
	t.Helper()
	v, err := corpusInterp(io.Discard).Eval(context.Background(), src, bind(vars))
	if err != nil {
		t.Fatalf("Eval(%s): %v", src, err)
	}
	return v
}

func TestTruthyZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want bool
	}{
		{`0`, true},
		{`0.0`, true},
		{`""`, true},
		{`[]`, true},
		{`{}`, true},
		{`"0"`, true},
		{`nil`, false},
		{`false`, false},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Truthy(); got != tt.want {
				t.Errorf("truthiness of %s = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestMatchIsBool(t *testing.T) {
	t.Parallel()

	m := evalCorpus(t, `"привет" ~ /привет/`, nil)
	if m.Kind() != mzs.KBool || !m.Bool() {
		t.Errorf(`"привет" ~ /привет/ = %s, want true (D5: ~ is always a Bool)`, m.Inspect())
	}

	i := evalCorpus(t, `"привет".index(/привет/)`, nil)
	if i.Kind() != mzs.KInt || i.Int() != 0 {
		t.Fatalf(`"привет".index(/привет/) = %s, want 0`, i.Inspect())
	}
	if !i.Truthy() {
		t.Error("a match at index 0 must still be truthy (D6)")
	}
}

func TestUnicodeLower(t *testing.T) {
	t.Parallel()

	if got := evalCorpus(t, `"ПРИВЕТ ЁЖ".lower`, nil).Str(); got != "привет ёж" {
		t.Errorf("lower = %q, want %q", got, "привет ёж")
	}
}

func TestNBSPTrim(t *testing.T) {
	t.Parallel()

	// U+00A0 on both sides: messengers send them and trim must remove them.
	if got := evalCorpus(t, `"`+nbsp+`да`+nbsp+`".trim`, nil).Str(); got != "да" {
		t.Errorf("trim = %q, want %q", got, "да")
	}
}

func TestEmptyInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want int64
	}{
		{`"".int`, 0},
		{`"".int + 1200`, 1200},
		{`"12abc".int`, 12},
		{`"abc".int`, 0},
		{`"  42 ".int`, 42},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Int(); got != tt.want {
				t.Errorf("%s = %d, want %d", tt.src, got, tt.want)
			}
		})
	}
}

func TestNoCoercion(t *testing.T) {
	t.Parallel()

	if v := evalCorpus(t, `1 == "1"`, nil); v.Bool() {
		t.Error(`1 == "1" is true; there is no coercion (§9.1)`)
	}
	if v := evalCorpus(t, `"2".int + 1`, nil); v.Int() != 3 {
		t.Errorf(`"2".int + 1 = %s, want 3`, v.Inspect())
	}

	_, err := corpusInterp(io.Discard).Eval(context.Background(), `"2" + 1`, nil)
	if err == nil {
		t.Fatal(`"2" + 1 returned no error; the conversion must be explicit`)
	}
	if got, want := firstError(t, err).Msg, "cannot add int to string"; got != want {
		t.Errorf(`"2" + 1 error = %q, want %q`, got, want)
	}
}

func TestApostropheValue(t *testing.T) {
	t.Parallel()

	v := evalCorpus(t, `$__sent == "О'Брайен"`, map[string]string{"__sent": "О'Брайен"})
	if !v.Bool() {
		t.Error("an apostrophe in a bound value must compare equal; values never reach the parser (§10)")
	}
}

func TestEmojiValue(t *testing.T) {
	t.Parallel()

	v := evalCorpus(t, `$__sent == "EN 🇬🇧"`, map[string]string{"__sent": "EN 🇬🇧"})
	if !v.Bool() {
		t.Error("an emoji in a bound value must compare equal")
	}
}

func TestUnboundGlobalIsNil(t *testing.T) {
	t.Parallel()

	if v := evalCorpus(t, `$not_existed == nil`, nil); !v.Bool() {
		t.Error("$not_existed != nil; an unbound global reads as nil, not as its own text (§9.2)")
	}
	if v := evalCorpus(t, `$not_existed`, nil); v.Truthy() {
		t.Error("$not_existed is truthy; nil is falsy, which is what makes it mean 'no match'")
	}
}

func TestClosureScope(t *testing.T) {
	t.Parallel()

	// `=` finds the outer binding, so the assignment inside the block is visible after it.
	if v := evalCorpus(t, `x = 0; if true { x = 1 }; x`, nil); v.Int() != 1 {
		t.Errorf("x = %s, want 1 (§8.2)", v.Inspect())
	}

	// A binding first created inside a block does not outlive it.
	_, err := corpusInterp(io.Discard).Eval(context.Background(), `if true { y = 1 }; y`, nil)
	if err == nil {
		t.Fatal("`if true { y = 1 }; y` compiled; every { … } is a scope (D2)")
	}
	if got, want := firstError(t, err).Msg, "undefined variable 'y'"; !strings.Contains(got, want) {
		t.Errorf("error = %q, want it to contain %q", got, want)
	}
}

func TestImplicitIt(t *testing.T) {
	t.Parallel()

	if v := evalCorpus(t, `[1,2,3].map { it * 2 } == [1,2,3].map { (x) -> x * 2 }`, nil); !v.Bool() {
		t.Error("the implicit `it` parameter and an explicit one must agree (§8.9)")
	}
}

func TestTrailingClosureIsArg(t *testing.T) {
	t.Parallel()

	const src = `double = { it * 2 }; [1,2,3].map(double) == [1,2,3].map { it * 2 }`
	if v := evalCorpus(t, src, nil); !v.Bool() {
		t.Error("a trailing closure is an ordinary last argument (§4.2)")
	}
}

func TestDictLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want string
	}{
		{`{a: 1}.json`, `{"a":1}`},
		{`{}.len`, "0"},
		{`[].len`, "0"},
		{`type({})`, "dict"},
		{`type([])`, "array"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestBraceIsAlwaysClosure(t *testing.T) {
	t.Parallel()

	// `{` after `if` opens the body, never a dict, so the dict literal needs no parens.
	if v := evalCorpus(t, `if {a: 1}.has("a") { 1 } else { 2 }`, nil); v.Int() != 1 {
		t.Errorf("= %s, want 1 (§3.11)", v.Inspect())
	}
}

func TestMatchFirstWins(t *testing.T) {
	t.Parallel()

	if got := evalCorpus(t, `match 5 { in 1..10 -> "a"; 5 -> "b"; else -> "c" }`, nil).Str(); got != "a" {
		t.Errorf("= %q, want %q; arms are tested top to bottom (§5.5)", got, "a")
	}
}

func TestMatchNoArm(t *testing.T) {
	t.Parallel()

	if v := evalCorpus(t, `match 99 { 1 -> "a" }`, nil); !v.IsNil() {
		t.Errorf("= %s, want nil; no matching arm and no else is nil (§5.5)", v.Inspect())
	}
}

func TestSubjectEvaluatedOnce(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	const src = `match say("side") { "x" -> 1; nil -> 2; else -> 3 }`
	v, err := corpusInterp(&out).Eval(context.Background(), src, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Int() != 2 {
		t.Errorf("= %s, want 2 (say returns nil)", v.Inspect())
	}
	if out.String() != "side\n" {
		t.Errorf("stdout = %q, want %q; the subject is evaluated exactly once", out.String(), "side\n")
	}
}

// TestDestructureMismatch is the §8.15 decision: a right side of the wrong shape raises
// at the assignment instead of filling the extra names with nil. A shape that does not
// fit is a bug where it is written, not three lines further down (D16).
func TestDestructureMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		kind string
		msg  string
	}{
		{`a, b = [1, 2, 3]`, "index", "destructuring expects 2 values, got 3"},
		{`a, b = [1]`, "index", "destructuring expects 2 values, got 1"},
		{`a, b = 1`, "type", "cannot destructure int: the right side must be an array"},
		{`a, b = {x: 1, y: 2}`, "type", "cannot destructure dict: the right side must be an array"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			_, err := corpusInterp(io.Discard).Eval(context.Background(), tt.src, nil)
			if err == nil {
				t.Fatalf("%s returned no error; a length mismatch is a run-time error", tt.src)
			}
			e := firstError(t, err)
			if e.Msg != tt.msg || e.Kind != tt.kind {
				t.Errorf("%s error = %s: %q, want %s: %q", tt.src, e.Kind, e.Msg, tt.kind, tt.msg)
			}
		})
	}
}

// TestArrayPatternBinds is the other half of §8.15: in a `match` arm the same shape
// binds rather than asserts, and a subject that does not fit simply moves to the next
// arm. This is the meaning §20 reserved the bare `[x, y]` arm for.
func TestArrayPatternBinds(t *testing.T) {
	t.Parallel()

	const src = `fn shape(o) { match o { [x, [y, z]] -> x + y + z; [x, y] -> x + y; [] -> 0; else -> -1 } }
		[shape([1, [2, 3]]), shape([1, 2]), shape([]), shape("no"), shape([1, 2, 3])].join(",")`
	if got := evalCorpus(t, src, nil).Str(); got != "6,3,0,-1,-1" {
		t.Errorf("= %q, want %q", got, "6,3,0,-1,-1")
	}

	// A name in the pattern binds; a literal in it still compares, so the arm an older
	// program wrote as an array literal keeps firing on the same values.
	if got := evalCorpus(t, `match [0, 7] { [0, n] -> n; else -> -1 }`, nil).Int(); got != 7 {
		t.Errorf("= %d, want 7; a literal element compares and a name binds", got)
	}
	if got := evalCorpus(t, `match [1, 2] { [1, 2] -> "eq"; else -> "no" }`, nil).Str(); got != "eq" {
		t.Errorf("= %q, want %q", got, "eq")
	}
}

func TestUfcsUserFn(t *testing.T) {
	t.Parallel()

	if got := evalCorpus(t, `fn shout(s) { s.upper + "!" }; "да".shout`, nil).Str(); got != "ДА!" {
		t.Errorf("= %q, want %q; x.f is f(x) when f is in scope (§4.3)", got, "ДА!")
	}
}

// TestBitOpsStayInt is the exception §12.5 writes into D9: arithmetic promotes an
// overflowing Int to a Float, and the bit rows do not. A bit shifted past the end is
// gone, not rounded — which is the whole reason masks and checksums come out right — and
// `bytes`/`pack_bytes` carry a string through the numbers and back unchanged.
func TestBitOpsStayInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a mask", `0x1234.band(0xff)`, "52"},
		{"flags together", `bor(0b0001, 0b0100).bit(2)`, "true"},
		{"the sign bit is still an int", `type(shl(1, 63))`, "int"},
		{"a bit shifted out is gone", `shl(1, 64)`, "0"},
		{"the same magnitude in arithmetic promotes", `type(2 ** 64)`, "float"},
		{"shr is arithmetic", `shr(-8, 1)`, "-4"},
		{"a byte round trip", `"héllo 🌲".bytes.pack_bytes`, "héllo 🌲"},
		{"bytes back into a number", `"\x01\x02".bytes.reduce(0) { (a, b) -> a.shl(8).bor(b) }`, "258"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}

	// Neither half of "no coercion" is negotiable here: a Float does not truncate into a
	// bit operation, and a value that is not a byte does not wrap into one.
	errs := []struct {
		src  string
		kind string
		msg  string
	}{
		{`2.9.band(1)`, "type", "band expects an int, got float: bit operations do not round, write x.int"},
		{`shl(1, -1)`, "argument", "shl: shift count -1 is negative; the other direction is shr"},
		{`bit(1, 64)`, "argument", "bit: index 64 is outside 0..63"},
		{`[65, 300].pack_bytes`, "argument", "pack_bytes: expected a byte in 0..255 at element 1, got 300"},
	}
	for _, tt := range errs {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			_, err := corpusInterp(io.Discard).Eval(context.Background(), tt.src, nil)
			if err == nil {
				t.Fatalf("%s returned no error", tt.src)
			}
			e := firstError(t, err)
			if e.Msg != tt.msg || e.Kind != tt.kind {
				t.Errorf("%s error = %s: %q, want %s: %q", tt.src, e.Kind, e.Msg, tt.kind, tt.msg)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	// The step budget is disabled so the deadline is what fires; with both on, a loop
	// this tight exhausts the budget first and the row would prove nothing about time.
	in := mzs.New(mzs.Options{Timeout: time.Second, StepBudget: -1})
	start := time.Now()
	_, err := in.Eval(context.Background(), `while true { }`, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, mzs.ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if elapsed > 1200*time.Millisecond {
		t.Errorf("took %s, want <= 1.2s", elapsed)
	}
}

func TestStepBudget(t *testing.T) {
	t.Parallel()

	in := mzs.New(mzs.Options{Timeout: 30 * time.Second, StepBudget: 1_000_000})
	_, err := in.Eval(context.Background(), `i = 0; while i < 1000000000 { i += 1 }; i`, nil)
	if !errors.Is(err, mzs.ErrBudget) {
		t.Fatalf("error = %v, want ErrBudget", err)
	}
}

func TestNoHostPanic(t *testing.T) {
	t.Parallel()

	rnd := rand.New(rand.NewSource(1))
	in := mzs.New(mzs.Options{Timeout: 100 * time.Millisecond, StepBudget: 100_000})
	ctx := context.Background()

	// A byte soup of the runes the corpus actually contains, so the fuzz explores
	// Cyrillic, emoji, quotes and operators rather than only ASCII noise.
	alphabet := []rune(`abc09 '"/\|&=~!<>+-*%.,;:?()[]{}#$_->привет🌲` + "\n\t")
	for i := 0; i < 10000; i++ {
		src := make([]rune, rnd.Intn(24))
		for j := range src {
			src[j] = alphabet[rnd.Intn(len(alphabet))]
		}
		s := string(src)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", s, r)
				}
			}()
			prog, err := in.Compile("fuzz", s)
			if err != nil {
				return
			}
			_, _ = in.Run(ctx, prog, nil)
		}()
	}
}

func TestIsolation(t *testing.T) {
	t.Parallel()

	in := mzs.New(mzs.Options{})
	prog, err := in.Compile("iso", `$x = $seed; $x`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 100; i++ {
		for _, seed := range []int64{1, 2} {
			wg.Add(1)
			go func(seed int64) {
				defer wg.Done()
				res, err := in.RunResult(context.Background(), prog,
					map[string]mzs.Value{"$seed": mzs.Int(seed)})
				if err != nil {
					errs <- err
					return
				}
				if res.Value.Int() != seed {
					errs <- fmt.Errorf("$x = %s, want %d", res.Value.Inspect(), seed)
				}
				if got := res.Globals["$x"]; got.Int() != seed {
					errs <- fmt.Errorf("Globals[$x] = %s, want %d", got.Inspect(), seed)
				}
			}(seed)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestHostGrantsTheFilesystem is §14.3 stated as an acceptance criterion rather than as
// prose: the same program reads a file or cannot name the module at all, and the only
// difference between the two is one field the *host* filled in. It goes through the
// public API alone, because that is the surface an embedder actually has.
func TestHostGrantsTheFilesystem(t *testing.T) {
	t.Parallel()

	const src = `include io
io.read("secret.txt").trim`

	if _, err := mzs.New(mzs.Options{}).Compile("t", src); err == nil {
		t.Fatal("the zero Options handed a script the filesystem")
	} else if !strings.Contains(err.Error(), "Options.FS") {
		t.Fatalf("error = %v; want it to name the field the host must install", err)
	}

	in := mzs.New(mzs.Options{Timeout: 2 * time.Second, FS: oneFile{"secret.txt", "да\n"}})
	prog, err := in.Compile("t", src)
	if err != nil {
		t.Fatalf("Compile with a host FS: %v", err)
	}
	v, err := in.Run(context.Background(), prog, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v.Str() != "да" {
		t.Fatalf("io.read = %q; want да", v.Str())
	}
}

// oneFile is the smallest mzs.FileSystem that means anything: one name, one content,
// everything else absent. A host writes this much to hand a script exactly what it meant
// to hand it and nothing else.
type oneFile struct{ name, content string }

func (f oneFile) Open(name string) (io.ReadCloser, error) {
	if name != f.name {
		return nil, fmt.Errorf("no such file")
	}
	return io.NopCloser(strings.NewReader(f.content)), nil
}

func (f oneFile) Create(string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("read-only")
}

func (f oneFile) Append(string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("read-only")
}

func (f oneFile) Stat(name string) (bool, int64, bool, error) {
	return name == f.name, int64(len(f.content)), false, nil
}

func (f oneFile) List(string) ([]string, error) { return []string{f.name}, nil }

func TestOneLiner(t *testing.T) {
	t.Parallel()

	tests := []struct{ src, want string }{
		{`fn f(a,b) { a += b; return a }; f(1,2)`, "3"},
		{`fn f(a,b) { a + b }; f(1,2)`, "3"},
		{`s = "  ДА "; s.lower.trim == "да"`, "true"},
		{`(0..6).map { it * 2 }.sum`, "42"},
		{`x = 1; x += 1 while x < 10; x`, "10"},
		{`[3,1,2].sort.join(",")`, "1,2,3"},
		{`match $__sent { "да" -> 1; else -> 0 }`, "0"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestLastExprIsResult(t *testing.T) {
	t.Parallel()

	tests := []struct{ src, want string }{
		{`1; 2; 3`, "3"},
		{`x = 5`, "5"},
		{`if true { 7 }`, "7"},
		{`(1; 2)`, "2"},
		{`fn f() { 9 }; f()`, "9"},
		{`return 4; 5`, "4"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			if got := evalCorpus(t, tt.src, nil).Str(); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestRegexBackendAgreement checks the RE2-safe patterns of §16.2 against Go's own
// engine over a sample of corpus-shaped inputs. It is an independent check of the
// byte→rune index conversion, which is the one place a silent off-by-N would hide.
func TestRegexBackendAgreement(t *testing.T) {
	t.Parallel()

	patterns := []string{
		`помощ|help|что умеешь|команд`,
		`эхо|echo|тест|ping|пинг`,
		`бесплатн.{0,14}(аудит|консультац|анализ|оценк|диагност)|бесплатно|пробн`,
		`классификац.{0,12}код|код.{0,12}(окпд|оквэд)`,
		`(Sun|Mon|Tue|Wed|Thu|Fri|Sat)`,
		`столов|кафе|кормя|питани|обед|завтрак|ужин|повар|кухн|тр[её]хразов`,
		`эта[пп]ы?|етап|процес|шаг[иове]?|сроки`,
	}
	// The sample is built from the fixtures below: every prefix of each — which is what
	// a user types character by character — plus each wrapped in the noise a messenger
	// adds. §16.2 asks for 500 strings; the guard below keeps it that way.
	seeds := []string{
		"", "помощь", "нужна ПОМОЩЬ срочно", "что умеешь?", "эхо", "PING",
		"бесплатная консультация", "бесплатно", "классификация кода оквэд",
		"Mon", "в среду Wed", "столовая", "трёхразовое питание", "🌲 обед 🌲",
		"привет мир", "Sun Mon Tue", "код окпд 2", "этапы работы", "какие сроки?",
		"шаги", "процесс внедрения", "❌ Отмена", "Оператор, помогите",
		"нужна помощь с оплатой заказа", "подскажите этапы работы над проектом",
		"хочу бесплатную консультацию по внедрению", "во сколько открывается столовая",
		"какие сроки внедрения и что входит в процесс", "ping pong эхо тест",
		"классификация кода ОКВЭД для кафе", "обед завтрак ужин повар кухня",
	}
	var inputs []string
	for _, s := range seeds {
		r := []rune(s)
		for i := 0; i <= len(r); i++ {
			inputs = append(inputs, string(r[:i]))
		}
		inputs = append(inputs, "  "+s+"  ", "«"+s+"»", s+"\n"+s)
	}
	if len(inputs) < 500 {
		t.Fatalf("sample is %d strings, want at least 500", len(inputs))
	}

	for _, p := range patterns {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			ours, err := rx.Compile(p, "i")
			if err != nil {
				t.Fatalf("rx.Compile: %v", err)
			}
			theirs, err := goRegexp(p)
			if err != nil {
				t.Fatalf("regexp.Compile: %v", err)
			}
			for _, in := range inputs {
				gotStart, _, gotOK, err := ours.FindIndexErr(in)
				if err != nil {
					t.Fatalf("FindIndexErr(%q): %v", in, err)
				}
				loc := theirs.FindStringIndex(in)
				if gotOK != (loc != nil) {
					t.Errorf("/%s/i ~ %q = %v, want %v", p, in, gotOK, loc != nil)
					continue
				}
				if loc == nil {
					continue
				}
				if want := len([]rune(in[:loc[0]])); gotStart != want {
					t.Errorf("/%s/i.index(%q) = %d, want rune index %d", p, in, gotStart, want)
				}
			}
		})
	}
}

// TestDiagnostics is A2: every row of SPEC §5.6 produces its message verbatim, at the
// right line and column, through the same Compile the host calls. The point of the table
// is that anyone pasting Ruby gets one precise fix-it rather than a cascade.
func TestDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		msg  string
		line int
		col  int
	}{
		{"unary minus in front of **", `-2 ** 2`,
			`ambiguous: write -(2 ** 2) or (-2) ** 2`, 1, 1},
		{"trailer on a range bound", `0..5.map { it }`,
			`ambiguous range: write (0..5).map`, 1, 2},
		{"chained range", `1..2..3`,
			`range operator is non-associative`, 1, 5},
		{"equality against a regex", `s == /re/`,
			`'==' with a regex operand: use '~' to match`, 1, 3},
		{"the Ruby match operator", `s =~ /re/`,
			`'=~' is not an mzs operator; use '~'`, 1, 3},
		{"predicate suffix", `x.empty?`,
			`'?' is not part of an identifier; did you mean 'empty'?`, 1, 8},
		{"a renamed method", `x.downcase`,
			`undefined method 'downcase'; did you mean 'lower'?`, 1, 3},
		{"and", `a and b`,
			`'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'`, 1, 3},
		{"or", `a or b`,
			`'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'`, 1, 3},
		{"not", `not a`,
			`'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'`, 1, 1},
		{"do and end", `if c do 1 end`,
			`'do'/'end' are not mzs keywords; use braces: if c { … }`, 1, 6},
		{"elsif", `elsif c { 1 }`,
			`'elsif' is not an mzs keyword; use 'else if'`, 1, 1},
		{"unless", `unless c { 1 }`,
			`'unless' is not an mzs keyword; use 'if !(c)'`, 1, 1},
		{"until", `until c { 1 }`,
			`'until' is not an mzs keyword; use 'while !(c)'`, 1, 1},
		{"loop", `loop { 1 }`,
			`'loop' is not an mzs keyword; use 'while true { … }'`, 1, 1},
		{"def", `def f() { }`,
			`'def' is not an mzs keyword; use 'fn'`, 1, 1},
		{"word array", `%w[a b]`,
			`'%w' is not mzs; write ["a", "b"]`, 1, 1},
		{"symbol", `:name`,
			`mzs has no symbols; write "name"`, 1, 1},
		{"bracket dict", `[a: 1]`,
			`a dict is written {a: 1}`, 1, 1},
		{"bracket empty dict", `[:]`,
			`the empty dict is written {}`, 1, 1},
		{"brace dict after a call", `f {a: 1}`,
			`a dict after a call is written (a: 1) or ({a: 1})`, 1, 3},
		{"brace dict in a body", `if c {a: 1}`,
			`this '{' opens the if body; write { {a: 1} } for a dict`, 1, 6},
		{"hash rocket", `k => v`,
			`'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure`, 1, 3},
		{"pipe closure parameters", `{ |x| x }`,
			`closure parameters are parenthesised: { (x) -> … }`, 1, 3},
		{"the Ruby safe call", `x &. y`,
			`'&.' is not an mzs operator; use '?.'`, 1, 3},
		{"bitwise and", `a & b`,
			`'&' is not an mzs operator; use band(a, b), or '&&' for logical and`, 1, 3},
		{"bitwise or", `a | b`,
			`'|' is not an mzs operator; use bor(a, b), or '||' for logical or`, 1, 3},
		{"bitwise xor", `a ^ b`,
			`'^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power`, 1, 3},
		{"rescue", `a rescue b`,
			`'rescue' is not an mzs keyword; use 'try a else b'`, 1, 3},
		{"hash interpolation", `"#{x}"`,
			`string interpolation is "${x}"`, 1, 2},
		{"the Ruby exclusive range", `1...5`,
			`'...' is not an mzs operator; use '..<'`, 1, 2},
		{"scope resolution", `a::B`,
			`'::' is not an mzs operator; use '.'`, 1, 2},
		{"the one.mzs typo", "a___1 = 13213\nbcde = 222\nstr =! \"sdfsdf\"",
			`unexpected '!' after '='; did you mean '!='?`, 3, 6},
		{"to_s", `x.to_s`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
		{"to_i", `x.to_i`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
		{"to_f", `x.to_f`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
		{"to_a", `x.to_a`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
		{"to_h", `x.to_h`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
		{"to_json", `x.to_json`,
			`undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'`, 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := corpusInterp(io.Discard).Compile("diag.mzs", tt.src)
			if err == nil {
				t.Fatalf("Compile(%q) = nil error, want %q", tt.src, tt.msg)
			}
			e := firstError(t, err)
			want := fmt.Sprintf("diag.mzs:%d:%d: syntax: %s", tt.line, tt.col, tt.msg)
			if got := e.Error(); got != want {
				t.Errorf("Compile(%q) = %q, want %q", tt.src, got, want)
			}
			if e.Line != tt.line || e.Col != tt.col {
				t.Errorf("Compile(%q) position = %d:%d, want %d:%d", tt.src, e.Line, e.Col, tt.line, tt.col)
			}
		})
	}
}

// BenchmarkCondition is the evidence for A4: the two shapes that make up ~99% of the
// corpus, measured through the API morzebot calls. The ruby fork they replace cost
// roughly 45 ms each.
func BenchmarkCondition(b *testing.B) {
	benches := []struct {
		name string
		expr string
		vars map[string]string
	}{
		{"equality", `$__sent == "да"`, map[string]string{"__sent": "да"}},
		{"lower trim equality", `$__sent.lower.trim == "оператор"`, map[string]string{"__sent": "  ОПЕРАТОР "}},
		{"re2 regex", `$__sent.lower ~ /привет|здравствуй|hello/i`, map[string]string{"__sent": "Привет"}},
		{"backtracking regex", `$__sent.lower ~ /\bменю\b|главное меню/i`, map[string]string{"__sent": "Меню"}},
		{"match ladder", `match $__sent.lower.trim { in ["да","ага"] -> 1; /^нет/ -> 0; else -> nil }`,
			map[string]string{"__sent": " АГА "}},
	}

	in := corpusInterp(io.Discard)
	ctx := context.Background()
	for _, bb := range benches {
		b.Run(bb.name, func(b *testing.B) {
			prog, err := in.Compile("bench", bb.expr)
			if err != nil {
				b.Fatalf("Compile: %v", err)
			}
			vars := bind(bb.vars)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := in.Run(ctx, prog, vars); err != nil {
					b.Fatalf("Run: %v", err)
				}
			}
		})
	}
}
