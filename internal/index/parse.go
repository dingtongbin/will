package index

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Error reports a malformed index line. Line is 1-based.
type Error struct {
	Line    int
	Message string
}

func (e *Error) Error() string {
	return "line " + strconv.Itoa(e.Line) + ": " + e.Message
}

// fieldKind is the value form a field must have on the line.
type fieldKind uint8

const (
	textField fieldKind = iota
	intField
)

// fieldSpec is one required field and the order it is written in.
type fieldSpec struct {
	name string
	kind fieldKind
}

// lineSpecs lists the fields of every line kind in the order the format
// requires. A line that misses, adds, reorders, or retypes a field is an
// error, so the writer never has to normalize anything.
var lineSpecs = map[string][]fieldSpec{
	"package": {{name: "path", kind: textField}},
	"file": {
		{name: "path", kind: textField},
		{name: "package", kind: textField},
	},
	"import": {
		{name: "from", kind: textField},
		{name: "to", kind: textField},
	},
	"decl": {
		{name: "file", kind: textField},
		{name: "line", kind: intField},
		{name: "col", kind: intField},
		{name: "kind", kind: textField},
		{name: "name", kind: textField},
		{name: "vis", kind: textField},
	},
	"alias": {
		{name: "file", kind: textField},
		{name: "line", kind: intField},
		{name: "col", kind: intField},
		{name: "name", kind: textField},
		{name: "target", kind: textField},
	},
	"method": {
		{name: "file", kind: textField},
		{name: "line", kind: intField},
		{name: "col", kind: intField},
		{name: "type", kind: textField},
		{name: "name", kind: textField},
		{name: "recv", kind: textField},
	},
	"satisfies": {
		{name: "type", kind: textField},
		{name: "interface", kind: textField},
	},
	"dispatch": {
		{name: "file", kind: textField},
		{name: "line", kind: intField},
		{name: "col", kind: intField},
		{name: "interface", kind: textField},
		{name: "targets", kind: textField},
	},
	"cfunc": {
		{name: "header", kind: textField},
		{name: "name", kind: textField},
		{name: "linkage", kind: textField},
	},
}

// field is one parsed name=value pair. raw keeps the exact value spelling:
// the index format fixes one line shape but not one escape spelling, and
// Write must not rewrite a value Parse accepted.
type field struct {
	name string
	kind fieldKind
	raw  string
	text string
	num  int
}

// Parse reads the text index. It rejects every line that the format does not
// allow, including a missing or extra field, a field out of order, a quoted
// integer, and a bare text value.
func Parse(text []byte) (*Index, error) {
	if err := checkText(text); err != nil {
		return nil, err
	}
	ix := newIndex()
	body := strings.TrimSuffix(string(text), "\n")
	for i, line := range strings.Split(body, "\n") {
		no := i + 1
		if line == "" {
			return nil, &Error{Line: no, Message: "the line is empty"}
		}
		if err := ix.parseRecord(no, line); err != nil {
			return nil, err
		}
	}
	ix.buildLookups()
	return ix, nil
}

// checkText enforces the document invariants that hold before line parsing:
// ASCII only, LF endings, and a final newline.
func checkText(data []byte) error {
	if len(data) == 0 {
		return &Error{Line: 1, Message: "the index is empty"}
	}
	line := 1
	for _, b := range data {
		switch {
		case b == '\n':
			line++
		case b == '\r':
			return &Error{Line: line, Message: "the line ends with CR; lines end with LF"}
		case b < 0x20 || b > 0x7E:
			return &Error{Line: line, Message: "the index is not ASCII"}
		}
	}
	if data[len(data)-1] != '\n' {
		return &Error{Line: line, Message: "the last line does not end with a newline"}
	}
	return nil
}

func (ix *Index) parseRecord(no int, line string) error {
	kind, at, err := scanKind(no, line)
	if err != nil {
		return err
	}
	specs, ok := lineSpecs[kind]
	if !ok {
		return &Error{Line: no, Message: "unknown line kind " + strconv.Quote(kind)}
	}
	fields, err := scanFields(no, kind, line, at, specs)
	if err != nil {
		return err
	}
	return ix.addRecord(no, kind, fields)
}

