package printer

import (
	"unicode/utf8"

	"mzs/internal/ast"
	"mzs/internal/token"
)

// writeExpr writes e starting at the current column, breaking it across lines when it
// does not fit and there is somewhere to break. ind is the indentation of the line the
// expression starts on; a continuation goes one level in from there.
func (p *printer) writeExpr(e ast.Expr, ind int) {
	if e == nil {
		return
	}
	if s, ok := p.flat(e); ok && p.col+utf8.RuneCountInString(s) <= Width {
		// Nothing is skipped here: a flat form is built out of the node's parts, so a
		// comment that stood inside the node is not in the text, and it stays pending for
		// leading() or leftovers() to write. (A comment cannot hide inside a literal's own
		// text either: `#` inside a string is text, and inside an interpolation it would
		// swallow the closing brace and fail to lex.)
		p.w(s)
		return
	}
	p.breakExpr(e, ind)
}

// breakExpr is writeExpr once the flat form has been ruled out: either it did not fit,
// or the node is one of the forms that never fits on a line — a block, a `match`, a
// statement list.
func (p *printer) breakExpr(e ast.Expr, ind int) {
	switch x := e.(type) {
	case *ast.IfExpr:
		p.ifExpr(x, ind)
	case *ast.WhileExpr:
		p.whileExpr(x, ind)
	case *ast.ForExpr:
		p.forExpr(x, ind)
	case *ast.MatchExpr:
		p.matchExpr(x, ind)
	case *ast.TryExpr:
		p.tryExpr(x, ind)
	case *ast.FnDecl:
		p.fnDecl(x, ind)
	case *ast.BlockStmt:
		p.writeBlock(x, ind)
	case *ast.FuncLit:
		p.funcLit(x, ind)
	case *ast.GroupExpr:
		p.group(x, ind)
	case *ast.ArrayLit:
		p.list("[", "]", x.Lbrack.Line, x.Rbrack.Offset, exprItems(x.Elems), ind)
	case *ast.DictLit:
		p.list("{", "}", x.Lbrack.Line, x.Rbrack.Offset, p.dictItems(x), ind)
	case *ast.MethodCall, *ast.CallExpr:
		p.callish(e, ind)
	case *ast.BinaryExpr:
		p.operands(x.L, x.Op, x.R, ind)
	case *ast.LogicalExpr:
		p.operands(x.L, x.Op, x.R, ind)
	case *ast.AssignExpr:
		p.writeExpr(x.Target, ind)
		p.w(" " + x.Op.String() + " ")
		p.writeExpr(x.Value, ind)
	case *ast.DestructureAssign:
		p.writeExpr(x.Pattern, ind)
		p.w(" " + x.Op.String() + " ")
		p.writeExpr(x.Value, ind)
	case *ast.TernaryExpr:
		// The operators end their lines rather than starting them: §3.10 swallows a
		// break after `?` and after `:`, and nothing swallows one in front of either.
		p.writeExpr(x.Cond, ind)
		p.w(" ?")
		p.nl()
		p.tab(ind + 1)
		p.writeExpr(x.Then, ind+1)
		p.w(" :")
		p.nl()
		p.tab(ind + 1)
		p.writeExpr(x.Else, ind+1)
	case *ast.IndexExpr:
		p.writeExpr(x.X, ind)
		p.w("[")
		p.writeExpr(x.Index, ind+1)
		if x.Index2 != nil {
			p.w(", ")
			p.writeExpr(x.Index2, ind+1)
		}
		p.w("]")
	default:
		// A name, a literal or an operand chain with no break point in it. Long is the
		// honest answer; folding it would only move the problem into the middle of a
		// word.
		p.w(p.text(e))
		p.skip(e.Pos().Offset, e.End().Offset)
	}
}

// ---------------------------------------------------------------------------
// Control flow
// ---------------------------------------------------------------------------

// modifier reports whether a control-flow node came from the statement modifier of §4.4
// — `x if c` rather than `if c { x }`. The tree is the same either way (§6.2), and the
// positions are what tell them apart: a modifier's keyword stands *after* its body.
func modifierForm(kw token.Pos, body *ast.BlockStmt) bool {
	return body != nil && len(body.Stmts) == 1 && kw.Offset > body.Start.Offset
}

