package mzs

import (
	"context"
	"io"
	"math/rand"
	"time"

	"mzs/internal/rx"
	"mzs/internal/token"
)

// HostFunc is a Go function callable from a script. It receives the per-call context
// and its already-evaluated arguments. Returning an error surfaces as a catchable
// script error unless the error wraps ErrFatal.
type HostFunc func(c *Ctx, args []Value) (Value, error)

// Ctx is the handle a host function, a builtin and a stdlib method are given. It is
// *not* retained after the call returns: the evaluator keeps one Ctx per Run and
// rewrites its fields around each invocation, so copying a *Ctx or holding it past the
// call is a bug.
type Ctx struct {
	rs      *runState
	args    []Value
	closure Value
	name    string    // the function or method being executed, for diagnostics
	pos     token.Pos // the call site
}

// newCtx builds the single Ctx a Run reuses.
func newCtx(rs *runState) *Ctx { return &Ctx{rs: rs} }

// Context returns the context the Run was started with. Host functions must honour it.
func (c *Ctx) Context() context.Context {
	if c.rs == nil || c.rs.ctx == nil {
		return context.Background()
	}
	return c.rs.ctx
}

// Interp returns the interpreter running this call.
func (c *Ctx) Interp() *Interp { return c.rs.in }

// Options returns the normalised options of the running interpreter.
func (c *Ctx) Options() Options { return *c.rs.opts }

// Arg returns the i-th argument, or Nil when out of range.
func (c *Ctx) Arg(i int) Value {
	if i < 0 || i >= len(c.args) {
		return Nil()
	}
	return c.args[i]
}

// NArgs is the number of arguments passed.
func (c *Ctx) NArgs() int { return len(c.args) }

// Args returns the argument slice. Do not retain it past the call.
func (c *Ctx) Args() []Value { return c.args }

// Name is the function or method name being executed; diagnostics use it.
func (c *Ctx) Name() string { return c.name }

// Pos is the call site position.
func (c *Ctx) Pos() token.Pos { return c.pos }

// setCall arms the Ctx for one invocation and returns a function that restores the
// previous state. Only the evaluator calls it.
func (c *Ctx) setCall(name string, pos token.Pos, args []Value, closure Value) func() {
	pn, pp, pa, pc := c.name, c.pos, c.args, c.closure
	c.name, c.pos, c.args, c.closure = name, pos, args, closure
	return func() { c.name, c.pos, c.args, c.closure = pn, pp, pa, pc }
}

// ---------------------------------------------------------------------------
// Globals
// ---------------------------------------------------------------------------

// Global reads a $var from the current Run's table. The name may be given with or
// without the leading '$'. An unbound name reads as Nil, in here and in a script alike
// (§9.2) — there is no shim that turns it into its own source text.
func (c *Ctx) Global(name string) Value {
	v, ok := c.rs.globals[globalName(name)]
	if !ok {
		return Nil()
	}
	return v
}

// HasGlobal reports whether the name is bound, which is what `defined($x)` needs.
func (c *Ctx) HasGlobal(name string) bool {
	_, ok := c.rs.globals[globalName(name)]
	return ok
}

// SetGlobal writes a $var into the current Run's table. The host reads the final state
// back through Result.Globals, which is how set_var-style blocks return values.
func (c *Ctx) SetGlobal(name string, v Value) {
	c.rs.globals[globalName(name)] = v
}

// ---------------------------------------------------------------------------
// Calling back into the script
// ---------------------------------------------------------------------------

// Call invokes a script or host function value with no trailing closure attached.
func (c *Ctx) Call(fn Value, args ...Value) (Value, error) {
	return evalCall(c, fn, args, Nil())
}

// CallWithClosure invokes fn with closure attached as its trailing closure.
func (c *Ctx) CallWithClosure(fn Value, closure Value, args ...Value) (Value, error) {
	return evalCall(c, fn, args, closure)
}

