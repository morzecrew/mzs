package mzs

import (
	"fmt"
	"io"
	"sort"
)

// Global builtins (SPEC §12.1), the conversions of §12.7 and the bool/function
// receivers of §12.9.
//
// Every row here is registered as a **free function** and nothing else. Under UFCS
// (D18) that is already both halves of the pair: `len(x)` reaches it as a call and
// `x.len` reaches it through step 2 of §8.7, so each operation has exactly one
// implementation and one name (D17). A per-kind table only gets a row when the
// behaviour really differs by kind — §12.2's `str.len` counts runes, this table's
// `len` does not know how.
//
// Output goes to Options.Stdout through Ctx.Out, never to os.Stdout: a script running
// inside a bot has no console, and the tests capture what it wrote.

func init() {
	RegisterBuiltins(
		Builtin{Name: "print", Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			out := c.Out()
			for _, a := range args {
				if err := writeOut(c, out, a.Str()); err != nil {
					return Nil(), err
				}
			}
			return Nil(), nil
		}},
		Builtin{Name: "println", Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			out := c.Out()
			if len(args) == 0 {
				return Nil(), writeOut(c, out, "\n")
			}
			for _, a := range args {
				if err := printlnValue(c, out, a); err != nil {
					return Nil(), err
				}
			}
			return Nil(), nil
		}},
		Builtin{Name: "debug", Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			out := c.Out()
			for _, a := range args {
				if err := writeOut(c, out, a.Inspect()+"\n"); err != nil {
					return Nil(), err
				}
			}
			if len(args) == 0 {
				return Nil(), nil
			}
			return args[0], nil
		}},

		Builtin{Name: "len", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Int(int64(args[0].Len())), nil
		}},
		Builtin{Name: "empty", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Bool(args[0].Len() == 0), nil
		}},
		Builtin{Name: "type", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Str(args[0].TypeName()), nil
		}},
		Builtin{Name: "is", Min: 2, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			name, err := argStr(c, args[1])
			if err != nil {
				return Nil(), err
			}
			yes, known := isKindName(args[0], name)
			if !known {
				// A shape declared in this Run is a type name too (§7.8), and it has to
				// answer for values that are *not* it — otherwise filtering a mixed array
				// by `it.is("Money")` would raise on the first element that is not one.
				//
				// It answers by **name**, which is the same question `type` answers, so
				// the two can never disagree. Identity is a question only `match` can
				// ask, because only there is the constructor itself in hand (§5.3).
				if c.rs.sh.records[name] {
					rt := args[0].recordType()
					return Bool(rt != nil && rt.Name == name), nil
				}
				return Nil(), c.ArgErrorf("is: unknown type name %s", quoteString(name))
			}
			return Bool(yes), nil
		}},

		Builtin{Name: "str", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Str(args[0].Str()), nil
		}},
		Builtin{Name: "int", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Int(args[0].Int()), nil
		}},
		Builtin{Name: "float", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Float(args[0].Float()), nil
		}},
		Builtin{Name: "bool", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Bool(args[0].Truthy()), nil
		}},
		Builtin{Name: "array", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return toArray(c, args[0])
		}},
		Builtin{Name: "dict", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return toDict(c, args[0])
		}},
		Builtin{Name: "json", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			s, err := jsonvText(c, args[0], "")
			if err != nil {
				return Nil(), err
			}
			return Str(s), nil
		}},
		Builtin{Name: "inspect", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Str(args[0].Inspect()), nil
		}},
		Builtin{Name: "hash", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			// The kind and not `type`: a record's label is not part of equality (§7.4,
			// §7.8), so it must not be part of this either, or `Money(1500, "RUB")` and
			// the dict it is equal to would hash apart.
			//
			// That fixes the label and nothing else. This hash is the rendered form, so
			// it still distinguishes things `==` does not — two dicts built in different
			// orders, `1` and `1.0` — and it always has; the docs promise it is stable
			// across runs and no more than that.
			return Int(fnv1a(args[0].Kind().String() + "\x00" + args[0].Inspect())), nil
		}},
		Builtin{Name: "dup", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return args[0].Clone(), nil
		}},
		Builtin{Name: "tap", Min: 2, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			f, err := argFunc(c, args[1])
			if err != nil {
				return Nil(), err
			}
			if _, err := evalCall(c, f, []Value{args[0]}, Nil()); err != nil {
				return Nil(), err
			}
			return args[0], nil
		}},
		Builtin{Name: "pipe", Min: 2, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			f, err := argFunc(c, args[1])
			if err != nil {
				return Nil(), err
			}
			return evalCall(c, f, []Value{args[0]}, Nil())
		}},

		Builtin{Name: "regex", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			pattern, err := argStr(c, args[0])
			if err != nil {
				return Nil(), err
			}
			flags := ""
			if len(args) > 1 {
				if flags, err = argStr(c, args[1]); err != nil {
					return Nil(), err
				}
			}
			return c.Regex(pattern, flags)
		}},

		Builtin{Name: "range", Min: 1, Max: 3, Fn: func(c *Ctx, args []Value) (Value, error) {
			return rangeBuiltin(c, args)
		}},
		Builtin{Name: "sum", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			xs, err := argElems(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return sumValues(xs), nil
		}},
		Builtin{Name: "min", Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return extremum(c, args, -1)
		}},
		Builtin{Name: "max", Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			return extremum(c, args, 1)
		}},
		Builtin{Name: "abs", Min: 1, Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			n, err := argNum(c, args[0])
			if err != nil {
				return Nil(), err
			}
			return numAbs(n), nil
		}},
		Builtin{Name: "round", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			return roundBuiltin(c, args, numRound)
		}},
		Builtin{Name: "ceil", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			return roundBuiltin(c, args, numCeil)
		}},
		Builtin{Name: "floor", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			return roundBuiltin(c, args, numFloor)
		}},
		Builtin{Name: "sort", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			return sortBuiltin(c, args)
		}},

		Builtin{Name: "format", Min: 1, Max: -1, Fn: func(c *Ctx, args []Value) (Value, error) {
			layout, err := argStr(c, args[0])
			if err != nil {
				return Nil(), err
			}
			out, err := formatValues(c, layout, args[1:])
			if err != nil {
				return Nil(), err
			}
			return Str(out), nil
		}},

		Builtin{Name: "raise", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			return Nil(), raiseValue(c, args)
		}},
		// `exit` ends the Run and hands the host a status; it is not a failure and not a
		// script error, so `try` never catches it (§8.11) and no diagnostic is printed
		// for it. Nothing here touches the process: an embedder's Run returns the error
		// and decides what a script asking to stop means (§12.1, §13.5).
		Builtin{Name: "exit", Max: 1, Fn: func(c *Ctx, args []Value) (Value, error) {
			code := int64(0)
			if len(args) > 0 {
				n, err := argInt(c, args[0])
				if err != nil {
					return Nil(), err
				}
				if n < 0 || n > 255 {
					return Nil(), c.position(argErrorf("exit code must be between 0 and 255, got %d", n))
				}
				code = n
			}
			return Nil(), c.position(exitError(code))
		}},
		Builtin{Name: "assert", Min: 1, Max: 2, Fn: func(c *Ctx, args []Value) (Value, error) {
			if args[0].Truthy() {
				return Nil(), nil
			}
			msg := "assertion failed"
			if len(args) > 1 {
				s, err := argStr(c, args[1])
				if err != nil {
					return Nil(), err
				}
				msg = s
			}
			return Nil(), c.position(raiseError(msg, Nil()))
		}},

		// `defined` is resolved structurally by the evaluator, which never evaluates its
		// operand — that is the whole point, since an unbound `$price` is legal there and
		// nowhere else. The row exists so lookups, gating and the §17 suggestions find the
		// name, and its Fn is what a value reaching it any other way gets.
		Builtin{Name: definedName, Min: 1, Max: 1, Special: true,
			Fn: func(c *Ctx, args []Value) (Value, error) {
				return Bool(!args[0].IsNil()), nil
			}},

		Builtin{Name: "rand", Max: 1, NeedsRand: true, Fn: func(c *Ctx, args []Value) (Value, error) {
			r, err := c.Rand()
			if err != nil {
				return Nil(), err
			}
			if len(args) == 0 {
				return Float(r.Float64()), nil
			}
			n, err := argInt(c, args[0])
			if err != nil {
				return Nil(), err
			}
			if n < 0 {
				n = -n
			}
			if n == 0 {
				return Float(r.Float64()), nil
			}
			return Int(r.Int63n(n)), nil
		}},
		Builtin{Name: "uuid", NeedsRand: true, Fn: func(c *Ctx, args []Value) (Value, error) {
			r, err := c.Rand()
			if err != nil {
				return Nil(), err
			}
			var b [16]byte
			for i := range b {
				b[i] = byte(r.Intn(256))
			}
			b[6] = (b[6] & 0x0f) | 0x40
			b[8] = (b[8] & 0x3f) | 0x80
			return Str(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])), nil
		}},
		Builtin{Name: "now", NeedsNow: true, Fn: func(c *Ctx, args []Value) (Value, error) {
			t, err := c.Now()
			if err != nil {
				return Nil(), err
			}
			return timeOf(t.In(c.Location())), nil
		}},
	)
}

