package mzs

import (
	"mzs/internal/ast"
	"mzs/internal/token"
)

// Calls, closures and method dispatch. The control-flow rules of §8.10 are all decided
// here, in exactly three places:
//
//   - invoke absorbs `return` at a named-function boundary and `next` at any call;
//   - the loop constructs in eval.go absorb `break` and `next`;
//   - every call site turns a `break` that escaped a closure into the value of that
//     call, which is what makes `xs.each { break 1 }` evaluate to 1.
//
// Nothing uses a Go panic, so the step budget stays accurate (§8.10).

// definedName is the one builtin the evaluator handles structurally: its argument is a
// name to test for boundness, not an expression to evaluate (§12.1).
const definedName = "defined"

// namedArg is one evaluated `name = value` argument (§8.7). The name is the callee's
// parameter, so the value stays unplaced until bindParams knows what that list looks
// like.
type namedArg struct {
	Name string
	Val  Value
}

// namedArgs is the named half of one call site's argument list. It is nil for every call
// the language makes on a script's behalf — a stdlib row invoking a closure, a host
// function calling back, a task's body — because only a call *expression* can spell a
// parameter name.
type namedArgs []namedArg

// reject is the answer for every callee that has no parameter names to bind: a host
// function, a builtin and a stdlib row all take their arguments by position (§8.7), so
// the name has nowhere to go and saying so beats binding it somewhere plausible.
func (na namedArgs) reject(name string) error {
	if len(na) == 0 {
		return nil
	}
	return argErrorf("%s takes its arguments by position, so '%s = …' has no parameter to bind",
		name, na[0].Name)
}

// init installs the evaluator behind the seam the stdlib calls through. Rebuilding an
// ev from the Ctx is sound because an ev is only ever those pointers, and a call always
// starts a fresh scope from the callee's captured Env.
func init() {
	evalCall = func(c *Ctx, fn Value, args []Value, closure Value) (Value, error) {
		e := ev{rs: c.rs, cx: c}
		// A trailing closure is an ordinary last argument (§4.2), so attaching one is
		// appending it — there is no separate block channel any more.
		if closure.Kind() == KFunc {
			all := make([]Value, 0, len(args)+1)
			args = append(append(all, args...), closure)
		}
		return e.invoke(fn, args, nil, c.pos)
	}
}

// makeFunc builds a closure over the current scope. The parser has already given a
// parameter-less `{ … }` its implicit `it` parameter (§8.9), so nothing is synthesised
// here.
func (e ev) makeFunc(name string, params []ast.Param, body *ast.BlockStmt, lambda, async bool) Value {
	arity := len(params)
	for _, p := range params {
		if p.Rest || p.Default != nil {
			arity = -1
			break
		}
	}
	frame := 0
	if body != nil {
		frame = body.FrameSize
	}
	return funcOf(&Func{
		Name:   name,
		Params: params,
		Body:   body,
		Env:    e.env,
		Arity:  arity,
		Lambda: lambda,
		Async:  async,
		Frame:  frame,
	})
}

