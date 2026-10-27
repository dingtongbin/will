package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

func (g *generator) arrayAppend(r willc.Record, base, elem, elemLLVM string, elemSize int, viewID int) error {
	view, typ, err := g.place(viewID)
	if err != nil {
		return err
	}
	if typ != "{ ptr, i64 }" {
		return g.errf(r.ID, "cannot translate node #%d: append needs a view", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, view)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, view)
	viewLen := g.load("i64", lenPtr)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, viewLen, elemSize)
	copyData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", copyData, bytes)
	g.needAllocDecl = true

	g.labels++
	n := g.labels
	copyCond := fmt.Sprintf("append.copy.cond.%d", n)
	copyBody := fmt.Sprintf("append.copy.body.%d", n)
	copyDone := fmt.Sprintf("append.copy.done.%d", n)
	index := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", index)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyCond + ":\n")
	at := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, viewLen)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, copyBody, copyDone)
	g.out.WriteString(copyBody + ":\n")
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, at)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, elemLLVM, copyData, at)
	if isAggregateType(elemLLVM) {
		if g.needsReleaseText(elem) {
			if err := g.retainValue(src, elem); err != nil {
				return err
			}
		}
		g.copyValue(src, dst, elemLLVM)
	} else {
		value := g.load(elemLLVM, src)
		if isRcText(elem) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, dst)
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyDone + ":\n")

	appendCond := fmt.Sprintf("append.cond.%d", n)
	appendBody := fmt.Sprintf("append.body.%d", n)
	appendDone := fmt.Sprintf("append.done.%d", n)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", appendCond)
	g.out.WriteString(appendCond + ":\n")
	at = g.load("i64", index)
	more = g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, viewLen)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, appendBody, appendDone)
	g.out.WriteString(appendBody + ":\n")
	src = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, copyData, at)
	_, arrayLenPtr, arrayLen, slot, err := g.arrayGrow(r, base, elemLLVM, elemSize)
	if err != nil {
		return err
	}
	if isAggregateType(elemLLVM) {
		g.copyValue(src, slot, elemLLVM)
	} else {
		value := g.load(elemLLVM, src)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, slot)
	}
	nextLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextLen, arrayLen)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextLen, arrayLenPtr)
	next = g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", appendCond)
	g.out.WriteString(appendDone + ":\n")
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", copyData)
	return nil
}

// arrayPop removes the last element and returns it in an Option. The value is
// moved out, so no reference-count operation is needed.
func (g *generator) arrayPop(r willc.Record, base, elem, elemLLVM string) (string, string, error) {
	text := "Option<" + elem + ">"
	info, ok := g.adtType(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, text)
	}
	slot := g.callSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.callSlots, "pop", r.ID, info.llvm)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	g.labels++
	n := g.labels
	empty := fmt.Sprintf("pop.empty.%d", n)
	some := fmt.Sprintf("pop.some.%d", n)
	done := fmt.Sprintf("pop.done.%d", n)
	cond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", cond, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, empty, some)
	g.out.WriteString(empty + ":\n")
	tag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tag)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(some + ":\n")
	newLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", newLen, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newLen, lenPtr)
	data := g.load("ptr", dataPtr)
	elemPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", elemPtr, elemLLVM, data, newLen)
	tag = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tag)
	if info.payloadIndex != 0 {
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
		if isAggregateType(elemLLVM) {
			g.copyValue(elemPtr, payload, elemLLVM)
		} else {
			value := g.load(elemLLVM, elemPtr)
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, payload)
		}
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return g.load(info.llvm, slot), text, nil
}

// arrayInsert makes room at one index, shifts the tail right, and stores the
// value there. The value is snapshotted first so inserting an element of the
// same array cannot read moved storage.
func (g *generator) arrayInsert(r willc.Record, base, elem, elemLLVM string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 2 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: insert takes an index and a value", r.ID)
	}
	elemSize, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element %s has no layout", r.ID, elem)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)

	temp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca %s\n", temp, elemLLVM)
	if isAggregateType(elemLLVM) {
		src, srcType, err := g.place(args[1])
		if err != nil {
			return "", "", err
		}
		if srcType != elemLLVM {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		move, _ := g.moveSource(args[1])
		if !move && g.needsReleaseText(elem) && g.isCopySource(args[1]) {
			if err := g.retainValue(src, elem); err != nil {
				return "", "", err
			}
		}
		g.copyValue(src, temp, elemLLVM)
		if move {
			if err := g.zeroMovedSource(args[1], temp); err != nil {
				return "", "", err
			}
		}
	} else {
		value, err := g.value(args[1])
		if err != nil {
			return "", "", err
		}
		if isRcText(elem) && g.isCopySource(args[1]) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, temp)
	}

	dataPtr, arrayLenPtr, arrayLen, _, err := g.arrayGrow(r, base, elemLLVM, elemSize)
	if err != nil {
		return "", "", err
	}
	data := g.load("ptr", dataPtr)
	dest := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dest, elemLLVM, data, idx)

	g.labels++
	n := g.labels
	cond := fmt.Sprintf("insert.cond.%d", n)
	body := fmt.Sprintf("insert.body.%d", n)
	done := fmt.Sprintf("insert.done.%d", n)
	cursor := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", cursor)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", arrayLen, cursor)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	at := g.load("i64", cursor)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", more, at, idx)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	prev := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", prev, at)
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, prev)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, elemLLVM, data, at)
	if isAggregateType(elemLLVM) {
		g.copyValue(src, dst, elemLLVM)
	} else {
		value := g.load(elemLLVM, src)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, dst)
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, cursor)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	if isAggregateType(elemLLVM) {
		g.copyValue(temp, dest, elemLLVM)
	} else {
		value := g.load(elemLLVM, temp)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, dest)
	}
	nextLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextLen, arrayLen)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextLen, arrayLenPtr)
	return "", "", nil
}