// writeOut sends text to Options.Stdout, charging it against the step budget so a
// runaway `while true { print("x") }` is stopped by the limit rather than by the disk.
func writeOut(c *Ctx, w io.Writer, s string) error {
	if err := c.Step(int64(len(s))/64 + 1); err != nil {
		return err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return c.ErrorfKind(ErrKindIO, "write failed: %s", err)
	}
	return nil
}

// printlnValue writes one `println` argument: an Array prints one element per line, everything
// else is its `str` followed by a newline (§12.1). A Range is not an Array here — it
// prints as `1..3`, which is both its `str` and the only form that cannot materialise a
// billion elements on the way to the writer.
func printlnValue(c *Ctx, w io.Writer, v Value) error {
	if v.Kind() == KArray {
		for _, e := range v.Elems() {
			if err := printlnValue(c, w, e); err != nil {
				return err
			}
		}
		return nil
	}
	return writeOut(c, w, v.Str()+"\n")
}

// isKindName answers `is(x, name)` (§12.1). The names are the §7.2 type names plus the
// two internal kinds a script can still see, "range" and "time". A Range answers true to
// "array" while `type(r)` stays "range" (§12.10) — the one place the two disagree. An
// unknown name is an argument error rather than false, so a pasted `x.is("Integer")`
// fails loudly instead of silently never matching.
func isKindName(v Value, name string) (yes, known bool) {
	switch name {
	case "nil":
		return v.Kind() == KNil, true
	case "bool":
		return v.Kind() == KBool, true
	case "int":
		return v.Kind() == KInt, true
	case "float":
		return v.Kind() == KFloat, true
	case "string":
		return v.Kind() == KString, true
	case "regex":
		return v.Kind() == KRegex, true
	case "array":
		return v.Kind() == KArray || v.Kind() == KRange, true
	case "dict":
		return v.Kind() == KDict, true
	case "function":
		return v.Kind() == KFunc, true
	case "range":
		return v.Kind() == KRange, true
	case "time":
		return v.Kind() == KTime, true
	}
	return false, false
}

