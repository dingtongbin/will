package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// isFinalContainerText reports whether text is one of the final GC container
// types. Their release is empty: the collector owns the payload.
func isFinalContainerText(text string) bool {
	return strings.HasPrefix(text, "list[") || strings.HasPrefix(text, "dict[") ||
		strings.HasPrefix(text, "set[")
}

// dictTypeText splits dict[K,V] into its key and value types.
func dictTypeText(text string) (k, v string, ok bool) {
	name, args, ok := splitGenericType(text)
	if !ok || name != "dict" || len(args) != 2 {
		return "", "", false
	}
	return args[0], args[1], true
}

// finalSetTypeText splits set[T] into its element type.
func finalSetTypeText(text string) (elem string, ok bool) {
	name, args, ok := splitGenericType(text)
	if !ok || name != "set" || len(args) != 1 {
		return "", false
	}
	return args[0], true
}

// mapEntryLLVM is the LLVM type of one Map entry.
func (g *generator) mapEntryLLVM(k, v string) (string, bool) {
	kt, ok := g.typeLLVMOf(k)
	if !ok {
		return "", false
	}
	vt, ok := g.typeLLVMOf(v)
	if !ok {
		return "", false
	}
	return "{ " + kt + ", " + vt + " }", true
}

// containerValueType is the LLVM type of a Set element or Map key/value.
func (g *generator) containerValueType(text string) (string, bool) {
	return g.typeLLVMOf(text)
}

// emitGuardedAlloc allocates cap*size bytes, skipping the allocation when the
// capacity is zero.
func (g *generator) emitGuardedAlloc(ptr *string, capValue string, size int, descriptor string) {
	g.labels++
	n := g.labels
	zero := fmt.Sprintf("seq.zero.%d", n)
	alloc := fmt.Sprintf("seq.alloc.%d", n)
	done := fmt.Sprintf("seq.ptr.%d", n)
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
	*ptr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n", *ptr, zero, allocated, alloc)
}

// containerCallResult returns the LLVM and will result types of a dict or set
// method. known is true for a method the lowering handles; llvm is empty for a
// method that returns no value.
func (g *generator) containerCallResult(r willc.Record) (llvm, text string, known bool) {
	if r.Kind != "mcall" {
		return "", "", false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", "", false
	}
	recvText = stripPredeclaredPackage(recvText)
	name := fieldText(r, "text")
	if k, v, ok := dictTypeText(recvText); ok {
		switch name {
		case "len":
			return "i64", "usize", true
		case "get":
			adt, ok := g.adtType("Option<" + v + ">")
			if !ok || adt.llvm == "" {
				return "", "", false
			}
			return adt.llvm, "Option<" + v + ">", true
		case "has", "delete":
			return "i1", "bool", true
		case "set", "clear", "each":
			return "", "", true
		case "keys":
			return "ptr", "list[" + k + "]", true
		case "values":
			return "ptr", "list[" + v + "]", true
		}
		return "", "", false
	}
	if _, ok := finalSetTypeText(recvText); ok {
		switch name {
		case "len":
			return "i64", "usize", true
		case "add", "has", "delete":
			return "i1", "bool", true
		case "clear", "each":
			return "", "", true
		}
	}
	return "", "", false
}

