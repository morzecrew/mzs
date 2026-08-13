package ast_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"mzs/internal/ast"
	"mzs/internal/parser"
	"mzs/internal/token"
)

// Walk, Dump and the Pos/End pair are the three things every tool outside the parser
// asks of a tree: `mzs --ast` prints it, the compile step of §6.3 walks it, and every
// diagnostic of §17 is a position taken from it. They are tested against trees the
// **parser** produced rather than hand-built ones, because a hand-built tree can only
// prove what the test author already believed the parser emits.
//
// The package under test is imported from outside (`package ast_test`) so the test may
// import the parser: parser depends on ast, and the test binary is a third package, so
// there is no cycle.

// everyNode is one program that makes the parser emit every node kind of §6.1. When a
// node is added to the AST this program is where it has to appear, and the census below
// says so out loud rather than silently covering one node less.
const everyNode = `
include json
include cart from "./cart.mzs"

export fn total(xs, rate = 0.2) {
  sum = 0
  for x in xs {
    next if x < 0
    sum += x
  }
  while sum > 100 { sum -= 10; break sum }
  return sum * (1 + rate)
}

async fn refresh(url, *rest) { url }

$greeting = "привет, ${name.upper}!"
prices = [100, 200.5, nil, true]
order = [name: "гель", qty: 3]
first, second = prices
[a, b] = [1, 2]
n = -prices.len
big = n > 2 && n < 10 || !false
label = big ? "many" : "few"
span = 1..<10
slice = prices[0]
window = prices[0, 2]
double = { (x) -> x * 2 }
mapped = prices.map { it ?? 0 }
value = (sum = 1; sum + 1)
safe = try json.parse("{") else (e) -> e["kind"]
missing = order?.name
kind = match label {
  "many" -> /^m/
  in span -> 1
  x if x.len > 3 -> 2
  else -> nil
}
for key, qty in order { say(key) }
total(mapped, rate: 0.1)
order.get("qty", default: 0)
`

// parseEveryNode is the shared fixture: the program above, parsed once per test.
func parseEveryNode(t *testing.T) *ast.Program {
	t.Helper()
	prog, err := parser.Parse("every.mzs", everyNode)
	if err != nil {
		t.Fatalf("Parse(everyNode) error = %v; want nil", err)
	}
	return prog
}

// nodeNames is the census: the type name of every node Walk reached, without the
// package qualifier.
func nodeNames(root ast.Node) map[string]int {
	seen := map[string]int{}
	ast.Walk(root, func(n ast.Node) bool {
		name := fmt.Sprintf("%T", n)
		seen[name[strings.LastIndex(name, ".")+1:]]++
		return true
	})
	return seen
}

func TestWalkVisitsEveryNodeKind(t *testing.T) {
	seen := nodeNames(parseEveryNode(t))

	// §6.1's list. A node missing here means either the program above stopped
	// producing it or Walk stopped descending into it — both are bugs, and both are
	// invisible without this table.
	want := []string{
		"Program", "ExprStmt", "ReturnStmt", "BreakStmt", "NextStmt",
		"IncludeDecl", "ExportDecl", "FnDecl", "BlockStmt",
		"IfExpr", "WhileExpr", "ForExpr", "MatchExpr", "TryExpr",
		"NilLit", "BoolLit", "IntLit", "FloatLit", "StrLit", "RegexLit",
		"ArrayLit", "DictLit", "Ident", "GlobalVar",
		"UnaryExpr", "BinaryExpr", "LogicalExpr", "TernaryExpr", "RangeExpr",
		"AssignExpr", "ArrayPattern", "DestructureAssign",
		"IndexExpr", "CallExpr", "MethodCall", "FuncLit", "GroupExpr",
	}
	for _, name := range want {
		if seen[name] == 0 {
			t.Errorf("Walk never reached a %s; §6.1 lists it", name)
		}
	}
}

// A walk that answers false is a walk that stops there. This is what the compile step
// relies on when it refuses to descend into a subtree it has already rewritten.
func TestWalkPrunesOnFalse(t *testing.T) {
	prog := parseEveryNode(t)

	full := 0
	ast.Walk(prog, func(ast.Node) bool { full++; return true })

	pruned := 0
	ast.Walk(prog, func(n ast.Node) bool {
		pruned++
		_, isFn := n.(*ast.FnDecl)
		return !isFn
	})

	if pruned >= full {
		t.Errorf("pruned walk visited %d nodes, full walk %d; false must skip the children", pruned, full)
	}
	// The FnDecl itself is still visited — pruning is about its children.
	if nodeNames(prog)["FnDecl"] == 0 {
		t.Fatal("the fixture lost its fn declaration")
	}
}

