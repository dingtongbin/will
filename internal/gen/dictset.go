package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// constructDictSet builds dict[K,V](cap) or set[T](cap): one GC payload with an
// entry sequence of cap entries, a zero length, and the capacity word.
func (g *generator) constructDictSet(r willc.Record) (string, string, error) {
	text := fieldText(r, "type")
	var entry string
	var entrySize int
	var descriptor string
	if k, v, ok := dictTypeText(text); ok {
		llvm, ok := g.mapEntryLLVM(k, v)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the dict entry is not lowered yet", r.ID)
		}
		size, _, ok := g.sizeAlign(llvm)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the dict entry has no layout", r.ID)
		}
		d, err := g.mapEntryDescriptor(k, v)
		if err != nil {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
		}
		entry, entrySize, descriptor = llvm, size, d
	} else if elem, ok := finalSetTypeText(text); ok {
		llvm, ok := g.typeLLVMOf(elem)
		if !ok || llvm == "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the set element is not lowered yet", r.ID)
		}
		size, _, ok := g.sizeAlign(llvm)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the set element has no layout", r.ID)
		}
		d, err := g.sequenceDescriptor(elem)
		if err != nil {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %v", r.ID, err)
		}
		entry, entrySize, descriptor = llvm, size, d
	} else {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a dict or set", r.ID, text)
	}
	t, ok := g.classTypeOfText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, text)
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", err
	}
	slot := g.allocSlot(g.newSlots, "container", r.ID, "ptr")
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
		g.gcAllocSequence(data, fmt.Sprintf("%d", lit*entrySize), descriptor)
	} else if lit < 0 {
		g.emitGuardedAlloc(&data, capValue, entrySize, descriptor)
	}
	dataField := g.listField(object, payload, 0)
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier_untagged(ptr %s, ptr %s)\n",
		dataField, data)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", g.listField(object, payload, 1))
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", capValue, g.listField(object, payload, 2))
	_ = entry
	if !g.gcGlobalInit {
		g.gcSafepoint(0)
	}
	return slot, "ptr", nil
}
