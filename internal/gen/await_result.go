package gen

import (
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

// awaitElemText returns the payload type of one await operand. A direct async
// call names its payload as the call result; a Task value names it in Task<T>.
func (g *generator) awaitElemText(id int) (string, error) {
	if r, ok := g.recs[id]; ok && (r.Kind == "call" || r.Kind == "mcall") {
		text, err := g.callResultText(r)
		if err != nil {
			return "", err
		}
		if elem, ok := taskElemText(text); ok {
			return elem, nil
		}
		// A direct async call yields its own result; await is transparent.
		if text == "" {
			return "()", nil
		}
		return text, nil
	}
	text, err := g.willTypeText(id)
	if err != nil {
		return "", err
	}
	if elem, ok := taskElemText(text); ok {
		return elem, nil
	}
	if r, found := g.recs[id]; found && (r.Kind == "recv" || r.Kind == "send") {
		return text, nil
	}
	return "", g.errf(id, "cannot translate node #%d: await needs a Task", id)
}

// awaitResultText returns Result<T,TaskError> for one await operand.
func (g *generator) awaitResultText(id int) (string, error) {
	if r, ok := g.recs[id]; ok && (r.Kind == "call" || r.Kind == "mcall") {
		text, err := g.callResultText(r)
		if err != nil {
			return "", err
		}
		if elem, isTask := taskElemText(text); isTask {
			return "Result<" + elem + ",TaskError>", nil
		}
		return text, nil
	}
	if r, ok := g.recs[id]; ok && (r.Kind == "recv" || r.Kind == "send") {
		return g.willTypeText(id)
	}
	elem, err := g.awaitElemText(id)
	if err != nil {
		return "", err
	}
	return "Result<" + elem + ",TaskError>", nil
}

// awaitTaskValue returns the Task pointer and payload type for one operand.
// A direct async call is materialized as a child task; a Task value is used as
// it is.
func (g *generator) awaitTaskValue(id int) (string, string, error) {
	r, ok := g.recs[id]
	if ok && (r.Kind == "call" || r.Kind == "mcall") {
		text, err := g.callResultText(r)
		if err != nil {
			return "", "", err
		}
		if elem, isTask := taskElemText(text); isTask {
			task, err := g.value(id)
			return task, elem, err
		}
		sym := g.awaitTargetSymbol(r)
		if sym == "" || !g.suspendableFor(sym) {
			return "", "", g.errf(id, "cannot translate node #%d: await needs an async call or Task", id)
		}
		task, err := g.emitConstructorCall(r, sym, "-1", false)
		if err != nil {
			return "", "", err
		}
		if err := g.finishAsyncSpawnArgs(r, sym); err != nil {
			return "", "", err
		}
		return task, text, nil
	}
	elem, err := g.awaitElemText(id)
	if err != nil {
		return "", "", err
	}
	task, err := g.value(id)
	return task, elem, err
}

// lowerAwaitResult joins one Task and turns its outcome into a Result value.
func (g *generator) lowerAwaitResult(r willc.Record) (string, error) {
	task, elem, err := g.awaitTaskValue(refField(r, "expr"))
	if err != nil {
		return "", err
	}
	return g.lowerTaskResult(task, elem, r.ID)
}

// lowerTaskResult joins one task handle and builds Result<T, TaskError>.
func (g *generator) lowerTaskResult(task, elem string, nodeID int) (string, error) {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok {
		return "", g.errf(nodeID, "cannot translate node #%d: task value %s is not lowered yet", nodeID, elem)
	}
	info, ok := g.adtType("Result<" + elem + ",TaskError>")
	if !ok {
		return "", g.errf(nodeID, "cannot translate node #%d: Result<%s, TaskError> is not lowered", nodeID, elem)
	}
	g.needTaskDecl = true
	out := ""
	if elemLLVM != "" {
		out = g.allocSlot(g.joinOutSlots, "await.out", nodeID, elemLLVM)
	}
	errSlot := g.allocSlot(g.joinErrSlots, "await.err", nodeID, "%willrt.task_error")
	status := ""
	if g.smGeneral {
		status = g.smTryJoin(task, out, errSlot)
	} else {
		status = g.newTmp()
		if out == "" {
			g.out.WriteString("  " + status + " = call i32 @willrt_join(ptr " + task + ", ptr null, ptr " + errSlot + ")\n")
		} else {
			g.out.WriteString("  " + status + " = call i32 @willrt_join(ptr " + task + ", ptr " + out + ", ptr " + errSlot + ")\n")
		}
	}
	statusCopy := g.newTmp()
	g.out.WriteString("  " + statusCopy + " = getelementptr inbounds %willrt.task_error, ptr " + errSlot + ", i32 0, i32 1\n")
	g.out.WriteString("  store i32 " + status + ", ptr " + statusCopy + "\n")
	slot := g.allocSlot(g.adtSlots, "await.result", nodeID, info.llvm)
	resultText := "Result<" + elem + ",TaskError>"
	if ownsReleaseText(g, resultText) {
		g.addSyntheticTemp(slot, resultText, info.llvm)
	}
	okStatus := g.newTmp()
	g.out.WriteString("  " + okStatus + " = icmp eq i32 " + status + ", 0\n")
	g.labels++
	n := g.labels
	okLabel := "await.ok." + strconv.Itoa(n)
	errLabel := "await.err." + strconv.Itoa(n)
	endLabel := "await.end." + strconv.Itoa(n)
	g.out.WriteString("  br i1 " + okStatus + ", label %" + okLabel + ", label %" + errLabel + "\n")
	g.out.WriteString(okLabel + ":\n")
	tagPtr := g.newTmp()
	g.out.WriteString("  " + tagPtr + " = getelementptr inbounds " + info.llvm + ", ptr " + slot + ", i32 0, i32 0\n")
	g.out.WriteString("  store i32 " + strconv.FormatUint(uint64(info.tags["Ok"]), 10) + ", ptr " + tagPtr + "\n")
	if out != "" {
		payload := g.newTmp()
		g.out.WriteString("  " + payload + " = getelementptr inbounds " + info.llvm + ", ptr " + slot + ", i32 0, i32 " + strconv.Itoa(info.payloadIndex) + "\n")
		g.copyValue(out, payload, elemLLVM)
	}
	g.out.WriteString("  br label %" + endLabel + "\n")
	g.out.WriteString(errLabel + ":\n")
	tagPtr = g.newTmp()
	g.out.WriteString("  " + tagPtr + " = getelementptr inbounds " + info.llvm + ", ptr " + slot + ", i32 0, i32 0\n")
	g.out.WriteString("  store i32 " + strconv.FormatUint(uint64(info.tags["Err"]), 10) + ", ptr " + tagPtr + "\n")
	payload := g.newTmp()
	g.out.WriteString("  " + payload + " = getelementptr inbounds " + info.llvm + ", ptr " + slot + ", i32 0, i32 " + strconv.Itoa(info.payloadIndex) + "\n")
	g.taskErrorInto(payload, errSlot)
	g.out.WriteString("  br label %" + endLabel + "\n")
	g.out.WriteString(endLabel + ":\n")
	return slot, nil
}
