package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// structInfo is the lowered form of one struct declaration.
type structInfo struct {
	id    int
	name  string
	llvm  string
	names []string
	texts []string
	types []string
	index map[string]int
	ok    bool
}

// isStructType reports whether an LLVM type is a struct this lowering made. A
// struct type whose name holds a package path is quoted.
func isStructType(typ string) bool {
	return strings.HasPrefix(typ, "%struct.") || strings.HasPrefix(typ, `%"struct.`)
}

// buildStructs records every struct of the current document, then emits the
// LLVM type of each one whose fields are all lowered. The types come out in
// declaration order, before any function. A struct whose fields are not
// lowered yet is left out and is reported only when something uses it, so an
// unused declaration never fails a build.
func (g *generator) buildStructs() {
	var structs []willc.Record
	for _, r := range g.recs {
		if r.Kind == "struct" {
			structs = append(structs, r)
		}
	}
	sortByID(structs)
	for _, r := range structs {
		g.ensureStruct(r.ID)
	}
	for _, r := range structs {
		info := g.structs[r.ID]
		if info == nil || !info.ok {
			continue
		}
		if len(info.types) == 0 {
			fmt.Fprintf(g.out, "%s = type {}\n", info.llvm)
			continue
		}
		fmt.Fprintf(g.out, "%s = type { %s }\n", info.llvm, strings.Join(info.types, ", "))
	}
}

// ensureStruct records one struct and decides whether its fields are lowered.
// The record is inserted before the fields are visited, so a struct that
// contains itself is reported as not lowered instead of looping.
func (g *generator) ensureStruct(id int) (*structInfo, bool) {
	if info, ok := g.structs[id]; ok {
		return info, info.ok
	}
	r, ok := g.recs[id]
	if !ok {
		return nil, false
	}
	name := fieldText(r, "text")
	info := &structInfo{id: id, name: name, llvm: g.structLLVM(name), index: map[string]int{}}
	g.structs[id] = info
	g.byType[info.llvm] = info
	g.byName[info.name] = info

	var fields []willc.Record
	for _, c := range g.recs {
		if c.Kind == "field" && refField(c, "parent") == id {
			fields = append(fields, c)
		}
	}
	sortByID(fields)
	info.ok = true
	for i, f := range fields {
		info.index[fieldText(f, "text")] = i
		info.names = append(info.names, fieldText(f, "text"))
		info.texts = append(info.texts, fieldText(f, "type"))
		t := fieldText(f, "type")
		if t == "i32" {
			info.types = append(info.types, "i32")
			continue
		}
		sub := g.typeRef(f.ID)
		if sub == 0 {
			info.ok = false
			continue
		}
		inner, ok := g.ensureStruct(sub)
		if !ok {
			info.ok = false
			continue
		}
		info.types = append(info.types, inner.llvm)
	}
	if !info.ok {
		info.types = nil
	}
	return info, info.ok
}

// structLLVM names one struct type. The package path keeps two packages' types
// apart; a document without a package keeps the plain name. The name is quoted
// when the package path holds a character an identifier cannot.
func (g *generator) structLLVM(name string) string {
	body := "struct." + name
	if g.pkg != "" {
		body = "struct." + g.pkg + "." + name
	}
	return "%" + llvmSymbol(body)
}

// typeRef returns the struct a declared type refers to, or 0 when the type is
// not a named node of this document.
func (g *generator) typeRef(parent int) int {
	for _, r := range g.recs {
		if r.Kind == "type" && refField(r, "parent") == parent {
			return refField(r, "to")
		}
	}
	return 0
}

