// Package lsp implements the will language server over the Language Server
// Protocol 3.17.
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

// JSON-RPC error codes. Only the codes the server can actually produce are
// listed.
const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// maxMessageBytes bounds one body so a malformed header cannot exhaust memory.
const maxMessageBytes = 64 << 20

// rpcError is the error member of a JSON-RPC response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// message is one JSON-RPC request, response, or notification. ID holds the
// original JSON token so numeric and string identifiers both round-trip.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// isRequest reports whether m expects a response. A notification has no ID.
func (m *message) isRequest() bool {
	return len(m.ID) > 0 && string(m.ID) != "null"
}

// frameReader reads Content-Length framed JSON-RPC messages.
type frameReader struct {
	r *bufio.Reader
}

// newFrameReader wraps r in a frame reader.
func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: bufio.NewReader(r)}
}

// read returns the next message. It returns io.EOF when the stream ends at a
// frame boundary, so a closed client is a clean shutdown rather than an error.
func (f *frameReader) read() (*message, error) {
	length := -1
	for {
		line, err := f.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("lsp: invalid Content-Length %q", value)
		}
		if n > maxMessageBytes {
			return nil, fmt.Errorf("lsp: message body of %d bytes is too large", n)
		}
		length = n
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(f.r, body); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("lsp: invalid JSON body: %w", err)
	}
	return &m, nil
}

// frameWriter writes Content-Length framed JSON-RPC messages. The mutex keeps
// a response and a notification from interleaving on the wire.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// newFrameWriter wraps w in a frame writer.
func newFrameWriter(w io.Writer) *frameWriter {
	return &frameWriter{w: w}
}

// write sends one message. The body is written in one call after the header so
// a reader that blocks on the header cannot observe a partial body.
func (f *frameWriter) write(m *message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := fmt.Fprintf(f.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = f.w.Write(body)
	return err
}
