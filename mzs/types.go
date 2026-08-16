package mzs

import (
	"context"
	"sync/atomic"
	"time"

	"mzs/internal/ast"
	"mzs/internal/lru"
	"mzs/internal/rx"
)

// This file declares the types the whole package hangs off. The evaluator owns their
// behaviour — interp.go writes New/Compile/Eval/Run/RunResult, eval.go and call.go
// write the tree walk — and may append fields; it must not change the shapes that
// options.go, host.go, methods.go and the stdlib already read.

// Interp is a compiled-program factory and a registry of host capabilities. One
// *Interp serves unlimited concurrent Runs. Register, RegisterModule and SetGlobal are
// setup-only: they are not safe once the first Run has started (§13.3).
type Interp struct {
	opts    Options
	globals map[string]Value // defaults installed with SetGlobal, copied per Run
	funcs   map[string]Value // host functions installed with Register
	modules map[string]Value // host modules installed with RegisterModule
	removed map[string]bool  // names dropped with Unregister
	progs   *lru.Cache[string, *Program]
	rxc     *lru.Cache[string, *rx.Regexp]
	started atomic.Bool
}

// initState prepares an Interp for use. interp.go's New MUST call it exactly once
// before returning; every other method assumes the maps and caches exist.
func (in *Interp) initState(opts Options) {
	in.opts = opts.normalize()
	in.globals = make(map[string]Value)
	in.funcs = make(map[string]Value)
	in.modules = make(map[string]Value)
	in.removed = make(map[string]bool)
	in.progs = lru.New[string, *Program](in.opts.ProgramCache)
	in.rxc = lru.New[string, *rx.Regexp](in.opts.RegexCacheSize)
}

// Options returns a copy of the normalised options this interpreter runs with.
func (in *Interp) Options() Options { return in.opts }

// compileRegex compiles a pattern through the shared LRU (§11.4). It is what the
// `regex()` builtin and every string method taking a string pattern must use, so a
// hot dialogue never recompiles the same expression.
func (in *Interp) compileRegex(pattern, flags string) (*rx.Regexp, error) {
	key := pattern + "\x00" + flags
	if r, ok := in.rxc.Get(key); ok {
		return r, nil
	}
	r, err := rx.Compile(pattern, flags)
	if err != nil {
		return nil, regexErrorf("%s", err.Error())
	}
	r.SetBudget(in.opts.RegexSteps)
	in.rxc.Add(key, r)
	return r, nil
}

// Program is a compiled script. It is immutable and may be run concurrently from many
// goroutines; all mutable state lives in the per-Run frame (§6.3).
type Program struct {
	name      string
	src       string
	file      *ast.Program
	warnings  []Warning
	frameSize int
}

// newProgram is what interp.go's Compile builds its result with.
func newProgram(name, src string, file *ast.Program, warns []Warning) *Program {
	p := &Program{name: name, src: src, file: file, warnings: warns}
	if file != nil {
		p.frameSize = file.FrameSize
	}
	return p
}

// Source returns the text the program was compiled from.
func (p *Program) Source() string { return p.src }

// Name returns the diagnostic name given to Compile.
func (p *Program) Name() string { return p.name }

// Warnings returns the compile-time warnings (§11.5, §17).
func (p *Program) Warnings() []Warning { return p.warnings }

// String pretty-prints the AST, which is what `mzs --ast` shows.
func (p *Program) String() string {
	if p == nil || p.file == nil {
		return ""
	}
	return ast.Dump(p.file)
}

// ast returns the syntax tree for the evaluator.
func (p *Program) tree() *ast.Program { return p.file }

// Result is the richer return of RunResult (§13.3).
type Result struct {
	Value   Value
	Globals map[string]Value
	Steps   int64
	Elapsed time.Duration
}

// Func is a callable: a script `fn`, a `{ … }` closure literal, or a Go function
// installed by the host. Closures capture their defining Env by reference, so assigning
// to an outer local from inside a closure mutates the outer local (§7.7).
type Func struct {
	Name   string
	Params []ast.Param
	Body   *ast.BlockStmt
	Env    *Env
	Host   HostFunc
	Arity  int  // -1 for variadic
	Lambda bool // true for a `{ … }` literal, false for a named `fn` (§7.7)
	Async  bool // true for `async fn`: the call starts a task and returns it (§8.14)
	// Frame is the number of local slots one invocation needs, from the compile pass.
	// It mirrors Body.FrameSize for script functions and is 0 for host functions.
	Frame int
}

// LenientArity reports whether the arity check is skipped for this function. Closure
// literals are exempt (§7.7): they are handed to library functions that decide how many
// values to pass, so extra arguments are dropped and missing ones become nil. That is
// what lets `dict.each { (k, v) -> … }` and `dict.each { it }` both work. A named `fn`
// and a host function are always checked.
func (f *Func) LenientArity() bool { return f != nil && f.Lambda && f.Host == nil }

// Env is one lexical scope. Every `{ … }` is a closure, so every `{ … }` — including
// an `if`, `while`, `for` or `match` body — invokes one and gets its own Env (D2, §8.2).
// Lookup walks the parent chain; the compile pass resolves most references to a slot
// index so the hot path never touches the name list.
type Env struct {
	parent *Env
	names  []string
	slots  []Value
}

