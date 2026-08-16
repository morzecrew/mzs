// Package lexer turns mzs source into the token stream of SPEC §3. It decodes the
// source to []rune once, never panics, and recovers from an unclassifiable rune by
// recording one diagnostic and continuing at the next rune, so a stray character
// yields one error rather than a cascade.
//
// One lexical decision of §3.3 lives here rather than in the parser: a string literal
// is flattened into STR_BEGIN/STR_TEXT/STR_GVAR/INTERP_BEGIN/…/INTERP_END/STR_END so
// the parser needs no sub-lexer. Interpolation is spelled with '$' (§3.7): `$name` is
// the same host global inside a string as outside it, `${` opens an expression, and a
// '$' that is followed by neither an identifier nor '{' is literal text.
//
// Newline suppression (§3.10) is also resolved here, in both directions: a line break
// following a continuation token is swallowed, and a NEWLINE already decided on is
// dropped again when the token that follows it is a leading `.`, a hanging `else`, an
// `->`, a closing bracket or a binary operator. Doing it in one place is what makes
// leading-dot method chains work without the parser second-guessing the stream.
package lexer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"mzs/internal/token"
)

// ErrNotImplemented is retained for callers written against the stub API. Nothing in
// this package returns it any more.
var ErrNotImplemented = errors.New("mzs: not implemented")

// maxErrors bounds the diagnostic slice so that a megabyte of binary garbage costs a
// bounded amount of memory. Scanning still continues to the end of the input.
const maxErrors = 100

// Error is one lexical diagnostic. Msg is the bare message; the caller adds the file
// name and the "syntax:" kind when it renders the error.
type Error struct {
	Msg string
	Pos token.Pos
}

func (e *Error) Error() string { return e.Msg }

// frame is one open string literal. interp is true while the scanner is inside a
// `${ … }` of that literal, in which case depth counts the `{` nesting so the `}`
// that returns depth to zero can be recognised as INTERP_END.
//
// A heredoc (§3.7) is the same kind of frame read from somewhere else: its body lives on
// the lines *after* the one the `<<~TAG` stands on, so the frame carries where that body
// ends, how much indentation each of its lines sheds, whether it interpolates, and the
// cursor the scanner returns to when the body is done — which is the rest of the tag's
// own line.
type frame struct {
	interp bool
	depth  int
	open   token.Pos

	heredoc bool
	raw     bool
	indent  int // runes of common leading whitespace each body line sheds
	endAt   int // rune index where the body stops: the start of the terminator line

	resume     int // cursor to restore when the body closes
	resumeLine int
	resumeCol  int
	hdEnd      int // pending skip to arm on close (see Lexer.hdEnd)
	hdLines    int
}

// Lexer is a pull-based scanner. Create one with New and call Next until it returns a
// token of kind token.EOF; Next always terminates and always returns a token.
type Lexer struct {
	name string
	rs   []rune
	pos  int
	line int
	col  int
	errs []*Error

	// queue holds tokens produced ahead of time: the tail of a single-quoted string
	// and a token pushed back to let a pending NEWLINE out in front of it.
	queue []token.Token
	stack []frame

	prev      token.Kind // kind of the last token handed to the caller
	pendingNL bool
	nlPos     token.Pos

	// A heredoc's body was read from the lines below the one its tag stands on, so the
	// scanner owes that line's own line break a jump over what it already consumed.
	// hdEnd is where the source resumes — just past the terminator of the last heredoc
	// opened on this line — and hdLines is how many source lines that skips, so the line
	// counter stays true. Zero means nothing is pending; a body always starts further
	// into the source than offset zero, so the sentinel cannot collide with an index.
	hdEnd   int
	hdLines int
}

// New creates a Lexer over src. name is used only in diagnostics. A byte-order mark at
// offset 0 is dropped, so rune offsets are counted from the first real character.
func New(name, src string) *Lexer {
	rs := []rune(src)
	if len(rs) > 0 && rs[0] == '\ufeff' {
		rs = rs[1:]
	}
	return &Lexer{name: name, rs: rs, line: 1, col: 1, prev: token.EOF}
}

// Name returns the source name given to New.
func (l *Lexer) Name() string { return l.name }

// Errors returns every diagnostic recorded so far, in source order.
func (l *Lexer) Errors() []*Error { return l.errs }

