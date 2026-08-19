package mzs

import (
	"math/big"
	"strconv"
	"strings"
)

// Exact decimal numbers — the `decimal` module (§12.15).
//
// mzs has two numeric kinds and neither of them can hold money. `0.1 + 0.2` is
// `0.30000000000000004` because a Float is binary (§7.1), and an Int that overflows is
// promoted to Float rather than raised on (D9), so the digits go quietly. A script that
// invoices, taxes or splits a bill has had one way out — count in kopecks by hand — and
// this module is the other one:
//
//	include decimal
//	price = decimal.of("1500.35")
//	total = decimal.plus(price, decimal.times(price, decimal.of("0.20")))
//	decimal.str(total, 2)                                   # "1800.42"
//
// **A decimal is not a fourteenth kind.** It is a dict of two entries carrying the
// record label of §7.8 — the same move `to_set` makes for sets (§12.3), and for the same
// reason: a new kind costs §7 in full (equality, hashing, ordering, JSON, comparison
// against Int and Float) and buys operators the module can spell out.
//
//	decimal.of("1500.35")            # {"units": 150035, "scale": 2}
//	type(decimal.of("1.5"))          # "Decimal"
//	decimal.of("1.5").is("dict")     # true — it never stopped being one
//
// The value is `units × 10 ** -scale`, and it is **canonical**: the trailing zeros of the
// fraction are shed when the value is built, so one number has exactly one form. That is
// what makes the dict equality of §7.4 the numeric one — `decimal.of("1.50") ==
// decimal.of("1.5")` is true, and `hash` agrees — and it is why there is no `decimal.eq`
// to be a second spelling of `==` (D17).
//
// What the module does not do is teach `<` and `sort` about its values: a dict has no
// ordering (§7.5), so `a < b` on two decimals is `cannot compare dict with dict` and
// `decimal.cmp` is the operation. That is the trade the dict form makes, and it is the
// right way round — a decimal held as a string would answer `"9.00" < "10.00"` with
// `false` and never say a word.

const (
	// maxDecScale is how many decimal places a decimal may carry, and maxDecDigits is how
	// many the unscaled value has room for. The unscaled value lives in an Int (§7.1), so
	// nineteen digits is the whole budget; a scale of 18 already spends all but one of
	// them on the fraction, and past it `10 ** scale` stops fitting in the same place the
	// units do.
	maxDecScale  = 18
	maxDecDigits = 19

	// decTypeName is what `type(d)` answers. It is capitalised because it is a record
	// name (§7.8) and the shapes in this language are written that way.
	decTypeName = "Decimal"

	decUnitsKey = "units"
	decScaleKey = "scale"

	// The two rounding modes of §12.15. half_up is the default because it is what
	// `round` already does on a number (§12.5) — one language, one default — and
	// half_even is the banker's rounding a ledger asks for by name.
	decModeHalfUp   = "half_up"
	decModeHalfEven = "half_even"
)

// decShape is the label every decimal carries. One *RecordType is shared by every value
// the module builds, which is what `type(d) == "Decimal"` reads; identity matters only to
// a `match` arm (§5.3), and no arm can name this one, because the module exposes a parser
// and not a constructor.
var decShape = &RecordType{Name: decTypeName, Fields: []string{decUnitsKey, decScaleKey}}

func init() {
	// Registration order is `decimal.keys` order: a module is a Dict and a Dict is
	// insertion-ordered (§8.13).
	RegisterModuleFunc("decimal", "of", 1, 1, decvOf)
	RegisterModuleFunc("decimal", "plus", 2, -1, decvPlus)
	RegisterModuleFunc("decimal", "minus", 2, 2, decvMinus)
	RegisterModuleFunc("decimal", "times", 2, -1, decvTimes)
	RegisterModuleFunc("decimal", "div", 2, 4, decvDiv)
	RegisterModuleFunc("decimal", "neg", 1, 1, decvNeg)
	RegisterModuleFunc("decimal", "abs", 1, 1, decvAbs)
	RegisterModuleFunc("decimal", "cmp", 2, 2, decvCmp)
	RegisterModuleFunc("decimal", "round", 2, 3, decvRound)
	RegisterModuleFunc("decimal", "str", 1, 2, decvStr)
	RegisterModuleFunc("decimal", "float", 1, 1, decvFloat)
	RegisterModuleFunc("decimal", "int", 1, 1, decvInt)
	RegisterModuleFunc("decimal", "sum", 1, 1, decvSum)
	RegisterModuleFunc("decimal", "split", 3, 3, decvSplit)
}