// containerMethod lowers a method call on dict[K,V] or set[T].
func (g *generator) containerMethod(r willc.Record, recvText string) (string, string, error) {
	ownerPin, owner, err := g.pinContainerReceiver(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if ownerPin != "" {
		defer g.unpinContainer(ownerPin)
	}
	var base, baseType string
	if _, _, ok := dictTypeText(recvText); ok {
		object, err := g.classPointerValue(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		object = g.pinObjectValue(object)
		defer g.unpinContainer(object)
		base, baseType = object, "{ ptr, i64, i64 }"
	} else if _, ok := finalSetTypeText(recvText); ok {
		object, err := g.classPointerValue(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		object = g.pinObjectValue(object)
		defer g.unpinContainer(object)
		base, baseType = object, "{ ptr, i64, i64 }"
	} else if owner != "" {
		base, baseType, _, err = g.classFieldAddressFrom(
			refField(r, "recv"), owner, r.ID)
	} else {
		base, baseType, err = g.place(refField(r, "recv"))
	}
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not a container", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	capPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capPtr, base)
	data := g.loadContainerData(dataPtr)
	// The data sequence can move while a read-only scan runs. Pin it once for
	// the whole method so every lookup sees one stable element array.
	dataPin := g.pinContainerData(data)
	defer g.unpinContainer(dataPin)
	data = dataPin
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	name := fieldText(r, "text")
	if k, v, ok := dictTypeText(recvText); ok {
		kt, _ := g.containerValueType(k)
		vt, _ := g.containerValueType(v)
		entry, ok := g.mapEntryLLVM(k, v)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node %d: the dict entry is not lowered yet", r.ID)
		}
		entrySize, _, ok := g.sizeAlign(entry)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node %d: the dict entry has no layout", r.ID)
		}
		descriptor, err := g.mapEntryDescriptor(k, v)
		if err != nil {
			return "", "", g.errf(r.ID, "cannot translate node %d: %v", r.ID, err)
		}
		args := g.callArgs(r)
		switch name {
		case "len":
			return length, "usize", nil
		case "clear":
			if err := g.releaseContainerEntries(data, length, entry, []string{k, v}); err != nil {
				return "", "", err
			}
			fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenPtr)
			return "", "", nil
		case "has":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: has takes one key", r.ID)
			}
			found, _, err := g.findMapKey(entry, kt, data, length, args[0])
			if err != nil {
				return "", "", err
			}
			return found, "bool", nil
		case "get":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: get takes one key", r.ID)
			}
			return g.mapGet(r, "Option<"+v+">", v, entry, kt, vt, data, length, args[0])
		case "set":
			if len(args) != 2 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: Set takes a key and a value", r.ID)
			}
			if err := g.mapSet(r, entry, entrySize, descriptor, kt, v, dataPtr, lenPtr, capPtr, args[0], args[1]); err != nil {
				return "", "", err
			}
			return "", "", nil
		case "delete":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: delete takes one key", r.ID)
			}
			return g.mapDelete(r, entry, entrySize, kt, k, v, dataPtr, lenPtr, args[0])
		case "keys", "values":
			field := 0
			elemText := k
			if name == "values" {
				field = 1
				elemText = v
			}
			return g.mapList(r, entry, data, length, field, elemText)
		case "each":
			return g.dictEach(r, entry, data, length, kt, vt)
		}
		return "", "", g.errf(r.ID, "cannot translate node #%d: dict.%s is not lowered yet", r.ID, name)
	}
	if elem, ok := finalSetTypeText(recvText); ok {
		et, _ := g.containerValueType(elem)
		entrySize, _, ok := g.sizeAlign(et)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node %d: the set element has no layout", r.ID)
		}
		descriptor, err := g.sequenceDescriptor(elem)
		if err != nil {
			return "", "", g.errf(r.ID, "cannot translate node %d: %v", r.ID, err)
		}
		args := g.callArgs(r)
		switch name {
		case "len":
			return length, "usize", nil
		case "clear":
			if err := g.releaseContainerEntries(data, length, et, []string{elem}); err != nil {
				return "", "", err
			}
			fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", lenPtr)
			return "", "", nil
		case "has":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: has takes one value", r.ID)
			}
			found, _, err := g.findSetValue(et, data, length, args[0])
			if err != nil {
				return "", "", err
			}
			return found, "bool", nil
		case "add":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: add takes one value", r.ID)
			}
			return g.setAdd(r, et, entrySize, descriptor, dataPtr, lenPtr, capPtr, args[0])
		case "delete":
			if len(args) != 1 {
				return "", "", g.errf(r.ID, "cannot translate node #%d: delete takes one value", r.ID)
			}
			return g.setDelete(r, et, elem, entrySize, dataPtr, lenPtr, args[0])
		case "each":
			return g.setEach(r, et, data, length)
		}
		return "", "", g.errf(r.ID, "cannot translate node #%d: set.%s is not lowered yet", r.ID, name)
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not a dict or set", r.ID)
}

// findMapKey scans a Map for a key and returns the found flag and the entry
// index in the block after the scan.
func (g *generator) findMapKey(entry, keyLLVM, data, length string, keyID int) (string, string, error) {
	return g.findContainerEntry(entry, data, length, func(kPtr string) (string, error) {
		return g.keyEquals(keyLLVM, kPtr, keyID)
	}, true)
}

// findSetValue scans a Set for a value and returns the found flag and the index.
func (g *generator) findSetValue(elemLLVM, data, length string, valueID int) (string, string, error) {
	return g.findContainerEntry(elemLLVM, data, length, func(kPtr string) (string, error) {
		return g.keyEquals(elemLLVM, kPtr, valueID)
	}, false)
}

