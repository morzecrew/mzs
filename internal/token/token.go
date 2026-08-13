// Package token defines the lexical vocabulary of mzs: the token kinds of SPEC §3.3, the
// fourteen keywords of §3.5, the longest-match operator table of §3.9, and the small set
// of predicates that the lexer and the parser must agree on — newline suppression (§3.10),
// the regex-versus-division rule (§3.8) and the precedence levels of §5.1.
//
// The operator table is exposed rather than open-coded so the historical bug of adding
// runes together and advancing the cursor by one for a two-rune lexeme cannot recur:
// MatchOperator returns the table entry together with the width **in runes** of the lexeme
// it matched, and the caller advances by exactly that.
//
// Poison lexemes. §3.9 lists two lexemes, "=~" and "=!", that exist only to produce the
// fix-it diagnostics of §5.6 and are never valid tokens. They are deliberately *not* Kinds:
// §3.3 fixes the Kind set exactly, and a Kind that can never be emitted is a trap for every
// switch over Kind in the parser and the evaluator. They live in the Poison table instead
// and are matched by the same longest-match scan as the real operators — they have to be,
// because otherwise "=~" lexes as ASSIGN followed by TILDE and the one diagnostic §5.6 asks
// for becomes a cascade. MatchOperator hands them back with a non-empty Operator.Diag; a
// caller that sees Diag != "" reports Diag at the lexeme's position, skips the lexeme's
// runes and emits no token.
package token

import (
	"slices"
	"unicode"
)

// Kind enumerates every token the lexer may emit. The order follows SPEC §3.3 exactly;
// values are not stable across spec revisions and must never be persisted.
type Kind uint8

const (
	EOF Kind = iota
	NEWLINE
	SEMI

	IDENT
	GVAR

	INT
	FLOAT
	STR_BEGIN
	STR_TEXT
	STR_GVAR
	INTERP_BEGIN
	INTERP_END
	STR_END
	REGEX

	// The sixteen keywords of §3.5, in that section's order.
	KW_FN
	KW_IF
	KW_ELSE
	KW_MATCH
	KW_WHILE
	KW_FOR
	KW_IN
	KW_BREAK
	KW_NEXT
	KW_RETURN
	KW_TRY
	KW_TRUE
	KW_FALSE
	KW_NIL
	KW_INCLUDE
	KW_EXPORT

	ASSIGN
	DECLARE
	PLUS_EQ
	MINUS_EQ
	STAR_EQ
	SLASH_EQ
	PERCENT_EQ
	POW_EQ
	OR_EQ
	AND_EQ
	NIL_EQ
	EQ
	NEQ
	TILDE
	NTILDE
	LT
	LTE
	GT
	GTE
	SPACESHIP
	ANDAND
	OROR
	BANG
	NILNIL
	PLUS
	MINUS
	STAR
	SLASH
	PERCENT
	POW
	DOT
	SAFEDOT
	DOTDOT
	DOTLT
	ARROW
	QUESTION
	COLON
	COMMA
	LPAREN
	RPAREN
	LBRACKET
	RBRACKET
	LBRACE
	RBRACE

	// numKinds is not a token; it bounds the tables below.
	numKinds
)

