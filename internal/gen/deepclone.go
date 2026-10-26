package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// cloneOwnedValue copies one value into fresh storage deep enough that the
// destination can outlive the source. rc handles gain one reference, str and
// container buffers are duplicated, and nested fields follow the same rule.
// It is the capture half of a deferred argument whose source may be reassigned
// or released before the thunk runs.
func (g *generator) cloneOwnedValue(src, dst, text string) error {
	text = g.qualifyTypeText(g.pkg, text)
	if isWeakText(text) {
		typ, _ := g.typeLLVMOf(text)
		g.copyValue(src, dst, typ)
		return nil
	}
	if isRcText(text) {
		value := g.load("i64", src)
		fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", value, dst)
		g.needRcDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		return nil
	}
	if text == "str" {
		return g.cloneStrValue(src, dst)
	}
	if elem, ok := arrayElemText(text); ok {
		return g.cloneArrayValue(src, dst, elem)
	}
	if k, v, ok := mapTypeText(text); ok {
		return g.cloneMapValue(src, dst, k, v)
	}
	if elem, ok := setTypeText(text); ok {
		return g.cloneSetValue(src, dst, elem)
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		return g.cloneADTValue(src, dst, kind, args)
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || typ == "" {
		return fmt.Errorf("type %s is not lowered yet", text)
	}
	g.copyValue(src, dst, typ)
	if !isStructType(typ) {
		return nil
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return nil
	}
	for i, fieldText := range info.texts {
		if !fieldTextNeedsClone(fieldText) {
			continue
		}
		from := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", from, info.llvm, src, i)
		to := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", to, info.llvm, dst, i)
		if err := g.cloneOwnedValue(from, to, fieldText); err != nil {
			return err
		}
	}
	return nil
}

// fieldTextNeedsClone reports whether a field can own a buffer or a handle.
func fieldTextNeedsClone(text string) bool {
	if text == "str" || isRcText(text) || isWeakText(text) {
		return true
	}
	if _, ok := arrayElemText(text); ok {
		return true
	}
	if _, _, ok := mapTypeText(text); ok {
		return true
	}
	if _, ok := setTypeText(text); ok {
		return true
	}
	if _, args, ok := parseADT(text); ok {
		return len(args) != 0
	}
	return strings.HasPrefix(text, "func(") || strings.HasPrefix(text, "%")
}

// cloneStrValue duplicates the bytes of one str.
func (g *generator) cloneStrValue(src, dst string) error {
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, src)
	lengthPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lengthPtr, src)
	data := g.load("ptr", dataPtr)
	length := g.load("i64", lengthPtr)
	buf := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buf, length)
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", buf, data, length)
	outData := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", outData, dst)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", buf, outData)
	outLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", outLen, dst)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, outLen)
	return nil
}

// cloneArrayValue duplicates the header and every element of an Array.
func (g *generator) cloneArrayValue(src, dst, elem string) error {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return fmt.Errorf("array element %s is not lowered yet", elem)
	}
	elemSize, _, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return fmt.Errorf("array element %s has no layout", elem)
	}
	srcDataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", srcDataPtr, src)
	srcLenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", srcLenPtr, src)
	srcData := g.load("ptr", srcDataPtr)
	length := g.load("i64", srcLenPtr)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, elemSize)
	newData := g.newTmp()
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", newData, bytes)
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("deep.arr.cond.%d", n)
	body := fmt.Sprintf("deep.arr.body.%d", n)
	done := fmt.Sprintf("deep.arr.done.%d", n)
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
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", from, elemLLVM, srcData, at)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", to, elemLLVM, newData, at)
	if err := g.cloneOwnedValue(from, to, elem); err != nil {
		return err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return g.storeContainerHeader(dst, newData, length)
}

// cloneMapValue duplicates the entries of a Map and clones each key and value.
func (g *generator) cloneMapValue(src, dst, k, v string) error {
	entry, ok := g.mapEntryLLVM(k, v)
	if !ok {
		return fmt.Errorf("map entry is not lowered yet")
	}
	entrySize, _, ok := g.sizeAlign(entry)
	if !ok {
		return fmt.Errorf("map entry has no layout")
	}
	return g.cloneEntries(src, dst, entry, entrySize, []string{k, v})
}

