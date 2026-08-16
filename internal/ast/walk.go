package ast

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"mzs/internal/token"
)

// Walk visits n and every node beneath it in source order, calling f on each. When f
// returns false the children of that node are skipped. A nil node is not visited.
func Walk(n Node, f func(Node) bool) {
	if n == nil || isNilNode(n) {
		return
	}
	if !f(n) {
		return
	}
	switch x := n.(type) {
	case *Program:
		walkStmts(x.Stmts, f)
	case *ExprStmt:
		Walk(x.X, f)
	case *ReturnStmt:
		Walk(x.X, f)
	case *BreakStmt:
		Walk(x.X, f)
	case *NextStmt:
		Walk(x.X, f)
	case *IncludeDecl:
		// A leaf: the path is a literal, not an expression.
	case *ExportDecl:
		Walk(x.Decl, f)
	case *FnDecl:
		walkParams(x.Params, f)
		Walk(x.Body, f)
	case *BlockStmt:
		walkStmts(x.Stmts, f)
	case *IfExpr:
		Walk(x.Cond, f)
		Walk(x.Then, f)
		Walk(x.Else, f)
	case *WhileExpr:
		Walk(x.Cond, f)
		Walk(x.Body, f)
	case *ForExpr:
		Walk(x.Iter, f)
		Walk(x.Body, f)
	case *MatchExpr:
		Walk(x.Subject, f)
		for i := range x.Arms {
			walkExprs(x.Arms[i].Pats, f)
			Walk(x.Arms[i].Guard, f)
			Walk(x.Arms[i].Body, f)
		}
	case *TryExpr:
		Walk(x.X, f)
		Walk(x.Fallback, f)
		Walk(x.Ensure, f)
	case *NilLit, *BoolLit, *IntLit, *FloatLit, *RegexLit, *Ident, *GlobalVar:
		// leaves
	case *StrLit:
		for _, p := range x.Parts {
			if p.Expr != nil {
				Walk(p.Expr, f)
			}
		}
	case *ArrayLit:
		walkExprs(x.Elems, f)
	case *DictLit:
		for i := range x.Keys {
			Walk(x.Keys[i], f)
			if i < len(x.Vals) {
				Walk(x.Vals[i], f)
			}
		}
	case *UnaryExpr:
		Walk(x.X, f)
	case *BinaryExpr:
		Walk(x.L, f)
		Walk(x.R, f)
	case *LogicalExpr:
		Walk(x.L, f)
		Walk(x.R, f)
	case *TernaryExpr:
		Walk(x.Cond, f)
		Walk(x.Then, f)
		Walk(x.Else, f)
	case *RangeExpr:
		Walk(x.Lo, f)
		Walk(x.Hi, f)
	case *AssignExpr:
		Walk(x.Target, f)
		Walk(x.Value, f)
	case *ArrayPattern:
		walkExprs(x.Elems, f)
	case *DestructureAssign:
		Walk(x.Pattern, f)
		Walk(x.Value, f)
	case *IndexExpr:
		Walk(x.X, f)
		Walk(x.Index, f)
		Walk(x.Index2, f)
	case *CallExpr:
		Walk(x.Fn, f)
		walkExprs(x.Args, f)
		walkNamed(x.Named, f)
	case *MethodCall:
		Walk(x.Recv, f)
		walkExprs(x.Args, f)
		walkNamed(x.Named, f)
	case *FuncLit:
		walkParams(x.Params, f)
		Walk(x.Body, f)
	case *GroupExpr:
		walkStmts(x.Stmts, f)
	}
}

func walkStmts(ss []Stmt, f func(Node) bool) {
	for _, s := range ss {
		Walk(s, f)
	}
}

func walkExprs(es []Expr, f func(Node) bool) {
	for _, e := range es {
		Walk(e, f)
	}
}

// walkNamed visits the values of `name = value` arguments. The name itself is not a
// node: it labels a parameter of the callee, not an expression of this scope.
func walkNamed(as []NamedArg, f func(Node) bool) {
	for _, a := range as {
		Walk(a.Value, f)
	}
}

func walkParams(ps []Param, f func(Node) bool) {
	for _, p := range ps {
		if p.Default != nil {
			Walk(p.Default, f)
		}
	}
}

