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

println(money.keys.json)
println(money.format(money.from_units(15)))
println(defined(money.rate))
println(money.currency)
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

## Two path forms: a file, or a library

The leading `./` — or `../` — is the whole of the difference, and it is the difference between
a file the program carries and a library it only names.

```
include cart  from "./cart.mzs"       # a file: beside the one doing the including
include log   from "../shared/log.mzs" # …still a file: `../` is the file form too
include money from "money"            # a library: on the search path
```

A **file** path is resolved against the file doing the including — `sub/mid.mzs` including
`"./deep.mzs"` finds `sub/deep.mzs` — and travels with the program, which is what makes a
two-file script movable.

A **library** name is not resolved against anything the program can see: it is looked for on
`$MZSPATH`, so the same `include money from "money"` works from every project on the machine.
That is the thing a relative path cannot express without naming where the other project
happens to sit today.

## The search path

`$MZSPATH` is a list of directories, `:`-separated (`;` on Windows). Unset, the search path
is `~/.mzs/lib`:

```sh
mzs report.mzs                                   # looks in ~/.mzs/lib
MZSPATH=/opt/team/mzs mzs report.mzs             # …and not in ~/.mzs/lib: MZSPATH replaces it
MZSPATH=/opt/team/mzs:$HOME/.mzs/lib mzs report.mzs   # both, in that order
MZSPATH=vendor mzs report.mzs                    # relative to the working directory
```

The first directory that holds the name wins. Empty entries are dropped rather than read as
`.` — a trailing `:` does not quietly make the working directory a library directory, and
`./` is how a script says that.

## Two layouts for a library

In each search directory, two spellings are tried in this order — so a library that outgrew
one file keeps the name its users already wrote:

```
~/.mzs/lib/money.mzs               include money from "money"
~/.mzs/lib/money/main.mzs          …the same include, once money is a directory
~/.mzs/lib/shop/cart.mzs           include cart from "shop/cart"
```

Inside `money/main.mzs`, its own helpers are ordinary file includes: `include cents from
"./cents.mzs"` finds `money/cents.mzs`. A library may also include another library by name.

## Every file is rooted where it was found

A path may not leave the tree the file belongs to. For a script that tree is the directory of
the program that was run (the working directory for `-e` and a pipe); for a library it is the
search directory it was found under. So a vendored library under `vendor/` cannot read the
project that vendored it, and a library included from anywhere is confined the same way
wherever it was included from.

**A symbolic link is a path too.** The check is made by the read rather than by the spelling,
so a link is followed while it stays inside the tree, and one that leads out of it is refused
exactly like a written `../` that leaves. An absolute link is refused with it — the tree cannot
see where it goes, which is why an include path may not be absolute either. To use a library
that lives somewhere else, put *its* directory on `$MZSPATH` rather than linking it into one
that is already there:

```sh
ln -s ~/dev/money/money.mzs ~/.mzs/lib/money.mzs   # refused: the link leads out of ~/.mzs/lib
MZSPATH=~/dev/money:~/.mzs/lib mzs report.mzs      # …this is the way to say it
```

| Path | Result |
|---|---|
| `"./lib.mzs"`, `"./sub/lib.mzs"` | resolved next to the including file |
| `"money"`, `"shop/cart"` | the first hit on `$MZSPATH` |
| `"/etc/hostname"` | `cannot include …: an include path must be relative` |
| `"../mzs"` | `cannot include …: outside the root directory …` |
| `"./nope.mzs"` | `cannot include …: no such file or directory` |
| `"./link.mzs"` (a link out of the tree, or an absolute one) | `cannot include …: outside the root directory …` |
| `"nope"` | `cannot include …: no such library: looked for nope.mzs and nope/main.mzs in …` |
| `"money.mzs"` | `a library name carries no .mzs: write "money", or "./money.mzs" for the file next to this one` |
| `"shop/../money"` | `a library name cannot contain '..'; "./shop/../money" is the form that may` |

A library that is not found says where it looked, which is the one thing the source line
cannot say:

```sh
MZSPATH=/opt/team/mzs mzs -e 'include money from "money"'
```

```
-e:1:1: name: cannot include "money": no such library: looked for money.mzs and money/main.mzs in /opt/team/mzs
  include money from "money"
  ^
```

## A host has to supply a loader

`Options.ModuleLoader` is nil by default, and then any path form fails with
`cannot include "./lib.mzs": the host did not enable module loading` (host — the CLI always
installs its own loader). The loader receives the including file and the written path,
returns the resolved name and the source, and decides what may be read at all.

## See also

- [./README.md](./README.md) — `include`, module values, the built-in table
- [../embedding/filesystem.md](../embedding/filesystem.md) — writing a `ModuleLoader`
- [../stdlib/dicts.md](../stdlib/dicts.md) — the dict operations a module value answers to
- [../cli/README.md](../cli/README.md) — the CLI's flags and the `MZSPATH` environment
- [`examples/27_modules_main.mzs`](../../examples/27_modules_main.mzs) — a three-file example
