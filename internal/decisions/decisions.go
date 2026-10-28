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

// Frame records the heap frame of one state-machine function. The state word is
// the first field; the remaining fields are written by FrameField.
func Frame(function string, size int) Record {
	r := Record{Category: "frame"}
	r.text("func", function)
	r.number("size", size)
	return r
}

// FrameField records one field of a state-machine frame.
func FrameField(function, name string, offset int, typ string) Record {
	r := Record{Category: "framefield"}
	r.text("func", function)
	r.text("name", name)
	r.number("offset", offset)
	r.text("type", typ)
	return r
}

// StackMap records one precise stack-map slot for a compiled function. The
// slot is a byte offset from the frame base.
func StackMap(function string, slot int, typ string) Record {
	r := Record{Category: "stackmap"}
	r.text("func", function)
	r.number("slot", slot)
	r.text("type", typ)
	return r
}

// Barrier records one lowered read, write, SATB, or card barrier.
func Barrier(function, pos, kind string) Record {
	r := Record{Category: "barrier"}
	r.text("func", function)
	r.text("pos", pos)
	r.text("kind", kind)
	return r
}

// Alloc records one allocation site and its lowering strategy.
func Alloc(function, pos, typ, site string) Record {
	r := Record{Category: "alloc"}
	r.text("func", function)
	r.text("pos", pos)
	r.text("type", typ)
	r.text("site", site)
	return r
}

// Merge records one plate-backed release group. Members are release record ids
// from the same file, written as a comma-separated list in one quoted field.
func Merge(pos, members string, count int, reason string) Record {
	r := Record{Category: "merge"}
	r.text("pos", pos)
	r.text("members", members)
	r.number("count", count)
	r.text("reason", reason)
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
