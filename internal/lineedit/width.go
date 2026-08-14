package lineedit

import "unicode"

// Display width. The editor moves the cursor by columns, not by runes, so it has to know
// that "日" takes two of them and that the accent in "é" written as e+U+0301 takes none.
// Getting this wrong is not cosmetic: the cursor lands on the wrong character and every
// subsequent edit is aimed one column off.
//
// This is a deliberately small wcwidth: the East Asian Wide and Fullwidth blocks, the
// emoji planes, the combining marks and the format characters. The corpus this REPL is
// typed into is Cyrillic, Latin and emoji (§3.2), and all three are exact here.

// displayWidth is how many terminal columns rs occupies.
func displayWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		// A control character. The editor never draws one, so it occupies nothing.
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

// wide is the set of code points a terminal draws in two columns, in ascending order.
var wide = [...]struct{ lo, hi rune }{
	{0x1100, 0x115f},   // Hangul Jamo, initial consonants
	{0x2e80, 0x303e},   // CJK radicals, Kangxi, CJK symbols
	{0x3041, 0x33ff},   // kana, Hangul compatibility jamo, CJK compatibility
	{0x3400, 0x4dbf},   // CJK extension A
	{0x4e00, 0x9fff},   // CJK unified ideographs
	{0xa000, 0xa4cf},   // Yi
	{0xa960, 0xa97f},   // Hangul Jamo extended A
	{0xac00, 0xd7a3},   // Hangul syllables
	{0xf900, 0xfaff},   // CJK compatibility ideographs
	{0xfe10, 0xfe19},   // vertical forms
	{0xfe30, 0xfe6f},   // CJK compatibility forms
	{0xff00, 0xff60},   // fullwidth forms
	{0xffe0, 0xffe6},   // fullwidth signs
	{0x1f300, 0x1f64f}, // emoji: symbols and pictographs, emoticons
	{0x1f680, 0x1f6ff}, // emoji: transport and map
	{0x1f900, 0x1f9ff}, // emoji: supplemental symbols
	{0x20000, 0x3fffd}, // CJK extensions B and beyond
}

func isWide(r rune) bool {
	for _, w := range wide {
		if r < w.lo {
			return false
		}
		if r <= w.hi {
			return true
		}
	}
	return false
}
