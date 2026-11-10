# RFC 0009 — Error traces

**Status:** Draft · **Area:** language, CLI · **Depends on:** — · **Audit:** L15, B5

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
with the frames on its way out of the evaluator (`at` in `mzs/eval.go`); a **limit**
error — step budget, timeout, depth, cancel — is returned straight from `checkLimits`
and reaches `RunResult` after every frame has unwound, so today its stack is empty
(measured: `fn a() { b() }; fn b() { while true { } }; a()` under a step budget ends
with `Stack == []`, while the same shape with `raise` has `[b, a]`). This RFC has
`checkLimits` capture `rs.frames()` into the error it creates, so the trace of §2 exists
for limits too — the case where it is most useful. `Frame` gains `File string` so that frames in an included module are
attributable. That is **not** purely additive: a positional literal `mzs.Frame{"f", 1, 2}`
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
