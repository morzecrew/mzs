// Package parser implements the grammar of SPEC §4 with the precedence of §5.1, the
// `match` forms of §5.2–§5.5, the static restrictions of §4.5 and the ambiguity
// diagnostics of §5.6. It is a precedence-climbing parser over the lexer's token stream,
// recovers at statement boundaries, and reports at most MaxErrors syntax errors per
// compile, joined with errors.Join (§17).
//
// One counter carries the whole of the `{` question. `[` is **always** an array (D3), and
// a `{` is read by position (D2): the body after a header, the trailing closure after a
// call, and in operand position a dict or a closure by the §3.12 lookahead. The single
// restriction of §3.11 is that inside the header of `if`, `while`, `for` or `match` a `{`
// at bracket depth zero ends the header and opens the body, which is what header counts.
// Descending into `(`, `[`, an argument list, an interpolation or a body re-opens
// ordinary expression position, which is what push/pop do. Nothing needs backtracking:
// decision, no `blockPos`, no `=>` mode, and no block concept at all — a trailing closure
// is appended to the call's Args (§4.2).
//
// Array versus dict is the bounded lookahead of §3.12: five rules, decided before any
// node is built, so no parse action is ever undone. Closure parameters are decided the
// same way (§4.1): after `{`, a `(` is a parameter list only when the token after the
// matching `)` is `->`.
//
// The §5.6 fix-its are first-class here, not an afterthought. Each one is recognised at
// the exact point where the offending shape becomes visible, reported once at the column
// the message names, and followed by a recovery that keeps the rest of the statement
// parseable, so a paste of Ruby yields one precise diagnostic instead of a cascade. Four
// of the rows are lexical (`=~`, `=!`, `&.`, `{ |x| … }`) and arrive from the lexer.
package parser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mzs/internal/ast"
	"mzs/internal/lexer"
	"mzs/internal/token"
)

// MaxErrors bounds how many syntax errors one Parse reports (§17).
const MaxErrors = 10

// maxDepth bounds recursive descent so that `((((((…` from a fuzz corpus is a
// diagnostic rather than a stack overflow (A7).
const maxDepth = 400

// Error is one syntax diagnostic. Msg is the bare message ("unexpected '!' after '=';
// did you mean '!='?"); the caller renders it as `<file>:<line>:<col>: syntax: <msg>`.
type Error struct {
	File string
	Msg  string
	Pos  token.Pos
}

func (e *Error) Error() string { return e.Msg }

// errAbort unwinds the recursive descent once the error budget is spent. It never
// escapes ParseTokens.
var errAbort = errors.New("mzs/parser: too many errors")

type parser struct {
	file  string
	toks  []token.Token
	pos   int
	errs  []*Error
	depth int

	// header counts the control-flow headers we are inside at bracket depth zero
	// (§3.11). While it is non-zero a `{` closes the header instead of opening a
	// closure.
	header int

	// arm counts the `match` arm patterns we are inside at bracket depth zero (§5.3).
	// While it is non-zero a `->` closes the pattern, so `(1) -> { … }` is an arm and
	// not the arrow function of §4.1 — the same kind of rule as header's, over the
	// other token that can end a construct.
	arm int
}

// state is what push saves: both counters answer "what does this token close", and
// both are off inside a bracket, an argument list, an interpolation or a body.
type state struct{ header, arm int }

// push saves the positional counters and re-opens ordinary expression position; every
// descent into a bracket, an argument list, an interpolation or a body does it.
func (p *parser) push() state {
	saved := state{header: p.header, arm: p.arm}
	p.header, p.arm = 0, 0
	return saved
}

func (p *parser) pop(saved state) { p.header, p.arm = saved.header, saved.arm }

// Parse lexes and parses src into a program. name is the file name used in
// diagnostics. On failure it returns a nil program and an error that is either a
// *Error or an errors.Join of several *Error values.
func Parse(name, src string) (*ast.Program, error) {
	toks, lerrs := lexer.Lex(name, src)
	if len(lerrs) > 0 {
		// A broken token stream makes every downstream message noise, so the lexical
		// diagnostics — four of the §5.6 rows among them — are reported alone.
		errs := make([]*Error, 0, MaxErrors)
		for i, e := range lerrs {
			if i >= MaxErrors {
				break
			}
			errs = append(errs, &Error{File: name, Msg: e.Msg, Pos: caretOf(e)})
		}
		return nil, joinErrors(errs)
	}
	return ParseTokens(name, toks)
}

// ParseTokens parses an already-lexed stream. The CLI's --tokens path and the golden
// tests share the lexer output this way instead of lexing twice.
func ParseTokens(name string, toks []token.Token) (prog *ast.Program, err error) {
	if len(toks) == 0 || toks[len(toks)-1].Kind != token.EOF {
		toks = append(append(make([]token.Token, 0, len(toks)+1), toks...), token.Token{Kind: token.EOF})
	}
	p := &parser{file: name, toks: toks}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if r != errAbort {
			p.errs = append(p.errs, &Error{
				File: name,
				Msg:  fmt.Sprintf("internal parser error: %v", r),
				Pos:  p.cur().Pos,
			})
		}
		if len(p.errs) == 0 {
			p.errs = append(p.errs, &Error{File: name, Msg: "unparsable input", Pos: p.cur().Pos})
		}
		prog, err = nil, joinErrors(p.errs)
	}()

	prog = p.parseProgram()
	if len(p.errs) > 0 {
		return nil, joinErrors(p.errs)
	}
	return prog, nil
}

func joinErrors(errs []*Error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	list := make([]error, len(errs))
	for i, e := range errs {
		list[i] = e
	}
	return errors.Join(list...)
}

// diagBangAfterAssign is the `=!` fix-it, read out of the lexer's poison table rather
// than spelled twice.
var diagBangAfterAssign = poisonDiag("=!")

func poisonDiag(text string) string {
	for _, o := range token.Poison {
		if o.Text == text {
			return o.Diag
		}
	}
	return ""
}

// caretOf places a lexical diagnostic. The lexer reports a poison lexeme at its first
// rune; for `=!` SPEC §16.3 pins the message at `one.mzs:3:6`, the column of the `!`,
// which is also the character the message names. Every other lexical position is used
// exactly as the lexer produced it.
func caretOf(e *lexer.Error) token.Pos {
	if e.Msg == diagBangAfterAssign && diagBangAfterAssign != "" {
		p := e.Pos
		p.Offset++
		p.Col++
		return p
	}
	return e.Pos
}

// ---------------------------------------------------------------------------
// Token cursor
// ---------------------------------------------------------------------------

func (p *parser) cur() token.Token { return p.toks[p.pos] }

func (p *parser) kind() token.Kind { return p.toks[p.pos].Kind }