// Next returns the next token. After the first token.EOF it keeps returning EOF.
func (l *Lexer) Next() token.Token {
	t := l.produce()
	if l.pendingNL {
		l.pendingNL = false
		// §3.10: the line break survives only when the token that follows it can
		// actually start a new statement.
		if t.Kind != token.EOF && !token.SuppressesNewlineBefore(t.Kind) {
			l.queue = append([]token.Token{t}, l.queue...)
			l.prev = token.NEWLINE
			return token.Token{Kind: token.NEWLINE, Pos: l.nlPos, End: l.nlPos}
		}
	}
	l.prev = t.Kind
	return t
}

func (l *Lexer) produce() token.Token {
	if len(l.queue) > 0 {
		t := l.queue[0]
		l.queue = l.queue[1:]
		return t
	}
	return l.scan()
}

// Lex scans the whole of src and returns the token stream and every diagnostic. The
// returned slice always ends with a token.EOF, so callers may index without bounds
// checks after the terminator.
func Lex(name, src string) ([]token.Token, []*Error) {
	l := New(name, src)
	out := make([]token.Token, 0, len(src)/3+2)
	for {
		t := l.Next()
		out = append(out, t)
		if t.Kind == token.EOF {
			break
		}
	}
	return out, l.Errors()
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

func (l *Lexer) at(i int) rune {
	if j := l.pos + i; j >= 0 && j < len(l.rs) {
		return l.rs[j]
	}
	return 0
}

func (l *Lexer) mark() token.Pos {
	return token.Pos{Offset: l.pos, Line: l.line, Col: l.col}
}

// bump consumes one rune that is known not to be a line terminator.
func (l *Lexer) bump() {
	if l.pos < len(l.rs) {
		l.pos++
		l.col++
	}
}

func (l *Lexer) advanceN(n int) {
	for i := 0; i < n; i++ {
		l.bump()
	}
}

// eatNewline consumes "\r\n", "\r" or "\n" as a single line break (§3.1). It is also
// where a heredoc's line break pays its debt: the body below this line has already been
// scanned, so the cursor jumps over it and the line counter is advanced by as many lines
// as that body and its terminator occupied (§3.7).
func (l *Lexer) eatNewline() {
	if l.at(0) == '\r' {
		l.pos++
		if l.at(0) == '\n' {
			l.pos++
		}
	} else {
		l.pos++
	}
	l.line++
	l.col = 1
	if l.hdEnd > 0 && l.pos <= l.hdEnd {
		l.pos = l.hdEnd
		l.line += l.hdLines
		l.hdEnd, l.hdLines = 0, 0
	}
}

func (l *Lexer) errorf(p token.Pos, format string, a ...any) {
	if len(l.errs) >= maxErrors {
		return
	}
	l.errs = append(l.errs, &Error{Msg: fmt.Sprintf(format, a...), Pos: p})
}

func (l *Lexer) op(k token.Kind, start token.Pos) token.Token {
	return token.Token{Kind: k, Value: k.String(), Pos: start, End: l.mark()}
}

// ---------------------------------------------------------------------------
// The main scanner
// ---------------------------------------------------------------------------

func (l *Lexer) scan() token.Token {
	for {
		// Inside a string body every rune is significant, so this test comes before
		// whitespace skipping.
		if n := len(l.stack); n > 0 && !l.stack[n-1].interp {
			return l.scanStringPart()
		}
		if l.pos >= len(l.rs) {
			return l.finish()
		}
		r := l.rs[l.pos]
		switch {
		case r == ' ' || r == '\t' || r == '\u00a0':
			// U+00A0 is whitespace here because messengers send it (§3.2).
			l.bump()
			continue
		case r == '\n' || r == '\r':
			p := l.mark()
			l.eatNewline()
			// A `${ … }` is always a continuation, whatever precedes the break, and a
			// break before the first token of the file is not a statement terminator.
			if len(l.stack) == 0 && l.prev != token.NEWLINE && l.prev != token.EOF &&
				!token.SuppressesNewlineAfter(l.prev) {
				l.pendingNL = true
				l.nlPos = p
			}
			continue
		case r == '#':
			// §3.2: '#' opens a comment with no exceptions. Inside a string literal the
			// scanner never gets here, and there '#' is ordinary text.
			l.skipLineComment()
			continue
		}

		start := l.mark()
		switch {
		case r == '"':
			l.bump()
			l.stack = append(l.stack, frame{open: start})
			return token.Token{Kind: token.STR_BEGIN, Value: `"`, Pos: start, End: l.mark()}
		case r == '\'':
			return l.scanSingleQuoted(start)
		case r == '$':
			return l.scanGlobal(start)
		case r == '@':
			// Reserved for a later version (§3.4, §20): report and skip the sigil only.
			l.bump()
			l.errorf(start, "'@' is reserved; instance variables do not exist in v0.1")
			continue
		case r >= '0' && r <= '9':
			return l.scanNumber(start)
		case r == '<' && l.at(1) == '<' && l.at(2) == '~':
			// §3.7: `<<~TAG` is a heredoc, and `<<` alone is still reserved (§20). The
			// three runes are matched here rather than in the operator table because the
			// token they open is a string literal and its body is not on this line.
			return l.scanHeredoc(start)
		case r == '/' && (l.pendingNL || token.AllowsRegexAfter(l.prev)):
			// A pending NEWLINE *is* the previous significant token: §3.8 lists NEWLINE
			// among the kinds a regex may follow, and promises that "a match arm
			// beginning with /re/ … and a statement-initial /re/ all take the regex
			// branch". Its own parenthetical "(ignoring NEWLINE)" would make both of
			// those a division whenever the line above ended in a value — which is
			// exactly the shape of every arm in examples/conditions.mzs. The list wins.
			return l.scanRegex(start)
		case token.IsIdentStart(r):
			return l.scanIdent(start)
		}

		if o, n, ok := token.MatchOperator(l.rs[l.pos:]); ok {
			l.advanceN(n)
			if o.IsPoison() {
				// §3.9: the lexeme exists only to carry a §5.6 fix-it. Report it once, at
				// its own column, and emit nothing. Both spellings begin with '=', so the
				// scan continues as if an assignment had been seen: what follows is an
				// operand, which keeps `s =~ /re/` from lexing its regex as a division.
				l.errorf(start, "%s", o.Diag)
				l.prev = token.ASSIGN
				continue
			}
			if f := l.top(); f != nil {
				switch o.Kind {
				case token.LBRACE:
					f.depth++
				case token.RBRACE:
					if f.depth == 0 {
						f.interp = false
						return token.Token{Kind: token.INTERP_END, Value: "}", Pos: start, End: l.mark()}
					}
					f.depth--
				}
			}
			return l.op(o.Kind, start)
		}

		l.badRune(start, r)
	}
}

func (l *Lexer) top() *frame {
	if n := len(l.stack); n > 0 {
		return &l.stack[n-1]
	}
	return nil
}

// badRune reports one diagnostic for a rune that no rule of §3 can start, and skips it
// (§3.3 recovery). Several spellings get their §5.6 fix-it here rather than in the parser,
// because none of '&', '|' and '^' is a token kind — the parser never sees them.
//
// The three bit operators are deliberately absent (§12.5): '&' and '|' one keystroke from
// '&&' and '||' is the ambiguity D16 refuses, so the operations are functions instead and
// this is where anyone who typed the operator is told their name.
func (l *Lexer) badRune(start token.Pos, r rune) {
	switch {
	case r == '&' && l.at(1) == '.':
		l.errorf(start, "'&.' is not an mzs operator; use '?.'")
		l.advanceN(2)
	case r == '|' && l.prev == token.LBRACE:
		l.errorf(start, "closure parameters are parenthesised: { (x) -> … }")
		l.bump()
	case r == '&':
		l.errorf(start, "'&' is not an mzs operator; use band(a, b), or '&&' for logical and")
		l.bump()
	case r == '|':
		l.errorf(start, "'|' is not an mzs operator; use bor(a, b), or '||' for logical or")
		l.bump()
	case r == '^':
		l.errorf(start, "'^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power")
		l.bump()
	default:
		l.errorf(start, "unexpected character %q", string(r))
		l.bump()
	}
}

// finish reports any string left open at end of input and returns the EOF token.
func (l *Lexer) finish() token.Token {
	if n := len(l.stack); n > 0 {
		l.errorf(l.stack[0].open, "unterminated string literal")
		l.stack = l.stack[:0]
	}
	p := l.mark()
	return token.Token{Kind: token.EOF, Pos: p, End: p}
}

func (l *Lexer) skipLineComment() {
	for l.pos < len(l.rs) && l.rs[l.pos] != '\n' && l.rs[l.pos] != '\r' {
		l.bump()
	}
}

// ---------------------------------------------------------------------------
// Identifiers and globals
// ---------------------------------------------------------------------------

// scanIdent reads an identifier or keyword (§3.4). There is no '?'/'!' suffix, so
// `empty?` is IDENT(empty) followed by QUESTION and the parser owns the fix-it.
func (l *Lexer) scanIdent(start token.Pos) token.Token {
	var sb strings.Builder
	for l.pos < len(l.rs) && token.IsIdentPart(l.rs[l.pos]) {
		sb.WriteRune(l.rs[l.pos])
		l.bump()
	}
	name := sb.String()
	k := token.Lookup(name)
	if token.IsKeywordKind(k) {
		return token.Token{Kind: k, Pos: start, End: l.mark()}
	}
	return token.Token{Kind: k, Value: name, Pos: start, End: l.mark()}
}

// scanGlobal reads `$` + ident_part+ (§3.4). The name keeps its sigil, so Value is
// "$__sent" and one string compares against the host binding table.
func (l *Lexer) scanGlobal(start token.Pos) token.Token {
	l.bump() // '$'
	var sb strings.Builder
	sb.WriteRune('$')
	for l.pos < len(l.rs) && token.IsIdentPart(l.rs[l.pos]) {
		sb.WriteRune(l.rs[l.pos])
		l.bump()
	}
	if sb.Len() == 1 {
		l.errorf(start, "'$' must be followed by a variable name")
	}
	return token.Token{Kind: token.GVAR, Value: sb.String(), Pos: start, End: l.mark()}
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

func (l *Lexer) scanNumber(start token.Pos) token.Token {
	if l.at(0) == '0' {
		switch l.at(1) {
		case 'x', 'X':
			return l.scanRadix(start, 16)
		case 'b', 'B':
			return l.scanRadix(start, 2)
		case 'o', 'O':
			return l.scanRadix(start, 8)
		}
	}
	var sb strings.Builder
	l.readDigits(&sb)
	isFloat := false
	// §3.6: a '.' continues the number only when a digit follows, so `1.str` is a
	// method call and `1..5` is a range.
	if l.at(0) == '.' && isDecDigit(l.at(1)) {
		isFloat = true
		sb.WriteByte('.')
		l.bump()
		l.readDigits(&sb)
	}
	if r := l.at(0); r == 'e' || r == 'E' {
		i := 1
		if l.at(1) == '+' || l.at(1) == '-' {
			i = 2
		}
		if isDecDigit(l.at(i)) {
			isFloat = true
			sb.WriteRune(r)
			l.bump()
			if i == 2 {
				sb.WriteRune(l.at(0))
				l.bump()
			}
			l.readDigits(&sb)
		}
	}
	text := sb.String()
	if isFloat {
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			l.errorf(start, "invalid float literal %q", text)
			text = "0"
		}
		return token.Token{Kind: token.FLOAT, Value: text, Pos: start, End: l.mark()}
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		l.errorf(start, "integer literal %q does not fit in an int", text)
		v = 0
	}
	return token.Token{Kind: token.INT, Value: strconv.FormatInt(v, 10), Pos: start, End: l.mark()}
}

func (l *Lexer) scanRadix(start token.Pos, base int) token.Token {
	l.advanceN(2)
	var sb strings.Builder
	for l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if r == '_' {
			l.bump()
			continue
		}
		if digitVal(r) >= base {
			break
		}
		sb.WriteRune(r)
		l.bump()
	}
	text := sb.String()
	if text == "" {
		l.errorf(start, "malformed number literal")
		text = "0"
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		l.errorf(start, "integer literal does not fit in an int")
		v = 0
	}
	return token.Token{Kind: token.INT, Value: strconv.FormatInt(v, 10), Pos: start, End: l.mark()}
}