var kindNames = [numKinds]string{
	EOF:          "EOF",
	NEWLINE:      "NEWLINE",
	SEMI:         ";",
	IDENT:        "IDENT",
	GVAR:         "GVAR",
	INT:          "INT",
	FLOAT:        "FLOAT",
	STR_BEGIN:    "STR_BEGIN",
	STR_TEXT:     "STR_TEXT",
	STR_GVAR:     "STR_GVAR",
	INTERP_BEGIN: "INTERP_BEGIN",
	INTERP_END:   "INTERP_END",
	STR_END:      "STR_END",
	REGEX:        "REGEX",

	KW_FN:      "fn",
	KW_IF:      "if",
	KW_ELSE:    "else",
	KW_MATCH:   "match",
	KW_WHILE:   "while",
	KW_FOR:     "for",
	KW_IN:      "in",
	KW_BREAK:   "break",
	KW_NEXT:    "next",
	KW_RETURN:  "return",
	KW_TRY:     "try",
	KW_TRUE:    "true",
	KW_FALSE:   "false",
	KW_NIL:     "nil",
	KW_INCLUDE: "include",
	KW_EXPORT:  "export",

	ASSIGN:     "=",
	DECLARE:    ":=",
	PLUS_EQ:    "+=",
	MINUS_EQ:   "-=",
	STAR_EQ:    "*=",
	SLASH_EQ:   "/=",
	PERCENT_EQ: "%=",
	POW_EQ:     "**=",
	OR_EQ:      "||=",
	AND_EQ:     "&&=",
	NIL_EQ:     "??=",
	EQ:         "==",
	NEQ:        "!=",
	TILDE:      "~",
	NTILDE:     "!~",
	LT:         "<",
	LTE:        "<=",
	GT:         ">",
	GTE:        ">=",
	SPACESHIP:  "<=>",
	ANDAND:     "&&",
	OROR:       "||",
	BANG:       "!",
	NILNIL:     "??",
	PLUS:       "+",
	MINUS:      "-",
	STAR:       "*",
	SLASH:      "/",
	PERCENT:    "%",
	POW:        "**",
	DOT:        ".",
	SAFEDOT:    "?.",
	DOTDOT:     "..",
	DOTLT:      "..<",
	ARROW:      "->",
	QUESTION:   "?",
	COLON:      ":",
	COMMA:      ",",
	LPAREN:     "(",
	RPAREN:     ")",
	LBRACKET:   "[",
	RBRACKET:   "]",
	LBRACE:     "{",
	RBRACE:     "}",
}

// String returns the lexeme for operators and keywords and a symbolic name for the
// value-carrying kinds. It is what diagnostics print, so it must stay stable.
func (k Kind) String() string {
	if k < numKinds && kindNames[k] != "" {
		return kindNames[k]
	}
	return "UNKNOWN"
}

// Pos is a source position. Line and Col are 1-based and Col counts runes, because
// the corpus is full of Cyrillic and emoji and a byte column would point nowhere.
type Pos struct {
	Offset int // rune offset into the decoded source
	Line   int
	Col    int
}

// IsValid reports whether p carries real position information.
func (p Pos) IsValid() bool { return p.Line > 0 }

// Token is one lexical unit. Value carries the lexeme for operators, the decoded text
// for IDENT/GVAR/STR_TEXT/STR_GVAR, the digits for INT/FLOAT, and the raw pattern for
// REGEX. Flags is used only by REGEX.
type Token struct {
	Kind  Kind
	Value string
	Flags string
	Pos   Pos
	End   Pos
}

func (t Token) String() string {
	if t.Value != "" {
		return t.Kind.String() + "(" + t.Value + ")"
	}
	return t.Kind.String()
}

// keywords is the complete table of SPEC §3.5. Sixteen entries, and nothing may be added:
// "it", "_" and "from" are ordinary identifiers (§3.4) — "from" is read positionally inside
// an `include`, so a variable may still be called that — and every word Ruby reserves that
// is absent here — do, end, elsif, unless, until, loop, and, or, not, def, rescue, then —
// lexes as an IDENT and is diagnosed by the parser (§5.6).
var keywords = map[string]Kind{
	"fn":      KW_FN,
	"if":      KW_IF,
	"else":    KW_ELSE,
	"match":   KW_MATCH,
	"while":   KW_WHILE,
	"for":     KW_FOR,
	"in":      KW_IN,
	"break":   KW_BREAK,
	"next":    KW_NEXT,
	"return":  KW_RETURN,
	"try":     KW_TRY,
	"true":    KW_TRUE,
	"false":   KW_FALSE,
	"nil":     KW_NIL,
	"include": KW_INCLUDE,
	"export":  KW_EXPORT,
}