// findContainerEntry emits a linear scan over data[0:length]. The comparison callback
// receives the address of the entry's key and returns an i1. The scan returns
// the found flag and the index in the block that follows it, so the caller can
// branch on the flag without a stack slot.
func (g *generator) findContainerEntry(entry, data, length string, eq func(kPtr string) (string, error), isMap bool) (string, string, error) {
	g.labels++
	n := g.labels
	pre := fmt.Sprintf("find.pre.%d", n)
	cond := fmt.Sprintf("find.cond.%d", n)
	body := fmt.Sprintf("find.body.%d", n)
	next := fmt.Sprintf("find.next.%d", n)
	hit := fmt.Sprintf("find.hit.%d", n)
	miss := fmt.Sprintf("find.miss.%d", n)
	done := fmt.Sprintf("find.done.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", pre)
	g.out.WriteString(pre + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	i := g.newTmp()
	nextName := "%next." + fmt.Sprint(n)
	fmt.Fprintf(g.out, "  %s = phi i64 [ 0, %%%s ], [ %s, %%%s ]\n", i, pre, nextName, next)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, i, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, miss)
	g.out.WriteString(body + ":\n")
	entryPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", entryPtr, entry, data, i)
	kPtr := entryPtr
	if isMap {
		kPtr = g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", kPtr, entry, entryPtr)
	}
	equal, err := eq(kPtr)
	if err != nil {
		return "", "", err
	}
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", equal, hit, next)
	g.out.WriteString(next + ":\n")
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextName, i)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(hit + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(miss + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	idx := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n", idx, i, hit, length, miss)
	found := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i1 [ true, %%%s ], [ false, %%%s ]\n", found, hit, miss)
	return found, idx, nil
}

// keyEquals compares one stored key with the call's key argument.
func (g *generator) keyEquals(keyLLVM, kPtr string, keyID int) (string, error) {
	if keyLLVM == "{ ptr, i64 }" {
		data, length, err := g.strParts(keyID)
		if err != nil {
			return "", err
		}
		return g.strEqAt(kPtr, data, length)
	}
	stored := g.load(keyLLVM, kPtr)
	value, err := g.value(keyID)
	if err != nil {
		return "", err
	}
	op := "icmp eq"
	if keyLLVM == "double" || keyLLVM == "float" {
		op = "fcmp oeq"
	}
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", out, op, keyLLVM, stored, value)
	return out, nil
}

// strEqAt compares the str at aPtr with an already loaded data pointer and
// length.
func (g *generator) strEqAt(aPtr, bData, bLen string) (string, error) {
	aDataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", aDataField, aPtr)
	aData := g.load("ptr", aDataField)
	aLenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", aLenField, aPtr)
	aLen := g.load("i64", aLenField)
	shorter := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", shorter, aLen, bLen)
	minLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 %s\n", minLen, shorter, aLen, bLen)
	g.needMemcmpDecl = true
	cmp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)\n", cmp, aData, bData, minLen)
	same := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", same, cmp)
	sameLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", sameLen, aLen, bLen)
	equal := g.newTmp()
	fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", equal, same, sameLen)
	return equal, nil
}

// storeContainerValue stores one element into a container slot. An interface
// element is boxed into its own owner; other elements keep the normal copy
// path used by Map and Set.
func (g *generator) storeContainerValue(dst string, id int, text string) error {
	if iface, ok := g.ifaceOfText(text); ok {
		sourceText, err := g.willTypeText(id)
		if err != nil {
			return err
		}
		if _, err := g.ifaceStoreValue(dst, id, iface, sourceText); err != nil {
			return err
		}
		value := g.load("ptr", dst)
		g.needGCDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
			dst, value)
		return nil
	}
	if isChanText(text) {
		if g.isCopySource(id) {
			value, err := g.value(id)
			if err != nil {
				return err
			}
			g.needTaskDecl = true
			g.needChanDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_chan_retain(ptr %s)\n", value)
		}
	}
	return g.storeValueAt(dst, id)
}

