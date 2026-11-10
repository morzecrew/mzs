# RFC 0001 — Rest elements and spread

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L1, L2, L3

## Summary

Give `*` one more job, in the four places SPEC §20 already reserves for it or that follow
from it: a rest element in destructuring (`a, *rest = xs`), a splat at a call site
(`f(*xs)`), a spread in an array literal (`[*a, x]`), and `**` for the dict counterparts
(`{**d, k: v}`, `f(**opts)`). §20 says the rest element and the call-site splat "land
together or not at all"; this RFC lands them together.

## Motivation

Destructuring is strict about length, and has no escape:

```
a, b = [1, 2, 3]        # index: destructuring expects 2 values, got 3
a, *rest = [1, 2, 3]    # syntax: unexpected '*'
```

So "the head and the tail" is `h = xs[0]; t = xs.drop(1)`, and a `match` arm cannot
describe "a list starting with `"GET"`". A function with `*nums` can receive many
arguments but an array cannot be passed as them: `sum_all(*xs)` is a syntax error, so every
variadic function needs a second, array-taking twin. Dict literals have the same gap —
"this dict, with one key changed" is `d + {k: v}`, which works, but there is no way to
build one literal out of two dicts and three keys in order.

## Design

### Grammar delta (SPEC §4)

```ebnf
Target         = IDENT | GVAR | Postfix "[" Expr "]" | TargetPattern
               | "*" IDENT ;                         (* rest: at most one per level *)
MatchElem      = IDENT | MatchPattern | Expr
               | "*" [ IDENT ] ;                     (* rest; anonymous "*" skips *)
Arg            = Expr | IDENT "=" Expr
               | "*" Expr                            (* positional splat *)
               | "**" Expr ;                         (* named splat *)
ArrayBody      = [ ArrayElem { "," ArrayElem } [ "," ] ] ;
ArrayElem      = Expr | "*" Expr ;
DictEntry      = … | "**" Expr ;
```

`*` and `**` are prefix here only directly after `(`, `[`, `{` or `,` — positions where a
binary operator cannot stand — so `a * b` and `a ** b` keep their one reading (D16).

### Semantics

**Rest in a target.** `a, *mid, z = xs` binds `a` to the first element, `z` to the last and
`mid` to a **new array** of what is between (possibly empty). The length rule becomes
"at least the number of non-rest targets"; without a rest it stays exact. One rest per
level; a nested pattern may have its own.

**Rest in a `match` arm.** `["GET", *path] -> …` matches an array of length ≥ 1 whose first
element equals `"GET"`. `[first, *]` ignores the tail without binding it.

**Splat at a call.** `f(1, *xs, 2)` evaluates left to right and passes the elements of `xs`
as positional arguments. The operand must be an array, a range or a seq (materialised
under `MaxCollection`); anything else is a `type` error. Arity is checked after expansion,
with the existing `argument` message.

**`**` at a call.** `f(**opts)` passes each entry of the dict `opts` as a named argument.
Keys must be strings naming parameters; an unknown key is the existing
`has no parameter named '…'` error. A parameter bound twice — positionally and through
`**`, or by two splats — is an `argument` error, never last-wins.

**Spread in literals.** `[*a, x, *b]` concatenates. `{**d, k: v, **e}` builds a **new
plain dict**: entries in written order, a later entry overwriting an earlier one's value
while keeping the earlier one's position (D11). It is not `d + {k: v} + e`, and the
difference is deliberate: a literal never carries a record label, so it neither keeps one
(`m + {…}` and `dup` do, §7.8) nor refuses two labelled operands (`a + b` between two
records is an error, §7.8; `{**a, **b}` is a plain dict of their entries).

**Not included:** `**kwargs` as a *parameter* (collecting unknown named arguments). It
turns every misspelled argument name from a diagnostic into a silent entry, which is the
trade D16 refuses. It stays reserved.

### Diagnostics

| Written | Message |
|---|---|
| `a, *b, *c = xs` | `syntax: a pattern takes one rest element` |
| `*rest = xs` | `syntax: a rest element needs a neighbour; write rest = xs.array` |
| `f(*5)` | `type: cannot splat int; '*' takes an array, a range or a seq` |
| `f(**[1])` | `type: cannot splat array as named arguments; '**' takes a dict` |
| `fn f(**kw) { }` | unchanged: `expected a parameter name, found '**'` |

## Compatibility

Additive. Every new form is a syntax error today (audit L1–L3), and SPEC §21.1 says
reserved syntax "may become a feature". §20's paragraph on the rest element is replaced.

## Alternatives

* **`...xs`** (JavaScript). `...` is already a diagnostic pointing at `..<`; reusing it
  would make that fix-it wrong half the time.
* **Functions only** — `apply(f, xs)`, `xs.head_tail`. Solves the call, not the patterns,
  and adds names where a reserved lexeme was waiting.

## Open questions

* Should a seq be splattable, or only array/range? A seq may be endless; the limit catches
  it, but uncatchably.
* `for a, *rest in rows` — follows from RFC 0002's `for` patterns; specified there.

## Implementation

`internal/parser` (four productions), `internal/ast` (`Splat`, `RestTarget`),
`mzs/eval.go` destructuring, `mzs/call.go` argument binding, `internal/printer`. The fold
pass (`mzs/fold.go`) must not constant-fold a literal containing a spread.