func (p *parser) peek(n int) token.Token {
	if i := p.pos + n; i < len(p.toks) {
		return p.toks[i]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) peekKind(n int) token.Kind { return p.peek(n).Kind }

// kindAt is the kind of the token at an absolute index, for the bounded lookaheads that
// scan from an index of their own. Past the end it is EOF, so a scan that runs off the
// stream declines instead of panicking.
func (p *parser) kindAt(i int) token.Kind {
	if i < 0 || i >= len(p.toks) {
		return token.EOF
	}
	return p.toks[i].Kind
}

// advance consumes the current token and returns it. It never moves past the EOF
// sentinel, so every caller can read p.cur() unconditionally.
func (p *parser) advance() token.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(k token.Kind) bool {
	if p.kind() == k {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(k token.Kind, ctx string) token.Token {
	if p.kind() == k {
		return p.advance()
	}
	p.errorAt(p.cur().Pos, "expected '%s' in %s, found %s", k.String(), ctx, describe(p.cur()))
	return p.cur()
}

func (p *parser) skipNewlines() {
	for p.kind() == token.NEWLINE {
		p.advance()
	}
}

func (p *parser) skipSeps() {
	for p.kind() == token.NEWLINE || p.kind() == token.SEMI {
		p.advance()
	}
}

func (p *parser) errorAt(pos token.Pos, format string, a ...any) {
	if n := len(p.errs); n > 0 && p.errs[n-1].Pos == pos {
		return // one diagnostic per position; the rest is cascade
	}
	p.errs = append(p.errs, &Error{File: p.file, Msg: fmt.Sprintf(format, a...), Pos: pos})
	if len(p.errs) >= MaxErrors {
		panic(errAbort)
	}
}

// describe names a token the way a message should: quoted source text, or a category
// for the value-carrying kinds.
func describe(t token.Token) string {
	switch t.Kind {
	case token.EOF:
		return "end of input"
	case token.NEWLINE:
		return "end of line"
	case token.IDENT, token.GVAR:
		return "'" + t.Value + "'"
	case token.INT, token.FLOAT:
		return t.Value
	case token.STR_BEGIN, token.STR_TEXT, token.STR_END, token.INTERP_BEGIN, token.INTERP_END:
		return "a string"
	case token.REGEX:
		return "a regex"
	}
	return "'" + t.Kind.String() + "'"
}

func contains(ks []token.Kind, k token.Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// adjacent reports whether b starts exactly where a ends, which is how the parser tells
// the two-rune spellings of §5.6 (`=>`, `::`, `...`) from two separate operators.
func adjacent(a, b token.Token) bool { return a.End.Offset == b.Pos.Offset }

// startsExpr reports whether a token of kind k can begin an expression. It decides
// whether `return` carries a value, whether a `?` opens a ternary, and whether a Ruby
// word is being used as a keyword.
func startsExpr(k token.Kind) bool {
	switch k {
	case token.INT, token.FLOAT, token.STR_BEGIN, token.REGEX, token.IDENT, token.GVAR,
		token.LPAREN, token.LBRACKET, token.LBRACE,
		token.BANG, token.MINUS, token.PLUS,
		token.KW_TRUE, token.KW_FALSE, token.KW_NIL,
		token.KW_FN, token.KW_IF, token.KW_WHILE, token.KW_FOR, token.KW_MATCH, token.KW_TRY:
		return true
	}
	return false
}

// skipBalanced returns the index just past the close token that matches the open token
// at index i. It is the bounded lookahead of §3.12 rule 4 and §4.1; it inspects tokens
// only and never builds or undoes a node.
func (p *parser) skipBalanced(i int, open, close token.Kind) int {
	depth := 0
	for ; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		case token.EOF:
			return i
		}
	}
	return len(p.toks) - 1
}

// skipString returns the index just past the STR_END that closes the string literal
// beginning at index i. Strings nest through interpolation, so the depth counter is not
// optional.
func (p *parser) skipString(i int) int {
	return p.skipBalanced(i, token.STR_BEGIN, token.STR_END)
}

// ---------------------------------------------------------------------------
// Program and statements
// ---------------------------------------------------------------------------

func (p *parser) parseProgram() *ast.Program {
	start := p.cur().Pos
	stmts := p.parseStmtList()
	return &ast.Program{File: p.file, Stmts: stmts, Start: start, Stop: p.cur().End}
}

// parseStmtList reads `StmtList` up to EOF or any of stops, which it does not consume.
func (p *parser) parseStmtList(stops ...token.Kind) []ast.Stmt {
	var out []ast.Stmt
	for {
		p.skipSeps()
		if p.kind() == token.EOF || contains(stops, p.kind()) {
			return out
		}
		before := p.pos
		if s := p.parseStmt(); s != nil {
			out = append(out, s)
		}
		if p.pos == before {
			// Defensive: every statement consumes at least one token, but a malformed
			// stream must not spin here.
			p.advance()
		}
		switch p.kind() {
		case token.SEMI, token.NEWLINE, token.EOF:
		default:
			if contains(stops, p.kind()) {
				continue
			}
			// A Ruby word left dangling after a statement (`a and b`, `a rescue b`) is
			// one of the §5.6 rows, and its fix-it is more useful than "unexpected".
			if msg, ok := p.rubyWordHere(); ok {
				p.errorAt(p.cur().Pos, "%s", msg)
			} else {
				p.errorAt(p.cur().Pos, "unexpected %s after statement", describe(p.cur()))
			}
			p.sync(stops)
		}
	}
}

// sync drops tokens until a statement boundary, so one bad line costs one diagnostic.
func (p *parser) sync(stops []token.Kind) {
	for {
		switch k := p.kind(); {
		case k == token.EOF, k == token.SEMI, k == token.NEWLINE, contains(stops, k):
			return
		}
		p.advance()
	}
}

func (p *parser) parseStmt() ast.Stmt {
	s := p.parseBareStmt()
	// §4.4: only `if` and `while` are modifiers, they apply left to right, and each
	// wraps everything to its left.
	for {
		switch p.kind() {
		case token.KW_IF:
			kw := p.advance().Pos
			cond := p.parseExpr()
			s = &ast.IfExpr{Cond: cond, Then: blockOf(s), Kw: kw, Stop: cond.End()}
		case token.KW_WHILE:
			kw := p.advance().Pos
			cond := p.parseExpr()
			s = &ast.WhileExpr{Cond: cond, Body: blockOf(s), Kw: kw, Stop: cond.End()}
		default:
			return s
		}
	}
}

func (p *parser) parseBareStmt() ast.Stmt {
	switch p.kind() {
	case token.KW_INCLUDE:
		return p.parseInclude()
	case token.KW_EXPORT:
		return p.parseExport()
	case token.IDENT:
		if s := p.parseAsyncFn(); s != nil {
			return s
		}
		if p.destructureAhead() {
			return &ast.ExprStmt{X: p.parseDestructure()}
		}
	case token.GVAR, token.LBRACKET:
		// `[a, b] = xs` needs no lookahead — parseAssign sees the pattern where the
		// target would be — but `[a, b], c = xs` does, because its comma is the one at
		// depth zero.
		if p.destructureAhead() {
			return &ast.ExprStmt{X: p.parseDestructure()}
		}
	case token.KW_FN:
		if p.peekKind(1) == token.IDENT {
			// A named `fn` is a declaration statement so the evaluator can hoist it
			// (§8.2) without digging through ExprStmts. The anonymous form is a value
			// and falls through to the expression statement below.
			return p.parseFn()
		}
	case token.KW_RETURN:
		t := p.advance()
		st := &ast.ReturnStmt{Kw: t.Pos, Stop: t.End}
		if p.stmtValueFollows() {
			st.X = p.parseExpr()
			st.Stop = st.X.End()
		}
		return st
	case token.KW_BREAK:
		t := p.advance()
		st := &ast.BreakStmt{Kw: t.Pos, Stop: t.End}
		if p.stmtValueFollows() {
			st.X = p.parseExpr()
			st.Stop = st.X.End()
		}
		return st
	case token.KW_NEXT:
		t := p.advance()
		st := &ast.NextStmt{Kw: t.Pos, Stop: t.End}
		if p.stmtValueFollows() {
			st.X = p.parseExpr()
			st.Stop = st.X.End()
		}
		return st
	}
	return &ast.ExprStmt{X: p.parseExpr()}
}

// parseAsyncFn reads `async fn name(…) { … }` (§8.14) and returns nil when the current
// identifier is not that shape, so the caller carries on with an ordinary expression.
//
// `async` is positional, not a keyword (§3.5): it means something only immediately
// before `fn`, exactly as `from` does only inside an `include`. The keyword table stays
// at sixteen entries and a variable may still be called `async`.
func (p *parser) parseAsyncFn() ast.Stmt {
	if p.cur().Value != "async" || p.peekKind(1) != token.KW_FN {
		return nil
	}
	kw := p.advance().Pos
	fd := p.parseFn()
	fd.Async, fd.Kw = true, kw
	if fd.Name == "" {
		// An anonymous `async fn` is a value, not a declaration: there is no name to
		// hoist and none to bind, so it reaches the statement list as an expression and
		// §17 warns about the value nobody receives.
		return &ast.ExprStmt{X: fd}
	}
	return fd
}

// parseInclude reads `include name` or `include name from "path"` (§12.8). The path is a
// plain string literal: it is resolved by the host's loader before anything runs, so it
// cannot be computed and cannot interpolate.
func (p *parser) parseInclude() ast.Stmt {
	kw := p.advance()
	st := &ast.IncludeDecl{Kw: kw.Pos, Stop: kw.End}

	if p.kind() != token.IDENT {
		p.errorAt(p.cur().Pos, "'include' expects a name: `include json` or `include lib from \"./lib.mzs\"`")
		return st
	}
	name := p.advance()
	st.Name, st.NamePos, st.Stop = name.Value, name.Pos, name.End

	// `from` is positional, not a keyword (§3.5): only right here does the identifier
	// mean anything, so a variable called `from` keeps working everywhere else.
	if p.kind() != token.IDENT || p.cur().Value != "from" {
		return st
	}
	p.advance()
	if p.kind() != token.STR_BEGIN {
		p.errorAt(p.cur().Pos, "'include %s from' expects a string path, found %s", st.Name, describe(p.cur()))
		return st
	}
	pos := p.cur().Pos
	lit, ok := p.parsePrimary().(*ast.StrLit)
	if !ok {
		return st
	}
	if !lit.IsConst() {
		p.errorAt(pos, "an include path cannot interpolate: the module is resolved before anything runs")
		return st
	}
	st.Path, st.PathPos, st.HasPath, st.Stop = lit.ConstText(), pos, true, lit.End()
	return st
}

// parseExport reads the three forms of §12.8: `export fn f(…) { … }`, `export x = …` and
// `export name`. The first two wrap a declaration that runs exactly as it would without
// the keyword; the third names a binding that already exists.
func (p *parser) parseExport() ast.Stmt {
	kw := p.advance()
	st := &ast.ExportDecl{Kw: kw.Pos, Stop: kw.End}

	switch {
	case p.kind() == token.KW_FN,
		p.kind() == token.IDENT && p.cur().Value == "async" && p.peekKind(1) == token.KW_FN:
		// `export async fn f(…)` exports the async function like any other (§8.14):
		// the importer calls it and gets a task, which is the whole difference.
		inner := p.parseBareStmt()
		fd, ok := inner.(*ast.FnDecl)
		if !ok || fd.Name == "" {
			// An anonymous `fn` has no name for the module table (§12.8), and the
			// declaration form is not the only way to export one.
			p.errorAt(kw.Pos, "'export' needs a name: write `export fn f(…) { … }` or `export f = fn(…) { … }`")
			return st
		}
		st.Names, st.Decl, st.Stop = []string{fd.Name}, fd, fd.End()
		return st

	case p.kind() == token.IDENT:
		name := p.cur()
		// `export x = 1` exports the binding the assignment creates; a bare
		// `export x` exports one that already exists.
		if p.peekKind(1) != token.ASSIGN && p.peekKind(1) != token.DECLARE {
			p.advance()
			st.Names, st.Stop = []string{name.Value}, name.End
			return st
		}
		inner := p.parseBareStmt()
		st.Names, st.Decl, st.Stop = []string{name.Value}, inner, inner.End()
		return st
	}

	p.errorAt(p.cur().Pos, "'export' expects `fn`, an assignment or a name, found %s", describe(p.cur()))
	return st
}

// stmtValueFollows reports whether `return`/`break`/`next` carries a value, as opposed
// to being followed by a terminator or by one of its own statement modifiers.
func (p *parser) stmtValueFollows() bool {
	switch p.kind() {
	case token.KW_IF, token.KW_WHILE:
		return false // a modifier, not a value (§4.4)
	}
	return startsExpr(p.kind())
}

func blockOf(s ast.Stmt) *ast.BlockStmt {
	return &ast.BlockStmt{Stmts: []ast.Stmt{s}, Start: s.Pos(), Stop: s.End()}
}

func exprBlock(e ast.Expr) *ast.BlockStmt {
	return &ast.BlockStmt{Stmts: []ast.Stmt{&ast.ExprStmt{X: e}}, Start: e.Pos(), Stop: e.End()}
}

// ---------------------------------------------------------------------------
// Expressions, loosest to tightest (§5.1)
// ---------------------------------------------------------------------------

func (p *parser) parseExpr() ast.Expr { return p.parseAssign() }

// parseAssign is level 13, right associative.
func (p *parser) parseAssign() ast.Expr {
	l := p.parseTernary()
	// §5.6: `k => v` lexes as ASSIGN followed by GT, and is the commonest paste from a
	// Ruby hash or lambda.
	if p.kind() == token.ASSIGN && p.peekKind(1) == token.GT && adjacent(p.cur(), p.peek(1)) {
		p.errorAt(p.cur().Pos,
			"'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure")
		p.advance()
		p.advance()
		p.parseAssign() // consume the right side so the statement ends here
		return l
	}
	if token.IsAssignOp(p.kind()) {
		op := p.advance()
		// `[` is always an array (D3), so the left side of `=` arrives here as an array
		// literal and becomes a pattern now — the one place the two spellings of §8.15
		// meet.
		if lit, ok := l.(*ast.ArrayLit); ok {
			return p.parseDestructureTail(p.targetPattern(lit), op)
		}
		if !assignable(l) {
			p.errorAt(op.Pos, "cannot assign to this expression")
		}
		return &ast.AssignExpr{Target: l, Op: op.Kind, Value: p.parseAssign(), OpPos: op.Pos}
	}
	return l
}

func assignable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.GlobalVar, *ast.IndexExpr:
		return true
	}
	return false
}

