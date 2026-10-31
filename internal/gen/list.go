package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// listElemText returns the element type of one list value.
func listElemText(text string) (string, bool) {
	if strings.HasPrefix(text, "list[") && strings.HasSuffix(text, "]") {
		elem := strings.TrimSpace(text[len("list[") : len(text)-1])
		if elem == "" {
			return "", false
		}
		return elem, true
	}
	return "", false
}

// listField is the address of one payload word. The payload is data, len, cap.
func (g *generator) listField(obj, payload string, index int) string {
	field := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		field, payload, obj, index)
	return field
}

// constructList builds list[T](cap): one GC payload with a data sequence of
// cap elements, a zero length, and the capacity word.
func (g *generator) constructList(r willc.Record) (string, string, error) {
	text := fieldText(r, "type")
	elem, ok := listElemText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a list", r.ID, text)
	}
	t, ok := g.classTypeOfText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, text)
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", err
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
	}
	slot := g.allocSlot(g.newSlots, "list", r.ID, "ptr")
	g.newSlots[r.ID] = slot
	g.needGCDecl = true
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
	capValue := "0"
	args := g.callArgs(r)
	if len(args) != 0 {
		value, err := g.indexValue(args[0], r)
		if err != nil {
			return "", "", err
		}
		capValue = value
	}
	data := "null"
	if lit := intLitValue(args, g); lit > 0 {
		data = g.newTmp()
		g.gcAllocSequence(data, fmt.Sprintf("%d", lit*size), descriptor)
	} else if lit < 0 {
		g.emitGuardedAlloc(&data, capValue, size, descriptor)
	}
	dataField := g.listField(object, payload, 0)
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataField, data)
	lenField := g.listField(object, payload, 1)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenField)
	capField := g.listField(object, payload, 2)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", capValue, capField)
	if !g.gcGlobalInit {
		g.gcSafepoint(0)
	}
	return slot, "ptr", nil
}

// listFromSequence builds a fresh list holding a copy of src's length elements.
// The caller owns the returned GC payload; the source may alias the new data.
func (g *generator) listFromSequence(src, length, elem string, r willc.Record) (string, string, error) {
	text := "list[" + elem + "]"
	t, ok := g.classTypeOfText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, text)
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", err
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
	if err != nil {
		return "", "", err
	}
	slot := g.allocSlot(g.callSlots, "list", r.ID, "ptr")
	g.needGCDecl = true
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %d, ptr %s)\n",
		object, t.Size, gcmeta.DescriptorSymbol(t.Name))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", object, slot)
	if root := g.gcRootSlots[r.ID]; root != "" {
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n", root, object)
	}
	object = g.pinObjectValue(object)
	defer g.unpinContainer(object)
	data := g.newTmp()
	g.emitGuardedAlloc(&data, length, size, descriptor)
	if err := g.copySequence(src, data, length, elemLLVM, size); err != nil {
		return "", "", err
	}
	dataField := g.listField(object, payload, 0)
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n", dataField, data)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, g.listField(object, payload, 1))
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, g.listField(object, payload, 2))
	return g.load("ptr", slot), text, nil
}

// copySequence copies length elements of elemLLVM from src to dst.
func (g *generator) copySequence(src, dst, length, elemLLVM string, size int) error {
	g.needMemcpyDecl = true
	zeros := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", zeros, length)
	g.labels++
	n := g.labels
	zero := fmt.Sprintf("seqcopy.zero.%d", n)
	copyLabel := fmt.Sprintf("seqcopy.copy.%d", n)
	done := fmt.Sprintf("seqcopy.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", zeros, zero, copyLabel)
	g.out.WriteString(copyLabel + ":\n")
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, size)
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", dst, src, bytes)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(zero + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return nil
}

