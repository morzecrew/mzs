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

- **The `csv` module.** `include csv` reads and writes RFC 4180: `csv.parse(s)` gives arrays
  of fields, `{header: true}` dicts in the header's order, and `csv.rows(io.lines)` the same
  records as a seq read one at a time, so a file larger than `MaxCollection` streams
  through. `csv.str(rows)` writes a document (a header from the first dict's keys) and
  `csv.line(row)` one record. Options: `sep`, `header` (also an array of names), `strict`,
  `trim` and `comment`. Every value read is a string; quotes, doubled quotes and line
  breaks inside quotes are handled, a byte-order mark is dropped, and a field is quoted
  when written only where it has to be. Malformed input is the new error kind `csv`, with
  the line and column in the message and in `data`. Like `json` and `url`, it needs no
  host option (SPEC §12.19, §13.5; RFC 0013).
- **Record fields across files.** With `Options.CompileIncludes`, which the CLI sets,
  `Compile` reads each `include … from` at the top level of a file through the host's
  loader and learns the fields of the records the module exports: `m.amount`,
  `m.amount = v` and the record pattern `money.Money {amount} ->` then work on a library's
  shape as on one of the file's own, with the same compile-time checks. Only names are
  kept — each run still loads and runs the module — a read that fails is no compile error
  (the include fails when it runs, as before), an include inside a block is not read, and
  a function the file can see keeps `m.amount` as its call, with a warning naming the
  field. The option is off by default, and without it nothing about includes changes
  (SPEC §7.8, §8.15, §13.2; RFC 0003).
- **Writing a record field.** `m.amount = 2` writes a field the way `m.amount` reads it, in
  place, with the label kept; every compound operator works (`m.amount += 10`,
  `m.note ??= ""`), and a field is a destructuring target (`p.x, p.y = [3, 4]`,
  `{a: p.x} = d`). The name is checked where the file compiles — `m.amout = 2` is
  `syntax: cannot assign to '.amout': not a record field; did you mean '.amount'?` — and at
  run time the receiver must be a record whose declaration has the field: a plain dict, or
  a record of another shape, is a `type` error. A field write never adds a key, so a
  removed entry is a `key` error rather than a write that grows the shape back; adding a
  key stays `d["k"] = v`. Before this, `x.name = …` was a syntax error for every name
  (SPEC §4, §7.8, §8.8; RFC 0003).
- **Dict patterns.** `{name, age: a} = user` takes a dict apart by key, `{"content-type": ct}`
  names a key that is not a word, `{data: {reason}}` and `[id, {name}]` nest, and `**rest`
  binds the entries no key names as a new plain dict. Every key named must be present —
  a missing one is the `key` error `fetch` raises — and keys not named are ignored. In a
  `match` arm `{kind: "paid", total: t} ->` fires on any dict with those keys whose values
  fit, a missing key being the next arm; `{age: in 18..65}` asks membership and `**`
  skips the rest. A `for` variable may be a pattern: `for [id, row] in pairs`,
  `for {name} in users`, `for k, {total} in by_user`. The LSP outline lists every name a
  top-level destructuring binds (SPEC §4, §5.3, §8.15; RFC 0002).
- **Record shapes in patterns.** `Money {amount, currency: "EUR"} ->` fires when the subject
  was built by that `record` and fits the dict pattern after the name; it nests, as in
  `Refund {of: Money {amount}}`. The keys are checked against the declaration at compile
  time — `Money {amout}` is `syntax: record Money has no field 'amout'; it has amount,
  currency`. `Name {…}` is a record pattern only when `Name` resolves to a record; any other
  name keeps the call-with-a-trailing-closure reading it had, so `choose {it} ->` is
  unchanged. A record an included module exports is matched as `money.Money {…}`
  (above) (SPEC §5.3, §8.15; RFC 0002).
- **Rest elements and spread.** `head, *tail = xs`, `*init, last = xs` and `a, *mid, z = xs`
  bind the positions no other target takes as a new array; in a `match` arm
  `["GET", *path] ->` binds them and `[first, *] ->` skips them. At a call `f(*xs)` passes
  an array's or a range's elements as positions and `f(**opts)` a dict's entries as named
  arguments — a parameter given twice is an error, never last-wins. In literals `[*a, x]`
  concatenates and `{**defaults, k: v}` copies entries in order, a later one overwriting in
  the earlier one's place, into a plain dict. A seq is refused (`*s.take(n).array`), and
  `**kw` as a parameter stays reserved. Every new form was a syntax error, so this is
  additive; the one parser change it needs — a line starting with `*` that begins a target
  list starts a new statement instead of continuing the line before — changes no program
  that parsed (SPEC §4, §8.15, §8.16, §20; RFC 0001).
- **`parse_int` and `parse_float`**: a conversion that can say no. `"12abc".parse_int`,
  `"".parse_int` and `"1e400".parse_float` are `nil` where `int` and `float` answer `12`, `0`
  and `0.0`, so a program reading a CSV column or a query parameter can tell `0` from
  garbage without a regex in front of every conversion. `parse_int(s, base)` takes a base
  2–36, or `0` for the `0x`/`0o`/`0b` prefix of a literal; a value outside `int64` is `nil`,
  never a lossy Float. A non-string argument is a `type` error — `x?.parse_int` handles a
  missing value. `int` and `float` are unchanged (SPEC §12.7; RFC 0008).
- **`eprint` and `eprintln`** write to `Options.Stderr` exactly as `print` and `println` write
  to `Options.Stdout`, so a filter in a pipeline can report a skipped row or a summary
  without corrupting the data it emits. A nil `Stderr` discards, so the zero `Options` stay
  silent; the CLI wires it to fd 2, and `mzs test` passes it through. `engine.Options`
  gains `Stderr` so a bot can capture them too. `debug` stays on stdout (SPEC §12.1,
  §13.6; RFC 0012).
- **Error traces.** An uncaught runtime error raised inside a function prints its call
  sites under the caret — `in c, called from st.mzs:2:10`, innermost first, a recursion
  collapsed to one line with a count, eight lines at most — and `--no-trace` turns that off.
  A failed `mzs test` case prints the calls inside the case. `mzs.FormatTrace` is the
  rendering, for a host that wants the same lines (SPEC §8.11, §15; RFC 0009).
- **A memory budget.** `Options.MaxMemory` bounds the bytes one Run holds *live* in script
  values (default 64 MiB; negative disables), which the per-operation caps could not: an
  array of a thousand 1 MB strings broke neither and ran until the clock stopped it with
  the process at 1.5 GB. Exceeding it is `ErrMemory`, `Kind == "limit"`, uncatchable, exit
  code 3. It is enforced by accounting and a reachability walk over the Run's own values —
  deterministic, per Run, never the Go runtime's opinion — so a loop that allocates and
  drops runs forever under it and a live set that grows ends. `Result.PeakMemory` reports
  the most a walk found live; the CLI gains `--mem <size>` (`64m`, `1g`; `0` disables) on
  `mzs` and `mzs test`, and `--stats` prints the peak (SPEC §14.2; RFC 0011).
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

- **A dict literal written as a `match` arm is a dict pattern** — a deliberate break (SPEC
  §21.4). `{a: 1} ->` used to fire only on a subject *equal* to `{a: 1}`; it now fires on
  any dict whose `"a"` is `1`, whatever else it holds, and `{} ->` fires on any dict rather
  than only the empty one. A name in value position binds instead of being read, so
  `{k: limit} ->` binds `limit` where it used to compare with it. The same holds for a
  dict literal that is an element of an array arm, `[{a: 1}] ->`. To keep the old meaning,
  parenthesise: `({a: 1}) ->` compares with the dict, `{k: (limit)} ->` with the variable.
  An arm that spreads, `{**defaults} ->`, binds the subject's entries instead of comparing
  with a copy, and `{**defaults, a: 1} ->` no longer compiles — a pattern's rest comes
  last; parenthesise both to compare. A brace that is not the whole pattern —
  `{a: 1}.len ->`, `{a: 1} in xs ->` — is the operand it always was (SPEC §5.3, §8.15;
  RFC 0002).
- **`Money {x} ->` and `Money {} ->` on a record name are record patterns** — a deliberate
  break (SPEC §21.4). They used to call the constructor with a trailing closure and compare
  the subject with the record that built — which held that closure, so the arm could never
  fire. They now ask for the shape; the name of anything that is not a record keeps the
  old reading (SPEC §8.15; RFC 0002).
- **A destructuring that does not fit writes nothing.** `a, [b, c] = [1, 2]` used to
  write `a` before failing on the nested pattern; every value is now read before any
  target is written, at every depth, so a nested container a target writes into is read
  as it was and an error leaves every target as it stood (SPEC §8.15).
- **The error dict has `file`, `col` and `stack`** — a deliberate break (SPEC §21.4). `else
  (e)` binds `message, kind, file, line, col, stack` (and `data`), so `e.len` is 6 where it
  was 3 and `e.json` and `e.keys` grow to match. `stack` is `[{fn, file, line, col}]`, one
  call site per entry, `[]` at the top level (SPEC §8.11; RFC 0009).
- **`mzs.Frame` has a `File` field** — a deliberate break (SPEC §21.4). A frame names the file
  its call was written in, so frames in an included module are attributable. A positional
  literal `mzs.Frame{"f", 1, 2}` no longer compiles; a keyed one does (SPEC §13.5).
- **An error in an included module is positioned in the module.** A function a module
  exports used to run with the importing file current, so its errors paired a module line
  with the caller's file name and the CLI drew the caret from the wrong source. It now runs
  with its own file, and the CLI shows the module's line (SPEC §8.11, §15).
- **Limit errors carry a stack and a position.** The step budget, the timeout, a cancel,
  the memory budget and the size caps used to reach the host with an empty `Stack` and,
  inside a function, positioned at the program's first line. They now carry the frames
  they were raised under and the position of the statement that ran out (SPEC §8.11).
- **The zero `Options` now bounds memory** — a deliberate break (SPEC §21.4). A program
  that legitimately holds more than 64 MiB of live values under default options now ends
  with `limit: memory budget exceeded`. Restore the old behaviour with `MaxMemory: -1`
  (`mzs --mem 0`), or raise the bound to what `Result.PeakMemory` reports. A Run that
  never comes near the budget pays only the bookkeeping of its roots — no allocation and
  no walk (SPEC §14.2, §14.4).
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
