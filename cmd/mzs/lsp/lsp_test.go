package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// session drives a server the way an editor does: a list of messages in, the frames it
// wrote out.
type session struct {
	t   *testing.T
	out []map[string]json.RawMessage
}

func run(t *testing.T, msgs ...any) *session {
	t.Helper()
	var in bytes.Buffer
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshalling %v: %v", m, err)
		}
		fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	var out bytes.Buffer
	if err := Serve(&in, &out, "test"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return &session{t: t, out: frames(t, out.Bytes())}
}

// frames splits the output stream, which is what proves the framing is right: a wrong
// Content-Length shows up here as a body that does not parse.
func frames(t *testing.T, b []byte) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for len(b) > 0 {
		i := bytes.Index(b, []byte("Content-Length: "))
		if i < 0 {
			break
		}
		j := bytes.Index(b[i:], []byte("\r\n\r\n"))
		if j < 0 {
			t.Fatalf("a header with no blank line after it: %q", b[i:])
		}
		var n int
		if _, err := fmt.Sscanf(string(b[i+len("Content-Length: "):i+j]), "%d", &n); err != nil {
			t.Fatalf("bad Content-Length: %v", err)
		}
		start := i + j + 4
		if start+n > len(b) {
			t.Fatalf("Content-Length %d runs past the end of the stream", n)
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(b[start:start+n], &msg); err != nil {
			t.Fatalf("frame does not parse: %v in %q", err, b[start:start+n])
		}
		out = append(out, msg)
		b = b[start+n:]
	}
	return out
}

// reply finds the answer to one request id.
func (s *session) reply(id float64) map[string]json.RawMessage {
	s.t.Helper()
	for _, m := range s.out {
		var got float64
		if raw, ok := m["id"]; ok && json.Unmarshal(raw, &got) == nil && got == id {
			return m
		}
	}
	s.t.Fatalf("no reply with id %v in %d frames", id, len(s.out))
	return nil
}

// notice finds the first notification with the given method.
func (s *session) notice(method string) map[string]json.RawMessage {
	s.t.Helper()
	for _, m := range s.out {
		var got string
		if raw, ok := m["method"]; ok && json.Unmarshal(raw, &got) == nil && got == method {
			return m
		}
	}
	s.t.Fatalf("no %s notification in %d frames", method, len(s.out))
	return nil
}

func into(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshalling %s: %v", raw, err)
	}
}

func open(uri, text string) any {
	return msg("textDocument/didOpen", nil, map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 1, "text": text},
	})
}

func at(method, uri string, id, line, char int) any {
	return msg(method, id, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
	})
}

func msg(method string, id any, params any) any {
	m := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != nil {
		m["id"] = id
	}
	return m
}

const uri = "file:///tmp/mzs-lsp-test/a.mzs"

func TestInitializeAdvertisesWhatIsImplemented(t *testing.T) {
	s := run(t, msg("initialize", 1, map[string]any{}), msg("exit", nil, nil))
	var res struct {
		Capabilities serverCapabilities `json:"capabilities"`
		ServerInfo   serverInfo         `json:"serverInfo"`
	}
	into(t, s.reply(1)["result"], &res)
	c := res.Capabilities
	if !c.HoverProvider || !c.DefinitionProvider || !c.DocumentFormattingProvider || !c.DocumentSymbolProvider {
		t.Errorf("a capability is missing: %+v", c)
	}
	if c.TextDocumentSync.Change != 1 {
		t.Errorf("sync kind = %d, want 1 (full)", c.TextDocumentSync.Change)
	}
	if res.ServerInfo.Name != "mzs" {
		t.Errorf("server name = %q", res.ServerInfo.Name)
	}
}

