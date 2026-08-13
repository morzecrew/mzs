# Host functions and modules

Adding Go functions, modules and default `$vars` to an interpreter, and the `Ctx` a host
function is called with.

```go
func (in *Interp) Register(name string, arity int, f HostFunc)
func (in *Interp) RegisterModule(name string, members map[string]Value)
func (in *Interp) SetGlobal(name string, v Value)
func (in *Interp) Unregister(name string)

type HostFunc func(c *Ctx, args []Value) (Value, error)
```

All four are setup-only: do them between `New` and the first `Run`. They are not safe once
a Run has started.

## Register

```go
in.Register("slugify", 1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
	s := strings.ToLower(strings.TrimSpace(args[0].Str()))
	if s == "" {
		return mzs.Nil(), c.Errorf("slugify: empty input")
	}
	return mzs.Str(strings.ReplaceAll(s, " ", "-")), nil
})
in.Register("sum", -1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
	var t int64
	for _, a := range args {
		t += a.Int()
	}
	return mzs.Int(t), nil
})
```

`arity` is the exact argument count; `-1` is variadic. The check happens before the call,
so `args` always has exactly `arity` elements for a fixed-arity function. Registering an
existing name replaces it.

```
slugify("  Hello World ")          => "hello-world"
"  Hello World ".slugify           => "hello-world"
sum(1, 2, 3, 4)                    => 10
slugify(1, 2)  => eval:1:1: argument: slugify expects 1 argument(s), got 2
```

A registered function is a method too, for free: `"…".slugify` is UFCS, not a second
registration. See [../stdlib/README.md](../stdlib/README.md).

## Errors from a host function

`c.Errorf` builds a positioned script error that `try` can catch:

```
slugify("")                     => eval:1:1: raise: slugify: empty input
try slugify("") else "fallback" => "fallback"
```

Wrap `mzs.ErrFatal` to make the error uncatchable and end the Run:

```go
return mzs.Nil(), fmt.Errorf("cannot continue: %w", mzs.ErrFatal)
```

```
try fatal() else "caught"  => eval:1:5: raise: cannot continue: mzs: fatal
```

## The Ctx API

| Method | Returns |
|---|---|
| `c.Context()` | the Run's `context.Context` — honour it |
| `c.Interp()` / `c.Options()` | the running `*Interp` / its normalised `Options` |
| `c.Arg(i)` / `c.NArgs()` / `c.Args()` | `Nil()` when out of range / count / slice |
| `c.Name()` | the name being executed, for diagnostics |
| `c.Global(n)` / `c.HasGlobal(n)` / `c.SetGlobal(n, v)` | `$vars` of this Run; `n` may omit the `$` |
| `c.Call(fn, args…)` | invoke a script function or closure |
| `c.Closure()` / `c.HasClosure()` / `c.CallClosure(args…)` | the trailing `{ … }` closure |
| `c.Errorf` / `c.ErrorfKind` / `c.TypeErrorf` / `c.ArgErrorf` | positioned script errors |
| `c.Step(n)` / `c.CheckCollection(n)` / `c.CheckString(n)` | charge budget, check limits |
| `c.Out()` / `c.Now()` / `c.Rand()` / `c.Location()` | host capabilities; `Now`/`Rand` error when not granted |
| `c.Blocking(f)` | run `f` with the interpreter released |

A `*Ctx` is reused across calls — never retain it past the call that received it.

```go
in.Register("apply", 2, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
	return c.Call(c.Arg(1), c.Arg(0))   // apply(5, { it + 1 }) => 6
})
in.Register("bump", 1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
	n := c.Global("count").Int() + args[0].Int()
	c.SetGlobal("count", mzs.Int(n))    // readable as res.Globals["$count"]
	return mzs.Int(n), nil
})
```

A host function that loops must call `c.Step(n)`, or it outruns the step budget. Return the
error it gives you; never swallow it.

## Blocking

Wrap a wait — a socket, a timer — so the Run's other tasks may use the interpreter
meanwhile. The closure must touch no `Value`, no global and no `Ctx` method.

```go
in.Register("sleep_ms", 1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
	if err := c.Step(1000); err != nil { return mzs.Nil(), err }
	d := time.Duration(args[0].Int()) * time.Millisecond
	ctx := c.Context() // captured here: the closure may touch no Ctx method
	var err error
	c.Blocking(func() {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	if err != nil { return mzs.Nil(), err }
	return args[0], nil
})
```

Two 60 ms tasks over that function finish the Run in 60 ms, not 120. See
[../language/async.md](../language/async.md).

**Wait on the context, not only on the timer.** A bare `time.Sleep(d)` cannot observe
cancellation: the Run stays blocked until `d` elapses, so `Eval` cannot return promptly
however its caller asks. `Ctx.Context()` is the context the Run was started with and host
functions must honour it; read it *before* `Blocking`, because the closure may touch no
`Ctx` method. With the `select` above, cancelling the caller's context returns in the
50 ms it was given rather than the 5 s the script asked to sleep.

**`Options.Timeout` is not a second escape hatch here.** It is enforced between evaluation
steps, and a host function that blocks is not between steps — so a `Timeout` alone leaves
the call running to completion and the Run returns no error. Anything that must be bounded
by wall clock has to arrive as a context with a deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
defer cancel()
v, err := in.Eval(ctx, `sleep_ms(5000)`, nil)   // returns in ~50ms
```

## RegisterModule, SetGlobal, Unregister

```go
in.RegisterModule("build", map[string]mzs.Value{
	"version": mzs.Str("2.0"),
	"tag": mzs.Fn("tag", 1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
		return mzs.Str("v" + args[0].Str()), nil
	}),
})
in.SetGlobal("env", mzs.Str("prod"))   // a default $env for every Run
in.Unregister("http")                  // drop a module a script must not reach
```

A host module is not ambient — the script names it first, exactly like a built-in one:

```
include build
build.version + " " + build.tag("9")     # => "2.0 v9"
```

`SetGlobal` sets a default; the `vars` map passed to `Run`/`Eval` overrides it per Run.
`Unregister` works on builtins, host functions and modules alike; afterwards no script can
name it, and only a later `Register`/`RegisterModule` brings it back:

```
include http
# name: unknown module 'http' (a script module needs a path: include http from "./http.mzs")
```

## See also

- [./README.md](./README.md) — `New`, `Compile`, `Run`, the `Options` table
- [./values.md](./values.md) — the `mzs.Value` constructors these functions return
- [./errors.md](./errors.md) — error kinds, `ErrFatal` and the limit sentinels
- [./filesystem.md](./filesystem.md) — `FS`, `Stdin`, `Env`, `ModuleLoader`
