package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/will-lang/will/internal/gcmeta"
)

// ensureGCProgram builds the GC type graph on demand. A program that only uses
// containers still needs descriptors even when it declares no class.
func (g *generator) ensureGCProgram() error {
	if g.gcProgram != nil {
		return nil
	}
	program, err := gcmeta.Build(g.rawDocs())
	if err != nil {
		return err
	}
	g.gcProgram = program
	g.needGCMeta = true
	return nil
}

// gcContainerName derives a stable descriptor name from its layout inputs.
func gcContainerName(prefix, text string) string {
	sum := sha256.Sum256([]byte(text))
	return prefix + hex.EncodeToString(sum[:12])
}

// sequenceDescriptor returns the runtime descriptor for an array of elemText.
// The descriptor scans each capacity slot; the collector derives the count from
// the object bounds, so one descriptor serves every capacity.
func (g *generator) sequenceDescriptor(elemText string) (string, error) {
	if err := g.ensureGCProgram(); err != nil {
		return "", err
	}
	elem, err := g.gcProgram.InlineType(g.pkg, elemText)
	if err != nil {
		return "", err
	}
	if elem.Size <= 0 {
		return "", fmt.Errorf("container element %s has no layout", elemText)
	}
	g.gcInlineTypes[elem.Name] = elem
	elemName := ""
	if len(elem.Refs) != 0 || len(elem.Weaks) != 0 ||
		len(elem.Nested) != 0 || len(elem.Variants) != 0 {
		elemName = elem.Name
	}
	name := gcContainerName("container.sequence.",
		fmt.Sprintf("%s:%d", elem.Name, elem.Size))
	sequence := &gcmeta.Type{
		Name:     name,
		Kind:     gcmeta.KindStruct,
		Size:     0,
		Align:    8,
		Sequence: true,
		ElemType: elemName,
		ElemSize: elem.Size,
	}
	g.gcInlineTypes[name] = sequence
	g.needGCMeta = true
	return name, nil
}

// mapEntryDescriptor returns the descriptor for one Map entry. The entry layout
// mirrors the LLVM struct { K, V } exactly, including value padding.
func (g *generator) mapEntryDescriptor(keyText, valueText string) (string, error) {
	if err := g.ensureGCProgram(); err != nil {
		return "", err
	}
	key, err := g.gcProgram.InlineType(g.pkg, keyText)
	if err != nil {
		return "", err
	}
	value, err := g.gcProgram.InlineType(g.pkg, valueText)
	if err != nil {
		return "", err
	}
	keyLLVM, ok := g.typeLLVMOf(keyText)
	if !ok || keyLLVM == "" {
		return "", fmt.Errorf("map key %s has no layout", keyText)
	}
	valueLLVM, ok := g.typeLLVMOf(valueText)
	if !ok || valueLLVM == "" {
		return "", fmt.Errorf("map value %s has no layout", valueText)
	}
	entryLLVM := "{ " + keyLLVM + ", " + valueLLVM + " }"
	entrySize, entryAlign, ok := g.sizeAlign(entryLLVM)
	if !ok {
		return "", fmt.Errorf("map entry has no layout")
	}
	_, keyAlign, ok := g.sizeAlign(keyLLVM)
	if !ok {
		return "", fmt.Errorf("map key has no layout")
	}
	_, valueAlign, ok := g.sizeAlign(valueLLVM)
	if !ok {
		return "", fmt.Errorf("map value has no layout")
	}
	valueOffset := alignTo(key.Size, maxInt(keyAlign, valueAlign))
	var refs, weaks []uint32
	refs = append(refs, shiftU32(key.Refs, 0)...)
	weaks = append(weaks, shiftU32(key.Weaks, 0)...)
	refs = append(refs, shiftU32(value.Refs, valueOffset)...)
	weaks = append(weaks, shiftU32(value.Weaks, valueOffset)...)
	name := gcContainerName("container.entry.",
		fmt.Sprintf("%s:%s:%d", key.Name, value.Name, entrySize))
	entry := &gcmeta.Type{
		Name:  name,
		Kind:  gcmeta.KindStruct,
		Size:  entrySize,
		Align: entryAlign,
		Refs:  refs,
		Weaks: weaks,
	}
	if len(key.Nested) != 0 || len(key.Variants) != 0 ||
		len(value.Nested) != 0 || len(value.Variants) != 0 {
		entry.Refs = nil
		entry.Weaks = nil
		entry.Nested = nil
		appendMapField := func(offset int, t *gcmeta.Type) {
			if len(t.Nested) != 0 || len(t.Variants) != 0 {
				entry.Nested = append(entry.Nested, gcmeta.NestedField{
					Offset: uint32(offset), Type: t.Name})
				return
			}
			for _, ref := range t.Refs {
				entry.Nested = append(entry.Nested, gcmeta.NestedField{
					Offset: uint32(offset) + ref, Type: t.Name})
			}
			for _, weak := range t.Weaks {
				entry.Nested = append(entry.Nested, gcmeta.NestedField{
					Offset: uint32(offset) + weak, Type: t.Name, Weak: true})
			}
		}
		appendMapField(0, key)
		appendMapField(valueOffset, value)
	}
	g.gcInlineTypes[name] = entry
	sequence := name
	if len(entry.Refs) == 0 && len(entry.Weaks) == 0 && len(entry.Nested) == 0 {
		sequence = ""
	}
	seqName, err := g.sequenceFromElement(name, sequence, entrySize)
	if err != nil {
		return "", err
	}
	return seqName, nil
}

