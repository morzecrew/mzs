package bt

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// goPattern renders the RE2 spelling of a Ruby pattern that uses none of the
// constructs RE2 lacks: Ruby's ^/$ are line anchors, so (?m) is always on, and Ruby's
// m is RE2's s. This mirrors internal/rx/translate.go, which is what makes the
// comparison meaningful — the two backends must agree wherever both can run.
func goPattern(pattern, flags string) string {
	var b strings.Builder
	b.WriteString("(?m")
	if strings.ContainsRune(flags, 'i') {
		b.WriteByte('i')
	}
	if strings.ContainsRune(flags, 'm') {
		b.WriteByte('s')
	}
	b.WriteByte(')')
	b.WriteString(strings.ReplaceAll(pattern, "(?<", "(?P<"))
	return b.String()
}

// agree compares the leftmost match and every capture span against the stdlib engine.
func agree(t *testing.T, pattern, flags, subject string) {
	t.Helper()
	re, err := Compile(pattern, flags)
	if err != nil {
		t.Fatalf("bt.Compile(/%s/%s) error = %v", pattern, flags, err)
	}
	gore, err := regexp.Compile(goPattern(pattern, flags))
	if err != nil {
		t.Fatalf("regexp.Compile(%q) error = %v", goPattern(pattern, flags), err)
	}

	rs := []rune(subject)
	got, err := re.FindFrom(rs, 0, 1_000_000)
	if err != nil {
		t.Fatalf("FindFrom(/%s/, %q) error = %v", pattern, subject, err)
	}
	want := gore.FindStringSubmatchIndex(subject)

	if (got == nil) != (want == nil) {
		t.Fatalf("/%s/%s on %q: bt matched %v, re2 matched %v",
			pattern, flags, subject, got != nil, want != nil)
	}
	if got == nil {
		return
	}
	// re2 reports byte offsets; the whole codebase is rune-indexed.
	toRune := func(b int) int {
		if b < 0 {
			return -1
		}
		return len([]rune(subject[:b]))
	}
	for i := 0; i < len(want)/2 && i < len(got); i++ {
		ws, we := toRune(want[2*i]), toRune(want[2*i+1])
		gs, ge := -1, -1
		if got[i].Ok {
			gs, ge = got[i].Start, got[i].End
		}
		if gs != ws || ge != we {
			t.Errorf("/%s/%s on %q: group %d = (%d,%d); re2 = (%d,%d)",
				pattern, flags, subject, i, gs, ge, ws, we)
		}
	}
}

// The corpus below is deliberately RE2-safe: no \b, no lookaround, no backreference.
// Everything else in the pattern language must behave identically in both backends
// (SPEC §11.2, TestRegexBackendAgreement).
var agreePatterns = []struct{ pattern, flags string }{
	{`привет|здравствуй|hello`, "i"},
	{`пока|до свидан|прощай`, "i"},
	{`помощ|help|что умеешь|команд`, "i"},
	{`эхо|echo|тест|ping|пинг`, "i"},
	{`клиентск.{0,8}баз|управлени.{0,10}клиент`, "i"},
	{`бесплатн.{0,14}(аудит|консультац|анализ|оценк)|free.?audit|пробн`, "i"},
	{`все темы|главное меню|показать все|в начало`, "i"},
	{`эта[пп]ы?|етап|процес|шаг[иове]?|сроки`, "i"},
	{`(Sun|Mon|Tue|Wed|Thu|Fri|Sat)`, ""},
	{`столов|кафе|кормя|питани|обед|тр[её]хразов`, "i"},
	{`^\d+$`, ""},
	{`^[а-яё]+$`, "i"},
	{`[a-z]+@[a-z]+\.[a-z]{2,4}`, "i"},
	{`(\d+)-(\d+)`, ""},
	{`(?<day>\d{2})/(?<mon>\d{2})/(?<year>\d{2})`, ""},
	{`a.*b`, ""},
	{`a.*?b`, ""},
	{`a.+b`, "m"},
	{`^.$`, ""},
	{`(a|b)*c`, ""},
	{`(?:ab)+`, ""},
	{`x{2,3}`, ""},
	{`x{2,}`, ""},
	{`x{0,2}y`, ""},
	{`\s+`, ""},
	{`\d\D\d`, ""},
	{`[^аеиоуыэюя ]+`, ""},
	{`\p{Cyrillic}+`, ""},
	{`\p{Lu}\p{Ll}+`, ""},
	{`^$`, ""},
	{`(a)(b)?(c)`, ""},
	{`(a+)(a*)`, ""},
	{`ц|цы|цыпа`, ""},
	{`(?i:МЕНЮ)`, ""},
	{`\/operator`, ""},
	{`[.]{3}`, ""},
	{`^(\w+):(.*)$`, ""},
	{`(?:да|нет)!*`, "i"},
	{`.*`, ""},
	{`a?b?c?`, ""},
}

