package mzs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"time"

	"mzs/internal/ast"
	"mzs/internal/parser"
	"mzs/internal/rx"
	"mzs/internal/token"
)

// The public entry points of §13.3 and the compile step of §6.3. Everything here
// upholds two promises: a *Program is immutable and may be run concurrently, and a
// script can never panic the host (A7) — Compile and Run both recover at the boundary
// and hand back an *Error.

// maxCompileErrors bounds one compilation's diagnostics, the same way §17 bounds the
// parser's: one bad line should cost one message, not a cascade.
const maxCompileErrors = 10

// New builds an interpreter. Register, RegisterModule and SetGlobal must all happen
// before the first Run (§13.3).
func New(opts Options) *Interp {
	in := &Interp{}
	in.initState(opts)
	return in
}

// Compile parses and compiles src. The result is immutable and safe for concurrent
// Runs; identical (name, src) pairs share one compilation through the program LRU.
func (in *Interp) Compile(name, src string) (p *Program, err error) {
	in.ensureState()
	key := name + "\x00" + src
	if cached, ok := in.progs.Get(key); ok {
		return cached, nil
	}
	defer func() {
		if r := recover(); r != nil {
			p, err = nil, internalError(name, r, in.opts.StrictWarnings)
		}
	}()

	file, perr := parser.Parse(name, src)
	if perr != nil {
		return nil, syntaxErrors(name, perr)
	}
	if file == nil {
		return nil, &Error{Kind: ErrKindSyntax, Msg: "empty parse result", File: name}
	}
	file.File = name

	warns, cerr := in.compilePass(name, file)
	if cerr != nil {
		return nil, cerr
	}
	if in.opts.StrictWarnings && len(warns) > 0 {
		w := warns[0]
		return nil, (&Error{Kind: ErrKindSyntax, Msg: w.Msg}).At(name, w.Line, w.Col)
	}
	p = newProgram(name, src, file, warns)
	in.progs.Add(key, p)
	return p, nil
}

// compilePass is §6.3. One walk compiles every regex literal, resolves UFCS, resolves
// every identifier, folds constants, rejects `==` against a regex literal and records
// the frame size of each scope. It runs before the *Program is published, which is why
// mutating the tree here is safe; nothing mutates it afterwards.
func (in *Interp) compilePass(name string, file *ast.Program) ([]Warning, error) {
	c := &compiler{in: in, file: name}
	c.push()
	// Top-level `fn`s are hoisted, so a call may appear above its declaration (§8.2).
	for _, s := range file.Stmts {
		if d, ok := s.(*ast.FnDecl); ok && d.Name != "" {
			c.declare(d.Name)
		}
	}
	c.stmts(file.Stmts)
	file.FrameSize = c.scope.size
	c.pop()
	if len(c.errs) == 1 {
		return nil, c.errs[0]
	}
	if len(c.errs) > 0 {
		return nil, errors.Join(c.errs...)
	}
	return c.warns, nil
}

// ---------------------------------------------------------------------------
// The compile step (§6.3)
// ---------------------------------------------------------------------------

// cscope mirrors, at compile time, exactly the scopes the evaluator will create at run
// time: one per `{ … }` (D2, §8.2), with a function's parameters sharing the scope of
// its body. size is the number of bindings created in it, which becomes the frame the
// evaluator pre-allocates.
type cscope struct {
	parent *cscope
	names  map[string]bool
	used   map[string]bool
	// mods marks the names an `include` introduced. They are ordinary bindings — the
	// statement assigns one like any other — but `.` on them reads members rather than
	// dispatching a method, which is the one thing the evaluator must be told
	// statically (§8.7, §12.8).
	mods map[string]bool
	size int
}

type compiler struct {
	in    *Interp
	file  string
	scope *cscope
	warns []Warning
	errs  []error
	pool  []string // method and function names, built only to make a suggestion
}

func (c *compiler) push() {
	c.scope = &cscope{parent: c.scope, names: map[string]bool{}, used: map[string]bool{}, mods: map[string]bool{}}
}

func (c *compiler) pop() { c.scope = c.scope.parent }

// markUsed records a read of name against the scope that declares it, which is what the
// unused-closure-parameter warning of §17 reads back.
func (c *compiler) markUsed(name string) {
	for s := c.scope; s != nil; s = s.parent {
		if s.names[name] {
			s.used[name] = true
			return
		}
	}
}

