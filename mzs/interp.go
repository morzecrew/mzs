package mzs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sort"
	"strings"
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
	c.collectFields(file)
	if in.opts.CompileIncludes {
		c.collectImports(file)
	}
	c.collectBound(file)
	c.push()
	// Top-level `fn`s and `record`s are hoisted, so a call may appear above its
	// declaration (§8.2, §7.8).
	for _, s := range file.Stmts {
		switch d := s.(type) {
		case *ast.FnDecl:
			if d.Name != "" {
				c.declare(d.Name)
			}
		case *ast.RecordDecl:
			if d.Name != "" {
				c.declareRecord(d)
			}
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
	// recs maps the names a `record` declaration bound in this scope to it, which is what
	// lets `Money {amount} ->` be told from a call with a trailing closure (RFC 0002).
	// A later binding of the same name in an inner scope shadows it, as for any name.
	recs map[string]*ast.RecordDecl
	size int
}

type compiler struct {
	in    *Interp
	file  string
	scope *cscope
	warns []Warning
	errs  []error
	pool  []string // method and function names, built only to make a suggestion

	// fields is every name any `record` in this unit declares (§7.8). A receiver's shape
	// is no more known statically than its kind is, so `m.amount` survives step 2 the way
	// a real method name does and the dispatch happens where the value is. It is
	// collected before the walk, because a field may be read above the declaration that
	// names it — inside a function, or simply further up the file.
	fields map[string]bool
	// imports is what CompileIncludes read (RFC 0003): for each top-level include of a
	// path, the records the module exports and their fields. importedFields is every such
	// field, with the record that first declares it for the diagnostics, and importFails
	// says which includes could not be read, so a `.field` nothing resolves can name why.
	imports        map[string]map[string][]string
	importedFields map[string]string
	importFails    []string
	// bound is every name the unit binds anywhere: an assignment, a `fn`, a parameter, a
	// loop or `try` variable. It is what lets a method call compiled before the binding
	// still try it at run time (§8.7), the way a prefix call reaches it by name.
	bound map[string]bool
}

// collectFields gathers the record field names of the whole unit. Scope does not come
// into it: the set answers "could any receiver answer this name", which is the same
// question methodAnyKind answers for the stdlib (§6.3 step 2).
func (c *compiler) collectFields(file *ast.Program) {
	ast.Walk(file, func(n ast.Node) bool {
		d, ok := n.(*ast.RecordDecl)
		if !ok {
			return true
		}
		if c.fields == nil {
			c.fields = map[string]bool{}
		}
		for _, f := range d.Fields {
			c.fields[f.Name] = true
		}
		return true
	})
}

// collectBound fills c.bound. A function body is compiled before a top-level binding
// that follows its declaration, but it runs after, so "in scope now" is not the question
// a method call needs answered: `fn g(x) { x.upper }; upper = { … }; g("a")` reaches the
// closure as `upper(x)` would. The set over-approximates on purpose — a name bound
// anywhere is looked up at run time, and only a value that is a function there wins.
func (c *compiler) collectBound(file *ast.Program) {
	c.bound = map[string]bool{}
	params := func(ps []ast.Param) {
		for _, p := range ps {
			c.bound[p.Name] = true
		}
	}
	ast.Walk(file, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.Ident:
			c.bound[d.Name] = true
		case *ast.FnDecl:
			c.bound[d.Name] = true
			params(d.Params)
		case *ast.FuncLit:
			params(d.Params)
		case *ast.ForExpr:
			c.bound[d.KeyVar] = true
			c.bound[d.ValVar] = true
		case *ast.TryExpr:
			c.bound[d.Var] = true
		}
		return true
	})
}

// localCandidate reports whether a method call spelled name must try a binding of that
// name at run time before the stdlib table (§8.7): one is in scope, or one is made
// anywhere in the unit. A name an `include` introduced is the module, never a candidate.
func (c *compiler) localCandidate(name string) bool {
	if c.knownModule(name) {
		return false
	}
	return c.known(name) || c.bound[name]
}

// recordField reports whether name is a field of some record declared in this unit.
func (c *compiler) recordField(name string) bool { return c.fields[name] }

// importedField reports whether name is a field of a record a module this unit includes
// at the top level exports, as CompileIncludes read it (RFC 0003).
func (c *compiler) importedField(name string) bool { return c.importedFields[name] != "" }

