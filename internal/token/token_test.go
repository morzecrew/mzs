package token

import "testing"

// The historical bug this table exists to prevent: adding runes together ('='+'=' →
// 'z') and advancing the cursor by one for a two-rune lexeme. MatchOperator returns
// the rune width so the lexer cannot get it wrong.
func TestMatchOperatorIsLongestMatch(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		want  Kind
		width int
		ok    bool
	}{
		{"power assign beats power", "**= 2", POW_EQ, 3, true},
		{"power beats star", "** 2", POW, 2, true},
		{"star alone", "*xs", STAR, 1, true},
		{"exclusive range beats range", "..<n", DOTLT, 3, true},
		{"range beats dot", "..5", DOTDOT, 2, true},
		{"dot alone", ".lower", DOT, 1, true},
		{"or assign beats or", "||= 1", OR_EQ, 3, true},
		{"logical or", "|| b", OROR, 2, true},
		{"and assign beats and", "&&= 1", AND_EQ, 3, true},
		{"logical and", "&& b", ANDAND, 2, true},
		{"nil assign beats nil coalesce", "??= 1", NIL_EQ, 3, true},
		{"nil coalesce beats question", "?? b", NILNIL, 2, true},
		{"safe call beats question", "?.lower", SAFEDOT, 2, true},
		{"question alone", "? a : b", QUESTION, 1, true},
		{"spaceship beats lte", "<=>", SPACESHIP, 3, true},
		{"lte beats lt", "<= 3", LTE, 2, true},
		{"lt alone", "< 3", LT, 1, true},
		{"equality beats assignment", "== 1", EQ, 2, true},
		{"assignment alone", "= 1", ASSIGN, 1, true},
		{"declare beats colon", ":= 1", DECLARE, 2, true},
		{"colon alone", ": b", COLON, 1, true},
		{"lambda arrow beats minus", "-> x", ARROW, 2, true},
		{"minus alone", "-1", MINUS, 1, true},
		{"not match beats bang", "!~ /re/", NTILDE, 2, true},
		{"not equal beats bang", "!= 1", NEQ, 2, true},
		{"bang alone", "!x", BANG, 1, true},
		{"tilde is the match operator", "~ /re/", TILDE, 1, true},
		{"slash assign beats slash", "/= 2", SLASH_EQ, 2, true},
		{"slash alone", "/ 2", SLASH, 1, true},
		{"semicolon", "; x", SEMI, 1, true},
		{"identifier is not an operator", "abc", EOF, 0, false},
		{"ampersand is not an mzs operator", "&x", EOF, 0, false},
		{"pipe is not an mzs operator", "|x|", EOF, 0, false},
		{"a lookalike rune is not an operator", "→ x", EOF, 0, false},
		{"empty input", "", EOF, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, n, ok := MatchOperator([]rune(tt.src))
			if ok != tt.ok {
				t.Fatalf("MatchOperator(%q) ok = %v; want %v", tt.src, ok, tt.ok)
			}
			if !ok {
				return
			}
			if op.IsPoison() {
				t.Fatalf("MatchOperator(%q) matched the poison lexeme %q", tt.src, op.Text)
			}
			if op.Kind != tt.want {
				t.Errorf("MatchOperator(%q) kind = %s; want %s", tt.src, op.Kind, tt.want)
			}
			if n != tt.width {
				t.Errorf("MatchOperator(%q) width = %d; want %d", tt.src, n, tt.width)
			}
			if got := len([]rune(op.Kind.String())); got != n {
				t.Errorf("Kind(%s).String() is %d runes; MatchOperator reported %d", op.Kind, got, n)
			}
		})
	}
}

