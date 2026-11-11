# RFC 0012 — `eprint` and `eprintln`: a second output stream

**Status:** Landed (#PR) · **Area:** stdlib · **Depends on:** — · **Audit:** S1

## Summary

Add `eprint(*args)` and `eprintln(*args)`, which write to `Options.Stderr` exactly as
`print` / `println` write to `Options.Stdout`.

## Motivation

mzs is built for pipelines (`cat access.log | mzs -n -e '…' | sort | uniq -c`). A filter in
a pipeline has one rule: data on stdout, everything else on stderr. Today a script has no
stderr:

```
eprintln("skipping bad row")   # name: undefined function 'eprintln' (did you mean 'println'?)
```

So a warning, a progress line or a "3 rows skipped" summary either corrupts the data
stream or is not printed. `Options.Stderr` already exists — the interpreter sends compile
warnings to it — so the plumbing is there and only the script-side name is missing.
`debug` is not a substitute: it writes the `inspect` form, to stdout.

## Design

| Name | Signature | Semantics |
|---|---|---|
| `eprint` | `eprint(*args) -> nil` | `print`, to `Options.Stderr` |
| `eprintln` | `eprintln(*args) -> nil` | `println`, to `Options.Stderr` (same rules: `\n` after each arg, an array prints one element per line, no args prints one `\n`) |

* A nil `Options.Stderr` discards, as it does for warnings — the zero `Options` stays
  silent and safe (D15). No new field, no new gate.
* The CLI already wires `Stderr` to fd 2. In `mzs test`, a case's stderr is passed
  through unchanged.
* Writes are unbuffered relative to each other on one stream; ordering *between* stdout
  and stderr is the terminal's business.
* Both are ordinary §12.1 rows, so `x.eprintln` works by UFCS (D18), like `x.println`.

Not proposed: a `log` module with levels and timestamps. Levels are a formatting
convention a three-line function gives you (`fn warn(m) { eprintln("warn: ${m}") }`), and
a timestamp needs the clock capability. If structured logging becomes a real need it is
its own RFC and will sit on top of these two rows.

## Compatibility

Additive. Two new names; a local of the same spelling shadows them.

## Alternatives

* **`println(x, to = "stderr")`.** `println` is variadic; a named argument that changes
  the destination is easy to typo into a printed string.
* **`io.stderr.write(…)`.** `io` is gated by `Options.FS`, and writing a diagnostic should
  not require filesystem capability. Also a stream *object* would be a new kind of value.
* **`warn`.** Shorter, but it promises a "warning:" prefix it would not add, and collides
  with the most likely name for a user's own helper.

## Open questions

* Should `debug` move to stderr? It is a debugging aid and arguably always belonged there,
  but SPEC §12.1 says `Options.Stdout` and the change would be a (small) break. Proposed:
  leave it.

## Implementation

`mzs/builtins.go` (two registrations sharing `print`'s body with a different writer),
SPEC §12.1, `docs/stdlib/core.md`, `docs/cli/input.md` (the pipeline idiom), apidoc.

## As landed

SPEC §12.1 is the normative text.

* **Open question — `debug` stays on stdout**, as proposed: moving it would break a program
  that reads its own `debug` lines from stdout, for a convenience nobody asked for yet.
* `print`/`eprint` and `println`/`eprintln` share one body each and differ only in the
  writer, so the array and no-argument rules cannot drift between the two streams. A write
  that fails is the same `io` error on either stream.
* No new `Options` field and no gate: `Options.Stderr` already existed for warnings, and a
  nil one discards (D15).