func (p *printer) ifExpr(x *ast.IfExpr, ind int) {
	if x.Else == nil && modifierForm(x.Kw, x.Then) {
		p.stmt(x.Then.Stmts[0], ind)
		p.w(" if ")
		p.writeExpr(x.Cond, ind)
		return
	}
	p.w("if ")
	p.writeExpr(x.Cond, ind)
	p.w(" ")
	p.writeBlock(x.Then, ind)
	switch e := x.Else.(type) {
	case nil:
	case *ast.IfExpr:
		p.w(" else ")
		p.ifExpr(e, ind)
	case *ast.BlockStmt:
		p.w(" else ")
		p.writeBlock(e, ind)
	default:
		p.w(" else ")
		p.stmt(x.Else, ind)
	}
}

func (p *printer) whileExpr(x *ast.WhileExpr, ind int) {
	if modifierForm(x.Kw, x.Body) {
		p.stmt(x.Body.Stmts[0], ind)
		p.w(" while ")
		p.writeExpr(x.Cond, ind)
		return
	}
	p.w("while ")
	p.writeExpr(x.Cond, ind)
	p.w(" ")
	p.writeBlock(x.Body, ind)
}

func (p *printer) forExpr(x *ast.ForExpr, ind int) {
	p.w("for " + x.KeyVar)
	if x.ValVar != "" {
		p.w(", " + x.ValVar)
	}
	p.w(" in ")
	p.writeExpr(x.Iter, ind)
	p.w(" ")
	p.writeBlock(x.Body, ind)
}

func (p *printer) matchExpr(x *ast.MatchExpr, ind int) {
	p.w("match")
	if x.Subject != nil {
		p.w(" ")
		p.writeExpr(x.Subject, ind)
	}
	p.w(" {")
	p.nl()
	p.opened(matchBrace(x))
	for _, arm := range x.Arms {
		p.leading(arm.Pos.Offset, ind+1)
		p.blank(arm.Pos.Line)
		p.tab(ind + 1)
		p.arm(arm, ind+1)
		line := arm.Body.End().Line
		p.trailing(line)
		p.nl()
		if p.last < line {
			p.last = line
		}
	}
	p.leading(x.Stop.Offset, ind+1)
	p.tab(ind)
	p.w("}")
}

// arm writes one `match` arm. Every pattern of an `in` or an `if` arm carries the word
// itself, because that is what the parser asks for: one arm is one form (§5.3).
func (p *printer) arm(a ast.MatchArm, ind int) {
	switch a.Kind {
	case ast.ArmElse:
		p.w("else")
	default:
		word := ""
		switch a.Kind {
		case ast.ArmIn:
			word = "in "
		case ast.ArmGuard:
			word = "if "
		}
		for i, pat := range a.Pats {
			if i > 0 {
				p.w(", ")
			}
			p.w(word)
			p.writeExpr(pat, ind)
		}
		if a.Guard != nil {
			p.w(" if ")
			p.writeExpr(a.Guard, ind)
		}
	}
	p.w(" -> ")
	p.armBody(a.Body, ind)
}

// armBody writes `-> expr` where the source wrote one and `-> { … }` where it wrote a
// block. The tree spells both as a BlockStmt (§5.3); the brace is what its Start points
// at when there was one.
func (p *printer) armBody(b *ast.BlockStmt, ind int) {
	if p.isBlock(b) {
		// Through writeExpr, so a braced arm written on one line stays on it.
		p.writeExpr(b, ind)
		return
	}
	if len(b.Stmts) == 1 {
		p.stmt(b.Stmts[0], ind)
		return
	}
	p.writeBlock(b, ind)
}

func (p *printer) tryExpr(x *ast.TryExpr, ind int) {
	p.w("try ")
	p.clause(x.X, ind)
	if x.Fallback != nil {
		p.w(" else ")
		if x.Var != "" {
			p.w("(" + x.Var + ") ")
			if !p.isClause(x.Fallback) {
				p.w("-> ")
			}
		}
		p.clause(x.Fallback, ind)
	}
	if x.Ensure != nil {
		p.w(" ensure ")
		p.writeBlock(x.Ensure, ind)
	}
}