// cloneSetValue duplicates the entries of a Set and clones each element.
func (g *generator) cloneSetValue(src, dst, elem string) error {
	entry, ok := g.containerValueType(elem)
	if !ok || entry == "" {
		return fmt.Errorf("set element %s is not lowered yet", elem)
	}
	entrySize, _, ok := g.sizeAlign(entry)
	if !ok {
		return fmt.Errorf("set element has no layout")
	}
	return g.cloneEntries(src, dst, entry, entrySize, []string{elem})
}

// cloneEntries copies a Map or Set buffer and recursively clones the fields of
// each entry. Fields is one type for a Set and key/value for a Map.
func (g *generator) cloneEntries(src, dst, entry string, entrySize int, fields []string) error {
	srcDataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", srcDataPtr, src)
	srcLenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", srcLenPtr, src)
	srcData := g.load("ptr", srcDataPtr)
	length := g.load("i64", srcLenPtr)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, entrySize)
	newData := g.newTmp()
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", newData, bytes)
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("deep.map.cond.%d", n)
	body := fmt.Sprintf("deep.map.body.%d", n)
	done := fmt.Sprintf("deep.map.done.%d", n)
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
	from := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", from, entry, srcData, at)
	to := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", to, entry, newData, at)
	for i, fieldText := range fields {
		fieldFrom := from
		fieldTo := to
		if len(fields) > 1 {
			fieldFrom = g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", fieldFrom, entry, from, i)
			fieldTo = g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", fieldTo, entry, to, i)
		}
		if err := g.cloneOwnedValue(fieldFrom, fieldTo, fieldText); err != nil {
			return err
		}
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return g.storeContainerHeader(dst, newData, length)
}

// storeContainerHeader writes a cloned container's three-word header.
func (g *generator) storeContainerHeader(dst, data, length string) error {
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataField, dst)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenField, dst)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, lenField)
	capField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 2\n", capField, dst)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, capField)
	return nil
}

// cloneADTValue copies an Option or Result and clones its live payload.
func (g *generator) cloneADTValue(src, dst, kind string, args []string) error {
	info, ok := g.adtType(kindTypeText(kind, args))
	if !ok {
		return fmt.Errorf("adt %s is not lowered yet", kindTypeText(kind, args))
	}
	g.copyValue(src, dst, info.llvm)
	if len(args) == 0 {
		return nil
	}
	tag := g.load("i32", src)
	var branches []struct {
		tag  int
		text string
	}
	if kind == "option" {
		branches = append(branches, struct {
			tag  int
			text string
		}{1, args[0]})
	} else {
		branches = append(branches, struct {
			tag  int
			text string
		}{0, args[0]})
		if len(args) > 1 {
			branches = append(branches, struct {
				tag  int
				text string
			}{1, args[1]})
		}
	}
	for _, branch := range branches {
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("deep.adt.%d.%d", n, branch.tag)
		done := fmt.Sprintf("deep.adt.done.%d.%d", n, branch.tag)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, branch.tag)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		from := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", from, info.llvm, src, info.payloadIndex)
		to := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", to, info.llvm, dst, info.payloadIndex)
		if err := g.cloneOwnedValue(from, to, branch.text); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
	}
	return nil
}

// kindTypeText rebuilds Option<T> or Result<T,E> from its parsed shape.
func kindTypeText(kind string, args []string) string {
	if kind == "option" {
		return "Option<" + args[0] + ">"
	}
	if len(args) > 1 {
		return "Result<" + args[0] + "," + args[1] + ">"
	}
	return "Result<" + args[0] + ">"
}

