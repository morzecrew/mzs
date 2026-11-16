# RFC 0014 — `test` grows up

**Status:** Landed (#29) · **Area:** stdlib, CLI · **Depends on:** 0009 (trace under a failure) · **Audit:** S3, T5

## Summary

The `test` module has two members, `case` and `eq`. Add the five every test file reaches
for next — `ne`, `near`, `raises`, `skip`, `group` — and four runner flags: `--filter`,
`--fail-fast`, `--json`, and `--now` for a fixed clock.

## Motivation

```
include test
test.keys                                  # ["case", "eq"]
test.case("rejects") { test.raises { parse("{") } }
# FAIL rejects
#      undefined member 'raises' in module 'test'
```

mzs treats errors as values with *kinds* precisely so that programs branch on them
(§8.11) — and the test module cannot assert that an error of a given kind is raised. The
idiom today:

```
test.case("rejects") {
  e = try { parse("{"); nil } else (e) { e }
  assert(e != nil, "expected an error")
  test.eq(e["kind"], "json")
}
```

Four lines of scaffolding per error-path test means error paths do not get tested. The
other gaps are of the same kind: floats cannot be compared (`0.1 + 0.2` is not `0.3`), a
test cannot be skipped with a reason, forty cases in a file are one flat list, and the
runner can neither run one of them nor report to CI in a format CI reads.

## Design

### Module members

| Member | Signature | Semantics |
|---|---|---|
| `ne` | `(actual, unexpected) -> nil` | raises when the two are `==`: `expected a value other than 3` |
| `near` | `(actual: number, expected: number, eps: number = 1e-9) -> nil` | raises unless `abs(actual - expected) <= eps`. NaN is never near anything |
| `raises` | `(kind: string = nil) { … } -> dict` | runs the block; raises if it completes (`expected an error, got 42`) or if `kind` is given and differs (`expected kind "json", got "type": <message>`). **Returns the error dict**, so the test can go on: `test.eq(test.raises("billing") { pay(o) }["data"]["short_by"], 30)` |
| `skip` | `(reason: string) -> never` | ends the current case as *skipped*, not failed. Outside a case it skips the rest of the file |
| `group` | `(name: string) { … } -> nil` | cases inside are reported as `name › case`; groups nest. A group is naming, not isolation: no setup hooks |

`raises` catches what `try` catches and nothing more (§8.11): a limit still ends the file.

No `before`/`after`. A file is a program: shared setup is a function the cases call, and
cleanup is `ensure` — both exist and neither needs a second lifecycle. No mocking API
either; the sandbox already *is* dependency injection (clock, RNG, FS, stdin are options),
which is what `--now` and `--rand <seed>` expose to the runner.

### The report

`Result.Tests` entries gain `Status` (`"pass" | "fail" | "skip"`), `Group []string` and
`Reason`. Human output:

```
money_test.mzs
ok   rounding › half up
skip rounding › bankers        (not implemented)
FAIL totals › with discount
     expected 30.00, got 29.99 (money_test.mzs:7:5)
       in total, called from money_test.mzs:6:12

1 file, 3 cases, 1 failed, 1 skipped  (0s)
```

### Runner flags

| Flag | Meaning |
|---|---|
| `--filter <regex>` | run only cases whose full name (`group › case`) matches. A non-matching case's block is **not executed** and it is not counted. File-level code still runs — it is how cases get declared |
| `--fail-fast` | stop after the first failed case; exit 1 |
| `--json` | one JSON object per line on stdout, with the keys `file`, `name`, `group`, `status`, `message`, `line`, `col`, `ms` — e.g. `{"file":"money_test.mzs","name":"half up","group":["rounding"],"status":"pass","message":null,"line":4,"col":1,"ms":0}` — then a summary object. Human lines are suppressed |
| `--now <rfc3339>` | implies `--time` with a **fixed** clock, so date logic is testable deterministically (D15) |
| `-v` | also print each case's captured stdout under it (today it is interleaved) |

Exit codes are unchanged: `0` all passed (skips do not fail), `1` a failure, `2` usage,
`3` a limit ended a file.

## Compatibility

Additive: five new members (a name that exists keeps its meaning, §21.1), new flags, new
fields on a Go struct. The summary line gains ", N skipped" only when N > 0; diagnostic
text is not stable anyway (§21.2). `test.case` still returns a bool — `true` for a skip.

## Alternatives

* **`assert_*` globals** instead of module members. Pollutes the flat namespace (D18) with
  names only test files use.
* **`test.raises` returning a bool.** Loses the error, forcing the `try` scaffolding back
  for any assertion about `message` or `data`.
* **Snapshot testing, parametrised `test.each`.** Both useful; both expressible today
  (`for row in rows { test.case("…${row}") { … } }`). Wait for demand.
* **Parallel files.** Each file is already its own Run; running them concurrently is a
  runner-only change with no language surface. Worth doing, not worth specifying here.

## Open questions

* Should `--filter` also match file names?
* `test.near` for arrays of floats / Decimals? Decimal equality is exact, so `eq` is right
  for those.
* Coverage (`mzs test --cover`) is specified in RFC 0019, since it needs interpreter
  support shared with the profiler.

## Implementation

`mzs/test.go` (members, status, groups on the Run), `cmd/mzs/test.go` (flags, JSON
reporter, filter passed through `Options`), SPEC §12.18 and §15.2, `docs/modules/test.md`,
`docs/cli/tools.md`, apidoc, and fixing the `--help` example (audit B2) while there.

## As landed

Landed in #29 as designed: the five members, the four runner flags and `-v`, and the
richer `Result.Tests`. SPEC §12.18 and §15.2 (with §13.2 and §13.5 for the Go API) are the
normative text, `docs/modules/test.md` and `docs/cli/tools.md` the guides, and
`examples/27_lib_money_test.mzs` uses a group and `test.raises`.

* **Open question 1 — `--filter` matches case names only.** Files are chosen by the paths on
  the command line, which already select a directory or a single file; a pattern that
  matched both would make `--filter money` mean two different things at once.
* **Open question 2 — `near` takes numbers only.** A Decimal is refused with the ordinary
  type error: Decimal arithmetic is exact, so `eq` is its comparison. Arrays of floats are
  left until there is demand; `for` over pairs with `near` covers them today.
* **Open question 3 — coverage** stays with RFC 0019, as the RFC says.
* **Equal values are near whatever `eps` is.** That is what makes an infinity near itself,
  where `abs(inf - inf)` is NaN. NaN is near nothing, itself included, and a negative or NaN
  `eps` is an `argument` error.
* **A skip runs `ensure`; the fail-fast stop does not.** A skip leaves its block the way
  `return` does, so what the case acquired is released. The stop that fail-fast starts once
  a failure is recorded ends the Run and, like a limit, runs no more script code. The
  failure itself is an ordinary error, so an `ensure` inside the case around the failing
  line runs before the case is recorded, as for any error. Neither signal is catchable by
  `try`.
* **A filtered-out case answers `nil`**, not `true` or `false`: it has neither passed nor
  failed.
* **`line` and `col` in `--json` are the case's declaration**, as the RFC's example shows;
  a failure's own position is in `message`, exactly as the human report prints it.
* **Under `--json` the scripts' own output goes to stderr**, as it happens, rather than
  being dropped: stdout is the stream, and nothing a script prints is lost.
* **Output capture is the CLI's default.** The `-v` row says a case's output is printed
  under it; without `-v`, a failed case's output is still printed under it — it is the
  evidence — and a passing case's is not. Output outside every case is printed as it
  happens. For an embedder, all of this is opt-in through `Options.Test.Capture`.
* **`--fail-fast` also stops at a file that did not finish**, which is as red as a failed
  case.
* **Exit codes are unchanged**, including that a limit ending a file is `1`, as it was
  before this RFC; the `3` in the Design table is not a code `mzs test` has used.
* **B2 (the `--help` example)** was fixed in #17; the example now also shows a group and
  `test.raises`, and still runs as a test.
