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
		if typ, _, ok := llvmScalar(t); ok {
			info.types = append(info.types, typ)
			continue
		}
		sub := g.typeRef(f.ID)
		if sub == 0 {
			if g.declKinds[t] == "enum" {
				info.types = append(info.types, qualifiedEnumName(t))
				continue
			}
			if g.structTypes[t] {
				info.types = append(info.types, qualifiedTypeName(t))
				continue
			}
			info.ok = false
			continue
		}
		if r, ok := g.recs[sub]; ok && r.Kind == "enum" {
			info.types = append(info.types, g.enumLLVM(fieldText(r, "text")))
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

// typeQualified returns the package-qualified name a type record carries when
// it names a type from another document, or "" when the type is local.
func (g *generator) typeQualified(parent int) string {
	for _, r := range g.recs {
		if r.Kind == "type" && refField(r, "parent") == parent {
			return textField(r, "to")
		}
	}
	return ""
}

// qualifiedTypeName renders a qualified type name as its LLVM struct type.
func qualifiedTypeName(name string) string {
	return "%" + llvmSymbol("struct."+name)
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
	if typ, ok := llvmTypeText(fieldText(r, "type")); ok {
		return typ, nil
	}
	if id := g.typeRef(decl); id != 0 {
		if r, ok := g.recs[id]; ok && r.Kind == "enum" {
			if info := g.enums[id]; info != nil {
				return info.llvm, nil
			}
			return "", g.errf(decl, "cannot translate node #%d: enum %s is not lowered yet", decl, fieldText(r, "text"))
		}
		if info, ok := g.ensureStruct(id); ok {
			return info.llvm, nil
		}
		return "", g.errf(decl, "cannot translate node #%d: struct %s has a field type that is not lowered yet", decl, fieldText(r, "type"))
	}
	if q := g.typeQualified(decl); q != "" {
		// The declaring package emits the type definition; a reference from
		// this document only needs the same name.
		if g.enumTypes[q] {
			return qualifiedEnumName(q), nil
		}
		return qualifiedTypeName(q), nil
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
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if info := g.globalRef(r); info != nil {
			return info.typ, nil
		}
		if typ, ok := g.patternTypes[decl]; ok {
			return typ, nil
		}
		if text, ok := g.loopBindType(decl, name); ok {
			typ, ok := llvmTypeText(text)
			if !ok {
				return "", g.errf(id, "cannot translate node #%d: type %s is not lowered yet", id, text)
			}
			return typ, nil
		}
		return g.declLLVMType(decl)
	case "field":
		if info, ok := g.importMember(r); ok {
			return info.typ, nil
		}
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
	case "slice":
		text, err := g.willTypeText(id)
		if err != nil {
			return "", err
		}
		typ, ok := llvmTypeText(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: type %s is not lowered yet", id, text)
		}
		return typ, nil
	case "index":
		text, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		elem, ok := elementText(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: only an array, a view, or an Array is indexed yet", id)
		}
		typ, ok := llvmTypeText(elem)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: element type %s is not lowered yet", id, elem)
		}
		return typ, nil
	case "cast":
		to, ok := g.recs[refField(r, "to")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: the target type is missing", id)
		}
		typ, _, ok := llvmScalar(fieldText(to, "text"))
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: conversion to %s is not lowered yet", id, fieldText(to, "text"))
		}
		return typ, nil
	case "match":
		return g.matchValueType(r)
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
		if r.Kind == "mcall" {
			if info, _ := g.enumVariantCall(r); info != nil {
				return info.llvm, nil
			}
		}
		text, err := g.callResultText(r)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
		}
		typ, _, ok := llvmScalar(text)
		if !ok {
			if g.structTypes[text] {
				return qualifiedTypeName(text), nil
			}
			if g.enumTypes[text] {
				return qualifiedEnumName(text), nil
			}
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
			if info := g.globalRef(r); info != nil {
				return info.text, nil
			}
			return "", g.errf(id, "cannot translate node #%d: no such declaration", id)
		}
		if text, ok := g.patternTexts[decl.ID]; ok {
			return text, nil
		}
		if info := g.globalRef(r); info != nil {
			return info.text, nil
		}
		if text, ok := g.loopBindType(decl.ID, fieldText(r, "text")); ok {
			return text, nil
		}
		return fieldText(decl, "type"), nil
	case "field":
		if info, ok := g.importMember(r); ok {
			return info.text, nil
		}
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
	case "slice":
		elem, err := g.sliceElem(r)
		if err != nil {
			return "", err
		}
		return "[]" + elem, nil
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
		if r.Kind == "mcall" {
			if info, _ := g.enumVariantCall(r); info != nil {
				return info.text, nil
			}
		}
		return g.callResultText(r)
	case "cast":
		to, ok := g.recs[refField(r, "to")]
		if !ok {
			return "", nil
		}
		return fieldText(to, "text"), nil
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
	if r.Kind == "mcall" && g.isCLink(r) {
		return "", nil
	}
	if r.Kind == "mcall" {
		symbol, err := g.methodSymbol(r)
		if err != nil {
			return "", err
		}
		if t, ok := g.sigs[symbol]; ok {
			return t, nil
		}
		return "", g.errf(r.ID, "cannot translate node #%d: %s has no known result", r.ID, symbol)
	}
	if fn, ok := g.recs[refField(r, "to")]; ok && fn.Kind == "func" {
		if sym := g.cur.funcs[fn.ID]; sym != "" {
			if t, ok := g.sigs[sym]; ok {
				return t, nil
			}
		}
		return fieldText(fn, "type"), nil
	}
	return "", nil
}

