package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

func (g *generator) listInsert(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
	}
	args := g.callArgs(r)
	if len(args) != 2 {
		return g.errf(r.ID, "cannot translate node #%d: list insert takes an index and a value", r.ID)
	}
	index, err := g.indexValue(args[0], r)
	if err != nil {
		return err
	}
	value, err := g.value(args[1])
	if err != nil {
		return err
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", bad, index, length)
	g.panicCheck(bad, panicIndex, r)
	need := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", need, length)
	data := g.listGrowTo(object, payload, need, size, descriptor)
	tailBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", tailBytes, length, index)
	tailScaled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", tailScaled, tailBytes, size)
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		from, elemLLVM, data, index)
	toIndex := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", toIndex, index)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		to, elemLLVM, data, toIndex)
	g.needMemmoveDecl = true
	fmt.Fprintf(g.out, "  call void @memmove(ptr %s, ptr %s, i64 %s)\n", to, from, tailScaled)
	g.gcStoreContainerScalar(from, value, elemLLVM, elem)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", need, lenField)
	return nil
}

// listRemove removes and returns the value at index i.
func (g *generator) listRemove(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list remove takes an index", r.ID)
	}
	index, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, index, length)
	g.panicCheck(bad, panicIndex, r)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, index)
	value := g.load(elemLLVM, cell)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", next, length)
	tailBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", tailBytes, next, index)
	tailScaled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", tailScaled, tailBytes, size)
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		from, elemLLVM, data, index)
	toIndex := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", toIndex, index)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		to, elemLLVM, data, toIndex)
	g.needMemmoveDecl = true
	fmt.Fprintf(g.out, "  call void @memmove(ptr %s, ptr %s, i64 %s)\n", from, to, tailScaled)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, lenField)
	return value, elem, nil
}

// listClone returns an independent payload and data sequence with the same
// length and capacity.
func (g *generator) listClone(r willc.Record, elem, recvText string) (string, string, error) {
	src, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	size, elemLLVM, descriptor, err := g.listElemLayout(r, elem)
	if err != nil {
		return "", "", err
	}
	_ = elemLLVM
	srcDataField := g.listField(src, payload, 0)
	srcLenField := g.listField(src, payload, 1)
	srcCapField := g.listField(src, payload, 2)
	length := g.load("i64", srcLenField)
	capacity := g.load("i64", srcCapField)
	srcData := g.loadContainerData(srcDataField)
	srcData = g.pinContainerData(srcData)
	defer g.unpinContainer(srcData)
	t, ok := g.classTypeOfText(recvText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, recvText)
	}
	slot := g.allocSlot(g.newSlots, "list", r.ID, "ptr")
	g.newSlots[r.ID] = slot
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %d, ptr %s)\n",
		object, t.Size, gcmeta.DescriptorSymbol(t.Name))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", object, slot)
	if root := g.gcRootSlots[r.ID]; root != "" {
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			root, object)
	}
	object = g.pinObjectValue(object)
	defer g.unpinContainer(object)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, capacity, size)
	copyBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", copyBytes, length, size)
	g.labels++
	n := g.labels
	copyLabel := fmt.Sprintf("list.clonecopy.%d", n)
	afterCopy := fmt.Sprintf("list.cloned.%d", n)
	newData := g.newTmp()
	g.gcAllocSequence(newData, bytes, descriptor)
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, afterCopy, copyLabel)
	g.out.WriteString(copyLabel + ":\n")
	g.needMemcpyDecl = true
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", newData, srcData, copyBytes)
	fmt.Fprintf(g.out, "  br label %%%s\n", afterCopy)
	g.out.WriteString(afterCopy + ":\n")
	dataField := g.listField(object, payload, 0)
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataField, newData)
	lenField := g.listField(object, payload, 1)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, lenField)
	capField := g.listField(object, payload, 2)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", capacity, capField)
	return g.load("ptr", slot), recvText, nil
}

// listElemLayout returns the size, LLVM type, and sequence descriptor of one
// list element.
func (g *generator) listElemLayout(r willc.Record, elem string) (int, string, string, error) {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return 0, "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return 0, "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return 0, "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
	}
	return size, elemLLVM, descriptor, nil
}

// listReserve makes room for at least n elements without changing the length.
func (g *generator) listReserve(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	size, _, descriptor, err := g.listElemLayout(r, elem)
	if err != nil {
		return err
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return g.errf(r.ID, "cannot translate node #%d: list reserve takes one capacity", r.ID)
	}
	need, err := g.indexValue(args[0], r)
	if err != nil {
		return err
	}
	g.listGrowTo(object, payload, need, size, descriptor)
	return nil
}