// §3.9: "=~" and "=!" are recognised only so that §5.6 gets one fix-it at the right column
// instead of a cascade out of the shorter ASSIGN match.
func TestMatchOperatorPoisonLexemes(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		diag  string
		width int
	}{
		{"ruby match operator", "=~ /re/", "'=~' is not an mzs operator; use '~'", 2},
		{"bang after assign", `=! "x"`, "unexpected '!' after '='; did you mean '!='?", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, n, ok := MatchOperator([]rune(tt.src))
			if !ok {
				t.Fatalf("MatchOperator(%q) did not match", tt.src)
			}
			if !op.IsPoison() {
				t.Fatalf("MatchOperator(%q) = %s; want a poison lexeme", tt.src, op.Kind)
			}
			if op.Diag != tt.diag {
				t.Errorf("MatchOperator(%q) diag = %q; want %q", tt.src, op.Diag, tt.diag)
			}
			if n != tt.width {
				t.Errorf("MatchOperator(%q) width = %d; want %d", tt.src, n, tt.width)
			}
			if op.Kind != EOF {
				t.Errorf("poison lexeme %q carries kind %s; it must never be emitted", op.Text, op.Kind)
			}
		})
	}
}

// The cursor advances by rune count, never by byte count: with a Cyrillic tail behind the
// operator, a byte-width advance lands in the middle of a rune.
func TestMatchOperatorAdvancesByRunes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		tail string
	}{
		{"safe call before cyrillic", "?.имя", "имя"},
		{"exclusive range before cyrillic", "..<длина", "длина"},
		{"tilde before a cyrillic regex", "~ /меню/", " /меню/"},
		{"comma before an emoji", ",🌲", "🌲"},
		{"poison before cyrillic", "=~ /меню/", " /меню/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := []rune(tt.src)
			_, n, ok := MatchOperator(rs)
			if !ok {
				t.Fatalf("MatchOperator(%q) did not match", tt.src)
			}
			if got := string(rs[n:]); got != tt.tail {
				t.Errorf("after advancing %d runes the rest is %q; want %q", n, got, tt.tail)
			}
		})
	}

	// Every lexeme in the table must report its own rune width, whatever follows it.
	for _, o := range append(append([]Operator{}, Operators...), Poison...) {
		op, n, ok := MatchOperator([]rune(o.Text + "щ"))
		if !ok || op.Text != o.Text {
			t.Fatalf("MatchOperator(%q) = %q, %v; want %q", o.Text+"щ", op.Text, ok, o.Text)
		}
		if want := len([]rune(o.Text)); n != want {
			t.Errorf("MatchOperator(%q) width = %d; want %d", o.Text+"щ", n, want)
		}
	}
}

// A linear scan is a correct longest match only while the merged table is ordered by
// descending rune width and holds no duplicate lexeme.
func TestOperatorTableInvariants(t *testing.T) {
	seen := make(map[string]bool, len(lexemes))
	prev := -1
	for _, l := range lexemes {
		if prev >= 0 && len(l.rs) > prev {
			t.Fatalf("lexeme %q of width %d follows a shorter lexeme (width %d)",
				string(l.rs), len(l.rs), prev)
		}
		prev = len(l.rs)
		if seen[string(l.rs)] {
			t.Errorf("duplicate lexeme %q in the table", string(l.rs))
		}
		seen[string(l.rs)] = true
		if l.op.IsPoison() {
			continue
		}
		if got := l.op.Kind.String(); got != l.op.Text {
			t.Errorf("Kind(%s).String() = %q; want the lexeme %q", l.op.Kind, got, l.op.Text)
		}
	}
	if len(lexemes) != len(Operators)+len(Poison) {
		t.Errorf("scan table has %d rows; want %d", len(lexemes), len(Operators)+len(Poison))
	}
}

