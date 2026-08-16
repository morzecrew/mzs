package mzs

import "mzs/internal/rx"

// Regex-value methods (SPEC §12.6) and the match helpers str.go shares with them. Every
// index here is a rune index: `"привет".index(/вет/)` is 3 (§11.3).
//
// Matching itself is not here: `~` is an operator and lives in ops.go (D5). What is left
// is the three ways of *reading* a match — captures, matches, index — plus the two
// accessors, each under exactly one name (D17).

// matchArray renders one match as the array §8.4 describes: element 0 is the whole match
// and 1..n are the groups, with a group that did not participate coming out as nil.
//
// §8.4 also says a named group is reachable as `m["name"]`, so the names travel with the
// value and `m["g"]` reads the group `(?<g>…)` captured. The array is an ordinary array
// in every other respect — `type`, `json`, `len` and the array methods see the positions.
func matchArray(gs []rx.Group) Value {
	out := make([]Value, len(gs))
	var names map[string]int
	for i, g := range gs {
		if g.Name != "" {
			if names == nil {
				names = make(map[string]int, 2)
			}
			names[g.Name] = i
		}
		if g.Ok {
			out[i] = Str(g.Text)
			continue
		}
		out[i] = Nil()
	}
	return namedArrayOf(out, names)
}

// regexCaptures is `captures` in both spellings — `S.captures(R)` and `R.captures(S)`:
// the match array, or nil when the pattern does not match.
func regexCaptures(c *Ctx, re *rx.Regexp, s string) (Value, error) {
	gs, ok, err := re.FindSubmatchErr(s)
	if err != nil {
		return Nil(), regexFail(c, err)
	}
	if !ok {
		return Nil(), nil
	}
	return matchArray(gs), nil
}

// regexAllMatches is `matches` in both spellings: one entry per match — the matched text
// when the pattern has no groups, and the array of its group texts when it has (§12.2).
func regexAllMatches(c *Ctx, re *rx.Regexp, s string) (Value, error) {
	ms, err := re.FindAllErr(s, -1)
	if err != nil {
		return Nil(), regexFail(c, err)
	}
	if err := c.CheckCollection(len(ms)); err != nil {
		return Nil(), err
	}
	out := make([]Value, 0, len(ms))
	for _, g := range ms {
		if err := c.Step(1); err != nil {
			return Nil(), err
		}
		if len(g) <= 1 {
			out = append(out, Str(g[0].Text))
			continue
		}
		groups := make([]Value, len(g)-1)
		for i := 1; i < len(g); i++ {
			if g[i].Ok {
				groups[i-1] = Str(g[i].Text)
				continue
			}
			groups[i-1] = Nil()
		}
		out = append(out, arrayOf(groups))
	}
	return arrayOf(out), nil
}

// regexIndex is `index` in both spellings: the rune index of the first match, or nil.
// A match at position 0 is still truthy (D6), which is why this may return 0 (§7.3).
func regexIndex(c *Ctx, re *rx.Regexp, s string) (Value, error) {
	start, _, ok, err := re.FindIndexErr(s)
	if err != nil {
		return Nil(), regexFail(c, err)
	}
	if !ok {
		return Nil(), nil
	}
	return Int(int64(start)), nil
}

func init() {
	RegisterMethods(KRegex,
		Method{Name: "captures", Min: 1, Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			s, err := regexSubject(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return regexCaptures(c, recv.rx(), s)
		}},
		Method{Name: "matches", Min: 1, Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			s, err := regexSubject(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return regexAllMatches(c, recv.rx(), s)
		}},
		Method{Name: "index", Min: 1, Max: 1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			s, err := regexSubject(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return regexIndex(c, recv.rx(), s)
		}},
		Method{Name: "source", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Str(recv.rx().Source()), nil
		}},
		Method{Name: "flags", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Str(recv.rx().Flags()), nil
		}},
		Method{Name: "str", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			return Str(recv.rx().String()), nil
		}},
	)
}

// regexSubject reads the string a regex method is applied to. An unbound `$var` arrives
// as nil and simply does not match, the same rule `nil ~ R` follows (§8.4); every other
// non-string is a type error, because there is no implicit `str` conversion.
func regexSubject(c *Ctx, v Value) (string, error) {
	if v.Kind() == KNil {
		return "", nil
	}
	return argStr(c, v)
}
