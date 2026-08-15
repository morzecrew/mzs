// Package ast defines the syntax tree of SPEC §6. It deliberately does not import
// internal/rx: a RegexLit stores the raw pattern and flag text, and the compile pass
// (SPEC §6.3, owned by the runtime) parks the compiled *rx.Regexp in Compiled as an
// `any`. Keeping ast free of rx is what lets the parser be written without waiting for
// the regex engine.
package ast

import "mzs/internal/token"

// Node is implemented by every syntax node. A node without a position is a bug (§17).
type Node interface {
	Pos() token.Pos
	End() token.Pos
	node()
}

// Expr is a node that produces a value. In mzs every control-flow construct is an
// expression (§4), so IfExpr and friends are Exprs.
type Expr interface {
	Node
	expr()
}

// Stmt is a node that may appear in a statement list. Every Expr reaches a StmtList
// wrapped in an ExprStmt.
type Stmt interface {
	Node
	stmt()
}

// RefKind records what an Ident resolved to during the compile pass (§6.3 step 4). An
// Ident still RefNone when Compile finishes is `undefined variable`: there is no bareword
// fallback (§9.3).
type RefKind uint8

const (
	RefNone RefKind = iota
	RefLocal
	RefGlobal
	RefFunc
	RefBuiltin
	RefModule
)

func (r RefKind) String() string {
	switch r {
	case RefLocal:
		return "local"
	case RefGlobal:
		return "global"
	case RefFunc:
		return "func"
	case RefBuiltin:
		return "builtin"
	case RefModule:
		return "module"
	}
	return "none"
}

// Param is one formal parameter. Rest marks a `*name` parameter, which may only be last.
// Parameter lists are parenthesised everywhere (D14), so a `fn` and a closure share this
// type.
type Param struct {
	Name    string
	Default Expr
	Rest    bool
	NamePos token.Pos
	Slot    int // filled by the compile pass; -1 until then
}

// StrPart is one piece of a string literal: exactly one of Text and Expr is meaningful.
// A part with a nil Expr is a text part, even when Text is empty.
type StrPart struct {
	Text string
	Expr Expr
}

// IsText reports whether p is a literal run rather than an interpolation.
func (p StrPart) IsText() bool { return p.Expr == nil }

// ArmKind is the pattern form of one `match` arm (§5.3).
type ArmKind uint8

const (
	ArmValue ArmKind = iota // equality, or match when the pattern is a regex
	ArmIn                   // `in expr`
	ArmGuard                // `if expr`
	ArmElse                 // `else`
	ArmArray                // `[x, y]` — an array pattern, which binds (§8.15)
)

func (k ArmKind) String() string {
	switch k {
	case ArmIn:
		return "in"
	case ArmGuard:
		return "guard"
	case ArmElse:
		return "else"
	case ArmArray:
		return "array"
	}
	return "value"
}

// MatchArm is one arm of a MatchExpr. Pats holds the arm's patterns, and more than one
// means "or" — including for ArmGuard, whose patterns are conditions tested for truth
// with the subject unconsulted. Guard is the arm's trailing `if` condition, which is an
// additional "and" (§5.3). ArmElse has no patterns and must be the last arm.
type MatchArm struct {
	Kind  ArmKind
	Pats  []Expr
	Guard Expr
	Body  *BlockStmt
	Pos   token.Pos
}

// ---------------------------------------------------------------------------
// Root and statements
// ---------------------------------------------------------------------------

// Program is a whole compilation unit. FrameSize is the number of local slots the top
// level needs, filled by the compile pass.
type Program struct {
	File      string
	Stmts     []Stmt
	Start     token.Pos
	Stop      token.Pos
	FrameSize int
}

// ExprStmt wraps an expression used for its value or its effect.
type ExprStmt struct {
	X Expr
}

// ReturnStmt unwinds to the nearest enclosing named function (§8.10). X may be nil.
type ReturnStmt struct {
	X    Expr
	Kw   token.Pos
	Stop token.Pos
}