// Keywords returns a copy of the keyword table, for tests and for the CLI's --tokens dump.
func Keywords() map[string]Kind {
	out := make(map[string]Kind, len(keywords))
	for k, v := range keywords {
		out[k] = v
	}
	return out
}

// IsKeyword reports whether s is a reserved word.
func IsKeyword(s string) bool { _, ok := keywords[s]; return ok }

// Lookup classifies a completed identifier lexeme: its keyword kind, or IDENT. The case of
// the first rune means nothing (§3.4) — there is no CONST kind — and an identifier can
// carry no "?"/"!" suffix, so no lexeme reaching here can be a keyword plus punctuation.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return IDENT
}

// Operator pairs a lexeme with its kind. A non-empty Diag marks a *poison* lexeme: one of
// the two spellings of §3.9 that exist only to produce a §5.6 fix-it. Its Kind is EOF and
// it must never be emitted as a token.
type Operator struct {
	Text string
	Kind Kind
	Diag string
}

// IsPoison reports whether o is an error-only lexeme rather than a real operator.
func (o Operator) IsPoison() bool { return o.Diag != "" }

// Operators is the table of SPEC §3.9, longest lexeme first. Note the absences: there is no
// "&", "|", "&.", "::", "..." or "=>" in mzs, so those runes and spellings are a lex or
// parse error rather than a token.
var Operators = []Operator{
	{Text: "**=", Kind: POW_EQ}, {Text: "..<", Kind: DOTLT}, {Text: "||=", Kind: OR_EQ},
	{Text: "&&=", Kind: AND_EQ}, {Text: "??=", Kind: NIL_EQ}, {Text: "<=>", Kind: SPACESHIP},

	{Text: "==", Kind: EQ}, {Text: "!=", Kind: NEQ}, {Text: "<=", Kind: LTE},
	{Text: ">=", Kind: GTE}, {Text: "**", Kind: POW}, {Text: "..", Kind: DOTDOT},
	{Text: "?.", Kind: SAFEDOT}, {Text: "->", Kind: ARROW}, {Text: ":=", Kind: DECLARE},
	{Text: "??", Kind: NILNIL}, {Text: "&&", Kind: ANDAND}, {Text: "||", Kind: OROR},
	{Text: "!~", Kind: NTILDE}, {Text: "+=", Kind: PLUS_EQ}, {Text: "-=", Kind: MINUS_EQ},
	{Text: "*=", Kind: STAR_EQ}, {Text: "/=", Kind: SLASH_EQ}, {Text: "%=", Kind: PERCENT_EQ},

	{Text: "=", Kind: ASSIGN}, {Text: "<", Kind: LT}, {Text: ">", Kind: GT},
	{Text: "+", Kind: PLUS}, {Text: "-", Kind: MINUS}, {Text: "*", Kind: STAR},
	{Text: "/", Kind: SLASH}, {Text: "%", Kind: PERCENT}, {Text: "!", Kind: BANG},
	{Text: "~", Kind: TILDE}, {Text: ".", Kind: DOT}, {Text: ",", Kind: COMMA},
	{Text: "?", Kind: QUESTION}, {Text: ":", Kind: COLON},
	{Text: "(", Kind: LPAREN}, {Text: ")", Kind: RPAREN},
	{Text: "[", Kind: LBRACKET}, {Text: "]", Kind: RBRACKET},
	{Text: "{", Kind: LBRACE}, {Text: "}", Kind: RBRACE}, {Text: ";", Kind: SEMI},
}

// Poison holds the two lexemes of §3.9 that are recognised only so that the fix-its of §5.6
// are produced once, at the right column, instead of cascading out of a shorter match.
var Poison = []Operator{
	{Text: "=~", Diag: "'=~' is not an mzs operator; use '~'"},
	{Text: "=!", Diag: "unexpected '!' after '='; did you mean '!='?"},
}

