package mzs

import (
	"math"
)

// The two modules of §12.8 that need no clock — `json` and `math` — and the JSON
// machinery the free function `json(x)` shares with them. Modules are ordinary
// lowercase values bound in the root scope, reached with `.` like anything else; there
// is no CONST kind and no `::` (D18).
//
// Encoding always goes through value.go's encodeJSON/decodeJSON. A second codec would
// let `"${[1, 2]}"` and `[1, 2].json` drift apart, since Value.Str already uses that
// one. Encoding has no module member: `json.parse` and the §12.1 function — written
// `x.json` in any file that includes the module, since a module is not callable — are
// the two halves of the pair, and a `generate` would be a second name for the second
// half (D17).

// jsonvMaxDepth bounds both directions. Go cannot recover from a stack overflow, so a
// self-referential value (`a.push(a)`) and a hostile 100k-deep document must both be
// refused before the recursion starts rather than after it crashes the host.
const jsonvMaxDepth = 256

func init() {
	RegisterModuleFunc("json", "parse", 1, 1, jsonvParse)
	RegisterModuleFunc("json", "pretty", 1, 1, jsonvPretty)

	RegisterModuleConst("math", "pi", Float(math.Pi))
	RegisterModuleConst("math", "e", Float(math.E))
	// The members are registered from ordered slices, not maps: a module is a Dict and
	// a Dict is insertion-ordered, so `math.keys` must not depend on Go's map iteration
	// (§8.13).
	for _, m := range []struct {
		name string
		fn   func(float64) float64
	}{
		{"sqrt", math.Sqrt},
		{"cbrt", math.Cbrt},
		{"sin", math.Sin},
		{"cos", math.Cos},
		{"tan", math.Tan},
		{"atan", math.Atan},
		{"log", math.Log},
		{"log2", math.Log2},
		{"log10", math.Log10},
		{"exp", math.Exp},
	} {
		RegisterModuleFunc("math", m.name, 1, 1, modvFloat1(m.name, m.fn))
	}
	for _, m := range []struct {
		name string
		fn   func(a, b float64) float64
	}{
		{"atan2", math.Atan2},
		{"pow", math.Pow},
		{"hypot", math.Hypot},
	} {
		RegisterModuleFunc("math", m.name, 2, 2, modvFloat2(m.name, m.fn))
	}
}

// ---------------------------------------------------------------------------
// json (§12.8)
// ---------------------------------------------------------------------------

// jsonvParse turns a JSON document into Values, keeping object key order (D11): objects
// become insertion-ordered Dicts, arrays Arrays, integral numbers Int, `null` nil.
func jsonvParse(c *Ctx, args []Value) (Value, error) {
	src, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := c.Step(int64(len(src)/16) + 1); err != nil {
		return Nil(), err
	}
	if d := jsonvNesting(src); d > jsonvMaxDepth {
		return Nil(), c.ArgErrorf("json.parse: input is nested too deeply (%d levels)", d)
	}
	v, derr := decodeJSON([]byte(src))
	if derr != nil {
		return Nil(), c.ArgErrorf("json.parse: %s", derr.Error())
	}
	return v, nil
}

func jsonvPretty(c *Ctx, args []Value) (Value, error) {
	s, err := jsonvText(c, args[0], "  ")
	if err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

// jsonvText is the single entry point the free function `json(x)` and `json.pretty`
// share: it refuses cycles, bounds the depth and charges the result against
// MaxStringBytes.
func jsonvText(c *Ctx, v Value, indent string) (string, error) {
	if err := jsonvWalk(c, v, map[any]bool{}, 0); err != nil {
		return "", err
	}
	s := encodeJSON(v, indent)
	if err := c.CheckString(len(s)); err != nil {
		return "", err
	}
	return s, nil
}

// jsonvWalk rejects a value that contains itself. `open` holds the containers on the
// current path only, so a value referenced twice side by side is fine — that is a DAG,
// not a cycle, and it serialises perfectly well.
func jsonvWalk(c *Ctx, v Value, open map[any]bool, depth int) error {
	if depth > jsonvMaxDepth {
		return c.ArgErrorf("json: value is nested too deeply (%d levels)", depth)
	}
	switch v.Kind() {
	case KArray:
		p := any(v.arr())
		if open[p] {
			return c.ArgErrorf("json: value contains a cycle")
		}
		open[p] = true
		xs := *v.arr()
		if err := c.Step(int64(len(xs)) + 1); err != nil {
			return err
		}
		for _, e := range xs {
			if err := jsonvWalk(c, e, open, depth+1); err != nil {
				return err
			}
		}
		delete(open, p)
	case KRange:
		// encodeJSON materialises a range without asking, so the cap has to be charged
		// here or `(1..1e12).json` would allocate until the host dies.
		if err := c.CheckCollection(v.Len()); err != nil {
			return err
		}
		if err := c.Step(int64(v.Len())); err != nil {
			return err
		}
	case KDict:
		d := v.odict()
		p := any(d)
		if open[p] {
			return c.ArgErrorf("json: value contains a cycle")
		}
		open[p] = true
		if err := c.Step(int64(d.Len()) + 1); err != nil {
			return err
		}
		var failed error
		d.Each(func(_, val Value) bool {
			if err := jsonvWalk(c, val, open, depth+1); err != nil {
				failed = err
				return false
			}
			return true
		})
		if failed != nil {
			return failed
		}
		delete(open, p)
	}
	return nil
}

// jsonvNesting is the cheap pre-scan that keeps decodeJSON's recursion bounded: it
// counts bracket depth outside string literals, which is an upper bound on the depth
// the decoder will recurse to.
func jsonvNesting(s string) int {
	depth, maxDepth := 0, 0
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '[', '{':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case ']', '}':
			depth--
		}
	}
	return maxDepth
}

// ---------------------------------------------------------------------------
// math (§12.8)
// ---------------------------------------------------------------------------

// modvFloat1 and modvFloat2 keep the math rows to one line each. They refuse
// non-numeric arguments: `math.sqrt("x")` is a mistake, not a zero (§9.1).
func modvFloat1(name string, fn func(float64) float64) HostFunc {
	return func(c *Ctx, args []Value) (Value, error) {
		x, err := modvNum(c, name, args[0])
		if err != nil {
			return Nil(), err
		}
		return Float(fn(x)), nil
	}
}

func modvFloat2(name string, fn func(a, b float64) float64) HostFunc {
	return func(c *Ctx, args []Value) (Value, error) {
		x, err := modvNum(c, name, args[0])
		if err != nil {
			return Nil(), err
		}
		y, err := modvNum(c, name, args[1])
		if err != nil {
			return Nil(), err
		}
		return Float(fn(x, y)), nil
	}
}

func modvNum(c *Ctx, name string, v Value) (float64, error) {
	if !v.IsNum() {
		return 0, c.TypeErrorf("math.%s expects a number, got %s", name, v.TypeName())
	}
	return v.Float(), nil
}
