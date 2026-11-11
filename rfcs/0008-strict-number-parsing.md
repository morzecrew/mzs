# RFC 0008 — `parse_int` and `parse_float`: a conversion that can say no

**Status:** Landed (#22) · **Area:** stdlib · **Depends on:** — · **Audit:** L14

## Summary

Add two rows: `parse_int(s, base = 10) -> int | nil` and `parse_float(s) -> float | nil`.
They answer `nil` when the *whole* string is not a number. `int` and `float` are unchanged.

## Motivation

`int` never raises, by design: `$price.int + 1200` must be safe when `$price` is unset
(SPEC §12.7). The consequence is that there is no way to validate input:

```
"abc".int      # 0
"12abc".int    # 12
"".int         # 0
"x".float      # 0.0
"0x1f".int     # 0   — and no other way to read hex
```

A program reading a CSV column, a query parameter or a line of user input cannot tell `0`
from garbage. The working idiom is a regex in front of every conversion
(`s ~ /^-?\d+$/ ? s.int : nil`), which each author writes slightly differently and which
gets floats, signs and overflow wrong. For a language whose host values all arrive as
strings (D8), "is this string a number?" is a first-order question.

## Design

| Name | Signature | Semantics |
|---|---|---|
| `parse_int` | `(s: string, base: int = 10) -> int \| nil` | the integer `s` spells, or `nil` |
| `parse_float` | `(s: string) -> float \| nil` | the float `s` spells, or `nil` |

**`parse_int`.**

* Accepts: optional surrounding whitespace, an optional sign, then one or more digits of
  the base, with `_` allowed *between* digits as in literals (§3.6). Nothing else.
* `base` is 2–36. With `base = 0` the prefix decides: `0x`, `0o`, `0b`, else decimal — the
  literal grammar of §3.6. With an explicit base a prefix is not accepted.
* A value outside `int64` is `nil`: it is not an Int. (It does **not** promote to Float —
  D9's promotion is for arithmetic, and a parse that silently loses digits is the problem
  being solved.)
* A non-string argument is a `type` error, as everywhere (§12): `parse_int(nil)` is a bug,
  not a missing number. Use `x?.parse_int`.

**`parse_float`.** Accepts what a float or int literal accepts, plus an exponent, plus
surrounding whitespace. `"inf"`, `"nan"`, hex floats and trailing text are `nil`.

**Why `nil` and not an error.** The result composes with what is already there:

```
n = s.parse_int ?? 0                       # explicit default
port = $PORT?.parse_int ?? raise("PORT must be a number")   # ?. — an unset $PORT is nil
rows.filter { it[2].parse_float != nil }
```

An exception would need `try … else` around an operation that fails routinely.

**Relation to `int`.** Two operations, two names (D17): `int` is a *lenient conversion*
that always has an answer; `parse_int` is a *question*. §12.7 gains one sentence pointing
from the first to the second.

The inverse already exists for bases: `format("%x", n)`, `%o`, `%b`.

## Compatibility

Additive (§21.2): two new names. A local variable called `parse_int` shadows the row.

## Alternatives

* **`int(s, strict = true)`.** A flag that changes the return type is a mode.
* **`s.is_int`** (a predicate). Forces parsing twice and still does not parse hex.
* **`int?`**-style naming. `?` is the ternary and `?.`; not available in identifiers.
* **Make `int` strict** and add `to_i`-style leniency elsewhere. A break of the most used
  conversion in the migrated morzebot corpus (§19); not on the table.

## Open questions

* Should `parse_int("1_000")` accept underscores? Proposed yes (literals do); CSV data with
  `_` separators is rare enough that the opposite answer is also defensible.
* A `decimal`-returning sibling is not needed: `decimal.of` already raises kind `decimal`
  on bad input.

## Implementation

`mzs/num.go` + `mzs/builtins.go` registration, `SPEC.md` §12.5/§12.7, `docs/stdlib`,
`internal/apidoc` regeneration, corpus tests for each rejected spelling.

## As landed

SPEC §12.7 ("Strict parsing") is the normative text.

* **Core rows, not string rows.** Both are registered beside `int` and `float` as
  functions of any first argument (§12.1), so `5.parse_int` and `parse_int(nil)` are the
  `type` error this RFC asks for. As §12.2 string rows they would have been
  `name: undefined method 'parse_int' for int` — an error, but naming the wrong problem.
  `x?.parse_int` still short-circuits on `nil`.
* **Open question — underscores:** accepted, as proposed, but only *between* two digits:
  `"1_000"` is `1000`; `"_1"`, `"1_"`, `"1__0"` and `"0x_1f"` (base 0) are `nil`. The lexer is
  laxer than that (`1_` and `0x_1f` lex today); data is held to the stricter rule because a
  stray `_` in a cell is more likely damage than a separator.
* **Prefixes (`base = 0`)** are case-insensitive, as they are in literals (`0X1F` lexes), and
  the sign comes before the prefix: `"-0x1f".parse_int(0)` is `-31`.
* **A base that is not an Int** is a `type` error rather than truncated, so
  `parse_int("1", 1.5)` does not silently mean base 1 and then fail as an `argument` error.
* **`parse_float` reads decimal only.** "What a float or int literal accepts" is taken in its
  decimal reading: `"0x1f".parse_float` is `nil`, like `"0x1p-2"`. It is the more
  conservative answer: accepting hex later only turns a `nil` into a number, while
  withdrawing a number once returned would break the programs that read it. A digit is required on both sides of a `.` (`".5"`, `"1."`
  are `nil`), the exponent takes no separators, and a value too small for `float64` is `0.0`
  as the literal `1e-400` is, while one too large is `nil` (the literal `1e400` does not lex).
* **Whitespace** is Unicode `White_Space` on both sides (`strings.TrimSpace`), so a cell
  ending in `\r\n` or padded with a no-break space parses. Digits are ASCII only: `"٣"` and
  `"９"` are `nil`.