// collectImports reads, through the host's loader, each `include … from` at the top level
// of the file and keeps the field names of the records the module exports (RFC 0003). An
// include inside a block is never read here — it may never run — and a read that fails is
// not an error: the include fails when it runs, where and how it always did, and the
// failure is kept only to explain a `.field` that nothing else resolves.
func (c *compiler) collectImports(file *ast.Program) {
	load := c.in.opts.ModuleLoader
	if load == nil {
		return
	}
	for _, s := range file.Stmts {
		n, ok := s.(*ast.IncludeDecl)
		if !ok || !n.HasPath || n.Name == "" {
			continue
		}
		recs, err := readExports(load, c.file, n.Path)
		if err != nil {
			c.importFails = append(c.importFails, fmt.Sprintf("module '%s' could not be read at compile time: %v", n.Name, err))
			continue
		}
		if c.imports == nil {
			c.imports, c.importedFields = map[string]map[string][]string{}, map[string]string{}
		}
		c.imports[n.Name] = recs
		names := make([]string, 0, len(recs))
		for r := range recs {
			names = append(names, r)
		}
		sort.Strings(names)
		for _, r := range names {
			for _, f := range recs[r] {
				if c.importedFields[f] == "" {
					c.importedFields[f] = n.Name + "." + r
				}
			}
		}
	}
}