// listShrink copies the live elements into a data sequence of exactly len
// capacity when the current capacity is larger.
func (g *generator) listShrink(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	size, _, descriptor, err := g.listElemLayout(r, elem)
	if err != nil {
		return err
	}
	lenField := g.listField(object, payload, 1)
	capField := g.listField(object, payload, 2)
	dataField := g.listField(object, payload, 0)
	length := g.load("i64", lenField)
	capacity := g.load("i64", capField)
	g.labels++
	n := g.labels
	shrink := fmt.Sprintf("list.shrink.%d", n)
	doneLabel := fmt.Sprintf("list.shrunk.%d", n)
	copyLabel := fmt.Sprintf("list.shrinkcopy.%d", n)
	afterCopy := fmt.Sprintf("list.shrinkdone.%d", n)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", more, capacity, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, shrink, doneLabel)
	g.out.WriteString(shrink + ":\n")
	oldData := g.loadContainerData(dataField)
	oldData = g.pinContainerData(oldData)
	copyBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", copyBytes, length, size)
	newData := g.newTmp()
	g.gcAllocSequence(newData, copyBytes, descriptor)
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, afterCopy, copyLabel)
	g.out.WriteString(copyLabel + ":\n")
	g.needMemcpyDecl = true
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", newData, oldData, copyBytes)
	fmt.Fprintf(g.out, "  br label %%%s\n", afterCopy)
	g.out.WriteString(afterCopy + ":\n")
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataField, newData)
	g.unpinContainer(oldData)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, capField)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return nil
}

// listAppend copies every element of a view to the end of the list.
func (g *generator) listAppend(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	size, _, descriptor, err := g.listElemLayout(r, elem)
	if err != nil {
		return err
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return g.errf(r.ID, "cannot translate node #%d: list append takes one view", r.ID)
	}
	base, baseType, err := g.place(args[0])
	if err != nil {
		return err
	}
	if baseType != "{ ptr, i64 }" {
		return g.errf(r.ID, "cannot translate node #%d: list append needs a view", r.ID)
	}
	viewDataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n",
		viewDataPtr, base)
	viewData := g.loadContainerData(viewDataPtr)
	viewData = g.pinContainerData(viewData)
	defer g.unpinContainer(viewData)
	viewLenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n",
		viewLenPtr, base)
	viewLen := g.load("i64", viewLenPtr)
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	need := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", need, length, viewLen)
	data := g.listGrowTo(object, payload, need, size, descriptor)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		dst, g.listElemLLVM(elem, r), data, length)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, viewLen, size)
	g.labels++
	n := g.labels
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, viewLen)
	skip := fmt.Sprintf("list.appenddone.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, skip,
		fmt.Sprintf("list.appendcopy.%d", n))
	g.out.WriteString(fmt.Sprintf("list.appendcopy.%d", n) + ":\n")
	g.needMemcpyDecl = true
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", dst, viewData, bytes)
	fmt.Fprintf(g.out, "  br label %%%s\n", skip)
	g.out.WriteString(skip + ":\n")
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", need, lenField)
	return nil
}

// listElemLLVM is the LLVM type of one list element.
func (g *generator) listElemLLVM(elem string, r willc.Record) string {
	typ, _ := g.typeLLVMOf(elem)
	return typ
}

// listSlice returns a view over [lo, hi) of the live elements.
func (g *generator) listSlice(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 2 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list slice takes two bounds", r.ID)
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	lo, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	hi, err := g.indexValue(args[1], r)
	if err != nil {
		return "", "", err
	}
	badLo := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", badLo, lo, hi)
	g.panicCheck(badLo, panicIndex, r)
	badHi := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", badHi, hi, length)
	g.panicCheck(badHi, panicIndex, r)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	viewData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		viewData, elemLLVM, data, lo)
	viewLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", viewLen, hi, lo)
	slot := g.allocSlot(g.callSlots, "slice", r.ID, "{ ptr, i64 }")
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n",
		dataPtr, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", viewData, dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n",
		lenPtr, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", viewLen, lenPtr)
	return g.load("{ ptr, i64 }", slot), "[]" + elem, nil
}

// listPop removes and returns the last element as Option<T>.
func (g *generator) listPop(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	optText := "Option<" + elem + ">"
	info, ok := g.adtType(optText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, optText)
	}
	slot := g.allocSlot(g.callSlots, "pop", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n",
		tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("list.pop.%d", n)
	doneLabel := fmt.Sprintf("list.popped.%d", n)
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, doneLabel, someLabel)
	g.out.WriteString(someLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", next, length)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, next)
	value := g.load(elemLLVM, cell)
	if info.payloadIndex != 0 {
		payloadPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			payloadPtr, info.llvm, slot, info.payloadIndex)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, payloadPtr)
	}
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, lenField)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.load(info.llvm, slot), optText, nil
}

// listPushValue appends one lowered value, growing the sequence when full.
