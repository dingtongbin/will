package willc

import (
	"strings"
)

// universalFields are allowed on every record: the tree shape, the source
// position (intermediate format, section 1), and the reference to a name or
// type, including one declared outside this file.
var universalFields = []string{"parent", "pos", "end", "to"}

// Read parses a willc document and validates the line grammar, the field
// sets, the node ids, and every reference.
func Read(data []byte) (*Doc, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, &Error{0, "the willc file does not end with a newline"}
	}
	for _, b := range data {
		if b == '\r' {
			return nil, &Error{0, "the willc file uses CR line endings"}
		}
		if b != '\n' && (b < 0x20 || b > 0x7E) {
			return nil, &Error{0, "the willc file is not ASCII"}
		}
	}
	body := strings.TrimSuffix(string(data), "\n")
	doc := &Doc{}
	for _, line := range strings.Split(body, "\n") {
		rec, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		if rec.ID != len(doc.Records)+1 {
			return nil, &Error{rec.ID, "node ids must run from #1 in order"}
		}
		doc.Records = append(doc.Records, rec)
	}
	if err := validate(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func parseLine(line string) (Record, error) {
	i := 0
	if i >= len(line) || line[i] != '#' {
		return Record{}, &Error{0, "a record must start with #<id>"}
	}
	i++
	start := i
	for i < len(line) && isDigit(line[i]) {
		i++
	}
	if i == start {
		return Record{}, &Error{0, "a record must start with #<id>"}
	}
	digits := line[start:i]
	if len(digits) > 1 && digits[0] == '0' {
		return Record{}, &Error{0, "node ids are written without leading zeros"}
	}
	id := parseUint(digits)
	if id < 1 {
		return Record{}, &Error{id, "node ids start at #1"}
	}
	if i >= len(line) || line[i] != ' ' {
		return Record{}, &Error{id, "a record needs a kind after its id"}
	}
	i++
	start = i
	for i < len(line) && isKindByte(line[i]) {
		i++
	}
	kind := line[start:i]
	if _, ok := kindSpecs[kind]; !ok {
		return Record{}, &Error{id, "unknown node kind " + kind}
	}
	rec := Record{ID: id, Kind: kind}
	for i < len(line) {
		if line[i] != ' ' {
			return Record{}, &Error{id, "fields are separated by one space"}
		}
		i++
		start = i
		for i < len(line) && isNameByte(line[i]) {
			i++
		}
		name := line[start:i]
		if name == "" || i >= len(line) || line[i] != '=' {
			return Record{}, &Error{id, "a field is written name=value"}
		}
		i++
		value, next, err := parseValue(line, i, id)
		if err != nil {
			return Record{}, err
		}
		i = next
		rec.Fields = append(rec.Fields, Field{Name: name, Value: value})
	}
	return rec, nil
}

func parseValue(line string, i, id int) (Value, int, error) {
	if i >= len(line) {
		return Value{}, i, &Error{id, "a field needs a value"}
	}
	c := line[i]
	switch {
	case c == '"':
		end, ok := scanQuoted(line, i)
		if !ok {
			return Value{}, i, &Error{id, "unterminated quoted text"}
		}
		s, ok := Unquote(line[i:end])
		if !ok {
			return Value{}, i, &Error{id, "invalid quoted text"}
		}
		return Text(s), end, nil
	case c == '#':
		j := i + 1
		start := j
		for j < len(line) && isDigit(line[j]) {
			j++
		}
		if j == start {
			return Value{}, i, &Error{id, "a reference is written #<id>"}
		}
		return Ref(parseUint(line[start:j])), j, nil
	case isDigit(c):
		j := i
		for j < len(line) && isDigit(line[j]) {
			j++
		}
		return Int(parseUint(line[i:j])), j, nil
	case hasWord(line[i:], "true"):
		return Bool(true), i + 4, nil
	case hasWord(line[i:], "false"):
		return Bool(false), i + 5, nil
	case isNameByte(c):
		j := i
		for j < len(line) && isNameByte(line[j]) {
			j++
		}
		return Word(line[i:j]), j, nil
	}
	return Value{}, i, &Error{id, "unknown field value"}
}

// wordVocab lists the allowed spellings of the fields whose value comes from a
// fixed vocabulary. A value outside its list is a shape error: for example a
// pattern kind of range cannot be produced by the grammar, whose two-dot range
// is not part of the language.
var wordVocab = map[string]map[string][]string{
	"pattern": {"kind": {"wild", "bind", "literal", "variant", "or"}},
	"type": {"kind": {"scalar", "named", "array", "view", "ref", "mutref", "func", "unit",
		"result", "option", "rc", "arc", "weak_rc", "weak_arc", "chan", "task"}},
	"lit":        {"kind": {"int", "float", "char", "str", "bytes", "bool", "duration", "unit"}},
	"release":    {"kind": {"value", "rc", "guard", "frame"}},
	"count":      {"name": {"values", "releases", "merges", "refs", "diagnostics"}},
	"diagnostic": {"severity": {"error", "warning"}},
}

func validate(doc *Doc) error {
	if len(doc.Records) == 0 {
		return &Error{0, "the willc file is empty"}
	}
	if doc.Records[0].Kind != "module" {
		return &Error{doc.Records[0].ID, "the first record must be module"}
	}
	maxID := len(doc.Records)
	last := doc.Records[maxID-1]
	if last.Kind != "end" {
		return &Error{last.ID, "the last record must be end"}
	}
	for i := 1; i < maxID-1; i++ {
		rec := doc.Records[i]
		if rec.Kind == "end" || rec.Kind == "module" {
			return &Error{rec.ID, "module and end appear once"}
		}
	}
	for _, rec := range doc.Records {
		if err := validateFields(rec); err != nil {
			return err
		}
		if err := validateWords(rec); err != nil {
			return err
		}
		for _, f := range rec.Fields {
			if f.Value.Kind == RefValue && (f.Value.Ref < 1 || f.Value.Ref > maxID) {
				return &Error{rec.ID, "dangling node reference"}
			}
		}
		for _, f := range rec.Fields {
			if f.Name != "to" {
				continue
			}
			switch f.Value.Kind {
			case RefValue:
				if f.Value.Ref < 1 || f.Value.Ref > maxID {
					return &Error{rec.ID, "dangling node reference"}
				}
			case TextValue:
				// A reference to a declaration outside this file is written
				// as its package-qualified name.
				if f.Value.Text == "" || strings.HasPrefix(f.Value.Text, "#") {
					return &Error{rec.ID, "to is either #<id> or a quoted symbol name"}
				}
			default:
				return &Error{rec.ID, "to is either #<id> or a quoted symbol name"}
			}
		}
	}
	return nil
}

// validateWords checks every field whose value must come from a fixed list.
func validateWords(rec Record) error {
	fields, ok := wordVocab[rec.Kind]
	if !ok {
		return nil
	}
	for _, f := range rec.Fields {
		allowed, ok := fields[f.Name]
		if !ok || (f.Value.Kind != WordValue && f.Value.Kind != TextValue) {
			continue
		}
		found := false
		for _, word := range allowed {
			if word == f.Value.Text {
				found = true
				break
			}
		}
		if !found {
			return &Error{rec.ID, "field " + f.Name + " has the value " + f.Value.Text +
				", which is not one of the spellings of " + rec.Kind}
		}
	}
	return nil
}

func validateFields(rec Record) error {
	spec := kindSpecs[rec.Kind]
	seen := map[string]int{}
	for _, f := range rec.Fields {
		if !allowedField(spec, f.Name) {
			return &Error{rec.ID, "field " + f.Name + " is not allowed on " + rec.Kind}
		}
		seen[f.Name]++
	}
	for _, name := range spec.Required {
		if seen[name] == 0 {
			return &Error{rec.ID, "record " + rec.Kind + " needs field " + name}
		}
	}
	if len(spec.RequiredAny) > 0 && !anySetPresent(spec.RequiredAny, seen) {
		return &Error{rec.ID, "record " + rec.Kind + " needs one of its field sets"}
	}
	for name, n := range seen {
		if n > 1 && !repeatable(spec, name) {
			return &Error{rec.ID, "field " + name + " repeats on " + rec.Kind}
		}
	}
	return nil
}

func anySetPresent(sets [][]string, seen map[string]int) bool {
	for _, set := range sets {
		ok := true
		for _, name := range set {
			if seen[name] == 0 {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func allowedField(spec KindSpec, name string) bool {
	for _, f := range universalFields {
		if f == name {
			return true
		}
	}
	for _, f := range spec.Required {
		if f == name {
			return true
		}
	}
	for _, f := range spec.Optional {
		if f == name {
			return true
		}
	}
	for _, f := range spec.Repeatable {
		if f == name {
			return true
		}
	}
	return false
}

func repeatable(spec KindSpec, name string) bool {
	for _, f := range spec.Repeatable {
		if f == name {
			return true
		}
	}
	return false
}

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

func hasWord(s, word string) bool {
	if !strings.HasPrefix(s, word) {
		return false
	}
	if len(s) == len(word) {
		return true
	}
	return s[len(word)] == ' '
}

func parseUint(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || isDigit(b)
}

func isKindByte(b byte) bool {
	return isNameByte(b) || b == '-'
}
