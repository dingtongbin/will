package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// hiddenIndex is the binding key of the counter a one-binding loop keeps for
// itself. A binding name cannot be this text.
const hiddenIndex = "$idx"

// arrayTypeText splits a fixed-array type text into its length and element
// text. Only [N]T with N written as digits is a fixed array.
func arrayTypeText(text string) (int, string, bool) {
	if !strings.HasPrefix(text, "[") {
		return 0, "", false
	}
	end := strings.IndexByte(text, ']')
	if end < 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(text[1:end])
	if err != nil || n < 0 {
		return 0, "", false
	}
	elem := text[end+1:]
	if elem == "" {
		return 0, "", false
	}
	return n, elem, true
}

// containerElem returns the element text of a view or Array type. Only a
// scalar element is lowered so far.
func containerElem(text string) (string, bool) {
	var elem string
	switch {
	case strings.HasPrefix(text, "Array<") && strings.HasSuffix(text, ">"):
		elem = text[len("Array<") : len(text)-1]
	case strings.HasPrefix(text, "[]"):
		elem = text[2:]
	default:
		return "", false
	}
	if _, _, ok := llvmScalar(elem); !ok {
		return "", false
	}
	return elem, true
}

// containerLLVM maps a view or Array type to its literal struct. A view is two
// words and an Array is three, as the layout volume fixes.
func containerLLVM(text string) (string, bool) {
	if _, ok := containerElem(text); !ok {
		return "", false
	}
	if strings.HasPrefix(text, "Array<") {
		return "{ ptr, i64, i64 }", true
	}
	return "{ ptr, i64 }", true
}

// llvmTypeText maps a type text the backend lowers to its LLVM type: a scalar,
// a fixed array of scalars, or a view/Array of a scalar.
func llvmTypeText(text string) (string, bool) {
	if typ, _, ok := llvmScalar(text); ok {
		return typ, true
	}
	if typ, ok := containerLLVM(text); ok {
		return typ, true
	}
	if n, elem, ok := arrayTypeText(text); ok {
		if et, _, ok := llvmScalar(elem); ok {
			return "[" + strconv.Itoa(n) + " x " + et + "]", true
		}
	}
	return "", false
}

// isAggregateType reports whether an LLVM type is one the lowering passes as a
// borrow and copies as a whole value.
func isAggregateType(typ string) bool {
	return isStructType(typ) || strings.HasPrefix(typ, "[") || strings.HasPrefix(typ, "{")
}

// loopBindType returns the will type text of one binding of a for record.
func (g *generator) loopBindType(decl int, name string) (string, bool) {
	types, ok := g.loopTypes[decl]
	if !ok {
		return "", false
	}
	t, ok := types[name]
	return t, ok
}

// nameSlot returns the slot and LLVM type behind a name record, for a local or
// a loop binding. A parameter or receiver is not a slot.
func (g *generator) nameSlot(r willc.Record) (string, string, bool) {
	decl := refField(r, "to")
	name := fieldText(r, "text")
	if binds, ok := g.loopBinds[decl]; ok {
		slot, ok := binds[name]
		if !ok {
			return "", "", false
		}
		t, ok := g.loopBindType(decl, name)
		if !ok {
			return "", "", false
		}
		typ, ok := llvmTypeText(t)
		if !ok {
			return "", "", false
		}
		return slot, typ, true
	}
	slot, ok := g.slots[decl]
	if !ok {
		return "", "", false
	}
	return slot, g.slotTypes[decl], true
}

// forContainer lowers a loop over a view or an Array. The two-word view and the
// three-word Array start with the data pointer and the length, so the loop
// loads both once, before the header: the container is borrowed for the whole
// run and its length cannot change, so the index needs no bound check. The
// element binding borrows the element, which a scalar read follows directly.
func (g *generator) forContainer(r willc.Record, elem string, void bool, label int) (bool, error) {
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	names := bindNames(r)
	if len(names) < 1 || len(names) > 2 {
		return false, g.errf(r.ID, "cannot translate node #%d: a loop over a view binds one element or an index and an element", r.ID)
	}
	elemType, _, ok := llvmScalar(elem)
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: only a view of a lowered scalar is iterated yet", r.ID)
	}
	base, baseType, err := g.place(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	binds := g.loopBinds[r.ID]
	idx := ""
	if binds != nil {
		idx = binds[hiddenIndex]
	}
	if idx == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop counter has no slot", r.ID)
	}
	elemName := names[len(names)-1]

	// The data pointer and the length are the first two words of both shapes.
	g.tmp++
	dataField := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataField, baseType, base)
	data := g.load("ptr", dataField)
	g.tmp++
	lenField := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", lenField, baseType, base)
	length := g.load("i64", lenField)

	condLabel := fmt.Sprintf("for.cond.%d", label)
	bodyLabel := fmt.Sprintf("for.body.%d", label)
	stepLabel := fmt.Sprintf("for.step.%d", label)
	endLabel := fmt.Sprintf("for.end.%d", label)

	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", idx)
	g.tmp++
	cond := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", cond, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	g.tmp++
	ptr := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemType, data, current)
	if g.loopPtrs[r.ID] == nil {
		g.loopPtrs[r.ID] = map[string]string{}
	}
	g.loopPtrs[r.ID][elemName] = ptr
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel})
	done, err := g.body(body, void)
	g.loops = g.loops[:len(g.loops)-1]
	if err != nil {
		return false, err
	}
	if !done {
		fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	}
	g.out.WriteString(stepLabel + ":\n")
	step := g.load("i64", idx)
	g.tmp++
	next := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, step)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

// forArray lowers a loop over a fixed array. The index runs from zero to the
// length, which is a constant, so no bound check is emitted: the array is a
// borrow and its length cannot change during the loop. An element binding
// receives a copy of the element, which for a scalar is indistinguishable from
// the read-only borrow the semantics volume specifies.
func (g *generator) forArray(r willc.Record, n int, elem string, void bool, label int) (bool, error) {
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	names := bindNames(r)
	if len(names) < 1 || len(names) > 2 {
		return false, g.errf(r.ID, "cannot translate node #%d: a loop over an array binds one element or an index and an element", r.ID)
	}
	elemType, _, ok := llvmScalar(elem)
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: only an array of a lowered scalar is iterated yet", r.ID)
	}
	base, baseType, err := g.place(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	want := "[" + strconv.Itoa(n) + " x " + elemType + "]"
	if baseType != want {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop needs %s, got %s", r.ID, want, baseType)
	}
	binds := g.loopBinds[r.ID]
	idx := ""
	if binds != nil {
		idx = binds[hiddenIndex]
	}
	if idx == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop counter has no slot", r.ID)
	}
	elemName := names[len(names)-1]

	condLabel := fmt.Sprintf("for.cond.%d", label)
	bodyLabel := fmt.Sprintf("for.body.%d", label)
	stepLabel := fmt.Sprintf("for.step.%d", label)
	endLabel := fmt.Sprintf("for.end.%d", label)

	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", idx)
	g.tmp++
	cond := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %d\n", cond, current, n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	g.tmp++
	ptr := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 0, i64 %s\n", ptr, baseType, base, current)
	if g.loopPtrs[r.ID] == nil {
		g.loopPtrs[r.ID] = map[string]string{}
	}
	g.loopPtrs[r.ID][elemName] = ptr
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel})
	done, err := g.body(body, void)
	g.loops = g.loops[:len(g.loops)-1]
	if err != nil {
		return false, err
	}
	if !done {
		fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	}
	g.out.WriteString(stepLabel + ":\n")
	step := g.load("i64", idx)
	g.tmp++
	next := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, step)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}
