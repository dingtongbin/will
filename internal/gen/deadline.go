package gen

import "fmt"

// deadlineReturnKind classifies the value an awaited child leaves in its
// result slot. Only a Result with an Error payload and an Option have a
// defined cancellation value; every other result keeps the panic path.
func (g *generator) deadlineReturnKind(text string) (string, bool) {
	if text == "" {
		return "void", true
	}
	kind, args, ok := parseADT(text)
	if !ok {
		return "", false
	}
	if kind == "option" {
		return "option", true
	}
	if kind == "result" && len(args) == 2 && args[1] == "Error" {
		return "result", true
	}
	return "", false
}

// deadlineJoinFailure handles a canceled or timed-out child join. It is called
// from the failure block of a join and either writes the language-level
// cancellation value and continues at okLabel, or emits the normal failure
// report.
func (g *generator) deadlineJoinFailure(status, out, text, errSlot, okLabel string, id int) error {
	kind, ok := g.deadlineReturnKind(text)
	if !ok {
		g.emitPanicFromError(errSlot)
		return nil
	}
	canceled := g.newTmp()
	timeout := g.newTmp()
	transient := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -5\n", canceled, status)
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -6\n", timeout, status)
	fmt.Fprintf(g.out, "  %s = or i1 %s, %s\n", transient, canceled, timeout)
	g.labels++
	n := g.labels
	hit := fmt.Sprintf("deadline.hit.%d", n)
	panicLabel := fmt.Sprintf("deadline.panic.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", transient, hit, panicLabel)
	fmt.Fprintf(g.out, "%s:\n", panicLabel)
	g.emitPanicFromError(errSlot)
	fmt.Fprintf(g.out, "%s:\n", hit)
	switch kind {
	case "void":
		fmt.Fprintf(g.out, "  br label %%%s\n", okLabel)
	case "option":
		if out == "" {
			return fmt.Errorf("deadline option result has no slot")
		}
		info, ok := g.adtType(text)
		if !ok || info.kind != "option" {
			return fmt.Errorf("deadline option %s has no layout", text)
		}
		tag := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, info.llvm, out)
		fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tag)
		fmt.Fprintf(g.out, "  br label %%%s\n", okLabel)
	case "result":
		if out == "" {
			return fmt.Errorf("deadline result has no slot")
		}
		g.labels++
		cn := g.labels
		canceledLabel := fmt.Sprintf("deadline.canceled.%d", cn)
		timeoutLabel := fmt.Sprintf("deadline.timeout.%d", cn)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", canceled, canceledLabel, timeoutLabel)
		fmt.Fprintf(g.out, "%s:\n", canceledLabel)
		if err := g.storeDeadlineResult(out, text, 8, id); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", okLabel)
		fmt.Fprintf(g.out, "%s:\n", timeoutLabel)
		if err := g.storeDeadlineResult(out, text, 9, id); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", okLabel)
	}
	return nil
}

// storeDeadlineResult writes Err(Canceled) or Err(Deadline) into one Result
// slot. The payload index and tag order come from the ordinary Result layout.
func (g *generator) storeDeadlineResult(out, text string, errorKind, id int) error {
	info, ok := g.adtType(text)
	if !ok || info.kind != "result" {
		return g.errf(id, "cannot translate node #%d: %s has no result layout", id, text)
	}
	tag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, info.llvm, out)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tag)
	if info.payloadIndex == 0 {
		return nil
	}
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, out, info.payloadIndex)
	g.builtinErrorInto(payload, errorKind)
	return nil
}
