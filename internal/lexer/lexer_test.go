package lexer

import (
	"strings"
	"testing"

	"mzs/internal/token"
)

// dump renders the stream the way a golden test can read it: the lexeme alone for
// operators and keywords, kind(value) for the tokens that carry text. The trailing EOF
// is dropped because every stream has one.
func dump(toks []token.Token) string {
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		if t.Kind == token.EOF {
			break
		}
		switch t.Kind {
		case token.IDENT, token.GVAR, token.INT, token.FLOAT, token.STR_TEXT, token.STR_GVAR:
			parts = append(parts, t.Kind.String()+"("+t.Value+")")
		case token.REGEX:
			parts = append(parts, "REGEX(/"+t.Value+"/"+t.Flags+")")
		default:
			parts = append(parts, t.Kind.String())
		}
	}
	return strings.Join(parts, " ")
}

// TestLexTokens is the golden token stream for the lexical rules SPEC §3 pins down.
func TestLexTokens(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"empty input", "", ""},
		{"assignment", "x = 1", "IDENT(x) = INT(1)"},
		{"declaration", "x := 1", "IDENT(x) := INT(1)"},

		// §3.2 comments.
		{"comment to end of line", "x = 1 # trailing", "IDENT(x) = INT(1)"},
		{"comment only", "# nothing here", ""},
		{"a comment swallows everything", `x # " $y /re`, "IDENT(x)"},
		{"a hash inside a string is text", `"a # b"`, "STR_BEGIN STR_TEXT(a # b) STR_END"},
		// §3.2 says '//' is not a comment; §3.8 then makes the second '/' the closing
		// delimiter of an empty regex, because the first one opened a literal.
		{"slash slash is not a comment", "// nope", "REGEX(//) IDENT(nope)"},

		// §3.4 identifiers.
		{"cyrillic identifier", "да == 1", "IDENT(да) == INT(1)"},
		{"case means nothing", "json.parse", "IDENT(json) . IDENT(parse)"},
		{"an upper-case name is still an identifier", "JSON.parse", "IDENT(JSON) . IDENT(parse)"},
		{"it is an identifier", "{ it > 5 }", "{ IDENT(it) > INT(5) }"},
		{"underscore is an identifier", "{ (_, v) -> v }", "{ ( IDENT(_) , IDENT(v) ) -> IDENT(v) }"},
		{"no question suffix", "x.empty?", "IDENT(x) . IDENT(empty) ?"},
		{"no bang suffix", "s.strip!", "IDENT(s) . IDENT(strip) !"},
		{"whitespace cannot change a ternary", "x.empty?1:2", "IDENT(x) . IDENT(empty) ? INT(1) : INT(2)"},
		{"not-equal is one operator", "a!=b", "IDENT(a) != IDENT(b)"},
		{"global keeps its sigil", "$__sent", "GVAR($__sent)"},
		{"a global may be all digits", "$1", "GVAR($1)"},
		{"keyword after a dot", "x.if", "IDENT(x) . if"},

		// §3.5 keywords, and the words that are not.
		{
			"the fourteen keywords",
			"fn if else match while for in break next return try true false nil",
			"fn if else match while for in break next return try true false nil",
		},
		{
			"ruby words are identifiers",
			"def end elsif unless until loop and or not do rescue then",
			"IDENT(def) IDENT(end) IDENT(elsif) IDENT(unless) IDENT(until) IDENT(loop) " +
				"IDENT(and) IDENT(or) IDENT(not) IDENT(do) IDENT(rescue) IDENT(then)",
		},

		// §3.6 numbers.
		{"int method call is not a float", "1.str", "INT(1) . IDENT(str)"},
		{"float", "1.2", "FLOAT(1.2)"},
		{"range beats float", "1..5", "INT(1) .. INT(5)"},
		{"exclusive range beats float", "1..<5", "INT(1) ..< INT(5)"},
		{"underscores are separators", "1_000_000", "INT(1000000)"},
		{"hexadecimal", "0xff", "INT(255)"},
		{"binary", "0b1010", "INT(10)"},
		{"octal", "0o17", "INT(15)"},
		{"exponent", "1.5e-3", "FLOAT(1.5e-3)"},
		{"exponent without a point", "1e9", "FLOAT(1e9)"},

		// §3.7 strings.
		{"single quoted is raw", `'a\nb'`, `STR_BEGIN STR_TEXT(a\nb) STR_END`},
		{"single quoted escapes", `'it\'s'`, "STR_BEGIN STR_TEXT(it's) STR_END"},
		{"single quoted does not interpolate", `'$x ${y}'`, "STR_BEGIN STR_TEXT($x ${y}) STR_END"},
		{"empty string has no text token", `""`, "STR_BEGIN STR_END"},
		{"double quoted escapes", `"a\tb"`, "STR_BEGIN STR_TEXT(a\tb) STR_END"},
		{
			"the golden stream of 3.7",
			`"a${x+1}b$c"`,
			"STR_BEGIN STR_TEXT(a) INTERP_BEGIN IDENT(x) + INT(1) INTERP_END STR_TEXT(b) STR_GVAR($c) STR_END",
		},
		{"a global inside a string", `"Ваш адрес $__sent?"`, "STR_BEGIN STR_TEXT(Ваш адрес ) STR_GVAR($__sent) STR_TEXT(?) STR_END"},
		{"the name takes the longest run", `"$n00"`, "STR_BEGIN STR_GVAR($n00) STR_END"},
		{"braces end the name early", `"${n}00"`, "STR_BEGIN INTERP_BEGIN IDENT(n) INTERP_END STR_TEXT(00) STR_END"},
		{"a dollar before a digit is text", `"$1"`, "STR_BEGIN STR_TEXT($1) STR_END"},
		{"a trailing dollar is text", `"цена: 100 $"`, "STR_BEGIN STR_TEXT(цена: 100 $) STR_END"},
		{"an escaped dollar is text", `"скидка 10\$"`, "STR_BEGIN STR_TEXT(скидка 10$) STR_END"},
		{"a hash brace is text", `"#{x}"`, "STR_BEGIN STR_TEXT(#{x}) STR_END"},
		{
			"an expression interpolation",
			`"Итоговая цена: ${$price.int + 1200}"`,
			"STR_BEGIN STR_TEXT(Итоговая цена: ) INTERP_BEGIN GVAR($price) . IDENT(int) + INT(1200) INTERP_END STR_END",
		},
		{"braces inside interpolation", `"${ {(x) -> x} }"`, "STR_BEGIN INTERP_BEGIN { ( IDENT(x) ) -> IDENT(x) } INTERP_END STR_END"},
		{"nested string in interpolation", `"${"x"}"`, "STR_BEGIN INTERP_BEGIN STR_BEGIN STR_TEXT(x) STR_END INTERP_END STR_END"},
		{"emoji in a string", `"RU 🇷🇺"`, "STR_BEGIN STR_TEXT(RU 🇷🇺) STR_END"},

		// §3.8 regex versus division.
		{"regex after the match operator", "s ~ /re/", "IDENT(s) ~ REGEX(/re/)"},
		{"regex after the negated match", "s !~ /re/i", "IDENT(s) !~ REGEX(/re/i)"},
		{"regex after equality", "s == /re/", "IDENT(s) == REGEX(/re/)"},
		{"regex in parentheses", "(/re/)", "( REGEX(/re/) )"},
		{"regex as an argument", "f(/re/)", "IDENT(f) ( REGEX(/re/) )"},
		{"regex after assignment", "x = /re/", "IDENT(x) = REGEX(/re/)"},
		{"regex after an arrow", "{ (x) -> /re/ }", "{ ( IDENT(x) ) -> REGEX(/re/) }"},
		{"regex in a match arm", "match s { /re/ -> 1 }", "match IDENT(s) { REGEX(/re/) -> INT(1) }"},
		{"regex at the start of input", "/re/", "REGEX(/re/)"},
		{"regex after a comma", "f(1, /re/)", "IDENT(f) ( INT(1) , REGEX(/re/) )"},
		{"division after an identifier", "a / b", "IDENT(a) / IDENT(b)"},
		{"division after a call", "f(1) / 2", "IDENT(f) ( INT(1) ) / INT(2)"},
		{"division after an index", "xs[0] / 2", "IDENT(xs) [ INT(0) ] / INT(2)"},
		{"division after a closure", "{ 1 } / 2", "{ INT(1) } / INT(2)"},
		{"division after a string", `"x" / 2`, "STR_BEGIN STR_TEXT(x) STR_END / INT(2)"},
		{"division after nil", "nil / 2", "nil / INT(2)"},
		{"division after a regex", "/a/ / 2", "REGEX(/a/) / INT(2)"},
		{"escaped delimiter in a regex", `/a\/b/`, "REGEX(/a/b/)"},
		{"slash inside a character class", `/[/]/`, "REGEX(/[/]/)"},
		{"alternation with cyrillic", `/^ага|конечно/`, "REGEX(/^ага|конечно/)"},
		{"all the flags", "/re/imxsu", "REGEX(/re/imxsu)"},

		// §3.9 operator lexemes, longest match first.
		{"exclusive range operator", "a ..< b", "IDENT(a) ..< IDENT(b)"},
		{"safe call", "a?.b", "IDENT(a) ?. IDENT(b)"},
		{"nil coalescing", "a ?? b", "IDENT(a) ?? IDENT(b)"},
		{"nil coalescing assignment", "a ??= b", "IDENT(a) ??= IDENT(b)"},
		{"match operator", "a ~ b", "IDENT(a) ~ IDENT(b)"},
		{"negated match operator", "a !~ b", "IDENT(a) !~ IDENT(b)"},
		{"spaceship", "a <=> b", "IDENT(a) <=> IDENT(b)"},
		{"power assignment", "a **= 2", "IDENT(a) **= INT(2)"},
		{"or assignment", "a ||= b", "IDENT(a) ||= IDENT(b)"},
		{"and assignment", "a &&= b", "IDENT(a) &&= IDENT(b)"},
		{"arrow", "-> x", "-> IDENT(x)"},
		{"three dots are a range and a dot", "1...5", "INT(1) .. . INT(5)"},
		{"double colon is two colons", "a::b", "IDENT(a) : : IDENT(b)"},
		{"fat arrow is assign and greater", "x => 1", "IDENT(x) = > INT(1)"},
		{"percent w is modulo", "%w[a b]", "% IDENT(w) [ IDENT(a) IDENT(b) ]"},
		{"a colon name is a colon and a name", ":name", ": IDENT(name)"},
		{"dict literal", "{a: 1, b: 2}", "{ IDENT(a) : INT(1) , IDENT(b) : INT(2) }"},
		{"empty dict", "{}", "{ }"},
		{"ternary", "x ? y : z", "IDENT(x) ? IDENT(y) : IDENT(z)"},

		// §3.10 termination and continuation.
		{"semicolon separates", "a; b", "IDENT(a) ; IDENT(b)"},
		{"newline separates", "a\nb", "IDENT(a) NEWLINE IDENT(b)"},
		{"blank lines collapse", "a\n\n\nb", "IDENT(a) NEWLINE IDENT(b)"},
		{"a leading blank line is not a statement", "\n\na", "IDENT(a)"},
		{"a trailing newline is not a statement", "a\n", "IDENT(a)"},
		{"carriage return newline", "a\r\nb", "IDENT(a) NEWLINE IDENT(b)"},
		{"newline after an operator is a continuation", "a +\nb", "IDENT(a) + IDENT(b)"},
		{"newline after a comma is a continuation", "f(1,\n2)", "IDENT(f) ( INT(1) , INT(2) )"},
		{"newline after a keyword is a continuation", "return\n1", "return INT(1)"},
		{"newline after an arrow is a continuation", "{ (x) ->\nx }", "{ ( IDENT(x) ) -> IDENT(x) }"},
		{"leading dot joins the chain", "a\n  .b", "IDENT(a) . IDENT(b)"},
		{"leading safe call joins the chain", "a\n  ?.b", "IDENT(a) ?. IDENT(b)"},
		{"leading binary operator joins", "a\n== b", "IDENT(a) == IDENT(b)"},
		{"hanging else joins", "if c { a }\nelse { b }", "if IDENT(c) { IDENT(a) } else { IDENT(b) }"},
		{"leading arrow joins", "match s {\n  1\n  -> 2 }", "match IDENT(s) { INT(1) -> INT(2) }"},
		{"closing bracket joins", "[1,\n2\n]", "[ INT(1) , INT(2) ]"},
		{"newline inside interpolation is suppressed", "\"${a\n+ 1}\"", "STR_BEGIN INTERP_BEGIN IDENT(a) + INT(1) INTERP_END STR_END"},
		{"a chain over three lines", "$__sent\n  .lower\n  .trim == \"да\"", `GVAR($__sent) . IDENT(lower) . IDENT(trim) == STR_BEGIN STR_TEXT(да) STR_END`},

		{"nbsp is whitespace", "a\u00a0=\u00a01", "IDENT(a) = INT(1)"},
		{"a byte order mark is skipped", "\ufeffx = 1", "IDENT(x) = INT(1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex("t", tt.src)
			if len(errs) != 0 {
				t.Fatalf("Lex(%q) errors = %v; want none", tt.src, errs)
			}
			if got := dump(toks); got != tt.want {
				t.Errorf("Lex(%q) =\n  %s\nwant\n  %s", tt.src, got, tt.want)
			}
			if last := toks[len(toks)-1]; last.Kind != token.EOF {
				t.Errorf("Lex(%q) last token = %s; want EOF", tt.src, last.Kind)
			}
		})
	}
}

