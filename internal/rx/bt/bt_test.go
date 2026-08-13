package bt

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

const testBudget = 200_000

// find is the shape every test wants: compile, match once from 0, report rune bounds.
func find(t *testing.T, pattern, flags, subject string) (start, end int, ok bool) {
	t.Helper()
	re, err := Compile(pattern, flags)
	if err != nil {
		t.Fatalf("Compile(/%s/%s) error = %v", pattern, flags, err)
	}
	gs, err := re.FindFrom([]rune(subject), 0, testBudget)
	if err != nil {
		t.Fatalf("FindFrom(%q) error = %v", subject, err)
	}
	if gs == nil {
		return 0, 0, false
	}
	return gs[0].Start, gs[0].End, true
}

// The whole reason this package exists: Onigmo's \b asks the encoding whether a rune
// is a word rune, so /\bменю\b/ matches in Ruby. Go's \b is ASCII-only and never
// would (SPEC §11.2, corpus row 24).
func TestUnicodeWordBoundary(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
		start   int
	}{
		{"cyrillic word alone", `\bменю\b`, "", "меню", true, 0},
		{"cyrillic word in a sentence", `\bменю\b`, "", "главное меню сегодня", true, 8},
		{"cyrillic prefix is not a whole word", `\bменю\b`, "", "меняю", false, 0},
		{"cyrillic suffix is not a whole word", `\bеда\b`, "", "победа", false, 0},
		{"eda as its own word", `\bеда\b`, "", "вкусная еда тут", true, 8},
		{"folded cyrillic word", `\bменю\b`, "i", "Меню", true, 0},
		{"upper cyrillic word", `\bоператор\b`, "i", "ПОЗОВИ ОПЕРАТОРА", false, 0},
		{"boundary before punctuation", `\bеда\b`, "", "еда, наконец", true, 0},
		{"boundary after a digit is inside a word", `\bеда\b`, "", "1еда", false, 0},
		{"ascii still works", `\bhi\b`, "", "say hi there", true, 4},
		{"ascii prefix rejected", `\bhi\b`, "", "shine", false, 0},
		{"non-boundary inside a cyrillic word", `\Bеда\B`, "", "победами", true, 3},
		{"non-boundary rejects a whole word", `\Bеда\B`, "", "еда", false, 0},
		{"emoji is not a word rune", `\bеда\b`, "", "🍲еда🍲", true, 1},
		{"underscore is a word rune", `\bеда\b`, "", "_еда", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, _, ok := find(t, tt.pattern, tt.flags, tt.subject)
			if ok != tt.want {
				t.Fatalf("/%s/%s.match(%q) = %v; want %v", tt.pattern, tt.flags, tt.subject, ok, tt.want)
			}
			if ok && start != tt.start {
				t.Errorf("/%s/.match(%q) start = %d; want %d (rune index)",
					tt.pattern, tt.subject, start, tt.start)
			}
		})
	}
}

// Rune indices, not byte indices (SPEC §11.3).
func TestRuneIndices(t *testing.T) {
	tests := []struct {
		name        string
		pattern     string
		flags       string
		subject     string
		start, stop int
	}{
		{"cyrillic offset", `\bвет`, "", "при вет", 4, 7},
		{"emoji offset", `\bb\b`, "", "🌲🇬🇧 b", 4, 5},
		{"lookahead keeps rune offsets", `при(?=вет)`, "", "ой привет", 3, 6},
		{"lookbehind keeps rune offsets", `(?<=при)вет`, "", "ой привет", 6, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := find(t, tt.pattern, tt.flags, tt.subject)
			if !ok {
				t.Fatalf("/%s/ did not match %q", tt.pattern, tt.subject)
			}
			if start != tt.start || end != tt.stop {
				t.Errorf("/%s/.match(%q) = (%d, %d); want (%d, %d)",
					tt.pattern, tt.subject, start, end, tt.start, tt.stop)
			}
		})
	}
}