func TestDiagnosticsCarryTheCompilersMessage(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = 1\ny = undefinedthing\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", p.Diagnostics)
	}
	d := p.Diagnostics[0]
	if !strings.Contains(d.Message, "undefined variable") {
		t.Errorf("message = %q", d.Message)
	}
	if d.Severity != severityError || d.Source != "mzs" || d.Code != "name" {
		t.Errorf("severity/source/code = %d/%q/%q", d.Severity, d.Source, d.Code)
	}
	// Zero-based line 1, and the range covers the whole name rather than its first rune.
	want := rng{Start: position{Line: 1, Character: 4}, End: position{Line: 1, Character: 18}}
	if d.Range != want {
		t.Errorf("range = %+v, want %+v", d.Range, want)
	}
}

// A gated module is not an error in an editor: which flags the program will be run with
// is not something a buffer knows (§12.8).
func TestGatedModulesAreNotErrors(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "include time\ninclude io\ninclude test\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", p.Diagnostics)
	}
}

// A script module is read for its exports when the program runs, and the server never
// runs a program — but the include still has to reach a loader or it is a compile error
// naming the host (§12.8). The one installed here reads nothing.
func TestAnIncludedFileIsNotAnError(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "include lib from \"./lib.mzs\"\nx = 1\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none", p.Diagnostics)
	}
}

// The columns the protocol counts are UTF-16 code units and the columns mzs counts are
// runes. A line of Cyrillic is where the two disagree, and where a caret lands in the
// wrong place if nobody converts.
func TestPositionsAreUTF16(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "привет = \"да\"\nx = приветик\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", p.Diagnostics)
	}
	want := rng{Start: position{Line: 1, Character: 4}, End: position{Line: 1, Character: 12}}
	if p.Diagnostics[0].Range != want {
		t.Errorf("range = %+v, want %+v", p.Diagnostics[0].Range, want)
	}
}

func TestHoverKnowsMethodsBuiltinsAndModules(t *testing.T) {
	src := "include json\nx = \"да\".upper\ny = println(x)\nz = json.parse(\"{}\")\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		at("textDocument/hover", uri, 2, 1, 11), // .upper
		at("textDocument/hover", uri, 3, 2, 6),  // println
		at("textDocument/hover", uri, 4, 3, 10), // json.parse
		at("textDocument/hover", uri, 5, 0, 9),  // the module name itself
		msg("exit", nil, nil),
	)
	for _, tc := range []struct {
		id   int
		want string
	}{
		{2, "**.upper**"},
		{3, "**println**"},
		{4, "**json.parse**"},
		{5, "**module json**"},
	} {
		var h hover
		into(t, s.reply(float64(tc.id))["result"], &h)
		if !strings.HasPrefix(h.Contents.Value, tc.want) {
			t.Errorf("hover %d = %q, want it to start with %q", tc.id, h.Contents.Value, tc.want)
		}
		if h.Contents.Kind != "markdown" {
			t.Errorf("hover %d kind = %q", tc.id, h.Contents.Kind)
		}
	}
}

func TestDefinitionFindsTheDeclaration(t *testing.T) {
	src := "fn greet(name) { \"hi ${name}\" }\nx = greet(\"ann\")\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		at("textDocument/definition", uri, 2, 1, 6),
		msg("exit", nil, nil),
	)
	var loc location
	into(t, s.reply(2)["result"], &loc)
	if loc.URI != uri || loc.Range.Start.Line != 0 {
		t.Errorf("definition = %+v, want the fn on line 0 of the same file", loc)
	}
}

// What a function body binds is a local of that body (§8.2). A walk of the tree would
// report it as something the file declares, which is how a loop variable ends up in the
// outline and in top-level completion.
func TestOnlyTheTopLevelDeclares(t *testing.T) {
	src := "fn helper(x) {\n    inner = x + 1\n    inner\n}\ntop = 1\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		msg("textDocument/documentSymbol", 2, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		at("textDocument/completion", uri, 3, 4, 3),
		msg("exit", nil, nil),
	)
	var syms []documentSymbol
	into(t, s.reply(2)["result"], &syms)
	var names []string
	for _, sym := range syms {
		names = append(names, sym.Name)
	}
	if strings.Join(names, ",") != "helper,top" {
		t.Errorf("symbols = %v, want helper,top: a body's locals are not the file's", names)
	}
	var items []completionItem
	into(t, s.reply(3)["result"], &items)
	if hasLabel(items, "inner") {
		t.Errorf("a function-local binding is offered at the top level")
	}
}

