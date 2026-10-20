package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// strMethodValue lowers the str methods other than Len. Byte-oriented methods
// work on the UTF-8 bytes; Trim only recognizes ASCII whitespace, which is the
// subset the current runtime can test without a Unicode table.
func (g *generator) strMethodValue(r willc.Record) (string, string, error) {
	name := fieldText(r, "text")
	data, length, err := g.strParts(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	args := g.callArgs(r)
	switch name {
	case "Contains":
		needle, needleLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		found := g.bytesSearch(data, length, needle, needleLen)
		bad := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp slt i64 %s, 0\n", bad, found)
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = xor i1 %s, true\n", value, bad)
		return value, "bool", nil
	case "HasPrefix":
		needle, needleLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		found := g.bytesSearch(data, length, needle, needleLen)
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", value, found)
		return value, "bool", nil
	case "HasSuffix":
		needle, needleLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		return g.strHasSuffix(r, data, length, needle, needleLen)
	case "Index":
		needle, needleLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		found := g.bytesSearch(data, length, needle, needleLen)
		return g.optionUSizeValue(r, found)
	case "Trim", "TrimLeft", "TrimRight":
		left, right := 1, 1
		if name == "TrimLeft" {
			right = 0
		} else if name == "TrimRight" {
			left = 0
		}
		return g.strTrim(r, data, length, left, right)
	case "Repeat":
		count, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		return g.strRepeat(r, data, length, count)
	case "Replace":
		oldData, oldLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		newData, newLen, err := g.strArg(args, 1)
		if err != nil {
			return "", "", err
		}
		return g.strReplace(r, data, length, oldData, oldLen, newData, newLen)
	case "Split", "Fields":
		return g.strSplit(r, data, length, args, name == "Fields")
	case "Clone":
		return g.strClone(r, data, length)
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: str.%s is not lowered yet", r.ID, name)
}

// strArg returns the bytes of argument i, which must be a str.
func (g *generator) strArg(args []int, i int) (string, string, error) {
	if i >= len(args) {
		return "", "", g.errf(0, "cannot translate: a str argument is missing")
	}
	return g.strParts(args[i])
}

// bytesSearch calls the runtime search and returns the signed offset.
func (g *generator) bytesSearch(data, length, needle, needleLen string) string {
	g.needBytesDecl = true
	found := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @willrt_bytes_index(ptr %s, i64 %s, ptr %s, i64 %s)\n",
		found, data, length, needle, needleLen)
	return found
}

// strHasSuffix compares the tail without reading before the start.
func (g *generator) strHasSuffix(r willc.Record, data, length, needle, needleLen string) (string, string, error) {
	enough := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", enough, length, needleLen)
	offset := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", offset, length, needleLen)
	safeOffset := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 0\n", safeOffset, enough, offset)
	start := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", start, data, safeOffset)
	g.needMemcmpDecl = true
	cmp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)\n", cmp, start, needle, needleLen)
	cmpZero := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", cmpZero, cmp)
	value := g.newTmp()
	fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", value, enough, cmpZero)
	return value, "bool", nil
}

// optionUSizeValue builds Option<usize> in the slot the prepass reserved and
// returns it as an aggregate value.
func (g *generator) optionUSizeValue(r willc.Record, found string) (string, string, error) {
	info, ok := g.adtType("Option<usize>")
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Option<usize> is not lowered yet", r.ID)
	}
	slot := g.strMethodSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.strMethodSlots, "strm", r.ID, info.llvm)
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	isSome := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp sge i64 %s, 0\n", isSome, found)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("str.index.some.%d", n)
	endLabel := fmt.Sprintf("str.index.end.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isSome, someLabel, endLabel)
	g.out.WriteString(someLabel + ":\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", found, payload)
	fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	g.out.WriteString(endLabel + ":\n")
	return g.load(info.llvm, slot), "Option<usize>", nil
}

