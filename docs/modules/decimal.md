# The `decimal` module

Exact base-ten numbers: what a price is, what a float is not, and the fourteen members that
add, round, print and split one.

```
include decimal

price = decimal.of("1500.35")
total = decimal.plus(price, decimal.times(price, decimal.of("0.20")))
decimal.str(total, 2)                    # "1800.42"
```

`decimal` needs no host capability — `include decimal` is enough, in the CLI and in an
embedding host alike.

## Why the module exists

A float is binary, and no binary fraction is `0.1`:

```
0.1 + 0.2                    # 0.30000000000000004
(0.1 + 0.2) == 0.3           # false
1500.35 * 100                # 150034.99999999997
```

An int is exact until it is not: past `2**63` it is promoted to a float rather than raised
on, so a total quietly loses its last digits.

```
9223372036854775807 + 1      # 9223372036854776000.0
type(9223372036854775807 * 10)   # float
```

Both are the right defaults for a language that mostly weighs conditions, and both are
wrong for money. A decimal keeps the digits you wrote, in base ten, and raises where it
cannot.

```
include decimal
decimal.str(decimal.plus(decimal.of("0.1"), decimal.of("0.2")))    # "0.3"
decimal.plus(decimal.of("0.1"), decimal.of("0.2")) == decimal.of("0.3")   # true
```

## A decimal is a dict

There is no `decimal` **kind**. A decimal is a dict of two entries carrying a record label
([../language/values.md](../language/values.md)): the value is `units × 10 ** -scale`.

```
include decimal
decimal.of("1500.35")            # {"units":150035,"scale":2}
type(decimal.of("1500.35"))      # Decimal
decimal.of("1.5").is("dict")     # true — it never stopped being one
```

The form is **canonical**: trailing zeros of the fraction are shed as a value is built, so
one number has exactly one form and `==` is the numeric question.

```
include decimal
decimal.of("1.50") == decimal.of("1.5")     # true
decimal.of("1.50").json                     # {"units":15,"scale":1}
decimal.of("2.000") == decimal.of(2)        # true
```

Scale is therefore a fact about the *number*, not about the column it prints in. How many
places to show is a separate question with its own row — `decimal.str(d, 2)`.

## Members

| Member | Arguments | Notes |
|---|---|---|
| `of` | `(x: string \| int \| decimal)` | the one way in; a float is refused |
| `plus` | `(a, b, *rest)` | `+`, exact, variadic |
| `minus` | `(a, b)` | `-`, exact |
| `times` | `(a, b, *rest)` | `*`, exact, variadic |
| `div` | `(a, b, places = nil, mode = "half_up")` | `/`; exact, or say how many places |
| `neg` / `abs` | `(a)` | |
| `cmp` | `(a, b)` | `-1`, `0`, `1` — the `<=>` a dict does not have |
| `round` | `(a, places, mode = "half_up")` | `places` may be negative |
| `str` | `(a, places = nil)` | canonical, or padded and rounded to exactly `places` |
| `float` | `(a)` | lossy, on purpose |
| `int` | `(a)` | truncates toward zero |
| `sum` | `(xs: array)` | exact total; `[]` is `0` |
| `split` | `(a, ways, places)` | parts that add back up to `a` |

```
include decimal
decimal.keys
# ["of","plus","minus","times","div","neg","abs","cmp","round","str","float","int","sum","split"]
```

Every row but `of` takes a decimal **or an int** — an int is exact and needs no conversion —
and refuses a string, because `of` is the row that reads text and conversions are explicit.

```
include decimal
decimal.str(decimal.times(decimal.of("1.05"), 3))      # "3.15"    an int mixes in
decimal.plus(decimal.of("1"), "2")
# type: decimal.plus expects a decimal or an int, got string; read it first with decimal.of("2")
```

## Why `plus` and not `add`

