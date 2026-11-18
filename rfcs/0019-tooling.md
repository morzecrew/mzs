# RFC 0019 — Tooling: lint, doc comments, coverage, profile, and the rest of the LSP

**Status:** Partly landed: §1 (#37), §2 (#38), §3 (#39) · **Area:** tools · **Depends on:** 0009 (frames carry files) · **Audit:** T1–T4

## Summary

Five additions around the language, none of which changes what a program means:

1. more compile-time **warnings** (`--check`),
2. **doc comments** (`##`) that hover, completion and a generated page can show,
3. **coverage** for `mzs test`,
4. a deterministic **profile** by interpreter steps,
5. four more **LSP** requests, including code actions for the fix-its diagnostics already carry.

They are one RFC because they share one mechanism each way: (1) and (5) share the
compile pass; (3) and (4) share one per-node counter in the evaluator.

## 1. Warnings

`mzs --check -e 'x = 1; y = 2; x'` prints `ok`. The compile pass warns about an unused
*closure parameter* and about `=` where `==` was meant, and little else. Proposed, each a
`Warning` (so `Options.StrictWarnings` promotes it, and the LSP shows it):

| Code | Fires on | Example message |
|---|---|---|
| `unused-variable` | a local assigned and never read (not `_`-prefixed, not exported, not the last statement's value) | `'y' is assigned and never used` |
| `shadowed-builtin` | a local or parameter named like a stdlib row *and then called through UFCS on a non-matching use* — or simply declared at file scope | `'len' shadows the built-in len for the rest of this scope` |
| `outer-assignment` | see RFC 0004: `=` inside a function writing an enclosing binding it never read | |
| `unreachable` | a statement after `return` / `break` / `next` / `raise(…)` / `exit(…)` in the same block | `unreachable: the line above always leaves` |
| `constant-condition` | `if true`, `while false`, a `match` arm after `else` | |
| `duplicate-key` | `{a: 1, a: 2}` | `key "a" is written twice; the second wins` |
| `self-compare` | `x == x`, `x != x` (suggest `is_nan`, RFC 0016) | |
| `empty-ensure` / `empty-else` | `try f() else { }` swallowing an error without a comment | `this 'else' discards every error; say so with 'else nil'` |

Warnings get a stable **code** (the first column), printed in brackets after the message
and usable in a suppression comment on the same or the preceding line:
`# mzs:allow unused-variable`. The *text* stays unstable (§21.2); the code is what a
suppression and an editor filter key on, so codes become part of §21.1.

## 2. Doc comments

A run of `##` comment lines directly above a `fn`, `record`, `enum`, `const` or `export`
is that declaration's documentation. Plain `#` stays an ordinary comment.

```
## Total of a cart in the cart's currency.
## Raises kind "billing" when currencies are mixed.
export fn total(items) { … }
```

* **Hover / completion / signature help** show it, for local declarations and for members
  of an included script module (the LSP already resolves `include … from`).
* **`mzs doc [path…]`** prints Markdown: one section per file, exported names first, each
  with its signature (parameters, defaults, RFC 0010 contracts) and its doc text.
  `--json` emits the same as data — the format `internal/apidoc` already uses for §12, so
  the built-in and user documentation are one shape.
* **`mzs fmt`** keeps doc comments attached to their declaration and re-flows nothing.
* The first line is the summary; there is no tag syntax (`@param`). Markdown in, Markdown
  out.

## 3. Coverage

`mzs test --cover` reports which statements ran.

```
cart.mzs        87.5%   (28/32)   missed: 41-43, 57
money.mzs      100.0%   (19/19)
total           92.2%
```

* Unit: the **statement** (a `Stmt` node), plus each `match` arm and each side of
  `if`/`else`/ternary/`&&`/`||`/`??` as a branch.
* Counted for every file loaded through `include … from` during the test Runs; `*_test.mzs`
  files themselves are excluded. Counts merge across files' Runs.
* `--cover-out <file>` writes LCOV, which every CI and editor already reads;
  `--cover-min <pct>` makes the run exit 1 below a threshold.
* Go API: `Options.Coverage *Coverage` — a host passes one in and reads it after `Run`;
  `nil` (the default) costs one predictable branch per statement.

## 4. Profile

`mzs --profile script.mzs` prints, after the run, where the **interpreter steps** went:

```
   steps      %   calls  function
 1840212  61.3%   12000  score          rank.mzs:14
  611004  20.4%       1  load           rank.mzs:3
  ...
```

Steps, not nanoseconds: the step counter already exists (§14.1), it is what the budget
charges, and it is deterministic — the same input gives the same profile on any machine
(D15), which makes it diffable and usable in CI. `--stats` keeps printing wall time for
the whole run. Self and cumulative steps per function, attributed through the frames of
RFC 0009; `--profile-out <file>` writes the folded-stack format flame-graph tools take.
Go API: `Result.Profile` when `Options.Profile` is set.

## 5. LSP

Today: diagnostics, hover, definition, completion, formatting, documentSymbol (§15.3),
with full-document sync. Add:

| Request | Answer |
|---|---|
| `textDocument/codeAction` | **apply the fix-it.** Nearly every diagnostic of §5.6/§17 names its replacement (`'unless' … use 'if !(c)'`, `did you mean 'lower'?`). The parser produces these from tables; expose the replacement as a `TextEdit` and the editor offers "Replace with `lower`". This is the highest-value item here: the language's signature feature becomes one keystroke |
| `textDocument/references` | every use of a local, a `fn`, a `record`/field, or an exported name within the file and the files that include it |
| `textDocument/rename` | the same set, edited; refuses when the new name would shadow or be shadowed |
| `textDocument/signatureHelp` | parameters and defaults while typing a call, built-in or user-defined, with the doc comment's first line |

Also: incremental sync (`change: 2`) — full sync re-sends the file per keystroke, which
shows on large files; and `workspace/didChangeWatchedFiles` so that a module edited on
disk refreshes its includers' diagnostics.

Still out: the server never runs a program (§15.3). No debugger protocol is proposed —
`debug()`, traces (RFC 0009) and the profile cover the ground a tree-walker with a
one-second default timeout needs.

## Compatibility

All additive. New warnings can turn a clean `--check` noisy and, under
`StrictWarnings`, turn a compiling program into a failing one; §21.2 lists diagnostics as
unstable, but hosts using `StrictWarnings` should be told in the changelog, and each new
code can be allow-listed in `Options` (`AllowWarnings []string`).

## Alternatives

* **A separate `mzs lint`.** Two commands that both parse and both complain; `--check`
  already is the linter, it is just short on rules.
* **Wall-clock profiling.** What people expect, and non-reproducible; offer `--stats` for
  that.
* **Doc comments as `#:` or `///`.** `///` is a regex followed by `/`; `##` is already a
  comment and already renders as a heading in the head of a Markdown-minded reader.

## Open questions

* Which warnings are on by default? Proposed: all but `shadowed-builtin`, which is noisy
  in one-liners (`len = xs.len`).
* Should `mzs fmt` gain `--fix` to apply unambiguous fix-its in bulk? It would make the
  §19.2 codemod a general tool.
* Coverage of `-e` one-liners and of script modules loaded by an embedder's own loader.

## Implementation

Compile pass in `mzs/interp.go` (warnings, codes, suppression comments via
`internal/lexer` comment tokens), `internal/parser` (attach `##` runs to declarations;
fix-its as structured edits), `mzs/eval.go` (one counter hook shared by coverage and
profile), `cmd/mzs/test.go`, `cmd/mzs/main.go`, new `cmd/mzs/doc.go`, `cmd/mzs/lsp/*`,
`internal/printer` (doc comments), SPEC §15 and §17, `docs/cli/tools.md`.

## As landed

### Part 1: warnings (#37)

§1 landed in `mzs/interp.go` and `mzs/errors.go`, with SPEC §17 and §21.1,
`docs/cli/diagnostics.md` and `docs/embedding/errors.md`.
Decisions:

* **Every warning has a code, the old ones too.** `regex-word-boundary`,
  `unused-parameter`, `discarded-closure`, `assign-in-condition`,
  `field-shadows-method`, `function-shadows-field` and `include-rebinds` name the seven
  that were already there. The code is printed in brackets after the message, set as the
  `code` of an LSP diagnostic, and carried by `Warning.Code`.
* **`unused-variable` is about a function's locals.** A name at file scope is the
  program's, and the one-liner `x = 1` that the REPL and `-e` are made of is not dead.
  An assignment that is the last statement of its block is the block's value and is
  exempt, as the RFC asks, and a `_`-prefixed name is unused on purpose. A `name = …`,
  `:=`, `||=` or `??=` that creates the binding is a candidate, and so is each name a
  destructuring assignment creates. A `for` header's names are loop variables and are
  not. The last-statement exemption takes a local off the list without marking it read,
  so `{ (x) -> x = 1 }` still warns that the parameter is never used.
* **`unreachable` fires once per block**, at the first dead statement. `raise` and `exit`
  count only when they resolve to the built-ins.
* **`constant-condition` looks at the literal as written**, before folding, so `if DEBUG`
  over a folded constant stays quiet. `while true` is how a loop that ends by `break` is
  spelled and is exempt. A `match` arm after `else` is already a syntax error, so that
  case of the proposal needs no warning.
* **`empty-else` fires on the braced form only.** `try { … } else { }` reads as "ignore
  the error"; the expression form `try json.parse(s) else {}` is a default value.
* **`shadowed-builtin` is not in this part.** The open question proposed it off by
  default; it needs the switch to turn it on, which a later part brings with the LSP.
  **`outer-assignment` belongs to RFC 0004.**
* **A suppression comment needs its codes.** A bare `# mzs:allow` allows nothing, so a
  new rule is never silenced by a comment written before it existed.

### Part 2: doc comments (#38)

§2 landed in `internal/doccomment`, `cmd/mzs/doc.go` and `cmd/mzs/lsp`, with SPEC §3.2 and
§15.4 and `docs/cli/tools.md`. Decisions:

* **The comments are read beside the tree, not stored in it.** The lexer already records
  every comment it skips, with whether it stands on a line of its own. `doccomment` pairs
  a `##` run with the statement on the line below, so the parser, the AST and the
  compiler are unchanged, and no program can observe a doc comment.
* **`##` must be followed by a space or end the line.** `###` and a `####…` rule are
  banners people already write, and a remark at the end of a line of code is not about
  the line below. A blank line ends a run, so a comment separated from its declaration
  documents nothing.
* **`const` and `enum` arrive with RFCs 0004 and 0005.** Until then a doc comment
  documents a `fn`, a `record`, an `export`, or a top-level binding that an assignment
  creates (`x = …` or `x := …`; `x += 1` creates nothing). `export name` under an
  undocumented declaration may carry the comment for it.
* **What `mzs doc` lists.** Every `fn` and `record` is listed, because a signature is
  documentation on its own. A binding is listed when it is documented or exported, since
  an undocumented helper value is not the file's interface. A default is quoted as the
  source wrote it, and one that spans lines is `…`. RFC 0010 contracts will appear here
  once they exist.
* **`--json` is apidoc's `modules` table, widened.** Each entry keeps `sig`, `sem` and
  `ex`, and adds `kind`, `exported` and `line`. A reader of the standard library's table
  reads this one unchanged.
* **A directory's `*_test.mzs` files are skipped.** A test file is the runner's input,
  not anyone's interface. Named on the command line, it is documented like any other file.
* **Hover and completion, not signature help.** The server has no signature help to put
  a doc comment in yet; §5 brings it. Completion after `lib.` reads the include from the
  tokens, because a file being typed does not parse and that is when completion is asked
  for. The included file is parsed for its doc comments and never compiled or run, and it
  is found by the CLI loader's rules: a `./` or `../` path is a neighbour, a bare name is
  a library on `$MZSPATH` where the first directory holding the name answers, and the
  read goes through `os.Root`. The CLI confines a neighbour to the directory of the
  script it starts, which an editor cannot know, so the server confines it to the
  outermost workspace folder that holds the document: every script the CLI could start
  from inside the workspace has its root inside that folder, nested folders included, so
  no include the CLI accepts from there is refused, and nothing outside the workspace is
  read. With no workspace the root is the document's own directory. That is narrower
  than the CLI on purpose: there is then no boundary that is not a guess, and a `../`
  include shows no documentation rather than reading wherever it points.
* **A destructuring line documents every name it binds**, and a comment above a bare
  `export name` documents that name wherever its declaration stands, since a `fn` is
  hoisted. A signature with a backtick in a default gets a longer code-span fence.
* **Carried from part 1:** the last-statement exemption of `unused-variable` now applies
  only when a block's value is received. In `fn f() { (a = 1); 0 }`, or an `if` whose
  value is thrown away, `a` is a dead store and is warned about. A loop body and an
  `ensure` never give their last statement as a value — `for` gives its iterable, `while`
  nil, and an `ensure`'s value is discarded — so their final assignment is not exempt
  either.

### Part 3: coverage (#39)

§3 landed in `mzs/coverage.go`, `mzs/eval.go` and `cmd/mzs/cover.go`, with SPEC §13.2 and
§15.2, `docs/cli/tools.md` and `docs/embedding/README.md`. §4 and §5 are still to come.
Decisions:

* **A file is registered when a Run starts it, not when it compiles.** `runProgram` is
  where the main program and every `include … from` begin, under the name the loader
  resolved, so each file's statements and branches are put in at zero there and counted
  as `execStmts` reaches them. A file no test ever loaded is not in the report: nothing
  in the run knew it existed.
* **The unit is the statement of a statement list**, which is exactly what `execStmts`
  runs: a file, a block, a function body, a `( … )` group, a `match` arm. A statement the
  compile pass folds away is not there to be counted. A **line** is covered when its
  least-run statement ran, so `if c { a() }` on one line is not covered when `a()` never
  was.
* **Branches** are each side of an `if` (an `if` with no `else` still has a false side),
  of a ternary and of `&&`, `||` and `??` (the short circuit, and the right side), and
  each arm of a `match`. They are keyed by the node's kind as well as its position,
  because a ternary and the `&&` that is its condition start at the same place. The text
  report shows statements, as the RFC's sample does; the branch counts go to LCOV `BRDA`
  lines and the `--json` object.
* **One `Coverage`, shared and locked.** Every Run that holds it adds to the same counts,
  and a mutex makes it safe across Runs and interpreters. With none, the cost is one nil
  check per statement and per branch.
* **A file is its name and its text.** The key is the name with a SHA-256 of the
  source, and a hit is counted on the tree node, never on a position. The same text adds
  up however often it is compiled, and two texts under one name — every `Interp.Eval`
  is `eval` — cannot count for each other.
* **What a test file is.** The report leaves out every file the command ran as a test,
  since a file named on the command line is a test whatever it is called, and any
  `*_test.mzs` another one includes. Both lists are compared as absolute paths, because
  the runner names a file as it was typed and the loader names it absolutely.
  `--cover-out` with an empty name is a usage error.
* **`--cover-out` and `--cover-min` imply `--cover`**, and a percentage may be written
  with or without `%`. `NaN` is not a percentage. `--cover-min` compares the total of the included files, so a run
  that includes nothing is at 100% and says that it covered nothing.
* **Open question, answered narrowly:** a `-e` one-liner and a script loaded by an
  embedder's own loader are covered when the host passes a `Coverage`. The CLI offers
  coverage only under `mzs test`.
* **Carried from part 2:** a reassignment declares nothing for `mzs doc`. `x = 2` after
  `x = 1` is not listed, and its comment does not document the first binding.
