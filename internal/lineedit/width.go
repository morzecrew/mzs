package lineedit

import "mzs/internal/width"

// Display width. The editor moves the cursor by columns, not by runes, so it has to know
// that "日" takes two of them and that the accent in "é" written as e+U+0301 takes none.
// Getting this wrong is not cosmetic: the cursor lands on the wrong character and every
// subsequent edit is aimed one column off.
//
// The table itself lives in internal/width, because `s.width` and `ljust` in the language
// have to answer exactly what the editor answers (§12.2): a name padded to twenty columns
// in a script and the same name typed at the prompt line up only if one table decides
// both.

// displayWidth is how many terminal columns rs occupies.
func displayWidth(rs []rune) int { return width.Runes(rs) }

// runeWidth is how many columns one code point occupies.
func runeWidth(r rune) int { return width.Rune(r) }