// arraySlice builds the read-only view [lo, hi). Both bounds are checked
// against the live length.
func (g *generator) arraySlice(r willc.Record, base, elem, elemLLVM string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 2 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Slice takes two bounds", r.ID)
	}
	lo, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	hi, err := g.indexValue(args[1], r)
	if err != nil {
		return "", "", err
	}
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	badLo := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", badLo, lo, hi)
	g.panicCheck(badLo, panicIndex, r)
	badHi := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", badHi, hi, length)
	g.panicCheck(badHi, panicIndex, r)
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	start := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", start, elemLLVM, data, lo)
	span := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", span, hi, lo)
	slot := g.callSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.callSlots, "slice", r.ID, "{ ptr, i64 }")
	}
	startField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", startField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", start, startField)
	spanField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", spanField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", span, spanField)
	return g.load("{ ptr, i64 }", slot), "[]" + elem, nil
}

// arrayShrink reallocates the backing block to the live length.
func (g *generator) arrayShrink(r willc.Record, base, elemLLVM string) (string, string, error) {
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	capPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capPtr, base)
	capacity := g.load("i64", capPtr)
	same := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", same, length, capacity)
	g.labels++
	n := g.labels
	done := fmt.Sprintf("shrink.done.%d", n)
	alloc := fmt.Sprintf("shrink.alloc.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", same, done, alloc)
	g.out.WriteString(alloc + ":\n")
	elemSize, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element has no layout", r.ID)
	}
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, elemSize)
	newData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", newData, bytes)
	g.needAllocDecl = true
	copyCond := fmt.Sprintf("shrink.copy.cond.%d", n)
	copyBody := fmt.Sprintf("shrink.copy.body.%d", n)
	copyDone := fmt.Sprintf("shrink.copy.done.%d", n)
	index := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", index)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyCond + ":\n")
	at := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, copyBody, copyDone)
	g.out.WriteString(copyBody + ":\n")
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, at)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, elemLLVM, newData, at)
	if isAggregateType(elemLLVM) {
		g.copyValue(src, dst, elemLLVM)
	} else {
		value := g.load(elemLLVM, src)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, dst)
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", copyCond)
	g.out.WriteString(copyDone + ":\n")
	isNull := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", isNull, data)
	freeDone := fmt.Sprintf("shrink.freed.%d", n)
	free := fmt.Sprintf("shrink.free.%d", n)
	noFree := fmt.Sprintf("shrink.nofree.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isNull, noFree, free)
	g.out.WriteString(free + ":\n")
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", data)
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  br label %%%s\n", freeDone)
	g.out.WriteString(noFree + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", freeDone)
	g.out.WriteString(freeDone + ":\n")
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", newData, dataPtr)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, capPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return "", "", nil
}

// arrayClone copies the live elements into a block of exactly that size. An
// element that owns rc handles gets one reference of its own.
func (g *generator) arrayClone(r willc.Record, base, elem, elemLLVM string) (string, string, error) {
	elemSize, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: array element has no layout", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, elemSize)
	newData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", newData, bytes)
	g.needAllocDecl = true
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("clone.cond.%d", n)
	body := fmt.Sprintf("clone.body.%d", n)
	done := fmt.Sprintf("clone.done.%d", n)
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
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, at)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, elemLLVM, newData, at)
	if err := g.cloneOwnedValue(src, dst, elem); err != nil {
		return "", "", err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	slot := g.callSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.callSlots, "clone", r.ID, "{ ptr, i64, i64 }")
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", newData, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, lenField)
	capField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, capField)
	return g.load("{ ptr, i64, i64 }", slot), "Array<" + elem + ">", nil
}

// arrayRemove removes one element, moves the tail left, and returns the value.
func (g *generator) arrayRemove(r willc.Record, base, elem, elemLLVM string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: remove takes one index", r.ID)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
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

	temp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca %s\n", temp, elemLLVM)
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, elemLLVM, data, idx)
	if isAggregateType(elemLLVM) {
		g.copyValue(src, temp, elemLLVM)
	} else {
		value := g.load(elemLLVM, src)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, temp)
	}

	g.labels++
	n := g.labels
	cond := fmt.Sprintf("remove.cond.%d", n)
	body := fmt.Sprintf("remove.body.%d", n)
	done := fmt.Sprintf("remove.done.%d", n)
	cursor := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", cursor)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", idx, cursor)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	at := g.load("i64", cursor)
	nextAt := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextAt, at)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, nextAt, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", from, elemLLVM, data, nextAt)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", to, elemLLVM, data, at)
	if isAggregateType(elemLLVM) {
		g.copyValue(from, to, elemLLVM)
	} else {
		value := g.load(elemLLVM, from)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, to)
	}
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextAt, cursor)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	newLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", newLen, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newLen, lenPtr)
	if isAggregateType(elemLLVM) {
		return g.load(elemLLVM, temp), elem, nil
	}
	return g.load(elemLLVM, temp), elem, nil
}