// `export` wraps the declaration it exports (§12.8), so the outline has to look through
// it or a module's whole surface goes missing from its own file.
func TestExportedDeclarationsAreDeclarations(t *testing.T) {
	src := "export fn total(xs) { xs.sum }\nexport rate = 0.2\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		msg("textDocument/documentSymbol", 2, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		msg("exit", nil, nil),
	)
	var syms []documentSymbol
	into(t, s.reply(2)["result"], &syms)
	var names []string
	for _, sym := range syms {
		names = append(names, sym.Name)
	}
	if strings.Join(names, ",") != "total,rate" {
		t.Errorf("symbols = %v, want total,rate", names)
	}
}

// A destructuring declares every name it binds (RFC 0001, RFC 0002); an index it writes
// through declares nothing.
func TestDestructuringDeclaresItsNames(t *testing.T) {
	src := "head, *tail = [1, 2, 3]\n{name, age: a, **rest} = {name: 1, age: 2}\n[x, {y}] = [1, {y: 2}]\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		msg("textDocument/documentSymbol", 2, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		msg("exit", nil, nil),
	)
	var syms []documentSymbol
	into(t, s.reply(2)["result"], &syms)
	var names []string
	for _, sym := range syms {
		names = append(names, sym.Name)
	}
	if strings.Join(names, ",") != "head,tail,name,a,rest,x,y" {
		t.Errorf("symbols = %v, want head,tail,name,a,rest,x,y", names)
	}
}

func TestDefinitionOpensAnIncludedFile(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.mzs")
	if err := os.WriteFile(lib, []byte("export x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.mzs")
	mainURI := pathToURI(main)
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(mainURI, "include lib from \"./lib.mzs\"\ny = lib\n"),
		at("textDocument/definition", mainURI, 2, 0, 9), // on the include itself
		at("textDocument/definition", mainURI, 3, 1, 5), // on a reference to it
		msg("exit", nil, nil),
	)
	for _, id := range []int{2, 3} {
		var loc location
		into(t, s.reply(float64(id))["result"], &loc)
		if loc.URI != pathToURI(lib) {
			t.Errorf("definition %d uri = %q, want %q", id, loc.URI, pathToURI(lib))
		}
	}
}

func TestCompletionAfterADotOffersMembers(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "include json\nx = json.pa\n"),
		at("textDocument/completion", uri, 2, 1, 11),
		msg("exit", nil, nil),
	)
	var items []completionItem
	into(t, s.reply(2)["result"], &items)
	if len(items) == 0 {
		t.Fatal("no completions after json.pa")
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Label, "pa") {
			t.Fatalf("completion %q does not match the prefix", it.Label)
		}
	}
	if !hasLabel(items, "parse") {
		t.Errorf("json.parse is not offered: %+v", items)
	}
}

func TestCompletionAtTheTopOffersWhatIsInScope(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "fn helper(x) { x }\nhel\n"),
		at("textDocument/completion", uri, 2, 1, 3),
		msg("exit", nil, nil),
	)
	var items []completionItem
	into(t, s.reply(2)["result"], &items)
	if !hasLabel(items, "helper") {
		t.Errorf("the file's own fn is not offered: %+v", items)
	}
}

