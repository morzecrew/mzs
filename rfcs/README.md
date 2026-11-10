# mzs RFCs

Proposals for what the language, its standard library and its tools do not have yet.
[`SPEC.md`](../SPEC.md) stays the single source of truth for what mzs *is*; an RFC is a
change to it that has not landed. Nothing here is normative until it is merged into the
specification.

Start with [**0000 — the audit**](0000-audit.md): it is the evidence every RFC below
points back to, plus the defects that need a fix rather than a design.

## Index

| # | RFC | Area | Size | Priority |
|---|---|---|---|---|
| [0001](0001-rest-and-spread.md) | Rest elements and spread: `a, *rest = xs`, `f(*xs)`, `[*a, x]`, `{**d, k: v}` | language | M | P1 |
| [0002](0002-dict-and-record-patterns.md) | Dict and record patterns in `match`, assignment and `for` | language | M | P1 |
| [0003](0003-record-field-assignment.md) | Record fields: `m.amount = 5`, and `.field` across modules | language | S | P1 |
| [0004](0004-const-bindings.md) | `const` — a binding that cannot be reassigned | language | S | P2 |
| [0005](0005-enum.md) | `enum` — a closed set of record shapes, and a `match` that is checked | language | M | P2 |
| [0006](0006-open-ended-ranges.md) | Open-ended ranges and slices: `xs[1..]`, `xs[..3]` | language | S | P2 |
| [0007](0007-labelled-loops.md) | Labelled `break` and `next` | language | S | P3 |
| [0008](0008-strict-number-parsing.md) | `parse_int` / `parse_float` — a conversion that can say no | stdlib | S | P1 |
| [0009](0009-error-traces.md) | Error traces: the call stack a script and a person can read | language, CLI | S | P1 |
| [0010](0010-parameter-contracts.md) | Parameter and field contracts: `fn f(n: int)` | language | L | P3 |
| [0011](0011-memory-budget.md) | An aggregate memory budget | embedding | M | P1 |
| [0012](0012-stderr.md) | `eprint` / `eprintln` — a second output stream | stdlib | S | P1 |
| [0013](0013-csv-module.md) | The `csv` module | stdlib | M | P1 |
| [0014](0014-test-module.md) | `test` grows up: `raises`, `near`, `skip`, `group`, and runner flags | stdlib, CLI | M | P1 |
| [0015](0015-path-and-io.md) | The `path` module, and `io.stat` / `rm` / `mkdir` / `mv` / `walk` | stdlib | M | P2 |
| [0016](0016-stdlib-completion.md) | Standard-library completion: the rows that are conspicuously absent | stdlib | M | P2 |
| [0017](0017-args-module.md) | The `args` module: declared command-line flags | stdlib | S | P2 |
| [0018](0018-big-integers.md) | `big` — integers past 2⁶³ | stdlib | M | P3 |
| [0019](0019-tooling.md) | Tooling: lint, doc comments, coverage, profile, and the rest of the LSP | tools | L | P2 |
| [0020](0020-library-manifest.md) | A library manifest and `mzs get` | tools | L | P3 |

**Size** is implementation effort: S ≈ a day, M ≈ a few days, L ≈ a week or more.
**Priority**: P1 — a program hits this wall quickly; P2 — worth having before 1.0;
P3 — reasonable, not urgent, or needs more design.

## What an RFC must respect

The design decisions of SPEC §2 are decided, and an RFC that needs one of them to move has
to say so in its first paragraph. In practice every proposal here was written against these:

* **D16 — ambiguity is a diagnostic.** New syntax must not give an existing program a second
  reading. Each RFC states what its syntax is *today* (usually: which diagnostic).
* **D17 — one name per operation.** No aliases. A new function is a new operation or it is
  not proposed.
* **D18 — UFCS, one flat namespace.** A new row is callable both ways; a new module is
  reached with `include` and nothing is ambient.
* **D15 / G4 — the zero `Options` is the safe one.** Anything that reaches outside the
  process is gated by a field the host sets — with the one documented exception SPEC §12.11
  makes on purpose: `http` is installed with no option asked for, so a host that runs
  untrusted scripts removes it with `Unregister("http")`. No RFC here adds a second
  exception.
* **G4 — the Go standard library only** (the repository's code and `CHANGELOG.md` call it
  I1; G4 is where SPEC §1.1 states it). No RFC adds a dependency.
* **SPEC §20's permanent list** — no classes, no operator overloading, no `eval`, no
  process spawning, no shared-memory parallelism. No RFC here reopens one.
* **SPEC §21 — compatibility.** Each RFC has a *Compatibility* section saying whether it is
  purely additive or a deliberate break under §21.4. A new stdlib row is **not**
  automatically additive: in method position it currently wins over a program's own
  function of the same name (audit B9), so an RFC that adds a row either lands after the
  dispatch fix proposed in RFC 0016 — which puts program functions *and* host functions
  registered with `Register` ahead of the stdlib table — or lists the row as a break.

## Format

```
# RFC NNNN — title
Status · Area · Depends on
## Summary          one paragraph
## Motivation       what a program cannot write today, with the observed behaviour
## Design           syntax (grammar delta), semantics, diagnostics
## Compatibility    against SPEC §21
## Alternatives     what was considered and why not
## Open questions
## Implementation   where it lands in the tree
```

Statuses: **Draft** (written, not agreed) → **Accepted** (agreed, not built) → **Landed**
(in `SPEC.md`; the RFC then only records the reasoning). Every RFC in this directory is a
**Draft**.