// invoke calls a function value: a host function, or a script `fn`/closure. named is the
// `name = value` half of the call site's arguments and is nil everywhere but there.
func (e ev) invoke(fn Value, args []Value, named namedArgs, pos token.Pos) (Value, error) {
	if fn.Kind() != KFunc {
		return Nil(), e.at(typeErrorf("not a function: %s", fn.Kind()), pos)
	}
	f := fn.fn()
	if f == nil {
		return Nil(), e.at(typeErrorf("not a function: nil"), pos)
	}
	if err := e.rs.step(1); err != nil {
		return Nil(), err
	}
	if err := e.rs.enter(f.Name, pos.Line, pos.Col); err != nil {
		return Nil(), e.at(err, pos)
	}
	defer e.rs.leave()

	if f.Host != nil {
		if err := named.reject(fnName(f)); err != nil {
			return Nil(), e.at(err, pos)
		}
		if f.Arity >= 0 && len(args) != f.Arity {
			return Nil(), e.at(argErrorf("%s expects %d argument(s), got %d",
				fnName(f), f.Arity, len(args)), pos)
		}
		restore := e.enterCall(f.Name, pos, args)
		v, err := f.Host(e.cx, args)
		restore()
		// A host function standing in for a closure may end an iteration with `next`;
		// there is no enclosing Go loop for the signal to reach, so absorb it here.
		if c, ok := ctrlOf(err); ok && c.kind == ctrlNext {
			return c.val, nil
		}
		return v, e.at(err, pos)
	}

	if f.Async {
		// `async fn` differs from `fn` at exactly one point, and this is it: the call
		// starts the body on a task and hands back the handle (§8.14). Everything above
		// — the step, the frame, the depth check — is charged to the caller either way.
		return e.spawnTask(f, args, named, pos)
	}

	env, err := e.bindParams(f, args, named)
	if err != nil {
		return Nil(), e.at(err, pos)
	}
	if f.Record != nil {
		// A record's constructor has no body: the fields it has just bound *are* the
		// dict it hands back (§7.8). Everything above this line — the arity check, the
		// `name = value` arguments, the defaults — is the ordinary call of §8.7, which is
		// the point of building it out of a parameter list.
		return f.Record.build(env, f.Params), nil
	}
	if f.Body == nil {
		return Nil(), nil
	}
	// The parameters and the body share one scope: the body's braces are the call's
	// own `{ … }`, not a second one nested inside it (§8.2).
	v, err := e.in(env).execStmts(f.Body.Stmts)
	if err == nil {
		return v, nil
	}
	c, ok := ctrlOf(err)
	if !ok {
		return Nil(), err
	}
	switch {
	case c.kind == ctrlReturn && !f.Lambda:
		// `return` unwinds to the nearest enclosing named function; inside a closure it
		// keeps travelling, so the lexically enclosing function is the one that returns.
		return c.val, nil
	case c.kind == ctrlNext:
		// `next` ends one invocation of a closure, and behaves as `return` in a `fn`.
		return c.val, nil
	}
	return Nil(), err
}

// call invokes fn and converts a `break` that escaped it into the value of this call,
// which is §8.10's rule that `break` ends the call a closure was passed to.
func (e ev) call(fn Value, args []Value, named namedArgs, pos token.Pos) (Value, error) {
	v, err := e.invoke(fn, args, named, pos)
	if bv, ok := isBreak(err); ok {
		return bv, nil
	}
	return v, err
}

// enterCall arms the shared Ctx for one stdlib invocation. A trailing closure argument
// is offered on the Ctx as well as in args, so a library function may reach for it
// either way without the evaluator caring which convention it uses.
func (e ev) enterCall(name string, pos token.Pos, args []Value) func() {
	closure := Nil()
	if n := len(args); n > 0 && args[n-1].Kind() == KFunc {
		closure = args[n-1]
	}
	return e.cx.setCall(name, pos, args, closure)
}

// bindParams builds the callee's frame. Positions come first, then the `name = value`
// arguments of §8.7, then the declared defaults; a parameter no rule reached is the
// arity error. A closure literal is exempt from that check because library functions
// decide how many values to pass it: extra arguments are dropped and missing ones become
// nil (§7.7).
func (e ev) bindParams(f *Func, args []Value, named namedArgs) (*Env, error) {
	size := f.Frame
	if size < len(f.Params) {
		size = len(f.Params)
	}
	env := newEnv(f.Env, size)
	fe := e.in(env)
	if err := checkNamed(f, args, named); err != nil {
		return nil, err
	}
	for i, p := range f.Params {
		if p.Rest {
			rest := []Value{}
			if i < len(args) {
				rest = append(rest, args[i:]...)
			}
			env.Define(p.Name, arrayOf(rest))
			return env, nil
		}
		if i < len(args) {
			env.Define(p.Name, args[i])
			continue
		}
		if j := named.index(p.Name); j >= 0 {
			env.Define(p.Name, named[j].Val)
			continue
		}
		if p.Default != nil {
			// Defaults see the parameters bound before them — including one another and
			// any parameter a name filled, since binding runs in declaration order.
			dv, err := fe.eval(p.Default)
			if err != nil {
				return nil, err
			}
			env.Define(p.Name, dv)
			continue
		}
		if f.LenientArity() {
			env.Define(p.Name, Nil())
			continue
		}
		return nil, missingArgError(f, args, named, p.Name)
	}
	if len(args) > len(f.Params) && !f.LenientArity() {
		return nil, argErrorf("%s expects %d argument(s), got %d", fnName(f), len(f.Params), len(args))
	}
	return env, nil
}