func hasLabel(items []completionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

func TestFormattingReplacesTheWholeDocument(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x=1+2\n"),
		msg("textDocument/formatting", 2, map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}),
		msg("exit", nil, nil),
	)
	var edits []textEdit
	into(t, s.reply(2)["result"], &edits)
	if len(edits) != 1 || edits[0].NewText != "x = 1 + 2\n" {
		t.Fatalf("edits = %+v", edits)
	}
	if edits[0].Range.Start != (position{}) {
		t.Errorf("the edit does not start at the top of the file: %+v", edits[0].Range)
	}
}

// A file being typed does not parse, and the answer is no edits rather than an error
// dialog on every keystroke.
func TestFormattingAnUnparsableFileChangesNothing(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = (1 +\n"),
		msg("textDocument/formatting", 2, map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}),
		msg("exit", nil, nil),
	)
	var edits []textEdit
	into(t, s.reply(2)["result"], &edits)
	if len(edits) != 0 {
		t.Errorf("edits = %+v, want none", edits)
	}
}

func TestSymbolsAreTheOutline(t *testing.T) {
	src := "include json\nrecord Money(amount, currency)\nfn total(xs) { xs.sum }\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		msg("textDocument/documentSymbol", 2, map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}),
		msg("exit", nil, nil),
	)
	var syms []documentSymbol
	into(t, s.reply(2)["result"], &syms)
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "json,Money,total" {
		t.Errorf("symbols = %v, want json,Money,total in source order", names)
	}
}

// What a jump selects is the name, not the body: `fn` is where the declaration starts and
// is not what anybody was looking for.
func TestDefinitionSelectsTheName(t *testing.T) {
	src := "fn greet(name) {\n    \"hi ${name}\"\n}\nx = greet(\"ann\")\n"
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, src),
		at("textDocument/definition", uri, 2, 3, 6),
		msg("textDocument/documentSymbol", 3, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		msg("exit", nil, nil),
	)
	var loc location
	into(t, s.reply(2)["result"], &loc)
	want := rng{Start: position{Line: 0, Character: 3}, End: position{Line: 0, Character: 8}}
	if loc.Range != want {
		t.Errorf("definition range = %+v, want %+v (the name, not the body)", loc.Range, want)
	}
	var syms []documentSymbol
	into(t, s.reply(3)["result"], &syms)
	if len(syms) == 0 || syms[0].SelectionRange != want {
		t.Errorf("outline selection = %+v, want %+v", syms, want)
	}
}

