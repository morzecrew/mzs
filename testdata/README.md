# testdata

The author's own files of SPEC §16.3, kept verbatim because §16 is normative: `main.mzs`
is the migrated program whose value is `3` and whose `test` returns `nil` below the length
guard, and `one.mzs` is the deliberate `=!` typo that MUST produce

    one.mzs:3:6: syntax: unexpected '!' after '='; did you mean '!='?

They are fixtures, not examples — teaching programs live in `examples/`.
