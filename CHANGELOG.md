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

- **Doc comments and `mzs doc`** (RFC 0019, part 2).
  - A run of `##` lines directly above a top-level `fn`, `record`, `export` or binding is
    that declaration's documentation. A plain `#` stays an ordinary comment.
  - `mzs lsp` shows it on hover and in completion, for the file's own names and for the
    exports of an `include … from` module. Completion after `lib.` works while the file
    is still being typed.
  - `mzs doc [--json] [path…]` prints each file's declarations with their signatures and
    doc comments, exported names first. `--json` emits the `modules` shape of the
    standard library's own documentation (SPEC §3.2, §15.4).
- **Warning codes, suppression, and seven new warnings** (RFC 0019, part 1).
  - Every warning has a stable code, printed in brackets after the message, and as the
    `code` of an LSP diagnostic: `… never used [unused-variable]`. `Warning.Code` carries
    it in the Go API.
  - `# mzs:allow code` allows a code on the line it follows, or on the next line when it
    stands alone. `Options.AllowWarnings` allows a code everywhere, before
    `StrictWarnings` sees it.
  - New: `unused-variable` (a function's local that is never read), `unreachable`,
    `constant-condition`, `duplicate-key`, `self-compare`, `empty-else` and
    `empty-ensure`. A host with `StrictWarnings` set may see a program that compiled
    before fail now, and can allow the new codes (SPEC §17, §21.1).

- **Dict and array rows that their neighbours already had** (RFC 0016, part 1).
  - **Dicts** gain `reduce(init)`, `count`, `none`, `sum`, `min_by`, `max_by`, `group_by`
    and `partition`, with closures that take `(k, v)`. They also gain `map_values`, which
    keeps a record's label, plus `pick` and `omit`.
  - **Arrays and ranges** gain `mean`, `median`, `product`, `transpose`, `rotate`,
    `chunk_while`, `accumulate` (the running `reduce`) and `bsearch`.
  - `avg` and `average` now suggest `mean` (SPEC §12.3, §12.4).
- **Time and crypto rows** (RFC 0016, part 5).
  - `strftime` gains `%s` (unix seconds), `%L` (milliseconds) and `%f` (microseconds), and
    a time has `ms`, its unix milliseconds.
  - `time.mono()` is seconds on a monotonic clock, for timing a step. It needs
    `Options.Now`, as `time.now` does.
  - `crypto` gains `sha512` and `sha384`, and `hmac` signs with either.
  - `crypto.random(n)` gives `n` secure random bytes in hex. For embedders: it reads a new
    `Options.CryptoRand io.Reader` and is absent without it, because `Options.Rand` may be
    seeded. The CLI's `--rand` sets it from the operating system, never from the seed
    (SPEC §12.8, §12.16).
- **Number rows and math members** (RFC 0016, part 4).
  - `div(a, b)` is floored division, the partner of `%`, so `div(a, b) * b + a % b == a` while the arithmetic stays in int64
    and `div(-7, 2)` is `-4` where `-7 / 2` is `-3`.
  - `sign` gives `-1`, `0` or `1`; `gcd` and `lcm` take ints and are never negative;
    `is_nan` and `is_inf` are the IEEE tests, which `==` cannot be.
  - `math` gains `asin`, `acos`, `sinh`, `cosh` and `tanh`, and the constants `inf` and
    `nan`, the two IEEE values that have no literal (SPEC §12.5, §12.8).
- **String rows for text that has to fit** (RFC 0016, part 3).
  - `trim`, `trim_start` and `trim_end` take an optional cutset, a set of runes:
    `"xxhixx".trim("x")` is `hi`.
  - `take` and `drop` count runes, as on an array.
  - `truncate(width, tail = "…")` and `wrap(width)` measure display columns, so CJK and
    emoji text fits its column. `wrap` reflows the text as one paragraph.
  - `indent(prefix)` prefixes every line that has something on it.
  - SPEC §21.1 now promises that a valid call stays valid, which lets a row gain an
    optional parameter (SPEC §12.2).
- **Seq rows for streaming pipelines** (RFC 0016, part 2).
  - `zip`, `concat`, `uniq`, `each_slice` and `each_cons` are lazy. `zip` stops with the
    shortest, because a seq may be endless, and `each_slice` / `each_cons` given a closure
    walk the sequence as on an array.
  - `last`, `min_by`, `max_by`, `tally` and `group_by` are terminals.
  - The rows that remember charge what they keep to `MaxCollection` as it grows, so
    `uniq` over an endless source of distinct keys ends on the cap (SPEC §12.14).
- **The `args` module.** `include args` gives a script command-line flags of its own,
  declared in one dict: `args.parse({width: 800, force: {default: false, short: "f"}})`.
  The kind of each default decides the conversion: int, float, string, bool with
  `--no-x`, an array for a repeated flag, or `nil` for an optional string. Short flags
  combine, `--` ends the flags, and `--help` prints a usage built from the same dict, which
  `args.usage` also returns. A usage error writes the message, with the near miss, and the
  usage to stderr and exits 2. With `{exit: false}` it raises an `argument` error with
  `data: {flag, reason}` instead. The module reads `$ARGV` and needs no host option (SPEC
  §12.21, RFC 0017).
- **The `path` module, and the rest of `io`.** `include path` takes file names apart and
  puts them together — `join`, `dir`, `base`, `ext`, `stem`, `clean`, `split`, `rel`,
  `is_abs` and `match`, a shell glob whose `**` crosses directories. It is lexical and
  always `/`, never looks at a disk, and needs no host option. `io` gains `stat` (`{size,
  dir}` or `nil`), `walk` (every path under a directory as a lazy seq, depth-first and
  sorted, never entering a symbolic link), and `rm` (one name, never recursive; `false`
  when it was not there), `mkdir` (with parents) and `mv`. For embedders: `FileSystem`
  is unchanged, and the four new members are backed by optional interfaces —
  `LinkStater` for `walk`, `FileRemover`, `DirMaker` and `FileMover` for the other three —
  of which the last three also need the new `Options.FSMutations`, so no existing host
  hands scripts `io.rm` on upgrade. A member the host did not grant is absent and a compile
  error that names what is missing. The CLI grants all of them; the new `--read-only`
  (on `mzs` and `mzs test`) takes back `write`, `append`, `rm`, `mkdir` and `mv` for one
  command (SPEC §12.13, §12.20, §13.2, §14.3, §15; RFC 0015).
- **`test` grows up.** Five members join `case` and `eq`: `test.ne`; `test.near(a, b, eps)`
  for floats (`1e-9` by default; NaN is near nothing); `test.raises(kind) { … }`, which
  fails unless the block raises — an error of that kind, when named — and answers the
  error dict, so a case goes on to its message and data; `test.skip(reason)`, which ends a
  case as skipped (not failed, not catchable, `ensure` runs) or, outside a case, the rest
  of the file; and `test.group(name) { … }`, which reports its cases as `name › case`.
  `mzs test` gains `--filter <regex>` (on the full name; a case left out is not run),
  `--fail-fast`, `--json` (one object per case, then a summary; the scripts' own output
  goes to stderr), `--now <rfc3339>` (`--time` with a fixed clock) and `-v`. A case's
  output is now held until it is over and printed under it when it fails, or always under
  `-v`, instead of being interleaved; the summary counts skips, which do not fail a run.
  For embedders: `TestCase` gains `Status`, `Group`, `Reason`, `Line`, `Col`, `Output`,
  `RestOfFile` and `FullName()`, and `Options.Test` (`Filter`, `FailFast`, `Quiet`, `Capture`, `Verbose`)
  asks for what the CLI does; `ErrFailFast` is what a fail-fast Run ends with. The zero
  `Options.Test` is the module as before (SPEC §12.18, §13.2, §15.2; RFC 0014).
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

### Fixed

- **A write into a module stays in the Run that made it.** A module is a dict a script may
  write to, and `include` handed every interpreter in the process the same built-in table:
  `json["parse"] = 1` in one Run broke `json.parse` in every interpreter after it, and a
  write into a host module (`RegisterModule`) stayed for the next Run of that `Interp`.
  Each `include` now gets its own copy (SPEC §14.3).

### Changed

- **An unknown `strftime` directive is an error** — a deliberate break (SPEC §21.4, audit
  B6). `t.strftime("%G")` used to answer `%G`, so a typo surfaced only in the output; it is
  now an `argument` error naming the directive, and so is a layout that ends in a lone `%`.
  Write `%%` for a literal percent sign.

- **Everything after the script path is the script's** — a deliberate break (SPEC §21.4).
  `mzs` used to read its own flags anywhere on the command line, so `mzs resize.mzs --width
  800` failed with `unknown flag --width` and `mzs s.mzs --json` turned on mzs's `--json`.
  Now mzs reads flags only before the script path, and everything after it reaches the
  script as `$ARGV`, as in every other interpreter. With `-e`, the first positional starts
  `$ARGV` instead of being refused as a script file. A `--` right after the script path is
  still dropped, so `mzs s.mzs -- --width 800` gives the script what it gave before. A `--`
  further along now reaches the script: `mzs s.mzs a -- b` gives `["a", "--", "b"]`. A
  script that exits 2 while an argument after it spells a long mzs flag gets a one-line
  note saying where the flag went. Put mzs's flags before the script: `mzs --json s.mzs`.
- **A CLI flag that takes no value refuses one** — a deliberate break (SPEC §21.4). `mzs` and
  `mzs test` used to accept `--no-io=false` or `--fail-fast=no` and act on the flag,
  ignoring what followed the `=`, which is the opposite of what was typed. Such a command is
  now a usage error (exit 2) that names the flag: `flag --read-only takes no value, got
  "--read-only=false"`. Flags that take a value — `--timeout=5s`, `--rand=7` — are
  unchanged.
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
