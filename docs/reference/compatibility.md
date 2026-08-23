# Compatibility and versions

What updating mzs is allowed to do to a program that already runs, and to a Go program that
already compiles. The normative text is [SPEC §21](../../SPEC.md#21-stability-and-versioning);
this page is the practical reading of it, and [`CHANGELOG.md`](../../CHANGELOG.md) is the log.

## The promise, in one line

Inside one MAJOR version, a program that runs keeps running the same way — and if something
did move, the compiler says so with the fix under the caret, rather than the program quietly
meaning something else.

## What is stable

| Stable | What that means for you |
|---|---|
| **Syntax** | a file that parses today parses tomorrow, to the same tree. `mzs fmt` output is not in this list — see below. |
| **Semantics** | `7 / 2` is `3`, `[]` is truthy, dicts iterate in insertion order, one goroutine of a Run evaluates at a time. The decisions D1–D19 are the list. |
| **Standard-library names** | a function keeps its name, its arity, its meaning, and both spellings UFCS gives it (`x.f(y)` and `f(x, y)`). |
| **Modules** | the module list, each module's members, and which capability each is gated behind. A module that needs no `Options` field will not start needing one. |
| **The Go API** | `Value`, `Options`, `Interp`, `Program`, `Result`, `Error`, `FileSystem`, `ModuleLoader` and their exported methods. New `Options` fields are additive, and their zero value keeps meaning the safe answer. |
| **The CLI** | the flags and subcommands that exist keep their spelling and meaning; `$ARGV` keeps its shape; exit codes `0`/`1`/`2`/`3` keep theirs. |

## What may change in any release

| Not stable | Depend on this instead |
|---|---|
| The **wording** of a diagnostic, its fix-it and its column | that the program is refused, and the error **kind** (`try f() else (e) { e["kind"] }`) — which is why `match` on a kind is the documented shape and matching on prose is not. |
| Everything under `internal/` | `mzs/` and `engine/`, the two importable packages. |
| **Performance** and allocation counts | the limits, which are configuration (`Timeout`, `StepBudget`, …) and are stable. |
| The **output of `mzs fmt`** | that it is idempotent, that it preserves the tree, and that it keeps the comments. A prettier line break in a later version is not a break. |
| Anything the specification does not fix — the exact text of `#<fn f>`, the order two tasks resume in after a wait | ask for the guarantee; when it is granted it lands in SPEC and in the table above. |

**Additions are not breaks.** A new function or module cannot shadow a name a program already
uses: locals win over the library, so `len = 3` keeps meaning `3`.

## A name is never removed

When an operation is renamed, the old spelling does not disappear — it becomes a compile-time
diagnostic naming the new one:

```sh
mzs -e '"ПРИВЕТ".downcase'
```

```
-e:1:10: syntax: undefined method 'downcase'; did you mean 'lower'?
  "ПРИВЕТ".downcase
           ^
```

That is the same table the Ruby codemod is driven by ([SPEC §19.2](../../SPEC.md)), and the
rule behind it is the one that keeps the library to one name per operation: the cost of a
rename is a diagnostic, never a silent change of meaning. The worst it can do to your program
is refuse to compile it.

## What `0.x` means here

The version is `0.1.0`. The `0` means *young*, not *anything may happen*: the promise above
may be broken deliberately, with a MINOR bump and a `CHANGELOG.md` entry saying what moved
and a diagnostic wherever one can carry the reader across. There is no version at which a
quiet break is allowed.

## One version string

```sh
mzs --version      # mzs 0.1.0
```

The binary, the newest released section of [`CHANGELOG.md`](../../CHANGELOG.md) and the line
`README.md` prints as this command's output must agree — CI fails when they do not, because a
promise about a version means nothing if the version is three different strings.

## See also

- [../../CHANGELOG.md](../../CHANGELOG.md) — what each version changed
- [./limitations.md](./limitations.md) — what the language does not have, and the syntax reserved for later
- [./sandbox.md](./sandbox.md) — the capabilities a host grants and the limits every run is bounded by
- [./verification.md](./verification.md) — the checks that keep the promise honest
