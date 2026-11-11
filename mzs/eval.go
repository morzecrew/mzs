package mzs

import (
	"slices"

	"mzs/internal/ast"
	"mzs/internal/rx"
	"mzs/internal/token"
)

// ev is the evaluator: the per-Run state, the single reusable Ctx, and the current
// lexical scope. It is a value type of three pointers so entering a scope costs a copy
// and no allocation, and so call.go can rebuild it from a *Ctx at the evalCall seam
// without the stdlib ever seeing it.
type ev struct {
	rs  *runState
	cx  *Ctx
	env *Env
}

// in returns the evaluator with its scope replaced. Every `{ … }` is a closure and
// therefore a scope (D2, §8.2), so this is used for a call frame and for every body
// block alike — an `if`, `while`, `for` or `match` arm body included.
func (e ev) in(env *Env) ev { e.env = env; return e }

// at stamps a position and the current call stack onto a script error on its way out.
// Control signals travel through untouched: they are not failures.
func (e ev) at(err error, p token.Pos) error {
	if err == nil {
		return nil
	}
	if _, ok := ctrlOf(err); ok {
		return err
	}
	if err == errNilChain {
		return err
	}
	x := asError(err)
	x.At(e.rs.file, p.Line, p.Col)
	if x.Stack == nil {
		x.Stack = e.rs.frames()
	}
	return x
}

