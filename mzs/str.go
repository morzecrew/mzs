package mzs

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"mzs/internal/rx"
)

// String methods (SPEC §12.2) and the shared formatting of §12.7.
//
// Everything here is rune-based. The corpus is Cyrillic and emoji end to end, so a
// byte length, a byte index or a byte slice is a bug rather than an optimisation:
// "привет".len is 6, "привет"[0] is "п", and "привет".index(/вет/) is 3.
//
// Every row is registered under exactly one name (D17). The Ruby spellings the corpus
// used to carry — downcase, strip, gsub, to_i, the `?` and `!` suffixes — are gone, and
// the rename table in errors.go turns each of them into a did-you-mean diagnostic.

// sm builds one §12.2 row. The receiver of a string method is always a String, so the
// body takes the text directly instead of unwrapping a Value on every call.
func sm(name string, min, max int, f func(c *Ctx, s string, args []Value) (Value, error)) Method {
	return Method{Name: name, Min: min, Max: max, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
		return f(c, recv.s, args)
	}}
}

// ---------------------------------------------------------------------------
// Argument coercion
// ---------------------------------------------------------------------------

// argStr requires a string argument. There is exactly one semantics and no coercion
// mode (§9.1): a number where a string is expected is a type error, and the fix is the
// explicit `.str` the author has to write everywhere else anyway.
func argStr(c *Ctx, v Value) (string, error) {
	if v.Kind() == KString {
		return v.s, nil
	}
	return "", c.TypeErrorf("%s expects a string, got %s", c.Name(), v.Kind())
}

// argInt requires a numeric argument; a Float truncates toward zero.
func argInt(c *Ctx, v Value) (int64, error) {
	if v.IsNum() {
		return v.Int(), nil
	}
	return 0, c.TypeErrorf("%s expects an integer, got %s", c.Name(), v.Kind())
}

// argNum requires a number and keeps the int/float split of D9.
func argNum(c *Ctx, v Value) (Value, error) {
	if v.IsNum() {
		return v, nil
	}
	return Nil(), c.TypeErrorf("%s expects a number, got %s", c.Name(), v.Kind())
}

// optInt reads an optional integer argument.
func optInt(c *Ctx, args []Value, i int, def int64) (int64, error) {
	if i >= len(args) {
		return def, nil
	}
	return argInt(c, args[i])
}

// argRegex coerces an argument for the rows spelled `string | regex` — index, count,
// replace, split: a regex passes through and a string is quoted so it matches literally.
func argRegex(c *Ctx, v Value) (*rx.Regexp, error) {
	re, err := c.RegexOf(v)
	if err != nil {
		return nil, c.TypeErrorf("%s expects a regex or a string, got %s", c.Name(), v.Kind())
	}
	return re, nil
}

// argOnlyRegex is the check for the rows spelled `(re: regex)` — matches and captures.
// Those take a compiled pattern and nothing else; a string there is a mistake, not a
// pattern to match literally.
func argOnlyRegex(c *Ctx, v Value) (*rx.Regexp, error) {
	if v.Kind() != KRegex {
		return nil, c.TypeErrorf("%s expects a regex, got %s", c.Name(), v.Kind())
	}
	return v.rx(), nil
}

// regexFail turns a match-time failure into a positioned script error. A backtracking
// step-budget overrun must surface, never degrade into a silent non-match (§11.2).
func regexFail(c *Ctx, err error) error {
	if err == nil {
		return nil
	}
	return c.ErrorfKind(ErrKindRegex, "%s", err.Error())
}

// ---------------------------------------------------------------------------
// Rune helpers
// ---------------------------------------------------------------------------

// isTrimSpace decides what trim removes: Unicode whitespace (which already includes
// U+00A0, the NBSP messengers insert), plus the two invisibles that are not
// White_Space and still arrive in pasted text — U+200B and U+FEFF (§12.2).
func isTrimSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff'
}

// substr slices n runes starting at i, with a negative i counting from the end and an
// out-of-range start yielding "" rather than an error (§8.8).
func substr(rs []rune, i, n int) (string, bool) {
	if i < 0 {
		i += len(rs)
	}
	if i < 0 || i > len(rs) {
		return "", false
	}
	if n < 0 {
		return "", false
	}
	end := i + n
	if end > len(rs) {
		end = len(rs)
	}
	return string(rs[i:end]), true
}

