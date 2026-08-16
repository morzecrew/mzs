package mzs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The `http` module (§12.11) — the one part of the stdlib that reaches outside the
// process. It is installed like any other module and needs no host option: a script
// that says `include http` gets it. That is the one hole in the §14.3 sandbox, and it
// is deliberate, because the CLI's job is scripts that fetch and serve. A host that
// evaluates expressions someone else wrote — a condition out of a dialogue store has
// no business opening a socket — takes the name away with Unregister("http"), and
// then `http.serve` is `undefined variable 'http'` with no special case anywhere
// (§9.3).
//
// Handlers are ordinary closures, and they run on the goroutine that called
// http.serve. One runState is one Run's private state and two goroutines must never
// touch it (§10), so accepted connections are queued on a channel and answered one at
// a time. That is the honest shape of a script server — a webhook, a health check, a
// local tool — and a host that needs real concurrency runs several Runs, each with
// its own listener, or keeps its HTTP layer in Go and calls mzs per request.
//
// Time spent waiting for a connection costs nothing: the loop blocks on a channel and
// charges no steps. Each request instead gets the whole per-Run budget and a fresh
// deadline (§14.1), so a slow handler fails that one request and the server keeps
// serving.
//
// Errors here carry the kind `http` when the failure is the network's — a refused
// connection, a body over the cap — and the ordinary `argument`/`type` kinds when the
// script called the member wrongly (§13.5). That is also why only the second group spells
// `http:` in its message: the prefix says which module when the kind does not.

func init() {
	RegisterModuleFunc("http", "serve", 2, 3, httpServe)
	RegisterModuleFunc("http", "stop", 0, 0, httpStop)
	RegisterModuleFunc("http", "json", 1, 3, httpJSONReply)
	RegisterModuleFunc("http", "text", 1, 3, httpTextReply)
	RegisterModuleFunc("http", "get", 1, 2, httpClientGet)
	RegisterModuleFunc("http", "post", 2, 3, httpClientPost)
	RegisterModuleFunc("http", "request", 2, 3, httpClientRequest)
}

const (
	// httpReadHeaderTimeout bounds a client that opens a connection and says nothing.
	httpReadHeaderTimeout = 10 * time.Second
	// httpShutdownGrace is how long a stopped server waits for in-flight writes.
	httpShutdownGrace = 5 * time.Second
	// httpClientTimeout is the default for http.get/post/request, overridable with
	// the `timeout` option and always additionally bounded by the Run's own deadline.
	httpClientTimeout = 10 * time.Second
)

// Canonical member names of the request and response dicts. They are spelled once
// here so a typo cannot make the script side and the Go side disagree.
var (
	httpKStatus  = Str("status")
	httpKBody    = Str("body")
	httpKHeaders = Str("headers")
	httpKTimeout = Str("timeout")
)

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// httpJob is one accepted request handed from a net/http goroutine to the serve loop.
// The accepting goroutine blocks on done until the loop has written the response, so
// the ResponseWriter is only ever touched by one goroutine at a time.
type httpJob struct {
	w      http.ResponseWriter
	r      *http.Request
	fn     Value
	params []string
	done   chan struct{}
}

// httpServer is the state one http.serve call owns. It lives on the runState so
// http.stop() can find it from inside a handler.
type httpServer struct {
	jobs    chan *httpJob
	closing chan struct{} // closed when the loop stops accepting jobs
	stop    bool          // set by http.stop(), read by the loop between requests
}

