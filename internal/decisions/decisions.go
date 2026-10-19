// Package decisions writes the decision record: one line per decision, in the
// line grammar of the intermediate format with the category word first. Values
// use the same kinds as willc, fields are written in the order the table lists
// them, and the lines come out in stage order and production order.
package decisions

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Record is one decision line.
type Record struct {
	Category string
	Fields   []willc.Field
}

// Field appends one field.
func (r *Record) Field(name string, v willc.Value) {
	r.Fields = append(r.Fields, willc.Field{Name: name, Value: v})
}

// text appends one quoted text field.
func (r *Record) text(name, value string) { r.Field(name, willc.Text(value)) }

// number appends one integer field.
func (r *Record) number(name string, value int) { r.Field(name, willc.Int(value)) }

// Write renders records in order: no blank lines, no comments, ASCII, LF, and
// a final newline.
func Write(records []Record) []byte {
	var b strings.Builder
	for _, rec := range records {
		b.WriteString(rec.Category)
		for _, f := range rec.Fields {
			b.WriteByte(' ')
			b.WriteString(f.Name)
			b.WriteByte('=')
			b.WriteString(willc.FormatValue(f.Value))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Layout records one type's size and alignment.
func Layout(typ string, size, align int) Record {
	r := Record{Category: "layout"}
	r.text("type", typ)
	r.number("size", size)
	r.number("align", align)
	return r
}

// FieldOffset records one field's place in its type.
func FieldOffset(typ, name string, offset, size int) Record {
	r := Record{Category: "field"}
	r.text("type", typ)
	r.text("name", name)
	r.number("offset", offset)
	r.number("size", size)
	return r
}

// Obligation records the verdict of one proof obligation.
func Obligation(pos, check, verdict, by string) Record {
	r := Record{Category: "obligation"}
	r.text("pos", pos)
	r.text("check", check)
	r.text("verdict", verdict)
	r.text("by", by)
	return r
}
