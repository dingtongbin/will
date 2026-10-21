package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// arrayElemText returns the element type of an Array<T>. A view []T has its
// own two-word lowering and is handled by viewElemText.
func arrayElemText(text string) (string, bool) {
	if !strings.HasPrefix(text, "Array<") || !strings.HasSuffix(text, ">") {
		return "", false
	}
	elem := strings.TrimSpace(text[len("Array<") : len(text)-1])
	if elem == "" {
		return "", false
	}
	return elem, true
}

// viewElemText returns the element text of a read-only view []T.
func viewElemText(text string) (string, bool) {
	if !strings.HasPrefix(text, "[]") {
		return "", false
	}
	elem := text[2:]
	if elem == "" {
		return "", false
	}
	return elem, true
}

// viewCallResult returns the LLVM and will result types of a view method.
func (g *generator) viewCallResult(r willc.Record) (llvm, text string, known bool) {
	if r.Kind != "mcall" {
		return "", "", false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", "", false
	}
	elem, ok := viewElemText(stripPredeclaredPackage(recvText))
	if !ok {
		return "", "", false
	}
	switch fieldText(r, "text") {
	case "Len":
		return "i64", "usize", true
	case "Get":
		elemLLVM, ok := g.typeLLVMOf(elem)
		if !ok || elemLLVM == "" {
			return "", "", false
		}
		return elemLLVM, elem, true
	case "Equal", "HasPrefix", "HasSuffix":
		if elem == "u8" {
			return "i1", "bool", true
		}
	case "Compare":
		if elem == "u8" {
			return "i32", "i32", true
		}
	case "Index":
		if elem == "u8" {
			if info, ok := g.adtType("Option<usize>"); ok {
				return info.llvm, "Option<usize>", true
			}
		}
	case "Clone":
		if elem == "u8" {
			if typ, ok := llvmTypeText("Array<u8>"); ok {
				return typ, "Array<u8>", true
			}
		}
	}
	return "", "", false
}

// viewMethod lowers a method call on a read-only view []T.
func (g *generator) viewMethod(r willc.Record, elem string) (string, string, error) {
	base, baseType, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not a view", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: view element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	name := fieldText(r, "text")
	if name == "Len" {
		return g.load("i64", lenField), "usize", nil
	}
	if elem == "u8" {
		switch name {
		case "Equal", "Compare", "HasPrefix", "HasSuffix", "Clone":
			return g.byteViewMethod(r, base, lenField, name)
		}
	}
	args := g.callArgs(r)
	if len(args) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Get needs an index", r.ID)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	length := g.load("i64", lenField)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemLLVM, data, idx)
	return g.load(elemLLVM, ptr), elem, nil
}

// viewGetPlace returns the address of one element of a view whose element is an
// aggregate, so a field or a str method can read it in place.
func (g *generator) viewGetPlace(r willc.Record, elem string) (string, string, error) {
	base, baseType, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Get needs a view", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: view element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	length := g.load("i64", lenField)
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Get takes one index", r.ID)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemLLVM, data, idx)
	return ptr, elemLLVM, nil
}

// constructArray builds `new Array<T>(cap)`: one heap block of cap elements and
// the three-word value (data, length, capacity). The length starts at zero.
func (g *generator) constructArray(r willc.Record, elem string) (string, string, error) {
	slot := g.allocSlot(g.newSlots, "new", r.ID, "{ ptr, i64, i64 }")
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s is not lowered yet", r.ID, elem)
	}
	size, align, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
	}
	capValue := "0"
	args := g.callArgs(r)
	if len(args) != 0 {
		v, err := g.indexValue(args[0], r)
		if err != nil {
			return "", "", err
		}
		capValue = v
	}
	ptr := "null"
	if lit := intLitValue(args, g); lit >= 0 {
		if lit == 0 {
			ptr = "null"
		} else {
			ptr = g.newTmp()
			fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %d)\n", ptr, lit*size)
			g.needAllocDecl = true
		}
	} else {
		// The size is only known at run time; a zero capacity still allocates
		// nothing, so the call is guarded by the capacity itself.
		g.labels++
		n := g.labels
		zero := fmt.Sprintf("arr.zero.%d", n)
		alloc := fmt.Sprintf("arr.alloc.%d", n)
		done := fmt.Sprintf("arr.ptr.%d", n)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", cond, capValue)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, zero, alloc)
		g.out.WriteString(zero + ":\n")
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(alloc + ":\n")
		bytes := g.newTmp()
		fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, capValue, size)
		allocated := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", allocated, bytes)
		g.needAllocDecl = true
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
		ptr = g.newTmp()
		fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n", ptr, zero, allocated, alloc)
	}
	_ = align
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ptr, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenField)
	capField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", capValue, capField)
	return slot, "{ ptr, i64, i64 }", nil
}

