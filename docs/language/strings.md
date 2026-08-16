# Strings

String literals, the three forms, `$`-interpolation, escapes, rune-based indexing and the
`%` format operator.

## Two quote forms

```
"escapes and interpolation"
'raw: the only escapes are \' and \\'
```

```
inspect("a\tb\nc")            # "a\tb\nc"
inspect('a\nb')               # "a\\nb"   — two characters, not a newline
inspect('it\'s')              # "it's"
```

Single quotes are the form for regex source text: `regex('\bмен', 'i')`. Both forms may span
lines; a raw newline in the source is a newline in the value. An unterminated literal is
`syntax: unterminated string literal`, reported at the opening quote.

## Heredoc

The third form, for the text that would otherwise be a pile of `"\n"`: a template, a query,
a message body. `<<~TAG` takes the lines **below** it, up to a line holding `TAG` alone, and
sheds the indentation they share — so the text lines up with the code and comes out flush
left.

```
name = "Ann"

sql = <<~SQL
  SELECT id
    FROM users
   WHERE name = '${name}'
SQL

sql            # "SELECT id\n  FROM users\n WHERE name = 'Ann'\n"
```

One form and not three:

| Written | Reads like | Interpolates | Escapes |
|---|---|---|---|
| `<<~TAG` | `"…"` | yes | yes |
| `<<~'TAG'` | `'…'` | no | no |

```
<<~'TPL'
  pay at /pay?id=$id — the ${…} belongs to something else, and \n is two characters
TPL
```

Inside a body a `"` is an ordinary quote and a `#` is an ordinary hash: neither has any
meaning in a string literal. The shed prefix is the shortest run of leading blanks over the
body's **non-blank** lines, so a paragraph break costs nothing and a deliberately deeper
line keeps its extra. The terminator may be indented with the body. A body always ends in a
newline.

The tag is an ordinary operand, so the rest of its line is read as usual and the body comes
from below — which is what lets a heredoc stand in an argument list:

```
report = <<~HEAD + rows.join("\n") + "\n" + <<~FOOT
  order   qty
  ─────────────
HEAD
  ─────────────
  end of report
FOOT
```

Both bodies are read in the order the tags are written, and the line's own break still
ends the statement.

Two things a heredoc will not do. It may not open inside a `${ … }` — the lines under an
interpolation already belong to the string it sits in — and a body with no terminator is
`syntax: unterminated heredoc`, reported at the `<<~`. In the REPL that message is the
signal to keep typing, so a heredoc can be entered a line at a time.

## Escapes

| Escape | Meaning |
|---|---|
| `\n \r \t \0 \a \b \f \v \e` | the usual control characters |
| `\\ \" \'` | backslash, quote |
| `\$` | a literal `$` |
| `\uXXXX` | a code point, exactly four hex digits |
| `\u{X…}` | a code point, 1–6 hex digits |
| `\xNN` | a byte, two hex digits |

An unknown escape is the literal character: `inspect("\c")` is `"c"`.

```
"\u{1F600}"                   # 😀
"\x41"                        # A
inspect("10\$ off")           # "10$ off"
```

## Interpolation

| Form | Meaning |
|---|---|
| `$name` | the **host** variable `$name` — exactly what `$name` means outside a string |
| `${ expr }` | any expression, including local variables |
| `$` not followed by a name or `{` | a literal `$` |

```
"Hello, $name!"               # with -v name=Ann → Hello, Ann!
"price: 100 $"                # price: 100 $
"${1 + 2 * 3}"                # 7
"${[1, 2].map { it * 2 }.json}"          # [2,4]
x = "in"; "a${x == "in" ? "yes" : "no"}b"    # ayesb
```

A **local** variable needs the braces, because `$s` is the host table either way:

```
s = "local"; inspect("$s")    # ""      — no host variable named $s
s = "local"; "${s}"           # local
```

`$name` takes the longest run of name characters, so `${n}` is how a name ends early:
`n = 5; "${n}00"` is `500`, whereas `"$n00"` reads the host variable `$n00`.

Interpolation is the only place where a conversion is implicit; it applies `str`:

```
"${nil}|${true}|${1.5}|${2.0}|${[1, 2]}|${{a: 1}}|${/x/i}"
# |true|1.5|2.0|[1,2]|{"a":1}|/x/i
```

Everywhere else there is none: `"a" + 1` is `type: cannot add int to string`.

Single quotes interpolate nothing: `inspect('Hello, $name!')` is `"Hello, $name!"`. Regex
literals do not interpolate either — build dynamic patterns with `regex(src, flags)`.

## Runes, not bytes

Length, indexing, slicing, `chars` and `reverse` all work on runes.

```
"привет".len                  # 6
"привет".bytes.len            # 12
"привет"[0]                   # п
"привет"[-1]                  # т
"привет"[1, 3]                # рив     — start, count
"привет".slice(1, 3)          # рив
"привет"[10]                  # nil     — out of range, not an error
"abc".chars                   # ["a","b","c"]
"éx".reverse                  # xé
"привет".index(/вет/)         # 3       — a rune index
```

A rune is not a grapheme: `"👩‍👩‍👧 family".len` is `12`, because the family emoji is five code
points joined by zero-width joiners. Case mapping is Unicode-aware and not always
reversible — `"я".upper` is `Я`, `"Straße".upper` is `STRAßE`.

Strings are immutable — `s[0] = "x"` is `type: cannot assign to an index of string` — so every
string method returns a new string.

## The `%` format operator

`fmt % args` is the `format` function as an operator. A single argument needs no array.

```
"%s has %d items" % ["cart", 3]          # cart has 3 items
"%s" % 1                                 # 1
format("%s-%s", "a", "b")                # a-b
```

| Verb | Result |
|---|---|
| `%s` | `str` of the value |
| `%d`, `%i` | integer |
| `%f %g %e` | float |
| `%x %X %o %b` | integer in base 16/8/2 |
| `%c` | one character |
| `%j` | JSON |
| `%%` | a literal `%` |

Flags `- + 0 space #`, a width and a `.precision` work as in Go, and a Dict argument may be
addressed by name.

```
"%05.2f" % 3.14159                       # 03.14
"%-6s|" % "ab"                           # ab    |
"%x %b %o" % [255, 5, 8]                 # ff 101 10
"%d%%" % 50                              # 50%
"%j" % [[1, 2]]                          # [1,2]
"%<name>s is %<age>d" % {name: "Ann", age: 34}   # Ann is 34
"%{name}" % {name: "Ann"}                # Ann
```

## See also

- [Host variables](./host-variables.md) — what `$name` reads inside a string
- [Regular expressions](./regex.md) — raw strings and `regex(src, flags)`
- [String functions](../stdlib/strings.md) — the full method table
- [Values](./values.md) — `str`, `inspect` and truthiness