// destructureAhead reports whether the statement starting here is `a, b = …`: a target
// list whose comma stands at bracket depth zero in front of an assignment operator. It
// is the bounded lookahead of §3.12 applied to the left of `=` — decided over tokens
// alone, before any node is built, so no parse action is ever undone.
//
// The bare form is a statement, not an expression: a comma at depth zero means something
// else inside a call, a collection, a `for` header and a `match` arm, and every one of
// those is reached through a bracket this scan stops at.
func (p *parser) destructureAhead() bool {
	depth, comma := 0, false
	for i := p.pos; i < len(p.toks); i++ {
		switch k := p.toks[i].Kind; k {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if depth == 0 {
				return false
			}
			depth--
		case token.STR_BEGIN:
			i = p.skipString(i) - 1
		case token.COMMA:
			if depth == 0 {
				comma = true
			}
		case token.NEWLINE, token.SEMI, token.EOF, token.ARROW:
			if depth == 0 {
				return false
			}
		default:
			if depth == 0 && token.IsAssignOp(k) {
				return comma
			}
		}
	}
	return false
}

// parseDestructure reads the bare `a, b = xs` form, which destructureAhead has already
// confirmed. Targets are read below the assignment level, so the operator that ends the
// list is still current when the loop stops.
func (p *parser) parseDestructure() ast.Expr {
	pat := &ast.ArrayPattern{Start: p.cur().Pos}
	for {
		el := p.targetElem(p.parseTernary())
		pat.Elems = append(pat.Elems, el)
		pat.Stop = el.End()
		if !p.accept(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	if !token.IsAssignOp(p.kind()) {
		t := p.cur()
		p.errorAt(t.Pos, "expected '=' after a destructuring target list, found %s", describe(t))
		return &ast.NilLit{Start: t.Pos, Stop: t.End}
	}
	return p.parseDestructureTail(pat, p.advance())
}

// parseDestructureTail reads the right side once the pattern and the operator are in
// hand. Only `=` and `:=` destructure: `a, b += xs` has no reading that is not a
// surprise, so it is a diagnostic rather than a definition.
func (p *parser) parseDestructureTail(pat *ast.ArrayPattern, op token.Token) ast.Expr {
	kind := op.Kind
	if kind != token.ASSIGN && kind != token.DECLARE {
		p.errorAt(op.Pos, "destructuring assigns with '=' or ':=', not '%s'", kind.String())
		kind = token.ASSIGN
	}
	return &ast.DestructureAssign{Pattern: pat, Op: kind, Value: p.parseAssign(), OpPos: op.Pos}
}

// targetPattern converts an array literal on the left of `=` into a pattern. Every
// element has to be something a value can be written to (§8.15).
func (p *parser) targetPattern(lit *ast.ArrayLit) *ast.ArrayPattern {
	pat := &ast.ArrayPattern{Brackets: true, Start: lit.Lbrack, Stop: lit.Rbrack}
	for _, el := range lit.Elems {
		pat.Elems = append(pat.Elems, p.targetElem(el))
	}
	return pat
}

func (p *parser) targetElem(x ast.Expr) ast.Expr {
	switch t := x.(type) {
	case *ast.Ident, *ast.GlobalVar, *ast.IndexExpr:
		return x
	case *ast.ArrayLit:
		return p.targetPattern(t)
	}
	p.errorAt(x.Pos(), "cannot assign to this expression: a destructuring target is a name, a $var, an index or a nested [ … ]")
	return x
}

// matchPattern converts an array literal in a `match` arm into a pattern: a bare name
// binds that position, a nested `[ … ]` recurses, and anything else stays an expression
// the evaluator compares against (§5.3).
func (p *parser) matchPattern(lit *ast.ArrayLit) *ast.ArrayPattern {
	pat := &ast.ArrayPattern{Brackets: true, Start: lit.Lbrack, Stop: lit.Rbrack}
	for _, el := range lit.Elems {
		if nested, ok := el.(*ast.ArrayLit); ok {
			pat.Elems = append(pat.Elems, p.matchPattern(nested))
			continue
		}
		pat.Elems = append(pat.Elems, el)
	}
	return pat
}

// parseTernary is level 12: `? :` and `try … else …`, both right associative.
func (p *parser) parseTernary() ast.Expr {
	if p.kind() == token.KW_TRY {
		return p.parseTry()
	}
	cond := p.parseBinary(token.PrecNilNil)
	if p.kind() != token.QUESTION {
		return cond
	}
	q := p.advance()
	// §3.4: there is no `?` suffix on an identifier. A `?` that cannot open a ternary
	// is the Ruby predicate spelling, and gets its own fix-it rather than "expected ':'".
	if !startsExpr(p.kind()) {
		p.reportPredicateQuestion(q)
		return cond
	}
	then := p.parseExpr()
	if p.kind() != token.COLON {
		if p.reportPredicateQuestion(q) {
			return cond
		}
		p.expect(token.COLON, "ternary")
		return cond
	}
	p.advance()
	return &ast.TernaryExpr{Cond: cond, Then: then, Else: p.parseTernary()}
}

// reportPredicateQuestion emits the §5.6 fix-it for `x.empty?`. The name it suggests is
// the identifier the `?` was written against; with no such name in front there is
// nothing to suggest and the caller falls back to the generic message.
func (p *parser) reportPredicateQuestion(q token.Token) bool {
	name := ""
	if i := p.indexOf(q); i > 0 && p.toks[i-1].Kind == token.IDENT {
		name = p.toks[i-1].Value
	}
	if name == "" {
		p.errorAt(q.Pos, "'?' is not part of an identifier")
		return true
	}
	p.errorAt(q.Pos, "'?' is not part of an identifier; did you mean '%s'?", name)
	return true
}

// indexOf locates a token the parser has already consumed, so a diagnostic can look at
// what preceded it. Tokens are unique by position, so the offset is the key.
func (p *parser) indexOf(t token.Token) int {
	for i := p.pos; i >= 0; i-- {
		if p.toks[i].Pos == t.Pos && p.toks[i].Kind == t.Kind {
			return i
		}
	}
	return -1
}

// parseTry reads `try X else Y` and `try X else (e) -> Y` (§4, §8.11).
func (p *parser) parseTry() ast.Expr {
	kw := p.advance().Pos
	x := p.parseExpr()
	p.expect(token.KW_ELSE, "try")
	name := ""
	if p.kind() == token.LPAREN && p.peekKind(1) == token.IDENT &&
		p.peekKind(2) == token.RPAREN && p.peekKind(3) == token.ARROW {
		name = p.peek(1).Value
		p.advance()
		p.advance()
		p.advance()
		p.advance()
	}
	return &ast.TryExpr{X: x, Var: name, Fallback: p.parseExpr(), Kw: kw}
}

// parseBinary is the precedence-climbing core of §5.1 levels 4..11 plus the ranges of
// level 6. Levels 1, 2, 3, 12, 13 and 14 are structural and live in the callers.
func (p *parser) parseBinary(min int) ast.Expr {
	l := p.parseUnary()
	for {
		k := p.kind()
		prec := token.Precedence(k)
		if prec == token.PrecNone || prec < min {
			return l
		}
		if k == token.DOTDOT || k == token.DOTLT {
			l = p.parseRangeTail(l)
			continue
		}
		op := p.advance()
		next := prec + 1
		if token.IsRightAssoc(k) {
			next = prec
		}
		r := p.parseBinary(next)
		switch k {
		case token.ANDAND, token.OROR, token.NILNIL:
			l = &ast.LogicalExpr{Op: k, L: l, R: r, OpPos: op.Pos}
		default:
			if k == token.EQ || k == token.NEQ {
				p.checkRegexEquality(k, op.Pos, l, r)
			}
			l = &ast.BinaryExpr{Op: k, L: l, R: r, OpPos: op.Pos}
		}
	}
}

// parseRangeTail reads the right operand of `..`/`..<` (level 6, non-associative) and
// applies the two range diagnostics: the Ruby `...` spelling (§5.6) and the trailer
// restriction of §4.5.
func (p *parser) parseRangeTail(lo ast.Expr) ast.Expr {
	op := p.advance()
	exclusive := op.Kind == token.DOTLT
	if op.Kind == token.DOTDOT && p.kind() == token.DOT && adjacent(op, p.cur()) {
		p.errorAt(op.Pos, "'...' is not an mzs operator; use '..<'")
		p.advance()
		exclusive = true
	}
	hi := p.parseBinary(token.PrecRange + 1)
	rng := &ast.RangeExpr{Lo: lo, Hi: hi, Exclusive: exclusive, OpPos: op.Pos}
	p.checkRangeTrailer(rng)
	if token.IsNonAssoc(p.kind()) {
		p.errorAt(p.cur().Pos, "range operator is non-associative")
		p.advance()
		p.parseBinary(token.PrecRange + 1) // swallow the third operand; no cascade
	}
	return rng
}

// parseUnary is level 3, right associative, and enforces §4.5 rule 1.
func (p *parser) parseUnary() ast.Expr {
	switch p.kind() {
	case token.BANG, token.MINUS, token.PLUS:
		op := p.advance()
		x := p.parseUnary()
		if op.Kind != token.BANG {
			if b, ok := x.(*ast.BinaryExpr); ok && b.Op == token.POW {
				p.errorAt(op.Pos, "ambiguous: write -(2 ** 2) or (-2) ** 2")
			}
		}
		return &ast.UnaryExpr{Op: op.Kind, X: x, OpPos: op.Pos}
	}
	return p.parsePow()
}

// parsePow is level 2: `**` binds tighter than a unary operator and associates to the
// right. The right operand is a UnaryExpr rather than the grammar's PowExpr, because
// §8.3 gives `2 ** -1` a meaning (a negative exponent promotes to Float) and there is no
// ambiguity in accepting it — `-2 ** 2` on the *left* is the shape §4.5 rejects.
func (p *parser) parsePow() ast.Expr {
	base := p.parsePostfix()
	if p.kind() != token.POW {
		return base
	}
	op := p.advance()
	return &ast.BinaryExpr{Op: token.POW, L: base, R: p.parseUnary(), OpPos: op.Pos}
}

// ---------------------------------------------------------------------------
// Postfix trailers (§5.1 level 1)
// ---------------------------------------------------------------------------

func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch p.kind() {
		case token.DOT, token.SAFEDOT:
			safe := p.kind() == token.SAFEDOT
			p.advance()
			x = p.parseMethodTail(x, safe)
		case token.LPAREN:
			call := &ast.CallExpr{Fn: x, Lparen: p.cur().Pos}
			call.Args, call.KwArgs, call.Stop = p.parseCallArgs()
			x = call
		case token.LBRACKET:
			x = p.parseIndexTail(x)
		case token.LBRACE:
			// §4.2: a trailing closure is the last argument of the nearest preceding
			// call. Only a call-shaped expression takes one, so `if c { … }` is never
			// re-read as a call on the `if`, and §3.11 keeps a header's `{` out of here.
			if p.header != 0 || !callShaped(x) {
				return x
			}
			x = p.attachClosure(x)
		case token.COLON:
			// §5.6: `a::B`. Two adjacent colons are never anything else — a ternary's
			// `:` is consumed by parseTernary and a dict's by the collection parser.
			if p.peekKind(1) != token.COLON || !adjacent(p.cur(), p.peek(1)) {
				return x
			}
			p.errorAt(p.cur().Pos, "'::' is not an mzs operator; use '.'")
			p.advance()
			p.advance()
			x = p.parseMethodTail(x, false)
		default:
			return x
		}
	}
}

