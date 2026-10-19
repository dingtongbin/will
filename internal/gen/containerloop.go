package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// prepareContainerLoop allocates the index and binding slots of a loop over a
// Map or a Set. It reports whether the iterator is such a container.
func (g *generator) prepareContainerLoop(r willc.Record, text string) bool {
	if k, v, ok := mapTypeText(text); ok {
		kt, ok := g.typeLLVMOf(k)
		if !ok {
			return false
		}
		vt, ok := g.typeLLVMOf(v)
		if !ok {
			return false
		}
		names := bindNames(r)
		if len(names) < 1 || len(names) > 2 {
			return false
		}
		if g.loopBinds[r.ID] == nil {
			g.loopBinds[r.ID] = map[string]string{}
			g.loopTypes[r.ID] = map[string]string{}
		}
		idx := g.newSlot(hiddenIndex)
		g.loopBinds[r.ID][hiddenIndex] = idx
		g.loopTypes[r.ID][hiddenIndex] = "usize"
		fmt.Fprintf(g.out, "  %s = alloca i64\n", idx)
		if len(names) == 2 {
			kSlot := g.newSlot(names[0])
			g.loopBinds[r.ID][names[0]] = kSlot
			g.loopTypes[r.ID][names[0]] = k
			fmt.Fprintf(g.out, "  %s = alloca %s\n", kSlot, kt)
			vSlot := g.newSlot(names[1])
			g.loopBinds[r.ID][names[1]] = vSlot
			g.loopTypes[r.ID][names[1]] = v
			fmt.Fprintf(g.out, "  %s = alloca %s\n", vSlot, vt)
			return true
		}
		vSlot := g.newSlot(names[0])
		g.loopBinds[r.ID][names[0]] = vSlot
		g.loopTypes[r.ID][names[0]] = v
		fmt.Fprintf(g.out, "  %s = alloca %s\n", vSlot, vt)
		return true
	}
	if elem, ok := setTypeText(text); ok {
		et, ok := g.typeLLVMOf(elem)
		if !ok {
			return false
		}
		names := bindNames(r)
		if len(names) < 1 || len(names) > 2 {
			return false
		}
		if g.loopBinds[r.ID] == nil {
			g.loopBinds[r.ID] = map[string]string{}
			g.loopTypes[r.ID] = map[string]string{}
		}
		idx := g.newSlot(hiddenIndex)
		g.loopBinds[r.ID][hiddenIndex] = idx
		g.loopTypes[r.ID][hiddenIndex] = "usize"
		fmt.Fprintf(g.out, "  %s = alloca i64\n", idx)
		elemName := names[len(names)-1]
		elemSlot := g.newSlot(elemName)
		g.loopBinds[r.ID][elemName] = elemSlot
		g.loopTypes[r.ID][elemName] = elem
		fmt.Fprintf(g.out, "  %s = alloca %s\n", elemSlot, et)
		if len(names) == 2 {
			g.loopBinds[r.ID][names[0]] = idx
			g.loopTypes[r.ID][names[0]] = "usize"
		}
		return true
	}
	return false
}

// forMap lowers `for k, v in m` and `for v in m`.
func (g *generator) forMap(r willc.Record, k, v string, void bool, label int) (bool, error) {
	base, baseType, err := g.place(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	if baseType != "{ ptr, i64, i64 }" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop needs a Map", r.ID)
	}
	entry, ok := g.mapEntryLLVM(k, v)
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the Map entry is not lowered yet", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	binds := g.loopBinds[r.ID]
	idx := binds[hiddenIndex]
	names := bindNames(r)
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	condLabel := fmt.Sprintf("for.cond.%d", label)
	bodyLabel := fmt.Sprintf("for.body.%d", label)
	stepLabel := fmt.Sprintf("for.step.%d", label)
	endLabel := fmt.Sprintf("for.end.%d", label)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", idx)
	cond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", cond, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	entryPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", entryPtr, entry, data, current)
	kPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", kPtr, entry, entryPtr)
	vPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", vPtr, entry, entryPtr)
	if len(names) == 2 {
		if err := g.copySlot(kPtr, binds[names[0]], k); err != nil {
			return false, err
		}
		if err := g.copySlot(vPtr, binds[names[1]], v); err != nil {
			return false, err
		}
	} else {
		if err := g.copySlot(vPtr, binds[names[0]], v); err != nil {
			return false, err
		}
	}
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
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, step)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

// forSet lowers `for v in s` and `for i, v in s`.
func (g *generator) forSet(r willc.Record, elem string, void bool, label int) (bool, error) {
	base, baseType, err := g.place(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	if baseType != "{ ptr, i64, i64 }" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop needs a Set", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the Set element is not lowered yet", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, base)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, base)
	length := g.load("i64", lenPtr)
	binds := g.loopBinds[r.ID]
	idx := binds[hiddenIndex]
	names := bindNames(r)
	elemName := names[len(names)-1]
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	condLabel := fmt.Sprintf("for.cond.%d", label)
	bodyLabel := fmt.Sprintf("for.body.%d", label)
	stepLabel := fmt.Sprintf("for.step.%d", label)
	endLabel := fmt.Sprintf("for.end.%d", label)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", idx)
	cond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", cond, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	elemPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", elemPtr, elemLLVM, data, current)
	if err := g.copySlot(elemPtr, binds[elemName], elem); err != nil {
		return false, err
	}
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
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, step)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

// copySlot copies one value text from a pointer into a binding slot.
func (g *generator) copySlot(src, dst, text string) error {
	typ, ok := g.typeLLVMOf(text)
	if !ok || typ == "" {
		return g.errf(0, "cannot translate the loop binding: type %s is not lowered yet", text)
	}
	if isAggregateType(typ) {
		g.copyValue(src, dst, typ)
		return nil
	}
	value := g.load(typ, src)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, dst)
	return nil
}
