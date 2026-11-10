# RFC 0017 — The `args` module: declared command-line flags

**Status:** Draft · **Area:** stdlib, CLI · **Depends on:** 0012 (usage errors go to stderr) · **Audit:** S9, B8

## Summary

Two changes that belong together. (1) The CLI stops parsing arguments that come *after*
the script name: they are the script's. (2) A built-in module `args` turns `$ARGV` into a
dict of typed options from a one-dict declaration, and prints `--help` from the same
declaration.

## Motivation

A script's interface is its flags, and today it has none:

```sh
mzs resize.mzs --width 800 in.png
# mzs: unknown flag --width
mzs resize.mzs -- --width 800 in.png      # the only way: $ARGV == ["--width","800","in.png"]
```

The interpreter claims every flag on the command line, wherever it stands, so a script
cannot be run as `tool --flag` — neither directly nor through `#!/usr/bin/env mzs`, where
the author does not control the argument order. And once the raw array arrives, each
script re-implements the loop that walks it, the `--name=value` form, the type
conversion, the "unknown flag" message and the usage text. That loop is where one-off
scripts stop being pleasant.

## Design

### 1. The CLI boundary

**Everything after the first positional argument (the script path) is passed to the
script untouched.** `mzs [mzs flags] script.mzs [anything]`. A `--` before the script is
still accepted and means "no more mzs flags". With `-e` there is no script path, so the
first non-flag argument starts `$ARGV`, as it effectively does now.

This is a break for `mzs script.mzs --json` (interpreter flag written after the script).
It is the convention of every interpreter (`python x.py --help` is x's help), it is
declared under §21.4, and for one MINOR the CLI prints a hint when a script exits 2 and an
argument after the script name spells a known mzs flag.

### 2. The module

```
include args

opt = args.parse({
  width:   {default: 800,   help: "target width in pixels"},
  quality: {default: 0.85,  short: "q"},
  out:     {default: nil,   help: "output file (default: stdout)"},
  force:   {default: false, short: "f"},
  tag:     {default: [],    help: "repeatable"},
}, {usage: "resize [flags] <file>…", min: 1})

opt["width"]     # 800 — an int, because the default is one
opt["rest"]      # ["in.png"] — the positionals
```

| Member | Signature | Semantics |
|---|---|---|
| `parse` | `(spec: dict, opts: dict = {}) -> dict` | parses `$ARGV` (or `opts["argv"]`) against `spec`; returns every flag by name, plus `rest` |
| `usage` | `(spec: dict, opts: dict = {}) -> string` | the help text `--help` prints, for a script that wants to show it itself |

**The spec** maps a flag name to a dict; a bare default is shorthand
(`{width: 800, force: false}`). The **kind of the default decides the conversion** — no
type vocabulary to learn:

| Default | Accepts | Value |
|---|---|---|
| `false` / `true` | `--force`, `--no-force`, `--force=false` | bool |
| an int | `--width 800`, `--width=800` | int, via strict parsing (RFC 0008 rules) |
| a float | | float |
| a string | | string |
| an array | repeated: `--tag a --tag b` | array of strings |
| `nil` | | string, or `nil` when absent |
| `{required: true}` with no default | | string; absence is a usage error |

Flag names use `_` in the spec and `-` on the command line (`dry_run` ↔ `--dry-run`).
`short: "q"` adds `-q`; short bools combine (`-fq`). `--` ends flags. `-h` / `--help` are
always present.

**Failure.** A usage error — unknown flag, bad number, missing value, fewer than `min` or
more than `max` positionals — writes `resize: unknown flag --widht (did you mean
--width?)` and the usage to **stderr** and ends the Run with `exit(2)`; `--help` writes
the usage to stdout and `exit(0)`. Those are the CLI's own exit codes (§15). With
`opts["exit"] = false`, `parse` raises kind `argument` instead, with `data: {flag, reason}`,
for an embedder or a test.

**Sandbox.** `args` reads `$ARGV`, a host-bound global (§10), and nothing else. No gate.
In an embedding with no `$ARGV` bound, it parses an empty list — defaults come back.

## Compatibility

The module is additive. The CLI boundary is a deliberate break (§21.4) as described; §15's
promise that "`$ARGV` keeps its shape" holds — it is still an array of strings, and it now
receives what it was always meant to.

## Alternatives

* **A declarative header** (`#! flag width int 800`). A second mini-language in comments.
* **`args.flag("width", 800)` called per flag** (Go's `flag`). Order-dependent, cannot
  print help before all calls have run, and needs module state.
* **Leave it to `$ARGV`.** Viable for two flags; the audit's point is that the third one
  is where authors switch to another language.
* **Subcommands.** `opt["rest"][0]` and a `match` do it; no API needed yet.

## Open questions

* Environment fallbacks (`{env: "WIDTH"}`) need `io.env`, hence `Options.Env`. Worth the
  coupling?
* Should the result be a `record`, so that `opt.width` works (RFC 0003)? A record needs a
  declaration; generating one from a run-time dict is a new mechanism. Proposed: plain
  dict, and `{width, out} = opt` via RFC 0002.

## Implementation

`cmd/mzs/main.go` (stop flag parsing at the script path; the transition hint),
`mzs/args.go`, registration in `mzs/module.go`, SPEC §12.8 table, a new §12.x, §15,
`docs/modules/args.md`, `docs/cli/README.md`, apidoc, and an example script with a
shebang.