// padTo builds n runes of padding by repeating pad.
func padTo(pad string, n int) string {
	if n <= 0 {
		return ""
	}
	pr := []rune(pad)
	out := make([]rune, 0, n)
	for len(out) < n {
		out = append(out, pr[len(out)%len(pr)])
	}
	return string(out)
}

// expandCharSet expands the character set `squeeze` takes: "a-z0-9" names its runes, a
// leading '^' negates the whole set, and a backslash escapes the next rune.
func expandCharSet(set string) (order []rune, negate bool) {
	rs := []rune(set)
	i := 0
	if len(rs) > 1 && rs[0] == '^' {
		negate = true
		i = 1
	}
	for ; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
			order = append(order, rs[i])
			continue
		}
		if i+2 < len(rs) && rs[i+1] == '-' && rs[i+2] >= rs[i] {
			for r := rs[i]; r <= rs[i+2]; r++ {
				order = append(order, r)
			}
			i += 2
			continue
		}
		order = append(order, rs[i])
	}
	return order, negate
}

// squeezeString collapses runs of the same rune, restricted to a set when one is given.
func squeezeString(s, set string) string {
	var members map[rune]bool
	neg := false
	if set != "" {
		order, n := expandCharSet(set)
		neg = n
		members = make(map[rune]bool, len(order))
		for _, r := range order {
			members[r] = true
		}
	}
	in := func(r rune) bool {
		if members == nil {
			return true
		}
		return members[r] != neg
	}
	var sb strings.Builder
	sb.Grow(len(s))
	prev := rune(-1)
	for _, r := range s {
		if r == prev && in(r) {
			continue
		}
		sb.WriteRune(r)
		prev = r
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// split
// ---------------------------------------------------------------------------

// splitString implements `split(sep = " ", limit = -1)` (§12.2):
//
//   - a separator of " " (or none at all) splits on runs of whitespace, with leading
//     whitespace ignored, which is why "Иван Петров".split(" ")[0] is "Иван";
//   - a separator of "" splits into runes;
//   - limit > 0 caps the number of fields, the last one holding the remainder;
//   - limit <= 0 (the default) splits everywhere and keeps every field, including the
//     empty ones — there is no field-dropping special case to remember.
func splitString(c *Ctx, s string, sep Value, limit int) ([]string, error) {
	var parts []string
	switch {
	case sep.Kind() == KNil, sep.Kind() == KString && sep.s == " ":
		parts = awkSplit(s, limit)
	case sep.Kind() == KRegex:
		// rx reads limit 0 as "no pieces at all", so the unlimited spelling is -1.
		n := limit
		if n <= 0 {
			n = -1
		}
		var err error
		parts, err = sep.rx().SplitErr(s, n)
		if err != nil {
			return nil, regexFail(c, err)
		}
	default:
		text, err := argStr(c, sep)
		if err != nil {
			return nil, err
		}
		parts = literalSplit(s, text, limit)
	}
	if err := c.CheckCollection(len(parts)); err != nil {
		return nil, err
	}
	return parts, nil
}

func literalSplit(s, sep string, limit int) []string {
	if sep == "" {
		rs := []rune(s)
		out := make([]string, 0, len(rs))
		for i, r := range rs {
			if limit > 0 && len(out) == limit-1 {
				out = append(out, string(rs[i:]))
				return out
			}
			out = append(out, string(r))
		}
		return out
	}
	if limit > 0 {
		return strings.SplitN(s, sep, limit)
	}
	return strings.Split(s, sep)
}

func awkSplit(s string, limit int) []string {
	rs := []rune(s)
	out := []string{}
	i := 0
	for {
		for i < len(rs) && isTrimSpace(rs[i]) {
			i++
		}
		if i >= len(rs) {
			return out
		}
		if limit > 0 && len(out) == limit-1 {
			out = append(out, string(rs[i:]))
			return out
		}
		j := i
		for j < len(rs) && !isTrimSpace(rs[j]) {
			j++
		}
		out = append(out, string(rs[i:j]))
		i = j
	}
}

// ---------------------------------------------------------------------------
// replace / replace_first
// ---------------------------------------------------------------------------

// substitute is `replace` (every occurrence) and `replace_first` (the first one). The
// replacement is either a string template understanding \0/\&, \1..\9 and \k<name>, or
// a function — an ordinary trailing closure argument (§4.2), which is why it arrives in
// args like anything else.
func substitute(c *Ctx, s string, all bool, args []Value) (Value, error) {
	re, err := argRegex(c, args[0])
	if err != nil {
		return Nil(), err
	}

	var out string
	if args[1].Kind() == KFunc {
		out, err = replaceCall(c, re, s, all, args[1])
	} else {
		var repl string
		if repl, err = argStr(c, args[1]); err != nil {
			return Nil(), err
		}
		out, err = re.ReplaceErr(s, repl, all)
		err = regexFail(c, err)
	}
	if err != nil {
		return Nil(), err
	}
	if err := c.CheckString(len(out)); err != nil {
		return Nil(), err
	}
	return Str(out), nil
}

// replaceCall drives the closure form of replace. The error the closure produces — a
// `break`, a `return`, a script error, a limit — is returned untouched, as the closure
// protocol requires (host.go); only a failure of the regex engine itself is positioned
// here.
func replaceCall(c *Ctx, re *rx.Regexp, s string, all bool, fn Value) (string, error) {
	var callErr error
	out, err := re.ReplaceFunc(s, all, func(g []rx.Group) (string, error) {
		if err := c.Step(1); err != nil {
			callErr = err
			return "", err
		}
		// The closure receives the match array of §8.4: the matched text first, the
		// groups after it, so `{ it.upper }` and `{ (m, a, b) -> … }` both read
		// naturally and a closure that wants neither simply drops them.
		v, err := c.Call(fn, matchArray(g).Elems()...)
		if err != nil {
			callErr = err
			return "", err
		}
		return v.Str(), nil
	})
	if callErr != nil {
		return "", callErr
	}
	return out, regexFail(c, err)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func init() {
	RegisterMethods(KString,
		sm("lower", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.ToLower(s)), nil
		}),
		sm("upper", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.ToUpper(s)), nil
		}),
		sm("capitalize", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			if s == "" {
				return Str(""), nil
			}
			r, n := utf8.DecodeRuneInString(s)
			return Str(string(unicode.ToUpper(r)) + strings.ToLower(s[n:])), nil
		}),
		sm("swapcase", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.Map(func(r rune) rune {
				if unicode.IsUpper(r) {
					return unicode.ToLower(r)
				}
				return unicode.ToUpper(r)
			}, s)), nil
		}),
		sm("trim", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.TrimFunc(s, isTrimSpace)), nil
		}),
		sm("trim_start", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.TrimLeftFunc(s, isTrimSpace)), nil
		}),
		sm("trim_end", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(strings.TrimRightFunc(s, isTrimSpace)), nil
		}),
		sm("chomp", 0, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			if len(args) == 0 {
				return Str(chompNewline(s)), nil
			}
			suf, err := argStr(c, args[0])
			if err != nil {
				return Nil(), err
			}
			if suf == "" {
				for {
					t := chompNewline(s)
					if t == s {
						return Str(s), nil
					}
					s = t
				}
			}
			return Str(strings.TrimSuffix(s, suf)), nil
		}),
		sm("chop", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			if strings.HasSuffix(s, "\r\n") {
				return Str(s[:len(s)-2]), nil
			}
			rs := []rune(s)
			if len(rs) == 0 {
				return Str(""), nil
			}
			return Str(string(rs[:len(rs)-1])), nil
		}),
		sm("squeeze", 0, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			set := ""
			if len(args) > 0 {
				var err error
				if set, err = argStr(c, args[0]); err != nil {
					return Nil(), err
				}
			}
			return Str(squeezeString(s, set)), nil
		}),
		sm("len", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Int(int64(utf8.RuneCountInString(s))), nil
		}),
		sm("empty", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Bool(s == ""), nil
		}),
		sm("blank", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Bool(strings.TrimFunc(s, isTrimSpace) == ""), nil
		}),
		sm("has", 1, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			sub, err := argStr(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return Bool(strings.Contains(s, sub)), nil
		}),
		sm("starts_with", 1, -1, func(c *Ctx, s string, args []Value) (Value, error) {
			return anyAffix(c, s, args, strings.HasPrefix)
		}),
		sm("ends_with", 1, -1, func(c *Ctx, s string, args []Value) (Value, error) {
			return anyAffix(c, s, args, strings.HasSuffix)
		}),
		sm("index", 1, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return strIndex(c, s, args, false)
		}),
		sm("last_index", 1, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			return strIndex(c, s, args, true)
		}),
		sm("count", 1, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			if args[0].Kind() == KRegex {
				ms, err := args[0].rx().FindAllErr(s, -1)
				if err != nil {
					return Nil(), regexFail(c, err)
				}
				return Int(int64(len(ms))), nil
			}
			sub, err := argStr(c, args[0])
			if err != nil {
				return Nil(), err
			}
			if sub == "" {
				return Int(0), nil
			}
			return Int(int64(strings.Count(s, sub))), nil
		}),
		sm("split", 0, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			sep := Nil()
			if len(args) > 0 {
				sep = args[0]
			}
			limit := -1
			if len(args) > 1 {
				n, err := argInt(c, args[1])
				if err != nil {
					return Nil(), err
				}
				limit = int(n)
			}
			parts, err := splitString(c, s, sep, limit)
			if err != nil {
				return Nil(), err
			}
			out := make([]Value, len(parts))
			for i, p := range parts {
				out[i] = Str(p)
			}
			return arrayOf(out), nil
		}),
		sm("replace", 2, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return substitute(c, s, true, args)
		}),
		sm("replace_first", 2, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return substitute(c, s, false, args)
		}),
		sm("matches", 1, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			re, err := argOnlyRegex(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return regexAllMatches(c, re, s)
		}),
		sm("captures", 1, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			re, err := argOnlyRegex(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return regexCaptures(c, re, s)
		}),
		sm("chars", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			rs := []rune(s)
			if err := c.CheckCollection(len(rs)); err != nil {
				return Nil(), err
			}
			out := make([]Value, len(rs))
			for i, r := range rs {
				out[i] = Str(string(r))
			}
			return arrayOf(out), nil
		}),
		sm("bytes", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			if err := c.CheckCollection(len(s)); err != nil {
				return Nil(), err
			}
			out := make([]Value, len(s))
			for i := 0; i < len(s); i++ {
				out[i] = Int(int64(s[i]))
			}
			return arrayOf(out), nil
		}),
		sm("lines", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			parts := textLines(s)
			if err := c.CheckCollection(len(parts)); err != nil {
				return Nil(), err
			}
			out := make([]Value, len(parts))
			for i, p := range parts {
				out[i] = Str(p)
			}
			return arrayOf(out), nil
		}),
		sm("reverse", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			rs := []rune(s)
			for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
				rs[i], rs[j] = rs[j], rs[i]
			}
			return Str(string(rs)), nil
		}),
		sm("first", 0, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			n, err := optInt(c, args, 0, 1)
			if err != nil {
				return Nil(), err
			}
			rs := []rune(s)
			if n < 0 {
				n = 0
			}
			if int(n) > len(rs) {
				n = int64(len(rs))
			}
			return Str(string(rs[:n])), nil
		}),
		sm("last", 0, 1, func(c *Ctx, s string, args []Value) (Value, error) {
			n, err := optInt(c, args, 0, 1)
			if err != nil {
				return Nil(), err
			}
			rs := []rune(s)
			if n < 0 {
				n = 0
			}
			if int(n) > len(rs) {
				n = int64(len(rs))
			}
			return Str(string(rs[len(rs)-int(n):])), nil
		}),
		// first_and_last is the fingerprint the bot flows take of a value; it survives
		// the rewrite because scripts in the corpus still call it.
		sm("first_and_last", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			rs := []rune(s)
			if len(rs) == 0 {
				return Str(""), nil
			}
			return Str(string(rs[0]) + string(rs[len(rs)-1])), nil
		}),
		sm("ljust", 1, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return justify(c, s, args, 'l')
		}),
		sm("rjust", 1, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return justify(c, s, args, 'r')
		}),
		sm("center", 1, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			return justify(c, s, args, 'c')
		}),
		sm("slice", 1, 2, func(c *Ctx, s string, args []Value) (Value, error) {
			i, err := argInt(c, args[0])
			if err != nil {
				return Nil(), err
			}
			n, err := optInt(c, args, 1, 1)
			if err != nil {
				return Nil(), err
			}
			out, ok := substr([]rune(s), int(i), int(n))
			if !ok {
				return Nil(), nil
			}
			return Str(out), nil
		}),
		sm("ord", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			if s == "" {
				return Nil(), c.ArgErrorf("ord: empty string")
			}
			r, _ := utf8.DecodeRuneInString(s)
			return Int(int64(r)), nil
		}),
		// each_char takes the closure as its trailing argument and returns the
		// receiver, so it chains like every other iterator (§12.2).
		sm("each_char", 1, 1, func(c *Ctx, s string, _ []Value) (Value, error) {
			for _, r := range s {
				if err := c.Step(1); err != nil {
					return Nil(), err
				}
				if _, err := c.CallClosure(Str(string(r))); err != nil {
					return Nil(), err
				}
			}
			return Str(s), nil
		}),

		// Conversions (§12.7). They are the same operations as the global `int`,
		// `float`, `str` and `json`, reachable both ways under UFCS (D18).
		sm("int", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Int(parseLeadingInt(s)), nil
		}),
		sm("float", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Float(parseLeadingFloat(s)), nil
		}),
		sm("str", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(s), nil
		}),
		sm("json", 0, 0, func(c *Ctx, s string, _ []Value) (Value, error) {
			return Str(encodeJSON(Str(s), "")), nil
		}),
	)
}