// readDigits copies decimal digits into sb, dropping the '_' separators of §3.6.
func (l *Lexer) readDigits(sb *strings.Builder) {
	for l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if r == '_' {
			l.bump()
			continue
		}
		if !isDecDigit(r) {
			return
		}
		sb.WriteRune(r)
		l.bump()
	}
}

func isDecDigit(r rune) bool { return r >= '0' && r <= '9' }

func digitVal(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return 99
}

// ---------------------------------------------------------------------------
// Strings
// ---------------------------------------------------------------------------

// scanSingleQuoted handles the raw form of §3.7 in one go: it has no interpolation and
// only two escapes, so the whole STR_BEGIN/STR_TEXT/STR_END triple is known immediately.
func (l *Lexer) scanSingleQuoted(start token.Pos) token.Token {
	l.bump() // opening quote
	textStart := l.mark()
	var sb strings.Builder
	closed := false
	for l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if r == '\\' {
			if n := l.at(1); n == '\'' || n == '\\' {
				sb.WriteRune(n)
				l.advanceN(2)
				continue
			}
			// Every other backslash is one literal backslash, which is what makes
			// '\bменю' the right way to write a regex source string.
			sb.WriteRune('\\')
			l.bump()
			continue
		}
		if r == '\'' {
			closed = true
			break
		}
		if r == '\n' || r == '\r' {
			sb.WriteRune('\n')
			l.eatNewline()
			continue
		}
		sb.WriteRune(r)
		l.bump()
	}
	textEnd := l.mark()
	if closed {
		l.bump()
	} else {
		l.errorf(start, "unterminated string literal")
	}
	end := l.mark()
	if sb.Len() > 0 {
		l.queue = append(l.queue, token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: textStart, End: textEnd})
	}
	l.queue = append(l.queue, token.Token{Kind: token.STR_END, Value: "'", Pos: textEnd, End: end})
	return token.Token{Kind: token.STR_BEGIN, Value: "'", Pos: start, End: textStart}
}

