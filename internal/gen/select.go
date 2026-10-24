package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// selectArms lists the arms of one select in writing order.
func (g *generator) selectArms(r willc.Record) []willc.Record {
	var arms []willc.Record
	for _, c := range g.recs {
		if c.Kind == "selarm" && refField(c, "parent") == r.ID {
			arms = append(arms, c)
		}
	}
	sortByID(arms)
	return arms
}

// prepareSelect reserves the arm array, receive buffers, and bind slots in the
// entry block, so a loop that selects repeatedly does not grow the stack.
func (g *generator) prepareSelect(r willc.Record) error {
	arms := g.selectArms(r)
	if len(arms) == 0 {
		return g.errf(r.ID, "cannot translate node #%d: select has no arms", r.ID)
	}
	name := "%v.select." + strconv.Itoa(r.ID)
	g.selectSlots[r.ID] = name
	fmt.Fprintf(g.out, "  %s = alloca [%d x %%willrt_arm]\n", name, len(arms))
	for _, arm := range arms {
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok {
			continue
		}
		switch chanNode.Kind {
		case "recv":
			chanText, err := g.willTypeText(refField(chanNode, "chan"))
			if err != nil {
				return err
			}
			elem, ok := chanElemText(chanText)
			if !ok {
				return g.errf(arm.ID, "cannot translate node #%d: a receive arm needs a channel", arm.ID)
			}
			elemLLVM, ok := g.typeLLVMOf(elem)
			if !ok || elemLLVM == "" {
				return g.errf(arm.ID, "cannot translate node #%d: channel element %s is not lowered yet", arm.ID, elem)
			}
			out := "%v.select.out." + strconv.Itoa(arm.ID)
			g.selectOutSlots[arm.ID] = out
			fmt.Fprintf(g.out, "  %s = alloca %s\n", out, elemLLVM)
			if bind := fieldText(arm, "bind"); bind != "" && bind != "_" {
				info, ok := g.adtType("Option<" + elem + ">")
				if !ok {
					return g.errf(arm.ID, "cannot translate node #%d: Option<%s> is not lowered yet", arm.ID, elem)
				}
				slot := "%v.select.bind." + strconv.Itoa(arm.ID)
				g.selectBindSlots[arm.ID] = slot
				g.selectBindTypes[arm.ID] = "Option<" + elem + ">"
				fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, info.llvm)
			}
		case "send":
			valueID := refField(chanNode, "value")
			typ, err := g.typeOf(valueID)
			if err != nil {
				return err
			}
			if typ != "" && !isAggregateType(typ) {
				value := "%v.select.send." + strconv.Itoa(chanNode.ID)
				g.sendValueSlots[chanNode.ID] = value
				fmt.Fprintf(g.out, "  %s = alloca %s\n", value, typ)
			}
		}
	}
	return nil
}