// index finds the named argument for parameter name, or -1.
func (na namedArgs) index(name string) int {
	for i, a := range na {
		if a.Name == name {
			return i
		}
	}
	return -1
}

// checkNamed rejects the three named-argument mistakes the call site cannot see for
// itself, all of which need the callee's parameter list: a name that is not a parameter,
// a name for a parameter a position already filled, and a name for `*rest`, which
// collects positions and so has no single value to be given. The parser has already
// ruled out a repeated name and a positional argument after a named one (§5.6).
func checkNamed(f *Func, args []Value, named namedArgs) error {
	for _, a := range named {
		i := paramIndex(f.Params, a.Name)
		switch {
		case i < 0:
			return argErrorf("%s has no parameter named '%s'%s", fnName(f), a.Name,
				paramHint(f.Params))
		case f.Params[i].Rest:
			return argErrorf("%s collects '*%s' from the remaining positions, so it cannot be given by name",
				fnName(f), a.Name)
		case i < len(args):
			return argErrorf("%s got two values for parameter '%s': one by position and one by name",
				fnName(f), a.Name)
		}
	}
	return nil
}

// missingArgError names the parameter that was left without a value. Where the call used
// no names at all the count is the whole story and §8.7's original wording still says it
// best; once names are in play the counts stop lining up and the parameter's name is the
// only useful thing to print.
func missingArgError(f *Func, args []Value, named namedArgs, param string) error {
	if len(named) == 0 {
		return argErrorf("%s expects %d argument(s), got %d", fnName(f), len(f.Params), len(args))
	}
	return argErrorf("%s is missing a value for parameter '%s'%s", fnName(f), param, paramHint(f.Params))
}

// paramHint lists what the callee will answer to, which is the whole fix for a misspelled
// or misremembered parameter name.
func paramHint(params []ast.Param) string {
	if len(params) == 0 {
		return ""
	}
	out := "; it takes "
	for i, p := range params {
		switch {
		case i == 0:
		case i == len(params)-1:
			out += " and "
		default:
			out += ", "
		}
		if p.Rest {
			out += "'*" + p.Name + "'"
			continue
		}
		out += "'" + p.Name + "'"
	}
	return out
}