// §3.5: fourteen keywords, and that is the complete list.
func TestKeywords(t *testing.T) {
	tests := []struct {
		name string
		word string
		want Kind
	}{
		{"fn", "fn", KW_FN},
		{"if", "if", KW_IF},
		{"else", "else", KW_ELSE},
		{"match", "match", KW_MATCH},
		{"while", "while", KW_WHILE},
		{"for", "for", KW_FOR},
		{"in", "in", KW_IN},
		{"break", "break", KW_BREAK},
		{"next", "next", KW_NEXT},
		{"return", "return", KW_RETURN},
		{"try", "try", KW_TRY},
		{"ensure", "ensure", KW_ENSURE},
		{"true", "true", KW_TRUE},
		{"false", "false", KW_FALSE},
		{"nil", "nil", KW_NIL},
		{"include", "include", KW_INCLUDE},
		{"export", "export", KW_EXPORT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Lookup(tt.word); got != tt.want {
				t.Errorf("Lookup(%q) = %s; want %s", tt.word, got, tt.want)
			}
			if !IsKeyword(tt.word) {
				t.Errorf("IsKeyword(%q) = false; want true", tt.word)
			}
			if !IsKeywordKind(tt.want) {
				t.Errorf("IsKeywordKind(%s) = false; want true", tt.want)
			}
			if got := tt.want.String(); got != tt.word {
				t.Errorf("Kind(%s).String() = %q; want %q", tt.want, got, tt.word)
			}
		})
	}

	if len(tests) != len(keywords) {
		t.Errorf("the keyword table has %d entries; §3.5 lists %d", len(keywords), len(tests))
	}

	copied := Keywords()
	delete(copied, "fn")
	if !IsKeyword("fn") {
		t.Errorf("Keywords() returned the live table; it must return a copy")
	}
}

// Everything Ruby reserves that §3.5 does not is an ordinary identifier; the parser turns
// each into its own §5.6 fix-it.
func TestLookup(t *testing.T) {
	tests := []struct {
		name  string
		ident string
		want  Kind
	}{
		{"plain identifier", "foo_bar", IDENT},
		{"cyrillic identifier", "да", IDENT},
		{"it is an ordinary identifier", "it", IDENT},
		{"underscore is an ordinary identifier", "_", IDENT},
		{"case means nothing", "JSON", IDENT},
		{"cyrillic capital means nothing", "Стрижка", IDENT},
		{"def is not a keyword", "def", IDENT},
		{"do is not a keyword", "do", IDENT},
		{"end is not a keyword", "end", IDENT},
		{"elsif is not a keyword", "elsif", IDENT},
		{"unless is not a keyword", "unless", IDENT},
		{"until is not a keyword", "until", IDENT},
		{"loop is not a keyword", "loop", IDENT},
		{"and is not a keyword", "and", IDENT},
		{"or is not a keyword", "or", IDENT},
		{"not is not a keyword", "not", IDENT},
		{"then is not a keyword", "then", IDENT},
		{"rescue is not a keyword", "rescue", IDENT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Lookup(tt.ident); got != tt.want {
				t.Errorf("Lookup(%q) = %s; want %s", tt.ident, got, tt.want)
			}
		})
	}
}

// §3.8: '/' starts a regex everywhere except directly after something that ends an
// expression.
func TestAllowsRegexAfter(t *testing.T) {
	tests := []struct {
		name string
		prev Kind
		want bool
	}{
		{"start of input", EOF, true},
		{"after the match operator", TILDE, true},
		{"after the negated match operator", NTILDE, true},
		{"after equality", EQ, true},
		{"after an open paren", LPAREN, true},
		{"after a comma", COMMA, true},
		{"after an arrow", ARROW, true},
		{"after an open brace", LBRACE, true},
		{"after a newline", NEWLINE, true},
		{"after a semicolon", SEMI, true},
		{"after a keyword", KW_RETURN, true},
		{"after match", KW_MATCH, true},
		{"after an identifier is division", IDENT, false},
		{"after a global is division", GVAR, false},
		{"after an int is division", INT, false},
		{"after a float is division", FLOAT, false},
		{"after a regex is division", REGEX, false},
		{"after a close paren is division", RPAREN, false},
		{"after a close bracket is division", RBRACKET, false},
		{"after a close brace is division", RBRACE, false},
		{"after a string end is division", STR_END, false},
		{"after true is division", KW_TRUE, false},
		{"after false is division", KW_FALSE, false},
		{"after nil is division", KW_NIL, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsRegexAfter(tt.prev); got != tt.want {
				t.Errorf("AllowsRegexAfter(%s) = %v; want %v", tt.prev, got, tt.want)
			}
		})
	}
}