// intLitValue returns the value of the first argument when it is an integer
// literal, or -1 otherwise.
func intLitValue(args []int, g *generator) int {
	if len(args) == 0 {
		return -1
	}
	r, ok := g.recs[args[0]]
	if !ok || r.Kind != "lit" || fieldText(r, "kind") != "int" {
		return -1
	}
	text := literalIntText(fieldText(r, "text"))
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return -1
		}
		n = n*10 + int(text[i]-'0')
		if n > 1<<30 {
			return -1
		}
	}
	return n
}

// arrayMethod lowers a method call on Array<T>.
func (g *generator) arrayMethod(r willc.Record, elem string) (string, string, error) {
	base, typ, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, i64, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not an Array", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s is not lowered yet", r.ID, elem)
	}
	field := func(i int) string {
		p := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 %d\n", p, base, i)
		return p
	}
	switch fieldText(r, "text") {
	case "Len":
		return g.load("i64", field(1)), "usize", nil
	case "Cap":
		return g.load("i64", field(2)), "usize", nil
	case "Get", "Set":
		args := g.callArgs(r)
		if len(args) == 0 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s needs an index", r.ID, fieldText(r, "text"))
		}
		ptr, err := g.arrayElemAddr(r, base, elemLLVM, args[0])
		if err != nil {
			return "", "", err
		}
		if fieldText(r, "text") == "Get" {
			return g.load(elemLLVM, ptr), elem, nil
		}
		if len(args) < 2 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Set needs a value", r.ID)
		}
		if isAggregateType(elemLLVM) {
			src, srcType, err := g.place(args[1])
			if err != nil {
				return "", "", err
			}
			if srcType != elemLLVM {
				return "", "", g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
			}
			if g.needsReleaseText(elem) {
				if err := g.releaseSlot(ptr, elem); err != nil {
					return "", "", err
				}
				if g.isCopySource(args[1]) {
					if err := g.retainValue(src, elem); err != nil {
						return "", "", err
					}
				}
			}
			g.copyValue(src, ptr, elemLLVM)
			return "", "", nil
		}
		v, err := g.value(args[1])
		if err != nil {
			return "", "", err
		}
		if isRcText(elem) {
			old := g.load("i64", ptr)
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", old)
			if g.isCopySource(args[1]) {
				fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", v)
			}
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, v, ptr)
		return "", "", nil
	case "Push":
		args := g.callArgs(r)
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Push takes one value", r.ID)
		}
		elemSize, _, ok := g.sizeAlign(elemLLVM)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
		}
		if err := g.arrayPush(r, base, elem, elemLLVM, elemSize, args[0]); err != nil {
			return "", "", err
		}
		return "", "", nil
	case "Append":
		args := g.callArgs(r)
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Append takes one view", r.ID)
		}
		elemSize, _, ok := g.sizeAlign(elemLLVM)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
		}
		if err := g.arrayAppend(r, base, elem, elemLLVM, elemSize, args[0]); err != nil {
			return "", "", err
		}
		return "", "", nil
	case "Pop":
		return g.arrayPop(r, base, elem, elemLLVM)
	case "Insert":
		return g.arrayInsert(r, base, elem, elemLLVM)
	case "Remove":
		return g.arrayRemove(r, base, elem, elemLLVM)
	case "Slice":
		return g.arraySlice(r, base, elem, elemLLVM)
	case "ShrinkToFit":
		return g.arrayShrink(r, base, elemLLVM)
	case "Clone":
		return g.arrayClone(r, base, elem, elemLLVM)
	case "Clear":
		if g.needsReleaseText(elem) {
			if err := g.eachElement(base, elem, false, 0); err != nil {
				return "", "", err
			}
		}
		fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", field(1))
		return "", "", nil
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: Array.%s is not lowered yet", r.ID, fieldText(r, "text"))
}

