package lsp

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"mzs/internal/apidoc"
	"mzs/internal/ast"
	"mzs/internal/parser"
	"mzs/internal/token"
	"mzs/mzs"
)

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

// checkOptions is what the server compiles with: every capability installed.
//
// An editor cannot know which flags the program will be run with, and `include time`
// under a host with no clock is a diagnostic about a command line rather than about the
// file (§12.8). Showing it on every open buffer would train people to ignore the panel
// the good diagnostics arrive in, so the server answers the question it can answer —
// does this program compile — and leaves the flags to whoever types them.
func checkOptions() mzs.Options {
	o := mzs.DefaultOptions()
	o.EnableTime = true
	o.Now = time.Now
	o.Rand = rand.New(rand.NewSource(1))
	o.FS = checkFS{}
	// `include x from "./y.mzs"` needs a loader or it is a compile error naming the host
	// (§12.8), so there has to be one. This one reads nothing: the server compiles, it
	// does not run, and opening a neighbour's file to compile it would be the editor
	// running a program nobody asked it to run.
	o.ModuleLoader = func(from, path string) (string, string, error) { return path, "", nil }
	return o
}

// checkFS exists to be non-nil: `LookupModule` asks whether the host installed a
// filesystem, never uses it, and compiling a program calls nothing on it.
type checkFS struct{}

func (checkFS) Open(string) (io.ReadCloser, error)     { return nil, errNoFS }
func (checkFS) Create(string) (io.WriteCloser, error)  { return nil, errNoFS }
func (checkFS) Append(string) (io.WriteCloser, error)  { return nil, errNoFS }
func (checkFS) Stat(string) (bool, int64, bool, error) { return false, 0, false, errNoFS }
func (checkFS) List(string) ([]string, error)          { return nil, errNoFS }

var errNoFS = errors.New("mzs lsp: the language server does not read files")

// diagnose compiles the document and turns everything the compiler said into
// diagnostics: the errors of §17 and the warnings of §11.5, each pointing where the
// compiler pointed.
func diagnose(d *document) []diagnostic {
	in := mzs.New(checkOptions())
	prog, err := in.Compile(d.name(), d.text)
	out := []diagnostic{}
	for _, e := range flatten(err) {
		out = append(out, diagnostic{
			Range:    d.spanAt(e.Line, e.Col),
			Severity: severityError,
			Source:   "mzs",
			Code:     e.Kind,
			Message:  e.Msg,
		})
	}
	if prog != nil {
		for _, w := range prog.Warnings() {
			out = append(out, diagnostic{
				Range:    d.spanAt(w.Line, w.Col),
				Severity: severityWarning,
				Source:   "mzs",
				Message:  w.Msg,
			})
		}
	}
	return out
}

// flatten unpacks the errors.Join the compiler returns when recovery found several
// (§17), so an editor shows every one of them instead of the first.
func flatten(err error) []*mzs.Error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var out []*mzs.Error
		for _, part := range joined.Unwrap() {
			out = append(out, flatten(part)...)
		}
		return out
	}
	var e *mzs.Error
	if errors.As(err, &e) {
		return []*mzs.Error{e}
	}
	return []*mzs.Error{{Kind: "error", Msg: err.Error(), Line: 1, Col: 1}}
}

// spanAt is the range a diagnostic underlines: the word at that position, or one
// character when there is no word there. A caret under a whole name reads better than a
// caret under its first letter, and it is what the terminal renderer already does.
func (d *document) spanAt(line, col int) rng {
	start := d.lspPosition(line, col)
	text := d.lineText(line - 1)
	n := 0
	for _, r := range runeSuffix(text, col-1) {
		if !token.IsIdentPart(r) {
			break
		}
		n++
	}
	if n == 0 {
		n = 1
	}
	return rng{Start: start, End: d.lspPosition(line, col+n)}
}

func runeSuffix(s string, n int) []rune {
	rs := []rune(s)
	if n < 0 || n > len(rs) {
		return nil
	}
	return rs[n:]
}

// ---------------------------------------------------------------------------
// The word under the cursor
// ---------------------------------------------------------------------------

// word is what the cursor is on: the name itself, the name in front of the `.` that
// reaches it, and where the name is.
type word struct {
	name string
	recv string
	span rng
}

