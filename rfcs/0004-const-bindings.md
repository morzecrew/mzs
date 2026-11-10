# RFC 0004 — `const`: a binding that cannot be reassigned

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L9

## Summary

Add `const name = expr`: a binding that every later assignment to is a compile error.
Treat names introduced by `fn` and `record` declarations the same way, first as a warning.

## Motivation

`=` assigns to "an existing binding found anywhere in the chain" (SPEC §8.2). That is what
makes `x = 0; if c { x = 1 }` work, and it has a cost:

```
rate = 0.2
fn total(items) {
  rate = items.len > 10 ? 0.1 : 0.2     # meant a local; rewrote the file's rate
  …
}
```

The fix is `:=`, but nothing tells the author one was needed — the program runs, and the
next call sees a different `rate`. The same goes for declarations:

```
fn f() { 1 }; f = 2; f      # 2 — a hoisted function, silently replaced by an int
```

A module's `export rate = 0.2` is the sharpest case: it is a constant in every sense except
that any function in the file can reassign it by accident.

## Design

```ebnf
BareStmt   = … | ConstStmt ;
ConstStmt  = "const" ( IDENT | TargetPattern | Target { "," Target } ) "=" Expr ;
ExportStmt = "export" ( FnDecl | RecordDecl | Assignment | ConstStmt | IDENT ) ;
```

`const` is a **positional** keyword, like `record` and `async` (§3.5): it is a keyword only
as the first token of a statement followed by a name or a pattern, so a variable called
`const` keeps working.

**Semantics.**

* `const x = e` evaluates `e` and creates `x` in the current scope, as `:=` does (it
  shadows, never assigns outward).
* Any later `=`, compound assignment, or destructuring that would write to that binding is
  a **compile error**. Scoping is lexical, so this is always decidable:
  `syntax: cannot assign to const 'rate' (declared at 1:7); use ':=' for a new local`.
* `:=` in an inner scope may still shadow it. That is the explicit spelling, and the
  message above recommends it.
* `const` is about the **binding**, not the value: `const xs = []; xs.push(1)` is legal.
  There is still no frozen value (§7.8, I4).
* `const` may only bind identifiers — `const $x = 1` and `const d["k"] = 1` are syntax
  errors. Host globals are the host's.
* `const a, b = pair` and `const [x, {y}] = v` make every name they bind constant.

**Declarations.** A name bound by a named `fn` or by `record` becomes const as well. Since
that refuses programs that run today, it lands in two steps (§21.4): a **warning** in the
release that adds `const` (`'f' is a function declared at 1:1; assigning to it replaces
it`), and an error in the next MINOR.

**A companion warning.** Independently of `const`, the compile pass warns when a function
body assigns with `=` to a binding of an *enclosing function or file* scope without having
read it first in that body:
`warning: '=' assigns to 'rate' declared outside this function; use ':=' for a local`.
Counters (`n = n + 1`, `n += 1`) read first and stay silent. This catches the accident for
bindings nobody thought to mark.

## Compatibility

`const X = 1` is `syntax: unexpected 'X' after statement` today, so the statement is
additive. The declaration rule is a deliberate break, staged as above. The warning is a
diagnostic and changes nothing a program means (§21.2).

## Alternatives

* **`let`/`var` everywhere** — make declaration explicit. Destroys G1: a one-liner would
  need a keyword per variable. `const` is opt-in.
* **`UPPER_CASE` means constant** (Ruby). Meaning from spelling is a mode; SPEC §12.8
  already says "there is no `CONST` kind".
* **Deep freeze** (`freeze`). A second model of change for every collection row, and a
  run-time error class where this RFC has a compile-time one. Explicitly out (§7.8).

## Open questions

* Should `export name = …` be const **by default**? Almost certainly what authors mean, but
  it is a break with no staging path other than a warning cycle.
* Loop variables (`for x in xs`) — const within the body? Other languages go both ways.

## Implementation

`internal/parser`, a `Const` flag on the resolver's binding record in the compile pass
(`mzs/interp.go`), no evaluator change beyond `:=` semantics; `internal/printer`; LSP
semantic info (hover shows `const`).
