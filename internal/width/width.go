// Package width answers one question — how many terminal columns does this text
// occupy — for the two places that have to agree on it: the line editor, which moves a
// cursor by columns (`internal/lineedit`), and `s.width`, `ljust`, `rjust` and `center`
// in the language itself (§12.2).
//
// They used to be one implementation and one absence: the editor knew that "日" takes
// two columns and that the accent in "é" written as e+U+0301 takes none, and `ljust`
// counted runes, so a table of names padded to twenty was straight in the REPL and
// ragged in its own output. One table, read by both, is what makes the two agree.
//
// It is a wcwidth: three answers per code point — none for the marks that render on top of
// what came before and for the characters that render as nothing, two for the East Asian
// Wide and Fullwidth ranges and for emoji, one for everything else. The width table below
// is generated from the Unicode data rather than recalled, because the difference shows up
// in ordinary text: ✅, ⭐ and ⚡ are two columns as surely as 日 is.
//
// The unit is the code point, which is the unit Unicode gives a width to. A joined emoji
// sequence is therefore counted emoji by emoji — U+1F469 U+200D U+1F3A8 is four — because
// whether a terminal composes such a sequence into one cell or draws two is the terminal's
// decision and not the text's, and counting clusters instead would need the segmentation
// tables of UAX #29 (§12.2, I1).
package width

import (
	"sort"
	"unicode"
)

// String is how many columns s occupies.
func String(s string) int {
	w := 0
	for _, r := range s {
		w += Rune(r)
	}
	return w
}

// Runes is String for text already taken apart, which is the shape the editor holds.
func Runes(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += Rune(r)
	}
	return w
}

// Rune is how many columns one code point occupies: none, one, or two.
func Rune(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		// A control character. Nothing draws one, so it occupies nothing.
		return 0
	case r < 0x7f:
		return 1
	case isZeroWidth(r):
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

// isZeroWidth covers the marks that render on top of the previous character and the
// format characters that render as nothing at all — the zero-width joiner and the
// variation selectors that hold a modern emoji sequence together.
func isZeroWidth(r rune) bool {
	switch {
	case r >= 0xfe00 && r <= 0xfe0f: // variation selectors
		return true
	case r >= 0xe0100 && r <= 0xe01ef: // variation selectors, supplement
		return true
	}
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r)
}

// wide is every code point Unicode gives a width of Wide or Fullwidth, in ascending order —
// the East Asian scripts, and the emoji that render as two cells. It is generated from the
// Unicode data rather than remembered: ✅ (U+2705), ⭐ (U+2B50) and ⚡ (U+26A1) are as common
// in the text this language handles as any ideograph, and a hand-written table had them a
// column short.
//
// The emoji *blocks* are covered whole rather than code point by code point. That is
// deliberate: those blocks fill with emoji and nothing else, so one added next year is
// already two columns here instead of being one until somebody remembers the table. The
// price is the handful of text-presentation symbols inside them — ⛆ and its neighbours —
// counted as two, which is what a terminal showing them as emoji does anyway.
var wide = [...]struct{ lo, hi rune }{
	{0x1100, 0x115f},   // Hangul Choseong Kiyeok
	{0x231a, 0x231b},   // Watch
	{0x2329, 0x232a},   // Left-Pointing Angle Bracket
	{0x23e9, 0x23ec},   // Black Right-Pointing Double Triangle
	{0x23f0, 0x23f0},   // Alarm Clock
	{0x23f3, 0x23f3},   // Hourglass With Flowing Sand
	{0x25fd, 0x25fe},   // White Medium Small Square
	{0x2614, 0x2615},   // Umbrella With Rain Drops
	{0x2648, 0x2653},   // Aries
	{0x267f, 0x267f},   // Wheelchair Symbol
	{0x2693, 0x2693},   // Anchor
	{0x26a1, 0x26a1},   // High Voltage Sign
	{0x26aa, 0x26ab},   // Medium White Circle
	{0x26bd, 0x26be},   // Soccer Ball
	{0x26c4, 0x26c5},   // Snowman Without Snow
	{0x26ce, 0x26ce},   // Ophiuchus
	{0x26d4, 0x26d4},   // No Entry
	{0x26ea, 0x26ea},   // Church
	{0x26f2, 0x26f3},   // Fountain
	{0x26f5, 0x26f5},   // Sailboat
	{0x26fa, 0x26fa},   // Tent
	{0x26fd, 0x26fd},   // Fuel Pump
	{0x2705, 0x2705},   // White Heavy Check Mark
	{0x270a, 0x270b},   // Raised Fist
	{0x2728, 0x2728},   // Sparkles
	{0x274c, 0x274c},   // Cross Mark
	{0x274e, 0x274e},   // Negative Squared Cross Mark
	{0x2753, 0x2755},   // Black Question Mark Ornament
	{0x2757, 0x2757},   // Heavy Exclamation Mark Symbol
	{0x2795, 0x2797},   // Heavy Plus Sign
	{0x27b0, 0x27b0},   // Curly Loop
	{0x27bf, 0x27bf},   // Double Curly Loop
	{0x2b1b, 0x2b1c},   // Black Large Square
	{0x2b50, 0x2b50},   // White Medium Star
	{0x2b55, 0x2b55},   // Heavy Large Circle
	{0x2e80, 0x303e},   // CJK Radical Repeat
	{0x3041, 0x3247},   // Hiragana Letter Small A
	{0x3250, 0x4dbf},   // Partnership Sign
	{0x4e00, 0xa4c6},   // CJK Unified Ideograph-4E00
	{0xa960, 0xa97c},   // Hangul Choseong Tikeut-Mieum
	{0xac00, 0xd7a3},   // Hangul Syllable Ga
	{0xf900, 0xfad9},   // CJK Compatibility Ideograph-F900
	{0xfe10, 0xfe19},   // Presentation Form For Vertical Comma
	{0xfe30, 0xfe6b},   // Presentation Form For Vertical Two Dot Leader
	{0xff01, 0xff60},   // Fullwidth Exclamation Mark
	{0xffe0, 0xffe6},   // Fullwidth Cent Sign
	{0x16fe0, 0x18d08}, // Tangut Iteration Mark
	{0x1b000, 0x1b2fb}, // Katakana Letter Archaic E
	{0x1f004, 0x1f004}, // Mahjong Tile Red Dragon
	{0x1f0cf, 0x1f0cf}, // Playing Card Black Joker
	{0x1f18e, 0x1f18e}, // Negative Squared Ab
	{0x1f191, 0x1f19a}, // Squared Cl
	{0x1f200, 0x1f2ff}, // enclosed ideographic supplement
	{0x1f300, 0x1f64f}, // pictographs, transport, emoticons
	{0x1f680, 0x1f6ff}, // transport and map
	{0x1f7e0, 0x1f7ff}, // the large circles and squares
	{0x1f900, 0x1f9ff}, // supplemental symbols
	{0x1fa70, 0x1faff}, // symbols and pictographs extended-A
	{0x20000, 0x3fffd}, // CJK extensions B and beyond
}

// isWide is a binary search over that table: the ranges are ascending and disjoint, so the
// first one whose end is not before r is the only one that can contain it.
func isWide(r rune) bool {
	i := sort.Search(len(wide), func(i int) bool { return r <= wide[i].hi })
	return i < len(wide) && r >= wide[i].lo
}
