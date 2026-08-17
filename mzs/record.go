package mzs

import "mzs/internal/ast"

// Records (SPEC §7.8). `record Money(amount, currency)` is not a tenth kind of value: it
// is a **name for a shape** over the dict mzs already has, so `json`, `keys`, `dig`,
// `each`, `==` and every other dict row go on meaning exactly what they meant, and the
// only thing that is new is a label the value carries.
//
// The label answers three questions and no others:
//
//	type(m) == "Money"                  what shape is this
//	m.amount                            read a field by name
//	match m { Money -> …; else -> … }   dispatch on the shape
//
// Everything a class would add is deliberately absent (§1.2): no inheritance, no methods
// on the type, no `method_missing`. Functions over a record stay free functions and are
// reached by UFCS like any other operation (D18), which is what keeps one namespace and
// one spelling per thing.
//
// The label is provenance and not content, in the same sense odict's `src` is: equality,
// hashing, JSON, `keys` and iteration all ignore it, so a `Money` and a hand-written dict
// with the same entries are the same value. What propagates it is the copy a record makes
// of itself — `dup` and `merge` — because those answer "the same shape with one field
// changed", which is the `with`-update the shape exists for.

// RecordType is one `record` declaration. It is created by the compile pass and shared by
// every value the constructor builds, so identity is the question a `match Money ->` arm
// asks: two declarations of the same name are two types, and one declaration is one type
// across every Run of the program that holds it.
type RecordType struct {
	Name   string
	Fields []string
}

// Has reports whether name is one of the record's fields. Field lists are short enough
// that a scan beats a map by every measure that matters here.
func (r *RecordType) Has(name string) bool {
	if r == nil {
		return false
	}
	for _, f := range r.Fields {
		if f == name {
			return true
		}
	}
	return false
}

// newRecordType builds the runtime type for a declaration. The compile pass parks it on
// the node (ast.RecordDecl.Type), the way it parks a compiled regex on a RegexLit.
func newRecordType(n *ast.RecordDecl) *RecordType {
	fields := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		fields[i] = f.Name
	}
	return &RecordType{Name: n.Name, Fields: fields}
}

// recordTypeFor is the shape one declaration names. It is the *RecordType the compile
// pass parked on the node — which is every program Compile produced — and otherwise one
// built here and remembered for the rest of the Run.
//
// Remembering it is not optional, and the node is the wrong place to remember it. A
// declaration is evaluated at two points, the hoist and the statement itself (§8.2), and
// two identities for one declaration would make `match m { Money -> … }` stop firing on
// values the hoisted constructor built. Writing the answer back onto the node would fix
// that and break something worse: a *Program is immutable and shared by concurrent Runs
// (§13.3), so the Run is as far as a fallback may reach.
func (rs *runState) recordTypeFor(n *ast.RecordDecl) *RecordType {
	if r, ok := n.Type.(*RecordType); ok && r != nil {
		return r
	}
	if r, ok := rs.sh.fallbackRecords[n]; ok {
		return r
	}
	r := newRecordType(n)
	if rs.sh.fallbackRecords == nil {
		rs.sh.fallbackRecords = map[*ast.RecordDecl]*RecordType{}
	}
	rs.sh.fallbackRecords[n] = r
	return r
}

// build turns a bound frame into the record's dict. The parameters were bound by
// bindParams, so positions, `name = value` arguments and defaults have all already had
// their say and the fields come out in declaration order (D11).
func (r *RecordType) build(env *Env, params []ast.Param) Value {
	d := NewOrderedDictCap(len(params))
	for _, p := range params {
		v, _ := env.Lookup(p.Name)
		_ = d.Set(Str(p.Name), v)
	}
	d.rec = r
	return dictOf(d)
}

// recordType is the shape a dict was built with, or nil for an ordinary dict and for
// every other kind.
func (v Value) recordType() *RecordType {
	if v.k != KDict {
		return nil
	}
	if d := v.odict(); d != nil {
		return d.rec
	}
	return nil
}

// recordCtor is the shape a function constructs, or nil for every other function. It is
// what makes a bare `Money` a pattern rather than a value to compare against (§5.3).
func (v Value) recordCtor() *RecordType {
	if f := v.fn(); f != nil {
		return f.Record
	}
	return nil
}

// makeRecord evaluates a declaration: it binds the name to a constructor closed over the
// scope the declaration stands in, so a field default reads the same names the rest of
// that scope does.
func (e ev) makeRecord(n *ast.RecordDecl) Value {
	rt := e.rs.recordTypeFor(n)
	e.rs.declareRecord(rt.Name)
	arity := len(n.Fields)
	for _, f := range n.Fields {
		if f.Default != nil {
			arity = -1
			break
		}
	}
	return funcOf(&Func{
		Name:   n.Name,
		Params: n.Fields,
		Env:    e.env,
		Arity:  arity,
		Frame:  len(n.Fields),
		Record: rt,
	})
}

// declareRecord notes that this Run has a shape of that name, which is all `is` needs:
// it answers by name, exactly as `type` does (§7.8). Keeping the *type* here instead
// would be wrong as well as unnecessary — a second declaration of one name would replace
// the entry, and `x.is("A")` would start disagreeing with `type(x) == "A"` for values the
// first one built.
//
// The table lives on the half of the Run every task shares (a task runs on a copy of
// runState) and is created here rather than up front, so a program that declares no shape
// allocates nothing.
func (rs *runState) declareRecord(name string) {
	if rs.sh.records == nil {
		rs.sh.records = make(map[string]bool, 4)
	}
	rs.sh.records[name] = true
}

// evalRecordDecl runs the declaration statement. Its value is the constructor, exactly as
// a `fn` declaration's is.
func (e ev) evalRecordDecl(n *ast.RecordDecl) (Value, error) {
	v := e.makeRecord(n)
	if n.Name != "" {
		e.env.Set(n.Name, v)
	}
	return v, nil
}

// recordAddError is the one pair of dicts `+` refuses (§8.3). `+` on dicts is `merge`,
// and merge keeps the right-hand value of every key both sides carry — which between two
// dicts of one *shape* is every key, so `a + b` is `b`, silently and always. On a
// hand-written dict that is a legible answer to a legible question ("these two dicts,
// combined"); on two values of one shape it is never what the line meant, and the shapes
// are exactly the values a program adds: `price + vat`, `Money(…) + Money(…)`.
//
// So the rule is narrow and reads off the label alone: **both** sides labelled is an
// error, one side labelled is still the with-update the label exists for
// (`m + {amount: 2}`), and everything else is the merge it always was. A shape that has
// an addition of its own says so in the message, because that is the line the author was
// reaching for (§17).
func recordAddError(a, b Value) error {
	ra, rb := a.recordType(), b.recordType()
	if ra == nil || rb == nil {
		return nil
	}
	hint := "overwrite fields with 'merge'"
	if ra == decShape && rb == decShape {
		hint = "add two decimals with 'decimal.plus' (§12.15), and overwrite fields with 'merge'"
	}
	return typeErrorf("cannot add %s to %s: '+' merges dicts, so this is the right-hand value and not a sum; %s",
		rb.Name, ra.Name, hint)
}