// One malformed message is one bad message, not a dead server: the frame was read whole,
// so the stream is still aligned and the next request must still be answered.
func TestAMalformedPayloadDoesNotEndTheServer(t *testing.T) {
	var in bytes.Buffer
	writeFrame := func(body string) {
		fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	writeFrame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	writeFrame(`{"jsonrpc":"2.0",,,`) // whole frame, broken JSON
	writeFrame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`)
	writeFrame(`{"jsonrpc":"2.0","method":"exit"}`)

	var out bytes.Buffer
	if err := Serve(&in, &out, "test"); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	s := &session{t: t, out: frames(t, out.Bytes())}
	// The parse error is answered with a null id, and the request after it still works.
	var sawParseError bool
	for _, m := range s.out {
		if raw, ok := m["error"]; ok && string(m["id"]) == "null" {
			sawParseError = true
			if !bytes.Contains(raw, []byte("-32700")) {
				t.Errorf("error code = %s, want -32700", raw)
			}
		}
	}
	if !sawParseError {
		t.Errorf("no parse error with a null id in %d frames", len(s.out))
	}
	if _, ok := s.reply(2)["result"]; !ok {
		t.Errorf("the request after the bad frame was not answered")
	}
}

func TestDidChangeRepublishes(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = nope\n"),
		msg("textDocument/didChange", nil, map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": 2},
			"contentChanges": []any{map[string]any{"text": "x = 1\n"}},
		}),
		msg("exit", nil, nil),
	)
	var last publishDiagnosticsParams
	for _, m := range s.out {
		var method string
		if raw, ok := m["method"]; ok && json.Unmarshal(raw, &method) == nil && method == "textDocument/publishDiagnostics" {
			into(t, m["params"], &last)
		}
	}
	if len(last.Diagnostics) != 0 {
		t.Errorf("the fixed document still has diagnostics: %+v", last.Diagnostics)
	}
	if last.Version != 2 {
		t.Errorf("version = %d, want 2", last.Version)
	}
}

func TestDidCloseClearsTheDiagnostics(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = nope\n"),
		msg("textDocument/didClose", nil, map[string]any{
			"textDocument": map[string]any{"uri": uri},
		}),
		msg("exit", nil, nil),
	)
	var last publishDiagnosticsParams
	for _, m := range s.out {
		var method string
		if raw, ok := m["method"]; ok && json.Unmarshal(raw, &method) == nil && method == "textDocument/publishDiagnostics" {
			into(t, m["params"], &last)
		}
	}
	if len(last.Diagnostics) != 0 {
		t.Errorf("closing left %d diagnostics behind", len(last.Diagnostics))
	}
}

// A JSON-RPC response carries exactly one of `result` and `error`, and a success with
// nothing to say still carries `result: null`. `shutdown` is that reply.
func TestAResponseCarriesExactlyOneOfResultAndError(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		msg("shutdown", 2, nil),
		msg("exit", nil, nil),
	)
	reply := s.reply(2)
	raw, ok := reply["result"]
	if !ok || string(raw) != "null" {
		t.Errorf("shutdown result = %s (present=%v), want null", raw, ok)
	}
	if _, bad := reply["error"]; bad {
		t.Errorf("a success reply also carries an error: %v", reply)
	}

	s2 := run(t,
		msg("initialize", 1, map[string]any{}),
		msg("textDocument/nothing", 2, map[string]any{}),
		msg("exit", nil, nil),
	)
	if _, has := s2.reply(2)["result"]; has {
		t.Errorf("an error reply carries a result member: %v", s2.reply(2))
	}
}

func TestUnknownRequestIsAnErrorAndUnknownNotificationIsNot(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		msg("textDocument/somethingElse", nil, map[string]any{}),
		msg("textDocument/somethingElse", 2, map[string]any{}),
		msg("exit", nil, nil),
	)
	reply := s.reply(2)
	if _, ok := reply["error"]; !ok {
		t.Errorf("an unknown request was not answered with an error: %v", reply)
	}
	if len(s.out) != 2 {
		t.Errorf("frames = %d, want 2: the notification must be ignored silently", len(s.out))
	}
}

// Between `shutdown` and `exit` a server does no more work: that is the difference
// between "we are finishing" and "we are gone".
func TestShutdownRefusesFurtherWork(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		msg("shutdown", 2, nil),
		at("textDocument/hover", uri, 3, 0, 0),
		msg("exit", nil, nil),
	)
	if _, ok := s.reply(2)["error"]; ok {
		t.Errorf("shutdown itself was refused")
	}
	if _, ok := s.reply(3)["error"]; !ok {
		t.Errorf("a request after shutdown was answered: %v", s.reply(3))
	}
}

// The server compiles; it never runs a program. The filesystem it installs exists to make
// `include io` compile (§12.8) and answers every call with a refusal, which is the promise
// "an editor does not read your files on your behalf" stated where it can be checked.
func TestTheServersFilesystemReadsNothing(t *testing.T) {
	fs := checkFS{}
	if _, err := fs.Open("x"); err == nil {
		t.Error("Open succeeded")
	}
	if _, err := fs.Create("x"); err == nil {
		t.Error("Create succeeded")
	}
	if _, err := fs.Append("x"); err == nil {
		t.Error("Append succeeded")
	}
	if _, _, _, err := fs.Stat("x"); err == nil {
		t.Error("Stat succeeded")
	}
	if _, err := fs.List("."); err == nil {
		t.Error("List succeeded")
	}
}

// A request whose params are not what the method takes is answered, not ignored: a client
// waiting on an id it never gets back is a client that has stopped working.
func TestBadParamsAreAnswered(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		msg("textDocument/hover", 2, "not an object"),
		msg("textDocument/didOpen", nil, "not an object either"),
		msg("exit", nil, nil),
	)
	reply := s.reply(2)
	if _, ok := reply["error"]; !ok {
		t.Errorf("bad params were not answered with an error: %v", reply)
	}
	// The malformed *notification* is dropped in silence — there is no id to answer to —
	// and the server is still alive to say so.
	if len(s.out) != 2 {
		t.Errorf("frames = %d, want 2", len(s.out))
	}
}

// Recovery finds more than one error in a file (§17), and an editor shows all of them.
func TestEveryCompileErrorBecomesADiagnostic(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = nope\ny = alsonope\nz = stillnope\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v, want three", p.Diagnostics)
	}
	for i, want := range []string{"nope", "alsonope", "stillnope"} {
		if !strings.Contains(p.Diagnostics[i].Message, want) {
			t.Errorf("diagnostic %d = %q, want it to name %q", i, p.Diagnostics[i].Message, want)
		}
	}
}

// A warning is a warning, not an error: §11.5's regex note and the §17 shadowing warnings
// have to reach the editor at their own severity or they read as breakage.
func TestWarningsArriveAsWarnings(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "include json\ninclude json\nx = 1\n"),
		msg("exit", nil, nil),
	)
	var p publishDiagnosticsParams
	into(t, s.notice("textDocument/publishDiagnostics")["params"], &p)
	if len(p.Diagnostics) == 0 {
		t.Fatal("the second include of one module produced no diagnostic")
	}
	for _, d := range p.Diagnostics {
		if d.Severity != severityWarning {
			t.Errorf("severity = %d, want %d (a warning): %q", d.Severity, severityWarning, d.Message)
		}
	}
}

// Hover on nothing is nothing: a cursor in whitespace, on a number, past the end of a
// line. Each of these is a null answer rather than a made-up one.
func TestHoverOnNothing(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		open(uri, "x = 42\n"),
		at("textDocument/hover", uri, 2, 0, 2),   // the '='
		at("textDocument/hover", uri, 3, 0, 5),   // inside the number
		at("textDocument/hover", uri, 4, 9, 0),   // past the end of the document
		at("textDocument/hover", uri, 5, 0, 200), // past the end of the line
		msg("exit", nil, nil),
	)
	for _, id := range []int{2, 3, 4, 5} {
		if raw := s.reply(float64(id))["result"]; string(raw) != "null" {
			t.Errorf("hover %d = %s, want null", id, raw)
		}
	}
}

// Every request against a document the server has never been told about answers emptily
// rather than failing: a client may ask about a buffer it has just closed.
func TestRequestsAboutAnUnknownDocument(t *testing.T) {
	s := run(t,
		msg("initialize", 1, map[string]any{}),
		at("textDocument/hover", uri, 2, 0, 0),
		at("textDocument/definition", uri, 3, 0, 0),
		at("textDocument/completion", uri, 4, 0, 0),
		msg("textDocument/documentSymbol", 5, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		msg("textDocument/formatting", 6, map[string]any{"textDocument": map[string]any{"uri": uri}}),
		msg("exit", nil, nil),
	)
	for _, id := range []int{2, 3, 4, 5, 6} {
		if _, bad := s.reply(float64(id))["error"]; bad {
			t.Errorf("request %d failed instead of answering emptily", id)
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	for _, path := range []string{"/tmp/a b/файл.mzs", "/tmp/plain.mzs"} {
		if got := uriToPath(pathToURI(path)); got != path {
			t.Errorf("uriToPath(pathToURI(%q)) = %q", path, got)
		}
	}
	if got := uriToPath("untitled:Untitled-1"); got != "" {
		t.Errorf("a non-file URI has no path, got %q", got)
	}
}
