package decisions

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Parse reads the decision record. The line grammar is the one the intermediate
// format fixes: a category word followed by name=value fields, with the same
// value kinds as willc. The category schemas are the writer's business; the
// reader enforces the line grammar and the absence of repeated fields, so a
// future category can be carried without teaching the reader its table.
func Parse(text []byte) ([]Record, error) {
	if len(text) == 0 {
		return nil, nil
	}
	if text[len(text)-1] != '\n' {
		return nil, fmt.Errorf("the decision record does not end with a newline")
	}
	lines := strings.Split(string(text[:len(text)-1]), "\n")
	records := make([]Record, 0, len(lines))
	for i, line := range lines {
		rec, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// parseLine reads one category line. Values are split on spaces outside a
// quoted text, so a quoted value may contain spaces.
func parseLine(line string) (Record, error) {
	if line == "" {
		return Record{}, fmt.Errorf("empty line")
	}
	if strings.TrimSpace(line) != line {
		return Record{}, fmt.Errorf("leading or trailing space")
	}
	parts, err := splitFields(line)
	if err != nil {
		return Record{}, err
	}
	category := parts[0]
	if category == "" || strings.ContainsAny(category, "=\"#") {
		return Record{}, fmt.Errorf("bad category %q", category)
	}
	rec := Record{Category: category}
	seen := map[string]bool{}
	for _, part := range parts[1:] {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 || eq == len(part)-1 {
			return Record{}, fmt.Errorf("field %q is not name=value", part)
		}
		name := part[:eq]
		if strings.ContainsAny(name, "=\"#") {
			return Record{}, fmt.Errorf("bad field name %q", name)
		}
		if seen[name] {
			return Record{}, fmt.Errorf("field %q is written twice", name)
		}
		seen[name] = true
		value, err := parseValue(part[eq+1:])
		if err != nil {
			return Record{}, fmt.Errorf("field %q: %w", name, err)
		}
		rec.Fields = append(rec.Fields, willc.Field{Name: name, Value: value})
	}
	return rec, nil
}

// splitFields splits a line on the spaces that are outside a quoted text.
func splitFields(line string) ([]string, error) {
	var out []string
	start := 0
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if quoted {
				i++
			}
		case '"':
			quoted = !quoted
		case ' ':
			if quoted {
				continue
			}
			if i == start {
				return nil, fmt.Errorf("field is empty")
			}
			out = append(out, line[start:i])
			start = i + 1
		}
	}
	if quoted {
		return nil, fmt.Errorf("text is not closed")
	}
	if start == len(line) {
		return nil, fmt.Errorf("trailing space")
	}
	out = append(out, line[start:])
	return out, nil
}

// parseValue reads one value in the willc value grammar.
func parseValue(text string) (willc.Value, error) {
	switch {
	case text == "":
		return willc.Value{}, fmt.Errorf("empty value")
	case text[0] == '"':
		s, ok := willc.Unquote(text)
		if !ok {
			return willc.Value{}, fmt.Errorf("bad text %q", text)
		}
		return willc.Text(s), nil
	case text[0] == '#':
		n, err := strconv.Atoi(text[1:])
		if err != nil || n <= 0 {
			return willc.Value{}, fmt.Errorf("bad reference %q", text)
		}
		return willc.Ref(n), nil
	case text == "true":
		return willc.Bool(true), nil
	case text == "false":
		return willc.Bool(false), nil
	case isInteger(text):
		n, err := strconv.Atoi(text)
		if err != nil {
			return willc.Value{}, fmt.Errorf("integer %q is out of range", text)
		}
		return willc.Int(n), nil
	case isWord(text):
		return willc.Word(text), nil
	}
	return willc.Value{}, fmt.Errorf("bad value %q", text)
}

// isInteger reports whether text is an optionally signed decimal integer.
func isInteger(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == '-' || text[0] == '+' {
		text = text[1:]
	}
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// isWord reports whether text is a bare word.
func isWord(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.' || c == '-' || c == '/' {
			continue
		}
		return false
	}
	return text != ""
}

