package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// isStrConcat reports whether a binary expression concatenates two str values.
func (g *generator) isStrConcat(r willc.Record) bool {
	if r.Kind != "bin" || fieldText(r, "op") != "+" {
		return false
	}
	left, err := g.willTypeText(refField(r, "l"))
	return err == nil && left == "str"
}

// strCallResult returns the LLVM and will result types of a built-in str
// method. known is true for a method the lowering handles.
func (g *generator) strCallResult(r willc.Record) (llvm, text string, known bool) {
	if r.Kind != "mcall" {
		return "", "", false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil || recvText != "str" {
		return "", "", false
	}
	switch fieldText(r, "text") {
	case "len":
		return "i64", "usize", true
	case "contains", "has_prefix", "has_suffix":
		return "i1", "bool", true
	case "index":
		if info, ok := g.adtType("Option<usize>"); ok {
			return info.llvm, "Option<usize>", true
		}
	case "trim", "trim_left", "trim_right", "repeat", "replace", "clone":
		return "{ ptr, i64 }", "str", true
	case "split", "split_n", "fields":
		return "{ ptr, i64 }", "[]str", true
	case "Chars":
		return "{ ptr, i64, i64 }", "Array<u32>", true
	}
	return "", "", false
}

// strMethod lowers a built-in str method.
func (g *generator) strMethod(r willc.Record) (string, string, error) {
	base, baseType, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not a str", r.ID)
	}
	if fieldText(r, "text") == "len" {
		lenField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
		return g.load("i64", lenField), "usize", nil
	}
	return g.strMethodValue(r)
}

// strConcat builds a + b: one block of len(a)+len(b) bytes, with both operands
// copied in. The result is a str value in the slot the prepass reserved.
func (g *generator) strConcat(r willc.Record) (string, string, error) {
	slot := g.allocSlot(g.strCatSlots, "cat", r.ID, "{ ptr, i64 }")
	aData, aLen, err := g.strParts(refField(r, "l"))
	if err != nil {
		return "", "", err
	}
	bData, bLen, err := g.strParts(refField(r, "r"))
	if err != nil {
		return "", "", err
	}
	total := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", total, aLen, bLen)
	g.labels++
	n := g.labels
	zero := fmt.Sprintf("cat.zero.%d", n)
	alloc := fmt.Sprintf("cat.alloc.%d", n)
	ready := fmt.Sprintf("cat.ready.%d", n)
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, total)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, zero, alloc)
	g.out.WriteString(alloc + ":\n")
	buf := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buf, total)
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", buf, aData, aLen)
	tail := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", tail, buf, aLen)
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", tail, bData, bLen)
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(zero + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(ready + ":\n")
	data := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n", data, zero, buf, alloc)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", total, lenField)
	return slot, "{ ptr, i64 }", nil
}
