# RFC 0007 — Labelled `break` and `next`

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L13

## Summary

Let a `while` or `for` carry a label, and let `break` / `next` name it:
`for@rows r in grid { for c in r { break@rows if c == 0 } }`.

## Motivation

`break` and `next` reach the innermost loop only. Leaving two levels takes a flag variable
and a second test, or wrapping the loops in a function to borrow `return`:

```
found = nil
for row in grid {
  for cell in row {
    if cell == target { found = cell; break }
  }
  break if found != nil          # the line that exists only to forward the break
}
```

Grid and search code (`examples/22_game_of_life.mzs`, `23_maze_bfs.mzs`) is where this
shows. It is a small feature; it is listed because the workaround is exactly the kind of
ceremony G1 argues against.

There is a second, mzs-specific reason. Because every `{ … }` is a closure (D2), `break`
inside `xs.each { … }` nested in a `for` is easy to misread; a label states the target.

## Design

```ebnf
WhileExpr  = "while" [ Label ] Expr Closure ;
ForExpr    = "for"   [ Label ] ForTarget [ "," ForTarget ] "in" Expr Closure ;
BreakStmt  = "break" [ Label ] [ Expr ] ;
NextStmt   = "next"  [ Label ] [ Expr ] ;
Label      = "@" IDENT ;                   (* no space between the keyword and "@" *)
```

`@` is reserved today (`'@' is reserved; instance variables do not exist`), and SPEC §20
keeps `class` out for good — so `@` has no future as an instance variable and is free.
The label is glued to the keyword (`for@rows`, `break@rows`) so that it can never be read
as the start of the next expression.

**Semantics.**

* `break@l v` ends the loop labelled `l` with value `v` (§8.10), unwinding the loops
  inside it; every `ensure` on the way runs (§8.11).
* `next@l` starts the next iteration of loop `l`, abandoning the inner loops.
* A label is lexically scoped to its loop body and does **not** cross a function
  boundary: a `fn`, an arrow function or a closure passed to a call (`xs.each { … }`)
  cannot `break@l` a loop outside it — the same rule an unlabelled `break` follows today.
  Loop bodies, `if`, `match` and `try` blocks are transparent.

### Diagnostics

| Written | Message |
|---|---|
| `break@x` with no enclosing `@x` | `syntax: no enclosing loop is labelled '@x'` |
| label reused in a nested loop | `syntax: label '@x' is already in use at 3:1` |
| crossing a closure | `syntax: '@x' labels a loop outside this function; a closure cannot break it` |
| a label nobody uses | `warning: label '@x' is never used` |

## Compatibility

Additive; `for@x` and `break@x` are lexer errors today.

## Alternatives

* **`outer: for …`** (Java, Go). `name:` at the start of a statement is where §3.12 decides
  "closure or dict" for a body and where RFC 0002 puts dict patterns; one more meaning for
  `ident :` is one too many.
* **`break 2`** (PHP, shell). `break` already takes a *value* (`break 2` returns 2).
* **Nothing; use a function and `return`.** It works, and it is the fallback if this RFC is
  judged not worth a lexeme. That is why it is P3.

## Open questions

* Should labels be allowed on `xs.each@l { … }`-style iteration? No: those are calls, and a
  non-local exit from a callee is a different feature.

## Implementation

`internal/lexer` (`@ident` after `for`/`while`/`break`/`next`), `internal/parser`,
`mzs/eval.go` (the break/next signal carries a label; loops compare it), compile pass
(label resolution and the diagnostics above), printer.
