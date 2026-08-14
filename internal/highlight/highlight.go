// Package highlight paints mzs source for a terminal. It answers one question — what
// colour is each rune of this line — and it answers it with the lexer of §3 rather than
// with a private set of patterns, so the REPL colours a line by the same rules that will
// compile it. A '#' inside a string is text and stays green; a `/…/` after a value is
// division and stays plain; a `}` that closes a `${` is not a block brace. None of that
// is guessed here, because none of it is guessed there.
//
// Colours are SGR parameters, not escape sequences: the caller decides when to emit them
// and where to reset. The palette is the sixteen ANSI colours, which every terminal and
// every theme has an opinion about and all of them a readable one.
package highlight

import (
	"mzs/internal/lexer"
	"mzs/internal/token"
)

// The palette, as SGR parameters. "" means the terminal's own foreground colour, which
// is what identifiers and operators are drawn in: colouring everything is colouring
// nothing, and a line where only the literals and the names light up is easier to read
// than a rainbow.
const (
	Comment  = "90"   // grey: the one thing that is not code
	Keyword  = "35"   // magenta
	Number   = "33"   // yellow
	String   = "32"   // green
	Regex    = "36"   // cyan
	Global   = "1;34" // bold blue: $vars, and the ${…} that carries one into a string
	Call     = "34"   // blue: the name being called, and every link of a method chain
	Mismatch = "1;31" // bold red: a bracket that closes nothing
)

// Colors returns one SGR parameter per rune of src. The result always has exactly
// len(src) entries, whatever src contains: this runs on every keystroke over a line that
// is usually half-typed, so a lexical error is an ordinary outcome and never an excuse
// to return something the caller cannot index.
func Colors(src []rune) []string {
	out := make([]string, len(src))
	if len(src) == 0 {
		return out
	}
	covered := make([]bool, len(src))

	// New drops a byte-order mark before it starts counting, so every offset it reports
	// is one short of the caller's index when the line begins with one.
	shift := 0
	if src[0] == '\ufeff' {
		shift = 1
	}
	// The bounds are clamped rather than trusted. Nothing the lexer reports today falls
	// outside the source it was handed, and a REPL that panicked on a keystroke because
	// one day something did would be a poor trade for two saved comparisons.
	paint := func(from, to int, c string) {
		for i := max(from, 0); i < min(to, len(src)); i++ {
			covered[i] = true
			if c != "" {
				out[i] = c
			}
		}
	}

	toks, _ := lexer.Lex("repl", string(src))
	for i, t := range toks {
		paint(t.Pos.Offset+shift, t.End.Offset+shift, colorOf(toks, i))
	}
	comments(src, covered, out)
	mismatches(toks, shift, paint)
	return out
}

// colorOf is the colour of one token, with the two lookarounds that make a line of mzs
// readable at a glance: an identifier in front of a '(' is a call, and one behind a '.'
// is a method — `s.lower.trim` is three names doing something, not three words.
func colorOf(toks []token.Token, i int) string {
	t := toks[i]
	switch t.Kind {
	case token.INT, token.FLOAT:
		return Number
	case token.STR_BEGIN, token.STR_TEXT, token.STR_END:
		return String
	case token.STR_GVAR, token.GVAR, token.INTERP_BEGIN, token.INTERP_END:
		return Global
	case token.REGEX:
		return Regex
	case token.IDENT:
		if i+1 < len(toks) && toks[i+1].Kind == token.LPAREN {
			return Call
		}
		if i > 0 && (toks[i-1].Kind == token.DOT || toks[i-1].Kind == token.SAFEDOT) {
			return Call
		}
		return ""
	}
	if t.Kind >= token.KW_FN && t.Kind <= token.KW_EXPORT {
		return Keyword
	}
	return ""
}

// comments colours what the lexer threw away. §3.2 gives '#' its meaning everywhere
// except inside a string literal, and inside one the lexer has already claimed the rune
// for a token — so a '#' no token covers is the start of a comment, and it runs to the
// end of its line.
func comments(src []rune, covered []bool, out []string) {
	for i := 0; i < len(src); i++ {
		if covered[i] || src[i] != '#' {
			continue
		}
		for ; i < len(src) && src[i] != '\n'; i++ {
			covered[i] = true
			out[i] = Comment
		}
	}
}

// mismatches reddens a bracket that closes nothing, or closes the wrong thing. An
// *unclosed* bracket is left alone on purpose: in the REPL it is not a mistake but the
// continuation prompt about to appear.
func mismatches(toks []token.Token, shift int, paint func(from, to int, c string)) {
	var stack []token.Kind
	for _, t := range toks {
		switch t.Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			stack = append(stack, t.Kind)
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if n := len(stack); n > 0 && stack[n-1] == opener(t.Kind) {
				stack = stack[:n-1]
				continue
			}
			paint(t.Pos.Offset+shift, t.End.Offset+shift, Mismatch)
		}
	}
}

func opener(k token.Kind) token.Kind {
	switch k {
	case token.RPAREN:
		return token.LPAREN
	case token.RBRACKET:
		return token.LBRACKET
	}
	return token.LBRACE
}