// warnAt records a §17 warning. Warnings never fail a compile unless StrictWarnings.
func (c *compiler) warnAt(pos token.Pos, format string, a ...any) {
	c.warns = append(c.warns, Warning{Msg: fmt.Sprintf(format, a...), Line: pos.Line, Col: pos.Col})
}

// declare records a binding in the current scope and grows its frame.
func (c *compiler) declare(name string) {
	if name == "" {
		return
	}
	c.scope.names[name] = true
	c.scope.size++
}

// known reports whether the name is bound anywhere up the chain, which is what decides
// between assigning to an existing binding and creating one (§8.2).
// declareModule declares an include's name and remembers that it is a module.
func (c *compiler) declareModule(name string) {
	c.declare(name)
	c.scope.mods[name] = true
}

// missingInclude reports whether name is a module the program can include but did not.
func (c *compiler) missingInclude(name string) bool {
	if c.known(name) {
		return false
	}
	_, ok := lookupIncludable(c.in, name)
	return ok
}

// knownModule reports whether name was introduced by an `include` still in scope.
func (c *compiler) knownModule(name string) bool {
	for s := c.scope; s != nil; s = s.parent {
		if s.names[name] {
			return s.mods[name]
		}
	}
	return false
}

func (c *compiler) known(name string) bool {
	for s := c.scope; s != nil; s = s.parent {
		if s.names[name] {
			return true
		}
	}
	return false
}

// visible lists every binding in scope, for a did-you-mean over the script's own names.
func (c *compiler) visible() []string {
	var out []string
	for s := c.scope; s != nil; s = s.parent {
		for n := range s.names {
			out = append(out, n)
		}
	}
	return out
}

// hasFunc reports whether the name is callable without being a local: a host function
// or a builtin. `defined` is always callable — it is a special form the evaluator
// handles structurally (§12.1), so it must resolve even before builtins.go registers it.
func (c *compiler) hasFunc(name string) bool {
	if name == definedName {
		return true
	}
	if c.in.isRemoved(name) {
		return false
	}
	if _, ok := c.in.hostFunc(name); ok {
		return true
	}
	b, ok := LookupBuiltin(name)
	return ok && b.Available(&c.in.opts)
}

func (c *compiler) errorf(pos token.Pos, format string, a ...any) {
	if len(c.errs) >= maxCompileErrors {
		return
	}
	c.errs = append(c.errs, nameErrorf(format, a...).At(c.file, pos.Line, pos.Col))
}

func (c *compiler) syntaxErrorf(pos token.Pos, format string, a ...any) {
	if len(c.errs) >= maxCompileErrors {
		return
	}
	c.errs = append(c.errs, syntaxErrorf(format, a...).At(c.file, pos.Line, pos.Col))
}

