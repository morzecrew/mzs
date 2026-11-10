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

the including unit's compile step knows the fields of every record the module exports.
`include … from` takes a string literal by grammar (§4), so the dependency is known when
the includer compiles; the compile step asks `Options.ModuleLoader` for the module then
rather than at the first run, and caches the compiled module on the `*Program` (the module
still *runs* once per Run, §12.8 — compiling is not running).

Built-in modules export no records today; if one ever does (`decimal`'s `Decimal`), the
same rule applies.

## Compatibility

Additive: `x.name = …` is a syntax error today for every `name`. Moving module compilation
from first-run to compile time changes *when* a missing library is reported (at `Compile`
rather than at `Run`) but not whether — and `--check` starts catching it, which is the
behaviour one would have guessed.

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
