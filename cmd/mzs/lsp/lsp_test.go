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
		at("textDocument/definition", mainURI, 2, 0, 9),
		msg("exit", nil, nil),
	)
	var loc location
	into(t, s.reply(2)["result"], &loc)
	if loc.URI != pathToURI(lib) {
		t.Errorf("definition uri = %q, want %q", loc.URI, pathToURI(lib))
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