// lexeme is one pre-decoded row of the scan table, so MatchOperator never re-decodes UTF-8
// in the lexer's hot loop.
type lexeme struct {
	rs []rune
	op Operator
}

// lexemes is Operators and Poison merged and sorted by descending rune width, which is what
// makes a linear scan a correct longest match across both tables. Merging is not optional:
// scanning Operators first would match ASSIGN inside "=~" and lose the diagnostic.
var lexemes = func() []lexeme {
	out := make([]lexeme, 0, len(Operators)+len(Poison))
	for _, o := range Operators {
		out = append(out, lexeme{rs: []rune(o.Text), op: o})
	}
	for _, o := range Poison {
		out = append(out, lexeme{rs: []rune(o.Text), op: o})
	}
	slices.SortStableFunc(out, func(a, b lexeme) int { return len(b.rs) - len(a.rs) })
	return out
}()

// MatchOperator returns the longest lexeme of §3.9 that is a prefix of rs, together with its
// width **in runes**. The lexer must advance by exactly n — never by len(op.Text) in bytes,
// and never by one.
//
// ok is true for poison lexemes as well; the caller must check op.IsPoison() before emitting
// a token and report op.Diag instead.
func MatchOperator(rs []rune) (op Operator, n int, ok bool) {
	if len(rs) == 0 {
		return Operator{}, 0, false
	}
	for _, l := range lexemes {
		if len(l.rs) > len(rs) {
			continue
		}
		match := true
		for j, r := range l.rs {
			if rs[j] != r {
				match = false
				break
			}
		}
		if match {
			return l.op, len(l.rs), true
		}
	}
	return Operator{}, 0, false
}

// SuppressesNewlineAfter reports whether a source line break that immediately follows a
// token of kind k is swallowed instead of becoming a NEWLINE (the continuation set of
// SPEC §3.10). The lexer also suppresses newlines inside an interpolation regardless of
// the preceding kind; that is brace-depth state, not a token predicate.
func SuppressesNewlineAfter(k Kind) bool {
	switch k {
	case ASSIGN, DECLARE, PLUS_EQ, MINUS_EQ, STAR_EQ, SLASH_EQ, PERCENT_EQ, POW_EQ,
		OR_EQ, AND_EQ, NIL_EQ,
		EQ, NEQ, TILDE, NTILDE,
		LT, LTE, GT, GTE, SPACESHIP,
		ANDAND, OROR, BANG, NILNIL,
		PLUS, MINUS, STAR, SLASH, PERCENT, POW,
		DOT, SAFEDOT, DOTDOT, DOTLT,
		ARROW, QUESTION, COLON,
		COMMA, LPAREN, LBRACKET, LBRACE,
		INTERP_BEGIN,
		KW_IF, KW_ELSE, KW_MATCH, KW_WHILE, KW_FOR, KW_IN, KW_RETURN, KW_TRY, KW_FN:
		return true
	}
	return false
}

// SuppressesNewlineBefore reports whether a pending NEWLINE is dropped because the next
// significant token is k (SPEC §3.10). This is what makes leading-dot method chains, a
// hanging `else` and multi-line `match` arms work.
func SuppressesNewlineBefore(k Kind) bool {
	switch k {
	case DOT, SAFEDOT, KW_ELSE, ARROW, RPAREN, RBRACKET, RBRACE:
		return true
	}
	return IsBinaryOp(k)
}

// AllowsRegexAfter reports whether a '/' occurring after a token of kind k starts a
// regex literal rather than a division (SPEC §3.8). Pass EOF for start-of-input.
func AllowsRegexAfter(k Kind) bool {
	switch k {
	case IDENT, GVAR, INT, FLOAT, STR_END, REGEX,
		RPAREN, RBRACKET, RBRACE,
		KW_TRUE, KW_FALSE, KW_NIL:
		return false
	}
	return true
}

