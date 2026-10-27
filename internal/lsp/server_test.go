package lsp

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/will-lang/will/internal/diag"
)

const badSource = `def f() {
    var x: i32 = "s"
    _ = x
}
`

const goodSource = `def f() {
    var x: i32 = 1
    _ = x
}
`

// client drives a server over an in-memory pipe.
type client struct {
	conn    net.Conn
	server  net.Conn
	reader  *frameReader
	writer  *frameWriter
	done    chan error
	stopped bool
}

func startServer(t *testing.T) *client {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(serverConn) }()
	if err := clientConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	c := &client{
		conn:   clientConn,
		server: serverConn,
		reader: newFrameReader(clientConn),
		writer: newFrameWriter(clientConn),
		done:   done,
	}
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
		if c.stopped {
			return
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return c
}

func (c *client) send(t *testing.T, m *message) {
	t.Helper()
	if err := c.writer.write(m); err != nil {
		t.Fatalf("write %s: %v", m.Method, err)
	}
}

func (c *client) read(t *testing.T) *message {
	t.Helper()
	m, err := c.reader.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func (c *client) request(t *testing.T, id int, method string, params any) *message {
	t.Helper()
	rawID, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal id: %v", err)
	}
	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
	}
	c.send(t, &message{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams})
	return c.read(t)
}

func (c *client) notify(t *testing.T, method string, params any) {
	t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
	}
	c.send(t, &message{JSONRPC: "2.0", Method: method, Params: rawParams})
}

func (c *client) wait(t *testing.T) error {
	t.Helper()
	c.stopped = true
	select {
	case err := <-c.done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
		return nil
	}
}

func (c *client) publish(t *testing.T) publishDiagnosticsParams {
	t.Helper()
	m := c.read(t)
	if m.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("method = %q, want publishDiagnostics", m.Method)
	}
	var p publishDiagnosticsParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatalf("unmarshal diagnostics: %v", err)
	}
	return p
}

