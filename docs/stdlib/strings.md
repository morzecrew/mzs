# String functions

Every operation on a String, grouped by what it does, with rune semantics spelled out.

Strings are immutable: nothing here mutates the receiver, every row returns a new value.
All indices and lengths are **runes**, never bytes. Padding is the one thing counted in
display columns instead, because a column is what a table lines up by — `width` below.

```
"привет".len          # 6
"привет".bytes.len    # 12
"привет".index("вет") # 3
"👋 hi".len           # 4
"👋 hi".width         # 5 — the emoji draws in two columns
```

## Case

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `lower` | `-> string` | Unicode lowercase | `"ПРИВЕТ".lower` → `привет` |
| `upper` | `-> string` | Unicode uppercase | `"Ёлка".upper` → `ЁЛКА` |
| `capitalize` | `-> string` | first rune upper, rest lower | `"ПРИВЕТ мир".capitalize` → `Привет мир` |
| `swapcase` | `-> string` | inverts each rune's case | `"aBc".swapcase` → `AbC` |
| `casefold` | `-> string` | the key for comparing without case | `"Straße".casefold` → `strasse` |

`casefold` is not `lower` with a longer name: it is Unicode's *full* case folding, the
mapping made for the question "are these the same text if case is not looked at". It answers
where lowercasing does not —

```
"Straße".casefold == "STRASSE".casefold    # true
"Straße".lower == "STRASSE".lower          # false
"ΟΔΟΣ".casefold == "Οδος".casefold         # true — final ς and Σ both fold to σ
"K".casefold == "k"                        # true — that K is the Kelvin sign U+212A
```

— and the price is that a folded string is a key and not text: `"ß".casefold.len` is `2`, so
fold both sides of a comparison and show neither. It is case that folding removes, not
accents: `"Οδός".casefold` is `οδόσ`, the same way `ёж` is not `еж`.

## Trimming

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `trim` | `-> string` | both ends | `"  ОПЕРАТОР  ".trim` → `ОПЕРАТОР` |
| `trim_start` / `trim_end` | `-> string` | one end | `"  x  ".trim_end` → `"  x"` |
| `chomp` | `(suffix: string = "\n") -> string` | drops one trailing `\n`/`\r\n`, or a given suffix; `chomp("")` drops every trailing newline | `"file.txt".chomp(".txt")` → `file` |
| `chop` | `-> string` | drops the last rune (`\r\n` counts as one) | `"abc".chop` → `ab` |
| `squeeze` | `(set: string = "") -> string` | collapses runs of repeated runes, or only the named ones | `"aa  bb".squeeze(" ")` → `aa bb` |

`trim` removes Unicode whitespace — which already covers NBSP (U+00A0) — **plus** U+200B and
U+FEFF, the two invisibles that are not `White_Space` and still arrive in pasted text.

## Testing

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `len` | `-> int` | rune count | `"привет".len` → `6` |
| `width` | `-> int` | display columns | `"日本".width` → `4` |
| `empty` | `-> bool` | `len == 0` | `" ".empty` → `false` |
| `blank` | `-> bool` | empty after `trim` | `" ".blank` → `true` |
| `has` | `(sub: string) -> bool` | substring test | `"hello".has("lo")` → `true` |
| `starts_with` | `(*prefixes: string) -> bool` | any prefix matches | `"/cmd".starts_with("/", "!")` → `true` |
| `ends_with` | `(*suffixes: string) -> bool` | any suffix matches | `"a.png".ends_with(".jpg", ".png")` → `true` |

`has`, `starts_with` and `ends_with` take strings only — `"abc".has(/b/)` raises
`type: has expects a string, got regex`. For a regex test write `"abc" ~ /b/`.

## Searching

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `index` | `(needle: string \| regex, from: int = 0) -> int \| nil` | rune index, `nil` when absent | `"привет".index(/ВЕТ/i)` → `3` |
| `last_index` | `(needle: string \| regex) -> int \| nil` | last occurrence; no `from` | `"abcabc".last_index("b")` → `4` |
| `count` | `(sub: string \| regex) -> int` | non-overlapping occurrences | `"a1b22".count(/\d/)` → `3` |
| `matches` | `(re: regex) -> array` | every match; with groups, group arrays | `"a1 b2".matches(/[a-z]\d/)` → `["a1","b2"]` |
| `captures` | `(re: regex) -> array \| nil` | whole match then groups, `nil` on no match | `"2024-05".captures(/(\d+)-(\d+)/)` → `["2024-05","2024","05"]` |

```
"a1 b2".matches(/([a-z])(\d)/)          # [["a","1"],["b","2"]]
"2024-05".captures(/(?P<y>\d+)/)["y"]   # 2024 — named groups are keys too
"no".captures(/(\d)/)                   # nil
"abcabc".index("b", 2)                  # 4 — the search starts at rune 2
```

A **string** needle is matched literally, even where a regex is allowed:
`"a.c".index(".")` is `1` while `"a.c".index(/./)` is `0`.
`matches` and `captures` accept a regex only — `"abc".matches("b")` raises.

