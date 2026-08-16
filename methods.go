package mzs

import (
	"sort"

	"mzs/internal/token"
)

// This file is the registry every stdlib file writes into and the evaluator reads
// from. str.go, num.go, regexv.go, array.go, dict.go, json.go and time.go each install
// their rows from an init(), so the library owners never touch a shared table by hand
// and never touch each other's files.
//
// Registration is init()-time only, so every table is complete and read-only before the
// first Compile. Dispatch is an **exact name match** and nothing else: there are no
// aliases anywhere in the standard library (D17), and mzs has no method_missing.

// Method is one row of a per-kind method table (§12). Min and Max bound the
// **positional** arguments; Max == -1 means variadic. A trailing closure is an ordinary
// last argument (§4.2), so a row that takes one counts it here.
type Method struct {
	Name string
	Min  int
	Max  int
	Fn   func(c *Ctx, recv Value, args []Value) (Value, error)

	// NeedsRand and NeedsNow gate a method on a host capability: `sample`/`shuffle`
	// need Options.Rand, the time members need Options.Now. A gated method that is
	// not available reports `undefined method`, exactly as if it were absent.
	NeedsRand bool
	NeedsNow  bool
}

// CheckArity validates a positional argument count against the row.
func (m Method) CheckArity(n int) error {
	if n < m.Min {
		return argErrorf("%s expects at least %d argument(s), got %d", m.Name, m.Min, n)
	}
	if m.Max >= 0 && n > m.Max {
		return argErrorf("%s expects at most %d argument(s), got %d", m.Name, m.Max, n)
	}
	return nil
}

// Available reports whether the host capabilities this row needs are installed.
func (m Method) Available(o *Options) bool {
	if m.NeedsRand && o.Rand == nil {
		return false
	}
	if m.NeedsNow && o.Now == nil {
		return false
	}
	return true
}

var (
	methodTables [256]map[string]Method
	// methodAnyKind is the union of every registered name, so the compile step can ask
	// "is this a method name at all?" without knowing the receiver's kind (§6.3 step 2).
	methodAnyKind = map[string]bool{}
)

// RegisterMethod installs one method for a receiver kind. Use KAny for the methods that
// take any first argument (§12.1: `len`, `str`, `json`, `dup`, `tap`, `pipe`, …), which
// are looked up after the kind's own table.
//
// Call only from init(). Registering the same (kind, name) twice replaces the row,
// which is how a later file may deliberately specialise a universal method.
func RegisterMethod(k Kind, m Method) {
	if m.Name == "" || m.Fn == nil {
		panic("mzs: RegisterMethod requires a name and a function")
	}
	if methodTables[k] == nil {
		methodTables[k] = make(map[string]Method)
	}
	methodTables[k][m.Name] = m
	methodAnyKind[m.Name] = true
}

// RegisterMethods installs several rows for one kind.
func RegisterMethods(k Kind, ms ...Method) {
	for _, m := range ms {
		RegisterMethod(k, m)
	}
}

// LookupMethod finds the method a receiver kind responds to, falling back to the
// universal KAny table. Dispatch is an exact name match (§8.7 step 1).
func LookupMethod(k Kind, name string) (Method, bool) {
	if t := methodTables[k]; t != nil {
		if m, ok := t[name]; ok {
			return m, true
		}
	}
	if k != KAny {
		if t := methodTables[KAny]; t != nil {
			if m, ok := t[name]; ok {
				return m, true
			}
		}
	}
	return Method{}, false
}

// HasMethod reports whether receiver kind k answers name, counting the universal KAny
// table. It is the exact-name predicate of UFCS step 1 (§4.3, §8.7).
func HasMethod(k Kind, name string) bool {
	_, ok := LookupMethod(k, name)
	return ok
}

// HasMethodAnyKind reports whether *some* receiver kind answers name. This is the form
// the compile step needs (§6.3 step 2): a receiver's kind is not known statically, so a
// `MethodCall` survives as a method call when any kind could answer it, and is rewritten
// to a call of the like-named function otherwise. The per-kind check happens at dispatch
// time, where the receiver exists.
func HasMethodAnyKind(name string) bool { return methodAnyKind[name] }

