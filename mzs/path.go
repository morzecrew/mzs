package mzs

import (
	"path"
	"strings"
)

// The `path` module (§12.20) — taking file names apart and putting them together.
//
//	include path
//	path.join("in", name)            # "in/report.json" — one separator, cleaned
//	path.ext("a/b.tar.gz")           # ".gz"
//	path.stem("a/b.tar.gz")          # "b.tar"
//
// It is **purely lexical and always `/`**: no member looks at a filesystem, the working
// directory or the platform, so every answer depends on the arguments alone (D15) and the
// module needs no gate. Which names a script may reach, and how a `/` name becomes the
// platform's own, is the FileSystem implementation's business, as path policy already is
// (§12.13). That is why this is written over Go's `path` and never `path/filepath`.

func init() {
	// Registration order is `path.keys` order (§8.13).
	RegisterModuleFunc("path", "join", 0, -1, pathvJoin)
	RegisterModuleFunc("path", "dir", 1, 1, pathvDir)
	RegisterModuleFunc("path", "base", 1, 1, pathvBase)
	RegisterModuleFunc("path", "ext", 1, 1, pathvExt)
	RegisterModuleFunc("path", "stem", 1, 1, pathvStem)
	RegisterModuleFunc("path", "clean", 1, 1, pathvClean)
	RegisterModuleFunc("path", "split", 1, 1, pathvSplit)
	RegisterModuleFunc("path", "rel", 2, 2, pathvRel)
	RegisterModuleFunc("path", "is_abs", 1, 1, pathvIsAbs)
	RegisterModuleFunc("path", "match", 2, 2, pathvMatch)
}

// pathvJoin is `path.join(*parts)`: the parts joined with `/` and cleaned, empty parts
// dropped. Joining nothing, or only empty parts, is "" — there is no name to clean.
func pathvJoin(c *Ctx, args []Value) (Value, error) {
	// The parts are joined one at a time, cleaning as it goes — Clean(Clean(a/b)/c) is
	// Clean(a/b/c), so the answer is the same — and the running result is held to
	// MaxStringBytes after each step. That bounds every intermediate by two strings under
	// the limit, where joining all the parts at once would first build their whole
	// concatenation: a thousand splatted references to one large string would allocate a
	// thousand times its size before any check could run. Each step is charged for the
	// bytes it copies. The limit is on the cleaned result, so a doubled `/` cleaning removes
	// does not count against it; a running result over the limit is refused even if a
	// later `..` would have brought it back under.
	for _, a := range args {
		if _, err := argStr(c, a); err != nil {
			return Nil(), err
		}
	}
	// What one step allocates is the running result, under the limit, plus one part that
	// already exists — a host may hand over a part longer than the limit, and the step then
	// costs a copy of it, as any member does that reads such a string. A part is never
	// cleaned on its own first: cleaning is only the same as cleaning the join when it sees
	// what the part is joined to — `join("y", "/../a/../x")` is "x", and the part alone
	// cleans to "/x".
	joined := ""
	for _, a := range args {
		s := a.Str()
		if s == "" {
			continue
		}
		if err := c.Step(int64(len(joined)+len(s))/64 + 1); err != nil {
			return Nil(), err
		}
		if joined == "" {
			joined = path.Clean(s)
		} else {
			joined = path.Join(joined, s)
		}
		if err := c.CheckString(len(joined)); err != nil {
			return Nil(), err
		}
	}
	return Str(joined), nil
}

// pathvDir is `path.dir(p)`: everything before the last element, cleaned, and "." when
// there is none. The trailing `/` of a directory name is not an element of its own, so
// `dir("a/b/")` is "a" — the same element `base` drops — and the last element is taken off
// *before* the rest is cleaned, so `dir("a/b/..")` is "a/b", the directory `..` is in.
func pathvDir(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return Str(path.Dir(p)), nil
}

// pathvBase is `path.base(p)`: the last element, without trailing `/`s. The base of "" is
// "." and of "/" is "/", as Go's path.Base has it.
func pathvBase(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return Str(path.Base(p)), nil
}

// pathExt is the last `.suffix` of the base, with its dot. A name whose only dot is its
// first character — `.gitignore` — has no extension: the dot names a hidden file, not a
// type. Neither do `.` and `..`.
func pathExt(p string) string {
	b := path.Base(p)
	if b == "." || b == ".." || b == "/" {
		return ""
	}
	i := strings.LastIndexByte(b, '.')
	if i <= 0 {
		return ""
	}
	return b[i:]
}

// pathvExt is `path.ext(p)`.
func pathvExt(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return Str(pathExt(p)), nil
}

// pathvStem is `path.stem(p)`: the base without its extension, so
// `stem(p) + ext(p) == base(p)` for every p.
func pathvStem(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b := path.Base(p)
	return Str(strings.TrimSuffix(b, pathExt(p))), nil
}

// pathvClean is `path.clean(p)`: `.` and `..` resolved and doubled separators collapsed,
// lexically — `..` is taken back against the element before it, which may be a link on a
// real filesystem; this module never asks. The clean of "" is ".".
func pathvClean(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return Str(path.Clean(p)), nil
}

// pathElems are the elements of the cleaned p. A rooted path starts with the element "/",
// so `join(*split(p))` gives back `clean(p)`; "." has no elements at all.
//
// The number of elements is counted, and held to MaxCollection, before any of them is
// allocated: a path of slashes is a short string and a long array.
func pathElems(c *Ctx, p string) ([]string, error) {
	p = path.Clean(p)
	if p == "." {
		return nil, nil
	}
	if p == "/" {
		// The root is one element, though its one `/` would count as two.
		if err := c.CheckCollection(1); err != nil {
			return nil, err
		}
		return []string{"/"}, nil
	}
	if err := c.CheckCollection(strings.Count(p, "/") + 1); err != nil {
		return nil, err
	}
	var out []string
	if strings.HasPrefix(p, "/") {
		out = append(out, "/")
		p = p[1:]
		if p == "" {
			return out, nil
		}
	}
	return append(out, strings.Split(p, "/")...), nil
}

