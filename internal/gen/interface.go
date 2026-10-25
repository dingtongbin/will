package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// ifaceMethod is one method signature of an interface.
type ifaceMethod struct {
	name   string
	result string
	params []string
}

// ifaceInfo is the lowered form of one interface. The value is two words: the
// data pointer and the method table pointer.
type ifaceInfo struct {
	id      int
	name    string
	llvm    string
	methods []ifaceMethod
	byName  map[string]int
}

// buildInterfaces records every interface of the current document and emits
// its two-word type. The method order is the declaration order, so a method
// table entry is fixed.
func (g *generator) buildInterfaces() {
	if g.errorIface == nil {
		g.errorIface = &ifaceInfo{
			name: "Error",
			llvm: "%interface.Error",
			methods: []ifaceMethod{
				{name: "Message", result: "str"},
				{name: "Status", result: "i32"},
			},
			byName: map[string]int{"Message": 0, "Status": 1},
		}
	}
	g.ifaceByName["Error"] = g.errorIface
	g.ifaceByName["TaskError"] = g.errorIface
	g.ifaceByLLVM["%interface.Error"] = g.errorIface
	if g.showIface == nil {
		g.showIface = &ifaceInfo{
			name:    "Show",
			llvm:    "%interface.Show",
			methods: []ifaceMethod{{name: "Show", result: "str"}},
			byName:  map[string]int{"Show": 0},
		}
	}
	g.ifaceByName["Show"] = g.showIface
	g.ifaceByLLVM["%interface.Show"] = g.showIface
	if !g.showUsed {
		for _, r := range g.recs {
			if fieldText(r, "text") == "Show" || fieldText(r, "type") == "Show" {
				g.showUsed = true
				break
			}
		}
	}
	if g.showUsed && !g.emittedTypes["%interface.Show"] {
		g.emittedTypes["%interface.Show"] = true
		fmt.Fprintf(g.out, "%%interface.Show = type { ptr, ptr }\n")
	}
	var ifaces []willc.Record
	for _, r := range g.recs {
		if r.Kind == "interface" {
			ifaces = append(ifaces, r)
		}
	}
	sortByID(ifaces)
	for _, r := range ifaces {
		info := &ifaceInfo{id: r.ID, name: fieldText(r, "text"), byName: map[string]int{}}
		info.llvm = g.ifaceLLVM(info.name)
		g.ifaces[r.ID] = info
		g.ifaceByLLVM[info.llvm] = info
		g.ifaceByName[info.name] = info
		var sigs []willc.Record
		for _, c := range g.recs {
			if c.Kind == "method-sig" && refField(c, "parent") == r.ID {
				sigs = append(sigs, c)
			}
		}
		sortByID(sigs)
		for _, s := range sigs {
			m := ifaceMethod{name: fieldText(s, "text"), result: g.qualifyTypeText(g.pkg, fieldText(s, "type"))}
			var params []willc.Record
			for _, c := range g.recs {
				if c.Kind == "param" && refField(c, "parent") == s.ID {
					params = append(params, c)
				}
			}
			sortByID(params)
			for _, p := range params {
				m.params = append(m.params, g.qualifyTypeText(g.pkg, fieldText(p, "type")))
			}
			info.byName[m.name] = len(info.methods)
			info.methods = append(info.methods, m)
		}
	}
	for _, r := range ifaces {
		info := g.ifaces[r.ID]
		if info == nil || g.emittedTypes[info.llvm] {
			continue
		}
		g.emittedTypes[info.llvm] = true
		fmt.Fprintf(g.out, "%s = type { ptr, ptr }\n", info.llvm)
	}
}

// ifaceLLVM names one interface type. The package path keeps two packages'
// interfaces apart, like a struct.
func (g *generator) ifaceLLVM(name string) string {
	body := "interface." + name
	if g.pkg != "" {
		body = "interface." + g.pkg + "." + name
	}
	return "%" + llvmSymbol(body)
}

// qualifiedIfaceName renders a package-qualified interface name.
func qualifiedIfaceName(name string) string {
	return "%" + llvmSymbol("interface."+name)
}

// isIfaceType reports whether an LLVM type names an interface.
func isIfaceType(typ string) bool {
	return strings.HasPrefix(typ, "%interface.") || strings.HasPrefix(typ, `%"interface.`)
}