// Placed is one field of a named type.
type Placed struct {
	Name   string
	Offset int
	Size   int
}

// TypeLayout is the layout of one type as a decision record states it.
type TypeLayout struct {
	Type   string
	Size   int
	Align  int
	Fields []Placed
}

// Index is the layout and field union of one build batch.
type Index struct {
	layouts map[string]*TypeLayout
	order   []string
}

// Union combines the decision records of one batch into an index. Only layout
// and field records enter the index; a type that appears twice or a field whose
// type has no layout is an error, because the lowering must not choose between
// two answers.
func Union(records []Record) (*Index, error) {
	ix := &Index{layouts: map[string]*TypeLayout{}}
	for _, rec := range records {
		switch rec.Category {
		case "layout":
			typ, size, align, err := layoutFields(rec)
			if err != nil {
				return nil, err
			}
			if _, ok := ix.layouts[typ]; ok {
				return nil, fmt.Errorf("type %s has two layouts", typ)
			}
			ix.layouts[typ] = &TypeLayout{Type: typ, Size: size, Align: align}
			ix.order = append(ix.order, typ)
		case "field":
			typ, name, offset, size, err := fieldFields(rec)
			if err != nil {
				return nil, err
			}
			layout, ok := ix.layouts[typ]
			if !ok {
				return nil, fmt.Errorf("field %s.%s has no layout for %s", typ, name, typ)
			}
			for _, f := range layout.Fields {
				if f.Name == name {
					return nil, fmt.Errorf("type %s has two fields named %s", typ, name)
				}
			}
			layout.Fields = append(layout.Fields, Placed{Name: name, Offset: offset, Size: size})
		}
	}
	return ix, nil
}

// Names returns the types with a layout, in record order.
func (ix *Index) Names() []string {
	return append([]string(nil), ix.order...)
}

// Layout returns one type's layout.
func (ix *Index) Layout(name string) (TypeLayout, bool) {
	got, ok := ix.layouts[name]
	if !ok {
		return TypeLayout{}, false
	}
	out := *got
	out.Fields = append([]Placed(nil), got.Fields...)
	return out, true
}

func layoutFields(rec Record) (typ string, size, align int, err error) {
	typ, err = textOf(rec, "type")
	if err != nil {
		return "", 0, 0, err
	}
	size, err = intOf(rec, "size")
	if err != nil {
		return "", 0, 0, err
	}
	align, err = intOf(rec, "align")
	if err != nil {
		return "", 0, 0, err
	}
	return typ, size, align, nil
}

func fieldFields(rec Record) (typ, name string, offset, size int, err error) {
	typ, err = textOf(rec, "type")
	if err != nil {
		return "", "", 0, 0, err
	}
	name, err = textOf(rec, "name")
	if err != nil {
		return "", "", 0, 0, err
	}
	offset, err = intOf(rec, "offset")
	if err != nil {
		return "", "", 0, 0, err
	}
	size, err = intOf(rec, "size")
	if err != nil {
		return "", "", 0, 0, err
	}
	return typ, name, offset, size, nil
}

func textOf(rec Record, name string) (string, error) {
	for _, f := range rec.Fields {
		if f.Name != name {
			continue
		}
		if f.Value.Kind != willc.TextValue {
			return "", fmt.Errorf("%s %s is not text", rec.Category, name)
		}
		return f.Value.Text, nil
	}
	return "", fmt.Errorf("%s has no %s", rec.Category, name)
}

func intOf(rec Record, name string) (int, error) {
	for _, f := range rec.Fields {
		if f.Name != name {
			continue
		}
		if f.Value.Kind != willc.IntValue {
			return 0, fmt.Errorf("%s %s is not an integer", rec.Category, name)
		}
		return f.Value.Int, nil
	}
	return 0, fmt.Errorf("%s has no %s", rec.Category, name)
}