// httpServe binds addr, routes every pattern in the routes dict to its closure and
// serves until http.stop() or until the Run's context is canceled.
//
//	http.serve(":8080", {
//	  "GET /hello":    { (req) -> "привет" },
//	  "GET /get/{id}": { (req) -> http.json({id: req["params"]["id"]}) },
//	})
//
// Patterns are net/http's own (Go 1.22): an optional method, a path, and {name}
// wildcards that arrive in req["params"]. The optional third argument is a closure
// called with the base URL once the listener is up, which is how ":0" is usable — the
// port is only known after binding.
func httpServe(c *Ctx, args []Value) (Value, error) {
	addr, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	routes := args[1]
	if routes.Kind() != KDict {
		return Nil(), c.TypeErrorf("http.serve expects a dict of routes, got %s", routes.Kind())
	}
	ready := Nil()
	if len(args) > 2 {
		ready = args[2]
		if !ready.IsNil() && ready.Kind() != KFunc {
			return Nil(), c.TypeErrorf("http.serve: the third argument is a closure called with the base URL, got %s", ready.Kind())
		}
	}

	rs := c.rs
	if rs.srv != nil {
		return Nil(), c.ArgErrorf("http.serve: a server is already running in this run")
	}

	srv := &httpServer{jobs: make(chan *httpJob), closing: make(chan struct{})}
	mux := http.NewServeMux()
	for _, k := range routes.Keys() {
		pattern := k.Str()
		fn := routes.Get(k)
		if fn.Kind() != KFunc {
			return Nil(), c.TypeErrorf("http.serve: route %q must be a closure, got %s", pattern, fn.Kind())
		}
		if err := httpHandle(mux, pattern, srv.handlerFor(fn, httpWildcards(pattern))); err != nil {
			return Nil(), c.ArgErrorf("http.serve: %v", err)
		}
	}

	ln, lerr := net.Listen("tcp", addr)
	if lerr != nil {
		return Nil(), c.ErrorfKind(ErrKindHTTP, "http.serve: %v", lerr)
	}
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: httpReadHeaderTimeout}
	go func() { _ = hs.Serve(ln) }()

	rs.srv = srv
	// Steps are re-armed per request below, so the Run's own counter is restored on
	// the way out: what the server cost is the sum of its handlers, and the waiting
	// in between costs nothing.
	baseSteps := rs.sh.steps
	handlerSteps := int64(0)
	defer func() {
		rs.srv = nil
		rs.sh.steps, rs.tick = baseSteps+handlerSteps, 0
		if rs.hasDL {
			rs.deadline = time.Now().Add(rs.opts.Timeout)
		}
		close(srv.closing)
		ctx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}()

	if ready.Kind() == KFunc {
		if _, err := httpCall(c, ready, Str("http://"+ln.Addr().String())); err != nil {
			return Nil(), err
		}
	}

	var done <-chan struct{}
	if rs.ctx != nil {
		done = rs.ctx.Done()
	}
	for !srv.stop {
		// Waiting for a connection is waiting like any other: the interpreter is
		// released for the Run's tasks and taken back to run the handler (§8.14), so a
		// server and an `async fn` compose instead of starving each other.
		var (
			job      *httpJob
			canceled bool
		)
		c.blocking(func() {
			select {
			case j := <-srv.jobs:
				job = j
			case <-done:
				canceled = true
			}
		})
		if canceled {
			// A canceled context ends the Run, server or not (§14.1). The deferred
			// Shutdown still gets its grace period for whatever is mid-write.
			return Nil(), limitErrorf(ErrCanceled, "canceled")
		}
		steps, fatal := srv.dispatch(c, job)
		handlerSteps += steps
		if fatal != nil {
			return Nil(), fatal
		}
	}
	return Nil(), nil
}

// httpHandle registers one route. net/http answers a malformed pattern with a panic,
// and a typo in a route is a script error like any other (§17), so it is converted
// here instead of reaching the embedder through A7's recover.
func httpHandle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("route %q: %v", pattern, r)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// httpStop ends the http.serve loop of this Run after the current request. It is an
// argument error outside a handler, because there would be nothing to stop.
func httpStop(c *Ctx, args []Value) (Value, error) {
	if c.rs.srv == nil {
		return Nil(), c.ArgErrorf("http.stop: no server is running in this run")
	}
	c.rs.srv.stop = true
	return Nil(), nil
}