// sequenceFromElement registers a sequence descriptor around an existing
// element descriptor. An empty element name means no per-element scanning.
func (g *generator) sequenceFromElement(elementName, scanName string, elementSize int) (string, error) {
	if elementSize <= 0 {
		return "", fmt.Errorf("container element has no layout")
	}
	name := gcContainerName("container.sequence.",
		fmt.Sprintf("%s:%d", elementName, elementSize))
	sequence := &gcmeta.Type{
		Name:     name,
		Kind:     gcmeta.KindStruct,
		Size:     0,
		Align:    8,
		Sequence: true,
		ElemType: scanName,
		ElemSize: elementSize,
	}
	g.gcInlineTypes[name] = sequence
	g.needGCMeta = true
	return name, nil
}

// gcAllocSequence emits one variable-length allocation with its sequence
// descriptor. The payload size is exactly bytes; the runtime derives the slot
// count from the object bounds.
func (g *generator) gcAllocSequence(target, bytes, descriptor string) {
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %s, ptr %s)\n",
		target, bytes, gcmeta.DescriptorSymbol(descriptor))
	g.needGCDecl = true
	g.needGCMeta = true
}

// gcAllocText allocates immutable text storage as a GC byte sequence. The str
// and bytes descriptors scan the first word, so class fields, containers, and
// frame roots keep the storage alive without a finalizer.
func (g *generator) gcAllocText(target, bytes string) error {
	descriptor, err := g.sequenceDescriptor("u8")
	if err != nil {
		return err
	}
	g.gcAllocSequence(target, bytes, descriptor)
	return nil
}

// pinContainerReceiver pins the class object that owns a container field. A
// derived field address cannot cross a safepoint because the owner may move.
func (g *generator) pinContainerReceiver(recvID int) (string, string, error) {
	r, ok := g.recs[recvID]
	if !ok || r.Kind != "field" {
		return "", "", nil
	}
	ownerID := refField(r, "recv")
	text, err := g.willTypeText(ownerID)
	if err != nil {
		return "", "", err
	}
	if !g.isClassText(text) {
		return "", "", nil
	}
	object, err := g.value(ownerID)
	if err != nil {
		return "", "", err
	}
	pinned := g.pinObjectValue(object)
	return pinned, pinned, nil
}