func fileURI(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func TestInitializeDeclaresOnlyFullSync(t *testing.T) {
	c := startServer(t)
	resp := c.request(t, 1, "initialize", map[string]any{
		"processId":    nil,
		"capabilities": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("initialize error = %+v", resp.Error)
	}
	var result struct {
		Capabilities struct {
			TextDocumentSync int `json:"textDocumentSync"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Capabilities.TextDocumentSync != 1 {
		t.Errorf("textDocumentSync = %d, want 1", result.Capabilities.TextDocumentSync)
	}
	if result.ServerInfo.Name != "will" {
		t.Errorf("serverInfo.name = %q, want will", result.ServerInfo.Name)
	}
	if result.ServerInfo.Version != "" {
		t.Errorf("serverInfo.version = %q, want empty", result.ServerInfo.Version)
	}
}

func TestRangeOfAPositionlessDiagnostic(t *testing.T) {
	got := rangeOf(diag.Diagnostic{Code: "B0002", Message: "bad module"})
	if got != (lspRange{}) {
		t.Fatalf("range = %+v, want the zero range", got)
	}
}

func TestDidOpenPublishesTypeError(t *testing.T) {
	c := startServer(t)
	path := filepath.Join(t.TempDir(), "bad.will")
	uri := fileURI(t, path)

	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": "will",
			"version":    1,
			"text":       badSource,
		},
	})

	p := c.publish(t)
	if p.URI != uri {
		t.Fatalf("uri = %q, want %q", p.URI, uri)
	}
	if len(p.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one", p.Diagnostics)
	}
	d := p.Diagnostics[0]
	if d.Severity != 1 {
		t.Errorf("severity = %d, want 1", d.Severity)
	}
	if d.Code != "T0001" {
		t.Errorf("code = %q, want T0001", d.Code)
	}
	if d.Source != "will" {
		t.Errorf("source = %q, want will", d.Source)
	}
	if d.Message != "type mismatch: got str, want i32\nhelp: convert explicitly with T(x)" {
		t.Errorf("message = %q", d.Message)
	}
	wantRange := lspRange{
		Start: position{Line: 1, Character: 17},
		End:   position{Line: 1, Character: 18},
	}
	if d.Range != wantRange {
		t.Errorf("range = %+v, want %+v", d.Range, wantRange)
	}
}

func TestDidChangeReplacesTheWholeDocument(t *testing.T) {
	c := startServer(t)
	path := filepath.Join(t.TempDir(), "change.will")
	uri := fileURI(t, path)

	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "will", "version": 1, "text": badSource},
	})
	if p := c.publish(t); len(p.Diagnostics) != 1 {
		t.Fatalf("opened diagnostics = %+v, want one", p.Diagnostics)
	}

	c.notify(t, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": goodSource}},
	})
	if p := c.publish(t); len(p.Diagnostics) != 0 {
		t.Fatalf("changed diagnostics = %+v, want none", p.Diagnostics)
	}
}

func TestDidChangeRejectsRanges(t *testing.T) {
	c := startServer(t)
	path := filepath.Join(t.TempDir(), "range.will")
	uri := fileURI(t, path)

	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "will", "version": 1, "text": goodSource},
	})
	if p := c.publish(t); len(p.Diagnostics) != 0 {
		t.Fatalf("opened diagnostics = %+v, want none", p.Diagnostics)
	}

	c.notify(t, "textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": 0, "character": 1},
			},
			"text": "x",
		}},
	})
	m := c.read(t)
	if m.Method != "window/showMessage" {
		t.Fatalf("method = %q, want window/showMessage", m.Method)
	}
	var msg showMessageParams
	if err := json.Unmarshal(m.Params, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type != 1 || !strings.Contains(msg.Message, "incremental") {
		t.Fatalf("message = %+v", msg)
	}
}

func TestDidCloseClearsDiagnostics(t *testing.T) {
	c := startServer(t)
	path := filepath.Join(t.TempDir(), "close.will")
	uri := fileURI(t, path)

	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "will", "version": 1, "text": badSource},
	})
	if p := c.publish(t); len(p.Diagnostics) != 1 {
		t.Fatalf("opened diagnostics = %+v, want one", p.Diagnostics)
	}

	c.notify(t, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	p := c.publish(t)
	if p.URI != uri {
		t.Fatalf("uri = %q, want %q", p.URI, uri)
	}
	if len(p.Diagnostics) != 0 {
		t.Fatalf("closed diagnostics = %+v, want none", p.Diagnostics)
	}
}

func TestModuleDocumentUsesInMemoryText(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "will.mod"), []byte("module example.com/app\nwill 1\n"), 0o644); err != nil {
		t.Fatalf("write will.mod: %v", err)
	}
	path := filepath.Join(root, "main.will")
	// The disk copy is valid; the buffer carries the type error, so a passing
	// test proves the analysis used the buffer rather than the file.
	if err := os.WriteFile(path, []byte(goodSource), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	c := startServer(t)
	c.notify(t, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI(t, path),
			"languageId": "will",
			"version":    1,
			"text":       badSource,
		},
	})
	p := c.publish(t)
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != "T0001" {
		t.Fatalf("diagnostics = %+v, want one T0001", p.Diagnostics)
	}
}

func TestUnknownRequestIsMethodNotFound(t *testing.T) {
	c := startServer(t)
	resp := c.request(t, 5, "workspace/unknown", nil)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("error = %+v, want MethodNotFound", resp.Error)
	}
}

func TestShutdownThenExitStopsServe(t *testing.T) {
	c := startServer(t)
	c.request(t, 1, "initialize", map[string]any{"capabilities": map[string]any{}})
	resp := c.request(t, 2, "shutdown", nil)
	if resp.Error != nil || string(resp.Result) != "null" {
		t.Fatalf("shutdown = %+v", resp)
	}
	c.notify(t, "exit", nil)
	if err := c.wait(t); err != nil {
		t.Fatalf("Serve returned %v", err)
	}
}
