package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// adtMethod lowers the Option and Result helpers that read a tag or choose
// between a payload and a default. It returns a scalar value or a loaded
// aggregate, the same shape as a built-in method.
func (g *generator) adtMethod(r willc.Record, kind string, args []string) (string, string, error) {
	recvID := refField(r, "recv")
	base, typ, err := g.place(recvID)
	if err != nil {
		return "", "", err
	}
	info, ok := g.adtType(kindTypeText(kind, args))
	if !ok || info.llvm == "" || typ != info.llvm {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not %s", r.ID, kindTypeText(kind, args))
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, base)
	tag := g.load("i32", tagPtr)
	name := fieldText(r, "text")
	presentTag := 1
	if kind == "result" {
		presentTag = 0
	}
	switch name {
	case "is_some", "is_none", "is_ok", "is_err":
		if len(g.callArgs(r)) != 0 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s takes no arguments", r.ID, name)
		}
		want := presentTag
		if name == "is_none" || name == "is_err" {
			want = 1 - presentTag
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", out, tag, want)
		return out, "bool", nil
	case "Or":
		if len(g.callArgs(r)) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Or takes one default", r.ID)
		}
		return g.adtOr(r, base, info, tag, presentTag, args[0], g.callArgs(r)[0])
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: %s.%s is not lowered yet", r.ID, kind, name)
}

// adtOr chooses the payload when present or the default otherwise. Scalars use
// select; aggregates copy into one result slot on each branch.
func (g *generator) adtOr(r willc.Record, base string, info *adtInfo, tag string, presentTag int, valueText string, defaultID int) (string, string, error) {
	valueLLVM, ok := g.typeLLVMOf(valueText)
	if !ok || valueLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, valueText)
	}
	present := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", present, tag, presentTag)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, base, info.payloadIndex)
	if !isAggregateType(valueLLVM) {
		value := g.load(valueLLVM, payload)
		defaultValue, err := g.value(defaultID)
		if err != nil {
			return "", "", err
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = select i1 %s, %s %s, %s %s\n", out, present, valueLLVM, value, valueLLVM, defaultValue)
		return out, valueText, nil
	}
	slot := g.callSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.callSlots, "adt", r.ID, valueLLVM)
	}
	g.labels++
	n := g.labels
	yes := fmt.Sprintf("adt.yes.%d", n)
	no := fmt.Sprintf("adt.no.%d", n)
	done := fmt.Sprintf("adt.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", present, yes, no)
	g.out.WriteString(yes + ":\n")
	g.copyValue(payload, slot, valueLLVM)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(no + ":\n")
	def, defType, err := g.place(defaultID)
	if err != nil {
		return "", "", err
	}
	if defType != valueLLVM {
		return "", "", g.errf(defaultID, "cannot translate node #%d: the default needs %s", defaultID, valueLLVM)
	}
	g.copyValue(def, slot, valueLLVM)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return g.load(valueLLVM, slot), valueText, nil
}