// TestLexTernarySpacing pins the §3.4 promise that dropping the spaces around a ternary
// cannot change how it is read, which is the whole reason identifiers have no suffix.
func TestLexTernarySpacing(t *testing.T) {
	spaced, _ := Lex("t", "x.empty ? 1 : 2")
	tight, _ := Lex("t", "x.empty?1:2")
	if a, b := dump(spaced), dump(tight); a != b {
		t.Errorf("spacing changed the stream:\n  %s\n  %s", a, b)
	}
}

// TestLexPositions checks that Col counts runes, which is the only way a caret lands
// under the right character in a Cyrillic or emoji one-liner.
func TestLexPositions(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		index int // index into the token stream
		kind  token.Kind
		line  int
		col   int
	}{
		{"first token", "x = 1", 0, token.IDENT, 1, 1},
		{"operator column", "x = 1", 1, token.ASSIGN, 1, 3},
		{"after cyrillic", "привет = 1", 1, token.ASSIGN, 1, 8},
		{"after emoji", `"🌲" == 1`, 3, token.EQ, 1, 5},
		{"second line", "a\nbb = 2", 2, token.IDENT, 2, 1},
		{"third line after two breaks", "a\n\nb", 2, token.IDENT, 3, 1},
		{"a global inside a string", `"a $b"`, 2, token.STR_GVAR, 1, 4},
		{"an interpolation inside a string", `"a${x}"`, 2, token.INTERP_BEGIN, 1, 3},
		{"the token after an interpolation", `"a${x}b"`, 5, token.STR_TEXT, 1, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, _ := Lex("t", tt.src)
			if tt.index >= len(toks) {
				t.Fatalf("Lex(%q) produced %d tokens; wanted index %d (%s)", tt.src, len(toks), tt.index, dump(toks))
			}
			got := toks[tt.index]
			if got.Kind != tt.kind {
				t.Fatalf("Lex(%q)[%d] kind = %s; want %s (%s)", tt.src, tt.index, got.Kind, tt.kind, dump(toks))
			}
			if got.Pos.Line != tt.line || got.Pos.Col != tt.col {
				t.Errorf("Lex(%q)[%d] position = %d:%d; want %d:%d", tt.src, tt.index, got.Pos.Line, got.Pos.Col, tt.line, tt.col)
			}
		})
	}
}