// BreakStmt ends the nearest enclosing loop, or the call a closure was passed to.
type BreakStmt struct {
	X    Expr
	Kw   token.Pos
	Stop token.Pos
}

// NextStmt ends the current closure invocation or loop iteration.
type NextStmt struct {
	X    Expr
	Kw   token.Pos
	Stop token.Pos
}

// IncludeDecl is `include name` — a built-in module of §12.8 — or
// `include name from "path"` — another script, read through Options.ModuleLoader. Both
// bind Name in the scope the statement stands in; there is no ambient module, so this
// declaration is the only way `json`, `http` or a script of your own becomes a name
// (§12.8).
//
// `from` is not a keyword: the parser reads it positionally, so a variable may still be
// called `from`.
type IncludeDecl struct {
	Name    string
	Path    string // "" for a built-in module
	HasPath bool
	NamePos token.Pos
	PathPos token.Pos
	Kw      token.Pos
	Stop    token.Pos
}

// ExportDecl is `export fn f(…) { … }`, `export x = 1` or `export name`. Decl is the
// wrapped declaration for the first two forms and nil for the third, which exports a
// binding that already exists. Names is what the statement adds to the module's exports,
// in declaration order (§12.8).
type ExportDecl struct {
	Names []string
	Decl  Stmt
	Kw    token.Pos
	Stop  token.Pos
}

// FnDecl is `fn name(...) { body }`. It is both a statement (hoisted, §8.2) and an
// expression (a `fn` in expression position evaluates to the function value). The frame
// the call needs is Body.FrameSize, which counts the parameter slots too.
//
// Async marks the `async fn` form of §8.14: the same declaration in every respect the
// compiler cares about — same scope, same frame, same hoisting — and a different thing
// at the call, where it starts a task instead of running the body. Kw is the `async`
// token in that form, so a diagnostic points at the modifier rather than past it.
type FnDecl struct {
	Name   string
	Params []Param
	Body   *BlockStmt
	Async  bool
	Kw     token.Pos
	Stop   token.Pos
}

// BlockStmt is a statement list and a scope of its own: every `{ … }` is a closure, so
// every `{ … }` is a scope (D2, §8.2). FrameSize is the number of local slots that scope
// needs, filled by the compile pass; for a function or closure body it includes the
// parameter slots, so it is the size of the frame a call allocates.
type BlockStmt struct {
	Stmts     []Stmt
	Start     token.Pos
	Stop      token.Pos
	FrameSize int
}

// ---------------------------------------------------------------------------
// Control flow (all expressions)
// ---------------------------------------------------------------------------

// IfExpr is `if cond { … }` and the `if` statement modifier (§6.2). Else is nil, a
// *BlockStmt, or a nested *IfExpr for `else if`. There is no `unless` (§5.6).
type IfExpr struct {
	Cond Expr
	Then *BlockStmt
	Else Stmt
	Kw   token.Pos
	Stop token.Pos
}

// WhileExpr is `while cond { … }` and the `while` statement modifier. There is no
// `until` and no `loop`: both are spelled with `while` (§5.6).
type WhileExpr struct {
	Cond Expr
	Body *BlockStmt
	Kw   token.Pos
	Stop token.Pos
}

// ForExpr is `for k[, v] in iter { … }`. ValVar is "" for the one-variable form.
type ForExpr struct {
	KeyVar string
	ValVar string
	Iter   Expr
	Body   *BlockStmt
	Kw     token.Pos
	Stop   token.Pos
}

// MatchExpr is `match [subject] { arms }` (§5.2). Subject is nil for the subject-less
// form, where every pattern is evaluated as a condition (§5.4). Arms are tested in order
// and the first one that fires wins; with no arm firing the value is nil.
type MatchExpr struct {
	Subject Expr
	Arms    []MatchArm
	Kw      token.Pos
	Stop    token.Pos
}