func paramIndex(params []ast.Param, name string) int {
	for i, p := range params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

func fnName(f *Func) string {
	if f.Name == "" {
		return "function"
	}
	return f.Name
}

// ---------------------------------------------------------------------------
// Call expressions
// ---------------------------------------------------------------------------

func (e ev) evalCallExpr(n *ast.CallExpr) (Value, error) {
	if id, ok := n.Fn.(*ast.Ident); ok {
		if id.Name == definedName {
			if _, shadowed := e.env.Lookup(id.Name); !shadowed {
				return e.evalDefined(n)
			}
		}
		args, named, err := e.evalArgs(n.Args, n.Named)
		if err != nil {
			return Nil(), err
		}
		// RefFunc is the compile pass saying this name is the function rather than a
		// binding of the same name — the UFCS rewrite of `x.f(y)` reaches here that way
		// even when `f` also names a module (§12.8).
		v, ok, err := e.callNamed(id.Name, args, named, id.Start, id.Ref == ast.RefFunc)
		if !ok {
			return Nil(), e.at(undefinedFunctionError(id.Name, e.scriptFuncNames()), id.Start)
		}
		return v, err
	}
	// The callee is the receiver position of a trailer, so a `?.` under it skips this
	// call whole — arguments included (§8.7).
	fn, err := e.evalChain(n.Fn)
	if err != nil {
		return Nil(), err
	}
	args, named, err := e.evalArgs(n.Args, n.Named)
	if err != nil {
		return Nil(), err
	}
	return e.call(fn, args, named, n.Stop)
}

// callFunction resolves a bare name to something callable and invokes it: a binding in
// scope, a host function, a builtin, and finally the method table of the first
// argument's kind. It is both the `f(…)` path and the UFCS fallback of §4.3 step 2, so
// the two can never disagree. ok is false when no such function exists, which is the
// caller's cue to produce its own diagnostic.
func (e ev) callFunction(name string, args []Value, pos token.Pos) (Value, bool, error) {
	return e.callNamed(name, args, nil, pos, false)
}

// callNamed is callFunction with one extra decision exposed: overName steps over a
// binding of that name that is not a function. It is the UFCS half of the lookup — the
// `f` of `x.f(y)` is a stdlib row, not the module dict `include json` bound under the
// same spelling, so `nil.json` still encodes (§4.3 step 2). A direct `f(…)` passes
// false, because there the binding *is* what was called and `not a function` is the
// answer.
func (e ev) callNamed(name string, args []Value, named namedArgs, pos token.Pos, overName bool) (Value, bool, error) {
	if fv, ok := e.env.Lookup(name); ok {
		switch {
		case fv.Kind() == KFunc:
			v, err := e.call(fv, args, named, pos)
			return v, true, err
		case !overName:
			return Nil(), true, e.at(typeErrorf("not a function: %s", fv.Kind()), pos)
		}
	}
	if !e.rs.in.isRemoved(name) {
		if fv, ok := e.rs.in.hostFunc(name); ok {
			v, err := e.call(fv, args, named, pos)
			return v, true, err
		}
		if b, ok := LookupBuiltin(name); ok && b.Available(e.rs.opts) {
			if err := named.reject(b.Name); err != nil {
				return Nil(), true, e.at(err, pos)
			}
			v, err := e.invokeBuiltin(b, args, pos)
			return v, true, err
		}
	}
	// UFCS has one namespace, not a function namespace plus a method namespace (§12):
	// `filter(xs, f)` and `xs.filter(f)` are the same operation, so a prefix call whose
	// name is only a method of the first argument's kind resolves here. dispatch calls
	// back into callFunction, but only after this same lookup has already failed, so the
	// two cannot recur.
	if len(args) > 0 && !e.rs.in.isRemoved(name) {
		if m, ok := LookupMethod(args[0].Kind(), name); ok && m.Available(e.rs.opts) {
			if err := named.reject(name); err != nil {
				return Nil(), true, e.at(err, pos)
			}
			v, err := e.invokeMethod(m, name, args[0], args[1:], pos)
			return v, true, err
		}
	}
	return Nil(), false, nil
}

// scriptFuncNames feeds the §17 "did you mean" suggestion with the functions this
// program actually declared, not just the builtins.
func (e ev) scriptFuncNames() []string {
	var out []string
	for s := e.env; s != nil; s = s.Parent() {
		for i, name := range s.names {
			if s.slots[i].Kind() == KFunc {
				out = append(out, name)
			}
		}
	}
	return out
}

func (e ev) invokeBuiltin(b Builtin, args []Value, pos token.Pos) (Value, error) {
	if err := b.CheckArity(len(args)); err != nil {
		return Nil(), e.at(err, pos)
	}
	if err := e.rs.step(1); err != nil {
		return Nil(), err
	}
	if err := e.rs.enter(b.Name, pos.Line, pos.Col); err != nil {
		return Nil(), e.at(err, pos)
	}
	restore := e.enterCall(b.Name, pos, args)
	v, err := b.Fn(e.cx, args)
	restore()
	e.rs.leave()
	if bv, ok := isBreak(err); ok {
		return bv, nil
	}
	return v, e.at(err, pos)
}

// evalDefined is the one structural builtin (§12.1): its argument is a name to test,
// not an expression to evaluate, which is why an unbound `$price` is legal here and
// nowhere else.
func (e ev) evalDefined(n *ast.CallExpr) (Value, error) {
	if len(n.Named) > 0 {
		// `defined` takes a name to test for boundness, so `defined(x = 1)` reads as an
		// argument named after the very thing being asked about. Say which one it is
		// rather than counting to zero at it.
		return Nil(), e.at(argErrorf("%s takes a name to test, not a named argument: write %s(%s)",
			definedName, definedName, n.Named[0].Name), n.Stop)
	}
	if len(n.Args) != 1 {
		return Nil(), e.at(argErrorf("%s expects 1 argument, got %d", definedName, len(n.Args)), n.Stop)
	}
	switch a := n.Args[0].(type) {
	case *ast.GlobalVar:
		_, ok := e.rs.globals[a.Name]
		return Bool(ok), nil
	case *ast.Ident:
		if _, ok := e.env.Lookup(a.Name); ok {
			return Bool(true), nil
		}
		if e.rs.in.isRemoved(a.Name) {
			return Bool(false), nil
		}
		if _, ok := e.rs.in.hostFunc(a.Name); ok {
			return Bool(true), nil
		}
		if b, ok := LookupBuiltin(a.Name); ok && b.Available(e.rs.opts) {
			return Bool(true), nil
		}
		_, ok := e.lookupModule(a.Name)
		return Bool(ok), nil
	}
	v, err := e.eval(n.Args[0])
	return Bool(err == nil && !v.IsNil()), nil
}

// evalArgs evaluates arguments strictly left to right (§8.7) and folds keyword
// arguments into the one trailing dict argument. A trailing closure is not special: it
// is already an element of Args.
func (e ev) evalArgs(exprs []ast.Expr, named []ast.NamedArg) ([]Value, namedArgs, error) {
	if len(exprs) == 0 && len(named) == 0 {
		return nil, nil, nil
	}
	args := make([]Value, 0, len(exprs))
	for _, a := range exprs {
		v, err := e.eval(a)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, v)
	}
	if len(named) == 0 {
		return args, nil, nil
	}
	// Positions before names is the source order, since the parser refuses a positional
	// argument after a named one (§5.6). The one exception is a trailing closure, which
	// §4.2 appends to the positional list and so is *constructed* before the named values
	// are evaluated; a closure literal only captures the scope it stands in, so no
	// evaluation of the script's can observe the difference (§8.7).
	out := make(namedArgs, 0, len(named))
	for _, a := range named {
		v, err := e.eval(a.Value)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, namedArg{Name: a.Name, Val: v})
	}
	return args, out, nil
}

