# Verification

How the language is checked: four commands, one acceptance suite, a coverage profile, and
the CI that runs all of it.

## The four checks

```sh
gofmt -l .                    # prints nothing
go vet ./...                  # prints nothing
go test -race ./...           # ok  mzs  …  for every package
go test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
```

These are the four steps of the `test and coverage` job. Compiling the examples is the
separate `cli smoke` job, and worth running by hand for anything that touches the grammar:

```sh
for f in examples/*.mzs; do mzs --check --time "$f"; done
```

`--check` compiles and stops: `<file>: ok` when it compiles, the diagnostics and exit `1`
when it does not.

```sh
mzs --check --time examples/05_arrays_pipeline.mzs
# examples/05_arrays_pipeline.mzs: ok
```

`--time` is needed because `--check` resolves modules at compile time and one example,
`29_time_scheduling.mzs`, includes the clock-gated modules ([sandbox](./sandbox.md)):

```sh
mzs --check examples/29_time_scheduling.mzs
# examples/29_time_scheduling.mzs:10:9: name: module 'time' needs a clock: the host did not set EnableTime (mzs --time)
# examples/29_time_scheduling.mzs:11:9: name: module 'date' needs a clock: the host did not set EnableTime (mzs --time)
```

One file per invocation — the CLI takes a single program and turns the rest of the arguments
into `$ARGV`.

## The two checks written in mzs

The `cli smoke` job also runs the tests that are written in the language itself, and the one
property the formatter is built around:

```sh
mzs test --time examples/     # 27_lib_money_test.mzs, case by case
```

```sh
set -e
for f in examples/*.mzs; do
  mzs fmt "$f" > /tmp/once.mzs
  mzs fmt /tmp/once.mzs > /tmp/twice.mzs
  cmp -s /tmp/once.mzs /tmp/twice.mzs || { echo "mzs fmt is not idempotent on $f"; exit 1; }
done
```

`fmt(fmt(x)) == fmt(x)` is asserted in Go as well (`internal/printer`), together with the
two promises every run of `mzs fmt` makes for itself: the formatted text parses to the same
tree, and it carries the same comments. A run that cannot promise both refuses and leaves
the file alone — see [../cli/tools.md](../cli/tools.md).

## The acceptance suite

`corpus_test.go` is the project's definition of done: the normative corpus of `SPEC.md` §16,
transcribed row for row and run through the public Go API rather than through the CLI.

| Test | What it pins |
|---|---|
| `TestCorpusConditions` | 59 condition-shaped one-liners: comparison, `.int`, interpolation, `match`, `??`, `dig` |
| `TestCorpusRegexes` | 14 patterns must compile and match |
| `TestCorpusRegexBehaviour` | Unicode `i` folding, Unicode `\b`, line anchors, rune `index`, lookahead |
| `TestRegexBackendAgreement` | RE2 and the backtracking backend agree over a 500-string sample |
| `TestAuthorFiles` | the §11.5 `\\b` gotcha, the s.md notes, and 29 shipped examples run end to end |
| `TestDiagnostics` | every fix-it message, with its line and column |
| `TestTruthyZero` … `TestBitOpsStayInt` | one named test per documented trap |
| `TestTimeout`, `TestStepBudget` | the limits fire, with their sentinel errors |
| `TestNoHostPanic` | 10 000 random byte strings: `Compile` + `Run` never panic |
| `TestIsolation` | 200 concurrent `Run`s of one `*Program` never see each other's `$vars` |
| `TestHostGrantsTheFilesystem` | `include io` fails under the zero `Options`, reads under a host `FS` |

The corpus is the acceptance material for the language, not a data set: every row is an
expression whose value the specification fixes.

Around it are the per-area unit suites (`str_test.go`, `array_test.go`, `dict_test.go`,
`num_test.go`, `http_test.go`, `io_test.go`, `async_test.go`, `module_test.go`, `test_test.go`,
the parser, lexer, printer and language-server suites, and `cmd/mzs/main_test.go` for the
CLI). Together:

```sh
go test ./... -list '.*' | grep -c '^Test'   # 665
```

Every example is also exercised somewhere: 29 through the corpus suite, the rest through
`cmd/mzs/main_test.go` and the parser suite. `examples/27_lib_money.mzs` and
`examples/27_lib_text.mzs` are libraries, reached through `examples/27_modules_main.mzs`.

## One version string

```sh
mzs --version      # mzs 0.1.0
```

The `cli smoke` job also asserts that this string, the newest released section of
`CHANGELOG.md` and the line `README.md` prints as the command's output are the same version.
Work that has not been released yet lives under `## Unreleased`, which the check skips on
purpose: the assertion is about the version that shipped ([compatibility](./compatibility.md),
[SPEC §21.5](../../SPEC.md#21-stability-and-versioning)).

## Coverage

```sh
go test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
# total:  (statements)  90.9%
go tool cover -html=coverage.out
```

`-coverpkg=./...` credits the library for what the other packages' tests exercise in it.
`codecov.yml` sets the rules: the project target is `auto` with a 1% threshold, new code
must come in at 80%, and `editors/` and `examples/` are ignored.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every pull request, with two
jobs:

| Job | Steps |
|---|---|
| `test and coverage` | `gofmt -l`, `go vet`, `go test -race`, the coverage profile, the Codecov upload |
| `cli smoke` | builds `cmd/mzs`, then `mzs --check --time` on each `examples/*.mzs`, the version check, `mzs test examples/`, and `mzs fmt` idempotence |

Notes that matter when a run goes red:

- Coverage runs **without** `-race`: the corpus gives each example a 2 s deadline, and race
  detection on top of coverage instrumentation can trip it on a shared runner.
- The Go version comes from `go.mod`; the module cache is disabled because there are no
  dependencies to cache.
- Without a `CODECOV_TOKEN` the upload step annotates instead of failing, so a fork stays
  green. The coverage number is written to the job summary either way.

## See also

- [Sandbox and limits](./sandbox.md)
- [Compatibility and versions](./compatibility.md)
- [Limitations and reserved syntax](./limitations.md)
- [Diagnostics and `--check`](../cli/diagnostics.md)