// ifaceOfText returns the interface a type text names, when this document has
// its declaration.
func (g *generator) ifaceOfText(text string) (*ifaceInfo, bool) {
	if text == "TaskError" {
		text = "Error"
	}
	if info := g.ifaceByName[text]; info != nil {
		if info == g.errorIface {
			g.usedError = true
		}
		return info, true
	}
	q := g.canonicalTypeText(text)
	if info := g.ifaceDecls[q]; info != nil {
		return info, true
	}
	if i := strings.LastIndexByte(q, '.'); i > 0 {
		if info := g.ifaceByName[q[i+1:]]; info != nil {
			return info, true
		}
		if info, ok := g.ifaceOfShortName(q[i+1:]); ok {
			return info, true
		}
	}
	if !strings.Contains(q, ".") {
		if info := g.ifaceDecls[qualify(g.pkg, q)]; info != nil {
			return info, true
		}
	}
	return nil, false
}

// ifaceOfShortName finds an interface by the bare name another package writes
// in a signature. A document that calls such a function does not import the
// package that declares the interface, so only the bare name reaches it. Two
// packages declaring the same bare name leave the choice open, and an open
// choice is reported as no match.
func (g *generator) ifaceOfShortName(name string) (*ifaceInfo, bool) {
	var found *ifaceInfo
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "interface" || fieldText(r, "text") != name {
				continue
			}
			info := g.ifaceDecls[qualify(d.path, name)]
			if info == nil {
				continue
			}
			if found != nil && found != info {
				return nil, false
			}
			found = info
		}
	}
	return found, found != nil
}

// ifaceOfType returns the interface an LLVM type names.
func (g *generator) ifaceOfType(typ string) (*ifaceInfo, bool) {
	info := g.ifaceByLLVM[typ]
	return info, info != nil
}

// ifaceValue writes one interface value into slot: the data pointer of the
// concrete value and the method table for that type.
func (g *generator) ifaceValue(slot string, valueID int, iface *ifaceInfo, concrete string) (string, error) {
	concrete = g.canonicalTypeText(concrete)
	expected, ok := g.typeLLVMOf(concrete)
	if !ok {
		return "", g.errf(valueID, "cannot translate node #%d: %s is not lowered yet", valueID, concrete)
	}
	data := ""
	if showScalar(concrete) {
		// A scalar has no address of its own, so the interface keeps one in a
		// slot and the value is stored there.
		data = g.ifaceDataSlots[valueID]
		if data == "" {
			data = g.newTmp()
			fmt.Fprintf(g.out, "  %s = alloca %s\n", data, expected)
			g.ifaceDataSlots[valueID] = data
		}
		if concrete == "str" {
			// A str is a two-word place, not a scalar value.
			ptr, typ, err := g.place(valueID)
			if err != nil {
				return "", err
			}
			if typ != expected {
				return "", g.errf(valueID, "cannot translate node #%d: the value needs %s", valueID, expected)
			}
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", expected, g.load(typ, ptr), data)
		} else {
			value, err := g.value(valueID)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", expected, value, data)
		}
	} else {
		placed, typ, err := g.place(valueID)
		if err != nil {
			return "", err
		}
		if typ != expected {
			return "", g.errf(valueID, "cannot translate node #%d: the value needs %s", valueID, expected)
		}
		data = placed
	}
	table, err := g.vtable(iface, concrete)
	if err != nil {
		return "", err
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataField, iface.llvm, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	tableField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", tableField, iface.llvm, slot)
	fmt.Fprintf(g.out, "  store ptr @%s, ptr %s\n", llvmSymbol(table), tableField)
	return slot, nil
}

// ifaceConversion builds one argument's interface value in the slot reserved
// for it.
func (g *generator) ifaceConversion(r willc.Record, argIndex int, valueID int, iface *ifaceInfo, concrete string) (string, string, error) {
	key := fmt.Sprintf("%d.%d", r.ID, argIndex)
	slot := g.ifaceSlots[key]
	if g.deferThunk {
		slot = g.newTmp()
		fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, iface.llvm)
	} else if slot == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the interface value has no slot", r.ID)
	}
	if _, err := g.ifaceValue(slot, valueID, iface, concrete); err != nil {
		return "", "", err
	}
	return slot, iface.llvm, nil
}

