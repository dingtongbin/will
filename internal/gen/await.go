package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// awaitCallJoin starts a waiting function as a child task and waits for it
// with the runtime join. An ordinary function has no frame to suspend in, so
// the wait blocks the calling thread; a state machine waits in its frame
// instead and never reaches this path.
func (g *generator) awaitCallJoin(r willc.Record) (string, string, string, string, error) {
	sym := g.awaitTargetSymbol(r)
	if sym == "" {
		return "", "", "", "", g.errf(r.ID, "cannot translate node #%d: the called function has no symbol", r.ID)
	}
	task, err := g.emitConstructorCall(r, sym, "-2", false)
	if err != nil {
		return "", "", "", "", err
	}
	text, err := g.callResultText(r)
	if err != nil {
		return "", "", "", "", err
	}
	llvm := ""
	if text != "" {
		llvm = g.resultLLVM(text)
	}
	out := ""
	if llvm != "" {
		out = g.allocSlot(g.awaitOutSlots, "await.out", r.ID, llvm)
		if ownsReleaseText(g, text) {
			g.addSyntheticTemp(out, text, llvm)
		}
	}
	errSlot := g.allocSlot(g.awaitErrSlots, "await.err", r.ID, "%willrt_error")
	g.needTaskDecl = true
	status := g.newTmp()
	if out == "" {
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr null, ptr %s)\n", status, task, errSlot)
	} else {
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr %s, ptr %s)\n", status, task, out, errSlot)
	}
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	g.labels++
	n := g.labels
	failLabel := fmt.Sprintf("await.fail.%d", n)
	okLabel := fmt.Sprintf("await.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, okLabel, failLabel)
	fmt.Fprintf(g.out, "%s:\n", failLabel)
	g.emitPanicFromError(errSlot)
	fmt.Fprintf(g.out, "%s:\n", okLabel)
	return out, llvm, errSlot, text, nil
}

// lowerAwaitedTask joins a task that was just constructed from an interface
// constructor. A direct async call returns T directly, so a task failure panics
// at the call site instead of becoming a Result value.
func (g *generator) lowerAwaitedTask(task, text string, nodeID int) (string, string, error) {
	llvm := ""
	if text != "" && text != "()" {
		llvm = g.resultLLVM(text)
	}
	out := ""
	if llvm != "" {
		out = g.allocSlot(g.awaitOutSlots, "await.out", nodeID, llvm)
		if ownsReleaseText(g, text) {
			g.addSyntheticTemp(out, text, llvm)
		}
	}
	errSlot := g.allocSlot(g.awaitErrSlots, "await.err", nodeID, "%willrt_error")
	g.needTaskDecl = true
	status := g.newTmp()
	if out == "" {
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr null, ptr %s)\n", status, task, errSlot)
	} else {
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr %s, ptr %s)\n", status, task, out, errSlot)
	}
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	g.labels++
	n := g.labels
	failLabel := fmt.Sprintf("await.fail.%d", n)
	okLabel := fmt.Sprintf("await.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, okLabel, failLabel)
	fmt.Fprintf(g.out, "%s:\n", failLabel)
	g.emitPanicFromError(errSlot)
	fmt.Fprintf(g.out, "%s:\n", okLabel)
	return out, llvm, nil
}

// awaitCallValue is the value form of an awaited call.
func (g *generator) awaitCallValue(r willc.Record) (string, string, error) {
	out, llvm, _, text, err := g.awaitCallJoin(r)
	if err != nil {
		return "", "", err
	}
	if llvm == "" {
		return "", text, nil
	}
	if isAggregateType(llvm) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: a call result is a place", r.ID)
	}
	return g.load(llvm, out), text, nil
}

// awaitCallPlace is the aggregate form of an awaited call. The child result
// slot is the place, so no copy is needed.
func (g *generator) awaitCallPlace(r willc.Record) (string, string, error) {
	out, llvm, _, _, err := g.awaitCallJoin(r)
	if err != nil {
		return "", "", err
	}
	if llvm == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the call result has no place", r.ID)
	}
	return out, llvm, nil
}
