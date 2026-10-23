package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// generalSelectStmt lowers a select inside a general state machine. The arms
// are built once, the context lives in the frame, and the arm bodies run after
// a selected poll. A selected arm is detached before its body runs, so a
// break or return in the body cannot leave the context linked.
func (g *generator) generalSelectStmt(st willc.Record, void bool) (bool, error) {
	arms := g.selectArms(st)
	if len(arms) == 0 {
		return false, g.errf(st.ID, "cannot translate node #%d: the select has no arms", st.ID)
	}
	armType := fmt.Sprintf("[%d x %%willrt_arm]", len(arms))
	armsPtr := g.smAlloca(armType, "select.arms")
	ctxSlot := g.smAlloca("ptr", "select.ctx")
	indexSlot := g.smAlloca("i32", "select.index")
	outIndex := map[int]int{}
	valueIndex := map[int]int{}
	for _, arm := range arms {
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok {
			continue
		}
		switch chanNode.Kind {
		case "recv":
			chanText, err := g.willTypeText(refField(chanNode, "chan"))
			if err != nil {
				return false, err
			}
			elem, ok := chanElemText(chanText)
			if !ok {
				return false, g.errf(arm.ID, "cannot translate node #%d: select receive needs a channel", arm.ID)
			}
			elemLLVM, ok := g.typeLLVMOf(elem)
			if !ok || elemLLVM == "" {
				return false, g.errf(arm.ID, "cannot translate node #%d: channel element %s is not lowered", arm.ID, elem)
			}
			g.selectOutSlots[arm.ID] = g.smAlloca(elemLLVM, "select.out."+strconv.Itoa(arm.ID))
			outIndex[arm.ID] = 0
		case "send":
			valueID := refField(chanNode, "value")
			typ, err := g.typeOf(valueID)
			if err != nil {
				return false, err
			}
			if typ != "" && !isAggregateType(typ) {
				g.sendValueSlots[chanNode.ID] = g.smAlloca(typ, "select.value."+strconv.Itoa(chanNode.ID))
				valueIndex[chanNode.ID] = 0
			}
		}
	}
	info := &selectMachineInfo{
		arms:       arms,
		outIndex:   outIndex,
		valueIndex: valueIndex,
	}
	if err := g.emitSelectArmsTo(info, armsPtr); err != nil {
		return false, err
	}
	g.queueCDecl("willrt_select_start", "ptr", []string{"ptr", "i64"})
	g.queueCDecl("willrt_select_poll", "i32", []string{"ptr"})
	g.queueCDecl("willrt_select_finish", "void", []string{"ptr"})
	g.needWaitDecl = true
	ctx := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_select_start(ptr %s, i64 %d)\n",
		ctx, armsPtr, len(arms))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ctx, ctxSlot)
	id := g.smReserve()
	g.smStoreState(id)

	loaded := g.newTmp()
	fmt.Fprintf(g.out, "  %s = load ptr, ptr %s\n", loaded, ctxSlot)
	isNull := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", isNull, loaded)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.select.fail.%d, label %%sm.select.poll.%d\n", isNull, id, id)
	fmt.Fprintf(g.out, "sm.select.poll.%d:\n", id)
	polled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_select_poll(ptr %s)\n", polled, loaded)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", polled, indexSlot)
	pending := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -1\n", pending, polled)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.select.park.%d, label %%sm.select.ready.%d\n",
		pending, id, id)
	fmt.Fprintf(g.out, "sm.select.park.%d:\n", id)
	g.out.WriteString("  call void @willrt_task_mark_wait()\n")
	g.out.WriteString("  ret i32 -1\n")
	fmt.Fprintf(g.out, "sm.select.fail.%d:\n", id)
	fmt.Fprintf(g.out, "  store i32 -7, ptr %s\n", indexSlot)
	fmt.Fprintf(g.out, "  br label %%sm.select.after.%d\n", id)
	fmt.Fprintf(g.out, "sm.select.ready.%d:\n", id)
	fmt.Fprintf(g.out, "  call void @willrt_select_finish(ptr %s)\n", loaded)
	index := g.load("i32", indexSlot)
	negative := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp slt i32 %s, 0\n", negative, index)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.select.after.%d, label %%sm.select.dispatch.%d\n",
		negative, id, id)
	fmt.Fprintf(g.out, "sm.select.dispatch.%d:\n", id)
	var cases strings.Builder
	for i := range arms {
		fmt.Fprintf(&cases, " i32 %d, label %%sm.select.arm.%d.%d", i, id, i)
	}
	fmt.Fprintf(g.out, "  switch i32 %s, label %%sm.select.after.%d [%s ]\n",
		index, id, cases.String())
	for i, arm := range arms {
		fmt.Fprintf(g.out, "sm.select.arm.%d.%d:\n", id, i)
		chanNode, hasChan := g.recs[refField(arm, "chan")]
		if hasChan && chanNode.Kind == "recv" {
			if bind := fieldText(arm, "bind"); bind != "" && bind != "_" {
				if err := g.selectBind(arm); err != nil {
					return false, err
				}
			}
		}
		body, ok := g.recs[refField(arm, "body")]
		if !ok {
			return false, g.errf(arm.ID, "cannot translate node #%d: the arm body is missing", arm.ID)
		}
		done, err := g.body(body, void)
		if err != nil {
			return false, err
		}
		if !done {
			fmt.Fprintf(g.out, "  br label %%sm.select.after.%d\n", id)
		}
	}
	fmt.Fprintf(g.out, "sm.select.after.%d:\n", id)
	return false, nil
}
