// Package printer is the whole of `mzs fmt`: one spelling of a program, chosen by the
// tool so that nobody has to choose it in review. It has no options, for the reason
// gofmt has none — a formatter with a knob is a formatter you argue about — and it
// changes no program: every run reparses its own output and compares the tree it gets
// with the tree it started from, so a bug here is a refusal rather than a rewrite.
//
// What it decides:
//
//   - four spaces of indentation per level, spaces around every binary operator, one
//     space after every comma and none before it;
//   - a collection, an argument list or a method chain that does not fit inside Width
//     breaks — the chain by its dots, everything else one element to a line;
//   - a comment stays with the line it was written on, or on its own line above the
//     statement it was written above, and a blank line between two statements survives
//     as exactly one blank line.
//
// What it leaves alone, on purpose:
//
//   - the text of a literal, which is copied from the source: `0xff` does not become
//     255, `1.50` does not become 1.5 and `'raw'` does not gain escapes;
//   - a statement whose text includes a heredoc (§3.7), which is moved sideways with the
//     code around it and otherwise reproduced as written — the body's lines are the
//     program's data, and re-flowing data is not formatting;
//   - parentheses, which are a node of their own in this grammar (§4) and therefore say
//     what the author meant to say.
package printer

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"mzs/internal/ast"
	"mzs/internal/lexer"
	"mzs/internal/parser"
	"mzs/internal/token"
)

// bom is the byte-order mark the lexer drops before it counts an offset, dropped here
// for the same reason: every position in the tree is an offset into the text without it.
const bom = '\ufeff'

const (
	// Indent is the one indentation, in spaces (§15, `mzs fmt`).
	Indent = 4
	// Width is the column a line is kept under whenever there is a way to break it.
	// Nothing is ever broken *to* it: a name, a literal or an operand chain with no
	// break point in it stays long rather than being folded into something unreadable.
	Width = 96
)

// Format returns the canonical spelling of src. name is used in diagnostics only.
//
// The result always ends in exactly one newline, and is empty for a source that holds
// nothing but whitespace. A source that does not lex or parse is returned as an error:
// there is no partial formatting, because half a tree is not a program.
func Format(name, src string) (string, error) {
	rs := []rune(src)
	if len(rs) > 0 && rs[0] == bom {
		rs = rs[1:]
	}
	prog, cmts, hds, err := parse(name, string(rs))
	if err != nil {
		return "", err
	}
	out := (&printer{rs: rs, cmts: cmts, hds: hds}).run(prog)

	// The two invariants worth paying for. A formatter that quietly changed a program
	// would be found out by a test suite one day and by a production incident the day
	// before, so both are checked on every run and both fail loudly.
	if err := sameTree(name, string(rs), out, prog); err != nil {
		return "", err
	}
	return out, nil
}

// parse lexes and parses, keeping what the parser is not given.
func parse(name, src string) (*ast.Program, []lexer.Comment, []lexer.Heredoc, error) {
	toks, cmts, hds, lerrs := lexer.Scan(name, src)
	if len(lerrs) > 0 {
		e := lerrs[0]
		return nil, nil, nil, fmt.Errorf("%s:%d:%d: syntax: %s", name, e.Pos.Line, e.Pos.Col, e.Msg)
	}
	prog, err := parser.ParseTokens(name, toks)
	if err != nil {
		return nil, nil, nil, err
	}
	return prog, cmts, hds, nil
}