// atCall is at for the failure of a call inside its own frame — the arity, a missing
// argument, a host function's error — whose frame is innermost and is the failure itself.
// A stack the error already carries came from deeper down and is kept as it is.
func (e ev) atCall(err error, p token.Pos) error {
	if x, ok := err.(*Error); ok && x.Stack == nil {
		x.Stack = framesOutside(e.rs.frames())
	}
	return e.at(err, p)
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

// runProgram is one whole script. Top-level `fn`s are hoisted so a call may appear
// above its declaration (§8.2), and a `return` at top level ends the program with that
// value (§8.1).
func (e ev) runProgram(f *ast.Program) (Value, error) {
	if f == nil {
		return Nil(), nil
	}
	e.hoist(f.Stmts)
	v, err := e.execStmts(f.Stmts)
	if c, ok := ctrlOf(err); ok {
		// break/next with nothing to leave are not worth an error at the top level;
		// `return v` ends the program with v.
		return c.val, nil
	}
	return v, err
}

// hoist binds the `fn` and `record` declarations of the top-level statement list before
// it runs, so a call may appear above the declaration it reaches (§8.2, §7.8).
func (e ev) hoist(stmts []ast.Stmt) {
	for _, s := range stmts {
		switch d := s.(type) {
		case *ast.FnDecl:
			if d.Name != "" {
				e.env.Set(d.Name, e.makeFunc(d.Name, d.Params, d.Body, false, d.Async))
			}
		case *ast.RecordDecl:
			if d.Name != "" {
				e.env.Set(d.Name, e.makeRecord(d))
			}
		}
	}
}

// execStmts runs a statement list in the current scope and yields the value of the
// last one (D4).
func (e ev) execStmts(stmts []ast.Stmt) (Value, error) {
	// The scope is a root for as long as its statements run (§14.2): its parents are
	// reached through it, and a caller's scope is rooted by the caller's own execStmts.
	m := e.rs.rootMark()
	e.rs.pushEnv(e.env)
	last := Nil()
	for _, s := range stmts {
		v, err := e.execStmt(s)
		if err != nil {
			e.rs.rootReset(m)
			// An error nothing positioned yet — a limit raised deep inside the statement,
			// a cap checked before an allocation — is positioned at the statement that
			// failed rather than at the start of the program (§8.11). at keeps a position
			// and a stack that are already set.
			return Nil(), e.at(err, s.Pos())
		}
		last = v
	}
	e.rs.rootReset(m)
	return last, nil
}

// execBlock runs a body block in a scope of its own. A variable first created inside
// an `if`, `while`, `for` or `match` body is therefore invisible after it (§8.2); a
// plain `=` to a name that already exists further out still writes the outer binding.
func (e ev) execBlock(b *ast.BlockStmt) (Value, error) {
	if b == nil {
		return Nil(), nil
	}
	return e.in(newEnv(e.env, b.FrameSize)).execStmts(b.Stmts)
}

func (e ev) execStmt(s ast.Stmt) (Value, error) {
	switch n := s.(type) {
	case *ast.ExprStmt:
		return e.eval(n.X)
	case *ast.IncludeDecl:
		return e.evalInclude(n)
	case *ast.ExportDecl:
		return e.evalExport(n)
	case *ast.RecordDecl:
		return e.evalRecordDecl(n)
	case *ast.ReturnStmt:
		v, err := e.evalOpt(n.X)
		if err != nil {
			return Nil(), err
		}
		return Nil(), returnSignal(v)
	case *ast.BreakStmt:
		v, err := e.evalOpt(n.X)
		if err != nil {
			return Nil(), err
		}
		return Nil(), breakSignal(v)
	case *ast.NextStmt:
		v, err := e.evalOpt(n.X)
		if err != nil {
			return Nil(), err
		}
		return Nil(), nextSignal(v)
	case *ast.BlockStmt:
		return e.execBlock(n)
	case ast.Expr:
		return e.eval(n)
	}
	return Nil(), newError(ErrKindInternal, "mzs: unknown statement %T", s)
}

func (e ev) evalOpt(x ast.Expr) (Value, error) {
	if x == nil {
		return Nil(), nil
	}
	return e.eval(x)
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

// eval is one expression, and the boundary a `?.` short-circuit stops at: a chain that
// began with a nil-guarded receiver is worth nil here, and nothing beyond this node is
// affected (§8.7).
func (e ev) eval(x ast.Expr) (Value, error) {
	v, err := e.evalChain(x)
	if err == errNilChain {
		return Nil(), nil
	}
	return v, err
}

// evalChain is the node loop. Every visit charges one step, which is what makes a
// runaway script interruptible mid-expression as well as mid-loop (§14.1). It differs
// from eval in one way: a `?.` on a nil receiver leaves through it as errNilChain, so
// the trailers stacked on top of that receiver are skipped rather than run on nil.
func (e ev) evalChain(x ast.Expr) (Value, error) {
	if err := e.rs.step(1); err != nil {
		return Nil(), err
	}
	switch n := x.(type) {
	case *ast.NilLit:
		return Nil(), nil
	case *ast.BoolLit:
		return Bool(n.Value), nil
	case *ast.IntLit:
		return Int(n.Value), nil
	case *ast.FloatLit:
		return Float(n.Value), nil
	case *ast.StrLit:
		return e.evalStr(n)
	case *ast.RegexLit:
		return e.evalRegex(n)
	case *ast.ArrayLit:
		return e.evalArray(n)
	case *ast.DictLit:
		return e.evalDict(n)
	case *ast.Splat:
		// Every owner of a Splat consumes it; one evaluated on its own is a node the
		// compile pass should have refused.
		return Nil(), e.at(syntaxErrorf("a spread stands only in a call, an array literal or a pattern"), n.Star)
	case *ast.Ident:
		v, err := e.evalIdent(n)
		return v, e.at(err, n.Start)
	case *ast.GlobalVar:
		return e.evalGlobal(n), nil
	case *ast.UnaryExpr:
		v, err := e.eval(n.X)
		if err != nil {
			return Nil(), err
		}
		r, err := e.unary(n.Op, v)
		return r, e.at(err, n.OpPos)
	case *ast.BinaryExpr:
		return e.evalBinary(n)
	case *ast.LogicalExpr:
		return e.evalLogical(n)
	case *ast.TernaryExpr:
		c, err := e.eval(n.Cond)
		if err != nil {
			return Nil(), err
		}
		if c.Truthy() {
			return e.eval(n.Then)
		}
		return e.eval(n.Else)
	case *ast.RangeExpr:
		return e.evalRange(n)
	case *ast.AssignExpr:
		return e.evalAssign(n)
	case *ast.DestructureAssign:
		return e.evalDestructure(n)
	case *ast.IndexExpr:
		return e.evalIndex(n)
	case *ast.CallExpr:
		return e.evalCallExpr(n)
	case *ast.MethodCall:
		return e.evalMethodCall(n)
	case *ast.FuncLit:
		return e.makeFunc("", n.Params, n.Body, true, false), nil
	case *ast.FnDecl:
		f := e.makeFunc(n.Name, n.Params, n.Body, false, n.Async)
		if n.Name != "" {
			e.env.Set(n.Name, f)
		}
		return f, nil
	case *ast.GroupExpr:
		// `( a; b )` groups statements without opening a scope: only a brace does that.
		return e.execStmts(n.Stmts)
	case *ast.BlockStmt:
		// A braced `try` clause (§8.11). Its value is its last statement (§8.1) and its
		// braces are a scope, which is the whole of what the braced form adds.
		return e.execBlock(n)
	case *ast.IfExpr:
		return e.evalIf(n)
	case *ast.WhileExpr:
		return e.evalWhile(n)
	case *ast.ForExpr:
		return e.evalFor(n)
	case *ast.MatchExpr:
		return e.evalMatch(n)
	case *ast.TryExpr:
		return e.evalTry(n)
	}
	return Nil(), newError(ErrKindInternal, "mzs: unknown expression %T", x)
}

// evalStr builds an interpolated string (§8.12). Interpolation converts with `str`,
// and it is the only implicit conversion in the language. A literal with a single text
// part is the common case and costs nothing beyond the switch.
func (e ev) evalStr(n *ast.StrLit) (Value, error) {
	if len(n.Parts) == 0 {
		return Str(""), nil
	}
	if len(n.Parts) == 1 && n.Parts[0].IsText() {
		return Str(n.Parts[0].Text), nil
	}
	var sb []byte
	for _, p := range n.Parts {
		if p.IsText() {
			sb = append(sb, p.Text...)
			continue
		}
		v, err := e.eval(p.Expr)
		if err != nil {
			return Nil(), err
		}
		sb = append(sb, v.Str()...)
		if err := e.rs.checkString(len(sb)); err != nil {
			return Nil(), err
		}
	}
	if err := e.rs.charge(int64(len(sb))); err != nil {
		return Nil(), err
	}
	return Str(string(sb)), nil
}

// evalRegex hands back the *rx.Regexp the compile pass parked on the node. A literal
// that reached here uncompiled (a hand-built tree, or a program compiled before the
// pass existed) compiles through the shared LRU instead of failing.
func (e ev) evalRegex(n *ast.RegexLit) (Value, error) {
	if r, ok := n.Compiled.(*rx.Regexp); ok && r != nil {
		return regexOf(r), nil
	}
	r, err := e.rs.in.compileRegex(n.Pattern, n.Flags)
	if err != nil {
		return Nil(), e.at(err, n.Start)
	}
	return regexOf(r), nil
}

func (e ev) evalArray(n *ast.ArrayLit) (Value, error) {
	if hasSplat(n.Elems) {
		return e.evalSpreadArray(n)
	}
	if err := e.rs.checkCollection(len(n.Elems)); err != nil {
		return Nil(), err
	}
	out := make([]Value, len(n.Elems))
	m := e.rs.rootMark()
	e.rs.pushVals(out)
	defer e.rs.rootReset(m)
	for i, el := range n.Elems {
		v, err := e.eval(el)
		if err != nil {
			return Nil(), err
		}
		out[i] = v
	}
	return arrayOf(out), nil
}

// hasSplat reports whether a list of elements or arguments spreads anything, so the
// common literal and the common call pay nothing for RFC 0001.
func hasSplat(xs []ast.Expr) bool {
	for _, x := range xs {
		if _, ok := x.(*ast.Splat); ok {
			return true
		}
	}
	return false
}

// evalSpreadArray is an array literal with `*` elements (RFC 0001). The result is laid
// out as each position is reached, left to right, so a spread is a snapshot of its operand
// at that moment — `[*a, (a[0] = 9)]` keeps a's old first element — and the slice being
// built is a root the whole time, followed through every reallocation.
func (e ev) evalSpreadArray(n *ast.ArrayLit) (Value, error) {
	out := make([]Value, 0, len(n.Elems))
	m := e.rs.rootMark()
	e.rs.holdGrowing(&out)
	defer e.rs.rootReset(m)
	for _, el := range n.Elems {
		s, isSplat := el.(*ast.Splat)
		if !isSplat {
			v, err := e.eval(el)
			if err != nil {
				return Nil(), err
			}
			// v is in no root until it lands in out, and the charge may walk.
			pending := e.rs.rootMark()
			e.rs.hold(v)
			if err := e.rs.allocCollection(len(out)+1, 1); err != nil {
				return Nil(), err
			}
			out = append(out, v)
			e.rs.rootReset(pending)
			continue
		}
		v, err := e.eval(s.X)
		if err != nil {
			return Nil(), err
		}
		if out, err = e.appendSplat(out, v, s.Star); err != nil {
			return Nil(), err
		}
	}
	return arrayOf(out), nil
}

// appendSplat appends the elements `*v` stands for to out (RFC 0001): an Array's, or a
// Range's, materialised only after the size the result will have has been checked and
// charged. A seq is refused rather than drained: it may be endless, and the limit that
// would stop it cannot be caught, so the one spelling that says how many is the one
// asked for.
func (e ev) appendSplat(out []Value, v Value, pos token.Pos) ([]Value, error) {
	switch v.Kind() {
	case KArray, KRange:
	case KSeq:
		return out, e.at(typeErrorf("cannot splat seq: it may be endless; write *s.take(n).array, or *s.array for one you know ends"), pos)
	default:
		return out, e.at(typeErrorf("cannot splat %s; '*' takes an array or a range", v.Kind()), pos)
	}
	n := v.Len()
	// The operand may be a value nothing else holds — the result of a call — and the
	// charge may walk, so it stays a root until its elements are in out.
	m := e.rs.rootMark()
	e.rs.hold(v)
	defer e.rs.rootReset(m)
	if err := e.rs.allocCollection(len(out)+n, n); err != nil {
		return out, err
	}
	if v.Kind() == KRange {
		// Laid out straight into the result: materialising the range first would hold a
		// second slice of the same size that nothing charged.
		r := v.rng()
		out = slices.Grow(out, n)
		for i := 0; i < n; i++ {
			out = append(out, Int(r.At(i)))
		}
		return out, nil
	}
	return append(out, v.Elems()...), nil
}

// evalDict builds a dict in source order, which is the order it iterates and
// serialises in (D11).
//
// A `**d` entry (RFC 0001) writes d's entries where it stands, in d's order, and a later
// entry — written or spread — overwrites an earlier one's value in the earlier one's
// place, exactly as assigning to an existing key does. The result is always a plain
// dict: a literal carries no record label, whatever the dicts it was built from carried.
func (e ev) evalDict(n *ast.DictLit) (Value, error) {
	d := NewOrderedDictCap(len(n.Keys))
	m := e.rs.rootMark()
	e.rs.hold(dictOf(d))
	defer e.rs.rootReset(m)
	spread := false
	for i := range n.Keys {
		if s, ok := n.Keys[i].(*ast.Splat); ok {
			if err := e.spreadDict(d, s); err != nil {
				return Nil(), err
			}
			spread = true
			continue
		}
		k, err := e.eval(n.Keys[i])
		if err != nil {
			return Nil(), err
		}
		v, err := e.eval(n.Vals[i])
		if err != nil {
			return Nil(), err
		}
		// Once a spread has made the dict as large as its operand, a written key that
		// is new is one more entry, and the limit is on the dict the literal produces.
		// Without a spread a literal is no larger than its source, as it always was.
		if spread && !d.Has(k) {
			if err := e.rs.allocCollection(d.Len()+1, 1); err != nil {
				return Nil(), err
			}
		}
		if err := d.Set(k, v); err != nil {
			return Nil(), e.at(err, n.Lbrack)
		}
	}
	return dictOf(d), nil
}

// spreadDict writes the entries of `**x` into d.
func (e ev) spreadDict(d *OrderedDict, s *ast.Splat) error {
	v, err := e.eval(s.X)
	if err != nil {
		return err
	}
	src := v.odict()
	if v.Kind() != KDict || src == nil {
		return e.at(typeErrorf("cannot spread %s into a dict; '**' takes a dict", v.Kind()), s.Star)
	}
	if err := e.rs.step(int64(src.Len())); err != nil {
		return err
	}
	// Only the keys d does not have yet make it larger: a spread that overlaps what is
	// already there overwrites in place (D11) and costs no room.
	added := 0
	src.Each(func(k, _ Value) bool {
		if !d.Has(k) {
			added++
		}
		return true
	})
	if err := e.rs.allocCollection(d.Len()+added, added); err != nil {
		return err
	}
	src.Each(func(k, val Value) bool {
		_ = d.Set(k, val) // a key already in a dict is hashable
		return true
	})
	return nil
}

// evalRange builds the lazy range of §8.6. String endpoints are not supported and a
// descending range is empty rather than an error.
func (e ev) evalRange(n *ast.RangeExpr) (Value, error) {
	lo, err := e.evalOpt(n.Lo)
	if err != nil {
		return Nil(), err
	}
	hi, err := e.evalOpt(n.Hi)
	if err != nil {
		return Nil(), err
	}
	if !lo.IsNum() || !hi.IsNum() {
		return Nil(), e.at(typeErrorf("a range needs integer endpoints, got %s and %s",
			lo.Kind(), hi.Kind()), n.OpPos)
	}
	return rangeOf(lo.Int(), hi.Int(), n.Exclusive), nil
}

func (e ev) evalBinary(n *ast.BinaryExpr) (Value, error) {
	l, err := e.eval(n.L)
	if err != nil {
		return Nil(), err
	}
	m := e.rs.rootMark()
	e.rs.hold(l)
	r, err := e.eval(n.R)
	e.rs.rootReset(m)
	if err != nil {
		return Nil(), err
	}
	if n.Op == token.KW_IN {
		return e.evalIn(l, r, n.OpPos)
	}
	v, err := e.binary(n.Op, l, r)
	return v, e.at(err, n.OpPos)
}

// evalIn is `a in xs` (§8.5): membership, asked of the right operand. It is the same
// question an `in` arm of a `match` asks and it is answered the same way — by dispatching
// `has` — so an array, a range, a dict's keys and a string's substrings all mean by `in`
// exactly what they already meant by `has`, and a kind that grows a `has` row grows `in`
// with it (I6). The result is a Bool, because `has` returns one.
func (e ev) evalIn(x, container Value, pos token.Pos) (Value, error) {
	v, err := e.dispatch(container, "has", []Value{x}, nil, pos)
	if err != nil {
		// A kind that answers membership nowhere fails inside dispatch as
		// `undefined method 'has' for int`, naming an operation the source never wrote.
		// The operator owns its own diagnostic (§5.6); anything else — a raise from a
		// user's own `has`, a budget — travels out untouched.
		if x, ok := err.(*Error); ok && x.Msg == undefinedMethodError(container.Kind(), "has").Msg {
			err = typeErrorf("the right side of 'in' must have members — an array, a range, a dict or a string — got %s",
				container.Kind())
		}
		return Nil(), e.at(err, pos)
	}
	return Bool(v.Truthy()), nil
}

// evalLogical short-circuits and returns an operand, not a Bool (§8.5). `??` fires on
// nil only, so `false ?? x` is false while `false || x` is x.
func (e ev) evalLogical(n *ast.LogicalExpr) (Value, error) {
	l, err := e.eval(n.L)
	if err != nil {
		return Nil(), err
	}
	switch n.Op {
	case token.OROR:
		if l.Truthy() {
			return l, nil
		}
	case token.NILNIL:
		if !l.IsNil() {
			return l, nil
		}
	default:
		if !l.Truthy() {
			return l, nil
		}
	}
	return e.eval(n.R)
}

func (e ev) evalIf(n *ast.IfExpr) (Value, error) {
	c, err := e.eval(n.Cond)
	if err != nil {
		return Nil(), err
	}
	if c.Truthy() {
		return e.execBlock(n.Then)
	}
	if n.Else == nil {
		return Nil(), nil
	}
	return e.execStmt(n.Else)
}

// evalWhile charges a step per iteration so an empty body — `while true { }` — still
// hits the limit check, and is interrupted mid-loop rather than between statements
// (§14.1, A5).
func (e ev) evalWhile(n *ast.WhileExpr) (Value, error) {
	for {
		if err := e.rs.step(1); err != nil {
			return Nil(), err
		}
		c, err := e.eval(n.Cond)
		if err != nil {
			return Nil(), err
		}
		if !c.Truthy() {
			return Nil(), nil
		}
		if _, err := e.execBlock(n.Body); err != nil {
			c, ok := ctrlOf(err)
			if !ok {
				return Nil(), err
			}
			switch c.kind {
			case ctrlNext:
				continue
			case ctrlBreak:
				return c.val, nil
			}
			return Nil(), err
		}
	}
}

// evalFor iterates an Array, Range, Dict, String or Seq. Each iteration gets a fresh
// scope holding the loop variables, so the body cannot leak a binding and a closure made
// inside it captures that iteration's values (§8.2).
func (e ev) evalFor(n *ast.ForExpr) (Value, error) {
	it, err := e.eval(n.Iter)
	if err != nil {
		return Nil(), err
	}
	// The iterable and the list of items are roots for the whole loop (§14.2): the loop
	// may be the only thing that still holds them.
	m := e.rs.rootMark()
	defer e.rs.rootReset(m)
	e.rs.hold(it)
	var items []Value
	switch it.Kind() {
	case KArray:
		items = it.Elems()
	case KRange:
		if err := e.rs.allocCollection(it.Len(), it.Len()); err != nil {
			return Nil(), err
		}
		items = it.Elems()
	case KDict:
		items = it.odict().Pairs()
	case KString:
		for _, r := range it.Str() {
			items = append(items, Str(string(r)))
		}
	case KSeq:
		// A seq is pulled, not listed: building `items` first would materialise exactly
		// what the kind exists to avoid, and an endless source has no `items` at all.
		// `for line in io.lines { … }` is the shape this is for (§12.14).
		return e.forSeq(n, it)
	default:
		return Nil(), e.at(typeErrorf("cannot iterate over %s", it.Kind()), n.Kw)
	}
	e.rs.pushVals(items)
	size := n.Body.FrameSize
	if size < 2 {
		size = 2
	}
	for _, item := range items {
		if err := e.rs.step(1); err != nil {
			return Nil(), err
		}
		val, stop, err := e.forItem(n, size, item)
		if err != nil {
			return Nil(), err
		}
		if stop {
			return val, nil
		}
	}
	return it, nil
}

// forItem runs the body once for one item. stop reports that the loop is over, and val
// is then what the `break` carried; a `next` ends the iteration and nothing more.
func (e ev) forItem(n *ast.ForExpr, size int, item Value) (Value, bool, error) {
	k, v := item, Nil()
	if n.ValVar != "" || n.ValPat != nil {
		// Two loop variables destructure the item, under the same rule `k, v = item`
		// follows (§8.15): a dict yields pairs, and anything that is not a pair is
		// an error rather than a silent nil.
		pair, err := e.forPair(item)
		if err != nil {
			return Nil(), true, e.at(err, n.Kw)
		}
		k, v = pair[0], pair[1]
	}
	env := newEnv(e.env, size)
	if n.KeyPat != nil || n.ValPat != nil {
		// A loop pattern's rest is new, and a second pattern may allocate while the
		// first one's names are bound but the body has not rooted the scope yet.
		m := e.rs.rootMark()
		defer e.rs.rootReset(m)
		e.rs.pushEnv(env)
	}
	if err := e.in(env).loopVar(n.KeyVar, n.KeyPat, k); err != nil {
		return Nil(), true, err
	}
	if n.ValVar != "" || n.ValPat != nil {
		if err := e.in(env).loopVar(n.ValVar, n.ValPat, v); err != nil {
			return Nil(), true, err
		}
	}
	if _, err := e.in(env).execStmts(n.Body.Stmts); err != nil {
		c, ok := ctrlOf(err)
		if !ok {
			return Nil(), true, err
		}
		switch c.kind {
		case ctrlNext:
			return Nil(), false, nil
		case ctrlBreak:
			return c.val, true, nil
		}
		return Nil(), true, err
	}
	return Nil(), false, nil
}

// loopVar binds one loop variable for one item: a name, or a pattern that takes the
// item apart under the rules `:=` follows (RFC 0002). An item of the wrong shape is an
// error at the pattern, exactly as it would be at `[id, row] := item`.
func (e ev) loopVar(name string, pat ast.Expr, v Value) error {
	if pat == nil {
		e.env.Define(name, v)
		return nil
	}
	m := e.rs.rootMark()
	defer e.rs.rootReset(m)
	e.rs.hold(v)
	return e.destructureAny(pat, v, token.DECLARE)
}

// forSeq is the same loop driven by a pull instead of a slice (§12.14). The value of the
// loop is the seq itself, as it is the collection for every other kind — one that has
// been walked once, which is all a `for` ever leaves behind.
func (e ev) forSeq(n *ast.ForExpr, sv Value) (Value, error) {
	size := n.Body.FrameSize
	if size < 2 {
		size = 2
	}
	// The pull calls the closures of the lazy rows the chain was built from, and those
	// calls take their position from the Ctx. Point it at the `for` for the duration, or
	// a frame raised inside a `map` a hundred lines above would carry that line instead
	// of this loop's.
	restore := e.cx.setCall("for", n.Kw, nil, Nil())
	defer restore()

	out := sv
	err := seqRun(e.cx, sv.seq(), func(item Value) (bool, error) {
		if err := e.rs.step(1); err != nil {
			return false, err
		}
		val, stop, err := e.forItem(n, size, item)
		if err != nil {
			return false, err
		}
		if stop {
			out = val
		}
		return !stop, nil
	})
	if err != nil {
		return Nil(), err
	}
	return out, nil
}

// forPair splits one item of a two-variable `for`. It is destructuring with the pattern
// written in the loop header, so `for k, v in xs` and `k, v = item` agree on what a pair
// is (§8.15).
func (e ev) forPair(item Value) ([]Value, error) {
	elems, ok, err := e.spread(item)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, typeErrorf("cannot destructure %s: a two-variable 'for' takes an array of two per item", item.Kind())
	}
	if len(elems) != 2 {
		return nil, indexErrorf("a two-variable 'for' expects 2 values per item, got %d", len(elems))
	}
	return elems, nil
}

// ---------------------------------------------------------------------------
// match (§5.2–§5.5)
// ---------------------------------------------------------------------------

// evalMatch tests the arms top to bottom and takes the first that fires; with no arm
// firing and no `else` the value is nil. The subject is evaluated exactly once, before
// any arm runs.
func (e ev) evalMatch(n *ast.MatchExpr) (Value, error) {
	subject := Nil()
	if n.Subject != nil {
		v, err := e.eval(n.Subject)
		if err != nil {
			return Nil(), err
		}
		subject = v
	}
	m := e.rs.rootMark()
	defer e.rs.rootReset(m)
	e.rs.hold(subject)
	for i := range n.Arms {
		arm := &n.Arms[i]
		am := e.rs.rootMark()
		env, ok, err := e.armFires(arm, subject, n.Subject != nil)
		if err != nil {
			return Nil(), e.at(err, arm.Pos)
		}
		if !ok {
			e.rs.rootReset(am)
			continue
		}
		// An array pattern binds into the scope its body runs in, so the guard sees the
		// names too. Every other arm gets that scope from execBlock as it always did.
		body := e
		if env != nil {
			body = e.in(env)
		}
		if arm.Guard != nil {
			g, err := body.eval(arm.Guard)
			if err != nil {
				return Nil(), err
			}
			if !g.Truthy() {
				e.rs.rootReset(am)
				continue
			}
		}
		if env != nil && arm.Body != nil {
			return body.execStmts(arm.Body.Stmts)
		}
		return e.execBlock(arm.Body)
	}
	return Nil(), nil
}

// armFires evaluates one arm's patterns, left to right, stopping at the first that
// fires — several patterns in one arm mean "or" (§5.3). With the subject omitted every
// pattern is a condition instead (§5.4). The Env is non-nil only for an array pattern,
// which is the one arm form that binds: it holds the names the pattern took apart, and
// the caller runs the guard and the body inside it.
func (e ev) armFires(arm *ast.MatchArm, subject Value, hasSubject bool) (*Env, bool, error) {
	switch arm.Kind {
	case ast.ArmElse:
		return nil, true, nil
	case ast.ArmArray, ast.ArmDict:
		if len(arm.Pats) == 0 {
			return nil, false, nil
		}
		size := 0
		if arm.Body != nil {
			size = arm.Body.FrameSize
		}
		env := newEnv(e.env, size)
		// What the pattern binds is new — a rest is a fresh collection — and the guard
		// runs before the body roots the scope, so the scope is a root from the first
		// binding on (§14.2). evalMatch releases it with the arm.
		e.rs.pushEnv(env)
		fired, err := e.matchAny(arm.Pats[0], subject, env)
		if err != nil || !fired {
			return nil, false, err
		}
		return env, true, nil
	}
	for _, pat := range arm.Pats {
		p, err := e.eval(pat)
		if err != nil {
			return nil, false, err
		}
		switch {
		case arm.Kind == ast.ArmIn:
			// `in expr` is membership: an array, a range, a dict's keys, or a
			// substring of a string — all of which spell it `has`.
			v, err := e.dispatch(p, "has", []Value{subject}, nil, arm.Pos)
			if err != nil {
				return nil, false, err
			}
			if v.Truthy() {
				return nil, true, nil
			}
		case arm.Kind == ast.ArmGuard || !hasSubject:
			if p.Truthy() {
				return nil, true, nil
			}
		default:
			ok, err := e.patternFires(p, subject)
			if err != nil {
				return nil, false, err
			}
			if ok {
				return nil, true, nil
			}
		}
	}
	return nil, false, nil
}

// patternFires is the value rule of §5.3 for one position: a regex matches, a record
// constructor asks the shape, anything else compares equal.
func (e ev) patternFires(pat, v Value) (bool, error) {
	if pat.Kind() == KRegex {
		return e.matches(pat, v)
	}
	if rt := pat.recordCtor(); rt != nil {
		// `match m { Money -> … }` (§7.8). Equality against the constructor is the one
		// reading this shape could otherwise have, and it could never fire — a dict is
		// not a function — so the name of a record in a pattern asks what it looks like
		// it asks.
		return v.recordType() == rt, nil
	}
	return v.Equal(pat), nil
}

// matchAny tests v against an array or a dict pattern, binding into env.
func (e ev) matchAny(pat ast.Expr, v Value, env *Env) (bool, error) {
	switch t := pat.(type) {
	case *ast.ArrayPattern:
		return e.matchPattern(t, v, env)
	case *ast.DictPattern:
		return e.matchDict(t, v, env)
	}
	return false, nil
}

// matchDict tests v against a dict pattern (RFC 0002). It fires when v is a dict, every
// key the pattern names is there, and every value position fires by the rule array
// patterns use: a name binds, a nested pattern recurses, `in` asks membership and any
// other expression compares as an arm pattern does (§5.3). A missing key is no match, not
// an error: an arm is a question.
func (e ev) matchDict(pat *ast.DictPattern, v Value, env *Env) (bool, error) {
	if v.Kind() != KDict {
		return false, nil
	}
	d := v.odict()
	for _, en := range pat.Entries {
		val, ok := d.Get(Str(en.Key))
		if !ok {
			return false, nil
		}
		if en.In {
			// `{age: in 18..65}` is exactly `subject["age"] in 18..65` (§8.4).
			p, err := e.in(env).eval(en.Value)
			if err != nil {
				return false, err
			}
			has, err := e.evalIn(val, p, en.KeyPos)
			if err != nil || !has.Truthy() {
				return false, err
			}
			continue
		}
		switch t := en.Value.(type) {
		case *ast.Ident:
			env.Define(t.Name, val)
		case *ast.ArrayPattern, *ast.DictPattern:
			ok, err := e.matchAny(t, val, env)
			if err != nil || !ok {
				return false, err
			}
		default:
			p, err := e.in(env).eval(t)
			if err != nil {
				return false, err
			}
			ok, err := e.patternFires(p, val)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	if pat.Rest != nil {
		rest, err := e.dictRest(pat, d)
		if err != nil {
			return false, err
		}
		env.Define(pat.Rest.Name, rest)
	}
	return true, nil
}

// matchPattern tests v against an array pattern and binds into env what the pattern
// takes apart (§8.15). A value of the wrong kind or the wrong length is not an error
// here — it is an arm that does not fire, which is the whole point of writing the shape
// as a pattern instead of asserting it with `=`.
//
// Bindings made before a later position fails stay in env, and that is harmless: the
// caller drops the whole scope with the arm.
func (e ev) matchPattern(pat *ast.ArrayPattern, v Value, env *Env) (bool, error) {
	elems, ok, err := e.spread(v)
	if err != nil || !ok {
		return false, err
	}
	rest := restIndex(pat)
	if !fitsPattern(len(elems), len(pat.Elems), rest) {
		return false, nil
	}
	for j, el := range pat.Elems {
		if s, ok := el.(*ast.Splat); ok {
			if id, ok := s.X.(*ast.Ident); ok {
				tail, err := e.restOf(elems, j, len(pat.Elems))
				if err != nil {
					return false, err
				}
				env.Define(id.Name, tail)
			}
			continue
		}
		i := patternPos(j, rest, len(elems), len(pat.Elems))
		switch t := el.(type) {
		case *ast.ArrayPattern, *ast.DictPattern:
			ok, err := e.matchAny(t, elems[i], env)
			if err != nil || !ok {
				return false, err
			}
		case *ast.Ident:
			env.Define(t.Name, elems[i])
		default:
			// Not a name, so it is a value to compare against — evaluated inside the
			// pattern's own scope, so a later position may refer to an earlier binding.
			p, err := e.in(env).eval(el)
			if err != nil {
				return false, err
			}
			ok, err := e.patternFires(p, elems[i])
			if err != nil || !ok {
				return false, err
			}
		}
	}
	return true, nil
}

// restIndex is the position of a pattern's rest element, or -1. The parser allows one
// per level (RFC 0001).
func restIndex(pat *ast.ArrayPattern) int {
	for i, el := range pat.Elems {
		if _, ok := el.(*ast.Splat); ok {
			return i
		}
	}
	return -1
}

// fitsPattern is the length rule: exact without a rest, at least the other positions
// with one.
func fitsPattern(have, positions, rest int) bool {
	if rest < 0 {
		return have == positions
	}
	return have >= positions-1
}

// patternPos maps pattern position j, which is not the rest, onto the value's elements.
// Positions before the rest read from the front and positions after it from the back, so
// in `a, *mid, z` the `z` is always the last element.
func patternPos(j, rest, have, positions int) int {
	if rest < 0 || j < rest {
		return j
	}
	return have - (positions - j)
}

// restOf is what the rest element at position rest binds: a new array of the elements
// no other position takes, possibly empty. It is a copy, never a view, so writing to it
// cannot reach the array that was taken apart.
func (e ev) restOf(elems []Value, rest, positions int) (Value, error) {
	tail := elems[rest : len(elems)-(positions-1-rest)]
	if err := e.rs.allocCollection(len(tail), len(tail)); err != nil {
		return Nil(), err
	}
	return arrayOf(append(make([]Value, 0, len(tail)), tail...)), nil
}

// ---------------------------------------------------------------------------
// try (§8.11)
// ---------------------------------------------------------------------------

// evalTry runs a `try` in all of its forms. It catches script errors only: a timeout, a
// budget overrun, a depth limit and a cancellation all keep unwinding. The bound error
// dict lives in a scope of its own, so `e` does not leak past the fallback.
//
// An `ensure` runs on every way out that leaves the Run alive — the value, an error
// caught or not, and a `return`/`break`/`next` from the body — and on no other. What
// ends the Run (a limit, a cancellation, `exit`) does not run it, because running script
// code past a limit is precisely what the limit forbids (§8.11, §14.1).
func (e ev) evalTry(n *ast.TryExpr) (Value, error) {
	v, err := e.eval(n.X)
	if err != nil {
		if x, ok := catchableError(err); ok && n.Fallback != nil {
			v, err = e.evalFallback(n, x)
		}
	}
	if n.Ensure != nil && ensureRuns(err) {
		// The ensure's own failure replaces what was pending: swallowing it would hide
		// a broken release, and there is nowhere else for it to go. The value it will
		// hand back is a root while it runs (§14.2).
		m := e.rs.rootMark()
		e.rs.hold(v)
		_, eerr := e.execBlock(n.Ensure)
		e.rs.rootReset(m)
		if eerr != nil {
			return Nil(), eerr
		}
	}
	if err != nil {
		return Nil(), err
	}
	return v, nil
}

// evalFallback runs the `else` side with the error dict bound when the source asked for
// it (§8.11).
func (e ev) evalFallback(n *ast.TryExpr, x *Error) (Value, error) {
	if n.Var == "" {
		return e.eval(n.Fallback)
	}
	env := newEnv(e.env, 1)
	env.Define(n.Var, x.ErrorValue())
	m := e.rs.rootMark()
	e.rs.pushEnv(env)
	v, err := e.in(env).eval(n.Fallback)
	e.rs.rootReset(m)
	return v, err
}

// ensureRuns reports whether an `ensure` block runs while err is unwinding. A control
// signal counts as a way out of the body: `return` from inside a `try` releases what the
// `ensure` releases before it leaves the function.
func ensureRuns(err error) bool {
	if err == nil {
		return true
	}
	if _, ok := ctrlOf(err); ok {
		return true
	}
	_, ok := catchableError(err)
	return ok
}

// catchableError reports whether `try` may swallow err, and yields the *Error its
// `(e) ->` clause binds. Control signals are never errors to catch.
func catchableError(err error) (*Error, bool) {
	if _, ok := ctrlOf(err); ok {
		return nil, false
	}
	x := asError(err)
	if x == nil || !x.Catchable() {
		return nil, false
	}
	return x, true
}

// ---------------------------------------------------------------------------
// Names (§8.2, §9.2, §9.3, §10)
// ---------------------------------------------------------------------------

// evalIdent resolves a bare name in value position. The compile pass has already
// decided what the name is (§6.3 step 4); Ref is honoured for the one case the dynamic
// order cannot get right on its own, a module that shares its name with a builtin
// (`json` the module versus `json` the function). Anything unresolved is an error:
// there is no bareword-to-string shim (§9.3).
func (e ev) evalIdent(n *ast.Ident) (Value, error) {
	if v, ok := e.env.Lookup(n.Name); ok {
		return v, nil
	}
	if !e.rs.in.isRemoved(n.Name) {
		if fv, ok := e.rs.in.hostFunc(n.Name); ok {
			return fv, nil
		}
		if b, ok := LookupBuiltin(n.Name); ok && b.Available(e.rs.opts) && !b.Special {
			return Fn(b.Name, -1, b.Fn), nil
		}
	}
	return Nil(), nameErrorf("undefined variable '%s'", n.Name)
}

// evalGlobal reads a $var from the per-Run table. An unbound name is nil, never an
// error and never its own source text: nil is falsy, so an unbound variable in a
// condition means "no match" (§9.2).
func (e ev) evalGlobal(n *ast.GlobalVar) Value {
	if v, ok := e.rs.globals[n.Name]; ok {
		return v
	}
	return Nil()
}

// lookupModule finds a host module first, then a built-in one, honouring both
// Unregister and the declarative capability gates of §12.8.
func (e ev) lookupModule(name string) (Value, bool) {
	if e.rs.in.isRemoved(name) {
		return Nil(), false
	}
	if mv, ok := e.rs.in.hostModule(name); ok {
		return mv, true
	}
	return LookupModule(name, e.rs.opts)
}

// ---------------------------------------------------------------------------
// Assignment (§8.2, §8.5, §8.8)
// ---------------------------------------------------------------------------

func (e ev) evalAssign(n *ast.AssignExpr) (Value, error) {
	switch t := n.Target.(type) {
	case *ast.Ident:
		return e.assignName(n, t)
	case *ast.GlobalVar:
		return e.assignGlobal(n, t)
	case *ast.IndexExpr:
		return e.assignIndex(n, t)
	}
	return Nil(), e.at(typeErrorf("cannot assign to %T", n.Target), n.OpPos)
}

// combine turns a compound assignment into its read-modify-write value. `||=`, `&&=`
// and `??=` short-circuit, so they report that no write is needed at all (§8.5).
func (e ev) combine(op token.Kind, cur Value, value ast.Expr, pos token.Pos) (Value, bool, error) {
	switch op {
	case token.ASSIGN, token.DECLARE:
		v, err := e.eval(value)
		return v, true, err
	case token.OR_EQ:
		if cur.Truthy() {
			return cur, false, nil
		}
		v, err := e.eval(value)
		return v, true, err
	case token.AND_EQ:
		if !cur.Truthy() {
			return cur, false, nil
		}
		v, err := e.eval(value)
		return v, true, err
	case token.NIL_EQ:
		if !cur.IsNil() {
			return cur, false, nil
		}
		v, err := e.eval(value)
		return v, true, err
	}
	bin := token.AssignBinaryOp(op)
	if bin == token.EOF {
		return Nil(), false, typeErrorf("unsupported assignment operator %s", op)
	}
	rhs, err := e.eval(value)
	if err != nil {
		return Nil(), false, err
	}
	v, err := e.binary(bin, cur, rhs)
	if err != nil {
		return Nil(), false, e.at(err, pos)
	}
	return v, true, nil
}

// assignName is `x = v` and `x := v`. `=` writes the nearest existing binding and
// creates one in the current scope when there is none; `:=` always creates or shadows
// here (§8.2).
func (e ev) assignName(n *ast.AssignExpr, t *ast.Ident) (Value, error) {
	cur := Nil()
	if n.Op != token.ASSIGN && n.Op != token.DECLARE {
		// An undefined local on the left of `||=` or `??=` reads as nil rather than
		// raising (§8.5); every other compound operator would fail on nil anyway.
		cur, _ = e.env.Lookup(t.Name)
	}
	v, write, err := e.combine(n.Op, cur, n.Value, n.OpPos)
	if err != nil {
		return Nil(), err
	}
	if !write {
		return cur, nil
	}
	if n.Op == token.DECLARE {
		e.env.Define(t.Name, v)
	} else {
		e.env.Set(t.Name, v)
	}
	return v, nil
}

func (e ev) assignGlobal(n *ast.AssignExpr, t *ast.GlobalVar) (Value, error) {
	cur := Nil()
	if n.Op != token.ASSIGN && n.Op != token.DECLARE {
		cur = e.evalGlobal(t)
	}
	v, write, err := e.combine(n.Op, cur, n.Value, n.OpPos)
	if err != nil {
		return Nil(), err
	}
	if !write {
		return cur, nil
	}
	e.rs.globals[globalName(t.Name)] = v
	return v, nil
}

// storeIndex writes v at t's subscript. Destructuring has no read-modify-write half, so
// it evaluates the receiver and the subscript once and goes straight to the write that
// assignIndex ends with.
func (e ev) storeIndex(t *ast.IndexExpr, v Value) error {
	if t.Index2 != nil {
		return e.at(typeErrorf("cannot assign to a slice"), t.Lbrack)
	}
	recv, err := e.eval(t.X)
	if err != nil {
		return err
	}
	idx, err := e.eval(t.Index)
	if err != nil {
		return err
	}
	return e.setIndex(recv, idx, v, t.Lbrack)
}

// setIndex is the write half of `x[i] = v` (§8.8), shared by assignment and
// destructuring.
func (e ev) setIndex(recv, idx, v Value, pos token.Pos) error {
	switch recv.Kind() {
	case KArray:
		if !idx.IsNum() {
			return e.at(typeErrorf("array index must be an int, got %s", idx.Kind()), pos)
		}
		if err := e.rs.allocCollection(int(idx.Int())+1, int(idx.Int())+1-recv.Len()); err != nil {
			return err
		}
		if err := recv.setIndex(int(idx.Int()), v); err != nil {
			return e.at(err, pos)
		}
	case KDict:
		if err := recv.setKey(idx, v); err != nil {
			return e.at(err, pos)
		}
	default:
		return e.at(typeErrorf("cannot assign to an index of %s", recv.Kind()), pos)
	}
	return nil
}

// assignIndex evaluates the receiver and the subscript exactly once, which is what
// makes `x[i] += 1` well defined (§8.8).
func (e ev) assignIndex(n *ast.AssignExpr, t *ast.IndexExpr) (Value, error) {
	if t.Index2 != nil {
		return Nil(), e.at(typeErrorf("cannot assign to a slice"), t.Lbrack)
	}
	recv, err := e.eval(t.X)
	if err != nil {
		return Nil(), err
	}
	idx, err := e.eval(t.Index)
	if err != nil {
		return Nil(), err
	}
	cur := Nil()
	if n.Op != token.ASSIGN && n.Op != token.DECLARE {
		cur, err = e.index(recv, idx, Nil(), false)
		if err != nil {
			return Nil(), e.at(err, t.Lbrack)
		}
	}
	v, write, err := e.combine(n.Op, cur, n.Value, n.OpPos)
	if err != nil {
		return Nil(), err
	}
	if !write {
		return cur, nil
	}
	if err := e.setIndex(recv, idx, v, t.Lbrack); err != nil {
		return Nil(), err
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Destructuring (§8.15)
// ---------------------------------------------------------------------------

// evalDestructure is `a, b = xs` and `[a, [b, c]] = xs`. The value of the expression is
// the right side, exactly as it is for `a = 1`; a right side of the wrong shape is a
// run-time error rather than a silent nil, because a shape that does not fit is a bug at
// the assignment and not three lines further down.
func (e ev) evalDestructure(n *ast.DestructureAssign) (Value, error) {
	v, err := e.eval(n.Value)
	if err != nil {
		return Nil(), err
	}
	// A rest allocates, and the walk an allocation may start has to see the value being
	// taken apart: it may be the only thing holding it (§14.2).
	m := e.rs.rootMark()
	defer e.rs.rootReset(m)
	e.rs.hold(v)
	if err := e.destructureAny(n.Pattern, v, n.Op); err != nil {
		return Nil(), e.at(err, n.OpPos)
	}
	return v, nil
}

// destructureAny takes v apart with an array or a dict pattern, in two passes: every
// value the pattern reaches is read first, at every depth, and only then is any target
// written. A target may write into the very value being taken apart — `xs[1], xs[0] =
// xs`, `{a: d["b"], b: x} = d`, or a nested container read further right — and the right
// side is read once, as it stood (§8.15). A shape that does not fit therefore writes
// nothing at all.
func (e ev) destructureAny(pat ast.Expr, v Value, op token.Kind) error {
	m := e.rs.rootMark()
	defer e.rs.rootReset(m)
	var out []binding
	if err := e.plan(pat, v, &out); err != nil {
		return err
	}
	for _, b := range out {
		if err := e.write(b, op); err != nil {
			return err
		}
	}
	return nil
}

// binding is one write a destructuring makes: a target and the value it receives.
type binding struct {
	target ast.Expr
	val    Value
}

// plan reads what pat takes from v into out, in source order, writing nothing. A value
// is held as it is read: a later write may replace the only other reference to it before
// it is bound, and a rest is a new collection nothing else holds (§14.2).
func (e ev) plan(pat ast.Expr, v Value, out *[]binding) error {
	switch t := pat.(type) {
	case *ast.ArrayPattern:
		return e.planArray(t, v, out)
	case *ast.DictPattern:
		return e.planDict(t, v, out)
	}
	e.rs.hold(v)
	*out = append(*out, binding{pat, v})
	return nil
}

func (e ev) planArray(pat *ast.ArrayPattern, v Value, out *[]binding) error {
	elems, ok, err := e.spread(v)
	if err != nil {
		return err
	}
	if !ok {
		return e.at(typeErrorf("cannot destructure %s: the right side must be an array", v.Kind()), pat.Start)
	}
	rest := restIndex(pat)
	if !fitsPattern(len(elems), len(pat.Elems), rest) {
		if rest >= 0 {
			return e.at(indexErrorf("destructuring expects at least %d values, got %d", len(pat.Elems)-1, len(elems)), pat.Start)
		}
		return e.at(indexErrorf("destructuring expects %d values, got %d", len(pat.Elems), len(elems)), pat.Start)
	}
	for j, target := range pat.Elems {
		if s, ok := target.(*ast.Splat); ok {
			tail, err := e.restOf(elems, j, len(pat.Elems))
			if err != nil {
				return err
			}
			e.rs.hold(tail)
			*out = append(*out, binding{s.X, tail})
			continue
		}
		if err := e.plan(target, elems[patternPos(j, rest, len(elems), len(pat.Elems))], out); err != nil {
			return err
		}
	}
	return nil
}

// planDict reads a dict pattern (RFC 0002). The right side must be a dict — a record is
// one — and every key the pattern names must be there: the array form is strict about
// length, the dict form about presence, and a missing key is the same `key` error
// `fetch` raises. Keys the pattern does not name are ignored unless a rest collects
// them, because a JSON object grows.
func (e ev) planDict(pat *ast.DictPattern, v Value, out *[]binding) error {
	if v.Kind() != KDict {
		return e.at(typeErrorf("cannot destructure %s with a dict pattern", v.Kind()), pat.Lbrace)
	}
	d := v.odict()
	for _, en := range pat.Entries {
		val, ok := d.Get(Str(en.Key))
		if !ok {
			return e.at(newError(ErrKindKey, "destructuring: key not found: %s", Str(en.Key).Inspect()), en.KeyPos)
		}
		if err := e.plan(en.Value, val, out); err != nil {
			return err
		}
	}
	if pat.Rest != nil {
		rest, err := e.dictRest(pat, d)
		if err != nil {
			return err
		}
		e.rs.hold(rest)
		*out = append(*out, binding{pat.Rest, rest})
	}
	return nil
}

// write makes one planned write: a name binds as `=` or `:=` say (§8.2), a $var inserts
// into the globals, an index stores.
func (e ev) write(b binding, op token.Kind) error {
	switch t := b.target.(type) {
	case *ast.Ident:
		e.bindTarget(t, b.val, op)
	case *ast.GlobalVar:
		e.rs.globals[globalName(t.Name)] = b.val
	case *ast.IndexExpr:
		return e.storeIndex(t, b.val)
	default:
		return e.at(typeErrorf("cannot assign to %T", b.target), b.target.Pos())
	}
	return nil
}

// dictRest is what `**rest` binds: a new dict of the entries the pattern does not name,
// in the order the dict holds them. It is a plain dict even when the source is a record,
// as `{**p}` is (§8.16): a record is a shape the constructor promised, and a dict with
// some fields taken out is no longer that shape.
func (e ev) dictRest(pat *ast.DictPattern, d *OrderedDict) (Value, error) {
	// Every entry is read twice, to size the rest and to fill it, and a step is a
	// read: a large dict pays for its rest before anything is built, as `**d` does.
	if err := e.rs.step(int64(d.Len())); err != nil {
		return Nil(), err
	}
	named := make(map[string]bool, len(pat.Entries))
	for _, en := range pat.Entries {
		named[en.Key] = true
	}
	n := 0
	d.Each(func(k, _ Value) bool {
		if k.Kind() != KString || !named[k.Str()] {
			n++
		}
		return true
	})
	if err := e.rs.allocCollection(n, n); err != nil {
		return Nil(), err
	}
	out := NewOrderedDictCap(n)
	var err error
	d.Each(func(k, val Value) bool {
		if k.Kind() == KString && named[k.Str()] {
			return true
		}
		err = out.Set(k, val)
		return err == nil
	})
	if err != nil {
		return Nil(), err
	}
	return dictOf(out), nil
}

// bindTarget writes one name a destructuring binds: `:=` defines it in this scope, `=`
// writes it wherever it resolves (§8.2).
func (e ev) bindTarget(t *ast.Ident, v Value, op token.Kind) {
	if op == token.DECLARE {
		e.env.Define(t.Name, v)
	} else {
		e.env.Set(t.Name, v)
	}
}

// spread is the shape rule every destructuring form shares: only an Array or a Range has
// positions to take apart, and a Range is materialised under the collection limit. The
// bool is false for a value with no positions at all, which the caller turns into an
// error or into "this arm does not fire" as its own rules require.
func (e ev) spread(v Value) ([]Value, bool, error) {
	switch v.Kind() {
	case KArray:
	case KRange:
		if err := e.rs.allocCollection(v.Len(), v.Len()); err != nil {
			return nil, false, err
		}
	default:
		return nil, false, nil
	}
	return v.Elems(), true, nil
}

// ---------------------------------------------------------------------------
// Indexing (§8.8)
// ---------------------------------------------------------------------------

func (e ev) evalIndex(n *ast.IndexExpr) (Value, error) {
	// `x?.f[0]` indexes the receiver position of a trailer, so a short-circuited chain
	// skips the subscript too (§8.7).
	recv, err := e.evalChain(n.X)
	if err != nil {
		return Nil(), err
	}
	i, err := e.eval(n.Index)
	if err != nil {
		return Nil(), err
	}
	i2 := Nil()
	if n.Index2 != nil {
		if i2, err = e.eval(n.Index2); err != nil {
			return Nil(), err
		}
	}
	v, err := e.index(recv, i, i2, n.Index2 != nil)
	return v, e.at(err, n.Lbrack)
}

// index is `x[i]` and `x[i, n]`. Indexing nil is an error rather than nil, because a
// silently-nil chain hides the real failure: `?.get(k)` is the nil-safe single step
// and `.dig(k1, k2)` the nil-safe path (§8.8).
func (e ev) index(recv, i, i2 Value, has2 bool) (Value, error) {
	switch recv.Kind() {
	case KDict:
		if has2 {
			return Nil(), typeErrorf("a dict does not support two-argument indexing")
		}
		return recv.Get(i), nil
	case KNil:
		return Nil(), typeErrorf("cannot index nil")
	case KString, KArray, KRange:
		if has2 {
			return e.slice(recv, int(i.Int()), int(i2.Int()))
		}
		if i.Kind() == KRange {
			r := i.rng()
			start := int(r.Lo)
			n := r.Len()
			if start < 0 {
				start += recv.Len()
			}
			return e.slice(recv, start, n)
		}
		if !i.IsNum() {
			// A match array answers to its named groups too (§8.4); every other array
			// is indexed by position alone (§8.8).
			if names := recv.arrNames(); names != nil && i.Kind() == KString {
				at, ok := names[i.Str()]
				if !ok {
					return Nil(), nil
				}
				return recv.Index(at), nil
			}
			return Nil(), typeErrorf("%s index must be an int, got %s", recv.Kind(), i.Kind())
		}
		return recv.Index(int(i.Int())), nil
	}
	return Nil(), typeErrorf("cannot index %s", recv.Kind())
}

// slice is `x[i, n]`: n runes of a String or n elements of an Array. A negative count
// is nil, and the range is clamped to the receiver.
func (e ev) slice(recv Value, start, count int) (Value, error) {
	n := recv.Len()
	if start < 0 {
		start += n
	}
	if start < 0 || start > n || count < 0 {
		return Nil(), nil
	}
	end := start + count
	if end > n {
		end = n
	}
	if recv.Kind() == KString {
		rs := []rune(recv.Str())
		return Str(string(rs[start:end])), nil
	}
	if err := e.rs.allocCollection(end-start, end-start); err != nil {
		return Nil(), err
	}
	xs := recv.Elems()
	out := make([]Value, end-start)
	copy(out, xs[start:end])
	return arrayOf(out), nil
}
