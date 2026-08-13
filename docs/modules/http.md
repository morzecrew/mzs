# The http module

`include http` is both halves of HTTP: a server whose routes are closures, and a client whose
answers are dicts.

It is the one capability in the stdlib that needs no host option: `include http` is the whole
requirement, where [./io.md](./io.md) additionally needs an `Options.FS`. Without the include
`http.get` is `name: undefined variable 'http'`, and the diagnostic names the missing include;
an embedder that must not allow a socket takes the module away with `Unregister("http")`
([../embedding/functions.md](../embedding/functions.md)).

| Member | Signature |
|---|---|
| `serve` | `(addr, routes, ready = nil) -> nil` |
| `stop` | `() -> nil` |
| `json` | `(body, status = 200, headers = {}) -> dict` |
| `text` | `(body, status = 200, headers = {}) -> dict` |
| `get` | `(url, opts = {}) -> dict` |
| `post` | `(url, body, opts = {}) -> dict` |
| `request` | `(method, url, opts = {}) -> dict` |

## The server

```
include http

ITEMS = [{id: 1, name: "haircut"}, {id: 2, name: "shave"}]

http.serve(":8080", {
  "GET /health":    { (_) -> "ok" },
  "GET /item/{id}": { (req) ->
    item = ITEMS.find { it["id"] == req["params"]["id"].int }
    item ?? http.json({error: "no item ${req["params"]["id"]}"}, 404)
  },
  "GET /shutdown":  { (_) -> http.stop(); "bye" },
}, { (url) -> say("listening on ${url}") })

say("stopped")
```

```sh
$ mzs -t 30 server.mzs &
listening on http://[::]:8080
$ curl -s localhost:8080/health
ok
$ curl -s localhost:8080/item/2
{"id":2,"name":"shave"}
$ curl -s -i localhost:8080/item/9 | head -2
HTTP/1.1 404 Not Found
Content-Type: application/json; charset=utf-8
$ curl -s localhost:8080/shutdown
bye
stopped
```

A route key is a `net/http` pattern — an optional method, a path, and `{name}` wildcards
(`{name...}` for a trailing segment); a malformed one is an argument error before the listener
binds. `serve` returns when a handler calls `http.stop()`, and the Run continues on the next
line; a canceled context instead ends the Run. The third argument is a closure called with the
base URL once the listener is up, which is what makes `":0"` usable — the port is only known
after binding.

Routing that never reaches a handler: an unmatched path is 404, an unmatched method 405.

```sh
$ curl -s -i localhost:8080/nope | head -1
HTTP/1.1 404 Not Found
$ curl -s -i -X POST localhost:8080/health | head -2
HTTP/1.1 405 Method Not Allowed
Allow: GET, HEAD
```

## The request dict

A handler that returns its own argument shows the whole of it — here for
`POST /u/ivan/note/7?draft=1` with `-H 'X-Trace: abc' -d 'hello'`:

```
{"method": "POST", "path": "/u/ivan/note/7",
 "params": {"user": "ivan", "id": "7"},
 "query": {"draft": "1"},
 "headers": {"accept": "*/*", "content-length": "5",
             "content-type": "application/x-www-form-urlencoded",
             "user-agent": "curl/7.81.0", "x-trace": "abc"},
 "body": "hello", "host": "localhost:8080", "remote": "127.0.0.1:57518"}
```

Header names are lowercased, and repeated header and query values collapse to the first.
Nothing is a live object — parse `body` yourself with [./json.md](./json.md). The body is read
under `MaxStringBytes`, and a larger one never reaches the handler:

```sh
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Expect:' --data-binary @9mb.bin localhost:8080/echo
400
# server's stderr: http: POST /echo: echo.mzs:2:6: raise: http: request body exceeds the 8388608 byte limit
```

## What a handler returns

| Returned | Response |
|---|---|
| `nil` | 204, no body |
| a string | 200, `text/plain; charset=utf-8` |
| a dict with an int `status` | that status, its `body`, its `headers` |
| anything else | 200, `application/json; charset=utf-8` |

```sh
$ curl -s -i localhost:8080/nil   | head -1   # { (_) -> nil }
HTTP/1.1 204 No Content
$ curl -s -i localhost:8080/value | tail -1   # { (_) -> {a: 1, b: [2, 3]} }
{"a":1,"b":[2,3]}
$ curl -s -i localhost:8080/dict  | head -3   # {status: 201, body: "made", headers: {"x-note": "hand built"}}
HTTP/1.1 201 Created
Content-Type: text/plain; charset=utf-8
X-Note: hand built
$ curl -s -i localhost:8080/text  | head -3   # http.text("нет", 418, ["x-brew": "no"])
HTTP/1.1 418 I'm a teapot
Content-Type: text/plain; charset=utf-8
X-Brew: no
```

`http.json` and `http.text` only build that third form — they are ordinary dicts, so a handler
can inspect or edit one before returning it. Inside a handler `return v` answers with `v`.

