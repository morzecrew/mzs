// Package apidoc is the standard library as the tools see it: what every name is
// spelled, what it takes and what it answers.
//
// The data is generated, never written by hand — the names come from the interpreter's
// own registry (`mzs.MethodNames`, `BuiltinNames`, `ModuleNames`) and the prose from the
// tables of SPEC §12, so a tool cannot offer a method that does not exist and cannot
// invent a description for one that does. `editors/vscode/data/gen` builds it.
//
// The same file is checked in twice: here, where `mzs lsp` compiles it into the binary,
// and in `editors/vscode/data`, where the extension reads it out of its own package. One
// copy cannot serve both — a `.vsix` carries no Go binary and a Go binary carries no
// `.vsix` — so the generator writes both and TestFileMatchesEditorCopy fails when they
// drift.
package apidoc

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

//go:embed api.json
var raw []byte

// Entry is one name's documentation: its signature, what it means, and an example. Any
// of the three may be empty — SPEC §12 has a row for almost every name, and "almost" is
// what the generator reports when it runs.
type Entry struct {
	Sig string `json:"sig"`
	Sem string `json:"sem"`
	Ex  string `json:"ex"`
}

// Empty reports whether the entry says nothing at all.
func (e Entry) Empty() bool { return e.Sig == "" && e.Sem == "" && e.Ex == "" }

// API is the whole table: methods by receiver kind, the global builtins, the members of
// every built-in module, the flag each module needs, and the keywords.
type API struct {
	Methods  map[string]map[string]Entry `json:"methods"`
	Builtins map[string]Entry            `json:"builtins"`
	Modules  map[string]map[string]Entry `json:"modules"`
	Gates    map[string]string           `json:"gates"`
	Keywords []string                    `json:"keywords"`
}

var (
	once   sync.Once
	loaded API
)

// Load returns the table. It is parsed once and shared: the value is read-only.
func Load() *API {
	once.Do(func() {
		if err := json.Unmarshal(raw, &loaded); err != nil {
			// The file is generated and embedded at build time, so a parse failure is a
			// broken build rather than a runtime condition. An empty table degrades the
			// tools that read it to names-only instead of taking the server down.
			loaded = API{}
		}
	})
	return &loaded
}

// Method returns what is known about a method name, together with the receiver kinds
// that answer it. A name several kinds share — `len`, `map` — is documented once per
// kind, and the first non-empty entry is the one worth showing.
func (a *API) Method(name string) (Entry, []string) {
	var (
		best  Entry
		kinds []string
	)
	for kind, table := range a.Methods {
		e, ok := table[name]
		if !ok {
			continue
		}
		kinds = append(kinds, kind)
		if best.Empty() {
			best = e
		}
	}
	sort.Strings(kinds)
	return best, kinds
}

// Builtin returns what is known about a global function of §12.1.
func (a *API) Builtin(name string) (Entry, bool) {
	e, ok := a.Builtins[name]
	return e, ok
}

// Member returns what is known about `module.name`.
func (a *API) Member(module, name string) (Entry, bool) {
	table, ok := a.Modules[module]
	if !ok {
		return Entry{}, false
	}
	e, ok := table[name]
	return e, ok
}

// IsModule reports whether name is a built-in module (§12.8).
func (a *API) IsModule(name string) bool {
	_, ok := a.Modules[name]
	return ok
}

// MemberNames lists a module's members, sorted.
func (a *API) MemberNames(module string) []string {
	out := make([]string, 0, len(a.Modules[module]))
	for n := range a.Modules[module] {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ModuleNames lists the built-in modules, sorted.
func (a *API) ModuleNames() []string {
	out := make([]string, 0, len(a.Modules))
	for n := range a.Modules {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// MethodNames lists every method name any kind answers to, sorted and without repeats.
func (a *API) MethodNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, table := range a.Methods {
		for n := range table {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// BuiltinNames lists the global functions, sorted.
func (a *API) BuiltinNames() []string {
	out := make([]string, 0, len(a.Builtins))
	for n := range a.Builtins {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Markdown renders an entry the way an editor shows it: the signature as code, the
// meaning as a sentence, the example on its own line. The strings already carry the
// backticks SPEC §12 wrote them with.
func (e Entry) Markdown(title string) string {
	var b strings.Builder
	b.WriteString("**" + title + "**")
	if e.Sig != "" {
		b.WriteString("  \n" + e.Sig)
	}
	if e.Sem != "" {
		b.WriteString("  \n" + e.Sem)
	}
	if e.Ex != "" {
		b.WriteString("  \n\n" + e.Ex)
	}
	return b.String()
}
