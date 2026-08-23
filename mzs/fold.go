package mzs

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Case folding — what `casefold` is and why `lower` is not it (§12.2).
//
// Two strings that a reader would call the same word can differ after `lower`: the German
// "STRASSE" lowercases to "strasse" and "Straße" to "straße", the Greek "ΟΔΟΣ" lowercases
// to a plain σ at the end and "Οδος" to the final ς, and a token typed with the Kelvin
// sign K (U+212A) lowercases to itself.
// Case folding is the mapping Unicode defines for exactly this question — is this the same
// text ignoring case — and it answers all three:
//
//	"Straße".casefold == "STRASSE".casefold      # true; ß folds to "ss"
//	"ΟΔΟΣ".casefold == "Οδος".casefold           # true; ς and Σ both fold to σ
//	"Straße".lower == "STRASSE".lower            # false — which is why the row exists
//
// What it is not is an accent-stripper: "Οδός" keeps its tonos and folds to "οδός", the
// same way "ёж" is not "еж". Case is what folding removes; a diacritic is a letter.
//
// It is **full** folding, the one Python spells `str.casefold`: a rune may fold to more
// than one rune, so the result is not the same length as its input and is not a rendering
// of it. It is a comparison key and nothing else — never text to show a user.
//
// The implementation is Go's own case tables plus the exceptions that folding has and
// lowercasing does not, because I1 keeps `golang.org/x/text` out of this project and the
// data below is what the dependency would have been:
//
//   - foldSimple maps a rune to the lowest rune of its fold orbit, lowercased. Go gives
//     the orbit away for free — unicode.SimpleFold walks it — and that rule alone is right
//     for every cased script the language is likely to see: Σ, σ and ς all reach σ, the
//     Kelvin sign reaches k, the long ſ reaches s, and Cyrillic and Latin are unremarkable.
//   - foldRanges is where that rule is wrong, in full, checked against the Unicode data:
//     the two symbols that fold into letters (µ, and the iota subscript), and Cherokee,
//     which is the one script that folds to its **uppercase** letters rather than away
//     from them.
//   - foldFull is the 104 runes whose folding is longer than one rune — ß, the ligatures,
//     the Greek letters that carry a subscript iota, the Armenian digraphs.
//
// What is deliberately absent is `normalize`: NFC and NFD need the decomposition tables of
// the whole standard, which is the dependency I1 forbids, and half of the answer under the
// name of the whole would be worse than no answer (§12.2, ROADMAP T15).

// foldRanges are the runes whose fold the orbit rule below gets wrong, ascending. delta is
// what to add to reach the folded form, so 0 means "folds to itself" — the answer for the
// 86 Cherokee capitals, which are the target of their own fold rather than its source.
var foldRanges = [...]struct{ lo, hi, delta rune }{
	{0x00B5, 0x00B5, 0x0307},  // µ MICRO SIGN → μ
	{0x0345, 0x0345, 0x0074},  // ͅ COMBINING GREEK YPOGEGRAMMENI → ι
	{0x0399, 0x0399, 0x0020},  // Ι GREEK CAPITAL LETTER IOTA → ι
	{0x039C, 0x039C, 0x0020},  // Μ GREEK CAPITAL LETTER MU → μ
	{0x03B9, 0x03B9, 0},       // ι GREEK SMALL LETTER IOTA
	{0x03BC, 0x03BC, 0},       // μ GREEK SMALL LETTER MU
	{0x13A0, 0x13F5, 0},       // Ꭰ..Ᏽ CHEROKEE LETTER A..MV
	{0x13F8, 0x13FD, -0x0008}, // ᏸ..ᏽ CHEROKEE SMALL LETTER YE..MV → Ᏸ..Ᏽ
	{0x1C88, 0x1C88, 0x89C3},  // ᲈ CYRILLIC SMALL LETTER UNBLENDED UK → ꙋ
	{0x1FBE, 0x1FBE, -0x1C05}, // ι GREEK PROSGEGRAMMENI → ι
	{0xA64A, 0xA64A, 0x0001},  // Ꙋ CYRILLIC CAPITAL LETTER MONOGRAPH UK → ꙋ
	{0xA64B, 0xA64B, 0},       // ꙋ CYRILLIC SMALL LETTER MONOGRAPH UK
	{0xAB70, 0xABBF, -0x97D0}, // ꭰ..ꮿ CHEROKEE SMALL LETTER A..YA → Ꭰ..Ꮿ
}

