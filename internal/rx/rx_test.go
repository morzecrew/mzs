package rx

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// SPEC §11.3: every index is a rune index. `"привет" =~ /вет/` must be 3, not 6.
func TestFindIndexIsRuneBased(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		flags     string
		subject   string
		wantStart int
		wantEnd   int
		wantOK    bool
	}{
		{"cyrillic offset", "вет", "", "привет", 3, 6, true},
		{"match at zero is a match", "привет", "", "привет", 0, 6, true},
		{"ascii", "lo", "", "hello", 3, 5, true},
		{"emoji offset", "b", "", "🌲🇬🇧b", 3, 4, true},
		{"alternation picks the leftmost", "пока|до свидан", "i", "ну пока", 3, 7, true},
		{"case folds cyrillic", "оператор", "i", "ОПЕРАТОР", 0, 8, true},
		{"no match", "эхо|echo", "i", "что?", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile(/%s/%s) error = %v", tt.pattern, tt.flags, err)
			}
			start, end, ok := re.FindIndex(tt.subject)
			if ok != tt.wantOK {
				t.Fatalf("FindIndex(%q) ok = %v; want %v", tt.subject, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("FindIndex(%q) = (%d, %d); want (%d, %d)",
					tt.subject, start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// Ruby's ^ and $ are line anchors always; \A and \z anchor the whole string (§11.1).
func TestAnchors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		subject string
		want    bool
	}{
		{"caret is a line anchor", "^b", "a\nb", true},
		{"dollar is a line anchor", "b$", "b\nc", true},
		{`\A anchors the string`, `\Ab`, "a\nb", false},
		{`\z anchors the string`, `b\z`, "b\nc", false},
		{`\Z allows a trailing newline`, `c\Z`, "abc\n", true},
		{"dot does not cross a newline", "a.b", "a\nb", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, "")
			if err != nil {
				t.Fatalf("Compile(/%s/) error = %v", tt.pattern, err)
			}
			if got := re.Match(tt.subject); got != tt.want {
				t.Errorf("/%s/.Match(%q) = %v; want %v", tt.pattern, tt.subject, got, tt.want)
			}
		})
	}
}

func TestFlags(t *testing.T) {
	tests := []struct {
		name      string
		flags     string
		wantFlags string
		wantErr   bool
	}{
		{"none", "", "", false},
		{"case insensitive", "i", "i", false},
		{"ruby s folds into m", "s", "m", false},
		{"m stays m", "m", "m", false},
		{"duplicates collapse", "iii", "i", false},
		{"canonical order", "mi", "im", false},
		{"u is accepted and inert", "iu", "iu", false},
		{"unknown letter", "z", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile("a", tt.flags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Compile(/a/%s) error = %v; wantErr %v", tt.flags, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := re.Flags(); got != tt.wantFlags {
				t.Errorf("Flags() = %q; want %q", got, tt.wantFlags)
			}
			if got, want := re.String(), "/a/"+tt.wantFlags; got != want {
				t.Errorf("String() = %q; want %q", got, want)
			}
		})
	}
}

// Dotall, extended mode and the escaped delimiter are all resolved at translation
// time, so RE2 never sees a Ruby-only construct.
func TestTranslation(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
	}{
		{"m makes dot match newline", "a.b", "m", "a\nb", true},
		{"escaped slash is a literal slash", `\/operator`, "", "/operator", true},
		{"x drops whitespace", "прив ет", "x", "привет", true},
		{"x drops comments", "прив # a comment\nет", "x", "привет", true},
		{"whitespace inside a class survives x", "a[ ]b", "x", "a b", true},
		{"named group", `(?<w>ab)`, "", "ab", true},
		{"posix class", "[[:digit:]]+", "", "42", true},
		{"unicode property", `\p{Cyrillic}+`, "", "меню", true},
		{"bracket ranges", "[а-яё]+", "i", "ЁЖ", true},
		{"non greedy", "a.+?b", "", "axxb", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile(/%s/%s) error = %v", tt.pattern, tt.flags, err)
			}
			if got := re.Match(tt.subject); got != tt.want {
				t.Errorf("/%s/%s.Match(%q) = %v; want %v",
					tt.pattern, tt.flags, tt.subject, got, tt.want)
			}
		})
	}
}