// isNilNode reports whether n is a nil interface or a typed nil pointer. The parser
// stores optional children as nil Exprs, and Walk must survive both spellings.
func isNilNode(n Node) bool {
	switch x := n.(type) {
	case nil:
		return true
	case *Program:
		return x == nil
	case *ExprStmt:
		return x == nil
	case *ReturnStmt:
		return x == nil
	case *BreakStmt:
		return x == nil
	case *NextStmt:
		return x == nil
	case *IncludeDecl:
		return x == nil
	case *ExportDecl:
		return x == nil
	case *FnDecl:
		return x == nil
	case *BlockStmt:
		return x == nil
	case *IfExpr:
		return x == nil
	case *WhileExpr:
		return x == nil
	case *ForExpr:
		return x == nil
	case *MatchExpr:
		return x == nil
	case *TryExpr:
		return x == nil
	case *NilLit:
		return x == nil
	case *BoolLit:
		return x == nil
	case *IntLit:
		return x == nil
	case *FloatLit:
		return x == nil
	case *StrLit:
		return x == nil
	case *RegexLit:
		return x == nil
	case *ArrayLit:
		return x == nil
	case *DictLit:
		return x == nil
	case *Ident:
		return x == nil
	case *GlobalVar:
		return x == nil
	case *UnaryExpr:
		return x == nil
	case *BinaryExpr:
		return x == nil
	case *LogicalExpr:
		return x == nil
	case *TernaryExpr:
		return x == nil
	case *RangeExpr:
		return x == nil
	case *AssignExpr:
		return x == nil
	case *ArrayPattern:
		return x == nil
	case *DestructureAssign:
		return x == nil
	case *IndexExpr:
		return x == nil
	case *CallExpr:
		return x == nil
	case *MethodCall:
		return x == nil
	case *FuncLit:
		return x == nil
	case *GroupExpr:
		return x == nil
	}
	return false
}

// ---------------------------------------------------------------------------
// Dumping
// ---------------------------------------------------------------------------

// Dump renders n as an indented s-expression. It is the format the parser's golden
// tests compare against and what `mzs --ast` prints, so it must stay stable: one node
// per line, two spaces of indent per level, no trailing whitespace.
func Dump(n Node) string {
	var sb strings.Builder
	dump(&sb, n, 0)
	return sb.String()
}

// Fprint writes Dump(n) to w.
func Fprint(w io.Writer, n Node) error {
	_, err := io.WriteString(w, Dump(n))
	return err
}

// String makes a Program printable, which is what mzs.Program.String() forwards to.
func (n *Program) String() string { return Dump(n) }

