# RFC 0011 — An aggregate memory budget

**Status:** Draft · **Area:** embedding, sandbox · **Depends on:** — · **Audit:** §3.1

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
| string | its byte length, when built by an operation (not for literals, which belong to the `*Program`) |
| array | capacity × word size of a `Value`, on allocation and on each growth |
| dict | entries × (key + value + bucket overhead), on growth |
| closure / env | a fixed size per frame that outlives its call |
| seq, task, time, regex | fixed |
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
* So the counter is an upper bound that is corrected lazily. A program that stays under
  the budget never walks. One that churns walks at most once per `MaxMemory` bytes
  allocated, so the amortised cost is O(1) per allocated byte.
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
