# Changelog

What changed, version by version. What a version *promises* is [SPEC §21](SPEC.md#21-stability-and-versioning):
inside one MAJOR the syntax, the semantics (D1–D19), the standard-library names, the module
set, the Go API and the CLI's contract do not move; diagnostic text, `internal/` and
performance do. A name is never removed — it becomes a diagnostic that says the new one.

Versions are `MAJOR.MINOR.PATCH`. Dates are the day the tag was cut. `Unreleased` is what is
on `main` and not yet in a version, so a host reading this page can tell what it would get by
updating today.

## Unreleased

### Added

- **A search path for libraries.** `include money from "money"` — a path with no `./` — is a
  *library name*, looked for on `$MZSPATH` (the `~/.mzs/lib` default when it is unset), in
  either of two layouts: `money.mzs`, or `money/main.mzs` for a library that outgrew one file.
  A path with `./` keeps meaning exactly what it meant: the file next to the including one.
  This is what makes a library reusable between projects without every file naming its
  neighbour by a relative path. CLI only — the core still asks `Options.ModuleLoader`
  (SPEC §12.8, §15).
- **A root per directory tree.** The confinement that kept a script inside its own directory
  now travels with the file doing the including: a library is rooted in the search directory
  it was found under, so it reaches its own neighbours and nothing above them — a vendored
  library inside a project cannot read the project (SPEC §15).
- **`CHANGELOG.md` and SPEC §21**, the compatibility promise this file is the log of. CI
  checks that `mzs --version`, the newest released section here and the line `README.md`
  prints are one version string.

### Changed

- A refusal to load a module now names the spelling that was meant: a library that is not
  found says where it looked, `"text.mzs"` says to write `"text"` or `"./text.mzs"`, and a
  library name with `..` in it is refused with the `./` form that may climb.

## 0.1.0 — 2026-08-24

The first version: the language as [`SPEC.md`](SPEC.md) describes it, the CLI around it, and
the Go API to embed it. `go.mod` has no `require` and never will (I1).

### The language

- Values: int, float, string (runes, immutable), bool, nil, array, dict (insertion-ordered),
  regex, function — and `record` for a name over a dict shape.
- One semantics, no modes: `{ … }` is a closure everywhere except operand position, `[ … ]`
  is always an array, `7 / 2` is `3`, only `nil` and `false` are falsy.
- Every form has a value: `if`, `while`, `for`, `match`, `try` — and statement modifiers.
- `match` with patterns: literals, ranges, `in`, regexes, record shapes, destructuring arms.
- Errors as values: `try … else … ensure`, `raise`, `assert`, `exit`, and a closed list of
  error *kinds* to branch on rather than prose.
- Destructuring in assignment, `match` arms and `for` headers; heredocs (`<<~TAG`, `<<~'TAG'`);
  named arguments; `*rest` parameters; anonymous `fn` and `(…) -> { … }`.
- `async fn` and the waits — `t.await`, `t.timeout`, `t.cancel`, `tasks.all`, `tasks.race`,
  `sleep` — with one goroutine of a Run evaluating at a time, so no value needs a lock.
- Regex with two backends behind one interface: RE2 where it is safe, a bundled backtracking
  engine with Unicode `\b` and lookaround where it is not.

### The standard library

One flat namespace reached by UFCS (`x.f(y)` ≡ `f(x, y)`): strings, arrays, dicts, numbers,
ranges, sets over arrays, and `seq` — the lazy sequence for input that does not fit in memory.
Modules, each `include`d by name and nothing ambient: `json`, `math`, `time`, `date`, `http`,
`tasks`, `io`, `decimal`, `crypto`, `url`, `test`.

### The tools

- `mzs` — one-liners (`-e`), scripts, pipes, line mode (`-n`/`-l`), `--json`, `--bool`,
  `$vars`, the REPL with history and completion.
- `mzs fmt` — one spelling of a program, nothing to configure; it refuses rather than rewrites
  when it cannot promise the tree and the comments survive.
- `mzs test` — the `*_test.mzs` files, each in its own Run.
- `mzs lsp` — the compiler's own diagnostics in an editor, plus the VS Code extension.

### Embedding

`mzs.New`/`Compile`/`Run` with an immutable, goroutine-safe `*Program`; host values bound
rather than substituted into source; and a sandbox that is a set of `Options` fields — no
clock, no RNG, no filesystem, no module loading unless the host hands it over. Every run is
bounded by a timeout, a step budget, a call depth, a task count and a collection size, and no
limit is catchable.