// wordAt reads the identifier under the cursor off the line it stands on rather than out
// of the token stream, because the file is being typed: half of it does not lex, and
// hover has to keep working while it does not.
func (d *document) wordAt(p position) (word, bool) {
	line := d.lineText(p.Line)
	rs := []rune(line)
	i := len([]rune(utf16Prefix(line, p.Character)))
	if i > len(rs) {
		return word{}, false
	}
	// A cursor just past the end of a word belongs to that word: that is where it sits
	// after typing one.
	if (i == len(rs) || !token.IsIdentPart(rs[i])) && i > 0 && token.IsIdentPart(rs[i-1]) {
		i--
	}
	if i >= len(rs) || !token.IsIdentPart(rs[i]) {
		return word{}, false
	}
	start := i
	for start > 0 && token.IsIdentPart(rs[start-1]) {
		start--
	}
	end := i
	for end < len(rs) && token.IsIdentPart(rs[end]) {
		end++
	}
	if !token.IsIdentStart(rs[start]) {
		return word{}, false
	}
	w := word{
		name: string(rs[start:end]),
		recv: receiverBefore(rs, start),
		span: rng{
			Start: position{Line: p.Line, Character: utf16Len(string(rs[:start]))},
			End:   position{Line: p.Line, Character: utf16Len(string(rs[:end]))},
		},
	}
	return w, true
}

// receiverBefore returns the name in front of the `.` or `?.` that precedes start, or ""
// when the name stands on its own. A receiver that is not a bare name — `f(x).upper` —
// answers "", which is honest: the server has no types and will not pretend otherwise.
func receiverBefore(rs []rune, start int) string {
	i := start
	for i > 0 && isSpace(rs[i-1]) {
		i--
	}
	if i == 0 || rs[i-1] != '.' {
		return ""
	}
	i--
	if i > 0 && rs[i-1] == '?' {
		i--
	}
	end := i
	for i > 0 && token.IsIdentPart(rs[i-1]) {
		i--
	}
	if i == end || !token.IsIdentStart(rs[i]) {
		return ""
	}
	return string(rs[i:end])
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }

// ---------------------------------------------------------------------------
// What a file declares
// ---------------------------------------------------------------------------

// decl is one name the file binds at its top level: a function, a record, an include, or
// a variable an ordinary assignment creates.
type decl struct {
	name      string
	detail    string
	kind      int // a symbolKind
	full      rng
	nameRange rng
	modPath   string // the path an `include … from` names, or ""
}

// declarations reads what a document binds **in its own top-level scope**. It is the
// whole of go-to-definition and half of completion, and it needs the file to parse: a
// tree is the only thing that knows a `fn` inside a `match` arm from the word "fn"
// inside a string.
//
// It reads the program's statement list rather than walking the tree, and that is the
// point: `x` assigned inside a function body is a local of that body (§8.2), and a walk
// would report it as something the file declares — offering it in completion at the top
// level, listing it in the outline, and sending go-to-definition into a scope the cursor
// is not in.
func declarations(d *document) []decl {
	// A file being typed does not parse, and parser.Parse answers nil for it. There is
	// nothing to read off a tree that does not exist, and the diagnostics already say why.
	prog, _ := parser.Parse(d.name(), d.text)
	if prog == nil {
		return nil
	}
	var out []decl
	seen := map[string]bool{}
	add := func(dc decl) {
		if dc.name == "" || seen[dc.name] {
			return
		}
		seen[dc.name] = true
		out = append(out, dc)
	}
	for _, stmt := range prog.Stmts {
		d.declare(stmt, add)
	}
	return out
}