// newEnv creates a scope with room for size locals.
func newEnv(parent *Env, size int) *Env {
	e := &Env{parent: parent}
	if size > 0 {
		e.names = make([]string, 0, size)
		e.slots = make([]Value, 0, size)
	}
	return e
}

// Parent returns the enclosing scope, or nil at the top.
func (e *Env) Parent() *Env { return e.parent }

// Lookup finds a binding by name, walking outwards.
func (e *Env) Lookup(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		for i := len(s.names) - 1; i >= 0; i-- {
			if s.names[i] == name {
				return s.slots[i], true
			}
		}
	}
	return Nil(), false
}

// Assign writes to the nearest existing binding and reports whether it found one.
// Plain `=` creates the binding in the current scope when this returns false (§8.2).
func (e *Env) Assign(name string, v Value) bool {
	for s := e; s != nil; s = s.parent {
		for i := len(s.names) - 1; i >= 0; i-- {
			if s.names[i] == name {
				s.slots[i] = v
				return true
			}
		}
	}
	return false
}

// Define creates or shadows a binding in this scope, which is what `:=` does.
func (e *Env) Define(name string, v Value) int {
	e.names = append(e.names, name)
	e.slots = append(e.slots, v)
	return len(e.slots) - 1
}

// Set is `=`: assign where the name already lives, otherwise create it here.
func (e *Env) Set(name string, v Value) {
	if !e.Assign(name, v) {
		e.Define(name, v)
	}
}

// Slot and SetSlot are the resolved-reference fast path.
func (e *Env) Slot(i int) Value {
	if i < 0 || i >= len(e.slots) {
		return Nil()
	}
	return e.slots[i]
}

func (e *Env) SetSlot(i int, v Value) {
	if i >= 0 && i < len(e.slots) {
		e.slots[i] = v
	}
}

// Len is the number of bindings in this scope alone.
func (e *Env) Len() int { return len(e.slots) }

// runState is the mutable state of one Run: the globals table, the budget, the call
// stack. It is never shared between Runs, which is what makes two concurrent
// dialogues on the same *Program isolated (§10).
//
// The evaluator owns this type's behaviour and may append fields.
type runState struct {
	in      *Interp
	ctx     context.Context
	opts    *Options
	file    string
	globals map[string]Value
	stack   []Frame

	// sh is the half of the Run every task shares: the lock that serialises them, the
	// step counter they all spend from, and the group the Run joins before it returns
	// (§8.14). A task runs on a copy of this struct — its own stack, depth and file —
	// and the two halves are exactly what may and may not diverge.
	sh *runShared
	// task is the task this state belongs to, or nil in the main program. It is what
	// makes `t.await` inside t itself a diagnostic rather than a hang.
	task *task

	limit    int64
	tick     int
	deadline time.Time
	hasDL    bool
	depth    int
	maxDepth int

	// modules caches the script modules this Run has loaded, keyed on the name the
	// loader resolved, and loading is the include stack that catches a cycle. exports
	// collects the names the module currently being loaded exported, in order; it is
	// nil while the main program runs (§12.8).
	modules map[string]Value
	loading []string
	exports []string

	// srv is the http.serve loop running in this Run, or nil. It lives here because
	// http.stop() is called from inside a handler and has to find the server it is
	// nested in, and because one Run serves at most one listener (§12.9).
	srv *httpServer
}

// newRunState builds the per-Run state with the budget already armed.
func newRunState(in *Interp, ctx context.Context, file string, globals map[string]Value) *runState {
	rs := &runState{
		in:       in,
		ctx:      ctx,
		opts:     &in.opts,
		file:     file,
		globals:  globals,
		sh:       &runShared{},
		limit:    in.opts.StepBudget,
		maxDepth: in.opts.MaxDepth,
		// The module cache is created here rather than on first use because a task runs
		// on a copy of this struct: an `include` inside a task must land in the map the
		// whole Run shares, not in one the copy grew for itself (§12.8).
		modules: map[string]Value{},
	}
	if in.opts.Timeout > 0 {
		rs.deadline = time.Now().Add(in.opts.Timeout)
		rs.hasDL = true
	}
	if rs.globals == nil {
		rs.globals = make(map[string]Value)
	}
	return rs
}

// evalCall is the seam between the stdlib and the evaluator. call.go installs the real
// implementation from an init(); every path that invokes a script function — Ctx.Call,
// Ctx.CallClosure, a host function calling back into a closure it was handed — goes
// through here, so no stdlib file needs to know how frames are built.
//
// closure is the trailing closure to attach to the call (Nil() for none). The returned
// error may be a control signal: a `return` or `break` that escaped fn's body (§8.10).
var evalCall = func(c *Ctx, fn Value, args []Value, closure Value) (Value, error) {
	return Nil(), newError(ErrKindInternal, "mzs: evaluator not installed")
}

// globalName normalises a global's spelling: the table is keyed on the '$'-prefixed
// form, and the host may pass either (§10).
func globalName(name string) string {
	if name == "" || name[0] == '$' {
		return name
	}
	return "$" + name
}