// toArray is the `array` conversion of §12.1: an Array is itself, a Range materialises,
// a Dict becomes its `[k, v]` pairs, nil becomes empty and anything else is wrapped. A
// String wraps too — `"ab".array` is `["ab"]`; the runes are `chars` (§12.2).
func toArray(c *Ctx, v Value) (Value, error) {
	switch v.Kind() {
	case KArray:
		return v, nil
	case KRange:
		if err := c.CheckCollection(v.Len()); err != nil {
			return Nil(), err
		}
		return arrayOf(v.Elems()), nil
	case KDict:
		return arrayOf(v.odict().Pairs()), nil
	case KNil:
		return Array(), nil
	}
	return Array(v), nil
}

// toDict is the `dict` conversion of §12.1: a Dict is itself and an Array of `[k, v]`
// pairs becomes one. Nothing else converts — there is no coercion (§9.1), and a value
// that is not already a pair list has no defensible key.
func toDict(c *Ctx, v Value) (Value, error) {
	switch v.Kind() {
	case KDict:
		return v, nil
	case KArray:
		xs := v.Elems()
		d := NewOrderedDictCap(len(xs))
		for _, pair := range xs {
			if pair.Kind() != KArray || pair.Len() != 2 {
				return Nil(), c.ArgErrorf("dict: expected an array of [key, value] pairs")
			}
			if err := d.Set(pair.Index(0), pair.Index(1)); err != nil {
				return Nil(), c.TypeErrorf("%s", err.Error())
			}
		}
		return dictOf(d), nil
	}
	return Nil(), c.TypeErrorf("dict expects an array of pairs or a dict, got %s", v.TypeName())
}

