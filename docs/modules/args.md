# The `args` module

A script's command-line flags, declared in one dict.

```
include args

opt = args.parse({
  width:   {default: 800, help: "target width in pixels"},
  quality: {default: 0.85, short: "q"},
  force:   {default: false, short: "f"},
  tag:     [],
}, {usage: "resize [flags] <file>…", min: 1})

opt["width"]      # 800, or what --width said, as an int
opt["rest"]       # the positionals
```

Everything after the script path is the script's ([cli](../cli/README.md#argv)), so
`mzs resize.mzs --width 640 -fq 0.5 photo.png` hands this script the whole of
`--width 640 -fq 0.5 photo.png`, and `args.parse` turns it into
`{width: 640, quality: 0.5, force: true, tag: [], rest: ["photo.png"]}`. `--help` prints
the usage, built from the same dict, and a typo stops the script the way mzs stops on one
of its own, with a message, the usage and exit status 2.

The module reads `$ARGV` and nothing else, so it needs no host option
([sandbox](../reference/sandbox.md)). An embedder that binds no `$ARGV` gets the defaults back.

## Members

| Call | Result |
|---|---|
| `args.parse(spec, opts = {})` | every flag by its spec key, in spec order, then `rest`, the positionals |
| `args.usage(spec, opts = {})` | the text `--help` prints |

## The spec

A key is a flag, written with `_` in the spec and `-` on the command line: `dry_run` is
`--dry-run`. The value is the flag's entry:

| Entry key | Meaning |
|---|---|
| `default` | the value when the flag is absent, and the kind it converts to |
| `help` | one line for the usage |
| `short` | one letter or digit, so `short: "q"` adds `-q` |
| `required` | `true`: the flag has no default and must be given |

Anything that is not a dict is shorthand for `{default: it}`, so a spec can be one line:
`args.parse({width: 800, force: false, tag: []})`.

**The default decides the conversion.** There is no type vocabulary to learn:

| Default | On the command line | Value |
|---|---|---|
| `false` / `true` | `--force`, `--no-force`, `--force=false` | bool |
| an int | `--width 800`, `--width=800` | int, read as `parse_int` reads it |
| a float | `--quality 0.5` | float, read as `parse_float` reads it |
| a string | `--name x` | string |
| an array | `--tag a --tag b` | the strings given, or the default when there are none |
| `nil`, or none | `--out x` | string, or `nil` when absent |

```sh
$ mzs -e 'include args; args.parse({width: 800, force: false, tag: []})' --json -- --width=1_024 --tag a --tag b x
{"width":1024,"force":false,"tag":["a","b"],"rest":["x"]}
```

`rest` and `help` cannot be flags, and `-h` cannot be a short. A mistake in the spec,
such as an unknown entry key, a duplicate short, a default of another kind, or a flag
`no_x` beside a bool `x` (both would answer to `--no-x`), is an ordinary error of the
script's, because nobody at the command line can fix it.

## The command line

- Flags and positionals may be mixed: `resize a.png --width 640 b.png`.
- `--` ends the flags, so a positional that starts with `-` goes after it.
- A flag that takes a value takes the next argument whatever it looks like, so
  `--width -5` is `-5`.
- Short bools combine (`-fv`). The last short in a cluster may take a value, attached
  (`-q0.5`, `-q=0.5`) or as the next argument (`-fq 0.5`).
- A flag given twice keeps the last value. A flag whose default is an array collects them
  all.
- `-h` and `--help` are always there.

## The options

| Option | Meaning |
|---|---|
| `usage` | the synopsis, printed after `usage: ` |
| `name` | what starts an error line; by default the first word of `usage` |
| `min`, `max` | how many positionals are allowed |
| `argv` | an array of strings to parse instead of `$ARGV` |
| `exit` | `false` to raise instead of exiting (below) |

## When the command line is wrong

```sh
$ mzs examples/52_args.mzs --widht 640 photo.png; echo $?
52_args.mzs: unknown flag --widht (did you mean --width?)
usage: 52_args.mzs [flags] <image>…

flags:
  -h, --help              show this help
      --width <int>       target width in pixels (default 800)
  -q, --quality <number>  JPEG quality, 0 to 1 (default 0.85)
      --out <string>      output directory (default: next to each file)
  -f, --force             overwrite what is there
      --tag <string>      a label for the output (repeatable)
      --[no-]dry-run      only say what would happen (default true)
2
```

A usage error can be any of these:
- an unknown flag, with the near miss;
- a value that does not convert;
- a flag missing its value;
- a value given to `--no-x`;
- an absent required flag;
- too few or too many positionals.

By default `args.parse` writes the message and the usage to stderr and ends the Run with
`exit(2)`. `--help` writes the usage to stdout and ends it with `exit(0)`. Those are the
statuses mzs itself uses, so a shell script calling yours can branch on them.

With `{exit: false}` nothing is written. `args.parse` raises an `argument` error instead,
with data `{flag, reason}`. `flag` is the flag as typed, the long spelling of a required
flag that was not given, or `nil` for a positional count. `reason` is one of `unknown`, `bad_value`, `needs_value`, `takes_no_value`,
`required`, `too_few`, `too_many` and `help`. Together with `argv`, this is how a test
checks a script's command line without starting a process:

```
r = try args.parse(spec, {argv: ["-q", "fast"], exit: false}) else (e) -> e["data"]
r          # {"flag":"-q","reason":"bad_value"}
```

## A script that runs as a command

A shebang line works because the kernel puts the script path first and the arguments
after it, and mzs leaves those alone:

```sh
$ head -1 examples/52_args.mzs
#!/usr/bin/env mzs
$ chmod +x examples/52_args.mzs && examples/52_args.mzs --width 640 photo.png | head -2
would resize 1 file(s) to 640px, quality 0.85
  photo.png → photo_640.png
```

## See also

- [../cli/README.md](../cli/README.md#argv) — `$ARGV`, and why everything after the script is the script's
- [./README.md](./README.md) — include, module rules, the module table
- [../../examples/52_args.mzs](../../examples/52_args.mzs) — a script with flags of its own