// Binary operator precedence, tightest first as a *larger* number, for a precedence
// climbing parser. Levels 1, 3, 12, 13 and 14 of SPEC §5.1 (postfix trailers, unary
// operators, ternary and `try`, assignment, statement modifiers) are handled structurally
// by the grammar and are not here.
const (
	PrecNone     = 0
	PrecNilNil   = 10 // ??
	PrecOr       = 20 // ||
	PrecAnd      = 30 // &&
	PrecEquality = 40 // == != ~ !~
	PrecCompare  = 50 // < <= > >= <=>
	PrecRange    = 60 // .. ..<
	PrecAdd      = 70 // + -
	PrecMul      = 80 // * / %
	PrecPow      = 90 // **
)

// Precedence returns the binding power of k as a binary operator, or PrecNone.
func Precedence(k Kind) int {
	switch k {
	case NILNIL:
		return PrecNilNil
	case OROR:
		return PrecOr
	case ANDAND:
		return PrecAnd
	case EQ, NEQ, TILDE, NTILDE:
		return PrecEquality
	case LT, LTE, GT, GTE, SPACESHIP:
		return PrecCompare
	case DOTDOT, DOTLT:
		return PrecRange
	case PLUS, MINUS:
		return PrecAdd
	case STAR, SLASH, PERCENT:
		return PrecMul
	case POW:
		return PrecPow
	}
	return PrecNone
}

// IsBinaryOp reports whether k can join two expressions.
func IsBinaryOp(k Kind) bool { return Precedence(k) != PrecNone }

// IsUnaryOp reports whether k can prefix an expression (SPEC §5.1 level 3).
func IsUnaryOp(k Kind) bool { return k == BANG || k == MINUS || k == PLUS }

// IsRightAssoc reports whether k associates to the right. Only `**` does among the
// binary operators (SPEC §5.1 level 2).
func IsRightAssoc(k Kind) bool { return k == POW }

// IsNonAssoc reports whether chaining k is a parse error; `1..2..3` is (SPEC §5.1 level 6).
func IsNonAssoc(k Kind) bool { return k == DOTDOT || k == DOTLT }

// IsAssignOp reports whether k is `=` or one of the compound assignments (§5.1 level 13).
func IsAssignOp(k Kind) bool {
	switch k {
	case ASSIGN, DECLARE, PLUS_EQ, MINUS_EQ, STAR_EQ, SLASH_EQ, PERCENT_EQ, POW_EQ,
		OR_EQ, AND_EQ, NIL_EQ:
		return true
	}
	return false
}

// AssignBinaryOp maps a compound assignment to the binary operator it applies, so the
// evaluator's read-modify-write path has one table instead of a switch per site.
// ASSIGN, DECLARE, OR_EQ, AND_EQ and NIL_EQ short-circuit and return EOF.
func AssignBinaryOp(k Kind) Kind {
	switch k {
	case PLUS_EQ:
		return PLUS
	case MINUS_EQ:
		return MINUS
	case STAR_EQ:
		return STAR
	case SLASH_EQ:
		return SLASH
	case PERCENT_EQ:
		return PERCENT
	case POW_EQ:
		return POW
	}
	return EOF
}

// IsKeywordKind reports whether k is one of the sixteen KW_* kinds. Keywords are legal
// method names after '.' (§3.5, §4 MethodName), which is the only place the parser needs
// this.
func IsKeywordKind(k Kind) bool { return k >= KW_FN && k <= KW_EXPORT }

// IsLiteralKind reports whether k carries a value that stands alone as a primary.
func IsLiteralKind(k Kind) bool {
	switch k {
	case INT, FLOAT, REGEX, KW_TRUE, KW_FALSE, KW_NIL:
		return true
	}
	return false
}

// IsIdentStart and IsIdentPart implement §3.4. unicode.IsLetter decides letterhood, so
// Cyrillic identifiers are identifiers. There is no '?'/'!' suffix: `empty?` is
// IDENT(empty) followed by QUESTION, which is what keeps `x.empty?1:2` a ternary.
func IsIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func IsIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
