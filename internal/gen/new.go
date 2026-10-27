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
	if elem, ok := rcElemText(text); ok && isRcText(text) {
		return g.constructRc(r, elem, strongName(text))
	}
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
		q := g.canonicalTypeText(text)
		if !strings.Contains(q, ".") {
			q = qualify(g.pkg, text)
		}
		if g.declKinds[q] == "struct" {
			return g.constructImportedNew(r, q)
		}
		return "", "", g.errf(r.ID, "cannot translate node #%d: new needs a struct declared in this document", r.ID)
	}
	storeSlot := g.newStoreSlots[r.ID]
	if storeSlot == "" {
		storeSlot = g.allocSlot(g.newSlots, "new", r.ID, info.llvm)
	}
	slot := g.newSlots[r.ID]
	if slot == "" {
		slot = storeSlot
	}
	fmt.Fprintf(g.out, "  store %s zeroinitializer, ptr %s\n", info.llvm, storeSlot)
	if info.foreign {
		// The defining document owns the fields and init. A generic instance
		// resolves its init in that document, including one with arguments.
		if sym, params, result, ok := g.initForNew(g.cur, r); ok {
			value, initText, err := g.callNewInit(r, storeSlot, sym, params, result)
			if err != nil {
				return "", "", err
			}
			if initText != "" {
				return g.constructInitResult(r, storeSlot, text, value, initText)
			}
			return slot, info.llvm, nil
		}
		if args := g.callArgs(r); len(args) != 0 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no init", r.ID, text)
		}
		return slot, info.llvm, nil
	}
	if err := g.fieldDefaults(info, storeSlot); err != nil {
		return "", "", err
	}
	initSym := g.cur.methods[text+".init"]
	if initSym == "" {
		initSym = g.methodsQualified[text+".init"]
	}
	params := g.paramTypes[initSym]
	initResult := g.sigs[initSym]
	if initSym == "" {
		// A generic init has no entry in the signature maps because its
		// instance document is created after the maps are collected.
		if sym, ps, res, ok := g.initForNew(g.cur, r); ok {
			initSym = sym
			params = ps
			initResult = res
		}
	}
	if initSym == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no init", r.ID, text)
	}
	value, initText, err := g.callNewInit(r, storeSlot, initSym, params, initResult)
	if err != nil {
		return "", "", err
	}
	if initText != "" {
		return g.constructInitResult(r, storeSlot, text, value, initText)
	}
	return slot, info.llvm, nil
}

// newInitParams returns the parameter types of the init a new expression calls.
// Interface arguments need this before the new expression itself is lowered, so
// their conversion slots are allocated in the function entry block.
func (g *generator) newInitParams(r willc.Record) []string {
	text := fieldText(r, "type")
	if g.byName[text] != nil {
		initSym := g.cur.methods[text+".init"]
		if initSym == "" {
			initSym = g.methodsQualified[text+".init"]
		}
		if initSym != "" {
			return g.paramTypes[initSym]
		}
	}
	q := g.canonicalTypeText(text)
	if !strings.Contains(q, ".") {
		q = qualify(g.pkg, text)
	}
	if g.declKinds[q] == "struct" {
		initSym := g.methodsQualified[q+".init"]
		if initSym != "" {
			return g.paramTypes[initSym]
		}
	}
	if _, params, _, ok := g.initForNew(g.cur, r); ok {
		return params
	}
	return nil
}

// callNewInit passes the storage and the evaluated constructor arguments to one
// init method. A non-empty result text means the call returns a Result value
// that the caller must turn into the new expression's value.
func (g *generator) callNewInit(r willc.Record, slot, initSym string, params []string, initResult string) (string, string, error) {
	g.noteExternalFunc(initSym)
	muts := g.paramMut[initSym]
	parts := []string{"ptr " + slot}
	for i, a := range g.callArgs(r) {
		paramType := ""
		if i < len(params) {
			paramType = params[i]
		}
		mut := i < len(muts) && muts[i]
		var err error
		parts, err = g.callArgParts(parts, r, i, a, paramType, mut, false)
		if err != nil {
			return "", "", err
		}
	}
	if g.packageMode && !g.emittedFuncs[initSym] && !strings.HasPrefix(initSym, g.pkg+".") {
		argTypes := make([]string, 0, len(parts))
		for _, part := range parts {
			if i := strings.LastIndexByte(part, ' '); i > 0 {
				argTypes = append(argTypes, part[:i])
			}
		}
		g.externalFuncs[initSym] = true
		g.externalSigs[initSym] = externalSignature{
			result:   initResult,
			params:   append([]string(nil), params...),
			argTypes: argTypes,
		}
	}
	if initResult == "" || initResult == "()" {
		fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(initSym), strings.Join(parts, ", "))
		return "", "", nil
	}
	llvm, ok := g.typeLLVMOf(initResult)
	if !ok || llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: init result %s is not lowered yet", r.ID, initResult)
	}
	value := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s @%s(%s)\n", value, llvm, llvmSymbol(initSym), strings.Join(parts, ", "))
	return value, initResult, nil
}