// storeValueAt writes one value into a slot of the given LLVM type.
func (g *generator) storeValueAt(dst string, id int) error {
	typ, err := g.typeOf(id)
	if err != nil {
		return err
	}
	if isAggregateType(typ) {
		src, srcType, err := g.place(id)
		if err != nil {
			return err
		}
		if srcType != typ {
			return g.errf(id, "cannot translate node #%d: the value needs %s", id, typ)
		}
		text, err := g.willTypeText(id)
		if err != nil {
			return err
		}
		g.gcPublishContainerRefs(dst, src, text)
		return g.cloneOwnedValue(src, dst, text)
	}
	value, err := g.value(id)
	if err != nil {
		return err
	}
	text, textErr := g.willTypeText(id)
	if textErr == nil {
		g.gcStoreContainerScalar(dst, value, typ, text)
		return nil
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, dst)
	return nil
}

// reserveEntries makes room for one more entry and returns the data pointer and
// length in the block after the growth.
func (g *generator) reserveEntries(entry string, entrySize int, descriptor string, dataPtr, lenPtr, capPtr string) (string, string, error) {
	data := g.loadContainerData(dataPtr)
	dataPin := g.pinContainerData(data)
	defer g.unpinContainer(dataPin)
	data = dataPin
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	capacity := g.load("i64", capPtr)
	g.labels++
	n := g.labels
	grow := fmt.Sprintf("res.grow.%d", n)
	ready := fmt.Sprintf("res.ready.%d", n)
	full := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", full, length, capacity)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", full, grow, ready)
	g.out.WriteString(grow + ":\n")
	doubled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, 2\n", doubled, capacity)
	isZero := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", isZero, capacity)
	newCap := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 4, i64 %s\n", newCap, isZero, doubled)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, newCap, entrySize)
	newData := g.newTmp()
	g.gcAllocSequence(newData, bytes, descriptor)
	// The fresh sequence has no root until the handle is published. Pin it
	// across the copy and the barrier store so a concurrent collection cannot
	// reclaim it while its bytes are being filled.
	newPinned := g.pinContainerData(newData)
	newData = newPinned
	g.emitEntryCopy(entry, data, newData, length)
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataPtr, newData)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newCap, capPtr)
	g.unpinContainer(newPinned)
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(ready + ":\n")
	return g.loadContainerData(dataPtr), g.load("i64", lenPtr), nil
}

// emitEntryCopy copies length entries from src to dst with a small loop.
func (g *generator) emitEntryCopy(entry, src, dst, length string) {
	g.labels++
	n := g.labels
	pre := fmt.Sprintf("copy.pre.%d", n)
	cond := fmt.Sprintf("copy.cond.%d", n)
	body := fmt.Sprintf("copy.body.%d", n)
	next := fmt.Sprintf("copy.next.%d", n)
	done := fmt.Sprintf("copy.done.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", pre)
	g.out.WriteString(pre + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	i := g.newTmp()
	nextValue := "%copy.next.v." + fmt.Sprint(n)
	fmt.Fprintf(g.out, "  %s = phi i64 [ 0, %%%s ], [ %s, %%%s ]\n", i, pre, nextValue, next)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, i, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	srcPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", srcPtr, entry, src, i)
	dstPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dstPtr, entry, dst, i)
	value := g.load(entry, srcPtr)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", entry, value, dstPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", next)
	g.out.WriteString(next + ":\n")
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextValue, i)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
}

// mapSet stores a value under a key, replacing an existing entry.
func (g *generator) mapSet(r willc.Record, entry string, entrySize int, descriptor, keyLLVM, valueText, dataPtr, lenPtr, capPtr string, keyID, valueID int) error {
	data := g.loadContainerData(dataPtr)
	pinned := g.pinContainerData(data)
	defer g.unpinContainer(pinned)
	data = pinned
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	found, idx, err := g.findMapKey(entry, keyLLVM, data, length, keyID)
	if err != nil {
		return err
	}
	g.labels++
	n := g.labels
	replace := fmt.Sprintf("set.replace.%d", n)
	appendLabel := fmt.Sprintf("set.append.%d", n)
	done := fmt.Sprintf("set.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", found, replace, appendLabel)
	g.out.WriteString(replace + ":\n")
	entryPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", entryPtr, entry, data, idx)
	vPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", vPtr, entry, entryPtr)
	if err := g.releaseOwnedValue(vPtr, valueText); err != nil {
		return err
	}
	if err := g.storeContainerValue(vPtr, valueID, valueText); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(appendLabel + ":\n")
	newData, newLen, err := g.reserveEntries(entry, entrySize, descriptor, dataPtr, lenPtr, capPtr)
	if err != nil {
		return err
	}
	newPinned := g.pinContainerData(newData)
	newData = newPinned
	newEntry := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", newEntry, entry, newData, newLen)
	kPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", kPtr, entry, newEntry)
	if err := g.storeValueAt(kPtr, keyID); err != nil {
		return err
	}
	vPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", vPtr, entry, newEntry)
	if err := g.storeContainerValue(vPtr, valueID, valueText); err != nil {
		return err
	}
	g.unpinContainer(newPinned)
	nextLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextLen, newLen)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextLen, lenPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return nil
}

