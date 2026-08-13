# Async functions and tasks

`async fn` is the whole of concurrency in mzs: calling one starts a task, `await` reads it, and one goroutine of a run evaluates at a time.

## A call starts a task, it does not run the body

```
async fn double(n) { n * 2 }

t = double(21)
[type(t), t.done]      # ["task",false]
t.await                # 42
t.done                 # true
str(t)                 # "#<task double done>"
```

The body waits for the caller to release the interpreter — at an `await`, inside a blocking
host call, or when the calling code ends:

```
async fn work() { say("the body runs here") }
t = work()
say("the call returned")
t.await
```

```
the call returned
the body runs here
```

What *does* happen at the call site is argument binding: arity is checked there
(`f expects 1 argument(s), got 0`), and a default argument is the caller's code, run before
the task exists — after `async fn f(x = bump()) { x }; t = f()`, `bump` has already run.

## `await` and `done`

A task has exactly two methods; by UFCS they are also `await(t)` and `done(t)`.

| Name | Meaning |
|---|---|
| `t.await` | wait, then yield the body's value; re-raises the body's error here |
| `t.done` | has it finished — the only question that never waits |

```
async fn f() { 7 }
t = f()
[t.await, t.await, t.done]   # [7,7,true]
```

Awaiting again returns the same value. Every other name falls through to UFCS on ordinary
functions — `t.type` is `"task"` and `t.str` is `"#<task f done>"` — and a name that is
neither is an error: `f().wibble` is `undefined method 'wibble'`, and `5.await` is
`undefined method 'await' for int`.

## Fan-out

```
include http                                            # network
urls = (1..5).map { "http://127.0.0.1:8731/slow/300" }
async fn get(u) { http.get(u)["body"] }
urls.map { get(it) }.map { it.await }
```

Two `map`s, not one: the first starts every task, the second reads them. Doing both in one
pass (`urls.map { get(it).await }`) waits for each before starting the next.

Measured against a local server that sleeps 300 ms per request, five requests:

```sh
# seq.mzs: urls.map { http.get(it)["body"] }
$ /usr/bin/time -f '%e s' mzs -t 10 seq.mzs
1.50 s
# par.mzs: urls.map { get(it) }.map { it.await }
$ /usr/bin/time -f '%e s' mzs -t 10 par.mzs
0.30 s
```

Five 300 ms requests cost 300 ms. Awaiting each task as it starts gives the sequential
number back: the same two URLs cost 0.60 s awaited immediately and 0.30 s awaited after both
calls.

## One evaluator at a time

A task is a goroutine, but a run holds one lock and whichever goroutine evaluates must hold
it. It is released at exactly three points: at an `await`, inside a blocking host call
(the `http` client, `http.serve` between connections, and the `io` file calls are the ones
in the box), and when the task finishes. So two tasks never touch one array, dict or `$var`
at the same time.

```
counter = [0]
log = []
async fn bump(id) { 20.times { counter[0] += 1 }; log.push(id) }
(1..8).map { (i) -> bump(i) }.map { it.await }
[counter[0], log].json    # [160,[2,8,6,7,3,4,1,5]]
```

`counter` is 160 on every run — no lock, no atomic, no lost update. `log` is not: three runs
gave `[2,3,4,5,1,6,7,8]`, `[1,8,5,7,3,4,2,6]`, `[2,8,6,7,3,4,1,5]`. Nothing is preemptive —
a task that only computes runs to its end once it holds the lock — but which *runnable* task
takes over at a release point is unspecified, so two tasks that both `say` may print in
either order.

## One budget, one deadline

Tasks spend the run's steps and end on the run's deadline; being a task buys no extra time.

```sh
$ mzs --steps 200000 -e 'async fn burn() { (1..100000).sum }
(1..5).map { burn() }.map { it.await }.len'
-e:1:31: limit: step budget exceeded (200000 steps)
```

Waiting honours the deadline too, so a cycle ends on the clock instead of hanging:

```sh
$ mzs -t 300ms -e 'async fn a() { $b.await }
async fn b() { $a.await }
$a = a(); $b = b(); $a.await'
-e:3:24: limit: execution timed out after 300ms      # exit 3
```

A task that awaits itself is a named error rather than a wait:

```
async fn selfish() { $me.await }
$me = selfish()
try $me.await else (e) -> e["message"]
# "a task cannot await itself: 'selfish' would wait for its own result"
```

## Unawaited tasks still finish

A run ends when the program *and* its tasks are over, so a fire-and-forget task is a use,
not a leak. Its failure goes to stderr rather than into a value, and does not change the
exit code:

```sh
$ mzs -e 'async fn boom() { raise("crashed") }
boom()
"done"'
mzs: task 'boom' failed and was never awaited: -e:1:19: raise: crashed   # stderr
done                                                                    # stdout, exit 0
```

Await it and the error is yours: `try boom().await else "fallback"` is `"fallback"`, and
nothing reaches stderr. A task cancelled by the deadline is not reported — that says the run
ended, not that the task was wrong.

## MaxTasks

`--tasks n` (`Options.MaxTasks`, default 64) bounds how many tasks are *unfinished at once*,
not how many a run may start: 200 tasks awaited one at a time are fine.

```sh
$ mzs -e 'async fn f(n) { n }; (1..200).map { (i) -> f(i).await }.sum'
20100                                                             # awaited one at a time

$ mzs -e 'async fn f(n) { n }; (1..100).map { (i) -> f(i) }.map { it.await }.sum'
-e:1:44: limit: too many tasks: 64 already running (MaxTasks)     # exit 3, all started first

$ mzs --tasks 0 -e 'async fn f() { 1 }; f()'
-e:1:21: limit: tasks are disabled: the host set MaxTasks to none, so 'f' cannot be started
```

## Where `async` may be written

Directly before the `fn` of a named declaration, and nowhere else. `async fn` nested inside
another function body is fine, and `export async fn f(…)` is that declaration exported —
`lib.twice(21).await` is `42`. There is no async closure
— `f = async fn (a) { a }` is `syntax: unexpected 'fn' after statement`, and `async` is
positional, so `async = 1` is an ordinary variable.

[examples/28_async_tasks.mzs](../../examples/28_async_tasks.mzs) is the runnable tour.

## See also

- [functions.md](./functions.md) — `fn`, closures, defaults, UFCS
- [errors.md](./errors.md) — `try … else`, what is not catchable
- [../modules/http.md](../modules/http.md) — the blocking calls worth fanning out
- [../reference/sandbox.md](../reference/sandbox.md) — `MaxTasks` among the other limits
