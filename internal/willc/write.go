package willc

import (
	"strconv"
	"strings"
)

// Write renders a document in the canonical form. Reading a canonical file
// and writing it again returns the same bytes.
func Write(doc *Doc) []byte {
	var b strings.Builder
	for _, rec := range doc.Records {
		b.WriteByte('#')
		b.WriteString(strconv.Itoa(rec.ID))
		b.WriteByte(' ')
		b.WriteString(rec.Kind)
		for _, f := range rec.Fields {
			b.WriteByte(' ')
			b.WriteString(f.Name)
			b.WriteByte('=')
			b.WriteString(FormatValue(f.Value))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// FormatValue renders one field value in the canonical spelling. The decision
// record uses the same value grammar, so it shares this function.
func FormatValue(v Value) string {
	switch v.Kind {
	case TextValue:
		return Quote(v.Text)
	case IntValue:
		return strconv.Itoa(v.Int)
	case RefValue:
		return "#" + strconv.Itoa(v.Ref)
	case BoolValue:
		if v.Bool {
			return "true"
		}
		return "false"
	}
	return v.Text
}