// readExports parses a module's source and lists the records it exports with their
// fields: `export record Name(…)`, and a bare `export Name` that names a top-level record.
// Nothing is compiled or run, and nothing is kept but names.
func readExports(load ModuleLoader, from, path string) (map[string][]string, error) {
	resolved, src, err := load(from, path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		resolved = path
	}
	file, err := parser.Parse(resolved, src)
	if err != nil {
		return nil, fmt.Errorf("it does not parse")
	}
	decls := map[string]*ast.RecordDecl{}
	for _, s := range file.Stmts {
		switch d := s.(type) {
		case *ast.RecordDecl:
			decls[d.Name] = d
		case *ast.ExportDecl:
			if r, ok := d.Decl.(*ast.RecordDecl); ok {
				decls[r.Name] = r
			}
		}
	}
	recs := map[string][]string{}
	for _, s := range file.Stmts {
		d, ok := s.(*ast.ExportDecl)
		if !ok {
			continue
		}
		for _, name := range d.Names {
			if r := decls[name]; r != nil {
				fields := make([]string, len(r.Fields))
				for i, f := range r.Fields {
					fields[i] = f.Name
				}
				recs[name] = fields
			}
		}
	}
	return recs, nil
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
	// A new binding of the name is not the record any more, even in the record's own
	// scope: `M := …` after `record M(…)` makes `M {…}` the call it reads as (RFC 0002).
	delete(c.scope.recs, name)
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

// someRecord stands for "a record, but which one depends on the path taken": the name
// holds a record constructor, so `Name {…}` is a record pattern, but the fields are not
// known statically and the shape test at run time is the whole answer.
var someRecord = &ast.RecordDecl{}

// declareRecord marks the binding a `record` writes as that declaration. Like `=`, the
// declaration writes the nearest binding of its name when there is one (§8.2) — a top-level
// record was declared ahead, to hoist — and declares one here otherwise. A record that
// writes a binding of an enclosing scope does so only if its block runs, so that binding
// becomes someRecord: still a record, its fields no longer the compile pass's to check.
func (c *compiler) declareRecord(d *ast.RecordDecl) {
	name := d.Name
	s := c.scope
	for ; s != nil && !s.names[name]; s = s.parent {
	}
	if s == nil {
		c.declare(name)
		s = c.scope
	}
	if s.recs == nil {
		s.recs = map[string]*ast.RecordDecl{}
	}
	// Two declarations of one name in a scope are both hoisted, and which one the name
	// holds when a function body runs depends on when it is called — so that name is
	// someRecord too, as is one a block's record may rewrite.
	if prev := s.recs[name]; (prev != nil && prev != d) || (s != c.scope && prev != d) {
		d = someRecord
	}
	s.recs[name] = d
}

// patternRecord is the declaration a record shape names, or nil when it names none:
// `Money {…}` resolves Money in this unit (RFC 0002), `money.Money {…}` asks the exports
// CompileIncludes read for the module the qualifier still names (RFC 0003). An imported
// record comes back as a declaration built from its field names, which is all a pattern
// can be checked against.
func (c *compiler) patternRecord(dp *ast.DictPattern) *ast.RecordDecl {
	if dp.Module == nil {
		return c.recordDecl(dp.Record.Name)
	}
	if !c.knownModule(dp.Module.Name) {
		return nil
	}
	fields, ok := c.imports[dp.Module.Name][dp.Record.Name]
	if !ok {
		return nil
	}
	d := &ast.RecordDecl{Name: dp.RecordName(), Fields: make([]ast.Param, len(fields))}
	for i, f := range fields {
		d.Fields[i] = ast.Param{Name: f}
	}
	return d
}

// recordDecl is the `record` name resolves to, or nil when the nearest binding of the
// name is anything else — a variable that shadows a record is not one (RFC 0002).
func (c *compiler) recordDecl(name string) *ast.RecordDecl {
	for s := c.scope; s != nil; s = s.parent {
		if s.names[name] {
			return s.recs[name]
		}
	}
	return nil
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
	case *ast.RecordDecl:
		c.record(n)
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

// record compiles a `record` declaration (§7.8). The name becomes an ordinary binding —
// the constructor is a function value like any other — and the field defaults are
// compiled in a scope holding the fields, exactly as a `fn`'s are, so a later default may
// read an earlier field.
//
// The *RecordType is built here rather than at run time so that identity belongs to the
// declaration: a `match Money ->` arm asks which shape a value has, and two Runs of one
// *Program must agree on the answer.
func (c *compiler) record(n *ast.RecordDecl) {
	if n.Name == "" {
		return
	}
	if !c.known(n.Name) {
		c.declare(n.Name)
	}
	c.declareRecord(n)
	c.funcScope(n.Fields, nil, false)
	n.Type = newRecordType(n)
	for _, f := range n.Fields {
		if methodExists(f.Name) {
			// §17: the field wins on this shape, which is what the shape is for — but
			// `m.len` then reads the field and not the count, and that is worth saying
			// once, here, rather than leaving it to be discovered.
			c.warnAt(f.NamePos,
				"record field '%s' shadows the method of that name: on a %s, '.%s' reads the field — write %s(m) for the method",
				f.Name, n.Name, f.Name, f.Name)
		}
	}
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
	case *ast.BlockStmt:
		// A braced `try` clause (§8.11): a block in expression position, and a scope
		// like every other brace.
		c.block(n)
		return n
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
	case *ast.DictPattern:
		// Every place a dict pattern may stand converted it already, so one reached as a
		// value is a `{ … }` the parser read in pattern position and something then used
		// as an operand: `{a} + x -> …` (RFC 0002).
		c.syntaxErrorf(n.Lbrace, "a dict pattern stands alone: on the left of '=', as a match arm, as a for variable, or as an element of another pattern")
		return n
	case *ast.Splat:
		// Reached as an element of a literal or an argument list. The anonymous `*` of
		// a match pattern never gets here — the arm turned it into a pattern — so here
		// it has nothing to spread (RFC 0001).
		if n.X == nil {
			c.syntaxErrorf(n.Star, "a bare '*' skips positions only in a match pattern; here it needs a value: *xs")
			return n
		}
		n.X = c.expr(n.X)
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
		// A pattern in the header binds fresh names for each item, which is `:=` (RFC
		// 0002): the loop scope is new, so nothing outside it is written.
		c.targets(n.KeyPat, token.DECLARE)
		c.targets(n.ValPat, token.DECLARE)
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
			c.recordArm(arm, n.Subject != nil)
			if arm.Kind == ast.ArmArray || arm.Kind == ast.ArmDict {
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
		} else {
			// `(e) -> …` binds the error dict for the fallback only, in a scope of its
			// own so that a braced fallback still opens its own on top of it.
			c.push()
			c.declare(n.Var)
			n.Fallback = c.expr(n.Fallback)
			c.pop()
		}
		c.block(n.Ensure)
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
	case *ast.FieldExpr:
		c.fieldTarget(t)
	case *ast.GlobalVar:
		// A $var needs no declaration: writing one inserts into the globals table (§10).
	default:
		c.syntaxErrorf(n.OpPos, "cannot assign to this expression")
	}
}

// fieldTarget resolves `m.amount = …` (RFC 0003) by the rule that governs the read
// (§7.8): the name must be a field of some record this unit declares. A write has no
// UFCS reading to fall back on — `x.len = 3` cannot mean a call — so any other name is
// the diagnostic, and the spelling that writes a plain dict's entry is the fix.
func (c *compiler) fieldTarget(t *ast.FieldExpr) {
	t.X = c.expr(t.X)
	if c.recordField(t.Name) || c.importedField(t.Name) {
		return
	}
	fields := make([]string, 0, len(c.fields)+len(c.importedFields))
	for f := range c.fields {
		fields = append(fields, f)
	}
	for f := range c.importedFields {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	if s := suggest(t.Name, fields); s != "" {
		c.syntaxErrorf(t.NamePos, "cannot assign to '.%s': not a record field; did you mean '.%s'?", t.Name, s)
		return
	}
	c.syntaxErrorf(t.NamePos, "cannot assign to '.%s': not a record field (a dict entry is written d[\"%s\"] = …)", t.Name, t.Name)
}

// targets resolves the left side of a destructuring assignment: the rule a single `=`
// follows (§8.2), applied once per position (§8.15).
func (c *compiler) targets(x ast.Expr, op token.Kind) {
	switch pat := x.(type) {
	case *ast.ArrayPattern:
		c.arrayTargets(pat, op)
	case *ast.DictPattern:
		for i := range pat.Entries {
			en := &pat.Entries[i]
			switch t := en.Value.(type) {
			case *ast.Ident:
				c.target(t, op)
			case *ast.IndexExpr:
				en.Value = c.expr(t)
			case *ast.FieldExpr:
				c.fieldTarget(t)
			case *ast.ArrayPattern, *ast.DictPattern:
				c.targets(t, op)
			}
		}
		if pat.Rest != nil {
			c.target(pat.Rest, op)
		}
	}
}

func (c *compiler) arrayTargets(pat *ast.ArrayPattern, op token.Kind) {
	for i, el := range pat.Elems {
		switch t := el.(type) {
		case *ast.ArrayPattern, *ast.DictPattern:
			c.targets(t, op)
		case *ast.Ident:
			c.target(t, op)
		case *ast.Splat:
			if id, ok := t.X.(*ast.Ident); ok {
				c.target(id, op)
			}
		case *ast.IndexExpr:
			pat.Elems[i] = c.expr(t)
		case *ast.FieldExpr:
			c.fieldTarget(t)
		case *ast.GlobalVar:
			// A $var needs no declaration: writing one inserts into the globals table (§10).
		}
	}
}

// target resolves one name a destructuring writes: `:=` declares it, and `=` declares it
// only where no enclosing scope already has it, exactly as a single `=` does (§8.2).
func (c *compiler) target(t *ast.Ident, op token.Kind) {
	t.Slot, t.Ref = -1, ast.RefLocal
	if op == token.DECLARE || !c.known(t.Name) {
		c.declare(t.Name)
	}
}

// recordArm settles each `Name {…}` the parser left in a value arm (RFC 0002). When Name
// is a record the arm is a record shape — a dict pattern that also asks for the shape,
// alone in its arm and with a subject, as a dict pattern is. When it is not, the pattern
// gives way to the call it also reads as, `choose {it}`, and the arm means what it meant.
func (c *compiler) recordArm(arm *ast.MatchArm, hasSubject bool) {
	if arm.Kind != ast.ArmValue {
		return
	}
	for j, pat := range arm.Pats {
		dp, ok := pat.(*ast.DictPattern)
		if !ok || dp.Record == nil {
			continue
		}
		if c.patternRecord(dp) == nil {
			arm.Pats[j] = c.notARecord(dp)
			continue
		}
		switch {
		case len(arm.Pats) > 1:
			c.syntaxErrorf(dp.Pos(), "a record pattern must be the only pattern in its arm")
		case !hasSubject:
			c.syntaxErrorf(dp.Pos(), "a record pattern needs a subject: match m { %s {…} -> … }", dp.RecordName())
		default:
			arm.Kind = ast.ArmDict
		}
	}
}

// notARecord is what `Name {…}` becomes when Name is not a record: the call with a
// trailing closure it also reads as, or — for a shape no closure has, `f {a, b}` — the
// diagnostic that names the rule.
func (c *compiler) notARecord(dp *ast.DictPattern) ast.Expr {
	if dp.Alt != nil {
		return dp.Alt
	}
	if dp.Module != nil {
		why := "it exports no record of that name"
		switch {
		case !c.knownModule(dp.Module.Name):
			why = fmt.Sprintf("'%s' is not a module this file includes", dp.Module.Name)
		case !c.in.opts.CompileIncludes:
			why = "the host compiles without reading includes (Options.CompileIncludes)"
		case c.imports[dp.Module.Name] == nil:
			why = "the module could not be read at compile time"
		}
		c.syntaxErrorf(dp.Pos(), "'%s {…}' is not a record pattern: %s", dp.RecordName(), why)
		return dp
	}
	c.syntaxErrorf(dp.Pos(), "'%s' is not a record, so '%s {…}' is not a pattern: a record pattern takes the name of a record declaration",
		dp.Record.Name, dp.Record.Name)
	return dp
}

// recordFields checks a record pattern's keys against the declaration (RFC 0002): a field
// the record does not have is a misspelling, and the pattern could never fire.
func (c *compiler) recordFields(dp *ast.DictPattern, d *ast.RecordDecl) {
	have := make(map[string]bool, len(d.Fields))
	names := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		have[f.Name] = true
		names[i] = f.Name
	}
	for _, en := range dp.Entries {
		if !have[en.Key] {
			c.syntaxErrorf(en.KeyPos, "record %s has no field '%s'; it has %s", d.Name, en.Key, strings.Join(names, ", "))
		}
	}
}

// armPattern compiles an arm whose pattern binds (§5.3). The names go into a scope of
// their own — the arm body's — so the guard and the body both see them and nothing after
// the arm does, which is exactly how a `for` treats its loop variables.
func (c *compiler) armPattern(arm *ast.MatchArm) {
	c.push()
	for _, pat := range arm.Pats {
		c.bindings(pat)
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
func (c *compiler) bindings(x ast.Expr) {
	switch pat := x.(type) {
	case *ast.ArrayPattern:
		c.arrayBindings(pat)
	case *ast.DictPattern:
		if pat.Record != nil {
			// The shape is asked of the constructor the name holds when the arm runs,
			// as the bare `Money ->` arm does (§7.8); the fields are checked now.
			if d := c.patternRecord(pat); d != nil && d != someRecord {
				c.recordFields(pat, d)
			}
			if pat.Module != nil {
				// The constructor is the module's member, read when the arm runs.
				if id, ok := c.expr(pat.Module).(*ast.Ident); ok {
					pat.Module = id
				}
			} else if id, ok := c.expr(pat.Record).(*ast.Ident); ok {
				pat.Record = id
			}
		}
		// Left to right, as an array's positions are: a comparison may name what an
		// earlier key bound.
		for i := range pat.Entries {
			en := &pat.Entries[i]
			switch t := en.Value.(type) {
			case *ast.Ident:
				if en.In {
					en.Value = c.expr(t)
					continue
				}
				t.Slot, t.Ref = -1, ast.RefLocal
				c.declare(t.Name)
			case *ast.ArrayPattern, *ast.DictPattern:
				en.Value = c.nestedPattern(t)
			default:
				en.Value = c.expr(t)
			}
		}
		if pat.Rest != nil {
			pat.Rest.Slot, pat.Rest.Ref = -1, ast.RefLocal
			c.declare(pat.Rest.Name)
		}
	}
}

// nestedPattern compiles a pattern inside another and returns what stands in its place:
// a `Name {…}` whose Name is not a record gives way to the call it also reads as, which
// is then compared like any other value (RFC 0002).
func (c *compiler) nestedPattern(x ast.Expr) ast.Expr {
	if dp, ok := x.(*ast.DictPattern); ok && dp.Record != nil && c.patternRecord(dp) == nil {
		if alt := c.notARecord(dp); alt != dp {
			return c.expr(alt)
		}
		return dp
	}
	c.bindings(x)
	return x
}

func (c *compiler) arrayBindings(pat *ast.ArrayPattern) {
	for i, el := range pat.Elems {
		switch t := el.(type) {
		case *ast.ArrayPattern, *ast.DictPattern:
			pat.Elems[i] = c.nestedPattern(t)
		case *ast.Ident:
			t.Slot, t.Ref = -1, ast.RefLocal
			c.declare(t.Name)
		case *ast.Splat:
			// `*rest` binds the tail; the anonymous `*` binds nothing (RFC 0001).
			if id, ok := t.X.(*ast.Ident); ok {
				id.Slot, id.Ref = -1, ast.RefLocal
				c.declare(id.Name)
			}
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
		what := "'" + n.Named[0].Name + " = …'"
		if n.Named[0].Spread {
			what = "a '**' spread"
		}
		c.argErrorf(n.Named[0].NamePos,
			"%s takes its arguments by position, so %s has no parameter to bind", id.Name, what)
	case len(n.Args) > 0 && c.hasMethod(id.Name):
		// One namespace, not two (§12): `filter(xs, f)` is `xs.filter(f)`, so a name
		// that is only a stdlib row of some kind is callable in prefix position too.
		// Which kind it belongs to depends on the first argument's value, so the
		// resolution itself stays at run time, in callFunction.
		id.Ref = ast.RefBuiltin
	default:
		if len(c.errs) < maxCompileErrors {
			err := undefinedFunctionError(id.Name, c.visible())
			if gap := capabilityGap(c.in, id.Name); gap != "" {
				err = nameErrorf("undefined function '%s': %s", id.Name, gap)
			}
			c.errs = append(c.errs, err.At(c.file, id.Start.Line, id.Start.Col))
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
	if module || c.hasMethod(n.Name) || c.recordField(n.Name) {
		// A module member, a real method and a record's field are all resolved by the
		// receiver's value, so the dispatch stays where it belongs, at run time. The
		// field is tested before the UFCS rewrite below on purpose: where a record's
		// field and a function share a name, `m.total` must reach the field and `x.total`
		// on anything else must still reach the function, and only the value can say
		// which (§7.8).
		//
		// A binding of the same name in scope is recorded here, and tried before the
		// stdlib table at run time (§8.7): whether it holds a function is the value's to
		// say, so the decision cannot be made now.
		if !module && c.localCandidate(n.Name) {
			n.Local = true
			c.markUsed(n.Name)
		}
		return n
	}
	if (c.known(n.Name) && !c.knownModule(n.Name)) || c.hasFunc(n.Name) {
		if r := c.importedFields[n.Name]; r != "" {
			// A field another module's record declares never takes a call away from a
			// function this unit can see (RFC 0003): the call stays what it compiled to
			// before CompileIncludes, and the overlap is said once, here.
			c.warnAt(n.NamePos, "'%s' is also a field of %s; this calls the function — read the field with m[\"%s\"]", n.Name, r, n.Name)
		}
		if n.Safe {
			// `?.` must not evaluate anything when the receiver is nil (§4.3), which a
			// plain call cannot express; the evaluator's UFCS fallback handles it — and
			// it must see the script's binding before a host function of the same name.
			if !module && c.localCandidate(n.Name) {
				n.Local = true
				c.markUsed(n.Name)
			}
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
	if c.importedField(n.Name) {
		// A field of a record an included module exports (RFC 0003). Only where nothing
		// else claimed the name: the dispatch at run time reads the receiver's own shape
		// first, as for a field this unit declares (§7.8).
		if c.localCandidate(n.Name) {
			n.Local = true
			c.markUsed(n.Name)
		}
		return n
	}
	c.undefinedMethod(n.Name, n.NamePos)
	return n
}

// undefinedMethod produces §5.6's message shape, with a suggestion drawn from every
// method table and every function name — the receiver's kind is not known statically,
// so the whole namespace is the candidate pool.
func (c *compiler) undefinedMethod(name string, pos token.Pos) {
	if gap := capabilityGap(c.in, name); gap != "" {
		c.errorf(pos, "undefined method '%s': %s", name, gap)
		return
	}
	if c.pool == nil {
		for _, k := range dispatchKinds {
			c.pool = append(c.pool, MethodNames(k)...)
		}
		c.pool = append(c.pool, BuiltinNames()...)
		c.pool = append(c.pool, c.visible()...)
		for f := range c.fields {
			// A record's field is reached with a '.' like a method (§7.8), so a mistyped
			// one is this diagnostic and its fix belongs in this pool.
			c.pool = append(c.pool, f)
		}
		for f := range c.importedFields {
			c.pool = append(c.pool, f)
		}
	}
	why := ""
	if len(c.importFails) > 0 {
		// A field of a module's record is only known when the module could be read, so
		// a name nothing resolved may be one: say which include was not (RFC 0003).
		why = "; " + strings.Join(c.importFails, "; ")
	}
	if s := suggest(name, c.pool); s != "" {
		c.errorf(pos, "undefined method '%s'; did you mean '%s'?%s", name, s, why)
		return
	}
	c.errorf(pos, "undefined method '%s'%s", name, why)
}

// dispatchKinds is every kind a method may be registered under, plus the universal
// table. It is what makes "is this name a method of anything?" a decidable question at
// compile time.
var dispatchKinds = [...]Kind{
	KNil, KBool, KInt, KFloat, KString, KRegex, KArray, KDict, KFunc, KTime, KRange, KTask, KSeq, KAny,
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
	rs.startMemory(vars)
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
		rs.stopMemory()
		rs.unlock()
		res.Steps, res.Elapsed, res.Tests = rs.sh.steps, time.Since(start), rs.sh.tests
		res.PeakMemory = rs.sh.memPeak
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