// MethodNames lists the methods registered for a kind, sorted. It feeds the "did you
// mean" suggestions of §17.
func MethodNames(k Kind) []string {
	t := methodTables[k]
	out := make([]string, 0, len(t))
	for n := range t {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Global builtins (§12.1)
// ---------------------------------------------------------------------------

// Builtin is one global function. The gates are declarative so the evaluator can hide
// `rand`/`uuid` without a random source and `now` without a clock, without either of
// them needing a special case in builtins.go.
type Builtin struct {
	Name string
	Min  int
	Max  int // -1 for variadic
	Fn   HostFunc

	NeedsRand bool
	NeedsNow  bool

	// Special marks a builtin the parser/evaluator handles structurally rather than
	// by evaluating its arguments — `defined` is the only one (§12.1).
	Special bool
}

// CheckArity validates a positional argument count.
func (b Builtin) CheckArity(n int) error {
	if n < b.Min {
		return argErrorf("%s expects at least %d argument(s), got %d", b.Name, b.Min, n)
	}
	if b.Max >= 0 && n > b.Max {
		return argErrorf("%s expects at most %d argument(s), got %d", b.Name, b.Max, n)
	}
	return nil
}

// Available reports whether the host capabilities allow this builtin.
func (b Builtin) Available(o *Options) bool {
	if b.NeedsRand && o.Rand == nil {
		return false
	}
	if b.NeedsNow && o.Now == nil {
		return false
	}
	return true
}

var builtins = map[string]Builtin{}

// RegisterBuiltin installs a global function. Call only from init().
func RegisterBuiltin(b Builtin) {
	if b.Name == "" || (b.Fn == nil && !b.Special) {
		panic("mzs: RegisterBuiltin requires a name and a function")
	}
	builtins[b.Name] = b
}

// RegisterBuiltins installs several.
func RegisterBuiltins(bs ...Builtin) {
	for _, b := range bs {
		RegisterBuiltin(b)
	}
}

// LookupBuiltin finds a global builtin by name. Gating is not applied here; the
// evaluator checks Available so it can produce the right diagnostic.
func LookupBuiltin(name string) (Builtin, bool) {
	b, ok := builtins[name]
	return b, ok
}

// HasBuiltin reports whether name is a global builtin, which is UFCS step 2 (§4.3): a
// method call whose name no kind answers becomes a call of the function of that name.
func HasBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

// Keywords lists §3.5's keywords, sorted. It is here so a tool outside the module — the
// editor support in editors/, for one — can read the lexer's table instead of keeping a
// copy that drifts.
func Keywords() []string {
	out := make([]string, 0, 17)
	for k := range token.Keywords() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuiltinNames lists every registered builtin, sorted. It feeds the §17 suggestions.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for n := range builtins {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Built-in modules (§12.8)
// ---------------------------------------------------------------------------

// ModuleGate records the host capabilities a module needs. A gated-off module is simply
// absent, so `time.now` without a clock is `undefined variable 'time'` — no special
// case anywhere.
type ModuleGate struct {
	NeedsTime bool // Options.EnableTime
	NeedsNow  bool // Options.Now
	NeedsRand bool // Options.Rand
	NeedsFS   bool // Options.FS
}

type moduleDef struct {
	name    string
	members *OrderedDict
	gate    ModuleGate
}

var moduleDefs = map[string]*moduleDef{}

func moduleFor(name string) *moduleDef {
	d, ok := moduleDefs[name]
	if !ok {
		d = &moduleDef{name: name, members: NewOrderedDict()}
		moduleDefs[name] = d
	}
	return d
}

// RegisterModuleFunc installs one member function of a built-in module, e.g.
// ("json", "parse", 1, 2, fn). Module names are lowercase (§12.8). Call only from
// init().
func RegisterModuleFunc(module, name string, min, max int, fn HostFunc) {
	arity := max
	if max < 0 || min != max {
		arity = -1
	}
	d := moduleFor(module)
	_ = d.members.Set(Str(name), Fn(module+"."+name, arity, func(c *Ctx, args []Value) (Value, error) {
		if len(args) < min {
			return Nil(), c.ArgErrorf("%s.%s expects at least %d argument(s), got %d",
				module, name, min, len(args))
		}
		if max >= 0 && len(args) > max {
			return Nil(), c.ArgErrorf("%s.%s expects at most %d argument(s), got %d",
				module, name, max, len(args))
		}
		return fn(c, args)
	}))
}

// RegisterModuleConst installs a constant member, e.g. ("math", "pi", Float(π)).
func RegisterModuleConst(module, name string, v Value) {
	_ = moduleFor(module).members.Set(Str(name), v)
}

// moduleGateOf returns what a module needs from the host, for the diagnostic that tells
// "gated off" apart from "no such module" (§12.8).
func moduleGateOf(module string) (ModuleGate, bool) {
	d, ok := moduleDefs[module]
	if !ok {
		return ModuleGate{}, false
	}
	return d.gate, true
}

// SetModuleGate records what a module needs from the host.
func SetModuleGate(module string, g ModuleGate) { moduleFor(module).gate = g }

// LookupModule returns a built-in module as a Dict value, honouring its gate. Modules
// are ordinary values bound in the root scope and reached with `.` like anything else
// (§12.8). The evaluator consults host modules (RegisterModule) first, then this table.
func LookupModule(name string, o *Options) (Value, bool) {
	d, ok := moduleDefs[name]
	if !ok {
		return Nil(), false
	}
	if d.gate.NeedsTime && !o.EnableTime {
		return Nil(), false
	}
	if d.gate.NeedsNow && o.Now == nil {
		return Nil(), false
	}
	if d.gate.NeedsRand && o.Rand == nil {
		return Nil(), false
	}
	if d.gate.NeedsFS && o.FS == nil {
		return Nil(), false
	}
	return dictOf(d.members), true
}

// ModuleNames lists the built-in modules available under o, sorted.
func ModuleNames(o *Options) []string {
	out := make([]string, 0, len(moduleDefs))
	for n := range moduleDefs {
		if _, ok := LookupModule(n, o); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
