package mzs

import (
	"fmt"
	"strings"

	"mzs/internal/ast"
)

// Modules (§12.8). There is no ambient namespace: `json`, `math`, `time`, `date` and
// `http` are reachable only after `include`, exactly like a script of your own. What a
// program can name is therefore visible in its first few lines, and a condition out of a
// dialogue store that never says `include` can reach nothing at all.
//
//	include json                          # a built-in module, gated by Options as before
//	include lib from "./lib.mzs"          # another script, read through Options.ModuleLoader
//
// A script module exports names explicitly:
//
//	export fn greet(n) { "привет, ${n}" }
//	export limit = 10
//	total = 0                             # not exported: private to the module
//
// The include binds one value — a dict of the exported names — under the name the
// importer chose, and member access goes through the same path a built-in module uses,
// so `lib.greet("Иван")` and `json.parse(s)` are the same operation (§8.7).
//
// What that value never is, is callable. `lib(x)` and `json(x)` are compile errors that
// name the member form, and where the spelling is also a §12.1 function — `json` is —
// the method form `x.json` is the one that cannot be misread. A name means the module or
// the function for a whole file, decided by the include at the top of it, never by the
// position it stands in.

// ModuleLoader is how a host lets scripts include other scripts. It is nil by default,
// and then `include x from "…"` is an error: reading source is filesystem access, and
// §14.3 does not hand that to a script on its own.
//
// from is the name of the program doing the including (a *Program's name, which for the
// CLI is the file path); path is the string written in the include. The loader resolves
// one against the other, decides what is allowed to be read, and returns the resolved
// name — which is what the module cache is keyed on, so two spellings of one file load
// once — together with its source.
type ModuleLoader func(from, path string) (resolved string, src string, err error)

// maxModuleDepth bounds a chain of includes. It is not about recursion — a cycle is
// caught by name below — but about a pathological tree of files taking the Go stack
// down with it.
const maxModuleDepth = 64

// loadModule resolves and runs one `include … from "…"`, returning the module's exports
// as a dict. The result is cached per Run: a diamond (a includes b and c, both include
// util) runs util once, and two Runs of the same program still share nothing (§10).
func (e ev) loadModule(n *ast.IncludeDecl) (Value, error) {
	in := e.rs.in
	load := in.opts.ModuleLoader
	if load == nil {
		return Nil(), nameErrorf("cannot include %q: the host did not enable module loading", n.Path)
	}

	resolved, src, err := load(e.rs.file, n.Path)
	if err != nil {
		return Nil(), nameErrorf("cannot include %q: %v", n.Path, err)
	}
	if resolved == "" {
		resolved = n.Path
	}

	if v, ok := e.rs.modules[resolved]; ok {
		return v, nil
	}
	for _, active := range e.rs.loading {
		if active == resolved {
			return Nil(), nameErrorf("include cycle: %s", strings.Join(append(e.rs.loading, resolved), " -> "))
		}
	}
	if len(e.rs.loading) >= maxModuleDepth {
		return Nil(), limitErrorf(ErrDepth, "include nesting too deep (%d)", maxModuleDepth)
	}

	prog, err := in.Compile(resolved, src)
	if err != nil {
		return Nil(), err
	}

	// The module runs inside this Run: it shares the globals table, the step budget and
	// the deadline, because it is part of the same evaluation and its cost is the
	// caller's cost. What it does not share is scope — a module's locals are its own.
	e.rs.loading = append(e.rs.loading, resolved)
	prevFile, prevExports := e.rs.file, e.rs.exports
	e.rs.file, e.rs.exports = resolved, nil

	menv := newEnv(nil, prog.frameSize)
	me := ev{rs: e.rs, cx: e.cx, env: menv}
	_, rerr := me.runProgram(prog.tree())

	exported := e.rs.exports
	e.rs.file, e.rs.exports = prevFile, prevExports
	e.rs.loading = e.rs.loading[:len(e.rs.loading)-1]
	if rerr != nil {
		return Nil(), me.at(rerr, prog.tree().Start)
	}

	d := NewOrderedDictCap(len(exported))
	for _, name := range exported {
		v, ok := menv.Lookup(name)
		if !ok {
			continue
		}
		_ = d.Set(Str(name), v)
	}
	mod := dictOf(d)
	if e.rs.modules == nil {
		e.rs.modules = map[string]Value{}
	}
	e.rs.modules[resolved] = mod
	return mod, nil
}