// strTrim narrows the receiver to a view over the same bytes. A trim never
// copies because the bytes are immutable for the lifetime of the value.
func (g *generator) strTrim(r willc.Record, data, length string, left, right int) (string, string, error) {
	g.needBytesDecl = true
	startSlot := g.trimStartSlots[r.ID]
	if startSlot == "" {
		startSlot = g.allocSlot(g.trimStartSlots, "trim.start", r.ID, "i64")
	}
	lenSlot := g.trimLenSlots[r.ID]
	if lenSlot == "" {
		lenSlot = g.allocSlot(g.trimLenSlots, "trim.len", r.ID, "i64")
	}
	fmt.Fprintf(g.out, "  call i64 @willrt_bytes_trim(ptr %s, i64 %s, i32 %d, i32 %d, ptr %s, ptr %s)\n",
		data, length, left, right, startSlot, lenSlot)
	start := g.load("i64", startSlot)
	trimmed := g.load("i64", lenSlot)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", ptr, data, start)
	return g.storeStrValue(r, ptr, trimmed)
}

// strRepeat allocates count copies of the receiver.
func (g *generator) strRepeat(r willc.Record, data, length, count string) (string, string, error) {
	total := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %s\n", total, length, count)
	g.needAllocDecl = true
	buf := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buf, total)
	g.needBytesDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_bytes_repeat(ptr %s, ptr %s, i64 %s, i64 %s)\n",
		buf, data, length, count)
	return g.storeStrValue(r, buf, total)
}

// strReplace counts the matches, allocates the exact result length, and fills
// it with the runtime replacement.
func (g *generator) strReplace(r willc.Record, data, length, oldData, oldLen, newData, newLen string) (string, string, error) {
	g.needBytesDecl = true
	count := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @willrt_bytes_count(ptr %s, i64 %s, ptr %s, i64 %s)\n",
		count, data, length, oldData, oldLen)
	delta := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", delta, newLen, oldLen)
	growth := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %s\n", growth, count, delta)
	total := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", total, length, growth)
	g.needAllocDecl = true
	buf := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buf, total)
	fmt.Fprintf(g.out, "  call i64 @willrt_bytes_replace_into(ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)\n",
		buf, total, data, length, oldData, oldLen, newData, newLen)
	return g.storeStrValue(r, buf, total)
}

// strSplit builds a []str view over a plate of two-word parts.
func (g *generator) strSplit(r willc.Record, data, length string, args []int, fields bool) (string, string, error) {
	g.needBytesDecl = true
	g.needPlateDecl = true
	count := g.newTmp()
	if fields {
		fmt.Fprintf(g.out, "  %s = call i64 @willrt_bytes_fields(ptr %s, i64 %s, ptr null, i64 0)\n",
			count, data, length)
	} else {
		sepData, sepLen, err := g.strArg(args, 0)
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  %s = call i64 @willrt_bytes_count(ptr %s, i64 %s, ptr %s, i64 %s)\n",
			count, data, length, sepData, sepLen)
		one := g.newTmp()
		fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", one, count)
		count = one
	}
	plate := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_plate_new(i64 %s, i64 16)\n", plate, count)
	if fields {
		fmt.Fprintf(g.out, "  call i64 @willrt_bytes_fields(ptr %s, i64 %s, ptr %s, i64 %s)\n",
			data, length, plate, count)
	} else {
		sepData, sepLen, _ := g.strArg(args, 0)
		fmt.Fprintf(g.out, "  call i64 @willrt_bytes_split(ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)\n",
			data, length, sepData, sepLen, plate, count)
	}
	slot := g.strMethodSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.strMethodSlots, "strm", r.ID, "{ ptr, i64 }")
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", plate, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", count, lenField)
	return g.load("{ ptr, i64 }", slot), "[]str", nil
}

// strClone copies the receiver into a fresh str.
func (g *generator) strClone(r willc.Record, data, length string) (string, string, error) {
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	buf := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buf, length)
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", buf, data, length)
	return g.storeStrValue(r, buf, length)
}

// storeStrValue writes a str result into the slot the prepass reserved.
func (g *generator) storeStrValue(r willc.Record, data, length string) (string, string, error) {
	slot := g.strMethodSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.strMethodSlots, "strm", r.ID, "{ ptr, i64 }")
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, lenField)
	return g.load("{ ptr, i64 }", slot), "str", nil
}
