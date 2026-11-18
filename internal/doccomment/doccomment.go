// Package doccomment reads the documentation a script writes for itself (RFC 0019 §2): a
// run of `##` comment lines directly above a declaration.
//
//	## Total of a cart in the cart's currency.
//	## Raises kind "billing" when currencies are mixed.
//	export fn total(items) { … }
//
// A plain `#` stays an ordinary comment, and nothing here changes what a program means:
// the scanner skips every comment (§3.2) and records it, and this package only reads that
// record next to the tree. `mzs lsp` shows the result on hover and in completion, and
// `mzs doc` prints it.
package doccomment

import (
	"strings"

	"mzs/internal/ast"
	"mzs/internal/lexer"
	"mzs/internal/parser"
	"mzs/internal/token"
)

// Kinds of Item.
const (
	KindFn     = "fn"
	KindRecord = "record"
	KindValue  = "value"
)

// Item is one documented name at the top level of a file.
type Item struct {
	Name string
	// Kind is KindFn, KindRecord or KindValue: a binding an assignment creates.
	Kind string
	// Sig is the declaration as a reader would write it to call it — `fn total(items,
	// cur = "USD")`, `record Money(amount, cur)` — with each default as it was written.
	Sig string
	// Doc is the comment text with the `##` markers taken off, its lines joined by "\n".
	// The first line is the summary; the rest is Markdown and is passed on as it is.
	Doc string
	// Exported says the file's exports include the name (§12.8).
	Exported bool
	// Line is where the declaration starts, one-based.
	Line int
}

// Summary is the first line of the doc text.
func (it Item) Summary() string {
	first, _, _ := strings.Cut(it.Doc, "\n")
	return first
}

// Docs answers which doc comment stands directly above a line.
type Docs struct {
	// byLast maps the line a run's last comment is on to the run's text.
	byLast map[int]string
}

// Index collects the `##` runs among a file's comments. A run is consecutive lines each
// holding nothing but a doc comment; a remark at the end of a line of code is not one,
// and a blank line ends a run.
func Index(comments []lexer.Comment) Docs {
	d := Docs{byLast: map[int]string{}}
	var lines []string
	last := 0
	flush := func() {
		if len(lines) > 0 {
			d.byLast[last] = strings.Join(lines, "\n")
		}
		lines = nil
	}
	for _, c := range comments {
		text, ok := docText(c)
		if !ok {
			flush()
			continue
		}
		if len(lines) > 0 && c.Pos.Line != last+1 {
			flush()
		}
		lines = append(lines, text)
		last = c.Pos.Line
	}
	flush()
	return d
}

// Above returns the doc comment whose last line is directly above line, or "".
func (d Docs) Above(line int) string { return d.byLast[line-1] }

// docText reports whether c is a doc comment and returns its text. `##` must be followed
// by a space or end the comment: `###` and a `####…` rule are banners, not
// documentation.
func docText(c lexer.Comment) (string, bool) {
	if !c.OwnLine || !strings.HasPrefix(c.Text, "##") {
		return "", false
	}
	rest := c.Text[2:]
	switch {
	case rest == "":
		return "", true
	case rest[0] == ' ':
		return strings.TrimRight(rest[1:], " \t"), true
	}
	return "", false
}

// Parse reads a file's documented names. It answers the parser's error for a file that
// does not parse.
func Parse(name, src string) ([]Item, error) {
	toks, comments, _, lexErrs := lexer.Scan(name, src)
	if len(lexErrs) > 0 {
		// The parser reports lexer errors with its own wording and position; asking it
		// keeps one spelling of the message.
		_, err := parser.Parse(name, src)
		if err != nil {
			return nil, err
		}
	}
	prog, err := parser.ParseTokens(name, toks)
	if err != nil {
		return nil, err
	}
	return Extract(prog, src, comments), nil
}

// Extract reads the top-level declarations of prog and the doc comment above each. src
// is the text prog was parsed from, which a signature quotes its defaults out of.
//
// Every `fn` and `record` is listed, documented or not, because its signature is
// documentation on its own. A binding an assignment creates is listed when it is
// documented or exported: an undocumented local helper value is not part of the file's
// interface. A name is listed once, at its first declaration.
func Extract(prog *ast.Program, src string, comments []lexer.Comment) []Item {
	if prog == nil {
		return nil
	}
	docs := Index(comments)
	runes := []rune(src)
	exported := map[string]bool{}
	for _, s := range prog.Stmts {
		if e, ok := s.(*ast.ExportDecl); ok {
			for _, n := range e.Names {
				exported[n] = true
			}
		}
	}

	var out []Item
	at := map[string]int{}
	add := func(it Item) {
		if it.Name == "" {
			return
		}
		if i, seen := at[it.Name]; seen {
			// `export total` under an undocumented `fn total` may carry the comment.
			if out[i].Doc == "" {
				out[i].Doc = it.Doc
			}
			return
		}
		it.Exported = exported[it.Name]
		at[it.Name] = len(out)
		out = append(out, it)
	}
	for _, s := range prog.Stmts {
		line := s.Pos().Line
		doc := docs.Above(line)
		if e, ok := s.(*ast.ExportDecl); ok {
			if e.Decl == nil {
				for _, n := range e.Names {
					if i, seen := at[n]; seen && out[i].Doc == "" {
						out[i].Doc = doc
					}
				}
				continue
			}
			s = e.Decl
		}
		switch x := s.(type) {
		case *ast.FnDecl:
			head := "fn "
			if x.Async {
				head = "async fn "
			}
			add(Item{Name: x.Name, Kind: KindFn, Sig: head + x.Name + "(" + params(x.Params, runes) + ")", Doc: doc, Line: line})
		case *ast.RecordDecl:
			add(Item{Name: x.Name, Kind: KindRecord, Sig: "record " + x.Name + "(" + params(x.Fields, runes) + ")", Doc: doc, Line: line})
		case *ast.ExprStmt:
			a, ok := x.X.(*ast.AssignExpr)
			if !ok || (a.Op != token.ASSIGN && a.Op != token.DECLARE) {
				// `x += 1` updates a binding; only the statement that creates one
				// declares it.
				continue
			}
			id, ok := a.Target.(*ast.Ident)
			if !ok || (doc == "" && !exported[id.Name]) {
				continue
			}
			add(Item{Name: id.Name, Kind: KindValue, Sig: id.Name, Doc: doc, Line: line})
		}
	}
	return out
}

// params spells a parameter list with each default quoted from the source. A default
// that spans lines is shown as `…`: a signature is one line.
func params(ps []ast.Param, src []rune) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		s := p.Name
		if p.Rest {
			s = "*" + s
		}
		if p.Default != nil {
			s += " = " + quote(p.Default, src)
		}
		out[i] = s
	}
	return strings.Join(out, ", ")
}

func quote(x ast.Expr, src []rune) string {
	from, to := x.Pos().Offset, x.End().Offset
	if from < 0 || to > len(src) || from >= to {
		return "…"
	}
	text := string(src[from:to])
	if strings.ContainsAny(text, "\n\r") {
		return "…"
	}
	return text
}