// clause writes one side of a `try`: the block the braced form wrote, or the expression
// the other one did.
func (p *printer) clause(e ast.Expr, ind int) {
	if b, ok := e.(*ast.BlockStmt); ok && p.isBlock(b) {
		p.writeBlock(b, ind)
		return
	}
	p.writeExpr(e, ind)
}

func (p *printer) isClause(e ast.Expr) bool {
	b, ok := e.(*ast.BlockStmt)
	return ok && p.isBlock(b)
}

// isBlock reports whether a BlockStmt is one the source wrote braces for, rather than
// one the parser wrapped an expression in (§5.3, §6.2).
func (p *printer) isBlock(b *ast.BlockStmt) bool {
	return b != nil && p.at(b.Start.Offset) == '{'
}

func (p *printer) funcLit(x *ast.FuncLit, ind int) {
	p.w("{")
	if !x.ImplicitIt {
		p.w(" (" + p.params(x.Params) + ") ->")
	}
	p.nl()
	p.opened(x.Start.Line)
	p.stmtList(x.Body.Stmts, ind+1, x.Body.Stop)
	p.tab(ind)
	p.w("}")
}

func (p *printer) group(x *ast.GroupExpr, ind int) {
	p.w("(")
	p.nl()
	p.opened(x.Lparen.Line)
	p.stmtList(x.Stmts, ind+1, x.Rparen)
	p.tab(ind)
	p.w(")")
}

// ---------------------------------------------------------------------------
// Calls, chains and collections
// ---------------------------------------------------------------------------

// callish breaks a call. A chain of two or more methods breaks by its dots, which is the
// shape the language is written in; anything else breaks by its arguments.
func (p *printer) callish(e ast.Expr, ind int) {
	if p.closureTail(e, ind) {
		return
	}
	base, links := chain(e)
	if len(links) >= 2 {
		p.chain(base, links, ind)
		return
	}
	switch x := e.(type) {
	case *ast.MethodCall:
		p.writeExpr(x.Recv, ind)
		p.w(p.dot(x) + x.Name)
		p.args(x.Args, x.Named, p.methodParens(x), x.NamePos.Line, p.bracketSpan(x), ind)
	case *ast.CallExpr:
		p.writeExpr(x.Fn, ind)
		p.args(x.Args, x.Named, p.hasParens(x.Lparen), x.Lparen.Line, p.bracketSpan(x), ind)
	}
}

// closureTail writes `xs.map { … }` with everything up to the brace on one line and the
// closure's body below it, and reports whether it did. It is what keeps the shape the
// language is written in: the call is short, the block is not, and breaking the chain by
// its dots because of a body ten lines long would move the wrong thing.
func (p *printer) closureTail(e ast.Expr, ind int) bool {
	var (
		head string
		ok   bool
		cl   *ast.FuncLit
	)
	switch x := e.(type) {
	case *ast.MethodCall:
		recv, rok := p.flat(x.Recv)
		if !rok {
			return false
		}
		cl, _ = p.trailingClosure(x.Args)
		head, ok = p.flatCallHead(recv+p.dot(x)+x.Name, x.Args, x.Named, p.methodParens(x))
	case *ast.CallExpr:
		fn, fok := p.flat(x.Fn)
		if !fok {
			return false
		}
		cl, _ = p.trailingClosure(x.Args)
		head, ok = p.flatCallHead(fn, x.Args, x.Named, p.hasParens(x.Lparen))
	default:
		return false
	}
	if !ok || cl == nil || p.col+utf8.RuneCountInString(head)+1 > Width {
		return false
	}
	p.w(head + " ")
	p.funcLit(cl, ind)
	return true
}

// chain flattens `a.b.c` into its receiver and its links, innermost first. A link with a
// receiver that is not itself a method call ends the walk: `f(x).g` chains off the call,
// and `(a + b).c` off the group.
func chain(e ast.Expr) (ast.Expr, []*ast.MethodCall) {
	var links []*ast.MethodCall
	for {
		m, ok := e.(*ast.MethodCall)
		if !ok || m.Recv == nil {
			break
		}
		links = append(links, m)
		e = m.Recv
	}
	for i, j := 0, len(links)-1; i < j; i, j = i+1, j-1 {
		links[i], links[j] = links[j], links[i]
	}
	return e, links
}

