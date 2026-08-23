package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"mzs/internal/printer"
)

// Serve runs the language server over r and w until the client closes the stream or
// asks it to exit. It returns nil for a clean shutdown.
//
// Version is what the client shows in its server list; the CLI passes the binary's.
func Serve(r io.Reader, w io.Writer, version string) error {
	s := &server{conn: newConn(r, w), docs: map[string]*document{}, version: version}
	return s.loop()
}

type server struct {
	conn    *conn
	version string

	mu   sync.Mutex
	docs map[string]*document

	// shutdown records the client's `shutdown` request. The protocol says a server must
	// refuse work after it and wait for `exit`, which is the difference between "we are
	// finishing" and "we are gone".
	shutdown bool
}

// errExit ends the loop on the client's `exit` notification.
var errExit = errors.New("exit")

func (s *server) loop() error {
	for {
		req, err := s.conn.read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// The client went away without saying goodbye, which is what happens
				// when an editor is killed. There is nothing left to answer to.
				return nil
			}
			return err
		}
		if err := s.handle(req); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			return err
		}
	}
}

func (s *server) handle(req *request) error {
	if s.shutdown && req.Method != "exit" {
		// Between `shutdown` and `exit` the server does no more work (LSP lifecycle).
		if len(req.ID) == 0 {
			return nil
		}
		return s.conn.replyError(req.ID, codeInvalid, "mzs lsp: shutdown has been requested")
	}
	switch req.Method {
	case "initialize":
		return s.conn.reply(req.ID, initializeResult{
			Capabilities: serverCapabilities{
				TextDocumentSync: textDocumentSyncOptions{
					OpenClose: true,
					Change:    1, // the whole text on every change
					Save:      true,
				},
				HoverProvider:              true,
				DefinitionProvider:         true,
				DocumentFormattingProvider: true,
				DocumentSymbolProvider:     true,
				CompletionProvider:         completionOptions{TriggerCharacters: []string{"."}},
			},
			ServerInfo: serverInfo{Name: "mzs", Version: s.version},
		})
	case "initialized":
		return nil
	case "shutdown":
		s.shutdown = true
		return s.conn.reply(req.ID, nil)
	case "exit":
		return errExit

	case "textDocument/didOpen":
		var p didOpenParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := newDocument(p.TextDocument.URI, p.TextDocument.Text, p.TextDocument.Version)
		s.put(d)
		return s.publish(d)

	case "textDocument/didChange":
		var p didChangeParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			d = newDocument(p.TextDocument.URI, "", p.TextDocument.Version)
			s.put(d)
		}
		if n := len(p.Changes); n > 0 {
			// Full sync: the last change carries the whole document.
			d.setText(p.Changes[n-1].Text)
		}
		d.version = p.TextDocument.Version
		return s.publish(d)

	case "textDocument/didSave":
		var p didSaveParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return nil
		}
		if p.Text != nil {
			d.setText(*p.Text)
		}
		return s.publish(d)

	case "textDocument/didClose":
		var p didCloseParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		s.drop(p.TextDocument.URI)
		// An empty list is how the protocol says "no diagnostics here any more"; without
		// it the editor keeps showing the last ones for a file nobody has open.
		return s.conn.notify("textDocument/publishDiagnostics",
			publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []diagnostic{}})

	case "textDocument/hover":
		var p textDocumentPositionParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return s.conn.reply(req.ID, nil)
		}
		w, ok := d.wordAt(p.Position)
		if !ok {
			return s.conn.reply(req.ID, nil)
		}
		text, ok := hoverFor(d, w)
		if !ok {
			return s.conn.reply(req.ID, nil)
		}
		span := w.span
		return s.conn.reply(req.ID, hover{
			Contents: markupContent{Kind: "markdown", Value: text},
			Range:    &span,
		})

	case "textDocument/definition":
		var p textDocumentPositionParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return s.conn.reply(req.ID, nil)
		}
		w, ok := d.wordAt(p.Position)
		if !ok {
			return s.conn.reply(req.ID, nil)
		}
		loc, ok := definitionFor(d, w)
		if !ok {
			return s.conn.reply(req.ID, nil)
		}
		return s.conn.reply(req.ID, loc)

	case "textDocument/completion":
		var p textDocumentPositionParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return s.conn.reply(req.ID, []completionItem{})
		}
		return s.conn.reply(req.ID, complete(d, p.Position))

	case "textDocument/documentSymbol":
		var p textDocumentParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return s.conn.reply(req.ID, []documentSymbol{})
		}
		return s.conn.reply(req.ID, symbolsOf(d))

	case "textDocument/formatting":
		var p textDocumentParams
		if err := unmarshal(req.Params, &p); err != nil {
			return s.badParams(req, err)
		}
		d := s.get(p.TextDocument.URI)
		if d == nil {
			return s.conn.reply(req.ID, []textEdit{})
		}
		out, err := printer.Format(d.name(), d.text)
		if err != nil {
			// A file that does not parse has no formatted form, and the diagnostics
			// already say why. Answering with no edits beats an error dialog on every
			// save of a file being written.
			return s.conn.reply(req.ID, []textEdit{})
		}
		if out == d.text {
			return s.conn.reply(req.ID, []textEdit{})
		}
		return s.conn.reply(req.ID, []textEdit{{
			Range:   rng{Start: position{}, End: d.endPosition()},
			NewText: out,
		}})
	}

	if len(req.ID) == 0 {
		// An unknown *notification* is ignored: the protocol is full of them and a
		// server is not obliged to know one it did not ask for.
		return nil
	}
	return s.conn.replyError(req.ID, codeMethodMissing, "mzs lsp: no method "+req.Method)
}

func (s *server) badParams(req *request, err error) error {
	if len(req.ID) == 0 {
		return nil
	}
	return s.conn.replyError(req.ID, codeParseError, fmt.Sprintf("mzs lsp: bad params for %s: %v", req.Method, err))
}

func (s *server) publish(d *document) error {
	return s.conn.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         d.uri,
		Version:     d.version,
		Diagnostics: diagnose(d),
	})
}

func (s *server) get(uri string) *document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[uri]
}

func (s *server) put(d *document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[d.uri] = d
}

func (s *server) drop(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, uri)
}

func unmarshal(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