// vtable returns the symbol of the method table for one concrete type and one
// interface, and records its definition for the end of the module. The last
// entry is the type identity of the concrete type; a downcast compares it
// instead of the table pointer, which differs when two packages build a table
// for the same type.
func (g *generator) vtable(iface *ifaceInfo, concrete string) (string, error) {
	key := iface.llvm + "|" + concrete
	if sym, ok := g.vtables[key]; ok {
		return sym, nil
	}
	sym := qualify(g.pkg, "vtable."+iface.name+"."+concrete)
	entries := make([]string, 0, len(iface.methods)+1)
	for _, m := range iface.methods {
		method := g.methodsQualified[concrete+"."+m.name]
		if method == "" {
			method = g.cur.methods[concrete+"."+m.name]
		}
		localThunk := false
		if method == "" && iface == g.showIface {
			// A predeclared scalar shows itself: the module defines the
			// method once and the table points at it.
			if thunk, ok := g.showThunkName(concrete); ok {
				method = thunk
				localThunk = true
			}
		}
		if method == "" {
			return "", g.errf(iface.id, "cannot translate node #%d: %s has no method %s", iface.id, concrete, m.name)
		}
		entries = append(entries, "ptr @"+llvmSymbol(method))
		if g.packageMode && !localThunk && !g.emittedFuncs[method] {
			g.externalFuncs[method] = true
			g.externalSigs[method] = externalSignature{
				result: g.sigs[method],
				params: append([]string(nil), g.paramTypes[method]...),
				recv:   true,
			}
		}
	}
	entries = append(entries, "ptr @"+llvmSymbol(g.typeIdentity(concrete)))
	g.vtables[key] = sym
	if g.emittedVtables[sym] {
		return sym, nil
	}
	g.emittedVtables[sym] = true
	g.strings = append(g.strings, fmt.Sprintf("@%s = internal constant [%d x ptr] [%s], align 8\n",
		llvmSymbol(sym), len(entries), strings.Join(entries, ", ")))
	return sym, nil
}

// typeIdentityText is the will type text that names one concrete type in a
// module-wide way. A local name gains its package path, while a predeclared
// scalar keeps the spelling that tells usize and u64 apart even though both
// lower to i64.
func (g *generator) typeIdentityText(concrete string) string {
	text := g.canonicalTypeText(concrete)
	if !strings.Contains(text, ".") {
		if q := qualify(g.pkg, text); g.declKinds[q] != "" {
			return q
		}
		var found string
		for name := range g.declKinds {
			if !strings.HasSuffix(name, "."+text) {
				continue
			}
			if found != "" && found != name {
				return text
			}
			found = name
		}
		if found != "" {
			return found
		}
	}
	return g.qualifyTypeText(g.pkg, text)
}

// typeIdentity returns the symbol of the type identity word. Every module that
// mentions the type defines the same linkonce symbol, so addresses compare
// equal after linking without a runtime registry.
func (g *generator) typeIdentity(concrete string) string {
	text := g.typeIdentityText(concrete)
	if sym := g.typeIDs[text]; sym != "" {
		return sym
	}
	sym := "will.type." + text
	g.typeIDs[text] = sym
	if !g.emittedTypeIDs[sym] {
		g.emittedTypeIDs[sym] = true
		g.strings = append(g.strings, fmt.Sprintf("@%s = linkonce_odr constant i8 0, align 1\n", llvmSymbol(sym)))
	}
	return sym
}