// chain writes a broken method chain: the receiver and its first link stay together, and
// every link after that starts a line with its own dot. §3.10 drops the line break in
// front of a leading `.`, which is what makes this parse.
func (p *printer) chain(base ast.Expr, links []*ast.MethodCall, ind int) {
	p.writeExpr(base, ind)
	for i, m := range links {
		if i > 0 {
			p.nl()
			p.tab(ind + 1)
		}
		p.w(p.dot(m) + m.Name)
		p.args(m.Args, m.Named, p.methodParens(m), m.NamePos.Line, p.bracketSpan(m), ind+1)
	}
}

func (p *printer) dot(m *ast.MethodCall) string {
	if m.Safe {
		return "?."
	}
	return "."
}

// methodParens reports whether a method call needs its parentheses. A call with no
// arguments does not — `x.upper` is the spelling §4.2 gives it — and one with named
// arguments does, since a name binds a parameter at the call (§8.7).
func (p *printer) methodParens(m *ast.MethodCall) bool {
	if len(m.Named) > 0 {
		return true
	}
	args := m.Args
	if cl, rest := p.trailingClosure(args); cl != nil {
		args = rest
	}
	return len(args) > 0
}

// args writes an argument list, breaking one argument to a line when the flat form does
// not fit. A trailing closure is written after the parentheses, where it was written.
func (p *printer) args(all []ast.Expr, named []ast.NamedArg, parens bool, line int, sp span, ind int) {
	closure, args := p.trailingClosure(all)
	if parens || len(args) > 0 || len(named) > 0 {
		items := callItems(args, named)
		// A comment between the parentheses goes straight to the broken form, which has a
		// line for every argument and one more before the `)`. Only when there is none may
		// the list take its own chance to fit — this is reached from a chain that broke by
		// its dots, where each link starts a fresh line and most of them are short.
		flat := ""
		fits := false
		if !p.hasComment(sp.from, sp.to) {
			s, ok := p.flatList("(", ")", items)
			flat, fits = s, ok && p.col+utf8.RuneCountInString(s) <= Width
		}
		switch {
		case fits:
			p.w(flat)
		case p.hug(items, ind):
		default:
			p.list("(", ")", line, sp.to, items, ind)
		}
	}
	if closure != nil {
		p.w(" ")
		p.writeExpr(closure, ind)
	}
}

// hug writes `f(a, {` … `})`: an argument list whose last argument is a collection or a
// function keeps everything in front of it on the line the call started on and breaks
// only the last one. It is the shape `http.serve(":8080", { … })` is written in, and the
// alternative — every argument on a line of its own — pushes a page of routes two levels
// in to buy nothing.
func (p *printer) hug(items []item, ind int) bool {
	if len(items) == 0 {
		return false
	}
	if p.hasComment(items[0].pos.Offset, items[len(items)-1].pos.Offset) {
		// A comment between two arguments needs the line every argument gets.
		return false
	}
	last := items[len(items)-1]
	if last.value == nil || !huggable(last.value) {
		return false
	}
	head := "("
	for _, it := range items[:len(items)-1] {
		s, ok := p.flat(it.value)
		if !ok {
			return false
		}
		head += it.prefix + s + ", "
	}
	head += last.prefix
	if p.col+utf8.RuneCountInString(head) > Width {
		return false
	}
	p.w(head)
	p.writeExpr(last.value, ind)
	p.w(")")
	return true
}

// huggable is what may be the argument left hanging: a value whose own text is a block
// with a closing bracket of its own to land the `)` on.
func huggable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.DictLit, *ast.ArrayLit, *ast.FuncLit, *ast.FnDecl:
		return true
	}
	return false
}

// trailingClosure separates the `{ … }` written after a call's parentheses from the
// arguments written inside them (§4.2). The tree appends both to Args, so the source is
// what tells them apart: what stands before the brace is `)` or the callee's own name.
func (p *printer) trailingClosure(args []ast.Expr) (*ast.FuncLit, []ast.Expr) {
	if len(args) == 0 {
		return nil, args
	}
	lit, ok := args[len(args)-1].(*ast.FuncLit)
	if !ok {
		return nil, args
	}
	for i := lit.Pos().Offset - 1; i >= 0; i-- {
		switch r := p.rs[i]; r {
		case ' ', '\t', '\n', '\r', '\u00a0':
			continue
		case '(', ',':
			return nil, args
		default:
			return lit, args[:len(args)-1]
		}
	}
	return nil, args
}

