package printer

import (
	"strings"

	"mzs/internal/ast"
)

// flat renders e on one line, or reports that it cannot be. "Cannot" is three things: a
// form that is a statement list at heart (a block, a `match`, a `for`), a span holding a
// comment — the line would end at the `#` — and a span holding a heredoc, whose body is
// on the lines below and cannot follow the tag onto one.
//
// It writes nothing and consumes nothing, so a caller may ask and then throw the answer
// away; that is what makes "try flat, then break" a decision rather than a rewind.
func (p *printer) flat(e ast.Expr) (string, bool) {
	if e == nil {
		return "", true
	}
	from, to := e.Pos().Offset, e.End().Offset
	if p.hasComment(from, to) || p.hasHeredoc(from, to) {
		return "", false
	}
	if braced(e) && e.Pos().Line != e.End().Line {
		// A body written across lines stays across lines. The formatter breaks a brace
		// that has grown too long for one and never closes one the author opened: where
		// a program is cut into steps is a claim about the program, not about spacing.
		return "", false
	}
	return p.flatNode(e)
}

// braced reports whether e is one of the forms whose body is a `{ … }` of its own.
func braced(e ast.Expr) bool {
	switch e.(type) {
	case *ast.BlockStmt, *ast.FuncLit, *ast.IfExpr, *ast.WhileExpr,
		*ast.ForExpr, *ast.MatchExpr, *ast.TryExpr, *ast.FnDecl:
		return true
	}
	return false
}

func (p *printer) flatNode(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.NilLit, *ast.BoolLit, *ast.IntLit, *ast.FloatLit, *ast.StrLit, *ast.RegexLit:
		// Copied, not rebuilt: `0xff` is not 255, `1.50` is not 1.5, and a single-quoted
		// string does not grow escapes it was written without (§3.6, §3.7).
		return p.text(e), true
	case *ast.Ident:
		return x.Name, true
	case *ast.GlobalVar:
		return x.Name, true
	case *ast.ArrayLit:
		return p.flatList("[", "]", exprItems(x.Elems))
	case *ast.DictLit:
		return p.flatList("{", "}", p.dictItems(x))
	case *ast.UnaryExpr:
		s, ok := p.flat(x.X)
		if !ok {
			return "", false
		}
		if strings.HasPrefix(s, x.Op.String()) {
			// `- -x` keeps its space: `--` is not an operator here, and gluing them
			// together would be a different token stream (§3.9).
			return x.Op.String() + " " + s, true
		}
		return x.Op.String() + s, true
	case *ast.BinaryExpr:
		return p.flatBinary(x.L, x.Op.String(), x.R)
	case *ast.LogicalExpr:
		return p.flatBinary(x.L, x.Op.String(), x.R)
	case *ast.AssignExpr:
		return p.flatBinary(x.Target, x.Op.String(), x.Value)
	case *ast.DestructureAssign:
		return p.flatBinary(x.Pattern, x.Op.String(), x.Value)
	case *ast.RangeExpr:
		op := ".."
		if x.Exclusive {
			op = "..<"
		}
		lo, ok1 := p.flat(x.Lo)
		hi, ok2 := p.flat(x.Hi)
		if !ok1 || !ok2 {
			return "", false
		}
		return lo + op + hi, true
	case *ast.TernaryExpr:
		c, ok1 := p.flat(x.Cond)
		t, ok2 := p.flat(x.Then)
		f, ok3 := p.flat(x.Else)
		if !ok1 || !ok2 || !ok3 {
			return "", false
		}
		return c + " ? " + t + " : " + f, true
	case *ast.ArrayPattern:
		parts, ok := p.flatEach(x.Elems)
		if !ok {
			return "", false
		}
		if x.Brackets {
			return "[" + strings.Join(parts, ", ") + "]", true
		}
		return strings.Join(parts, ", "), true
	case *ast.IndexExpr:
		recv, ok := p.flat(x.X)
		if !ok {
			return "", false
		}
		i, ok := p.flat(x.Index)
		if !ok {
			return "", false
		}
		if x.Index2 != nil {
			j, ok := p.flat(x.Index2)
			if !ok {
				return "", false
			}
			return recv + "[" + i + ", " + j + "]", true
		}
		return recv + "[" + i + "]", true
	case *ast.CallExpr:
		fn, ok := p.flat(x.Fn)
		if !ok {
			return "", false
		}
		return p.flatCall(fn, x.Args, x.Named, p.at(x.Lparen.Offset) == '(')
	case *ast.MethodCall:
		recv, ok := p.flat(x.Recv)
		if !ok {
			return "", false
		}
		return p.flatCall(recv+p.dot(x)+x.Name, x.Args, x.Named, p.methodParens(x))
	case *ast.FuncLit:
		return p.flatFuncLit(x)
	case *ast.GroupExpr:
		parts, ok := p.flatStmts(x.Stmts)
		if !ok {
			return "", false
		}
		return "(" + strings.Join(parts, "; ") + ")", true
	case *ast.BlockStmt:
		return p.flatBlock(x)
	case *ast.FnDecl:
		body, ok := p.flatBlock(x.Body)
		if !ok {
			return "", false
		}
		head := "fn " + x.Name + "(" + p.params(x.Params) + ") "
		if x.Async {
			head = "async " + head
		}
		return head + body, true
	case *ast.IfExpr:
		return p.flatIf(x)
	case *ast.WhileExpr:
		if modifierForm(x.Kw, x.Body) {
			return p.flatModifier(x.Body, "while", x.Cond)
		}
		cond, ok := p.flat(x.Cond)
		if !ok {
			return "", false
		}
		body, ok := p.flatBlock(x.Body)
		if !ok {
			return "", false
		}
		return "while " + cond + " " + body, true
	case *ast.ForExpr:
		iter, ok := p.flat(x.Iter)
		if !ok {
			return "", false
		}
		body, ok := p.flatBlock(x.Body)
		if !ok {
			return "", false
		}
		vars := x.KeyVar
		if x.ValVar != "" {
			vars += ", " + x.ValVar
		}
		return "for " + vars + " in " + iter + " " + body, true
	case *ast.MatchExpr:
		return p.flatMatch(x)
	case *ast.TryExpr:
		return p.flatTry(x)
	}
	// Everything left is a form built out of a statement list: those are written across
	// lines whatever their length (§4).
	return "", false
}