// arrayCallResult returns the LLVM and will result types of an Array method.
// known is true for a method the lowering handles; llvm is empty for a method
// that returns no value.
func (g *generator) arrayCallResult(r willc.Record) (llvm, text string, known bool) {
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", "", false
	}
	elem, ok := arrayElemText(stripPredeclaredPackage(recvText))
	if !ok {
		return "", "", false
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", false
	}
	switch fieldText(r, "text") {
	case "Len", "Cap":
		return "i64", "usize", true
	case "Get", "Remove":
		return elemLLVM, elem, true
	case "Pop":
		if info, ok := g.adtType("Option<" + elem + ">"); ok {
			return info.llvm, info.text, true
		}
		return "", "", false
	case "Push", "Append", "Set", "Insert", "Clear", "ShrinkToFit":
		return "", "", true
	case "Slice":
		return "{ ptr, i64 }", "[]" + elem, true
	case "Clone":
		if llvm, ok := llvmTypeText("Array<" + elem + ">"); ok {
			return llvm, "Array<" + elem + ">", true
		}
		return "", "", false
	}
	return "", "", false
}

// arrayElemAddr checks one index and returns the address of that element.
func (g *generator) arrayElemAddr(r willc.Record, base, elemLLVM string, indexID int) (string, error) {
	idx, err := g.indexValue(indexID, r)
	if err != nil {
		return "", err
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemLLVM, data, idx)
	return ptr, nil
}

// arrayGrow makes room for one more element and returns the new element's
// address together with the header fields. The length is not changed here.
func (g *generator) arrayGrow(r willc.Record, base, elemLLVM string, elemSize int) (dataPtr, lenPtr, length, slot string, err error) {
	dataPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length = g.load("i64", lenPtr)
	capPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capPtr, base)
	capacity := g.load("i64", capPtr)

	g.labels++
	n := g.labels
	full := fmt.Sprintf("arr.full.%d", n)
	ready := fmt.Sprintf("arr.ready.%d", n)
	cond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", cond, length, capacity)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, full, ready)
	g.out.WriteString(full + ":\n")

	// Grow to twice the capacity, or four elements when the array is empty.
	doubled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, 2\n", doubled, capacity)
	isZero := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", isZero, capacity)
	newCap := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 4, i64 %s\n", newCap, isZero, doubled)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, newCap, elemSize)
	newData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", newData, bytes)
	g.needAllocDecl = true

	// Copy the live elements with a small loop.
	copyCond := fmt.Sprintf("arr.copy.cond.%d", n)
	copyBody := fmt.Sprintf("arr.copy.body.%d", n)
	copyEnd := fmt.Sprintf("arr.copy.end.%d", n)
	copySlot := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", copySlot)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", copySlot)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyCond + ":\n")
	at := g.load("i64", copySlot)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, copyBody, copyEnd)
	g.out.WriteString(copyBody + ":\n")
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, at)
	v := g.load(elemLLVM, src)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, elemLLVM, newData, at)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, v, dst)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, copySlot)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyEnd + ":\n")
	isNull := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", isNull, data)
	noFree := fmt.Sprintf("arr.nofree.%d", n)
	doFree := fmt.Sprintf("arr.free.%d", n)
	freed := fmt.Sprintf("arr.freed.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isNull, noFree, doFree)
	g.out.WriteString(doFree + ":\n")
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", data)
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  br label %%%s\n", freed)
	g.out.WriteString(noFree + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", freed)
	g.out.WriteString(freed + ":\n")
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", newData, dataPtr)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(ready + ":\n")

	data = g.load("ptr", dataPtr)
	length = g.load("i64", lenPtr)
	slot = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", slot, elemLLVM, data, length)
	return dataPtr, lenPtr, length, slot, nil
}

// arrayPush stores one value at the length and grows the block when it is full.
func (g *generator) arrayPush(r willc.Record, base, elem, elemLLVM string, elemSize int, valueID int) error {
	_, lenPtr, length, slot, err := g.arrayGrow(r, base, elemLLVM, elemSize)
	if err != nil {
		return err
	}
	if isAggregateType(elemLLVM) {
		src, srcType, err := g.place(valueID)
		if err != nil {
			return err
		}
		if srcType != elemLLVM {
			return g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		if g.needsReleaseText(elem) && g.isCopySource(valueID) {
			if err := g.retainValue(src, elem); err != nil {
				return err
			}
		}
		g.copyValue(src, slot, elemLLVM)
	} else {
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		if isRcText(elem) && g.isCopySource(valueID) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, slot)
	}
	nextLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextLen, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextLen, lenPtr)
	return nil
}

// arrayAppend copies every element of a view into the array. The elements are
// first copied to an owned temporary block; that keeps the source valid when
// it aliases the destination and gives each borrowed rc element one reference
// before it enters the array.
