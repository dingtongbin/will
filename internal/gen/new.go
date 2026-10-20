package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// constructNew builds `new T(args)`: zero the storage, apply the field
// defaults, then call T.init with a mutable borrow of the storage. The value
// is the storage itself, which the caller copies or returns.
func (g *generator) constructNew(r willc.Record) (string, string, error) {
	text := fieldText(r, "type")
	if elem, ok := arrayElemText(text); ok {
		return g.constructArray(r, elem)
	}
	if k, v, ok := mapTypeText(text); ok {
		entry, ok := g.mapEntryLLVM(k, v)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the Map entry is not lowered yet", r.ID)
		}
		return g.constructMapSet(r, entry)
	}
	if elem, ok := setTypeText(text); ok {
		entry, ok := g.containerValueType(elem)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the Set element is not lowered yet", r.ID)
		}
		return g.constructMapSet(r, entry)
	}
	info := g.byName[text]
	if info == nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: new needs a struct declared in this document", r.ID)
	}
	slot := g.allocSlot(g.newSlots, "new", r.ID, info.llvm)
	fmt.Fprintf(g.out, "  store %s zeroinitializer, ptr %s\n", info.llvm, slot)
	if err := g.fieldDefaults(info, slot); err != nil {
		return "", "", err
	}
	initSym := g.cur.methods[text+".init"]
	if initSym == "" {
		initSym = g.methodsQualified[text+".init"]
	}
	if initSym == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no init", r.ID, text)
	}
	if result := g.sigs[initSym]; result != "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: an init returning %s is not lowered yet", r.ID, result)
	}
	parts := []string{"ptr " + slot}
	params := g.paramTypes[initSym]
	for i, a := range g.callArgs(r) {
		paramType := ""
		if i < len(params) {
			paramType = params[i]
		}
		var err error
		parts, err = g.callArgParts(parts, r, i, a, paramType, false)
		if err != nil {
			return "", "", err
		}
	}
	fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(initSym), strings.Join(parts, ", "))
	return slot, info.llvm, nil
}

// fieldDefaults stores the declared default of every field that has one, in
// declaration order. A field without a default keeps the zero the storage
// already holds.
func (g *generator) fieldDefaults(info *structInfo, slot string) error {
	var fields []willc.Record
	for _, r := range g.recs {
		if r.Kind == "field" && refField(r, "parent") == info.id {
			fields = append(fields, r)
		}
	}
	sortByID(fields)
	for i, f := range fields {
		kids := g.children(f.ID)
		if len(kids) == 0 {
			continue
		}
		valueID := kids[0].ID
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", ptr, info.llvm, slot, i)
		typ := info.types[i]
		if isAggregateType(typ) {
			src, srcType, err := g.place(valueID)
			if err != nil {
				return err
			}
			if srcType != typ {
				return g.errf(valueID, "cannot translate node #%d: the default needs %s", valueID, typ)
			}
			g.copyValue(src, ptr, typ)
			continue
		}
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, ptr)
	}
	return nil
}