```sh
$ mzs --json -e 'include http; http.json({a: 1}, 201, {"X-Id": "7"})'
{"status":201,"body":"{\"a\":1}","headers":{"content-type":"application/json; charset=utf-8","x-id":"7"}}
```

A `status` outside 100–599 is an argument error. In a dict response a string `body` is sent as
is, any other value as JSON.

## One request at a time

Handlers run on the goroutine that called `serve`, so requests are queued and answered one at a
time. With `"GET /work": { (_) -> n = 0; while n < 300000 { n = n + 1 }; "done ${n}" }`, two
parallel curls take twice as long as one:

```sh
$ time curl -s localhost:8080/work
real    0m0,112s
$ time ( curl -s localhost:8080/work & curl -s localhost:8080/work & wait )
real    0m0,218s
```

A host that needs parallelism runs several Runs, each with its own listener, or keeps HTTP in
Go and calls mzs per request. A second `http.serve` in the same Run is an argument error:
`http.serve: a server is already running in this run`.

## Budgets and failures

Waiting for a connection charges nothing. Each request starts from a **fresh step budget and a
fresh timeout deadline**, so a handler that runs out of either fails that one request with 500
and the server keeps serving. The diagnostic goes to stderr, never into the body.

```sh
$ curl -s -i localhost:8080/boom | head -1
HTTP/1.1 500 Internal Server Error
$ curl -s localhost:8080/health
ok
# server's stderr: http: GET /boom: boom.mzs:4:29: raise: handler exploded
```

`http.stop()` outside a handler is an argument error. A canceled context ends the whole Run and
takes the listener down with it.

## The client

The answer is always `{status:, body:, headers:}` — status an int, body a string, header names
lowercased.

```sh
$ mzs -t 15 -e 'include http; r = http.get("https://example.com"); [r["status"], r["headers"]["content-type"], r["body"].len]' --json
[200,"text/html",559]
```

`post` sends a string body as is and anything else as JSON; `request` takes `body`, `headers`
and `timeout` in its options dict. Against an `/echo` route that reports what it received:

```sh
$ mzs -t 10 -e 'include http; http.post("http://localhost:8080/echo", {n: 1})["body"]'
{"body":"{\"n\":1}","ctype":"application/json; charset=utf-8"}
$ mzs -t 10 -e 'include http; http.post("http://localhost:8080/echo", "raw text")["body"]'
{"body":"raw text","ctype":"text/plain; charset=utf-8"}
$ mzs -t 10 -e 'include http; http.request("PUT", "http://localhost:8080/echo", {body: "x", timeout: 3})["body"]'
{"method":"PUT","body":"x"}
```

**A non-2xx status is a value, not an error** — nothing needs catching to read a 404. What *is*
an error is the wire, and that is catchable.

```sh
$ mzs -t 15 -e 'include http; http.get("https://example.com/no-such-page")["status"]'
404
$ mzs -t 10 -e 'include http; try http.get("http://localhost:9/x") else (e) -> e["message"]'
http: GET http://localhost:9/x: Get "http://localhost:9/x": dial tcp 127.0.0.1:9: connect: connection refused
$ mzs -e 'include http; http.get("localhost:8080/health")'
-e:1:20: argument: http: url must start with http:// or https://, got "localhost:8080/health"
```

## Timeouts

A call is bounded by `opts.timeout` (default 10 s) **and** by what is left of the Run's own
deadline, whichever is shorter. The first is an ordinary error; the second is a limit, which
`try` cannot catch and which ends the Run with exit code 3 — which is why a script that
fetches wants `-t 30` and not the default `-t 1s`.

```sh
$ mzs -t 20 -e 'include http; try http.get("http://10.255.255.1/x", {timeout: 2}) else (e) -> e["message"]'
http: GET http://10.255.255.1/x: Get "http://10.255.255.1/x": context deadline exceeded
$ mzs -e 'include http; try http.get("http://10.255.255.1/x") else "caught"'
-e:1:24: limit: execution timed out after 1s
$ echo $?
3
```

Client calls release the interpreter while the request is on the wire, so tasks overlap their
waiting — two 2-second timeouts in two tasks finish in two seconds, not four:

```
include http
async fn probe(u) { try http.get(u, {timeout: 2}) else "timed out" }
a = probe("http://10.255.255.1/x")
b = probe("http://10.255.255.2/x")
say("${a.await} / ${b.await}")
```

```sh
$ mzs -t 20 probe.mzs      # -t 20: the Run's own 1s deadline would fire first
timed out / timed out      # real 0m2,005s
```

Responses are read under `MaxStringBytes`; a larger one is a catchable error and never a
prefix — `http: response exceeds the 8388608 byte limit`.

Worked programs: [../../examples/30_http_service.mzs](../../examples/30_http_service.mzs) (both
halves) and [../../examples/31_api_pipeline.mzs](../../examples/31_api_pipeline.mzs) (a client
report).

## See also

- [./README.md](./README.md) — include, module rules, the module table
- [./json.md](./json.md) — parsing a request or response body
- [./io.md](./io.md) — the other module that reaches outside the process
- [../language/async.md](../language/async.md) — tasks, `await`, overlapping waits