// TestLexPoison covers the two lexemes of §3.9 that exist only to produce a §5.6 fix-it:
// each is reported once, at its own column, and emits no token.
func TestLexPoison(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
		col  int
		want string
	}{
		{
			"the ruby match operator",
			"s =~ /re/",
			"'=~' is not an mzs operator; use '~'",
			3,
			"IDENT(s) REGEX(/re/)",
		},
		{
			"the one.mzs typo",
			`str =! "x"`,
			"unexpected '!' after '='; did you mean '!='?",
			5,
			`IDENT(str) STR_BEGIN STR_TEXT(x) STR_END`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex("t", tt.src)
			if len(errs) != 1 {
				t.Fatalf("Lex(%q) reported %d errors %v; want 1", tt.src, len(errs), errs)
			}
			if errs[0].Msg != tt.msg {
				t.Errorf("Lex(%q) message = %q; want %q", tt.src, errs[0].Msg, tt.msg)
			}
			if errs[0].Pos.Line != 1 || errs[0].Pos.Col != tt.col {
				t.Errorf("Lex(%q) position = %d:%d; want 1:%d", tt.src, errs[0].Pos.Line, errs[0].Pos.Col, tt.col)
			}
			if got := dump(toks); got != tt.want {
				t.Errorf("Lex(%q) =\n  %s\nwant\n  %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestLexErrors pins the recovery rule of §3.3: one diagnostic per bad rune, scanning
// continues, and the stream still terminates.
func TestLexErrors(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		count int
		msg   string
		line  int
		col   int
	}{
		{"instance variable is reserved", "@x", 1, "'@' is reserved; instance variables do not exist in v2.0", 1, 1},
		{"bare dollar", "$ = 1", 1, "'$' must be followed by a variable name", 1, 1},
		{"unterminated double quote", `"abc`, 1, "unterminated string literal", 1, 1},
		{"unterminated single quote", `'abc`, 1, "unterminated string literal", 1, 1},
		{"unterminated interpolation", `"a${x`, 1, "unterminated string literal", 1, 1},
		{"unterminated regex", "x ~ /abc", 1, "unterminated regex literal", 1, 5},
		{"newline in a regex", "x ~ /a\nb/", 1, "newline in regex literal; write \\n instead", 1, 5},
		{"integer overflow", "99999999999999999999", 1, "integer literal \"99999999999999999999\" does not fit in an int", 1, 1},
		{"the ruby safe call", "x &. y", 1, "'&.' is not an mzs operator; use '?.'", 1, 3},
		{"ruby closure parameters", "{ |x| x }", 2, "closure parameters are parenthesised: { (x) -> … }", 1, 3},
		{"bitwise and", "a & b", 1, "'&' is not an mzs operator; use band(a, b), or '&&' for logical and", 1, 3},
		{"bitwise or", "a | b", 1, "'|' is not an mzs operator; use bor(a, b), or '||' for logical or", 1, 3},
		{"bitwise xor", "a ^ b", 1, "'^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power", 1, 3},
		// §3.2 promises '//' is not a comment; under §3.8 the first '/' here is a
		// division and the second opens a regex that never closes.
		{"slash slash after a value", "a // b", 1, "unterminated regex literal", 1, 4},
		{"one diagnostic per bad rune", "a \x01 b \x02 c", 2, "unexpected character \"\\x01\"", 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex("t", tt.src)
			if len(errs) != tt.count {
				t.Fatalf("Lex(%q) reported %d errors %v; want %d", tt.src, len(errs), errs, tt.count)
			}
			if errs[0].Msg != tt.msg {
				t.Errorf("Lex(%q) first message = %q; want %q", tt.src, errs[0].Msg, tt.msg)
			}
			if errs[0].Pos.Line != tt.line || errs[0].Pos.Col != tt.col {
				t.Errorf("Lex(%q) first position = %d:%d; want %d:%d", tt.src, errs[0].Pos.Line, errs[0].Pos.Col, tt.line, tt.col)
			}
			if last := toks[len(toks)-1]; last.Kind != token.EOF {
				t.Errorf("Lex(%q) last token = %s; want EOF even after an error", tt.src, last.Kind)
			}
		})
	}
}

// TestLexDecodesEscapes covers the escape table of §3.7, including the Unicode forms
// the corpus needs.
func TestLexDecodesEscapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"newline", `"a\nb"`, "a\nb"},
		{"tab", `"a\tb"`, "a\tb"},
		{"escaped quote", `"a\"b"`, `a"b`},
		{"escaped backslash", `"a\\b"`, `a\b`},
		{"escaped dollar", `"a\$b"`, "a$b"},
		{"an escaped dollar before a brace", `"a\${b}"`, "a${b}"},
		{"unknown escape is literal", `"a\qb"`, "aqb"},
		{"four digit unicode", `"\u0416"`, "Ж"},
		{"braced unicode", `"\u{1F600}"`, "😀"},
		{"escape byte", `"\x41"`, "A"},
		{"raw newline is kept", "\"a\nb\"", "a\nb"},
		{"single quoted keeps its backslashes", `'\bменю'`, `\bменю`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, errs := Lex("t", tt.src)
			if len(errs) != 0 {
				t.Fatalf("Lex(%q) errors = %v; want none", tt.src, errs)
			}
			var got string
			for _, tok := range toks {
				if tok.Kind == token.STR_TEXT {
					got += tok.Value
				}
			}
			if got != tt.want {
				t.Errorf("Lex(%q) decoded %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestLexTerminates asserts the two properties every caller relies on: Next always
// makes progress, and the stream after EOF stays EOF.
func TestLexTerminates(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"binary garbage", "\x00\x01\x02\xff\xfe"},
		{"unclosed everything", `"a${ [ ( {`},
		{"lone sigils", "@ $ % & | ^ ~"},
		{"nested interpolation", `"${"${"${x}"}"}"`},
		{"long operator run", strings.Repeat("=", 200)},
		{"cyrillic and emoji", "привет 🌲 да"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New("t", tt.src)
			for i := 0; ; i++ {
				if i > 10*len(tt.src)+100 {
					t.Fatalf("Lex(%q) did not reach EOF after %d tokens", tt.src, i)
				}
				if l.Next().Kind == token.EOF {
					break
				}
			}
			for i := 0; i < 3; i++ {
				if k := l.Next().Kind; k != token.EOF {
					t.Fatalf("Lex(%q) returned %s after EOF; want EOF forever", tt.src, k)
				}
			}
		})
	}
}

// FuzzLex asserts A7 for the scanner: no input panics, no rune is dropped silently,
// and the stream always terminates with EOF.
func FuzzLex(f *testing.F) {
	seeds := []string{
		"", " ", "\n", ";", "#", "//", "@", "$", "/re", "~", "!~", "??=", "..<",
		`s := $__sent.lower.trim; s == "да" || s ~ /^ага|конечно/`,
		`"a${b}c"`, `"$a$b"`, `"$"`, `"\$"`, `'raw\n'`, "1.2.3", "0x", "1e", "1_",
		"{a: 1}", "x ? y : z", "match x { 1 -> 2 }", "привет", "🌲", "\u00a0",
		"\ufeffx = 1", "\"\\u{110000}\"", "\"\\x\"", strings.Repeat("(", 100),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		toks, errs := Lex("fuzz", src)
		if len(toks) == 0 || toks[len(toks)-1].Kind != token.EOF {
			t.Fatalf("Lex(%q) did not end with EOF", src)
		}
		for _, e := range errs {
			if !e.Pos.IsValid() {
				t.Fatalf("Lex(%q) produced a diagnostic without a position: %v", src, e)
			}
		}
		for _, tok := range toks {
			if !tok.Pos.IsValid() {
				t.Fatalf("Lex(%q) produced %s without a position", src, tok.Kind)
			}
		}
	})
}
