package rx

import (
	"errors"
	"strings"
)

// normFlags validates the flag letters of SPEC §3.8 and returns them in a canonical
// order so that two spellings of the same regex share one cache entry. Ruby's `s` is
// an accepted alias of `m` (dot matches newline) and is folded into it; `u` is a no-op
// kept only so pasted Ruby compiles.
func normFlags(flags string) (string, error) {
	var i, m, x, u bool
	for _, r := range flags {
		switch r {
		case 'i':
			i = true
		case 'm', 's':
			m = true
		case 'x':
			x = true
		case 'u':
			u = true
		default:
			return "", errors.New("unknown regex flag " + string(r))
		}
	}
	var sb strings.Builder
	if i {
		sb.WriteByte('i')
	}
	if m {
		sb.WriteByte('m')
	}
	if x {
		sb.WriteByte('x')
	}
	if u {
		sb.WriteByte('u')
	}
	return sb.String(), nil
}

// features records what a pattern needs, decided by one scan at Compile time.
type features struct {
	wordBoundary bool // \b or \B outside a character class — Go's are ASCII-only
	lookaround   bool // (?= (?! (?<= (?<!
	atomic       bool // (?>
	backref      bool // \1..\9, \k<name>
	possessive   bool // a*+ a++ a?+ a{2,3}+
	exotic       bool // \G \K (?( — nothing can run these yet
	posixClass   bool // [[:alpha:]] & co — Go's are ASCII-only, Ruby's are Unicode
}

// needsBT reports whether the pattern must go to the backtracking backend. SPEC §11.2
// pins this exactly: RE2 is used when the pattern has no lookaround, no backreference
// and no \b/\B.
//
// POSIX classes are here for the same reason \b is: Go implements [[:alpha:]] as
// ASCII-only while Ruby's is Unicode-aware, so /[[:alpha:]]+/ on "Привет" matches under
// Ruby and under bt but not under RE2. Leaving it on RE2 made the two backends disagree
// with each other and with Ruby — a silent wrong answer of exactly the kind this package
// exists to prevent.
func (f features) needsBT() bool {
	return f.wordBoundary || f.lookaround || f.atomic || f.backref ||
		f.possessive || f.exotic || f.posixClass
}

// scanPattern walks the raw Ruby pattern once, tracking character-class state, and
// reports which constructs it contains.
func scanPattern(pattern string) features {
	var f features
	rs := []rune(pattern)
	inClass := false
	classStart := -1
	lastQuant := false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\\' && i+1 < len(rs) {
			n := rs[i+1]
			if !inClass {
				switch {
				case n == 'b' || n == 'B':
					f.wordBoundary = true
				case n >= '1' && n <= '9':
					f.backref = true
				case n == 'k' && i+2 < len(rs) && (rs[i+2] == '<' || rs[i+2] == '\''):
					f.backref = true
				case n == 'G' || n == 'K':
					f.exotic = true
				}
			}
			i++
			lastQuant = false
			continue
		}
		if inClass {
			// A POSIX class such as [:alpha:] contains a ']' that does not close.
			if c == '[' && i+1 < len(rs) && rs[i+1] == ':' {
				if j := indexFrom(rs, i+2, ":]"); j >= 0 {
					f.posixClass = true
					i = j + 1
					continue
				}
			}
			if c == ']' && i > classStart+1 && !(i == classStart+2 && rs[classStart+1] == '^') {
				inClass = false
			}
			continue
		}
		switch c {
		case '[':
			inClass = true
			classStart = i
			lastQuant = false
		case '(':
			if i+1 < len(rs) && rs[i+1] == '?' && i+2 < len(rs) {
				switch rs[i+2] {
				case '=', '!':
					f.lookaround = true
				case '>':
					f.atomic = true
				case '(':
					f.exotic = true
				case '<':
					if i+3 < len(rs) && (rs[i+3] == '=' || rs[i+3] == '!') {
						f.lookaround = true
					}
				}
			}
			lastQuant = false
		case '*', '+', '?':
			// A '+' or '?' directly after a quantifier is a possessive or lazy
			// modifier, not a quantifier of its own.
			if lastQuant {
				if c == '+' {
					f.possessive = true
				}
				lastQuant = false
			} else {
				lastQuant = true
			}
		case '}':
			lastQuant = true
		default:
			lastQuant = false
		}
	}
	return f
}

