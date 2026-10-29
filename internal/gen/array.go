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
	case "len":
		return "i64", "usize", true
	case "get":
		elemLLVM, ok := g.typeLLVMOf(elem)
		if !ok || elemLLVM == "" {
			return "", "", false
		}
		return elemLLVM, elem, true
	case "equal", "has_prefix", "has_suffix":
		if elem == "u8" {
			return "i1", "bool", true
		}
	case "compare":
		if elem == "u8" {
			return "i32", "i32", true
		}
	case "index":
		if elem == "u8" {
			if info, ok := g.adtType("Option<usize>"); ok {
				return info.llvm, "Option<usize>", true
			}
		}
	case "clone":
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
	if name == "len" {
		return g.load("i64", lenField), "usize", nil
	}
	if elem == "u8" {
		switch name {
		case "equal", "compare", "has_prefix", "has_suffix", "clone":
			return g.byteViewMethod(r, base, lenField, name)
		}
	}
	args := g.callArgs(r)
	if len(args) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: get needs an index", r.ID)
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
		return "", "", g.errf(r.ID, "cannot translate node #%d: get needs a view", r.ID)
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
		return "", "", g.errf(r.ID, "cannot translate node #%d: get takes one index", r.ID)
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
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
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
			g.gcAllocSequence(ptr, fmt.Sprintf("%d", lit*size), descriptor)
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
		g.gcAllocSequence(allocated, bytes, descriptor)
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
		ptr = g.newTmp()
		fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n", ptr, zero, allocated, alloc)
	}
	_ = align
	dataPin := g.pinContainerData(ptr)
	ptr = dataPin
	defer g.unpinContainer(dataPin)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ptr, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenField)
	capField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", capValue, capField)
	// The new handle is not a root by itself. Publish its data word into the
	// frame root before the construction pin is released.
	g.gcInlineReady[r.ID] = true
	g.refreshInlineRoot(r.ID)
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
	ownerPin, owner, err := g.pinContainerReceiver(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if ownerPin != "" {
		defer g.unpinContainer(ownerPin)
	}
	var base, typ string
	if owner != "" {
		base, typ, _, err = g.classFieldAddressFrom(
			refField(r, "recv"), owner, r.ID)
	} else {
		base, typ, err = g.place(refField(r, "recv"))
	}
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, i64, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not an Array", r.ID)
	}
	// Every Array method reads or writes element storage through a raw
	// pointer. Pin the backing sequence for the whole call, before an argument
	// or value expression can reach an allocation.
	arrayDataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n",
		arrayDataPtr, base)
	arrayData := g.loadContainerData(arrayDataPtr)
	arrayDataPin := g.pinContainerData(arrayData)
	defer g.unpinContainer(arrayDataPin)
	g.publishContainerData(arrayDataPtr, arrayDataPin)
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
	case "len":
		return g.load("i64", field(1)), "usize", nil
	case "cap":
		return g.load("i64", field(2)), "usize", nil
	case "get", "set":
		args := g.callArgs(r)
		if len(args) == 0 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s needs an index", r.ID, fieldText(r, "text"))
		}
		ptr, err := g.arrayElemAddr(r, base, elemLLVM, args[0])
		if err != nil {
			return "", "", err
		}
		if fieldText(r, "text") == "get" {
			return g.load(elemLLVM, ptr), elem, nil
		}
		if len(args) < 2 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: set needs a value", r.ID)
		}
		dataPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
		data := g.loadContainerData(dataPtr)
		pinned := g.pinContainerData(data)
		defer g.unpinContainer(pinned)
		if isAggregateType(elemLLVM) {
			if iface, ok := g.ifaceOfText(elem); ok {
				valueText, err := g.willTypeText(args[1])
				if err != nil {
					return "", "", err
				}
				tmp := g.newTmp()
				g.tempAlloca(tmp, iface.llvm)
				if _, err := g.ifaceStoreValue(tmp, args[1], iface, valueText); err != nil {
					return "", "", err
				}
				if err := g.releaseIfaceValue(ptr, iface); err != nil {
					return "", "", err
				}
				g.gcStoreContainerAggregate(ptr, tmp, iface.llvm, elem)
				return "", "", nil
			}
			src, srcType, err := g.place(args[1])
			if err != nil {
				return "", "", err
			}
			if srcType != elemLLVM {
				return "", "", g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
			}
			move, _ := g.moveSource(args[1])
			if g.needsReleaseText(elem) {
				if err := g.releaseSlot(ptr, elem); err != nil {
					return "", "", err
				}
				if !move && g.isCopySource(args[1]) {
					if err := g.retainValue(src, elem); err != nil {
						return "", "", err
					}
				}
			}
			g.gcStoreContainerAggregate(ptr, src, elemLLVM, elem)
			if move {
				if err := g.zeroMovedSource(args[1], ptr); err != nil {
					return "", "", err
				}
			}
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
		if isChanText(elem) {
			old := g.load("ptr", ptr)
			g.needTaskDecl = true
			g.needChanDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_chan_release(ptr %s)\n", old)
			if g.isCopySource(args[1]) {
				fmt.Fprintf(g.out, "  call void @willrt_chan_retain(ptr %s)\n", v)
			}
		}
		g.gcStoreContainerScalar(ptr, v, elemLLVM, elem)
		return "", "", nil
	case "push":
		args := g.callArgs(r)
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: push takes one value", r.ID)
		}
		elemSize, _, ok := g.sizeAlign(elemLLVM)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
		}
		if err := g.arrayPush(r, base, elem, elemLLVM, elemSize, args[0]); err != nil {
			return "", "", err
		}
		return "", "", nil
	case "append":
		args := g.callArgs(r)
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: append takes one view", r.ID)
		}
		elemSize, _, ok := g.sizeAlign(elemLLVM)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
		}
		if err := g.arrayAppend(r, base, elem, elemLLVM, elemSize, args[0]); err != nil {
			return "", "", err
		}
		return "", "", nil
	case "pop":
		return g.arrayPop(r, base, elem, elemLLVM)
	case "insert":
		return g.arrayInsert(r, base, elem, elemLLVM)
	case "remove":
		return g.arrayRemove(r, base, elem, elemLLVM)
	case "slice":
		return g.arraySlice(r, base, elem, elemLLVM)
	case "shrink_to_fit":
		return g.arrayShrink(r, base, elem, elemLLVM)
	case "clone":
		return g.arrayClone(r, base, elem, elemLLVM)
	case "clear":
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
	case "len", "cap":
		return "i64", "usize", true
	case "get", "remove":
		return elemLLVM, elem, true
	case "pop":
		if info, ok := g.adtType("Option<" + elem + ">"); ok {
			return info.llvm, info.text, true
		}
		return "", "", false
	case "push", "append", "set", "insert", "clear", "shrink_to_fit":
		return "", "", true
	case "slice":
		return "{ ptr, i64 }", "[]" + elem, true
	case "clone":
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
	data := g.loadContainerData(dataPtr)
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
func (g *generator) arrayGrow(r willc.Record, base, elem, elemLLVM string, elemSize int) (dataPtr, lenPtr, length, slot, newPin string, err error) {
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return "", "", "", "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
	}
	dataPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.loadContainerData(dataPtr)
	lenPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length = g.load("i64", lenPtr)
	capPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capPtr, base)
	capacity := g.load("i64", capPtr)

	g.labels++
	n := g.labels
	start := fmt.Sprintf("arr.start.%d", n)
	full := fmt.Sprintf("arr.full.%d", n)
	ready := fmt.Sprintf("arr.ready.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", start)
	g.out.WriteString(start + ":\n")
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
	g.gcAllocSequence(newData, bytes, descriptor)
	// The old sequence is pinned by the enclosing Array method. Pin the new
	// sequence before the copy so a concurrent cycle cannot move it while the
	// destination is still being filled.
	newPinned := g.pinContainerData(newData)
	newData = newPinned

	// Copy the live elements with a small loop.
	copyCond := fmt.Sprintf("arr.copy.cond.%d", n)
	copyBody := fmt.Sprintf("arr.copy.body.%d", n)
	copyEnd := fmt.Sprintf("arr.copy.end.%d", n)
	copySlot := g.newTmp()
	g.tempAlloca(copySlot, "i64")
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
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataPtr, newData)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(ready + ":\n")

	newPin = g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n",
		newPin, start, newPinned, copyEnd)
	data = g.loadContainerData(dataPtr)
	length = g.load("i64", lenPtr)
	slot = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", slot, elemLLVM, data, length)
	return dataPtr, lenPtr, length, slot, newPin, nil
}

