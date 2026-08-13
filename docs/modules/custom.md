# Your own modules

`include … from`, `export`, what a module's value is, and how the CLI resolves a path.

Two files in one directory:

```
# money.mzs — a module: only what it exports is visible outside.
export currency = "EUR"

rate = 100                                    # private: not exported

export fn from_units(units) { units * rate }

fn format(cents) { "${cents / rate}.${(cents % rate).str.rjust(2, "0")} ${currency}" }
export format
```

```
# main.mzs
include money from "./money.mzs"

say(money.keys.json)
say(money.format(money.from_units(15)))
say(defined(money.rate))
say(money.currency)
```

```sh
mzs main.mzs
```

```
["currency","from_units","format"]
15.00 EUR
false
EUR
```

## `export`

Inline in front of an assignment or a `fn`, or later on a line of its own by name. Nothing
else leaves the file.

| Form | Effect |
|---|---|
| `export rate = 20` | exports the binding `rate` |
| `export fn total(xs) { … }` | exports the function |
| `export helper` | exports a binding that already exists |

`export` records the name, not a snapshot: a module whose body is `export limit = 10` then
`limit = 42` hands out `42`. Exporting a name that does not exist is a compile error:

```sh
mzs -e 'include bad from "./bad.mzs"'      # bad.mzs contains: export nope
```

```
…/bad.mzs:1:1: name: cannot export 'nope': it is not defined
```

The error is reported against the module file (its absolute path, elided here), not the
line that included it.

Everything else is private. Reaching for it is a `name` error at run time — `try` catches
it — and `defined` says false:

```sh
mzs -e 'include money from "./money.mzs"; money.rate'
```

```
-e:1:41: name: undefined member 'rate' in module 'money'
  include money from "./money.mzs"; money.rate
                                          ^
```

## The module's value is a dict of its exports

Keys are in declaration order, so `money.keys` is the module's public list. Everything a
dict can do, a module can do:

```
include money from "./money.mzs"
[money.has("format"), money.has("rate"), defined(money.rate)].json   # [true,false,false]
[15, 20].map(money["from_units"]).json                               # [1500,2000]
```

`money.from_units(15)` *calls* the member; `money["from_units"]` hands back the function
itself. A module is never callable: `money(15)` is
`name: 'money' is a module, not a function: call one of its members`.

## One file, two roles

Running `money.mzs` directly ignores its `export`s and just runs the file — it prints
nothing and exits 0. The same file included from `main.mzs` is a module. There is no
separate module header.

## Loaded once per Run

A module runs inside the Run that included it: the same `$variables`, step budget and
deadline, but its own locals — a module reading `$user` sees it, and `defined(secret)` for
a local of the including file is false. A diamond loads the shared file once —
`cart.mzs` and `report.mzs` both include `log.mzs`, which prints on load:

```sh
mzs diamond.mzs
```

```
log.mzs ran
· cart: 2
· report: 3
```

The cache key is the *resolved* path, so two spellings of one file are one module:
`include a from "./log.mzs"; include b from "./sub/../log.mzs"` loads it once and `a == b`.
A second run of the program runs the module again — nothing is cached across runs.

## Cycles and depth

A cycle is a named error, not a hang (paths are absolute in the real message):

```
cyc_a.mzs:1:1: name: include cycle: cyc_b.mzs -> cyc_a.mzs -> cyc_b.mzs
  include b from "./cyc_b.mzs"
  ^
```

Includes nest at most 64 deep; deeper is `limit: include nesting too deep (64)` and exit
code 3.

## Where a path resolves (the CLI loader)

A path is resolved against the file doing the including — `sub/mid.mzs` including
`"./deep.mzs"` finds `sub/deep.mzs` — and then checked against the root: the directory of
the script that was started, or the working directory when the program came from `-e` or a
pipe.

| Path | Result |
|---|---|
| `"./lib.mzs"`, `"./sub/lib.mzs"` | resolved next to the including file |
| `"/etc/hostname"` | `cannot include …: an include path must be relative` |
| `"../mzs"` | `cannot include …: outside the root directory …` |
| `"./nope.mzs"` | `cannot include …: no such file or directory` |

## A host has to supply a loader

`Options.ModuleLoader` is nil by default, and then any path form fails with
`cannot include "./lib.mzs": the host did not enable module loading` (host — the CLI always
installs its own loader). The loader receives the including file and the written path,
returns the resolved name and the source, and decides what may be read at all.

## See also

- [./README.md](./README.md) — `include`, module values, the built-in table
- [../embedding/filesystem.md](../embedding/filesystem.md) — writing a `ModuleLoader`
- [../stdlib/dicts.md](../stdlib/dicts.md) — the dict operations a module value answers to
- [`examples/27_modules_main.mzs`](../../examples/27_modules_main.mzs) — a three-file example
