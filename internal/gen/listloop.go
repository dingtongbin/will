package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// forList lowers `for v in xs` and `for i, v in xs` over one list. The payload
// is pinned for the loop, while the data sequence is read through the read
// barrier on every iteration because a collection can move it.
func (g *generator) forList(r willc.Record, elem, iterText string, void bool, label int) (bool, error) {
	object, err := g.classPointerValue(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	payload, ok := g.listPayloadText(iterText)
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: no payload for %s", r.ID, iterText)
	}
	object = g.pinObjectValue(object)
	defer g.unpinContainer(object)
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	binds := g.loopBinds[r.ID]
	idx := binds[hiddenIndex]
	if idx == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop index has no slot", r.ID)
	}
	names := bindNames(r)
	elemName := names[len(names)-1]
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	lenField := g.listField(object, payload, 1)
	length := g.load("i64", lenField)
	dataField := g.listField(object, payload, 0)
	data := g.loadContainerData(dataField)
	// The element address escapes into the loop body, which may reach a
	// safepoint, so the data sequence stays pinned until the loop ends.
	data = g.pinContainerData(data)
	defer g.unpinContainer(data)
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
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		elemPtr, elemLLVM, data, current)
	if g.loopPtrs[r.ID] == nil {
		g.loopPtrs[r.ID] = map[string]string{}
	}
	g.loopPtrs[r.ID][elemName] = elemPtr
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel, deferDepth: len(g.defers)})
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