func TestLookaround(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
	}{
		// The live corpus pattern: everything except a cancel button.
		{"negative lookahead rejects", `^(?!❌ Отмена).*$`, "", "❌ Отмена", false},
		{"negative lookahead accepts", `^(?!❌ Отмена).*$`, "", "Записаться", true},
		{"positive lookahead", `\d+(?= руб)`, "", "цена 500 руб", true},
		{"positive lookahead fails", `\d+(?= руб)`, "", "цена 500 $", false},
		{"negative lookbehind", `(?<!не )работает`, "", "работает", true},
		{"negative lookbehind rejects", `(?<!не )работает`, "", "не работает", false},
		{"positive lookbehind", `(?<=\$)\d+`, "", "цена $42", true},
		{"positive lookbehind fails", `(?<=\$)\d+`, "", "цена 42", false},
		{"variable width lookbehind", `(?<=abc|xy)z`, "", "xyz", true},
		{"lookahead inside alternation", `(?:да(?!вай)|нет)`, "", "давай", false},
		{"lookahead inside alternation accepts", `(?:да(?!вай)|нет)`, "", "да!", true},
		{"nested lookahead", `a(?=b(?!c))`, "", "abd", true},
		{"nested lookahead rejects", `a(?=b(?!c))`, "", "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := find(t, tt.pattern, tt.flags, tt.subject)
			if ok != tt.want {
				t.Errorf("/%s/%s.match(%q) = %v; want %v", tt.pattern, tt.flags, tt.subject, ok, tt.want)
			}
		})
	}
}

func TestBackreferences(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
		text    string
	}{
		{"doubled word", `(\w+) \1`, "", "the the cat", true, "the the"},
		{"doubled word absent", `(\w+) \1`, "", "the cat", false, ""},
		{"cyrillic doubled word", `(\p{Cyrillic}+) \1`, "", "да да", true, "да да"},
		{"folded backreference", `(\w+)-\1`, "i", "Abc-ABC", true, "Abc-ABC"},
		{"unfolded backreference", `(\w+)-\1`, "", "Abc-ABC", false, ""},
		{"named backreference", `(?<q>['"]).*?\k<q>`, "", `say "hi" now`, true, `"hi"`},
		{"backreference to a skipped group", `(?:(a)|b)\1`, "", "ba", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			rs := []rune(tt.subject)
			gs, err := re.FindFrom(rs, 0, testBudget)
			if err != nil {
				t.Fatalf("FindFrom error = %v", err)
			}
			if (gs != nil) != tt.want {
				t.Fatalf("match = %v; want %v", gs != nil, tt.want)
			}
			if gs == nil {
				return
			}
			if got := string(rs[gs[0].Start:gs[0].End]); got != tt.text {
				t.Errorf("match text = %q; want %q", got, tt.text)
			}
		})
	}
}

func TestAtomicAndPossessive(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		want    bool
	}{
		{"atomic group blocks the retry", `(?>a+)ab`, "aaab", false},
		{"plain group allows the retry", `(a+)ab`, "aaab", true},
		{"possessive star blocks the retry", `a*+b`, "aaab", true},
		{"possessive star cannot give back", `a*+a`, "aaa", false},
		{"possessive plus", `\d++x`, "123x", true},
		{"possessive optional", `a?+b`, "ab", true},
		{"possessive bounded", `a{1,3}+b`, "aab", true},
		{"atomic commits to its first success", `(?>a|ab)c`, "abc", false},
		{"plain group would have retried", `(?:a|ab)c`, "abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := find(t, tt.pattern, "", tt.subject)
			if ok != tt.want {
				t.Errorf("/%s/.match(%q) = %v; want %v", tt.pattern, tt.subject, ok, tt.want)
			}
		})
	}
}