// scanStringPart advances a double-quoted literal by one token: a run of decoded text,
// a `$name` global, the INTERP_BEGIN that opens `${`, or the closing quote.
func (l *Lexer) scanStringPart() token.Token {
	f := l.top()
	if f.heredoc {
		return l.scanHeredocPart(f)
	}
	start := l.mark()
	var sb strings.Builder
	for l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if r == '"' {
			if sb.Len() > 0 {
				return token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: start, End: l.mark()}
			}
			l.bump()
			l.stack = l.stack[:len(l.stack)-1]
			return token.Token{Kind: token.STR_END, Value: `"`, Pos: start, End: l.mark()}
		}
		if r == '$' && l.at(1) == '{' {
			if sb.Len() > 0 {
				return token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: start, End: l.mark()}
			}
			l.advanceN(2)
			f.interp = true
			f.depth = 0
			return token.Token{Kind: token.INTERP_BEGIN, Value: "${", Pos: start, End: l.mark()}
		}
		if r == '$' && token.IsIdentStart(l.at(1)) {
			// §3.7: `$name` means the same global here as outside the string, and it
			// takes the longest run of ident_part — "$n00" is the global $n00.
			if sb.Len() > 0 {
				return token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: start, End: l.mark()}
			}
			return l.scanStringGlobal(start)
		}
		if r == '\\' {
			l.readEscape(&sb)
			continue
		}
		if r == '\n' || r == '\r' {
			sb.WriteRune('\n')
			l.eatNewline()
			continue
		}
		// A '$' that is followed by neither ident_start nor '{' is literal text, and so
		// is every '#': §3.2 gives '#' no meaning inside a string literal.
		sb.WriteRune(r)
		l.bump()
	}
	if sb.Len() > 0 {
		// Hand the text over first; the next call reports the missing quote.
		return token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: start, End: l.mark()}
	}
	l.errorf(f.open, "unterminated string literal")
	l.stack = l.stack[:len(l.stack)-1]
	return token.Token{Kind: token.STR_END, Pos: start, End: start}
}