// scanKind reads the unquoted line kind and returns the offset of the first
// field separator.
func scanKind(no int, line string) (string, int, error) {
	i := 0
	for i < len(line) && isLineWordByte(line[i]) {
		i++
	}
	if i == 0 {
		return "", 0, &Error{Line: no, Message: "a line starts with its kind"}
	}
	if i < len(line) && line[i] != ' ' {
		return "", 0, &Error{Line: no, Message: "the kind is followed by one space"}
	}
	return line[:i], i, nil
}

func scanFields(no int, kind, line string, at int, specs []fieldSpec) ([]field, error) {
	fields := make([]field, 0, len(specs))
	for at < len(line) {
		if line[at] != ' ' {
			return nil, &Error{Line: no, Message: "fields are separated by one space"}
		}
		at++
		start := at
		for at < len(line) && isNameByte(line[at]) {
			at++
		}
		name := line[start:at]
		if name == "" {
			return nil, &Error{Line: no, Message: "a field is written name=value"}
		}
		if at >= len(line) || line[at] != '=' {
			return nil, &Error{Line: no, Message: "field " + strconv.Quote(name) + " is written name=value"}
		}
		at++
		value, next, err := scanValue(no, line, at)
		if err != nil {
			return nil, err
		}
		value.name = name
		at = next
		if len(fields) == len(specs) {
			return nil, &Error{Line: no, Message: "field " + strconv.Quote(name) +
				" is not allowed on " + strconv.Quote(kind)}
		}
		spec := specs[len(fields)]
		if name != spec.name {
			return nil, &Error{Line: no, Message: "field " + strconv.Quote(name) +
				" is out of order; " + strconv.Quote(kind) + " expects " + strconv.Quote(spec.name)}
		}
		if value.kind != spec.kind {
			return nil, &Error{Line: no, Message: typeMessage(name, spec.kind)}
		}
		fields = append(fields, value)
	}
	if len(fields) != len(specs) {
		return nil, &Error{Line: no, Message: kind + " needs field " + specs[len(fields)].name}
	}
	return fields, nil
}

// typeMessage names the value form a field must have.
func typeMessage(name string, kind fieldKind) string {
	if kind == textField {
		return "field " + strconv.Quote(name) + " is quoted text, not an integer"
	}
	return "field " + strconv.Quote(name) + " is an integer, not quoted text"
}

func scanValue(no int, line string, at int) (field, int, error) {
	if at >= len(line) {
		return field{}, 0, &Error{Line: no, Message: "a field needs a value"}
	}
	if line[at] == '"' {
		end, ok := scanQuoted(line, at)
		if !ok {
			return field{}, 0, &Error{Line: no, Message: "the quoted text is not terminated"}
		}
		raw := line[at:end]
		text, ok := willc.Unquote(raw)
		if !ok {
			return field{}, 0, &Error{Line: no, Message: "the quoted text uses an escape the index format does not allow"}
		}
		return field{kind: textField, raw: raw, text: text}, end, nil
	}
	if line[at] >= '0' && line[at] <= '9' {
		end := at
		for end < len(line) && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		raw := line[at:end]
		if len(raw) > 1 && raw[0] == '0' {
			return field{}, 0, &Error{Line: no, Message: "the integer " + strconv.Quote(raw) + " has a leading zero"}
		}
		num, err := strconv.Atoi(raw)
		if err != nil {
			return field{}, 0, &Error{Line: no, Message: "the integer " + strconv.Quote(raw) + " is out of range"}
		}
		return field{kind: intField, raw: raw, num: num}, end, nil
	}
	return field{}, 0, &Error{Line: no, Message: "a value is quoted text or an integer"}
}