// isCLink reports whether a method call goes through the c qualifier.
func (g *generator) isCLink(r willc.Record) bool {
	if r.Kind != "mcall" {
		return false
	}
	recv := g.recs[refField(r, "recv")]
	if recv.Kind != "name" {
		return false
	}
	cimport := refField(recv, "to")
	return cimport != 0 && g.linkNamed(cimport, fieldText(r, "text")) != ""
}

// methodSymbol resolves a method call on the receiver's type to the symbol the
// method is emitted under. A receiver whose type is declared in another
// document resolves through the module-wide qualified type index.
func (g *generator) methodSymbol(r willc.Record) (string, error) {
	typ, err := g.receiverTypeName(refField(r, "recv"))
	if err != nil {
		return "", err
	}
	name := fieldText(r, "text")
	if sym, ok := g.methodsQualified[typ+"."+name]; ok {
		return sym, nil
	}
	if sym, ok := g.cur.methods[typ+"."+name]; ok {
		return sym, nil
	}
	return "", g.errf(r.ID, "cannot translate node #%d: method %s is not declared for %s", r.ID, name, typ)
}

// receiverTypeName returns the qualified type name of a receiver. A local type
// carries the package path of this document; a type from another document
// carries its qualified name in the type record.
func (g *generator) receiverTypeName(recvID int) (string, error) {
	r, ok := g.recs[recvID]
	if !ok {
		return "", g.errf(recvID, "cannot translate node #%d: no such receiver", recvID)
	}
	if r.Kind == "name" {
		decl, ok := g.recs[refField(r, "to")]
		if ok {
			if q := g.typeQualified(decl.ID); q != "" {
				return q, nil
			}
			for _, c := range g.recs {
				if c.Kind == "type" && refField(c, "parent") == decl.ID {
					if id := refField(c, "to"); id != 0 {
						if s, ok := g.recs[id]; ok && s.Kind == "struct" {
							return qualify(g.pkg, fieldText(s, "text")), nil
						}
					}
				}
			}
		}
	}
	text, err := g.willTypeText(recvID)
	if err != nil {
		return "", err
	}
	return qualify(g.pkg, text), nil
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
		name := fieldText(r, "text")
		if info := g.globalRef(r); info != nil {
			if c, ok := g.recs[decl]; ok && c.Kind == "const" {
				return "", "", g.errf(id, "cannot translate node #%d: a constant is not a place", id)
			}
			return "@" + llvmSymbol(info.sym), info.typ, nil
		}
		if ptr, ok := g.patternPlaces[decl]; ok {
			return ptr, g.patternTypes[decl], nil
		}
		if ptrs, ok := g.loopPtrs[decl]; ok {
			if ptr, ok := ptrs[name]; ok {
				text, ok := g.loopBindType(decl, name)
				if !ok {
					return "", "", g.errf(id, "cannot translate node #%d: the binding has no type", id)
				}
				typ, ok := llvmTypeText(text)
				if !ok {
					return "", "", g.errf(id, "cannot translate node #%d: type %s is not lowered yet", id, text)
				}
				return ptr, typ, nil
			}
		}
		if slot, typ, ok := g.nameSlot(r); ok {
			if typ == "" {
				return "", "", g.errf(id, "cannot translate node #%d: the local has no type", id)
			}
			return slot, typ, nil
		}
		decl = refField(r, "to")
		if name, ok := g.scope[decl]; ok && g.borrows[decl] {
			typ, err := g.declLLVMType(decl)
			if err != nil {
				return "", "", err
			}
			return name, typ, nil
		}
		return "", "", g.errf(id, "cannot translate node #%d: %s is not a place", id, fieldText(r, "text"))
	case "field":
		if info, ok := g.importMember(r); ok {
			if info.isConst {
				return "", "", g.errf(id, "cannot translate node #%d: a constant is not a place", id)
			}
			return "@" + llvmSymbol(info.sym), info.typ, nil
		}
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
	case "slice":
		slot, err := g.sliceValue(r)
		if err != nil {
			return "", "", err
		}
		return slot, "{ ptr, i64 }", nil
	case "index":
		ptr, typ, err := g.indexAddr(r)
		if err != nil {
			return "", "", err
		}
		return ptr, typ, nil
	case "call", "mcall":
		if info, v := g.enumVariantCall(r); info != nil {
			return g.enumConstruct(r, info, v)
		}
		text, err := g.callResultText(r)
		if err != nil {
			return "", "", err
		}
		if text == "" || (!g.structTypes[text] && !g.enumTypes[text]) {
			return "", "", g.errf(id, "cannot translate node #%d: the call result is not an aggregate", id)
		}
		typ := qualifiedTypeName(text)
		if g.enumTypes[text] {
			typ = qualifiedEnumName(text)
		}
		slot := g.callSlots[id]
		if slot == "" {
			return "", "", g.errf(id, "cannot translate node #%d: the call result has no slot", id)
		}
		value, _, err := g.callExpr(r)
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, slot)
		return slot, typ, nil
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
		if isAggregateType(info.types[i]) {
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
