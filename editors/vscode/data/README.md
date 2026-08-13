# data/api.json — a generated file

Do not edit it by hand. The sources are:

* **the names** — the interpreter's live registry (`mzs.MethodNames(kind)`,
  `mzs.BuiltinNames()`, `mzs.ModuleNames`/`LookupModule`, `mzs.Keywords()`), which is why the
  extension cannot offer a method, a module or a keyword that does not exist;
* **the gates** — which flag a module needs (`time` → `--time`, `http` → nothing), found by
  probing the registry under different `Options` rather than from a list in the code;
* **the descriptions** — the SPEC.md §12 tables (signature, semantics, example), §12.8 and
  §12.11 included.

Rebuilding after a change to the stdlib (needs Go and Node, no network):

```sh
cd editors/vscode/data/gen
go run . > registry.json && node parse.js
```

`parse.js` prints the coverage and the list of names SPEC §12 has no row for — a handy way to
notice the implementation and the specification drifting apart. If the coverage suddenly
drops several-fold, a section heading has most likely been renamed: the "§12.N → receiver"
mapping in `SECTION_RECV` is keyed by number rather than by the heading text, but a new
section still has to be added there by hand.