// §3.10: a newline is swallowed after a continuation token and before a leading dot, a
// hanging else, an arrow or a closing bracket.
func TestNewlineSuppression(t *testing.T) {
	tests := []struct {
		name       string
		k          Kind
		wantAfter  bool
		wantBefore bool
	}{
		{"dot", DOT, true, true},
		{"safe call", SAFEDOT, true, true},
		{"comma", COMMA, true, false},
		{"open paren", LPAREN, true, false},
		{"close paren", RPAREN, false, true},
		{"open bracket", LBRACKET, true, false},
		{"close bracket", RBRACKET, false, true},
		{"open brace", LBRACE, true, false},
		{"close brace", RBRACE, false, true},
		{"arrow", ARROW, true, true},
		{"question", QUESTION, true, false},
		{"colon", COLON, true, false},
		{"assignment", ASSIGN, true, false},
		{"nil assign", NIL_EQ, true, false},
		{"plus", PLUS, true, true},
		{"logical or", OROR, true, true},
		{"nil coalesce", NILNIL, true, true},
		{"match operator", TILDE, true, true},
		{"exclusive range", DOTLT, true, true},
		{"bang is unary only", BANG, true, false},
		{"interpolation start", INTERP_BEGIN, true, false},
		{"if", KW_IF, true, false},
		{"match", KW_MATCH, true, false},
		{"try", KW_TRY, true, false},
		{"else", KW_ELSE, true, true},
		{"break is not a continuation", KW_BREAK, false, false},
		{"identifier", IDENT, false, false},
		{"int", INT, false, false},
		{"semicolon is never suppressed", SEMI, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SuppressesNewlineAfter(tt.k); got != tt.wantAfter {
				t.Errorf("SuppressesNewlineAfter(%s) = %v; want %v", tt.k, got, tt.wantAfter)
			}
			if got := SuppressesNewlineBefore(tt.k); got != tt.wantBefore {
				t.Errorf("SuppressesNewlineBefore(%s) = %v; want %v", tt.k, got, tt.wantBefore)
			}
		})
	}
}

// §5.1: the precedence table, tightest first.
func TestPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		a, b  Kind
		tight bool // a binds tighter than b
	}{
		{"pow over mul", POW, STAR, true},
		{"mul over add", STAR, PLUS, true},
		{"add over range", PLUS, DOTDOT, true},
		{"range over in", DOTDOT, KW_IN, true},
		{"in over compare", KW_IN, LT, true},
		{"range over compare", DOTLT, LT, true},
		{"compare over equality", SPACESHIP, EQ, true},
		{"equality over and", TILDE, ANDAND, true},
		{"and over or", ANDAND, OROR, true},
		{"or over nil coalesce", OROR, NILNIL, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Precedence(tt.a) > Precedence(tt.b); got != tt.tight {
				t.Errorf("Precedence(%s)=%d vs Precedence(%s)=%d; want tighter = %v",
					tt.a, Precedence(tt.a), tt.b, Precedence(tt.b), tt.tight)
			}
		})
	}

	if !IsRightAssoc(POW) {
		t.Errorf("** must associate to the right")
	}
	if IsRightAssoc(PLUS) {
		t.Errorf("+ must associate to the left")
	}
	if !IsNonAssoc(DOTDOT) || !IsNonAssoc(DOTLT) {
		t.Errorf("ranges must be non-associative")
	}
	// `in` is non-associative in §5.1 but not through this predicate: it is read after a
	// range's right operand, where `1..5 in xs` is the ordinary reading.
	if IsNonAssoc(KW_IN) {
		t.Errorf("IsNonAssoc must not claim 'in': it would break 1..5 in xs")
	}
	// A line that starts with `in` is a match arm, so the newline in front of it stands
	// even though `in` is a binary operator (§3.10, §5.3).
	if !IsBinaryOp(KW_IN) {
		t.Errorf("'in' must be a binary operator")
	}
	if SuppressesNewlineBefore(KW_IN) {
		t.Errorf("a newline before 'in' must stand: the next line may be a match arm")
	}
	if !SuppressesNewlineAfter(KW_IN) {
		t.Errorf("a newline after 'in' must be swallowed: the operand follows")
	}
	if Precedence(ASSIGN) != PrecNone {
		t.Errorf("assignment is not a binary operator level")
	}
	if Precedence(QUESTION) != PrecNone {
		t.Errorf("the ternary is not a binary operator level")
	}
	if !IsUnaryOp(BANG) || !IsUnaryOp(MINUS) || !IsUnaryOp(PLUS) {
		t.Errorf("! - + are the unary operators of level 3")
	}
	if IsUnaryOp(TILDE) {
		t.Errorf("~ is binary only")
	}
}