// selectStmt evaluates every channel and send value once, waits in the runtime
// in arm order, and runs the chosen body.
func (g *generator) selectStmt(r willc.Record, void bool) (bool, error) {
	arms := g.selectArms(r)
	if len(arms) == 0 {
		return false, g.errf(r.ID, "cannot translate node #%d: select has no arms", r.ID)
	}
	if fast, err := g.selectFast(r, arms, void); fast {
		return g.selectFastStmt(r, arms, void)
	} else if err != nil {
		return false, err
	}
	slot := g.allocSlot(g.selectSlots, "select", r.ID, fmt.Sprintf("[%d x %%willrt_arm]", len(arms)))
	g.needTaskDecl = true
	for i, arm := range arms {
		base := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x %%willrt_arm], ptr %s, i32 0, i32 %d\n", base, len(arms), slot, i)
		chanField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 0\n", chanField, base)
		outField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 1\n", outField, base)
		valueField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 2\n", valueField, base)
		kindField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 3\n", kindField, base)
		nsField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 4\n", nsField, base)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", chanField)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", outField)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", valueField)
		fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", nsField)
		if fieldText(arm, "default") == "true" {
			fmt.Fprintf(g.out, "  store i32 3, ptr %s\n", kindField)
			continue
		}
		if timeoutID := refField(arm, "timeout"); timeoutID != 0 {
			duration, err := g.value(timeoutID)
			if err != nil {
				return false, err
			}
			now := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i64 @willrt_now_ns()\n", now)
			deadline := g.newTmp()
			fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", deadline, now, duration)
			fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", deadline, nsField)
			fmt.Fprintf(g.out, "  store i32 2, ptr %s\n", kindField)
			continue
		}
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok {
			return false, g.errf(arm.ID, "cannot translate node #%d: the arm has no channel operation", arm.ID)
		}
		switch chanNode.Kind {
		case "recv":
			ptr, err := g.value(refField(chanNode, "chan"))
			if err != nil {
				return false, err
			}
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ptr, chanField)
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", g.selectOutSlots[arm.ID], outField)
			fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", kindField)
		case "send":
			ptr, err := g.value(refField(chanNode, "chan"))
			if err != nil {
				return false, err
			}
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ptr, chanField)
			valueID := refField(chanNode, "value")
			typ, err := g.typeOf(valueID)
			if err != nil {
				return false, err
			}
			valueText, err := g.willTypeText(valueID)
			if err != nil {
				return false, err
			}
			valuePtr := ""
			if isAggregateType(typ) {
				valuePtr, _, err = g.place(valueID)
				if err != nil {
					return false, err
				}
			} else {
				valuePtr = g.sendValueSlots[chanNode.ID]
				value, err := g.value(valueID)
				if err != nil {
					return false, err
				}
				fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, valuePtr)
			}
			if err := g.shareValue(valuePtr, valueText); err != nil {
				return false, err
			}
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", valuePtr, valueField)
			fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", kindField)
		default:
			return false, g.errf(arm.ID, "cannot translate node #%d: a select arm must receive, send, use default, or time out", arm.ID)
		}
	}
	g.labels++
	n := g.labels
	index := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_select(ptr %s, i64 %d)\n", index, slot, len(arms))
	end := fmt.Sprintf("select.end.%d", n)
	dispatch := fmt.Sprintf("select.dispatch.%d", n)
	negative := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp slt i32 %s, 0\n", negative, index)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", negative, end, dispatch)
	g.out.WriteString(dispatch + ":\n")
	var cases []string
	for i := range arms {
		cases = append(cases, fmt.Sprintf("i32 %d, label %%select.arm.%d.%d", i, n, i))
	}
	fmt.Fprintf(g.out, "  switch i32 %s, label %%%s [ %s ]\n", index, end, strings.Join(cases, " "))
	anyFall := false
	for i, arm := range arms {
		fmt.Fprintf(g.out, "select.arm.%d.%d:\n", n, i)
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
			anyFall = true
			fmt.Fprintf(g.out, "  br label %%%s\n", end)
		}
	}
	g.out.WriteString(end + ":\n")
	if !anyFall {
		g.out.WriteString("  unreachable\n")
		return true, nil
	}
	return false, nil
}

// selectFast reports a select that cannot block because it has a default arm
// and no timeout. Its arms are evaluated once and tried in source order, so it
// needs neither the arm array nor the runtime select call.
func (g *generator) selectFast(r willc.Record, arms []willc.Record, void bool) (bool, error) {
	hasDefault := false
	_ = r
	_ = void
	for _, arm := range arms {
		if refField(arm, "timeout") != 0 {
			return false, nil
		}
		if fieldText(arm, "default") == "true" {
			hasDefault = true
			continue
		}
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok || (chanNode.Kind != "recv" && chanNode.Kind != "send") {
			return false, nil
		}
	}
	return hasDefault, nil
}

