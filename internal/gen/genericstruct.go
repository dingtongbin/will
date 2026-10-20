package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// structInstanceKey is the will spelling of one instantiation.
func structInstanceKey(name string, args []string) string {
	return name + "<" + strings.Join(args, ",") + ">"
}

// splitGenericType splits Name<A,B> into its name and its top-level arguments.
func splitGenericType(text string) (string, []string, bool) {
	open := strings.IndexByte(text, '<')
	if open <= 0 || !strings.HasSuffix(text, ">") {
		return "", nil, false
	}
	inner := text[open+1 : len(text)-1]
	var args []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '<', '[', '(':
			depth++
		case '>', ']', ')':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	args = append(args, strings.TrimSpace(inner[start:]))
	return text[:open], args, true
}

func isTypeIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// substituteTypeText replaces whole type-parameter names inside a type text.
// Identifiers are matched as a whole, so a parameter T never rewrites Tree.
func substituteTypeText(text string, subst map[string]string) string {
	if len(subst) == 0 {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if !isTypeIdentByte(text[i]) {
			b.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) && isTypeIdentByte(text[j]) {
			j++
		}
		word := text[i:j]
		if repl, ok := subst[word]; ok {
			b.WriteString(repl)
		} else {
			b.WriteString(word)
		}
		i = j
	}
	return b.String()
}

// structIsGeneric reports whether a struct declaration has type parameters.
func (g *generator) structIsGeneric(id int) bool {
	for _, r := range g.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == id {
			return true
		}
	}
	return false
}

// typeRecordArgs returns the type argument texts a type record names.
func (g *generator) typeRecordArgs(r willc.Record) []string {
	var texts []string
	for _, id := range refsOf(r, "arg") {
		if t, ok := g.recs[id]; ok {
			texts = append(texts, fieldText(t, "text"))
		}
	}
	return texts
}

// structInstanceFromText instantiates a generic struct named by a type text.
// It returns false when the text does not name a generic struct of this
// document.
func (g *generator) structInstanceFromText(text string) (string, bool) {
	name, args, ok := splitGenericType(text)
	if !ok {
		return "", false
	}
	id, ok := g.structDecl[name]
	if !ok {
		return "", false
	}
	info, ok := g.ensureStructInstance(id, args)
	if !ok {
		return "", false
	}
	return info.llvm, true
}

// ensureStructInstance lowers one instantiation of a generic struct. The
// record is inserted before its fields are visited, so a recursive
// instantiation that never reaches a concrete type is reported as not lowered
// instead of looping.
func (g *generator) ensureStructInstance(id int, args []string) (*structInfo, bool) {
	decl, ok := g.recs[id]
	if !ok || decl.Kind != "struct" {
		return nil, false
	}
	name := fieldText(decl, "text")
	key := structInstanceKey(name, args)
	if info, ok := g.structInstances[key]; ok {
		return info, info.ok
	}
	info := &structInfo{
		id:    id,
		name:  key,
		llvm:  g.structLLVM(instanceName(name, args)),
		index: map[string]int{},
	}
	g.structInstances[key] = info
	g.byType[info.llvm] = info
	g.byName[key] = info
	params := g.typeParamNames(decl)
	if len(params) != len(args) {
		return info, false
	}
	subst := make(map[string]string, len(params))
	for i, p := range params {
		subst[p] = args[i]
	}
	var fields []willc.Record
	for _, r := range g.recs {
		if r.Kind == "field" && refField(r, "parent") == id {
			fields = append(fields, r)
		}
	}
	sortByID(fields)
	info.ok = true
	for i, f := range fields {
		text := substituteTypeText(fieldText(f, "type"), subst)
		typ, ok := g.fieldLLVMType(text)
		if !ok {
			info.ok = false
			break
		}
		info.index[fieldText(f, "text")] = i
		info.names = append(info.names, fieldText(f, "text"))
		info.texts = append(info.texts, text)
		info.types = append(info.types, typ)
	}
	if !info.ok {
		info.types = nil
	}
	return info, info.ok
}

// fieldLLVMType lowers the type of one struct field after substitution.
func (g *generator) fieldLLVMType(text string) (string, bool) {
	if typ, _, ok := llvmScalar(text); ok {
		return typ, true
	}
	if typ, ok := llvmTypeText(text); ok {
		return typ, true
	}
	if adt, ok := g.adtType(text); ok {
		return adt.llvm, true
	}
	if typ, ok := g.structInstanceFromText(text); ok {
		return typ, true
	}
	if id, ok := g.structDecl[text]; ok {
		if info, ok := g.ensureStruct(id); ok {
			return info.llvm, true
		}
	}
	if id, ok := g.enumDecl[text]; ok {
		return g.enumLLVM(fieldText(g.recs[id], "text")), true
	}
	if id, ok := g.ifaceDecl[text]; ok {
		return g.ifaceLLVM(fieldText(g.recs[id], "text")), true
	}
	return "", false
}

// planStructInstances finds every generic struct use in the document. The
// type definitions are emitted before the functions, so the uses have to be
// known before lowering starts.
func (g *generator) planStructInstances() {
	for _, r := range g.recs {
		switch r.Kind {
		case "type":
			to := refField(r, "to")
			if to == 0 || !g.structIsGeneric(to) {
				continue
			}
			if args := g.typeRecordArgs(r); len(args) > 0 {
				g.ensureStructInstance(to, args)
			}
		case "agg":
			to := refField(r, "to")
			if to == 0 || !g.structIsGeneric(to) {
				continue
			}
			if args := g.typeArgTexts(r); len(args) > 0 {
				g.ensureStructInstance(to, args)
			}
		case "var", "param", "receiver":
			text := fieldText(r, "type")
			if _, _, ok := splitGenericType(text); ok {
				g.structInstanceFromText(text)
			}
		case "new":
			g.structInstanceFromText(fieldText(r, "type"))
		}
	}
}

// aggStructInfo returns the struct info an aggregate literal builds,
// instantiating a generic declaration when the literal carries arguments.
func (g *generator) aggStructInfo(r willc.Record) (*structInfo, bool) {
	id := refField(r, "to")
	if id == 0 {
		// A type declared in another file is written by qualified name.
		if q := textField(r, "to"); q != "" {
			info := g.structInfoByLLVM(qualifiedTypeName(q))
			return info, info != nil && info.ok
		}
		return nil, false
	}
	if args := g.typeArgTexts(r); len(args) > 0 && g.structIsGeneric(id) {
		return g.ensureStructInstance(id, args)
	}
	return g.ensureStruct(id)
}