// scanStringGlobal reads the `$name` form of §3.7. Value keeps the sigil, exactly as a
// GVAR outside a string does.
func (l *Lexer) scanStringGlobal(start token.Pos) token.Token {
	l.bump() // '$'
	var sb strings.Builder
	sb.WriteRune('$')
	for l.pos < len(l.rs) && token.IsIdentPart(l.rs[l.pos]) {
		sb.WriteRune(l.rs[l.pos])
		l.bump()
	}
	return token.Token{Kind: token.STR_GVAR, Value: sb.String(), Pos: start, End: l.mark()}
}

// readEscape decodes one backslash escape of §3.7 into sb. An unknown escape is the
// literal character, so `\$` is a dollar and `\q` is a q.
func (l *Lexer) readEscape(sb *strings.Builder) {
	start := l.mark()
	l.bump() // backslash
	if l.pos >= len(l.rs) {
		sb.WriteRune('\\')
		return
	}
	r := l.rs[l.pos]
	if r == '\n' || r == '\r' {
		// A line break is its own literal character; there is no line splicing.
		sb.WriteRune('\n')
		l.eatNewline()
		return
	}
	l.bump()
	switch r {
	case 'n':
		sb.WriteRune('\n')
	case 'r':
		sb.WriteRune('\r')
	case 't':
		sb.WriteRune('\t')
	case '0':
		sb.WriteRune(0)
	case 'a':
		sb.WriteRune(7)
	case 'b':
		sb.WriteRune(8)
	case 'f':
		sb.WriteRune(12)
	case 'v':
		sb.WriteRune(11)
	case 'e':
		sb.WriteRune(27)
	case '\\', '"', '\'', '$':
		sb.WriteRune(r)
	case 'u':
		l.readUnicodeEscape(sb, start)
	case 'x':
		l.readHexEscape(sb, start)
	default:
		sb.WriteRune(r)
	}
}