## Splitting and joining

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `split` | `(sep: string \| regex = " ", limit: int = -1) -> array` | see below | `"a:b:c".split(":")` → `["a","b","c"]` |
| `lines` | `-> array` | splits on `\n`, terminator dropped, CRLF handled | `"a\r\nb\r\n".lines` → `["a","b"]` |
| `chars` | `-> array` | one-rune strings | `"да".chars` → `["д","а"]` |
| `bytes` | `-> array` | byte values; `array.pack_bytes` is the inverse | `"я".bytes` → `[209,143]` |

```
"a b  c".split          # ["a","b","c"] — " " splits on runs of whitespace
"a:b:c".split(":", 2)   # ["a","b:c"]
"abc".split("")         # ["a","b","c"] — "" splits into runes
"a1b2".split(/\d/)      # ["a","b",""] — empty fields are kept
```

Joining lives on Array: `"да".chars.join("-")` → `д-а`. See [arrays.md](./arrays.md).

## Replacing

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `replace` | `(pat: string \| regex, repl: string \| fn) -> string` | replaces **all** | `"a-b-a".replace("a", "X")` → `X-b-X` |
| `replace_first` | `(pat, repl) -> string` | replaces the first | `"a-b-a".replace_first("a", "X")` → `X-b-a` |

A string replacement understands `\0`, `\1`..`\9` and `\k<name>` — write it as a
**single-quoted (raw) string**, since `"\1"` is the unknown escape for `1`:

```
"John Smith".replace(/(\w+) (\w+)/, '\2, \1')   # Smith, John
"John Smith".replace(/(\w+) (\w+)/, "\2, \1")   # 2, 1  ← the escape ate the backslash
'2024-05'.replace(/(?P<y>\d+)-(?P<m>\d+)/, '\k<m>/\k<y>')   # 05/2024
```

A closure replacement receives the match array **spread** over its parameters: the whole
match first, then one parameter per group.

```
"a1b2".replace(/\d+/) { it.int + 1 }                  # a2b3
"ab cd".replace(/(\w)(\w)/) { (_, a, b) -> b + a }    # ba dc
```

## Slicing and padding

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `first` / `last` | `(n: int = 1) -> string` | first/last `n` runes | `"привет".last(2)` → `ет` |
| `first_and_last` | `-> string` | `first(1) + last(1)` | `"привет".first_and_last` → `пт` |
| `slice` | `(i: int, n: int = 1) -> string` | same as `s[i, n]`; negative `i` from the end | `"привет".slice(1, 3)` → `рив` |
| `reverse` | `-> string` | rune-wise | `"👋 hi".reverse` → `ih 👋` |
| `ljust` / `rjust` / `center` | `(width: int, pad: string = " ") -> string` | pads to `width` **columns** | `"ab".center(7, "-")` → `--ab---` |

```
"привет"[-1]       # т
"abc".slice(9)     # nil — out of range
"ab".ljust(1)      # ab — a width below the length changes nothing
```

The three padding rows count columns, not runes, which is what makes a table with CJK or
emoji in it come out straight:

```
["Ivan", "日本語", "🙂 ok"].map { it.ljust(8, ".") + "|" }
# ["Ivan....|", "日本語..|", "🙂 ok...|"]
```

`center` puts the odd column on the right. Padding two columns wide stops a column short
rather than overrunning the width that was asked for (`"a".ljust(4, "日")` is `a日`), and
padding of no width at all — `""`, or a lone combining mark — is an argument error rather
than a loop that never ends.

The unit is the code point, which is the unit Unicode gives a width to. An emoji sequence
joined with U+200D is therefore counted emoji by emoji — `"👩‍🎨".width` is `4` — because
whether a terminal composes such a sequence into one glyph or draws two is the terminal's
decision; the same table moves the REPL's own cursor, where guessing wrong costs two columns
per emoji.

## Iteration

`each_char { (c) -> … } -> string` runs the closure per rune and returns the receiver:

```
"abc".each_char { print(it.upper) }    # writes ABC, evaluates to "abc"
```

## Conversion and formatting

| Method | Signature | Does | Example → value |
|---|---|---|---|
| `int` | `-> int` | leading base-10 digits, never raises | `"12abc".int` → `12` |
| `float` | `-> float` | leading number, never raises | `"3.7kg".float` → `3.7` |
| `str` | `-> string` | itself | `"a".str` → `a` |
| `json` | `-> string` | JSON-quoted | `"a\"b".json` → `"a\"b"` |
| `ord` | `-> int` | code point of the first rune | `"Я".ord` → `1071` |
| `%` | `s % (args) -> string` | operator, not a method: `format` with the receiver as the layout | `"%s-%d" % ["a", 1]` → `a-1` |

```
"0x1f".int      # 0 — base 10 only
"".ord          # argument: ord: empty string
"%d%%" % [50]   # 50%
```

`1071.chr` is the other direction; see [numbers.md](./numbers.md).

## See also

- [core.md](./core.md) — `format`, `len`, `inspect` and the shared conversions
- [arrays.md](./arrays.md) — `join`, `pack_bytes`, and what `split`/`chars` return
- [../language/strings.md](../language/strings.md) — literals, escapes, `$` interpolation
- [../language/regex.md](../language/regex.md) — the `~` operator, flags, dynamic patterns