// ---------------------------------------------------------------------------
// Method calls (§4.3, §8.7)
// ---------------------------------------------------------------------------

func (e ev) evalMethodCall(n *ast.MethodCall) (Value, error) {
	if n.Recv == nil {
		return Nil(), e.at(newError(ErrKindInternal, "mzs: method call without a receiver"), n.NamePos)
	}
	recv, err := e.evalChain(n.Recv)
	if err != nil {
		return Nil(), err
	}
	// `?.` short-circuits on a nil receiver *before* the arguments are evaluated, and
	// the whole postfix chain above it goes with it (§8.7) — errNilChain is how the
	// trailers stacked on this one learn to skip themselves.
	if n.Safe && recv.IsNil() {
		return Nil(), errNilChain
	}
	args, named, err := e.evalArgs(n.Args, n.Named)
	if err != nil {
		return Nil(), err
	}
	// A module is reached with `.` like anything else (§12.8), and the compile pass has
	// already decided that this receiver names one. An ordinary dict never dispatches
	// `.` to its own keys (§8.7), which is what keeps UFCS unambiguous.
	if id, ok := n.Recv.(*ast.Ident); ok && id.Ref == ast.RefModule {
		v, err := e.moduleCall(recv, id.Name, n.Name, args, named, n.NamePos)
		return v, e.at(err, n.NamePos)
	}
	v, err := e.dispatch(recv, n.Name, args, named, n.NamePos)
	return v, e.at(err, n.NamePos)
}

// moduleCall resolves `math.sqrt(4)`: the module's own members first, then the Dict rows
// a module answers like any other value (`json.len`). It deliberately stops short of
// UFCS step 2 — rewriting `math.max(1, 2)` to `max(math, 1, 2)` can never be what was
// meant, and the error it produces names the wrong thing entirely.
func (e ev) moduleCall(mod Value, modName, name string, args []Value, named namedArgs, pos token.Pos) (Value, error) {
	if v, ok, err := e.moduleMember(mod, name, args, named, pos); ok {
		return v, err
	}
	if m, ok := LookupMethod(mod.Kind(), name); ok && m.Available(e.rs.opts) {
		if err := named.reject(modName + "." + name); err != nil {
			return Nil(), err
		}
		return e.invokeMethod(m, name, mod, args, pos)
	}
	return Nil(), undefinedMemberError(modName, name, mod.Keys())
}