// Ruby anchors: ^ and $ are line anchors always; \A, \z and \Z anchor the string.
func TestAnchorsAndFlags(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
	}{
		{"caret is a line anchor", `^b\b`, "", "a\nb", true},
		{"dollar is a line anchor", `\bb$`, "", "b\nc", true},
		{`\A anchors the string`, `\Ab\b`, "", "a\nb", false},
		{`\z anchors the string`, `\bb\z`, "", "b\nc", false},
		{`\Z allows a trailing newline`, `\bc\Z`, "", "ab c\n", true},
		{"dot stops at a newline", `a.b\b`, "", "a\nb", false},
		{"m makes dot match a newline", `a.b\b`, "m", "a\nb", true},
		{"i folds cyrillic", `\bпривет\b`, "i", "ПРИВЕТ", true},
		{"i folds the yo", `\bёж\b`, "i", "ЁЖ", true},
		{"x drops whitespace", `\b при вет \b`, "x", "привет", true},
		{"x drops comments", "\\bпри # a comment\nвет\\b", "x", "привет", true},
		{"x keeps an escaped space", `\bа\ б\b`, "x", "а б", true},
		{"whitespace inside a class survives x", `\ba[ ]b\b`, "x", "a b", true},
		{"inline flag applies to the rest", `\b(?i)меню\b`, "", "МЕНЮ", true},
		{"scoped inline flag ends", `(?i:мен)ю\b`, "", "МЕНЮ", false},
		{"scoped inline flag matches", `(?i:мен)ю\b`, "", "МЕНю", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := find(t, tt.pattern, tt.flags, tt.subject)
			if ok != tt.want {
				t.Errorf("/%s/%s.match(%q) = %v; want %v", tt.pattern, tt.flags, tt.subject, ok, tt.want)
			}
		})
	}
}

func TestCharacterClasses(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
	}{
		{"cyrillic range", `\b[а-яё]+\b`, "", "привет", true},
		{"cyrillic range folded", `\b[а-яё]+\b`, "i", "ПРИВЕТ", true},
		{"negated class", `\b[^аеиоуыэюя]+\b`, "", "ЁЖ", true},
		{"negated class rejects", `^[^а]+$`, "", "да", false},
		{"unicode property", `\b\p{Cyrillic}+\b`, "", "меню", true},
		{"negated unicode property", `\b\P{Cyrillic}+\b`, "", "menu", true},
		{"unicode letter property", `\b\p{L}+\b`, "", "привет", true},
		{"posix alpha is unicode aware", `\b[[:alpha:]]+\b`, "", "меню", true},
		{"posix digit", `\b[[:digit:]]+\b`, "", "42", true},
		{"class with a shorthand", `\b[\d_]+\b`, "", "1_2", true},
		{"negated shorthand in a class", `\b[\D]+\b`, "", "ab", true},
		{"escaped dash in a class", `^[a\-b]+$`, "", "a-b", true},
		{"literal bracket first", `^[]]$`, "", "]", true},
		{"class with a caret later", `^[a^]+$`, "", "^a", true},
		{"backspace escape in a class", "^[\\b]$", "", "\b", true},
		{"escaped slash", `\/operator\b`, "", "/operator", true},
		{"dot is literal when escaped", `^a\.b$`, "", "a.b", true},
		{"dot is literal when escaped, negative", `^a\.b$`, "", "axb", false},
		{"hex escape", `^\x41$`, "", "A", true},
		{"braced hex escape", `^\x{43F}$`, "", "п", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := find(t, tt.pattern, tt.flags, tt.subject)
			if ok != tt.want {
				t.Errorf("/%s/%s.match(%q) = %v; want %v", tt.pattern, tt.flags, tt.subject, ok, tt.want)
			}
		})
	}
}

func TestQuantifiers(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		want    bool
		text    string
	}{
		{"greedy star", `\ba.*b\b`, "a x b", true, "a x b"},
		{"lazy star", `\ba.*?b`, "a b b", true, "a b"},
		{"bounded", `\bбесплатн.{0,14}(аудит|консультац)`, "бесплатная консультация", true, "бесплатная консультац"},
		{"bounded overrun", `\bx.{0,2}y`, "xaaay", false, ""},
		{"exact count", `^a{3}$`, "aaa", true, "aaa"},
		{"open ended count", `^a{2,}$`, "aaaa", true, "aaaa"},
		{"open start count", `^a{,2}$`, "aa", true, "aa"},
		{"lazy bounded", `\ba{2,4}?\b`, "aaaa", true, "aaaa"},
		{"nested star does not spin", `^(a*)*$`, "aaa", true, "aaa"},
		{"empty body star terminates", `^(?:)*$`, "", true, ""},
		{"quantified anchor terminates", `^*a`, "a", true, "a"},
		// Onigmo ends a loop whose iteration consumed nothing and keeps what that
		// iteration captured; RE2 would report (0,1) for the group here.
		{"nullable body repeat", `(b*)+`, "b", true, "b"},
		{"optional group", `^(ab)?c$`, "c", true, "c"},
		{"literal brace", `^a{$`, "a{", true, "a{"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, "")
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			rs := []rune(tt.subject)
			gs, err := re.FindFrom(rs, 0, testBudget)
			if err != nil {
				t.Fatalf("FindFrom error = %v", err)
			}
			if (gs != nil) != tt.want {
				t.Fatalf("match = %v; want %v", gs != nil, tt.want)
			}
			if gs == nil {
				return
			}
			if got := string(rs[gs[0].Start:gs[0].End]); got != tt.text {
				t.Errorf("match text = %q; want %q", got, tt.text)
			}
		})
	}
}