func chompNewline(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "\n"), strings.HasSuffix(s, "\r"):
		return s[:len(s)-1]
	}
	return s
}

// textLines splits text into lines the one way mzs splits text into lines: the terminator
// is dropped, so a trailing newline does not add an empty last field, an empty line in
// the middle is a field of its own, and a CRLF file reads like a LF one. `"…".lines` and
// `io.lines` (§12.13) are the same operation over different text, so they are one
// function — a second implementation is exactly the drift D17 exists to prevent.
func textLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// anyAffix backs starts_with and ends_with, both of which take any number of candidates
// and answer true when one of them matches.
func anyAffix(c *Ctx, s string, args []Value, has func(string, string) bool) (Value, error) {
	for _, a := range args {
		sub, err := argStr(c, a)
		if err != nil {
			return Nil(), err
		}
		if has(s, sub) {
			return Bool(true), nil
		}
	}
	return Bool(false), nil
}

// strIndex is index/last_index: a rune index, or nil when the needle is absent. `from`
// belongs to index alone (§12.2), so last_index always scans the whole receiver.
func strIndex(c *Ctx, s string, args []Value, last bool) (Value, error) {
	rs := []rune(s)
	from := 0
	if len(args) > 1 {
		n, err := argInt(c, args[1])
		if err != nil {
			return Nil(), err
		}
		from = int(n)
		if from < 0 {
			from += len(rs)
		}
		if from < 0 {
			from = 0
		}
		if from > len(rs) {
			return Nil(), nil
		}
	}
	hay := string(rs[from:])

	if args[0].Kind() == KRegex {
		ms, err := args[0].rx().FindAllErr(hay, -1)
		if err != nil {
			return Nil(), regexFail(c, err)
		}
		if len(ms) == 0 {
			return Nil(), nil
		}
		g := ms[0]
		if last {
			g = ms[len(ms)-1]
		}
		return Int(int64(from + g[0].Start)), nil
	}

	sub, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	var b int
	if last {
		b = strings.LastIndex(hay, sub)
	} else {
		b = strings.Index(hay, sub)
	}
	if b < 0 {
		return Nil(), nil
	}
	return Int(int64(from + utf8.RuneCountInString(hay[:b]))), nil
}

