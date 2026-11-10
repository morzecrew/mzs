# RFC 0019 — Tooling: lint, doc comments, coverage, profile, and the rest of the LSP

**Status:** Draft · **Area:** tools · **Depends on:** 0009 (frames carry files) · **Audit:** T1–T4

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
