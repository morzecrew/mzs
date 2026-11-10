# RFC 0020 — A library manifest and `mzs get`

**Status:** Draft · **Area:** tools · **Depends on:** — · **Audit:** T6

## Summary

`$MZSPATH` (SPEC §15) lets `include money from "money"` find a library that is already on
the machine. Nothing says *which* library a project means, or puts it there. This RFC adds
a project file, `mzs.lock`, listing each library by name, URL and content hash, and a
subcommand, `mzs get`, that downloads and verifies them into `./.mzs/lib`. There is no
version resolution, no registry, and no change to the language or the library API.

## Motivation

After T19 a library is "a directory on `$MZSPATH`". For a second machine — a colleague,
CI, a container — the instructions are prose: clone this repository at that commit into
`~/.mzs/lib/money`. The failure mode is the classic one: the script runs against whatever
`money` happens to be installed, which is how two machines disagree about a total.

The repository's own stance makes the usual solutions unavailable, and that is a feature:
no dependencies (I1), no process spawning (so no shelling out to `git`), and a CLI that
confines an include to the tree it was found in. A design has to fit inside those.

## Design

### The file

`mzs.lock`, in the project root (the directory of the script being run, or the nearest
parent that has one). One library per line, three fields:

```
# name    source                                                        sha256
money     https://example.org/mzs/money-1.4.0.tar.gz                    9f2c…e1
text      https://codeload.github.com/acme/mzs-text/tar.gz/v0.3.1       51ab…07
```

* **name** — what `include x from "name"` asks for. Lowercase, as module names are (§12.8).
* **source** — an `https://` URL of a `.tar.gz` or `.zip` archive, or a `./relative/path`
  to a directory inside the project (a vendored library; no hash needed, written `-`).
* **sha256** — of the archive bytes. It is the identity: a URL that starts serving
  different bytes is an error, not an update.

A line format rather than JSON because it diffs one library per line and merges cleanly.
Comments are `#`.

### The command

| Command | Does |
|---|---|
| `mzs get` | for every line: if `./.mzs/lib/<name>/` is absent or its recorded hash differs, download, verify, unpack |
| `mzs get <name> <url>` | download, compute the hash, unpack, and **add or replace** the line. This is how a library is added or upgraded — by a human stating the new URL |
| `mzs get --check` | verify that `./.mzs/lib` matches the file; exit 1 if not. For CI |
| `mzs get --offline` | never touch the network; fail if anything is missing |

Unpacking: a single top-level directory in the archive is stripped (what GitHub and most
release archives produce). The result must contain `main.mzs` or `<name>.mzs`, the two
layouts §15 already defines. Entries that would escape the destination (`..`, absolute
paths, symlinks pointing out) abort the unpack — the same rule the include confinement
enforces at read time. Size is capped (64 MiB per archive, overridable).

Downloading uses `net/http`, `archive/tar`, `archive/zip`, `compress/gzip`,
`crypto/sha256`: all standard library. No `git`, no subprocess.

### Resolution

One rule added to the search of §15: **`./.mzs/lib` of the project root is searched
first**, before `$MZSPATH`. Each library found there is rooted in its own directory,
exactly as a library found on `$MZSPATH` is, so a fetched library cannot read the project
that fetched it.

When a project has an `mzs.lock` and a name it lists is missing from `./.mzs/lib`, the
include error says so — `library 'money' is listed in mzs.lock but not fetched; run 'mzs
get'` — instead of silently falling through to a different `money` on `$MZSPATH`. That is
the existing principle "a library that is there and cannot be read is an error, not an
absence" applied one step earlier.

### Transitive libraries

A library may ship its own `mzs.lock`. `mzs get` reads it and fetches those into the
**project's** `./.mzs/lib` (flat). Two lines for one name with different hashes are an
error that names both requesters; the project resolves it by listing that name itself,
which wins. No semver, no solver: with content hashes there is nothing to solve, only
conflicts to show.

### What is not in scope

* **The library and embedders.** `Options.ModuleLoader` is untouched. Fetching is a CLI
  tool; a host keeps full control of what `include … from` means (§12.8).
* **A registry, names without URLs, version ranges, publishing.** A registry is
  infrastructure and governance, not a language feature; the file format leaves room
  (a future `source` scheme) without committing to one.
* **Running anything at install time.** No hooks. A library is source files.

## Compatibility

Additive: a new subcommand, a new file, one directory searched ahead of `$MZSPATH`. A
project that happens to have a `./.mzs/lib` directory today starts having it searched —
unlikely, and documented in the changelog. `.gitignore` templates should list `.mzs/`.

## Alternatives

* **Vendoring only** (commit the library into the repo, use `./vendor/x/main.mzs`). Works
  today with a relative path and needs nothing; it is the right answer for many projects,
  which is why `source` may be a local path. It does not help with upgrades or with size.
* **Git URLs** (`github.com/acme/text@v0.3.1`, Go-style). Needs either a git
  implementation or a hosting-specific archive URL scheme baked into the tool. Plain
  archive URLs are what those schemes resolve to anyway.
* **A global cache** (`~/.mzs/cache`) with links into projects. Faster repeat installs;
  the include confinement refuses symlinks that leave the tree, so it would have to copy.
  A later optimisation.
* **Semantic versions and a solver.** The cost centre of every package manager. Pins plus
  human-initiated upgrades cover a young ecosystem.

## Open questions

* File name: `mzs.lock` suggests a generated companion to a manifest that does not exist.
  `mzs.libs`? `mzs.mod`?
* Signature verification beyond a hash (a publisher key) — out until there are publishers.
* Should `mzs test` and `mzs lsp` run `mzs get --check` implicitly and warn?
* Proxy and private sources: honour `HTTPS_PROXY` (Go does by default) and `~/.netrc`?

## Implementation

New `cmd/mzs/get.go` (parser for the file, fetch, verify, safe unpack), `cmd/mzs/include.go`
(project root discovery, the extra search directory, the "listed but not fetched" error),
SPEC §15, `docs/modules/custom.md`, `docs/cli/tools.md`, tests against an
`httptest.Server` serving fixture archives, including the hostile ones (path traversal,
hash mismatch, oversized).
