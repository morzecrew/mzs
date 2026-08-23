// Package lsp is `mzs lsp`: the compiler's diagnostics, and what it knows about a name,
// spoken over the Language Server Protocol so an editor can show them.
//
// The best part of this language is its error messages (§17), and until now they were
// visible only to whoever ran the command. Everything here exists to move that one thing
// into the editor; the rest — hover, go-to-definition, completion, formatting — is the
// same knowledge answered to a different question.
//
// It is JSON-RPC 2.0 over stdio, framed with `Content-Length` headers, written on
// `encoding/json` alone. No dependency is added for it, which is I1 and not an accident:
// the protocol is a header, a length and a JSON object, and a language whose promise is
// "the standard library and nothing else" cannot buy a client for that.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// request is one incoming JSON-RPC message. A notification is a request with no id, and
// the difference decides one thing only: whether an answer is written back.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// A JSON-RPC response carries exactly one of `result` and `error`, and a success with
// nothing to say still carries `result: null` — which is why these are two types rather
// than one with two omitempty fields. `shutdown` is the reply that shape is about: its
// result is null, and a client entitled to reject a response with neither member would
// be right to reject it.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// A response always carries an id, and a parse error — the one answer with no request to
// attach to — carries `null`, which is what a nil RawMessage marshals to. Neither member
// is omitted, because "absent" and "null" are different words in this protocol.
type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   responseError   `json:"error"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// notification is what the server sends unasked: diagnostics, mostly.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// The JSON-RPC error codes this server answers with.
const (
	codeParseError    = -32700
	codeInvalid       = -32600
	codeMethodMissing = -32601
)

// conn is the framed connection. Writes are serialised because diagnostics are published
// from wherever a document changed, and two writers interleaving would produce a message
// no client can read.
type conn struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{r: bufio.NewReaderSize(r, 64*1024), w: w}
}

// read returns the next message, or io.EOF when the client has closed the stream.
func (c *conn) read() (*request, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // the blank line that ends the header block
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("a message with no Content-Length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	var req request
	if err := json.Unmarshal(buf, &req); err != nil {
		// The frame was read whole, so the stream is still aligned and the next message
		// is readable: this is one bad payload, not a broken transport. Saying so lets
		// the caller answer with a parse error and keep the editor's server alive, which
		// a `return nil, err` would not — a server that exits on one malformed message
		// takes the diagnostics down with it until the editor is restarted.
		return nil, fmt.Errorf("%w: %v", errBadPayload, err)
	}
	return &req, nil
}

// errBadPayload is a frame whose bytes arrived intact and whose JSON did not.
var errBadPayload = errors.New("malformed JSON-RPC payload")

func (c *conn) write(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.w.Write(b)
	return err
}

func (c *conn) reply(id json.RawMessage, result any) error {
	return c.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (c *conn) replyError(id json.RawMessage, code int, msg string) error {
	return c.write(errorResponse{JSONRPC: "2.0", ID: id, Error: responseError{Code: code, Message: msg}})
}

func (c *conn) notify(method string, params any) error {
	return c.write(notification{JSONRPC: "2.0", Method: method, Params: params})
}
