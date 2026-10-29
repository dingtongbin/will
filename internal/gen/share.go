package gen

import "fmt"

// shareValue is kept as the one crossing hook. A strong handle is already
// atomic when it is an arc, and the checker rejects an rc at every crossing,
// so there is no promotion left to emit.
func (g *generator) shareValue(ptr, text string) error {
	return nil
}

// recordCrossing notes one promotion. The crossing site names the position
// when it knows it; a promotion inside a machine epilogue falls back to the
// position of the function being lowered.
func (g *generator) recordCrossing() {
	if g.quietShare {
		return
	}
	pos := g.crossingPos
	if pos == "" {
		pos = g.fnPos
	}
	if pos == "" {
		return
	}
	g.crossings = append(g.crossings, Crossing{Pos: pos})
}

func (g *generator) shareValueDepth(ptr, text string, depth int) error {
	if depth > 32 {
		return nil
	}
	if true {
		return nil
	}
	text = g.qualifyTypeText(g.pkg, text)
	if !g.needsShareText(text) {
		return nil
	}
	if isRcText(text) {
		g.needRcDecl = true
		handle := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_share(i64 %s)\n", handle)
		g.recordCrossing()
		return nil
	}
	if isFuncText(text) {
		envPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envPtr, ptr)
		env := g.load("ptr", envPtr)
		g.needClosureDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_closure_share(ptr %s)\n", env)
		g.recordCrossing()
		return nil
	}
	if elem, ok := arrayElemText(text); ok {
		return g.eachElementShare(ptr, elem, depth)
	}
	if elem, ok := viewElemText(text); ok {
		return g.eachViewShare(ptr, elem, depth)
	}
	if n, elem, ok := arrayTypeText(text); ok {
		return g.shareFixedArray(ptr, elem, n, depth)
	}
	if k, v, ok := mapTypeText(text); ok {
		return g.shareEntries(ptr, k, v, true, depth)
	}
	if elem, ok := setTypeText(text); ok {
		return g.shareEntries(ptr, elem, "", false, depth)
	}
	if info := g.enumInfoForText(text); info != nil {
		return g.shareEnumValue(ptr, info, depth)
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		return g.shareADTValue(ptr, kind, args, depth)
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return nil
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return nil
	}
	for i, ft := range info.texts {
		if !g.needsShareText(ft) {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, info.llvm, ptr, i)
		if err := g.shareValueDepth(field, ft, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// needsShareText reports whether a value can reach an rc handle that must be
// promoted before the value crosses a task or channel boundary. Closure
// environments and interface boxes are included because their generated share
// thunks walk the concrete captures and payload.
func (g *generator) needsShareText(text string) bool {
	text = g.qualifyTypeText(g.pkg, text)
	if text == "" || isWeakText(text) {
		return false
	}
	if isRcText(text) {
		return true
	}
	if text == "str" || isChanText(text) {
		return false
	}
	if isFuncText(text) {
		return true
	}
	if _, ok := g.ifaceOfText(text); ok {
		return true
	}
	if elem, ok := arrayElemText(text); ok {
		return g.needsShareText(elem)
	}
	if elem, ok := viewElemText(text); ok {
		return g.needsShareText(elem)
	}
	if n, elem, ok := arrayTypeText(text); ok {
		return n > 0 && g.needsShareText(elem)
	}
	if k, v, ok := mapTypeText(text); ok {
		return g.needsShareText(k) || g.needsShareText(v)
	}
	if elem, ok := setTypeText(text); ok {
		return g.needsShareText(elem)
	}
	if info := g.enumInfoForText(text); info != nil {
		for i := range info.variants {
			for _, arg := range info.variants[i].args {
				if g.needsShareText(arg) {
					return true
				}
			}
		}
		return false
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		for _, arg := range adtBranchTexts(kind, args) {
			if g.needsShareText(arg) {
				return true
			}
		}
		return false
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return false
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return false
	}
	for _, ft := range info.texts {
		if g.needsShareText(ft) {
			return true
		}
	}
	return false
}

// eachViewShare walks the live elements of a view and shares every rc handle
// it reaches.
func (g *generator) eachViewShare(ptr, elem string, depth int) error {
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	length := g.load("i64", lenPtr)
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return nil
	}
	g.recordCrossing()
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("share.view.cond.%d", n)
	body := fmt.Sprintf("share.view.body.%d", n)
	done := fmt.Sprintf("share.view.done.%d", n)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	at := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	element := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", element, elemLLVM, data, at)
	if err := g.shareValueDepth(element, elem, depth+1); err != nil {
		return err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return nil
}

// eachElementShare walks the live elements of an Array and shares every rc
// handle it reaches.
func (g *generator) eachElementShare(ptr, elem string, depth int) error {
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	length := g.load("i64", lenPtr)
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return nil
	}
	// One loop shares every element the buffer holds at run time, so the
	// static site carries a single obligation.
	g.recordCrossing()
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("share.elem.cond.%d", n)
	body := fmt.Sprintf("share.elem.body.%d", n)
	done := fmt.Sprintf("share.elem.done.%d", n)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	at := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	element := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", element, elemLLVM, data, at)
	if err := g.shareValueDepth(element, elem, depth+1); err != nil {
		return err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return nil
}