// newResultText is the type of the value a new expression produces. A generic
// init returning Result<(),E> makes new return Result<T,E>.
func (g *generator) newResultText(r willc.Record) string {
	text := fieldText(r, "type")
	_, _, result, ok := g.initForNew(g.cur, r)
	if !ok || result == "" {
		return text
	}
	kind, args, ok := parseADT(result)
	if !ok || kind != "result" || len(args) != 2 || args[0] != "()" {
		return text
	}
	return "Result<" + text + "," + args[1] + ">"
}

// constructInitResult turns the Result<(),E> an init returns into Result<T,E>
// and releases the half-built object on the error path.
func (g *generator) constructInitResult(r willc.Record, storeSlot, baseText, value, initText string) (string, string, error) {
	resultText := g.newResultText(r)
	resultADT, ok := g.adtType(resultText)
	if !ok || resultADT.llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, resultText)
	}
	initADT, ok := g.adtType(initText)
	if !ok || initADT.llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, initText)
	}
	slot := g.newSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.newSlots, "new", r.ID, resultADT.llvm)
	}
	initSlot := g.callSlots[r.ID]
	if initSlot == "" {
		initSlot = g.allocSlot(g.callSlots, "newinit", r.ID, initADT.llvm)
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", initADT.llvm, value, initSlot)
	tag := g.load("i32", initSlot)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, resultADT.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", tag, tagPtr)
	g.labels++
	n := g.labels
	okLabel := fmt.Sprintf("new.ok.%d", n)
	errLabel := fmt.Sprintf("new.err.%d", n)
	done := fmt.Sprintf("new.done.%d", n)
	okCond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", okCond, tag)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", okCond, okLabel, errLabel)
	g.out.WriteString(okLabel + ":\n")
	baseLLVM, ok := g.typeLLVMOf(baseText)
	if !ok || baseLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, baseText)
	}
	okPayload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", okPayload, resultADT.llvm, slot, resultADT.payloadIndex)
	g.copyValue(storeSlot, okPayload, baseLLVM)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(errLabel + ":\n")
	if err := g.releaseOwnedValue(storeSlot, baseText); err != nil {
		return "", "", err
	}
	errText := resultADT.args[1]
	errLLVM, ok := g.typeLLVMOf(errText)
	if !ok || errLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, errText)
	}
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", from, initADT.llvm, initSlot, initADT.payloadIndex)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", to, resultADT.llvm, slot, resultADT.payloadIndex)
	g.copyValue(from, to, errLLVM)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return slot, resultADT.llvm, nil
}

// constructImportedNew builds a value whose struct is declared in another
// document. The layout is the named type the other document defines, and init
// is the method symbol the module index carries. The field defaults of a
// foreign struct are not visible here; init is expected to set the fields it
// owns.
func (g *generator) constructImportedNew(r willc.Record, q string) (string, string, error) {
	typ := qualifiedTypeName(q)
	slot := g.allocSlot(g.newSlots, "new", r.ID, typ)
	fmt.Fprintf(g.out, "  store %s zeroinitializer, ptr %s\n", typ, slot)
	initSym := g.methodsQualified[q+".init"]
	if initSym == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no init", r.ID, q)
	}
	g.noteExternalFunc(initSym)
	if g.sigs[initSym] != "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: an init that returns a value is not lowered yet", r.ID)
	}
	args := g.callArgs(r)
	params := g.paramTypes[initSym]
	muts := g.paramMut[initSym]
	if len(args) != len(params) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s.init takes %d arguments", r.ID, q, len(params))
	}
	parts := []string{"ptr " + slot}
	for i, a := range args {
		mut := i < len(muts) && muts[i]
		var err error
		parts, err = g.callArgParts(parts, r, i, a, params[i], mut, false)
		if err != nil {
			return "", "", err
		}
	}
	if g.packageMode && !g.emittedFuncs[initSym] && !strings.HasPrefix(initSym, g.pkg+".") {
		argTypes := make([]string, 0, len(parts))
		for _, part := range parts {
			if i := strings.LastIndexByte(part, ' '); i > 0 {
				argTypes = append(argTypes, part[:i])
			}
		}
		g.externalFuncs[initSym] = true
		g.externalSigs[initSym] = externalSignature{argTypes: argTypes}
	}
	fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(initSym), strings.Join(parts, ", "))
	return slot, typ, nil
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
