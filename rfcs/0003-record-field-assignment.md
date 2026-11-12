# RFC 0003 — Record fields: writing them, and reading them across modules

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L7, L8

## Summary

`m.amount` reads a record field; `m.amount = 5` is a syntax error, and in another module
even the read is gone. This RFC makes `.field` a complete access path: it becomes an
assignment target, and an `export`ed record carries its field names to whoever includes
the module.

## Motivation

```
record Point(x, y)
p = Point(1, 2)
p.x              # 1
p.x = 5          # syntax: cannot assign to this expression
p["x"] = 5       # the only spelling that works
p.x += 1         # same error
```

SPEC §7.8 says a record "is an ordinary dict: `m["amount"] = 2` writes it in place". So the
mutation is allowed, only its natural spelling is not, and a program ends up with `.x` on
the right of `=` and `["x"]` on the left. Across files it is worse: §7.8 again — "a shape
declared in another module is read with `m["amount"]`". The feature most likely to live in
a library (`27_lib_money.mzs`) is the one that stops working at the library boundary.

## Design

### 1. `.field` as an assignment target

```ebnf
Target = … | Postfix "." IDENT ;      (* IDENT must be a record field known to this unit *)
```

`m.amount = v` is exactly `m["amount"] = v`; `m.amount += 1` and the other compound
operators follow. It is valid as a destructuring target (`p.x, p.y = [1, 2]`).

The name is resolved as a field at compile time by the rule that already governs reads
(§7.8): it must be a field of some record visible in the compilation unit. Otherwise:

```
-e:1:3: syntax: cannot assign to '.len': not a record field
        (a dict entry is written d["len"] = …)
```

At run time the receiver must be a dict that has the key — a record of the right shape
always does. A dict without it is a `key` error: `.field =` never *adds* a key, so a typo
that slipped past the compile check (two records, one field name) cannot grow a shape.
Adding a key stays `d["k"] = v`.

### 2. Fields travel with `export`

`export record Money(amount, currency = "RUB")` already exports the constructor. It now
also exports the *declaration*: after

```
include money from "money"
m = money.Money(1500)
m.amount                    # works
match m { money.Money {amount} -> … }   # with RFC 0002
```

the including unit's compile step knows the **field names** of every record the module
exports. `include … from` takes a string literal by grammar (§4), so the dependency is
known when the includer compiles.

**It is opt-in.** `Options.ModuleLoader` has never promised that a second read of the same
path answers the same way — a loader may hand out a one-time token, count reads, or fetch
from somewhere that changes — so reading at compile time is enabled only by a new
`Options.CompileIncludes bool`. With it false, the zero value, nothing about includes or
`.field` changes from today: a field of another module's record is read with `m["k"]`.
The CLI sets it, because its loader is a file read. Four rules keep the opted-in reading
from changing what an include does today:

* **Only an include at the top level of a file is read at compile time.** One inside a
  block (`if debug { include trace from "trace" }`) stays exactly as lazy as it is now:
  never loaded unless it runs, and its records are read with `m["k"]`.
* **A read that fails at compile time is not a compile error.** The include fails when it
  runs, as today, and is still catchable by `try` wherever it is today. What fails at
  compile time is only a `.field` that nothing else could resolve, and its message names
  the cause: `'.amount' is not a field of a record known here; module 'money' could not be
  read at compile time: …`.
* **Nothing compiled is cached across Runs.** The compile step keeps a *table of field
  names*, not the module's program. Each Run calls the loader and runs the module exactly
  as it does now (§12.8), and the dispatch of `.field` happens at run time anyway (§7.8),
  so a module whose source changed between Runs is the module that runs. A stale table can
  only make the compile step accept a name the new module no longer has. At run time that
  read is dispatched exactly as it is today for a record that lacks the field (§8.7,
  `dispatch` in `mzs/call.go`): the receiver's stdlib row of that name if there is one
  (`m.len` counts entries), then a function of that name by UFCS, then `undefined method`.
  So a removed field behaves like a field that was never declared — it is **not** a `key`
  error, and this RFC does not change that order.
* **A function the including unit can see keeps its meaning.** Today `m.amount`, with
  `fn amount(x) { … }` in scope, is rewritten by the compile pass to `amount(m)`. That
  rewrite is kept: the imported table only lets `.name` compile where it would otherwise
  be `undefined method`, and never replaces a rewrite to a visible function — the compile
  pass warns instead (`'amount' is also a field of money.Money; this calls the function`).
  The warning exists only with `CompileIncludes` set, and `StrictWarnings` promotes it like
  any other warning: a host that turns both on is choosing to be told, and no host has
  this warning before it opts in.
  The same rule covers a field the module *adds* after the includer compiled: where a
  visible function claimed the name, the call stays the call it compiled to; where none
  did, `.amount` was never rewritten and reaches the field through run-time dispatch,
  which looks at the receiver's own shape first (§7.8).

Built-in modules export no records today; if one ever does (`decimal`'s `Decimal`), the
same rule applies.

## Compatibility

Additive: `x.name = …` is a syntax error today for every `name`. The cross-module half
changes nothing unless the host sets `Options.CompileIncludes`, a new field whose zero
value is today's behaviour (§21.1). With it set, a module that cannot be read still fails
where and how it fails today, a conditional include is still not read, and a call that
compiled to a visible function still does. The one new observable is that `Compile`
calls the loader once more per top-level include, which is why the field exists: a host
sets it only for a loader whose reads repeat.

## Alternatives

* **Dynamic `.field`** — resolve any unknown `.name` at run time against the receiver's
  label. Simpler than compile-time module loading, but it turns `x.lenght` from a compile
  error with a did-you-mean into a run-time one, on every path. The compile-time
  diagnostic is one of the best things about the language; not worth trading.
* **`.name` on any dict** (`d.a` ≡ `d["a"]`). Collides head-on with UFCS: `d.keys` would
  depend on the data. Rejected by D16; records are precisely the case where the names are
  known.
* **A `with` function** for functional update. Already exists as `m + {amount: 5}` /
  `merge`, which keep the label (§7.8). No new name needed (D17).

## Open questions

* Should assigning a field that exists on *no declared record of that receiver's label*
  be a `type` error naming the label, instead of the generic `key` error?
* Host-provided loaders that are slow or remote now run during `Compile`. Is a
  `CompileOptions.SkipIncludes` needed for hosts that compile untrusted source eagerly?

## Implementation

`internal/parser` (target), `mzs/eval.go` (assign), `mzs/record.go` + `mzs/module.go`
(export table carries `fields`), `mzs/interp.go` compile pass (load includes), LSP
completion after `.` on a module-qualified record.

This RFC also lands the module-qualified **record pattern** of RFC 0002,
`money.Money {amount} ->`: RFC 0002 resolves `Name {…}` against the unit's own records
(#25), and the `mod.Name` form needs the export table built here. It follows the same
rule — a record pattern only when `money.Money` is an exported record, the method call
with a trailing closure otherwise — and the parser's `recordShapeAt` extends to
`IDENT "." IDENT "{"`.