// callShaped reports whether a trailing closure may attach to x. A bare name is
// call-shaped because `xs.each { … }` and `each(xs) { … }` are the same thing (D18).
func callShaped(x ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.CallExpr, *ast.MethodCall, *ast.IndexExpr:
		return true
	}
	return false
}

// attachClosure appends `{ … }` to the argument list of x, wrapping a bare name in a
// call first. There is no Block field anywhere in the AST (§6.1).
func (p *parser) attachClosure(x ast.Expr) ast.Expr {
	fl := p.parseFuncLit()
	switch e := x.(type) {
	case *ast.CallExpr:
		e.Args = append(e.Args, fl)
		e.Stop = fl.End()
		return e
	case *ast.MethodCall:
		e.Args = append(e.Args, fl)
		e.Stop = fl.End()
		return e
	}
	return &ast.CallExpr{Fn: x, Args: []ast.Expr{fl}, Lparen: x.Pos(), Stop: fl.End()}
}

func (p *parser) parseMethodTail(recv ast.Expr, safe bool) ast.Expr {
	nameTok := p.cur()
	name := p.methodName()
	// The `?` of `x.empty?` owns the diagnostic for that spelling, so the rename table
	// stays quiet there and one message comes out instead of two.
	if p.kind() != token.QUESTION {
		p.checkRenamedMethod(name, nameTok.Pos)
	}
	mc := &ast.MethodCall{Recv: recv, Name: name, Safe: safe, NamePos: nameTok.Pos, Stop: nameTok.End}
	if p.kind() == token.LPAREN {
		mc.Args, mc.KwArgs, mc.Stop = p.parseCallArgs()
	}
	return mc
}

func (p *parser) parseIndexTail(x ast.Expr) ast.Expr {
	lb := p.advance().Pos
	saved := p.push()
	p.skipNewlines()
	idx := p.parseExpr()
	var idx2 ast.Expr
	p.skipNewlines()
	if p.accept(token.COMMA) {
		p.skipNewlines()
		idx2 = p.parseExpr()
		p.skipNewlines()
	}
	p.pop(saved)
	rb := p.expect(token.RBRACKET, "index")
	return &ast.IndexExpr{X: x, Index: idx, Index2: idx2, Lbrack: lb, Rbrack: rb.End}
}

// methodName accepts identifiers and keywords after a '.' (§3.5, §4 MethodName).
func (p *parser) methodName() string {
	t := p.cur()
	switch {
	case t.Kind == token.IDENT:
		p.advance()
		return t.Value
	case token.IsKeywordKind(t.Kind):
		p.advance()
		return t.Kind.String()
	}
	p.errorAt(t.Pos, "expected a method name, found %s", describe(t))
	return ""
}

// parseCallArgs reads `( ArgList )`, collecting keyword arguments into the single
// trailing dict of §8.7.
func (p *parser) parseCallArgs() (args []ast.Expr, kwargs *ast.DictLit, stop token.Pos) {
	p.expect(token.LPAREN, "argument list")
	saved := p.push()
	for {
		p.skipNewlines()
		if p.kind() == token.RPAREN || p.kind() == token.EOF {
			break
		}
		if p.kind() == token.IDENT && p.peekKind(1) == token.COLON {
			key := p.advance()
			p.advance() // ':'
			p.skipNewlines()
			val := p.parseExpr()
			if kwargs == nil {
				kwargs = &ast.DictLit{Lbrack: key.Pos}
			}
			kwargs.Keys = append(kwargs.Keys, strLit(key.Value, key.Pos, key.End))
			kwargs.Vals = append(kwargs.Vals, val)
			kwargs.Rbrack = val.End()
		} else {
			args = append(args, p.parseExpr())
		}
		p.skipNewlines()
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.skipNewlines()
	p.pop(saved)
	rp := p.expect(token.RPAREN, "argument list")
	return args, kwargs, rp.End
}

func strLit(text string, start, stop token.Pos) *ast.StrLit {
	return &ast.StrLit{Parts: []ast.StrPart{{Text: text}}, Start: start, Stop: stop}
}

// ---------------------------------------------------------------------------
// Primaries
// ---------------------------------------------------------------------------

func (p *parser) parsePrimary() ast.Expr {
	p.depth++
	if p.depth > maxDepth {
		p.errorAt(p.cur().Pos, "expression nesting is too deep")
		panic(errAbort)
	}
	defer func() { p.depth-- }()

	t := p.cur()
	switch t.Kind {
	case token.INT:
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			p.errorAt(t.Pos, "invalid integer literal %q", t.Value)
		}
		return &ast.IntLit{Value: v, Start: t.Pos, Stop: t.End}
	case token.FLOAT:
		p.advance()
		v, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			p.errorAt(t.Pos, "invalid float literal %q", t.Value)
		}
		return &ast.FloatLit{Value: v, Start: t.Pos, Stop: t.End}
	case token.KW_TRUE, token.KW_FALSE:
		p.advance()
		return &ast.BoolLit{Value: t.Kind == token.KW_TRUE, Start: t.Pos, Stop: t.End}
	case token.KW_NIL:
		p.advance()
		return &ast.NilLit{Start: t.Pos, Stop: t.End}
	case token.STR_BEGIN:
		return p.parseStrLit()
	case token.REGEX:
		p.advance()
		return &ast.RegexLit{Pattern: t.Value, Flags: t.Flags, Start: t.Pos, Stop: t.End}
	case token.IDENT:
		if t.Value == "async" && p.header == 0 && p.arm == 0 && p.arrowFollows(p.pos+1) {
			// §8.14 puts `async` in front of a `fn` and nowhere else; the arrow form has
			// no keyword to put it in front of, so it has one spelling and this names it.
			p.errorAt(t.Pos, "an async function is written `async fn(a, b) { … }`")
			p.advance()
			return p.parseArrowFn()
		}
		if t.Value == "async" && p.peekKind(1) == token.KW_FN {
			// §8.14 in expression position: `f = async fn(…) { … }`. `async` is
			// positional rather than a keyword (§3.5), and an identifier in front of
			// `fn` can be nothing else, so this needs no lookahead past the two tokens.
			kw := p.advance().Pos
			fd := p.parseFn()
			fd.Async, fd.Kw = true, kw
			return fd
		}
		if e, ok := p.parseRubyWord(); ok {
			return e
		}
		p.advance()
		return &ast.Ident{Name: t.Value, Slot: -1, Start: t.Pos, Stop: t.End}
	case token.GVAR:
		p.advance()
		return &ast.GlobalVar{Name: t.Value, Start: t.Pos, Stop: t.End}
	case token.LPAREN:
		// §4.1: `(params) -> { body }` is the anonymous `fn` without the keyword. The
		// scan is the closure's own parameter-list lookahead with the brace added, and
		// it is off in the two positions where the tokens it reads are already spoken
		// for: a header, where the `{` opens the body (§3.11), and a `match` arm's
		// pattern, where the `->` opens the arm (§5.3).
		if p.header == 0 && p.arm == 0 && p.arrowFollows(p.pos) {
			return p.parseArrowFn()
		}
		return p.parseGroup()
	case token.LBRACKET:
		return p.parseCollection()
	case token.LBRACE:
		// §3.12: the dict lookahead runs before §3.11, because a body can never begin
		// with `name :` — so `if x == {a: 1} { … }` reads the dict and still leaves the
		// body to the caller. Only a non-dict `{` in a header ends the header.
		if d, ok := p.braceDict(); ok {
			return d
		}
		if p.header == 0 {
			return p.parseFuncLit()
		}
	case token.KW_FN:
		return p.parseFn()
	case token.KW_IF:
		return p.parseIf()
	case token.KW_WHILE:
		return p.parseWhile()
	case token.KW_FOR:
		return p.parseFor()
	case token.KW_MATCH:
		return p.parseMatch()
	case token.KW_TRY:
		return p.parseTry()
	case token.COLON:
		// §5.6: `:name` is a Ruby symbol. mzs has none; the string is the fix.
		if p.peekKind(1) == token.IDENT && adjacent(p.cur(), p.peek(1)) {
			name := p.peek(1)
			p.errorAt(t.Pos, "mzs has no symbols; write %q", name.Value)
			p.advance()
			p.advance()
			return strLit(name.Value, t.Pos, name.End)
		}
	case token.PERCENT:
		// §5.6: `%w[a b]` is a Ruby word array. It lexes as PERCENT IDENT LBRACKET.
		if p.peekKind(1) == token.IDENT && p.peekKind(2) == token.LBRACKET &&
			adjacent(p.cur(), p.peek(1)) && adjacent(p.peek(1), p.peek(2)) {
			letter := p.peek(1).Value
			p.errorAt(t.Pos, "'%%%s' is not mzs; write [\"a\", \"b\"]", letter)
			p.advance()
			p.advance()
			end := p.skipBalanced(p.pos, token.LBRACKET, token.RBRACKET)
			stop := p.toks[end-1].End
			p.pos = end
			return &ast.ArrayLit{Lbrack: t.Pos, Rbrack: stop}
		}
	}
	p.errorAt(t.Pos, "unexpected %s", describe(t))
	p.advance()
	return &ast.NilLit{Start: t.Pos, Stop: t.End}
}