func TestAssignBinaryOp(t *testing.T) {
	tests := []struct {
		name string
		k    Kind
		want Kind
	}{
		{"plus", PLUS_EQ, PLUS},
		{"minus", MINUS_EQ, MINUS},
		{"star", STAR_EQ, STAR},
		{"slash", SLASH_EQ, SLASH},
		{"percent", PERCENT_EQ, PERCENT},
		{"pow", POW_EQ, POW},
		{"plain assign has none", ASSIGN, EOF},
		{"declare has none", DECLARE, EOF},
		{"or assign is short circuit", OR_EQ, EOF},
		{"and assign is short circuit", AND_EQ, EOF},
		{"nil assign is short circuit", NIL_EQ, EOF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AssignBinaryOp(tt.k); got != tt.want {
				t.Errorf("AssignBinaryOp(%s) = %s; want %s", tt.k, got, tt.want)
			}
			if !IsAssignOp(tt.k) {
				t.Errorf("IsAssignOp(%s) = false; want true", tt.k)
			}
		})
	}
}

func TestIdentRunes(t *testing.T) {
	tests := []struct {
		name      string
		r         rune
		wantStart bool
		wantPart  bool
	}{
		{"latin letter", 'a', true, true},
		{"cyrillic letter", 'д', true, true},
		{"underscore", '_', true, true},
		{"digit", '5', false, true},
		{"question mark", '?', false, false},
		{"bang", '!', false, false},
		{"dollar", '$', false, false},
		{"at sign", '@', false, false},
		{"emoji", '🌲', false, false},
		{"nbsp", ' ', false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsIdentStart(tt.r); got != tt.wantStart {
				t.Errorf("IsIdentStart(%q) = %v; want %v", tt.r, got, tt.wantStart)
			}
			if got := IsIdentPart(tt.r); got != tt.wantPart {
				t.Errorf("IsIdentPart(%q) = %v; want %v", tt.r, got, tt.wantPart)
			}
		})
	}
}

func TestTokenString(t *testing.T) {
	tests := []struct {
		name string
		tok  Token
		want string
	}{
		{"identifier carries its text", Token{Kind: IDENT, Value: "имя"}, "IDENT(имя)"},
		{"global keeps the sigil", Token{Kind: GVAR, Value: "$__sent"}, "GVAR($__sent)"},
		{"string global", Token{Kind: STR_GVAR, Value: "$price"}, "STR_GVAR($price)"},
		{"regex", Token{Kind: REGEX, Value: "меню", Flags: "i"}, "REGEX(меню)"},
		{"operator prints its lexeme", Token{Kind: SAFEDOT}, "?."},
		{"keyword prints itself", Token{Kind: KW_MATCH}, "match"},
		{"eof", Token{Kind: EOF}, "EOF"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.String(); got != tt.want {
				t.Errorf("Token.String() = %q; want %q", got, tt.want)
			}
		})
	}

	if (Pos{}).IsValid() {
		t.Errorf("the zero Pos must not be valid")
	}
	if !(Pos{Offset: 3, Line: 1, Col: 4}).IsValid() {
		t.Errorf("a 1-based line must be valid")
	}
}

func TestIsLiteralKind(t *testing.T) {
	tests := []struct {
		name string
		k    Kind
		want bool
	}{
		{"int", INT, true},
		{"float", FLOAT, true},
		{"regex", REGEX, true},
		{"true", KW_TRUE, true},
		{"false", KW_FALSE, true},
		{"nil", KW_NIL, true},
		{"identifier", IDENT, false},
		{"string begin needs a parser", STR_BEGIN, false},
		{"match is a control keyword", KW_MATCH, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLiteralKind(tt.k); got != tt.want {
				t.Errorf("IsLiteralKind(%s) = %v; want %v", tt.k, got, tt.want)
			}
		})
	}
}
