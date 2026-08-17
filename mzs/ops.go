package mzs

import (
	"math"
	"strings"
	"time"

	"mzs/internal/token"
)

// Operators live here: the numeric tower of D9, the match operators of §8.4, equality
// and ordered comparison. There is no coercion anywhere in this file — a string meets a
// number only through an explicit `.int`/`.float`/`.str` (§9.1).
//
// Everything takes an `ev` rather than bare values because three of the operators need
// more than their operands: `+` and `*` charge the string/collection caps of §14.2, `~`
// needs the regex step budget, and `%` on a string reaches into the stdlib formatter.

func (e ev) binary(op token.Kind, a, b Value) (Value, error) {
	switch op {
	case token.PLUS:
		return e.add(a, b)
	case token.MINUS:
		return e.sub(a, b)
	case token.STAR:
		return e.mul(a, b)
	case token.SLASH:
		return e.div(a, b)
	case token.PERCENT:
		return e.mod(a, b)
	case token.POW:
		return e.pow(a, b)
	case token.EQ:
		return Bool(a.Equal(b)), nil
	case token.NEQ:
		return Bool(!a.Equal(b)), nil
	case token.TILDE:
		ok, err := e.matches(a, b)
		return Bool(ok), err
	case token.NTILDE:
		ok, err := e.matches(a, b)
		return Bool(!ok), err
	case token.LT, token.LTE, token.GT, token.GTE, token.SPACESHIP:
		return e.ordered(op, a, b)
	}
	return Nil(), typeErrorf("unsupported operator %s", op)
}

// ---------------------------------------------------------------------------
// Arithmetic (§8.3)
// ---------------------------------------------------------------------------

func (e ev) add(a, b Value) (Value, error) {
	if a.IsNum() && b.IsNum() {
		return addNum(a, b), nil
	}
	switch a.Kind() {
	case KString:
		if b.Kind() != KString {
			return Nil(), typeErrorf("cannot add %s to string", b.Kind())
		}
		return e.concat(a.Str(), b.Str())
	case KArray:
		if b.Kind() != KArray {
			return Nil(), typeErrorf("cannot add %s to array", b.Kind())
		}
		xs, ys := a.Elems(), b.Elems()
		if err := e.rs.checkCollection(len(xs) + len(ys)); err != nil {
			return Nil(), err
		}
		out := make([]Value, 0, len(xs)+len(ys))
		out = append(out, xs...)
		out = append(out, ys...)
		return arrayOf(out), nil
	case KDict:
		if b.Kind() != KDict {
			return Nil(), typeErrorf("cannot add %s to dict", b.Kind())
		}
		if err := recordAddError(a, b); err != nil {
			return Nil(), err
		}
		d := a.odict().Clone()
		d.Merge(b.odict())
		return dictOf(d), nil
	case KTime:
		if b.IsNum() {
			return timeOf(a.Time().Add(time.Duration(b.Int()) * time.Second)), nil
		}
		return Nil(), typeErrorf("cannot add %s to time", b.Kind())
	}
	return Nil(), typeErrorf("cannot add %s to %s", b.Kind(), a.Kind())
}

func (e ev) sub(a, b Value) (Value, error) {
	if a.IsNum() && b.IsNum() {
		return subNum(a, b), nil
	}
	switch a.Kind() {
	case KArray:
		if b.Kind() != KArray {
			return Nil(), typeErrorf("cannot subtract %s from array", b.Kind())
		}
		drop := b.Elems()
		xs := a.Elems()
		out := make([]Value, 0, len(xs))
		for _, x := range xs {
			keep := true
			for _, d := range drop {
				if x.Equal(d) {
					keep = false
					break
				}
			}
			if keep {
				out = append(out, x)
			}
		}
		return arrayOf(out), nil
	case KTime:
		if b.IsNum() {
			return timeOf(a.Time().Add(-time.Duration(b.Int()) * time.Second)), nil
		}
		if b.Kind() == KTime {
			return Int(int64(a.Time().Sub(b.Time()) / time.Second)), nil
		}
	}
	return Nil(), typeErrorf("cannot subtract %s from %s", b.Kind(), a.Kind())
}

