# Changelog

What changed, version by version. What a version *promises* is [SPEC §21](SPEC.md#21-stability-and-versioning):
inside one MAJOR the syntax, the semantics (D1–D19), the standard-library names, the module
set, the Go API and the CLI's contract do not move; diagnostic text, `internal/` and
performance do. A name is never removed — it becomes a diagnostic that says the new one.

Versions are `MAJOR.MINOR.PATCH`. While the MAJOR is `0` that promise has one exception, and
only one: a **deliberate** break bumps the MINOR and says here what moved (SPEC §21.4), so
`0.2.0` may break a program `0.1.0` ran and `0.1.1` may not. A quiet break is allowed at no
version.

`Unreleased` is what is on `main` and not in a version yet, so a host reading this page can
tell what updating today would give it. A version section carries the date its tag was cut;
`0.1.0` has none because no tag has been cut yet — it is the version `mzs --version` prints,
and the release is still ahead of it.

## Unreleased

### Added

- **A search path for libraries.** `include money from "money"` — a path with neither `./`
  nor `../` — is a *library name*, looked for on `$MZSPATH` (the `~/.mzs/lib` default when it
  is unset), in either of two layouts: `money.mzs`, or `money/main.mzs` for a library that
  outgrew one file. A path with `./` or `../` keeps meaning exactly what it meant: a file
  beside the including one. This is what makes a library reusable between projects without
  every file naming its neighbour by a relative path. CLI only — the core still asks
  `Options.ModuleLoader` (SPEC §12.8, §15).
- **A root per directory tree.** The confinement that kept a script inside its own directory
  now travels with the file doing the including: a library is rooted in the search directory
  it was found under, so it reaches its own neighbours and nothing above them — a vendored
  library inside a project cannot read the project (SPEC §15).
- **`CHANGELOG.md` and SPEC §21**, the compatibility promise this file is the log of. CI
  checks that `mzs --version`, the newest version section here and every version README and
  `docs/` print are one string.

### Changed

- **A function you can see wins in method position too** — a deliberate break (SPEC §21.4).
  `x.f(y)` now tries a binding named `f` that holds a function (a `fn`, a closure, a
  parameter), then a host function installed with `Register`, and only then the stdlib
  table — the order `f(x, y)` always used. So `fn sum(xs) { 99 }; [1, 2].sum` is `99`
  where it was `3`, and the promise of §21.2, that a name the library adds can only
  collide with a local and the local wins, holds for method calls as well. What can
  change: a program with a function or parameter spelled like a stdlib row and called in
  method form — `fn pick(xs, filter) { xs.filter(filter) }` now calls the parameter, and
  `fn sort(xs) { xs.sort }` now recurses. A host that registered a function spelled like a
  row now answers that row in method form as well (`Register("upper", …)` takes
  `"x".upper`). Rename the function, the parameter or the host function. A
  binding holding no function (`count = 0`) leaves the row alone, a record's own field
  still comes first, and `x in c` still asks the `has` row (SPEC §4.3, §8.7; audit B9).
- **A gated name says which option it needs.** `now()`, `rand()`, `uuid()`,
  `xs.shuffle` and `xs.sample` without the capability used to answer with a suggestion of
  the very name that failed (`undefined function 'now' (did you mean 'now'?)`). They now
  name the option — `it needs a clock, and the host did not install Options.Now (mzs
  --time)` — the way `include time` already did, and no suggestion is ever the name
  itself. A name removed with `Unregister` is not reported as gated (SPEC §17).
- **The example in `mzs test --help` runs.** It multiplied a `decimal(…)` call, and a
  module is never callable; it is SPEC §12.18's `decimal.times(decimal.of("10.00"), 3)`
  now, and a test runs the help text's example so it cannot drift again.
- **SPEC §4's grammar spells a dict with braces.** `Collection` still showed the earlier
  draft's `[a: 1]` form, which D3 and the parser have refused since 0.1.0.
- **An include may not leave its tree through a symbolic link.** The confinement is enforced
  by the read (`os.Root`) rather than by the spelling of the path, so a link that is
  lexically inside the root and leads outside it is refused with the same message as a
  written `../` that leaves; an absolute link is refused with it. A link that stays inside
  the tree is followed as before. A library kept elsewhere is reached by putting its own
  directory on `$MZSPATH` (SPEC §15).
- **A library that is there and cannot be read is an error, not an absence.** The search no
  longer passes over a candidate whose read failed — a permission, an I/O error, a link out
  of the tree — because the next directory's answer would be a different library under a name
  this one already claimed.
- A refusal to load a module now names the spelling that was meant: a library that is not
  found says where it looked, `"text.mzs"` says to write `"text"` or `"./text.mzs"`, and a
  library name with `..` in it is refused with the `./` form that may climb.

## 0.1.0

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