// handlerFor is the net/http side of one route: it hands the request to the serve
// loop and waits. Once the loop is gone — stopped, or the Run canceled — waiting
// would be forever, so the request is answered with 503 instead.
func (s *httpServer) handlerFor(fn Value, params []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job := &httpJob{w: w, r: r, fn: fn, params: params, done: make(chan struct{})}
		select {
		case s.jobs <- job:
			<-job.done
		case <-s.closing:
			http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		case <-r.Context().Done():
		}
	})
}

// dispatch runs one handler on the serve goroutine and writes its response. It
// returns the steps the handler took, and a non-nil error only when the whole Run
// must end: one broken handler is a 500 and the next request, not a dead server.
func (s *httpServer) dispatch(c *Ctx, job *httpJob) (steps int64, fatal error) {
	defer close(job.done)
	rs := c.rs

	req, err := httpRequestValue(c, job)
	if err != nil {
		httpFail(rs, job, http.StatusBadRequest, err)
		return 0, nil
	}

	// Every request starts from a clean budget and a fresh deadline: an hour-old
	// server must not answer more slowly than a fresh one.
	rs.sh.steps, rs.tick = 0, 0
	if rs.hasDL {
		rs.deadline = time.Now().Add(rs.opts.Timeout)
	}
	v, err := httpCall(c, job.fn, req)
	steps = rs.sh.steps
	if err != nil {
		if errors.Is(err, ErrCanceled) {
			httpFail(rs, job, http.StatusServiceUnavailable, err)
			return steps, err
		}
		httpFail(rs, job, http.StatusInternalServerError, err)
		return steps, nil
	}
	if err := httpWrite(c, job.w, v); err != nil {
		fmt.Fprintf(rs.opts.errOut(), "http: %s %s: %v\n", job.r.Method, job.r.URL.Path, err)
	}
	return steps, nil
}

// httpCall invokes a script closure and absorbs the control signals a top-level
// closure can raise: `return v` inside a handler means "answer v", and there is no
// enclosing loop or function for a `break`/`next` to reach (§8.10).
func httpCall(c *Ctx, fn Value, args ...Value) (Value, error) {
	v, err := c.Call(fn, args...)
	if sig, ok := ctrlOf(err); ok {
		return sig.val, nil
	}
	return v, err
}

// httpFail writes a status-only response and records why on Stderr. The body a
// client sees never carries the script's diagnostic: an error message can quote a
// $var, and a $var can hold whatever the last caller typed (§13.5).
func httpFail(rs *runState, job *httpJob, status int, err error) {
	fmt.Fprintf(rs.opts.errOut(), "http: %s %s: %v\n", job.r.Method, job.r.URL.Path, err)
	http.Error(job.w, http.StatusText(status), status)
}

// ---------------------------------------------------------------------------
// Request and response values
// ---------------------------------------------------------------------------

// httpRequestValue builds the dict a handler is called with:
//
//	{method: "GET", path: "/get/7", params: {id: "7"}, query: {full: "1"},
//	 headers: {accept: "*/*"}, body: "", host: "…", remote: "…"}
//
// Header names are lowercased and repeated values collapse to the first, because a
// script that reaches for req["headers"]["accept"] wants a string, not a case
// convention. The raw request stays out of reach: everything here is an ordinary
// mzs value, so nothing a handler does can reach back into net/http.
func httpRequestValue(c *Ctx, job *httpJob) (Value, error) {
	r := job.r
	max := c.rs.opts.MaxStringBytes
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(max)+1))
	if err != nil {
		return Nil(), c.ErrorfKind(ErrKindHTTP, "reading the request body: %v", err)
	}
	if len(body) > max {
		return Nil(), c.ErrorfKind(ErrKindHTTP, "request body exceeds the %d byte limit", max)
	}

	params := NewOrderedDictCap(len(job.params))
	for _, name := range job.params {
		_ = params.Set(Str(name), Str(r.PathValue(name)))
	}

	query := NewOrderedDict()
	q := r.URL.Query()
	for _, k := range sortedStrings(q) {
		if vs := q[k]; len(vs) > 0 {
			_ = query.Set(Str(k), Str(vs[0]))
		}
	}

	headers := NewOrderedDictCap(len(r.Header))
	for _, k := range sortedStrings(r.Header) {
		if vs := r.Header[k]; len(vs) > 0 {
			_ = headers.Set(Str(strings.ToLower(k)), Str(vs[0]))
		}
	}

	d := NewOrderedDictCap(8)
	_ = d.Set(Str("method"), Str(r.Method))
	_ = d.Set(Str("path"), Str(r.URL.Path))
	_ = d.Set(Str("params"), dictOf(params))
	_ = d.Set(Str("query"), dictOf(query))
	_ = d.Set(httpKHeaders, dictOf(headers))
	_ = d.Set(httpKBody, Str(string(body)))
	_ = d.Set(Str("host"), Str(r.Host))
	_ = d.Set(Str("remote"), Str(r.RemoteAddr))
	return dictOf(d), nil
}