// mapDelete removes one entry and reports whether it existed.
func (g *generator) mapDelete(r willc.Record, entry string, entrySize int, keyLLVM, keyText, valueText, dataPtr, lenPtr string, keyID int) (string, string, error) {
	data := g.loadContainerData(dataPtr)
	dataPin := g.pinContainerData(data)
	defer g.unpinContainer(dataPin)
	data = dataPin
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	found, idx, err := g.findMapKey(entry, keyLLVM, data, length, keyID)
	if err != nil {
		return "", "", err
	}
	return g.deleteEntry(entry, entrySize, found, idx, dataPtr, lenPtr, data, length, func(entryPtr string) error {
		if err := g.releaseOwnedValue(g.entryField(entry, entryPtr, 0), keyText); err != nil {
			return err
		}
		return g.releaseOwnedValue(g.entryField(entry, entryPtr, 1), valueText)
	})
}

// setAdd adds one value and reports whether it was new.
func (g *generator) setAdd(r willc.Record, elem string, entrySize int, descriptor string, dataPtr, lenPtr, capPtr string, valueID int) (string, string, error) {
	data := g.loadContainerData(dataPtr)
	pinned := g.pinContainerData(data)
	defer g.unpinContainer(pinned)
	data = pinned
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	found, _, err := g.findSetValue(elem, data, length, valueID)
	if err != nil {
		return "", "", err
	}
	g.labels++
	n := g.labels
	hit := fmt.Sprintf("add.hit.%d", n)
	appendLabel := fmt.Sprintf("add.append.%d", n)
	result := fmt.Sprintf("add.result.%d", n)
	appendEnd := fmt.Sprintf("add.end.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", found, hit, appendLabel)
	g.out.WriteString(hit + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", result)
	g.out.WriteString(appendLabel + ":\n")
	newData, newLen, err := g.reserveEntries(elem, entrySize, descriptor, dataPtr, lenPtr, capPtr)
	if err != nil {
		return "", "", err
	}
	newPinned := g.pinContainerData(newData)
	newData = newPinned
	slot := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", slot, elem, newData, newLen)
	if err := g.storeValueAt(slot, valueID); err != nil {
		return "", "", err
	}
	g.unpinContainer(newPinned)
	nextLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", nextLen, newLen)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", nextLen, lenPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", appendEnd)
	g.out.WriteString(appendEnd + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", result)
	g.out.WriteString(result + ":\n")
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i1 [ false, %%%s ], [ true, %%%s ]\n", out, hit, appendEnd)
	return out, "bool", nil
}

// setDelete removes one value and reports whether it existed.
func (g *generator) setDelete(r willc.Record, elem, elemText string, entrySize int, dataPtr, lenPtr string, valueID int) (string, string, error) {
	data := g.loadContainerData(dataPtr)
	dataPin := g.pinContainerData(data)
	defer g.unpinContainer(dataPin)
	data = dataPin
	g.publishContainerData(dataPtr, data)
	length := g.load("i64", lenPtr)
	found, idx, err := g.findSetValue(elem, data, length, valueID)
	if err != nil {
		return "", "", err
	}
	return g.deleteEntry(elem, entrySize, found, idx, dataPtr, lenPtr, data, length, func(entryPtr string) error {
		return g.releaseOwnedValue(entryPtr, elemText)
	})
}

// deleteEntry shifts the entries after idx down by one and returns the found
// flag. Release runs before the shift, while the removed entry is still in
// place.
func (g *generator) deleteEntry(entry string, entrySize int, found, idx, dataPtr, lenPtr, data, length string, release func(string) error) (string, string, error) {
	g.labels++
	n := g.labels
	del := fmt.Sprintf("del.yes.%d", n)
	miss := fmt.Sprintf("del.no.%d", n)
	done := fmt.Sprintf("del.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", found, del, miss)
	g.out.WriteString(del + ":\n")
	entryPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", entryPtr, entry, data, idx)
	if err := release(entryPtr); err != nil {
		return "", "", err
	}
	tail := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", tail, idx)
	remaining := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", remaining, length, tail)
	count := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", count, remaining, entrySize)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, entry, data, idx)
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, entry, data, tail)
	g.needMemmoveDecl = true
	fmt.Fprintf(g.out, "  call void @memmove(ptr %s, ptr %s, i64 %s)\n", dst, src, count)
	newLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, 1\n", newLen, length)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", newLen, lenPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(miss + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i1 [ true, %%%s ], [ false, %%%s ]\n", out, del, miss)
	return out, "bool", nil
}

// mapGet writes the found value or None into the Option slot of the call.
func (g *generator) mapGet(r willc.Record, text, valueText, entry, keyLLVM, valueLLVM, data, length string, keyID int) (string, string, error) {
	adt, ok := g.adtType(text)
	if !ok || adt.llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, text)
	}
	slot := g.callSlots[r.ID]
	if slot == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the Option result has no slot", r.ID)
	}
	found, idx, err := g.findMapKey(entry, keyLLVM, data, length, keyID)
	if err != nil {
		return "", "", err
	}
	g.labels++
	n := g.labels
	hit := fmt.Sprintf("get.hit.%d", n)
	miss := fmt.Sprintf("get.miss.%d", n)
	done := fmt.Sprintf("get.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", found, hit, miss)
	g.out.WriteString(hit + ":\n")
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, adt.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	entryPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", entryPtr, entry, data, idx)
	valuePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", valuePtr, entry, entryPtr)
	payloadPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payloadPtr, adt.llvm, slot, adt.payloadIndex)
	if err := g.cloneOwnedValue(valuePtr, payloadPtr, valueText); err != nil {
		return "", "", err
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(miss + ":\n")
	tagPtr = g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, adt.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return g.load(adt.llvm, slot), text, nil
}