// declLLVMType returns the LLVM type of a local, parameter, or receiver.
func (g *generator) declLLVMType(decl int) (string, error) {
	r, ok := g.recs[decl]
	if !ok {
		return "", g.errf(decl, "cannot translate node #%d: no such declaration", decl)
	}
	if r.Kind == "var" || r.Kind == "const" {
		if t, ok := g.slotTypes[decl]; ok {
			return t, nil
		}
	}
	if typ, _, ok := llvmScalar(fieldText(r, "type")); ok {
		return typ, nil
	}
	if id := g.typeRef(decl); id != 0 {
		if info, ok := g.ensureStruct(id); ok {
			return info.llvm, nil
		}
		return "", g.errf(decl, "cannot translate node #%d: struct %s has a field type that is not lowered yet", decl, fieldText(r, "type"))
	}
	return "", g.errf(decl, "cannot translate node #%d: type %s is not lowered yet", decl, fieldText(r, "type"))
}

// typeOf returns the LLVM type an expression produces, without emitting an
// instruction. It is used where the answer picks the form of an instruction,
// such as whether an argument is passed by value or as a borrow.
func (g *generator) typeOf(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "lit":
		typ, _, ok := llvmScalar(fieldText(r, "type"))
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: literal type %s is not lowered yet", id, fieldText(r, "type"))
		}
		return typ, nil
	case "name":
		return g.declLLVMType(refField(r, "to"))
	case "field":
		base, err := g.typeOf(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		info := g.byType[base]
		if info == nil {
			return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, base)
		}
		i, ok := info.index[fieldText(r, "text")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
		}
		return info.types[i], nil
	case "agg":
		info, ok := g.ensureStruct(refField(r, "to"))
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: the aggregate type is not lowered yet", id)
		}
		return info.llvm, nil
	case "bin":
		switch fieldText(r, "op") {
		case "&&", "||", "==", "!=", "<", "<=", ">", ">=":
			return "i1", nil
		}
		return g.typeOf(refField(r, "l"))
	case "un":
		if fieldText(r, "op") == "!" {
			return "i1", nil
		}
		return g.typeOf(refField(r, "expr"))
	case "call", "mcall":
		text, err := g.callResultText(r)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
		}
		typ, _, ok := llvmScalar(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: result type %s is not lowered yet", id, text)
		}
		return typ, nil
	}
	return "", g.errf(id, "cannot translate node #%d: node kind %s is not lowered yet", id, r.Kind)
}

// willTypeText returns the will type text of a scalar expression, which is what
// picks the signed or unsigned instruction. A struct expression has no scalar
// text.
func (g *generator) willTypeText(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "lit":
		return fieldText(r, "type"), nil
	case "name":
		decl, ok := g.recs[refField(r, "to")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: no such declaration", id)
		}
		return fieldText(decl, "type"), nil
	case "field":
		base, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		info := g.byName[base]
		if info == nil {
			return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, base)
		}
		i, ok := info.index[fieldText(r, "text")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
		}
		return info.texts[i], nil
	case "agg":
		return "", nil
	case "bin":
		switch fieldText(r, "op") {
		case "&&", "||", "==", "!=", "<", "<=", ">", ">=":
			return "bool", nil
		}
		return g.willTypeText(refField(r, "l"))
	case "un":
		if fieldText(r, "op") == "!" {
			return "bool", nil
		}
		return g.willTypeText(refField(r, "expr"))
	case "call", "mcall":
		return g.callResultText(r)
	}
	return "", nil
}

// callResultText returns the result type text of one call. A call names its
// callee by a document reference or by the module-wide symbol.
func (g *generator) callResultText(r willc.Record) (string, error) {
	if qualified := textField(r, "to"); qualified != "" {
		if t, ok := g.sigs[qualified]; ok {
			return t, nil
		}
		return "", g.errf(r.ID, "cannot translate node #%d: %s is not a function of this module", r.ID, qualified)
	}
	if fn, ok := g.recs[refField(r, "to")]; ok && fn.Kind == "func" {
		return fieldText(fn, "type"), nil
	}
	return "", nil
}

