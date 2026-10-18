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

// Note is one extra location attached to a diagnostic, used when a rule
// needs two real positions.
type Note struct {
	Pos     source.Pos
	Message string
}

// Diagnostic is one compiler message with a stable code and a help line.
type Diagnostic struct {
	Pos      source.Pos
	End      source.Pos
	Severity Severity
	Code     string
	Message  string
	Help     string
	Notes    []Note
}

// Bag accumulates diagnostics and returns them in a deterministic order.
type Bag struct {
	items []Diagnostic
}

// Add appends d to the bag.
func (b *Bag) Add(d Diagnostic) {
	b.items = append(b.items, d)
}

// AddAll appends every diagnostic in ds.
func (b *Bag) AddAll(ds []Diagnostic) {
	b.items = append(b.items, ds...)
}

// Error records an error with a source position.
func (b *Bag) Error(pos source.Pos, code, message, help string) {
	b.Add(Diagnostic{Pos: pos, End: pos, Severity: Error, Code: code, Message: message, Help: help})
}

// Warning records a warning with a source position.
func (b *Bag) Warning(pos source.Pos, code, message, help string) {
	b.Add(Diagnostic{Pos: pos, End: pos, Severity: Warning, Code: code, Message: message, Help: help})
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
		if sameDiagnostic(d, out[len(out)-1]) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func sameDiagnostic(a, b Diagnostic) bool {
	if a.Pos != b.Pos || a.End != b.End || a.Severity != b.Severity ||
		a.Code != b.Code || a.Message != b.Message || a.Help != b.Help {
		return false
	}
	if len(a.Notes) != len(b.Notes) {
		return false
	}
	for i := range a.Notes {
		if a.Notes[i] != b.Notes[i] {
			return false
		}
	}
	return true
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
	for _, n := range d.Notes {
		if n.Pos.IsValid() {
			b.WriteString(n.Pos.String())
			b.WriteString(": ")
		}
		b.WriteString("note: ")
		b.WriteString(n.Message)
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