func (p *printer) flatBinary(l ast.Expr, op string, r ast.Expr) (string, bool) {
	a, ok1 := p.flat(l)
	b, ok2 := p.flat(r)
	if !ok1 || !ok2 {
		return "", false
	}
	return a + " " + op + " " + b, true
}

func (p *printer) flatEach(es []ast.Expr) ([]string, bool) {
	out := make([]string, len(es))
	for i, e := range es {
		s, ok := p.flat(e)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func (p *printer) flatList(open, close string, items []item) (string, bool) {
	parts := make([]string, len(items))
	for i, it := range items {
		s := it.bare
		if it.value != nil {
			v, ok := p.flat(it.value)
			if !ok {
				return "", false
			}
			s = v
		}
		parts[i] = it.prefix + s
	}
	return open + strings.Join(parts, ", ") + close, true
}

func (p *printer) flatCall(head string, all []ast.Expr, named []ast.NamedArg, parens bool) (string, bool) {
	closure, _ := p.trailingClosure(all)
	out, ok := p.flatCallHead(head, all, named, parens)
	if !ok {
		return "", false
	}
	if closure != nil {
		s, ok := p.flat(closure)
		if !ok {
			return "", false
		}
		out += " " + s
	}
	return out, true
}

// flatCallHead is flatCall without the trailing closure: the callee, and the arguments
// the parentheses hold. It is the part whose width decides whether a call with a block
// after it fits on the line it starts on.
func (p *printer) flatCallHead(head string, all []ast.Expr, named []ast.NamedArg, parens bool) (string, bool) {
	_, args := p.trailingClosure(all)
	if !parens && len(args) == 0 && len(named) == 0 {
		return head, true
	}
	s, ok := p.flatList("(", ")", callItems(args, named))
	if !ok {
		return "", false
	}
	return head + s, true
}

// flatFuncLit writes a closure on one line, which it reaches only for a closure that was
// written on one: flat() turns every multi-line brace away before this is called.
func (p *printer) flatFuncLit(x *ast.FuncLit) (string, bool) {
	if len(x.Body.Stmts) == 0 {
		return "", false
	}
	parts, ok := p.flatStmts(x.Body.Stmts)
	if !ok {
		return "", false
	}
	body := strings.Join(parts, "; ")
	if x.ImplicitIt {
		return "{ " + body + " }", true
	}
	return "{ (" + p.params(x.Params) + ") -> " + body + " }", true
}

func (p *printer) flatStmts(list []ast.Stmt) ([]string, bool) {
	out := make([]string, len(list))
	for i, s := range list {
		v, ok := p.flatStmt(s)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// flatStmt is flat for the statements that are not expressions. `include` and `export`
// are here because a closure body may hold one, not because either is common there.
func (p *printer) flatStmt(s ast.Stmt) (string, bool) {
	switch x := s.(type) {
	case *ast.ExprStmt:
		return p.flat(x.X)
	case *ast.ReturnStmt:
		return p.flatKeyword("return", x.X)
	case *ast.BreakStmt:
		return p.flatKeyword("break", x.X)
	case *ast.NextStmt:
		return p.flatKeyword("next", x.X)
	case *ast.RecordDecl:
		return "record " + x.Name + "(" + p.params(x.Fields) + ")", true
	case *ast.IncludeDecl:
		out := "include " + x.Name
		if x.HasPath {
			out += " from " + strings.TrimRight(p.slice(x.PathPos.Offset, x.Stop.Offset), " \t")
		}
		return out, true
	case ast.Expr:
		return p.flat(x)
	}
	return "", false
}

func (p *printer) flatKeyword(kw string, x ast.Expr) (string, bool) {
	if x == nil {
		return kw, true
	}
	s, ok := p.flat(x)
	if !ok {
		return "", false
	}
	return kw + " " + s, true
}

// mustFlat is flat where the grammar leaves no room to break: a parameter default and a
// dict key. A form that cannot be flattened there is copied from the source, which is
// the one answer that is always both legal and unchanged.
func (p *printer) mustFlat(e ast.Expr) string {
	if s, ok := p.flat(e); ok {
		return s
	}
	return p.text(e)
}

// flatBlock writes `{ a; b }`. An empty one has no flat form at all: `{}` in this
// position is the empty dict (§3.12), which is a different node and a different value.
func (p *printer) flatBlock(b *ast.BlockStmt) (string, bool) {
	if b == nil || len(b.Stmts) == 0 {
		return "", false
	}
	parts, ok := p.flatStmts(b.Stmts)
	if !ok {
		return "", false
	}
	return "{ " + strings.Join(parts, "; ") + " }", true
}

func (p *printer) flatIf(x *ast.IfExpr) (string, bool) {
	if x.Else == nil && modifierForm(x.Kw, x.Then) {
		return p.flatModifier(x.Then, "if", x.Cond)
	}
	cond, ok := p.flat(x.Cond)
	if !ok {
		return "", false
	}
	then, ok := p.flatBlock(x.Then)
	if !ok {
		return "", false
	}
	out := "if " + cond + " " + then
	switch e := x.Else.(type) {
	case nil:
		return out, true
	case *ast.IfExpr:
		tail, ok := p.flatIf(e)
		if !ok {
			return "", false
		}
		return out + " else " + tail, true
	case *ast.BlockStmt:
		tail, ok := p.flatBlock(e)
		if !ok {
			return "", false
		}
		return out + " else " + tail, true
	}
	return "", false
}

// flatModifier writes the statement modifier of §4.4 back the way it was written.
func (p *printer) flatModifier(body *ast.BlockStmt, kw string, cond ast.Expr) (string, bool) {
	inner, ok := p.flatStmt(body.Stmts[0])
	if !ok {
		return "", false
	}
	c, ok := p.flat(cond)
	if !ok {
		return "", false
	}
	return inner + " " + kw + " " + c, true
}

func (p *printer) flatMatch(x *ast.MatchExpr) (string, bool) {
	head := "match "
	if x.Subject != nil {
		s, ok := p.flat(x.Subject)
		if !ok {
			return "", false
		}
		head += s + " "
	}
	arms := make([]string, len(x.Arms))
	for i, a := range x.Arms {
		s, ok := p.flatArm(a)
		if !ok {
			return "", false
		}
		arms[i] = s
	}
	return head + "{ " + strings.Join(arms, "; ") + " }", true
}

func (p *printer) flatArm(a ast.MatchArm) (string, bool) {
	var out string
	if a.Kind == ast.ArmElse {
		out = "else"
	} else {
		word := ""
		switch a.Kind {
		case ast.ArmIn:
			word = "in "
		case ast.ArmGuard:
			word = "if "
		}
		pats, ok := p.flatEach(a.Pats)
		if !ok {
			return "", false
		}
		for i := range pats {
			pats[i] = word + pats[i]
		}
		out = strings.Join(pats, ", ")
		if a.Guard != nil {
			g, ok := p.flat(a.Guard)
			if !ok {
				return "", false
			}
			out += " if " + g
		}
	}
	body, ok := p.flatArmBody(a.Body)
	if !ok {
		return "", false
	}
	return out + " -> " + body, true
}

func (p *printer) flatArmBody(b *ast.BlockStmt) (string, bool) {
	if p.isBlock(b) {
		return p.flatBlock(b)
	}
	if len(b.Stmts) == 1 {
		return p.flatStmt(b.Stmts[0])
	}
	return p.flatBlock(b)
}

func (p *printer) flatTry(x *ast.TryExpr) (string, bool) {
	out, ok := p.flatClause(x.X)
	if !ok {
		return "", false
	}
	out = "try " + out
	if x.Fallback != nil {
		s, ok := p.flatClause(x.Fallback)
		if !ok {
			return "", false
		}
		out += " else "
		if x.Var != "" {
			out += "(" + x.Var + ") "
			if !p.isClause(x.Fallback) {
				out += "-> "
			}
		}
		out += s
	}
	if x.Ensure != nil {
		s, ok := p.flatBlock(x.Ensure)
		if !ok {
			return "", false
		}
		out += " ensure " + s
	}
	return out, true
}

func (p *printer) flatClause(e ast.Expr) (string, bool) {
	if b, ok := e.(*ast.BlockStmt); ok && p.isBlock(b) {
		return p.flatBlock(b)
	}
	return p.flat(e)
}