// justify backs ljust/rjust/center; widths are counted in runes.
func justify(c *Ctx, s string, args []Value, mode byte) (Value, error) {
	w, err := argInt(c, args[0])
	if err != nil {
		return Nil(), err
	}
	pad := " "
	if len(args) > 1 {
		if pad, err = argStr(c, args[1]); err != nil {
			return Nil(), err
		}
		if pad == "" {
			return Nil(), c.ArgErrorf("%s: zero width padding", c.Name())
		}
	}
	n := utf8.RuneCountInString(s)
	if int(w) <= n {
		return Str(s), nil
	}
	if err := c.CheckString(int(w) * 4); err != nil {
		return Nil(), err
	}
	total := int(w) - n
	switch mode {
	case 'l':
		return Str(s + padTo(pad, total)), nil
	case 'r':
		return Str(padTo(pad, total) + s), nil
	}
	left := total / 2
	return Str(padTo(pad, left) + s + padTo(pad, total-left)), nil
}

// ---------------------------------------------------------------------------
// format (§12.7)
// ---------------------------------------------------------------------------

// formatValues renders a format string. Directives are translated one at a time into
// Go's spelling and handed to fmt.Sprintf with a single, already-coerced argument, so an
// unknown verb is a script error rather than a `%!v(…)` leaking into a bot message.
//
// It backs the `format` builtin and the `%` operator, which is why it takes an argument
// slice rather than a Value: the operator expands an array argument before calling.
func formatValues(c *Ctx, format string, args []Value) (string, error) {
	rs := []rune(format)
	var sb strings.Builder
	sb.Grow(len(format) + 16)

	argi := 0
	next := func() (Value, error) {
		if argi >= len(args) {
			return Nil(), c.ArgErrorf("too few arguments for format string")
		}
		v := args[argi]
		argi++
		return v, nil
	}
	named := func(name string) (Value, error) {
		if len(args) == 0 || args[0].Kind() != KDict {
			return Nil(), c.ArgErrorf("format: %%<%s> needs a dict argument", name)
		}
		return args[0].Get(Str(name)), nil
	}

	for i := 0; i < len(rs); i++ {
		if rs[i] != '%' {
			sb.WriteRune(rs[i])
			continue
		}
		if i+1 >= len(rs) {
			return "", c.ArgErrorf("malformed format string: trailing '%%'")
		}
		if rs[i+1] == '%' {
			sb.WriteByte('%')
			i++
			continue
		}

		j := i + 1
		// %{name} interpolates with `str` and carries no verb.
		if rs[j] == '{' {
			k := j + 1
			for k < len(rs) && rs[k] != '}' {
				k++
			}
			if k >= len(rs) {
				return "", c.ArgErrorf("malformed format string: unclosed '%%{'")
			}
			v, err := named(string(rs[j+1 : k]))
			if err != nil {
				return "", err
			}
			sb.WriteString(v.Str())
			i = k
			continue
		}

		var spec strings.Builder
		spec.WriteByte('%')
		name := ""
		if rs[j] == '<' {
			k := j + 1
			for k < len(rs) && rs[k] != '>' {
				k++
			}
			if k >= len(rs) {
				return "", c.ArgErrorf("malformed format string: unclosed '%%<'")
			}
			name = string(rs[j+1 : k])
			j = k + 1
		}
		for j < len(rs) && strings.ContainsRune("-+ 0#", rs[j]) {
			spec.WriteRune(rs[j])
			j++
		}
		var err error
		if j, err = formatNumber(rs, j, &spec, next); err != nil {
			return "", err
		}
		if j < len(rs) && rs[j] == '.' {
			spec.WriteByte('.')
			j++
			if j, err = formatNumber(rs, j, &spec, next); err != nil {
				return "", err
			}
		}
		if j >= len(rs) {
			return "", c.ArgErrorf("malformed format string: missing conversion")
		}

		verb := rs[j]
		var arg Value
		if name != "" {
			if arg, err = named(name); err != nil {
				return "", err
			}
		} else if verb != '%' {
			if arg, err = next(); err != nil {
				return "", err
			}
		}

		out, err := formatOne(c, spec.String(), verb, arg)
		if err != nil {
			return "", err
		}
		sb.WriteString(out)
		i = j
	}
	if err := c.CheckString(sb.Len()); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// formatNumber consumes a width or precision, which may be a literal or a `*` that
// takes its value from the argument list.
func formatNumber(rs []rune, j int, spec *strings.Builder, next func() (Value, error)) (int, error) {
	if j < len(rs) && rs[j] == '*' {
		v, err := next()
		if err != nil {
			return j, err
		}
		spec.WriteString(strconv.FormatInt(v.Int(), 10))
		return j + 1, nil
	}
	for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
		spec.WriteRune(rs[j])
		j++
	}
	return j, nil
}

// formatOne applies one translated directive: %i is %d and %j is JSON (§12.7);
// everything else keeps the meaning Go already gives it.
func formatOne(c *Ctx, spec string, verb rune, arg Value) (string, error) {
	switch verb {
	case 's':
		return fmt.Sprintf(spec+"s", arg.Str()), nil
	case 'j':
		return fmt.Sprintf(spec+"s", encodeJSON(arg, "")), nil
	case 'd', 'i':
		return fmt.Sprintf(spec+"d", arg.Int()), nil
	case 'f':
		return fmt.Sprintf(spec+"f", arg.Float()), nil
	case 'e', 'E', 'g', 'G':
		return fmt.Sprintf(spec+string(verb), arg.Float()), nil
	case 'x', 'X', 'o', 'b':
		return fmt.Sprintf(spec+string(verb), arg.Int()), nil
	case 'c':
		if arg.Kind() == KString {
			if arg.Str() == "" {
				return "", nil
			}
			r, _ := utf8.DecodeRuneInString(arg.Str())
			return fmt.Sprintf(spec+"s", string(r)), nil
		}
		return fmt.Sprintf(spec+"s", string(rune(arg.Int()))), nil
	}
	return "", c.ArgErrorf("unknown format verb '%%%c'", verb)
}