func (e ev) mul(a, b Value) (Value, error) {
	if a.IsNum() && b.IsNum() {
		return mulNum(a, b), nil
	}
	switch a.Kind() {
	case KString:
		if !b.IsNum() {
			return Nil(), typeErrorf("cannot multiply string by %s", b.Kind())
		}
		n := int(b.Int())
		if n <= 0 {
			return Str(""), nil
		}
		if err := e.rs.checkString(len(a.Str()) * n); err != nil {
			return Nil(), err
		}
		return Str(strings.Repeat(a.Str(), n)), nil
	case KArray:
		if !b.IsNum() {
			return Nil(), typeErrorf("cannot multiply array by %s", b.Kind())
		}
		n := int(b.Int())
		xs := a.Elems()
		if n <= 0 {
			return Array(), nil
		}
		if err := e.rs.checkCollection(len(xs) * n); err != nil {
			return Nil(), err
		}
		out := make([]Value, 0, len(xs)*n)
		for i := 0; i < n; i++ {
			out = append(out, xs...)
		}
		return arrayOf(out), nil
	}
	return Nil(), typeErrorf("cannot multiply %s by %s", a.Kind(), b.Kind())
}

func (e ev) div(a, b Value) (Value, error) {
	if !a.IsNum() || !b.IsNum() {
		return Nil(), typeErrorf("cannot divide %s by %s", a.Kind(), b.Kind())
	}
	if a.Kind() == KInt && b.Kind() == KInt {
		y := b.Int()
		if y == 0 {
			return Nil(), zeroDivError()
		}
		x := a.Int()
		// The one int division that overflows; D9 says promote rather than wrap.
		if x == math.MinInt64 && y == -1 {
			return Float(-float64(x)), nil
		}
		return Int(x / y), nil
	}
	// Float division by zero is IEEE and never an error (§8.3).
	return Float(a.Float() / b.Float()), nil
}

func (e ev) mod(a, b Value) (Value, error) {
	if a.Kind() == KString {
		return e.formatString(a, b)
	}
	if !a.IsNum() || !b.IsNum() {
		return Nil(), typeErrorf("cannot take %s modulo %s", a.Kind(), b.Kind())
	}
	if a.Kind() == KInt && b.Kind() == KInt {
		x, y := a.Int(), b.Int()
		if y == 0 {
			return Nil(), zeroDivError()
		}
		if x == math.MinInt64 && y == -1 {
			return Int(0), nil
		}
		m := x % y
		// `%` takes the sign of the divisor, so -7 % 3 is 2 (§8.3).
		if m != 0 && (m < 0) != (y < 0) {
			m += y
		}
		return Int(m), nil
	}
	x, y := a.Float(), b.Float()
	m := math.Mod(x, y)
	if m != 0 && (m < 0) != (y < 0) {
		m += y
	}
	return Float(m), nil
}

// formatString is `"%s" % args`. The formatter itself is `format` in the stdlib
// (§12.7), and D17 allows it exactly one name, so the operator borrows that one
// implementation rather than growing a second.
func (e ev) formatString(a, b Value) (Value, error) {
	args := []Value{b}
	if b.Kind() == KArray {
		args = b.Elems()
	}
	if m, ok := LookupMethod(KString, "format"); ok {
		restore := e.enterCall("format", e.cx.Pos(), args)
		v, err := m.Fn(e.cx, a, args)
		restore()
		return v, err
	}
	if fn, ok := LookupBuiltin("format"); ok {
		all := make([]Value, 0, len(args)+1)
		all = append(append(all, a), args...)
		return e.invokeBuiltin(fn, all, e.cx.Pos())
	}
	return Nil(), typeErrorf("cannot take string modulo %s", b.Kind())
}

func (e ev) pow(a, b Value) (Value, error) {
	if !a.IsNum() || !b.IsNum() {
		return Nil(), typeErrorf("cannot raise %s to %s", a.Kind(), b.Kind())
	}
	if a.Kind() == KInt && b.Kind() == KInt {
		exp := b.Int()
		if exp >= 0 {
			if v, ok := intPow(a.Int(), exp); ok {
				return v, nil
			}
		}
	}
	return Float(math.Pow(a.Float(), b.Float())), nil
}

// concat is the one place strings grow, so it is the one place the §14.2 cap is
// charged.
func (e ev) concat(a, b string) (Value, error) {
	if err := e.rs.checkString(len(a) + len(b)); err != nil {
		return Nil(), err
	}
	return Str(a + b), nil
}

// addNum, subNum and mulNum implement D9: int64 arithmetic that promotes to float on
// overflow instead of wrapping.
func addNum(a, b Value) Value {
	if a.Kind() == KInt && b.Kind() == KInt {
		x, y := a.Int(), b.Int()
		s := x + y
		if (x^s)&(y^s) >= 0 {
			return Int(s)
		}
		return Float(float64(x) + float64(y))
	}
	return Float(a.Float() + b.Float())
}