`add`/`sub`/`mul`/`div` is the obvious set, and `sub` is a name the language has already
spent: `.sub` is the fix-it for `replace_first`
([../cli/diagnostics.md](../cli/diagnostics.md)), so a member spelled `sub` would be a parse
error in every file that wrote it. Four names after the four operators is one story; three
names and an exception is not.

## Reading text

`of` reads digits, one dot and an optional sign. Nothing else: no exponent, no thousands
separator, no decimal comma. A price that arrives as `"1 500,35"` is a column that needs a
decision, and the module will not make it for you.

```
include decimal
[decimal.str(decimal.of("1500.35")), decimal.str(decimal.of("+3.25")),
 decimal.str(decimal.of(".5")), decimal.str(decimal.of("  1.5  "))].json
# ["1500.35","3.25","0.5","1.5"]

decimal.of("1 500,35")    # decimal: decimal.of: cannot read "1 500,35" as a decimal …
decimal.of("1.5e3")       # decimal: decimal.of: cannot read "1.5e3" as a decimal …
```

A **float** is refused too, and the message hands back the string to parse instead: a float
that reached the call has already lost the digits the module exists to keep.

```
include decimal
decimal.of(1500.35)
# type: decimal.of: a float has already lost the exact digits; write the number as text
# — decimal.of("1500.35")
```

## Division says when it cannot

With `places`, `div` rounds to that many. Without, it insists on an exact answer — and
where there is none it raises, rather than picking a precision nobody asked for.

```
include decimal
decimal.str(decimal.div(decimal.of(10), decimal.of(4)))       # "2.5"
decimal.str(decimal.div(decimal.of(1), decimal.of(8)))        # "0.125"
decimal.str(decimal.div(decimal.of(1), decimal.of(3), 4))     # "0.3333"

decimal.div(decimal.of(1), decimal.of(3))
# decimal: decimal.div: 1 / 3 has no exact decimal form within 18 places; say how many — decimal.div(a, b, 2)

decimal.div(decimal.of(1), decimal.of(0))
# zero-division: divided by 0
```

## Rounding

Two modes. `"half_up"` goes away from zero on a tie and is the default, because that is what
`round` already does to a number ([../stdlib/numbers.md](../stdlib/numbers.md));
`"half_even"` is the banker's rounding a ledger asks for by name.

```
include decimal
decimal.str(decimal.round(decimal.of("2.665"), 2), 2)                 # "2.67"
decimal.str(decimal.round(decimal.of("2.665"), 2, "half_even"), 2)    # "2.66"
decimal.str(decimal.round(decimal.of("2.675"), 2, "half_even"), 2)    # "2.68"
decimal.str(decimal.round(decimal.of("1250"), -2))                    # "1300"

decimal.round(decimal.of(1), 2, "ceil")
# argument: decimal.round: unknown rounding mode "ceil"; the modes are "half_up" and "half_even"
```

`round` changes the **number**; `str` changes the **rendering**. Rounding 1.50 to two places
is still 1.5, and printing 1.5 at two places is `"1.50"`.

```
include decimal
decimal.str(decimal.round(decimal.of("1.50"), 2))    # "1.5"
decimal.str(decimal.of("1.5"), 2)                    # "1.50"
```

## Comparing and sorting

A dict has no ordering, so `<` on two decimals says so instead of guessing — and `cmp` is
what `sort`'s comparator wants anyway:

```
include decimal
decimal.of("9") < decimal.of("10")      # type: cannot compare dict with dict

["9.5", "10", "0.75"].map { decimal.of(it) }
  .sort { (a, b) -> decimal.cmp(a, b) }
  .map { decimal.str(it) }
# ["0.75","9.5","10"]
```

That is the trade the dict form makes, and it is the right way round: a decimal carried as
a string would answer `"9.00" < "10.00"` with `false` and never say a word. For the same
reason a decimal is not a dict key — `{(decimal.of("1.5")): true}` is `dict key must be
hashable, got dict`.