// httpWrite turns whatever the handler returned into an HTTP response (§12.11):
//
//	nil                     → 204, no body
//	a string                → 200 text/plain
//	a dict with `status`    → that status, its `body` and its `headers`
//	anything else           → 200 application/json
//
// http.json and http.text build the third form, so a handler that needs a status or
// a header never has to remember the shape.
func httpWrite(c *Ctx, w http.ResponseWriter, v Value) error {
	status, headers, body, ctype, err := httpResponse(c, v)
	if err != nil {
		return err
	}
	h := w.Header()
	for _, k := range headers.Keys() {
		h.Set(k.Str(), headers.Get(k).Str())
	}
	if ctype != "" && h.Get("Content-Type") == "" {
		h.Set("Content-Type", ctype)
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, err = w.Write(body)
	}
	return err
}

// httpResponse decodes the handler's return value into the four things a response
// needs. headers is always a dict value, possibly empty.
func httpResponse(c *Ctx, v Value) (status int, headers Value, body []byte, ctype string, err error) {
	empty := dictOf(NewOrderedDict())
	switch {
	case v.IsNil():
		return http.StatusNoContent, empty, nil, "", nil

	case v.Kind() == KString:
		return http.StatusOK, empty, []byte(v.Str()), "text/plain; charset=utf-8", nil

	case v.Kind() == KDict && v.Get(httpKStatus).Kind() == KInt:
		status = int(v.Get(httpKStatus).Int())
		if status < 100 || status > 599 {
			return 0, empty, nil, "", c.ArgErrorf("http: status must be between 100 and 599, got %d", status)
		}
		headers = v.Get(httpKHeaders)
		if headers.Kind() != KDict {
			headers = empty
		}
		switch bv := v.Get(httpKBody); {
		case bv.IsNil():
			return status, headers, nil, "", nil
		case bv.Kind() == KString:
			return status, headers, []byte(bv.Str()), "text/plain; charset=utf-8", nil
		default:
			body, err = httpJSONBytes(c, bv)
			return status, headers, body, "application/json; charset=utf-8", err
		}

	default:
		body, err = httpJSONBytes(c, v)
		return http.StatusOK, empty, body, "application/json; charset=utf-8", err
	}
}

// httpJSONBytes encodes a value as JSON under the Run's string limit — a handler that
// builds a gigabyte of array must fail the way every other oversized value fails
// (§14.2), not stream it out.
func httpJSONBytes(c *Ctx, v Value) ([]byte, error) {
	b, err := v.MarshalJSON()
	if err != nil {
		return nil, c.ErrorfKind(ErrKindHTTP, "encoding the response: %v", err)
	}
	if err := c.CheckString(len(b)); err != nil {
		return nil, err
	}
	return b, nil
}