// ---------------------------------------------------------------------------
// The closure-argument protocol
// ---------------------------------------------------------------------------
//
// There is no "block" in mzs: `xs.map { … }` passes an ordinary closure value as the
// call's last argument (D2, §4.2), so `map(xs, { … })` and `xs.map(double)` reach a
// method exactly the same way. A row that accepts a closure therefore counts it in its
// Method.Min/Max like any other argument.
//
// Ctx.Closure is the handle to that trailing closure, armed by the evaluator, so a
// method need not re-check the kind of its last argument. CallClosure's contract, which
// every stdlib method depends on:
//
//   - normal completion returns (value, nil);
//   - `next v` inside the closure also returns (v, nil) — CallClosure absorbs it,
//     because `next` ends one iteration and nothing more;
//   - `break v` returns (Nil, err); the method MUST return that error unchanged, and
//     the evaluator's dispatch site turns it into the value of the whole method call,
//     so `xs.each { break 1 }` is 1;
//   - `return v` likewise returns an error the method MUST propagate unchanged, so a
//     `return` inside a closure leaves the enclosing function;
//   - a script error, and a limit error, propagate unchanged as well.
//
// In short: a stdlib method never inspects the error CallClosure returns. It returns it.
//
// Closure literals are exempt from the arity check (§7.7): extra arguments are dropped
// and missing ones become nil. A method may therefore always pass the arguments its
// table row promises, which is what makes `dict.each { (k, v) -> … }` and
// `dict.each { it }` both work.

// Closure returns the trailing closure of this call, or Nil when there is none.
func (c *Ctx) Closure() Value { return c.closure }

// HasClosure reports whether a trailing closure was attached to this call.
func (c *Ctx) HasClosure() bool { return c.closure.Kind() == KFunc }

// CallClosure invokes the attached closure. Calling it without one is an argument error
// naming the method, which is the diagnostic a script author needs.
func (c *Ctx) CallClosure(args ...Value) (Value, error) {
	if !c.HasClosure() {
		return Nil(), argErrorf("%s requires a closure", c.name)
	}
	return evalCall(c, c.closure, args, Nil())
}

// ---------------------------------------------------------------------------
// Budget and limits
// ---------------------------------------------------------------------------

// Step charges n interpreter steps. A host function that loops must call it, or it can
// outrun the budget. It returns a limit error once the budget, the deadline or the
// context is exhausted; that error must be propagated, never swallowed.
func (c *Ctx) Step(n int64) error { return c.rs.step(n) }

// CheckCollection reports a limit error when an operation would materialise more than
// Options.MaxCollection elements (§14.2).
func (c *Ctx) CheckCollection(n int) error { return c.rs.checkCollection(n) }

// CheckString reports a limit error when a produced string would exceed
// Options.MaxStringBytes.
func (c *Ctx) CheckString(n int) error { return c.rs.checkString(n) }

// ---------------------------------------------------------------------------
// Capabilities and diagnostics
// ---------------------------------------------------------------------------

// Out is the sink for print/println/debug; never nil.
func (c *Ctx) Out() io.Writer { return c.rs.opts.out() }

// Now returns the host clock, or an error when the host did not install one. Time is a
// capability, not a given (§14.3, D15).
func (c *Ctx) Now() (time.Time, error) {
	if c.rs.opts.Now == nil {
		return time.Time{}, nameErrorf("undefined function 'now' (the host did not enable a clock)")
	}
	return c.rs.opts.Now(), nil
}

// Rand returns the host randomness source, or an error when there is none.
func (c *Ctx) Rand() (*rand.Rand, error) {
	if c.rs.opts.Rand == nil {
		return nil, nameErrorf("undefined function 'rand' (the host did not enable randomness)")
	}
	return c.rs.opts.Rand, nil
}

// Location is the default zone for strftime and in_time_zone; never nil.
func (c *Ctx) Location() *time.Location {
	if c.rs.opts.Location == nil {
		return time.UTC
	}
	return c.rs.opts.Location
}

// Regex compiles a pattern through the interpreter's shared LRU cache (§11.4). Every
// stdlib method that accepts a string pattern must go through here rather than calling
// rx.Compile, so a hot dialogue compiles each pattern once.
func (c *Ctx) Regex(pattern, flags string) (Value, error) {
	r, err := c.rs.in.compileRegex(pattern, flags)
	if err != nil {
		return Nil(), err
	}
	return regexOf(r), nil
}