// evalInclude runs one include statement: it binds the module value under the declared
// name in the scope the statement stands in. A built-in module that the host did not
// enable says so here, which is a better answer than the `undefined variable` a missing
// capability used to produce three lines later.
func (e ev) evalInclude(n *ast.IncludeDecl) (Value, error) {
	var (
		mod Value
		err error
	)
	if n.HasPath {
		mod, err = e.loadModule(n)
	} else {
		var ok bool
		mod, ok = e.builtinModule(n.Name)
		if !ok {
			err = nameErrorf("%s", unknownModuleMessage(e.rs.in, n.Name))
		}
	}
	if err != nil {
		return Nil(), e.at(err, n.Kw)
	}
	e.env.Set(n.Name, mod)
	return mod, nil
}

// evalExport runs the declaration an `export` wraps, if any, and records the name. The
// value itself is read back at the end of the module run, so
// `export x = 1` followed by `x = 2` exports 2 — one binding, one value, no snapshot.
func (e ev) evalExport(n *ast.ExportDecl) (Value, error) {
	v := Nil()
	if n.Decl != nil {
		var err error
		v, err = e.execStmt(n.Decl)
		if err != nil {
			return Nil(), err
		}
	}
	for _, name := range n.Names {
		if !e.rs.exported(name) {
			e.rs.exports = append(e.rs.exports, name)
		}
	}
	if n.Decl == nil {
		if bound, ok := e.env.Lookup(n.Names[0]); ok {
			return bound, nil
		}
	}
	return v, nil
}

// exported reports whether the module being loaded already exports name, so
// `export x = 1` followed later by `export x` does not list it twice.
func (rs *runState) exported(name string) bool {
	for _, n := range rs.exports {
		if n == name {
			return true
		}
	}
	return false
}

// builtinModule is the value `include json` binds: a host module installed with
// RegisterModule first, then the built-in table, honouring its gate (§12.8).
func (e ev) builtinModule(name string) (Value, bool) {
	return lookupIncludable(e.rs.in, name)
}

func lookupIncludable(in *Interp, name string) (Value, bool) {
	if in.isRemoved(name) {
		return Nil(), false
	}
	if mv, ok := in.hostModule(name); ok {
		return mv, true
	}
	return LookupModule(name, &in.opts)
}

// unknownModuleMessage tells the two failures apart, because the fixes are different: a
// module that exists but is gated off needs a host option, a name that is not a module
// at all needs a path.
func unknownModuleMessage(in *Interp, name string) string {
	if gate, ok := moduleGateOf(name); ok {
		switch {
		case gate.NeedsTime:
			return fmt.Sprintf("module '%s' needs a clock: the host did not set EnableTime (mzs --time)", name)
		case gate.NeedsNow:
			return fmt.Sprintf("module '%s' needs a clock: the host did not install Options.Now", name)
		case gate.NeedsRand:
			return fmt.Sprintf("module '%s' needs randomness: the host did not install Options.Rand", name)
		case gate.NeedsFS:
			return fmt.Sprintf("module '%s' needs a filesystem: the host did not install Options.FS", name)
		}
	}
	return fmt.Sprintf("unknown module '%s' (a script module needs a path: include %s from \"./%s.mzs\")",
		name, name, name)
}

// moduleNotCallableMessage is what `json({total: 1})` says after `include json` (§12.8).
// A module is a dict of names: what a program calls is always one of them, so the fix is
// to name the member. When the same spelling is also a §12.1 function, UFCS already
// gives that function a spelling no one can read as the module — `x.json` — and the
// message hands it over rather than leaving the reader to find it.
func moduleNotCallableMessage(in *Interp, name string, isFunc bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "'%s' is a module, not a function: call one of its members", name)
	if mod, ok := lookupIncludable(in, name); ok {
		if members := memberList(mod, name); members != "" {
			fmt.Fprintf(&b, " (%s)", members)
		}
	}
	if isFunc {
		fmt.Fprintf(&b, "; the '%s' function is written 'x.%s'", name, name)
	}
	return b.String()
}

// memberList names the first few members of a module, which is enough to show the shape
// of the call that was meant. A script module's members are only known once it has run,
// so there the message stops at the rule.
func memberList(mod Value, name string) string {
	const show = 3
	keys := mod.Keys()
	if len(keys) == 0 {
		return ""
	}
	out := make([]string, 0, show+1)
	for _, k := range keys {
		if len(out) == show {
			out = append(out, "…")
			break
		}
		out = append(out, name+"."+k.Str())
	}
	return strings.Join(out, ", ")
}
