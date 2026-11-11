# RFC 0001 — Rest elements and spread

**Status:** Landed (#23) · **Area:** language · **Depends on:** — · **Audit:** L1, L2, L3

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

## As landed

SPEC §4 (grammar), §8.15 (the rest element) and §8.16 (splat and spread) are the normative
text.

* **Open question — is a seq splattable?** No, the more conservative answer. `*` takes an
  Array or a Range; a Seq is a `type` error that names the bounded spelling, `*s.take(n).array`,
  and the unbounded one for a seq the author knows ends. An endless seq would otherwise run
  into `MaxCollection`, which a script cannot catch. Accepting a seq later turns an error
  into a value, so it stays open as an addition.
* **`for a, *rest in rows`** is RFC 0002's, as this RFC said, and is not part of this change.
* **One node, `ast.Splat`**, for every position: an array element, a positional argument,
  a pattern element (with a nil `X` for the anonymous `*`), and a dict key with a nil value
  beside it. `**` at a call is a `NamedArg` with `Spread` set, so it lives in the named half
  of the argument list and may stand among the names in any order.
* **A line that starts with `*`.** §3.10 joins a line that starts with a binary operator to
  the one before it, so `*init, last = xs` on a line of its own would have read as
  `prev * init, last = xs`. A `*` that starts its line at bracket depth zero of a statement
  and begins a target list — a comma at depth zero, then `=` or `:=`, before the line ends —
  now starts a statement of its own. The other reading was a syntax error in every case, so
  no program changes meaning; a product written across lines (`a\n  * b`) still continues.
* **`*rest = xs` alone** is a diagnostic naming `rest = xs.array`, as proposed; the bracketed
  `[*all] = xs` is a copy and is allowed, since it has the brackets that make it a pattern.
* **A rest target is a name.** `*$g` and `*xs[0]` are refused at parse time: the RFC's grammar
  gives `"*" IDENT`, and a rest writes a new array, which a subscript or a global would only
  obscure. A pattern element `*[1]` in a `match` arm is refused for the same reason: a rest
  binds or skips, it does not compare.
* **The rest is a copy** in a target and in an arm alike, charged to the memory budget like
  any new array.
* **Duplicates.** `f(**{a: 1}, a = 2)` and `f(**{a: 1}, **{a: 2})` are the `argument` error
  `argument 'a' is given twice, once through '**'; a spread never overrides`. A spread that
  names a parameter a position already filled is the existing `got two values for parameter`.
* **A bare `*`** outside a match pattern — `[1, *]`, `f(*)` — is a compile error, not a parse
  error, because the parser reads arrays before it knows they are patterns.
* **A spread is a snapshot.** Its elements are copied when its position is reached, so a
  later element that changes the operand — `[*a, (a[0] = 9)]` — does not reach back into
  the result.
* **Limits.** A spread array literal and a splatting call check `MaxCollection` on the
  laid-out size — positions written after a splat included — and charge what they build to
  `MaxMemory` before a range is materialised. In a dict literal only keys that are new
  count, so overlapping spreads are not refused, and a written key after a spread counts
  too. A `**`, in a dict literal or at a call, charges one step per entry, as
  `merge` does, and the duplicate-name check at a call is a set, so a large dict costs time
  linear in its size.