// argFunc reads an argument that must be callable. The trailing closure of `x.tap { … }`
// arrives here as an ordinary last argument (§4.2), so there is nothing to unwrap.
func argFunc(c *Ctx, v Value) (Value, error) {
	if v.Kind() != KFunc {
		return Nil(), c.TypeErrorf("%s expects a closure, got %s", c.Name(), v.TypeName())
	}
	return v, nil
}

// argElems reads a sequence argument: an Array or a materialised Range.
func argElems(c *Ctx, v Value) ([]Value, error) {
	switch v.Kind() {
	case KArray:
		return v.Elems(), nil
	case KRange:
		if err := c.CheckCollection(v.Len()); err != nil {
			return nil, err
		}
		return v.Elems(), nil
	}
	return nil, c.TypeErrorf("%s expects an array, got %s", c.Name(), v.TypeName())
}

// sumValues adds numerically, staying in Int until an operand or an overflow forces
// Float (D9). An empty sequence is 0, never nil.
func sumValues(xs []Value) Value {
	acc := Int(0)
	for _, x := range xs {
		if acc.Kind() == KInt && x.Kind() == KInt {
			s := acc.n + x.n
			// Overflow shows up as a sign flip that both operands disagree with.
			if (acc.n >= 0) == (x.n >= 0) && (s >= 0) != (acc.n >= 0) {
				acc = Float(float64(acc.n) + float64(x.n))
				continue
			}
			acc = Int(s)
			continue
		}
		acc = Float(acc.Float() + x.Float())
	}
	return acc
}

// extremum backs min and max, which take either one sequence or a bare argument list
// (§12.1). sign is -1 for min and 1 for max; an empty input is nil.
func extremum(c *Ctx, args []Value, sign int) (Value, error) {
	xs := args
	if len(args) == 1 && (args[0].Kind() == KArray || args[0].Kind() == KRange) {
		var err error
		if xs, err = argElems(c, args[0]); err != nil {
			return Nil(), err
		}
	}
	if len(xs) == 0 {
		return Nil(), nil
	}
	best := xs[0]
	for _, x := range xs[1:] {
		cmp, ok := compare(x, best)
		if !ok {
			return Nil(), c.TypeErrorf("%s: %s and %s are not comparable", c.Name(), x.TypeName(), best.TypeName())
		}
		if cmp*sign > 0 {
			best = x
		}
	}
	return best, nil
}

func roundBuiltin(c *Ctx, args []Value, f func(Value, int) Value) (Value, error) {
	n, err := argNum(c, args[0])
	if err != nil {
		return Nil(), err
	}
	d, err := optInt(c, args, 1, 0)
	if err != nil {
		return Nil(), err
	}
	return f(n, int(d)), nil
}