func TestCaptureGroups(t *testing.T) {
	re, err := Compile(`\b(?<day>Mon|Tue)\s+(\d+)(?:\s+(x))?`, "")
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}
	rs := []rune("на Tue 14 в")
	gs, err := re.FindFrom(rs, 0, testBudget)
	if err != nil || gs == nil {
		t.Fatalf("FindFrom = %v, %v; want a match", gs, err)
	}

	tests := []struct {
		name        string
		index       int
		ok          bool
		start, stop int
	}{
		{"whole match", 0, true, 3, 9},
		{"named group", 1, true, 3, 6},
		{"positional group", 2, true, 7, 9},
		{"group that did not participate", 3, false, -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gs[tt.index]
			if g.Ok != tt.ok {
				t.Fatalf("group %d Ok = %v; want %v", tt.index, g.Ok, tt.ok)
			}
			if g.Start != tt.start || g.End != tt.stop {
				t.Errorf("group %d = (%d, %d); want (%d, %d)", tt.index, g.Start, g.End, tt.start, tt.stop)
			}
		})
	}

	if got, want := re.NumGroups(), 3; got != want {
		t.Errorf("NumGroups() = %d; want %d", got, want)
	}
	if got := re.GroupNames(); len(got) != 4 || got[0] != "" || got[1] != "day" || got[2] != "" {
		t.Errorf("GroupNames() = %q; want [\"\" \"day\" \"\" \"\"]", got)
	}
}

// Captures survive a positive lookaround and are rolled back by a negative one, which
// is what the shared journal in exec.go buys.
func TestCaptureRollback(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		group   int
		want    string
		ok      bool
	}{
		{"lookahead keeps its capture", `(?=(\d+))\d`, "42", 1, "42", true},
		{"failed branch rolls back", `(?:(a)x)?b`, "b", 1, "", false},
		{"negative lookahead rolls back", `(?!(a))b`, "b", 1, "", false},
		{"repeat keeps the last iteration", `(?:(\w)\w)+`, "abcd", 1, "c", true},
		// Onigmo's empty-loop rule: the iteration that matched nothing still counts as
		// the last one, so group 1 is the empty span it captured at rune 1.
		{"nullable repeat keeps its empty last iteration", `(b*)+`, "b", 1, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, "")
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			rs := []rune(tt.subject)
			gs, err := re.FindFrom(rs, 0, testBudget)
			if err != nil || gs == nil {
				t.Fatalf("FindFrom = %v, %v; want a match", gs, err)
			}
			g := gs[tt.group]
			if g.Ok != tt.ok {
				t.Fatalf("group %d Ok = %v; want %v", tt.group, g.Ok, tt.ok)
			}
			if !g.Ok {
				return
			}
			if got := string(rs[g.Start:g.End]); got != tt.want {
				t.Errorf("group %d = %q; want %q", tt.group, got, tt.want)
			}
		})
	}
}

// FindFrom is a leftmost search from an offset; internal/rx builds gsub and split on
// exactly this.
func TestFindFromOffset(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		from    int
		want    int
		ok      bool
	}{
		{"from zero", `\bа\b`, "а б а", 0, 0, true},
		{"past the first match", `\bа\b`, "а б а", 1, 4, true},
		{"past every match", `\bа\b`, "а б а", 5, 0, false},
		{"at the end of the string", `\b`, "ab", 2, 2, true},
		{"beyond the end", `\bа\b`, "а", 9, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, "")
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			gs, err := re.FindFrom([]rune(tt.subject), tt.from, testBudget)
			if err != nil {
				t.Fatalf("FindFrom error = %v", err)
			}
			if (gs != nil) != tt.ok {
				t.Fatalf("match = %v; want %v", gs != nil, tt.ok)
			}
			if gs != nil && gs[0].Start != tt.want {
				t.Errorf("start = %d; want %d", gs[0].Start, tt.want)
			}
		})
	}
}