// arrayPush stores one value at the length and grows the block when it is full.
func (g *generator) arrayPush(r willc.Record, base, elem, elemLLVM string, elemSize int, valueID int) error {
	_, lenPtr, length, slot, newPin, err := g.arrayGrow(r, base, elem, elemLLVM, elemSize)
	if err != nil {
		return err
	}
	if newPin != "" {
		defer g.unpinContainer(newPin)
	}
	if isAggregateType(elemLLVM) {
		converted := false
		if iface, ok := g.ifaceOfText(elem); ok {
			valueText, err := g.willTypeText(valueID)
			if err != nil {
				return err
			}
			if _, err := g.ifaceStoreValue(slot, valueID, iface, valueText); err != nil {
				return err
			}
			converted = true
		}
		if !converted {
			src, srcType, err := g.place(valueID)
			if err != nil {
				return err
			}
			if srcType != elemLLVM {
				return g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
			}
			move, _ := g.moveSource(valueID)
			if !move && g.needsReleaseText(elem) && g.isCopySource(valueID) {
				if err := g.retainValue(src, elem); err != nil {
					return err
				}
			}
			g.gcStoreContainerAggregate(slot, src, elemLLVM, elem)
			if move {
				if err := g.zeroMovedSource(valueID, slot); err != nil {
					return err
				}
			}
		}
	} else {
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		if isRcText(elem) && g.isCopySource(valueID) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		if isChanText(elem) && g.isCopySource(valueID) {
			g.needTaskDecl = true
			g.needChanDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_chan_retain(ptr %s)\n", value)
		}
		g.gcStoreContainerScalar(slot, value, elemLLVM, elem)
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
