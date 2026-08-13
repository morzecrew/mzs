# Modules

How `include` works, what a module value is, and which built-in modules exist behind which host capability.

```
include json                      # a built-in module
include time                      # built-in, but needs a clock (mzs --time)
include cart from "./cart.mzs"    # a script of your own
```

## Nothing is ambient

A name enters the program only through `include`. Without it the compiler says so and
names the fix:

```sh
mzs -e 'json.parse("{}")'
```

```
-e:1:1: name: 'json' is a module: add `include json` at the top of the file
  json.parse("{}")
  ^
```

`include` is a statement, not a declaration that floats to the top: it binds the name in
the scope it stands in, and using the name earlier — or outside that scope — is a compile
error.

```sh
mzs -e 'json.parse("1"); include json'      # name: 'json' is a module: add `include json` …
mzs -e 'if true { include math }; math.pi'  # name: undefined variable 'math' (add `include math` …)
```

Two `include`s of the same name are a warning, and the program still runs:

```sh
mzs -e 'include json; include json; json.keys.len'
```

```
-e:1:23: warning: 'json' is already bound; the include replaces it
  include json; include json; json.keys.len
                        ^
2
```

## A module is a dict

```
include math
type(math)          # dict
math.len            # 15
math.keys           # ["pi","e","sqrt","cbrt","sin","cos","tan","atan","log","log2","log10","exp","atan2","pow","hypot"]
math["sqrt"]        # the function itself, not called
```

Member access with `.` *calls* a member that is a function, exactly like a method:
`math.sqrt` on its own is `math.sqrt expects 1 argument(s), got 0`. To pass a member
around as a value, index the module like the dict it is — `[1, 4].map(math["sqrt"])`.
There is no `::`; `json::parse(s)` is `syntax: '::' is not an mzs operator; use '.'`.

## A module is never callable

```sh
mzs -e 'include json; json({total: 1})'
```

```
-e:1:15: name: 'json' is a module, not a function: call one of its members (json.parse, json.pretty); the 'json' function is written 'x.json'
```

A module of your own has no function half, so the message stops at the rule:

```
-e:1:35: name: 'money' is a module, not a function: call one of its members
  include money from "./money.mzs"; money(15)
                                    ^
```

## The built-in modules

| Module | Members | Host capability |
|---|---|---|
| `json` | `parse pretty` | none |
| `math` | `pi e sqrt cbrt sin cos tan atan atan2 log log2 log10 exp pow hypot` | none |
| `http` | `serve stop json text get post request` | none (reaches the network) |
| `time` | `now parse at` | `EnableTime`; `Now` for `time.now` — `mzs --time` |
| `date` | `today parse` | `EnableTime`; `Now` for `date.today` — `mzs --time` |
| `io` | `stdin lines read write append exists ls env` | `Options.FS` — the CLI installs one, `--no-io` takes it back |

A module the host did not enable is absent, and the diagnostic names the option instead of
the symptom:

```sh
mzs -e 'include time'    # name: module 'time' needs a clock: the host did not set EnableTime (mzs --time)
mzs --no-io -e 'io.read("x")'  # name: module 'io' needs a filesystem: the host did not install Options.FS
```

`defined(name)` answers whether a module is reachable at all, before you include it:

```sh
mzs -e 'defined(time)'          # false
mzs --time -e 'defined(time)'   # true
mzs --no-io -e 'defined(io)'    # false
```

A host adds modules of its own with `RegisterModule` and removes built-ins with
`Unregister` — see [../embedding/functions.md](../embedding/functions.md).

## See also

- [./json.md](./json.md) — parsing and encoding JSON
- [./custom.md](./custom.md) — `include … from` and `export`
- [./time.md](./time.md) — the clock-gated modules
- [../reference/sandbox.md](../reference/sandbox.md) — the capabilities behind the gates