// listMethod lowers one method call on list[T]. The receiver is a GC payload
// pointer; every word read after an allocation goes through the pinned object.
func (g *generator) listMethod(r willc.Record, elem, recvText string) (string, string, error) {
	text := fieldText(r, "text")
	switch text {
	case "len":
		object, payload, done, err := g.listReceiver(r, recvText)
		if err != nil {
			return "", "", err
		}
		defer done()
		lenField := g.listField(object, payload, 1)
		return g.load("i64", lenField), "usize", nil
	case "get":
		return g.listGet(r, elem, recvText)
	case "set":
		return "", "", g.listSet(r, elem, recvText)
	case "push":
		return "", "", g.listPush(r, elem, recvText)
	case "clone":
		return g.listClone(r, elem, recvText)
	case "insert":
		return "", "", g.listInsert(r, elem, recvText)
	case "reserve":
		return "", "", g.listReserve(r, elem, recvText)
	case "shrink_to_fit":
		return "", "", g.listShrink(r, elem, recvText)
	case "append":
		return "", "", g.listAppend(r, elem, recvText)
	case "slice":
		return g.listSlice(r, elem, recvText)
	case "pop":
		return g.listPop(r, elem, recvText)
	case "any":
		return g.listAny(r, elem, recvText)
	case "all":
		return g.listAll(r, elem, recvText)
	case "filter":
		return g.listFilter(r, elem, recvText)
	case "find":
		return g.listFind(r, elem, recvText)
	case "map":
		return g.listMap(r, elem, recvText)
	case "fold":
		return g.listFold(r, elem, recvText)
	case "remove":
		return g.listRemove(r, elem, recvText)
	case "clear":
		object, payload, done, err := g.listReceiver(r, recvText)
		if err != nil {
			return "", "", err
		}
		defer done()
		lenField := g.listField(object, payload, 1)
		fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenField)
		return "", "", nil
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: list method %s is not lowered yet", r.ID, text)
}

// listReceiver pins the receiver payload for the rest of one lowering step.
func (g *generator) listReceiver(r willc.Record, recvText string) (string, string, func(), error) {
	object, err := g.classPointerValue(refField(r, "recv"))
	if err != nil {
		return "", "", nil, err
	}
	payload, ok := g.listPayloadText(recvText)
	if !ok {
		return "", "", nil, g.errf(r.ID, "cannot translate node #%d: no payload for %s", r.ID, recvText)
	}
	object = g.pinObjectValue(object)
	done := func() { g.unpinContainer(object) }
	return object, payload, done, nil
}

// listPayloadText resolves the payload type of one final container value.
func (g *generator) listPayloadText(text string) (string, bool) {
	base := stripPredeclaredPackage(text)
	_, isList := listElemText(base)
	_, _, isDict := dictTypeText(base)
	_, isSet := finalSetTypeText(base)
	if !isList && !isDict && !isSet {
		return "", false
	}
	t, ok := g.classTypeOfText(base)
	if !ok {
		return "", false
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", false
	}
	return payload, true
}

// listGetPlace returns the address of one list element after a bounds check.
func (g *generator) listGetPlace(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list get takes one index", r.ID)
	}
	index, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	g.labels++
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, index, length)
	g.panicCheck(bad, panicIndex, r)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, index)
	return cell, elemLLVM, nil
}

// listGet reads one element after a bounds check.
func (g *generator) listGet(r willc.Record, elem, recvText string) (string, string, error) {
	cell, elemLLVM, err := g.listGetPlace(r, elem, recvText)
	if err != nil {
		return "", "", err
	}
	return g.load(elemLLVM, cell), elem, nil
}

// listSet overwrites one element after a bounds check.
func (g *generator) listSet(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	args := g.callArgs(r)
	if len(args) != 2 {
		return g.errf(r.ID, "cannot translate node #%d: list set takes an index and a value", r.ID)
	}
	index, err := g.indexValue(args[0], r)
	if err != nil {
		return err
	}
	g.labels++
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, index, length)
	g.panicCheck(bad, panicIndex, r)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, index)
	if isAggregateType(elemLLVM) {
		src, srcType, err := g.place(args[1])
		if err != nil {
			return err
		}
		if srcType != elemLLVM {
			return g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		g.gcStoreContainerAggregate(cell, src, elemLLVM, elem)
		return nil
	}
	value, err := g.value(args[1])
	if err != nil {
		return err
	}
	g.gcStoreContainerScalar(cell, value, elemLLVM, elem)
	return nil
}