// A nil node is not a visit, and neither is a typed nil hiding in an interface — the
// shape every optional field (`else`, a bare `return`) has.
func TestWalkSkipsNilNodes(t *testing.T) {
	calls := 0
	count := func(ast.Node) bool { calls++; return true }

	ast.Walk(nil, count)
	ast.Walk((*ast.IfExpr)(nil), count)
	ast.Walk((*ast.UnaryExpr)(nil), count)
	ast.Walk((*ast.IncludeDecl)(nil), count)
	if calls != 0 {
		t.Errorf("Walk called f %d times for nil nodes; want 0", calls)
	}

	// A real node with nil optional fields walks without reaching for them.
	prog, err := parser.Parse("t", "fn f() { return }\nif true { 1 }")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	ast.Walk(prog, func(ast.Node) bool { return true })
}

// Every node reports a position range that a diagnostic can print: inside the file,
// and never ending before it starts (§17).
func TestNodePositionsAreSane(t *testing.T) {
	prog := parseEveryNode(t)
	lines := strings.Count(everyNode, "\n") + 1

	ast.Walk(prog, func(n ast.Node) bool {
		p, e := n.Pos(), n.End()
		if p.Line < 1 || p.Line > lines {
			t.Errorf("%T.Pos() = %s, outside the 1..%d lines of the file",
				n, ast.PosString(p), lines)
		}
		if e.Line < p.Line || (e.Line == p.Line && e.Col < p.Col) {
			t.Errorf("%T ends at %s before it starts at %s",
				n, ast.PosString(e), ast.PosString(p))
		}
		return true
	})
}

func TestPosString(t *testing.T) {
	if got := ast.PosString(token.Pos{Line: 12, Col: 4}); got != "12:4" {
		t.Errorf("PosString = %q; want %q", got, "12:4")
	}
}

// A node whose optional child is missing still has to answer Pos and End, because a
// diagnostic asks before it knows how the node was built. The parser cannot make these
// shapes today — `..5` and `1..` are syntax errors — but the accessors are written to
// survive them, and an unguarded one would be a nil dereference inside the error path,
// the worst possible place for a panic (A7).
func TestPositionsWithMissingChildren(t *testing.T) {
	op := token.Pos{Line: 3, Col: 7}

	bare := &ast.RangeExpr{OpPos: op}
	if got := bare.Pos(); got != op {
		t.Errorf("RangeExpr with no bounds: Pos() = %s; want the operator at %s",
			ast.PosString(got), ast.PosString(op))
	}
	if got := bare.End(); got != op {
		t.Errorf("RangeExpr with no bounds: End() = %s; want the operator at %s",
			ast.PosString(got), ast.PosString(op))
	}

	// A method call always has a receiver in a parsed tree; when a rewrite drops one,
	// the name is what is left to point at.
	headless := &ast.MethodCall{Name: "len", NamePos: op}
	if got := headless.Pos(); got != op {
		t.Errorf("MethodCall with no receiver: Pos() = %s; want the name at %s",
			ast.PosString(got), ast.PosString(op))
	}
}

// StrPart tells text from interpolation, which is what §8.12 folds on.
func TestStrPartIsText(t *testing.T) {
	lit := firstStrLit(t, `"привет, ${name}"`)
	text, interp := 0, 0
	for _, p := range lit.Parts {
		if p.IsText() {
			text++
			continue
		}
		interp++
	}
	if text == 0 || interp != 1 {
		t.Errorf("parts of a one-interpolation string: %d text, %d interpolated; want some text and exactly one interpolation",
			text, interp)
	}
}

// The arm kinds of §5.3 print by name — the dump of a `match` is unreadable otherwise.
func TestArmKindString(t *testing.T) {
	tests := []struct {
		kind ast.ArmKind
		want string
	}{
		{ast.ArmValue, "value"}, // equality, and a regex pattern too (§5.3)
		{ast.ArmIn, "in"},
		{ast.ArmGuard, "guard"},
		{ast.ArmArray, "array"},
		{ast.ArmElse, "else"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("ArmKind(%d).String() = %q; want %q", tt.kind, got, tt.want)
		}
	}
}

