package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// generalAsyncC starts one async C operation, parks on its poll, and returns
// its finish status. The operation handle lives in the task frame, so the
// generated continuation can poll after any other wait. A runtime-service
// operation (sleep, poller readiness) also arms its deadline on the next park.
func (g *generator) generalAsyncC(r willc.Record) (string, error) {
	name := fieldText(r, "text")
	parts, argTypes, err := g.asyncStartArgs(r)
	if err != nil {
		return "", err
	}
	startName := asyncStartName(name)
	pollName := "willrt_blocking_poll"
	finishName := "willrt_blocking_finish"
	deadlineName := ""
	if op, ok := asyncOps[name]; ok {
		pollName = op[0]
		finishName = op[1]
		deadlineName = op[2]
	}
	opSlot := g.smAlloca("ptr", "async.op")
	statusSlot := g.smAlloca("i32", "async.status")
	g.queueCDecl(startName, "ptr", argTypes)
	g.queueCDecl(pollName, "i32", []string{"ptr"})
	g.queueCDecl(finishName, "i32", []string{"ptr"})
	if deadlineName != "" {
		g.queueCDecl(deadlineName, "i64", []string{"ptr"})
		g.queueCDecl("willrt_task_wait_until", "void", []string{"i64"})
	}
	id := g.smReserve()
	handle := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @%s(%s)\n",
		handle, llvmSymbol(startName), joinParts(parts))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", handle, opSlot)
	started := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ne ptr %s, null\n", started, handle)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.async.have.%d, label %%sm.async.fail.%d\n",
		started, id, id)
	fmt.Fprintf(g.out, "sm.async.fail.%d:\n", id)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", statusSlot)
	fmt.Fprintf(g.out, "  br label %%sm.async.cont.%d\n", id)
	fmt.Fprintf(g.out, "sm.async.have.%d:\n", id)
	g.smStoreState(id)
	loaded := g.newTmp()
	fmt.Fprintf(g.out, "  %s = load ptr, ptr %s\n", loaded, opSlot)
	polled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s)\n", polled, llvmSymbol(pollName), loaded)
	pending := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", pending, polled)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.async.park.%d, label %%sm.async.done.%d\n",
		pending, id, id)
	fmt.Fprintf(g.out, "sm.async.park.%d:\n", id)
	if deadlineName != "" {
		deadline := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i64 @%s(ptr %s)\n", deadline,
			llvmSymbol(deadlineName), loaded)
		fmt.Fprintf(g.out, "  call void @willrt_task_wait_until(i64 %s)\n", deadline)
	}
	g.needIOMarkDecl = true
	g.out.WriteString("  call void @willrt_task_mark_io()\n")
	g.out.WriteString("  call void @willrt_task_mark_wait()\n")
	g.out.WriteString("  ret i32 -1\n")
	fmt.Fprintf(g.out, "sm.async.done.%d:\n", id)
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s)\n", status, llvmSymbol(finishName), loaded)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", status, statusSlot)
	fmt.Fprintf(g.out, "  br label %%sm.async.cont.%d\n", id)
	fmt.Fprintf(g.out, "sm.async.cont.%d:\n", id)
	return g.load("i32", statusSlot), nil
}

// generalCallWait starts a suspendable child and joins it from a general
// state-machine step. The child result stays in the frame; the caller sees
// either a scalar value or the result slot.
func (g *generator) generalCallWait(r willc.Record) (string, error) {
	sym := g.awaitTargetSymbol(r)
	if sym == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: the called function has no symbol", r.ID)
	}
	task, err := g.emitConstructorCall(r, sym)
	if err != nil {
		return "", err
	}
	text, err := g.callResultText(r)
	if err != nil {
		return "", err
	}
	llvm := ""
	if text != "" {
		llvm = g.resultLLVM(text)
	}
	out := ""
	if llvm != "" {
		out = g.smAlloca(llvm, "call.out")
	}
	errSlot := g.smAlloca("%willrt_error", "call.err")
	status := g.smTryJoin(task, out, errSlot)
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	g.labels++
	n := g.labels
	failLabel := fmt.Sprintf("sm.call.fail.%d", n)
	okLabel := fmt.Sprintf("sm.call.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, okLabel, failLabel)
	fmt.Fprintf(g.out, "%s:\n", failLabel)
	g.emitPanicFromError(errSlot)
	fmt.Fprintf(g.out, "%s:\n", okLabel)
	if llvm == "" {
		return "", nil
	}
	if isAggregateType(llvm) {
		return "", g.errf(r.ID, "cannot translate node #%d: a call result is a place", r.ID)
	}
	return g.load(llvm, out), nil
}

// generalCallWaitPlace is the aggregate form: the child result slot is the
// place, so no copy is needed.
func (g *generator) generalCallWaitPlace(r willc.Record) (string, string, error) {
	sym := g.awaitTargetSymbol(r)
	if sym == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the called function has no symbol", r.ID)
	}
	task, err := g.emitConstructorCall(r, sym)
	if err != nil {
		return "", "", err
	}
	text, err := g.callResultText(r)
	if err != nil {
		return "", "", err
	}
	llvm := g.resultLLVM(text)
	if llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the call result has no place", r.ID)
	}
	out := g.smAlloca(llvm, "call.out")
	errSlot := g.smAlloca("%willrt_error", "call.err")
	status := g.smTryJoin(task, out, errSlot)
	success := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", success, status)
	g.labels++
	n := g.labels
	failLabel := fmt.Sprintf("sm.call.fail.%d", n)
	okLabel := fmt.Sprintf("sm.call.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", success, okLabel, failLabel)
	fmt.Fprintf(g.out, "%s:\n", failLabel)
	g.emitPanicFromError(errSlot)
	fmt.Fprintf(g.out, "%s:\n", okLabel)
	return out, llvm, nil
}

func joinParts(parts []string) string {
	out := ""
	for i, part := range parts {
		if i != 0 {
			out += ", "
		}
		out += part
	}
	return out
}
