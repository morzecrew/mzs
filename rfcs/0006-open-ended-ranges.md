# RFC 0006 — Open-ended ranges and slices

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L12

## Summary

Allow a range to omit one bound **inside an index**: `xs[1..]`, `xs[..3]`, `s[..<n]`.
Nothing else changes: there is no endless range *value*.

## Motivation

```
xs = [1, 2, 3]
xs[0..1]        # [1, 2]
xs[1..]         # syntax: unexpected ']'
```

"Everything after the first" and "the first n" are the two commonest slices, and today
they are spelled with a different vocabulary than the slice next to them: `xs.drop(1)`,
`xs.take(n)`, or the index arithmetic `xs[1..xs.len - 1]` that open bounds exist to remove.
The same holds for strings (`s[1..]`), where slicing is the main way to cut text.

## Design

```ebnf
Trailer    = … | "[" IndexExpr [ "," Expr ] "]" ;
IndexExpr  = Expr
           | AddExpr ( ".." | "..<" )            (* from a to the end *)
           | ( ".." | "..<" ) AddExpr ;          (* from the start to b *)
```

* `x[a..]` is from index `a` through the last element. `x[a..<]` is not written: with no
  bound there is nothing to exclude, and the parser says so
  (`syntax: an open range ends at the last element; write x[a..]`).
* `x[..b]` is `x[0..b]`; `x[..<b]` is `x[0..<b]`.
* A negative bound counts from the end, as a negative scalar index does (§8.8):
  `s[-3..]` is the last three runes, `xs[..<-1]` is everything but the last.
* Out-of-range bounds clamp; an empty selection is `[]` / `""`, never an error.
* Applies to every receiver that accepts a range index today: arrays, strings, ranges.

**This RFC also has to specify the closed form**, because §8.8 does not: its table lists
`x[i]` and `x[i, n]` and never mentions `x[a..b]`, which the implementation accepts. What
it does today with a negative bound is an accident of materialising the range first:

```
[1,2,3][-2..-1]    # [2, 3]   — both negative: the range -2..-1 is ascending
[1,2,3][0..-1]     # []       — the range 0..-1 is empty, so nothing is selected
"hello"[1..-1]     # ""       — same
```

The normative rule proposed: **each bound is resolved against the length first** (negative
→ `len + bound`), then the slice is taken. So `xs[0..-1]` is the whole array. That changes
the two empty answers above — a deliberate fix under §21.4 (audit B7), since no program
can have wanted them.

The open forms are legal **only as the whole index expression**, where the closing `]`
(or the `,` of the two-argument form) ends them. Everywhere else a range still needs both
bounds, so `for i in 1.. { }`, `(1..)` and `x = ..5` remain syntax errors, with a fix-it:
`an endless range is not a value; for a counter write seq { (i) -> i + 1 }`.

## Why not an endless range value

`(1..)` as a first-class value looks like the general solution and is the wrong one here:

* Range is one of the nine kinds and is **eager** in most of §12.10 — `len`, `array`,
  `sum`, `reverse`, `sort`, `last` would all need an "unless endless" clause, each a new
  run-time error.
* The lazy, possibly endless thing already exists and has one spelling: `seq` (§12.14).
  A second way to write an infinite counter is the alias D17 forbids.
* In a `for` header, `1.. {` puts a `{` after an operator, which is exactly where §3.11's
  body-or-operand question would need a new special case.

## Compatibility

The open forms are additive: both are syntax errors today. The negative-bound rule for the
closed form changes `xs[0..-1]` from `[]` to the whole array (§21.4, changelog entry).

## Alternatives

* **Do nothing; `drop`/`take` exist.** True for arrays. For strings there is `slice(a, b)`
  with explicit bounds only, and no `drop`; the gap is real there.
* **Python's `xs[1:]`.** `:` in brackets was the dict literal of the earlier draft and
  still has a dedicated diagnostic; and two range spellings is one too many.

## Open questions

* Assignment through an open slice (`xs[1..] = […]`) — slices are not assignment targets
  today at all; out of scope.

## Implementation

`internal/parser` (index trailer only), an `OpenRange` AST node, the range-index path in
`mzs/eval.go` (resolve bounds against the length instead of materialising the range),
`internal/printer`, SPEC §8.8 (a `x[a..b]` column).
