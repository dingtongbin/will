package gcmeta

import (
	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/willc"
)

// HasClasses reports whether a batch carries the class records that need a GC
// decision block. A legacy batch with only structs and rc handles keeps its
// existing decision record byte for byte.
func HasClasses(docs []*willc.Doc) bool {
	for _, doc := range docs {
		for _, r := range doc.Records {
			if r.Kind == "class" {
				return true
			}
		}
	}
	return false
}

// DecisionRecords writes the class and struct layouts that participate in the
// GC graph. The record is part of the lowering cache key, so a size or offset
// change cannot reuse an old object file.
func DecisionRecords(docs []*willc.Doc) ([]decisions.Record, error) {
	if !HasClasses(docs) {
		return nil, nil
	}
	program, err := Build(docs)
	if err != nil {
		return nil, err
	}
	var out []decisions.Record
	for _, t := range program.Types() {
		switch t.Kind {
		case KindClass, KindStruct, KindEnum, KindInterface:
		default:
			continue
		}
		out = append(out, decisions.Layout(t.Name, t.Size, t.Align))
		for _, f := range t.Fields {
			out = append(out, decisions.FieldOffset(t.Name, f.Name, f.Offset,
				f.Size))
		}
	}
	return out, nil
}
