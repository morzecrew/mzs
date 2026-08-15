# mzs — Morze Script Language Specification

**Version:** 0.1 (normative)
**Module:** `mzs` (Go 1.26, stdlib only, no cgo, no subprocess)
**Status:** This document is the single source of truth. Where an implementation and this
document disagree, this document wins. Everything marked **MUST** is required for the
morzebot-backend-v2 migration to be accepted.

mzs is its own language. It takes the *approach* of expression-oriented scripting with method
chains — every construct is an expression, the last value is the result, one-liners are first
class — and nothing else. It is not Ruby-shaped, there is no compatibility dialect, and there
is no mode switch. Code written for the earlier draft of this document does not run; §19 is the
migration.

---

## 0. Table of contents

1. Goals, non-goals, acceptance criteria
2. Design decisions (the short list)
3. Lexical structure
4. Grammar (EBNF)
5. Operator precedence and `match`
6. AST
7. Values and the object model
8. Evaluation semantics
9. One semantics — no modes
10. `$variables` and host binding
11. Regular expressions
12. Standard library
13. Go API
14. Resource limits, determinism, sandboxing
15. CLI
16. Acceptance corpus
17. Diagnostics
18. Package layout and implementation work-split
19. Migrating morzebot-backend-v2
20. Reserved / out of scope

---

## 1. Goals, non-goals, acceptance criteria

### 1.1 Goals

* **G1 — One-liners are first class.** A useful script must fit on one line, with no
  ceremony. Everything in the language is available inline: `;` separates statements,
  `{ … }` is an inline closure, statement modifiers (`x = 1 if c`) exist, ternaries exist,
  `match` arms fit on one line, and the value of the last expression is the result —
  `return` is never required. Concretely, this must be a legal and useful program:
  `mzs -e 's = $__sent.lower.trim; s == "yes" || s ~ /^yeah|sure/'`
* **G2 — Replace the Ruby subprocess** in `morzebot-backend-v2/pkg/engine/eval`. Same call
  shapes (`Bool`, `String`), no `ruby` binary, no fork. The stored condition strings are
  migrated once (§19); mzs does **not** parse Ruby.
* **G3 — One syntax.** There is exactly one lexer, one grammar and one evaluator. No dialects,
  no compatibility mode, no bug-compatibility with any other language. Anything that used to
  be ambiguous is now a diagnostic (§5.6).
* **G4 — Embeddable and safe.** Pure Go library, zero external modules, no cgo, no exec
  from scripts ever, no filesystem until a host hands one over (§12.13), hard timeout +
  step budget, per-eval isolated environment. The zero `Options` is the safe one: what a
  script can reach beyond arithmetic is a list of fields somebody filled in on purpose —
  network being the single exception, and a removable one (§12.11).
* **G5 — A real language, not an expression evaluator.** Functions, closures, loops,
  collections, string/array/dict/number stdlib, JSON, regex, pattern matching.

### 1.2 Non-goals

* Not Ruby, not Kotlin, not Go. mzs borrows the *approach* of expression-oriented scripting
  with method chains, and nothing else. No metaclasses, no `method_missing`, no modules/mixins,
  no user-defined types in v0.1, no `require`. No threads either, in the sense that matters:
  `async fn` (§8.14) runs tasks on goroutines but never two at a time, so a script has
  concurrency and no shared-memory parallelism — no locks, no atomics, no memory model.
* No mutation of strings (strings are immutable values).
* No macro system, no operator overloading.
* Not a JIT. A tree-walking interpreter over a pre-compiled AST is the specified design.

### 1.3 Acceptance criteria

| # | Criterion |
|---|---|
| A1 | Every expression in §16.1 evaluates to the stated value. |
| A2 | Every diagnostic in §5.6 is produced verbatim, with the correct line and column. |
| A3 | `morzebot-backend-v2/tests/eval_test.go` and `pkg/engine/eval/eval_test.go` pass against the mzs-backed `eval` package, after the one-time condition migration of §19. |
| A4 | Median condition evaluation ≤ 20 µs (compiled+cached), p99 ≤ 200 µs, versus ~45 ms for the Ruby fork. |
| A5 | No goroutine leaks; `Run` honours `ctx` cancellation and the step budget; a runaway `while true { }` is interrupted mid-loop, not between statements. |
| A6 | `go test ./...` passes with `-race`; `go vet` clean; no non-stdlib imports in `go.mod`. |
| A7 | A script can never panic the host process: all internal panics are recovered at the `Run`/`Eval` boundary and returned as `error`. |

---

## 2. Design decisions (the short list)

These are decided. No alternatives are offered anywhere in this document.

| # | Decision |
|---|---|
| D1 | Newline **and** `;` both terminate a statement. Newlines are suppressed after an operator, comma, opening bracket, `->`, `?`, `:`, and before a leading `.`/`?.`/`else`/`)`/`]`/`}`/`->`. |
| D2 | `{ … }` is a **closure literal** everywhere except operand position, where the §3.12 lookahead may read it as a **dict**. Constructs that take a body (`if`, `while`, `for`, `fn`, `match` arms) invoke it immediately with no arguments; after a call it is appended as the last argument. A closure body can never begin with a key and its separator — `name :`, `name ->`, `1 ->` — so the two readings never overlap and the parser still carries no state. |
| D3 | `[ … ]` is **always an array literal**; `[]` is the empty array. There is no other meaning of `[`. A dict is `{a: 1}` and the empty dict is `{}` — one spelling each (D17), and the one a JSON document already uses. |
| D4 | The value of the last evaluated statement is the value of a script, a function body, and a closure. `return` is optional and exists for early exit. |
| D5 | Regex matching is the `~` operator (and `!~`), always returning Bool. The index of a match is `s.index(/re/)`. `==` with a regex operand is a compile error. |
| D6 | Truthiness: only `nil` and `false` are falsy. `0`, `0.0`, `""`, `[]`, `{}` are **truthy**. |
| D7 | `$name` is a first-class identifier bound from a host table, both in code and inside string literals. It is a separate namespace from local variables and is never resolved through the scope chain. |
| D8 | Host values arrive as strings; conversions are explicit (`$n.int`). There is no coercion mode, no bareword shim, and no textual pre-substitution pass. |
| D9 | Integer/float split: `7 / 2 == 3`, `7.0 / 2 == 3.5`. `Int` is `int64`, `Float` is `float64`. Overflow of `Int` **promotes to Float** (never wraps). |
| D10 | Strings are immutable UTF-8. All indexing, `len`, `[]`, `chars`, `reverse` operate on **runes**, not bytes. |
| D11 | Dicts are **insertion-ordered**. Iteration and JSON output follow insertion order. Keys may be any hashable value (nil/bool/int/float/string/regex-source). |
| D12 | Regex: two backends behind one interface. RE2 (`regexp`) is used when the pattern is RE2-safe and contains no `\b`/`\B`; otherwise a bundled backtracking engine with Unicode-aware `\b` and lookaround. Same syntax accepted either way. |
| D13 | Errors are Go `error` values with position info and a kind from a closed list (§13.5). Scripts raise with `raise`, catch with `try … else …`, release with `ensure`. Internal panics are recovered at the API boundary. |
| D14 | Parameter lists are **always parenthesised**: `fn f(a, b) { … }` and `{ (a, b) -> … }`. A closure with no parameter list implicitly binds `it`. |
| D15 | Determinism by default: no clock, no RNG, no I/O unless the host supplies them via `Options` (`Now`, `Rand`, `Stdout`, `FS`, `Stdin`, `Env`, `Register`). |
| D16 | Ambiguity is a diagnostic, never a silent reading. Every construct that another language resolves by a surprising precedence rule is rejected with a fix-it message (§5.6). |
| D17 | Every operation has exactly one name. There are no aliases anywhere in the standard library. |
| D18 | Function-call syntax and method syntax are the same thing: `x.f(y)` ≡ `f(x, y)` (UFCS). The library is one flat namespace. |
| D19 | Concurrency is `async fn` + `t.await` and nothing else: calling one starts a task on a goroutine, and **one goroutine of a Run evaluates at a time**. A script gets overlapping waits, never shared-memory parallelism, so no value in the language needs a lock (§8.14). |

---
## 3. Lexical structure

### 3.1 Encoding and source

Source is UTF-8 text. The lexer operates on a `[]rune` slice (decode once, up front). A
byte-order mark at offset 0 is skipped. Line endings `\n`, `\r\n`, `\r` all count as one
newline. Source may be any length; there is no line-length limit — a whole program on one
line is normal.

### 3.2 Whitespace, indentation, comments

Space (U+0020), tab, and U+00A0 (NBSP, messengers send them) are insignificant whitespace
between tokens. **Indentation is never significant.** There is no offside rule, ever.

Comments start with `#` and run to end of line, with **no exceptions** — `#` has no meaning
inside string literals (§3.7). `//` is not a comment; it is division followed by division.
There are no block comments.

```
x = 1   # a comment
```

### 3.3 Token kinds

The lexer emits exactly these token kinds (Go: `internal/token.Kind`).

```go
package token

type Kind uint8

const (
    EOF Kind = iota
    NEWLINE      // statement terminator produced by a source newline (§3.10)
    SEMI         // ;

    IDENT        // foo, foo_bar, привет, it, _
    GVAR         // $__sent, $price  ($ + identifier chars)

    INT          // 42, 1_000, 0xff, 0b1010, 0o17
    FLOAT        // 1.2, 1e9, 1.5e-3
    STR_BEGIN    // opening quote of a string literal
    STR_TEXT     // a run of decoded literal text inside a string
    STR_GVAR     // $name occurring inside a string literal (§3.7)
    INTERP_BEGIN // ${
    INTERP_END   // }  that closes an interpolation
    STR_END      // closing quote of a string literal
    REGEX        // /pattern/flags   (Value = pattern, Flags = flags)

    // keywords — all seventeen
    KW_FN; KW_IF; KW_ELSE; KW_MATCH; KW_WHILE; KW_FOR; KW_IN
    KW_BREAK; KW_NEXT; KW_RETURN; KW_TRY; KW_ENSURE; KW_TRUE; KW_FALSE; KW_NIL
    KW_INCLUDE; KW_EXPORT

    // operators and punctuation (one Kind per lexeme)
    ASSIGN      // =
    DECLARE     // :=
    PLUS_EQ; MINUS_EQ; STAR_EQ; SLASH_EQ; PERCENT_EQ; POW_EQ; OR_EQ; AND_EQ; NIL_EQ
    EQ; NEQ; TILDE; NTILDE                   // == != ~ !~
    LT; LTE; GT; GTE; SPACESHIP              // < <= > >= <=>
    ANDAND; OROR; BANG; NILNIL               // && || ! ??
    PLUS; MINUS; STAR; SLASH; PERCENT; POW   // + - * / % **
    DOT; SAFEDOT                             // . ?.
    DOTDOT; DOTLT                            // .. ..<
    ARROW                                    // ->
    QUESTION; COLON                          // ? :
    COMMA
    LPAREN; RPAREN; LBRACKET; RBRACKET; LBRACE; RBRACE
)
```

Every token carries position:

```go
type Pos struct { Offset, Line, Col int } // Line and Col are 1-based, Col counts runes
type Token struct {
    Kind  Kind
    Value string // lexeme for operators; decoded text for IDENT/STR_TEXT; digits for INT/FLOAT; pattern for REGEX
    Flags string // REGEX only: the trailing flag letters
    Pos   Pos
    End   Pos
}
```

The lexer never panics and never silently drops a rune. Any rune it cannot classify produces
a `LexError` at that position and lexing continues from the next rune (error recovery), so a
single bad character yields one diagnostic rather than a cascade.

### 3.4 Identifiers

```
ident_start ::= unicode_letter | "_"
ident_part  ::= unicode_letter | unicode_digit | "_"
ident       ::= ident_start ident_part*
```

* `unicode.IsLetter` decides letterhood, so **Cyrillic identifiers are identifiers**
  (`да`, `столовая`).
* There is **no `?` or `!` suffix**. `empty?` lexes as `IDENT(empty)` followed by `QUESTION`,
  and the parser reports `'?' is not part of an identifier; did you mean 'empty'?`. Because
  of this, whitespace never changes how a ternary is read: `x.empty ? 1 : 2` and
  `x.empty?1:2` are the same tokens.
* The case of the first rune means nothing. There is no `CONST` token kind and no `::`
  operator; `json`, `math`, `time`, `date` and `http` are ordinary values that an
  `include` binds (§12.8), and are reached with `.` like anything else.
* `_` is an ordinary identifier. It is the conventional name for a parameter you do not use
  (`{ (_, v) -> v }`), but the language attaches no special meaning to it.
* `$` + `ident_part+` lexes as `GVAR`. The name **includes** the `$` (`Token.Value ==
  "$__sent"`). A `$` alone, outside a string literal, is a lex error.
* `@name` is reserved and is a lex error in v0.1 (§20).

### 3.5 Keywords

```
fn  if  else  match  while  for  in  break  next  return  try  ensure  true  false  nil
include  export
```

Seventeen, and that is the complete list. All are lexed as their `KW_*` kind, never as
`IDENT`.

`ensure` is the one addition since v0.1's first draft, and it costs what a keyword always
costs: `ensure` is no longer a name a program may bind. It is here because the clause it
opens has to be recognisable from the token stream alone, exactly as `else` is — a `try`'s
release runs on paths no expression can name (§8.11).

`from` is **not** a keyword. It is read positionally inside an `include` and is an ordinary
identifier everywhere else, so a variable may still be called `from`.

`async` is **not** a keyword either, for the same reason: it is read positionally, directly
before `fn` (§8.14), and is an ordinary identifier everywhere else. `async = 1` and
`fn async(x)` both still compile.

`it` is **not** a keyword — it is an ordinary identifier that a closure implicitly binds when
it declares no parameter list (§8.9).

A keyword is accepted as a method name after `.` (`x.if`), but no such name exists in the
standard library and UFCS will not find one, so this is only a parser convenience.

### 3.6 Number literals

```
int    ::= dec | "0x" hex+ | "0b" bin+ | "0o" oct+
dec    ::= digit ( digit | "_" )*
float  ::= dec "." dec [ exp ] | dec exp
exp    ::= ("e"|"E") ["+"|"-"] digit+
```

Underscores are separators and are stripped. **Disambiguation with method calls:** a `.`
after digits starts a float **only if** the next rune is a digit. `1.str` therefore lexes as
`INT(1) DOT IDENT(str)`, while `1.2` lexes as `FLOAT(1.2)`. `1..5` lexes as
`INT(1) DOTDOT INT(5)` (the `..`/`..<` check runs before the float check).

`INT` values that do not fit in `int64` are a lex error. Float parsing uses
`strconv.ParseFloat`; `Inf`/`NaN` literals do not exist.

Because a float must begin with a digit, `.5` is never a literal — which is what makes `?.`
unambiguous against a ternary (§3.9).

### 3.7 String literals

Two forms.

* **Single-quoted** `'…'` — **raw**. The only escapes are `\'` and `\\`. No interpolation.
  Everything else, including `\n` and `\b`, is literal. This is the form to use for regex
  source text: `regex('\bменю', 'i')`.
* **Double-quoted** `"…"` — escapes and interpolation.
  Escapes: `\n \r \t \0 \\ \" \' \a \b \f \v \e \$`, `\uXXXX`, `\u{X…}` (1–6 hex digits),
  `\xNN`. An unknown escape `\c` is the literal character `c`.

**Interpolation is spelled with `$`.**

| Form | Meaning |
|---|---|
| `$name` | the host global `$name` — **exactly** what `$name` means outside a string |
| `${ expr }` | any expression, including local variables |
| `$` not followed by `ident_start` or `{` | a literal `$` (`"price: 100 $"`) |
| `\$` | a literal `$` in any position |

`$name` takes the longest run of `ident_part`. To end the name early, use `${name}`:
`"${n}00"` is `n` followed by `00`, whereas `"$n00"` is the global `$n00`.

```
"Your address is $__sent?"               # a global; '?' is not part of the name (§3.4)
"Total price: ${$price.int + 1200}"
"var:time:${n}:00"                       # n is a local, so braces are required
"10\$ off"
```

`$name` therefore denotes the same thing inside and outside a string, which is the whole
point of the sigil. The cost is that a local variable inside a string needs `${x}`; in
practice the values that get interpolated are host globals (`$__sent`, `$price`,
`$book_time`, `$user_name`).

**Token stream for strings.** The lexer flattens interpolation so the parser needs no
sub-lexer:

```
"a${x+1}b$c"  =>  STR_BEGIN STR_TEXT("a")
                  INTERP_BEGIN IDENT(x) PLUS INT(1) INTERP_END
                  STR_TEXT("b") STR_GVAR("$c") STR_END
```

`STR_TEXT` carries **decoded** text. A string with no interpolation still emits
`STR_BEGIN STR_TEXT STR_END` (with `STR_TEXT` omitted when empty). Inside `${ … }` the lexer
runs in normal mode with a brace-depth counter; the `}` that returns depth to zero is
`INTERP_END`. Newlines inside `${}` are suppressed.

An unterminated string is an error at the position of the opening quote (never silently
accepted).

There is no word-array literal: write `["yes", "yeah", "sure"]`.

### 3.8 Regex literals

```
regex ::= "/" pattern "/" flag*
flag  ::= "i" | "m" | "x" | "s" | "u"
```

The pattern is raw: a single backslash is one escape character. `\/` is an escaped delimiter
and becomes `/` in the pattern. A newline inside a regex literal is a lex error (use `\n`).
Flags:

| Flag | Meaning |
|---|---|
| `i` | case-insensitive, Unicode-aware (Cyrillic included) |
| `m` | `.` matches newline (Go's `s`). The spelling `s` is also accepted, same meaning. |
| `x` | extended: unescaped whitespace and `#…` comments in the pattern are ignored |
| `u` | no-op, accepted because patterns get pasted around (mzs is always Unicode) |

**`^`/`$` are always multi-line anchors** (line anchors). `\A` and `\z`/`\Z` anchor the whole
string. This differs from Go's default and MUST be handled by the compiler (§11).

Interpolation inside a regex literal is not supported; build dynamic patterns with
`regex(str, flags)` (§12.6), passing the source as a raw `'…'` string.

**Regex vs. division disambiguation.** A `/` starts a regex literal iff the previous
significant token (ignoring NEWLINE) is one of:

* nothing (start of input), or
* any operator or punctuation **except** `)`, `]`, `}`, or
* `SEMI`, `NEWLINE`, `COMMA`, `LPAREN`, `LBRACKET`, `LBRACE`, `ARROW`, or
* any keyword.

Otherwise `/` is division. In particular: after `IDENT`, `GVAR`, `INT`, `FLOAT`, `STR_END`,
`REGEX`, `RPAREN`, `RBRACKET`, `RBRACE`, `KW_TRUE`, `KW_FALSE`, `KW_NIL` → division.
Everywhere else → regex. `~ /re/`, `!~ /re/`, `(/re/)`, `f(/re/)`, `x = /re/`, a `match` arm
beginning with `/re/`, and a statement-initial `/re/` all take the regex branch; `a / b` and
`f(1) / 2` take the division branch.

### 3.9 Operator lexemes (longest match first)

```
**=  ..<  ||=  &&=  ??=
==   !=   <=   >=   **   ..   ?.   ->   :=   ??   &&   ||   !~   <=>
+=   -=   *=   /=   %=
=    <    >    +    -    *    /    %    !    ~    .    ,    ?    :
(    )    [    ]    {    }    ;
```

The lexer MUST use a longest-match table (or an explicit switch) and MUST advance `pos` by
`len([]rune(lexeme))`.

`?.` cannot collide with a ternary: a float must start with a digit (§3.6), so `x ? .5 : 1`
is unwritable and `?.` is always a safe call.

Two lexemes exist only to produce a good error and are never valid tokens:

* `=~` → `'=~' is not an mzs operator; use '~'`
* `=!` → `unexpected '!' after '='; did you mean '!='?`

`&`, `|` and `^` are not lexemes at all: the bit operations are functions (§12.5), because
`&` beside `&&` is the ambiguity D16 refuses. Each of the three runes carries its own fix-it
naming that function (§5.6), reported by the lexer since the parser never sees them.

### 3.10 Statement termination and line continuation

The lexer emits `NEWLINE` for a source line break **unless** the last significant token is
in the *continuation set*:

```
any binary or unary operator, ASSIGN and all compound assigns,
COMMA  LPAREN  LBRACKET  LBRACE  ARROW  QUESTION  COLON
DOT  SAFEDOT  DOTDOT  DOTLT
KW_IF  KW_ELSE  KW_MATCH  KW_WHILE  KW_FOR  KW_IN  KW_RETURN  KW_TRY  KW_FN
INTERP_BEGIN, and any token inside an interpolation
```

Consecutive `NEWLINE`s collapse into one (blank lines never produce empty statements). The
parser additionally skips a pending `NEWLINE` when the **next** significant token is
`DOT`, `SAFEDOT`, `KW_ELSE`, `ARROW`, `RPAREN`, `RBRACKET`, `RBRACE`, or a binary operator
**other than `KW_IN`**. `in` is excluded from that last set, and has to be: a line that
*starts* with `in` is the `in` arm of a `match` (§5.3), so swallowing the newline in front
of it would glue that arm onto the previous one's body. The continuation still works from
the other side — `KW_IN` is in the set above — so `x in\n  xs` is one expression while
`-> "low"\nin 6..10 ->` stays two arms.

This gives leading-dot method chains, hanging `else`, and multi-line `match` arms:

```
$__sent
  .lower
  .trim == "operator"

if c { a }
else { b }
```

`;` is always a hard terminator and is never suppressed. A trailing `;` is allowed.

### 3.11 `{` and `[`

`[` always opens a collection. `{` opens one of three things, and **which one is decided by
position alone, never by parser state** — this is the single largest simplification in the
language, and nothing may be added that reintroduces the ambiguity.

| Position | Reading |
|---|---|
| after the header of `if`, `while`, `for`, `fn` or a `match` arm | the **body** |
| in a clause of a `try` — the body, the `else`, the `ensure` (§8.11) | the **body** |
| directly after a call-shaped expression (§4.2) | the **trailing closure** |
| operand position | a **dict** if the §3.12 lookahead says so, otherwise a **closure** |

The three never overlap, because a closure body and a block can never begin with a key
and its separator — `name :`, `name ->`, `1 ->` — the shapes §3.12 keys on. Each position
has exactly one reading, so no token is ever re-read and no node is ever undone.

One restriction, the same one Go uses: **inside the header of `if`, `while`, `for` or
`match`, a `{` at bracket depth 0 ends the header and opens the body.** A call with a
trailing closure in a header must therefore be parenthesised:

```
if xs.any { it > 5 } { … }      # ERROR: the header ended at the first '{'
if (xs.any { it > 5 }) { … }    # correct
```

A braced `try` clause is the same case and gets the same fix, because its brace would
otherwise have to compete with the body's:

```
if try { f() } else { 0 } { … }     # ERROR: a braced 'try' cannot open a header
if (try { f() } else { 0 }) { … }   # correct
if try f() else 0 { … }             # correct: no clause took a brace
```

A dict in a header needs no parentheses, because the lookahead runs before the header
rule — the brace that ends a header is a brace the lookahead declined:

```
if {a: 1}.has("a") { … }        # legal as written
if x == {a: 1} { … }            # legal as written
```

Body position keeps its meaning, so a body that *evaluates to* a dict writes the dict
inside it — one brace for the body, one for the value:

```
status = if ready { {code: 200} } else { {code: 503} }
```

Trailing position keeps its meaning too, so a dict argument is spelled with the call's own
parentheses. `{}` there is the empty closure, not the empty dict:

```
f {a: 1}                        # ERROR: a dict after a call is written f({a: 1})
f(a: 1)                         # ERROR: `a = 1` names a parameter; `f({a: 1})` is the dict
f({a: 1})                       # an explicit dict argument
f(a = 1)                        # a named argument, binding the parameter `a` (§8.7)
xs.each { }                     # an empty trailing closure
```

### 3.12 Closure or dict

`[` is an array and needs no lookahead at all. The lookahead belongs to `{`, and in
operand position — and only there (§3.11) — it decides closure or dict by a bounded,
deterministic scan run one token past the brace:

1. `}` immediately → the empty dict `{}`.
2. First token is `IDENT` or a string literal and the second is `:` or `->` → **dict**.
3. First token is `(` → skip to the matching `)`; if the next token is `:` → **dict**,
   otherwise closure. A `->` there is a parameter list (§4.1) and never a key.
4. First token is a literal — a number with an optional sign, `true`, `false`, `nil`, a
   regex — and the token after it is `->` or `:` → **dict**.
5. Otherwise → **closure**.

No parse action is ever undone, and nothing is consumed unless the scan commits. A
ternary does not interfere: in `{x ? a : b}` the first token is `IDENT` and the second is
`?`, so it is a closure whose body is a ternary. Neither does a body that opens with a
number: `{ -1 }` is a closure, because the token after the literal is `}`.

```
{}                  # empty dict
{a: 1, b: 2}        # dict with the string keys "a" and "b"
{"a": 1}            # a quoted key is the same dict
{a -> 1}            # `->` ends a key wherever `:` does — the same dict again
{(k): 1}            # computed key
{1 -> "A"}          # the Int key 1 (§7.6)
{-2.5 -> a, true -> b, nil -> c}    # every other literal key
{(x) -> x * 2}      # closure: the token after the matching ')' is '->', not ':'
{ it * 2 }          # closure
{x ? a : b}         # closure whose body is a ternary
{ -1 }              # closure: a literal is a key only in front of a separator
{ nil }             # the empty closure value, since `{}` is the empty dict
```

A bare-identifier key becomes a string literal, so dicts are JSON-serialisable with no
symbol type in the language — and a JSON document is already an mzs dict literal, which
is why the brace is the dict's only spelling. `->` changes nothing about that: `{a -> 1}`
and `{a: 1}` are the same dict, and the identifier is the string "a" either way, never
the variable `a` — that is what `(a): 1` is for.

**One spelling per key.** A key that is not a string takes `->`, a computed key takes
`:`, and each other spelling is a diagnostic that names its replacement (§5.6):

| You wrote | Message |
|---|---|
| `{1: "A"}` | `a dict key that is not a string takes '->', not ':'` |
| `(x) -> x * 2` | `an arrow function's body is braced: (x) -> { x * 2 }, or write the closure { (x) -> x * 2 }` |
| `async (x) -> { x }` | ``an async function is written `async fn(a, b) { … }` `` |
| `{a: 1, (k) -> 2}` | `a computed dict key takes ':', not '->': write (k): v` |

**Brackets never carry keys.** `[a: 1]` and `[:]` were the earlier draft's dict literals; both are
now diagnostics that name the replacement, so no such source is read as something else:

| You wrote | Message |
|---|---|
| `[a: 1]` | `a dict is written {a: 1}` |
| `[:]` | `the empty dict is written {}` |

```
[]                  # empty array
[1, 2, 3]           # array
[x ? a : b]         # one-element array
```

---
## 4. Grammar (EBNF)

Notation: `A*` zero or more, `A+` one or more, `[A]` optional, `A | B` alternation,
`"x"` a terminal lexeme, `TERM` a token kind. `SEP` is the statement separator.

```ebnf
Program        = StmtList EOF ;

SEP            = NEWLINE | ";" ;
StmtList       = SEP* [ Stmt ( SEP+ Stmt )* SEP* ] ;

Stmt           = BareStmt { Modifier } ;
Modifier       = "if" Expr | "while" Expr ;

BareStmt       = IncludeStmt | ExportStmt | FnDecl | ReturnStmt | BreakStmt | NextStmt
               | Destructure | Expr ;

IncludeStmt    = "include" IDENT [ "from" STRING ] ;   (* §12.8; "from" is positional *)
ExportStmt     = "export" ( FnDecl | Assignment | IDENT ) ;

(* ---------- destructuring (§8.15) ---------- *)

Destructure    = Target ( "," Target )+ ( "=" | ":=" ) Expr ;  (* the bare form: statement level only *)
Target         = IDENT | GVAR | Postfix "[" Expr "]" | TargetPattern ;
TargetPattern  = "[" [ Target { "," Target } [ "," ] ] "]" ;   (* also valid alone: [a, b] = xs *)

ReturnStmt     = "return" [ Expr ] ;
BreakStmt      = "break"  [ Expr ] ;
NextStmt       = "next"   [ Expr ] ;

(* ---------- functions and closures ---------- *)

FnDecl         = [ "async" ] "fn" [ IDENT ] "(" ParamList ")" Closure ;  (* §8.14; "async" is positional *)
               (* the named form is a declaration and hoists (§8.2); the anonymous form
                  is a value and binds nothing (§4.1) *)
ParamList      = [ Param { "," Param } [ "," ] ] ;
Param          = IDENT [ "=" Expr ]                (* default value *)
               | "*" IDENT ;                       (* rest parameter, last only *)

Closure        = "{" [ ClosureParams ] StmtList "}" ;
ClosureParams  = "(" ParamList ")" "->" ;

(* ---------- control flow (all are expressions) ---------- *)

IfExpr         = "if" Expr Closure { "else" "if" Expr Closure } [ "else" Closure ] ;
WhileExpr      = "while" Expr Closure ;
ForExpr        = "for" IDENT [ "," IDENT ] "in" Expr Closure ;
MatchExpr      = "match" [ Expr ] "{" SEP* MatchArm ( SEP+ MatchArm )* SEP* "}" ;
MatchArm       = ArmPattern { "," ArmPattern } [ "if" Expr ] "->" ( Expr | Closure )
               | MatchPattern [ "if" Expr ] "->" ( Expr | Closure )
               | "else" "->" ( Expr | Closure ) ;
ArmPattern     = "in" Expr          (* membership *)
               | "if" Expr          (* bare condition *)
               | Expr ;             (* equality; a regex operand means match *)
MatchPattern   = "[" [ MatchElem { "," MatchElem } [ "," ] ] "]" ;   (* §8.15; binds *)
MatchElem      = IDENT              (* binds this position *)
               | MatchPattern       (* nested *)
               | Expr ;             (* compared, exactly as an ArmPattern is *)
TryExpr        = "try" TryClause [ "else" [ TryBinder ] TryClause ] [ "ensure" Block ] ;
               (* at least one of "else" and "ensure" is required (§8.11) *)
TryClause      = Block | Expr ;     (* §3.12 reads a dict operand first: try {a: 1} is a dict *)
TryBinder      = "(" IDENT ")" [ "->" ] ;   (* optional before a "{" — block or dict; required before any other Expr *)
Block          = "{" StmtList "}" ; (* a body, not a closure: it declares no parameters *)

(* ---------- expressions, loosest to tightest ---------- *)

Expr           = AssignExpr ;
AssignExpr     = TernaryExpr [ AssignOp AssignExpr ] ;                (* right assoc *)
               (* a TargetPattern on the left of "=" / ":=" destructures, §8.15 *)
AssignOp       = "=" | ":=" | "+=" | "-=" | "*=" | "/=" | "%=" | "**=" | "||=" | "&&=" | "??=" ;
TernaryExpr    = NilExpr [ "?" Expr ":" TernaryExpr ] | TryExpr ;     (* right assoc *)
NilExpr        = OrExpr  { "??" OrExpr } ;
OrExpr         = AndExpr { "||" AndExpr } ;
AndExpr        = EqExpr  { "&&" EqExpr } ;
EqExpr         = CmpExpr { ( "==" | "!=" | "~" | "!~" ) CmpExpr } ;
CmpExpr        = InExpr { ( "<" | "<=" | ">" | ">=" | "<=>" ) InExpr } ;
InExpr         = RangeExpr [ "in" RangeExpr ] ;                       (* non-associative *)
RangeExpr      = AddExpr [ ( ".." | "..<" ) AddExpr ] ;               (* non-associative *)
AddExpr        = MulExpr { ( "+" | "-" ) MulExpr } ;
MulExpr        = UnaryExpr { ( "*" | "/" | "%" ) UnaryExpr } ;
UnaryExpr      = ( "!" | "-" | "+" ) UnaryExpr | PowExpr ;
PowExpr        = Postfix [ "**" PowExpr ] ;                           (* right assoc *)

Postfix        = Primary { Trailer } ;
Trailer        = ( "." | "?." ) MethodName [ "(" ArgList ")" ] [ Closure ]
               | "(" ArgList ")" [ Closure ]
               | "[" Expr [ "," Expr ] "]" ;
MethodName     = IDENT | KEYWORD ;
ArgList        = [ Arg { "," Arg } [ "," ] ] ;
Arg            = Expr | IDENT "=" Expr ;           (* named argument -> parameter, §8.7 *)

Primary        = INT | FLOAT | String | Regex | "true" | "false" | "nil"
               | IDENT | GVAR
               | Collection
               | Closure                           (* a function value *)
               | ArrowFn                           (* the same, spelled with "->" *)
               | GroupExpr
               | IfExpr | WhileExpr | ForExpr | MatchExpr
               | FnDecl ;
ArrowFn        = "(" ParamList ")" "->" Closure ;  (* an anonymous FnDecl, §4.1 *)

GroupExpr      = "(" StmtList ")" ;                (* value = last statement *)
Collection     = "[" ( ArrayBody | DictBody | ":" ) "]" ;
ArrayBody      = [ Expr { "," Expr } [ "," ] ] ;
DictBody       = DictEntry { "," DictEntry } [ "," ] ;
DictEntry      = ( IDENT | String ) ( ":" | "->" ) Expr   (* a string key *)
               | "(" Expr ")" ":" Expr                    (* computed; never "->", §3.12 *)
               | LiteralKey "->" Expr ;                   (* a key that is not a string, §7.6 *)
LiteralKey     = [ "-" | "+" ] ( INT | FLOAT ) | "true" | "false" | "nil" | Regex ;
String         = STR_BEGIN { STR_TEXT | STR_GVAR | INTERP_BEGIN Expr INTERP_END } STR_END ;
Regex          = REGEX ;
```

### 4.1 Closures

`{ … }` is the closure literal, and with the anonymous `fn` below it is one of the two
function-value forms — the short one, which a library calls.

```
{ it * 2 }               # no parameter list -> one implicit parameter, `it`
{ (x) -> x * 2 }         # one parameter
{ (k, v) -> "${k}=${v}" }  # two
{ (_, v) -> v }          # first parameter unused
{ () -> 42 }             # explicitly zero parameters
{ 42 }                   # same thing, `it` simply goes unused
```

Parameter lists are parenthesised everywhere in the language (D14), so a closure and a named
function declare parameters the same way:

```
fn add(a, b) { a + b }
add2 = { (a, b) -> a + b }
```

**Parsing.** After `{`, if the next token is `(`, the parser skips to the matching `)` and
checks whether the following token is `->`. If it is, the parenthesised text is a parameter
list; otherwise it is the start of the body (a `GroupExpr`). This is the same bounded
lookahead as §3.12 and requires no backtracking of parse actions.

If a closure declares no parameter list, it implicitly declares one parameter named `it`.
`it` is an ordinary local inside the closure and may be shadowed by an explicit parameter.

A closure is an ordinary value. There is no separate concept of a "block", and therefore no
`&f` syntax for passing a function where a block is expected:

```
double = { it * 2 }
[1,2,3].map(double)      # same as
[1,2,3].map { it * 2 }   # trailing closure = last argument
```

**The anonymous `fn`.** Dropping the name from a `fn` makes it an expression and nothing
else: it is not hoisted, it binds nothing, and its value is the only way to reach it.

```
add  = fn(a, b) { a + b }        # a function value
add(2, 3)                        # 5
fn(x) { x * 3 }(5)               # 15 — called where it stands
[1,2,3].map(fn(x) { x * 2 })     # a closure would do as well
task = async fn(u) { http.get(u) }   # §8.14, the same modifier
```

**The arrow form.** `(params) -> { body }` is that same anonymous `fn` with the keyword
left out — the same node, the same value, nothing else changed. The parameters are outside
the braces, so the braces are a **body** and not a value, which is the whole difference
from the closure literal that reverses them:

```
add = (a, b) -> { a + b }        # the same function as fn(a, b) { a + b }
(x) -> { x * 3 }(5)              # 15
[1,2,3].map((x) -> { x * 2 })    # [2,4,6]
{ (x) -> x * 2 }                 # a closure: the braces are the value (§4.1 above)
```

The body is braced, and the braceless arrow keeps its one meaning — the closure's
parameter list — so `(x) -> x * 2` is a diagnostic that names both replacements (§5.6).
`async` has no keyword to stand in front of here and stays with `fn` (§8.14). Two
positions read these tokens before this rule does, and each keeps its reading: a header,
where the `{` opens the body (§3.11), and a `match` arm's pattern, where the `->` opens the
arm (§5.3). Both take parentheses when an arrow function is really meant:
`if ((x) -> { x })(1) { … }`.

Either spelling is a **function**, not a closure, in the two ways a program can tell
(§7.7): its arity is checked, and a `return` inside it returns from it rather than from the
function around it. Which form to reach for is therefore a question of what the body does —
`{ … }` for the short one that a library calls, `fn(…) { … }` or `(…) -> { … }` for the one
with an interface of its own:

```
f = fn(a, b) { a + b }
f(1)                     # error: function expects 2 argument(s), got 1
g = { (a, _) -> a }
g.call(1)                # 1 — a closure takes what it is given (§7.7)
```

### 4.2 Calls

* `x.f` with no parentheses is a **method call with zero arguments**, not a field access.
  Values have no fields.
* Paren-less calls **with** arguments (`say "hi"`) are not supported. Always write `println("hi")`.
* A trailing closure after `)` or after a method name is appended as the **last argument**:
  `xs.map { … }` ≡ `map(xs, { … })`, and `xs.reduce(0) { … }` ≡ `reduce(xs, 0, { … })`.
* A trailing closure binds to the **nearest preceding call**: `a.map { … }.join(",")` parses
  as `((a.map{…}).join(","))`.
* An argument written `name = value` binds the callee's **parameter** of that name
  (`f(1, c = 5)`, §8.7). Named arguments follow every positional one, and each name may be
  given once. There is no second spelling: `f(a: 1)` is a §5.6 diagnostic, and an
  assignment in argument position is written with its own parentheses, `f((x = 5))`.

### 4.3 UFCS

`x.f(y)` resolves in this order (D18):

1. the stdlib method table for `x`'s kind (§12) — an exact name match;
2. a function named `f` visible in lexical scope (a user `fn` or a builtin) → `f(x, y)`.
   A binding of that name holding something other than a function — a module above all —
   is not a candidate and does not hide the row: `nil.json` encodes under `include json`
   as it does without it;
3. otherwise `undefined method 'f' for string`.

Consequences: `len(s)` and `s.len` are the same function; `json(x)` and `x.json` are the same
function (and where an `include` has made `json` the module, `x.json` is the only spelling
of it, §12.8); and a user function is immediately usable as a method:

```
fn shout(s) { s.upper + "!" }
"yes".shout         # "YES!"
```

`?.` short-circuits: if the receiver is `nil` the whole postfix expression is `nil` and the
arguments are **not** evaluated.

### 4.4 Modifiers

Only `if` and `while`. They apply left to right and each wraps everything to its left:

```
x = 1 if c
x += 1 while x < 5
```

There are no `unless`, `until` or `rescue` modifiers.

### 4.5 Static parse restrictions

The grammar above admits two constructs that other languages resolve with a surprising
precedence rule. mzs rejects them instead (D16):

1. **Unary minus in front of `**`.** `UnaryExpr = ("!"|"-"|"+") UnaryExpr | PowExpr` formally
   yields `-(2 ** 2)`. If the operand of a unary `-`/`+` is a `PowExpr` with a top-level
   `**`, the parser reports `ambiguous: write -(2 ** 2) or (-2) ** 2`.
2. **A trailer on a numeric literal to the right of a range.** `RangeExpr` formally yields
   `0..(5.map { … })`. If the right operand of a range is a numeric literal **and** carries
   at least one `Trailer`, the parser reports `ambiguous range: write (0..5).map`.
   The restriction is deliberately narrow: `0..xs.len` and `0..n.abs` are legal and mean what
   they say. Nobody calls a method on a numeric literal used as a range bound, and everybody
   writes `0..5.map { … }`.

Both checks run on the node immediately after it is built; no backtracking is required.

---
## 5. Operator precedence and `match`

### 5.1 Precedence

From tightest (1) to loosest (14). All levels are left-associative unless noted.

| # | Operators | Assoc | Notes |
|---|---|---|---|
| 1 | `x.y` `x?.y` `x(…)` `x[…]` `x {…}` | left | postfix trailers |
| 2 | `**` | **right** | |
| 3 | `!` `-` `+` (unary) | right | `-x ** y` is a parse error (§4.5) |
| 4 | `*` `/` `%` | left | integer `/` when both sides are Int |
| 5 | `+` `-` | left | `+` also concatenates String/Array |
| 6 | `..` `..<` | **non-assoc** | `1..2..3` is a parse error |
| 7 | `in` | **non-assoc** | membership, always Bool (§8.5); `a in b in c` is a parse error |
| 8 | `<` `<=` `>` `>=` `<=>` | left | |
| 9 | `==` `!=` `~` `!~` | left | `~` is regex match, always Bool (D5) |
| 10 | `&&` | left | short-circuit, returns an operand |
| 11 | `\|\|` | left | short-circuit, returns an operand |
| 12 | `??` | left | fires on `nil` only, not on `false` |
| 13 | `? :` and `try … else … ensure …` | **right** | the `else` and `ensure` clauses bind to the nearest `try` (§8.11) |
| 14 | `=` `:=` `+=` `-=` `*=` `/=` `%=` `**=` `\|\|=` `&&=` `??=` | **right** | |
| 15 | modifiers `if` `while` | left | statement level only |

`in` sits where it does so that both of its neighbours read the way they are written: the
range is its operand (`a in 1..20` is `a in (1..20)`, never `(a in 1)..20`), and a
condition joined with `&&` groups around it (`a in xs && b` is `(a in xs) && b`).

Consequences worth pinning as tests:

```
a = b || c          # (a = (b || c))
!x == y             # ((!x) == y)
1 + 2 == 3          # ((1+2) == 3) => true
'a' + 'b' == 'ab'   # true
a ?? b ?? c         # ((a ?? b) ?? c)
a in 1..20          # (a in (1..20))
a in xs && b        # ((a in xs) && b)
x = 1 if c          # the modifier binds loosest
```

### 5.2 `match`

`match` replaces the `if`/`else if` ladder, which is the shape ~136 of the 272 production
conditions are written in.

```
match Subject {
  Pattern [, Pattern…] [if Guard] -> Expr | Closure
  …
  else -> Expr | Closure
}
```

Arms are separated by a newline or `;`, so `match` works on one line.

### 5.3 Patterns

| Form | Fires when |
|---|---|
| a literal (`"yes"`, `42`, `true`, `nil`) | `subject == pattern` |
| a regex literal, or any expression of kind regex | `subject ~ pattern` |
| `in expr` | `expr.has(subject)` — array, range, dict keys, or substring of a string |
| `if expr` | `expr` is truthy (the subject is not consulted) |
| `[p, …]` | the subject is an array (or range) of exactly that length and every position fits — a bare name binds it (§8.15) |
| `else` | always; allowed only as the last arm |
| any other expression | `subject == expr` |

Several patterns in one arm, separated by `,`, mean "or". An `if Guard` after the patterns is
an additional "and".

**Inside a pattern or a guard, a `->` at bracket depth zero ends it** — the same kind of
rule §3.11 has for the `{` of a header, over the other token that can close a construct.
That is what keeps `(1) -> { … }` an arm with a parenthesised pattern and a block body
instead of the arrow function of §4.1; inside a bracket, an argument list or the arm's own
body the arrow means what it means everywhere else:

```
match n { (1) -> { "one" }; else -> "many" }      # a pattern, then the arm's body
match n { 1 -> (y) -> { y + 1 }; else -> nil }    # the body is an arrow function
```

An array pattern is the one form that **binds**, so it must be the only pattern in its arm —
"or" over patterns that bind different names has no reading the body could rely on — and it
needs a subject, because with none every pattern is a condition (§5.4). The names it binds
live in the arm's own scope: the guard sees them, and nothing after the arm does.

```
match order {
  [x, y]        -> x + y          # binds both
  [0, n]        -> n              # a literal element still compares
  [m, n] if m > n -> "desc"       # the guard sees the bindings
  else          -> 0
}
```

`in [1, 2]` and `[a, b]` are different questions and stay spelled differently: the first asks
whether the subject is *in* that array, the second takes the subject *apart*. To compare
against an array value rather than destructure it, parenthesise it — `(xs) ->` — or compare in
a guard.

### 5.4 `match` with no subject

With the subject omitted, every pattern is evaluated as a boolean condition. This is the full
replacement for an `if`/`else if` ladder:

```
intent = match {
  yes                -> "confirm"
  s ~ /\boperator/i  -> "handoff"
  s.len > 500        -> "too_long"
  else               -> "unknown"
}
```

### 5.5 Value and evaluation

Arms are tested top to bottom and the first match wins. The value of the `match` is the value
of the winning arm's body. With no matching arm and no `else`, the value is `nil`. The
subject is evaluated exactly once.

```
s = $__sent.lower.trim

intent = match s {
  in ["yes", "yeah", "sure", "ok"]                  -> "confirm"
  in ["no", "nope", "nah"]                          -> "decline"
  /\boperator|\/operator|transfer.{0,12}operator/i  -> "handoff_operator"
  /all topics|main menu|\bmenu\b/i                  -> "main_menu"
  /hello|good morning|good evening|\bhi\b/i         -> "greeting"
  else                                              -> "unknown"
}
```

On one line:

```
match $__sent.lower.trim { "yes" -> 1; "no" -> 0; else -> nil }
```

### 5.6 Ambiguity diagnostics

Each row is its own named diagnostic test (A2). The point is that anyone pasting Ruby, or
code written against an older draft of this document, gets one precise fix-it rather than a
cascade.

| Input | Diagnostic |
|---|---|
| `-2 ** 2` | `ambiguous: write -(2 ** 2) or (-2) ** 2` |
| `0..5.map { it }` | `ambiguous range: write (0..5).map` |
| `1..2..3` | `range operator is non-associative` |
| `a in b in c` | `'in' is non-associative: write (a in b) in c if that is what you meant` |
| `f(1, a: 2)` | `a named argument is written 'a = …'; for a dict argument write f({a: …})` |
| `f(a = 1, 2)` | `a positional argument may not follow a named one; move it before 'a = …'` |
| `f(a = 1, a = 2)` | `argument 'a' is named twice` |
| `f(a = 1) { … }` | `a trailing closure is a positional argument, so it cannot follow the named argument 'a = …': pass the closure by name too, or give every argument by position` |
| `s == /re/` | `'==' with a regex operand: use '~' to match` |
| `s =~ /re/` | `'=~' is not an mzs operator; use '~'` |
| `x.empty?` | `'?' is not part of an identifier; did you mean 'empty'?` |
| `x.downcase` | `undefined method 'downcase'; did you mean 'lower'?` |
| `a and b`, `a or b`, `not a` | `'and'/'or'/'not' are not mzs keywords; use '&&', '\|\|', '!'` |
| `if c do … end` | `'do'/'end' are not mzs keywords; use braces: if c { … }` |
| `elsif c { … }` | `'elsif' is not an mzs keyword; use 'else if'` |
| `unless c { … }` | `'unless' is not an mzs keyword; use 'if !(c)'` |
| `until c { … }` | `'until' is not an mzs keyword; use 'while !(c)'` |
| `loop { … }` | `'loop' is not an mzs keyword; use 'while true { … }'` |
| `def f() { }` | `'def' is not an mzs keyword; use 'fn'` |
| `%w[a b]` | `'%w' is not mzs; write ["a", "b"]` |
| `:name` | `mzs has no symbols; write "name"` |
| `[a: 1]` | `a dict is written {a: 1}` |
| `[:]` | `the empty dict is written {}` |
| `{1: "A"}` | `a dict key that is not a string takes '->', not ':'` |
| `{a: 1, (k) -> 2}` | `a computed dict key takes ':', not '->': write (k): v` |
| `f {a: 1}` | `a dict after a call is written f({a: 1})` |
| `if c {a: 1}` | `this '{' opens the if body; write { {a: 1} } for a dict` |
| `k => v` | `'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure` |
| `{ \|x\| … }` | `closure parameters are parenthesised: { (x) -> … }` |
| `x &. y` | `'&.' is not an mzs operator; use '?.'` |
| `a & b` | `'&' is not an mzs operator; use band(a, b), or '&&' for logical and` |
| `a \| b` | `'\|' is not an mzs operator; use bor(a, b), or '\|\|' for logical or` |
| `a ^ b` | `'^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power` |
| `a rescue b` | `'rescue' is not an mzs keyword; use 'try a else b'` |
| `import lib from "…"`, `require "…"`, `use lib` | `'import' is not an mzs keyword; use 'include': include lib from "./lib.mzs"` |
| `"#{x}"` | `string interpolation is "${x}"` |
| `1...5` | `'...' is not an mzs operator; use '..<'` |
| `a::B` | `'::' is not an mzs operator; use '.'` |
| `str =! "x"` | `unexpected '!' after '='; did you mean '!='?` |
| `x.to_s`, `x.to_i`, `x.to_f`, `x.to_a`, `x.to_h`, `x.to_json` | `undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'` |

---
## 6. AST

Package `internal/ast`. Every node implements `Node`:

```go
type Node interface {
    Pos() token.Pos
    End() token.Pos
    node()
}
type Expr interface { Node; expr() }
type Stmt interface { Node; stmt() }   // every Expr is also a Stmt via ExprStmt
```

### 6.1 Node list

**Root / statements**

| Node | Fields |
|---|---|
| `Program` | `Stmts []Stmt`, `File string` |
| `ExprStmt` | `X Expr` |
| `ReturnStmt` | `X Expr` (may be nil) |
| `BreakStmt` | `X Expr` (may be nil) |
| `NextStmt` | `X Expr` (may be nil) |
| `FnDecl` | `Name string` (empty for the anonymous form, §4.1), `Params []Param`, `Body *BlockStmt`, `Async bool` (§8.14) |
| `BlockStmt` | `Stmts []Stmt` — a statement list; carries its own scope (§8.2). An Expr as well as a Stmt: its value is its last statement (§8.1), which is what a braced `try` clause is (§8.11) |

**Control flow (expressions)**

| Node | Fields |
|---|---|
| `IfExpr` | `Cond Expr`, `Then *BlockStmt`, `Else Stmt` (nil, `*BlockStmt`, or a nested `*IfExpr` for `else if`) |
| `WhileExpr` | `Cond Expr`, `Body *BlockStmt` |
| `ForExpr` | `KeyVar string`, `ValVar string` (may be ""), `Iter Expr`, `Body *BlockStmt` |
| `MatchExpr` | `Subject Expr` (nil for the subject-less form), `Arms []MatchArm` |
| `TryExpr` | `X Expr`, `Var string` (may be ""), `Fallback Expr` (nil with no `else`), `Ensure *BlockStmt` (nil with no `ensure`) — `X` and `Fallback` hold a `*BlockStmt` in the braced form (§8.11) |

**Expressions**

| Node | Fields |
|---|---|
| `NilLit` | — |
| `BoolLit` | `Value bool` |
| `IntLit` | `Value int64` |
| `FloatLit` | `Value float64` |
| `StrLit` | `Parts []StrPart` — each part is `{Text string}` or `{Expr Expr}`; a literal string has one text part |
| `RegexLit` | `Pattern string`, `Flags string`, `compiled *rx.Regexp` (filled at compile time) |
| `ArrayLit` | `Elems []Expr` |
| `DictLit` | `Keys []Expr`, `Vals []Expr` (parallel, insertion order) |
| `Ident` | `Name string` |
| `GlobalVar` | `Name string` (includes the `$`) |
| `UnaryExpr` | `Op token.Kind`, `X Expr` |
| `BinaryExpr` | `Op token.Kind`, `L, R Expr` — arithmetic, comparison, match, and `in` (§8.5) |
| `LogicalExpr` | `Op token.Kind` (`&&`, `\|\|`, `??`), `L, R Expr` — short-circuit |
| `TernaryExpr` | `Cond, Then, Else Expr` |
| `RangeExpr` | `Lo, Hi Expr`, `Exclusive bool` (set by `..<`) |
| `AssignExpr` | `Target Expr` (`*Ident`, `*GlobalVar`, `*IndexExpr`), `Op token.Kind` (`=`, `:=`, `+=`, …), `Value Expr` |
| `ArrayPattern` | `Elems []Expr`, `Brackets bool` — one entry per position; a target or a nested pattern on the left of `=`, a binding name or a compared expression in a `match` arm (§8.15) |
| `DestructureAssign` | `Pattern *ArrayPattern`, `Op token.Kind` (`=` or `:=`), `Value Expr` |
| `IndexExpr` | `X Expr`, `Index Expr`, `Index2 Expr` (nil unless `a[i, n]`) |
| `CallExpr` | `Fn Expr`, `Args []Expr`, `Named []NamedArg` (the `name = value` arguments, §8.7) |
| `MethodCall` | `Recv Expr`, `Name string`, `Args []Expr`, `Named []NamedArg`, `Safe bool` (`?.`) |
| `FuncLit` | `Params []Param`, `Body *BlockStmt`, `ImplicitIt bool` |
| `GroupExpr` | `Stmts []Stmt` — `( a; b; c )`, value is the last |

**Support types**

```go
type Param struct { Name string; Default Expr; Rest bool; Pos token.Pos }
type NamedArg struct { Name string; Value Expr; NamePos token.Pos }  // `name = v` at a call (§8.7)
type StrPart struct { Text string; Expr Expr }   // exactly one of the two is set

type ArmKind uint8
const (
    ArmValue ArmKind = iota // equality, or match when the pattern is a regex
    ArmIn                   // `in expr`
    ArmGuard                // `if expr`
    ArmElse                 // `else`
    ArmArray                // `[x, y]` — an array pattern, the one arm form that binds
)

type MatchArm struct {
    Kind  ArmKind
    Pats  []Expr      // ArmValue / ArmIn: more than one means "or"; ArmArray: exactly one
    Guard Expr        // the arm's `if` condition; may be nil
    Body  *BlockStmt
    Pos   token.Pos
}
```

A trailing closure is **not** a separate field: the parser appends it to `Args`. There is no
`Block`, no `BlockRef` and no `IsBlock` anywhere in the AST, because there is no block
concept in the language (§4.1).

### 6.2 Desugarings performed by the parser

| Surface syntax | AST produced |
|---|---|
| `else if` | nested `IfExpr` in `Else` |
| `{ B }` in body position | `BlockStmt` — evaluated immediately, in its own scope |
| `{ B }` in expression position | `FuncLit{ImplicitIt: true, Params: [it]}` |
| `{ (x) -> B }` | `FuncLit{Params: [x]}` |
| `{ B }` following a call | appended to that call's `Args` |
| `[1, 2]` | `ArrayLit` |
| `{a: 1}` | `DictLit` with `StrLit"a"` as the key |
| `{}` | `DictLit{}` |
| `a..<b` | `RangeExpr{Exclusive: true}` |
| `x if c` | `IfExpr{Cond: c, Then: BlockStmt{x}}` |
| `x while c` | `WhileExpr{Cond: c, Body: BlockStmt{x}}` |
| `try X else Y`, `try { … } ensure { … }` | `TryExpr` — the braced clauses are `BlockStmt`s, not closures |
| `a += b` | `AssignExpr{Op: PLUS_EQ}` (evaluator does read-modify-write; target evaluated once) |
| `a \|\|= b` | `AssignExpr{Op: OR_EQ}` — `b` evaluated only if `a` is falsy |
| `a ??= b` | `AssignExpr{Op: NIL_EQ}` — `b` evaluated only if `a` is nil |
| `"a${b}c$d"` | `StrLit{Parts: [text "a", expr b, text "c", expr GlobalVar("$d")]}` |
| `f(1, b = 2)` | `CallExpr{Args: [1], Named: [{b, 2}]}` — the name binds a parameter at the call (§8.7) |
| `a in xs` | `BinaryExpr{Op: KW_IN}` — evaluated as `xs.has(a)`, always a Bool (§8.5) |
| `x.foo` (no parens) | `MethodCall{Args: nil}` |
| `a, b = xs`, `[a, b] = xs` | `DestructureAssign{Pattern: ArrayPattern{[a, b]}}` (§8.15) |
| `[x, y] ->` in a `match` arm | `MatchArm{Kind: ArmArray, Pats: [ArrayPattern{[x, y]}]}` |

### 6.3 Compile step

`Compile` walks the AST once after parsing and:

1. compiles every `RegexLit` (errors surface at compile time, not at eval time);
2. resolves UFCS: every `MethodCall` whose name is not in the receiver-kind method table is
   rewritten to a `CallExpr` on the named function, or reported as `undefined method` with a
   did-you-mean suggestion drawn from the rename table of §19.2;
3. resolves local variable slots per scope (a `map[string]int` per scope, so the evaluator
   uses slice indices, not map lookups, in the hot path);
4. marks each `Ident` as one of `LocalRef` / `GlobalRef` / `FuncRef`, and reports anything
   left over as `undefined variable`;
5. constant-folds literal arithmetic and string concatenation;
6. rejects `==`/`!=` with a `RegexLit` operand (D5), which is the one type error catchable
   statically and the most likely paste mistake;
7. records the maximum frame size for pre-allocation.

`Compile` is pure and its output (`*Program`) is **immutable and safe for concurrent use by
many goroutines**. All mutable state lives in the per-`Run` frame.

---
## 7. Values and the object model

### 7.1 The nine kinds

| Kind | Go representation | Literal | Notes |
|---|---|---|---|
| `KNil` | — | `nil` | falsy |
| `KBool` | `bool` | `true` / `false` | `false` is the only falsy non-nil |
| `KInt` | `int64` | `42`, `0xff` | overflow promotes to `KFloat` |
| `KFloat` | `float64` | `1.2`, `1e9` | |
| `KString` | `string` (UTF-8, immutable) | `"a"`, `'a'` | rune-indexed |
| `KRegex` | `*rx.Regexp` | `/re/i` | immutable, shared |
| `KArray` | `*[]Value` | `[1,2]` | reference semantics, mutable |
| `KDict` | `*OrderedDict` | `{a: 1}` | insertion-ordered, reference semantics, mutable |
| `KFunc` | `*Func` | `fn f(…) { … }`, `{ (x) -> … }` | closures capture by reference |

`Value` is a **struct**, not an interface, to keep the hot path allocation-free:

```go
type Value struct {
    k   Kind
    n   int64          // KInt; KBool (0/1); KFloat via math.Float64bits
    s   string         // KString
    ptr unsafe.Pointer // KRegex/KArray/KDict/KFunc (implementers may use `any` instead;
}                      //  the exported API is identical either way)
```

Implementers MAY use `p any` instead of `unsafe.Pointer`; the exported API in §13 is
normative, the layout is not. What **is** normative: `Value` is comparable-by-copy for
nil/bool/int/float/string without allocation, and passing a `Value` never allocates.

### 7.2 Type names (`type(x)`)

`"nil" "bool" "int" "float" "string" "regex" "array" "dict" "function"`, plus the two
internal kinds the language can produce: `"time"` (§12.8) and `"task"` (§8.14).

The data structure is called a **dict**, never a "map" — `map` is the higher-order function
(§12.3), and one name may not mean two things (D17).

### 7.3 Truthiness (D6)

`nil` and `false` are falsy. **Everything else is truthy**, including `0`, `0.0`, `""`,
`[]`, `{}`, and `NaN`. It differs from Go, Lua, Python and JavaScript — do not "fix" it.
It is why `s.index(/re/)` returning `0` (a match at position 0) is still truthy.

### 7.4 Equality

`==` compares by kind and value:

* `nil == nil` → true; `nil == anything-else` → false.
* Int vs Float: compared numerically (`1 == 1.0` → true).
* String vs String: byte-exact (no case folding, no normalisation).
* Array/Dict: deep structural equality, element order significant for arrays, insertion
  order **in**significant for dicts.
* Func: identity.
* Regex on exactly one side: a **compile error** — use `~` (D5). If the regex only becomes
  known at runtime, `==` compares two regexes by source+flags and is `false` against any
  other kind.
* Number vs String: `false`. There is no coercion (§9.1).
* Every other cross-kind comparison → false (never an error).

`!=` is `!(a == b)`. There is no `===`.

### 7.5 Ordering

`<`, `<=`, `>`, `>=`, `<=>` are defined for: Int/Float (numeric, mixed allowed), String
(lexicographic by UTF-8 code point), Array (element-wise, then by length), and Bool
(`false < true`). `<=>` returns `-1|0|1`, or `nil` for incomparable operands.

Mixed number/string ordering is an **error**: `cannot compare string with int`. Convert
explicitly with `.int` or `.float` (§9.1).

### 7.6 Hashing / dict keys

Keys may be `nil`, `bool`, `int`, `float`, `string`, or `regex` (hashed by
source+flags). `1` and `1.0` are the **same** key (normalised to Int when integral).
Array/Dict/Func keys are an error: `dict key must be hashable, got array`.

A literal of any of those kinds is a key in the literal syntax too, written with `->`
(§3.12); `set` and a computed key `(k):` take the rest. All three build the same dict:

```
{1 -> "A"}                      # the Int key 1
{(1): "A"}                      # the same key, computed
d = {}; d.set(1, "A"); d        # and the same again
```

The rendering is a separate question: `str`, `json` and the CLI print a dict as a JSON
object, and JSON has string keys only, so a key is written out as `str(key)` (§12.7). Two
keys that differ only in kind — `1` and `"1"` — are two entries that print alike.

`OrderedDict` layout:

```go
type OrderedDict struct {
    idx  map[dictKey]int
    keys []Value
    vals []Value
}
```

Deleting a key tombstones the slot; iteration skips tombstones; `len` is O(1).

### 7.7 Functions and closures

```go
type Func struct {
    Name    string
    Params  []Param
    Body    *ast.BlockStmt
    Env     *Env          // captured lexical environment (by reference)
    Host    HostFunc      // non-nil for host/builtin functions
    Arity   int           // -1 for variadic
    Lambda  bool           // true for a `{ … }` literal, false for a `fn`, named or not
}
```

Closures capture their defining `Env` **by reference**: a function that assigns to an outer
local mutates the outer local.

Arity: calling with too few arguments fills the missing parameters with their defaults; a
missing argument with no default is `ArgumentError: f expects 2 arguments, got 1`. Extra
arguments are an error unless the function has a `*rest` parameter.

A `fn` is checked whether or not it has a name: the anonymous form of §4.1 is the same
value in every respect but the binding it does not make.

**Closure literals are exempt from the arity check**, because they are handed to library
functions that decide how many values to pass: extra values are dropped and missing ones
become `nil`. That is what lets `dict.each { (k, v) -> … }` and `dict.each { it }` both work.
A closure literal invoked as a body (`if`, `while`, `for`, a `match` arm) additionally
propagates `return`/`break`/`next` to its enclosing function or loop (§8.10).

---

## 8. Evaluation semantics

### 8.1 Program result

Evaluating a `Program` evaluates its statements in order; the program's value is the value of
the **last statement executed**. An empty program evaluates to `nil`. A `return` at top level
ends the program with that value.

### 8.2 Scopes and environments

* One `Env` per closure invocation. `Env` chains to its parent.
* **Every `{ … }` is a closure, so every `{ … }` is a scope** (D2). A variable first created
  inside an `if`, `while`, `for` or `match` body is not visible after it.
* `=` assigns to an existing binding found anywhere in the chain; if none exists it
  **creates** the binding in the current scope. So `x = 0; if c { x = 1 }; x` sees the
  assignment, while `if c { y = 1 }; y` is `undefined variable 'y'`.
* `:=` always **creates or shadows** a binding in the current scope.
* `$name` never resolves through the chain: it reads and writes the **globals table** of the
  interpreter run (§10).
* Top-level `fn` declarations are hoisted: they are bound before the first statement runs, so
  `f(1,2)` may appear above `fn f(a, b) { … }`.

### 8.3 Arithmetic

| Expr | Rule |
|---|---|
| `Int op Int` | Int arithmetic; `+ - *` promote to Float on overflow; `/` truncates toward zero; `%` takes the sign of the **divisor**, i.e. `-7 % 3 == 2` |
| any Float | Float arithmetic |
| `x / 0` (Int) | error `divided by 0` |
| `x / 0.0` | `±Inf` / `NaN` (IEEE, no error) |
| `Str + Str` | concatenation |
| `Arr + Arr` | new array, concatenation |
| `Dict + Dict` | new dict, right side wins |
| `Str * Int` | repetition (`"ab" * 3`) |
| `Arr * Int` | repetition |
| `Str % (Arr\|Value)` | `format` (§12.7) |
| `Arr - Arr` | set difference (order preserved) |
| `Str + Number`, `Number + Str` | **error** `cannot add int to string` — convert explicitly (§9.1) |
| `**` | Int**Int→Int (negative exponent → Float); otherwise Float |
| unary `-` | Int/Float only; error otherwise |

### 8.4 Match operators

Let `R` be a regex and `S` a string.

| Expr | Result |
|---|---|
| `S ~ R`, `R ~ S` | `true` if the pattern matches, else `false` |
| `S !~ R`, `R !~ S` | `!(S ~ R)` |
| `S.index(R)` | Int **rune** index of the first match, or `nil` |
| `S.captures(R)` | `nil`, or an Array whose element 0 is the whole match and 1..n the groups |
| `S.matches(R)` | every match; with groups, an array of group-arrays |
| `S == R` | **compile error** when either side is a regex literal (D5, §6.3) |

A `captures` result is an ordinary Array — every array method, `json`, `==` and `dup` treat
it as one — except that it additionally carries the pattern's group names, so a named group
is reachable as `m["name"]`. This is the **only** value for which a string subscript on an
Array is legal; `[1,2]["k"]` remains `array index must be an int, got string` (§8.8).

`~` with a non-string, non-regex operand is an error; there is no `str` coercion.
`nil ~ R` is `false`. Matching sets no side-channel variables (`$~`, `$1` do not exist).

### 8.5 Logical operators

`&&` returns the left operand if it is falsy, else the right operand. `||` returns the left
operand if it is truthy, else the right operand. `??` returns the left operand unless it is
`nil`, in which case it returns the right one — note that `false ?? x` is `false` while
`false || x` is `x`. All three short-circuit. `!` always returns a Bool.

`a ||= b` assigns only if `a` is currently falsy; `a &&= b` only if it is truthy; `a ??= b`
only if it is `nil`. In each case the right side is not evaluated otherwise. An undefined
local on the left of `||=`/`??=` is treated as `nil`, not as an error.

**`in`** asks the right operand whether it holds the left one, and always returns a Bool:

```
if code in 200..<300 { … }
if name in ["да", "yes"] { … }
if "key" in order { … }             # a dict answers about its keys
if "вет" in "привет" { … }          # a string about its substrings
ready = flag in allowed             # an ordinary value, like any other expression
```

It is the `in` of a `match` arm written infix (§5.3) and it is answered the same way — by
dispatching `has` on the right operand — so `x in xs` and `xs.has(x)` are one operation
under two spellings, and a kind that grows a `has` row grows `in` with it (I6, D18). A
right operand of a kind that answers membership nowhere is an error naming `in`, not the
`has` the source never wrote.

Both operands are evaluated, left first; `in` does not short-circuit and there is no `not
in` — the negation is `!(x in xs)`.

### 8.6 Ranges

`a..b` (inclusive) and `a..<b` (exclusive) over Int endpoints produce a lazy `Range`, an
Array-like iterable exposing `each`, `map`, `filter`, `array`, `has`, `first`, `last`, `len`,
`sum`, `min`, `max`, `step`, `each_slice`, `reverse`, `reduce`. String ranges are not
supported. A descending range (`5..1`) is empty. A Range is materialised to an Array whenever
an array-only method is called; materialising a range longer than `Options.MaxCollection`
(default 1e6) is an error.

### 8.7 Calls

Argument evaluation is strictly **left to right**.

**Named arguments.** An argument written `name = value` binds the callee's parameter of
that name instead of the next free position. **Named arguments come last** — every
positional argument precedes every named one — and that single rule is what the rest of
this section is made of:

```
fn area(w, h = 2, unit = "cm") { "${w * h} ${unit}²" }

area(3)                    # "6 cm²"
area(3, 5)                 # "15 cm²"
area(3, unit = "m")        # "6 m²"      — the defaulted `h` is skipped, not shifted
area(h = 5, w = 3)         # "15 cm²"    — every argument may be named, in any order
```

The rules, and where each is caught:

| Rule | Diagnosed |
|---|---|
| Named arguments follow every positional one | parse time (§5.6) |
| A name is given at most once | parse time (§5.6) |
| The name must be a parameter of the callee | run time, `has no parameter named 'z'` |
| A parameter filled by position may not also be named | run time, `got two values for parameter 'a'` |
| A `*rest` parameter collects positions and cannot be named | run time |
| A parameter no rule reached is the arity error, by name | run time |

A default is an ordinary expression evaluated **at each call**, never once when the
declaration is read, so `fn f(xs = [])` hands out a fresh array every time rather than one
array that accumulates. Binding runs in declaration order, so a default may also read a
parameter bound before it — by position or by name alike: `fn f(a = 1, b = a * 2)` called
as `f(a = 5)` gives `b` the value 10.

Only a script function has parameter names. A builtin, a host function and a stdlib method
take their arguments by position, so a name there is an error rather than a guess — which
is also what makes `xs.map(f = …)` say something useful instead of binding nothing.

`f(a: 1)` is **not** a call form: `:` builds a dict, and a dict argument is written
`f({a: 1})` (§3.11, §5.6). Nor is `f(x = 5)` an assignment any more — write `f((x = 5))`
when the assignment is the point.

**Order.** Left to right across both halves: positional arguments first, then named ones,
which *is* source order because a positional argument may not follow a named one. A
trailing closure is an ordinary last argument (§4.2) and is evaluated — that is,
constructed as a `KFunc` — in that same order.

That last rule is why a trailing closure may not be combined with a named argument at all.
The closure is positional and is written *after* the parentheses, so "a trailing closure
is the last argument" and "a positional argument may not follow a named one" would name
different parameters and the call would have no single reading. It is refused instead
(§5.6), and both unambiguous spellings stay available:

```
fn retry(times = 1, body) { … }

retry(3) { … }                  # every argument by position, closure included
retry(times = 3, body = { … })  # every argument by name
retry(times = 3) { … }          # ERROR: a trailing closure cannot follow a named argument
```

Dispatch for `recv.name(args)` is UFCS (§4.3), resolved at compile time (§6.3):

1. the stdlib method table for `recv`'s kind (§12) — an exact name match;
2. a function named `name` visible in lexical scope → `name(recv, args…)`;
3. otherwise `undefined method 'name' for string`, with a did-you-mean suggestion.

A Dict never dispatches `.` to its own keys: dict values are read with `[]`, `.get` or
`.dig`, never with `.`. This keeps UFCS unambiguous.

`?.` short-circuits: if `recv` is `nil` the whole postfix chain is `nil` and the arguments are
**not** evaluated.

Calling a non-function (`5(…)`) is `not a function: int`.

### 8.8 Indexing

| Receiver | `x[i]` | `x[i] = v` | `x[i, n]` |
|---|---|---|---|
| String | rune at `i` as a 1-rune String; negative `i` counts from the end; out of range → `nil` | error (immutable) | substring of `n` runes |
| Array | element; negative from end; out of range → `nil` | sets, extending with `nil`s if needed | sub-array |
| Dict | value for key, `nil` if absent | sets (inserting at the end if new) | error |
| Regex | error | error | error |
| nil | error `cannot index nil` | error | — |

`x[i] += 1` works: the target is evaluated once.

Indexing `nil` is an error rather than `nil`, because a silently-nil chain hides the real
failure. Use `?.get(k)` for one nil-safe step or `.dig(k1, k2, …)` for a nil-safe path — the
latter is the idiom for digging through parsed JSON (§16.1 rows 38–39).

### 8.9 Closures and `it`

A closure literal with no parameter list implicitly declares one parameter named `it`
(§4.1). These are equivalent, and the first is the one-liner form:

```
xs.map { it * 2 }
xs.map { (x) -> x * 2 }
xs.map(double)              # where double = { (x) -> x * 2 }
```

`it` is an ordinary local inside the closure and is shadowed by an explicit parameter.

### 8.10 `return`, `break`, `next`

* `return v` unwinds to the nearest enclosing **named function** (`fn`). Inside a closure
  literal, `return` returns from the function that lexically encloses it; at top level it
  ends the program.
* `next v` ends the current closure invocation or loop iteration with value `v`.
* `break v` ends the nearest enclosing loop **or** the call the closure was passed to, so
  `xs.each { break 1 }` evaluates to `1`.
* All three are implemented as sentinel control values returned up the eval chain (a `ctrl`
  field on the evaluator or a typed error), **never** as Go panics, so cost stays predictable
  and the step budget stays accurate.

### 8.11 Errors, `raise`, `try`, `ensure`

* Runtime errors (undefined method, type error, division by zero, index type errors, budget
  exhaustion) and a `raise` are the same kind of value.
* `try X else Y` evaluates `Y` if `X` raises. `try X else (e) -> Y` additionally binds `e` to
  a Dict `{message: …, kind: …, line: …}` while `Y` is evaluated; `data` is there too when
  the error carries a payload.
* `try` catches **script errors only**. It does **not** catch timeout, step-budget,
  depth-limit, context cancellation or `exit` (§12.1); those propagate to the host.
* Errors carry the script name, line, column and a short call stack.

**The braced form.** `try` and `else` take either an expression or a block; `ensure`
takes a block and nothing else, since its value is discarded either way:

```
try { a; b } else { "-" }
try { a } else (e) { e["message"] }         # before a brace the binder needs no arrow
try { take() } ensure { release() }
try { … } else { … } ensure { … }
```

A block is a body and not a value: it holds statements, its value is its last statement
(§8.1), and its braces are a scope like every other pair (§8.2) — a name first bound inside
one does not outlive it, which is the only difference from the `try (a; b)` grouping that
predates it. §3.12 is unaffected and runs first, so `try {a: 1} else 0` and `try {} else 0`
are the dict operands they always were. A braced clause may not open a header, where the
brace is already the body's (§3.11): write `if (try { … } else { … }) { … }`.

**`ensure`.** The clause runs on every way out of the `try` that leaves the Run alive:

| Leaving by | `ensure` runs | Then |
|---|---|---|
| the body's value | yes | the value is the body's; the `ensure`'s own value is discarded |
| a caught error | yes, after the `else` | the value is the `else`'s |
| an uncaught error (no `else`, or an error the `else` re-raised) | yes | the error keeps unwinding |
| `return`, `break`, `next` out of the body | yes | the signal keeps travelling |
| a limit, a cancellation, `exit` | **no** | the Run is over |

`ensure` does not catch: `try { … } ensure { … }` with no `else` releases and lets the
failure through. Nor does it survive what `try` cannot catch — a timeout, the step budget,
the depth limit, a cancelled context and `exit` end the Run, and running script code after
that is exactly what the limit forbids (§14.1). An `ensure` that leaves by any way of its
own — a raise, a `return`, a `break` — replaces whatever was pending: a release that itself
broke is not something to swallow, and a release that decides where control goes has said
so last.
`e` is not in scope in an `ensure`; the binder belongs to the `else`.

**Kinds.** Every error carries a `kind` from the closed list of §13.5 —
`syntax`, `name`, `type`, `argument`, `index`, `key`, `zero-division`, `regex`, `json`,
`http`, `io`, `raise`, `limit`, `exit`, `internal` — stamped where the failure is born, so a
handler decides on a value rather than on the wording of a message:

```
try f() else (e) {
  match e["kind"] {
    "json" -> "bad payload"
    "io"   -> "no file"
    else   -> raise(e)
  }
}
```

**`raise`.** `raise("msg")` raises with kind `raise`. `raise("msg", kind)` names the kind
instead; a script may invent any name — `"user"`, `"billing"` — except the four the runtime
keeps for itself, `syntax`, `limit`, `exit` and `internal`, each of which is a claim only
the runtime can make truthfully (§13.5). Raising one of those, or an empty kind, is an
`argument` error.

`raise(dict)` reads three keys — `message`, `kind` and `data` — and is the spelling for an
error with a payload:

```
raise({message: "insufficient funds", kind: "billing", data: {short_by: 30}})
```

A dict that names neither a `kind` nor a `data` key is a payload and is carried whole as
`data`, with its JSON as the message: `raise({code: "limit", id: o})` means what it always
did.

**Re-raising.** `raise(e)`, where `e` is the dict an `else (e)` bound, keeps the file, line
and call stack of the *original* failure — the arm that does not know what to do passes the
error on, and the diagnostic still names the line that broke rather than the handler that
declined it. `message`, `kind` and `data` are read from the dict as it stands, so editing it
before re-raising works. A dict built by hand has no such origin and is positioned at the
`raise`, like any other value.

### 8.12 String interpolation

`"$name"` reads the global `$name`; `"${e}"` evaluates `e` (§3.7). Both convert the result
with `str` (§12.7): `nil` → `""`, Bool → `"true"`/`"false"`, Int → decimal, Float → shortest
round-trip decimal (never an exponent for |x| < 1e21, so `1.5` → `"1.5"` and `2.0` → `"2.0"`),
String → itself, Array/Dict → JSON, Regex → `/src/flags`, Func → `#<fn name>`.

This is the only place where a conversion is implicit.

### 8.13 Determinism

Given the same source, same `$vars`, same host functions and the same `Options`, evaluation
is deterministic. Dict iteration is insertion-ordered. No hash-order dependence anywhere.
`rand`/`now`/`uuid` are only available when the host installs them (§14.3). A program that
starts tasks (§8.14) orders their interleaving no further than that section says.

### 8.14 `async fn` and tasks (D19)

```
async fn fetch(u) { http.get(u)["body"] }

a = fetch(u1)          # started; the request is on its way
b = fetch(u2)          # started too, alongside the first
[a.await, b.await]     # …and this is where they are read
```

Calling an `async fn` **starts a task** and returns it; it never runs the body to a value
the way `fn` does. A task is an opaque value (`type(t) == "task"`) that answers exactly two
names, `await` and `done` (§12.12); by UFCS both are also `await(t)` and `done(t)` (D18),
and every other name is `undefined method 'x' for task`.

`async` is positional, not a keyword (§3.5), and it is legal in exactly one place: directly
before a `fn`. Before a named one it is a declaration, and `export async fn f(…)` is that
declaration exported (§12.8); before an anonymous one it is a value, `f = async fn(u) { … }`
(§4.1), and calling it starts a task exactly as calling the named form does. There is no
`async` closure literal: `{ … }` is a closure and stays one.

**The body.** It is an ordinary function body: same scope rules, same frame, same hoisting,
same arity checks, and `return` ends it with a value. Default arguments are evaluated by the
*caller*, before the task starts, because they are the caller's code.

**One evaluator at a time.** A task is a goroutine, and a Run holds one lock that whichever
goroutine is evaluating must hold. A task releases it at exactly three points:

1. at `await`, while waiting for another task;
2. inside a blocking host call — `http.get`/`post`/`request` are the ones in the box
   (§12.11), and `Ctx.Blocking` is how a host adds its own;
3. when it finishes.

So two tasks never touch one Array, one Dict or one `$var` at the same time: **a script
cannot write a data race**, and mzs values need no locks. What overlaps is the waiting, which
is the point — N slow requests in N tasks cost about one of them.

Nothing is preemptive: a task that only computes runs to its end once it holds the lock.
Which *runnable* task takes over at a release point is unspecified, so two tasks that both
`println` may print in either order. Code outside a task is as ordered as it ever was.

**Limits.** One Run, one budget: the steps a task spends are the Run's steps and the
deadline is the Run's deadline (§14.1). Waiting honours both, so a task awaiting one that can
never finish — a cycle, or a task the host cancelled — ends on the clock rather than never.
`Options.MaxTasks` (default 64) bounds how many tasks of a Run may be unfinished at once;
exceeding it is a limit error at the call, and `MaxTasks: -1` refuses `async` calls outright.

**The end of a Run.** A Run is over when the program *and* its tasks are over: a task that
nobody awaited still runs, because `notify(user)` on the last line is a use, not a mistake.
That waiting is bounded by the same deadline and context; what is still going when they run
out is cancelled and joined. **No goroutine outlives the `Run` that made it** (A5), so
`Result.Globals` is handed back with nothing still writing to it. A failure in a task nobody
awaited is written to `Options.Stderr` — never into the value, never silently.

**Errors.** They surface at the `await`, with the position and the call stack of where they
happened, and are catchable there like any other (`try t.await else …`, §8.11). Limit errors
stay uncatchable wherever they are reached. A task that awaits itself is a named error, not a
hang.

### 8.15 Destructuring

One shape rule, three places to write it: an assignment, a `match` arm, and the
two-variable `for`.

```
a, b = pair                      # an array of exactly two
[a, b] = pair                    # the same thing, with the brackets written
a, b, c = 1..3                   # a range has positions too
[a, [b, c]] = [1, [2, 3]]        # nesting is recursive
name, price = [item["name"], item["price"]]  # a dict, taken apart by name
d["x"], $y = pair                # an index and a $var are targets like any other
a, b = [b, a]                    # a swap needs no temporary
```

* Only an **Array** or a **Range** has positions. A Dict does not: key order is insertion
  order (D11), not a positional contract, so `a, b = {x: 1, y: 2}` is a type error and
  taking a dict apart is spelled `d["x"], d["y"] = …` or `for k, v in d`.
* The lengths must be **equal**. Too few values and too many are both run-time errors —
  never a silent `nil` in the extra name, never a silently dropped value:

  | Input | Error |
  |---|---|
  | `a, b = [1, 2, 3]` | `index: destructuring expects 2 values, got 3` |
  | `a, b = [1]` | `index: destructuring expects 2 values, got 1` |
  | `a, b = 1` | `type: cannot destructure int: the right side must be an array` |
* A target is a name, a `$var`, an index, or a nested pattern; anything else is
  `cannot assign to this expression` at parse time.
* `=` and `:=` mean per position exactly what they mean alone (§8.2): `=` writes the
  nearest existing binding and creates one here when there is none, `:=` always creates
  or shadows here. No compound operator destructures — `a, b += xs` is a diagnostic.
* The value of the expression is the **right side**, as it is for `a = 1`; the positions
  are filled left to right, and the right side is evaluated — and read — exactly once,
  including when a target writes into the array being taken apart: `xs[1], xs[0] = xs`
  swaps in place.
* There is no rest element. `a, *rest` stays reserved together with `*splat` (§20).

The bare `a, b = …` form is a **statement**, not an expression: a comma at bracket depth
zero means something else inside a call, a collection, a `for` header and a `match` arm.
The bracketed form is an ordinary expression and works anywhere one does.

In a `match` arm the same shape asks a question instead of asserting one (§5.3): a subject
of the wrong kind or the wrong length does not raise, it moves to the next arm. That is
the whole difference between the two sides — `=` says "this is the shape", `match` asks
"is it?".

```
match order {
  [x, [y, z]] -> x + y + z
  [x, y]      -> x + y
  []          -> 0
  else        -> -1
}
```

`for k, v in xs` is the same rule with the pattern written in the loop header: every item
must be an array (or range) of exactly two. A Dict iterates as `[key, value]` pairs, which
is why `for k, v in d` reads the way it does; an item that is not a pair is an error, not
a `nil` in `v`.

---
## 9. One semantics — no modes

There is no strict mode, no compat mode and no `Options.Compat`. There is one lexer, one
grammar, one evaluator and one set of rules (G3). This section records what that costs and
why it is still the right trade.

### 9.1 Host values are strings; conversions are explicit

Values arriving from the bot engine (`$__sent`, `$price`, `$bot_check_attempts`, …) are
**strings**, because that is what the flow storage holds. mzs does not guess:

| Expression | Result |
|---|---|
| `$__sent == "yes"` | string comparison — works as written |
| `$n >= 2` where `$n` is `"3"` | **error** `cannot compare string with int` |
| `$n.int >= 2` | `true` |
| `$n + 1` where `$n` is `"2"` | **error** `cannot add int to string` |
| `$n.int + 1` | `3` |
| `"total: $n"` | `"total: 2"` — `str` conversion is implicit in interpolation only |

`.int` and `.float` never raise: `"".int == 0`, `"12abc".int == 12`, `"abc".int == 0` (§12.7).
This is what makes `$price.int + 1200` safe for an unset `$price`.

The previous draft of this document had a coercion mode that made `$n >= 2` work by guessing.
It was removed because guessing is only ever needed for input written against a different
language, and there is no longer any such input: the stored conditions are migrated once
(§19), and the migration inserts `.int` mechanically wherever a `$var` meets a number.

### 9.2 An unbound `$var` is `nil`

Reading a `$name` the host did not bind yields `nil` — not an error, and not its own source
text. `nil` is falsy, so an unbound variable in a condition means "no match", which is the
behaviour the bot engine wants. Writing `$name = v` inserts into the globals table.

### 9.3 An unresolved bare identifier is an error

`Compile` reports `undefined variable 'x'` for any identifier that is not a local, a
parameter, a declared `fn`, a builtin or a module. There is no bareword-to-string shim: a
plain-text answer template is not a program, and the host must not evaluate it as one. The
`String` entry point of §13.6 returns the literal text when compilation fails, which is
where that behaviour belongs.

---

## 10. `$variables` and host binding

`$name` is a first-class token and AST node (D7). At `Run` time the host passes
`map[string]Value` (or `map[string]string`, auto-lifted). Reads and writes go to that table.

```go
res, err := in.Eval(ctx, `$__sent.lower.trim == "оператор"`, map[string]mzs.Value{
    "$__sent": mzs.Str("  ОПЕРАТОР "),
})
```

Rules:

* `$name` is a **separate namespace** from local variables and is never resolved through the
  scope chain. A local named `sent` and a global named `$sent` are unrelated.
* An unbound `$name` reads as `nil` (§9.2).
* Writing `$name = v` inserts into the globals table; after `Run` the host reads the mutated
  table back via `Result.Globals` (this is how `set_var` blocks get their value).
* Keys may be given with or without the leading `$`; the interpreter normalises to the
  `$`-prefixed form.
* Binding is **per-Run**. Two concurrent dialogues never share globals; `*Program` is
  immutable and shared, `*Env` and the globals table are not.
* Inside a double-quoted string, `$name` interpolates the same global (§3.7). Inside a
  single-quoted string it is literal text.

Values are never parsed, only bound. A value containing spaces, quotes, an apostrophe
(`О'Брайен`), an emoji (`EN 🇬🇧`) or a newline cannot affect the parse, because it never
reaches the parser. There is no textual pre-substitution pass in mzs — the `Translate`
function of earlier drafts is gone, along with the entire class of bugs that
`$__sent.gsub(/'/, '')` was invented to work around.

---
## 11. Regular expressions

### 11.1 Syntax accepted

The full observed corpus subset, which is also the specified surface:

alternation `|`; grouping `( )`, non-capturing `(?: )`, named `(?<name> )`; character
classes `[…]` with ranges, negation, and Unicode contents (`[а-яА-Я]`, `[её]`); escapes
`\d \D \w \W \s \S \b \B \A \z \Z \n \r \t \\ \/ \. …`; Unicode classes `\p{L}`, `\p{Cyrillic}`;
quantifiers `* + ? {n} {n,} {n,m}` and their non-greedy `?` forms; anchors `^ $`;
lookahead `(?= )` `(?! )`; lookbehind `(?<= )` `(?<! )`; backreferences `\1`…`\9`;
inline flags `(?i)`, `(?i: … )`; flags `i m/s x u`.

`^`/`$` are **line** anchors (Ruby semantics), always.

### 11.2 Two backends, one interface

```go
package rx

type Regexp struct { /* opaque */ }
func Compile(pattern, flags string) (*Regexp, error)
func (r *Regexp) FindIndex(s string) (start, end int, ok bool)   // rune indices
func (r *Regexp) Match(s string) bool
func (r *Regexp) FindSubmatch(s string) ([]Group, bool)
func (r *Regexp) FindAll(s string, limit int) [][]Group
func (r *Regexp) Replace(s, repl string, all bool) string
func (r *Regexp) Split(s string, limit int) []string
func (r *Regexp) String() string
type Group struct { Start, End int; Text string; Name string; Ok bool }
```

Backend selection, decided at `Compile` time and fixed thereafter:

* **RE2 backend** (`regexp` from the stdlib) when the pattern contains **no** lookaround,
  **no** backreference, and **no** `\b`/`\B`. The pattern is translated: `(?m)` is always
  prepended (line anchors), `m`→`(?s)`, `i`→`(?i)`, `x` is expanded by stripping whitespace
  and `#`-comments outside classes, `\Z` → `(?:\n?\z)`, `\z`/`\A` pass through.
* **Backtracking backend** (bundled, `internal/rx/bt`) otherwise. It is a straightforward
  recursive matcher over a parsed pattern tree with:
  * **Unicode-aware `\b`**: a boundary between a word rune (`\p{L}`, `\p{N}`, `_`) and a
    non-word rune or a string edge. This is mandatory: `\bменю\b`, `\bеда\b`, `\bмрп\b` all
    ship today and Go's ASCII-only `\b` silently never matches them.
  * a **step budget** (`Options.RegexSteps`, default 200 000 per match) and memoisation of
    `(node, pos)` pairs to bound catastrophic backtracking. Exceeding the budget is an
    error, not a hang.
  * case-insensitive matching via `unicode.SimpleFold`, so `И`/`и` fold correctly.

Both backends MUST agree on every pattern in §16.2 (a cross-check test compares the two on
the RE2-safe subset).

### 11.3 Indices

All regex results use **rune** indices, so `index` returns a rune index. This matters
because `"привет".index(/вет/)` must be `3`, not `6`.

### 11.4 Compilation and caching

`RegexLit` nodes compile once at `Compile` time. `regex(str, flags)` and methods taking a
string pattern compile through a bounded LRU cache (`Options.RegexCacheSize`, default 256)
shared per `*Interp` and guarded by a mutex.

### 11.5 The `\\b` gotcha

`main.mzs` line 12 contains **literal double backslashes** (`\\bеда\\b`, verified in hex).
In Ruby and in mzs, `\\b` inside a `/…/` literal matches a backslash followed by `b` — not a
word boundary. mzs does not "fix" this silently. Instead:

* the regex compiler emits a **warning diagnostic** (`Program.Warnings`) for any `\\b`,
  `\\d`, `\\w`, `\\s` inside a regex literal: `"\\\\b" matches a literal backslash; did you
  mean "\\b"? (pattern probably came from a JSON string)`;
* `mzs --check file.mzs` prints warnings; `Options.StrictWarnings` promotes them to errors.

---
## 12. Standard library

Conventions used in the tables:

* Signature notation: `name(arg: type = default) -> type`. `any` is any Value.
* `str` receivers are rune-based. Negative indices count from the end.
* **There are no aliases** (D17). Every operation has exactly one name. A name that used to
  exist under a different spelling produces a did-you-mean diagnostic (§5.6, §19.2).
* Because of UFCS (D18) there is **one namespace**, not a global-function namespace plus a
  per-kind method namespace. Every row below is callable both ways: `len(s)` ≡ `s.len`,
  `json(x)` ≡ `x.json`, `filter(xs, f)` ≡ `xs.filter(f)`. The tables are grouped by the kind
  of the first argument purely for readability.
* Every function raises `wrong argument type` on a type mismatch. There is no coercion (§9.1),
  except where a row explicitly says otherwise.

### 12.1 Core — any first argument

| Name | Signature | Semantics | Example |
|---|---|---|---|
| `print` | `print(*args) -> nil` | writes `str` of each arg to `Options.Stdout`, no separator, no newline | `print(a)` |
| `println` | `println(*args) -> nil` | like `print` but appends `\n` after each arg; `println()` writes one `\n`; an Array arg prints one element per line | `println("hi")` |
| `debug` | `debug(*args) -> any` | writes the `inspect` form + `\n`; returns the first arg | `debug(x)` |
| `len` | `len(x) -> int` | rune length of a String, element count of Array/Dict/Range; `nil` → 0 | `s.len > 2` |
| `empty` | `empty(x) -> bool` | `len(x) == 0` | `xs.empty` |
| `type` | `type(x) -> string` | §7.2 names | `type(1) == "int"` |
| `is` | `is(x, name: string) -> bool` | kind test | `x.is("array")` |
| `str` | `str(x) -> string` | §12.7 | `str(1) == "1"` |
| `int` | `int(x) -> int` | §12.7, never raises | `"12abc".int == 12` |
| `float` | `float(x) -> float` | §12.7 | `"1.5".float` |
| `bool` | `bool(x) -> bool` | truthiness of `x` | `"".bool == true` |
| `array` | `array(x) -> array` | Array→itself, Range→materialise, Dict→`[[k,v],…]`, nil→`[]`, else `[x]` | `(1..3).array` |
| `dict` | `dict(x) -> dict` | from an Array of `[k,v]` pairs; Dict→itself | `[[1,2]].dict` |
| `json` | `json(x) -> string` | compact JSON, keys in insertion order; under `include json` only the method spelling (§12.8) | `{a: 1}.json` |
| `inspect` | `inspect(x) -> string` | §12.7 | |
| `hash` | `hash(x) -> int` | FNV-1a, stable across runs | |
| `dup` | `dup(x) -> any` | shallow copy for Array/Dict, identity otherwise | |
| `tap` | `tap(x) { (v) -> … } -> any` | runs the closure, returns `x` | |
| `pipe` | `pipe(x) { (v) -> … } -> any` | runs the closure, returns **its** value | `x.pipe { it * 2 }` |
| `regex` | `regex(pattern: string, flags: string = "") -> regex` | compiles at runtime (cached) | `regex('\bменю', "i")` |
| `range` | `range(a: int, b: int = nil, step: int = 1) -> array` | half-open: `range(0,3) == [0,1,2]`, `range(3) == [0,1,2]` | `range(3)` |
| `sum` | `sum(xs: array) -> number` | numeric sum; empty → `0` | `[1,2].sum` |
| `min` / `max` | `(xs: array \| *args) -> any` | by `<=>`; empty → `nil` | `max(1,2,3)` |
| `abs` | `abs(x: number) -> number` | | `(-2).abs` |
| `round` | `round(x: number, digits: int = 0) -> number` | half away from zero; `digits == 0` → Int | `round(1.256, 2)` |
| `ceil` / `floor` | `(x: number, digits: int = 0) -> number` | | `1.2.ceil` |
| `sort` | `sort(xs: array) [{ (a, b) -> int }] -> array` | stable, new array | `xs.sort { (a,b) -> b <=> a }` |
| `format` | `format(fmt: string, *args) -> string` | §12.7 | `format("%.2f", x)` |
| `raise` | `raise(msg: any, kind: string = "raise") -> never` | raises a script error; a dict reads `message`/`kind`/`data`, and the dict an `else (e)` bound re-raises with its original position (§8.11) | `raise("bad")`, `raise("no funds", "billing")` |
| `exit` | `exit(code: int = 0) -> never` | ends the Run with that status; not catchable, and never touches the process (§13.5) | `exit(1)` |
| `assert` | `assert(cond: any, msg: string = "assertion failed") -> nil` | raises when falsy | |
| `defined` | `defined(name) -> bool` | true if the identifier or `$var` is bound (parser-level special form) | `defined($price)` |
| `rand` | `rand(n: int = 0) -> number` | **only if `Options.Rand` is set**, else `undefined function` | |
| `uuid` | `uuid() -> string` | **only if `Options.Rand` is set** | |
| `now` | `now() -> time` | **only if `Options.Now` is set** | |

### 12.2 Strings

| Method | Signature | Semantics | Example |
|---|---|---|---|
| `lower` | `-> string` | Unicode lowercase | `"ПРИВЕТ".lower == "привет"` |
| `upper` | `-> string` | Unicode uppercase | `"да".upper == "ДА"` |
| `capitalize` | `-> string` | first rune upper, rest lower | |
| `swapcase` | `-> string` | | |
| `trim` | `-> string` | trims Unicode whitespace **and NBSP (U+00A0), U+200B, U+FEFF** | `"  ОПЕРАТОР ".lower.trim` |
| `trim_start` / `trim_end` | `-> string` | | |
| `chomp` | `(suffix: string = "\n") -> string` | | |
| `chop` | `-> string` | drops the last rune | |
| `squeeze` | `(set: string = "") -> string` | collapses runs of repeated runes | |
| `len` | `-> int` | rune count | `s.len` |
| `empty` | `-> bool` | `len == 0` | |
| `blank` | `-> bool` | empty after `trim` | |
| `has` | `(sub: string) -> bool` | substring test | `"hello".has("lo")` |
| `starts_with` | `(*prefixes: string) -> bool` | any prefix matches | `s.starts_with("/")` |
| `ends_with` | `(*suffixes: string) -> bool` | | |
| `index` | `(needle: string \| regex, from: int = 0) -> int \| nil` | rune index | `s.index(/оператор/i)` |
| `last_index` | `(needle) -> int \| nil` | | |
| `count` | `(sub: string \| regex) -> int` | non-overlapping occurrences | |
| `split` | `(sep: string \| regex = " ", limit: int = -1) -> array` | `" "` splits on runs of whitespace; `""` splits into runes | `$__sent.split(":")[1]` |
| `replace` | `(pat: string \| regex, repl: string \| fn) -> string` | replaces **all**; `\1`/`\k<name>` in `repl`; a closure receives the match array **spread** | `s.replace(/'/, "")` |
| `replace_first` | `(pat, repl) -> string` | replaces the first occurrence | |
| `matches` | `(re: regex) -> array` | every match; with groups, an array of group-arrays | `d.matches(/(Mon\|Tue)/).first` |
| `captures` | `(re: regex) -> array \| nil` | element 0 is the whole match, 1..n the groups; named groups also reachable as `m["name"]` | |
| `chars` | `-> array` | one-rune strings | |
| `bytes` | `-> array` | integer byte values; `array.pack_bytes` (§12.3) puts them back | |
| `lines` | `-> array` | split on `\n`, terminator dropped | |
| `reverse` | `-> string` | rune-wise | |
| `first` / `last` | `(n: int = 1) -> string` | first/last `n` runes | |
| `first_and_last` | `-> string` | `first(1) + last(1)` | |
| `ljust` / `rjust` / `center` | `(width: int, pad: string = " ") -> string` | rune widths | |
| `slice` | `(i: int, n: int = 1) -> string` | same as `s[i, n]` | |
| `ord` | `-> int` | code point of the first rune | |
| `each_char` | `{ (c) -> … } -> string` | iterates, returns the receiver | |
| `%` (operator) | `(*args) -> string` | `"%s-%d" % ["a", 1]` | |

A closure passed to `replace`/`replace_first` receives the match array spread over its
parameters, following the convention of §7.7: `s.replace(/\d+/) { it.int + 1 }` gets the
matched text in `it`, and `s.replace(/(\w)(\w)/) { (m, a, b) -> b + a }` gets the match and
its groups. Passing the array as a single argument instead would make the one-liner form a
type error, which G1 does not allow.

Strings are immutable. There are no in-place string operations under any spelling.

### 12.3 Arrays

| Method | Signature | Semantics |
|---|---|---|
| `len` | `-> int` | |
| `empty` | `-> bool` | |
| `count` | `(v) \| { (x) -> … } -> int` | counts equal elements, or matches |
| `first` / `last` | `(n: int = nil) -> any \| array` | element, or first/last `n` |
| `push` | `(*v) -> array` | appends, mutates, returns the receiver |
| `pop` / `shift` | `-> any` | removes and returns; `nil` when empty |
| `unshift` | `(*v) -> array` | prepends |
| `insert` | `(i: int, *v) -> array` | |
| `delete_at` | `(i: int) -> any` | |
| `delete` | `(v: any) -> array` | removes all equal elements |
| `has` | `(v) -> bool` | uses `==` |
| `index` | `(v) \| { (x) -> … } -> int \| nil` | |
| `join` | `(sep: string = "") -> string` | `str` of each element |
| `map` | `{ (x) -> … } -> array` | |
| `each` | `{ (x) -> … } -> array` | returns the receiver |
| `each_with_index` | `{ (x, i) -> … } -> array` | |
| `each_slice` | `(n: int) [{ (chunk) -> … }] -> array` | without a closure returns an array of chunks |
| `each_cons` | `(n: int) -> array` | sliding windows |
| `filter` | `{ (x) -> … } -> array` | |
| `reject` | `{ (x) -> … } -> array` | |
| `find` | `{ (x) -> … } -> any \| nil` | |
| `any` / `all` / `none` | `[{ (x) -> … }] -> bool` | without a closure, tests truthiness |
| `reduce` | `(init: any = nil) { (acc, x) -> … } -> any` | |
| `sum` | `[{ (x) -> … }] -> number` | |
| `min` / `max` | `[{ (x) -> … }] -> any` | |
| `min_by` / `max_by` / `sort_by` / `group_by` / `partition` | `{ (x) -> … } -> …` | |
| `sort` | `[{ (a, b) -> int }] -> array` | stable; the closure returns a `<=>`-style int |
| `reverse` | `-> array` | |
| `uniq` | `[{ (x) -> … }] -> array` | preserves the first occurrence |
| `flatten` | `(depth: int = -1) -> array` | |
| `flat_map` | `{ (x) -> … } -> array` | |
| `dig` | `(*keys) -> any` | nested lookup through Arrays and Dicts, nil-safe at every step |
| `compact` | `-> array` | drops `nil`s |
| `tally` | `-> dict` | element → count |
| `slice` | `(i: int, n: int = 1) -> array` | |
| `take` / `drop` | `(n: int) -> array` | |
| `take_while` / `drop_while` | `{ (x) -> … } -> array` | |
| `zip` | `(*others: array) -> array` | |
| `pack_bytes` | `-> string` | the inverse of String#`bytes`: one element, one byte; each must be an int in `0..255` |
| `concat` | `(other: array) -> array` | mutates |
| `sample` / `shuffle` | `-> any \| array` | require `Options.Rand` |
| `sort_in_place` / `reverse_in_place` | `-> array` | the mutating variants, named so at the call site |

`pack_bytes` works in bytes, not runes, so it can build a string that is not valid UTF-8 —
the same thing `io.read` of a binary file produces (§12.13). Nothing raises: the rune-based
rows of §12.2 then see U+FFFD where a byte does not decode, and `json` escapes it the same
way. An element that is not an int in `0..255` **is** an error, and it names the index:
a byte silently truncated from 300 to 44 would surface as corruption much later.

### 12.4 Dicts

| Method | Signature | Semantics |
|---|---|---|
| `keys` / `values` | `-> array` | insertion order |
| `len` | `-> int` | |
| `empty` | `-> bool` | |
| `has` | `(k) -> bool` | key test |
| `has_val` | `(v) -> bool` | value test |
| `get` | `(k, default: any = nil) -> any` | `nil` (or the default) when absent |
| `fetch` | `(k) -> any` | raises `key` when absent (§13.5) |
| `set` | `(k, v) -> dict` | mutates, returns the receiver; inserts at the end when new |
| `delete` | `(k) -> any` | returns the removed value |
| `merge` | `(*others: dict) -> dict` | new dict, later wins |
| `merge_in_place` | `(*others: dict) -> dict` | mutating |
| `dig` | `(*keys) -> any` | nested lookup, nil-safe |
| `each` | `{ (k, v) -> … } -> dict` | |
| `map` | `{ (k, v) -> … } -> array` | |
| `filter` / `reject` | `{ (k, v) -> … } -> dict` | |
| `find` | `{ (k, v) -> … } -> array \| nil` | the `[k, v]` pair |
| `any` / `all` | `{ (k, v) -> … } -> bool` | |
| `invert` | `-> dict` | |
| `sort_by` | `{ (k, v) -> … } -> array` | |

### 12.5 Numbers (Int and Float)

| Method | Signature | Semantics |
|---|---|---|
| `int` | `-> int` | Float truncates toward zero |
| `float` | `-> float` | |
| `str` | `(base: int = 10) -> string` | |
| `abs` | `-> number` | |
| `round` / `ceil` / `floor` | `(digits: int = 0) -> number` | |
| `clamp` | `(lo, hi) -> number` | |
| `zero` / `positive` / `negative` | `-> bool` | |
| `even` / `odd` | `-> bool` | Int only |
| `times` | `[{ (i) -> … }] -> array` | `3.times` without a closure → `[0,1,2]` |
| `upto` / `downto` | `(n: int) [{ (i) -> … }] -> array` | |
| `step` | `(limit, by) [{ (i) -> … }] -> array` | |
| `pow` | `(e) -> number` | same as `**` |
| `chr` | `-> string` | code point → string |
| `days` / `hours` / `minutes` / `seconds` / `weeks` | `-> duration` | only with the `time` module installed (§12.8) |

**Bit operations.** Functions, not operators, and that is normative: `&` and `|` one
keystroke away from `&&` and `||` is the ambiguity D16 refuses to introduce, and `<<`/`>>`
are reserved (§20). Writing one of the operators is a diagnostic naming the function
(§5.6). Under UFCS each row is also a method, which is how they are meant to be read:
`flags.band(0xff)`.

| Name | Signature | Semantics |
|---|---|---|
| `band` / `bor` / `bxor` | `(a: int, b: int) -> int` | and, or, xor, bit for bit |
| `bnot` | `(a: int) -> int` | one's complement; `bnot(0) == -1` |
| `shl` | `(a: int, n: int) -> int` | left shift; bits past 63 are dropped |
| `shr` | `(a: int, n: int) -> int` | **arithmetic** right shift: the sign bit is copied in, so `shr(-8, 1) == -4` |
| `popcount` | `(a: int) -> int` | set bits in the two's-complement form; `popcount(-1) == 64` |
| `bit` | `(a: int, i: int) -> bool` | is bit `i` set; `i` outside `0..63` is an error |

These rows are **pure `int64` and never promote**. D9's rule that an overflowing Int
becomes a Float stops here: `shl(1, 63)` is the most negative int64 and `shl(1, 64)` is `0`,
because a bit shifted out is gone — that is what makes masks and checksums come out right.
Two consequences follow, and both are errors rather than guesses (§9.1, I5):

* a Float argument does not truncate — `2.9.band(1)` says so and points at `x.int`;
* a negative shift count is not a shift the other way — each direction has its own name.

A shift count above 63 saturates instead of wrapping: `shl` and `shr` of a non-negative
value give `0`, and `shr` of a negative value gives `-1`.

Building a number out of bytes is the pair `"…".bytes` (§12.2) and `array.pack_bytes`
(§12.3).

### 12.6 Regex

| Method | Signature | Semantics |
|---|---|---|
| `captures` | `(s: string) -> array \| nil` | §8.4 |
| `matches` | `(s: string) -> array` | every match |
| `index` | `(s: string) -> int \| nil` | rune index of the first match |
| `source` | `-> string` | the pattern text |
| `flags` | `-> string` | |
| `str` | `-> string` | `/src/flags` |

Matching itself is the `~` operator, not a method (D5).

### 12.7 Conversions and formatting

`str(x)`: `nil`→`""`, Bool→`"true"`/`"false"`, Int→decimal, Float→shortest round-trip
(always with a `.` or an exponent, so `2.0` → `"2.0"`), String→itself, Array/Dict→JSON,
Regex→`/src/flags`, Func→`#<fn name>`.

`inspect(x)`: like `str` but Strings are double-quoted and escaped, and `nil` → `"nil"`.

`int(x)`: Int→itself, Float→truncate, Bool→`1`/`0`, `nil`→`0`,
String→leading optional sign + digits (underscores allowed, leading whitespace skipped),
`""`→`0`, `"abc"`→`0`, `"12abc"`→`12`, `"0x1f"`→`0` (base 10 only).
**Never raises** — this is what makes `$price.int + 1200` safe when `$price` is unset.

`format` verbs: `%s %d %i %f %g %e %x %X %o %b %c %% %j` with the flags `- + 0 space #`,
width, `.precision`, and `%<name>s`/`%{name}` when the single argument is a Dict. `%j` emits
JSON. Implemented by delegating to `fmt.Sprintf` after verb translation; `%i`→`%d`,
`%j`→JSON string.

### 12.8 Modules, `include` and `export`

A module is an ordinary value reached with `.` like anything else (D18), and **nothing is
ambient**: a name becomes reachable only when the program says so.

```
include json                       # a built-in module of the table below
include time                       # …still gated by Options: EnableTime or it is an error
include cart from "./cart.mzs"     # another script, through Options.ModuleLoader
```

`include NAME` binds NAME in the scope the statement stands in, exactly like an
assignment; it is a statement, not a declaration that floats to the top. Using the name
above its include is `undefined variable`. Two things are checked before anything runs: a
built-in module must exist and be enabled — the diagnostic names the option, not the
symptom — and a path form needs `Options.ModuleLoader`, or it is an error that says the
host did not enable module loading.

**A module is never callable.** `json` is both a module and the §12.1 function, and the
include decides which one the name means for the rest of the file: after `include json`,
`json(x)` is a compile error naming the two ways to write what was meant — a member
(`json.parse(s)`) or the method form `x.json`, which UFCS makes the very same call.
Calling a name is therefore always calling a function, and reaching into a module is
always naming one of its members; no position of a name switches between the two.

```
include json
json.parse(s)                      # a member of the module
{total: 1500}.json                 # the §12.1 function, spelled as a method
json({total: 1500})                # error: 'json' is a module, not a function
```

The same holds for a script module, where there is no function half at all: `cart(order)`
after `include cart from "./cart.mzs"` is the same compile error, not the runtime `not a
function: dict` it used to be.

**`export` (script modules).** A file included by another exposes what it marks and
nothing else:

```
export fn total(items) { … }       # inline, before the declaration
export rate = 0.2                  # inline, before an assignment
helper = { (x) -> x * 2 }
export helper                      # or afterwards, naming a binding that exists
```

Exporting a name that does not exist is a compile error, and so is exporting something
that has none — `export fn(…) { … }` and `export async fn(…) { … }` report that `export`
needs a name and print the two spellings that give it one. The module's value is a dict of
its exported names in declaration order, so `cart.total(x)` and `json.parse(s)` are the
same operation (§8.7) and `cart.keys` lists what a module offers. A name the module never
exported is simply absent — `defined(cart.sep)` is false.

Running a file directly ignores its exports, so one file is both a program and a module.

**How a script module runs.** It is part of the Run that included it: same globals table,
same step budget, same deadline (§10, §14.1). Its locals are its own. It runs **once per
Run** — a diamond loads the shared module a single time — and a second Run runs it again,
because caching across Runs would share mutable state between two dialogues. A cycle is a
named error, not a hang, and includes nest at most 64 deep.

Module names are lowercase; there is no `CONST` kind and no `::` operator.

| Module | Member | Signature | Semantics |
|---|---|---|---|
| `json` | `parse` | `(s: string) -> any` | objects→Dict (insertion-ordered), arrays→Array, numbers→Int when integral else Float, `null`→nil. Raises on malformed input. |
| `json` | `pretty` | `(x: any) -> string` | 2-space indent |
| `math` | `pi`, `e` | constants | |
| `math` | `sqrt cbrt sin cos tan atan atan2 log log2 log10 exp pow hypot` | `(…) -> float` | |
| `time` | `now` | `-> time` | requires `Options.Now` |
| `time` | `parse` | `(s: string, layout: string = auto) -> time` | accepts RFC3339, `YYYY-MM-DD{ HH:MM[:SS]}`, `DD/MM/YY`, `DD.MM.YYYY` |
| `time` | `at` | `(unix: int) -> time` | |
| `date` | `today` | `-> time` (midnight) | requires `Options.Now` |
| `date` | `parse` | `(s: string) -> time` | |
| `http` | `serve stop json text get post request` | see §12.11 | no host option |
| `io` | `stdin lines read write append exists ls env` | see §12.13 | requires `Options.FS` |

Encoding to JSON is the §12.1 function, written `x.json` wherever the module is included,
so `json.parse` and `x.json` are the two halves of the pair and no `generate` member is
needed — a second name for the second half is exactly what D17 forbids. Both halves fail
with kind `json` (§13.5): bad input on the way in, a cycle or too much nesting on the way
out.

Time values: `strftime(layout)` (C-style `%Y %y %m %d %H %M %S %a %A %b %B %j %p %z %Z %%`,
plus `%-d`/`%-m` for no padding), `to_date`, `int` (unix seconds), `in_time_zone(tz)` (uses
`time.LoadLocation` with the embedded `time/tzdata` — stdlib, still zero external deps),
`year month day hour min sec wday yday`, and `+`/`-` with a duration or an int (seconds).

`time` is an **internal kind** exposed as an opaque value whose `type()` is `"time"`. It
exists only when `Options.EnableTime` is true, and then still only after `include time`.
Scripts that never touch time are unaffected.

### 12.9 nil, bool and function receivers

| Receiver | Methods |
|---|---|
| nil | `str` → `""`, `int` → `0`, `float` → `0.0`, `array` → `[]`, `inspect` → `"nil"`, `json` → `"null"` |
| bool | `str`, `int` (`1`/`0`), `json` |
| function | `call(*args)`, `arity` → int, `str` |

There is no `nil?` predicate: write `x == nil`. There are no non-short-circuit boolean
operators: `&`, `|` and `^` are not lexemes (§3.9), so `&&`/`||`/`!` are the whole set.

### 12.10 Ranges

`len`, `array`, `each`, `map`, `filter`, `reject`, `has`, `first(n)`, `last(n)`, `min`, `max`,
`sum`, `step(n)`, `each_slice(n)`, `reverse`, `reduce`.

A Range answers `true` to `is("array")` but reports `type(r) == "range"`.

### 12.11 The `http` module

`http` is the one module that reaches outside the process. It needs no host option — it
is installed like `json` — but still only after `include http` like every other module
(§12.8): without the include the name does not exist. It is the single exception to
§14.3, and it is deliberate: the CLI's work is scripts that fetch and serve, and a flag
on every command line bought nothing. A host that must not allow a socket takes the name
away with `Unregister("http")`; do that for an interpreter that evaluates expressions
someone else authored.

| Member | Signature | Semantics |
|---|---|---|
| `serve` | `(addr: string, routes: dict, ready: fn?) -> nil` | binds `addr` and serves until `http.stop()` or context cancel |
| `stop` | `() -> nil` | ends the `serve` of this Run after the current request; an argument error outside one |
| `json` | `(body: any, status: int = 200, headers: dict = {}) -> dict` | response whose body is `body` encoded as JSON |
| `text` | `(body: any, status: int = 200, headers: dict = {}) -> dict` | response whose body is `str(body)` |
| `get` | `(url: string, opts: dict = {}) -> dict` | request; returns `{status:, body:, headers:}` |
| `post` | `(url: string, body: any, opts: dict = {}) -> dict` | a string body is sent as is, anything else as JSON |
| `request` | `(method: string, url: string, opts: dict = {}) -> dict` | `opts`: `body`, `headers`, `timeout` (seconds) |

**Routes.** A key is a `net/http` pattern — an optional method, a path, and `{name}`
wildcards (`{name...}` for a trailing segment) — and a value is a closure of one argument.
An unroutable pattern is an argument error before the listener is bound; an unmatched path
is 404 and an unmatched method 405, neither of which reaches a handler. The optional third
argument is a closure called with the base URL once the listener is up, which is what makes
`":0"` usable.

**Request.** The handler is called with
`{method:, path:, params:, query:, headers:, body:, host:, remote:}`. Header names are
lowercased and repeated header and query values collapse to the first. `params` holds the
pattern's wildcards. The body is read under `MaxStringBytes`; a larger one is 400.

**Response.** What the handler returns is what the client gets:

| Returned | Response |
|---|---|
| `nil` | 204, no body |
| a string | 200, `text/plain; charset=utf-8` |
| a dict with an int `status` | that status, its `body`, its `headers` |
| anything else | 200, `application/json; charset=utf-8` |

`http.json` and `http.text` build the third form. A `return` inside a handler answers with
its value; `body` may itself be a string (sent as is) or any other value (sent as JSON).

**Concurrency.** Handlers run on the goroutine that called `serve`: one runState is one
Run's private state (§10), so requests are queued and answered one at a time. A host that
needs parallelism runs several Runs, each with its own listener, or keeps HTTP in Go and
calls mzs per request.

A **client** call is the other side of that coin: `http.get`/`post`/`request` release the
interpreter while the request is on the wire (§8.14), so the tasks of one Run overlap their
waiting and N calls in N tasks cost about one of them. Nothing else about them changes —
same timeouts, same limits, same errors.

**Limits.** Waiting for a connection charges no steps. Each request starts from a fresh
`StepBudget` and a fresh `Timeout` deadline, so a slow handler fails that request only
(500) and the server keeps serving; what the handlers spent is added back to the Run's
counter when `serve` returns. A handler error is 500 with the diagnostic on `Stderr` and
never in the body (§13.5). A canceled context ends the Run with `ErrCanceled` (§14.1) and
takes the listener down with it.

Client calls are bounded by `opts.timeout` (default 10 s) **and** by whatever is left of
the Run's own deadline, whichever is shorter. Running out of the Run's time is a limit
error and is not catchable; everything else — refused connection, DNS failure, the
per-call timeout — is an ordinary error of kind `http` (§13.5), so `try http.get(u) else …`
is how a script handles a service being down. A non-2xx status is a value, not an error. Responses are read
under `MaxStringBytes`.

### 12.12 Tasks

The receiver an `async fn` call produces (§8.14). Two names, and a task answers nothing else.

| Receiver | Name | Signature | Semantics | Example |
|---|---|---|---|---|
| task | `await` | `await(t) -> any` | waits for the task and yields the value of its body; raises here, at the await, whatever the body raised. Awaiting a finished task again is the same answer again. | `t.await` |
| task | `done` | `done(t) -> bool` | has the task finished — the one question about a task that never waits | `t.done` |

### 12.13 The `io` module

`io` is stdin, files and the environment: the second and last part of the standard library
that reaches outside the process, and the mirror image of §12.11. `http` is installed with
no option asked for; `io` is installed **only** when the host supplies an
`Options.FS` (§13.2), because reading a file is the capability an embedder is most likely
to be embedding mzs in order to withhold. With the zero `Options` the name does not exist
and `include io` is a compile error that says which field is missing:

```
$ mzs -e 'include io'      # inside a host that installed no FS
-e:1:9: name: module 'io' needs a filesystem: the host did not install Options.FS
```

| Member | Signature | Semantics | Example |
|---|---|---|---|
| `stdin` | `-> string` | the whole of `Options.Stdin`, read once per Run and kept for the rest of it. No reader is `""`, not an error | `io.stdin.trim` |
| `lines` | `-> array` | `io.stdin.lines` (§12.2): the terminator is dropped, a CRLF file reads like an LF one | `io.lines.len` |
| `read` | `(path: string) -> string` | the file as a string | `io.read("a.txt")` |
| `write` | `(path: string, s: string) -> int` | truncates or creates; returns the bytes written | `io.write("a.txt", s)` |
| `append` | `(path: string, s: string) -> int` | creates when absent; returns the bytes written | `io.append("log", l)` |
| `exists` | `(path: string) -> bool` | a name that is not there is `false`; a filesystem that cannot answer raises | `io.exists("a.txt")` |
| `ls` | `(dir: string = ".") -> array` | the entry names of a directory, sorted (§8.13) | `io.ls(".").len` |
| `env` | `(name: string, default: any = nil) -> string \| nil` | an unset **or empty** name is the default | `io.env("HOME", "/tmp")` |

There is no `io.rm`, no `io.mkdir` and no `io.open`: the members above are the whole
module. A script reads a file, writes a file and lists a directory; anything structural is
the host's to do, or the shell's.

**The host resolves paths.** Nothing in the module joins, cleans or judges a name: what a
script writes reaches `Options.FS` verbatim, and whether `"../../etc/shadow"` is allowed is
the policy of the code that implemented the interface — exactly as it already is for
`include … from` and `ModuleLoader` (§12.8). The library ships no implementation of
`FileSystem` at all. The CLI does, and its policy is the loosest one there is, because the
person typing `mzs -e` already owns the machine; an embedder writes the narrow one it
wants.

**Capabilities inside the module.** `Options.Stdin` and `Options.Env` are separately
optional. Without a reader `io.stdin` is `""` and `io.lines` is `[]`; without an `Env`
every name is unset. Neither is an error, so one script runs both in a pipe and out of
one. `io.stdin` is read **once per Run** and cached on the Run — a reader gives its bytes
away once, so a second `io.stdin` must answer what the first one read, and the tasks of
§8.14 share that one string rather than racing for the reader.

**Errors and limits.** Everything the outside world can refuse is an ordinary catchable
error of kind `io` naming the path, so `try io.read(p) else ""` is how a script meets a
missing file (§8.11, §13.5). Each member charges 1000 steps before it starts, and file waits release the
interpreter for the Run's other tasks the way `http` does (§8.14) — the stdin drain does
not, because "read once" is a promise across the whole Run. `io.read` and `io.stdin` stop
at `MaxStringBytes` and report it (§14.2); they never truncate.

---
## 13. Go API

Import path `mzs` (module root). Everything below is the **normative public surface** for
*embedders*: a host program uses only these names.

The root package additionally exports the standard-library registration seam that §18
mandates — `Method`, `RegisterMethod`, `LookupMethod`, `HasMethod`, `MethodNames`,
`Builtin`, `RegisterBuiltin`, `LookupBuiltin`, `BuiltinNames`, `RegisterModuleFunc`,
`RegisterModuleConst`, `SetModuleGate`, `ModuleNames`, `KAny`, `KRange`, and the wider
`Ctx` the method tables need. Those exist so each stdlib file installs its own rows from
an `init()` without any file owning a shared table by hand. They are stable, but they are
an implementation seam, not part of the embedding contract, and a host has no reason to
call them — extend the language with `Register`/`RegisterModule` instead.

### 13.1 Values

```go
package mzs

type Kind uint8

const (
    KNil Kind = iota
    KBool
    KInt
    KFloat
    KString
    KRegex
    KArray
    KDict
    KFunc
    KTime
    KRange   // §12.10: type(r) == "range", but r.is("array") is true
    KAny     // not a value kind; the key of the universal method table (§12.1)
)

func (k Kind) String() string // "nil","bool","int","float","string","regex","array","dict","function","time","range"

// Value is an immutable handle. Copying a Value is free and safe.
// Array/Dict/Func values are references: copies alias the same underlying data.
type Value struct{ /* unexported */ }

// Constructors
func Nil() Value
func Bool(b bool) Value
func Int(i int64) Value
func Float(f float64) Value
func Str(s string) Value
func Regex(pattern, flags string) (Value, error)
func Array(elems ...Value) Value
func Dict(pairs ...Value) Value             // pairs: k1, v1, k2, v2, ... (panics on odd count)
func Fn(name string, arity int, f HostFunc) Value

// Inspection
func (v Value) Kind() Kind
func (v Value) IsNil() bool
func (v Value) Truthy() bool                 // §7.3
func (v Value) Bool() bool                   // false unless KBool
func (v Value) Int() int64                   // int() semantics, never panics
func (v Value) Float() float64               // to_f semantics, never panics
func (v Value) Str() string                  // str() semantics, never panics
func (v Value) String() string               // == Str(); implements fmt.Stringer
func (v Value) Inspect() string
func (v Value) Len() int                     // 0 for scalars
func (v Value) Equal(o Value) bool           // §7.4 ==

// Collections (nil-safe; no-ops / zero values off-kind)
func (v Value) Index(i int) Value            // Array/String
func (v Value) Get(key Value) Value          // Dict
func (v Value) Set(key, val Value)           // Dict (mutates)
func (v Value) Append(vals ...Value)         // Array (mutates)
func (v Value) Keys() []Value                // Dict, insertion order
func (v Value) Elems() []Value               // Array (copy-on-return is NOT performed; do not retain)

// Go interop
func (v Value) Interface() any               // nil,bool,int64,float64,string,*regexp-ish,[]any,map[string]any(ordered→dict),func
func From(x any) (Value, error)              // reflect-based; supports scalars, []T, map[string]T, json.RawMessage, encoding/json-compatible structs
func MustFrom(x any) Value
func (v Value) MarshalJSON() ([]byte, error)
func (v *Value) UnmarshalJSON(b []byte) error
```

`From` maps every Go integer kind to `Int`, with one exception that D9 decides: a `uint`
or `uint64` above `math.MaxInt64` has no `Int`, so it becomes a `Float` — the same
promotion an overflowing `+` performs, and never a wrap into a negative. `uint8`,
`uint16` and `uint32` always fit.

### 13.2 Options

```go
type Options struct {
    // Semantics
    StrictWarnings  bool          // promote Program.Warnings to compile errors

    // Resource limits (0 means "use the default")
    Timeout         time.Duration // wall clock per Run. Default 1s. 0 disables (not recommended).
    StepBudget      int64         // interpreter steps per Run. Default 5_000_000. -1 disables.
    MaxDepth        int           // call/recursion depth. Default 200.
    MaxTasks        int           // tasks of one Run unfinished at once (§8.14). Default 64. -1 forbids them.
    MaxCollection   int           // max elements materialised by one operation. Default 1_000_000.
    MaxStringBytes  int           // max size of a single produced string. Default 8 << 20.
    RegexSteps      int           // backtracking budget per match. Default 200_000.
    RegexCacheSize  int           // Default 256.
    ProgramCache    int           // compiled-source LRU for Eval(). Default 512. 0 disables.

    // Capabilities (all nil/false by default => deterministic, sandboxed)
    Stdout          io.Writer     // print/say/debug sink. nil => discard.
    Stderr          io.Writer     // warnings. nil => discard.
    Now             func() time.Time // enables now()/time.now/date.today
    Rand            *rand.Rand       // enables rand()/uuid()/sample/shuffle
    EnableTime      bool             // installs the time/date modules (needs Now for now/today)
    ModuleLoader    ModuleLoader     // enables `include x from "path"` (§12.8); nil => error
    FS              FileSystem       // installs the io module (§12.13); nil => `include io` is an error
    Stdin           io.Reader        // what io.stdin/io.lines read, once per Run. nil => ""
    Env             func(string) string // answers io.env. nil => every name is unset
    Location        *time.Location   // default zone for in_time_zone/strftime. Default time.UTC.
}

func DefaultOptions() Options
```

There is **no** option that enables process access, and no `system`, `exec` or `spawn`
anywhere; such a capability can only be added by the host through `Register` (§13.4).
Filesystem access is an option — `FS` — but not an implementation: the interface is
declared here and the package contains no code that opens a real file, so *the host*
decides what a path may name and what it may read. `ModuleLoader` works the same way, and
for the same reason.

```go
type ModuleLoader func(from, path string) (resolved string, src string, err error)

type FileSystem interface {
    Open(name string) (io.ReadCloser, error)
    Create(name string) (io.WriteCloser, error)   // truncate or create
    Append(name string) (io.WriteCloser, error)   // create when absent
    Stat(name string) (exists bool, size int64, dir bool, err error)
    List(dir string) ([]string, error)
}
```

For `ModuleLoader`, `from` is the name of the including program and `path` is the string
the script wrote; the loader returns the resolved name — the key the per-Run module cache
uses, so two spellings of one file load once — and its source.

For `FileSystem`, a name arrives exactly as the script spelled it (§12.13). A missing file
is an ordinary `error`, which the script sees as a catchable error; `Stat` reports a name
that is not there as `(false, 0, false, nil)`, because not being there is an answer and not
a failure.

Network access is the one thing with no switch at all: `http` (§12.11) is installed
unconditionally, which is the one thing the zero `Options` grants beyond pure computation.
A host that must not grant it calls `Unregister("http")` — which also works on `io`, for a
host that installed an `FS` and wants to take it back for one interpreter.

### 13.3 Interpreter, programs, runs

```go
type Interp struct{ /* unexported; safe for concurrent use */ }

func New(opts Options) *Interp

// Compile parses and compiles. The returned *Program is immutable and may be run
// concurrently from many goroutines. name is used in diagnostics.
func (in *Interp) Compile(name, src string) (*Program, error)

// Eval compiles (via the program cache) and runs in one call.
func (in *Interp) Eval(ctx context.Context, src string, vars map[string]Value) (Value, error)

// Run executes a compiled program with a fresh, isolated globals table seeded from vars.
func (in *Interp) Run(ctx context.Context, p *Program, vars map[string]Value) (Value, error)

// RunResult additionally returns the mutated globals (for set_var-style blocks) and stats.
func (in *Interp) RunResult(ctx context.Context, p *Program, vars map[string]Value) (Result, error)

type Result struct {
    Value   Value
    Globals map[string]Value // final state of $vars, including ones the script created
    Steps   int64
    Elapsed time.Duration
}

type Program struct{ /* unexported */ }

func (p *Program) Source() string
func (p *Program) Name() string
func (p *Program) Warnings() []Warning
func (p *Program) String() string // pretty-printed AST, for --ast
```

Concurrency contract: one `*Interp` may serve unlimited concurrent `Run`s. `Register`,
`SetGlobal` and option mutation are **not** safe after the first `Run`; do all setup first.

### 13.4 Host functions and globals

```go
type HostFunc func(c *Ctx, args []Value) (Value, error)

// Ctx is the per-call handle given to host functions.
type Ctx struct{ /* unexported */ }

func (c *Ctx) Context() context.Context
func (c *Ctx) Interp() *Interp
func (c *Ctx) Arg(i int) Value            // Nil() when out of range
func (c *Ctx) NArgs() int
func (c *Ctx) Global(name string) Value   // "$x" or "x"
func (c *Ctx) SetGlobal(name string, v Value)
func (c *Ctx) Call(fn Value, args ...Value) (Value, error) // invoke a script function (e.g. a closure argument)
func (c *Ctx) Errorf(format string, a ...any) error        // positioned script error
func (c *Ctx) Step(n int64) error                          // charge n steps; returns ErrBudget when exhausted
func (c *Ctx) Blocking(f func())                           // run f with the interpreter released (§8.14)

// Registration (before the first Run)
func (in *Interp) Register(name string, arity int, f HostFunc)      // global function; arity -1 = variadic
func (in *Interp) RegisterModule(name string, members map[string]Value) // e.g. "http"
func (in *Interp) SetGlobal(name string, v Value)                   // a default $var for every Run
func (in *Interp) Unregister(name string)                           // remove a builtin (e.g. drop eval)
```

Host functions must not block indefinitely; they receive `c.Context()` and are expected to
honour it. A host function that returns an error surfaces as a catchable script error unless
it wraps `ErrFatal`.

A host function that *does* wait — on a socket, on a queue — should wrap the wait in
`c.Blocking`, which hands the interpreter to the Run's other tasks until it returns
(§8.14). The closure must touch no `Value`, no global and no `Ctx` method: outside the
lock, that is the one data race the design cannot prevent. `http.get` is written this way,
which is why N requests in N tasks overlap.

### 13.5 Errors

```go
var (
    ErrTimeout   = errors.New("mzs: execution timed out")
    ErrBudget    = errors.New("mzs: step budget exceeded")
    ErrDepth     = errors.New("mzs: max call depth exceeded")
    ErrCanceled  = errors.New("mzs: canceled")
    ErrFatal     = errors.New("mzs: fatal")       // wrap to make a host error uncatchable
    ErrExit      = errors.New("mzs: exit")        // the `exit` builtin, §12.1
)

type Error struct {
    Kind    string // "syntax" | "name" | "type" | "argument" | "index" | "key" |
                   // "zero-division" | "regex" | "json" | "http" | "io" | "raise" |
                   // "limit" | "exit" | "internal" — or a name the script chose, §8.11
    Msg     string
    File    string
    Line    int
    Col     int
    Stack   []Frame        // innermost first
    Data    Value          // payload from raise(dict); the status from exit(code)
    wrapped error
}

func (e *Error) Error() string  // "script.mzs:3:12: type: cannot add int to string"
func (e *Error) Unwrap() error

// ExitCode reports the status an exit(code) asked for, and false for every other error
// — including a script that failed on its own. A host that has a process to end asks
// here first; one that does not treats an exit as the end of the Run and nothing more.
func ExitCode(err error) (code int, ok bool)

type Frame struct { Fn string; Line int; Col int }

type Warning struct { Msg string; Line, Col int }
```

`Compile` returns `*Error` with `Kind == "syntax"` (and may return a multi-error joined with
`errors.Join` when recovery found several). `Run` never panics; a recovered internal panic
becomes `Kind == "internal"` and is reported with the Go stack in `Msg` when
`Options.StrictWarnings` is set.

`Kind == "exit"` is the one error that is not a failure: `exit(code)` (§12.1) ends the Run
the way a limit does — nothing catches it, and it always reaches the host — but it says the
program is finished rather than broken. Nothing in the library calls `os.Exit`; a script
inside a bot cannot end its process, and what the status means is the host's decision.

**The kinds, and where each is born.** The list is closed for the runtime: a failure the
language produces is always one of these, which is what makes `match e["kind"]` a decision
over a known set (§8.11).

| Kind | Born at |
|---|---|
| `syntax` | `Compile` — a parse or resolve failure; a script never sees one (§9.3) |
| `name` | an undefined variable, function, method or module member (§17) |
| `type` | an operand or receiver of the wrong kind (§8.3, §9.1) |
| `argument` | an arity or argument-shape failure, in a builtin, a stdlib row or a call (§8.7) |
| `index` | a position out of range, and a destructuring length mismatch (§8.15) |
| `key` | a key that is not in the dict — `fetch` (§12.4) |
| `zero-division` | integer `/` or `%` by zero (§8.3) |
| `regex` | a pattern that does not compile (§12.6) |
| `json` | `json.parse` on bad input, and a value `json` cannot encode (§12.8) |
| `http` | a transport failure, and a body over the cap (§12.11, §14.2) |
| `io` | a filesystem or stream failure, and a read over the cap (§12.13, §14.2) |
| `raise` | `raise` and `assert` (§12.1) |
| `limit` | a timeout, the step budget, the depth limit, a size cap, a cancellation (§14.1) |
| `exit` | `exit(code)` (§12.1) |
| `internal` | a recovered panic — a bug in mzs (A7) |

A script may put a kind of its own on an error it raises (§8.11), so a host switching on
`Kind` needs a default branch. Four are refused to a script and mean exactly what the table
says wherever a host sees them: `syntax`, `limit`, `exit` and `internal`.

### 13.6 `mzs/engine` — the morzebot adapter

```go
package engine // import "mzs/engine"

// Engine is safe for concurrent use and caches compiled programs.
type Engine struct{ /* unexported */ }

type Options struct {
    Timeout    time.Duration // default 1s (the ruby path used 5s)
    CacheSize  int           // compiled-expression LRU, default 1024
    Stdout     io.Writer     // for say/print inside scripts; default io.Discard
    Now        func() time.Time
    Rand       *rand.Rand
}

func New(o Options) *Engine
func Default() *Engine // Options{} zero value, i.e. 1s timeout, no clock, no rand

// --- the three functions morzebot's pkg/engine/eval must forward to ---

// Bool evaluates expr for a condition block. Any error (syntax, runtime, timeout)
// returns (false, err); condition.go treats a non-nil error as "no match".
func (e *Engine) Bool(ctx context.Context, expr string, vars map[string]string) (bool, error)

// String evaluates expr for need_eval_* fields and stringifies the result.
// A compile error returns the source text unchanged, so a need_eval accidentally
// enabled on plain text sends that text instead of failing (§9.3).
// A nil result is ErrNilResult, so an empty bubble is never sent.
func (e *Engine) String(ctx context.Context, expr string, vars map[string]string) (string, error)

// Value evaluates expr and returns the raw value, for need_eval_buttons
// (an array of dicts that the caller serialises into an inline keyboard).
func (e *Engine) Value(ctx context.Context, expr string, vars map[string]string) (mzs.Value, error)

// Package-level conveniences that use Default():
func Bool(ctx context.Context, expr string, vars map[string]string) (bool, error)
func String(ctx context.Context, expr string, vars map[string]string) (string, error)
func Value(ctx context.Context, expr string, vars map[string]string) (mzs.Value, error)
```

Behaviour of `engine.Engine`, precisely:

1. `mzs.Options{EnableTime: true, Timeout: o.Timeout, StepBudget: 5e6}`.
2. `vars` (`map[string]string`) is lifted to `map[string]Value` with `mzs.Str`; keys are
   normalised to the `$`-prefixed form. Values are therefore **strings**, and expressions
   convert explicitly (§9.1).
3. Compiled programs are cached by source text alone. Values never enter the cache key,
   because they are bound and never substituted — a hot dialogue compiles once.
4. `Bool` returns `res.Truthy()`.
5. `String` returns `mzs.Value.Str()`, returns `ErrNilResult` when the result is `KNil`, and
   returns the source text unchanged when `Compile` fails.
6. Timeouts return an error wrapping `mzs.ErrTimeout`; callers keep their existing
   "error ⇒ fall back" behaviour unchanged.

### 13.7 Usage sketch

```go
in := mzs.New(mzs.Options{Timeout: time.Second, Stdout: os.Stdout})
in.Register("http_get", 1, func(c *mzs.Ctx, a []mzs.Value) (mzs.Value, error) {
    return mzs.Str(fetch(c.Context(), a[0].Str())), nil
})
prog, err := in.Compile("cond#12", `$__sent.lower.trim == "оператор"`)
if err != nil { return err }
v, err := in.Run(ctx, prog, map[string]mzs.Value{"$__sent": mzs.Str("  ОПЕРАТОР ")})
// v.Truthy() == true
```

---

## 14. Resource limits, determinism, sandboxing

### 14.1 Interruption

The evaluator increments a step counter on every AST node visit, every loop iteration, every
method call, and every regex match attempt. Every `stepCheckInterval` (default 1024) steps it
checks:

1. `ctx.Err()` → return `ErrCanceled`;
2. wall clock against `Timeout` → `ErrTimeout`;
3. the step counter against `StepBudget` → `ErrBudget`.

Because the check is inside the node loop, a `while true { }` and a pathological regex are
both interrupted **mid-loop**, not merely between statements. No timer per Run (one
`time.Time` deadline comparison), no `runtime.Goexit`, and no goroutine unless the script
asked for one with `async fn`.

Tasks change none of it and share all of it: one counter, one deadline, one context for the
whole Run (§8.14). The one addition is that *waiting* is checked too — an `await` and the
join at the end of a Run both wake on the deadline and on `ctx.Done()`, so no wait can
outlive the limits the host set.

Limits are **not catchable** by `try` (§8.11).

### 14.2 Memory

`MaxCollection` bounds any single operation that materialises elements (`array`, `map`,
`*`, `flatten`, `range`, `split`, `matches`, `each_slice`). `MaxStringBytes` bounds string
construction (`*`, `+`, `join`, `replace`, interpolation). Exceeding either raises
`Kind == "limit"` and is not catchable.

The two members that read from *outside* the process are the exception, and only in how
they report: an HTTP response (§12.11) and a file or a stdin bigger than `MaxStringBytes`
(§12.13) raise an ordinary catchable error of kind `http` or `io` naming what was too big.
Neither is `Kind == "limit"`, and that is the point. The size is a property
of what the world handed over rather than of the script's own arithmetic, so `try` is the
right tool for it, and nothing is evaded by catching it — the reader stops at the limit
either way and the bytes are never buffered.

Recursion is bounded by `MaxDepth`; the Go stack is never allowed to overflow.

### 14.3 Sandbox

Scripts get **no** process, reflection or timer access, and no filesystem or environment
either until a host hands one over. There is no `require`, no `import`, no `load`, no
`system`, no `File` and no `ENV`: what exists instead is `include` (§12.8) and the `io`
module (§12.13), and neither is ambient. The one thread-shaped thing a script may ask for
is a task (§8.14), and it buys concurrency without parallelism: `Options.MaxTasks` bounds
how many exist, they end when the Run does, and no two of them evaluate at once.
`Options.Now` and `Options.Rand` are the only sources of nondeterminism, both off by
default. Any capability beyond those is opt-in, host-supplied, and named by the host
(`in.Register("http_get", …)`).

Reading a **file** and reading another **script** are off by default and work the same way
when they are on: `Options.FS` and `Options.ModuleLoader` are interfaces the host
implements, so the host's own code decides what a path may name. Without them, `include io`
and `include x from "…"` are compile errors that say which field is missing. The package
itself contains no code that opens a file — the only implementation of `FileSystem` in this
repository is the CLI's, where the host is the person typing the command line. That is why
`mzs -e 'include io; io.read("/etc/hostname")'` works while the same program inside a bot
that evaluates stored conditions cannot even name the module.

`Options.Env` is the same shape one step smaller: a `func(string) string`, usually
`os.Getenv`, and `nil` means every name is unset rather than an error.

Network access is the single exception: the `http` module (§12.11) is installed with no
option asked for, so a script that says `include http` can open a listener and call out.
A host that evaluates expressions it did not write — a condition out of a dialogue store
(§19) — calls `Unregister("http")`, and then a script cannot even name it. Every other
sandbox rule, and every limit in §14.1–14.2, applies to `http` unchanged.

There is no `eval` builtin: a script cannot compile new source at runtime.

### 14.4 Performance targets

| Operation | Target |
|---|---|
| `Compile` of a typical 40-char condition | ≤ 15 µs |
| `Run` of `$__sent.lower.trim == "оператор"` (cached program) | ≤ 5 µs, ≤ 3 allocations |
| `Run` of a `~ /…/i` condition, RE2 backend | ≤ 20 µs |
| `Run` of a `~ /…\b…/i` condition, backtracking backend | ≤ 120 µs |
| Steady-state allocation per `Run` | one frame slice + result only |

Hot-path rules for implementers: resolved locals are slice indices, not map lookups; regex
literals are pre-compiled; `Value` never escapes to the heap for scalars; the method
dispatch table is a `map[string]*method` per kind, looked up once per call site and memoised
on the AST node (inline cache keyed by receiver kind).

---

## 15. CLI

`cmd/mzs` builds the `mzs` binary.

```
mzs [flags] [file.mzs] [args...]
mzs -e '<source>'
cat script.mzs | mzs -
cat data | mzs -n -e '<source>'
```

| Flag | Meaning |
|---|---|
| `-e <src>` | evaluate `<src>`; may be repeated (joined with `\n`); mutually exclusive with a file |
| `-p` | print the value of the last expression (default **on** for `-e`, off for a file) |
| `--json` | print the result as JSON instead of `str` |
| `-n` | run the program once per line of the data stream; the line is `$_` |
| `-l` | `-n`, and print each line's value when it is not `nil` |
| `--in <path>` | read the data stream from a file instead of stdin |
| `--io` / `--no-io` | install the `io` module (§12.13) or withhold it; **on** by default |
| `-v k=v` | set `$k` to the string `v`; repeatable |
| `--vars <json>` | set all `$vars` from a JSON object (values may be any JSON type) |
| `--vars-file <path>` | same, from a file |
| `-t <dur>` | timeout (default `1s`; `0` disables) |
| `--steps <n>` | step budget |
| `--tasks <n>` | tasks running at once, for `async fn` (§8.14); `0` forbids them |
| `--time` | enable the `time`/`date` modules and a real clock |
| `--rand [seed]` | enable rand/uuid; with a seed for reproducibility |
| `--tokens` | dump the token stream and exit |
| `--ast` | dump the AST and exit |
| `--check` | parse + compile only; print errors and warnings; exit 1 on error |
| `--repl` | interactive REPL (line-based, persistent env; `.exit`, `:q`, `exit(code)`, Ctrl-D or Ctrl-C twice to quit) |
| `--version` | print version |

The CLI installs a `ModuleLoader` (§12.8): `include x from "./lib.mzs"` resolves against the
file doing the including, and against the working directory for `-e` and stdin. Paths must be
relative and may not leave the directory of the program that was run, so a script may include
its neighbours and their subdirectories and nothing above them.

It also installs an `FS` and an `Env`, so the `io` module (§12.13) is available with no flag:
`mzs -e 'include io; io.read("/etc/hostname").trim'` reads the file the shell would have read.
That policy is deliberately wider than the loader's above, and the reason is who wrote the
path: an include comes out of a file that may have travelled, while an `io.read` path is
being typed by the person running the command. An embedder gets neither unless it installs
them itself. `--no-io` withholds all of it for one command — no `FS`, no `Env`, no `Stdin` —
which is how the person typing runs somebody else's script and sees it the way an embedder
would: `include io` becomes the compile error of §12.13.

**Stdin is either the program or its data, never both.** A reader has one set of bytes.
With `-e` or a file the program came from somewhere else, so stdin is free to be data and
reaches the script as `io.stdin`/`io.lines`; with neither, stdin is the program, as
`cat script.mzs | mzs` has always meant. `--in <path>` names the data explicitly and always
wins, which is what makes `cat script.mzs | mzs --in access.log -n` sensible — program on
the pipe, data in the file. No data at all is not an error: `io.stdin` is `""` and
`io.lines` is `[]`.

**Line modes.** `-n` runs the compiled program once for every line of that data stream,
with the line bound to `$_` (the terminator is dropped, CRLF like LF, as in §12.2). It is a
loop on the CLI's side and not a mode in the language: the same `*Program`, run again.

```sh
cat access.log | mzs -n -e '$_.split(" ")[0]' | sort | uniq -c
mzs -n --in access.log --bool -e '$_ ~ /ERROR/' && notify
```

* Lines are read as they arrive, so `tail -f | mzs -n …` prints as the file grows.
* Each line is its own **Run**: it gets its own timeout and step budget (§14.1), and its
  own frame. `$variables` carry from line to line — the CLI feeds `Result.Globals` back in,
  so `$n = ($n ?? 0) + 1` counts — and locals do not, because every Run gets a fresh
  environment (§10). Reach for `io.lines` when the program needs the whole input at once.
* Printing follows the ordinary rules: `-e` prints each line's value, a script file prints
  nothing unless asked, `-p` prints the nils too, `--no-print` silences everything. `-l` is
  `-n` plus "print the value when it is not `nil`", which is what makes a *file* speak.
* Under `-n` the CLI owns the reader, so `io.stdin` is `""` — the line is in `$_`.
* An error on a line ends the run with the usual exit code, after a `mzs: input line N:`
  telling which line it was; the diagnostic below it points into the program, which is
  where the fault is.
* `--bool` asks grep's question: exit `0` when **any** line was truthy, `1` when none was.
  Every line still runs, so the output of a program that prints does not depend on where
  the first match landed.
* `-n` with the program itself on stdin is a usage error, not a silent zero-line run:
  there is nothing left to read, and `--in` is the answer. So is `-n` with no program at
  all — there is no REPL to fall back to when the flag says "run this for every line".

`args...` are exposed to the script as the array `$ARGV` (strings). Exit code: `0` success;
`1` script error or `--check` failure; `2` CLI usage error; `3` timeout/budget. A script
that calls `exit(n)` (§12.1) sets the status itself: the CLI prints nothing for it, and in
`-n` mode the lines after it are never read.
Errors print to stderr as `file:line:col: kind: message` followed by the source line and a
caret.

Reference one-liners (all must work):

```sh
mzs -e 'println("hi")'
mzs -v '__sent=  ОПЕРАТОР ' -e '$__sent.lower.trim == "оператор"'
mzs -e 's = $__sent.lower; s ~ /привет|hello/i' --vars '{"__sent":"Привет!"}'
mzs -e '(0..6).map { it * 2 }.each_slice(2).array' --json
mzs -e 'fn f(a, b) { a += b; return a }; f(1, 2)'
mzs -e 'match $__sent.lower.trim { in ["да","ага"] -> 1; else -> 0 }' -v '__sent=Ага'
```

---
## 16. Acceptance corpus

These are the real expressions mined from the production bot flows
(`morze-script-engine/console_data/**/*.json`, `morze-assistant/.dev/*.json`) and from the
existing Go test corpora, **rewritten into mzs** by the migration of §19. Each row MUST
evaluate as stated. Turn this table into a table-driven Go test verbatim.

> Distribution reality check: 272 condition entries, 107 unique. `X == 'literal'` ≈ 133 and
> `X.lower(.trim) ~ /re/i` ≈ 136. Everything else is ≈ 3 expressions total. Optimise the
> interpreter for those two shapes; make everything else merely possible.

### 16.1 Conditions and values

| # | Expression | `$vars` | Expected |
|---|---|---|---|
| 1 | `$__sent == "да"` | `__sent="да"` | true |
| 2 | `$__sent == "да"` | `__sent="Да"` | false |
| 3 | `$__sent == "нет"` | `__sent="нет"` | true |
| 4 | `$__sent == "1"` | `__sent="1"` | true (plain string comparison) |
| 5 | `$__sent == "10"` | `__sent="10"` | true |
| 6 | `$__sent == "/start"` | `__sent="/start"` | true |
| 7 | `$__sent == "RU 🇷🇺"` | `__sent="RU 🇷🇺"` | true |
| 8 | `$__sent == "Orange & Lime"` | `__sent="Orange & Lime"` | true |
| 9 | `$__sent == "Elite Plus (350k)"` | `__sent="Elite Plus (350k)"` | true |
| 10 | `$__sent == "Стрижка c фейдом"` | `__sent="Стрижка c фейдом"` | true |
| 11 | `$__msg_type == "plain_text"` | `__msg_type="plain_text"` | true |
| 12 | `$__sent == "привет" && $__msg_type == "tg_buttons"` | `__sent="привет"`, `__msg_type="tg_buttons"` | true |
| 13 | `$__sent == "btn_1" \|\| $test == "1"` | `__sent="x"`, `test="1"` | true |
| 14 | `$bot_check_attempts.int >= 2` | `bot_check_attempts="0"` | false |
| 15 | `$bot_check_attempts.int >= 2` | `bot_check_attempts="3"` | true |
| 16 | `$__sent == "🌲"` | `__sent="🌲"` | true |
| 17 | `$operator == "human"` | `operator="human"` | true |
| 18 | `$__sent.lower.trim == "/operator"` | `__sent=" /Operator "` | true |
| 19 | `$__sent.lower.trim == "оператор"` | `__sent="  ОПЕРАТОР "` | **true** (Unicode fold + Unicode trim, NBSP included) |
| 20 | `$__sent.lower.trim == "сколько стоит?"` | `__sent="Сколько стоит?"` | true |
| 21 | `$__sent.lower ~ /привет\|здравствуй\|hello\|\bhi\b/i` | `__sent="Привет"` | **true** |
| 22 | `$__sent.lower ~ /пока\|до свидан\|\bbye\b\|прощай/i` | `__sent="ну пока"` | true |
| 22a | `$__sent.lower.index(/пока\|до свидан/i)` | `__sent="ну пока"` | `3` (rune index) |
| 23 | `$__sent.lower ~ /эхо\|echo\|тест\|ping\|пинг/i` | `__sent="что?"` | false |
| 24 | `$__sent.lower ~ /\bменю\b\|главное меню/i` | `__sent="Меню"` | true — **Unicode `\b`** |
| 25 | `$__sent.lower ~ /бесплатн.{0,14}(аудит\|консультац)\|free.?audit/i` | `__sent="Бесплатная консультация"` | true |
| 26 | `$__sent.lower ~ /\bcrm\b\|црм\|клиентск.{0,8}баз/i` | `__sent="нужна CRM"` | true |
| 27 | `$price.int + 1200` | `price="800"` | `2000` |
| 28 | `$price.int + 1200` | `price=""` | `1200` (`"".int == 0`, never an error) |
| 29 | `$bot_check_attempts.int + 1` | `bot_check_attempts="2"` | `3` |
| 30 | `$__sent.int + 92304` | `__sent="1"` | `92305` |
| 31 | `$__sent.split(":")[0]` | `__sent="ivan:i@x.ru"` | `"ivan"` |
| 32 | `$__sent.split(":")[1]` | `__sent="ivan:i@x.ru"` | `"i@x.ru"` |
| 33 | `$__sent.split(" ")[0]` | `__sent="Иван Петров"` | `"Иван"` |
| 34 | `$__sent.replace(/'/, "")` | `__sent="О'Брайен"` | `"ОБрайен"` |
| 35 | `"Ваш адрес $__sent?"` | `__sent="Ленина 1"` | `"Ваш адрес Ленина 1?"` |
| 36 | `"Итоговая цена: ${$price.int}"` | `price="1500"` | `"Итоговая цена: 1500"` |
| 37 | `"Вы записаны на $book_time на имя $user_name"` | `book_time="14:00"`, `user_name="Иван"` | `"Вы записаны на 14:00 на имя Иван"` |
| 38 | `include json` + `json.parse($__webhook_res).dig(0, "generated_text") ?? "Упс, что-то пошло не так..."` | `__webhook_res='[{"generated_text":"ok"}]'` | `"ok"` |
| 39 | same as 38 | `__webhook_res='[]'` | `"Упс, что-то пошло не так..."` (`dig` is nil-safe, `??` fires) |
| 40 | `include json` + `(t = $__sent != $time ? $__sent : $time; json.parse($times).has(t))` | `__sent="14:00 - 14:30"`, `time="15:00"`, `times='["14:00 - 14:30", "14:30 - 15:00"]'` | true — **requires the stored `$times` value to be migrated from a Ruby array literal to JSON** (§19.3) |
| 41 | `date = $__sent != $date ? $__sent : $date` | `__sent="12/03/25"`, `date="11/03/25"` | `"12/03/25"` |
| 42 | `!($__sent == "да")` | `__sent="нет"` | true |
| 43 | `"Hello " + $__sent.upper` | `__sent="world"` | `"Hello WORLD"` |
| 44 | `(2 + 3).str` | — | `"5"` |
| 45 | `"hello".has("lo")` | — | true |
| 46 | `$__sent == "a" && $b == "c"` | `__sent="a"`, `b="c"` | true |
| 47 | `!($__sent == "да")` | `__sent="нет"` | true |
| 48 | `$__sent.lower !~ /отмена/i` | `__sent="да"` | true |
| 49 | `["да", "ага", "конечно"].has($__sent.lower)` | `__sent="Ага"` | true |
| 50 | `3.times.each { it.str }` | — | `[0,1,2]` |
| 51 | `(0..6).map { it }.each_slice(2).array` | — | `[[0,1],[2,3],[4,5],[6]]` |
| 52 | `(0..6).map { {text: it.str, data: "var:date:${it}"} }.each_slice(2).array` | — | array of 2-element arrays of dicts; `.json` round-trips |
| 53 | `$not_existed` | — | `nil` (§9.2) |
| 54 | `0` | — | `0`, and `Bool` of it is **true** |
| 55 | `$sent == "лол"` | `sent="лол"` | true |
| 56 | `$sent == println("test")` | `sent="x"` | `println` writes `test`, returns `nil`, `"x" == nil` → false |
| 57 | `match $__sent.lower.trim { in ["да","ага"] -> "yes"; /^нет/ -> "no"; else -> "?" }` | `__sent=" АГА "` | `"yes"` |
| 58 | `match { $__sent.len > 3 -> "long"; else -> "short" }` | `__sent="да"` | `"short"` |

### 16.2 Regex corpus (must compile and match)

```
/привет|здравствуй|hello|\bhi\b/i
/пока|до свидан|\bbye\b|прощай/i
/помощ|help|что умеешь|команд/i
/эхо|echo|тест|ping|пинг/i
/\bcrm\b|црм|клиентск.{0,8}баз|управлени.{0,10}клиент|от заявки до отгруз/i
/бесплатн.{0,14}(аудит|консультац|анализ|оценк|диагност)|бесплатно|free.?audit|пробн/i
/все темы|главное меню|\bменю\b|показать все|все раздел|остальные темы|другие раздел|в начало/i
/эта[пп]ы?|етап|\bкак\b.{0,16}(работа|внедр|происходит|устроен|стро)|процес|шаг[иове]?|сроки/i
/\bоператор|\boperator|\/operator|перевед.{0,12}оператор|переключ.{0,14}(на )?оператор/i
/ке[ийс]с|кэйс|\bcase\b|пример|портфолио|резул[ьъ]?тат|ваши проект|опыт работ|клиент/i
/классификац.{0,12}код|код.{0,12}(окпд|оквэд)/i
/^(?!❌ Отмена).*$/                      <- negative lookahead: backtracking backend
/(Sun|Mon|Tue|Wed|Thu|Fri|Sat)/
/столов|кафе|\bеда\b|кормя|питани|обед|завтрак|ужин|повар|кухн|тр[её]хразов/i
```

Required behaviours: `i` folds Cyrillic; `\b` is Unicode-aware; `^`/`$` are line anchors;
`(?!…)` selects the backtracking backend; `\/` is a literal slash; `index` returns a **rune**
index.

### 16.3 The author's own files, migrated

These two are normative fixtures rather than teaching material, so they live in
`testdata/` — `examples/` holds the thirty programs of the tutorial set, which §18's
layout lists and which the acceptance suite runs end to end.

`main.mzs`:

```
fn f(a, b) {
  a += b
  print(a)
  return a
}

fn test(s) {
  if s.len > 2 {
    s.lower ~ /столов|кафе|\bеда\b|\bеду\b|\bеды\b|кормя|кормит|покорм|питани|обед|завтрак|ужин|повар|кухн|голодн|перекус|тр[её]хразов/i
  }
}

f(1, 2)
```

The program value is `3` (from `f(1,2)`). `test` is never called; it returns `nil` when
`s.len <= 2`. Note that `return` in `f` is optional and kept only to pin that it works.

`one.mzs`:

```
a___1 = 13213
bcde = 222
str =! "sdfsdf"
```

Lines 1–2 parse. Line 3 is a typo and MUST produce
`one.mzs:3:6: syntax: unexpected '!' after '='; did you mean '!='?`.

`s.md` — the author's notes, migrated:

```
a := 1.2
b := "a"
c := 1
d := {a: 1}
e := [1, 2, "3"]
if $__sent.int > 5 { print("big") }
```

### 16.4 Gotcha tests (each is its own named test case)

| Test | Assertion |
|---|---|
| `TestTruthyZero` | `Bool("0")` is true; `Bool("\"\"")` is true; the *value* `nil` is false |
| `TestMatchIsBool` | `"привет" ~ /привет/` → `true`; `"привет".index(/привет/)` → `0`, and `Bool` of it is **true** |
| `TestUnicodeLower` | `"ПРИВЕТ ЁЖ".lower == "привет ёж"` |
| `TestNBSPTrim` | `" да ".trim == "да"` |
| `TestEmptyInt` | `"".int == 0`; `"".int + 1200 == 1200` |
| `TestNoCoercion` | `1 == "1"` is **false**; `"2" + 1` is an **error**; `"2".int + 1 == 3` |
| `TestApostropheValue` | `$__sent == "О'Брайен"` with that bound value → true |
| `TestEmojiValue` | `$__sent == "EN 🇬🇧"` with that bound value → true |
| `TestUnboundGlobalIsNil` | `$not_existed == nil` → true; `Bool("$not_existed")` → false |
| `TestClosureScope` | `x = 0; if true { x = 1 }; x` → `1`; `if true { y = 1 }; y` and `try { y = 1 } else 0; y` → `undefined variable 'y'` |
| `TestImplicitIt` | `[1,2,3].map { it * 2 }` == `[1,2,3].map { (x) -> x * 2 }` |
| `TestTrailingClosureIsArg` | `[1,2,3].map(double)` == `[1,2,3].map { it * 2 }` where `double = { it * 2 }` |
| `TestDictLiteral` | `{a: 1}.json == "{\"a\":1}"`; `{}.len == 0`; `[].len == 0`; `type({}) == "dict"` |
| `TestBraceIsAlwaysClosure` | `if {a: 1}.has("a") { 1 } else { 2 }` → `1` (no parens needed around the dict) |
| `TestDictLiteralKeys` | `{1 -> "A"}[1]` → `"A"`; `{a -> 1} == {a: 1}`; `{1 -> "A"}.has("1")` → **false**; `{ -1 }` is still a closure |
| `TestArrowFunction` | `(a) -> { a }` and `fn(a) { a }` parse to one tree; `(1) -> { … }` in a `match` arm is still an arm; `(x) -> x` names both replacements |
| `TestExit` | `exit(3)` ends the Run with status 3 and `try` does not catch it; `ExitCode` answers for an exit and for nothing else; `exit(256)` and `exit("x")` are refused |
| `TestAnonymousFn` | `f = fn(a, b) { a + b }; f(2, 3)` → `5`; `fn(x) { x * 3 }(5)` → `15`; `f(1)` raises on arity where a closure would not, and the literal binds no name |
| `TestMatchFirstWins` | `match 5 { in 1..10 -> "a"; 5 -> "b"; else -> "c" }` → `"a"` |
| `TestMatchNoArm` | `match 99 { 1 -> "a" }` → `nil` |
| `TestSubjectEvaluatedOnce` | a subject with a side effect runs exactly once across all arms |
| `TestUfcsUserFn` | `fn shout(s) { s.upper + "!" }; "да".shout` → `"ДА!"` |
| `TestDestructureMismatch` | `a, b = [1,2,3]`, `a, b = [1]`, `a, b = 1` and `a, b = {x: 1, y: 2}` each raise, with the kind and text of §8.15 |
| `TestArrayPatternBinds` | `[x, [y, z]]`, `[x, y]`, `[]` and `else` pick the arm by shape; a literal element still compares |
| `TestEnsureRunsOnEveryExit` | an `ensure` runs on the value, on a raise and on a `return` out of the body, in that order and without changing the value; a limit runs none (§8.11, §14.1) |
| `TestErrorKindsAreClosed` | each of `type`, `name`, `index`, `key`, `zero-division`, `regex`, `json`, `raise` is stamped where it is born; `raise(msg, kind)` names a kind of its own; `limit`/`exit`/`internal`/`syntax` are refused; `raise(e)` keeps the original position and stack |
| `TestBitOpsStayInt` | `shl(1, 63)` is still an `int` and `shl(1, 64)` is `0` where `2 ** 64` promotes to a Float; `2.9.band(1)`, `shl(1, -1)`, `bit(1, 64)` and a non-byte in `pack_bytes` each raise with the text of §12.5 |
| `TestTimeout` | `Bool("while true { }")` returns `ErrTimeout` in ≤ 1.2 s |
| `TestStepBudget` | a 10⁹-iteration loop returns `ErrBudget` without OOM |
| `TestNoHostPanic` | fuzz corpus of 10⁴ random byte strings: `Compile`+`Run` never panic |
| `TestIsolation` | two concurrent `Run`s of `$x = 1` / `$x = 2` on the same `*Program` never see each other's `$x` |
| `TestHostGrantsTheFilesystem` | `include io; io.read(p)` is a compile error naming `Options.FS` under the zero `Options`, and reads the file under a host that installed one |
| `TestOneLiner` | `fn f(a,b) { a += b; return a }; f(1,2)` → `3` |
| `TestLastExprIsResult` | `1; 2; 3` → `3`; `x = 5` → `5` |
| `TestRegexBackendAgreement` | every RE2-safe pattern in §16.2 gives identical results on both backends over a 500-string sample |
| `TestDiagnostics` | every row of §5.6 produces its message verbatim, with the right line and column |

---
## 17. Diagnostics

Error text format: `<file>:<line>:<col>: <kind>: <message>`, then the offending source line
and a caret column marker when `Options.Stderr` is set.

```
cond#12:1:12: type: cannot add int to string
  "Hello " + 1
             ^
```

Rules:

* Every `Error` has a position. A node without a position is a bug.
* The parser recovers at statement boundaries (`;`, NEWLINE, `}`) and reports up to 10
  syntax errors per compile, joined with `errors.Join`.
* Messages name the actual thing: `undefined method 'lenght' for string (did you mean
  'len'?)` — a suggestion is required for every `name`/`type` error, drawn from two sources:
  a Levenshtein-1 search over the receiver's method table, and an exact lookup in the rename
  table of §19.2 (so `.downcase` suggests `.lower`, not nothing). One-liners are typed by
  hand into a web `<input>`; a bad suggestion costs less than none.
* A name **mzs itself** has renamed joins that same table, because D17 buys "one name per
  operation" with a diagnostic and an old name that merely stops existing is not one.
  There is one so far: `say` became `println`, so that which of the pair adds the newline
  is legible from the name — `say("hi")` answers
  `undefined function 'say' (did you mean 'println'?)`.
* Every row of §5.6 is a diagnostic with a fixed message, produced by the lexer or parser
  before any other error can cascade from it.
* Warnings never fail a compile unless `StrictWarnings`. Current warnings:
  `\\b` in a regex (§11.5); an unused closure parameter; a closure literal — or an
  anonymous `fn` (§4.1) — in statement position whose value is discarded; `=` used where
  `==` was likely meant (an assignment as the whole condition of an `if`).

---

## 18. Package layout and implementation work-split

```
mzs/                       module "mzs", go 1.26, zero external requires
  go.mod
  SPEC.md                  this document
  value.go                 Value, Kind, constructors, conversions, Interface/From
  odict.go                 OrderedDict
  options.go               Options, DefaultOptions
  interp.go                Interp, New, Compile, Eval, Run, RunResult, Result
  eval.go                  the tree-walking evaluator (statements, expressions, control flow, match)
  call.go                  calls, closures, arity, return/break/next
  ops.go                   arithmetic, comparison, match, truthiness
  errors.go                Error, Warning, sentinel errors, formatting
  builtins.go              global builtins (§12.1)
  str.go array.go dict.go num.go regexv.go time.go json.go  method tables (§12.1-12.10)
  http.go                  the http module: listener, routing, client (§12.11)
  host.go                  HostFunc, Ctx, Register, RegisterModule, SetGlobal
  limits.go                step counter, deadline, depth, collection/string caps
  async.go                 `async fn`, tasks, the per-Run lock and the join (§8.14)
  internal/token/          Kind, Token, Pos, keyword table, operator table
  internal/lexer/          Lexer (runes in, tokens out, error recovery, no panics)
  internal/ast/            node types, Walk, Print
  internal/parser/         Pratt/precedence-climbing parser, desugarings (§6.2), §5.6 diagnostics
  internal/rx/             Regexp facade, backend selection, pattern parser
  internal/rx/bt/          backtracking engine (Unicode \b, lookaround, backrefs, budget)
  internal/lru/            tiny generic LRU used for programs and regexes
  engine/                  Engine, Bool, String, Value (§13.6)
  cmd/mzs/                 CLI (§15)
  testdata/                §16.3 author files, corpus JSON, golden token/AST dumps, fuzz seeds
  examples/                thirty-three runnable programs, one per feature area (examples/README.md)
```

Four independent implementers, four seams. Each seam is a package boundary with a frozen
signature, so nobody blocks anybody:

| Owner | Scope | Depends on | Frozen contract |
|---|---|---|---|
| **1. Front end** | `internal/token`, `internal/lexer` | nothing | §3 token kinds, `Token` struct, newline suppression, regex/division rule, `$`-interpolation token stream |
| **2. Parser** | `internal/ast`, `internal/parser` | token, lexer | §4 grammar, §5 precedence and `match`, §5.6 diagnostics, §6 node list and desugarings, `Parse(name, src) (*ast.Program, error)` |
| **3. Runtime** | `value.go`, `odict.go`, `eval.go`, `call.go`, `ops.go`, `interp.go`, `errors.go`, `limits.go`, `async.go`, `host.go` | ast | §7, §8, §9, §13.1–13.5, §14 |
| **4. Library & edges** | `internal/rx`, `internal/rx/bt`, all method-table files, `builtins.go`, `json.go`, `time.go`, `http.go`, `engine/`, `cmd/mzs` | value, host | §11, §12, §13.6, §15, §16 |

Cross-cutting agreements that must not drift: `Value` (owner 3) is imported by owner 4 —
freeze `value.go` first; `rx.Regexp` (owner 4) is referenced by `ast.RegexLit` (owner 2) —
`internal/rx` therefore must not import `internal/ast`. Method tables register themselves
into a `map[Kind]map[string]*method` via `init()`, so owner 4 can add methods without
touching owner 3's files.

Owner 2 additionally owns UFCS resolution (§6.3 step 2), which is the one place the parser
side needs the method tables owned by 4; the seam is a `func(kind Kind, name string) bool`
predicate injected into `Compile`, so neither package imports the other.

Test ownership: owner 1 golden token dumps; owner 2 golden AST dumps and the §5.6 diagnostic
table; owner 3 semantics tables (§7–§9) and limits; owner 4 stdlib tables (§12), regex corpus
(§16.2) and the acceptance corpus (§16.1). Tests are table-driven with an anonymous struct
slice, `t.Run` with a descriptive name, `t.Fatalf`/`t.Errorf`.

---

## 19. Migrating morzebot-backend-v2

mzs is not Ruby-compatible and has no legacy dialect (G3). The stored condition strings are
therefore rewritten **once**, mechanically, before the cutover. This section is the plan.

### 19.1 Why this is cheap

§16 measured the corpus: 272 condition entries, 107 unique, and only two shapes matter.

* `X == 'literal'` — ≈133 entries. Valid mzs as written, except that `'…'` is now a raw
  string (no interpolation), which for a literal changes nothing.
* `X.downcase(.strip) =~ /re/i` — ≈136 entries. Three token substitutions each.
* Everything else — ≈3 expressions, migrated by hand and listed in §19.3.

### 19.2 The codemod

Applied to every `need_eval_*` and `condition` field in the flow storage. The same table
drives the did-you-mean diagnostics of §5.6, so anything the codemod misses fails loudly at
publish time rather than silently at runtime.

| Find | Replace |
|---|---|
| `'$var'` (a `$var` alone inside quotes) | `$var` |
| `"#{expr}"` | `"${expr}"`; `"#{$v}"` → `"$v"` |
| `.downcase` / `.upcase` | `.lower` / `.upper` |
| `.strip` / `.lstrip` / `.rstrip` | `.trim` / `.trim_start` / `.trim_end` |
| ` =~ ` | ` ~ ` |
| `X == /re/` | `X ~ /re/` |
| `.include?(` / `.has_key?(` / `.cover?(` | `.has(` |
| `.empty?` / `.blank?` / `.any?` / `.all?` / `.none?` | `.empty` / `.blank` / `.any` / `.all` / `.none` |
| `.to_i` / `.to_f` / `.to_s` / `.to_a` / `.to_h` / `.to_json` | `.int` / `.float` / `.str` / `.array` / `.dict` / `.json` |
| `.length` / `.size` | `.len` |
| `.gsub(` / `.sub(` | `.replace(` / `.replace_first(` |
| `.scan(` / `.match?(` / `.match(` | `.matches(` / `~` / `.captures(` |
| `.select(` / `.collect(` / `.detect(` / `.inject(` | `.filter(` / `.map(` / `.find(` / `.reduce(` |
| `JSON.parse(` / `JSON.generate(` | `json.parse(` / `json(` |
| `%w[a b c]` | `["a", "b", "c"]` |
| `{k: v}` | `{k: v}` |
| `{ \|x\| … }` | `{ (x) -> … }` |
| `->(x) { … }` / `&fn` | `{ (x) -> … }` / `fn` |
| `puts(` / `p(` | `println(` / `debug(` |
| ` and ` / ` or ` / `not ` | ` && ` / ` \|\| ` / `!` |
| `a rescue b` | `try a else b` |
| `x&.y` | `x?.y` |
| `a...b` | `a..<b` |
| `if … do … end`, `elsif`, `unless`, `until`, `loop do` | brace forms (§5.6) |
| `$v <op> <number>` where `<op>` is `< <= > >= + - * /` | `$v.int <op> <number>` |

The last row is the only semantic rewrite: host values are strings and mzs does not coerce
(§9.1). It is decidable syntactically — a `$var` adjacent to a numeric literal under an
arithmetic or ordering operator — and it covers corpus rows 14, 15 and 29.

### 19.3 The three hand-migrated expressions

1. **Row 40** stores a Ruby array literal in a `$var`
   (`"['14:00 - 14:30', '14:30 - 15:00']"`) and used the `eval` builtin to read it back.
   `eval` does not exist in mzs. The **stored value** migrates to JSON
   (`["14:00 - 14:30", "14:30 - 15:00"]`) and the expression becomes
   `json.parse($times).has(t)`. This is a data migration, not a code one; run it in the same
   pass.
2. **Rows 38/39** chained `.first[...]` and relied on `nil[k]` being `nil`. Rewrite to
   `json.parse($__webhook_res).dig(0, "generated_text") ?? "…"`, which is nil-safe by
   construction.
3. **Plain-text `need_eval_answer` fields** were "evaluated" and fell back to their own text
   via the bareword shim. There is no shim (§9.3); `compat.Engine.String` returns the literal
   text when `Compile` fails, which is where the fallback belongs.

### 19.4 Host-side changes

1. `pkg/engine/eval/eval.go`: delete the `exec.Command("ruby", …)` runner. Keep the package's
   exported functions; make them thin forwarders:

   ```go
   var engine = compat.New(compat.Options{Timeout: time.Second})
   func Bool(ctx context.Context, expr string, vars map[string]string) (bool, error)    { return engine.Bool(ctx, expr, vars) }
   func String(ctx context.Context, expr string, vars map[string]string) (string, error) { return engine.String(ctx, expr, vars) }
   ```

   `Translate` is gone: values are bound, never substituted (§10).

2. `pkg/engine/blocks/condition.go` keeps its `first_right` loop and its "error ⇒ no match"
   rule unchanged. Nothing else in `blocks/` changes.
3. `need_eval_buttons` switches from `String` to `compat.Value` and serialises the resulting
   Array/Dict directly, which removes the current string round-trip.
4. The publish-time validator becomes `mzs.Compile` plus a capability check (no host
   functions referenced) plus the §11.5 warnings. Do not port
   `validators/condition_validator.rb`: it rejects `.downcase`, `.strip` and `=~`, which
   >99% of production conditions use.
5. `ruby_script` blocks (`DEVELOPMENT_ONLY`, 3 files) are out of scope for the cutover; they
   can move to mzs later, since mzs is a full language.

### 19.5 Cutover

1. Run the codemod over a **copy** of the flow storage; compile every migrated expression
   with `mzs.Compile`. Anything that fails to compile is a codemod gap — fix the codemod, not
   the expression, and re-run.
2. Shadow mode for one week: evaluate both the Ruby original (against the untouched storage)
   and the migrated mzs expression on live traffic, logging every disagreement with the
   expression, the vars and both results.
3. Expected disagreements, all of them Ruby bugs the migration fixes: values containing an
   apostrophe (`О'Брайен`), spaces (`Стрижка c фейдом`) or emoji (`EN 🇬🇧`), which the
   textual-substitution path turned into syntax errors and therefore into "no match".
4. Delete the Ruby path when the disagreement rate, excluding the class in step 3, is zero.
   Then apply the codemod to the live storage.

---
## 20. Reserved and out of scope for v0.1

Reserved syntax — lexed or parsed as an error today so it can be added later without breaking
anyone: `@ivar`, `@@cvar`, `class`, `module`, `yield`, `defer`,
`<<`/`>>`, `<<~HEREDOC`, `|>`, `**kwargs`, `*splat` at a call site, string mutation.

`import`, `require` and `use` are not reserved for a future feature: mzs has the feature and
spells it `include` (§12.8), so each of them is an ordinary identifier that the parser
diagnoses with that fix-it when it is written in the dependency shape (§5.6).

Destructuring is no longer reserved: `a, b = pair` and the binding `match` arm `[x, y] -> …`
are §8.15. What stayed behind is the **rest element** — `a, *rest = xs` — which is reserved
together with `*splat` at a call site and lands with it or not at all.

`<<` and `>>` stay reserved even though the shifts arrived: they are the functions `shl`
and `shr` (§12.5), so nothing spends the lexeme. `&`, `|` and `^` are not reserved either —
each is a diagnostic naming its function (§5.6), and that is where they will stay, because
`&` beside `&&` is exactly what D16 exists to prevent.

The multi-statement `try` is no longer reserved: `try { … } else { … } ensure { … }` is
§8.11, and it arrived the way that entry predicted — as sugar, with no grammar conflict,
because the only thing `{` could have meant in that position was a closure nobody wrote.
`ensure` is the keyword it cost (§3.5).

Explicitly out of scope, permanently: shared-memory parallelism, `system`, reflection over
Go types, user-defined operator overloading, and any dependency outside the Go standard
library. Concurrency itself is in — `async fn` (§8.14) — under the rule that makes it safe:
one goroutine of a Run evaluates at a time, so there is no memory model for a script to
learn and no lock for it to forget.
Filesystem access is not out of scope but is not the language's either: `io` (§12.13) and
`include … from` (§12.8) both go through an interface the *host* implements, so the
capability exists and the policy is never mzs's. Starting a process is the line that does
not move — there is no `system`, no `exec`, no `spawn`, and no option that would add one.

---

*End of specification.*