// ---------------------------------------------------------------------------
// The number behind the dict
// ---------------------------------------------------------------------------

// decNum is one decimal while the module is working on it: an unscaled integer and the
// number of decimal places it is read at. The integer is a big.Int rather than an int64
// so that an intermediate result — the aligned operands of an add, the numerator of a
// division — cannot overflow *silently* on its way to a value that would have fitted.
// The one place the width is checked is decValue, where the number becomes a script
// value and has to fit in the Int that holds it.
type decNum struct {
	u     *big.Int
	scale int
}

var (
	decOne  = big.NewInt(1)
	decTwo  = big.NewInt(2)
	decFive = big.NewInt(5)
	decTen  = big.NewInt(10)
)

// decPow10 is 10**n as a big.Int, for n >= 0.
func decPow10(n int) *big.Int {
	return new(big.Int).Exp(decTen, big.NewInt(int64(n)), nil)
}

// decZero is the canonical zero: one form, scale 0, so `decimal.of("0.00")` and
// `decimal.of(0)` are the same value.
func decZero() decNum { return decNum{u: new(big.Int), scale: 0} }

// canonical sheds the trailing zeros of the fraction, which is what gives one number one
// form and lets `==` and `hash` (§7.4, §7.6) be the numeric questions. It is applied on
// the way *into* a value and never on the way out of a formatter: `decimal.str(d, 2)`
// pads back to two places, because how many places to show is a question about the
// output and not about the number.
func (n decNum) canonical() decNum {
	if n.u.Sign() == 0 {
		return decZero()
	}
	u := new(big.Int).Set(n.u)
	q, r := new(big.Int), new(big.Int)
	for n.scale > 0 {
		q.QuoRem(u, decTen, r)
		if r.Sign() != 0 {
			break
		}
		u.Set(q)
		n.scale--
	}
	return decNum{u: u, scale: n.scale}
}

// at returns the unscaled value read at `places` places, exactly, for places >= scale.
func (n decNum) at(places int) *big.Int {
	if places == n.scale {
		return new(big.Int).Set(n.u)
	}
	return new(big.Int).Mul(n.u, decPow10(places-n.scale))
}

// decAlign reads two decimals at one scale, which is the first step of every comparison
// and of every sum. The scale it picks is the larger of the two, so nothing is lost.
func decAlign(a, b decNum) (ua, ub *big.Int, scale int) {
	scale = a.scale
	if b.scale > scale {
		scale = b.scale
	}
	return a.at(scale), b.at(scale), scale
}

