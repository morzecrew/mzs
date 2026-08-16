# mzs for VS Code

Syntax highlighting, completion, hovers and diagnostics for the mzs language (the language
itself is described in `README.md` at the root of the repository, the specification is
`SPEC.md`).

The extension is plain CommonJS **with no dependencies and no build step** — dropping it
into the extensions folder is enough, `npm install` is not needed.

## Installing

```sh
# 1. build the interpreter, if it is not there yet
cd /home/alexander/go-test-project/mzs
go build -o ~/bin/mzs ./cmd/mzs

# 2. link the extension in (edits show up straight away)
ln -s "$PWD/editors/vscode" ~/.vscode/extensions/mzs-1.1.0
```

Restart VS Code. If `mzs` is not on the `PATH`, name the path in the `mzs.path` setting.

To install from a VSIX (needs `npx @vscode/vsce`, and the network):

```sh
cd editors/vscode && npx @vscode/vsce package
code --install-extension mzs-1.1.0.vsix
```

## What it does

| Feature | How it works |
|---|---|
| **Highlighting** | a TextMate grammar following SPEC §3: strings with `${}` interpolation and `$globals` inside them, raw `'…'` strings, `<<~TAG` heredocs and their raw form, regexes `/…/imxsu` with their anchors, quantifiers and classes, numbers in every base, dict keys `{a: 1}`, closure parameters `(x) ->`, `include`/`export`, `record Name(…)`, module names, `#` comments |
| **Completion** | after `include ` — the built-in modules, each marked with the flag it needs; after `module.` — its members (for a neighbouring `.mzs`, the names it really exports); after `.` — the receiver's methods (the type is inferred from the literal on the left); otherwise — built-in functions, keywords, the modules already included, plus the functions, local variables and `$globals` of the file itself |
| **Hovers** | the signature, the description and the example from SPEC §12; for a module member, also what has to be `include`d (and with which flag) |
| **Diagnostics** | `mzs --check -` as you type: the buffer goes in through stdin, so errors are visible **before the file is saved**. Warnings show up as warnings, errors as errors |
| **Commands** | `mzs: Run file`, `mzs: Evaluate selection as a one-liner` (<kbd>Ctrl+Shift+E</kbd>), `mzs: Show the token stream`, `mzs: Show the AST` |
| **File icon** | `.mzs` gets an icon of its own in the explorer — a light and a dark variant of the logo (`icons/mzs-*.svg`), plus the **mzs (Seti)** icon theme: the whole of Seti, with `.mzs` carrying the logo |
| **Snippets** | `inc`, `incfrom`, `expfn`, `exp`, `record`, `exrec`, `matchrec`, `fn`, `cl`, `heredoc`, `heredocraw`, `if`, `match`, `for`, `try`, `tryb`, `ensure`, `map`, `filter`, `each`, `reduce`, `serve` — a web server — and `cond=`, `cond~`, `cond?`, skeletons for conditions in the morzebot style |

## Settings

| Key | Default | Meaning |
|---|---|---|
| `mzs.path` | `mzs` | path to the executable |
| `mzs.time` | `true` | check and run with `--time` (without it, `include time`/`include date` is an error) |
| `mzs.diagnostics.enable` | `true` | turn checking on |
| `mzs.diagnostics.delay` | `300` | delay after typing, in ms |
| `mzs.completion.enable` | `true` | turn completion on |

`mzs.time` is on so that a file is checked the way it is going to be run. Turn it off to see
the file through the eyes of an embedding host that does not grant that capability. The
`http` module needs no flag — it is always there.

## About the icon

The icon comes in two forms, and they are two different VS Code mechanisms.

**1. The language icon** (`contributes.languages[].icon`) works with your current icon theme,
if that theme supports language icons and knows nothing about `.mzs` itself. That is how the
default theme (Seti) behaves. If you run, say, Material Icon Theme, it wins and draws its own
"unnamed" icon — a VS Code limitation, not the extension's.

**2. The "mzs (Seti)" icon theme** — for when the icon has to be there always:

```
Ctrl+Shift+P → Preferences: File Icon Theme → mzs (Seti)
```

This is VS Code's own Seti theme in full (the same icons for 233 file extensions, the same
`seti.woff`) plus one entry: `.mzs` is drawn with the logo, separately for the dark and the
light theme. VS Code icon themes have no inheritance — a theme supplies either the whole
table or nothing — so the table is copied, not inherited. A script makes the copy, not hands:

```sh
node editors/vscode/icons/theme/build.js
# or, if VS Code lives somewhere non-standard:
node editors/vscode/icons/theme/build.js /path/to/vscode/resources/app
```

Run it after a VS Code update to pick up the new Seti icons.
Seti is distributed under MIT (Microsoft's packaging on top of MIT jesseweed/seti-ui);
`icons/theme/ThirdPartyNotices.txt` is copied along with it.

`icons/logo-128.png` is the extension's gallery icon (only PNG is allowed there).

## Where the hints come from

`data/api.json` is **generated**, not written by hand: the list of methods is taken from the
interpreter's live registry (`mzs.MethodNames`/`BuiltinNames`), and the descriptions are
parsed out of the SPEC.md §12 tables. That is why completion cannot offer a method the
implementation does not have.

Description coverage is 290 of 304 entries (95%). The other 14 exist in the code but are not
described in the SPEC §12 tables (`bool.&`, `range.step`, the methods of a `time` value) —
they are still completed, but on hover they say honestly that the specification has no
description for them.

To rebuild the data after a change to the stdlib, see `editors/vscode/data/README.md`.

## Limits

* A receiver's type is inferred only from the literal to the left of the dot. For a variable
  (`s.` where `s = "..."`) the union of every type's methods is offered — a longer list than
  you need, but one that never lies about a method existing. Modules are the exception: they
  are known exactly, because it is `include` in this very file that brings them in.
* A neighbouring module's exports are read out of its text with a regex rather than parsed:
  an `export` inside a string or a comment will end up in the list. This does not affect the
  diagnostics — those come from `mzs --check` itself.
* The grammar tells a regex from a division by the same rule the lexer uses (SPEC §3.8), but
  by TextMate means — in rare expressions such as `a /b/ c` the highlighting may get it wrong
  where the interpreter itself parses it right.
* There is no "go to definition" and no rename: that needs a language server, not a
  highlighter. If it turns out to be wanted, that is the next step — the interpreter already
  hands out the AST through `mzs --ast`.
