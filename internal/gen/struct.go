package gen

import (
	"fmt"
	"sort"
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
	// foreign marks an instance whose declaration lives in another document.
	// Its fields are known, but its init is not called by this document.
	foreign bool
}

// structDeclInfo is the field list of one struct declaration as the module
// index records it, before any LLVM type is attached.
type structDeclInfo struct {
	name  string
	names []string
	texts []string
}

// packageOfQualified strips the declaration name from a qualified type name.
func packageOfQualified(q string) string {
	if i := strings.LastIndexByte(q, '.'); i >= 0 {
		return q[:i]
	}
	return ""
}

// structInfoByLLVM returns the field layout of a struct type, including one
// declared in another file or package. The field types are lowered on demand,
// so a foreign struct does not need a declaration in the current document.
func (g *generator) structInfoByLLVM(typ string) *structInfo {
	if info := g.byType[typ]; info != nil && info.ok {
		return info
	}
	q := g.structByLLVM[typ]
	if q == "" {
		// The table key and the type spelling can differ in quoting. The
		// qualified struct name itself is unambiguous, so reconstruct it.
		raw := typ
		if strings.HasPrefix(raw, `%"`) && strings.HasSuffix(raw, `"`) {
			raw = raw[2 : len(raw)-1]
		} else if strings.HasPrefix(raw, "%") {
			raw = raw[1:]
		}
		raw = strings.TrimPrefix(raw, "struct.")
		if _, ok := g.structDecls[raw]; ok {
			q = raw
		} else if raw != "" && !strings.Contains(raw, ".") {
			// A field type recorded without its package prefix still names one
			// struct in the module. Accept it only when the suffix is unique.
			for key := range g.structDecls {
				if strings.HasSuffix(key, "."+raw) {
					if q != "" {
						q = ""
						break
					}
					q = key
				}
			}
		}
	}
	if q != "" {
		if _, exists := g.structDecls[q]; !exists && !strings.Contains(q, ".") {
			for key := range g.structDecls {
				if strings.HasSuffix(key, "."+q) {
					q = key
				}
			}
		}
	}
	if _, exists := g.structDecls[q]; !exists {
		return nil
	}
	decl, ok := g.structDecls[q]
	if !ok {
		return nil
	}
	info := &structInfo{name: decl.name, llvm: typ, index: map[string]int{}}
	g.byType[typ] = info
	for i, text := range decl.texts {
		info.index[decl.names[i]] = i
		info.names = append(info.names, decl.names[i])
		info.texts = append(info.texts, text)
		qualified := g.qualifyTypeText(packageOfQualified(q), text)
		field, ok := g.typeLLVMOf(qualified)
		if !ok && qualified != text {
			field, ok = g.typeLLVMOf(text)
		}
		if !ok {
			if _, isStruct := g.structDecls[qualified]; isStruct {
				nested := g.structInfoByLLVM(qualifiedTypeName(qualified))
				if nested != nil && nested.ok {
					field = nested.llvm
					ok = true
				}
			}
		}
		if !ok {
			info.ok = false
			return info
		}
		info.types = append(info.types, field)
	}
	info.ok = true
	return info
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
			g.structDecl[fieldText(r, "text")] = r.ID
		}
	}
	sortByID(structs)
	for _, r := range structs {
		if g.structIsGeneric(r.ID) {
			continue
		}
		g.ensureStruct(r.ID)
	}
	g.planStructInstances()
	// A nested type text, such as the Pair inside Option<Pair<K,V>>, may not
	// have its own type record in a cloned document. Lower every recorded type
	// text and every call result now so its instance is emitted before the
	// functions that use it.
	for _, r := range g.recs {
		switch r.Kind {
		case "type":
			_, _ = g.typeLLVMOf(fieldText(r, "type"))
		case "call", "mcall":
			if text, err := g.callResultText(r); err == nil {
				_, _ = g.typeLLVMOf(text)
			}
		}
	}
	for _, r := range structs {
		if g.structIsGeneric(r.ID) {
			continue
		}
		info := g.structs[r.ID]
		if info == nil || !info.ok {
			continue
		}
		if g.emittedTypes[info.llvm] {
			continue
		}
		g.emittedTypes[info.llvm] = true
		if len(info.types) == 0 {
			fmt.Fprintf(g.out, "%s = type {}\n", info.llvm)
			continue
		}
		fmt.Fprintf(g.out, "%s = type { %s }\n", info.llvm, strings.Join(info.types, ", "))
	}
	var keys []string
	for key := range g.structInstances {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		info := g.structInstances[key]
		if info == nil || !info.ok {
			continue
		}
		if g.emittedTypes[info.llvm] {
			continue
		}
		g.emittedTypes[info.llvm] = true
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
		if typ, ok := llvmTypeText(t); ok {
			info.types = append(info.types, typ)
			continue
		}
		if adt, ok := g.adtType(t); ok {
			info.types = append(info.types, adt.llvm)
			continue
		}
		if t == "Error" {
			g.usedError = true
			info.types = append(info.types, "%interface.Error")
			continue
		}
		sub := g.typeRef(f.ID)
		if sub == 0 {
			// A field names its type through an import alias; the declaration
			// index is keyed by the canonical package path.
			t = g.canonicalTypeText(t)
			qualified := t
			if !strings.Contains(qualified, ".") {
				qualified = qualify(g.pkg, qualified)
			}
			switch g.declKinds[qualified] {
			case "enum":
				info.types = append(info.types, qualifiedEnumName(qualified))
				continue
			case "interface":
				info.types = append(info.types, qualifiedIfaceName(qualified))
				continue
			case "struct":
				info.types = append(info.types, qualifiedTypeName(qualified))
				continue
			}
			if g.declKinds[t] == "enum" {
				info.types = append(info.types, qualifiedEnumName(t))
				continue
			}
			if g.declKinds[t] == "interface" {
				info.types = append(info.types, qualifiedIfaceName(t))
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
		if r, ok := g.recs[sub]; ok && r.Kind == "interface" {
			info.types = append(info.types, g.ifaceLLVM(fieldText(r, "text")))
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
			return stripPredeclaredPackage(textField(r, "to"))
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
	if r.Kind == "param" && fieldText(r, "variadic") == "true" {
		return "{ ptr, i64 }", nil
	}
	if r.Kind == "var" || r.Kind == "const" {
		if t, ok := g.slotTypes[decl]; ok {
			return t, nil
		}
	}
	if fieldText(r, "type") == "interface {}" {
		return "%interface.empty", nil
	}
	if typ, ok := llvmTypeText(fieldText(r, "type")); ok {
		return typ, nil
	}
	if strings.HasPrefix(fieldText(r, "type"), "func(") {
		return "{ ptr, ptr }", nil
	}
	if typ, ok := g.structInstanceFromText(fieldText(r, "type")); ok {
		return typ, nil
	}
	if adt, ok := g.adtType(fieldText(r, "type")); ok {
		return adt.llvm, nil
	}
	if typ, ok := g.fieldLLVMType(fieldText(r, "type")); ok {
		return typ, nil
	}
	if id := g.typeRef(decl); id != 0 {
		if r, ok := g.recs[id]; ok && r.Kind == "enum" {
			if info := g.enums[id]; info != nil {
				return info.llvm, nil
			}
			return "", g.errf(decl, "cannot translate node #%d: enum %s is not lowered yet", decl, fieldText(r, "text"))
		}
		if r, ok := g.recs[id]; ok && r.Kind == "interface" {
			if info := g.ifaces[id]; info != nil {
				return info.llvm, nil
			}
			return "", g.errf(decl, "cannot translate node #%d: interface %s is not lowered yet", decl, fieldText(r, "text"))
		}
		if info, ok := g.ensureStruct(id); ok {
			return info.llvm, nil
		}
		return "", g.errf(decl, "cannot translate node #%d: struct %s has a field type that is not lowered yet", decl, fieldText(r, "type"))
	}
	if q := g.typeQualified(decl); q != "" {
		// The declaring package emits the type definition; a reference from
		// this document only needs the same name.
		switch g.declKinds[q] {
		case "enum":
			return qualifiedEnumName(q), nil
		case "interface":
			return qualifiedIfaceName(q), nil
		case "struct":
			return qualifiedTypeName(q), nil
		}
		if g.enumTypes[q] {
			return qualifiedEnumName(q), nil
		}
		if g.ifaceTypes[q] {
			return qualifiedIfaceName(q), nil
		}
		return qualifiedTypeName(q), nil
	}
	if fieldText(r, "type") == "Error" {
		g.usedError = true
		return "%interface.Error", nil
	}
	if fieldText(r, "type") == "Show" {
		g.showUsed = true
		return "%interface.Show", nil
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
	case "await":
		return g.typeOf(refField(r, "expr"))
	case "lit":
		typ, ok := llvmTypeText(fieldText(r, "type"))
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: literal type %s is not lowered yet", id, fieldText(r, "type"))
		}
		return typ, nil
	case "name":
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if name == "Canceled" || name == "Deadline" {
			return "%interface.Error", nil
		}
		if c, ok := g.recs[decl]; ok && c.Kind == "func" {
			return "{ ptr, ptr }", nil
		}
		if info := g.globalRef(r); info != nil {
			return info.typ, nil
		}
		if t := fieldText(r, "type"); t != "" {
			if adt, ok := g.adtType(t); ok {
				return adt.llvm, nil
			}
		}
		if typ, ok := g.patternTypes[decl]; ok {
			return typ, nil
		}
		if text, ok := g.selectBindTypes[decl]; ok {
			info, ok := g.adtType(text)
			if !ok {
				return "", g.errf(id, "cannot translate node #%d: %s is not lowered yet", id, text)
			}
			return info.llvm, nil
		}
		if text, ok := g.loopBindType(decl, name); ok {
			typ, ok := g.typeLLVMOf(text)
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
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			if elem, ok := g.guardElemText(recvText); ok {
				elemLLVM, ok := g.fieldLLVMType(elem)
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: guard value %s is not lowered yet", id, elem)
				}
				info := g.structInfoByLLVM(elemLLVM)
				if info == nil || !info.ok {
					return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, elemLLVM)
				}
				index, ok := info.index[fieldText(r, "text")]
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
				}
				return info.types[index], nil
			}
		}
		base, err := g.typeOf(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			if elem, ok := rcElemText(recvText); ok && isRcText(recvText) {
				if t, ok := g.typeLLVMOf(elem); ok {
					base = t
				}
			}
		}
		info := g.structInfoByLLVM(base)
		if info == nil || !info.ok {
			return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, base)
		}
		i, ok := info.index[fieldText(r, "text")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
		}
		return info.types[i], nil
	case "agg":
		info, ok := g.aggStructInfo(r)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: the aggregate type is not lowered yet", id)
		}
		return info.llvm, nil
	case "interp":
		return "{ ptr, i64 }", nil
	case "spawn", "channew":
		return "ptr", nil
	case "lanehere":
		return "i64", nil
	case "send":
		info, ok := g.adtType("Result<(), Error>")
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: Result<(), Error> is not lowered", id)
		}
		return info.llvm, nil
	case "close":
		return "", nil
	case "recv", "tryrecv", "join":
		text, err := g.willTypeText(id)
		if err != nil {
			return "", err
		}
		info, ok := g.adtType(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: %s is not lowered yet", id, text)
		}
		return info.llvm, nil
	case "trysend":
		return "i1", nil
	case "func":
		if _, ok := g.lambdaNames[r.ID]; ok {
			return "{ ptr, ptr }", nil
		}
		return "", g.errf(id, "cannot translate node #%d: a function declaration is not a value", id)
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
		typ, ok := g.typeLLVMOf(elem)
		if !ok || typ == "" {
			return "", g.errf(id, "cannot translate node #%d: element type %s is not lowered yet", id, elem)
		}
		return typ, nil
	case "cast":
		to, ok := g.recs[refField(r, "to")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: the target type is missing", id)
		}
		if text := fieldText(to, "text"); text == "str" || text == "[]u8" {
			return "{ ptr, i64 }", nil
		}
		typ, _, ok := llvmScalar(fieldText(to, "text"))
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: conversion to %s is not lowered yet", id, fieldText(to, "text"))
		}
		return typ, nil
	case "match":
		return g.matchValueType(r)
	case "try":
		typ, _, err := g.trySuccessType(r)
		return typ, err
	case "unwrap":
		typ, _, err := g.unwrapType(r)
		return typ, err
	case "new":
		text := g.newResultText(r)
		typ, ok := g.typeLLVMOf(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: type %s is not lowered yet", id, text)
		}
		return typ, nil
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
		if arg, ok := g.waitArg(r); ok {
			return g.typeOf(arg)
		}
		if r.Kind == "mcall" {
			if llvm, _, known := g.strCallResult(r); known {
				if llvm == "" {
					return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
				}
				return llvm, nil
			}
			if llvm, _, known := g.containerCallResult(r); known {
				if llvm == "" {
					return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
				}
				return llvm, nil
			}
			if llvm, _, known := g.viewCallResult(r); known {
				return llvm, nil
			}
			if llvm, _, known := g.arrayCallResult(r); known {
				if llvm == "" {
					return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
				}
				return llvm, nil
			}
			if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
				if iface, ok := g.ifaceOfText(recvText); ok {
					if idx, ok := iface.byName[fieldText(r, "text")]; ok {
						result := iface.methods[idx].result
						if result == "" {
							return "", g.errf(id, "cannot translate node #%d: the call returns no value", id)
						}
						return g.resultLLVM(result), nil
					}
				}
			}
		}
		if t := fieldText(r, "type"); t != "" {
			if adt, ok := g.adtType(t); ok {
				return adt.llvm, nil
			}
		}
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
		typ, ok := llvmTypeText(text)
		if !ok {
			if adt, ok := g.adtType(text); ok {
				return adt.llvm, nil
			}
			if g.structTypes[text] {
				return qualifiedTypeName(text), nil
			}
			if g.enumTypes[text] {
				return qualifiedEnumName(text), nil
			}
			if g.ifaceTypes[text] {
				return qualifiedIfaceName(text), nil
			}
			if typ, ok := g.typeLLVMOf(text); ok && typ != "" {
				return typ, nil
			}
			if id := g.structDecl[text]; id != 0 {
				info, ok := g.ensureStruct(id)
				if ok {
					return info.llvm, nil
				}
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
	case "await":
		return g.willTypeText(refField(r, "expr"))
	case "lit":
		return fieldText(r, "type"), nil
	case "name":
		name := fieldText(r, "text")
		if name == "Canceled" || name == "Deadline" {
			return "Error", nil
		}
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
		if text, ok := g.selectBindTypes[decl.ID]; ok {
			return text, nil
		}
		if info, _ := g.enumVariantCall(r); info != nil {
			return info.text, nil
		}
		if fieldText(decl, "variadic") == "true" {
			return "[]" + fieldText(decl, "type"), nil
		}
		if t := fieldText(r, "type"); t != "" {
			return t, nil
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
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			if elem, ok := g.guardElemText(recvText); ok {
				elemLLVM, ok := g.fieldLLVMType(elem)
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: guard value %s is not lowered yet", id, elem)
				}
				info := g.structInfoByLLVM(elemLLVM)
				if info == nil || !info.ok {
					return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, elemLLVM)
				}
				index, ok := info.index[fieldText(r, "text")]
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
				}
				return info.texts[index], nil
			}
		}
		base, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		if elem, ok := rcElemText(base); ok && isRcText(base) {
			base = elem
		}
		info := g.byName[base]
		if info == nil {
			if t, ok := g.typeLLVMOf(base); ok {
				info = g.structInfoByLLVM(t)
			}
		}
		if info == nil {
			info = g.structInfoByLLVM(base)
		}
		if info == nil || !info.ok {
			return "", g.errf(id, "cannot translate node #%d: %s has no fields", id, base)
		}
		i, ok := info.index[fieldText(r, "text")]
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: struct %s has no field %s", id, info.name, fieldText(r, "text"))
		}
		return info.texts[i], nil
	case "agg":
		if to := refField(r, "to"); to != 0 {
			if c, ok := g.recs[to]; ok {
				return fieldText(c, "text"), nil
			}
		}
		return "", nil
	case "interp":
		return "str", nil
	case "spawn":
		text, err := g.willTypeText(refField(r, "call"))
		if err != nil {
			return "", err
		}
		return "Task<" + text + ">", nil
	case "channew":
		return fieldText(r, "type"), nil
	case "lanehere":
		return "usize", nil
	case "recv", "tryrecv":
		chanText, err := g.willTypeText(refField(r, "chan"))
		if err != nil {
			return "", err
		}
		elem, ok := chanElemText(chanText)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: Recv needs a channel", id)
		}
		return "Option<" + elem + ">", nil
	case "trysend":
		return "bool", nil
	case "join":
		taskText, err := g.willTypeText(refField(r, "task"))
		if err != nil {
			return "", err
		}
		elem, ok := taskElemText(taskText)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: Join needs a Task", id)
		}
		return "Result<" + elem + ",TaskError>", nil
	case "send":
		return "Result<(),Error>", nil
	case "close":
		return "", nil
	case "slice":
		elem, err := g.sliceElem(r)
		if err != nil {
			return "", err
		}
		return "[]" + elem, nil
	case "index":
		text, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		elem, ok := elementText(text)
		if !ok {
			return "", g.errf(id, "cannot translate node #%d: only an array, a view, or an Array is indexed yet", id)
		}
		return elem, nil
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
		if arg, ok := g.waitArg(r); ok {
			return g.willTypeText(arg)
		}
		if text, ok := g.downcastTypeText(r); ok {
			return text, nil
		}
		if r.Kind == "mcall" {
			if _, _, fnText, ok := g.functionField(r); ok {
				_, result, ok := funcTypeParts(fnText)
				if ok {
					return result, nil
				}
			}
			if _, text, known := g.arrayCallResult(r); known {
				return text, nil
			}
			if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
				if iface, ok := g.ifaceOfText(recvText); ok {
					if idx, ok := iface.byName[fieldText(r, "text")]; ok {
						return iface.methods[idx].result, nil
					}
				}
			}
		}
		if t := fieldText(r, "type"); t != "" {
			return t, nil
		}
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
	case "try":
		_, text, err := g.trySuccessType(r)
		return text, err
	case "unwrap":
		_, text, err := g.unwrapType(r)
		return text, err
	case "new":
		return g.newResultText(r), nil
	}
	return "", nil
}