// decString renders a decimal the way `decimal.str` does with no places given: the
// canonical shortest form, sign in front, no exponent, ever.
func decString(n decNum) string {
	digits := n.u.String()
	neg := strings.HasPrefix(digits, "-")
	if neg {
		digits = digits[1:]
	}
	if n.scale > 0 {
		if len(digits) <= n.scale {
			digits = strings.Repeat("0", n.scale-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-n.scale] + "." + digits[len(digits)-n.scale:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// decQuoRound divides and rounds in one place, because every row that rounds — `round`,
// `str` with places, `div` with places — is this division and differs only in what it
// divides by. den must not be zero; the sign convention of big.Int's QuoRem (truncation
// toward zero, remainder signed like the numerator) is what the tie test below reads.
func decQuoRound(num, den *big.Int, mode string) *big.Int {
	if den.Sign() < 0 {
		num, den = new(big.Int).Neg(num), new(big.Int).Neg(den)
	}
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	twice := new(big.Int).Abs(r)
	twice.Mul(twice, decTwo)
	switch cmp := twice.Cmp(den); {
	case cmp > 0, cmp == 0 && mode == decModeHalfUp, cmp == 0 && q.Bit(0) == 1:
		// Away from zero: past the half, or exactly on it under half_up, or exactly on
		// it with an odd quotient under half_even (that is what "banker's" means).
		if num.Sign() < 0 {
			q.Sub(q, decOne)
		} else {
			q.Add(q, decOne)
		}
	}
	return q
}

// decRescale moves a decimal to `places` decimal places, rounding when that loses
// digits. A negative `places` rounds to tens, hundreds and so on, exactly as
// `1250.round(-2)` does on an Int (§12.5), and the result is then a whole number.
func decRescale(n decNum, places int, mode string) decNum {
	switch {
	case places >= n.scale:
		return decNum{u: n.at(places), scale: places}
	case places >= 0:
		return decNum{u: decQuoRound(n.u, decPow10(n.scale-places), mode), scale: places}
	default:
		q := decQuoRound(n.u, decPow10(n.scale-places), mode)
		return decNum{u: q.Mul(q, decPow10(-places)), scale: 0}
	}
}

// ---------------------------------------------------------------------------
// Reading and building the script value
// ---------------------------------------------------------------------------

// decValue is where a number becomes a value: canonical form, a width check, and the
// record label. Every member answers through here, so no path can hand back a decimal
// that `==` would read differently from an equal one built another way.
func decValue(c *Ctx, n decNum) (Value, error) {
	n = n.canonical()
	if n.scale > maxDecScale {
		return Nil(), c.ErrorfKind(ErrKindDecimal,
			"%s: the result needs %d decimal places and a decimal holds %d",
			c.Name(), n.scale, maxDecScale)
	}
	if !n.u.IsInt64() {
		return Nil(), c.ErrorfKind(ErrKindDecimal,
			"%s: %s does not fit a decimal (the digits live in an int, so -2**63 <= units < 2**63)",
			c.Name(), decString(n))
	}
	d := NewOrderedDictCap(2)
	_ = d.Set(Str(decUnitsKey), Int(n.u.Int64()))
	_ = d.Set(Str(decScaleKey), Int(int64(n.scale)))
	d.rec = decShape
	// `is("Decimal")` answers by name and only for a name the Run has actually used
	// (§7.8), so the shape has to be declared where it is first built rather than at
	// include time — a program that never makes a decimal never learns the name.
	c.rs.declareRecord(decTypeName)
	return dictOf(d), nil
}

// decArg reads an operand of an arithmetic row: a decimal, or an Int, which is exact and
// therefore no conversion at all. A Float is refused with the fix rather than converted,
// because a float that reached this call has already lost the digits the module exists to
// keep — `decimal.of(0.1 + 0.2)` would be 0.30000000000000004 and look like a bug in
// mzs. A String is refused too: conversions are explicit (§9.1) and `decimal.of` is the
// one that reads text.
func decArg(c *Ctx, v Value) (decNum, error) {
	switch v.Kind() {
	case KDict:
		return decFromDict(c, v)
	case KInt:
		return decNum{u: big.NewInt(v.Int()), scale: 0}, nil
	case KFloat:
		return decNum{}, c.TypeErrorf(
			"%s: a float has already lost the exact digits; write the number as text — decimal.of(%s)",
			c.Name(), quoteString(decFloatText(v.Float())))
	case KString:
		return decNum{}, c.TypeErrorf(
			"%s expects a decimal or an int, got string; read it first with decimal.of(%s)",
			c.Name(), quoteString(v.Str()))
	default:
		return decNum{}, c.TypeErrorf("%s expects a decimal or an int, got %s", c.Name(), v.Kind())
	}
}

// decFromDict reads the two entries a decimal is made of. The label is not required: a
// decimal that went through `json` and came back is a plain dict with the same entries
// (§7.8 — the label is provenance, not content), and refusing it would make storing a
// price a one-way trip.
func decFromDict(c *Ctx, v Value) (decNum, error) {
	d := v.odict()
	if d == nil {
		return decNum{}, decNotADict(c, v)
	}
	units, uok := d.Get(Str(decUnitsKey))
	scale, sok := d.Get(Str(decScaleKey))
	if !uok || !sok || units.Kind() != KInt || scale.Kind() != KInt {
		return decNum{}, decNotADict(c, v)
	}
	s := scale.Int()
	if s < 0 || s > maxDecScale {
		return decNum{}, c.TypeErrorf("%s expects a decimal: 'scale' is %d, and a decimal holds 0..%d places",
			c.Name(), s, maxDecScale)
	}
	return decNum{u: big.NewInt(units.Int()), scale: int(s)}, nil
}

func decNotADict(c *Ctx, v Value) error {
	return c.TypeErrorf("%s expects a decimal — a dict of int 'units' and int 'scale' — got %s; build one with decimal.of",
		c.Name(), v.Inspect())
}

// decFloatText is the float spelled the way `str` spells it (§12.7), which is what the
// diagnostic above hands back as a string literal to parse instead.
func decFloatText(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// decParse reads the text form: an optional sign, digits, an optional dot with more
// digits. No exponent, no thousands separator and no decimal comma — a decimal comes
// from a price list or a CSV column, where "1 500,35" and "1.5e3" are the shapes that
// mean the reader guessed, and guessing is what this module is for avoiding.
func decParse(c *Ctx, text string) (decNum, error) {
	s := strings.TrimSpace(text)
	// Everything a diagnostic quotes back goes through ellipsis: the text may be as
	// long as a string is allowed to be (§14.2), and an eight-megabyte error message is
	// not a message.
	shown := quoteString(ellipsis(s))
	bad := func() (decNum, error) {
		return decNum{}, c.ErrorfKind(ErrKindDecimal,
			"%s: cannot read %s as a decimal (digits, one dot and an optional sign — %s)",
			c.Name(), shown, quoteString("1500.35"))
	}
	neg := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if !decDigits(intPart) || (hasDot && !decDigits(fracPart)) {
		return bad()
	}
	if intPart == "" && fracPart == "" {
		return bad()
	}
	if len(fracPart) > maxDecScale {
		return decNum{}, c.ErrorfKind(ErrKindDecimal,
			"%s: %s has %d decimal places and a decimal holds %d",
			c.Name(), shown, len(fracPart), maxDecScale)
	}
	// Leading zeros are free; significant digits are not, and the count is checked
	// *before* anything is converted. A decimal that cannot fit is an error either way,
	// but big.Int's SetString is superlinear, and a script may hand this row an
	// eight-megabyte string of digits (§14.2): a minute spent inside one call is a
	// minute the deadline and the step budget cannot interrupt (§14.1).
	whole := strings.TrimLeft(intPart, "0")
	if len(whole) > maxDecDigits {
		return decNum{}, c.ErrorfKind(ErrKindDecimal,
			"%s: %s has %d digits before the dot and a decimal holds %d (the digits live in an int, so -2**63 <= units < 2**63)",
			c.Name(), shown, len(whole), maxDecDigits)
	}
	u, ok := new(big.Int).SetString("0"+whole+fracPart, 10)
	if !ok {
		return bad()
	}
	if neg {
		u.Neg(u)
	}
	return decNum{u: u, scale: len(fracPart)}, nil
}

// decDigits reports whether s is ASCII digits only. An empty part is allowed — ".5" and
// "5." are both readable — but decParse refuses the pair of them.
func decDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// decPlaces reads a `places` argument. The bound is the same one a decimal itself has,
// and a negative value is the `round(-2)` of §12.5 — rounding to hundreds — which only
// the rows that round accept.
//
// An argument that is *there* is read, whatever it holds: an explicit `nil` is a type
// error rather than a second way of writing "omitted", exactly as it is for `round(nil)`,
// `255.str(nil)`, `"abc".slice(1, nil)` and `time.parse(s, nil)`. One optional argument,
// one way to leave it out.
func decPlaces(c *Ctx, v Value, allowNegative bool) (int, error) {
	n, err := argInt(c, v)
	if err != nil {
		return 0, err
	}
	low := int64(0)
	if allowNegative {
		low = -maxDecScale
	}
	if n < low || n > maxDecScale {
		return 0, c.ArgErrorf("%s: places must be %d..%d, got %d", c.Name(), low, maxDecScale, n)
	}
	return int(n), nil
}

// decMode reads a rounding mode. There are two and the message names both, because the
// next guess is the interesting part of a diagnostic (§17).
func decMode(c *Ctx, args []Value, i int) (string, error) {
	if i >= len(args) {
		return decModeHalfUp, nil
	}
	m, err := argStr(c, args[i])
	if err != nil {
		return "", err
	}
	if m != decModeHalfUp && m != decModeHalfEven {
		return "", c.ArgErrorf("%s: unknown rounding mode %s; the modes are %s and %s",
			c.Name(), quoteString(m), quoteString(decModeHalfUp), quoteString(decModeHalfEven))
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Members (§12.15)
// ---------------------------------------------------------------------------

// decvOf is the one conversion into a decimal, and the only row that reads text. An Int
// passes through exactly; a decimal is returned in canonical form, so `of` is also how a
// dict that came back from `json` is checked before it is used.
func decvOf(c *Ctx, args []Value) (Value, error) {
	var (
		n   decNum
		err error
	)
	switch v := args[0]; v.Kind() {
	case KString:
		n, err = decParse(c, v.Str())
	case KInt, KDict, KFloat:
		// The three decArg already has an answer for, including the float it refuses
		// with the text to write instead.
		n, err = decArg(c, v)
	default:
		return Nil(), c.TypeErrorf("%s expects text, an int or a decimal, got %s", c.Name(), v.Kind())
	}
	if err != nil {
		return Nil(), err
	}
	return decValue(c, n)
}

// decvPlus and decvTimes are variadic like `merge` and `zip` (§12.3): a sum of three
// prices is one call, and the pairwise form is the same call with two arguments.
//
// The four are named after the operators they stand in for, and not `add`/`sub`/`mul`/
// `div`, because `sub` is not a name this language has to give: §5.6 spends it on the
// did-you-mean for `replace_first`, and a member spelled `sub` would be a parse error in
// every file that wrote it. One story for four names beats three names and an exception.
func decvPlus(c *Ctx, args []Value) (Value, error) {
	acc, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	for _, v := range args[1:] {
		b, err := decArg(c, v)
		if err != nil {
			return Nil(), err
		}
		ua, ub, scale := decAlign(acc, b)
		acc = decNum{u: ua.Add(ua, ub), scale: scale}
	}
	return decValue(c, acc)
}

func decvMinus(c *Ctx, args []Value) (Value, error) {
	a, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b, err := decArg(c, args[1])
	if err != nil {
		return Nil(), err
	}
	ua, ub, scale := decAlign(a, b)
	return decValue(c, decNum{u: ua.Sub(ua, ub), scale: scale})
}

func decvTimes(c *Ctx, args []Value) (Value, error) {
	acc, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	for _, v := range args[1:] {
		b, err := decArg(c, v)
		if err != nil {
			return Nil(), err
		}
		// A product is exact: the digits multiply and the places add. Canonical form
		// then sheds what was only ever trailing zeros, so 0.5 × 0.2 is 0.1 and not a
		// two-place 0.10 that `==` would have to be taught about.
		acc = decNum{u: new(big.Int).Mul(acc.u, b.u), scale: acc.scale + b.scale}
	}
	return decValue(c, acc)
}

// decvDiv divides, and its third argument is the whole design. With `places` it rounds
// to that many, like every other language. **Without** `places` it insists on an exact
// answer and raises when there is none: 1/3 has no decimal form, and the alternative — a
// default precision nobody asked for — is the silent choice this language does not make
// anywhere else (D16, §5.6). The fix is in the message and is one argument long.
func decvDiv(c *Ctx, args []Value) (Value, error) {
	a, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b, err := decArg(c, args[1])
	if err != nil {
		return Nil(), err
	}
	if b.u.Sign() == 0 {
		// The same error `1 / 0` raises (§8.3), because it is the same mistake.
		return Nil(), zeroDivError()
	}
	mode, err := decMode(c, args, 3)
	if err != nil {
		return Nil(), err
	}
	// a / b = (a.units × 10**b.scale) / (b.units × 10**a.scale) — one fraction of
	// integers, which is what both branches below work on.
	num := new(big.Int).Mul(a.u, decPow10(b.scale))
	den := new(big.Int).Mul(b.u, decPow10(a.scale))
	if len(args) >= 3 {
		places, err := decPlaces(c, args[2], true)
		if err != nil {
			return Nil(), err
		}
		return decValue(c, decDivPlaces(num, den, places, mode))
	}
	n, ok := decDivExact(num, den)
	if !ok {
		return Nil(), c.ErrorfKind(ErrKindDecimal,
			"%s: %s / %s has no exact decimal form within %d places; say how many — decimal.div(a, b, 2)",
			c.Name(), decString(a), decString(b), maxDecScale)
	}
	return decValue(c, n)
}

// decDivPlaces is num/den rounded to `places`, with a negative `places` rounding to tens
// and hundreds the way decRescale does.
func decDivPlaces(num, den *big.Int, places int, mode string) decNum {
	if places >= 0 {
		return decNum{u: decQuoRound(new(big.Int).Mul(num, decPow10(places)), den, mode), scale: places}
	}
	q := decQuoRound(num, new(big.Int).Mul(den, decPow10(-places)), mode)
	return decNum{u: q.Mul(q, decPow10(-places)), scale: 0}
}

// decDivExact answers num/den when the quotient has a decimal form, and false when it
// does not. A fraction in lowest terms terminates exactly when its denominator is made
// of 2s and 5s — that is the whole test — and the number of places it needs is the
// larger of the two counts.
func decDivExact(num, den *big.Int) (decNum, bool) {
	if num.Sign() == 0 {
		return decZero(), true
	}
	n, d := new(big.Int).Set(num), new(big.Int).Set(den)
	if d.Sign() < 0 {
		n.Neg(n)
		d.Neg(d)
	}
	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(n), d)
	n.Quo(n, g)
	d.Quo(d, g)

	rest, twos, fives := new(big.Int).Set(d), 0, 0
	r := new(big.Int)
	for {
		q, _ := new(big.Int).QuoRem(rest, decTwo, r)
		if r.Sign() != 0 {
			break
		}
		rest, twos = q, twos+1
	}
	for {
		q, _ := new(big.Int).QuoRem(rest, decFive, r)
		if r.Sign() != 0 {
			break
		}
		rest, fives = q, fives+1
	}
	if rest.Cmp(decOne) != 0 {
		return decNum{}, false
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	if scale > maxDecScale {
		return decNum{}, false
	}
	n.Mul(n, decPow10(scale))
	return decNum{u: n.Quo(n, d), scale: scale}, true
}

func decvNeg(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return decValue(c, decNum{u: new(big.Int).Neg(n.u), scale: n.scale})
}

func decvAbs(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return decValue(c, decNum{u: new(big.Int).Abs(n.u), scale: n.scale})
}

// decvCmp is the `<=>` a dict does not have (§7.5): -1, 0 or 1, which is exactly what
// `sort`'s comparator wants — `xs.sort { (a, b) -> decimal.cmp(a, b) }` (§12.3).
func decvCmp(c *Ctx, args []Value) (Value, error) {
	a, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b, err := decArg(c, args[1])
	if err != nil {
		return Nil(), err
	}
	ua, ub, _ := decAlign(a, b)
	return Int(int64(ua.Cmp(ub))), nil
}

// decvRound changes the number, which is why it hands back a decimal and why its result
// is canonical: `decimal.round(decimal.of("1.005"), 2)` is 1.01, and 1.01 shed nothing,
// while rounding 1.50 to two places is still 1.5. Asking for two places *in print* is
// `decimal.str(d, 2)` — a different question, and a different row.
func decvRound(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	places, err := decPlaces(c, args[1], true)
	if err != nil {
		return Nil(), err
	}
	mode, err := decMode(c, args, 2)
	if err != nil {
		return Nil(), err
	}
	return decValue(c, decRescale(n, places, mode))
}

// decvStr renders. With no places it is the canonical shortest form; with places it pads
// and rounds to exactly that many, which is the row a price list wants — and the only
// place in the module where trailing zeros survive, because they are a fact about the
// column and not about the number.
func decvStr(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if len(args) == 2 {
		places, err := decPlaces(c, args[1], false)
		if err != nil {
			return Nil(), err
		}
		n = decRescale(n, places, decModeHalfUp)
	}
	return Str(decString(n)), nil
}

// decvFloat is the way out to §12.5's arithmetic, and it is lossy on purpose: a float
// has 53 bits of mantissa and this is the row that says so out loud.
func decvFloat(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	// ParseFloat cannot fail on a canonical decimal — at most 37 characters, magnitude
	// under 2**63 — and this is the brace to that belt: a silently wrong price is the one
	// outcome this module exists to prevent.
	f, ferr := strconv.ParseFloat(decString(n), 64)
	if ferr != nil {
		return Nil(), c.ErrorfKind(ErrKindDecimal, "%s: %s has no float form", c.Name(), decString(n))
	}
	return Float(f), nil
}

// decvInt truncates toward zero, which is what `.int` does to a Float (§12.7). Rounding
// is `decimal.round(d, 0)` and says so.
func decvInt(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	// Truncating only ever shrinks a value that already fit an Int, so the check below
	// cannot fire today. It stands because the alternative to checking is a wrapped
	// int64, and the module's whole promise is that it says so instead.
	whole := new(big.Int).Quo(n.u, decPow10(n.scale))
	if !whole.IsInt64() {
		return Nil(), c.ErrorfKind(ErrKindDecimal, "%s: %s does not fit an int", c.Name(), decString(n))
	}
	return Int(whole.Int64()), nil
}

// decvSum is the aggregate `xs.sum` cannot be: a decimal is a dict, and `sum` adds
// numbers (§12.3). Summing exactly is the reason the module exists, so it is a row and
// not a `reduce` the caller writes out.
func decvSum(c *Ctx, args []Value) (Value, error) {
	xs, err := arrElems(c, args[0])
	if err != nil {
		return Nil(), err
	}
	acc := decZero()
	for _, v := range xs {
		b, err := decArg(c, v)
		if err != nil {
			return Nil(), err
		}
		ua, ub, scale := decAlign(acc, b)
		acc = decNum{u: ua.Add(ua, ub), scale: scale}
	}
	return decValue(c, acc)
}

// decvSplit divides a value into whole units so that the parts add back up to it — the
// operation every invoice needs and every hand-written version gets wrong by one kopeck.
// The places are given rather than taken from the value because a canonical decimal has
// no memory of its zeros: "10.00" *is* 10, and splitting it three ways at zero places
// would be [4, 3, 3] where the caller meant [3.34, 3.33, 3.33].
//
// The remainder goes to the first parts, in order, which makes the answer the same on
// every run (§8.13).
func decvSplit(c *Ctx, args []Value) (Value, error) {
	n, err := decArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	ways, err := argInt(c, args[1])
	if err != nil {
		return Nil(), err
	}
	if ways < 1 {
		return Nil(), c.ArgErrorf("%s: ways must be at least 1, got %d", c.Name(), ways)
	}
	places, err := decPlaces(c, args[2], false)
	if err != nil {
		return Nil(), err
	}
	if places < n.scale {
		return Nil(), c.ErrorfKind(ErrKindDecimal,
			"%s: %s has %d decimal places and the parts were asked for %d; round it first",
			c.Name(), decString(n), n.scale, places)
	}
	if err := c.CheckCollection(int(ways)); err != nil {
		return Nil(), err
	}
	if err := c.Step(ways); err != nil {
		return Nil(), err
	}

	total := n.at(places)
	base, rem := new(big.Int).QuoRem(total, big.NewInt(ways), new(big.Int))
	extra := new(big.Int).Abs(rem).Int64()
	step := decOne
	if total.Sign() < 0 {
		step = big.NewInt(-1)
	}
	out := make([]Value, 0, ways)
	for i := int64(0); i < ways; i++ {
		part := new(big.Int).Set(base)
		if i < extra {
			part.Add(part, step)
		}
		v, err := decValue(c, decNum{u: part, scale: places})
		if err != nil {
			return Nil(), err
		}
		out = append(out, v)
	}
	return arrayOf(out), nil
}