func indexFrom(rs []rune, from int, sub string) int {
	s := []rune(sub)
	for i := from; i+len(s) <= len(rs); i++ {
		ok := true
		for j, r := range s {
			if rs[i+j] != r {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// translate rewrites a Ruby pattern into an equivalent Go/RE2 pattern (SPEC §11.2):
//
//   - `(?m)` is always prepended, because Ruby's ^/$ are line anchors always;
//   - flag i becomes `(?i)`, Ruby's m becomes Go's `(?s)`;
//   - x (extended) is applied here, by dropping unescaped whitespace and #-comments
//     outside character classes, so RE2 never sees it;
//   - `\Z` becomes `(?:\n?\z)`; `\A` and `\z` pass through unchanged;
//   - `\/` is an escaped delimiter and becomes a plain `/`;
//   - Ruby's `(?<name>…)` and `(?'name'…)` become Go's `(?P<name>…)`.
func translate(pattern, flags string) (string, error) {
	ci := strings.ContainsRune(flags, 'i')
	dotAll := strings.ContainsRune(flags, 'm')
	extended := strings.ContainsRune(flags, 'x')

	var sb strings.Builder
	sb.Grow(len(pattern) + 8)
	sb.WriteString("(?m")
	if ci {
		sb.WriteByte('i')
	}
	if dotAll {
		sb.WriteByte('s')
	}
	sb.WriteByte(')')

	rs := []rune(pattern)
	inClass := false
	classStart := -1
	for i := 0; i < len(rs); i++ {
		c := rs[i]

		if c == '\\' && i+1 < len(rs) {
			n := rs[i+1]
			switch {
			case n == '/':
				sb.WriteRune('/')
			case !inClass && n == 'Z':
				sb.WriteString(`(?:\n?\z)`)
			case n == 'h':
				if inClass {
					sb.WriteString(`0-9a-fA-F`)
				} else {
					sb.WriteString(`[0-9a-fA-F]`)
				}
			case !inClass && n == 'H':
				sb.WriteString(`[^0-9a-fA-F]`)
			default:
				sb.WriteRune('\\')
				sb.WriteRune(n)
			}
			i++
			continue
		}

		if inClass {
			if c == '[' && i+1 < len(rs) && rs[i+1] == ':' {
				if j := indexFrom(rs, i+2, ":]"); j >= 0 {
					sb.WriteString(string(rs[i : j+2]))
					i = j + 1
					continue
				}
			}
			if c == ']' && i > classStart+1 && !(i == classStart+2 && rs[classStart+1] == '^') {
				inClass = false
			}
			sb.WriteRune(c)
			continue
		}

		if extended {
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
				continue
			}
			if c == '#' {
				for i < len(rs) && rs[i] != '\n' {
					i++
				}
				continue
			}
		}

		if c == '[' {
			inClass = true
			classStart = i
			sb.WriteRune(c)
			continue
		}

		// Ruby named groups: (?<name>…) and (?'name'…). Lookbehind (?<= (?<! never
		// reaches here — scanPattern routes it to the backtracking backend.
		if c == '(' && i+2 < len(rs) && rs[i+1] == '?' {
			if rs[i+2] == '<' && i+3 < len(rs) && rs[i+3] != '=' && rs[i+3] != '!' {
				sb.WriteString("(?P<")
				i += 2
				continue
			}
			if rs[i+2] == '\'' {
				if j := indexFrom(rs, i+3, "'"); j >= 0 {
					sb.WriteString("(?P<")
					sb.WriteString(string(rs[i+3 : j]))
					sb.WriteString(">")
					i = j
					continue
				}
			}
		}

		sb.WriteRune(c)
	}
	if inClass {
		return "", errors.New("unterminated character class")
	}
	return sb.String(), nil
}

// LintPattern implements the SPEC §11.5 warning. main.mzs contains literal double
// backslashes (`\\bеда\\b`, verified in hex) which in Ruby and in mzs match a
// backslash followed by 'b' — almost certainly a pattern that travelled through a JSON
// string. mzs does not silently repair it; it warns.
func LintPattern(pattern string) []string {
	var out []string
	rs := []rune(pattern)
	seen := map[rune]bool{}
	for i := 0; i+2 < len(rs); i++ {
		if rs[i] != '\\' || rs[i+1] != '\\' {
			continue
		}
		c := rs[i+2]
		switch c {
		case 'b', 'B', 'd', 'D', 'w', 'W', 's', 'S', 'A', 'z', 'Z':
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, `"\\`+string(c)+`" matches a literal backslash; did you mean "\`+
				string(c)+`"? (pattern probably came from a JSON string)`)
		}
		i += 2
	}
	return out
}
