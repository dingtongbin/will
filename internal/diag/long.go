package diag

import "strings"

// FormatLong renders one diagnostic followed by the source line and a caret.
// The echo is not part of the canonical format; it exists for people reading a
// terminal and never enters a recorded output.
func FormatLong(d Diagnostic, src []byte) string {
	var b strings.Builder
	b.WriteString(Format(d))
	line := sourceLine(src, d.Pos.Line)
	if line == "" {
		return b.String()
	}
	b.WriteString("    ")
	b.WriteString(line)
	b.WriteByte('\n')
	col := d.Pos.Col
	if col < 1 {
		col = 1
	}
	b.WriteString("    ")
	b.WriteString(strings.Repeat(" ", col-1))
	b.WriteString("^\n")
	return b.String()
}

// sourceLine returns one line of src, without its newline. Lines count from 1.
func sourceLine(src []byte, line int) string {
	if line < 1 {
		return ""
	}
	lines := strings.Split(string(src), "\n")
	if line > len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], "\r")
}

// FormatAllLong renders every diagnostic in order, echoing the source line of
// each one. The reader returns the source of one file, or nil when it cannot
// be read.
func FormatAllLong(ds []Diagnostic, read func(file string) []byte) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(FormatLong(d, read(d.Pos.File)))
	}
	return b.String()
}