// parseStrLit assembles the flattened string token stream of §3.7 into one StrLit.
func (p *parser) parseStrLit() ast.Expr {
	begin := p.advance()
	lit := &ast.StrLit{Start: begin.Pos, Stop: begin.End}
	for {
		switch p.kind() {
		case token.STR_TEXT:
			t := p.advance()
			p.checkHashInterp(t)
			lit.Parts = append(lit.Parts, ast.StrPart{Text: t.Value})
		case token.STR_GVAR:
			t := p.advance()
			lit.Parts = append(lit.Parts, ast.StrPart{
				Expr: &ast.GlobalVar{Name: t.Value, Start: t.Pos, Stop: t.End},
			})
		case token.INTERP_BEGIN:
			p.advance()
			saved := p.push()
			p.skipNewlines()
			e := p.parseExpr()
			p.skipNewlines()
			p.pop(saved)
			p.expect(token.INTERP_END, "string interpolation")
			lit.Parts = append(lit.Parts, ast.StrPart{Expr: e})
		case token.STR_END:
			t := p.advance()
			lit.Stop = t.End
			if len(lit.Parts) == 0 {
				lit.Parts = []ast.StrPart{{}}
			}
			return lit
		default:
			p.errorAt(p.cur().Pos, "unterminated string literal")
			if len(lit.Parts) == 0 {
				lit.Parts = []ast.StrPart{{}}
			}
			return lit
		}
	}
}

// parseGroup reads `( StmtList )` (§4 GroupExpr); its value is the last statement.
func (p *parser) parseGroup() ast.Expr {
	lp := p.advance().Pos
	saved := p.push()
	stmts := p.parseStmtList(token.RPAREN)
	p.pop(saved)
	rp := p.expect(token.RPAREN, "parenthesised expression")
	return &ast.GroupExpr{Stmts: stmts, Lparen: lp, Rparen: rp.End}
}

// ---------------------------------------------------------------------------
// Collections (§3.12)
// ---------------------------------------------------------------------------

// parseCollection reads `[ … ]`. `[` is always an array (D3). The two dict shapes it
// used to carry are still recognised by the §3.12 lookahead, but only so that each can
// name its brace replacement instead of failing as a mangled array.
func (p *parser) parseCollection() ast.Expr {
	lbIdx := p.pos
	lb := p.advance().Pos
	saved := p.push()
	defer p.pop(saved)

	p.skipNewlines()
	if p.kind() == token.RBRACKET {
		rb := p.advance()
		return &ast.ArrayLit{Lbrack: lb, Rbrack: rb.End}
	}
	if p.kind() == token.COLON && p.peekKind(1) == token.RBRACKET {
		return p.bracketDict(lbIdx, lb, "the empty dict is written {}")
	}
	if p.dictFollows() {
		return p.bracketDict(lbIdx, lb, "a dict is written {a: 1}")
	}
	return p.parseArrayBody(lb) // rule 5
}

// bracketDict reports a dict written in brackets — the earlier draft's spelling — and names the
// brace form that replaces it. The literal is skipped whole from its '[' at lbIdx, so
// nothing inside it can cascade, and a DictLit stands in for the rest of the parse.
func (p *parser) bracketDict(lbIdx int, lb token.Pos, msg string) ast.Expr {
	p.errorAt(lb, "%s", msg)
	end := p.skipBalanced(lbIdx, token.LBRACKET, token.RBRACKET)
	stop := p.toks[end-1].End
	p.pos = end
	return &ast.DictLit{Lbrack: lb, Rbrack: stop}
}

// dictFollows is §3.12 rules 2 to 4, read from the token after the '['.
func (p *parser) dictFollows() bool { return p.dictFollowsAt(p.pos) }

// dictFollowsAt is dictFollows read from an arbitrary index, so the same four shapes
// decide a `{ … }` in operand position (§3.11) without duplicating the rule.
//
// `->` ends a key wherever `:` does, which is what gives a dict a key that is not a
// string (§3.12, §7.6) — with one exception, and it is the whole of why the exception
// exists: a `)` in front of a `->` is a parameter list (§4.1), so `{(x) -> x * 2}` stays
// the closure it has always been and a computed key keeps its one spelling, `(k): v`.
func (p *parser) dictFollowsAt(i int) bool {
	switch p.kindAt(i) {
	case token.IDENT:
		return endsDictKey(p.kindAt(i + 1))
	case token.STR_BEGIN:
		return endsDictKey(p.kindAt(p.skipString(i)))
	case token.LPAREN:
		return p.kindAt(p.skipBalanced(i, token.LPAREN, token.RPAREN)) == token.COLON
	}
	return p.literalKeyAt(i)
}

// endsDictKey reports whether k separates a dict key from its value.
func endsDictKey(k token.Kind) bool { return k == token.COLON || k == token.ARROW }

// literalKeyAt is §3.12 rule 4: a literal — `1`, `-2.5`, `true`, `nil`, a regex — in
// front of a separator is a dict key, because nothing else in mzs puts one there. It
// reads three tokens at most, decides before anything is consumed, and accepts exactly
// what parseLiteralKey consumes; a `:` is admitted so that the JSON habit `{1: "a"}`
// reaches the fix-it of §5.6 instead of failing as a closure body.
func (p *parser) literalKeyAt(i int) bool {
	if k := p.kindAt(i); k == token.MINUS || k == token.PLUS {
		i++
		if k := p.kindAt(i); k != token.INT && k != token.FLOAT {
			return false
		}
	} else if !token.IsLiteralKind(k) {
		return false
	}
	return endsDictKey(p.kindAt(i + 1))
}

