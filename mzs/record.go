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

// recordTypeOf reads back what the compile pass parked, compiling it on the spot for a
// tree that never went through the pass (a hand-built program, or one compiled before
// records existed) rather than failing.
func recordTypeOf(n *ast.RecordDecl) *RecordType {
	if r, ok := n.Type.(*RecordType); ok && r != nil {
		return r
	}
	return newRecordType(n)
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
	rt := recordTypeOf(n)
	e.rs.declareRecord(rt)
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

// declareRecord records a shape under its name for `is` to find. The table lives on the
// half of the Run every task shares (a task runs on a copy of runState) and is created
// here rather than up front, so a program that declares no shape allocates nothing.
func (rs *runState) declareRecord(rt *RecordType) {
	if rs.sh.records == nil {
		rs.sh.records = make(map[string]*RecordType, 4)
	}
	rs.sh.records[rt.Name] = rt
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