// listPush appends one element, growing the data sequence when it is full.
func (g *generator) listPush(r willc.Record, elem, recvText string) error {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return err
	}
	defer done()
	args := g.callArgs(r)
	if len(args) != 1 {
		return g.errf(r.ID, "cannot translate node #%d: list push takes one value", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	if isAggregateType(elemLLVM) {
		src, srcType, err := g.place(args[0])
		if err != nil {
			return err
		}
		if srcType != elemLLVM {
			return g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		tmp := g.newTmp()
		g.tempAlloca(tmp, elemLLVM)
		g.copyValue(src, tmp, elemLLVM)
		return g.listPushAggregate(object, payload, elem, tmp)
	}
	value, err := g.value(args[0])
	if err != nil {
		return err
	}
	return g.listPushValue(object, payload, elem, value)
}

// listPushAggregate appends one aggregate element from a stable source slot.
func (g *generator) listPushAggregate(object, payload, elem, src string) error {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(0, "cannot translate a list element: %s is not lowered yet", elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return g.errf(0, "cannot translate a list element: %s has no layout", elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
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
	grow := fmt.Sprintf("list.grow.%d", n)
	appendLabel := fmt.Sprintf("list.append.%d", n)
	doneLabel := fmt.Sprintf("list.done.%d", n)
	full := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", full, length, capacity)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", full, grow, appendLabel)
	g.out.WriteString(grow + ":\n")
	doubled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, 2\n", doubled, capacity)
	small := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, 1\n", small, doubled)
	newCap := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 1, i64 %s\n", newCap, small, doubled)
	oldData := g.loadContainerData(dataField)
	oldData = g.pinContainerData(oldData)
	allocBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", allocBytes, newCap, size)
	newData := g.newTmp()
	g.gcAllocSequence(newData, allocBytes, descriptor)
	if length != "0" {
		g.needMemcpyDecl = true
		copyBytes := g.newTmp()
		fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", copyBytes, length, size)
		empty := g.newTmp()
		afterCopy := fmt.Sprintf("list.copied.%d", n)
		copyLabel := fmt.Sprintf("list.copy.%d", n)
		fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, length)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, afterCopy, copyLabel)
		g.out.WriteString(copyLabel + ":\n")
		fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", newData, oldData, copyBytes)
		fmt.Fprintf(g.out, "  br label %%%s\n", afterCopy)
		g.out.WriteString(afterCopy + ":\n")
	}
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataField, newData)
	g.unpinContainer(oldData)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capField)
	fmt.Fprintf(g.out, "  br label %%%s\n", appendLabel)
	g.out.WriteString(appendLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, length)
	g.gcStoreContainerAggregate(cell, src, elemLLVM, elem)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, lenField)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return nil
}

// listGrowTo makes room for need elements and returns the data pointer. The old
// sequence is pinned before the allocation, so a moving collection cannot lose
// the bytes being copied.
func (g *generator) listGrowTo(object, payload, need string, size int, descriptor string) string {
	capField := g.listField(object, payload, 2)
	dataField := g.listField(object, payload, 0)
	lenField := g.listField(object, payload, 1)
	capacity := g.load("i64", capField)
	length := g.load("i64", lenField)
	g.labels++
	n := g.labels
	grow := fmt.Sprintf("list.reserve.%d", n)
	doneLabel := fmt.Sprintf("list.ready.%d", n)
	copyLabel := fmt.Sprintf("list.reservecopy.%d", n)
	afterCopy := fmt.Sprintf("list.reserved.%d", n)
	enough := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", enough, capacity, need)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", enough, doneLabel, grow)
	g.out.WriteString(grow + ":\n")
	doubled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, 2\n", doubled, capacity)
	bigger := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ugt i64 %s, %s\n", bigger, doubled, need)
	newCap := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 %s\n", newCap, bigger, doubled, need)
	oldData := g.loadContainerData(dataField)
	oldData = g.pinContainerData(oldData)
	allocBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", allocBytes, newCap, size)
	copyBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", copyBytes, length, size)
	newData := g.newTmp()
	g.gcAllocSequence(newData, allocBytes, descriptor)
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
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capField)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.loadContainerData(dataField)
}

// listInsert inserts one value before index i, moving the tail right.

func (g *generator) listPushValue(object, payload, elem, value string) error {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(0, "cannot translate a list element: %s is not lowered yet", elem)
	}
	size, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return g.errf(0, "cannot translate a list element: %s has no layout", elem)
	}
	descriptor, err := g.sequenceDescriptor(elem)
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
	grow := fmt.Sprintf("list.grow.%d", n)
	appendLabel := fmt.Sprintf("list.append.%d", n)
	doneLabel := fmt.Sprintf("list.done.%d", n)
	copyLabel := fmt.Sprintf("list.copy.%d", n)
	afterCopy := fmt.Sprintf("list.copied.%d", n)
	full := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", full, length, capacity)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", full, grow, appendLabel)
	g.out.WriteString(grow + ":\n")
	doubled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, 2\n", doubled, capacity)
	small := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, 1\n", small, doubled)
	newCap := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 1, i64 %s\n", newCap, small, doubled)
	oldData := g.loadContainerData(dataField)
	oldData = g.pinContainerData(oldData)
	allocBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", allocBytes, newCap, size)
	copyBytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", copyBytes, length, size)
	newData := g.newTmp()
	g.gcAllocSequence(newData, allocBytes, descriptor)
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
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capField)
	fmt.Fprintf(g.out, "  br label %%%s\n", appendLabel)
	g.out.WriteString(appendLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, length)
	g.gcStoreContainerScalar(cell, value, elemLLVM, elem)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, lenField)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return nil
}

// listFuncValue loads the code and environment words of one function value.