func (g *generator) selectFastStmt(r willc.Record, arms []willc.Record, void bool) (bool, error) {
	index := g.allocSlot(g.selectFastSlots, "select.idx", r.ID, "i32")
	statusSlot := g.allocSlot(g.selectFastStatusSlots, "select.status", r.ID, "i32")
	chanVals := make([]string, len(arms))
	valueVals := make([]string, len(arms))
	valueTexts := make([]string, len(arms))
	valueTypes := make([]string, len(arms))
	defaultIndex := -1

	g.needTaskDecl = true
	g.needSelectSetDecl = true
	for i, arm := range arms {
		if fieldText(arm, "default") == "true" {
			defaultIndex = i
			continue
		}
		chanNode := g.recs[refField(arm, "chan")]
		chanPtr, err := g.value(refField(chanNode, "chan"))
		if err != nil {
			return false, err
		}
		chanVals[i] = chanPtr
		if chanNode.Kind != "send" {
			continue
		}
		valueID := refField(chanNode, "value")
		typ, err := g.typeOf(valueID)
		if err != nil {
			return false, err
		}
		text, err := g.willTypeText(valueID)
		if err != nil {
			return false, err
		}
		valueTypes[i] = typ
		valueTexts[i] = text
		if isAggregateType(typ) {
			ptr, _, err := g.place(valueID)
			if err != nil {
				return false, err
			}
			valueVals[i] = ptr
		} else {
			slot := g.sendValueSlots[chanNode.ID]
			if slot == "" {
				return false, g.errf(arm.ID, "cannot translate node #%d: the send arm has no value slot", arm.ID)
			}
			value, err := g.value(valueID)
			if err != nil {
				return false, err
			}
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, slot)
			valueVals[i] = slot
		}
		if err := g.shareValue(valueVals[i], text); err != nil {
			return false, err
		}
	}

	g.labels++
	n := g.labels
	dispatch := fmt.Sprintf("select.fast.dispatch.%d", n)
	for i, arm := range arms {
		if fieldText(arm, "default") == "true" {
			continue
		}
		chanNode := g.recs[refField(arm, "chan")]
		var status string
		if chanNode.Kind == "recv" {
			status = g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i32 @willrt_chan_try_recv(ptr %s, ptr %s)\n",
				status, chanVals[i], g.selectOutSlots[arm.ID])
		} else {
			status = g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i32 @willrt_chan_try_send(ptr %s, ptr %s)\n",
				status, chanVals[i], valueVals[i])
		}
		ok := g.newTmp()
		closed := g.newTmp()
		ready := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -4\n", closed, status)
		fmt.Fprintf(g.out, "  %s = or i1 %s, %s\n", ready, ok, closed)
		g.labels++
		next := fmt.Sprintf("select.fast.next.%d.%d", n, i)
		hit := fmt.Sprintf("select.fast.hit.%d.%d", n, i)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ready, hit, next)
		fmt.Fprintf(g.out, "%s:\n", hit)
		fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", i, index)
		fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", status, statusSlot)
		fmt.Fprintf(g.out, "  br label %%%s\n", dispatch)
		fmt.Fprintf(g.out, "%s:\n", next)
	}
	if defaultIndex < 0 {
		return false, g.errf(r.ID, "cannot translate node #%d: the fast select has no default", r.ID)
	}
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", defaultIndex, index)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", statusSlot)
	fmt.Fprintf(g.out, "  br label %%%s\n", dispatch)
	fmt.Fprintf(g.out, "%s:\n", dispatch)
	value := g.load("i32", index)
	g.labels++
	n = g.labels
	end := fmt.Sprintf("select.fast.end.%d", n)
	var cases []string
	for i := range arms {
		cases = append(cases, fmt.Sprintf("i32 %d, label %%select.fast.arm.%d.%d", i, n, i))
	}
	fmt.Fprintf(g.out, "  switch i32 %s, label %%%s [ %s ]\n", value, end, strings.Join(cases, " "))
	previousStatus := g.fastSelectStatus
	g.fastSelectStatus = statusSlot
	defer func() { g.fastSelectStatus = previousStatus }()
	anyFall := false
	for i, arm := range arms {
		fmt.Fprintf(g.out, "select.fast.arm.%d.%d:\n", n, i)
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
			anyFall = true
			fmt.Fprintf(g.out, "  br label %%%s\n", end)
		}
	}
	fmt.Fprintf(g.out, "%s:\n", end)
	if !anyFall {
		g.out.WriteString("  unreachable\n")
		return true, nil
	}
	return false, nil
}

// selectBind builds the Option<T> one receive arm binds, using the status the
// runtime recorded for the arm it chose.
func (g *generator) selectBind(arm willc.Record) error {
	slot := g.selectBindSlots[arm.ID]
	text := g.selectBindTypes[arm.ID]
	if slot == "" || text == "" {
		return g.errf(arm.ID, "cannot translate node #%d: the arm has no bind slot", arm.ID)
	}
	info, ok := g.adtType(text)
	if !ok {
		return g.errf(arm.ID, "cannot translate node #%d: %s is not lowered yet", arm.ID, text)
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	status := ""
	if g.fastSelectStatus != "" {
		status = g.load("i32", g.fastSelectStatus)
	} else {
		status = g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_select_status()\n", status)
	}
	okStatus := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", okStatus, status)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("select.some.%d", n)
	mergeLabel := fmt.Sprintf("select.merge.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", okStatus, someLabel, mergeLabel)
	g.out.WriteString(someLabel + ":\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	value := g.load(info.argLLVM[0], g.selectOutSlots[arm.ID])
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.argLLVM[0], value, payload)
	fmt.Fprintf(g.out, "  br label %%%s\n", mergeLabel)
	g.out.WriteString(mergeLabel + ":\n")
	return nil
}
