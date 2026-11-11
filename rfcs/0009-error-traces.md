# RFC 0009 — Error traces

**Status:** Landed (#20) · **Area:** language, CLI · **Depends on:** — · **Audit:** L15, B5

## Summary

The interpreter already records a call stack on every error (`mzs.Error.Stack`). Nothing
shows it. This RFC surfaces it in the two places it is missing: the CLI's diagnostic, and
the dict a script receives in `else (e)`.

## Motivation

SPEC §8.11: "Errors carry the script name, line, column and a short call stack." Observed:

```
# st.mzs
fn a() { b() }
fn b() { c() }
fn c() { raise("boom") }
try a() else (e) { println(e.json) }
a()
```

```
{"message":"boom","kind":"raise","line":3}
st.mzs:3:10: raise: boom
  fn c() { raise("boom") }
           ^
```

The CLI names the line that raised and not how the program got there; with a helper called
from twenty places (`fn must(x) { raise(…) if x == nil }`) the diagnostic identifies the
helper every time. The script-side dict has `line` but no file — useless once an error
crosses an `include` — and no column or stack, so a script cannot log a useful report of a
failure it caught. A long-running `http.serve` handler is the concrete case: it must catch,
and what it can log is a message and a bare number.

## Design

### 1. The error dict

`else (e)` binds, in this order:

| Key | Value | New |
|---|---|---|
| `message` | string | |
| `kind` | string | |
| `file` | the script name the error was born in (`"-e"`, `"cart.mzs"`) | ✔ |
| `line` | int | |
| `col` | int (in runes, as diagnostics count) | ✔ |
| `stack` | array of `{fn, file, line, col}`, innermost first; `[]` at top level | ✔ |
| `data` | the payload, when there is one | |

`stack` holds call sites: entry *i* says "function `fn` was executing at `file:line:col`".
Anonymous functions are `"fn"`, closures `"{}"`, the file body `"main"`, an `async fn`
task adds one frame `"task"` where it was started. The array is capped at the same length
the Go API caps it; the cap is not a stability promise (§21.2).

`raise(e)` re-raise semantics are unchanged: origin and stack of the original (§8.11).
Extra keys a script adds to `e` are ignored by `raise`.

### 2. The CLI

After the caret block, an uncaught error prints its frames, innermost first. None is
skipped: a frame is a **call site** — where the function was called from — and the caret
already shows where it failed, so no frame repeats it. In `fn f() { 1 / 0 }; f()` the one
frame is the `f()` on the last line, which is the location a reader needs:

```
st.mzs:3:10: raise: boom
  fn c() { raise("boom") }
           ^
  in c, called from st.mzs:2:10
  in b, called from st.mzs:1:10
  in a, called from st.mzs:5:1
```

Rules: nothing is printed when the stack is empty (a top-level error looks exactly as it
does today); at most 8 frames, then `… N more`; recursion collapses
(`in fact, called from x.mzs:1:30 (×197)`). `--no-trace` suppresses the block for
tools that parse the first line. `mzs test` appends the same block under a failed case.

### 3. Go API

`Error.Stack` exists, but it is not filled for every error. A script error is stamped
with the frames on its way out of the evaluator (`at` in `mzs/eval.go`). Most **limit**
errors skip that path: the step budget, the timeout and a cancel are returned straight
from `checkLimits`, and the collection and string caps straight from `checkCollection` /
`checkString`, so they reach `RunResult` after every frame has unwound with an empty stack
(measured: under a step budget `fn a() { b() }; fn b() { while true { } }; a()` ends with
`Stack == []`, and an oversized array literal inside a function also has none, while the
same shape with `raise` has `[b, a]`). The depth limit is the exception: `enter` raises
it inside the call, `at` stamps it, and it already carries its frames (measured: 5 under
`MaxDepth = 5`). This RFC has all three helpers capture `rs.frames()` into the error they
create, and `at` keeps a stack that is already set, so no error gets two and none gets
none. The helpers' callers also pass the error through `at` at the expression that
failed, so its *position* is the oversized literal or the loop that ran out of steps, not
the program start that `RunResult` stamps on a position-less error (measured: with
`MaxCollection = 2`, a `[1, 2, 3]` on line 2 of `f`'s body, called from line 4, is
reported at `1:1`). `Frame` gains `File string` so that frames in an included module are
attributable. A field alone would record the wrong file: `include` restores `rs.file`
when the module has loaded (`mzs/module.go`), and an exported function's body later runs
with the *importing* file current, so a module line would be paired with the caller's
file. So a function value also records the file it was compiled in; a call pushes its
frame with the caller's file and position, then makes the callee's file current for the
body and restores it on return — the same save-and-restore `include` already does. That is **not** purely additive: a positional literal `mzs.Frame{"f", 1, 2}`
stops compiling, and §21.1 promises that a Go program that compiles keeps compiling. It is
listed as a deliberate §21.4 change (keyed literals, which is what the package itself
writes, are unaffected). `Frame.String()` does not change; whether a frame's file is
printed is decided by the renderer in `cmd/mzs/diag.go`, which holds both the error and the
frame.

## Compatibility

* New keys in `e` are **a deliberate §21.4 break**, not an addition: the dict is a value a
  program can observe, and `try raise("bad") else (e) { e.len }` goes from 3 to 6, as do
  `e.json`, `e.keys` and iteration. It lands with a MINOR bump and a changelog entry. The
  alternative — keeping the dict as it is and adding `trace(e)` — was weighed under
  *Alternatives* and rejected.
* CLI output gains lines **after** the existing three. The first line — the one editors
  and CI regexes match — is untouched. Diagnostic text is not stable anyway (§21.2).

## Alternatives

* **A `trace(e)` function** instead of a key. An error is already a dict, and data belongs
  in the data.
* **Print the stack only with `--trace`.** The default is what people see when something
  breaks at 2 a.m.; the cost is a few lines.
* **Full source excerpt per frame.** Noisy; position is enough to jump to.

## Open questions

* Should limit errors (timeout, step budget, depth) print a trace? For `max call depth
  exceeded` it is the most useful diagnostic there is, and it is 200 identical frames —
  hence the collapsing rule. Proposed: yes.
* Positions inside a `-n` line-mode program: include the input line number?

## Implementation

`mzs/errors.go` (`Frame.File`, dict construction in the `else` binder), `mzs/limits.go`
(`frames()` records the file), `cmd/mzs/diag.go` (rendering), `cmd/mzs/test.go`,
`docs/language/errors.md`, `docs/cli/diagnostics.md`, SPEC §8.11/§13.5/§15.

## As landed

SPEC §8.11, §13.5, §15 and §15.2 are the normative text; this records the decisions the RFC
left open or that the implementation had to make.

* **Open question 1 — limit errors print a trace: yes**, as proposed. They also carry the
  stack in the Go API now: the helpers that raise them capture it (`limitErr`), and
  `execStmts` positions any error that is still unpositioned at the statement that failed.
* **Open question 2 — the input line under `-n`:** nothing new. The CLI already prints
  `mzs: input line N:` before the diagnostic, and the trace follows the caret as usual.
* **No frame repeats the caret.** Builtins and stdlib rows push frames too, so the innermost
  frame of a `raise` is the `raise` call itself, at the error's own position. That frame is
  dropped, once per error. A **limit** error keeps all of its frames: it is raised between
  steps rather than by a call, and under `MaxDepth` its innermost frame is the previous
  level of a recursion written at the same site, which is not a repetition.
* **Frame names.** A closure literal is `"{}"`. A stdlib row that called back into the
  script (`map`, `each`, `test.case`) is a frame under its own name, which is the path the
  call really took. There is no extra `"task"` frame: a task's stack already ends with the
  `async fn`'s own frame at the site it was started, which is the same information.
* **The module's file.** A function records the file it was compiled in, and a call makes
  that file current for the body, its defaults included, and restores it on return.
* **The caret of a module's error** is drawn from the source the CLI's loader read, kept
  per resolved name, and never from a second read of the disk: that read would bypass the
  include confinement the first one went through, and could show a line the program never
  compiled.
* **`mzs test`** prints the frames inside the case: the failure's stack minus the block's
  own frame, `test.case` and everything outside it.
* **`mzs.FormatTrace`** is exported so the CLI and the `test` module share one renderer.
  Its text is diagnostic output (§21.2).

