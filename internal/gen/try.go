package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// tryCore lowers `expr?`. It branches on the tag of the source Option or
// Result: the error path builds the variant of the current function's result
// and returns it, and the success path continues with the payload's address.
func (g *generator) tryCore(r willc.Record) (string, string, string, error) {
	exprID := refField(r, "expr")
	srcText, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", "", err
	}
	src, ok := g.adtType(srcText)
	if !ok {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: ? needs an Option or Result", r.ID)
	}
	dst, ok := g.adtType(g.fnResultText)
	if !ok || src.kind != dst.kind {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the function result %s does not match the operand %s", r.ID, g.fnResultText, srcText)
	}
	if g.resultType != dst.llvm {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the function result is not lowered", r.ID)
	}
	srcSlot, srcType, err := g.place(exprID)
	if err != nil {
		return "", "", "", err
	}
	if srcType != src.llvm {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the operand needs %s", r.ID, src.llvm)
	}
	slot := g.trySlots[r.ID]
	if slot == "" {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: ? has no result slot", r.ID)
	}
	errName := "Err"
	if src.kind == "option" {
		errName = "None"
	}
	g.labels++
	n := g.labels
	errLabel := fmt.Sprintf("try.err.%d", n)
	okLabel := fmt.Sprintf("try.ok.%d", n)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, src.llvm, srcSlot)
	tag := g.load("i32", tagPtr)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", bad, tag, src.tags[errName])
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", bad, errLabel, okLabel)
	g.out.WriteString(errLabel + ":\n")

	dstTag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dstTag, dst.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", dst.tags[errName], dstTag)
	if dst.kind == "result" {
		if len(dst.argLLVM) < 2 || len(src.argLLVM) < 2 {
			return "", "", "", g.errf(r.ID, "cannot translate node #%d: the Result error type is not lowered", r.ID)
		}
		srcPayload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", srcPayload, src.llvm, srcSlot, src.payloadIndex)
		dstPayload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", dstPayload, dst.llvm, slot, dst.payloadIndex)
		srcErr, dstErr := src.argLLVM[1], dst.argLLVM[1]
		if srcErr != dstErr {
			return "", "", "", g.errf(r.ID, "cannot translate node #%d: the error types %s and %s differ", r.ID, src.args[1], dst.args[1])
		}
		if isAggregateType(srcErr) {
			g.copyValue(srcPayload, dstPayload, srcErr)
		} else {
			v := g.load(srcErr, srcPayload)
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", srcErr, v, dstPayload)
		}
	}
	returned := g.load(dst.llvm, slot)
	fmt.Fprintf(g.out, "  ret %s %s\n", dst.llvm, returned)
	g.out.WriteString("  unreachable\n")
	g.out.WriteString(okLabel + ":\n")

	if src.payloadIndex == 0 {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the success value has no payload", r.ID)
	}
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", addr, src.llvm, srcSlot, src.payloadIndex)
	return addr, src.argLLVM[0], src.args[0], nil
}

// tryValue lowers `expr?` in a value position.
func (g *generator) tryValue(r willc.Record) (string, error) {
	addr, typ, _, err := g.tryCore(r)
	if err != nil {
		return "", err
	}
	if typ == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: ? on a unit value is not supported yet", r.ID)
	}
	if isAggregateType(typ) {
		return "", g.errf(r.ID, "cannot translate node #%d: the success value is an aggregate place", r.ID)
	}
	return g.load(typ, addr), nil
}

// tryPlace lowers `expr?` in a place position, for an aggregate payload.
func (g *generator) tryPlace(r willc.Record) (string, string, error) {
	addr, typ, _, err := g.tryCore(r)
	if err != nil {
		return "", "", err
	}
	if !isAggregateType(typ) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the success value is a scalar, not a place", r.ID)
	}
	return addr, typ, nil
}

// trySuccessType returns the type of the value `expr?` yields, without
// emitting instructions.
func (g *generator) trySuccessType(r willc.Record) (string, string, error) {
	exprID := refField(r, "expr")
	text, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", err
	}
	info, ok := g.adtType(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: ? needs an Option or Result", r.ID)
	}
	if len(info.args) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the operand has no success type", r.ID)
	}
	return info.argLLVM[0], info.args[0], nil
}