func (l *Lexer) readUnicodeEscape(sb *strings.Builder, start token.Pos) {
	if l.at(0) == '{' {
		l.bump()
		var digits strings.Builder
		for l.pos < len(l.rs) && digitVal(l.rs[l.pos]) < 16 && digits.Len() < 6 {
			digits.WriteRune(l.rs[l.pos])
			l.bump()
		}
		if l.at(0) != '}' || digits.Len() == 0 {
			l.errorf(start, "malformed \\u{...} escape")
			sb.WriteString("u{" + digits.String())
			return
		}
		l.bump()
		l.writeCodepoint(sb, digits.String(), start)
		return
	}
	var digits strings.Builder
	for l.pos < len(l.rs) && digits.Len() < 4 && digitVal(l.rs[l.pos]) < 16 {
		digits.WriteRune(l.rs[l.pos])
		l.bump()
	}
	if digits.Len() != 4 {
		l.errorf(start, "malformed \\u escape")
		sb.WriteString("u" + digits.String())
		return
	}
	l.writeCodepoint(sb, digits.String(), start)
}

func (l *Lexer) writeCodepoint(sb *strings.Builder, hex string, start token.Pos) {
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || v > unicode.MaxRune {
		l.errorf(start, "escape \\u{%s} is not a Unicode code point", hex)
		return
	}
	sb.WriteRune(rune(v))
}

func (l *Lexer) readHexEscape(sb *strings.Builder, start token.Pos) {
	var digits strings.Builder
	for l.pos < len(l.rs) && digits.Len() < 2 && digitVal(l.rs[l.pos]) < 16 {
		digits.WriteRune(l.rs[l.pos])
		l.bump()
	}
	if digits.Len() == 0 {
		l.errorf(start, "malformed \\x escape")
		sb.WriteRune('x')
		return
	}
	v, _ := strconv.ParseUint(digits.String(), 16, 8)
	sb.WriteByte(byte(v))
}

// ---------------------------------------------------------------------------
// Heredocs (§3.7)
// ---------------------------------------------------------------------------

// scanHeredoc reads `<<~TAG` and hands back the STR_BEGIN of the literal whose body is
// the lines below. There is one form and not three: the indentation common to the body's
// non-blank lines is shed, `<<~TAG` interpolates exactly as a double-quoted string does
// and `<<~'TAG'` is raw exactly as a single-quoted one is.
//
// The whole difficulty is that the body is not where the cursor is. §3.10 swallows the
// line break after a comma or an operator, so a heredoc inside an argument list must not
// read its body from the token's position — it reads whole *lines*, starting below the
// one the tag stands on, and leaves behind a debt the tag line's own line break pays:
// eatNewline jumps the cursor over what has already been consumed. That is what makes
//
//	f(<<~A, <<~B)
//
// read two bodies in the order they are written and then close the call.
func (l *Lexer) scanHeredoc(start token.Pos) token.Token {
	if len(l.stack) > 0 {
		// A body is whole source lines, and the lines below an interpolation belong to
		// the literal the interpolation sits in. One diagnostic beats two literals
		// fighting over the same text.
		l.advanceN(3)
		l.errorf(start, "a heredoc cannot open inside a string interpolation")
		return l.emptyHeredoc(start)
	}
	l.advanceN(3) // "<<~"
	raw := l.at(0) == '\''
	if raw {
		l.bump()
	}
	var tag strings.Builder
	for l.pos < len(l.rs) && token.IsIdentPart(l.rs[l.pos]) {
		tag.WriteRune(l.rs[l.pos])
		l.bump()
	}
	if tag.Len() == 0 {
		l.errorf(start, "'<<~' needs a tag: write <<~TEXT, the body on the lines below, then TEXT alone")
		return l.emptyHeredoc(start)
	}
	if raw && !l.accept('\'') {
		// Recover as though the quote were there. The lines below are still the body the
		// author wrote, and lexing them as code would turn one missing rune into a page
		// of diagnostics (§17).
		l.errorf(start, "unterminated heredoc tag: write <<~'%s'", tag.String())
	}

	resume, resumeLine, resumeCol := l.pos, l.line, l.col
	open := token.Token{Kind: token.STR_BEGIN, Value: "<<~", Pos: start, End: l.mark()}

	// A second heredoc on the same line takes its body from below the first one's
	// terminator, which is exactly the debt hdEnd already records.
	bodyStart, above := l.hdEnd, l.hdLines
	if bodyStart == 0 {
		bodyStart = l.afterLineBreak(l.pos)
	}
	bodyEnd, termEnd, lines, ok := l.heredocBody(bodyStart, tag.String())
	if !ok {
		l.errorf(start, "unterminated heredoc: the body ends at a line holding %s alone", tag.String())
	}

	l.hdEnd, l.hdLines = 0, 0
	l.stack = append(l.stack, frame{
		heredoc:    true,
		raw:        raw,
		open:       start,
		indent:     l.heredocIndent(bodyStart, bodyEnd),
		endAt:      bodyEnd,
		resume:     resume,
		resumeLine: resumeLine,
		resumeCol:  resumeCol,
		hdEnd:      termEnd,
		hdLines:    above + lines,
	})
	l.pos, l.line, l.col = bodyStart, resumeLine+1+above, 1
	l.skipHeredocIndent(l.top())
	return open
}

