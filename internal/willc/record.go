// Package willc reads and writes the line-oriented semantic intermediate
// format. Reading then writing the same document must be byte-identical.
package willc

// ValueKind classifies one field value in a record.
type ValueKind uint8

const (
	// TextValue is a double-quoted string.
	TextValue ValueKind = iota
	// IntValue is a decimal integer.
	IntValue
	// RefValue is a node reference written as #<id>.
	RefValue
	// WordValue is an unquoted name such as an operator spelling.
	WordValue
	// BoolValue is true or false.
	BoolValue
)

// Value is one field value.
type Value struct {
	Kind ValueKind
	Text string
	Int  int
	Ref  int
	Bool bool
}

// TextValue returns a quoted-text value.
func Text(s string) Value { return Value{Kind: TextValue, Text: s} }

// Int returns a decimal-integer value.
func Int(n int) Value { return Value{Kind: IntValue, Int: n} }

// Ref returns a node-reference value.
func Ref(id int) Value { return Value{Kind: RefValue, Ref: id} }

// Word returns an unquoted-name value.
func Word(s string) Value { return Value{Kind: WordValue, Text: s} }

// Bool returns a boolean value.
func Bool(b bool) Value { return Value{Kind: BoolValue, Bool: b} }

// Field is one name=value pair. Records may repeat a field name.
type Field struct {
	Name  string
	Value Value
}

// Record is one line of a willc document.
type Record struct {
	ID     int
	Kind   string
	Fields []Field
}

// Doc is a whole willc document in record order.
type Doc struct {
	Records []Record
}

// Error reports a willc shape error at one node.
type Error struct {
	ID      int
	Message string
}

func (e *Error) Error() string { return e.Message }

// KindSpec lists the fields a record kind allows. RequiredAny holds
// alternatives, each of which must be fully present; it exists because the
// node table uses the name field for both a struct field and a field read.
type KindSpec struct {
	Required    []string
	RequiredAny [][]string
	Repeatable  []string
	Optional    []string
}
