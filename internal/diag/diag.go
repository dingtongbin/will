// Package diag collects compiler diagnostics and renders the canonical human format.
package diag

import (
	"sort"
	"strings"

	"github.com/will-lang/will/internal/source"
)

// Severity is the impact of a diagnostic.
type Severity int

const (
	// Error means the program is not valid and no artifact may be produced.
	Error Severity = iota
	// Warning means the program is valid but likely differs from the author's intent.
	Warning
)

// String returns the lowercase severity name used by machine-readable output.
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is one compiler message with a stable code and a help line.
type Diagnostic struct {
	Pos      source.Pos
	Severity Severity
	Code     string
	Message  string
	Help     string
}

// Bag accumulates diagnostics and returns them in a deterministic order.
type Bag struct {
	items []Diagnostic
}

// Add appends d to the bag.
func (b *Bag) Add(d Diagnostic) {
	b.items = append(b.items, d)
}

// Error records an error with a source position.
func (b *Bag) Error(pos source.Pos, code, message, help string) {
	b.Add(Diagnostic{Pos: pos, Severity: Error, Code: code, Message: message, Help: help})
}

// Warning records a warning with a source position.
func (b *Bag) Warning(pos source.Pos, code, message, help string) {
	b.Add(Diagnostic{Pos: pos, Severity: Warning, Code: code, Message: message, Help: help})
}

// Diagnostics returns a sorted, de-duplicated copy of the recorded messages.
func (b *Bag) Diagnostics() []Diagnostic {
	out := make([]Diagnostic, len(b.items))
	copy(out, b.items)
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Pos.File != c.Pos.File {
			return a.Pos.File < c.Pos.File
		}
		if a.Pos.Line != c.Pos.Line {
			return a.Pos.Line < c.Pos.Line
		}
		if a.Pos.Col != c.Pos.Col {
			return a.Pos.Col < c.Pos.Col
		}
		if a.Code != c.Code {
			return a.Code < c.Code
		}
		return a.Message < c.Message
	})
	out = dedupe(out)
	return out
}

// HasErrors reports whether at least one error was recorded.
func (b *Bag) HasErrors() bool {
	for _, d := range b.items {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

func dedupe(in []Diagnostic) []Diagnostic {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, d := range in[1:] {
		last := out[len(out)-1]
		if d == last {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Format renders one diagnostic in the canonical human format.
func Format(d Diagnostic) string {
	var b strings.Builder
	if d.Pos.IsValid() {
		b.WriteString(d.Pos.String())
		b.WriteString(": ")
	}
	b.WriteString(d.Code)
	b.WriteString(" ")
	b.WriteString(d.Message)
	b.WriteByte('\n')
	if d.Help != "" {
		b.WriteString("    help: ")
		b.WriteString(d.Help)
		b.WriteByte('\n')
	}
	return b.String()
}

// FormatAll renders every diagnostic in order.
func FormatAll(ds []Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(Format(d))
	}
	return b.String()
}
