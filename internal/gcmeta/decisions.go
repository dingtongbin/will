package gcmeta

import (
	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/willc"
)

// DecisionRecords writes the class and struct layouts that participate in the
// GC graph. The record is part of the lowering cache key, so a size or offset
// change cannot reuse an old object file.
func DecisionRecords(docs []*willc.Doc) ([]decisions.Record, error) {
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