// foldSimple is one rune folded to one rune: the lowest member of its fold orbit,
// lowercased, with foldRanges consulted first.
func foldSimple(r rune) rune {
	for _, x := range foldRanges {
		if r < x.lo {
			break
		}
		if r <= x.hi {
			return r + x.delta
		}
	}
	lo := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < lo {
			lo = f
		}
	}
	return unicode.ToLower(lo)
}

// foldString is `casefold` of a whole string. Text that is already folded — which is most
// text, and all of ASCII lowercase — is returned as it came, without allocating.
//
// A rune that folds to itself is copied as the *bytes* it arrived as rather than re-encoded.
// That matters for input that is not valid UTF-8 — what `crypto.unbase64` and a binary
// `io.read` hand back (§12.16) — which passes through unchanged instead of becoming a row of
// U+FFFD, and it is what lets foldLen below count the answer exactly.
func foldString(s string) string {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if _, multi := foldFull[r]; !multi && foldSimple(r) == r {
			i += size
			continue
		}
		var b strings.Builder
		b.Grow(len(s) + 8)
		b.WriteString(s[:i])
		for j := i; j < len(s); {
			r, size := utf8.DecodeRuneInString(s[j:])
			switch f, multi := foldFull[r]; {
			case multi:
				b.WriteString(f)
			case foldSimple(r) == r:
				b.WriteString(s[j : j+size])
			default:
				b.WriteRune(foldSimple(r))
			}
			j += size
		}
		return b.String()
	}
	return s
}

// foldLen is how many bytes foldString would produce, without producing them. `casefold` is
// the one string row that can answer longer than it was asked — ß folds to "ss", and
// U+0390 to three runes — so the size is checked against MaxStringBytes *before* the bytes
// exist rather than after (§14.1); an 8 MB string of expansions would otherwise allocate its
// whole tripled self only to be refused.
//
// It walks by the same three cases as foldString above, and TestFoldLenMatchesFoldString
// pins that the two never drift apart.
func foldLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch f, multi := foldFull[r]; {
		case multi:
			n += len(f)
		case foldSimple(r) == r:
			n += size
		default:
			n += utf8.RuneLen(foldSimple(r))
		}
		i += size
	}
	return n
}