// callResultText returns the result type text of one call. A call names its
// callee by a document reference or by the module-wide symbol.
func (g *generator) callResultText(r willc.Record) (string, error) {
	if r.Kind == "mcall" && fieldText(r, "text") == "Clone" {
		recvText, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", err
		}
		if elem, ok := viewElemText(stripPredeclaredPackage(recvText)); ok {
			return "Array<" + elem + ">", nil
		}
		return recvText, nil
	}
	if text, ok := g.downcastTypeText(r); ok {
		return text, nil
	}
	if info, _ := g.enumVariantCall(r); info != nil {
		return info.text, nil
	}
	if name, ok := g.predeclaredCallName(r); ok {
		switch name {
		case "take_c", "take_c_rc":
			if args := g.typeArgTexts(r); len(args) == 1 {
				return "rc<" + args[0] + ">", nil
			}
		case "SizeOf", "AlignOf", "OffsetOf":
			return "usize", nil
		}
	}
	if text, known := g.rcCallResult(r); known {
		return text, nil
	}
	if text := g.genericCallResult(r); text != "" {
		return text, nil
	}
	if text := g.genericMethodResult(r); text != "" {
		return text, nil
	}
	if r.Kind == "mcall" {
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			if iface, ok := g.ifaceOfText(recvText); ok {
				if idx, ok := iface.byName[fieldText(r, "text")]; ok {
					return iface.methods[idx].result, nil
				}
			}
		}
		if sym := g.genericMethodName(r); sym != "" {
			if text, ok := g.sigs[sym]; ok {
				return text, nil
			}
			// A generic method with no result is void; its instance is
			// planned even though it has no signature entry.
			return "", nil
		}
	}
	if _, text, known := g.strCallResult(r); known {
		return text, nil
	}
	if _, text, known := g.containerCallResult(r); known {
		return text, nil
	}
	if _, text, known := g.viewCallResult(r); known {
		return text, nil
	}
	if _, text, known := g.arrayCallResult(r); known {
		return text, nil
	}
	if _, text, known := g.rawCallResult(r); known {
		return text, nil
	}
	if text, known := g.functionValueResult(r); known {
		return text, nil
	}
	if qualified := textField(r, "to"); qualified != "" {
		if t, ok := g.sigs[qualified]; ok {
			return t, nil
		}
		return "", g.errf(r.ID, "cannot translate node #%d: %s is not a function of this module", r.ID, qualified)
	}
	if r.Kind == "mcall" && g.isCLink(r) {
		name := fieldText(r, "text")
		if g.linkBlock[name] {
			if sig, ok := g.linkSigs[name]; ok && sig.typed {
				result := sig.result
				if result == "" {
					result = "()"
				}
				return "Task<" + result + ">", nil
			}
			return "Task<i32>", nil
		}
		if sig, ok := g.linkSigs[fieldText(r, "text")]; ok && sig.typed {
			if sig.result == "" || sig.result == "()" {
				return "", nil
			}
			return sig.result, nil
		}
		return "", nil
	}
	if r.Kind == "mcall" {
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			if iface, ok := g.ifaceOfText(recvText); ok {
				if idx, ok := iface.byName[fieldText(r, "text")]; ok {
					return iface.methods[idx].result, nil
				}
			}
		}
		if typ, err := g.typeOf(refField(r, "recv")); err == nil {
			if iface, ok := g.ifaceOfType(typ); ok {
				if idx, ok := iface.byName[fieldText(r, "text")]; ok {
					return iface.methods[idx].result, nil
				}
			}
		}
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			recvText = stripPredeclaredPackage(recvText)
			if kind, args, ok := parseADT(recvText); ok {
				switch fieldText(r, "text") {
				case "IsSome", "IsNone", "IsOk", "IsErr":
					return "bool", nil
				case "Or":
					if len(args) != 0 {
						return args[0], nil
					}
				}
				_ = kind
			}
		}
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
	name := fieldText(r, "text")
	if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
		if elem, ok := g.guardElemText(recvText); ok {
			if sym := g.methodForType(elem, name); sym != "" {
				return sym, nil
			}
		}
	}
	typ, err := g.receiverTypeName(refField(r, "recv"))
	if err != nil {
		return "", err
	}
	if elem, ok := g.guardElemText(typ); ok {
		if sym := g.methodForType(elem, name); sym != "" {
			return sym, nil
		}
	}
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
					if text := fieldText(c, "text"); strings.Contains(text, "<") {
						return qualify(g.pkg, text), nil
					}
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
	if strings.Contains(text, ".") {
		return g.canonicalTypeText(text), nil
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
	if snap, ok := g.deferSnapshots[id]; ok {
		return g.deferFieldPtr(snap), snap.llvm, nil
	}
	if p, ok := g.smPlaces[id]; ok {
		return p.ptr, p.typ, nil
	}
	switch r.Kind {
	case "await":
		return g.place(refField(r, "expr"))
	case "lit":
		kind := fieldText(r, "kind")
		if kind == "str" || kind == "bytes" {
			return g.stringPlace(r)
		}
		return "", "", g.errf(id, "cannot translate node #%d: a %s literal is a value, not a place", id, kind)
	case "send":
		slot, err := g.lowerSendResult(r)
		if err != nil {
			return "", "", err
		}
		info, ok := g.adtType("Result<(), Error>")
		if !ok {
			return "", "", g.errf(id, "cannot translate node #%d: Result<(), Error> is not lowered", id)
		}
		return slot, info.llvm, nil
	case "join":
		slot, err := g.lowerJoinResult(r)
		if err != nil {
			return "", "", err
		}
		text, err := g.willTypeText(id)
		if err != nil {
			return "", "", err
		}
		info, ok := g.adtType(text)
		if !ok {
			return "", "", g.errf(id, "cannot translate node #%d: %s is not lowered", id, text)
		}
		return slot, info.llvm, nil
	case "name":
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if name == "Canceled" || name == "Deadline" {
			return g.builtinErrorPlace(r, name)
		}
		if c, ok := g.recs[decl]; ok && c.Kind == "func" {
			return g.placeNamedFunction(r, c)
		}
		if info := g.globalRef(r); info != nil {
			if c, ok := g.recs[decl]; ok && c.Kind == "const" {
				return "", "", g.errf(id, "cannot translate node #%d: a constant is not a place", id)
			}
			return "@" + llvmSymbol(info.sym), info.typ, nil
		}
		if adt, variant, arg, ok := g.adtConstructor(r); ok {
			return g.adtConstruct(r, adt, variant, arg)
		}
		if info, v := g.enumVariantCall(r); info != nil {
			return g.enumConstruct(r, info, v)
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
				typ, ok := g.typeLLVMOf(text)
				if !ok || typ == "" {
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
		var base, typ string
		recvID := refField(r, "recv")
		recvText, rcErr := g.willTypeText(recvID)
		if rcErr == nil {
			if _, ok := g.guardElemText(recvText); ok {
				ptr, fieldType, _, err := g.guardFieldPlace(recvID, fieldText(r, "text"), r)
				if err != nil {
					return "", "", err
				}
				return ptr, fieldType, nil
			}
			if elem, ok := rcElemText(recvText); ok && isRcText(recvText) {
				handle, err := g.value(recvID)
				if err != nil {
					return "", "", err
				}
				g.needRcDecl = true
				object := g.newTmp()
				fmt.Fprintf(g.out, "  %s = call ptr @willrt_rc_deref(i64 %s)\n", object, handle)
				typ, ok = g.typeLLVMOf(elem)
				if !ok {
					return "", "", g.errf(id, "cannot translate node #%d: rc value %s is not lowered yet", id, elem)
				}
				base = object
			}
		}
		if base == "" {
			var err error
			base, typ, err = g.place(recvID)
			if err != nil {
				return "", "", err
			}
		}
		info := g.structInfoByLLVM(typ)
		if info == nil || !info.ok {
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
	case "func":
		if _, ok := g.lambdaNames[r.ID]; ok {
			return g.closurePlace(r)
		}
		return "", "", g.errf(id, "cannot translate node #%d: a function declaration is not a place", id)
	case "agg":
		info, ok := g.aggStructInfo(r)
		if !ok {
			return "", "", g.errf(id, "cannot translate node #%d: the aggregate type is not lowered yet", id)
		}
		slot := g.allocSlot(g.aggSlots, "agg", id, info.llvm)
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
	case "bin":
		if g.isStrConcat(r) {
			return g.strConcat(r)
		}
		return "", "", g.errf(id, "cannot translate node #%d: a binary expression is not a place", id)
	case "interp":
		return g.interpValue(r)
	case "cast":
		if to, ok := g.recs[refField(r, "to")]; ok {
			if text := fieldText(to, "text"); text == "str" || text == "[]u8" {
				return g.castPlace(r)
			}
		}
		return "", "", g.errf(id, "cannot translate node #%d: a scalar cast is not a place", id)
	case "recv":
		return g.lowerRecvPlace(r)
	case "tryrecv":
		return g.lowerTryRecv(r)
	case "try":
		return g.tryPlace(r)
	case "unwrap":
		return g.unwrapPlace(r)
	case "match":
		terminated, err := g.lowerMatch(r, true, false)
		if err != nil {
			return "", "", err
		}
		if terminated {
			return "", "", g.errf(r.ID, "cannot translate node #%d: every arm of the match returns", r.ID)
		}
		slot := g.matchSlots[r.ID]
		typ := g.matchSlotTypes[r.ID]
		if slot == "" || typ == "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the match has no result slot", r.ID)
		}
		return slot, typ, nil
	case "new":
		return g.constructNew(r)
	case "call", "mcall":
		if arg, ok := g.waitArg(r); ok {
			return g.place(arg)
		}
		if d, ok := g.downcastOf(r); ok {
			if !d.isAs {
				return "", "", g.errf(r.ID, "cannot translate node #%d: a bool is not a place", r.ID)
			}
			return g.ifaceDowncastAs(r)
		}
		if g.smGeneral {
			if sym := g.awaitTargetSymbol(r); sym != "" && g.suspendableFor(sym) {
				return g.generalCallWaitPlace(r)
			}
		} else if sym := g.awaitTargetSymbol(r); sym != "" && g.suspendableFor(sym) {
			return g.awaitCallPlace(r)
		}
		if r.Kind == "mcall" && fieldText(r, "text") == "Get" {
			if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
				if elem, ok := viewElemText(recvText); ok {
					if typ, ok := g.typeLLVMOf(elem); ok && isAggregateType(typ) {
						return g.viewGetPlace(r, elem)
					}
				}
			}
		}
		if r.Kind == "mcall" && fieldText(r, "text") == "Index" {
			if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
				if elem, ok := viewElemText(recvText); ok && elem == "u8" {
					return g.viewIndexPlace(r)
				}
			}
		}
		if adt, variant, arg, ok := g.adtConstructor(r); ok {
			return g.adtConstruct(r, adt, variant, arg)
		}
		if info, v := g.enumVariantCall(r); info != nil {
			return g.enumConstruct(r, info, v)
		}
		text, err := g.callResultText(r)
		if err != nil {
			return "", "", err
		}
		typ := ""
		if t, ok := llvmTypeText(text); ok && isAggregateType(t) {
			typ = t
		} else if adt, ok := g.adtType(text); ok {
			typ = adt.llvm
		} else if text != "" && g.structTypes[text] {
			typ = qualifiedTypeName(text)
		} else if text != "" && g.enumTypes[text] {
			typ = qualifiedEnumName(text)
		} else if text != "" {
			if t, ok := g.typeLLVMOf(text); ok && isAggregateType(t) {
				typ = t
			}
		}
		if typ == "" {
			return "", "", g.errf(id, "cannot translate node #%d: the call result is not an aggregate", id)
		}
		slot := g.allocSlot(g.callSlots, "ret", id, typ)
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
			if iface, ok := g.ifaceOfText(info.texts[i]); ok {
				valueText, err := g.willTypeText(valueID)
				if err != nil {
					return err
				}
				if _, same := g.ifaceOfText(valueText); !same {
					if _, err := g.ifaceValue(ptr, valueID, iface, valueText); err != nil {
						return err
					}
					continue
				}
			}
			src, typ, err := g.place(valueID)
			if err != nil {
				return err
			}
			if typ != info.types[i] {
				return g.errf(valueID, "cannot translate node #%d: field %s needs %s", valueID, name, info.types[i])
			}
			move, _ := g.moveSource(valueID)
			if !move && g.needsReleaseText(info.texts[i]) && g.isCopySource(valueID) {
				if err := g.retainValue(src, info.texts[i]); err != nil {
					return err
				}
			}
			g.copyValue(src, ptr, typ)
			if move {
				if err := g.zeroMovedSource(valueID, ptr); err != nil {
					return err
				}
			}
			continue
		}
		v, err := g.value(valueID)
		if err != nil {
			return err
		}
		if isRcText(info.texts[i]) && g.isCopySource(valueID) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", v)
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
