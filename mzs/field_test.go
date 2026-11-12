package mzs

import "testing"

// RFC 0003, part 1: a record's `.field` on the left of an assignment.

func TestFieldAssignment(t *testing.T) {
	spreadCases(t, [][2]string{
		{`record P(x, y); p = P(1, 2); p.x = 5; p`, `{"x":5,"y":2}`},
		// It is the write `p["x"] = v` makes: in place, and the label survives.
		{`record P(x, y); p = P(1, 2); q = p; q.y = 9; [p, type(p)]`, `[{"x":1,"y":9},"P"]`},
		// Every compound operator, reading the field it writes.
		{`record P(x); p = P(1); p.x += 2; p.x *= 10; p.x -= 1; p.x`, `29`},
		{`record P(s); p = P("a"); p.s += "b"; p.s`, `"ab"`},
		{`record P(x); p = P(nil); p.x ??= 3; p.x ??= 4; p.x`, `3`},
		{`record P(x); p = P(0); p.x ||= 7; p.x &&= 8; p.x`, `8`},
		// The value of the assignment is the right side, as for every other target.
		{`record P(x); p = P(1); [(p.x = 4), p.x]`, `[4,4]`},
		// The receiver is evaluated once, before the right side, as an index's is (§8.8).
		{`record P(x); p = P(1); $n = 0; fn get() { $n += 1; p }; get().x += 1; [p.x, $n]`, `[2,1]`},
		{`record P(x); p = P(1); log = []; fn r() { log.push("r"); p }; fn v() { log.push("v"); 2 }; r().x = v(); log`, `["r","v"]`},
		// A chain: the receiver may be any expression that yields the record.
		{`record P(x); record Box(p); b = Box(P(1)); b.p.x = 3; b`, `{"p":{"x":3}}`},
		{`record P(x); ps = [P(1), P(2)]; ps[1].x = 0; ps`, `[{"x":1},{"x":0}]`},
		{`record P(x); ps = [P(1), P(2)]; ps.each { it.x *= 10 }; ps`, `[{"x":10},{"x":20}]`},
		// Destructuring writes a field as it writes an index.
		{`record P(x, y); p = P(0, 0); p.x, p.y = [3, 4]; p`, `{"x":3,"y":4}`},
		{`record P(x, y); p = P(0, 0); [p.y, p.x] = [1, 2]; p`, `{"x":2,"y":1}`},
		{`record P(x, y); p = P(0, 0); {a: p.x, b: p.y} = {a: 5, b: 6}; p`, `{"x":5,"y":6}`},
		{`record P(x, y); p = P(1, 2); p.x, p.y = [p.y, p.x]; p`, `{"x":2,"y":1}`},
		// A field wins over a function and over a stdlib row of the same name (§7.8): a
		// write has no call reading at all.
		{`record M(amount); fn amount(m) { 0 }; m = M(1); m.amount = 5; [m.amount, amount(m)]`, `[5,0]`},
		{`record Page(len); p = Page(1); p.len = 3; [p.len, len(p)]`, `[3,1]`},
		// Two records sharing a field name: the value decides whose field it is.
		{`record A(x); record B(x, y); xs = [A(1), B(2, 3)]; for v in xs { v.x += 1 }; xs`, `[{"x":2},{"x":3,"y":3}]`},
		// A record in a function, written through its parameter.
		{`record P(x); fn bump(p) { p.x += 1; p }; bump(P(1)).x`, `2`},
		// Inside a block that declares the record.
		{`fn f() { record Q(v); q = Q(1); q.v = 2; q }; f()`, `{"v":2}`},
	})
}

func TestFieldAssignmentErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		// Compile time: the name must be a field of some record this unit declares.
		{`d = {len: 1}; d.len = 3`, ErrKindSyntax, `cannot assign to '.len': not a record field (a dict entry is written d["len"] = …)`},
		{`record M(amount); m = M(1); m.amout = 3`, ErrKindSyntax, `cannot assign to '.amout': not a record field; did you mean '.amount'?`},
		{`record M(amount); m = M(1); m.amout += 3`, ErrKindSyntax, `did you mean '.amount'?`},
		{`record M(amount); m = M(1); m.amout, x = [1, 2]`, ErrKindSyntax, `did you mean '.amount'?`},
		{`record M(amount); m = M(1); {a: m.amout} = {a: 1}`, ErrKindSyntax, `did you mean '.amount'?`},
		// Only the bare spelling is a field: a call stays a call.
		{`record M(a); m = M(1); m.a() = 3`, ErrKindSyntax, "cannot assign to this expression"},
		{`record M(a); m = M(1); m?.a = 3`, ErrKindSyntax, "cannot assign to this expression"},
		{`record M(a); m = M(1); m.a(), b = [1, 2]`, ErrKindSyntax, "a destructuring target is a name, a $var, an index, a record field or a nested [ … ]"},
		// Run time: the receiver must be a record whose declaration has the field.
		{`record P(x); p = 5; p.x = 1`, ErrKindType, "cannot assign to '.x' of int: a field belongs to a record"},
		{`record P(x); d = {x: 1}; d.x = 2`, ErrKindType, `cannot assign to '.x' of a plain dict: a field belongs to a record (an entry is written d["x"] = …)`},
		{`record P(x); record Q(y); q = Q(1); q.x = 2`, ErrKindType, "record Q has no field 'x'; it has y"},
		{`record P(x); record Q(y); q = Q(1); q.x += 2`, ErrKindType, "record Q has no field 'x'; it has y"},
		{`record P(x); record Q(y); q = Q(1); q.x, z = [1, 2]`, ErrKindType, "record Q has no field 'x'; it has y"},
		// `.field =` never adds a key, not even one the declaration names.
		{`record P(x, y); p = P(1, 2); p.delete("x"); p.x = 3`, ErrKindKey, `this P has no 'x' entry any more, and '.x =' never adds one (write m["x"] = …)`},
		{`record P(x, y); p = P(1, 2); p.x = (p.delete("x"); 3)`, ErrKindKey, "this P has no 'x' entry any more"},
		// An error on either side is the assignment's, and nothing is written.
		{`record P(x); fn bad() { raise("no receiver", "custom") }; bad().x = 1`, "custom", "no receiver"},
		{`record P(x); fn bad() { raise("no receiver", "custom") }; bad().x, y = [1, 2]`, "custom", "no receiver"},
		{`record P(x); p = P(1); p.x += raise("no value", "custom")`, "custom", "no value"},
	})
}

// A write that fails writes nothing, and the error points at the field. A destructuring
// writes its targets in order, so a failing one leaves the writes before it, exactly as
// a failing index target does.
func TestFieldAssignmentFailsCleanly(t *testing.T) {
	spreadCases(t, [][2]string{
		{`record P(x); record Q(y); q = Q(1); try (q.x = 2) else nil; q`, `{"y":1}`},
		{`record P(x, y); p = P(1, 2); p.delete("x"); try (p.x = 3) else nil; p`, `{"y":2}`},
		{`record P(x); record Q(y); q = Q(1); p = P(0); try (p.x, q.x = [5, 6]) else nil; p`, `{"x":5}`},
	})
	_, err := spreadEval(t, DefaultOptions(), "record P(x)\nd = {}\nd.x = 1")
	e, ok := err.(*Error)
	if !ok || e.Line != 3 || e.Col != 3 {
		t.Fatalf("error = %#v, want one at 3:3, the field's name", err)
	}
}
