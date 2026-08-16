package parser

import (
	"os"
	"strings"
	"testing"

	"mzs/internal/ast"
	"mzs/internal/token"
)

// parse is the shared happy-path helper: it fails the test on any diagnostic and hands
// back the frozen ast.Dump text, which is what the golden tables compare.
func parse(t *testing.T, src string) string {
	t.Helper()
	prog, err := Parse("t", src)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v; want nil", src, err)
	}
	return ast.Dump(prog)
}

// flatten unwraps the errors.Join that Parse returns for a multi-error compile.
func flatten(err error) []*Error {
	if e, ok := err.(*Error); ok {
		return []*Error{e}
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return nil
	}
	var out []*Error
	for _, e := range joined.Unwrap() {
		out = append(out, flatten(e)...)
	}
	return out
}

// TestParseGolden pins the tree for every production of SPEC §4. The expected text is
// ast.Dump's frozen format, so a change to either the parser or the dump shows up here.
func TestParseGolden(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "literals",
			src:  "1; 1.5; true; false; nil; 'raw'; /re/i",
			want: `
Program "t"
  ExprStmt
    Int 1
  ExprStmt
    Float 1.5
  ExprStmt
    Bool true
  ExprStmt
    Bool false
  ExprStmt
    Nil
  ExprStmt
    Str "raw"
  ExprStmt
    Regex /re/i
`,
		},
		{
			name: "interpolated string",
			src:  `"a${x+1}b$c"`,
			want: `
Program "t"
  ExprStmt
    Str
      text "a"
      interp
        Binary +
          Ident x
          Int 1
      text "b"
      interp
        Global $c
`,
		},
		{
			name: "the corpus one-liner",
			src:  `s = $__sent.lower.trim; s == "да" || s ~ /^ага|конечно/`,
			want: `
Program "t"
  ExprStmt
    Assign =
      Ident s
      MethodCall .trim
        recv:
          MethodCall .lower
            recv:
              Global $__sent
  ExprStmt
    Logical ||
      Binary ==
        Ident s
        Str "да"
      Binary ~
        Ident s
        Regex /^ага|конечно/
`,
		},
		{
			name: "collections",
			src:  `[]; {}; [1, 2]; {a: 1, "b": 2, (k): 3}`,
			want: `
Program "t"
  ExprStmt
    Array
  ExprStmt
    Dict
  ExprStmt
    Array
      Int 1
      Int 2
  ExprStmt
    Dict
      entry
        Str "a"
        Int 1
      entry
        Str "b"
        Int 2
      entry
        Ident k
        Int 3
`,
		},
		{
			name: "fn declaration with defaults and a rest parameter",
			src:  "fn f(a, b = 2, *rest) { a + b }",
			want: `
Program "t"
  FnDecl f (a, b=…, *rest)
    body:
      Block
        ExprStmt
          Binary +
            Ident a
            Ident b
`,
		},
		{
			// §8.14: `async` is positional, so the parser reads it only right before
			// `fn` — and `async` stays an ordinary name everywhere else, which the last
			// statement here is what pins.
			name: "async fn declaration",
			src:  "async fn f(u) { u }\nexport async fn g() { 1 }\nasync = 2",
			want: `
Program "t"
  FnDecl async f (u)
    body:
      Block
        ExprStmt
          Ident u
  Export g
    FnDecl async g ()
      body:
        Block
          ExprStmt
            Int 1
  ExprStmt
    Assign =
      Ident async
      Int 2
`,
		},
		{
			name: "closures: implicit it, parameters, none",
			src:  "{ it * 2 }; { (x) -> x }; { () -> 42 }",
			want: `
Program "t"
  ExprStmt
    Closure implicit (it)
      body:
        Block
          ExprStmt
            Binary *
              Ident it
              Int 2
  ExprStmt
    Closure (x)
      body:
        Block
          ExprStmt
            Ident x
  ExprStmt
    Closure ()
      body:
        Block
          ExprStmt
            Int 42
`,
		},
		{
			// §4.1: the anonymous form is the same node with no name, so it is a value
			// and nothing else — nothing is hoisted and nothing is bound.
			name: "anonymous fn",
			src:  "f = fn(a, b) { a + b }; g = async fn() { 1 }",
			want: `
Program "t"
  ExprStmt
    Assign =
      Ident f
      FnDecl (a, b)
        body:
          Block
            ExprStmt
              Binary +
                Ident a
                Ident b
  ExprStmt
    Assign =
      Ident g
      FnDecl async ()
        body:
          Block
            ExprStmt
              Int 1
`,
		},
		{
			// §4.1: `(params) -> { body }` is the anonymous `fn` without the keyword, so
			// it is the same node — the parameters are outside the braces either way.
			name: "arrow function",
			src:  "f = (a, b) -> { a + b }",
			want: `
Program "t"
  ExprStmt
    Assign =
      Ident f
      FnDecl (a, b)
        body:
          Block
            ExprStmt
              Binary +
                Ident a
                Ident b
`,
		},
		{
			// A named `fn` is a declaration statement, so it is hoisted; an anonymous one
			// is a value, so it reaches the list as an expression and nothing is hoisted.
			name: "an anonymous fn is a statement's expression",
			src:  "async fn(u) { u }",
			want: `
Program "t"
  ExprStmt
    FnDecl async (u)
      body:
        Block
          ExprStmt
            Ident u
`,
		},
		{
			// §3.12: `->` ends a key wherever `:` does, and it is the only separator a
			// key that is not a string may take (§7.6).
			name: "dict keys that are not strings",
			src:  `{1 -> "a", -2.5 -> b, nil -> c, "s" -> d, k -> e}`,
			want: `
Program "t"
  ExprStmt
    Dict
      entry
        Int 1
        Str "a"
      entry
        Unary -
          Float 2.5
        Ident b
      entry
        Nil
        Ident c
      entry
        Str "s"
        Ident d
      entry
        Str "k"
        Ident e
`,
		},
		{
			name: "a trailing closure is the last argument",
			src:  "xs.reduce(0) { (a, b) -> a + b }",
			want: `
Program "t"
  ExprStmt
    MethodCall .reduce
      recv:
        Ident xs
      arg
        Int 0
      arg
        Closure (a, b)
          body:
            Block
              ExprStmt
                Binary +
                  Ident a
                  Ident b
`,
		},
		{
			name: "a trailing closure binds to the nearest preceding call",
			src:  `a.map { it }.join(",")`,
			want: `
Program "t"
  ExprStmt
    MethodCall .join
      recv:
        MethodCall .map
          recv:
            Ident a
          arg
            Closure implicit (it)
              body:
                Block
                  ExprStmt
                    Ident it
      arg
        Str ","
`,
		},
		{
			name: "named arguments bind by parameter name",
			src:  "f(1, a = 2, b = 3)",
			want: `
Program "t"
  ExprStmt
    Call
      fn:
        Ident f
      arg
        Int 1
      arg a =
        Int 2
      arg b =
        Int 3
`,
		},
		{
			name: "in is a binary operator under the range",
			src:  "a in 1..20",
			want: `
Program "t"
  ExprStmt
    Binary in
      Ident a
      Range ..
        Int 1
        Int 20
`,
		},
		{
			name: "trailers: zero-argument method, safe call, index, slice",
			src:  `x.f; d?.get("k"); a[1]; a[1, 2]`,
			want: `
Program "t"
  ExprStmt
    MethodCall .f
      recv:
        Ident x
  ExprStmt
    MethodCall ?.get
      recv:
        Ident d
      arg
        Str "k"
  ExprStmt
    Index
      Ident a
      Int 1
  ExprStmt
    Index
      Ident a
      Int 1
      Int 2
`,
		},
		{
			name: "if / else if / else",
			src:  "if a { 1 } else if b { 2 } else { 3 }",
			want: `
Program "t"
  ExprStmt
    If
      cond:
        Ident a
      then:
        Block
          ExprStmt
            Int 1
      else:
        If
          cond:
            Ident b
          then:
            Block
              ExprStmt
                Int 2
          else:
            Block
              ExprStmt
                Int 3
`,
		},
		{
			name: "while with break",
			src:  "while true { break 1 }",
			want: `
Program "t"
  ExprStmt
    While
      cond:
        Bool true
      body:
        Block
          Break
            Int 1
`,
		},
		{
			name: "for over a pair",
			src:  "for k, v in d { next }",
			want: `
Program "t"
  ExprStmt
    For k, v
      iter:
        Ident d
      body:
        Block
          Next
`,
		},
		{
			name: "destructuring assignment",
			src:  "a, b = pair",
			want: `
Program "t"
  ExprStmt
    Destructure =
      Pattern
        Ident a
        Ident b
      Ident pair
`,
		},
		{
			name: "a bracketed and nested pattern",
			src:  "[a, [b, c]] := xs",
			want: `
Program "t"
  ExprStmt
    Destructure :=
      Pattern
        Ident a
        Pattern
          Ident b
          Ident c
      Ident xs
`,
		},
		{
			name: "an array pattern in a match arm",
			src:  "match o { [x, 0] -> x; else -> 1 }",
			want: `
Program "t"
  ExprStmt
    Match
      subject:
        Ident o
      arm array
        pat
          Pattern
            Ident x
            Int 0
        body
          Block
            ExprStmt
              Ident x
      arm else
        body
          Block
            ExprStmt
              Int 1
`,
		},
		{
			name: "return with and without a value",
			src:  "return 1; return",
			want: `
Program "t"
  Return
    Int 1
  Return
`,
		},
		{
			name: "statement modifiers",
			src:  "x = 1 if c; x += 1 while x < 5",
			want: `
Program "t"
  If
    cond:
      Ident c
    then:
      Block
        ExprStmt
          Assign =
            Ident x
            Int 1
  While
    cond:
      Binary <
        Ident x
        Int 5
    body:
      Block
        ExprStmt
          Assign +=
            Ident x
            Int 1
`,
		},
		{
			name: "group is a statement list",
			src:  "(a; b).str",
			want: `
Program "t"
  ExprStmt
    MethodCall .str
      recv:
        Group
          ExprStmt
            Ident a
          ExprStmt
            Ident b
`,
		},
		{
			name: "try with and without a bound error",
			src:  `try f() else 0; try f() else (e) -> e["message"]`,
			want: `
Program "t"
  ExprStmt
    Try
      body:
        Call
          fn:
            Ident f
      fallback:
        Int 0
  ExprStmt
    Try e
      body:
        Call
          fn:
            Ident f
      fallback:
        Index
          Ident e
          Str "message"
`,
		},
		{
			name: "the braced try, with an ensure and a binder without an arrow",
			src:  "try { f() } else (e) { 0 } ensure { g() }",
			want: `
Program "t"
  ExprStmt
    Try e
      body:
        Block
          ExprStmt
            Call
              fn:
                Ident f
      fallback:
        Block
          ExprStmt
            Int 0
      ensure:
        Block
          ExprStmt
            Call
              fn:
                Ident g
`,
		},
		{
			name: "an ensure with no else catches nothing and still releases",
			src:  "try f() ensure { g() }",
			want: `
Program "t"
  ExprStmt
    Try
      body:
        Call
          fn:
            Ident f
      ensure:
        Block
          ExprStmt
            Call
              fn:
                Ident g
`,
		},
		{
			name: "match on one line",
			src:  `match $__sent.lower.trim { "да" -> 1; "нет" -> 0; else -> nil }`,
			want: `
Program "t"
  ExprStmt
    Match
      subject:
        MethodCall .trim
          recv:
            MethodCall .lower
              recv:
                Global $__sent
      arm value
        pat
          Str "да"
        body
          Block
            ExprStmt
              Int 1
      arm value
        pat
          Str "нет"
        body
          Block
            ExprStmt
              Int 0
      arm else
        body
          Block
            ExprStmt
              Nil
`,
		},
		{
			name: "match arm kinds: regex, in, guard, several patterns",
			src:  "match x { /re/ if c -> 1\n in [1], in [2] -> 2\n if d -> 3\n else -> 4 }",
			want: `
Program "t"
  ExprStmt
    Match
      subject:
        Ident x
      arm value
        pat
          Regex /re/
        guard
          Ident c
        body
          Block
            ExprStmt
              Int 1
      arm in
        pat
          Array
            Int 1
        pat
          Array
            Int 2
        body
          Block
            ExprStmt
              Int 2
      arm guard
        pat
          Ident d
        body
          Block
            ExprStmt
              Int 3
      arm else
        body
          Block
            ExprStmt
              Int 4
`,
		},
		{
			name: "match with no subject is the if/else if ladder",
			src:  "match {\n  yes -> \"confirm\"\n  s.len > 500 -> \"too_long\"\n  else -> \"unknown\"\n}",
			want: `
Program "t"
  ExprStmt
    Match
      arm value
        pat
          Ident yes
        body
          Block
            ExprStmt
              Str "confirm"
      arm value
        pat
          Binary >
            MethodCall .len
              recv:
                Ident s
            Int 500
        body
          Block
            ExprStmt
              Str "too_long"
      arm else
        body
          Block
            ExprStmt
              Str "unknown"
`,
		},
		{
			name: "a match arm body may be a closure body",
			src:  "match x { 1 -> { a; b } }",
			want: `
Program "t"
  ExprStmt
    Match
      subject:
        Ident x
      arm value
        pat
          Int 1
        body
          Block
            ExprStmt
              Ident a
            ExprStmt
              Ident b
`,
		},
		{
			name: "ranges",
			src:  "0..5; 0..<n",
			want: `
Program "t"
  ExprStmt
    Range ..
      Int 0
      Int 5
  ExprStmt
    Range ..<
      Int 0
      Ident n
`,
		},
		{
			name: "assignment operators",
			src:  "a := 1; a ||= 2; a ??= 3; a **= 2",
			want: `
Program "t"
  ExprStmt
    Assign :=
      Ident a
      Int 1
  ExprStmt
    Assign ||=
      Ident a
      Int 2
  ExprStmt
    Assign ??=
      Ident a
      Int 3
  ExprStmt
    Assign **=
      Ident a
      Int 2
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(t, tt.src)
			if want := strings.TrimPrefix(tt.want, "\n"); got != want {
				t.Errorf("Parse(%q) tree =\n%s\nwant\n%s", tt.src, got, want)
			}
		})
	}
}

// TestPrecedence pins the "consequences worth pinning as tests" of SPEC §5.1, plus the
// associativity of the levels that are not left-associative.
func TestPrecedence(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"assignment is looser than ||", "a = b || c", "Assign =(Ident a, Logical ||(Ident b, Ident c))"},
		{"unary ! is tighter than ==", "!x == y", "Binary ==(Unary !(Ident x), Ident y)"},
		{"+ is tighter than ==", "1 + 2 == 3", "Binary ==(Binary +(Int 1, Int 2), Int 3)"},
		{"+ is tighter than == for strings", "'a' + 'b' == 'ab'", `Binary ==(Binary +(Str "a", Str "b"), Str "ab")`},
		{"?? is left associative", "a ?? b ?? c", "Logical ??(Logical ??(Ident a, Ident b), Ident c)"},
		{"** is right associative", "2 ** 3 ** 2", "Binary **(Int 2, Binary **(Int 3, Int 2))"},
		{"** is tighter than unary minus", "(-2) ** 2", "Binary **(Group(Unary -(Int 2)), Int 2)"},
		{"* is tighter than +", "1 + 2 * 3", "Binary +(Int 1, Binary *(Int 2, Int 3))"},
		{"comparison is tighter than equality", "a < b == c", "Binary ==(Binary <(Ident a, Ident b), Ident c)"},
		{"range is tighter than comparison", "a..b < c", "Binary <(Range ..(Ident a, Ident b), Ident c)"},
		{"range is tighter than in", "a in 1..20", "Binary in(Ident a, Range ..(Int 1, Int 20))"},
		{"in is tighter than comparison", "a in xs < c", "Binary <(Binary in(Ident a, Ident xs), Ident c)"},
		{"&& is looser than in", "a in xs && b", "Logical &&(Binary in(Ident a, Ident xs), Ident b)"},
		{"+ is tighter than in", "a + 1 in xs", "Binary in(Binary +(Ident a, Int 1), Ident xs)"},
		{"&& is tighter than ||", "a && b || c", "Logical ||(Logical &&(Ident a, Ident b), Ident c)"},
		{"|| is tighter than ??", "a || b ?? c", "Logical ??(Logical ||(Ident a, Ident b), Ident c)"},
		{"ternary is right associative", "a ? b : c ? d : e", "Ternary(Ident a, Ident b, Ternary(Ident c, Ident d, Ident e))"},
		{"trailers are tightest", "-x.f", "Unary -(MethodCall .f(Ident x))"},
		{"the modifier binds loosest", "x = 1 if c", "If(Ident c, Block(Assign =(Ident x, Int 1)))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := Parse("t", tt.src)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.src, err)
			}
			if len(prog.Stmts) != 1 {
				t.Fatalf("Parse(%q) produced %d statements; want 1", tt.src, len(prog.Stmts))
			}
			if got := sexpr(prog.Stmts[0]); got != tt.want {
				t.Errorf("Parse(%q) = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// sexpr renders a node on one line, which keeps a precedence table readable. It is a
// test helper only; ast.Dump is the format the golden tables use.
func sexpr(n ast.Node) string {
	switch x := n.(type) {
	case *ast.ExprStmt:
		return sexpr(x.X)
	case *ast.BlockStmt:
		return "Block(" + joinNodes(stmtNodes(x.Stmts)) + ")"
	case *ast.IfExpr:
		parts := []ast.Node{x.Cond, x.Then}
		if x.Else != nil {
			parts = append(parts, x.Else)
		}
		return "If(" + joinNodes(parts) + ")"
	case *ast.AssignExpr:
		return "Assign " + x.Op.String() + "(" + joinNodes([]ast.Node{x.Target, x.Value}) + ")"
	case *ast.BinaryExpr:
		return "Binary " + x.Op.String() + "(" + joinNodes([]ast.Node{x.L, x.R}) + ")"
	case *ast.LogicalExpr:
		return "Logical " + x.Op.String() + "(" + joinNodes([]ast.Node{x.L, x.R}) + ")"
	case *ast.UnaryExpr:
		return "Unary " + x.Op.String() + "(" + sexpr(x.X) + ")"
	case *ast.TernaryExpr:
		return "Ternary(" + joinNodes([]ast.Node{x.Cond, x.Then, x.Else}) + ")"
	case *ast.RangeExpr:
		op := ".."
		if x.Exclusive {
			op = "..<"
		}
		return "Range " + op + "(" + joinNodes([]ast.Node{x.Lo, x.Hi}) + ")"
	case *ast.MethodCall:
		return "MethodCall ." + x.Name + "(" + joinNodes(append([]ast.Node{x.Recv}, exprNodes(x.Args)...)) + ")"
	case *ast.GroupExpr:
		return "Group(" + joinNodes(stmtNodes(x.Stmts)) + ")"
	}
	return strings.TrimSuffix(ast.Dump(n), "\n")
}

func joinNodes(ns []ast.Node) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, sexpr(n))
	}
	return strings.Join(parts, ", ")
}

func stmtNodes(ss []ast.Stmt) []ast.Node {
	out := make([]ast.Node, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

func exprNodes(es []ast.Expr) []ast.Node {
	out := make([]ast.Node, 0, len(es))
	for _, e := range es {
		out = append(out, e)
	}
	return out
}

// TestMatchSemantics covers the pattern forms of SPEC §5.3 and the structural promises
// of §5.2–§5.5: the subject is optional, several patterns in one arm mean "or", a
// trailing `if` is an extra "and", and `else` is only legal last.
func TestMatchSemantics(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		hasSubject bool
		kinds      []ast.ArmKind
		pats       []int
		guards     []bool
	}{
		{
			name:       "literal, regex and else",
			src:        `match s { "да" -> 1; /re/ -> 2; else -> 3 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmValue, ast.ArmValue, ast.ArmElse},
			pats:       []int{1, 1, 0},
			guards:     []bool{false, false, false},
		},
		{
			name:       "membership",
			src:        `match s { in ["да", "ага"] -> 1 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmIn},
			pats:       []int{1},
			guards:     []bool{false},
		},
		{
			name:       "several patterns mean or",
			src:        `match s { "да", "ага", "ок" -> 1 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmValue},
			pats:       []int{3},
			guards:     []bool{false},
		},
		{
			name:       "a trailing if is a guard",
			src:        `match s { "да" if ready -> 1 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmValue},
			pats:       []int{1},
			guards:     []bool{true},
		},
		{
			name:       "a leading if ignores the subject",
			src:        `match s { if ready -> 1; else -> 2 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmGuard, ast.ArmElse},
			pats:       []int{1, 0},
			guards:     []bool{false, false},
		},
		{
			name:       "an array pattern is its own arm kind",
			src:        `match o { [x, y] -> x; else -> 2 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmArray, ast.ArmElse},
			pats:       []int{1, 0},
			guards:     []bool{false, false},
		},
		{
			name:       "membership keeps meaning membership",
			src:        `match o { in [1, 2] -> 1 }`,
			hasSubject: true,
			kinds:      []ast.ArmKind{ast.ArmIn},
			pats:       []int{1},
			guards:     []bool{false},
		},
		{
			name:       "no subject: every pattern is a condition",
			src:        `match { a -> 1; b -> 2; else -> 3 }`,
			hasSubject: false,
			kinds:      []ast.ArmKind{ast.ArmValue, ast.ArmValue, ast.ArmElse},
			pats:       []int{1, 1, 0},
			guards:     []bool{false, false, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := Parse("t", tt.src)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.src, err)
			}
			m, ok := prog.Stmts[0].(*ast.ExprStmt).X.(*ast.MatchExpr)
			if !ok {
				t.Fatalf("Parse(%q) top node = %T; want *ast.MatchExpr", tt.src, prog.Stmts[0])
			}
			if got := m.Subject != nil; got != tt.hasSubject {
				t.Errorf("Parse(%q) has subject = %v; want %v", tt.src, got, tt.hasSubject)
			}
			if len(m.Arms) != len(tt.kinds) {
				t.Fatalf("Parse(%q) produced %d arms; want %d", tt.src, len(m.Arms), len(tt.kinds))
			}
			for i, arm := range m.Arms {
				if arm.Kind != tt.kinds[i] {
					t.Errorf("arm %d kind = %v; want %v", i, arm.Kind, tt.kinds[i])
				}
				if len(arm.Pats) != tt.pats[i] {
					t.Errorf("arm %d has %d patterns; want %d", i, len(arm.Pats), tt.pats[i])
				}
				if got := arm.Guard != nil; got != tt.guards[i] {
					t.Errorf("arm %d guard = %v; want %v", i, got, tt.guards[i])
				}
				if arm.Body == nil {
					t.Errorf("arm %d has no body", i)
				}
			}
		})
	}
}

func TestMatchElseMustBeLast(t *testing.T) {
	src := `match s { else -> 1; "да" -> 2 }`
	_, err := Parse("t", src)
	if err == nil {
		t.Fatalf("Parse(%q) error = nil; want a diagnostic", src)
	}
	if got := flatten(err); len(got) != 1 || got[0].Msg != "'else' must be the last arm of a match" {
		t.Fatalf("Parse(%q) errors = %v; want exactly the else-position diagnostic", src, err)
	}
}

// TestAmbiguityDiagnostics is acceptance criterion A2: every row of SPEC §5.6 produces
// its message verbatim, at the right line and column, and nothing cascades out of it.
func TestAmbiguityDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
		line int
		col  int
		// extra tolerates diagnostics the lexer emits after the one §5.6 names. It is
		// non-zero for exactly one row: the second '|' of a Ruby block parameter list
		// is an unclassifiable rune wherever it appears, and the lexer reports it.
		extra int
	}{
		{name: "unary minus in front of **", src: "-2 ** 2",
			msg: "ambiguous: write -(2 ** 2) or (-2) ** 2", line: 1, col: 1},
		{name: "trailer on a range bound", src: "0..5.map { it }",
			msg: "ambiguous range: write (0..5).map", line: 1, col: 2},
		{name: "chained range", src: "1..2..3",
			msg: "range operator is non-associative", line: 1, col: 5},
		{name: "chained in", src: "a in b in c",
			msg: "'in' is non-associative: write (a in b) in c if that is what you meant", line: 1, col: 8},
		{name: "a dict-style keyword argument", src: "f(1, a: 2)",
			msg: "a named argument is written 'a = …'; for a dict argument write f({a: …})", line: 1, col: 6},
		{name: "a positional argument after a named one", src: "f(a = 1, 2)",
			msg: "a positional argument may not follow a named one; move it before 'a = …'", line: 1, col: 10},
		{name: "the same argument named twice", src: "f(a = 1, a = 2)",
			msg: "argument 'a' is named twice", line: 1, col: 10},
		{name: "a trailing closure after a named argument", src: "f(a = 1) { 2 }",
			msg:  "a trailing closure is a positional argument, so it cannot follow the named argument 'a = …': pass the closure by name too, or give every argument by position",
			line: 1, col: 10},
		{name: "a trailing closure after a named argument on a method", src: "x.f(a = 1) { 2 }",
			msg:  "a trailing closure is a positional argument, so it cannot follow the named argument 'a = …': pass the closure by name too, or give every argument by position",
			line: 1, col: 12},
		{name: "equality against a regex", src: "s == /re/",
			msg: "'==' with a regex operand: use '~' to match", line: 1, col: 3},
		{name: "the Ruby match operator", src: "s =~ /re/",
			msg: "'=~' is not an mzs operator; use '~'", line: 1, col: 3},
		{name: "predicate suffix", src: "x.empty?",
			msg: "'?' is not part of an identifier; did you mean 'empty'?", line: 1, col: 8},
		{name: "renamed method", src: "x.downcase",
			msg: "undefined method 'downcase'; did you mean 'lower'?", line: 1, col: 3},
		{name: "and", src: "a and b",
			msg: "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", line: 1, col: 3},
		{name: "or", src: "a or b",
			msg: "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", line: 1, col: 3},
		{name: "not", src: "not a",
			msg: "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'", line: 1, col: 1},
		{name: "do and end", src: "if c do 1 end",
			msg: "'do'/'end' are not mzs keywords; use braces: if c { … }", line: 1, col: 6},
		{name: "elsif", src: "elsif c { 1 }",
			msg: "'elsif' is not an mzs keyword; use 'else if'", line: 1, col: 1},
		{name: "unless", src: "unless c { 1 }",
			msg: "'unless' is not an mzs keyword; use 'if !(c)'", line: 1, col: 1},
		{name: "until", src: "until c { 1 }",
			msg: "'until' is not an mzs keyword; use 'while !(c)'", line: 1, col: 1},
		{name: "loop", src: "loop { 1 }",
			msg: "'loop' is not an mzs keyword; use 'while true { … }'", line: 1, col: 1},
		{name: "def", src: "def f() { }",
			msg: "'def' is not an mzs keyword; use 'fn'", line: 1, col: 1},
		{name: "word array", src: "%w[a b]",
			msg: `'%w' is not mzs; write ["a", "b"]`, line: 1, col: 1},
		{name: "symbol", src: ":name",
			msg: `mzs has no symbols; write "name"`, line: 1, col: 1},
		{name: "bracket dict", src: "[a: 1]",
			msg: "a dict is written {a: 1}", line: 1, col: 1},
		{name: "bracket dict with a string key", src: `["a": 1]`,
			msg: "a dict is written {a: 1}", line: 1, col: 1},
		{name: "bracket empty dict", src: "[:]",
			msg: "the empty dict is written {}", line: 1, col: 1},
		{name: "brace dict after a call", src: "f {a: 1}",
			msg: "a dict after a call is written f({a: 1})", line: 1, col: 3},
		{name: "brace dict in a body", src: "if c {a: 1}",
			msg: "this '{' opens the if body; write { {a: 1} } for a dict", line: 1, col: 6},
		// §3.10 suppresses the newline after `{`, so the lookahead of §3.12 reads the
		// key from the next line unaided and these reach the same two fix-its.
		{name: "multi-line brace dict after a call", src: "f {\n  a: 1\n}",
			msg: "a dict after a call is written f({a: 1})", line: 1, col: 3},
		{name: "multi-line brace dict in a body", src: "if c {\n  a: 1\n}",
			msg: "this '{' opens the if body; write { {a: 1} } for a dict", line: 1, col: 6},
		{name: "hash rocket", src: "k => v",
			msg: "'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure", line: 1, col: 3},
		{name: "pipe closure parameters", src: "{ |x| x }",
			msg: "closure parameters are parenthesised: { (x) -> … }", line: 1, col: 3, extra: 1},
		{name: "the Ruby safe call", src: "x &. y",
			msg: "'&.' is not an mzs operator; use '?.'", line: 1, col: 3},
		{name: "rescue", src: "a rescue b",
			msg: "'rescue' is not an mzs keyword; use 'try a else b'", line: 1, col: 3},
		{name: "hash interpolation", src: `"#{x}"`,
			msg: `string interpolation is "${x}"`, line: 1, col: 2},
		{name: "the Ruby exclusive range", src: "1...5",
			msg: "'...' is not an mzs operator; use '..<'", line: 1, col: 2},
		{name: "scope resolution", src: "a::B",
			msg: "'::' is not an mzs operator; use '.'", line: 1, col: 2},
		{name: "the one.mzs typo", src: "a___1 = 13213\nbcde = 222\nstr =! \"sdfsdf\"",
			msg: "unexpected '!' after '='; did you mean '!='?", line: 3, col: 6},
		{name: "to_s", src: "x.to_s",
			msg: "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", line: 1, col: 3},
		{name: "to_i", src: "x.to_i",
			msg: "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", line: 1, col: 3},
		{name: "to_json", src: "x.to_json",
			msg: "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'", line: 1, col: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := Parse("t", tt.src)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil; want %q", tt.src, tt.msg)
			}
			if prog != nil {
				t.Errorf("Parse(%q) returned a program alongside an error", tt.src)
			}
			got := flatten(err)
			if len(got) != 1+tt.extra {
				t.Fatalf("Parse(%q) reported %d diagnostics; want %d:\n%v", tt.src, len(got), 1+tt.extra, err)
			}
			if got[0].Msg != tt.msg {
				t.Fatalf("Parse(%q) message = %q; want %q", tt.src, got[0].Msg, tt.msg)
			}
			if got[0].Pos.Line != tt.line || got[0].Pos.Col != tt.col {
				t.Errorf("Parse(%q) position = %d:%d; want %d:%d",
					tt.src, got[0].Pos.Line, got[0].Pos.Col, tt.line, tt.col)
			}
		})
	}
}

// TestStaticParseRestrictions checks both halves of SPEC §4.5: the two rejected shapes,
// and the neighbouring shapes the restriction deliberately leaves legal.
func TestStaticParseRestrictions(t *testing.T) {
	rejected := []struct {
		name string
		src  string
		msg  string
	}{
		{"minus in front of a power", "-2 ** 2", "ambiguous: write -(2 ** 2) or (-2) ** 2"},
		{"plus in front of a power", "+2 ** 2", "ambiguous: write -(2 ** 2) or (-2) ** 2"},
		{"a method on an int range bound", "0..5.map { it }", "ambiguous range: write (0..5).map"},
		{"a call on a float range bound", "0..5.0.floor", "ambiguous range: write (0..5).map"},
		{"an index on an int range bound", "0..5[0]", "ambiguous range: write (0..5).map"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("t", tt.src)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil; want %q", tt.src, tt.msg)
			}
			if got := flatten(err); len(got) != 1 || got[0].Msg != tt.msg {
				t.Fatalf("Parse(%q) errors = %v; want exactly %q", tt.src, err, tt.msg)
			}
		})
	}

	accepted := []struct {
		name string
		src  string
	}{
		{"parenthesised operand", "-(2 ** 2)"},
		{"parenthesised base", "(-2) ** 2"},
		{"not on a power", "!x ** 2"},
		{"a method on a named range bound", "0..xs.len"},
		{"a method on an identifier bound", "0..n.abs"},
		{"a method on a parenthesised range", "(0..5).map { it }"},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse("t", tt.src); err != nil {
				t.Fatalf("Parse(%q) error = %v; want nil", tt.src, err)
			}
		})
	}
}

// TestBraceIsAClosureOrADict covers §3.11: in operand position a `{` opens a dict when
// the §3.12 lookahead says so and a closure otherwise, and the one restriction is that a
// header's `{` opens the body.
func TestBraceIsAClosureOrADict(t *testing.T) {
	if _, err := Parse("t", `if {a: 1}.has("a") { 1 } else { 2 }`); err != nil {
		t.Errorf("a dict literal in a header needs no parentheses: %v", err)
	}
	if got, want := parse(t, "{ nil }"), "Closure"; !strings.Contains(got, want) {
		t.Errorf("{ nil } is the empty closure value, got\n%s", got)
	}
	if _, err := Parse("t", "if x == {a: 1} { 1 }"); err != nil {
		t.Errorf("a brace dict as a header operand must parse: %v", err)
	}
	// A body's `{` still wins: the dict has to be written inside it.
	if _, err := Parse("t", "if c { {a: 1} } else { {b: 2} }"); err != nil {
		t.Errorf("a brace dict inside a body must parse: %v", err)
	}
	// Trailing position stays the closure slot, empty braces included.
	if got, want := parse(t, "xs.each { }"), "Closure"; !strings.Contains(got, want) {
		t.Errorf("an empty trailing brace is a closure, got\n%s", got)
	}
	if _, err := Parse("t", "if (xs.any { it > 5 }) { 1 }"); err != nil {
		t.Errorf("a parenthesised trailing closure in a header must parse: %v", err)
	}
	src := "if xs.any { it > 5 } { 1 }"
	if _, err := Parse("t", src); err == nil {
		t.Errorf("Parse(%q) error = nil; want the header to end at the first '{'", src)
	}
	// The same call outside a header keeps its trailing closure.
	if got, want := parse(t, "xs.any { it > 5 }"), "MethodCall .any"; !strings.Contains(got, want) {
		t.Errorf("Parse of a trailing closure outside a header =\n%s", got)
	}
}

// TestArrowFunction covers §4.1's arrow form and the two positions where the tokens it
// reads are already spoken for: a `match` arm, where the `->` opens the arm (§5.3), and a
// body, where a `(…) ->` is the parameter list a body may not have.
func TestArrowFunction(t *testing.T) {
	// The arrow form and the keyword form build the same tree, which is the whole claim.
	if got, want := parse(t, "(a) -> { a }"), parse(t, "fn(a) { a }"); got != want {
		t.Errorf("(a) -> { a } parsed as\n%s\nwant the tree of fn(a) { a }:\n%s", got, want)
	}
	for _, src := range []string{
		"() -> { 42 }",
		"f((x) -> { x })",
		"[(x) -> { x }]",
		"{k: (x) -> { x }}",
		"(n) -> { (x) -> { x * n } }",           // nested, the inner one in body position
		"fn f() { (x) -> { x } }",               // body position again, with a keyword
		`match x { (1) -> { "a" }; else -> 2 }`, // an arm pattern keeps its arrow
		`match x { in (1..5) -> "y"; else -> 2 }`,
		"try f() else (e) -> 0", // the error binder of §8.11 is not an arrow function
	} {
		if _, err := Parse("t", src); err != nil {
			t.Errorf("Parse(%q) error = %v; want nil", src, err)
		}
	}
	// A header's `{` opens the body (§3.11), so an arrow function in one needs its own
	// parentheses — exactly as a trailing closure does.
	if _, err := Parse("t", "if (x) -> { 1 } { 2 }"); err == nil {
		t.Error("an unparenthesised arrow function in a header parsed; want the §3.11 rule to hold")
	}
	if _, err := Parse("t", "if ((x) -> { 1 })(2) { 3 }"); err != nil {
		t.Errorf("a parenthesised arrow function in a header: %v", err)
	}
	// The body of an `if` still may not declare parameters: that shape has its own
	// diagnostic and the brace is what tells the two apart.
	if _, err := Parse("t", "if c { (x) -> x }"); err == nil {
		t.Error("a parameter list on an if body parsed; want the §4.1 diagnostic")
	}
}

// TestCollectionLookahead walks the five rules of §3.12 in order.
func TestCollectionLookahead(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"rule 1: empty array", "[]", "Array"},
		{"rule 2: empty dict", "{}", "Dict"},
		{"rule 3: identifier key", "{a: 1}", "Dict"},
		{"rule 3: string key", `{"a": 1}`, "Dict"},
		{"rule 4: computed key", "{(k): 1}", "Dict"},
		{"rule 4: parenthesised element", "[(k), 1]", "Array"},
		{"rule 5: ternary element", "[x ? a : b]", "Array"},
		{"rule 5: values", "[1, 2, 3]", "Array"},
		// §3.11: the same five rules decide a `{` in operand position, with the empty
		// brace reading as the dict and everything else falling through to the closure.
		{"brace: empty dict", "{}", "Dict"},
		{"brace: identifier key", "{a: 1}", "Dict"},
		{"brace: string key", `{"a": 1}`, "Dict"},
		{"brace: computed key", "{(k): 1}", "Dict"},
		{"brace: parameters are not a key", "{(x) -> x}", "Closure"},
		{"brace: implicit it", "{ it * 2 }", "Closure"},
		{"brace: ternary body", "{x ? a : b}", "Closure"},
		// The arrow ends a key wherever the colon does (rule 2), and a literal in front
		// of one is a key and nothing else (rule 4) — but never after a `)`, which is a
		// parameter list first.
		{"brace: arrow after an identifier", "{a -> 1}", "Dict"},
		{"brace: arrow after a string", `{"a" -> 1}`, "Dict"},
		{"brace: literal key", "{1 -> 1}", "Dict"},
		{"brace: signed literal key", "{-1.5 -> 1}", "Dict"},
		{"brace: literal keys of every kind", "{true -> 1, nil -> 2, /re/ -> 3}", "Dict"},
		{"brace: a negative number is not a key", "{ -1 }", "Closure"},
		{"brace: a literal body is not a key", "{ 1 }", "Closure"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(t, tt.src)
			line := strings.Fields(strings.Split(got, "\n")[2])[0]
			if line != tt.want {
				t.Errorf("Parse(%q) built a %s; want %s", tt.src, line, tt.want)
			}
		})
	}
}

// TestParseErrors pins the ordinary syntax diagnostics and their positions.
func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
		line int
		col  int
	}{
		{"unclosed paren", "(1 + 2", "expected ')' in parenthesised expression, found end of input", 1, 7},
		{"unclosed bracket", "[1, 2", "expected ']' in array literal, found end of input", 1, 6},
		{"unclosed closure", "{ 1", "expected '}' in closure, found end of input", 1, 4},
		{"missing if body", "if c", "expected '{' in if body, found end of input", 1, 5},
		{"bad assignment target", "1 = 2", "cannot assign to this expression", 1, 3},
		{"empty expression", "1 +", "unexpected end of input", 1, 4},
		{"missing arm arrow", "match x { 1 2 }", "expected '->' in match arm, found 2", 1, 13},
		{"empty match", "match x { }", "a match needs at least one arm", 1, 11},
		{"missing for variable", "for in xs { }", "expected a loop variable, found 'in'", 1, 5},
		{"a function name is a name", "fn 1(a) { a }", "expected a function name or '(' after 'fn', found 1", 1, 4},
		{"an exported fn needs a name", "export fn (a) { a }", "'export' needs a name: write `export fn f(…) { … }` or `export f = fn(…) { … }`", 1, 1},
		{"an arrow function's body is braced", "f = (x) -> x * 2",
			"an arrow function's body is braced: (x) -> { x * 2 }, or write the closure { (x) -> x * 2 }", 1, 9},
		{"async has one spelling", "f = async (x) -> { x }", "an async function is written `async fn(a, b) { … }`", 1, 5},
		{"a literal dict key takes an arrow", "{1: 2}", "a dict key that is not a string takes '->', not ':'", 1, 3},
		{"a computed dict key takes a colon", "{a: 1, (k) -> 2}", "a computed dict key takes ':', not '->': write (k): v", 1, 12},
		{"a dict entry needs a separator", "{a: 1, \"b\" 2}", "expected ':' or '->' in dict entry, found 2", 1, 12},
		{"a body may not take parameters", "if c { (x) -> x }", "the body of if body cannot declare parameters", 1, 9},
		{"a rest parameter must be last", "fn f(*a, b) { a }", "a rest parameter must be last", 1, 7},
		{"destructuring is = or :=", "a, b += xs", "destructuring assigns with '=' or ':=', not '+='", 1, 6},
		{"a destructuring target is writable", "f(1), b = xs", "cannot assign to this expression: a destructuring target is a name, a $var, an index or a nested [ … ]", 1, 1},
		{"one array pattern per arm", "match x { 1, [a, b] -> 2 }", "an array pattern must be the only pattern in its arm", 1, 14},
		// Recovery inside a collection has to consume something: sync stops *at* a `;`
		// without eating it, and one diagnostic per position means the error budget
		// would never end the loop either. Left unfixed this input never returns.
		{"a bad dict key does not spin", "{A:0,A;;;", "expected a dict key, found 'A'", 1, 6},
		{"an array pattern needs a subject", "match { [a, b] -> 1 }", "an array pattern needs a subject: match xs { [a, b] -> … }", 1, 9},
		{"a try handles or releases", "try { f() }", "expected 'else' or 'ensure' in try, found end of input", 1, 12},
		{"an ensure takes a block", "try f() else 0 ensure g()", "expected '{' in ensure, found 'g'", 1, 23},
		{"a braced try in a header", "if try { f() } else { 0 } { 1 }",
			"a braced 'try' cannot open a header; parenthesise it: if (try { … } else { … }) { … }", 1, 8},
		{"a braced else in a header", "if try f() else { 0 } { 1 }",
			"a braced 'try' cannot open a header; parenthesise it: if (try { … } else { … }) { … }", 1, 17},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("t", tt.src)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil; want %q", tt.src, tt.msg)
			}
			var got *Error
			for _, e := range flatten(err) {
				if e.Msg == tt.msg {
					got = e
					break
				}
			}
			if got == nil {
				t.Fatalf("Parse(%q) errors = %v; want one saying %q", tt.src, err, tt.msg)
			}
			if got.Pos.Line != tt.line || got.Pos.Col != tt.col {
				t.Errorf("Parse(%q) position = %d:%d; want %d:%d", tt.src, got.Pos.Line, got.Pos.Col, tt.line, tt.col)
			}
		})
	}
}

// TestParseErrorBudget checks that recovery keeps going and that §17's cap holds.
func TestParseErrorBudget(t *testing.T) {
	src := strings.Repeat("1 = 2\n", 40)
	_, err := Parse("t", src)
	if err == nil {
		t.Fatalf("Parse(%q…) error = nil; want several", src[:12])
	}
	if n := len(flatten(err)); n == 0 || n > MaxErrors {
		t.Errorf("Parse reported %d errors; want 1..%d", n, MaxErrors)
	}
}

// TestParseMultiline covers the layouts §3.10's suppression sets exist for: leading-dot
// chains, a hanging `else`, trailing commas across lines, and multi-line `match` arms —
// where the newline in front of the final `else` is suppressed and the arm needs none.
func TestParseMultiline(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "leading dot chain",
			src:  "$__sent\n  .lower\n  .trim == \"оператор\"",
			want: `
Program "t"
  ExprStmt
    Binary ==
      MethodCall .trim
        recv:
          MethodCall .lower
            recv:
              Global $__sent
      Str "оператор"
`,
		},
		{
			name: "hanging else",
			src:  "if c { a }\nelse { b }",
			want: `
Program "t"
  ExprStmt
    If
      cond:
        Ident c
      then:
        Block
          ExprStmt
            Ident a
      else:
        Block
          ExprStmt
            Ident b
`,
		},
		{
			name: "multi-line dict with a trailing comma",
			src:  "m = {\n  a: 1,\n  b: 2,\n}",
			want: `
Program "t"
  ExprStmt
    Assign =
      Ident m
      Dict
        entry
          Str "a"
          Int 1
        entry
          Str "b"
          Int 2
`,
		},
		{
			name: "multi-line match arms",
			src:  "match s {\n  in [\"да\"] -> \"yes\"\n  \"нет\" -> \"no\"\n  else -> \"unknown\"\n}",
			want: `
Program "t"
  ExprStmt
    Match
      subject:
        Ident s
      arm in
        pat
          Array
            Str "да"
        body
          Block
            ExprStmt
              Str "yes"
      arm value
        pat
          Str "нет"
        body
          Block
            ExprStmt
              Str "no"
      arm else
        body
          Block
            ExprStmt
              Str "unknown"
`,
		},
		{
			name: "a closure body across lines",
			src:  "xs.each { (o) ->\n  println(o)\n}",
			want: `
Program "t"
  ExprStmt
    MethodCall .each
      recv:
        Ident xs
      arg
        Closure (o)
          body:
            Block
              ExprStmt
                Call
                  fn:
                    Ident println
                  arg
                    Ident o
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(t, tt.src)
			if want := strings.TrimPrefix(tt.want, "\n"); got != want {
				t.Errorf("Parse(%q) tree =\n%s\nwant\n%s", tt.src, got, want)
			}
		})
	}
}

// TestParseShippedExamples is the front-end half of acceptance criterion A2: the shipped
// examples — every construct the language has, spread over thirty programs — parse
// without the evaluator's help.
func TestParseShippedExamples(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"the language tour parses", "../../examples/01_values_and_operators.mzs"},
		{"match arms parse", "../../examples/03_match_dispatch.mzs"},
		{"the regex toolkit parses", "../../examples/10_regex_toolkit.mzs"},
		{"a state machine parses", "../../examples/17_state_machine.mzs"},
		{"async fn parses", "../../examples/28_async_tasks.mzs"},
		{"destructuring parses", "../../examples/33_destructuring.mzs"},
		{"the http service parses", "../../examples/30_http_service.mzs"},
		{"the api pipeline parses", "../../examples/31_api_pipeline.mzs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := os.ReadFile(tt.path)
			if err != nil {
				t.Skipf("cannot read %s: %v", tt.path, err)
			}
			prog, perr := Parse(tt.path, string(src))
			if perr != nil {
				t.Fatalf("Parse(%s) error = %v; want nil", tt.path, perr)
			}
			if len(prog.Stmts) == 0 {
				t.Errorf("Parse(%s) produced an empty program", tt.path)
			}
		})
	}
}