// TryExpr is `try X else Y` and `try X else (e) -> Y` (§8.11). Var is "" unless the
// source bound the error dict.
type TryExpr struct {
	X        Expr
	Var      string
	Fallback Expr
	Kw       token.Pos
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

type NilLit struct {
	Start token.Pos
	Stop  token.Pos
}

type BoolLit struct {
	Value bool
	Start token.Pos
	Stop  token.Pos
}

type IntLit struct {
	Value int64
	Start token.Pos
	Stop  token.Pos
}

type FloatLit struct {
	Value float64
	Start token.Pos
	Stop  token.Pos
}

// StrLit is a (possibly interpolated) string. mzs has no symbol type, so a literal
// string always has exactly one text part.
type StrLit struct {
	Parts []StrPart
	Start token.Pos
	Stop  token.Pos
}

// IsConst reports whether the literal has no interpolation, so the compile pass may
// fold it to a constant.
func (s *StrLit) IsConst() bool {
	for _, p := range s.Parts {
		if p.Expr != nil {
			return false
		}
	}
	return true
}

// ConstText returns the concatenated literal text; only meaningful when IsConst.
func (s *StrLit) ConstText() string {
	if len(s.Parts) == 1 {
		return s.Parts[0].Text
	}
	out := ""
	for _, p := range s.Parts {
		out += p.Text
	}
	return out
}

// RegexLit holds the *raw* pattern and flag letters exactly as they appeared between
// the slashes. Compiled is filled by the compile pass with an *rx.Regexp; ast keeps it
// as `any` so this package never imports internal/rx.
type RegexLit struct {
	Pattern  string
	Flags    string
	Compiled any
	Start    token.Pos
	Stop     token.Pos
}

type ArrayLit struct {
	Elems  []Expr
	Lbrack token.Pos
	Rbrack token.Pos
}

// DictLit keeps Keys and Vals parallel so insertion order (D11) is the source order.
// `{a: 1}` gives a StrLit key, `{}` gives an empty DictLit (D3).
type DictLit struct {
	Keys   []Expr
	Vals   []Expr
	Lbrack token.Pos
	Rbrack token.Pos
}

// Ident is a bare name. Ref and Slot are filled by the compile pass; a name that
// resolves to nothing is an error, not a string (§9.3).
type Ident struct {
	Name  string
	Ref   RefKind
	Slot  int
	Start token.Pos
	Stop  token.Pos
}

// GlobalVar is `$name`; Name includes the leading '$'. It is never resolved through the
// scope chain (D7).
type GlobalVar struct {
	Name  string
	Start token.Pos
	Stop  token.Pos
}

type UnaryExpr struct {
	Op    token.Kind
	X     Expr
	OpPos token.Pos
}

// BinaryExpr covers arithmetic, comparison and the match operators. Ranges have their
// own node; short-circuit operators have their own node.
type BinaryExpr struct {
	Op    token.Kind
	L, R  Expr
	OpPos token.Pos
}

// LogicalExpr is `&&`, `||` and `??` — the short-circuiting operators (§8.5).
type LogicalExpr struct {
	Op    token.Kind
	L, R  Expr
	OpPos token.Pos
}

type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

// RangeExpr is `a..b` and `a..<b` (Exclusive). The operator is non-associative (§5.1).
type RangeExpr struct {
	Lo        Expr
	Hi        Expr
	Exclusive bool
	OpPos     token.Pos
}

// AssignExpr covers `=`, `:=` and every compound assignment. Target is an *Ident,
// *GlobalVar or *IndexExpr; the evaluator evaluates the target exactly once (§6.2).
type AssignExpr struct {
	Target Expr
	Op     token.Kind
	Value  Expr
	OpPos  token.Pos
}

// ArrayPattern is the left side of a destructuring assignment and an array pattern in a
// `match` arm (§8.15). Brackets is false for the bare `a, b = …` form, which the parser
// accepts at statement level only.
//
// Elems holds one entry per position, and what an entry may be depends on the side it
// stands on. On the left of `=` it is a target: an *Ident, a *GlobalVar, an *IndexExpr
// or a nested *ArrayPattern. In a `match` arm a bare *Ident binds that position and any
// other expression is compared exactly as an ordinary arm pattern is (§5.3), so
// `[0, x]` fires on an array of two whose first element is 0 and binds the second.
//
// A pattern is never evaluated on its own: it is consumed by the node that owns it.
type ArrayPattern struct {
	Elems    []Expr
	Brackets bool
	Start    token.Pos
	Stop     token.Pos
}

// DestructureAssign is `a, b = xs`, `[a, [b, c]] = xs` and their `:=` forms (§8.15).
// The value of the expression is the right side, exactly as it is for `a = 1`. A right
// side that is not an array, or is one of the wrong length, is a run-time error rather
// than a silent nil.
type DestructureAssign struct {
	Pattern *ArrayPattern
	Op      token.Kind // ASSIGN or DECLARE
	Value   Expr
	OpPos   token.Pos
}

// IndexExpr is `x[i]` or `x[i, n]`; Index2 is nil for the one-argument form.
type IndexExpr struct {
	X      Expr
	Index  Expr
	Index2 Expr
	Lbrack token.Pos
	Rbrack token.Pos
}

// CallExpr is `f(...)`. KwArgs collects `f(a: 1)` into one trailing dict argument (§8.7).
// A trailing closure is not a field: the parser appends it to Args (§4.2).
type CallExpr struct {
	Fn     Expr
	Args   []Expr
	KwArgs *DictLit
	Lparen token.Pos
	Stop   token.Pos
}

// MethodCall is `recv.name(...)` and `recv?.name(...)`. `x.foo` without parentheses is a
// zero-argument call, not a field access (§4.2). Cache is reserved for the compile-time
// method cache of §14.4 and must not be mutated after Compile — a *Program is shared by
// concurrent Runs.
type MethodCall struct {
	Recv    Expr
	Name    string
	Args    []Expr
	KwArgs  *DictLit
	Safe    bool
	Cache   any
	NamePos token.Pos
	Stop    token.Pos
}

// FuncLit is a closure literal `{ … }` — one of the two function-value forms (D2, §4.1),
// the other being an FnDecl with no name. A literal with no `(params) ->` list gets
// ImplicitIt and a single parameter named "it" (§8.9). The frame a call allocates is
// Body.FrameSize.
type FuncLit struct {
	Params     []Param
	Body       *BlockStmt
	ImplicitIt bool
	Start      token.Pos
	Stop       token.Pos
}

// GroupExpr is `( a; b; c )`; its value is the last statement. This is what lets a
// one-liner group statements inside an expression (§4).
type GroupExpr struct {
	Stmts  []Stmt
	Lparen token.Pos
	Rparen token.Pos
}

// ---------------------------------------------------------------------------
// Interface plumbing
// ---------------------------------------------------------------------------

func (*Program) node()           {}
func (*ExprStmt) node()          {}
func (*ReturnStmt) node()        {}
func (*BreakStmt) node()         {}
func (*NextStmt) node()          {}
func (*IncludeDecl) node()       {}
func (*ExportDecl) node()        {}
func (*FnDecl) node()            {}
func (*BlockStmt) node()         {}
func (*IfExpr) node()            {}
func (*WhileExpr) node()         {}
func (*ForExpr) node()           {}
func (*MatchExpr) node()         {}
func (*TryExpr) node()           {}
func (*NilLit) node()            {}
func (*BoolLit) node()           {}
func (*IntLit) node()            {}
func (*FloatLit) node()          {}
func (*StrLit) node()            {}
func (*RegexLit) node()          {}
func (*ArrayLit) node()          {}
func (*DictLit) node()           {}
func (*Ident) node()             {}
func (*GlobalVar) node()         {}
func (*UnaryExpr) node()         {}
func (*BinaryExpr) node()        {}
func (*LogicalExpr) node()       {}
func (*TernaryExpr) node()       {}
func (*RangeExpr) node()         {}
func (*AssignExpr) node()        {}
func (*ArrayPattern) node()      {}
func (*DestructureAssign) node() {}
func (*IndexExpr) node()         {}
func (*CallExpr) node()          {}
func (*MethodCall) node()        {}
func (*FuncLit) node()           {}
func (*GroupExpr) node()         {}

func (*ExprStmt) stmt()    {}
func (*ReturnStmt) stmt()  {}
func (*BreakStmt) stmt()   {}
func (*NextStmt) stmt()    {}
func (*IncludeDecl) stmt() {}
func (*ExportDecl) stmt()  {}
func (*FnDecl) stmt()      {}
func (*BlockStmt) stmt()   {}
func (*IfExpr) stmt()      {}
func (*WhileExpr) stmt()   {}
func (*ForExpr) stmt()     {}
func (*MatchExpr) stmt()   {}
func (*TryExpr) stmt()     {}

func (*FnDecl) expr()            {}
func (*IfExpr) expr()            {}
func (*WhileExpr) expr()         {}
func (*ForExpr) expr()           {}
func (*MatchExpr) expr()         {}
func (*TryExpr) expr()           {}
func (*NilLit) expr()            {}
func (*BoolLit) expr()           {}
func (*IntLit) expr()            {}
func (*FloatLit) expr()          {}
func (*StrLit) expr()            {}
func (*RegexLit) expr()          {}
func (*ArrayLit) expr()          {}
func (*DictLit) expr()           {}
func (*Ident) expr()             {}
func (*GlobalVar) expr()         {}
func (*UnaryExpr) expr()         {}
func (*BinaryExpr) expr()        {}
func (*LogicalExpr) expr()       {}
func (*TernaryExpr) expr()       {}
func (*RangeExpr) expr()         {}
func (*AssignExpr) expr()        {}
func (*ArrayPattern) expr()      {}
func (*DestructureAssign) expr() {}
func (*IndexExpr) expr()         {}
func (*CallExpr) expr()          {}
func (*MethodCall) expr()        {}
func (*FuncLit) expr()           {}
func (*GroupExpr) expr()         {}

// ---------------------------------------------------------------------------
// Positions
// ---------------------------------------------------------------------------

func (n *Program) Pos() token.Pos { return n.Start }
func (n *Program) End() token.Pos { return n.Stop }

func (n *ExprStmt) Pos() token.Pos { return nodePos(n.X) }
func (n *ExprStmt) End() token.Pos { return nodeEnd(n.X) }

func (n *ReturnStmt) Pos() token.Pos { return n.Kw }
func (n *ReturnStmt) End() token.Pos { return n.Stop }

func (n *BreakStmt) Pos() token.Pos { return n.Kw }
func (n *BreakStmt) End() token.Pos { return n.Stop }

func (n *NextStmt) Pos() token.Pos { return n.Kw }
func (n *NextStmt) End() token.Pos { return n.Stop }

func (n *IncludeDecl) Pos() token.Pos { return n.Kw }
func (n *IncludeDecl) End() token.Pos { return n.Stop }

func (n *ExportDecl) Pos() token.Pos { return n.Kw }
func (n *ExportDecl) End() token.Pos { return n.Stop }

func (n *FnDecl) Pos() token.Pos { return n.Kw }
func (n *FnDecl) End() token.Pos { return n.Stop }

func (n *BlockStmt) Pos() token.Pos { return n.Start }
func (n *BlockStmt) End() token.Pos { return n.Stop }

func (n *IfExpr) Pos() token.Pos { return n.Kw }
func (n *IfExpr) End() token.Pos { return n.Stop }

func (n *WhileExpr) Pos() token.Pos { return n.Kw }
func (n *WhileExpr) End() token.Pos { return n.Stop }

func (n *ForExpr) Pos() token.Pos { return n.Kw }
func (n *ForExpr) End() token.Pos { return n.Stop }

func (n *MatchExpr) Pos() token.Pos { return n.Kw }
func (n *MatchExpr) End() token.Pos { return n.Stop }

func (n *TryExpr) Pos() token.Pos { return n.Kw }
func (n *TryExpr) End() token.Pos { return nodeEnd(n.Fallback) }

func (n *NilLit) Pos() token.Pos   { return n.Start }
func (n *NilLit) End() token.Pos   { return n.Stop }
func (n *BoolLit) Pos() token.Pos  { return n.Start }
func (n *BoolLit) End() token.Pos  { return n.Stop }
func (n *IntLit) Pos() token.Pos   { return n.Start }
func (n *IntLit) End() token.Pos   { return n.Stop }
func (n *FloatLit) Pos() token.Pos { return n.Start }
func (n *FloatLit) End() token.Pos { return n.Stop }
func (n *StrLit) Pos() token.Pos   { return n.Start }
func (n *StrLit) End() token.Pos   { return n.Stop }
func (n *RegexLit) Pos() token.Pos { return n.Start }
func (n *RegexLit) End() token.Pos { return n.Stop }

func (n *ArrayLit) Pos() token.Pos { return n.Lbrack }
func (n *ArrayLit) End() token.Pos { return n.Rbrack }
func (n *DictLit) Pos() token.Pos  { return n.Lbrack }
func (n *DictLit) End() token.Pos  { return n.Rbrack }

func (n *Ident) Pos() token.Pos     { return n.Start }
func (n *Ident) End() token.Pos     { return n.Stop }
func (n *GlobalVar) Pos() token.Pos { return n.Start }
func (n *GlobalVar) End() token.Pos { return n.Stop }

func (n *UnaryExpr) Pos() token.Pos { return n.OpPos }
func (n *UnaryExpr) End() token.Pos { return nodeEnd(n.X) }

func (n *BinaryExpr) Pos() token.Pos { return nodePos(n.L) }
func (n *BinaryExpr) End() token.Pos { return nodeEnd(n.R) }

func (n *LogicalExpr) Pos() token.Pos { return nodePos(n.L) }
func (n *LogicalExpr) End() token.Pos { return nodeEnd(n.R) }

func (n *TernaryExpr) Pos() token.Pos { return nodePos(n.Cond) }
func (n *TernaryExpr) End() token.Pos { return nodeEnd(n.Else) }

func (n *RangeExpr) Pos() token.Pos {
	if n.Lo != nil {
		return nodePos(n.Lo)
	}
	return n.OpPos
}
func (n *RangeExpr) End() token.Pos {
	if n.Hi != nil {
		return nodeEnd(n.Hi)
	}
	return n.OpPos
}

func (n *AssignExpr) Pos() token.Pos { return nodePos(n.Target) }
func (n *AssignExpr) End() token.Pos { return nodeEnd(n.Value) }

func (n *ArrayPattern) Pos() token.Pos { return n.Start }
func (n *ArrayPattern) End() token.Pos { return n.Stop }

func (n *DestructureAssign) Pos() token.Pos { return nodePos(n.Pattern) }
func (n *DestructureAssign) End() token.Pos { return nodeEnd(n.Value) }

func (n *IndexExpr) Pos() token.Pos { return nodePos(n.X) }
func (n *IndexExpr) End() token.Pos { return n.Rbrack }

func (n *CallExpr) Pos() token.Pos { return nodePos(n.Fn) }
func (n *CallExpr) End() token.Pos { return n.Stop }

func (n *MethodCall) Pos() token.Pos {
	if n.Recv != nil {
		return nodePos(n.Recv)
	}
	return n.NamePos
}
func (n *MethodCall) End() token.Pos { return n.Stop }

func (n *FuncLit) Pos() token.Pos { return n.Start }
func (n *FuncLit) End() token.Pos { return n.Stop }

func (n *GroupExpr) Pos() token.Pos { return n.Lparen }
func (n *GroupExpr) End() token.Pos { return n.Rparen }

func nodePos(n Node) token.Pos {
	if n == nil {
		return token.Pos{}
	}
	return n.Pos()
}

func nodeEnd(n Node) token.Pos {
	if n == nil {
		return token.Pos{}
	}
	return n.End()
}