// cloneValueCall lowers the universal Clone method for a value type that has
// no predeclared method: structs, Option/Result, and scalars. The result is a
// fresh owner in a call slot, so the caller can bind or return it independently.
func (g *generator) cloneValueCall(r willc.Record, text string) (string, string, error) {
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		src, _, err := g.place(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		info, ok := g.adtType(kindTypeText(kind, args))
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, text)
		}
		slot := g.allocSlot(g.callSlots, "clone", r.ID, info.llvm)
		if err := g.cloneOwnedValue(src, slot, text); err != nil {
			return "", "", err
		}
		return g.load(info.llvm, slot), text, nil
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || typ == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: type %s is not lowered yet", r.ID, text)
	}
	if !isAggregateType(typ) {
		value, err := g.value(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		return value, text, nil
	}
	if !isStructType(typ) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Clone of %s is not lowered yet", r.ID, text)
	}
	src, _, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	slot := g.allocSlot(g.callSlots, "clone", r.ID, typ)
	if err := g.cloneOwnedValue(src, slot, text); err != nil {
		return "", "", err
	}
	return g.load(typ, slot), text, nil
}

// releaseOwnedValue is the release half of cloneOwnedValue. It frees storage
// the clone owns, including nested buffers that plain releaseValue does not
// visit.
func (g *generator) releaseOwnedValue(ptr, text string) error {
	text = g.qualifyTypeText(g.pkg, text)
	if isWeakText(text) {
		return nil
	}
	// An enum has a tag in front of every variant payload; walking it as a
	// plain struct would release the wrong variant. Enum release is not lowered
	// yet, so it must stay a no-op rather than corrupt the tag.
	if info := g.enumInfoForText(text); info != nil {
		return g.releaseEnumValue(ptr, info)
	}
	if info := g.enumInfoForText(qualify(g.pkg, text)); info != nil {
		return g.releaseEnumValue(ptr, info)
	}
	if isRcText(text) {
		g.needRcDecl = true
		value := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", value)
		return nil
	}
	if text == "str" {
		g.needFreeStrDecl = true
		dataPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
		data := g.load("ptr", dataPtr)
		fmt.Fprintf(g.out, "  call void @willrt_free_str(ptr %s)\n", data)
		return nil
	}
	if elem, ok := arrayElemText(text); ok {
		return g.releaseArrayValue(ptr, elem)
	}
	if n, elem, ok := arrayTypeText(text); ok {
		return g.releaseFixedArray(ptr, elem, n)
	}
	if k, v, ok := mapTypeText(text); ok {
		return g.releaseEntries(ptr, k, v, true)
	}
	if elem, ok := setTypeText(text); ok {
		return g.releaseEntries(ptr, elem, "", false)
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		return g.releaseADTValue(ptr, kind, args)
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || typ == "" || !isStructType(typ) {
		return nil
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return nil
	}
	for i, fieldText := range info.texts {
		if !fieldTextNeedsClone(fieldText) {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, info.llvm, ptr, i)
		if err := g.releaseOwnedValue(field, fieldText); err != nil {
			return err
		}
	}
	return nil
}

// releaseEnumValue releases the payload of the active variant. The tag is read
// once and each variant with an owning argument gets its own branch.
func (g *generator) releaseEnumValue(ptr string, info *enumInfo) error {
	if info == nil || info.payloadIndex == 0 || len(info.variants) == 0 {
		return nil
	}
	tag := g.load("i32", ptr)
	for i := range info.variants {
		v := &info.variants[i]
		if len(v.args) == 0 || len(v.argLLVM) != len(v.args) {
			continue
		}
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("enum.rel.%d.%d", n, v.tag)
		done := fmt.Sprintf("enum.rel.done.%d.%d", n, v.tag)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, v.tag)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			payload, info.llvm, ptr, info.payloadIndex)
		for j, arg := range v.args {
			if !ownsReleaseText(g, arg) {
				continue
			}
			at := payload
			if len(v.args) > 1 {
				at = g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
					at, v.payload, payload, j)
			}
			if err := g.releaseOwnedValue(at, arg); err != nil {
				return err
			}
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
	}
	return nil
}

// releaseFixedArray releases every element of an inline [N]T. The array itself
// has no buffer to free.
func (g *generator) releaseFixedArray(ptr, elem string, n int) error {
	if n == 0 || !ownsReleaseText(g, elem) {
		return nil
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return fmt.Errorf("fixed array element %s is not lowered yet", elem)
	}
	for i := 0; i < n; i++ {
		at := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x %s], ptr %s, i32 0, i32 %d\n",
			at, n, elemLLVM, ptr, i)
		if err := g.releaseOwnedValue(at, elem); err != nil {
			return err
		}
	}
	return nil
}