func dump(sb *strings.Builder, n Node, depth int) {
	if n == nil || isNilNode(n) {
		return
	}
	ind := strings.Repeat("  ", depth)
	line := func(s string) {
		sb.WriteString(ind)
		sb.WriteString(s)
		sb.WriteByte('\n')
	}
	child := func(c Node) { dump(sb, c, depth+1) }
	labeled := func(label string, c Node) {
		if c == nil || isNilNode(c) {
			return
		}
		sb.WriteString(ind)
		sb.WriteString("  ")
		sb.WriteString(label)
		sb.WriteByte('\n')
		dump(sb, c, depth+2)
	}

	switch x := n.(type) {
	case *Program:
		line("Program " + strconv.Quote(x.File))
		for _, s := range x.Stmts {
			child(s)
		}
	case *ExprStmt:
		line("ExprStmt")
		child(x.X)
	case *ReturnStmt:
		line("Return")
		child(x.X)
	case *BreakStmt:
		line("Break")
		child(x.X)
	case *NextStmt:
		line("Next")
		child(x.X)
	case *IncludeDecl:
		if x.HasPath {
			line("Include " + x.Name + " from " + strconv.Quote(x.Path))
		} else {
			line("Include " + x.Name)
		}
	case *ExportDecl:
		line("Export " + strings.Join(x.Names, ", "))
		child(x.Decl)
	case *FnDecl:
		kind := "FnDecl"
		if x.Async {
			// The modifier is the whole difference at the call (§8.14), so a dump that
			// hid it would show two identical trees for two different programs.
			kind = "FnDecl async"
		}
		if x.Name != "" {
			// The anonymous form has no name to print, and a blank where one would go
			// reads as a bug in the dump rather than as the node it is.
			kind += " " + x.Name
		}
		line(kind + " (" + paramsText(x.Params) + ")")
		labeled("body:", x.Body)
	case *BlockStmt:
		line("Block")
		for _, s := range x.Stmts {
			child(s)
		}
	case *IfExpr:
		line("If")
		labeled("cond:", x.Cond)
		labeled("then:", x.Then)
		labeled("else:", x.Else)
	case *WhileExpr:
		line("While")
		labeled("cond:", x.Cond)
		labeled("body:", x.Body)
	case *ForExpr:
		name := x.KeyVar
		if x.ValVar != "" {
			name += ", " + x.ValVar
		}
		line("For " + name)
		labeled("iter:", x.Iter)
		labeled("body:", x.Body)
	case *MatchExpr:
		line("Match")
		labeled("subject:", x.Subject)
		for i := range x.Arms {
			a := &x.Arms[i]
			sb.WriteString(ind + "  arm " + a.Kind.String() + "\n")
			for _, p := range a.Pats {
				sb.WriteString(ind + "    pat\n")
				dump(sb, p, depth+3)
			}
			if a.Guard != nil {
				sb.WriteString(ind + "    guard\n")
				dump(sb, a.Guard, depth+3)
			}
			sb.WriteString(ind + "    body\n")
			dump(sb, a.Body, depth+3)
		}
	case *TryExpr:
		s := "Try"
		if x.Var != "" {
			s += " " + x.Var
		}
		line(s)
		labeled("body:", x.X)
		labeled("fallback:", x.Fallback)
		labeled("ensure:", x.Ensure)
	case *NilLit:
		line("Nil")
	case *BoolLit:
		line("Bool " + strconv.FormatBool(x.Value))
	case *IntLit:
		line("Int " + strconv.FormatInt(x.Value, 10))
	case *FloatLit:
		line("Float " + strconv.FormatFloat(x.Value, 'g', -1, 64))
	case *StrLit:
		if x.IsConst() {
			line("Str " + strconv.Quote(x.ConstText()))
			return
		}
		line("Str")
		for _, p := range x.Parts {
			if p.Expr == nil {
				sb.WriteString(ind + "  text " + strconv.Quote(p.Text) + "\n")
			} else {
				sb.WriteString(ind + "  interp\n")
				dump(sb, p.Expr, depth+2)
			}
		}
	case *RegexLit:
		line("Regex /" + x.Pattern + "/" + x.Flags)
	case *ArrayLit:
		line("Array")
		for _, e := range x.Elems {
			child(e)
		}
	case *DictLit:
		line("Dict")
		for i := range x.Keys {
			sb.WriteString(ind + "  entry\n")
			dump(sb, x.Keys[i], depth+2)
			if i < len(x.Vals) {
				dump(sb, x.Vals[i], depth+2)
			}
		}
	case *Ident:
		s := "Ident " + x.Name
		if x.Ref != RefNone {
			s += " [" + x.Ref.String() + ":" + strconv.Itoa(x.Slot) + "]"
		}
		line(s)
	case *GlobalVar:
		line("Global " + x.Name)
	case *UnaryExpr:
		line("Unary " + x.Op.String())
		child(x.X)
	case *BinaryExpr:
		line("Binary " + x.Op.String())
		child(x.L)
		child(x.R)
	case *LogicalExpr:
		line("Logical " + x.Op.String())
		child(x.L)
		child(x.R)
	case *TernaryExpr:
		line("Ternary")
		labeled("cond:", x.Cond)
		labeled("then:", x.Then)
		labeled("else:", x.Else)
	case *RangeExpr:
		op := ".."
		if x.Exclusive {
			op = "..<"
		}
		line("Range " + op)
		child(x.Lo)
		child(x.Hi)
	case *AssignExpr:
		line("Assign " + x.Op.String())
		child(x.Target)
		child(x.Value)
	case *ArrayPattern:
		line("Pattern")
		for _, e := range x.Elems {
			child(e)
		}
	case *DestructureAssign:
		line("Destructure " + x.Op.String())
		child(x.Pattern)
		child(x.Value)
	case *IndexExpr:
		line("Index")
		child(x.X)
		child(x.Index)
		child(x.Index2)
	case *CallExpr:
		line("Call")
		labeled("fn:", x.Fn)
		for _, a := range x.Args {
			sb.WriteString(ind + "  arg\n")
			dump(sb, a, depth+2)
		}
		for _, a := range x.Named {
			sb.WriteString(ind + "  arg " + a.Name + " =\n")
			dump(sb, a.Value, depth+2)
		}
	case *MethodCall:
		op := "."
		if x.Safe {
			op = "?."
		}
		line("MethodCall " + op + x.Name)
		labeled("recv:", x.Recv)
		for _, a := range x.Args {
			sb.WriteString(ind + "  arg\n")
			dump(sb, a, depth+2)
		}
		for _, a := range x.Named {
			sb.WriteString(ind + "  arg " + a.Name + " =\n")
			dump(sb, a.Value, depth+2)
		}
	case *FuncLit:
		kind := "Closure"
		if x.ImplicitIt {
			kind = "Closure implicit"
		}
		line(kind + " (" + paramsText(x.Params) + ")")
		labeled("body:", x.Body)
	case *GroupExpr:
		line("Group")
		for _, s := range x.Stmts {
			child(s)
		}
	default:
		line(fmt.Sprintf("%T", n))
	}
}

func paramsText(ps []Param) string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		s := p.Name
		if p.Rest {
			s = "*" + s
		}
		if p.Default != nil {
			s += "=…"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// PosString renders a position the way diagnostics do.
func PosString(p token.Pos) string {
	return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col)
}