// declare reads one top-level statement. `export` is unwrapped rather than skipped: what
// it exports is declared here too, and the tree spells `export fn f(…)` as a declaration
// inside the export (§12.8).
func (d *document) declare(stmt ast.Stmt, add func(decl)) {
	switch x := stmt.(type) {
	case *ast.ExportDecl:
		if x.Decl != nil {
			d.declare(x.Decl, add)
		}
	case *ast.FnDecl:
		if x.Name == "" {
			return
		}
		head := "fn"
		if x.Async {
			head = "async fn"
		}
		add(decl{
			name:      x.Name,
			detail:    fmt.Sprintf("%s %s(%s)", head, x.Name, paramList(x.Params)),
			kind:      symbolFunction,
			full:      d.nodeRange(x),
			nameRange: d.spanAt(x.NamePos.Line, x.NamePos.Col),
		})
	case *ast.RecordDecl:
		add(decl{
			name:      x.Name,
			detail:    fmt.Sprintf("record %s(%s)", x.Name, paramList(x.Fields)),
			kind:      symbolStruct,
			full:      d.nodeRange(x),
			nameRange: d.spanAt(x.NamePos.Line, x.NamePos.Col),
		})
	case *ast.IncludeDecl:
		detail := "include " + x.Name
		if x.HasPath {
			detail += " from " + strconv.Quote(x.Path)
		}
		add(decl{
			name:      x.Name,
			detail:    detail,
			kind:      symbolModule,
			full:      d.nodeRange(x),
			nameRange: d.spanAt(x.NamePos.Line, x.NamePos.Col),
			modPath:   x.Path,
		})
	case *ast.ExprStmt:
		assign, ok := x.X.(*ast.AssignExpr)
		if !ok {
			return
		}
		id, ok := assign.Target.(*ast.Ident)
		if !ok {
			return
		}
		add(decl{
			name:      id.Name,
			detail:    id.Name,
			kind:      symbolVariable,
			full:      d.nodeRange(id),
			nameRange: d.nodeRange(id),
		})
	}
}

func (d *document) nodeRange(n ast.Node) rng {
	return rng{
		Start: d.lspPosition(n.Pos().Line, n.Pos().Col),
		End:   d.lspPosition(n.End().Line, n.End().Col),
	}
}

func paramList(ps []ast.Param) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		s := p.Name
		if p.Rest {
			s = "*" + s
		}
		if p.Default != nil {
			s += " = …"
		}
		out[i] = s
	}
	return strings.Join(out, ", ")
}

// ---------------------------------------------------------------------------
// Hover
// ---------------------------------------------------------------------------

// hoverFor answers what a name is. The order is the order a reader means it in: a
// module's member first when the receiver names a module, then a method, then a global
// function, then a module, then a keyword, and last what this very file declared.
func hoverFor(d *document, w word) (string, bool) {
	a := apidoc.Load()
	switch {
	case w.recv != "" && a.IsModule(w.recv):
		if e, ok := a.Member(w.recv, w.name); ok {
			return e.Markdown(w.recv + "." + w.name), true
		}
	case w.recv != "":
		if e, kinds := a.Method(w.name); len(kinds) > 0 {
			return e.Markdown("."+w.name) + "  \non " + strings.Join(kinds, ", "), true
		}
	}
	for _, dc := range declarations(d) {
		if dc.name != w.name || w.recv != "" {
			continue
		}
		if dc.modPath == "" && a.IsModule(dc.name) {
			// `include json` binds a name this file declares, and what a reader wants to
			// know about it is what the module holds, not that the line above exists.
			return moduleMarkdown(a, dc.name), true
		}
		return "**" + dc.detail + "**  \nin this file", true
	}
	if w.recv == "" {
		if e, ok := a.Builtin(w.name); ok {
			return e.Markdown(w.name), true
		}
		if a.IsModule(w.name) {
			return moduleMarkdown(a, w.name), true
		}
		if isKeyword(w.name) {
			return "**" + w.name + "**  \nkeyword (SPEC §3.5)", true
		}
	}
	if e, kinds := a.Method(w.name); len(kinds) > 0 {
		return e.Markdown("."+w.name) + "  \non " + strings.Join(kinds, ", "), true
	}
	return "", false
}

func moduleMarkdown(a *apidoc.API, name string) string {
	var b strings.Builder
	b.WriteString("**module " + name + "**  \n`include " + name + "`")
	if gate := a.Gates[name]; gate != "" {
		b.WriteString(" — needs `" + gate + "`")
	}
	if members := a.MemberNames(name); len(members) > 0 {
		b.WriteString("  \n" + name + "." + strings.Join(members, ", "+name+"."))
	}
	return b.String()
}