// mapList copies one entry field of every live entry into a fresh list.
func (g *generator) mapList(r willc.Record, entry string, data, length string, field int, elemText string) (string, string, error) {
	outText := "list[" + elemText + "]"
	outLLVM, ok := g.typeLLVMOf(elemText)
	if !ok || outLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elemText)
	}
	size, _, ok := g.sizeAlign(outLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, elemText)
	}
	descriptor, err := g.sequenceDescriptor(elemText)
	if err != nil {
		return "", "", err
	}
	t, ok := g.classTypeOfText(outText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, outText)
	}
	payload, err := g.ensureClassPayload(t)
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
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			root, object)
	}
	object = g.pinObjectValue(object)
	defer g.unpinContainer(object)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, size)
	outData := g.newTmp()
	g.gcAllocSequence(outData, bytes, descriptor)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", outData, g.listField(object, payload, 0))
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, g.listField(object, payload, 1))
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, g.listField(object, payload, 2))
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("listcopy.cond.%d", n)
	body := fmt.Sprintf("listcopy.body.%d", n)
	done := fmt.Sprintf("listcopy.done.%d", n)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, entry, data, current)
	fieldPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		fieldPtr, entry, src, field)
	dst := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", dst, outLLVM, outData, current)
	g.gcStoreContainerAggregate(dst, fieldPtr, outLLVM, elemText)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return g.load("ptr", slot), outText, nil
}

// dictEach calls the callback for every live entry.
func (g *generator) dictEach(r willc.Record, entry, data, length, kt, vt string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: each takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("each.cond.%d", n)
	body := fmt.Sprintf("each.body.%d", n)
	done := fmt.Sprintf("each.done.%d", n)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	src := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", src, entry, data, current)
	kp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", kp, entry, src)
	k := g.load(kt, kp)
	vp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", vp, entry, src)
	v := g.load(vt, vp)
	fmt.Fprintf(g.out, "  call void %s(ptr %s, %s %s, %s %s)\n", code, env, kt, k, vt, v)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return "", "", nil
}

// setEach calls the callback for every live value.
func (g *generator) setEach(r willc.Record, et, data, length string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: each takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("seteach.cond.%d", n)
	body := fmt.Sprintf("seteach.body.%d", n)
	done := fmt.Sprintf("seteach.done.%d", n)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	slot := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", slot, et, data, current)
	v := g.load(et, slot)
	fmt.Fprintf(g.out, "  call void %s(ptr %s, %s %s)\n", code, env, et, v)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return "", "", nil
}
