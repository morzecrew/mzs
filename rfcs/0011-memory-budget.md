# RFC 0011 — An aggregate memory budget

**Status:** Landed (#19), except `mzs.Opaque` · **Area:** embedding, sandbox · **Depends on:** — · **Audit:** §3.1

## Summary

Add `Options.MaxMemory`: a bound on the bytes a Run may have *live* in script values,
enforced by accounting inside the interpreter. Exceeding it ends the Run with
`Kind == "limit"`, uncatchable, like every other limit.

## Motivation

The sandbox bounds time, steps, depth, tasks, and the size of **one** collection or string
(SPEC §14.2). It does not bound how many of those a script may hold. Measured on `main`:

```sh
mzs -e 'xs = []; s = "x" * 1000000; i = 0
        while true { xs.push(s + i.str); i += 1 }'
# -e:1:1: limit: execution timed out after 1s        — peak RSS ≈ 1.5 GB
```

That is with **every option at its default**. Each string is 1 MB (limit: 8 MiB), the
array has about 1500 elements when the clock stops it (limit: 1,000,000), and the loop has
spent a few thousand of its 5,000,000 steps. The timeout is the only thing that ended it,
and a host that raises the timeout raises the ceiling with it.

A7 says a script can never panic the host; the README says it "cannot bring its host
down". Out-of-memory is not a panic and is not recovered at the `Run` boundary — the
kernel kills the process. For the stated use (evaluating conditions inside
morzebot-backend, where one process serves many dialogues), one hostile or merely buggy
rule takes every dialogue with it. This is the only sandbox escape the audit found.

## Design

### The option

```go
type Options struct {
    …
    // MaxMemory bounds the bytes held live by the values of one Run, as accounted by
    // the interpreter (an estimate, never less than the payload). 0 means the default.
    MaxMemory int64   // default 64 MiB; < 0 disables
}
```

CLI: `--mem <size>` (`64m`, `1g`, `0` disables), listed with `--steps` and `--timeout`.
Exit code `3`, like the other budgets (§15).

### Accounting

Every heap-backed value is charged when it is created or grown, to a counter on the Run:

| Value | Charge |
|---|---|
| string | its byte length, when built by an operation (not for literals, which belong to the `*Program`). A substring shares its source's storage in Go, so charging its length would undercount a one-byte slice that keeps a large source alive. The operations that cut a string (`split`, `slice`, `[a..b]`, `lines`, `matches`, `captures`, `trim*`, `first`/`last`) therefore **always copy** their result unless it is the whole source. A copy costs exactly the bytes the result is charged for, so what is charged is what is held, with no threshold to game |
| array | capacity × the size of a `Value` (`unsafe.Sizeof`, 48 bytes on a 64-bit host — not a word), on allocation and on each growth |
| dict | entries × (key + value + bucket overhead), on growth |
| frame / env | every live frame, not only the ones a closure keeps: a header plus the backing arrays an `Env` allocates — `cap(slots)` × the size of a `Value` and `cap(names)` × the size of a string header (48 and 16 bytes on a 64-bit host) — charged on entry and on growth. A frame is **not** released on return when a closure captured it: its charge stays until a reachability walk finds it unreachable, the same way every other value's does. Only a frame nothing captured is released at once. Deep recursion holds every caller's frame, and `MaxDepth` bounds only how many there are, not how large each is |
| regex | an estimate of the compiled program, `64 × len(pattern)` bytes, charged when `regex(…)` builds one. Literal regexes belong to the `*Program`, like literal strings. The estimate is stated as one, since Go does not report a compiled regexp's size |
| seq, task, time | fixed |
| host values (`map[string]Value` passed to `Run`) | not charged: the host allocated them |

Charging is the easy half. A budget on *cumulative* allocation would be simple and
deterministic, but it would kill any long loop that allocates and drops (`io.lines.each`
over a large log, `http.serve` for a week). The budget must be on **live** bytes, which
needs release:

* The counter is decremented by a finalizer-free scheme: the Run keeps the accounted total
  and, when a charge would exceed the budget, performs a **reachability walk** from its
  roots (globals table, live environments, task stacks, pending values) summing the real
  sizes, and replaces the counter with that sum. If the walk's result still exceeds the
  budget, the Run ends.
* So the counter is an upper bound that is corrected lazily, and a program that stays
  under the budget never walks. The naive version of this has a bad case: with live data
  just under the budget, every small allocation would trigger a full walk. So the budget
  is enforced with **slack**: after a walk that finds `L` live bytes, the next walk happens
  only once another `max(MaxMemory − L, MaxMemory / 8)` bytes have been charged. When a
  walk runs *before* a charge `c` that would cross the threshold and lets it through, the
  next threshold is measured from `L + c` — the live set including the allocation just
  allowed — so the triggering charge is counted in the interval that ends, never in the
  one that starts. Worked through with `MaxMemory = 800`, `L = 749` and a loop that
  allocates and drops 51 bytes: the threshold is `749 + max(51, 100) = 849`; the second
  allocation would reach 851, so it walks, finds `749 + 51 = 800 ≤ 800`, allows it, and
  sets the next threshold to `800 + max(0, 100) = 900`. The following walk is therefore
  at least `MaxMemory / 8 = 100` charged bytes later, and every interval between walks is
  at least that long. The live
  set may therefore overshoot `MaxMemory` by up to an eighth before the walk that ends the
  Run, which the documentation states, and in exchange a walk (whose cost is bounded by the
  live set, itself ≤ `MaxMemory × 9/8`) is paid for by at least `MaxMemory / 8` allocated
  bytes — at most about nine units of walking per allocated byte, whatever the program
  does.
* **A single charge larger than the remaining slack** is not allowed to slip past: a
  charge is checked *before* the allocation it pays for, and one that would cross the
  threshold walks first. If the walk's live bytes plus the charge exceed `MaxMemory`, the
  Run ends without allocating, so the overshoot bound holds for one large allocation too.
* **Host values** (bound with `Run`, or retained by registered functions) are reached by
  the walk but not charged. Traversing them is real work, so the walk-cost bound is stated
  with them included: a walk costs at most the script's live bytes plus the host-bound
  graph `H`, and `H` is fixed for the Run, so the amortised cost per allocated byte is
  `9 + 8H / MaxMemory`. A host binding a graph far larger than the budget can skip it:
  values the host marks with a new `mzs.Opaque` wrapper are treated as leaves.
* The walk is deterministic (it depends on the script's values, not on the Go GC), so a
  Run that fails the budget fails it at the same step every time (D15).

The estimate ignores Go allocator overhead and garbage not yet collected; real RSS can be
a small multiple of `MaxMemory`. That is acceptable: the purpose is to turn "unbounded"
into "bounded", and the doc says *estimate* in the field's first sentence.

### Reporting

```
-e:2:22: limit: memory budget exceeded (64 MiB live)
```

`Kind == "limit"`, not catchable, `ensure` does not run (§8.11) — identical to the step
budget. `Result` gains `PeakMemory int64` (the highest corrected value), and `--stats`
prints it.

### Interaction with existing limits

`MaxCollection` and `MaxStringBytes` stay: they bound a single operation *before* it
allocates, which the budget cannot (a `"x" * 1e12` must be refused, not attempted).
`io.read` and `http` bodies are charged like any string; their own size errors (kind `io`
/ `http`, catchable, §14.2) fire first because 8 MiB < 64 MiB.

## Compatibility

A new `Options` field is additive (§21.1) and "keeps the zero value meaning the safe
answer" — which is the point: the zero value now *has* a memory bound. That is a behaviour
change for a program that legitimately holds more than 64 MiB under default options. It
is a deliberate change under §21.4 and goes in the changelog with the flag that restores
the old behaviour (`--mem 0`, `MaxMemory: -1`). The CLI may reasonably default higher
(256 MiB) than the library.

## Alternatives

* **`runtime/debug.SetMemoryLimit`.** Process-wide and soft: it makes the GC work harder
  and then lets the process die. It cannot attribute or stop one Run.
* **Cumulative allocation budget** (no walk). One line of code per allocation site and
  fully deterministic, but wrong for streaming and serving, the two things `seq` and
  `http.serve` were built for. Could be offered *in addition* (`AllocBudget`) for hosts
  running short conditions, where it is the tighter and cheaper bound.
* **Reference counting.** Exact and immediate, but touches every value copy in a
  tree-walker and breaks on cycles (`d["self"] = d`).
* **Run each script in a subprocess.** The thing G2 exists to remove.

## Open questions

* Default: 64 MiB is a guess. The morzebot corpus should be measured first.
* Should `http.serve` reset or scope the budget per request? A handler leak is a real bug
  the budget should catch; a per-request sub-budget is a possible later addition.
* Values that reach a host function (`Register`ed) and are retained by Go code are
  invisible to the walk. Document it; the host owns what it keeps.

## Implementation

`mzs/limits.go` (counter, walk), `mzs/value.go` (size of each kind), allocation sites in
`array.go`, `dict.go`/`odict.go`, `str.go`, `seq.go`, `async.go` (task roots),
`mzs/options.go`, `cmd/mzs/main.go` (`--mem`, `--stats`), SPEC §14.2, README limits table,
`docs/reference/sandbox.md`. A regression test with the program above, and a benchmark
proving the A4 numbers do not move when the budget is never approached.

## As landed

SPEC §14.2 is now the normative text; this section records where the implementation
departs from the design above, and why.

* **Charging is a step allowance plus exact charges, not a charge at every allocation
  site.** Every interpreter step is charged 64 bytes, folded in at the §14.1 sampling
  interval. The stdlib already charges steps in proportion to its work — an element per
  step, 64 bytes of string per step — so the allowance tracks allocation without a hook in
  every row, and a row added later cannot forget to charge. The operations that build a lot
  in a single step are charged their exact size before they allocate: `+` and `*` on strings
  and arrays, `push`, `concat`, `insert`, `unshift`, a range made into an array, writing past
  an array's end, interpolation, `replace`, padding, `json`, `seq.array`. Over-charging is
  safe by construction: the walk replaces the total with what is live.
* **Frames are not charged on entry.** They are found by the walk instead. Making that
  sound needed an explicit root set, since a Go-level tree-walker keeps much of its state
  in Go variables: every `execStmts` roots its scope (and through `parent` every scope it
  can see), every call roots its arguments, and the evaluator and the stdlib rows that call
  back into the script hold what they are building — an array or dict literal, the left
  operand of a binary, a method's receiver, a `for` iterable, a `match` subject, the value
  an `ensure` will return, the output of `map`, `group_by`, a dict's `map`, `seq.array` and
  a `seq.reduce` accumulator. Every live task's roots are walked too. A seq records the
  values its cursors read from (`Seq.refs`), because a Go closure cannot be looked inside.
* **The host baseline is measured by the first walk**, not at the start of the Run, and the
  root stacks come from a pool, so a Run that never approaches the budget allocates
  nothing for it. Measured with `BenchmarkMemoryBudget` (the A4 shape,
  `$s.lower.trim == "оператор"`): the allocation count is unchanged and the time grows by
  roughly 100–250 ns per Run — the bookkeeping of the roots — against SPEC §14.4's 5 µs
  target and A4's 20 µs median.
* **The diagnostic names both numbers**: `memory budget exceeded (64.85 MiB live, the limit
  is 64 MiB)`. The sentinel is `ErrMemory`; exit code 3.
* **Cuts copy**, as designed: `split`, `lines`, `trim*`, `chomp`, `chop` and the texts of
  `captures` and `matches` return a copy unless the piece is the whole source. The other
  cutting rows already built a new string from runes.
* **The CLI default is the library's** (64 MiB), so a script behaves the same under `mzs`
  and in a host that passed zero `Options`.

### Not landed

* `mzs.Opaque`. A host binding a graph far larger than the budget pays for walking it at
  every walk; the wrapper that would make such a value a leaf needs a representation that
  every accessor of `Value` sees through, and is left for a follow-up.
* The two open questions stand: a per-request budget under `http.serve`, and values a
  registered Go function retains (documented as the host's, in
  `docs/reference/sandbox.md`).