// accept consumes r when it is the current rune.
func (l *Lexer) accept(r rune) bool {
	if l.at(0) != r {
		return false
	}
	l.bump()
	return true
}

// emptyHeredoc stands in for a malformed `<<~`: the parser is handed the empty string
// literal it was reaching for, so one bad tag costs one diagnostic (§17).
func (l *Lexer) emptyHeredoc(start token.Pos) token.Token {
	end := l.mark()
	l.queue = append(l.queue, token.Token{Kind: token.STR_END, Value: "<<~", Pos: end, End: end})
	return token.Token{Kind: token.STR_BEGIN, Value: "<<~", Pos: start, End: end}
}

// heredocBody locates the terminator: the first line from `from` on whose only content is
// the tag. bodyEnd is where that line begins — so the body keeps the line break in front
// of it — termEnd is where the source resumes below it, and lines is how many source
// lines the two together occupy. ok is false at end of input, where the caller reports
// the missing terminator and takes the rest of the file as the body.
func (l *Lexer) heredocBody(from int, tag string) (bodyEnd, termEnd, lines int, ok bool) {
	for i := from; i < len(l.rs); {
		e := i
		for e < len(l.rs) && l.rs[e] != '\n' && l.rs[e] != '\r' {
			e++
		}
		next := l.afterLineBreak(e)
		lines++
		if isHeredocTag(l.rs[i:e], tag) {
			return i, next, lines, true
		}
		i = next
	}
	return len(l.rs), len(l.rs), lines, false
}

// isHeredocTag reports whether a body line is the terminator. Leading and trailing blanks
// are ignored, so the terminator may be indented with the body it closes.
func isHeredocTag(line []rune, tag string) bool {
	i, j := 0, len(line)
	for i < j && isHeredocBlank(line[i]) {
		i++
	}
	for j > i && isHeredocBlank(line[j-1]) {
		j--
	}
	return string(line[i:j]) == tag
}

// isHeredocBlank is the whitespace a terminator line may be padded with. U+00A0 is in it
// for the reason §3.2 has: messengers send it instead of a space, and a terminator that
// failed to close because of one would read as a body with no end.
func isHeredocBlank(r rune) bool { return r == ' ' || r == '\t' || r == ' ' }

// heredocIndent is the indentation `<<~` sheds: the shortest run of leading blanks over
// the body's **non-blank** lines. A line made of blanks alone has no say, so a heredoc
// whose paragraphs are separated by empty lines dedents by what its text says rather than
// by nothing.
func (l *Lexer) heredocIndent(from, to int) int {
	indent := -1
	for i := from; i < to; {
		e := i
		for e < to && l.rs[e] != '\n' && l.rs[e] != '\r' {
			e++
		}
		n := 0
		for i+n < e && (l.rs[i+n] == ' ' || l.rs[i+n] == '\t') {
			n++
		}
		if i+n < e && (indent < 0 || n < indent) {
			indent = n
		}
		i = min(l.afterLineBreak(e), to)
	}
	if indent < 0 {
		return 0
	}
	return indent
}

// skipHeredocIndent drops one body line's shed indentation. A line shorter than the
// common indent — a blank one — gives up what it has and no more.
func (l *Lexer) skipHeredocIndent(f *frame) {
	for i := 0; i < f.indent && l.pos < f.endAt; i++ {
		if r := l.rs[l.pos]; r != ' ' && r != '\t' {
			return
		}
		l.bump()
	}
}

