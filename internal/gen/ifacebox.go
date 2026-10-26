package gen

import (
	"fmt"
)

// ifaceStoreValue writes an owned interface value: it deep-copies the source
// into a heap box. A call argument stays borrowed and goes through ifaceValue;
// this function is only for locals, fields, containers, and returns.
func (g *generator) ifaceStoreValue(dst string, valueID int, iface *ifaceInfo, sourceText string) (string, error) {
	if _, same := g.ifaceOfText(sourceText); same {
		return g.ifaceCloneValue(dst, valueID, iface)
	}
	return g.ifaceOwnedValue(dst, valueID, iface, sourceText)
}

// ifaceOwnedValue boxes one concrete value. The box owns a deep copy, so the
// source keeps its own owner and can be released independently.
func (g *generator) ifaceOwnedValue(dst string, valueID int, iface *ifaceInfo, concrete string) (string, error) {
	concrete = g.canonicalTypeText(concrete)
	expected, ok := g.typeLLVMOf(concrete)
	if !ok || expected == "" {
		return "", g.errf(valueID, "cannot translate node #%d: %s is not lowered yet", valueID, concrete)
	}
	g.needAllocDecl = true
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, g.sizeOf(expected))
	if isAggregateType(expected) {
		src, srcType, err := g.place(valueID)
		if err != nil {
			return "", err
		}
		if srcType != expected {
			return "", g.errf(valueID, "cannot translate node #%d: the value needs %s", valueID, expected)
		}
		if err := g.cloneOwnedValue(src, box, concrete); err != nil {
			return "", err
		}
	} else {
		value, err := g.value(valueID)
		if err != nil {
			return "", err
		}
		if isRcText(concrete) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", expected, value, box)
	}
	return g.storeIfaceBox(dst, iface, box, concrete)
}

// ifaceCloneValue copies an interface value by calling the concrete clone
// thunk stored in its table. The source may be a borrowed parameter or a boxed
// owner; the result is always a fresh box.
func (g *generator) ifaceCloneValue(dst string, valueID int, iface *ifaceInfo) (string, error) {
	src, typ, err := g.place(valueID)
	if err != nil {
		return "", err
	}
	if typ != iface.llvm {
		return "", g.errf(valueID, "cannot translate node #%d: the interface value needs %s", valueID, iface.llvm)
	}
	return g.cloneIfaceAt(src, dst, iface)
}

// cloneIfaceAt copies one two-word interface value from src into dst.
func (g *generator) cloneIfaceAt(src, dst string, iface *ifaceInfo) (string, error) {
	dataPtr, tablePtr := g.ifaceWords(src, iface)
	data := g.load("ptr", dataPtr)
	table := g.load("ptr", tablePtr)
	null := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", null, data)
	g.labels++
	n := g.labels
	empty := fmt.Sprintf("iface.clone.empty.%d", n)
	have := fmt.Sprintf("iface.clone.have.%d", n)
	done := fmt.Sprintf("iface.clone.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", null, empty, have)
	g.out.WriteString(empty + ":\n")
	g.storeIfaceWords(dst, iface, "null", "null")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(have + ":\n")
	fnPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n",
		fnPtr, len(iface.methods)+3, table, len(iface.methods))
	fn := g.load("ptr", fnPtr)
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr %s(ptr %s)\n", box, fn, data)
	g.storeIfaceWords(dst, iface, box, table)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return dst, nil
}

// storeIfaceBox writes a data pointer and the method table for a concrete type.
func (g *generator) storeIfaceBox(dst string, iface *ifaceInfo, data, concrete string) (string, error) {
	table, err := g.vtable(iface, concrete)
	if err != nil {
		return "", err
	}
	g.storeIfaceWords(dst, iface, data, "@"+llvmSymbol(table))
	return dst, nil
}

func (g *generator) ifaceWords(base string, iface *ifaceInfo) (string, string) {
	data := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", data, iface.llvm, base)
	table := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", table, iface.llvm, base)
	return data, table
}

func (g *generator) storeIfaceWords(dst string, iface *ifaceInfo, data, table string) {
	dataPtr, tablePtr := g.ifaceWords(dst, iface)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataPtr)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", table, tablePtr)
}

// releaseIfaceValue releases one boxed interface value. A null data word is
// the empty interface and owns nothing.
func (g *generator) releaseIfaceValue(ptr string, iface *ifaceInfo) error {
	dataPtr, tablePtr := g.ifaceWords(ptr, iface)
	data := g.load("ptr", dataPtr)
	table := g.load("ptr", tablePtr)
	null := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", null, data)
	g.labels++
	n := g.labels
	empty := fmt.Sprintf("iface.rel.empty.%d", n)
	have := fmt.Sprintf("iface.rel.have.%d", n)
	done := fmt.Sprintf("iface.rel.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", null, empty, have)
	g.out.WriteString(empty + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(have + ":\n")
	fnPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n",
		fnPtr, len(iface.methods)+3, table, len(iface.methods)+1)
	fn := g.load("ptr", fnPtr)
	fmt.Fprintf(g.out, "  call void %s(ptr %s)\n", fn, data)
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", data)
	fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", dataPtr)
	fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", tablePtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return nil
}

// emitIfaceThunks drains the clone/drop pairs queued by vtable construction.
func (g *generator) emitIfaceThunks() error {
	if len(g.ifaceThunks) == 0 {
		return nil
	}
	thunks := g.ifaceThunks
	g.ifaceThunks = nil
	for _, t := range thunks {
		if err := g.emitIfaceThunk(t); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) emitIfaceThunk(t ifaceThunk) error {
	if t.iface == nil || t.concrete == "" {
		return nil
	}
	typ, ok := g.typeLLVMOf(t.concrete)
	if !ok || typ == "" {
		return fmt.Errorf("interface concrete type %s is not lowered", t.concrete)
	}
	g.needAllocDecl = true
	// Clone: returns a fresh box holding a deep copy. A slot avoids a phi
	// predecessor that cloneOwnedValue's internal branches would not preserve.
	fmt.Fprintf(g.out, "define internal ptr @%s(ptr %%data) {\nentry:\n", llvmSymbol(t.clone))
	result := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca ptr\n", result)
	fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", result)
	null := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %%data, null\n", null)
	g.labels++
	n := g.labels
	empty := fmt.Sprintf("iface.clone.null.%d", n)
	have := fmt.Sprintf("iface.clone.have.%d", n)
	done := fmt.Sprintf("iface.clone.ret.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", null, empty, have)
	g.out.WriteString(empty + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(have + ":\n")
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, g.sizeOf(typ))
	if err := g.cloneOwnedValue("%data", box, t.concrete); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", box, result)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	ret := g.load("ptr", result)
	fmt.Fprintf(g.out, "  ret ptr %s\n}\n", ret)

	// Drop: releases the concrete payload. The caller frees the box.
	fmt.Fprintf(g.out, "define internal void @%s(ptr %%data) {\nentry:\n", llvmSymbol(t.drop))
	null = g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %%data, null\n", null)
	g.labels++
	n = g.labels
	empty = fmt.Sprintf("iface.drop.null.%d", n)
	have = fmt.Sprintf("iface.drop.have.%d", n)
	done = fmt.Sprintf("iface.drop.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", null, empty, have)
	g.out.WriteString(empty + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(have + ":\n")
	if err := g.releaseOwnedValue("%data", t.concrete); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n  ret void\n}\n")
	return nil
}
