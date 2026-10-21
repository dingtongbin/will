package gen

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// lowerLayout folds SizeOf, AlignOf, and OffsetOf to an integer. The layout is
// the same one the record uses, so a struct field query and a real field access
// cannot disagree.
func (g *generator) lowerLayout(r willc.Record, name string) (string, error) {
	args := g.typeArgTexts(r)
	if len(args) != 1 {
		return "", g.errf(r.ID, "cannot translate node #%d: %s needs one type argument", r.ID, name)
	}
	typ, ok := g.typeLLVMOf(args[0])
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: type %s is not lowered yet", r.ID, args[0])
	}
	size, align, ok := g.sizeAlign(typ)
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: type %s has no layout", r.ID, args[0])
	}
	if name == "SizeOf" {
		return strconv.Itoa(size), nil
	}
	if name == "AlignOf" {
		return strconv.Itoa(align), nil
	}
	callArgs := g.callArgs(r)
	if len(callArgs) != 1 {
		return "", g.errf(r.ID, "cannot translate node #%d: OffsetOf needs one field path", r.ID)
	}
	lit, ok := g.recs[callArgs[0]]
	if !ok || lit.Kind != "lit" || fieldText(lit, "kind") != "str" {
		return "", g.errf(r.ID, "cannot translate node #%d: OffsetOf needs a string literal", r.ID)
	}
	offset := 0
	for _, field := range strings.Split(fieldText(lit, "text"), ".") {
		info := g.structInfoByLLVM(typ)
		if info == nil {
			return "", g.errf(r.ID, "cannot translate node #%d: %s is not a struct", r.ID, typ)
		}
		index, ok := info.index[field]
		if !ok || index >= len(info.types) {
			return "", g.errf(r.ID, "cannot translate node #%d: %s has no field %s", r.ID, typ, field)
		}
		fieldOffset := 0
		for i := 0; i < index; i++ {
			fs, fa, ok := g.sizeAlign(info.types[i])
			if !ok {
				return "", g.errf(r.ID, "cannot translate node #%d: field %s has no layout", r.ID, field)
			}
			fieldOffset = alignUp(fieldOffset, fa) + fs
		}
		_, fieldAlign, ok := g.sizeAlign(info.types[index])
		if !ok {
			return "", g.errf(r.ID, "cannot translate node #%d: field %s has no layout", r.ID, field)
		}
		offset += alignUp(fieldOffset, fieldAlign)
		typ = info.types[index]
	}
	return strconv.Itoa(offset), nil
}