// scanQuoted returns the offset just past the closing quote.
func scanQuoted(line string, start int) (int, bool) {
	for i := start + 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

func (ix *Index) addRecord(no int, kind string, f []field) error {
	switch kind {
	case "package":
		ix.Packages = append(ix.Packages, Package{Path: f[0].text})
	case "file":
		ix.Files = append(ix.Files, File{Path: f[0].text, Package: f[1].text})
	case "import":
		ix.Imports = append(ix.Imports, Import{From: f[0].text, To: f[1].text})
	case "decl":
		d := Decl{
			File: f[0].text,
			Line: f[1].num,
			Col:  f[2].num,
			Kind: f[3].text,
			Name: f[4].text,
			Vis:  f[5].text,
		}
		if err := checkPos(no, "decl", d.Line, d.Col); err != nil {
			return err
		}
		if d.Name == "" {
			return &Error{Line: no, Message: "a decl name is never empty"}
		}
		if d.Vis != "public" && d.Vis != "private" {
			return &Error{Line: no, Message: "decl vis has the value " + strconv.Quote(d.Vis) +
				"; it must be public or private"}
		}
		ix.Decls = append(ix.Decls, d)
	case "alias":
		a := Alias{
			File:   f[0].text,
			Line:   f[1].num,
			Col:    f[2].num,
			Name:   f[3].text,
			Target: f[4].text,
		}
		if err := checkPos(no, "alias", a.Line, a.Col); err != nil {
			return err
		}
		if a.Name == "" || a.Target == "" {
			return &Error{Line: no, Message: "an alias name and target are never empty"}
		}
		ix.Aliases = append(ix.Aliases, a)
	case "method":
		m := Method{
			File: f[0].text,
			Line: f[1].num,
			Col:  f[2].num,
			Type: f[3].text,
			Name: f[4].text,
			Recv: f[5].text,
		}
		if err := checkPos(no, "method", m.Line, m.Col); err != nil {
			return err
		}
		if m.Recv != "value" && m.Recv != "ref" && m.Recv != "mut" {
			return &Error{Line: no, Message: "method recv has the value " + strconv.Quote(m.Recv) +
				"; it must be value, ref, or mut"}
		}
		ix.Methods = append(ix.Methods, m)
	case "satisfies":
		ix.Satisfies = append(ix.Satisfies, Satisfies{Type: f[0].text, Interface: f[1].text})
	case "dispatch":
		d := Dispatch{
			File:      f[0].text,
			Line:      f[1].num,
			Col:       f[2].num,
			Interface: f[3].text,
		}
		if err := checkPos(no, "dispatch", d.Line, d.Col); err != nil {
			return err
		}
		key := DispatchKey{File: d.File, Line: d.Line, Col: d.Col}
		if _, seen := ix.dispatchAt[key]; seen {
			return &Error{Line: no, Message: "two dispatch lines share the position " + key.String()}
		}
		targets, err := splitTargets(no, f[4].text)
		if err != nil {
			return err
		}
		d.Targets = targets
		for _, t := range targets {
			ix.Targets = append(ix.Targets, Target{File: d.File, Line: d.Line, Col: d.Col, Type: t})
		}
		ix.Dispatches = append(ix.Dispatches, d)
		ix.dispatchAt[key] = len(ix.Dispatches) - 1
	case "cfunc":
		c := CFunc{Header: f[0].text, Name: f[1].text, Linkage: f[2].text}
		switch c.Linkage {
		case "fast", "blocking", "plain":
		default:
			return &Error{Line: no, Message: "cfunc linkage has the value " + strconv.Quote(c.Linkage) +
				"; it must be fast, blocking, or plain"}
		}
		ix.CFuncs = append(ix.CFuncs, c)
	}
	ix.lines = append(ix.lines, renderLine(kind, f))
	return nil
}

// checkPos rejects a source position that cannot exist: lines and columns
// are 1-based.
func checkPos(no int, kind string, line, col int) error {
	if line < 1 {
		return &Error{Line: no, Message: kind + " line must be at least 1"}
	}
	if col < 1 {
		return &Error{Line: no, Message: kind + " col must be at least 1"}
	}
	return nil
}

// splitTargets reads the comma-separated target names. The empty text means
// the interface is known to have no implementation, so it yields no targets.
func splitTargets(no int, text string) ([]string, error) {
	if text == "" {
		return nil, nil
	}
	targets := strings.Split(text, ",")
	for _, t := range targets {
		if t == "" {
			return nil, &Error{Line: no, Message: "the targets field has an empty entry"}
		}
	}
	return targets, nil
}

// renderLine rebuilds one line from its parsed fields, which keeps the value
// spelling exactly as written.
func renderLine(kind string, fields []field) string {
	var b strings.Builder
	b.WriteString(kind)
	for _, f := range fields {
		b.WriteByte(' ')
		b.WriteString(f.name)
		b.WriteByte('=')
		b.WriteString(f.raw)
	}
	return b.String()
}

func isLineWordByte(b byte) bool {
	return isNameByte(b) || b == '-'
}

func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
