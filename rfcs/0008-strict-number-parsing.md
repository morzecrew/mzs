# RFC 0008 — `parse_int` and `parse_float`: a conversion that can say no

**Status:** Draft · **Area:** stdlib · **Depends on:** — · **Audit:** L14

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
port = $PORT.parse_int ?? raise("PORT must be a number")
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
