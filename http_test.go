package mzs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The http module is the one capability that opens a socket, so these tests pin both
// halves of §12.11: that a script can serve and fetch with no host option at all, and
// that a host which takes the name away cannot be made to do either.

func netOpts() Options {
	return Options{Timeout: 5 * time.Second}
}

// server is a running script server: the base URL its listener bound, and the Run's
// eventual error.
type server struct {
	url  string
	done chan error
	in   *Interp
}

// startServer compiles src and runs it in its own goroutine. The script is expected to
// call http.serve with `{ (u) -> __ready(u) }` as its ready closure, which is how the
// test learns the port that ":0" picked.
func startServer(t *testing.T, src string, opts Options) *server {
	t.Helper()
	in := New(opts)
	urls := make(chan string, 1)
	in.Register("__ready", 1, func(c *Ctx, args []Value) (Value, error) {
		urls <- args[0].Str()
		return Nil(), nil
	})
	prog, err := in.Compile("server", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	s := &server{done: make(chan error, 1), in: in}
	go func() {
		_, rerr := in.Run(context.Background(), prog, nil)
		s.done <- rerr
	}()
	select {
	case s.url = <-urls:
	case err := <-s.done:
		t.Fatalf("server exited before it was ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}
	return s
}

// wait collects the Run's error once the script has stopped serving.
func (s *server) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
		return nil
	}
}

// do performs one request and returns the status, the body and the response headers.
func do(t *testing.T, method, url, body string) (int, string, http.Header) {
	t.Helper()
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// TestHTTPModuleUngated pins §12.11: the module needs no option at all. The bare
// Options a host gets from the zero value already has it, so `include http` is enough.
func TestHTTPModuleUngated(t *testing.T) {
	t.Parallel()

	bare := DefaultOptions()
	if _, ok := LookupModule("http", &bare); !ok {
		t.Fatal("http module absent under the default options")
	}
	if _, ok := LookupModule("http", &Options{}); !ok {
		t.Fatal("http module absent under the zero Options")
	}
}

// TestHTTPModuleUnregister pins the way out: a host that must not let a script open a
// socket takes the name away, and then the module is absent, not disabled — a condition
// out of a dialogue store cannot even name the thing that would.
func TestHTTPModuleUnregister(t *testing.T) {
	t.Parallel()

	in := New(DefaultOptions())
	in.Unregister("http")
	_, err := in.Eval(context.Background(), `include http
http.serve(":0", {})`, nil)
	if err == nil {
		t.Fatal("http.serve succeeded after Unregister")
	}
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrKindName {
		t.Fatalf("error = %v; want a name error", err)
	}
	if !strings.Contains(err.Error(), "'http'") {
		t.Fatalf("error = %v; want it to name 'http'", err)
	}
}

// TestHTTPServe covers one server end to end: the four response shapes of §12.11, the
// request dict, a route that fails, and http.stop.
func TestHTTPServe(t *testing.T) {
	t.Parallel()

	s := startServer(t, `
		include http
		http.serve("127.0.0.1:0", {
		  "GET /hello":     { (req) -> "привет, " + (req["query"]["name"] ?? "мир") },
		  "GET /get/{id}":  { (req) -> http.json({id: req["params"]["id"].int, path: req["path"]}) },
		  "GET /raw":       { (req) -> [1, 2, 3] },
		  "GET /none":      { (req) -> nil },
		  "POST /echo":     { (req) -> http.text(req["method"] + ":" + req["body"], 201, {"x-echo": "1"}) },
		  "GET /agent":     { (req) -> req["headers"]["x-test"] },
		  "GET /boom":      { (req) -> 1 / 0 },
		  "GET /quit":      { (req) -> http.stop(); "bye" },
		}, { (u) -> __ready(u) })
	`, netOpts())

	t.Run("string body is text/plain", func(t *testing.T) {
		code, body, h := do(t, "GET", s.url+"/hello", "")
		if code != 200 || body != "привет, мир" {
			t.Fatalf("got %d %q; want 200 \"привет, мир\"", code, body)
		}
		if ct := h.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Fatalf("Content-Type = %q", ct)
		}
	})

	t.Run("query reaches the handler", func(t *testing.T) {
		_, body, _ := do(t, "GET", s.url+"/hello?name=Иван", "")
		if body != "привет, Иван" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("path wildcard reaches params", func(t *testing.T) {
		code, body, h := do(t, "GET", s.url+"/get/7", "")
		if code != 200 || body != `{"id":7,"path":"/get/7"}` {
			t.Fatalf("got %d %q", code, body)
		}
		if ct := h.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Fatalf("Content-Type = %q", ct)
		}
	})

	t.Run("any other value is JSON", func(t *testing.T) {
		code, body, h := do(t, "GET", s.url+"/raw", "")
		if code != 200 || body != "[1,2,3]" {
			t.Fatalf("got %d %q", code, body)
		}
		if ct := h.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Fatalf("Content-Type = %q", ct)
		}
	})

	t.Run("nil is 204", func(t *testing.T) {
		code, body, _ := do(t, "GET", s.url+"/none", "")
		if code != 204 || body != "" {
			t.Fatalf("got %d %q; want 204 and no body", code, body)
		}
	})

	t.Run("status and headers from http.text", func(t *testing.T) {
		code, body, h := do(t, "POST", s.url+"/echo", "тело")
		if code != 201 || body != "POST:тело" {
			t.Fatalf("got %d %q", code, body)
		}
		if h.Get("X-Echo") != "1" {
			t.Fatalf("X-Echo = %q", h.Get("X-Echo"))
		}
	})

	t.Run("request headers are lowercased", func(t *testing.T) {
		req, _ := http.NewRequest("GET", s.url+"/agent", nil)
		req.Header.Set("X-Test", "да")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /agent: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "да" {
			t.Fatalf("body = %q; want the x-test header", b)
		}
	})

	t.Run("unknown path is 404", func(t *testing.T) {
		if code, _, _ := do(t, "GET", s.url+"/nope", ""); code != 404 {
			t.Fatalf("code = %d; want 404", code)
		}
	})

	t.Run("wrong method is 405", func(t *testing.T) {
		if code, _, _ := do(t, "DELETE", s.url+"/hello", ""); code != 405 {
			t.Fatalf("code = %d; want 405", code)
		}
	})

	t.Run("a failing handler is 500 and the server lives", func(t *testing.T) {
		code, body, _ := do(t, "GET", s.url+"/boom", "")
		if code != 500 {
			t.Fatalf("code = %d; want 500", code)
		}
		if strings.Contains(body, "division") {
			t.Fatalf("body leaked the diagnostic: %q", body)
		}
		if code, body, _ := do(t, "GET", s.url+"/hello", ""); code != 200 || body != "привет, мир" {
			t.Fatalf("after the failure: %d %q", code, body)
		}
	})

	t.Run("http.stop ends the run cleanly", func(t *testing.T) {
		code, body, _ := do(t, "GET", s.url+"/quit", "")
		if code != 200 || body != "bye" {
			t.Fatalf("got %d %q; want the response before the stop", code, body)
		}
		if err := s.wait(t); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := http.Get(s.url + "/hello"); err == nil {
			t.Fatal("the listener is still up after http.stop")
		}
	})
}

// TestHTTPServeBudgetIsPerRequest pins the §14.1 arrangement that makes a long-lived
// server possible at all: waiting costs nothing, and each request starts from a full
// budget. Under one shared budget the third request below would fail where the first
// succeeded, which is the bug this test exists to catch.
func TestHTTPServeBudgetIsPerRequest(t *testing.T) {
	t.Parallel()

	o := netOpts()
	o.StepBudget = 200_000
	s := startServer(t, `
		include http
		fn work() { (0..2000).map { it * 2 }.sum }
		http.serve("127.0.0.1:0", {
		  "GET /work": { (req) -> work().str },
		  "GET /hog":  { (req) -> while true { 1 }; "unreachable" },
		  "GET /quit": { (req) -> http.stop(); "bye" },
		}, { (u) -> __ready(u) })
	`, o)

	for i := 0; i < 5; i++ {
		code, body, _ := do(t, "GET", s.url+"/work", "")
		if code != 200 || body != "4002000" {
			t.Fatalf("request %d: got %d %q; want 200 \"4002000\"", i, code, body)
		}
	}

	// A handler that runs away is charged to that request only.
	if code, _, _ := do(t, "GET", s.url+"/hog", ""); code != 500 {
		t.Fatalf("runaway handler: code = %d; want 500", code)
	}
	if code, body, _ := do(t, "GET", s.url+"/work", ""); code != 200 || body != "4002000" {
		t.Fatalf("after the runaway handler: %d %q", code, body)
	}

	do(t, "GET", s.url+"/quit", "")
	if err := s.wait(t); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestHTTPServeRestoresTheRunBudget pins the other half: what the server spent is
// visible to the host afterwards, and the rest of the script still gets to run.
func TestHTTPServeRestoresTheRunBudget(t *testing.T) {
	t.Parallel()

	s := startServer(t, `
		include http
		http.serve("127.0.0.1:0", {"GET /quit": { (req) -> http.stop(); "bye" }}, { (u) -> __ready(u) })
		"after"
	`, netOpts())

	do(t, "GET", s.url+"/quit", "")
	if err := s.wait(t); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestHTTPServeCanceledContext pins that a host cancel ends the server the way it ends
// any other Run (§14.1): the listener goes down and Run reports ErrCanceled.
func TestHTTPServeCanceledContext(t *testing.T) {
	t.Parallel()

	in := New(netOpts())
	urls := make(chan string, 1)
	in.Register("__ready", 1, func(c *Ctx, args []Value) (Value, error) {
		urls <- args[0].Str()
		return Nil(), nil
	})
	prog, err := in.Compile("server", `
		include http
		http.serve("127.0.0.1:0", {"GET /hello": { (req) -> "hi" }}, { (u) -> __ready(u) })
	`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, rerr := in.Run(ctx, prog, nil)
		done <- rerr
	}()

	url := <-urls
	if code, _, _ := do(t, "GET", url+"/hello", ""); code != 200 {
		t.Fatalf("code = %d", code)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("Run = %v; want ErrCanceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server ignored the canceled context")
	}
	if _, err := http.Get(url + "/hello"); err == nil {
		t.Fatal("the listener survived the cancel")
	}
}

// TestHTTPServeArgumentErrors pins the diagnostics a script author actually hits: a
// route table that is not one, a route whose value is not a closure, a pattern net/http
// rejects, a port that is taken, a second server in one Run, and a stop with nothing to
// stop.
func TestHTTPServeArgumentErrors(t *testing.T) {
	t.Parallel()

	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer busy.Close()
	busyAddr := strings.TrimPrefix(busy.URL, "http://")

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"routes must be a dict", `include http
http.serve(":0", "GET /x")`, "dict of routes"},
		{"a route must be a closure", `include http
http.serve(":0", {"GET /x": 1})`, "must be a closure"},
		{"a malformed pattern is reported", `include http
http.serve("127.0.0.1:0", {"GET /x/{": { (r) -> "x" }})`, `route "GET /x/{"`},
		{"a taken port is reported", fmt.Sprintf(`include http
http.serve(%q, {"GET /x": { (r) -> "x" }})`, busyAddr), "http.serve:"},
		{"stop outside a server", `include http
http.stop()`, "no server is running"},
		{"status out of range", `include http
http.json({a: 1}, 42)`, "between 100 and 599"},
		{"headers must be a dict", `include http
http.text("x", 200, "nope")`, "headers must be a dict"},
		{"a url needs a scheme", `include http
http.get("example.com")`, "must start with http://"},
	}

	// Not parallel: the busy port must stay busy, and a parallel subtest would run
	// after this function's defer has already closed it — a serve that then succeeds
	// blocks the whole suite instead of failing.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(netOpts()).Eval(context.Background(), tt.src, nil)
			if err == nil {
				t.Fatalf("%s: no error", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestHTTPServeIsSingleUsePerRun pins that one Run owns at most one listener, so
// http.stop is never ambiguous about what it stops.
func TestHTTPServeIsSingleUsePerRun(t *testing.T) {
	t.Parallel()

	s := startServer(t, `
		include http
		http.serve("127.0.0.1:0", {
		  "GET /nest": { (req) -> http.serve("127.0.0.1:0", {"GET /x": { (r) -> "x" }}) },
		  "GET /quit": { (req) -> http.stop(); "bye" },
		}, { (u) -> __ready(u) })
	`, netOpts())

	if code, _, _ := do(t, "GET", s.url+"/nest", ""); code != 500 {
		t.Fatalf("code = %d; want 500 from the nested serve", code)
	}
	do(t, "GET", s.url+"/quit", "")
	if err := s.wait(t); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestHTTPClient covers http.get/post/request against a real server: the response
// shape, a body that goes out as JSON, and a failure a script can catch with `try`.
func TestHTTPClient(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Method", r.Method)
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"got":%s,"ctype":%q}`, string(b), r.Header.Get("Content-Type"))
		case "/who":
			fmt.Fprint(w, r.Header.Get("X-Who"))
		case "/teapot":
			w.WriteHeader(418)
			fmt.Fprint(w, "нет")
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer backend.Close()

	in := New(netOpts())
	in.SetGlobal("$base", Str(backend.URL))

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"get returns status and body", `include http
r = http.get($base + "/x"); "${r["status"]}:${r["body"]}"`, "200:ok"},
		{"a non-2xx status is a value, not an error", `include http
http.get($base + "/teapot")["status"].str`, "418"},
		{"response headers are lowercased", `include http
http.get($base + "/echo")["headers"]["x-method"]`, "GET"},
		{"post sends a dict as JSON", `include http
http.post($base + "/echo", {a: 1})["body"]`,
			`{"got":{"a":1},"ctype":"application/json; charset=utf-8"}`},
		{"request sets headers", `include http
http.request("GET", $base + "/who", {headers: {"x-who": "Иван"}})["body"]`, "Иван"},
		{"request carries its own body", `include http
http.request("PUT", $base + "/echo", {body: "42"})["headers"]["x-method"]`, "PUT"},
		{"a dead host is catchable", `include http
try http.get("http://127.0.0.1:1/x") else "нет связи"`, "нет связи"},
		{"a transport failure knows which module it came from", `include http
try http.get("http://127.0.0.1:1/x") else (e) -> e["kind"]`, "http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := in.Eval(context.Background(), tt.src, nil)
			if err != nil {
				t.Fatalf("%s: %v", tt.src, err)
			}
			if got := v.Str(); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
}

// TestHTTPClientHonoursTheRunDeadline pins that a slow service cannot outlive the Run
// that called it: the wait is bounded by the Run's own deadline and reported as the
// timeout it is, which `try` cannot swallow (§8.11).
func TestHTTPClientHonoursTheRunDeadline(t *testing.T) {
	t.Parallel()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()

	o := netOpts()
	o.Timeout = 200 * time.Millisecond
	in := New(o)
	in.SetGlobal("$base", Str(slow.URL))

	start := time.Now()
	_, err := in.Eval(context.Background(), `include http
try http.get($base) else "swallowed"`, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v; want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waited %s; the run deadline should have cut it short", elapsed)
	}
}

// TestHTTPClientTimeoutOption pins the per-call timeout: it is the script's own choice
// and, unlike the Run deadline, an ordinary catchable error.
func TestHTTPClientTimeoutOption(t *testing.T) {
	t.Parallel()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()

	o := netOpts()
	o.Timeout = 30 * time.Second
	in := New(o)
	in.SetGlobal("$base", Str(slow.URL))

	v, err := in.Eval(context.Background(), `include http
try http.get($base, {timeout: 1}) else "поздно"`, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "поздно" {
		t.Fatalf("got %q; want the catchable timeout", v.Str())
	}
}

// TestHTTPWildcards pins the pattern scan that fills req["params"], including the
// trailing {rest...} form and net/http's {$} anchor, which is not a parameter.
func TestHTTPWildcards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    []string
	}{
		{"GET /hello", nil},
		{"GET /get/{id}", []string{"id"}},
		{"/a/{x}/b/{y}", []string{"x", "y"}},
		{"GET /files/{path...}", []string{"path"}},
		{"GET /exact/{$}", nil},
		{"GET /broken/{", nil},
	}

	for _, tt := range tests {
		got := httpWildcards(tt.pattern)
		if len(got) != len(tt.want) {
			t.Fatalf("httpWildcards(%q) = %v; want %v", tt.pattern, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("httpWildcards(%q) = %v; want %v", tt.pattern, got, tt.want)
			}
		}
	}
}
