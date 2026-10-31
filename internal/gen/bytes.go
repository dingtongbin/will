package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// viewDataLen reads the data pointer and length of a two-word view.
func (g *generator) viewDataLen(base, lenField string) (string, string) {
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	length := g.load("i64", lenField)
	return data, length
}

// byteViewMethod lowers Compare, Equal, HasPrefix, HasSuffix, and Clone on a
// []u8 view. Every comparison first clips to the shorter side, so the memcmp
// never reads past either view.
func (g *generator) byteViewMethod(r willc.Record, base, lenField, name string) (string, string, error) {
	data, length := g.viewDataLen(base, lenField)
	if name == "clone" {
		return g.listFromSequence(data, length, "u8", r)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s takes one byte view", r.ID, name)
	}
	otherBase, otherType, err := g.place(args[0])
	if err != nil {
		return "", "", err
	}
	if otherType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s needs a byte view", r.ID, name)
	}
	otherLenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", otherLenField, otherBase)
	otherData, otherLength := g.viewDataLen(otherBase, otherLenField)
	shorter := g.newTmp()
	shorterCond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp slt i64 %s, %s\n", shorterCond, length, otherLength)
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 %s\n", shorter, shorterCond, length, otherLength)
	if name == "has_suffix" {
		// Start at length-suffix when the receiver is long enough; the length
		// check below still reports false when it is not.
		enough := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", enough, length, otherLength)
		offset := g.newTmp()
		shifted := g.newTmp()
		fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", shifted, length, otherLength)
		fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 0\n", offset, enough, shifted)
		start := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", start, data, offset)
		data = start
	}
	g.needMemcmpDecl = true
	cmp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)\n", cmp, data, otherData, shorter)
	cmpZero := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", cmpZero, cmp)
	switch name {
	case "equal":
		sameLen := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", sameLen, length, otherLength)
		result := g.newTmp()
		fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", result, sameLen, cmpZero)
		return result, "bool", nil
	case "has_prefix", "has_suffix":
		enough := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", enough, length, otherLength)
		result := g.newTmp()
		fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", result, enough, cmpZero)
		return result, "bool", nil
	case "compare":
		sameLen := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", sameLen, length, otherLength)
		lengthOrder := g.newTmp()
		lower := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp slt i64 %s, %s\n", lower, length, otherLength)
		fmt.Fprintf(g.out, "  %s = select i1 %s, i32 -1, i32 1\n", lengthOrder, lower)
		tail := g.newTmp()
		fmt.Fprintf(g.out, "  %s = select i1 %s, i32 0, i32 %s\n", tail, sameLen, lengthOrder)
		result := g.newTmp()
		fmt.Fprintf(g.out, "  %s = select i1 %s, i32 %s, i32 %s\n", result, cmpZero, tail, cmp)
		return result, "i32", nil
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: byte view method %s is not lowered yet", r.ID, name)
}

// viewIndexPlace builds Option<usize> from a byte search.
func (g *generator) viewIndexPlace(r willc.Record) (string, string, error) {
	recv := refField(r, "recv")
	base, baseType, err := g.place(recv)
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: index needs a byte view", r.ID)
	}
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	data, length := g.viewDataLen(base, lenField)
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: index takes one byte view", r.ID)
	}
	needleBase, needleType, err := g.place(args[0])
	if err != nil {
		return "", "", err
	}
	if needleType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: index needs a byte view", r.ID)
	}
	needleLenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", needleLenField, needleBase)
	needle, needleLength := g.viewDataLen(needleBase, needleLenField)
	g.needBytesDecl = true
	found := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @willrt_bytes_index(ptr %s, i64 %s, ptr %s, i64 %s)\n",
		found, data, length, needle, needleLength)
	info, ok := g.adtType("Option<usize>")
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Option<usize> is not lowered yet", r.ID)
	}
	slot := g.allocSlot(g.callSlots, "ret", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	isSome := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp sge i64 %s, 0\n", isSome, found)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("bytes.some.%d", n)
	endLabel := fmt.Sprintf("bytes.end.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isSome, someLabel, endLabel)
	g.out.WriteString(someLabel + ":\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", found, payload)
	fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	g.out.WriteString(endLabel + ":\n")
	return slot, info.llvm, nil
}
