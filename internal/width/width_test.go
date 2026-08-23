package width

import "testing"

// The table is data, so what its tests pin is the shape it has to keep: ascending and
// disjoint, because isWide binary-searches it, and three answers per code point and no
// fourth.

// TestWideTableIsOrdered pins the invariant the search depends on. A range pasted into the
// wrong place would otherwise make every code point after it narrow, silently.
func TestWideTableIsOrdered(t *testing.T) {
	for i, w := range wide {
		if w.lo > w.hi {
			t.Errorf("range %d is inverted: %04X..%04X", i, w.lo, w.hi)
		}
		if i > 0 && w.lo <= wide[i-1].hi {
			t.Errorf("range %d (%04X..%04X) overlaps or precedes range %d (%04X..%04X)",
				i, w.lo, w.hi, i-1, wide[i-1].lo, wide[i-1].hi)
		}
	}
}

// TestWideSearchAgreesWithAScan pins the binary search against the obvious linear one over
// every code point there is: the two may not differ anywhere.
func TestWideSearchAgreesWithAScan(t *testing.T) {
	scan := func(r rune) bool {
		for _, w := range wide {
			if r >= w.lo && r <= w.hi {
				return true
			}
		}
		return false
	}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if got, want := isWide(r), scan(r); got != want {
			t.Fatalf("isWide(%04X) = %v; a scan of the table says %v", r, got, want)
		}
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii", 'a', 1},
		{"cyrillic", '\u0436', 1},
		{"an ideograph", '\u65e5', 2},
		{"a hangul syllable", '\uac00', 2},
		{"a fullwidth form", '\uff21', 2},
		{"an emoji", '\U0001f469', 2},
		{"an emoji from a later block", '\U0001fa77', 2},
		{"a symbol that is an emoji", '\u2705', 2},
		{"a combining mark", '\u0301', 0},
		{"a control character", '\u0001', 0},
		{"a zero width joiner", '\u200d', 0},
		{"a variation selector", '\ufe0f', 0},
		{"a private-use rune", '\U000f0000', 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Rune(tt.r); got != tt.want {
				t.Errorf("Rune(%04X) = %d; want %d", tt.r, got, tt.want)
			}
		})
	}
}

// TestStringAndRunes pins that the two entry points are one answer: the editor holds runes
// and the language holds a string, and a cursor that disagreed with `ljust` by a column
// would be the bug this package exists to prevent.
func TestStringAndRunes(t *testing.T) {
	for _, s := range []string{"", "abc", "\u043f\u0440\u0438\u0432\u0435\u0442", "\u65e5\u672c\u8a9e", "\U0001f469\u200d\U0001f3a8", "e\u0301"} {
		if got, want := String(s), Runes([]rune(s)); got != want {
			t.Errorf("String(%q) = %d; Runes = %d", s, got, want)
		}
	}
}
