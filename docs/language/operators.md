# Operators

Every operator, its precedence, and the four rules that trip people up: integer division,
the sign of `%`, no implicit conversion, and the three runes that are not operators at all.

## Precedence

Tightest (1) to loosest (15); left-associative unless the table says otherwise.

| # | Operators | Assoc |
|---|---|---|
| 1 | `x.y` `x?.y` `x(…)` `x[…]` `x {…}` | left |
| 2 | `**` | right |
| 3 | `!` `-` `+` (unary) | right |
| 4 | `*` `/` `%` | left |
| 5 | `+` `-` | left |
| 6 | `..` `..<` | non-assoc |
| 7 | `in` | non-assoc |
| 8 | `<` `<=` `>` `>=` `<=>` | left |
| 9 | `==` `!=` `~` `!~` | left |
| 10 | `&&` | left |
| 11 | `\|\|` | left |
| 12 | `??` | left |
| 13 | `? :` and `try … else …` | right |
| 14 | `=` `:=` `+=` `-=` `*=` `/=` `%=` `**=` `\|\|=` `&&=` `??=` | right |
| 15 | modifiers `if` `while` | statement level |

```
2 + 3 * 4              # 14
1 + 2 == 3             # true    == is looser than +
!true == false         # true    ! binds tighter than ==
2 ** 3 ** 2            # 512     right-associative
1 + 2 .. 5             # 3..5    .. is looser than +
nil || nil ?? "d"      # d       ?? is looser than ||
false ?? "x" || "y"    # false
```

## Arithmetic

```
7 / 2          # 3       both sides Int -> integer division, truncated toward zero
-7 / 2         # -3
7.0 / 2        # 3.5
7 % 3          # 1
-7 % 3         # 2       % takes the sign of the divisor
7 % -3         # -2
2 ** 10        # 1024
2 ** -1        # 0.5     a negative exponent gives a Float
1 / 0          # zero-division: divided by 0
1 / 0.0        # Infinity   float division never raises
```

Int overflow **promotes to Float**, it never wraps:

```
9223372036854775807 + 1     # 9223372036854776000.0
2 ** 63                     # 9223372036854776000.0
```

`+` also works on strings, arrays and dicts, `*` on strings and arrays, `-` on arrays only,
and `%` on a string is `format`:

```
"a" + "b"              # ab
"ab" * 3               # ababab
[1,2] + [3]            # [1,2,3]
[1,2] * 2              # [1,2,1,2]
[1,2,3] - [2,3,9]      # [1]      set difference, order preserved
{a: 1} + {a: 2, b: 3}  # {"a":2,"b":3}   right side wins
"%s has %d" % ["a", 2] # a has 2  format
```

## No implicit conversion

```
"2" + 1     # -e:1:5: type: cannot add int to string
1 + "2"     # -e:1:3: type: cannot add string to int
1 < "2"     # -e:1:3: type: cannot compare int with string
-"a"        # -e:1:1: type: cannot negate string
```

Convert on purpose: `"2".int + 1` is `3`. String interpolation is the one place a value is
converted for you.

## Comparison

`<` `<=` `>` `>=` `<=>` are defined for numbers (mixed Int/Float), strings (by code point),
arrays (element-wise, then by length) and bools (`false < true`). `<=>` gives `-1`, `0`, `1`,
or `nil` when the operands are incomparable.

```
5 <=> 5.0          # 0
"b" <=> "a"        # 1
[1] <=> [1,2]      # -1
inspect(1 <=> "a") # nil
1 < 2 < 3          # type: cannot compare bool with int — comparisons do not chain
```

`==` never raises across kinds; see [./values.md](./values.md).

## Regex match

`~` and `!~` always return a Bool, and one side must be a regex.

```
"привет" ~ /вет/    # true
"abc" !~ /z/        # true
nil ~ /a/           # false
1 ~ /1/             # type: cannot match against int
"a" ~ "a"           # type: ~ needs a regex operand, got string and string
"abc" == /abc/      # syntax: '==' with a regex operand: use '~' to match
```

The *position* of a match is `s.index(/re/)`. See [./regex.md](./regex.md).