// scanHeredocPart advances a heredoc body by one token. It is scanStringPart's rule with
// two differences: a `"` is ordinary text, and the literal ends at an index rather than
// at a quote. The raw form takes neither escapes nor interpolation, so `<<~'SQL'` carries
// a `$1` placeholder and a `\d` the way `'…'` does.
func (l *Lexer) scanHeredocPart(f *frame) token.Token {
	start := l.mark()
	var sb strings.Builder
	text := func() token.Token {
		return token.Token{Kind: token.STR_TEXT, Value: sb.String(), Pos: start, End: l.mark()}
	}
	for l.pos < f.endAt && l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if !f.raw {
			if r == '$' && l.at(1) == '{' {
				if sb.Len() > 0 {
					return text()
				}
				p := l.mark()
				l.advanceN(2)
				f.interp = true
				f.depth = 0
				return token.Token{Kind: token.INTERP_BEGIN, Value: "${", Pos: p, End: l.mark()}
			}
			if r == '$' && token.IsIdentStart(l.at(1)) {
				if sb.Len() > 0 {
					return text()
				}
				return l.scanStringGlobal(l.mark())
			}
			if r == '\\' {
				l.readEscape(&sb)
				if l.col == 1 {
					// The escape swallowed the line break, and the line below it still
					// sheds the body's common indent.
					l.skipHeredocIndent(f)
				}
				continue
			}
		}
		if r == '\n' || r == '\r' {
			sb.WriteRune('\n')
			l.eatNewline()
			l.skipHeredocIndent(f)
			continue
		}
		sb.WriteRune(r)
		l.bump()
	}
	if sb.Len() > 0 {
		return text()
	}
	return l.closeHeredoc(f)
}

// closeHeredoc ends the literal and puts the cursor back where the tag left it, so the
// rest of the tag's own line is scanned next. The skip over the body is armed here and
// collected by that line's line break (eatNewline).
//
// STR_END is reported at the resume point rather than in the body, which is what makes
// the literal's node span `<<~TAG` — the text the author wrote in that expression.
func (l *Lexer) closeHeredoc(f *frame) token.Token {
	l.pos, l.line, l.col = f.resume, f.resumeLine, f.resumeCol
	l.hdEnd, l.hdLines = f.hdEnd, f.hdLines
	l.stack = l.stack[:len(l.stack)-1]
	p := l.mark()
	return token.Token{Kind: token.STR_END, Value: "<<~", Pos: p, End: p}
}

// afterLineBreak is the index just past the first line break at or after i, or the end of
// input when there is none.
func (l *Lexer) afterLineBreak(i int) int {
	for ; i < len(l.rs); i++ {
		if l.rs[i] == '\n' {
			return i + 1
		}
		if l.rs[i] == '\r' {
			if i+1 < len(l.rs) && l.rs[i+1] == '\n' {
				return i + 2
			}
			return i + 1
		}
	}
	return len(l.rs)
}

// ---------------------------------------------------------------------------
// Regex literals
// ---------------------------------------------------------------------------

// scanRegex reads `/pattern/flags` (§3.8). The pattern is kept raw except for `\/`,
// which becomes a plain '/', because the regex engine wants the pattern text itself.
// A '/' inside a character class does not close the literal.
func (l *Lexer) scanRegex(start token.Pos) token.Token {
	l.bump() // opening '/'
	var sb strings.Builder
	inClass := false
	closed := false
	broken := false
	for l.pos < len(l.rs) {
		r := l.rs[l.pos]
		if r == '\n' || r == '\r' {
			// One diagnostic, not two: the literal is already unusable.
			l.errorf(start, "newline in regex literal; write \\n instead")
			broken = true
			break
		}
		if r == '\\' {
			n := l.at(1)
			if n == 0 {
				sb.WriteRune('\\')
				l.bump()
				continue
			}
			if n == '/' {
				sb.WriteRune('/')
			} else {
				sb.WriteRune('\\')
				sb.WriteRune(n)
			}
			l.advanceN(2)
			continue
		}
		switch {
		case r == '[':
			inClass = true
		case r == ']':
			inClass = false
		case r == '/' && !inClass:
			l.bump()
			closed = true
		}
		if closed {
			break
		}
		sb.WriteRune(r)
		l.bump()
	}
	if !closed && !broken {
		l.errorf(start, "unterminated regex literal")
	}
	var flags strings.Builder
	for l.pos < len(l.rs) && strings.ContainsRune("imxsu", l.rs[l.pos]) {
		flags.WriteRune(l.rs[l.pos])
		l.bump()
	}
	return token.Token{
		Kind:  token.REGEX,
		Value: sb.String(),
		Flags: flags.String(),
		Pos:   start,
		End:   l.mark(),
	}
}
