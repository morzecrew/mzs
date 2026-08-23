package lsp

// The slice of the Language Server Protocol this server speaks. Only the fields that are
// read or written appear: an unknown field on the way in is dropped by encoding/json, and
// an absent field on the way out is the protocol's own default.

type position struct {
	Line      int `json:"line"`      // zero-based
	Character int `json:"character"` // zero-based, in UTF-16 code units
}

type rng struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string `json:"uri"`
	Range rng    `json:"range"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
	Text    string `json:"text"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type contentChange struct {
	// Range is absent for a full-document change, which is the only kind this server
	// asks for: `TextDocumentSyncKind.Full`. Whole-file compilation is what produces the
	// diagnostics anyway, so an incremental update would be bookkeeping with nothing
	// downstream to spend it on.
	Text string `json:"text"`
}

type didChangeParams struct {
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	Changes      []contentChange                 `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

// textDocumentParams is every request whose subject is the whole document: formatting and
// the outline. Neither reads an option the client sends with it.
type textDocumentParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

// diagnosticSeverity values the protocol defines.
const (
	severityError   = 1
	severityWarning = 2
)

type diagnostic struct {
	Range    rng    `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type markupContent struct {
	Kind  string `json:"kind"` // always "markdown" here
	Value string `json:"value"`
}

type hover struct {
	Contents markupContent `json:"contents"`
	Range    *rng          `json:"range,omitempty"`
}

// completionItemKind values, of which this server uses five.
const (
	kindFunction = 3
	kindMethod   = 2
	kindModule   = 9
	kindKeyword  = 14
	kindVariable = 6
)

type completionItem struct {
	Label         string         `json:"label"`
	Kind          int            `json:"kind,omitempty"`
	Detail        string         `json:"detail,omitempty"`
	Documentation *markupContent `json:"documentation,omitempty"`
	SortText      string         `json:"sortText,omitempty"`
}

type textEdit struct {
	Range   rng    `json:"range"`
	NewText string `json:"newText"`
}

// initializeResult tells the client what this server can do. Everything listed here is
// implemented; nothing implemented is left out.
type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapabilities struct {
	TextDocumentSync           textDocumentSyncOptions `json:"textDocumentSync"`
	HoverProvider              bool                    `json:"hoverProvider"`
	DefinitionProvider         bool                    `json:"definitionProvider"`
	DocumentFormattingProvider bool                    `json:"documentFormattingProvider"`
	CompletionProvider         completionOptions       `json:"completionProvider"`
	DocumentSymbolProvider     bool                    `json:"documentSymbolProvider"`
}

type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // 1 = full text on every change
	Save      bool `json:"save"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// documentSymbol is the outline: what a file declares, in the order it declares it.
type documentSymbol struct {
	Name           string `json:"name"`
	Detail         string `json:"detail,omitempty"`
	Kind           int    `json:"kind"`
	Range          rng    `json:"range"`
	SelectionRange rng    `json:"selectionRange"`
}

// symbolKind values used by the outline.
const (
	symbolFunction = 12
	symbolStruct   = 23
	symbolModule   = 2
	symbolVariable = 13
)