// argErrorf is the compile pass reporting an argument list the callee cannot accept. It
// exists so a mistake the compiler can see keeps the kind the same mistake carries at run
// time: `filter(xs = 1)` and `filter([1], xs = 1)` are one complaint, and printing one of
// them as `name:` and the other as `argument:` would make them look like two (§17).
func (c *compiler) argErrorf(pos token.Pos, format string, a ...any) {
	if len(c.errs) >= maxCompileErrors {
		return
	}
	c.errs = append(c.errs, argErrorf(format, a...).At(c.file, pos.Line, pos.Col))
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (c *compiler) stmts(list []ast.Stmt) {
	for i, s := range list {
		list[i] = c.stmt(s)
		// §17: a closure literal whose value nothing receives. The last statement of a
		// block is its value (§8.1), so only an earlier one is genuinely thrown away —
		// which is what `xs.each` written on the line above its `{ … }` looks like.
		if i < len(list)-1 {
			if x, ok := list[i].(*ast.ExprStmt); ok {
				switch fn := x.X.(type) {
				case *ast.FuncLit:
					c.warnAt(fn.Start, "closure literal in statement position: its value is discarded")
				case *ast.FnDecl:
					// A named `fn` is a declaration and never reaches here; an anonymous
					// one is a value, and a value nobody receives is the same mistake.
					if fn.Name == "" {
						c.warnAt(fn.Kw, "anonymous 'fn' in statement position: its value is discarded")
					}
				}
			}
		}
	}
}

func (c *compiler) stmt(s ast.Stmt) ast.Stmt {
	switch n := s.(type) {
	case *ast.ExprStmt:
		n.X = c.expr(n.X)
		return n
	case *ast.ReturnStmt:
		n.X = c.expr(n.X)
		return n
	case *ast.BreakStmt:
		n.X = c.expr(n.X)
		return n
	case *ast.NextStmt:
		n.X = c.expr(n.X)
		return n
	case *ast.IncludeDecl:
		c.include(n)
		return n
	case *ast.ExportDecl:
		c.export(n)
		return n
	case *ast.FnDecl:
		if !c.known(n.Name) {
			c.declare(n.Name)
		}
		c.funcScope(n.Params, n.Body, false)
		return n
	case *ast.BlockStmt:
		c.block(n)
		return n
	case ast.Expr:
		x := c.expr(n)
		if rs, ok := x.(ast.Stmt); ok {
			return rs
		}
		return &ast.ExprStmt{X: x}
	}
	return s
}

// block walks a body block in a scope of its own and records the frame it needs.
func (c *compiler) block(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	c.push()
	c.stmts(b.Stmts)
	b.FrameSize = c.scope.size
	c.pop()
}

// funcScope walks a function or closure body with the parameters bound in the body's
// own scope, because the braces the parameters belong to are the body's braces. warnUnused
// is set for a closure literal, whose unused parameter is one of §17's warnings — a named
// `fn` is an interface and may legitimately ignore an argument.
func (c *compiler) funcScope(params []ast.Param, body *ast.BlockStmt, warnUnused bool) {
	c.push()
	for i := range params {
		if params[i].Default != nil {
			params[i].Default = c.expr(params[i].Default)
		}
		c.declare(params[i].Name)
	}
	if body != nil {
		c.stmts(body.Stmts)
		body.FrameSize = c.scope.size
	}
	if warnUnused {
		for _, p := range params {
			if p.Name != "" && p.Name != itName && p.Name != blankName && !c.scope.used[p.Name] {
				c.warnAt(p.NamePos, "closure parameter '%s' is never used", p.Name)
			}
		}
	}
	c.pop()
}

// itName is the parameter a bare `{ … }` gets (§8.9). It is synthesised rather than
// written, so `{ 1 }` must not be warned about for ignoring it.
const itName = "it"

// blankName is the conventional name of a parameter you do not use (§3.4, §4.1). It is
// an ordinary identifier in every other way; it only exempts `{ (_, v) -> v }` from the
// unused-parameter warning, which is the spelling the spec itself recommends.
const blankName = "_"

// warnIfAssignCond is §17's fourth warning: an assignment that is the whole condition of
// an `if` is almost always a typed `==`. Only a plain `=` qualifies — `:=` is a
// deliberate declaration and a compound assign cannot be confused with a comparison.
func (c *compiler) warnIfAssignCond(cond ast.Expr) {
	a, ok := cond.(*ast.AssignExpr)
	if !ok || a.Op != token.ASSIGN {
		return
	}
	c.warnAt(a.OpPos, "'=' assigns; did you mean '==' ?")
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

func (c *compiler) expr(x ast.Expr) ast.Expr {
	switch n := x.(type) {
	case nil:
		return nil
	case *ast.RegexLit:
		c.compileRegex(n)
		return n
	case *ast.StrLit:
		for i := range n.Parts {
			if n.Parts[i].Expr != nil {
				n.Parts[i].Expr = c.expr(n.Parts[i].Expr)
			}
		}
		return n
	case *ast.ArrayLit:
		for i := range n.Elems {
			n.Elems[i] = c.expr(n.Elems[i])
		}
		return n
	case *ast.DictLit:
		c.dict(n)
		return n
	case *ast.Ident:
		c.ident(n)
		return n
	case *ast.UnaryExpr:
		n.X = c.expr(n.X)
		return foldUnary(n)
	case *ast.BinaryExpr:
		n.L, n.R = c.expr(n.L), c.expr(n.R)
		c.checkRegexEquality(n)
		return foldBinary(n)
	case *ast.LogicalExpr:
		n.L, n.R = c.expr(n.L), c.expr(n.R)
		return n
	case *ast.TernaryExpr:
		n.Cond, n.Then, n.Else = c.expr(n.Cond), c.expr(n.Then), c.expr(n.Else)
		return n
	case *ast.RangeExpr:
		n.Lo, n.Hi = c.expr(n.Lo), c.expr(n.Hi)
		return n
	case *ast.AssignExpr:
		c.assign(n)
		return n
	case *ast.DestructureAssign:
		n.Value = c.expr(n.Value)
		c.targets(n.Pattern, n.Op)
		return n
	case *ast.IndexExpr:
		n.X, n.Index, n.Index2 = c.expr(n.X), c.expr(n.Index), c.expr(n.Index2)
		return n
	case *ast.CallExpr:
		return c.call(n)
	case *ast.MethodCall:
		return c.methodCall(n)
	case *ast.FuncLit:
		c.funcScope(n.Params, n.Body, !n.ImplicitIt)
		return n
	case *ast.FnDecl:
		if !c.known(n.Name) {
			c.declare(n.Name)
		}
		c.funcScope(n.Params, n.Body, false)
		return n
	case *ast.GroupExpr:
		// Parentheses group statements without opening a scope: only a brace does that.
		c.stmts(n.Stmts)
		return n
	case *ast.IfExpr:
		c.warnIfAssignCond(n.Cond)
		n.Cond = c.expr(n.Cond)
		c.block(n.Then)
		if n.Else != nil {
			n.Else = c.stmt(n.Else)
		}
		return n
	case *ast.WhileExpr:
		n.Cond = c.expr(n.Cond)
		c.block(n.Body)
		return n
	case *ast.ForExpr:
		n.Iter = c.expr(n.Iter)
		c.push()
		c.declare(n.KeyVar)
		c.declare(n.ValVar)
		if n.Body != nil {
			c.stmts(n.Body.Stmts)
			n.Body.FrameSize = c.scope.size
		}
		c.pop()
		return n
	case *ast.MatchExpr:
		n.Subject = c.expr(n.Subject)
		for i := range n.Arms {
			arm := &n.Arms[i]
			if arm.Kind == ast.ArmArray {
				c.armPattern(arm)
				continue
			}
			for j := range arm.Pats {
				arm.Pats[j] = c.expr(arm.Pats[j])
			}
			arm.Guard = c.expr(arm.Guard)
			c.block(arm.Body)
		}
		return n
	case *ast.TryExpr:
		n.X = c.expr(n.X)
		if n.Var == "" {
			n.Fallback = c.expr(n.Fallback)
			return n
		}
		// `(e) -> …` binds the error dict for the fallback only.
		c.push()
		c.declare(n.Var)
		n.Fallback = c.expr(n.Fallback)
		c.pop()
		return n
	}
	return x
}

func (c *compiler) dict(n *ast.DictLit) {
	for i := range n.Keys {
		n.Keys[i] = c.expr(n.Keys[i])
		n.Vals[i] = c.expr(n.Vals[i])
	}
}

// compileRegex is step 1: a bad pattern is a compile error rather than a surprise at
// eval time, and the §11.5 `\\b` lint runs here too.
func (c *compiler) compileRegex(n *ast.RegexLit) {
	for _, msg := range rx.LintPattern(n.Pattern) {
		c.warns = append(c.warns, Warning{Msg: msg, Line: n.Start.Line, Col: n.Start.Col})
	}
	r, err := c.in.compileRegex(n.Pattern, n.Flags)
	if err != nil {
		if len(c.errs) < maxCompileErrors {
			c.errs = append(c.errs, asError(err).At(c.file, n.Start.Line, n.Start.Col))
		}
		return
	}
	n.Compiled = r
}

// checkRegexEquality is step 6 (D5): `==`/`!=` against a regex literal is the one type
// error that is catchable statically, and the likeliest paste mistake.
func (c *compiler) checkRegexEquality(n *ast.BinaryExpr) {
	if n.Op != token.EQ && n.Op != token.NEQ {
		return
	}
	if !isRegexLit(n.L) && !isRegexLit(n.R) {
		return
	}
	c.syntaxErrorf(n.OpPos, "'%s' with a regex operand: use '~' to match", n.Op)
}

func isRegexLit(x ast.Expr) bool { _, ok := x.(*ast.RegexLit); return ok }

// ident is step 4: every name resolves to a local, a parameter, a declared `fn`, an
// included module, a host function or a builtin, and anything left over is an error —
// there is no bareword-to-string shim (§9.3) and no ambient module (§12.8).
func (c *compiler) ident(n *ast.Ident) {
	if n == nil {
		return
	}
	n.Slot = -1
	switch {
	case c.known(n.Name):
		// An included name is an ordinary binding — the include statement assigns it —
		// but the receiver of a `.` must read members rather than dispatch a method.
		if c.knownModule(n.Name) {
			n.Ref = ast.RefModule
		} else {
			n.Ref = ast.RefLocal
		}
		c.markUsed(n.Name)
	case c.hasFunc(n.Name):
		n.Ref = ast.RefFunc
	default:
		c.errorf(n.Start, "%s", undefinedNameMessage(c.in, n.Name, c.visible()))
	}
}

// include is the compile-time half of `include` (§12.8). The name it declares is what
// makes a module reachable at all, and the checks here are the ones that can be made
// before anything runs: a built-in module must exist and be enabled, and a path needs a
// host loader. What the module *contains* is settled at run time, exactly as a dict's
// keys always were.
func (c *compiler) include(n *ast.IncludeDecl) {
	if n.Name == "" {
		return
	}
	switch {
	case n.HasPath:
		if c.in.opts.ModuleLoader == nil {
			c.errorf(n.Kw, "cannot include %q: the host did not enable module loading", n.Path)
		}
	default:
		if _, ok := lookupIncludable(c.in, n.Name); !ok {
			c.errorf(n.NamePos, "%s", unknownModuleMessage(c.in, n.Name))
		}
	}
	if c.known(n.Name) {
		c.warnAt(n.NamePos, "'%s' is already bound; the include replaces it", n.Name)
	}
	c.declareModule(n.Name)
}

// export compiles the declaration an `export` wraps and checks the bare form names a
// binding that exists. Exports are only read when the file is included (§12.8); running
// it directly ignores them, so the same script is both a program and a module.
func (c *compiler) export(n *ast.ExportDecl) {
	if n.Decl != nil {
		n.Decl = c.stmt(n.Decl)
		return
	}
	for _, name := range n.Names {
		if !c.known(name) {
			c.errorf(n.Kw, "cannot export '%s': it is not defined", name)
			continue
		}
		c.markUsed(name)
	}
}

// assign resolves an assignment target. `=`, `:=`, `||=` and `??=` may create the
// binding; every other compound operator reads the current value first and therefore
// needs one to exist (§8.5).
func (c *compiler) assign(n *ast.AssignExpr) {
	n.Value = c.expr(n.Value)
	switch t := n.Target.(type) {
	case *ast.Ident:
		t.Slot = -1
		t.Ref = ast.RefLocal
		switch n.Op {
		case token.DECLARE:
			c.declare(t.Name)
		case token.ASSIGN, token.OR_EQ, token.NIL_EQ:
			if !c.known(t.Name) {
				c.declare(t.Name)
			}
		default:
			// Every other compound operator reads the target before writing it, so it
			// both requires a binding and counts as a use of one.
			if !c.known(t.Name) {
				c.errorf(t.Start, "undefined variable '%s'", t.Name)
			}
			c.markUsed(t.Name)
		}
	case *ast.IndexExpr:
		n.Target = c.expr(t)
	case *ast.GlobalVar:
		// A $var needs no declaration: writing one inserts into the globals table (§10).
	default:
		c.syntaxErrorf(n.OpPos, "cannot assign to this expression")
	}
}

// targets resolves the left side of a destructuring assignment: the rule a single `=`
// follows (§8.2), applied once per position (§8.15).
func (c *compiler) targets(pat *ast.ArrayPattern, op token.Kind) {
	if pat == nil {
		return
	}
	for i, el := range pat.Elems {
		switch t := el.(type) {
		case *ast.ArrayPattern:
			c.targets(t, op)
		case *ast.Ident:
			t.Slot, t.Ref = -1, ast.RefLocal
			if op == token.DECLARE || !c.known(t.Name) {
				c.declare(t.Name)
			}
		case *ast.IndexExpr:
			pat.Elems[i] = c.expr(t)
		case *ast.GlobalVar:
			// A $var needs no declaration: writing one inserts into the globals table (§10).
		}
	}
}

// armPattern compiles an arm whose pattern binds (§5.3). The names go into a scope of
// their own — the arm body's — so the guard and the body both see them and nothing after
// the arm does, which is exactly how a `for` treats its loop variables.
func (c *compiler) armPattern(arm *ast.MatchArm) {
	c.push()
	for _, pat := range arm.Pats {
		if p, ok := pat.(*ast.ArrayPattern); ok {
			c.bindings(p)
		}
	}
	arm.Guard = c.expr(arm.Guard)
	if arm.Body != nil {
		c.stmts(arm.Body.Stmts)
		arm.Body.FrameSize = c.scope.size
	}
	c.pop()
}

// bindings declares the names an array pattern binds and compiles the elements that are
// comparisons rather than bindings.
func (c *compiler) bindings(pat *ast.ArrayPattern) {
	for i, el := range pat.Elems {
		switch t := el.(type) {
		case *ast.ArrayPattern:
			c.bindings(t)
		case *ast.Ident:
			t.Slot, t.Ref = -1, ast.RefLocal
			c.declare(t.Name)
		default:
			pat.Elems[i] = c.expr(el)
		}
	}
}

// call resolves `f(…)`. A bare name here must be callable; a module is not.
func (c *compiler) call(n *ast.CallExpr) ast.Expr {
	id, isName := n.Fn.(*ast.Ident)
	if isName && id.Name == definedName && !c.known(id.Name) {
		// `defined(x)` takes a name, not a value, so its argument is not resolved.
		if len(n.Args) == 1 {
			switch n.Args[0].(type) {
			case *ast.Ident, *ast.GlobalVar:
				return n
			}
		}
	}
	for i := range n.Args {
		n.Args[i] = c.expr(n.Args[i])
	}
	for i := range n.Named {
		n.Named[i].Value = c.expr(n.Named[i].Value)
	}
	if !isName {
		n.Fn = c.expr(n.Fn)
		return n
	}
	id.Slot = -1
	switch {
	case c.knownModule(id.Name):
		// A module is a dict of names; calling it means nothing. `include json` used to
		// leave `json(x)` the §12.1 function — one name with two roles, decided by the
		// call position — and that is exactly the ambiguity §12.8 refuses everywhere
		// else, so it is now the error it always looked like.
		c.markUsed(id.Name)
		c.errorf(id.Start, "%s", moduleNotCallableMessage(c.in, id.Name, c.hasFunc(id.Name)))
	case c.known(id.Name):
		id.Ref = ast.RefLocal
		c.markUsed(id.Name)
	case c.hasFunc(id.Name):
		id.Ref = ast.RefFunc
	case len(n.Args) == 0 && len(n.Named) > 0 && c.hasMethod(id.Name):
		// A stdlib row is reached through the first argument's kind, so a call that
		// gives only names has no receiver to dispatch on and can never resolve. Left
		// to the run time it comes out as `undefined function 'filter'` — about a name
		// that plainly exists — so the one true thing is said here instead (§5.6).
		c.argErrorf(n.Named[0].NamePos,
			"%s takes its arguments by position, so '%s = …' has no parameter to bind",
			id.Name, n.Named[0].Name)
	case len(n.Args) > 0 && c.hasMethod(id.Name):
		// One namespace, not two (§12): `filter(xs, f)` is `xs.filter(f)`, so a name
		// that is only a stdlib row of some kind is callable in prefix position too.
		// Which kind it belongs to depends on the first argument's value, so the
		// resolution itself stays at run time, in callFunction.
		id.Ref = ast.RefBuiltin
	default:
		if len(c.errs) < maxCompileErrors {
			c.errs = append(c.errs, undefinedFunctionError(id.Name, c.visible()).
				At(c.file, id.Start.Line, id.Start.Col))
		}
	}
	return n
}

// methodCall is step 2, UFCS resolution. A name the receiver's kind cannot possibly
// answer is a call of the function of that name with the receiver as its first
// argument (D18); a name that is neither a method of any kind nor a visible function is
// `undefined method`.
func (c *compiler) methodCall(n *ast.MethodCall) ast.Expr {
	module := false
	if id, ok := n.Recv.(*ast.Ident); ok {
		c.ident(id)
		module = id.Ref == ast.RefModule
		// `json.parse(s)` without `include json` used to work by ambience and now must
		// not. The name still resolves — `json` is also the §12.1 function — so say the
		// one useful thing instead of letting it fail as `undefined method` (§12.8).
		if !module && id.Ref == ast.RefFunc && c.missingInclude(id.Name) {
			c.errorf(id.Start, "'%s' is a module: add `include %s` at the top of the file", id.Name, id.Name)
			// Treat it as the module it is meant to be: one diagnostic is the fix, and
			// `undefined method 'parse'` underneath it is only noise (§17).
			module = true
		}
	} else {
		n.Recv = c.expr(n.Recv)
	}
	for i := range n.Args {
		n.Args[i] = c.expr(n.Args[i])
	}
	for i := range n.Named {
		n.Named[i].Value = c.expr(n.Named[i].Value)
	}
	if module || c.hasMethod(n.Name) {
		// A module member and a real method are both resolved by the receiver's value,
		// so the dispatch stays where it belongs, at run time.
		return n
	}
	if (c.known(n.Name) && !c.knownModule(n.Name)) || c.hasFunc(n.Name) {
		if n.Safe {
			// `?.` must not evaluate anything when the receiver is nil (§4.3), which a
			// plain call cannot express; the evaluator's UFCS fallback handles it.
			return n
		}
		fn := &ast.Ident{Name: n.Name, Slot: -1, Start: n.NamePos, Stop: n.NamePos}
		fn.Ref = ast.RefFunc
		if c.known(n.Name) && !c.knownModule(n.Name) {
			// RefFunc where the name is also a module: the step over the binding is the
			// point, since `x.json` means the function even where `json` is included.
			fn.Ref = ast.RefLocal
			c.markUsed(n.Name)
		}
		args := make([]ast.Expr, 0, len(n.Args)+1)
		args = append(append(args, n.Recv), n.Args...)
		return &ast.CallExpr{Fn: fn, Args: args, Named: n.Named, Lparen: n.NamePos, Stop: n.Stop}
	}
	c.undefinedMethod(n.Name, n.NamePos)
	return n
}

// undefinedMethod produces §5.6's message shape, with a suggestion drawn from every
// method table and every function name — the receiver's kind is not known statically,
// so the whole namespace is the candidate pool.
func (c *compiler) undefinedMethod(name string, pos token.Pos) {
	if c.pool == nil {
		for _, k := range dispatchKinds {
			c.pool = append(c.pool, MethodNames(k)...)
		}
		c.pool = append(c.pool, BuiltinNames()...)
		c.pool = append(c.pool, c.visible()...)
	}
	if s := suggest(name, c.pool); s != "" {
		c.errorf(pos, "undefined method '%s'; did you mean '%s'?", name, s)
		return
	}
	c.errorf(pos, "undefined method '%s'", name)
}

// dispatchKinds is every kind a method may be registered under, plus the universal
// table. It is what makes "is this name a method of anything?" a decidable question at
// compile time.
var dispatchKinds = [...]Kind{
	KNil, KBool, KInt, KFloat, KString, KRegex, KArray, KDict, KFunc, KTime, KRange, KTask, KAny,
}

// hasMethod is methodExists with this interpreter's Unregister list applied, so a name
// the host has taken away is reported at compile time in both of its spellings rather
// than resolving to a stdlib row the script is no longer allowed to reach.
func (c *compiler) hasMethod(name string) bool {
	return !c.in.isRemoved(name) && methodExists(name)
}

func methodExists(name string) bool {
	for _, k := range dispatchKinds {
		if _, ok := LookupMethod(k, name); ok {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Constant folding (§6.3 step 5)
// ---------------------------------------------------------------------------

// foldBinary folds literal arithmetic and literal string concatenation. Division and
// modulo are deliberately left alone: their error cases belong to the evaluator, and
// nobody writes `7 / 0` in a condition.
func foldBinary(n *ast.BinaryExpr) ast.Expr {
	switch n.Op {
	case token.PLUS, token.MINUS, token.STAR:
	default:
		return n
	}
	if a, ok := numLit(n.L); ok {
		if b, ok := numLit(n.R); ok {
			switch n.Op {
			case token.PLUS:
				return litOf(addNum(a, b), n.Pos(), n.End())
			case token.MINUS:
				return litOf(subNum(a, b), n.Pos(), n.End())
			}
			return litOf(mulNum(a, b), n.Pos(), n.End())
		}
	}
	if n.Op != token.PLUS {
		return n
	}
	l, lok := n.L.(*ast.StrLit)
	r, rok := n.R.(*ast.StrLit)
	if lok && rok && l.IsConst() && r.IsConst() {
		return litOf(Str(l.ConstText()+r.ConstText()), n.Pos(), n.End())
	}
	return n
}

// foldUnary folds `-1` and `+1` into a literal, so a negative constant costs no node.
func foldUnary(n *ast.UnaryExpr) ast.Expr {
	v, ok := numLit(n.X)
	if !ok {
		return n
	}
	switch n.Op {
	case token.PLUS:
		return litOf(v, n.Pos(), n.End())
	case token.MINUS:
		if v.Kind() == KInt {
			if v.Int() == math.MinInt64 { // negating it promotes to Float; leave that to the evaluator
				return n
			}
			return litOf(Int(-v.Int()), n.Pos(), n.End())
		}
		return litOf(Float(-v.Float()), n.Pos(), n.End())
	}
	return n
}

func numLit(x ast.Expr) (Value, bool) {
	switch n := x.(type) {
	case *ast.IntLit:
		return Int(n.Value), true
	case *ast.FloatLit:
		return Float(n.Value), true
	}
	return Nil(), false
}

func litOf(v Value, start, stop token.Pos) ast.Expr {
	switch v.Kind() {
	case KInt:
		return &ast.IntLit{Value: v.Int(), Start: start, Stop: stop}
	case KFloat:
		return &ast.FloatLit{Value: v.Float(), Start: start, Stop: stop}
	default:
		return &ast.StrLit{Parts: []ast.StrPart{{Text: v.Str()}}, Start: start, Stop: stop}
	}
}

// ---------------------------------------------------------------------------
// Running
// ---------------------------------------------------------------------------

// Eval compiles through the program cache and runs in one call — the shape a hot
// dialogue uses, where the same condition text recurs thousands of times.
func (in *Interp) Eval(ctx context.Context, src string, vars map[string]Value) (Value, error) {
	p, err := in.Compile("eval", src)
	if err != nil {
		return Nil(), err
	}
	return in.Run(ctx, p, vars)
}

// Run executes a compiled program against a fresh globals table seeded from vars. Two
// concurrent Runs of the same *Program never see each other's $vars (§10).
func (in *Interp) Run(ctx context.Context, p *Program, vars map[string]Value) (Value, error) {
	res, err := in.RunResult(ctx, p, vars)
	return res.Value, err
}

// RunResult additionally reports the mutated globals — how a set_var-style script hands
// values back — plus the steps taken and the wall time.
func (in *Interp) RunResult(ctx context.Context, p *Program, vars map[string]Value) (res Result, err error) {
	in.ensureState()
	in.started.Store(true)
	if p == nil || p.tree() == nil {
		return Result{Value: Nil()}, newError(ErrKindInternal, "mzs: nil program")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	globals := make(map[string]Value, len(in.globals)+len(vars))
	for k, v := range in.globals {
		globals[k] = v
	}
	for k, v := range vars {
		globals[globalName(k)] = v
	}

	// The Run's own context: cancelling it is how the tasks of §8.14 are stopped when
	// the program is over, whatever the host's context is doing.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rs := newRunState(in, ctx, p.name, globals)
	rs.sh.cancel = cancel
	start := time.Now()
	res = Result{Value: Nil(), Globals: globals}

	// The main program holds the Run's lock from here, and gives it up only where a task
	// may take it over (§8.14).
	rs.lock()

	// A7: a panic anywhere below — a bug in the evaluator or in a host function — is
	// converted here rather than reaching the embedder. Tasks are joined on this path
	// too: a Run never returns with a goroutine of its own still running, so the host
	// reads Result.Globals with nothing left to write to it (A5).
	defer func() {
		r := recover()
		rs.joinTasks()
		rs.unlock()
		res.Steps, res.Elapsed = rs.sh.steps, time.Since(start)
		if r != nil {
			res.Value = Nil()
			err = internalError(p.name, r, in.opts.StrictWarnings)
		}
	}()

	e := ev{rs: rs, cx: newCtx(rs), env: newEnv(nil, p.frameSize)}
	v, rerr := e.runProgram(p.tree())
	res.Value = v
	if rerr != nil {
		res.Value = Nil()
		return res, e.at(rerr, p.tree().Start)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Error conversion
// ---------------------------------------------------------------------------

// syntaxErrors turns the parser's diagnostics into *Error values of kind "syntax",
// preserving the errors.Join shape when recovery found several (§17).
func syntaxErrors(name string, err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		out := make([]error, 0, len(parts))
		for _, p := range parts {
			out = append(out, syntaxErrors(name, p))
		}
		if len(out) == 1 {
			return out[0]
		}
		if len(out) > 0 {
			return errors.Join(out...)
		}
	}
	var pe *parser.Error
	if errors.As(err, &pe) {
		file := pe.File
		if file == "" {
			file = name
		}
		return (&Error{Kind: ErrKindSyntax, Msg: pe.Msg}).At(file, pe.Pos.Line, pe.Pos.Col)
	}
	var me *Error
	if errors.As(err, &me) {
		return me
	}
	return &Error{Kind: ErrKindSyntax, Msg: err.Error(), File: name, wrapped: err}
}

// internalError converts a recovered panic. The Go stack is attached only under
// StrictWarnings, so a production embedding never leaks implementation detail into a
// message a bot might echo back to a user (§13.5).
func internalError(file string, r any, withStack bool) *Error {
	msg := fmt.Sprintf("internal error: %v", r)
	if withStack {
		msg += "\n" + string(debug.Stack())
	}
	e := &Error{Kind: ErrKindInternal, Msg: msg, File: file}
	if err, ok := r.(error); ok {
		e.wrapped = err
	}
	return e
}