// sameTree is the promise `mzs fmt` makes: the output is the input, differently spaced.
// It reparses and compares the dumps, and compares the comment text as well — a tree is
// blind to comments, and losing one is exactly the bug an AST comparison cannot see.
func sameTree(name, src, out string, before *ast.Program) error {
	after, cmts, _, err := parse(name, out)
	if err != nil {
		return fmt.Errorf("mzs fmt: the formatted text does not parse (this is a bug in mzs fmt): %w", err)
	}
	if a, b := ast.Dump(before), ast.Dump(after); a != b {
		return fmt.Errorf("mzs fmt: formatting %s would change the program (this is a bug in mzs fmt)", name)
	}
	_, wasCmts, _, _ := lexer.Scan(name, src)
	if len(wasCmts) != len(cmts) {
		return fmt.Errorf("mzs fmt: formatting %s would lose a comment (this is a bug in mzs fmt)", name)
	}
	for i := range cmts {
		if strings.TrimRight(wasCmts[i].Text, " \t") != strings.TrimRight(cmts[i].Text, " \t") {
			return fmt.Errorf("mzs fmt: formatting %s would change a comment (this is a bug in mzs fmt)", name)
		}
	}
	return nil
}

// printer holds the source and the two things the tree does not carry: where the
// comments were, and where the heredoc bodies are.
type printer struct {
	rs   []rune
	cmts []lexer.Comment
	ci   int
	hds  []lexer.Heredoc

	b   strings.Builder
	col int
	// last is the source line the previously written item ended on. It is what turns a
	// blank line in the input into a blank line in the output, and nothing else: a run
	// of them is one, and the formatter never invents one that was not written.
	last int
	// blocks are the spans of every `{ … }` written in the source. A heredoc inside one
	// belongs to the statements of that block and is reached again further down; a
	// heredoc outside every one of them belongs to the statement being printed now.
	blocks []span
}

type span struct{ from, to int }

