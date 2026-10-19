package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// unwrapAddr lowers `expr!` on an Option and returns the address of the Some
// payload. None panics with the fixed message; Result unwrapping waits for the
// error-message path.
func (g *generator) unwrapAddr(r willc.Record) (string, string, string, error) {
	exprID := refField(r, "expr")
	text, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", "", err
	}
	info, ok := g.adtType(text)
	if !ok || info.kind != "option" {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: only Option unwrapping is lowered yet", r.ID)
	}
	slot, typ, err := g.place(exprID)
	if err != nil {
		return "", "", "", err
	}
	if typ != info.llvm {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the operand needs %s", r.ID, info.llvm)
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	tag := g.load("i32", tagPtr)
	none := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", none, tag, info.tags["None"])
	g.panicCheck(none, panicNone, r)
	if info.payloadIndex == 0 || len(info.argLLVM) == 0 {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: the Option has no payload", r.ID)
	}
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", addr, info.llvm, slot, info.payloadIndex)
	return addr, info.argLLVM[0], info.args[0], nil
}

// unwrapValue lowers `expr!` in a value position.
func (g *generator) unwrapValue(r willc.Record) (string, error) {
	addr, typ, _, err := g.unwrapAddr(r)
	if err != nil {
		return "", err
	}
	if typ == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: unwrapping a unit payload is not supported yet", r.ID)
	}
	if isAggregateType(typ) {
		return "", g.errf(r.ID, "cannot translate node #%d: the payload is an aggregate place", r.ID)
	}
	return g.load(typ, addr), nil
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
	text, err := g.willTypeText(refField(r, "expr"))
	if err != nil {
		return "", "", err
	}
	info, ok := g.adtType(text)
	if !ok || info.kind != "option" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: only Option unwrapping is lowered yet", r.ID)
	}
	if len(info.argLLVM) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the Option has no payload", r.ID)
	}
	return info.argLLVM[0], info.args[0], nil
}
