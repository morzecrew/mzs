# RFC 0018 — `big`: integers past 2⁶³

**Status:** Draft · **Area:** stdlib · **Depends on:** — · **Audit:** L17

## Summary

Add a built-in module `big` for arbitrary-precision integers, shaped exactly like
`decimal`: values made by `big.of`, arithmetic through named members, no operators, no
coercion. D9 does not move — `Int` stays `int64` and still promotes to Float on overflow.
Separately, add a lint for arithmetic that *provably* overflows at compile time.

## Motivation

D9: "Overflow of `Int` promotes to Float (never wraps)". Not wrapping is the safe half.
The other half is that exactness ends without notice:

```
2 ** 62                 # 4611686018427387904
2 ** 64                 # 1.8446744073709552e+19     — a float; the low digits are gone
fn fact(n) { n <= 1 ? 1 : n * fact(n - 1) }
fact(25)                # 1.5511210043330986e+25
9223372036854775807 + 1 # 9223372036854776000.0
```

Nothing is wrong by the spec, and the result is unusable wherever digits matter: IDs
above 2⁶³ from a JSON API, hashes, combinatorics, modular arithmetic, a Snowflake ID
multiplied by anything. `decimal` does not help — it "holds 19 digits" because its units
live in an `int64` (observed: `decimal.of("123456789012345678901234567890")` is a
`decimal` error). There is no exact number in the language wider than 64 bits.

## Design

```
include big

n = big.of("123456789012345678901234567890")
big.str(big.times(n, n))             # "15241578753238836750495351562536198787501905199875019052100"
big.str(big.pow(big.of(2), 100))     # "1267650600228229401496703205376"
big.int(big.mod(n, big.of(97)))      # an ordinary int again
```

| Member | Signature | Semantics |
|---|---|---|
| `of` | `(x: int \| string) -> big` | from an Int, or a string of digits with optional sign, `_`, and `0x`/`0o`/`0b` prefix. A Float is a `type` error: there is no exact reading of one |
| `plus minus times` | `(a, b) -> big` | either operand may be an Int |
| `div` | `(a, b) -> big` | **floored**, with `mod` as its partner: `div(a,b)*b + mod(a,b) == a`; division by zero is `zero-division` |
| `mod` | `(a, b) -> big` | sign of the divisor, like `%` (§8.3) |
| `pow` | `(a, e: int, m = nil) -> big` | `e ≥ 0`; with `m`, modular exponentiation |
| `neg abs` | `(a) -> big` | |
| `cmp` | `(a, b) -> int` | -1, 0, 1 |
| `gcd` | `(a, b) -> big` | |
| `str` | `(a, base: int = 10) -> string` | |
| `int` | `(a) -> int` | raises kind `big` when it does not fit — never promotes |
| `float` | `(a) -> float` | nearest float |
| `bits` | `(a) -> int` | bit length |

**The value.** Follows `decimal` (§12.15) point for point: a labelled value whose `type` is
`"Big"`; `+ - * /` between two of them is the `type` error that names the member to call
(`cannot add Big to Big … use big.plus`), exactly as `Decimal` does, because operator
overloading is permanently out (§20). Equality and ordering go through `big.cmp`; `==` on
two Bigs compares content, so they work as dict keys. `str(b)` and interpolation print the
digits. **JSON** writes a Big as a bare integer literal (JSON numbers have no width) and
`json.parse` keeps today's behaviour — an integer that does not fit `int64` becomes a
Float — unless called as `json.parse(s, {big: true})`, where it becomes a Big.

**Limits.** A Big is bounded by a new `Options.MaxBigBits` (default 1,000,000 bits ≈ 300k
digits); exceeding it is `limit`. `pow` and `times` charge interpreter steps
proportionally to operand size, so `big.pow(x, 10**6)` is paid for by the step budget
rather than by the host's CPU.

**No gate**: pure computation over `math/big`, which is the standard library (I1).

### The lint

Independent of the module: when constant folding (`mzs/fold.go`) evaluates an Int
operation that overflows — `2 ** 64`, `9223372036854775807 + 1` — the compiler warns:
`warning: this is a float: int overflow promotes (D9); for exact digits use big.of`.
It catches the literal cases and costs nothing at run time.

## Compatibility

Additive: a module, an `Options` field, an option key on `json.parse`, an error kind
`big`. D9 and every existing result are untouched.

## Alternatives

* **Make `Int` arbitrary-precision** (Python, Ruby). The right design for a new language
  and a rewrite of D9, of the value representation, and of the A4 performance budget for
  this one. Every `+` would need an overflow branch into a heap number.
* **Make overflow an error.** Changes working programs (a deliberate §21.4 break) and
  makes `fact(25)` fail instead of approximating. Arguably more in the spirit of D16 than
  promotion is; if the project ever wants that, it should be its own RFC, and `big` is
  what the error message would point to.
* **Widen `decimal`** to arbitrary precision instead. Solves the 19-digit limit there too,
  and is probably worth doing *as well*; but a decimal is not an integer (no `mod`, `pow`,
  `gcd`, bit operations), and conflating them repeats the int/float confusion.

## Open questions

* Is this a P3 module or a P1 fix to `json.parse`? The lossy step that bites in practice
  is a 64-bit unsigned ID in an API response turning into `1.8446744073709552e+19`. A
  smaller alternative: `json.parse(s, {ints: "string"})` keeping oversized integers as
  strings.
* Bit operations (`band`, `shl`, …) on Bigs — extend the §12.5 functions or add members?

## Implementation

`mzs/big.go` (mirroring `mzs/decimal.go`), `mzs/ops.go` (the operator diagnostic),
`mzs/json.go`, `mzs/fold.go` (lint), `mzs/options.go`, SPEC §12 + §13.5, docs, apidoc.