// pathvSplit is `path.split(p)`: the elements of the cleaned path — `"a/b/c"` is
// `["a", "b", "c"]` and `"/a/b"` is `["/", "a", "b"]`.
func pathvSplit(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	elems, err := pathElems(c, p)
	if err != nil {
		return Nil(), err
	}
	out := make([]Value, len(elems))
	for i, e := range elems {
		out[i] = Str(e)
	}
	return arrayOf(out), nil
}

// pathvRel is `path.rel(p, base)`: p relative to base, so that `join(base, rel(p, base))`
// is `clean(p)`, lexically. It is `nil` when no relative path says it: one of the two is
// rooted and the other is not, or base climbs out with `..` further than p does, where
// the answer would need the name of a directory the arguments do not contain.
func pathvRel(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	base, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}
	r, ok, err := pathRel(c, p, base)
	if err != nil {
		return Nil(), err
	}
	if !ok {
		return Nil(), nil
	}
	// Every `..` the answer climbs is three bytes the arguments did not have, so the
	// result is measured on its own.
	if err := c.CheckString(len(r)); err != nil {
		return Nil(), err
	}
	return Str(r), nil
}

func pathRel(c *Ctx, p, base string) (string, bool, error) {
	pe, err := pathElems(c, p)
	if err != nil {
		return "", false, err
	}
	be, err := pathElems(c, base)
	if err != nil {
		return "", false, err
	}
	if path.IsAbs(path.Clean(p)) != path.IsAbs(path.Clean(base)) {
		return "", false, nil
	}
	i := 0
	for i < len(pe) && i < len(be) && pe[i] == be[i] {
		i++
	}
	up := be[i:]
	for _, e := range up {
		if e == ".." {
			return "", false, nil
		}
	}
	out := make([]string, 0, len(up)+len(pe)-i)
	for range up {
		out = append(out, "..")
	}
	out = append(out, pe[i:]...)
	if len(out) == 0 {
		return ".", true, nil
	}
	return strings.Join(out, "/"), true, nil
}

// pathvIsAbs is `path.is_abs(p)`: whether p starts with `/`.
func pathvIsAbs(c *Ctx, args []Value) (Value, error) {
	p, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return Bool(strings.HasPrefix(p, "/")), nil
}

// pathvMatch is `path.match(pattern, name)`: a shell glob against one name. `*` and `?`
// match within an element, `[a-z]` and `[^a-z]` are character classes, `\` escapes, and
// an element that is exactly `**` matches any number of elements, none included — so
// `**/*.mzs` matches `a.mzs` and `src/lib/a.mzs`. The name is matched as written, not
// cleaned: a directory io.walk yields as `src/lib/` ends in an empty element, which `**`
// matches and `*.mzs` does not. A malformed pattern is an argument error, whatever the
// name.
func pathvMatch(c *Ctx, args []Value) (Value, error) {
	pattern, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	name, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}
	// Everything below is paid for before it is done: the elements are counted and held
	// to MaxCollection before either side is split, and the whole match is charged before
	// the pattern is validated, so a pattern of a million slashes is refused, not built.
	np, nn := strings.Count(pattern, "/")+1, strings.Count(name, "/")+1
	if err := c.CheckCollection(np); err != nil {
		return Nil(), err
	}
	if err := c.CheckCollection(nn); err != nil {
		return Nil(), err
	}
	// The pattern is read whole to validate it whatever the name is, so its bytes are
	// charged even against an empty name.
	if err := c.Step(int64(np)*int64(nn) + int64(len(pattern))*(int64(len(name))+1)/64); err != nil {
		return Nil(), err
	}
	pe := strings.Split(pattern, "/")
	for _, e := range pe {
		if e == "**" {
			continue
		}
		if _, err := path.Match(e, ""); err != nil {
			return Nil(), c.ArgErrorf("path.match: malformed pattern %s", quoteString(pattern))
		}
	}
	ne := strings.Split(name, "/")
	// The table has len(pe)·len(ne) cells, and a cell is one path.Match of an element
	// against an element — which, for a `*` followed by a long literal, is as much work
	// as the two lengths multiplied. Both were paid for above, so a pattern built to be
	// slow runs out of budget before it runs at all, rather than inside a Go call the
	// deadline cannot interrupt.
	return Bool(globElems(pe, ne)), nil
}

// globElems matches pattern elements against name elements. It is the table of which
// suffixes of the name each suffix of the pattern can match, filled from the end, so a
// pattern of many `**` costs len(pattern)·len(name) and never backtracks.
func globElems(pe, ne []string) bool {
	// ok[j] is whether pe[i:] matches ne[j:], for the i of the current row.
	ok := make([]bool, len(ne)+1)
	ok[len(ne)] = true // the empty pattern matches the empty name
	for i := len(pe) - 1; i >= 0; i-- {
		next := make([]bool, len(ne)+1)
		if pe[i] == "**" {
			// `**` here and then pe[i+1:] at any later position.
			next[len(ne)] = ok[len(ne)]
			for j := len(ne) - 1; j >= 0; j-- {
				next[j] = ok[j] || next[j+1]
			}
		} else {
			for j := len(ne) - 1; j >= 0; j-- {
				if ok[j+1] {
					m, _ := path.Match(pe[i], ne[j])
					next[j] = m
				}
			}
		}
		ok = next
	}
	return ok[0]
}