func subNum(a, b Value) Value {
	if a.Kind() == KInt && b.Kind() == KInt {
		x, y := a.Int(), b.Int()
		d := x - y
		if (x^y)&(x^d) >= 0 {
			return Int(d)
		}
		return Float(float64(x) - float64(y))
	}
	return Float(a.Float() - b.Float())
}

func mulNum(a, b Value) Value {
	if a.Kind() == KInt && b.Kind() == KInt {
		if p, ok := mulOverflow(a.Int(), b.Int()); ok {
			return Int(p)
		}
		return Float(a.Float() * b.Float())
	}
	return Float(a.Float() * b.Float())
}

func mulOverflow(x, y int64) (int64, bool) {
	if x == 0 || y == 0 {
		return 0, true
	}
	p := x * y
	if x == -1 && y == math.MinInt64 || y == -1 && x == math.MinInt64 {
		return 0, false
	}
	if p/y != x {
		return 0, false
	}
	return p, true
}

func intPow(base, exp int64) (Value, bool) {
	result, b, n := int64(1), base, exp
	for n > 0 {
		if n&1 == 1 {
			r, ok := mulOverflow(result, b)
			if !ok {
				return Nil(), false
			}
			result = r
		}
		n >>= 1
		if n > 0 {
			r, ok := mulOverflow(b, b)
			if !ok {
				return Nil(), false
			}
			b = r
		}
	}
	return Int(result), true
}

// ---------------------------------------------------------------------------
// Unary operators
// ---------------------------------------------------------------------------

func (e ev) unary(op token.Kind, x Value) (Value, error) {
	switch op {
	case token.BANG:
		return Bool(!x.Truthy()), nil
	case token.PLUS:
		if x.IsNum() {
			return x, nil
		}
		return Nil(), typeErrorf("cannot apply unary + to %s", x.Kind())
	case token.MINUS:
		switch x.Kind() {
		case KInt:
			n := x.Int()
			if n == math.MinInt64 {
				return Float(-float64(n)), nil
			}
			return Int(-n), nil
		case KFloat:
			return Float(-x.Float()), nil
		}
		return Nil(), typeErrorf("cannot negate %s", x.Kind())
	}
	return Nil(), typeErrorf("unsupported unary operator %s", op)
}

// ---------------------------------------------------------------------------
// The match operators (D5, §8.4)
// ---------------------------------------------------------------------------

// matches is `~` in either order: `S ~ R` and `R ~ S` both ask whether the pattern
// matches the string, and both answer with a Bool. `nil ~ R` is false, so an unbound
// $var in a condition simply does not match; anything else non-string is an error,
// because there is no implicit `str` conversion (§8.4). The index of a match is
// `s.index(/re/)`, never this operator.
func (e ev) matches(a, b Value) (bool, error) {
	var r, s Value
	switch {
	case a.Kind() == KRegex:
		r, s = a, b
	case b.Kind() == KRegex:
		r, s = b, a
	default:
		return false, typeErrorf("~ needs a regex operand, got %s and %s", a.Kind(), b.Kind())
	}
	if s.IsNil() {
		return false, nil
	}
	if s.Kind() != KString {
		return false, typeErrorf("cannot match against %s", s.Kind())
	}
	ok, err := r.rx().MatchErr(s.Str())
	if err != nil {
		return false, regexErrorf("%s", err.Error())
	}
	return ok, nil
}

// ---------------------------------------------------------------------------
// Ordering (§7.5)
// ---------------------------------------------------------------------------

// ordered compares two values. Mixed number/string ordering is an error rather than a
// guess: `$n >= 2` on the string "3" tells the author to write `$n.int >= 2` (§9.1).
func (e ev) ordered(op token.Kind, a, b Value) (Value, error) {
	c, ok := compare(a, b)
	if !ok {
		if op == token.SPACESHIP {
			return Nil(), nil
		}
		return Nil(), typeErrorf("cannot compare %s with %s", a.Kind(), b.Kind())
	}
	switch op {
	case token.LT:
		return Bool(c < 0), nil
	case token.LTE:
		return Bool(c <= 0), nil
	case token.GT:
		return Bool(c > 0), nil
	case token.GTE:
		return Bool(c >= 0), nil
	}
	return Int(int64(c)), nil
}
