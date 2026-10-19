package lsp

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

func TestFrameRoundTrip(t *testing.T) {
	var wire bytes.Buffer
	w := newFrameWriter(&wire)
	body := json.RawMessage(`{"nested":true}`)
	if err := w.write(&message{JSONRPC: "2.0", ID: json.RawMessage("7"), Result: body}); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := newFrameReader(&wire).read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m.JSONRPC != "2.0" || string(m.ID) != "7" || string(m.Result) != string(body) {
		t.Fatalf("message = %+v", m)
	}
}

func TestFrameReadsOtherHeadersInAnyCase(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"ping"}`
	raw := "content-length: " + strconv.Itoa(len(body)) + "\r\nX-Test: yes\r\n\r\n" + body
	m, err := newFrameReader(iotest.OneByteReader(strings.NewReader(raw))).read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m.Method != "ping" {
		t.Fatalf("method = %q", m.Method)
	}
}

func TestFrameReadsEndOfStream(t *testing.T) {
	if _, err := newFrameReader(strings.NewReader("")).read(); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestFrameRejectsMissingLength(t *testing.T) {
	raw := "X-Test: yes\r\n\r\n{}"
	if _, err := newFrameReader(strings.NewReader(raw)).read(); err == nil {
		t.Fatal("read accepted a frame without Content-Length")
	}
}