// Backend selection is what the regex-engine implementer builds against: these
// patterns MUST reach bt.Compile, and only these.
func TestBackendSelection(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"plain alternation stays on re2", "привет|hello", false},
		{"character class stays on re2", "[а-яА-Я]+", false},
		{"quantifiers stay on re2", "бесплатн.{0,14}(аудит|консультац)", false},
		{"word boundary needs bt", `\bменю\b`, true},
		{"non-word boundary needs bt", `\Bx`, true},
		{"backspace inside a class does not", `[\b]`, false},
		{"negative lookahead needs bt", "^(?!❌ Отмена).*$", true},
		{"positive lookahead needs bt", "a(?=b)", true},
		{"lookbehind needs bt", "(?<=a)b", true},
		{"named group is not lookbehind", "(?<name>a)", false},
		{"backreference needs bt", `(a)\1`, true},
		{"named backreference needs bt", `(?<a>x)\k<a>`, true},
		{"atomic group needs bt", "(?>ab)", true},
		{"possessive quantifier needs bt", "a*+", true},
		{"lazy quantifier does not", "a*?", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scanPattern(tt.pattern).needsBT(); got != tt.want {
				t.Errorf("scanPattern(%q).needsBT() = %v; want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestFindSubmatchAndGroups(t *testing.T) {
	re, err := Compile(`(?<day>Mon|Tue)\s+(\d+)`, "")
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}
	groups, ok := re.FindSubmatch("на Tue 14 в")
	if !ok {
		t.Fatalf("FindSubmatch found nothing")
	}

	tests := []struct {
		name  string
		index int
		text  string
		gname string
		start int
	}{
		{"whole match", 0, "Tue 14", "", 3},
		{"named group", 1, "Tue", "day", 3},
		{"positional group", 2, "14", "", 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := groups[tt.index]
			if !g.Ok {
				t.Fatalf("group %d did not participate", tt.index)
			}
			if g.Text != tt.text {
				t.Errorf("group %d text = %q; want %q", tt.index, g.Text, tt.text)
			}
			if g.Name != tt.gname {
				t.Errorf("group %d name = %q; want %q", tt.index, g.Name, tt.gname)
			}
			if g.Start != tt.start {
				t.Errorf("group %d start = %d; want %d (rune index)", tt.index, g.Start, tt.start)
			}
		})
	}
	if got := re.NumGroups(); got != 2 {
		t.Errorf("NumGroups() = %d; want 2", got)
	}
}

