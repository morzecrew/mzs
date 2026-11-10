# RFC 0014 — `test` grows up

**Status:** Draft · **Area:** stdlib, CLI · **Depends on:** 0009 (trace under a failure) · **Audit:** S3, T5

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