func (p *printer) run(prog *ast.Program) string {
	p.collectBlocks(prog)
	p.stmtList(prog.Stmts, 0, prog.Stop)
	out := p.b.String()
	out = strings.TrimLeft(out, "\n")
	out = strings.TrimRight(out, " \t\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

// collectBlocks records every brace-delimited block of the program, which is how a
// heredoc is attributed to the innermost statement that holds it.
func (p *printer) collectBlocks(prog *ast.Program) {
	ast.Walk(prog, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStmt); ok && p.at(b.Start.Offset) == '{' {
			p.blocks = append(p.blocks, span{b.Start.Offset, b.Stop.Offset})
		}
		return true
	})
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

func (p *printer) w(s string) {
	p.b.WriteString(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		p.col = utf8.RuneCountInString(s[i+1:])
		return
	}
	p.col += utf8.RuneCountInString(s)
}

func (p *printer) nl() {
	p.b.WriteByte('\n')
	p.col = 0
}

func (p *printer) tab(ind int) { p.w(strings.Repeat(" ", ind*Indent)) }

// blank writes the one blank line that separates two things the author separated. It is
// called with the source line the next item starts on, before its indentation.
func (p *printer) blank(line int) {
	if p.last > 0 && line > p.last+1 {
		p.nl()
	}
}

// opened records that a bracket has just been opened on source line l. What it buys is
// the blank line that does *not* appear: a body whose first statement stands two lines
// below its `{` is a body with a blank line at the top, and nobody writes one on purpose.
func (p *printer) opened(l int) {
	if l > p.last {
		p.last = l
	}
}

func (p *printer) at(off int) rune {
	if off < 0 || off >= len(p.rs) {
		return 0
	}
	return p.rs[off]
}

func (p *printer) slice(from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > len(p.rs) {
		to = len(p.rs)
	}
	if from >= to {
		return ""
	}
	return string(p.rs[from:to])
}

func (p *printer) text(n ast.Node) string { return p.slice(n.Pos().Offset, n.End().Offset) }

// ---------------------------------------------------------------------------
// Comments
// ---------------------------------------------------------------------------

// leading writes every comment standing before off, each on a line of its own. A comment
// the formatter has no better place for arrives here too, which is what makes "no comment
// is ever dropped" a property of the printer and not of luck.
func (p *printer) leading(off, ind int) {
	for p.ci < len(p.cmts) && p.cmts[p.ci].Pos.Offset < off {
		c := p.cmts[p.ci]
		p.ci++
		// A comment standing further up the file than the line just written is one the
		// layout moved — out of a condition, out from between an operator and its
		// operand. Its distance from what follows it is a fact about the source it no
		// longer has, so the blank line that distance would buy is not written either.
		moved := c.Pos.Line < p.last
		p.blank(c.Pos.Line)
		p.tab(ind)
		p.w(strings.TrimRight(c.Text, " \t"))
		p.nl()
		if moved {
			p.last = 0
			continue
		}
		p.last = c.End.Line
	}
}

// trailing writes the comment that ends the line just written, if there is one. `# …`
// after code is a remark on that code and belongs where it was put.
func (p *printer) trailing(line int) {
	if p.ci >= len(p.cmts) {
		return
	}
	c := p.cmts[p.ci]
	if c.OwnLine || c.Pos.Line != line {
		return
	}
	p.ci++
	p.w("  " + strings.TrimRight(c.Text, " \t"))
	p.last = c.End.Line
}

// skip drops the comments inside [from, to) — a span the printer has just written out
// whole, where the text is already there and writing it twice is the other way to get
// this wrong.
//
// It stops at a comment standing *before* from rather than dropping it. Such a comment
// has not been written by anybody yet — it sits in a gap the layout has no slot for, an
// operator and its operand for one — and the cursor staying on it is what sends it to
// leading() or leftovers() instead of into the void.
func (p *printer) skip(from, to int) {
	for p.ci < len(p.cmts) {
		off := p.cmts[p.ci].Pos.Offset
		if off < from || off >= to {
			return
		}
		p.ci++
	}
}

// hasParens reports whether pos really points at a `(`. A call written without them —
// `f { … }`, the trailing closure of §4.2 — carries no position there, and reading rune
// zero of the file instead would make the answer depend on what the file starts with.
func (p *printer) hasParens(pos token.Pos) bool {
	return pos.IsValid() && p.at(pos.Offset) == '('
}

// bracketSpan is brackets() for a caller that has already decided the node has some.
func (p *printer) bracketSpan(e ast.Expr) span {
	from, to, ok := p.brackets(e)
	if !ok {
		return span{}
	}
	return span{from, to}
}

// brackets returns the span between a node's own brackets — the region whose broken form
// has a slot for every comment in it — and whether the node has any.
func (p *printer) brackets(e ast.Expr) (from, to int, ok bool) {
	switch x := e.(type) {
	case *ast.ArrayLit:
		return x.Lbrack.Offset, x.Rbrack.Offset, true
	case *ast.DictLit:
		return x.Lbrack.Offset, x.Rbrack.Offset, true
	case *ast.GroupExpr:
		return x.Lparen.Offset, x.Rparen.Offset, true
	case *ast.CallExpr:
		if !p.hasParens(x.Lparen) {
			return 0, 0, false
		}
		return x.Lparen.Offset, x.Stop.Offset, true
	case *ast.MethodCall:
		lp := p.parenAfter(x.NamePos.Offset + utf8.RuneCountInString(x.Name))
		if lp < 0 {
			return 0, 0, false
		}
		return lp, x.Stop.Offset, true
	}
	return 0, 0, false
}

// parenAfter is the offset of the `(` that follows off across blanks, or -1 when the next
// thing is something else — a method written without parentheses (§4.2).
func (p *printer) parenAfter(off int) int {
	for i := off; i < len(p.rs); i++ {
		switch p.rs[i] {
		case ' ', '\t':
		case '(':
			return i
		default:
			return -1
		}
	}
	return -1
}

// hasComment reports whether a comment stands inside [from, to).
func (p *printer) hasComment(from, to int) bool {
	for i := p.ci; i < len(p.cmts); i++ {
		off := p.cmts[i].Pos.Offset
		if off >= to {
			return false
		}
		if off >= from {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Heredocs
// ---------------------------------------------------------------------------

// heredocEnd returns the offset the text of a statement spanning [from, to) really ends
// at, which is past the terminator of every heredoc opened inside it, and -1 when the
// statement opens none of its own. A heredoc that lies inside a nested block belongs to
// a statement further down and is not one of these.
func (p *printer) heredocEnd(from, to int) int {
	end := -1
	for {
		grew := false
		for _, h := range p.hds {
			off := h.Pos.Offset
			if off < from || off >= max(to, end) || p.inBlock(off, from) {
				continue
			}
			if h.Term > end {
				end, grew = h.Term, true
			}
		}
		if !grew {
			return end
		}
	}
}

// inBlock reports whether off lies inside a braced block that itself begins after from —
// that is, inside one of the blocks the statement starting at from contains.
func (p *printer) inBlock(off, from int) bool {
	for _, b := range p.blocks {
		if b.from >= from && off > b.from && off < b.to {
			return true
		}
	}
	return false
}

// hasHeredoc reports whether a heredoc tag stands inside [from, to).
func (p *printer) hasHeredoc(from, to int) bool {
	for _, h := range p.hds {
		if off := h.Pos.Offset; off >= from && off < to {
			return true
		}
	}
	return false
}

// verbatim copies [from, to) with its first line's indentation replaced by ind and every
// other line shifted by the same amount. Shifting a heredoc's lines together is the one
// edit that cannot change what it holds: `<<~` sheds the indentation the body's lines
// have in common (§3.7), and moving all of them moves that too.
func (p *printer) verbatim(from, to, ind int) {
	lines := strings.Split(strings.TrimRight(p.slice(from, to), "\n"), "\n")
	lineStart := p.lineStart(from)
	shift := ind*Indent - runeIndent(p.slice(lineStart, from))
	if shift < 0 && !canDedent(lines[1:], -shift) {
		shift = 0
	}
	p.tab(ind)
	p.w(strings.TrimRight(lines[0], " \t\r"))
	for _, ln := range lines[1:] {
		p.nl()
		p.w(shiftLine(ln, shift))
	}
	p.skip(from, to)
}

// lineStart is the offset of the first rune of the line off stands on.
func (p *printer) lineStart(off int) int {
	for i := off - 1; i >= 0; i-- {
		if p.rs[i] == '\n' || p.rs[i] == '\r' {
			return i + 1
		}
	}
	return 0
}

// runeIndent counts the leading blanks of s, which is the column its first non-blank rune
// stands in. U+00A0 is one of them for the reason §3.2 has: messengers send it instead of
// a space, and a line indented with one is indented.
func runeIndent(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\u00a0' {
			break
		}
		n++
	}
	return n
}

// blankIndent counts what `<<~` actually sheds: spaces and tabs, and nothing else. It is
// deliberately *not* runeIndent — a U+00A0 at the start of a body line is text the heredoc
// keeps (the lexer's heredocIndent counts the same two runes), so a shift that treated one
// as indentation would delete a character from the string the program builds.
func blankIndent(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

// canDedent reports whether every non-blank line has n shed-able blanks to give up. Moving
// a body left past its own margin would take text with it — the terminator's own line
// included, which is why that line is not exempt — so when one line cannot afford the
// shift, none of them makes it and the block stays where it was written.
func canDedent(lines []string, n int) bool {
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if blankIndent(ln) < n {
			return false
		}
	}
	return true
}

// shiftLine moves one line of a verbatim block sideways. The carriage return of a CRLF
// file goes with the trailing blanks: the lexer drops it too (§3.2), so the text the
// heredoc holds is the same either way, and the output has one kind of line ending.
func shiftLine(ln string, shift int) string {
	if strings.TrimSpace(ln) == "" {
		return ""
	}
	switch {
	case shift > 0:
		return strings.Repeat(" ", shift) + strings.TrimRight(ln, " \t\r")
	case shift < 0:
		// Runes, not bytes: runeIndent counted runes, and a U+00A0 in the indentation is
		// two bytes wide. Slicing by the count would cut inside a character.
		return strings.TrimRight(string([]rune(ln)[-shift:]), " \t\r")
	}
	return strings.TrimRight(ln, " \t\r")
}