## Logic and nil

`&&` returns the left operand if it is falsy, `||` returns it if it is truthy — both return
an operand, not a Bool. `??` fires on `nil` only. `!` always returns a Bool.

```
0 && "x"           # x     0 is truthy
false || "x"       # x
nil ?? "fallback"  # fallback
false ?? "back"    # false  — this is where ?? differs from ||
!0                 # false
```

`?.` stops a whole postfix chain at the first `nil`, and the arguments after it are not
evaluated: `inspect(nil?.upper.len)` is `nil`, `{a: 1}?.get("a")` is `1`.

## Ranges

`a..b` includes `b`, `a..<b` does not. Both are lazy and non-associative.

```
(1..<5).array      # [1,2,3,4]
(5..1).array       # []        descending is empty
(1..5).len         # 5
1..2..3            # syntax: range operator is non-associative
0..5.map { it }    # syntax: ambiguous range: write (0..5).map
```

## Membership: `in`

`x in xs` asks `xs` whether it holds `x`, and always returns a Bool. It is `xs.has(x)`
under a second spelling, so every kind that answers `has` answers `in`, and a kind that
grows a `has` grows `in` with it.

```
5 in 1..20            # true
20 in 1..<20          # false
2 in [1, 2, 3]        # true
"k" in {k: 1}         # true    a dict answers about its keys
"вет" in "привет"     # true    a string about its substrings
1 in 5                # type: the right side of 'in' must have members … got int
```

It binds looser than a range and tighter than a comparison, which is what makes both of
these read the way they are written:

```
if code in 200..<300 { … }        # in (200..<300)
if a in xs && b in ys { … }       # (a in xs) && (b in ys)
```

There is no `not in`; the negation is `!(x in xs)`. Chaining is refused —
`a in b in c` is `syntax: 'in' is non-associative`.

The same question is spelled `in 1..5 -> …` as a `match` arm ([./control-flow.md](./control-flow.md)).
The `in` of `for x in xs` is a different word in the same spelling: it names the thing being
iterated, and asks nothing.

## Assignment

`=` writes the nearest existing binding and creates one here if there is none; `:=` always
creates or shadows in the current scope. The compound forms evaluate the target once.

```
a = 5;     a ||= 9; a      # 5   ||= assigns only when falsy
c = false; c ||= 9; c      # 9
b = nil;   b ??= 9; b      # 9   ??= assigns only when nil
d = 1;     d &&= 2; d      # 2
x = 10;    x **= 2; x      # 100
z = [1,2]; z[0] += 10; z   # [11,2]
```

The full list: `= := += -= *= /= %= **= ||= &&= ??=`. Commas on the left destructure —
[./destructuring.md](./destructuring.md).

## Ternary

`x = 3 > 2 ? "yes" : "no"` gives `yes`. A ternary can never collide with `?.`, because a
float must start with a digit and `x ? .5 : 1` is therefore unwritable.

## `&`, `|`, `^` are not operators

Bit operations are functions, so that `&` can never be confused with `&&`:

```
1 & 2   # syntax: '&' is not an mzs operator; use band(a, b), or '&&' for logical and
1 | 2   # syntax: '|' is not an mzs operator; use bor(a, b), or '||' for logical or
2 ^ 3   # syntax: '^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power
```

`band` `bor` `bxor` `bnot` `shl` `shr` stay in `int64` and never promote —
[../stdlib/numbers.md](../stdlib/numbers.md).

```
[band(6, 3), bor(6, 3), bxor(6, 3), bnot(0)]   # [2,7,5,-1]
```

## Unary minus and `**`

```
-(2 ** 2)   # -4
(-2) ** 2   # 4
-2 ** 2     # syntax: ambiguous: write -(2 ** 2) or (-2) ** 2
```

## See also

- [./README.md](./README.md) — line breaks: an operator at either end of a line joins the two
- [./values.md](./values.md) — what the operands are
- [./control-flow.md](./control-flow.md) — modifiers, the loosest level of all
- [./regex.md](./regex.md) — what `~` matches against
- [../stdlib/numbers.md](../stdlib/numbers.md) — bit functions, rounding, clamping