// Untrusted bot authors write these patterns, so an exponential one must come back as
// an error in milliseconds rather than wedge the host (SPEC §11.2).
func TestStepBudget(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
	}{
		{"nested plus", `\b(a+)+$`, strings.Repeat("a", 40) + "!"},
		{"nested star alternation", `\b(a|aa)+$`, strings.Repeat("a", 40) + "!"},
		{"nested quantified group", `\b(a*)*b$`, strings.Repeat("a", 40) + "c"},
		{"overlapping alternation", `\b(x|x|x)+y$`, strings.Repeat("x", 30) + "z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, "")
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			gs, err := re.FindFrom([]rune(tt.subject), 0, 20_000)
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("FindFrom = %v, %v; want ErrBudget", gs, err)
			}
		})
	}

	t.Run("a benign pattern stays well inside the budget", func(t *testing.T) {
		re := mustCompile(t, `\bменю\b|главное меню`, "i")
		subject := []rune(strings.Repeat("это не то сообщение. ", 200) + "Меню")
		gs, err := re.FindFrom(subject, 0, testBudget)
		if err != nil {
			t.Fatalf("FindFrom error = %v", err)
		}
		if gs == nil {
			t.Fatalf("no match; want the trailing Меню")
		}
	})
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
	}{
		{"unbalanced group", "(a", ""},
		{"unmatched close", "a)", ""},
		{"unterminated class", "[a", ""},
		{"dangling quantifier", "*a", ""},
		{"quantifier with no target after a pipe", `a|*`, ""},
		{"bad flag", "a", "q"},
		{"reversed bounds", "a{3,1}", ""},
		{"bad property", `\p{Nope}`, ""},
		{"undefined named backref", `\k<none>`, ""},
		{"backref past the group count", `(a)\2`, ""},
		{"unbounded lookbehind", `(?<=a*)b`, ""},
		{"unsupported anchor", `\Ga`, ""},
		{"trailing backslash", `a\`, ""},
		{"bad group option", `(?y:a)`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Compile(tt.pattern, tt.flags); err == nil {
				t.Errorf("Compile(/%s/%s) = nil error; want an error", tt.pattern, tt.flags)
			}
		})
	}
}

// SPEC §16.2 — the production regex corpus. Every one of these ships in a live bot.
func TestSpecCorpus(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		want    bool
		start   int
	}{
		{"greeting", `привет|здравствуй|hello|\bhi\b`, "привет", true, 0},
		{"greeting hi", `привет|здравствуй|hello|\bhi\b`, "hi there", true, 0},
		{"greeting rejects hint", `привет|здравствуй|hello|\bhi\b`, "hint", false, 0},
		{"farewell rune index", `пока|до свидан|\bbye\b|прощай`, "ну пока", true, 3},
		{"echo misses", `эхо|echo|тест|ping|пинг`, "что?", false, 0},
		{"crm", `\bcrm\b|црм|клиентск.{0,8}баз`, "нужна crm", true, 6},
		{"crm rejects a substring", `\bcrm\b|црм|клиентск.{0,8}баз`, "crmx", false, 0},
		{"free audit", `бесплатн.{0,14}(аудит|консультац|анализ)|free.?audit`, "бесплатная консультация", true, 0},
		{"menu", `все темы|главное меню|\bменю\b|показать все|в начало`, "меню", true, 0},
		{"stages", `эта[пп]ы?|етап|\bкак\b.{0,16}(работа|внедр)|процес|шаг[иове]?|сроки`, "как это работает", true, 0},
		{"operator", `\bоператор|\boperator|\/operator|перевед.{0,12}оператор`, "/operator", true, 0},
		{"cases", `ке[ийс]с|кэйс|\bcase\b|пример|портфолио|резул[ьъ]?тат`, "покажите пример", true, 9},
		{"classifier codes", `классификац.{0,12}код|код.{0,12}(окпд|оквэд)`, "код оквэд", true, 0},
		{"not cancel", `^(?!❌ Отмена).*$`, "Записаться", true, 0},
		{"is cancel", `^(?!❌ Отмена).*$`, "❌ Отмена", false, 0},
		{"weekday", `(Sun|Mon|Tue|Wed|Thu|Fri|Sat)`, "on Fri", true, 3},
		{"food", `столов|кафе|\bеда\b|кормя|питани|обед|тр[её]хразов`, "трёхразовое питание", true, 0},
		{"food word only", `столов|кафе|\bеда\b|кормя|питани`, "победа", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, _, ok := find(t, tt.pattern, "i", tt.subject)
			if ok != tt.want {
				t.Fatalf("/%s/i.match(%q) = %v; want %v", tt.pattern, tt.subject, ok, tt.want)
			}
			if ok && start != tt.start {
				t.Errorf("start = %d; want %d", start, tt.start)
			}
		})
	}
}

// The engine must never panic, whatever it is handed; a bad pattern is an error.
func TestNoPanicOnJunk(t *testing.T) {
	junk := []string{
		"", "(", ")", "[", "]", "{", "}", "*", "+", "?", "|", "\\", "(?", "(?<",
		"(?<=", "(?#", "[[:", `\p{`, `\k<`, "a{1", "((((((((((", "[a-", `\`,
		"a**", "(?i", "(|)", "[]", "[^]", `\x{`, `(?<>)`, "🌲*", `\p`,
	}
	for _, p := range junk {
		t.Run(p, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Compile(%q) panicked: %v", p, r)
				}
			}()
			re, err := Compile(p, "")
			if err != nil {
				return
			}
			if _, err := re.FindFrom([]rune("а b 1 🌲"), 0, 1000); err != nil && !errors.Is(err, ErrBudget) {
				t.Errorf("FindFrom error = %v", err)
			}
		})
	}
}

// The same guarantee over random metacharacter soup: a bad pattern is an error and a
// good one terminates inside its budget.
func TestNoPanicOnRandomPatterns(t *testing.T) {
	alphabet := []rune(`ab*+?()[]{}|.^$\\/dwsбщ<>=!:,-1kpPBAzZ`)
	rnd := rand.New(rand.NewSource(7))
	subject := []rune("аб ab 12 🌲\n")
	for i := 0; i < 20000; i++ {
		n := 1 + rnd.Intn(12)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteRune(alphabet[rnd.Intn(len(alphabet))])
		}
		pat := sb.String()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("pattern %q panicked: %v", pat, r)
				}
			}()
			re, err := Compile(pat, "")
			if err != nil {
				return
			}
			if _, err := re.FindFrom(subject, 0, 5000); err != nil && !errors.Is(err, ErrBudget) {
				t.Errorf("FindFrom(/%s/) error = %v", pat, err)
			}
		}()
	}
}

// The two shapes that are 100% of the hot path: a literal compare and an
// alternation of Cyrillic words with a Unicode \b (SPEC §16 distribution note).
func BenchmarkCorpusAlternation(b *testing.B) {
	re, err := Compile(`\bменю\b|главное меню|показать все|в начало`, "i")
	if err != nil {
		b.Fatal(err)
	}
	subject := []rune("покажите пожалуйста главное меню бота")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := re.FindFrom(subject, 0, testBudget); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCorpusNoMatch(b *testing.B) {
	re, err := Compile(`\bоператор|\boperator|\/operator|перевед.{0,12}оператор`, "i")
	if err != nil {
		b.Fatal(err)
	}
	subject := []rune("здравствуйте, подскажите сколько стоит подписка на месяц?")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := re.FindFrom(subject, 0, testBudget); err != nil {
			b.Fatal(err)
		}
	}
}

func mustCompile(t *testing.T, pattern, flags string) *Regexp {
	t.Helper()
	re, err := Compile(pattern, flags)
	if err != nil {
		t.Fatalf("Compile(/%s/%s) error = %v", pattern, flags, err)
	}
	return re
}