// dispatch is §8.7's lookup order: the receiver kind's stdlib table (falling back to
// the universal table), then a function of that name in lexical scope taking the
// receiver as its first argument, then `undefined method` with a suggestion.
func (e ev) dispatch(recv Value, name string, args []Value, named namedArgs, pos token.Pos) (Value, error) {
	// A record's own fields come first (§7.8). The label exists so that `m.amount` reads
	// the field, and a field sharing a name with a stdlib row would otherwise be the one
	// thing the shape could not say. Nothing is lost: UFCS gives every row a prefix
	// spelling, so `len(m)` still counts the entries of a record whose field is `len`
	// (D18), and the compile pass warns where the two names collide (§17).
	if rt := recv.recordType(); rt.Has(name) {
		if err := named.reject(name); err != nil {
			return Nil(), err
		}
		if len(args) > 0 {
			return Nil(), argErrorf("'%s' is a field of %s, so it takes no arguments, got %d",
				name, rt.Name, len(args))
		}
		return recv.Get(Str(name)), nil
	}
	// Unregister narrows the surface a script can reach, and UFCS gives every row two
	// spellings, so a removed name has to disappear from both of them.
	if !e.rs.in.isRemoved(name) {
		if m, ok := LookupMethod(recv.Kind(), name); ok && m.Available(e.rs.opts) {
			if err := named.reject(name); err != nil {
				return Nil(), err
			}
			return e.invokeMethod(m, name, recv, args, pos)
		}
	}
	// UFCS: `x.f(y)` is `f(x, y)` (D18). The compile pass rewrites what it can prove
	// statically; this covers the rest, where the name is a method of some other kind.
	// A binding that is not a function is not a candidate here — `include json` must not
	// take `nil.json` down with it.
	ufcs := make([]Value, 0, len(args)+1)
	ufcs = append(append(ufcs, recv), args...)
	if v, ok, err := e.callNamed(name, ufcs, named, pos, true); ok {
		return v, err
	}
	if rt := recv.recordType(); rt != nil {
		return Nil(), undefinedRecordMethodError(rt, name)
	}
	return Nil(), undefinedMethodError(recv.Kind(), name)
}

// invokeMethod runs one stdlib row against a receiver. It is shared by the two spellings
// of the same call — `xs.filter(f)` through dispatch and `filter(xs, f)` through
// callFunction — so neither can drift from the other in arity checking, step accounting
// or the `break` rule of §8.10.
func (e ev) invokeMethod(m Method, name string, recv Value, args []Value, pos token.Pos) (Value, error) {
	if err := m.CheckArity(len(args)); err != nil {
		return Nil(), err
	}
	if err := e.rs.step(1); err != nil {
		return Nil(), err
	}
	if err := e.rs.enter(name, pos.Line, pos.Col); err != nil {
		return Nil(), err
	}
	restore := e.enterCall(name, pos, args)
	v, err := m.Fn(e.cx, recv, args)
	restore()
	e.rs.leave()
	if bv, ok := isBreak(err); ok {
		return bv, nil
	}
	return v, err
}

// moduleMember reads `json.parse` and `math.pi` off a module value. A member that is a
// function is called; any other member is the value itself, so a module constant needs
// no parentheses.
func (e ev) moduleMember(mod Value, name string, args []Value, named namedArgs, pos token.Pos) (Value, bool, error) {
	if mod.Kind() != KDict {
		return Nil(), false, nil
	}
	member, ok := mod.odict().Get(Str(name))
	if !ok {
		return Nil(), false, nil
	}
	if member.Kind() == KFunc {
		// A module's exported `fn` is an ordinary script function, so `m.f(c = 5)` binds
		// by name exactly as `f(c = 5)` does inside the module (§12.8).
		v, err := e.call(member, args, named, pos)
		return v, true, err
	}
	if err := named.reject(name); err != nil {
		return Nil(), true, err
	}
	if len(args) == 0 {
		return member, true, nil
	}
	return Nil(), true, typeErrorf("not a function: %s", member.Kind())
}