// item is one element of a bracketed list: the text before its expression (a key, a
// name), the expression itself, and where it began in the source, for its comments.
type item struct {
	prefix string
	value  ast.Expr
	pos    token.Pos
	// bare is an item with no expression of its own — a value already rendered.
	bare string
}

func exprItems(es []ast.Expr) []item {
	out := make([]item, len(es))
	for i, e := range es {
		out[i] = item{value: e, pos: e.Pos()}
	}
	return out
}

func callItems(args []ast.Expr, named []ast.NamedArg) []item {
	out := exprItems(args)
	for _, n := range named {
		out = append(out, item{prefix: n.Name + " = ", value: n.Value, pos: n.NamePos})
	}
	return out
}

// dictItems renders the keys, which decide the separator: a string key takes `:`, a
// literal of any other type takes `->`, and anything computed takes `:` with the
// parentheses the parser read it through (§3.12).
func (p *printer) dictItems(d *ast.DictLit) []item {
	out := make([]item, 0, len(d.Keys))
	for i, k := range d.Keys {
		var prefix string
		switch {
		case isStrKey(k):
			prefix = p.text(k) + ": "
		case isLiteralKey(k):
			prefix = p.mustFlat(k) + " -> "
		default:
			prefix = "(" + p.mustFlat(k) + "): "
		}
		out = append(out, item{prefix: prefix, value: d.Vals[i], pos: k.Pos()})
	}
	return out
}

func isStrKey(e ast.Expr) bool { _, ok := e.(*ast.StrLit); return ok }

func isLiteralKey(e ast.Expr) bool {
	if u, ok := e.(*ast.UnaryExpr); ok {
		e = u.X
	}
	switch e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.NilLit, *ast.RegexLit:
		return true
	}
	return false
}

// list writes a bracketed list broken one item to a line, with the trailing comma the
// grammar allows everywhere it allows a list (§3.12, §4.1).
func (p *printer) list(open, close string, line, closeOff int, items []item, ind int) {
	p.w(open)
	if len(items) == 0 {
		p.w(close)
		return
	}
	if len(items) == 1 {
		// Breaking a list of one helps only when the one fits on a line of its own.
		// `println("…")` with a string longer than the page is not improved by putting
		// the string on the next line and the `)` on the one after it.
		if s, ok := p.flat(items[0].value); ok {
			if (ind+1)*Indent+utf8.RuneCountInString(items[0].prefix+s)+1 > Width {
				p.w(items[0].prefix + s)
				p.w(close)
				return
			}
		}
	}
	p.nl()
	p.opened(line)
	for _, it := range items {
		p.leading(it.pos.Offset, ind+1)
		p.tab(ind + 1)
		p.w(it.prefix)
		if it.value != nil {
			p.writeExpr(it.value, ind+1)
		} else {
			p.w(it.bare)
		}
		p.w(",")
		if it.value != nil {
			line := it.value.End().Line
			p.trailing(line)
			if p.last < line {
				// So that a comment on the line below this element is next to it and not
				// a blank line away from it.
				p.last = line
			}
		}
		p.nl()
	}
	p.leading(closeOff, ind+1)
	p.tab(ind)
	p.w(close)
}

// operands breaks a binary or logical expression after its operator, which §3.10 lets a
// line end on. Only the top operator breaks: `a && b || c` becomes two lines, not four.
func (p *printer) operands(l ast.Expr, op token.Kind, r ast.Expr, ind int) {
	p.writeExpr(l, ind)
	p.w(" " + op.String())
	p.nl()
	p.tab(ind + 1)
	p.writeExpr(r, ind+1)
}

// matchBrace is the line a `match`'s `{` stands on: the end of its subject, or the
// keyword itself when it has none (§5.4).
func matchBrace(x *ast.MatchExpr) int {
	if x.Subject != nil {
		return x.Subject.End().Line
	}
	return x.Kw.Line
}