// releaseArrayValue releases every live element and the data buffer.
func (g *generator) releaseArrayValue(ptr, elem string) error {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return fmt.Errorf("array element %s is not lowered yet", elem)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	data := g.load("ptr", dataPtr)
	length := g.load("i64", lenPtr)
	if err := g.emitElementLoop("deep.rel.arr", data, elemLLVM, length, func(element string) error {
		return g.releaseOwnedValue(element, elem)
	}); err != nil {
		return err
	}
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", data)
	return nil
}

// entryField returns a pointer to one field of an entry.
func (g *generator) entryField(entry, ptr string, index int) string {
	field := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, entry, ptr, index)
	return field
}

// releaseContainerEntries releases every live entry without freeing the data
// buffer. Clear uses it so the container keeps its capacity.
func (g *generator) releaseContainerEntries(data, length, entry string, fields []string) error {
	return g.emitElementLoop("deep.rel.entries", data, entry, length, func(element string) error {
		for i, fieldText := range fields {
			field := element
			if len(fields) > 1 {
				field = g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, entry, element, i)
			}
			if err := g.releaseOwnedValue(field, fieldText); err != nil {
				return err
			}
		}
		return nil
	})
}

// releaseEntries releases a Map or Set buffer and its fields.
func (g *generator) releaseEntries(ptr, k, v string, isMap bool) error {
	var entry string
	var fields []string
	if isMap {
		llvm, ok := g.mapEntryLLVM(k, v)
		if !ok {
			return fmt.Errorf("map entry is not lowered yet")
		}
		entry = llvm
		fields = []string{k, v}
	} else {
		llvm, ok := g.containerValueType(k)
		if !ok || llvm == "" {
			return fmt.Errorf("set element %s is not lowered yet", k)
		}
		entry = llvm
		fields = []string{k}
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	data := g.load("ptr", dataPtr)
	length := g.load("i64", lenPtr)
	if err := g.emitElementLoop("deep.rel.map", data, entry, length, func(element string) error {
		for i, fieldText := range fields {
			field := element
			if len(fields) > 1 {
				field = g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, entry, element, i)
			}
			if err := g.releaseOwnedValue(field, fieldText); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", data)
	return nil
}

// releaseADTValue releases the live payload of an Option or Result.
func (g *generator) releaseADTValue(ptr, kind string, args []string) error {
	info, ok := g.adtType(kindTypeText(kind, args))
	if !ok {
		return fmt.Errorf("adt %s is not lowered yet", kindTypeText(kind, args))
	}
	tag := g.load("i32", ptr)
	for _, branch := range adtBranches(kind, args) {
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("deep.rel.adt.%d.%d", n, branch.tag)
		done := fmt.Sprintf("deep.rel.adt.done.%d.%d", n, branch.tag)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, branch.tag)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, ptr, info.payloadIndex)
		if err := g.releaseOwnedValue(payload, branch.text); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
	}
	return nil
}

// adtBranches lists the tag and payload type of every live variant.
func adtBranches(kind string, args []string) []struct {
	tag  int
	text string
} {
	var out []struct {
		tag  int
		text string
	}
	if kind == "option" {
		return append(out, struct {
			tag  int
			text string
		}{1, args[0]})
	}
	out = append(out, struct {
		tag  int
		text string
	}{0, args[0]})
	if len(args) > 1 {
		out = append(out, struct {
			tag  int
			text string
		}{1, args[1]})
	}
	return out
}

// emitElementLoop runs one callback per element of a contiguous buffer.
func (g *generator) emitElementLoop(prefix, data, elem string, length string, visit func(string) error) error {
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("%s.cond.%d", prefix, n)
	body := fmt.Sprintf("%s.body.%d", prefix, n)
	done := fmt.Sprintf("%s.done.%d", prefix, n)
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
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", element, elem, data, at)
	if err := visit(element); err != nil {
		return err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return nil
}
