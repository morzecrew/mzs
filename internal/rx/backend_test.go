package rx

import (
	"errors"
	"strings"
	"testing"

	"mzs/internal/rx/bt"
)

// The seam: everything scanPattern routes to the backtracking backend must now
// compile there and behave as Ruby does. Nothing degrades to RE2 any more, so
// /\bменю\b/i really matches Cyrillic instead of quietly never matching.
func TestBacktrackingBackendIsUsed(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		subject string
		want    bool
		start   int
	}{
		{"unicode word boundary", `\bменю\b`, "i", "Меню", true, 0},
		{"unicode word boundary in a sentence", `\bеда\b`, "i", "вкусная еда", true, 8},
		{"unicode word boundary rejects a substring", `\bеда\b`, "i", "победа", false, 0},
		{"corpus menu alternation", `\bменю\b|главное меню`, "i", "Главное Меню", true, 0},
		{"corpus negative lookahead", `^(?!❌ Отмена).*$`, "", "Записаться", true, 0},
		{"corpus negative lookahead rejects", `^(?!❌ Отмена).*$`, "", "❌ Отмена", false, 0},
		{"lookbehind", `(?<=цена )\d+`, "", "цена 500", true, 5},
		{"backreference", `(\p{L}+) \1`, "", "да да", true, 0},
		{"atomic group", `(?>a|ab)c`, "", "abc", false, 0},
		{"possessive quantifier", `\d++x`, "", "12x", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := Compile(tt.pattern, tt.flags)
			if err != nil {
				t.Fatalf("Compile(/%s/%s) error = %v", tt.pattern, tt.flags, err)
			}
			if got := re.Backend(); got != "bt" {
				t.Errorf("Backend() = %q; want \"bt\"", got)
			}
			if re.Approx() {
				t.Errorf("Approx() = true; the backtracking backend is exact")
			}
			start, _, ok := re.FindIndex(tt.subject)
			if ok != tt.want {
				t.Fatalf("FindIndex(%q) ok = %v; want %v", tt.subject, ok, tt.want)
			}
			if ok && start != tt.start {
				t.Errorf("FindIndex(%q) start = %d; want %d (rune index)", tt.subject, start, tt.start)
			}
		})
	}
}

// The facade's FindAll/Replace/Split loops run over the backtracking backend too.
func TestBacktrackingFacadeOperations(t *testing.T) {
	t.Run("find all", func(t *testing.T) {
		re := MustCompile(`\b\p{L}+\b`, "")
		got := re.FindAll("да, нет и меню", -1)
		if len(got) != 4 {
			t.Fatalf("FindAll = %d matches; want 4", len(got))
		}
		if got[3][0].Text != "меню" || got[3][0].Start != 10 {
			t.Errorf("last match = %q at %d; want \"меню\" at 10", got[3][0].Text, got[3][0].Start)
		}
	})

	t.Run("replace with a group reference", func(t *testing.T) {
		re := MustCompile(`\b(\p{L})(\p{L}*)\b`, "")
		if got := re.Replace("да нет", `\2\1`, true); got != "ад етн" {
			t.Errorf("Replace = %q; want %q", got, "ад етн")
		}
	})

	t.Run("split", func(t *testing.T) {
		re := MustCompile(`\b-\b`, "")
		got := strings.Join(re.Split("а-б-в", -1), "|")
		if got != "а|б|в" {
			t.Errorf("Split = %q; want %q", got, "а|б|в")
		}
	})
}

// A catastrophic pattern must come back as an error, not a hang. The *Err forms
// surface it; the SPEC §11.2 forms report "no match" (SPEC decision 6).
func TestRegexStepBudget(t *testing.T) {
	re := MustCompile(`\b(a+)+$`, "")
	re.SetBudget(10_000)
	subject := strings.Repeat("a", 40) + "!"

	if _, err := re.MatchErr(subject); !errors.Is(err, bt.ErrBudget) {
		t.Errorf("MatchErr error = %v; want bt.ErrBudget", err)
	}
	if re.Match(subject) {
		t.Errorf("Match = true; a budget overrun must read as no match")
	}
	if got := re.Budget(); got != 10_000 {
		t.Errorf("Budget() = %d; want 10000", got)
	}
}

// A construct no backend can express is a compile error, never a silent downgrade.
func TestUnsupportedConstructsFailLoudly(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{"unbounded lookbehind", `(?<=a*)b`},
		{"match reset", `a\Kb`},
		{"previous match anchor", `\Ga`},
		{"undefined named backref", `\k<none>`},
		{"backref past the group count", `(a)\3`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Compile(tt.pattern, ""); err == nil {
				t.Errorf("Compile(/%s/) = nil error; want an error", tt.pattern)
			}
		})
	}
}