// classFieldAddressFrom computes a field address from an already pinned owner
// pointer. The owner pointer is stable for the whole call, so a concurrent
// relocation cannot invalidate the interior pointer.
func (g *generator) classFieldAddressFrom(fieldID int, owner string, id int) (string, string, bool, error) {
	r, ok := g.recs[fieldID]
	if !ok || r.Kind != "field" {
		return "", "", false, g.errf(id, "cannot translate node #%d: the receiver is not a field", id)
	}
	recvID := refField(r, "recv")
	recvText, err := g.willTypeText(recvID)
	if err != nil {
		return "", "", false, err
	}
	t, ok := g.classTypeOfText(recvText)
	if !ok {
		return "", "", false, g.errf(id, "cannot translate node #%d: %s is not a class", id, recvText)
	}
	index, fieldName, fieldLLVM, isRef, err := g.classField(t, fieldText(r, "text"))
	if err != nil {
		return "", "", false, err
	}
	if fieldName == "" && index == 0 {
		return "", "", false, g.errf(id, "cannot translate node #%d: class %s has no field %s",
			id, t.Name, fieldText(r, "text"))
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", false, err
	}
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		ptr, payload, owner, index)
	return ptr, fieldLLVM, isRef, nil
}

// pinContainerData pins a sequence object while a lowering evaluates a value
// that may reach a safepoint. The pin is released at the end of the operation.
func (g *generator) pinContainerData(data string) string {
	pinned := g.newTmp()
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_pin(ptr %s)\n", pinned, data)
	return pinned
}

// publishContainerData writes a pinned sequence pointer back to its handle
// slot. A read barrier can return the pre-forwarding address while the pin
// follows the forwarding pointer; publishing keeps the handle and the pinned
// address in agreement for later operations.
func (g *generator) publishContainerData(dataPtr, data string) {
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataPtr, data)
}

func (g *generator) unpinContainer(object string) {
	if object == "" {
		return
	}
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_unpin(ptr %s)\n", object)
}

// loadContainerData reads the data word of a mutable container handle through
// the read barrier. The barrier returns the current untagged address after any
// relocation and writes it back into the handle.
func (g *generator) loadContainerData(dataPtr string) string {
	g.needGCDecl = true
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_read_barrier_slot(ptr %s)\n",
		out, dataPtr)
	return out
}

// isContainerTypeText reports whether a type text names a mutable container
// handle whose data pointer needs a write barrier.
func isContainerTypeText(text string) bool {
	return false
}

// gcInlineRefs returns the reference offsets of one inline container element.
// The second result is false when the type has no GC layout.
func (g *generator) gcInlineRefs(text string) ([]uint32, bool) {
	if err := g.ensureGCProgram(); err != nil {
		return nil, false
	}
	value, err := g.gcProgram.InlineType(g.pkg, text)
	if err != nil {
		return nil, false
	}
	g.gcInlineTypes[value.Name] = value
	return value.Refs, true
}

// gcStoreContainerScalar writes one scalar element. A reference element uses
// the untagged barrier because container storage is read by raw pointer.
func (g *generator) gcStoreContainerScalar(dst, value, llvm, text string) {
	refs, ok := g.gcInlineRefs(text)
	if ok && len(refs) == 1 && refs[0] == 0 {
		g.needGCDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
			dst, value)
		return
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", llvm, value, dst)
}

// gcPublishContainerRefs publishes the reference fields of an aggregate before
// its storage is overwritten. The old value reaches SATB and the new value
// reaches the barrier and card set.
func (g *generator) gcPublishContainerRefs(dst, src, text string) {
	refs, ok := g.gcInlineRefs(text)
	if !ok {
		return
	}
	g.needGCDecl = true
	for _, offset := range refs {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %d\n",
			field, dst, offset)
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %d\n",
			value, src, offset)
		loaded := g.load("ptr", value)
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
			field, loaded)
	}
}

// gcStoreContainerAggregate writes one aggregate element. Reference fields are
// published before the copy so the old values reach SATB and the new values
// reach the write barrier.
func (g *generator) gcStoreContainerAggregate(dst, src, llvm, text string) {
	g.gcPublishContainerRefs(dst, src, text)
	if llvm == "ptr" {
		g.gcStoreContainerScalar(dst, src, llvm, text)
		return
	}
	g.copyValue(src, dst, llvm)
}

func shiftU32(offsets []uint32, base int) []uint32 {
	if len(offsets) == 0 {
		return nil
	}
	out := make([]uint32, len(offsets))
	for i, offset := range offsets {
		out[i] = uint32(base) + offset
	}
	return out
}

func alignTo(value, align int) int {
	if align <= 1 {
		return value
	}
	return (value + align - 1) / align * align
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