// foldFull is Unicode's full case folding where it is longer than one rune. Every entry is
// a rune that folds into two or three, which is what makes `casefold` a key rather than a
// spelling: `"ß".casefold.len` is 2.
var foldFull = map[rune]string{
	0x00DF: "ss",                 // LATIN SMALL LETTER SHARP S
	0x0130: "i\u0307",            // LATIN CAPITAL LETTER I WITH DOT ABOVE
	0x0149: "\u02bcn",            // LATIN SMALL LETTER N PRECEDED BY APOSTROPHE
	0x01F0: "j\u030c",            // LATIN SMALL LETTER J WITH CARON
	0x0390: "\u03b9\u0308\u0301", // GREEK SMALL LETTER IOTA WITH DIALYTIKA AND TONOS
	0x03B0: "\u03c5\u0308\u0301", // GREEK SMALL LETTER UPSILON WITH DIALYTIKA AND TONOS
	0x0587: "\u0565\u0582",       // ARMENIAN SMALL LIGATURE ECH YIWN
	0x1E96: "h\u0331",            // LATIN SMALL LETTER H WITH LINE BELOW
	0x1E97: "t\u0308",            // LATIN SMALL LETTER T WITH DIAERESIS
	0x1E98: "w\u030a",            // LATIN SMALL LETTER W WITH RING ABOVE
	0x1E99: "y\u030a",            // LATIN SMALL LETTER Y WITH RING ABOVE
	0x1E9A: "a\u02be",            // LATIN SMALL LETTER A WITH RIGHT HALF RING
	0x1E9E: "ss",                 // LATIN CAPITAL LETTER SHARP S
	0x1F50: "\u03c5\u0313",       // GREEK SMALL LETTER UPSILON WITH PSILI
	0x1F52: "\u03c5\u0313\u0300", // GREEK SMALL LETTER UPSILON WITH PSILI AND VARIA
	0x1F54: "\u03c5\u0313\u0301", // GREEK SMALL LETTER UPSILON WITH PSILI AND OXIA
	0x1F56: "\u03c5\u0313\u0342", // GREEK SMALL LETTER UPSILON WITH PSILI AND PERISPOMENI
	0x1F80: "\u1f00\u03b9",       // GREEK SMALL LETTER ALPHA WITH PSILI AND YPOGEGRAMMENI
	0x1F81: "\u1f01\u03b9",       // GREEK SMALL LETTER ALPHA WITH DASIA AND YPOGEGRAMMENI
	0x1F82: "\u1f02\u03b9",       // GREEK SMALL LETTER ALPHA WITH PSILI AND VARIA AND YPOGEGRAMMENI
	0x1F83: "\u1f03\u03b9",       // GREEK SMALL LETTER ALPHA WITH DASIA AND VARIA AND YPOGEGRAMMENI
	0x1F84: "\u1f04\u03b9",       // GREEK SMALL LETTER ALPHA WITH PSILI AND OXIA AND YPOGEGRAMMENI
	0x1F85: "\u1f05\u03b9",       // GREEK SMALL LETTER ALPHA WITH DASIA AND OXIA AND YPOGEGRAMMENI
	0x1F86: "\u1f06\u03b9",       // GREEK SMALL LETTER ALPHA WITH PSILI AND PERISPOMENI AND YPOGEGRAMMENI
	0x1F87: "\u1f07\u03b9",       // GREEK SMALL LETTER ALPHA WITH DASIA AND PERISPOMENI AND YPOGEGRAMMENI
	0x1F88: "\u1f00\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH PSILI AND PROSGEGRAMMENI
	0x1F89: "\u1f01\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH DASIA AND PROSGEGRAMMENI
	0x1F8A: "\u1f02\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH PSILI AND VARIA AND PROSGEGRAMMENI
	0x1F8B: "\u1f03\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH DASIA AND VARIA AND PROSGEGRAMMENI
	0x1F8C: "\u1f04\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH PSILI AND OXIA AND PROSGEGRAMMENI
	0x1F8D: "\u1f05\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH DASIA AND OXIA AND PROSGEGRAMMENI
	0x1F8E: "\u1f06\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH PSILI AND PERISPOMENI AND PROSGEGRAMMENI
	0x1F8F: "\u1f07\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH DASIA AND PERISPOMENI AND PROSGEGRAMMENI
	0x1F90: "\u1f20\u03b9",       // GREEK SMALL LETTER ETA WITH PSILI AND YPOGEGRAMMENI
	0x1F91: "\u1f21\u03b9",       // GREEK SMALL LETTER ETA WITH DASIA AND YPOGEGRAMMENI
	0x1F92: "\u1f22\u03b9",       // GREEK SMALL LETTER ETA WITH PSILI AND VARIA AND YPOGEGRAMMENI
	0x1F93: "\u1f23\u03b9",       // GREEK SMALL LETTER ETA WITH DASIA AND VARIA AND YPOGEGRAMMENI
	0x1F94: "\u1f24\u03b9",       // GREEK SMALL LETTER ETA WITH PSILI AND OXIA AND YPOGEGRAMMENI
	0x1F95: "\u1f25\u03b9",       // GREEK SMALL LETTER ETA WITH DASIA AND OXIA AND YPOGEGRAMMENI
	0x1F96: "\u1f26\u03b9",       // GREEK SMALL LETTER ETA WITH PSILI AND PERISPOMENI AND YPOGEGRAMMENI
	0x1F97: "\u1f27\u03b9",       // GREEK SMALL LETTER ETA WITH DASIA AND PERISPOMENI AND YPOGEGRAMMENI
	0x1F98: "\u1f20\u03b9",       // GREEK CAPITAL LETTER ETA WITH PSILI AND PROSGEGRAMMENI
	0x1F99: "\u1f21\u03b9",       // GREEK CAPITAL LETTER ETA WITH DASIA AND PROSGEGRAMMENI
	0x1F9A: "\u1f22\u03b9",       // GREEK CAPITAL LETTER ETA WITH PSILI AND VARIA AND PROSGEGRAMMENI
	0x1F9B: "\u1f23\u03b9",       // GREEK CAPITAL LETTER ETA WITH DASIA AND VARIA AND PROSGEGRAMMENI
	0x1F9C: "\u1f24\u03b9",       // GREEK CAPITAL LETTER ETA WITH PSILI AND OXIA AND PROSGEGRAMMENI
	0x1F9D: "\u1f25\u03b9",       // GREEK CAPITAL LETTER ETA WITH DASIA AND OXIA AND PROSGEGRAMMENI
	0x1F9E: "\u1f26\u03b9",       // GREEK CAPITAL LETTER ETA WITH PSILI AND PERISPOMENI AND PROSGEGRAMMENI
	0x1F9F: "\u1f27\u03b9",       // GREEK CAPITAL LETTER ETA WITH DASIA AND PERISPOMENI AND PROSGEGRAMMENI
	0x1FA0: "\u1f60\u03b9",       // GREEK SMALL LETTER OMEGA WITH PSILI AND YPOGEGRAMMENI
	0x1FA1: "\u1f61\u03b9",       // GREEK SMALL LETTER OMEGA WITH DASIA AND YPOGEGRAMMENI
	0x1FA2: "\u1f62\u03b9",       // GREEK SMALL LETTER OMEGA WITH PSILI AND VARIA AND YPOGEGRAMMENI
	0x1FA3: "\u1f63\u03b9",       // GREEK SMALL LETTER OMEGA WITH DASIA AND VARIA AND YPOGEGRAMMENI
	0x1FA4: "\u1f64\u03b9",       // GREEK SMALL LETTER OMEGA WITH PSILI AND OXIA AND YPOGEGRAMMENI
	0x1FA5: "\u1f65\u03b9",       // GREEK SMALL LETTER OMEGA WITH DASIA AND OXIA AND YPOGEGRAMMENI
	0x1FA6: "\u1f66\u03b9",       // GREEK SMALL LETTER OMEGA WITH PSILI AND PERISPOMENI AND YPOGEGRAMMENI
	0x1FA7: "\u1f67\u03b9",       // GREEK SMALL LETTER OMEGA WITH DASIA AND PERISPOMENI AND YPOGEGRAMMENI
	0x1FA8: "\u1f60\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH PSILI AND PROSGEGRAMMENI
	0x1FA9: "\u1f61\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH DASIA AND PROSGEGRAMMENI
	0x1FAA: "\u1f62\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH PSILI AND VARIA AND PROSGEGRAMMENI
	0x1FAB: "\u1f63\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH DASIA AND VARIA AND PROSGEGRAMMENI
	0x1FAC: "\u1f64\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH PSILI AND OXIA AND PROSGEGRAMMENI
	0x1FAD: "\u1f65\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH DASIA AND OXIA AND PROSGEGRAMMENI
	0x1FAE: "\u1f66\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH PSILI AND PERISPOMENI AND PROSGEGRAMMENI
	0x1FAF: "\u1f67\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH DASIA AND PERISPOMENI AND PROSGEGRAMMENI
	0x1FB2: "\u1f70\u03b9",       // GREEK SMALL LETTER ALPHA WITH VARIA AND YPOGEGRAMMENI
	0x1FB3: "\u03b1\u03b9",       // GREEK SMALL LETTER ALPHA WITH YPOGEGRAMMENI
	0x1FB4: "\u03ac\u03b9",       // GREEK SMALL LETTER ALPHA WITH OXIA AND YPOGEGRAMMENI
	0x1FB6: "\u03b1\u0342",       // GREEK SMALL LETTER ALPHA WITH PERISPOMENI
	0x1FB7: "\u03b1\u0342\u03b9", // GREEK SMALL LETTER ALPHA WITH PERISPOMENI AND YPOGEGRAMMENI
	0x1FBC: "\u03b1\u03b9",       // GREEK CAPITAL LETTER ALPHA WITH PROSGEGRAMMENI
	0x1FC2: "\u1f74\u03b9",       // GREEK SMALL LETTER ETA WITH VARIA AND YPOGEGRAMMENI
	0x1FC3: "\u03b7\u03b9",       // GREEK SMALL LETTER ETA WITH YPOGEGRAMMENI
	0x1FC4: "\u03ae\u03b9",       // GREEK SMALL LETTER ETA WITH OXIA AND YPOGEGRAMMENI
	0x1FC6: "\u03b7\u0342",       // GREEK SMALL LETTER ETA WITH PERISPOMENI
	0x1FC7: "\u03b7\u0342\u03b9", // GREEK SMALL LETTER ETA WITH PERISPOMENI AND YPOGEGRAMMENI
	0x1FCC: "\u03b7\u03b9",       // GREEK CAPITAL LETTER ETA WITH PROSGEGRAMMENI
	0x1FD2: "\u03b9\u0308\u0300", // GREEK SMALL LETTER IOTA WITH DIALYTIKA AND VARIA
	0x1FD3: "\u03b9\u0308\u0301", // GREEK SMALL LETTER IOTA WITH DIALYTIKA AND OXIA
	0x1FD6: "\u03b9\u0342",       // GREEK SMALL LETTER IOTA WITH PERISPOMENI
	0x1FD7: "\u03b9\u0308\u0342", // GREEK SMALL LETTER IOTA WITH DIALYTIKA AND PERISPOMENI
	0x1FE2: "\u03c5\u0308\u0300", // GREEK SMALL LETTER UPSILON WITH DIALYTIKA AND VARIA
	0x1FE3: "\u03c5\u0308\u0301", // GREEK SMALL LETTER UPSILON WITH DIALYTIKA AND OXIA
	0x1FE4: "\u03c1\u0313",       // GREEK SMALL LETTER RHO WITH PSILI
	0x1FE6: "\u03c5\u0342",       // GREEK SMALL LETTER UPSILON WITH PERISPOMENI
	0x1FE7: "\u03c5\u0308\u0342", // GREEK SMALL LETTER UPSILON WITH DIALYTIKA AND PERISPOMENI
	0x1FF2: "\u1f7c\u03b9",       // GREEK SMALL LETTER OMEGA WITH VARIA AND YPOGEGRAMMENI
	0x1FF3: "\u03c9\u03b9",       // GREEK SMALL LETTER OMEGA WITH YPOGEGRAMMENI
	0x1FF4: "\u03ce\u03b9",       // GREEK SMALL LETTER OMEGA WITH OXIA AND YPOGEGRAMMENI
	0x1FF6: "\u03c9\u0342",       // GREEK SMALL LETTER OMEGA WITH PERISPOMENI
	0x1FF7: "\u03c9\u0342\u03b9", // GREEK SMALL LETTER OMEGA WITH PERISPOMENI AND YPOGEGRAMMENI
	0x1FFC: "\u03c9\u03b9",       // GREEK CAPITAL LETTER OMEGA WITH PROSGEGRAMMENI
	0xFB00: "ff",                 // LATIN SMALL LIGATURE FF
	0xFB01: "fi",                 // LATIN SMALL LIGATURE FI
	0xFB02: "fl",                 // LATIN SMALL LIGATURE FL
	0xFB03: "ffi",                // LATIN SMALL LIGATURE FFI
	0xFB04: "ffl",                // LATIN SMALL LIGATURE FFL
	0xFB05: "st",                 // LATIN SMALL LIGATURE LONG S T
	0xFB06: "st",                 // LATIN SMALL LIGATURE ST
	0xFB13: "\u0574\u0576",       // ARMENIAN SMALL LIGATURE MEN NOW
	0xFB14: "\u0574\u0565",       // ARMENIAN SMALL LIGATURE MEN ECH
	0xFB15: "\u0574\u056b",       // ARMENIAN SMALL LIGATURE MEN INI
	0xFB16: "\u057e\u0576",       // ARMENIAN SMALL LIGATURE VEW NOW
	0xFB17: "\u0574\u056d",       // ARMENIAN SMALL LIGATURE MEN XEH
}