// rangeBuiltin is the half-open `range` of §12.1 — Go's and Python's shape, not the
// `..` operator's: range(3) and range(0, 3) are both [0, 1, 2].
func rangeBuiltin(c *Ctx, args []Value) (Value, error) {
	var lo, hi, step int64
	var err error
	if len(args) == 1 {
		if hi, err = argInt(c, args[0]); err != nil {
			return Nil(), err
		}
	} else {
		if lo, err = argInt(c, args[0]); err != nil {
			return Nil(), err
		}
		if hi, err = argInt(c, args[1]); err != nil {
			return Nil(), err
		}
	}
	step = 1
	if len(args) > 2 {
		if step, err = argInt(c, args[2]); err != nil {
			return Nil(), err
		}
	}
	if step == 0 {
		return Nil(), c.ArgErrorf("range: step cannot be 0")
	}
	n := int64(0)
	if step > 0 && hi > lo {
		n = (hi - lo + step - 1) / step
	} else if step < 0 && hi < lo {
		n = (lo - hi - step - 1) / (-step)
	}
	if err := c.CheckCollection(int(min(n, int64(1)<<31))); err != nil {
		return Nil(), err
	}
	out := make([]Value, 0, n)
	for i, k := lo, int64(0); k < n; i, k = i+step, k+1 {
		out = append(out, Int(i))
	}
	if err := c.Step(n); err != nil {
		return Nil(), err
	}
	return arrayOf(out), nil
}

// sortBuiltin is `sort(xs)` and `sort(xs) { (a, b) -> … }`. The closure is a **comparator**
// returning a `<=>`-style int (§12.1), not a key extractor: one name, one meaning (D17),
// and it is the same row Array#sort exposes.
//
// The sort is stable and returns a new array; `sort_in_place` is the mutating variant
// and lives in §12.3.
func sortBuiltin(c *Ctx, args []Value) (Value, error) {
	src, err := argElems(c, args[0])
	if err != nil {
		return Nil(), err
	}
	out := make([]Value, len(src))
	copy(out, src)

	if len(args) < 2 {
		var failed error
		sort.SliceStable(out, func(i, j int) bool {
			cmp, ok := compare(out[i], out[j])
			if !ok && failed == nil {
				failed = c.TypeErrorf("sort: %s and %s are not comparable",
					out[i].TypeName(), out[j].TypeName())
			}
			return cmp < 0
		})
		if failed != nil {
			return Nil(), failed
		}
		return arrayOf(out), nil
	}

	cmpFn, err := argFunc(c, args[1])
	if err != nil {
		return Nil(), err
	}
	// A comparator that raises, or that a `break` escapes, stops the sort: the first
	// error is kept and returned unchanged, and the remaining comparisons answer false
	// so Go's sort finishes without calling back into the script again.
	var failed error
	sort.SliceStable(out, func(i, j int) bool {
		if failed != nil {
			return false
		}
		if err := c.Step(1); err != nil {
			failed = err
			return false
		}
		r, err := evalCall(c, cmpFn, []Value{out[i], out[j]}, Nil())
		if err != nil {
			failed = err
			return false
		}
		return r.Int() < 0
	})
	if failed != nil {
		return Nil(), failed
	}
	return arrayOf(out), nil
}

// ---------------------------------------------------------------------------
// bool and function receivers (§12.9)
// ---------------------------------------------------------------------------
//
// nil needs no rows: `str`, `int`, `float`, `array`, `inspect` and `json` are the free
// functions above, and every one of them already answers for nil the way §12.9 says.
//
// bool needs none either, for the same reason — and it must not have `&`, `|` and `^`.
// §12.9 is explicit that `&&`/`||`/`!` are the whole set, `&`/`|`/`^` are not lexemes
// (§3.9), and `.` is followed by an identifier, so rows under those names were
// unreachable from any program: a second spelling of `&&` that only Go could call, which
// is exactly what D17 forbids. They are also why the bit operations of §12.5 are
// functions.

func init() {
	RegisterMethods(KFunc,
		Method{Name: "call", Max: -1, Fn: func(c *Ctx, recv Value, args []Value) (Value, error) {
			// A trailing closure is already the last element of args (§4.2), so there is
			// nothing to attach here.
			return evalCall(c, recv, args, Nil())
		}},
		Method{Name: "arity", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			f := recv.fn()
			if f == nil {
				return Int(0), nil
			}
			return Int(int64(f.Arity)), nil
		}},
	)
}