func isKeyword(name string) bool {
	for _, k := range mzs.Keywords() {
		if k == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

// complete offers what may stand at the cursor. After a `.` the answer is a member list
// — the module's own when the receiver names one, and every method there is when it does
// not, because this server has no types and a guess would be worse than a list.
func complete(d *document, p position) []completionItem {
	a := apidoc.Load()
	line := d.lineText(p.Line)
	rs := []rune(utf16Prefix(line, p.Character))
	prefix, recv := completionContext(rs)

	var out []completionItem
	if recv != "" || afterDot(rs) {
		if recv != "" && a.IsModule(recv) {
			for _, n := range a.MemberNames(recv) {
				e, _ := a.Member(recv, n)
				out = append(out, item(n, kindFunction, recv+"."+n, e))
			}
			return filterByPrefix(out, prefix)
		}
		for _, n := range a.MethodNames() {
			e, kinds := a.Method(n)
			out = append(out, item(n, kindMethod, "."+n+" — "+strings.Join(kinds, ", "), e))
		}
		return filterByPrefix(out, prefix)
	}

	for _, dc := range declarations(d) {
		out = append(out, completionItem{Label: dc.name, Kind: declKind(dc.kind), Detail: dc.detail, SortText: "0" + dc.name})
	}
	for _, n := range a.BuiltinNames() {
		e, _ := a.Builtin(n)
		out = append(out, item(n, kindFunction, n, e))
	}
	for _, n := range a.ModuleNames() {
		out = append(out, completionItem{Label: n, Kind: kindModule, Detail: "module " + n, SortText: "2" + n})
	}
	for _, k := range mzs.Keywords() {
		out = append(out, completionItem{Label: k, Kind: kindKeyword, Detail: "keyword", SortText: "3" + k})
	}
	return filterByPrefix(out, prefix)
}

func declKind(symbol int) int {
	switch symbol {
	case symbolFunction:
		return kindFunction
	case symbolModule:
		return kindModule
	}
	return kindVariable
}

func item(name string, kind int, detail string, e apidoc.Entry) completionItem {
	ci := completionItem{Label: name, Kind: kind, Detail: detail, SortText: "1" + name}
	if !e.Empty() {
		ci.Documentation = &markupContent{Kind: "markdown", Value: e.Markdown(name)}
	}
	return ci
}

// completionContext reads back from the cursor: the word being typed, and the receiver
// in front of the `.` when there is one.
func completionContext(rs []rune) (prefix, recv string) {
	i := len(rs)
	for i > 0 && token.IsIdentPart(rs[i-1]) {
		i--
	}
	prefix = string(rs[i:])
	return prefix, receiverBefore(rs, i)
}

// afterDot reports whether the cursor stands right after a `.`, which is the trigger the
// client sends and the one case where a receiver it could not name is still a member
// list.
func afterDot(rs []rune) bool {
	i := len(rs)
	for i > 0 && token.IsIdentPart(rs[i-1]) {
		i--
	}
	return i > 0 && rs[i-1] == '.'
}

func filterByPrefix(items []completionItem, prefix string) []completionItem {
	if prefix == "" {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if strings.HasPrefix(it.Label, prefix) {
			out = append(out, it)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Go to definition and the outline
// ---------------------------------------------------------------------------

// definitionFor finds where a name was bound. A name an `include … from "path"` brought
// in resolves to that file from every reference to it, which is the one answer that
// leaves the document — the include statement itself is one line from the top and is not
// where anybody was trying to get to.
func definitionFor(d *document, w word) (location, bool) {
	for _, dc := range declarations(d) {
		if dc.name != w.name {
			continue
		}
		if dc.modPath != "" {
			if uri, ok := includeURI(d, dc.modPath); ok {
				return location{URI: uri, Range: rng{}}, true
			}
		}
		return location{URI: d.uri, Range: dc.nameRange}, true
	}
	return location{}, false
}

// includeURI resolves an include path against the including file, which is what the CLI's
// loader does (§15). A document with no path of its own — an untitled buffer — has
// nothing to resolve against.
func includeURI(d *document, path string) (string, bool) {
	base := d.path()
	if base == "" || filepath.IsAbs(path) {
		return "", false
	}
	target := filepath.Join(filepath.Dir(base), path)
	if _, err := os.Stat(target); err != nil {
		return "", false
	}
	return pathToURI(target), true
}

// symbolsOf is the outline: what the file declares, in source order.
func symbolsOf(d *document) []documentSymbol {
	decls := declarations(d)
	out := make([]documentSymbol, 0, len(decls))
	for _, dc := range decls {
		out = append(out, documentSymbol{
			Name:           dc.name,
			Detail:         dc.detail,
			Kind:           dc.kind,
			Range:          dc.full,
			SelectionRange: dc.nameRange,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Range.Start.Line != out[j].Range.Start.Line {
			return out[i].Range.Start.Line < out[j].Range.Start.Line
		}
		return out[i].Range.Start.Character < out[j].Range.Start.Character
	})
	return out
}
