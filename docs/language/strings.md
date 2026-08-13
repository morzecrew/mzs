# Strings

String literals, the two quote forms, `$`-interpolation, escapes, rune-based indexing and the
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
