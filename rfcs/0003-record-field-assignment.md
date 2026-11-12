# RFC 0003 — Record fields: writing them, and reading them across modules

**Status:** Landed (#26, #27) · **Area:** language · **Depends on:** — · **Audit:** L7, L8

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

## As landed

**Part 1 landed in #26: §1, `.field` as an assignment target.** `m.amount = v`, every
compound form and a field as a destructuring target, at any depth of a dict or array
pattern. SPEC §4 (the `FieldTarget` rule), §7.8 ("Writing a field") and §8.8 are the
normative text. **What remains is §2** — fields travel with `export`, behind
`Options.CompileIncludes` — together with the module-qualified record pattern
`money.Money {…}` that RFC 0002 handed over, and the LSP completion after `.` on a
module-qualified record.

* **The run-time check asks what the read asks.** The RFC said only that the receiver must
  be "a dict that has the key". A read, though, reaches a field only on a record whose
  declaration has it (§7.8; `dispatch` in `mzs/call.go`): `{x: 1}.x` is `undefined method`
  even where some record declares `x`. A write that worked where the read with the same
  spelling fails would make `.x` two different questions on the two sides of `=`, so the
  write asks the same one: a plain dict is a `type` error that names the `d["x"]`
  spelling, and so is a record of another shape. `.field =` never adds a key, as the RFC
  says; the one way to get there is a record whose entry was removed, and that is the `key`
  error. The check runs again right before the write, so a right side that deleted the
  entry does not get it back.
* **Open question 1 — a field no declaration of the receiver's label has: a `type` error
  naming the label**, `record Q has no field 'x'; it has y`, which is the message the
  record pattern of RFC 0002 gives for the same mistake at compile time. The generic `key`
  error is kept for the one case that really is about the entry.
* **Only the bare spelling is a field.** `m.amount()`, a trailing closure and `m?.amount`
  stay calls and remain `cannot assign to this expression`. A write has no call reading,
  so a function or a stdlib row of the same name (`record Page(len)`) changes nothing on
  the left of `=`; on the right the rules of §7.8 stand unchanged.
* **The AST gains `FieldExpr`**, built by the parser only in target position; a read of the
  same spelling is still the zero-argument `MethodCall` it always was, so nothing that
  walks a read changes.
* Open question 2 belongs to §2 and stays open until it lands.

**Part 2 landed in #27: §2, fields travel with `export`,** behind `Options.CompileIncludes`
as designed, together with the module-qualified record pattern `money.Money {…}` that RFC
0002 handed over. SPEC §4 (the qualified `DictMatch`), §5.3, §7.8 (the cross-module
bullet), §8.15 and §13.2 are the normative text; the CLI sets the option.

* **What the compile step reads.** Each `include … from` among the file's top-level
  statements is asked of the loader once and parsed, nothing more: the records it exports
  are `export record Name(…)` and a bare `export Name` that names a top-level record of the
  module. A record the module keeps private is not learnt. The module's own includes are
  not followed — a record re-exported from a third file is read with `m["k"]` — because a
  chain of compile-time reads is a chain of loader calls nobody wrote down.
* **The four safety rules hold as written.** A block's include is not read; a read that
  fails (the loader's error, or a source that does not parse) is no error of its own and
  only explains a `.field` nothing resolved; only field names are kept and each Run reads
  and runs the module as before (`TestImportsAreReadAgainEachRun`); a visible function
  keeps `m.amount` and the overlap is a warning, which `StrictWarnings` promotes.
* **`money.Money {…}` and its call reading.** The parser reads `IDENT "." IDENT` followed by
  a record shape as a qualified pattern; for `money.Money {}` and `money.Money {x}` it
  keeps, as `Alt`, the method call with a trailing closure the same tokens are elsewhere,
  and the compile pass puts it back when the module exports no record of that name or was
  not read — so a host without `CompileIncludes` sees no change. Any other shape is then a
  diagnostic naming which of the three it was. At run time the constructor is the member
  of the value the qualifier holds, and a qualifier that no longer holds the module has no
  such member: the arm does not fire, as with any shape that does not fit.
* **The formatter keeps the call spelling** for a qualified shape that reads both ways
  (`money.Money { x }`): it cannot see another file's exports, and both spellings are the
  same tokens.
* **Open question 2 — no `SkipIncludes`.** `CompileIncludes` is already off by default, so
  a host that compiles untrusted source eagerly does nothing to keep its loader out of
  `Compile`.
* **LSP: no completion across files, on purpose.** The language server's loader reads no
  file (`cmd/mzs/lsp`: compiling a neighbour would be the editor running code nobody asked
  it to), so it cannot learn a module's fields and leaves `CompileIncludes` off; the
  completion the Implementation section mentions needs that read and is not added.