// Dump is what `mzs --ast` prints and what the parser's golden tables compare, so its
// shape is a contract: one node per line, two spaces per level, no trailing blanks.
func TestDumpShape(t *testing.T) {
	prog := parseEveryNode(t)
	dump := ast.Dump(prog)

	if !strings.HasPrefix(dump, `Program "every.mzs"`) {
		t.Errorf("dump starts with %q; want the Program line with the file name",
			strings.SplitN(dump, "\n", 2)[0])
	}
	for i, line := range strings.Split(strings.TrimRight(dump, "\n"), "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%2 != 0 {
			t.Errorf("line %d is indented by %d spaces; want a multiple of two", i+1, indent)
		}
	}

	// The lines that carry more than a node name: a dump is only useful because the
	// operator, the target, the arity and the modifiers are printed with it.
	for _, want := range []string{
		`Include json`, `Include cart from "./cart.mzs"`, `Export total`,
		`FnDecl total (xs, rate=…)`, `FnDecl async refresh (url, *rest)`,
		`Match`, `Try e`, `Ternary`, `Destructure =`, `Pattern`,
		`Range ..<`, `Global $greeting`, `Regex /^m/`,
		`Closure (x)`, `Closure implicit (it)`, `MethodCall .map`,
		`MethodCall ?.name`, `kwargs`, `guard`, `For key, qty`,
		`Binary +`, `Logical ??`, `Unary -`, `Assign +=`, `Group`,
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump has no %q line:\n%s", want, dump)
		}
	}
}

// Program.String is what mzs.Program.String() forwards to, and Fprint is the io.Writer
// spelling of the same text — `mzs --ast` reaches the tree through them.
func TestDumpSpellings(t *testing.T) {
	prog := parseEveryNode(t)
	want := ast.Dump(prog)

	if got := prog.String(); got != want {
		t.Errorf("Program.String() and Dump disagree")
	}
	var buf bytes.Buffer
	if err := ast.Fprint(&buf, prog); err != nil {
		t.Fatalf("Fprint error = %v", err)
	}
	if buf.String() != want {
		t.Errorf("Fprint and Dump disagree")
	}
	if got := ast.Dump(nil); got != "" {
		t.Errorf("Dump(nil) = %q; want the empty string", got)
	}
}

// The resolution kinds the compile step stamps on an Ident (§6.3 step 2) print by name,
// because that is how they appear in a dump anyone reads.
func TestRefKindString(t *testing.T) {
	tests := []struct {
		ref  ast.RefKind
		want string
	}{
		{ast.RefNone, "none"},
		{ast.RefLocal, "local"},
		{ast.RefGlobal, "global"},
		{ast.RefFunc, "func"},
		{ast.RefBuiltin, "builtin"},
		{ast.RefModule, "module"},
		{ast.RefKind(99), "none"},
	}
	for _, tt := range tests {
		if got := tt.ref.String(); got != tt.want {
			t.Errorf("RefKind(%d).String() = %q; want %q", tt.ref, got, tt.want)
		}
	}
}

// IsConst and ConstText are how the compile step folds a string that has nothing to
// interpolate (§8.12): a one-part literal is its own text, and a literal split into
// several parts still concatenates to it.
func TestStrLitConstText(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		const_ bool
		text   string
	}{
		{"one part", `"привет"`, true, "привет"},
		{"escapes stay one constant", `"строка\nи ещё"`, true, "строка\nи ещё"},
		{"an interpolation is not constant", `"привет, ${name}"`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lit := firstStrLit(t, tt.src)
			if got := lit.IsConst(); got != tt.const_ {
				t.Errorf("IsConst() = %v; want %v", got, tt.const_)
			}
			if !tt.const_ {
				// ConstText is only meaningful for a constant, but it must still
				// answer without reaching past the parts it has.
				lit.ConstText()
				return
			}
			if got := lit.ConstText(); got != tt.text {
				t.Errorf("ConstText() = %q; want %q", got, tt.text)
			}
		})
	}
}

func firstStrLit(t *testing.T, src string) *ast.StrLit {
	t.Helper()
	prog, err := parser.Parse("t", src)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", src, err)
	}
	var lit *ast.StrLit
	ast.Walk(prog, func(n ast.Node) bool {
		if s, ok := n.(*ast.StrLit); ok && lit == nil {
			lit = s
		}
		return true
	})
	if lit == nil {
		t.Fatalf("no StrLit in %q", src)
	}
	return lit
}
