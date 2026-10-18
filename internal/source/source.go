// Package source defines source positions shared by every compiler stage.
package source

import "strconv"

// Pos identifies a byte offset and a one-based line and column in a file.
type Pos struct {
	File   string
	Offset int
	Line   int
	Col    int
}

// IsValid reports whether p carries a source location.
func (p Pos) IsValid() bool {
	return p.Line > 0
}

// String formats p as file:line:col.
func (p Pos) String() string {
	if p.File == "" {
		return strconv.Itoa(p.Offset)
	}
	return p.File + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col)
}
