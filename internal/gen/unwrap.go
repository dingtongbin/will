package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// unwrapAddr lowers `expr!` and returns the address of the success payload.
// Option None uses the fixed panic; a Result error reports the message its
// Error interface returns.
func (g *generator) unwrapAddr(r willc.Record) (string, string, string, error) {
	exprID := refField(r, "expr")
	for {
		inner, ok := g.recs[exprID]
		if !ok {
			break
		}
		if inner.Kind == "await" {
			exprID = refField(inner, "expr")
			continue
		}
		if arg, ok := g.waitArg(inner); ok {
			exprID = arg
			continue
		}
		break
	}
	if inner, ok := g.recs[exprID]; ok && inner.Kind == "join" {
		return g.joinUnwrap(inner)
	}
	text, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", "", err
	}
	info, ok := g.adtType(text)
	if !ok || (info.kind != "option" && info.kind != "result") {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: ! needs an Option or Result", r.ID)
	}
	slot, typ, err := g.place(exprID)
	if err != nil {
		return "", "", "", err
	}
	if typ != info.llvm {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the operand needs %s", r.ID, info.llvm)
	}
	unit := info.payloadIndex == 0 || len(info.argLLVM) == 0 || info.argLLVM[0] == ""
	if unit {
		// The unit payload still needs the tag check above; after it there is
		// no address to return.
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	tag := g.load("i32", tagPtr)
	if info.kind == "option" {
		none := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", none, tag, info.tags["None"])
		g.panicCheck(none, panicNone, r)
	} else {
		failed := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", failed, tag, info.tags["Err"])
		g.labels++
		n := g.labels
		failLabel := fmt.Sprintf("unwrap.err.%d", n)
		okLabel := fmt.Sprintf("unwrap.ok.%d", n)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", failed, failLabel, okLabel)
		g.out.WriteString(failLabel + ":\n")
		if info.args[1] != "Error" {
			return "", "", "", g.errf(r.ID, "cannot translate node #%d: unwrapping a Result with error %s is not lowered yet", r.ID, info.args[1])
		}
		iface, ok := g.ifaceOfText("Error")
		if !ok {
			return "", "", "", g.errf(r.ID, "cannot translate node #%d: the Error interface is not lowered yet", r.ID)
		}
		errData, errTable := g.ifaceSlotValue(info, slot)
		index, ok := iface.byName["Message"]
		if !ok {
			return "", "", "", g.errf(r.ID, "cannot translate node #%d: Error has no Message method", r.ID)
		}
		method := g.ifaceMethod(errTable, index)
		message := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call { ptr, i64 } %s(ptr %s)\n", message, method, errData)
		data := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue { ptr, i64 } %s, 0\n", data, message)
		length := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue { ptr, i64 } %s, 1\n", length, message)
		g.needPanicDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 %s, ptr null, ptr null)\n", data, length)
		g.out.WriteString("  unreachable\n")
		g.out.WriteString(okLabel + ":\n")
	}
	if unit {
		return "", "", "", nil
	}
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", addr, info.llvm, slot, info.payloadIndex)
	return addr, info.argLLVM[0], info.args[0], nil
}

// ifaceSlotValue reads the data and method-table words of an interface stored
// as the second payload of a Result.
func (g *generator) ifaceSlotValue(info *adtInfo, slot string) (string, string) {
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataPtr, info.argLLVM[1], payload)
	tablePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", tablePtr, info.argLLVM[1], payload)
	data := g.load("ptr", dataPtr)
	table := g.load("ptr", tablePtr)
	return data, table
}

// ifaceMethod loads one method pointer from a method table.
func (g *generator) ifaceMethod(table string, index int) string {
	entry := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds ptr, ptr %s, i32 %d\n", entry, table, index)
	return g.load("ptr", entry)
}

// unwrapValue lowers `expr!` in a value position.
func (g *generator) unwrapValue(r willc.Record) (string, error) {
	addr, typ, _, err := g.unwrapAddr(r)
	if err != nil {
		return "", err
	}
	if typ == "" {
		return "", nil
	}
	if isAggregateType(typ) {
		return "", g.errf(r.ID, "cannot translate node #%d: the payload is an aggregate place", r.ID)
	}
	value := g.load(typ, addr)
	if _, text, err := g.unwrapType(r); err == nil && isRcText(text) {
		// The Option still owns its reference, so the copy this ! yields owns
		// one of its own.
		g.needRcDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
	}
	return value, nil
}

// unwrapPlace lowers `expr!` in a place position.
func (g *generator) unwrapPlace(r willc.Record) (string, string, error) {
	addr, typ, _, err := g.unwrapAddr(r)
	if err != nil {
		return "", "", err
	}
	if !isAggregateType(typ) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the payload is a scalar, not a place", r.ID)
	}
	return addr, typ, nil
}

// unwrapType returns the payload type of `expr!` without emitting code.
func (g *generator) unwrapType(r willc.Record) (string, string, error) {
	exprID := refField(r, "expr")
	if inner, ok := g.recs[exprID]; ok && inner.Kind == "join" {
		taskText, err := g.willTypeText(refField(inner, "task"))
		if err != nil {
			return "", "", err
		}
		elem, ok := taskElemText(taskText)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Join needs a Task", r.ID)
		}
		typ, ok := g.typeLLVMOf(elem)
		if !ok {
			return "", "", g.errf(r.ID, "cannot translate node #%d: task value %s is not lowered yet", r.ID, elem)
		}
		return typ, elem, nil
	}
	text, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", err
	}
	info, ok := g.adtType(text)
	if !ok || (info.kind != "option" && info.kind != "result") {
		return "", "", g.errf(r.ID, "cannot translate node #%d: ! needs an Option or Result", r.ID)
	}
	if len(info.argLLVM) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the Option has no payload", r.ID)
	}
	return info.argLLVM[0], info.args[0], nil
}
