# The mzs language

The rules that make the syntax predictable, the complete keyword list, and an index of the
language pages.

## Three rules

**`{ … }` is a closure — unless it is a dict.** Position alone decides, never parser
state: after a header it is the body, after a call it is the trailing closure, and in
operand position it is a dict when it starts like one (`{a: 1}`, `{"a": 1}`, `{}`) and a
closure otherwise. The readings never overlap, because a body can never begin with
`name :`.

```
if c { a } else { b }               # body
[1,2,3].map { it * 2 }              # [2,4,6] — trailing closure
double = { (n) -> n * 2 }; double(4) # 8 — closure value
config = {retries: 3, mode: "fast"} # dict
```

**`[ … ]` is always an array.** Never a dict, never a third thing — keys live in braces.

```
[[], [1, 2, 3], {}, {name: "Ivan"}]   # [[],[1,2,3],{},{"name":"Ivan"}]
```

One spelling each, so a JSON document is already an mzs literal and pastes into source
unedited. `[a: 1]` and `[:]` are diagnostics naming `{a: 1}` and `{}`. See
[values](./values.md#collections).

**`x.f(y)` is exactly `f(x, y)`** (UFCS, one flat namespace). `len(s)` and `s.len` are the
same function, and so is a function you wrote yourself.

```
fn shout(s) { s.upper + "!" }
"yes".shout      # YES!
len("abc") == "abc".len   # true
```

## Everything is an expression

`if`, `while`, `for`, `match` and `try` all have a value; the value of a program, a function
body and a closure is the value of its last statement. `return` exists only for early exit.

```
x = if 3 > 5 { "big" } else if 3 > 2 { "mid" } else { "small" }   # mid
```

## Nothing converts itself

There is exactly one implicit conversion in the language: string interpolation. Everything
else is spelled out.

```
"2" + 1      # -e:1:5: type: cannot add int to string
"2".int + 1  # 3
```

## Ambiguity is a diagnostic

Constructs that other languages read with a surprising precedence rule are rejected with a
fix-it instead. A few of the ~30 in [../cli/diagnostics.md](../cli/diagnostics.md):

| Input | Message |
|---|---|
| `-2 ** 2` | `ambiguous: write -(2 ** 2) or (-2) ** 2` |
| `0..5.map { it }` | `ambiguous range: write (0..5).map` |
| `s == /re/` | `'==' with a regex operand: use '~' to match` |
| `a & b` | `'&' is not an mzs operator; use band(a, b), or '&&' for logical and` |
| `[a: 1]` | `a dict is written {a: 1}` |
| `[:]` | `the empty dict is written {}` |
| `f {a: 1}` | `a dict after a call is written (a: 1) or ({a: 1})` |
| `if c {a: 1}` | `this '{' opens the if body; write { {a: 1} } for a dict` |
| `x.empty?` | `'?' is not part of an identifier; did you mean 'empty'?` |

## Keywords

Sixteen, and this is the whole list (`internal/token/token.go`):

```
fn  if  else  match  while  for  in  break  next  return  try  true  false  nil
include  export
```

Words that look like keywords and are not — none of them is in the list above:

| Word | What it really is |
|---|---|
| `async` | read positionally, only directly before `fn` ([./async.md](./async.md)) |
| `from` | read positionally, only inside `include x from "…"` |
| `it` | the parameter a closure with no parameter list declares |
| `_` | a plain name, by convention an unused parameter |
| `not` `unless` `until` `loop` `def` `elsif` `import` `require` `use` | nothing; a fix-it only in the keyword shape (`loop {`, `unless c`), an ordinary name otherwise |
| `and` `or` `do` `end` `then` `rescue` | nothing, and not usable as names either — every occurrence is a fix-it |

```
async = 1; from = 2; loop = 3; async + from + loop      # 6
and = 1     # syntax: 'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'
```

Indentation is never significant, and both a newline and `;` terminate a statement. `#`
starts a comment that runs to end of line, except inside a string literal where it is an
ordinary character.

```
"a # b"     # a # b
```

## Line breaks

A newline ends the statement unless the line cannot be finished yet, or the next line cannot
start one. Both tests are lexical, one token wide; indentation is never part of either.

| A newline is suppressed | Tokens |
|---|---|
| after | any operator, `=` and every compound assign, `,` `(` `[` `{` `->` `?` `:` `.` `?.` `..` `..<`, the keywords `fn if else match while for in return try`, and anything inside `${…}` |
| before | `.` `?.` `->` `else` `)` `]` `}`, and any binary operator |

After anything else — a name, a literal, `)` `]` `}`, `break`, `next`, `include` — the newline
terminates. That is what makes the three usual shapes legal:

```
"one two three".split(" ")
  .map { it.upper }
  .join("-")             # ONE-TWO-THREE

if 3 > 5 { "big" }
else { "small" }         # small

total = 1 +
  2 +
  3                      # 6
```

`;` is a hard terminator and is never suppressed; a trailing one is allowed.

```
a = 1; b = 2; a + b      # 3
```

The one shape that surprises: a line opening with `-` or `+` continues the line above it.

```
x = 1; x = 5
-x                       # 4 — read as x = 5 - x
```

## The pages

| Page | Covers |
|---|---|
| [./values.md](./values.md) | the kinds, every literal form, truthiness, copying |
| [./operators.md](./operators.md) | precedence, arithmetic, comparison, `~`, `??`, ranges, assignment |
| [./control-flow.md](./control-flow.md) | `if` / `while` / `for`, modifiers, `match` in depth |
| [./destructuring.md](./destructuring.md) | one shape rule in assignment, `match` arms and `for` |
| [./functions.md](./functions.md) | `fn`, closures, `it`, defaults, varargs, UFCS, scope |
| [./strings.md](./strings.md) | literals, interpolation, runes, escapes |
| [./errors.md](./errors.md) | `try` / `else`, `raise`, what is not catchable |
| [./host-variables.md](./host-variables.md) | `$variables` and the globals table |
| [./async.md](./async.md) | `async fn`, `await`, `done`, the scheduling model |
| [./regex.md](./regex.md) | the two engines, flags, dynamic patterns |

## See also

- [../getting-started/README.md](../getting-started/README.md) — first program
- [../stdlib/README.md](../stdlib/README.md) — the one flat namespace
- [../cli/diagnostics.md](../cli/diagnostics.md) — the full diagnostic table
- [../reference/limitations.md](../reference/limitations.md) — reserved syntax
