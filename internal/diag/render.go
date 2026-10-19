package diag

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Build is the build identity attached to every machine-readable diagnostic.
type Build struct {
	Compiler string `json:"compiler"`
	Target   string `json:"target"`
	Opt      string `json:"opt"`
}

type jsonNote struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
}

type jsonDiagnostic struct {
	File     string     `json:"file"`
	Line     int        `json:"line"`
	Col      int        `json:"col"`
	EndLine  int        `json:"end_line"`
	EndCol   int        `json:"end_col"`
	Severity string     `json:"severity"`
	Code     string     `json:"code"`
	Message  string     `json:"message"`
	Help     string     `json:"help"`
	Notes    []jsonNote `json:"notes"`
	Build    Build      `json:"build"`
}

// JSON renders one diagnostic as a single JSON Lines record.
func JSON(d Diagnostic, b Build) string {
	end := d.End
	if !end.IsValid() {
		end = d.Pos
	}
	notes := make([]jsonNote, 0, len(d.Notes))
	for _, n := range d.Notes {
		notes = append(notes, jsonNote{File: n.Pos.File, Line: n.Pos.Line, Col: n.Pos.Col, Message: n.Message})
	}
	rec := jsonDiagnostic{
		File:     d.Pos.File,
		Line:     d.Pos.Line,
		Col:      d.Pos.Col,
		EndLine:  end.Line,
		EndCol:   end.Col,
		Severity: d.Severity.String(),
		Code:     d.Code,
		Message:  d.Message,
		Help:     d.Help,
		Notes:    notes,
		Build:    b,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// JSONAll renders every diagnostic as JSON Lines, each line ending in LF.
func JSONAll(ds []Diagnostic, b Build) string {
	var out strings.Builder
	for _, d := range ds {
		out.WriteString(JSON(d, b))
		out.WriteByte('\n')
	}
	return out.String()
}