// TestParseTokensAcceptsTruncatedStream guards the contract that ParseTokens tolerates
// a slice that does not end in EOF, which is how a --tokens dump may arrive.
func TestParseTokensAcceptsTruncatedStream(t *testing.T) {
	toks := []token.Token{
		{Kind: token.INT, Value: "1", Pos: token.Pos{Line: 1, Col: 1}, End: token.Pos{Line: 1, Col: 2}},
	}
	prog, err := ParseTokens("t", toks)
	if err != nil {
		t.Fatalf("ParseTokens error = %v; want nil", err)
	}
	if len(prog.Stmts) != 1 {
		t.Errorf("ParseTokens produced %d statements; want 1", len(prog.Stmts))
	}
}

// FuzzParse asserts A7 for the front end: no input may panic, and a successful parse
// must yield a non-nil program with no nil children.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"", " ", "\n\n\n", ";;;",
		`s = $__sent.lower.trim; s == "да" || s ~ /^ага|конечно/`,
		`fn f(a, b) { a += b; print(a); return a }; f(1, 2)`,
		`3.times.each { print(it) }`,
		`d = {a: 1, b: 2}; d.keys.join(",")`,
		`match s { in ["да"] -> 1; /re/i if c -> 2; else -> nil }`,
		`match { a -> 1 }`,
		`try f() else (e) -> e["message"]`,
		`for k, v in d { println("${k}=${v}") }`,
		`a, b = pair`, `[a, [b, c]] := xs`, `d["k"], $g = pair`, `a, b += xs`,
		`match o { [x, y] -> x + y; [] -> 0; else -> nil }`, `match { [a, b] -> 1 }`,
		`%w[да ага]`, `"#{x}"`, `{a: 1}`, `k => v`, `:sym`, `a::B`, `1...5`,
		`x.empty?`, `a rescue b`, `not a`, `if c do 1 end`, `-2 ** 2`, `0..5.map { it }`,
		`"unterminated`, `'unterminated`, "/unterminated",
		`str =! "x"`, `1..2..3`, `(((((`, `}}}}`, `@x`, `$`, `#`, `//`,
		`0x`, `1e`, `1_000_000`, `"\u{1F600}"`, `"\xff"`, "\x00\x01\x02",
		`привет == "да"`, `🌲`, `x ? y : z`, `{}`, `[]`, `{ (x) -> x }`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		prog, err := Parse("fuzz", src)
		if err == nil && prog == nil {
			t.Fatalf("Parse(%q) returned (nil, nil)", src)
		}
		if err != nil && prog != nil {
			t.Fatalf("Parse(%q) returned both a program and an error", src)
		}
		if prog != nil {
			// Dump walks every node; a node the parser built with a nil child would
			// show up here rather than in the evaluator.
			_ = ast.Dump(prog)
		}
	})
}
