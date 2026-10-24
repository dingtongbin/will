package gen

import (
	"fmt"
	"strings"
)

// shareValue marks every rc handle reachable in a value as cross-thread. A
// value only needs this at a task frame, a task result, or a channel send;
// ordinary local copies stay on the plain count path. Every promotion lands
// in the crossing list, so the decision record carries one obligation per
// willrt_rc_share the module emits.
func (g *generator) shareValue(ptr, text string) error {
	return g.shareValueDepth(ptr, text, 0)
}

// recordCrossing notes one promotion. The crossing site names the position
// when it knows it; a promotion inside a machine epilogue falls back to the
// position of the function being lowered.
func (g *generator) recordCrossing() {
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
	text = g.qualifyTypeText(g.pkg, text)
	if isWeakText(text) {
		return nil
	}
	if isRcText(text) {
		g.needRcDecl = true
		handle := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_share(i64 %s)\n", handle)
		g.recordCrossing()
		return nil
	}
	if strings.HasPrefix(text, "Array<") && strings.HasSuffix(text, ">") {
		elem := strings.TrimSpace(text[len("Array<") : len(text)-1])
		if !needsReleaseText(g, elem) {
			return nil
		}
		return g.eachElementShare(ptr, elem, depth)
	}
	if strings.HasPrefix(text, "[]") && len(text) > 2 {
		elem := text[2:]
		if !needsReleaseText(g, elem) {
			return nil
		}
		return g.eachViewShare(ptr, elem, depth)
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		info, ok := g.adtType(text)
		if !ok {
			return nil
		}
		tag := g.load("i32", ptr)
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("share.keep.%d", n)
		done := fmt.Sprintf("share.done.%d", n)
		cond := g.newTmp()
		if kind == "option" {
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 1\n", cond, tag)
		} else {
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", cond, tag)
		}
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, ptr, info.payloadIndex)
		if err := g.shareValueDepth(payload, args[0], depth+1); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
		return nil
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
		if !needsReleaseText(g, ft) {
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
	fmt.Fprintf(g.out, "  %s = alloca i64\n", index)
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
	fmt.Fprintf(g.out, "  %s = alloca i64\n", index)
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