// httpJSONReply builds a JSON response dict: http.json(body), http.json(body, 201),
// http.json(body, 201, [x-request-id: id]). The body is encoded here, so the value a
// handler passes is snapshotted at the call and cannot change under the writer.
func httpJSONReply(c *Ctx, args []Value) (Value, error) {
	b, err := httpJSONBytes(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return httpReply(c, args, Str(string(b)), "application/json; charset=utf-8")
}

// httpTextReply is the same for a text body: http.text("нет", 404).
func httpTextReply(c *Ctx, args []Value) (Value, error) {
	return httpReply(c, args, Str(args[0].Str()), "text/plain; charset=utf-8")
}

// httpReply assembles the canonical [status:, body:, headers:] dict shared by
// http.json and http.text. args[1] is the optional status, args[2] the optional
// extra headers; a Content-Type the caller supplied is left alone.
func httpReply(c *Ctx, args []Value, body Value, ctype string) (Value, error) {
	status := int64(http.StatusOK)
	if len(args) > 1 && !args[1].IsNil() {
		n, err := argInt(c, args[1])
		if err != nil {
			return Nil(), err
		}
		status = n
	}
	if status < 100 || status > 599 {
		return Nil(), c.ArgErrorf("http: status must be between 100 and 599, got %d", status)
	}

	headers := NewOrderedDict()
	_ = headers.Set(Str("content-type"), Str(ctype))
	if len(args) > 2 && !args[2].IsNil() {
		if args[2].Kind() != KDict {
			return Nil(), c.TypeErrorf("http: headers must be a dict, got %s", args[2].Kind())
		}
		for _, k := range args[2].Keys() {
			_ = headers.Set(Str(strings.ToLower(k.Str())), Str(args[2].Get(k).Str()))
		}
	}

	d := NewOrderedDictCap(3)
	_ = d.Set(httpKStatus, Int(status))
	_ = d.Set(httpKBody, body)
	_ = d.Set(httpKHeaders, dictOf(headers))
	return dictOf(d), nil
}

// httpWildcards lists the {name} segments of a net/http pattern in order, so the
// serve loop can fill req["params"] without asking net/http what it matched. A
// trailing {name...} is the same parameter with a different spelling.
func httpWildcards(pattern string) []string {
	var out []string
	for {
		i := strings.IndexByte(pattern, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(pattern[i:], '}')
		if j < 0 {
			return out
		}
		name := pattern[i+1 : i+j]
		pattern = pattern[i+j+1:]
		name = strings.TrimSuffix(name, "...")
		if name != "" && name != "$" {
			out = append(out, name)
		}
	}
}

// sortedStrings orders the keys of a header or query map so two runs of the same
// request build the same dict (§8.13).
func sortedStrings(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// httpClientGet is http.get(url) / http.get(url, opts).
func httpClientGet(c *Ctx, args []Value) (Value, error) {
	return httpDo(c, http.MethodGet, args[0], Nil(), argAt(args, 1))
}

// httpClientPost is http.post(url, body) / http.post(url, body, opts). A string body
// is sent as is; anything else is sent as JSON.
func httpClientPost(c *Ctx, args []Value) (Value, error) {
	return httpDo(c, http.MethodPost, args[0], args[1], argAt(args, 2))
}

// httpClientRequest is the general form: http.request("PUT", url, {body: …,
// headers: {…}, timeout: 3}).
func httpClientRequest(c *Ctx, args []Value) (Value, error) {
	method, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	opts := argAt(args, 2)
	body := Nil()
	if opts.Kind() == KDict {
		body = opts.Get(httpKBody)
	}
	return httpDo(c, strings.ToUpper(method), args[1], body, opts)
}

// httpDo performs one request and returns [status:, body:, headers:]. Everything that
// can go wrong on the wire is an ordinary catchable error — `try http.get(u) else …`
// is how a script handles a service being down (§8.11) — while running out of the
// Run's own time is a limit error, which is not catchable and ends the Run.
func httpDo(c *Ctx, method string, urlV, bodyV, opts Value) (Value, error) {
	url, err := argStr(c, urlV)
	if err != nil {
		return Nil(), err
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return Nil(), c.ArgErrorf("http: url must start with http:// or https://, got %q", url)
	}
	if err := c.Step(1000); err != nil {
		return Nil(), err
	}

	var payload io.Reader
	ctype := ""
	if !bodyV.IsNil() {
		if bodyV.Kind() == KString {
			payload, ctype = strings.NewReader(bodyV.Str()), "text/plain; charset=utf-8"
		} else {
			b, jerr := httpJSONBytes(c, bodyV)
			if jerr != nil {
				return Nil(), jerr
			}
			payload, ctype = strings.NewReader(string(b)), "application/json; charset=utf-8"
		}
	}

	timeout := httpClientTimeout
	if opts.Kind() == KDict {
		if t := opts.Get(httpKTimeout); !t.IsNil() {
			secs, terr := argInt(c, t)
			if terr != nil {
				return Nil(), terr
			}
			if secs <= 0 {
				return Nil(), c.ArgErrorf("http: timeout must be positive, got %d", secs)
			}
			timeout = time.Duration(secs) * time.Second
		}
	}

	parent := c.rs.ctx
	if parent == nil {
		parent = context.Background()
	}
	// The Run's deadline wins: a one second Run cannot spend ten seconds waiting.
	if c.rs.hasDL && time.Until(c.rs.deadline) < timeout {
		timeout = time.Until(c.rs.deadline)
	}
	if timeout <= 0 {
		return Nil(), limitErrorf(ErrTimeout, "execution timed out after %s", c.rs.opts.Timeout)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	req, rerr := http.NewRequestWithContext(ctx, method, url, payload)
	if rerr != nil {
		return Nil(), c.ArgErrorf("http: %v", rerr)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if opts.Kind() == KDict {
		if hs := opts.Get(httpKHeaders); hs.Kind() == KDict {
			for _, k := range hs.Keys() {
				req.Header.Set(k.Str(), hs.Get(k).Str())
			}
		}
	}

	// The wire is where a Run has time to spare: the lock is released for the request
	// and for reading the body, so the other tasks of §8.14 run while this one waits —
	// two `http.get` calls in two tasks are in flight together. Nothing inside the
	// closure touches interpreter state, which is what makes that safe.
	var (
		resp *http.Response
		derr error
	)
	c.blocking(func() { resp, derr = http.DefaultClient.Do(req) })
	if derr != nil {
		if errors.Is(derr, context.DeadlineExceeded) && c.rs.hasDL && !time.Now().Before(c.rs.deadline) {
			return Nil(), limitErrorf(ErrTimeout, "execution timed out after %s", c.rs.opts.Timeout)
		}
		if errors.Is(derr, context.Canceled) {
			return Nil(), limitErrorf(ErrCanceled, "canceled")
		}
		return Nil(), c.ErrorfKind(ErrKindHTTP, "%s %s: %v", method, url, derr)
	}
	defer resp.Body.Close()

	max := c.rs.opts.MaxStringBytes
	var (
		body []byte
		berr error
	)
	c.blocking(func() { body, berr = io.ReadAll(io.LimitReader(resp.Body, int64(max)+1)) })
	if berr != nil {
		return Nil(), c.ErrorfKind(ErrKindHTTP, "reading the response of %s %s: %v", method, url, berr)
	}
	if len(body) > max {
		return Nil(), c.ErrorfKind(ErrKindHTTP, "response exceeds the %d byte limit", max)
	}

	headers := NewOrderedDictCap(len(resp.Header))
	for _, k := range sortedStrings(resp.Header) {
		if vs := resp.Header[k]; len(vs) > 0 {
			_ = headers.Set(Str(strings.ToLower(k)), Str(vs[0]))
		}
	}
	d := NewOrderedDictCap(3)
	_ = d.Set(httpKStatus, Int(int64(resp.StatusCode)))
	_ = d.Set(httpKBody, Str(string(body)))
	_ = d.Set(httpKHeaders, dictOf(headers))
	return dictOf(d), nil
}

// argAt is the optional-argument reader: a missing argument is nil, exactly as it is
// for a script function (§7.7).
func argAt(args []Value, i int) Value {
	if i < 0 || i >= len(args) {
		return Nil()
	}
	return args[i]
}
