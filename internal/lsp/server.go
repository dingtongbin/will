package lsp

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/will-lang/will/internal/diag"
)

// position is one LSP position: a zero-based line and a zero-based UTF-16
// column. will source is ASCII by the lexical rules, so a byte column and a
// UTF-16 column are the same number.
type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspRange is one LSP range. End is exclusive.
type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// lspDiagnostic is the LSP form of one compiler diagnostic.
type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Code     string   `json:"code"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

// publishDiagnosticsParams is the payload of textDocument/publishDiagnostics.
// Version is omitted when it is zero, which is also the case for the empty
// list sent after didClose.
type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Version     int             `json:"version,omitempty"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

// showMessageParams is the payload of window/showMessage.
type showMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

// serverCapabilities declares only what this server implements. Diagnostics
// are pushed, so no diagnosticProvider is announced.
type serverCapabilities struct {
	TextDocumentSync int `json:"textDocumentSync"`
}

// serverInfo identifies the server. Version stays empty because the language
// server does not carry a separate release number.
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange                 `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

// contentChange is one entry of a didChange notification. Range is kept raw:
// the server declares full sync, so any range present is a client error that
// must be reported instead of silently misapplied.
type contentChange struct {
	Range json.RawMessage `json:"range,omitempty"`
	Text  string          `json:"text"`
}

// document is one open buffer.
type document struct {
	text    string
	version int
}

// server answers one LSP connection. Requests are handled in arrival order;
// there is no request concurrency.
type server struct {
	writer       *frameWriter
	docs         map[string]*document
	shuttingDown bool
}

// Serve runs the language server over conn until the client sends exit or
// closes its side of the stream. The caller owns conn and closes it.
func Serve(conn io.ReadWriteCloser) error {
	s := &server{
		writer: newFrameWriter(conn),
		docs:   map[string]*document{},
	}
	reader := newFrameReader(conn)
	for {
		m, err := reader.read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		done, err := s.handleMessage(m)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// handleMessage keeps one bad message from ending the connection. A panic is a
// defect in the server, but dropping the client leaves the editor without any
// signal, so it becomes an internal error response instead.
func (s *server) handleMessage(m *message) (done bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			done, err = false, nil
			if m.isRequest() {
				_ = s.respondError(m.ID, codeInternalError, "internal error")
			} else {
				_ = s.notifyError("internal error")
			}
		}
	}()
	return s.handle(m)
}

// handle dispatches one message. The bool result reports that the exit
// notification was received.
func (s *server) handle(m *message) (bool, error) {
	if m.Method == "" {
		// A response to a server-initiated request. The server sends none.
		return false, nil
	}
	if m.Method == "exit" {
		if m.isRequest() {
			if err := s.respond(m.ID, json.RawMessage("null")); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if s.shuttingDown {
		if m.isRequest() {
			return false, s.respondError(m.ID, codeInvalidRequest, "server is shutting down")
		}
		return false, nil
	}

	switch m.Method {
	case "initialize":
		result, err := json.Marshal(initializeResult{
			Capabilities: serverCapabilities{TextDocumentSync: 1},
			ServerInfo:   serverInfo{Name: "will", Version: ""},
		})
		if err != nil {
			return false, err
		}
		return false, s.respond(m.ID, result)
	case "initialized":
		return false, s.ack(m)
	case "shutdown":
		s.shuttingDown = true
		return false, s.respond(m.ID, json.RawMessage("null"))
	case "textDocument/didOpen":
		return s.handleDidOpen(m)
	case "textDocument/didChange":
		return s.handleDidChange(m)
	case "textDocument/didClose":
		return s.handleDidClose(m)
	}

	if m.isRequest() {
		return false, s.respondError(m.ID, codeMethodNotFound, "method not found: "+m.Method)
	}
	return false, nil
}

func (s *server) handleDidOpen(m *message) (bool, error) {
	var p didOpenParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return false, s.paramsError(m, err)
	}
	if p.TextDocument.URI == "" {
		return false, s.paramsError(m, errors.New("missing document URI"))
	}
	s.docs[p.TextDocument.URI] = &document{
		text:    p.TextDocument.Text,
		version: p.TextDocument.Version,
	}
	if err := s.publishDiagnostics(p.TextDocument.URI); err != nil {
		return false, err
	}
	return false, s.ack(m)
}

func (s *server) handleDidChange(m *message) (bool, error) {
	var p didChangeParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return false, s.paramsError(m, err)
	}
	doc, ok := s.docs[p.TextDocument.URI]
	if !ok {
		return false, s.stateError(m, "document is not open: "+p.TextDocument.URI)
	}
	for _, change := range p.ContentChanges {
		if len(change.Range) > 0 && string(change.Range) != "null" {
			return false, s.stateError(m,
				"incremental changes are not supported; send the full document text")
		}
	}
	if len(p.ContentChanges) == 0 {
		return false, s.paramsError(m, errors.New("contentChanges is empty"))
	}
	// The server declares full sync, so the last change carries the whole
	// document. Earlier entries, if any, are superseded by it.
	doc.text = p.ContentChanges[len(p.ContentChanges)-1].Text
	doc.version = p.TextDocument.Version
	if err := s.publishDiagnostics(p.TextDocument.URI); err != nil {
		return false, err
	}
	return false, s.ack(m)
}

func (s *server) handleDidClose(m *message) (bool, error) {
	var p didCloseParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return false, s.paramsError(m, err)
	}
	delete(s.docs, p.TextDocument.URI)
	if err := s.writeDiagnostics(p.TextDocument.URI, 0, nil); err != nil {
		return false, err
	}
	return false, s.ack(m)
}

// publishDiagnostics analyzes one open document and sends the result under the
// same URI the client used. An infrastructure failure is reported to the
// client and clears the document's diagnostics rather than leaving a stale
// list on screen.
func (s *server) publishDiagnostics(uri string) error {
	doc, ok := s.docs[uri]
	if !ok {
		return s.writeDiagnostics(uri, 0, nil)
	}
	path, ok := filePathFromURI(uri)
	if !ok {
		return s.writeDiagnostics(uri, doc.version, nil)
	}
	ds, err := analyzeText(path, doc.text)
	if err != nil {
		if err := s.notifyError(err.Error()); err != nil {
			return err
		}
		return s.writeDiagnostics(uri, doc.version, nil)
	}
	return s.writeDiagnostics(uri, doc.version, ds)
}

func (s *server) writeDiagnostics(uri string, version int, ds []diag.Diagnostic) error {
	items := make([]lspDiagnostic, 0, len(ds))
	for _, d := range ds {
		if !d.Pos.IsValid() {
			continue
		}
		items = append(items, lspDiagnostic{
			Range:    rangeOf(d),
			Severity: severityOf(d.Severity),
			Code:     d.Code,
			Source:   "will",
			Message:  d.Message,
		})
	}
	params, err := json.Marshal(publishDiagnosticsParams{URI: uri, Version: version, Diagnostics: items})
	if err != nil {
		return err
	}
	return s.writer.write(&message{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params:  params,
	})
}

// rangeOf maps a compiler position to an LSP range. Compiler columns are
// one-based bytes; LSP lines and columns are zero-based, and ASCII source
// makes the byte and UTF-16 columns identical. A diagnostic without an end
// covers one character so editors can draw it.
func rangeOf(d diag.Diagnostic) lspRange {
	start := position{Line: d.Pos.Line - 1, Character: d.Pos.Col - 1}
	end := start
	if d.End.IsValid() && d.End.File == d.Pos.File &&
		(d.End.Line > d.Pos.Line || d.End.Line == d.Pos.Line && d.End.Col > d.Pos.Col) {
		end = position{Line: d.End.Line - 1, Character: d.End.Col - 1}
	} else {
		end.Character++
	}
	return lspRange{Start: start, End: end}
}

// severityOf maps a compiler severity to the LSP numbering: Error is 1 and
// Warning is 2.
func severityOf(s diag.Severity) int {
	if s == diag.Warning {
		return 2
	}
	return 1
}

func (s *server) respond(id json.RawMessage, result json.RawMessage) error {
	if len(id) == 0 || string(id) == "null" {
		return nil
	}
	return s.writer.write(&message{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) respondError(id json.RawMessage, code int, text string) error {
	if len(id) == 0 || string(id) == "null" {
		return nil
	}
	return s.writer.write(&message{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: text},
	})
}

// ack answers a request that the protocol defines as a notification. A real
// client sends no ID, so this normally writes nothing.
func (s *server) ack(m *message) error {
	if !m.isRequest() {
		return nil
	}
	return s.respond(m.ID, json.RawMessage("null"))
}

// paramsError answers malformed parameters: a request gets InvalidParams, a
// notification gets a visible message because nothing else can carry it.
func (s *server) paramsError(m *message, err error) error {
	if m.isRequest() {
		return s.respondError(m.ID, codeInvalidParams, "invalid params: "+err.Error())
	}
	return s.notifyError("invalid params: " + err.Error())
}

// stateError reports a request that cannot be applied in the current state.
func (s *server) stateError(m *message, text string) error {
	if m.isRequest() {
		return s.respondError(m.ID, codeInvalidParams, text)
	}
	return s.notifyError(text)
}

// notifyError sends an error through window/showMessage. Type 1 is the LSP
// Error severity.
func (s *server) notifyError(text string) error {
	if !strings.HasPrefix(text, "will lsp:") {
		text = "will lsp: " + text
	}
	params, err := json.Marshal(showMessageParams{Type: 1, Message: text})
	if err != nil {
		return err
	}
	return s.writer.write(&message{
		JSONRPC: "2.0",
		Method:  "window/showMessage",
		Params:  params,
	})
}