func (p *parser) parseArrayBody(lb token.Pos) ast.Expr {
	var elems []ast.Expr
	for {
		p.skipNewlines()
		if p.kind() == token.RBRACKET || p.kind() == token.EOF {
			break
		}
		elems = append(elems, p.parseExpr())
		p.skipNewlines()
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.skipNewlines()
	rb := p.expect(token.RBRACKET, "array literal")
	return &ast.ArrayLit{Elems: elems, Lbrack: lb, Rbrack: rb.End}
}

// keyForm is the separator one key form takes. A bare word and a string take both, so
// `{a: 1}` and `{a -> 1}` are the same dict; a computed key takes `:` alone, because `)`
// in front of `->` is a parameter list (§4.1); a literal key takes `->` alone, which is
// what keeps one spelling per thing.
type keyForm uint8

const (
	keyEither keyForm = iota
	keyColon
	keyArrow
)

// parseDictBody reads `DictEntry { "," DictEntry } [ "," ]` up to the closing `}`
// (§3.12). A bare-identifier key becomes a string literal, so a dict is
// JSON-serialisable with no symbol type; a literal key written `1 -> v` is how a dict
// gets one of the other key types of §7.6.
func (p *parser) parseDictBody(lb token.Pos) ast.Expr {
	const close = token.RBRACE
	d := &ast.DictLit{Lbrack: lb}
	for {
		p.skipNewlines()
		if p.kind() == close || p.kind() == token.EOF {
			break
		}
		var key ast.Expr
		form := keyEither
		switch {
		case p.kind() == token.IDENT && endsDictKey(p.peekKind(1)):
			t := p.advance()
			key = strLit(t.Value, t.Pos, t.End)
		case p.kind() == token.STR_BEGIN:
			key = p.parseStrLit()
		case p.kind() == token.LPAREN:
			p.advance()
			inner := p.push()
			key = p.parseExpr()
			p.pop(inner)
			p.expect(token.RPAREN, "computed dict key")
			form = keyColon
		case p.literalKeyAt(p.pos):
			key = p.parseLiteralKey()
			form = keyArrow
		default:
			p.errorAt(p.cur().Pos, "expected a dict key, found %s", describe(p.cur()))
			before := p.pos
			p.sync([]token.Kind{close})
			p.skipNewlines()
			if p.pos == before {
				// sync stops *at* a boundary without consuming it, and the loop above
				// only breaks on `]` or EOF — so a `;` inside a collection would spin
				// here forever, the more so because §17 reports one diagnostic per
				// position and the error budget would never trip. One bad key costs at
				// least one token.
				p.advance()
			}
			continue
		}
		p.dictSeparator(form)
		p.skipNewlines()
		d.Keys = append(d.Keys, key)
		d.Vals = append(d.Vals, p.parseExpr())
		p.skipNewlines()
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.skipNewlines()
	rb := p.expect(close, "dict literal")
	d.Rbrack = rb.End
	return d
}

// parseLiteralKey consumes the shape literalKeyAt promised: a literal primary, or a sign
// and a number. It reads no trailer and no operator, so the separator is the next token
// however the entry continues.
func (p *parser) parseLiteralKey() ast.Expr {
	if k := p.kind(); k == token.MINUS || k == token.PLUS {
		op := p.advance()
		return &ast.UnaryExpr{Op: op.Kind, X: p.parsePrimary(), OpPos: op.Pos}
	}
	return p.parsePrimary()
}

// dictSeparator consumes the `:` or `->` between a key and its value and reports the two
// spellings §3.12 does not have: `:` after a literal key, which is the JSON habit, and
// `->` after a computed one, which is the closure's parameter list. Each names its
// replacement and each keeps going, so one wrong separator costs one diagnostic and the
// entry still reaches the dict.
func (p *parser) dictSeparator(form keyForm) {
	switch k := p.kind(); {
	case k == token.COLON && form != keyArrow, k == token.ARROW && form != keyColon:
		p.advance()
	case k == token.COLON:
		p.errorAt(p.cur().Pos, "a dict key that is not a string takes '->', not ':'")
		p.advance()
	case k == token.ARROW:
		p.errorAt(p.cur().Pos, "a computed dict key takes ':', not '->': write (k): v")
		p.advance()
	default:
		p.errorAt(p.cur().Pos, "expected ':' or '->' in dict entry, found %s", describe(p.cur()))
	}
}

// ---------------------------------------------------------------------------
// Closures, bodies and functions (§4.1)
// ---------------------------------------------------------------------------

// parseFuncLit reads `{ … }` in expression position: the closure form of a function
// value, the other being an anonymous `fn` (§4.1). With no `(params) ->` list the closure
// implicitly declares `it` (§8.9).
func (p *parser) parseFuncLit() ast.Expr {
	lb := p.advance()
	// Reached from operand position only after braceDict declined, so a dict shape here
	// is the trailing-closure slot of §4.2 — where a dict argument has its own spelling.
	if e, ok := p.braceDictHere(lb, "a dict after a call is written (a: 1) or ({a: 1})"); ok {
		return e
	}
	saved := p.push()
	params, implicit := p.closureParams(lb)
	stmts := p.parseStmtList(token.RBRACE)
	p.pop(saved)
	rb := p.expect(token.RBRACE, "closure")
	return &ast.FuncLit{
		Params:     params,
		Body:       &ast.BlockStmt{Stmts: stmts, Start: lb.End, Stop: rb.Pos},
		ImplicitIt: implicit,
		Start:      lb.Pos,
		Stop:       rb.End,
	}
}

// parseBody reads `{ … }` in body position — the body of `if`, `while`, `for` or a
// `match` arm, and the body of a function. §6.2: in body position a `{ … }` is a
// BlockStmt, evaluated immediately in its own scope, so it declares no parameters.
func (p *parser) parseBody(what string) *ast.BlockStmt {
	if p.kind() != token.LBRACE {
		if msg, ok := p.rubyWordHere(); ok {
			// `if c do … end`: report the fix-it once and skip the Ruby body.
			p.errorAt(p.cur().Pos, "%s", msg)
			return p.recoverDoEnd()
		}
		pos := p.cur().Pos
		p.errorAt(pos, "expected '{' in %s, found %s", what, describe(p.cur()))
		return &ast.BlockStmt{Start: pos, Stop: pos}
	}
	lb := p.advance()
	if _, ok := p.braceDictHere(lb, "this '{' opens the %s; write { {a: 1} } for a dict", what); ok {
		return &ast.BlockStmt{Start: lb.Pos, Stop: p.cur().Pos}
	}
	saved := p.push()
	// A `(…) -> {` in body position is an arrow function opening the body's first
	// statement (§4.1), not a parameter list the body may not have — the brace is what
	// tells them apart, so `{ (x) -> x }` still reaches the diagnostic below.
	if !p.arrowFnFollows(p.pos) {
		if params, implicit := p.closureParams(lb); !implicit && len(params) > 0 {
			p.errorAt(params[0].NamePos, "the body of %s cannot declare parameters", what)
		}
	}
	stmts := p.parseStmtList(token.RBRACE)
	p.pop(saved)
	rb := p.expect(token.RBRACE, what)
	return &ast.BlockStmt{Stmts: stmts, Start: lb.Pos, Stop: rb.Pos}
}

// closureParams implements the bounded lookahead of §4.1: after `{`, a `(` opens a
// parameter list only when the token after the matching `)` is `->`; otherwise it is a
// GroupExpr that starts the body. No parse action is undone either way.
func (p *parser) closureParams(lb token.Token) (params []ast.Param, implicit bool) {
	if p.kind() == token.LPAREN && p.toks[p.skipBalanced(p.pos, token.LPAREN, token.RPAREN)].Kind == token.ARROW {
		params = p.parseParams()
		p.expect(token.ARROW, "closure parameters")
		return params, false
	}
	// §8.9: no parameter list means one implicit parameter named `it`.
	return []ast.Param{{Name: "it", NamePos: lb.Pos, Slot: -1}}, true
}

// braceDict reads `{ … }` in operand position when the §3.12 lookahead says dict: `{}`
// is the empty dict and `{a: 1}`, `{"a": 1}`, `{(k): 1}` carry entries. The scan starts
// one token past the `{` and decides over tokens alone — nothing is consumed unless it
// commits.
//
// A closure body can never begin with `name :`, which is what keeps the two readings
// apart, and it is why this may run inside a header where a `{` otherwise opens the
// body (§3.11): `if x == {a: 1} { … }` needs no parentheses.
// A line break right after `{` is never a token — §3.10 suppresses it, `LBRACE` being in
// the continuation set — so the scan reads the first real token at p.pos+1 and needs no
// newline handling of its own. That is what lets braceDictHere use the same three shapes
// from p.pos, and what makes `f {` + newline + `a: 1` reach the §5.6 fix-it.
func (p *parser) braceDict() (ast.Expr, bool) {
	if i := p.pos + 1; p.toks[i].Kind != token.RBRACE && !p.dictFollowsAt(i) {
		return nil, false
	}
	lb := p.advance().Pos
	saved := p.push()
	defer p.pop(saved)
	if p.kind() == token.RBRACE {
		rb := p.advance()
		return &ast.DictLit{Lbrack: lb, Rbrack: rb.End}, true
	}
	return p.parseDictBody(lb), true
}

// braceDictHere reports a `{a: 1}` written where §3.11 has already spoken for the brace
// — after a call, where `{` is the trailing closure, and in body position, where `{`
// opens the body. Operand position is the only place a brace dict is read (braceDict),
// so both of those stay decidable without the parser carrying any state. The braces are
// skipped whole so that nothing inside them can cascade.
func (p *parser) braceDictHere(lb token.Token, msg string, args ...any) (ast.Expr, bool) {
	if !p.dictFollowsAt(p.pos) {
		return nil, false
	}
	p.errorAt(lb.Pos, msg, args...)
	end := p.skipBalanced(p.pos-1, token.LBRACE, token.RBRACE)
	stop := p.toks[end-1].End
	p.pos = end
	return &ast.DictLit{Lbrack: lb.Pos, Rbrack: stop}, true
}

// parseParams reads a parenthesised parameter list (D14). Defaults are expressions and a
// `*rest` parameter may only be last (§4).
func (p *parser) parseParams() []ast.Param {
	p.expect(token.LPAREN, "parameter list")
	saved := p.push()
	var params []ast.Param
	for {
		p.skipNewlines()
		if p.kind() == token.RPAREN || p.kind() == token.EOF {
			break
		}
		rest := p.accept(token.STAR)
		t := p.cur()
		if t.Kind != token.IDENT {
			p.errorAt(t.Pos, "expected a parameter name, found %s", describe(t))
			break
		}
		p.advance()
		prm := ast.Param{Name: t.Value, Rest: rest, NamePos: t.Pos, Slot: -1}
		if p.accept(token.ASSIGN) {
			prm.Default = p.parseExpr()
		}
		if n := len(params); n > 0 && params[n-1].Rest {
			p.errorAt(params[n-1].NamePos, "a rest parameter must be last")
		}
		params = append(params, prm)
		p.skipNewlines()
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.skipNewlines()
	p.pop(saved)
	p.expect(token.RPAREN, "parameter list")
	return params
}

// arrowFollows reports whether the '(' at i is followed by its match and a `->` — the
// arrow form of §4.1, whatever comes after the arrow. It is the closure's own
// parameter-list lookahead (§4.1) read from an index instead of from the cursor.
func (p *parser) arrowFollows(i int) bool {
	if p.kindAt(i) != token.LPAREN {
		return false
	}
	return p.kindAt(p.skipBalanced(i, token.LPAREN, token.RPAREN)) == token.ARROW
}

// arrowFnFollows is arrowFollows plus the brace: the complete form, `(params) -> {`. Body
// position asks for this one, because a `(…) -> ` without a brace there is the parameter
// list a body may not have and keeps its own diagnostic.
func (p *parser) arrowFnFollows(i int) bool {
	return p.arrowFollows(i) && p.kindAt(p.skipBalanced(i, token.LPAREN, token.RPAREN)+1) == token.LBRACE
}

// parseArrowFn reads `(params) -> { body }`. It builds the node an anonymous `fn` builds,
// because it is that same function with the keyword left out: the parameters are outside
// the braces, so the braces are a body and not a value (§4.1).
//
// A body that is not braced is §5.6: the braceless arrow is the closure's spelling, and
// naming both replacements is more use than "unexpected '->'". The expression is read as
// the body anyway, so one mistake stays one diagnostic (§17).
func (p *parser) parseArrowFn() ast.Expr {
	start := p.cur().Pos
	params := p.parseParams()
	arrow := p.expect(token.ARROW, "arrow function")
	if p.kind() != token.LBRACE {
		p.errorAt(arrow.Pos, "an arrow function's body is braced: (x) -> { x * 2 }, or write the closure { (x) -> x * 2 }")
		x := p.parseExpr()
		return &ast.FnDecl{Params: params, Body: exprBlock(x), Kw: start, Stop: x.End()}
	}
	body := p.parseBody("function body")
	return &ast.FnDecl{Params: params, Body: body, Kw: start, Stop: body.Stop}
}

// parseFn reads `fn [name](params) { body }`. Both forms yield the FnDecl node, which is
// both a statement and an expression (§6.1). The anonymous form is that node with an
// empty Name: a value and nothing else, so it is neither hoisted nor bound, and it is a
// function rather than a closure in the two ways that matter (§7.7) — its arity is
// checked, and a `return` inside it returns from it.
func (p *parser) parseFn() *ast.FnDecl {
	kw := p.advance().Pos
	name := ""
	switch {
	case p.kind() == token.IDENT:
		name = p.advance().Value
	case p.kind() != token.LPAREN:
		p.errorAt(p.cur().Pos, "expected a function name or '(' after 'fn', found %s", describe(p.cur()))
	}
	var params []ast.Param
	if p.kind() == token.LPAREN {
		params = p.parseParams()
	} else {
		p.errorAt(p.cur().Pos, "expected '(' in parameter list, found %s", describe(p.cur()))
	}
	body := p.parseBody("function body")
	return &ast.FnDecl{Name: name, Params: params, Body: body, Kw: kw, Stop: body.Stop}
}

// ---------------------------------------------------------------------------
// Control flow (§4) — every construct is an expression
// ---------------------------------------------------------------------------

// parseHeaderExpr parses the condition of a control-flow construct with the header
// counter raised, so that a `{` at bracket depth zero ends the header (§3.11).
func (p *parser) parseHeaderExpr() ast.Expr {
	p.header++
	e := p.parseExpr()
	p.header--
	return e
}

func (p *parser) parseIf() *ast.IfExpr {
	kw := p.advance().Pos
	cond := p.parseHeaderExpr()
	node := &ast.IfExpr{Cond: cond, Kw: kw}
	node.Then = p.parseBody("if body")
	node.Stop = node.Then.Stop
	if p.kind() != token.KW_ELSE {
		return node
	}
	p.advance()
	if p.kind() == token.KW_IF { // §6.2: `else if` is a nested IfExpr in Else
		nested := p.parseIf()
		node.Else, node.Stop = nested, nested.End()
		return node
	}
	els := p.parseBody("else body")
	node.Else, node.Stop = els, els.Stop
	return node
}

func (p *parser) parseWhile() ast.Expr {
	kw := p.advance().Pos
	cond := p.parseHeaderExpr()
	body := p.parseBody("while body")
	return &ast.WhileExpr{Cond: cond, Body: body, Kw: kw, Stop: body.Stop}
}

func (p *parser) parseFor() ast.Expr {
	kw := p.advance().Pos
	node := &ast.ForExpr{Kw: kw}
	t := p.cur()
	if t.Kind != token.IDENT {
		p.errorAt(t.Pos, "expected a loop variable, found %s", describe(t))
	} else {
		p.advance()
		node.KeyVar = t.Value
	}
	if p.accept(token.COMMA) {
		v := p.cur()
		if v.Kind != token.IDENT {
			p.errorAt(v.Pos, "expected a second loop variable, found %s", describe(v))
		} else {
			p.advance()
			node.ValVar = v.Value
		}
	}
	p.expect(token.KW_IN, "for loop")
	node.Iter = p.parseHeaderExpr()
	node.Body = p.parseBody("for body")
	node.Stop = node.Body.Stop
	return node
}

// parseMatch reads §5.2–§5.5. The subject is optional (§5.4); arms are separated by a
// newline or `;`, which is what lets a whole `match` fit on one line.
func (p *parser) parseMatch() ast.Expr {
	kw := p.advance().Pos
	node := &ast.MatchExpr{Kw: kw}
	if p.kind() != token.LBRACE {
		node.Subject = p.parseHeaderExpr()
	}
	lb := p.expect(token.LBRACE, "match")
	saved := p.push()
	p.skipSeps()
	for p.kind() != token.RBRACE && p.kind() != token.EOF {
		before := p.pos
		node.Arms = append(node.Arms, p.parseMatchArm())
		if p.pos == before {
			p.advance()
		}
		switch p.kind() {
		case token.SEMI, token.NEWLINE:
			p.skipSeps()
		case token.RBRACE, token.EOF:
		case token.KW_ELSE:
			// §3.10 drops the newline in front of `else` so that a hanging `else`
			// works, which leaves the last arm of a multi-line `match` with no
			// separator in front of it. It needs none: `else` can only start an arm.
		default:
			p.errorAt(p.cur().Pos, "expected a newline or ';' between match arms, found %s", describe(p.cur()))
			p.sync([]token.Kind{token.RBRACE})
			p.skipSeps()
		}
	}
	p.pop(saved)
	rb := p.expect(token.RBRACE, "match")
	node.Stop = rb.End
	if lb.Kind == token.LBRACE && len(node.Arms) == 0 {
		p.errorAt(rb.Pos, "a match needs at least one arm")
	}
	for i := range node.Arms {
		if node.Arms[i].Kind == ast.ArmElse && i != len(node.Arms)-1 {
			p.errorAt(node.Arms[i].Pos, "'else' must be the last arm of a match")
		}
		// Without a subject every pattern is a condition (§5.4), and there is nothing
		// for an array pattern to take apart.
		if node.Arms[i].Kind == ast.ArmArray && node.Subject == nil {
			p.errorAt(node.Arms[i].Pos, "an array pattern needs a subject: match xs { [a, b] -> … }")
		}
	}
	return node
}

// parseMatchArm reads one arm (§5.3). Several patterns separated by `,` mean "or"; a
// trailing `if` is an additional "and"; `in` and a leading `if` set the arm's kind.
func (p *parser) parseMatchArm() ast.MatchArm {
	arm := ast.MatchArm{Pos: p.cur().Pos}
	if p.kind() == token.KW_ELSE {
		p.advance()
		arm.Kind = ast.ArmElse
		arm.Body = p.parseArmBody()
		return arm
	}
	for first := true; ; first = false {
		kind := ast.ArmValue
		switch p.kind() {
		case token.KW_IN:
			p.advance()
			kind = ast.ArmIn
		case token.KW_IF:
			p.advance()
			kind = ast.ArmGuard
		}
		if first {
			arm.Kind = kind
		} else if kind != arm.Kind {
			p.errorAt(p.cur().Pos, "every pattern in one arm must use the same form")
		}
		arm.Pats = append(arm.Pats, p.armExpr())
		if !p.accept(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.armArrayPattern(&arm)
	if p.accept(token.KW_IF) {
		arm.Guard = p.armExpr()
	}
	arm.Body = p.parseArmBody()
	return arm
}

// armExpr parses one pattern or guard of a `match` arm with the arm counter raised, so
// that a `->` at bracket depth zero ends it (§5.3). Without this `(1) -> { … }` — a
// parenthesised pattern with a block body — would read as the arrow function of §4.1 and
// swallow the arm.
func (p *parser) armExpr() ast.Expr {
	p.arm++
	e := p.parseExpr()
	p.arm--
	return e
}

// armArrayPattern turns `[x, y] ->` into a destructuring arm (§5.3, §8.15). This is the
// one meaning §20 kept free, and the one incompatibility of the change: an array literal
// in value position used to mean "equal to this array", and a bare name inside it now
// binds instead of being read. Element-wise comparison keeps `[1, 2] ->` meaning what it
// always did, so only patterns written with names change.
func (p *parser) armArrayPattern(arm *ast.MatchArm) {
	if arm.Kind != ast.ArmValue {
		return
	}
	found := -1
	for i, pat := range arm.Pats {
		if _, ok := pat.(*ast.ArrayLit); ok {
			found = i
			break
		}
	}
	if found < 0 {
		return
	}
	if len(arm.Pats) > 1 {
		// "Or" over patterns that bind different names in the same arm has no reading
		// the body could rely on, so it is refused rather than guessed at.
		p.errorAt(arm.Pats[found].Pos(), "an array pattern must be the only pattern in its arm")
		return
	}
	arm.Kind = ast.ArmArray
	arm.Pats[0] = p.matchPattern(arm.Pats[0].(*ast.ArrayLit))
}

// parseArmBody reads `-> ( Expr | Closure )`. A `{ … }` here is a body, evaluated
// immediately in its own scope (§6.2), not a function value.
func (p *parser) parseArmBody() *ast.BlockStmt {
	p.expect(token.ARROW, "match arm")
	if p.kind() == token.LBRACE {
		// `->` already accepts a bare expression, so the §3.12 lookahead runs here as it
		// does in operand position: `-> {ok: true}` is the dict, and only a brace the
		// lookahead declines opens a body.
		if d, ok := p.braceDict(); ok {
			return exprBlock(d)
		}
		return p.parseBody("match arm body")
	}
	return exprBlock(p.parseExpr())
}

// ---------------------------------------------------------------------------
// §4.5 static restrictions and §5.6 ambiguity diagnostics
// ---------------------------------------------------------------------------

// checkRegexEquality is §5.6 `s == /re/` (and D5). Comparing against a regex literal is
// never what the author meant; `~` is the match operator and always returns Bool.
func (p *parser) checkRegexEquality(op token.Kind, pos token.Pos, l, r ast.Expr) {
	if !isRegexLit(l) && !isRegexLit(r) {
		return
	}
	p.errorAt(pos, "'%s' with a regex operand: use '~' to match", op.String())
}

func isRegexLit(e ast.Expr) bool { _, ok := e.(*ast.RegexLit); return ok }

// checkRangeTrailer is §4.5 rule 2: `0..5.map { … }` formally means `0..(5.map { … })`,
// so a numeric literal carrying a trailer on the right of a range is rejected. The
// restriction is deliberately narrow — `0..xs.len` and `0..n.abs` mean what they say.
func (p *parser) checkRangeTrailer(rng *ast.RangeExpr) {
	x, trailers := rng.Hi, 0
	for {
		switch e := x.(type) {
		case *ast.MethodCall:
			x, trailers = e.Recv, trailers+1
			continue
		case *ast.CallExpr:
			x, trailers = e.Fn, trailers+1
			continue
		case *ast.IndexExpr:
			x, trailers = e.X, trailers+1
			continue
		}
		break
	}
	if trailers == 0 {
		return
	}
	switch x.(type) {
	case *ast.IntLit, *ast.FloatLit:
		p.errorAt(rng.OpPos, "ambiguous range: write (0..5).map")
	}
}

// checkHashInterp is §5.6 `"#{x}"`. §3.2 gives `#` no meaning inside a string, so the
// Ruby spelling arrives as ordinary text and would otherwise be silently wrong.
func (p *parser) checkHashInterp(t token.Token) {
	i := strings.Index(t.Value, "#{")
	if i < 0 {
		return
	}
	pos := t.Pos
	// The text is decoded, so this column is exact unless an escape precedes the `#`;
	// that is still the right line and the closest column the token stream can give.
	n := len([]rune(t.Value[:i]))
	pos.Offset += n
	pos.Col += n
	p.errorAt(pos, `string interpolation is "${x}"`)
}

// renames is the Ruby-to-mzs method table of SPEC §19.2, minus the spellings that are
// also mzs names. The codemod and the did-you-mean diagnostics of §5.6 are driven by the
// same table, so a name the codemod missed fails loudly at publish time instead of
// silently at run time. None of these exists in the standard library — D17 forbids
// aliases — so recognising them here costs nothing.
var renames = map[string]string{
	"downcase": "lower",
	"upcase":   "upper",
	"strip":    "trim",
	"lstrip":   "trim_start",
	"rstrip":   "trim_end",
	"include":  "has",
	"has_key":  "has",
	"cover":    "has",
	"length":   "len",
	"size":     "len",
	"gsub":     "replace",
	"sub":      "replace_first",
	"scan":     "matches",
	"select":   "filter",
	"collect":  "map",
	"detect":   "find",
	"inject":   "reduce",
}

// conversions are the `to_*` spellings of §5.6, which share one message because the
// answer depends on the type the author wanted.
var conversions = map[string]bool{
	"to_s": true, "to_i": true, "to_f": true,
	"to_a": true, "to_h": true, "to_json": true,
}

func (p *parser) checkRenamedMethod(name string, pos token.Pos) {
	if conversions[name] {
		p.errorAt(pos, "undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'")
		return
	}
	if to, ok := renames[name]; ok {
		p.errorAt(pos, "undefined method '%s'; did you mean '%s'?", name, to)
	}
}

// rubyWords are the words §3.5 leaves out of the keyword table on purpose. They lex as
// identifiers and the parser turns each into the one fix-it §5.6 promises.
var rubyWords = map[string]string{
	"and":    "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'",
	"or":     "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'",
	"not":    "'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'",
	"do":     "'do'/'end' are not mzs keywords; use braces: if c { … }",
	"end":    "'do'/'end' are not mzs keywords; use braces: if c { … }",
	"then":   "'then' is not an mzs keyword; use braces: if c { … }",
	"elsif":  "'elsif' is not an mzs keyword; use 'else if'",
	"unless": "'unless' is not an mzs keyword; use 'if !(c)'",
	"until":  "'until' is not an mzs keyword; use 'while !(c)'",
	"loop":   "'loop' is not an mzs keyword; use 'while true { … }'",
	"def":    "'def' is not an mzs keyword; use 'fn'",
	"rescue": "'rescue' is not an mzs keyword; use 'try a else b'",
	// The words other languages spell a module dependency with. mzs has exactly one
	// (§12.8), and someone who writes another is one rename away from working code.
	"import":  "'import' is not an mzs keyword; use 'include': include lib from \"./lib.mzs\"",
	"require": "'require' is not an mzs keyword; use 'include': include lib from \"./lib.mzs\"",
	"use":     "'use' is not an mzs keyword; use 'include': include lib from \"./lib.mzs\"",
}

// rubyWordHere reports the fix-it for the current token when it is a Ruby word used
// where mzs expects a keyword. A word used as an ordinary name (`loop = 3`) is left
// alone: only the keyword shape is diagnosed.
func (p *parser) rubyWordHere() (string, bool) {
	t := p.cur()
	if t.Kind != token.IDENT {
		return "", false
	}
	msg, ok := rubyWords[t.Value]
	if !ok {
		return "", false
	}
	switch t.Value {
	case "loop":
		ok = p.peekKind(1) == token.LBRACE
	case "not", "def", "unless", "until", "elsif":
		ok = startsExpr(p.peekKind(1))
	case "import", "require", "use":
		// Only the dependency shape: `use = 3` and `require("x")` are ordinary code.
		ok = p.peekKind(1) == token.IDENT || p.peekKind(1) == token.STR_BEGIN
	}
	return msg, ok
}

// parseRubyWord handles a Ruby keyword in expression position: it reports the §5.6
// fix-it and then parses the construct the author meant, so one paste of Ruby costs one
// diagnostic rather than a cascade.
func (p *parser) parseRubyWord() (ast.Expr, bool) {
	msg, ok := p.rubyWordHere()
	if !ok {
		return nil, false
	}
	t := p.cur()
	p.errorAt(t.Pos, "%s", msg)
	switch t.Value {
	case "not":
		p.advance()
		return &ast.UnaryExpr{Op: token.BANG, X: p.parseUnary(), OpPos: t.Pos}, true
	case "unless", "elsif":
		p.advance()
		cond := p.parseHeaderExpr()
		body := p.parseBody("if body")
		return &ast.IfExpr{Cond: cond, Then: body, Kw: t.Pos, Stop: body.Stop}, true
	case "until":
		p.advance()
		cond := p.parseHeaderExpr()
		body := p.parseBody("while body")
		return &ast.WhileExpr{Cond: cond, Body: body, Kw: t.Pos, Stop: body.Stop}, true
	case "loop":
		p.advance()
		body := p.parseBody("while body")
		return &ast.WhileExpr{
			Cond: &ast.BoolLit{Value: true, Start: t.Pos, Stop: t.End},
			Body: body, Kw: t.Pos, Stop: body.Stop,
		}, true
	case "def":
		p.advance()
		name := ""
		if p.kind() == token.IDENT {
			name = p.advance().Value
		}
		var params []ast.Param
		if p.kind() == token.LPAREN {
			params = p.parseParams()
		}
		body := p.parseBody("function body")
		return &ast.FnDecl{Name: name, Params: params, Body: body, Kw: t.Pos, Stop: body.Stop}, true
	}
	p.advance()
	return &ast.NilLit{Start: t.Pos, Stop: t.End}, true
}

// recoverDoEnd swallows a Ruby `do … end` body after its fix-it has been reported, so
// the tokens inside it produce no further diagnostics.
func (p *parser) recoverDoEnd() *ast.BlockStmt {
	start := p.cur().Pos
	for {
		t := p.cur()
		if t.Kind == token.EOF {
			return &ast.BlockStmt{Start: start, Stop: t.Pos}
		}
		p.advance()
		if t.Kind == token.IDENT && t.Value == "end" {
			return &ast.BlockStmt{Start: start, Stop: t.End}
		}
	}
}