## `+` between two decimals is an error

`+` on dicts merges, and merging two dicts of one shape keeps the right-hand one — a wrong
answer with no diagnostic. Two labelled dicts therefore refuse to add:

```
include decimal
decimal.of("1500.35") + decimal.of("20")
# type: cannot add Decimal to Decimal: '+' merges dicts, so this is the right-hand value
# and not a sum; add two decimals with 'decimal.plus' (§12.15), and overwrite fields with 'merge'
```

The rule reads off the record label and applies to every shape
([../language/values.md](../language/values.md)); a plain dict on either side merges exactly
as it always did.

## Splitting without losing a kopeck

```
include decimal
decimal.split(decimal.of("10"), 3, 2).map { decimal.str(it, 2) }
# ["3.34","3.33","3.33"]
decimal.str(decimal.sum(decimal.split(decimal.of("10"), 3, 2)), 2)
# "10.00"
```

The places are given rather than taken from the value, because a canonical decimal has no
memory of its zeros: `"10.00"` *is* `10`, and splitting it at zero places would be
`[4, 3, 3]`. The remainder goes to the first parts, in order, so the answer is the same on
every run.

## The width, and what happens at the edge

The digits live in an int, so `|units| < 2**63` and `scale` is `0..18`. Past either edge the
module raises — kind `decimal` — where an int would have promoted to a float:

```
include decimal
decimal.str(decimal.of("9223372036854775807"))        # "9223372036854775807"
decimal.of("9223372036854775808")
# decimal: decimal.of: 9223372036854775808 does not fit a decimal (the digits live in an int,
# so |units| < 2**63)
decimal.times(decimal.of("0.0000000001"), decimal.of("0.0000000001"))
# decimal: decimal.times: the result needs 20 decimal places and a decimal holds 18
```

Text too long to be a decimal is refused by its digit count, before anything is converted:
a script may hand `of` a string as long as `MaxStringBytes` allows, and a conversion that
took a minute would be a minute the deadline could not interrupt
([../reference/sandbox.md](../reference/sandbox.md)).

```
include decimal
decimal.of("9" * 8_000_000)
# decimal: decimal.of: "999999999999999999999999…" has 8000000 digits before the dot and a
# decimal holds 19 (the digits live in an int, so |units| < 2**63)
```

## Storing one

`json` writes the dict, and `of` reads that dict back — label or no label — so a price
survives a round trip through a store or an HTTP body:

```
include decimal
include json
decimal.str(decimal.of(json.parse("{\"units\":150035,\"scale\":2}")), 2)    # "1500.35"
```

What `json` does **not** do is write `1500.35`: a money field in a document is
`decimal.str(d, 2)`, written out where a reader can see the places being chosen.

```
include decimal
include json
{total: decimal.str(decimal.of("1500.3"), 2)}.json     # {"total":"1500.30"}
```

## Errors

| Kind | When |
|---|---|
| `decimal` | text that is not a decimal, a result past the width or the 18 places, a division with no exact form |
| `type` | a float or a string where a decimal was expected, a dict that is not one |
| `argument` | an unknown rounding mode, `places` out of range, `ways` below 1 |
| `zero-division` | `decimal.div(a, decimal.of(0))` |

All of them are ordinary catchable errors ([../language/errors.md](../language/errors.md)):

```
include decimal
try decimal.of($amount) else (e) -> "не число: ${e["message"]}"
```

## See also

- [../stdlib/numbers.md](../stdlib/numbers.md) — int and float, `round`, and why `/` truncates
- [../language/values.md](../language/values.md) — `record` shapes, equality, and the label a decimal carries
- [./json.md](./json.md) — what `json` writes for a dict
- [./README.md](./README.md) — `include`, and why `decimal(1)` is an error
- [../../examples/41_decimal_money.mzs](../../examples/41_decimal_money.mzs) — an invoice, end to end