func TestReplaceSplitAndFindAll(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		repl    string
		all     bool
		want    string
	}{
		{"gsub drops apostrophes", "'", "", "О'Брай'ен", "", true, "ОБрайен"},
		{"sub replaces the first only", "а", "", "мама", "о", false, "мома"},
		{"group reference", `(\d+)-(\d+)`, "", "12-34", `\2:\1`, true, "34:12"},
		{"whole match reference", `\d+`, "", "a1b", `[\0]`, true, "a[1]b"},
		{"named reference", `(?<n>\d+)`, "", "a1", `<\k<n>>`, true, "a<1>"},
		{"escaped backslash", "a", "", "a", `\\`, true, `\`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			if got := re.Replace(tt.subject, tt.repl, tt.all); got != tt.want {
				t.Errorf("Replace(%q, %q, %v) = %q; want %q",
					tt.subject, tt.repl, tt.all, got, tt.want)
			}
		})
	}

	t.Run("split", func(t *testing.T) {
		re := MustCompile(`\s*,\s*`, "")
		got := strings.Join(re.Split("а , б,в", -1), "|")
		if got != "а|б|в" {
			t.Errorf("Split = %q; want %q", got, "а|б|в")
		}
	})

	t.Run("find all counts non-overlapping matches", func(t *testing.T) {
		re := MustCompile("aa", "")
		if got := len(re.FindAll("aaaa", -1)); got != 2 {
			t.Errorf("FindAll = %d matches; want 2", got)
		}
	})

	t.Run("find all honours the limit", func(t *testing.T) {
		re := MustCompile("a", "")
		if got := len(re.FindAll("aaaa", 2)); got != 2 {
			t.Errorf("FindAll(limit 2) = %d matches; want 2", got)
		}
	})
}

// §11.5: a doubled backslash in a regex literal matches a literal backslash. mzs warns
// rather than silently repairing it, because main.mzs really does contain `\\bеда\\b`.
func TestLintPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    int
	}{
		{"clean", `\bменю\b`, 0},
		{"doubled b", `столов|\\bеда\\b`, 1},
		{"several distinct classes", `\\b|\\d|\\w`, 3},
		{"an escaped backslash before a letter is fine", `\\x`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(LintPattern(tt.pattern)); got != tt.want {
				t.Errorf("LintPattern(%q) = %d warnings; want %d", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
	}{
		{"unbalanced group", "(a", ""},
		{"unterminated class", "[a", ""},
		{"bad flag", "a", "q"},
		{"dangling quantifier", "*a", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Compile(tt.pattern, tt.flags); err == nil {
				t.Errorf("Compile(/%s/%s) = nil error; want an error", tt.pattern, tt.flags)
			}
		})
	}
}

// The audit's quadratic gsub: each match scans the rest of the subject in a lookahead,
// so a fresh budget per match made one call cost O(n²) steps — 11 s for 40 000 runes —
// without ever running out. One call is one budget now.
func TestOneBudgetPerCall(t *testing.T) {
	re := MustCompile(`a(?=[^z]*z|)`, "")
	s := strings.Repeat("a", 40_000)
	start := time.Now()
	_, err := re.ReplaceErr(s, "b", true)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("ReplaceErr error = %v; want ErrBudget", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("ReplaceErr took %s; the budget should stop it in milliseconds", d)
	}
	for name, f := range map[string]func() error{
		"FindAll": func() error { _, err := re.FindAllErr(s, -1); return err },
		"Split":   func() error { _, err := re.SplitErr(s, -1); return err },
		"ReplaceFunc": func() error {
			_, err := re.ReplaceFunc(s, true, func([]Group) (string, error) { return "", nil })
			return err
		},
	} {
		if err := f(); !errors.Is(err, ErrBudget) {
			t.Errorf("%s error = %v; want ErrBudget", name, err)
		}
	}
}

// A Meter is charged the work as it is done and can stop it: that is how the
// interpreter bills a call to the Run and interrupts it at the deadline.
func TestMeterChargesAndStops(t *testing.T) {
	re := MustCompile(`a(?=[^z]*z|)`, "")
	re.SetBudget(1 << 40)
	s := strings.Repeat("a", 3000)

	charged := 0
	m := &Meter{Charge: func(n int) error { charged += n; return nil }}
	if _, err := re.With(m).ReplaceErr(s, "b", true); err != nil {
		t.Fatalf("ReplaceErr error = %v", err)
	}
	// Each of the 3000 matches scans what is left of the subject: about n²/2 steps.
	if charged < 3000*3000/2 {
		t.Errorf("charged %d steps; want at least %d", charged, 3000*3000/2)
	}

	stop := errors.New("deadline")
	calls := 0
	m = &Meter{Every: 1000, Charge: func(int) error {
		if calls++; calls == 3 {
			return stop
		}
		return nil
	}}
	if _, err := re.With(m).FindAllErr(s, -1); err != stop {
		t.Fatalf("FindAllErr error = %v; want the meter's own error", err)
	}
	if calls != 3 {
		t.Errorf("Charge called %d times; want 3, the search stopping at the third", calls)
	}

	// RE2 cannot be interrupted, so its linear work is charged before it starts.
	charged = 0
	m = &Meter{Charge: func(n int) error { charged += n; return nil }}
	if _, err := MustCompile(`b`, "").With(m).MatchErr(strings.Repeat("a", 4000)); err != nil {
		t.Fatal(err)
	}
	if charged != 1000 {
		t.Errorf("RE2 search over 4000 bytes charged %d; want 1000", charged)
	}
}

// MaxBytes stops a replacement before it builds what it would refuse: 2 000 matches of a
// 1 MB template are 2 GB, and that string must never exist.
func TestMeterMaxBytes(t *testing.T) {
	big := strings.Repeat("b", 1<<20)
	s := strings.Repeat("a", 2000)
	m := &Meter{MaxBytes: 8 << 20}
	for _, pattern := range []string{`a`, `(?=a)a`} { // RE2, then the backtracker
		re := MustCompile(pattern, "")
		if _, err := re.With(m).ReplaceErr(s, big, true); !errors.Is(err, ErrTooLarge) {
			t.Errorf("/%s/ ReplaceErr error = %v; want ErrTooLarge", pattern, err)
		}
		if _, err := re.With(m).ReplaceErr(s, strings.Repeat(`\0`, 1<<20), true); !errors.Is(err, ErrTooLarge) {
			t.Errorf("/%s/ ReplaceErr with a long \\0 template error = %v; want ErrTooLarge", pattern, err)
		}
		_, err := re.With(m).ReplaceFunc(s, true, func([]Group) (string, error) { return big, nil })
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("/%s/ ReplaceFunc error = %v; want ErrTooLarge", pattern, err)
		}
		if out, err := re.With(m).ReplaceErr("xax", "[\\0]", true); err != nil || out != "x[a]x" {
			t.Errorf("/%s/ a small replacement = %q, %v; want x[a]x", pattern, out, err)
		}
	}
}

// Size is what the interpreter charges for a compiled pattern, so it must grow with what
// the pattern compiled to, not only with its text.
func TestSize(t *testing.T) {
	short := MustCompile(`a`, "")
	unrolled := MustCompile(`a{1000}`, "")
	if unrolled.Size() < 1000*re2InstBytes {
		t.Errorf("Size(/a{1000}/) = %d; want at least %d", unrolled.Size(), 1000*re2InstBytes)
	}
	if short.Size() >= unrolled.Size() {
		t.Errorf("Size(/a/) = %d is not below Size(/a{1000}/) = %d", short.Size(), unrolled.Size())
	}
	bt := MustCompile(`(?=x)a{1000}`, "")
	if bt.Backend() != "bt" || bt.Size() < 1000*16 {
		t.Errorf("Size of a backtracking a{1000} = %d (%s); want at least %d", bt.Size(), bt.Backend(), 1000*16)
	}
}
