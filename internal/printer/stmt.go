package printer

import (
	"strconv"
	"strings"

	"mzs/internal/ast"
	"mzs/internal/token"
)

// stmtList writes a statement list at ind, then the comments standing between its last
// statement and stop — the `}` that closes it, or the end of the file. Without that last
// flush a remark written at the bottom of a block would climb out of it.
func (p *printer) stmtList(list []ast.Stmt, ind int, stop token.Pos) {
	for _, s := range list {
		p.leading(s.Pos().Offset, ind)
		p.blank(s.Pos().Line)
		p.writeStmt(s, ind)
	}
	p.leading(stop.Offset, ind)
}

// writeStmt writes one statement on its own line, including its indentation, its line
// break and the comment that ends it.
func (p *printer) writeStmt(s ast.Stmt, ind int) {
	from, to := s.Pos().Offset, s.End().Offset
	if end := p.heredocEnd(from, to); end > 0 {
		// The statement's text runs past the tree: the body lives on the lines below.
		p.verbatim(from, end, ind)
		p.nl()
		p.last = p.lineAt(end - 1)
		return
	}
	p.tab(ind)
	p.stmt(s, ind)
	line := s.End().Line
	p.trailing(line)
	p.nl()
	if p.last < line {
		p.last = line
	}
	// Anything the layout had no slot for — a comment inside a condition, say — is
	// written out here rather than carried into the next statement's leading run, so it
	// stays as near to where it was written as the shape of the output allows.
	p.leftovers(to, ind)
}

// leftovers writes the comments a statement's own layout passed over.
func (p *printer) leftovers(to, ind int) {
	for p.ci < len(p.cmts) && p.cmts[p.ci].Pos.Offset < to {
		c := p.cmts[p.ci]
		p.ci++
		p.tab(ind)
		p.w(strings.TrimRight(c.Text, " \t"))
		p.nl()
		p.last = c.End.Line
	}
}

// lineAt is the 1-based line the rune at off stands on.
func (p *printer) lineAt(off int) int {
	line := 1
	for i := 0; i < off && i < len(p.rs); i++ {
		if p.rs[i] == '\n' {
			line++
		}
	}
	return line
}

// stmt writes a statement without its indentation or its line break.
func (p *printer) stmt(s ast.Stmt, ind int) {
	switch x := s.(type) {
	case *ast.ExprStmt:
		p.writeExpr(x.X, ind)
	case *ast.ReturnStmt:
		p.keywordValue("return", x.X, ind)
	case *ast.BreakStmt:
		p.keywordValue("break", x.X, ind)
	case *ast.NextStmt:
		p.keywordValue("next", x.X, ind)
	case *ast.IncludeDecl:
		p.include(x)
	case *ast.ExportDecl:
		p.export(x, ind)
	case *ast.RecordDecl:
		p.w("record " + x.Name + "(" + p.params(x.Fields) + ")")
	case ast.Expr:
		// An `fn` declaration and a bare block are expressions too (§6.1), so both reach
		// writeExpr and both may stay on the line they were written on.
		p.writeExpr(x, ind)
	default:
		p.w(p.text(s))
	}
}

func (p *printer) keywordValue(kw string, x ast.Expr, ind int) {
	p.w(kw)
	if x == nil {
		return
	}
	p.w(" ")
	p.writeExpr(x, ind)
}

// include writes `include name` and `include name from "path"`. The path is copied from
// the source rather than requoted: a `$` in it is interpolation in one spelling and text
// in the other, and the author already chose (§3.7).
func (p *printer) include(x *ast.IncludeDecl) {
	p.w("include " + x.Name)
	if !x.HasPath {
		return
	}
	lit := p.slice(x.PathPos.Offset, x.Stop.Offset)
	if lit == "" {
		lit = strconv.Quote(x.Path)
	}
	p.w(" from " + strings.TrimRight(lit, " \t"))
}

func (p *printer) export(x *ast.ExportDecl, ind int) {
	p.w("export ")
	if x.Decl == nil {
		p.w(strings.Join(x.Names, ", "))
		return
	}
	p.stmt(x.Decl, ind)
}

func (p *printer) fnDecl(x *ast.FnDecl, ind int) {
	if x.Async {
		p.w("async ")
	}
	p.w("fn " + x.Name + "(" + p.params(x.Params) + ") ")
	p.writeBlock(x.Body, ind)
}

// params renders a parameter list. A default is an expression and is written flat: a
// parameter list that needed to break would be a signature worth shortening instead.
func (p *printer) params(ps []ast.Param) string {
	out := make([]string, len(ps))
	for i, prm := range ps {
		s := prm.Name
		if prm.Rest {
			s = "*" + s
		}
		if prm.Default != nil {
			s += " = " + p.mustFlat(prm.Default)
		}
		out[i] = s
	}
	return strings.Join(out, ", ")
}

// writeBlock writes `{ … }` with its statements one level in. An empty block keeps its
// braces on two lines, which is what the grammar leaves room for: `{}` in this position
// is the empty dict (§3.12).
func (p *printer) writeBlock(b *ast.BlockStmt, ind int) {
	p.w("{")
	p.nl()
	p.opened(b.Start.Line)
	p.stmtList(b.Stmts, ind+1, b.Stop)
	p.tab(ind)
	p.w("}")
}