// RegexOf coerces a value to a compiled regex: a KRegex passes through, a String is
// compiled as a literal pattern with no flags.
func (c *Ctx) RegexOf(v Value) (*rx.Regexp, error) {
	switch v.Kind() {
	case KRegex:
		return v.rx(), nil
	case KString:
		return c.rs.in.compileRegex(quoteMeta(v.Str()), "")
	}
	return nil, typeErrorf("expected a regex or a string, got %s", v.Kind())
}

// Errorf builds a positioned script error. It is the general-purpose failure a host
// function reports; use ErrorfKind when the failure has one of the §13.5 kinds.
func (c *Ctx) Errorf(format string, a ...any) error {
	return c.position(newError(ErrKindRaise, format, a...))
}

// ErrorfKind builds a positioned script error of a specific kind.
func (c *Ctx) ErrorfKind(kind, format string, a ...any) error {
	return c.position(newError(kind, format, a...))
}

// TypeErrorf is the shorthand every stdlib argument check wants.
func (c *Ctx) TypeErrorf(format string, a ...any) error {
	return c.position(typeErrorf(format, a...))
}

// ArgErrorf is the shorthand for arity and argument-shape failures.
func (c *Ctx) ArgErrorf(format string, a ...any) error {
	return c.position(argErrorf(format, a...))
}

// position stamps the current file, call site and stack onto an error.
func (c *Ctx) position(e *Error) *Error {
	e.At(c.rs.file, c.pos.Line, c.pos.Col)
	if e.Stack == nil {
		e.Stack = c.rs.frames()
	}
	return e
}

// quoteMeta escapes every regex metacharacter in s, so a plain string used where a
// pattern is expected matches literally.
func quoteMeta(s string) string {
	const meta = `\.+*?()|[]{}^$`
	out := make([]rune, 0, len(s))
	for _, r := range s {
		for _, m := range meta {
			if r == m {
				out = append(out, '\\')
				break
			}
		}
		out = append(out, r)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Registration (§13.4). All of it is setup-only: do it before the first Run.
// ---------------------------------------------------------------------------

// Register installs a global host function. arity -1 makes it variadic. Registering a
// name that already exists replaces it.
func (in *Interp) Register(name string, arity int, f HostFunc) {
	in.ensureState()
	in.funcs[name] = Fn(name, arity, f)
	delete(in.removed, name)
}

// RegisterModule installs a module as an ordinary value bound in the root scope: after
// RegisterModule("http", …), `http.get(u)` reads the member and calls it (§12.8).
func (in *Interp) RegisterModule(name string, members map[string]Value) {
	in.ensureState()
	d := NewOrderedDictCap(len(members))
	for _, k := range sortedKeys(members) {
		_ = d.Set(Str(k), members[k])
	}
	in.modules[name] = dictOf(d)
	delete(in.removed, name)
}

// SetGlobal installs a default $var seeded into every Run's globals table. The per-Run
// vars passed to Run and Eval override it.
func (in *Interp) SetGlobal(name string, v Value) {
	in.ensureState()
	in.globals[globalName(name)] = v
}

// Unregister removes a builtin, a host function or a module by name — the supported
// way for a host to narrow the surface a script can reach.
func (in *Interp) Unregister(name string) {
	in.ensureState()
	delete(in.funcs, name)
	delete(in.modules, name)
	in.removed[name] = true
}

// ensureState makes the registration methods safe even if they are called on an Interp
// whose New forgot initState. It never resets an already-initialised interpreter.
func (in *Interp) ensureState() {
	if in.funcs == nil {
		in.initState(in.opts)
	}
}

// hostFunc looks up a name installed with Register.
func (in *Interp) hostFunc(name string) (Value, bool) {
	v, ok := in.funcs[name]
	return v, ok
}

// hostModule looks up a module installed with RegisterModule.
func (in *Interp) hostModule(name string) (Value, bool) {
	v, ok := in.modules[name]
	return v, ok
}

// isRemoved reports whether the host dropped a name with Unregister.
func (in *Interp) isRemoved(name string) bool { return in.removed[name] }
