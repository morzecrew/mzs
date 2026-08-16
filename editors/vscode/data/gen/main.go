// Command gen dumps the live registry of the interpreter: what methods each kind
// answers, what global builtins exist, and what every built-in module offers. parse.js
// joins it with the prose of SPEC §12 and writes ../api.json.
//
// Names come from here rather than from the spec so the extension can never suggest a
// method that does not exist; descriptions come from the spec so it can never invent one.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"time"

	"mzs/mzs"
)

func main() {
	kinds := map[string]mzs.Kind{
		"nil": mzs.KNil, "bool": mzs.KBool, "int": mzs.KInt, "float": mzs.KFloat,
		"string": mzs.KString, "regex": mzs.KRegex, "array": mzs.KArray,
		"dict": mzs.KDict, "fn": mzs.KFunc, "time": mzs.KTime, "range": mzs.KRange,
		"task": mzs.KTask, "seq": mzs.KSeq, "any": mzs.KAny,
	}

	methods := map[string][]string{}
	for name, k := range kinds {
		if ns := mzs.MethodNames(k); len(ns) > 0 {
			methods[name] = ns
		}
	}

	// Every capability on, so the dump lists every module. What each one *needs* is
	// probed below rather than hardcoded, so a new gate cannot go unnoticed here.
	full := mzs.DefaultOptions()
	full.EnableTime = true
	full.Now, full.Rand = time.Now, rand.New(rand.NewSource(1))
	full.FS = probeFS{}

	modules := map[string][]string{}
	gates := map[string]string{}
	for _, name := range mzs.ModuleNames(&full) {
		mod, ok := mzs.LookupModule(name, &full)
		if !ok {
			continue
		}
		members := make([]string, 0, mod.Len())
		for _, k := range mod.Keys() {
			members = append(members, k.Str())
		}
		sort.Strings(members)
		modules[name] = members
		gates[name] = gateOf(name)
	}

	out := map[string]any{
		"methods":  methods,
		"builtins": mzs.BuiltinNames(),
		"modules":  modules,
		"gates":    gates,
		"keywords": keywords(),
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

// gateOf reports what a host must switch on for this module to exist, by asking the
// registry under narrower and narrower options (§12.8).
func gateOf(name string) string {
	bare := mzs.DefaultOptions()
	if _, ok := mzs.LookupModule(name, &bare); ok {
		return ""
	}
	withTime := mzs.DefaultOptions()
	withTime.EnableTime, withTime.Now = true, time.Now
	if _, ok := mzs.LookupModule(name, &withTime); ok {
		return "--time"
	}
	withRand := mzs.DefaultOptions()
	withRand.Rand = rand.New(rand.NewSource(1))
	if _, ok := mzs.LookupModule(name, &withRand); ok {
		return "--rand"
	}
	// A module gated on Options.FS needs nothing *typed*: the CLI installs a filesystem
	// itself, because on a command line the host is the person typing it (§12.13). The
	// string here is what a reader of the extension has to do, and for `io` that is
	// nothing — the capability is the embedder's problem, not the user's.
	withFS := mzs.DefaultOptions()
	withFS.FS = probeFS{}
	if _, ok := mzs.LookupModule(name, &withFS); ok {
		return ""
	}
	return "?"
}

// probeFS exists to be non-nil. Nothing here is ever called: LookupModule only asks
// whether the field was filled in.
type probeFS struct{}

func (probeFS) Open(string) (io.ReadCloser, error)     { return nil, errNotUsed }
func (probeFS) Create(string) (io.WriteCloser, error)  { return nil, errNotUsed }
func (probeFS) Append(string) (io.WriteCloser, error)  { return nil, errNotUsed }
func (probeFS) Stat(string) (bool, int64, bool, error) { return false, 0, false, errNotUsed }
func (probeFS) List(string) ([]string, error)          { return nil, errNotUsed }

var errNotUsed = errors.New("gen: the probe filesystem is never read")

// keywords is §3.5 straight from the token table, so the extension's list cannot drift
// from the lexer's.
func keywords() []string {
	return mzs.Keywords()
}