// place emits the address of an expression and returns it with the LLVM type
// it points to. Aggregates are always addressed: a local is its slot, a borrow
// parameter is its pointer, and an aggregate literal is stored in a temporary
// slot reserved in the entry block.
func (g *generator) place(id int) (string, string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "name":
		decl := refField(r, "to")
		if slot, ok := g.slots[decl]; ok {
			typ, ok := g.slotTypes[decl]
			if !ok {
				return "", "", g.errf(id, "cannot translate node #%d: the local has no type", id)
			}
			return slot, typ, nil
		}
		if name, ok := g.scope[decl]; ok && g.borrows[decl] {
			typ, err := g.declLLVMType(decl)
			if err != nil {
				return "", "", err
			}
			return name, typ, nil
		}
		return "", "", g.errf(id, "cannot translate node #%d: %s is not a place", id, fieldText(r, "text"))
	case "field":
		base, typ, err := g.place(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		info := g.byType[typ]
		if info == nil {
			return "", "", g.errf(id, "cannot translate node #%d: %s has no fields", id, typ)
		}
		i, ok := info.index[fieldText(r, "text")]
		if !ok {
			return "", "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
		}
		g.tmp++
		ptr := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", ptr, typ, base, i)
		return ptr, info.types[i], nil
	case "agg":
		info, ok := g.ensureStruct(refField(r, "to"))
		if !ok {
			return "", "", g.errf(id, "cannot translate node #%d: the aggregate type is not lowered yet", id)
		}
		slot := g.aggSlots[id]
		if slot == "" {
			return "", "", g.errf(id, "cannot translate node #%d: the aggregate has no slot", id)
		}
		if err := g.storeAggregate(r, info, slot); err != nil {
			return "", "", err
		}
		return slot, info.llvm, nil
	case "call", "mcall":
		info, err := g.callResultStruct(r)
		if err != nil {
			return "", "", err
		}
		slot := g.callSlots[id]
		if slot == "" {
			return "", "", g.errf(id, "cannot translate node #%d: the call result has no slot", id)
		}
		value, _, err := g.callExpr(r)
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.llvm, value, slot)
		return slot, info.llvm, nil
	}
	return "", "", g.errf(id, "cannot translate node #%d: %s is not a place", id, r.Kind)
}

// storeAggregate writes each field of an aggregate literal into its slot.
func (g *generator) storeAggregate(r willc.Record, info *structInfo, slot string) error {
	values := map[string]int{}
	for _, f := range r.Fields {
		if f.Name != "field" {
			continue
		}
		name, id, ok := splitAggregateField(f.Value.Text)
		if !ok {
			return g.errf(r.ID, "cannot translate node #%d: the aggregate field is malformed", r.ID)
		}
		values[name] = id
	}
	for i := 0; i < len(info.types); i++ {
		name := info.names[i]
		valueID, ok := values[name]
		if !ok {
			return g.errf(r.ID, "cannot translate node #%d: field %s has no value", r.ID, name)
		}
		g.tmp++
		ptr := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", ptr, info.llvm, slot, i)
		if isStructType(info.types[i]) {
			src, typ, err := g.place(valueID)
			if err != nil {
				return err
			}
			if typ != info.types[i] {
				return g.errf(valueID, "cannot translate node #%d: field %s needs %s", valueID, name, info.types[i])
			}
			g.copyValue(src, ptr, typ)
			continue
		}
		v, err := g.value(valueID)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.types[i], v, ptr)
	}
	return nil
}

// splitAggregateField reads one field="name=#id" entry.
func splitAggregateField(text string) (string, int, bool) {
	eq := strings.IndexByte(text, '=')
	if eq <= 0 || eq+2 >= len(text) || text[eq+1] != '#' {
		return "", 0, false
	}
	id, err := strconv.Atoi(text[eq+2:])
	if err != nil || id == 0 {
		return "", 0, false
	}
	return text[:eq], id, true
}

// copyValue copies one value between two places.
func (g *generator) copyValue(src, dst, typ string) {
	g.tmp++
	tmp := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = load %s, ptr %s\n", tmp, typ, src)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, tmp, dst)
}