// interfaceCall lowers a method call whose receiver is an interface: load the
// data pointer and the method pointer, then call it with the data pointer as
// the receiver.
func (g *generator) interfaceCall(r willc.Record, iface *ifaceInfo) (string, string, error) {
	idx, ok := iface.byName[fieldText(r, "text")]
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no method %s", r.ID, iface.name, fieldText(r, "text"))
	}
	method := iface.methods[idx]
	base, typ, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if typ != iface.llvm {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver needs %s", r.ID, iface.llvm)
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataField, iface.llvm, base)
	data := g.load("ptr", dataField)
	tableField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", tableField, iface.llvm, base)
	table := g.load("ptr", tableField)
	fnField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n", fnField, len(iface.methods)+1, table, idx)
	fn := g.load("ptr", fnField)

	parts := []string{"ptr " + data}
	args := g.callArgs(r)
	if len(args) != len(method.params) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s takes %d arguments", r.ID, method.name, len(method.params))
	}
	for i, a := range args {
		parts, err = g.callArgParts(parts, r, i, a, method.params[i], false)
		if err != nil {
			return "", "", err
		}
	}
	result := method.result
	resultLLVM := ""
	if result != "" {
		resultLLVM = g.resultLLVM(result)
		if resultLLVM == "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: result type %s is not lowered yet", r.ID, result)
		}
	}
	if resultLLVM == "" {
		// A void call has no result, so it cannot carry a temporary name.
		fmt.Fprintf(g.out, "  call void %s(%s)\n", fn, strings.Join(parts, ", "))
		return "", "", nil
	}
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s %s(%s)\n", out, resultLLVM, fn, strings.Join(parts, ", "))
	return out, result, nil
}

// callArgParts appends one argument, converting a concrete value to an
// interface when the parameter type asks for one. The index is the argument's
// position, which names the slot reserved for the conversion.
func (g *generator) callArgParts(parts []string, r willc.Record, index int, argID int, paramType string, link bool) ([]string, error) {
	typ, err := g.typeOf(argID)
	if err != nil {
		return nil, err
	}
	argText, err := g.willTypeText(argID)
	if err != nil {
		return nil, err
	}
	if iface, ok := g.ifaceOfText(paramType); ok {
		if _, already := g.ifaceOfText(argText); !already {
			slot, _, err := g.ifaceConversion(r, index, argID, iface, argText)
			if err != nil {
				return nil, err
			}
			return append(parts, "ptr "+slot), nil
		}
	}
	if isAggregateType(typ) {
		if link {
			return nil, g.errf(argID, "cannot translate node #%d: a C link takes only scalar arguments yet", argID)
		}
		ptr, ptrType, err := g.place(argID)
		if err != nil {
			return nil, err
		}
		if ptrType != typ {
			return nil, g.errf(argID, "cannot translate node #%d: the argument needs %s", argID, typ)
		}
		return append(parts, "ptr "+ptr), nil
	}
	if typ == "" {
		return nil, g.errf(argID, "cannot translate node #%d: the argument has no scalar type", argID)
	}
	v, err := g.value(argID)
	if err != nil {
		return nil, err
	}
	return append(parts, typ+" "+v), nil
}

// callParamTypes returns the will parameter types of one call, in the order the
// arguments are passed. It is used to find the arguments that convert to an
// interface before the body is lowered.
func (g *generator) callParamTypes(r willc.Record) []string {
	if qualified := textField(r, "to"); qualified != "" {
		return g.paramTypes[qualified]
	}
	if r.Kind == "mcall" {
		if text, err := g.willTypeText(refField(r, "recv")); err == nil {
			iface, ok := g.ifaceOfText(text)
			if ok {
				if idx, ok := iface.byName[fieldText(r, "text")]; ok {
					return iface.methods[idx].params
				}
			}
		}
		if sym, err := g.methodSymbol(r); err == nil {
			return g.paramTypes[sym]
		}
		return nil
	}
	if qualified := textField(r, "to"); qualified != "" {
		return g.paramTypes[qualified]
	}
	if to := refField(r, "to"); to != 0 {
		if fn, ok := g.recs[to]; ok && fn.Kind == "func" {
			return g.paramTypes[g.cur.funcs[to]]
		}
	}
	return nil
}

// resultLLVM maps a result type text to its LLVM type, or "" when it is not
// lowered. It is the same order the function declaration uses.
func (g *generator) resultLLVM(text string) string {
	if typ, ok := llvmTypeText(text); ok {
		return typ
	}
	if text == "Error" {
		g.usedError = true
		return "%interface.Error"
	}
	if adt, ok := g.adtType(text); ok {
		return adt.llvm
	}
	if g.structTypes[text] {
		return qualifiedTypeName(text)
	}
	if g.enumTypes[text] {
		return qualifiedEnumName(text)
	}
	if info := g.ifaceByName[text]; info != nil {
		return info.llvm
	}
	if g.ifaceTypes[text] {
		return qualifiedIfaceName(text)
	}
	if typ, ok := g.typeLLVMOf(text); ok && isAggregateType(typ) {
		return typ
	}
	return ""
}