var agreeSubjects = []string{
	"", "a", "ab", "abc", "aaab", "xxy", "x", "xx", "xxx", "xxxx",
	"привет", "Привет", "ПРИВЕТ", "ну пока", "что?", "меню", "Меню", "МЕНЮ",
	"главное меню", "все темы", "бесплатная консультация", "free-audit",
	"нужна crm", "победа", "еда", "вкусная еда тут", "трёхразовое питание",
	"12/03/25", "800", "1500", "", "12-34", "ivan:i@x.ru", "Иван Петров",
	"a\nb", "a\n\nb", "line1\nline2\n", "\n", " ", "  \t ", "О'Брайен",
	"RU 🇷🇺", "🌲", "EN 🇬🇧 test", "/operator", "/start", "Sun Mon Tue",
	"этапы работы", "шаги", "сроки внедрения", "цыпа", "ц", "цы",
	"Elite Plus (350k)", "Orange & Lime", "...", "a.b", "ЁЖ", "ёж",
}

func TestAgreementWithRE2(t *testing.T) {
	for _, p := range agreePatterns {
		t.Run(p.pattern+"/"+p.flags, func(t *testing.T) {
			for _, s := range agreeSubjects {
				agree(t, p.pattern, p.flags, s)
			}
		})
	}
}

// A generated corpus catches the cases a hand-written table never thinks of. The
// alphabet stays inside the constructs where Ruby and RE2 are defined to agree.
func TestAgreementGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("generated agreement corpus is slow")
	}
	rnd := rand.New(rand.NewSource(20260809))
	subjects := []string{"", "a", "b", "ab", "ba", "aab", "abab", "bbb", "aaa",
		"ц", "aц", "цa", "цц", "a\nb", "abcab", "cab", "abc", "cc", "acbacb"}

	for i := 0; i < 4000; i++ {
		pat := genPattern(rnd, 0)
		flags := ""
		if rnd.Intn(4) == 0 {
			flags = "i"
		}
		if _, err := regexp.Compile(goPattern(pat, flags)); err != nil {
			continue // the generator can emit something RE2 rejects; nothing to compare
		}
		re, err := Compile(pat, flags)
		if err != nil {
			t.Errorf("bt.Compile(/%s/%s) = %v; re2 accepted it", pat, flags, err)
			continue
		}
		// Deliberate divergence: a repeat over a nullable body is where Onigmo and RE2
		// disagree about the last iteration's captures (see star() in compile.go). bt
		// follows Ruby, so there is nothing to compare.
		p := &parser{src: []rune(pat), names: []string{""}}
		if root, perr := p.parse(); perr == nil && hasNullableRepeat(root) {
			continue
		}
		_ = re
		for _, s := range subjects {
			agree(t, pat, flags, s)
		}
	}
}

func hasNullableRepeat(n node) bool {
	switch t := n.(type) {
	case *nRepeat:
		return nullable(t.sub) || hasNullableRepeat(t.sub)
	case *nCat:
		for _, s := range t.subs {
			if hasNullableRepeat(s) {
				return true
			}
		}
	case *nAlt:
		for _, a := range t.alts {
			if hasNullableRepeat(a) {
				return true
			}
		}
	case *nGroup:
		return hasNullableRepeat(t.sub)
	case *nAtomic:
		return hasNullableRepeat(t.sub)
	case *nLook:
		return hasNullableRepeat(t.sub)
	}
	return false
}

// genPattern builds a small random pattern from the RE2-safe subset.
func genPattern(rnd *rand.Rand, depth int) string {
	if depth > 2 {
		return genAtom(rnd)
	}
	switch rnd.Intn(10) {
	case 0, 1:
		return genPattern(rnd, depth+1) + "|" + genPattern(rnd, depth+1)
	case 2, 3:
		return genPattern(rnd, depth+1) + genPattern(rnd, depth+1)
	case 4:
		return "(" + genPattern(rnd, depth+1) + ")"
	case 5:
		return "(?:" + genPattern(rnd, depth+1) + ")"
	case 6:
		return genAtom(rnd) + genQuant(rnd)
	case 7:
		return "(" + genPattern(rnd, depth+1) + ")" + genQuant(rnd)
	}
	return genAtom(rnd)
}

func genAtom(rnd *rand.Rand) string {
	atoms := []string{"a", "b", "c", "ц", ".", "[ab]", "[^a]", `\d`, `\w`, "^", "$", "[a-c]"}
	return atoms[rnd.Intn(len(atoms))]
}

func genQuant(rnd *rand.Rand) string {
	quants := []string{"*", "+", "?", "*?", "+?", "??", "{2}", "{1,2}", "{0,3}", "{2,}"}
	return quants[rnd.Intn(len(quants))]
}
