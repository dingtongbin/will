package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// armsOf returns the arms of a match in declaration order.
func (g *generator) armsOf(r willc.Record) []willc.Record {
	var arms []willc.Record
	for _, c := range g.recs {
		if c.Kind == "arm" && refField(c, "parent") == r.ID {
			arms = append(arms, c)
		}
	}
	sortByID(arms)
	return arms
}

// armValueExpr returns the expression that gives an arm its value. A block arm
// takes its value from its last expression statement; an arm whose body returns
// has no value.
func (g *generator) armValueExpr(arm willc.Record) (int, bool) {
	body, ok := g.recs[refField(arm, "body")]
	if !ok {
		return 0, false
	}
	switch body.Kind {
	case "exprstmt":
		return refField(body, "expr"), true
	case "block":
		stmts := g.children(body.ID)
		sortByID(stmts)
		if len(stmts) != 0 && stmts[len(stmts)-1].Kind == "exprstmt" {
			return refField(stmts[len(stmts)-1], "expr"), true
		}
	}
	return 0, false
}

// matchValueType returns the LLVM type a match used as an expression produces,
// or "" when the match is a statement or every arm returns. The type is found
// before the body is lowered, so the result slot can live in the entry block.
func (g *generator) matchValueType(r willc.Record) (string, error) {
	parent, ok := g.recs[refField(r, "parent")]
	if ok && parent.Kind == "block" {
		return "", nil
	}
	subjID := refField(r, "subject")
	subjLLVM, err := g.typeOf(subjID)
	if err != nil {
		return "", err
	}
	subjText, err := g.willTypeText(subjID)
	if err != nil {
		return "", err
	}
	var result string
	for _, arm := range g.armsOf(r) {
		llvmBinds := map[int]string{}
		textBinds := map[int]string{}
		if err := g.collectPatternTypes(refField(arm, "pattern"), subjLLVM, subjText, llvmBinds, textBinds); err != nil {
			return "", err
		}
		exprID, ok := g.armValueExpr(arm)
		if !ok {
			continue
		}
		savedLLVM, savedText := g.patternTypes, g.patternTexts
		g.patternTypes = mergeTypes(savedLLVM, llvmBinds)
		g.patternTexts = mergeTypes(savedText, textBinds)
		typ, err := g.typeOf(exprID)
		g.patternTypes, g.patternTexts = savedLLVM, savedText
		if err != nil {
			return "", err
		}
		if result == "" {
			result = typ
		} else if result != typ {
			return "", g.errf(r.ID, "cannot translate node #%d: the match arms have types %s and %s", r.ID, result, typ)
		}
	}
	return result, nil
}

// mergeTypes returns a copy of base with extra added, so a temporary lookup does
// not disturb the bindings of an enclosing match.
func mergeTypes(base, extra map[int]string) map[int]string {
	out := make(map[int]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// collectPatternTypes records the type every bind in one pattern receives. A
// top-level bind receives the subject type; a variant's child receives the
// corresponding argument type.
func (g *generator) collectPatternTypes(patID int, llvmType, text string, outLLVM, outText map[int]string) error {
	pat, ok := g.recs[patID]
	if !ok {
		return nil
	}
	switch fieldText(pat, "kind") {
	case "bind":
		outLLVM[patID] = llvmType
		outText[patID] = text
	case "wild", "literal":
		return nil
	case "variant":
		v := g.variantByID[refField(pat, "to")]
		if v == nil {
			return g.errf(patID, "cannot translate node #%d: the variant is not lowered yet", patID)
		}
		kids := g.children(patID)
		sortByID(kids)
		for i, kid := range kids {
			if i >= len(v.argLLVM) {
				return g.errf(patID, "cannot translate node #%d: the pattern binds more values than the variant carries", patID)
			}
			if err := g.collectPatternTypes(kid.ID, v.argLLVM[i], v.args[i], outLLVM, outText); err != nil {
				return err
			}
		}
	default:
		return g.errf(patID, "cannot translate node #%d: pattern kind %s is not lowered yet", patID, fieldText(pat, "kind"))
	}
	return nil
}

// matchStmt lowers a match used as a statement. It reports whether every arm
// ended in a terminator, so the enclosing block knows a following statement is
// unreachable.
func (g *generator) matchStmt(r willc.Record, void bool) (bool, error) {
	return g.lowerMatch(r, false, void)
}

// matchValue lowers a match used as an expression and loads the value its arms
// stored.
func (g *generator) matchValue(r willc.Record) (string, error) {
	slot := g.matchSlots[r.ID]
	typ := g.matchSlotTypes[r.ID]
	if slot == "" || typ == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: a match in value position must have a value on at least one arm", r.ID)
	}
	terminated, err := g.lowerMatch(r, true, false)
	if err != nil {
		return "", err
	}
	if terminated {
		return "", g.errf(r.ID, "cannot translate node #%d: every arm of the match returns", r.ID)
	}
	return g.load(typ, slot), nil
}

// lowerMatch emits the subject, the arm tests, the arm bodies, and the merge.
func (g *generator) lowerMatch(r willc.Record, value bool, void bool) (bool, error) {
	subjID := refField(r, "subject")
	subjLLVM, err := g.typeOf(subjID)
	if err != nil {
		return false, err
	}
	subjText, err := g.willTypeText(subjID)
	if err != nil {
		return false, err
	}
	subjPtr := ""
	subjVal := ""
	if isAggregateType(subjLLVM) {
		ptr, typ, err := g.place(subjID)
		if err != nil {
			return false, err
		}
		if typ != subjLLVM {
			return false, g.errf(r.ID, "cannot translate node #%d: the subject needs %s", r.ID, subjLLVM)
		}
		subjPtr = ptr
	} else {
		subjVal, err = g.value(subjID)
		if err != nil {
			return false, err
		}
	}
	g.labels++
	n := g.labels
	end := fmt.Sprintf("match.end.%d", n)
	arms := g.armsOf(r)
	allTerminated := true
	for i, arm := range arms {
		armLabel := fmt.Sprintf("match.arm.%d.%d", n, i)
		nextLabel := fmt.Sprintf("match.next.%d.%d", n, i)
		pat, ok := g.recs[refField(arm, "pattern")]
		if !ok {
			return false, g.errf(arm.ID, "cannot translate node #%d: the arm has no pattern", arm.ID)
		}
		cond, err := g.patternCond(pat, subjLLVM, subjText, subjPtr, subjVal)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, armLabel, nextLabel)
		g.out.WriteString(armLabel + ":\n")
		if err := g.bindPattern(pat, subjLLVM, subjText, subjPtr, subjVal); err != nil {
			return false, err
		}
		if guardID := refField(arm, "guard"); guardID != 0 {
			guard, err := g.value(guardID)
			if err != nil {
				return false, err
			}
			okLabel := fmt.Sprintf("match.guard.%d.%d", n, i)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", guard, okLabel, nextLabel)
			g.out.WriteString(okLabel + ":\n")
		}
		terminated, err := g.lowerArmBody(arm, value, void)
		if err != nil {
			return false, err
		}
		if !terminated {
			allTerminated = false
			if value {
				if err := g.storeMatchValue(r, arm); err != nil {
					return false, err
				}
			}
			fmt.Fprintf(g.out, "  br label %%%s\n", end)
		}
		g.out.WriteString(nextLabel + ":\n")
	}
	// A match must be exhaustive. A fallthrough therefore means the arm chain
	// had no unconditional arm, which the type checker should have rejected.
	g.out.WriteString("  unreachable\n")
	if !(allTerminated && !value) {
		g.out.WriteString(end + ":\n")
	}
	return allTerminated, nil
}

// patternCond emits the test of one pattern against the subject.
func (g *generator) patternCond(pat willc.Record, subjLLVM, subjText, subjPtr, subjVal string) (string, error) {
	switch kind := fieldText(pat, "kind"); kind {
	case "wild", "bind":
		return "true", nil
	case "literal":
		if subjVal == "" {
			return "", g.errf(pat.ID, "cannot translate node #%d: a literal pattern needs a scalar subject", pat.ID)
		}
		text := fieldText(pat, "text")
		cond := g.newTmp()
		if isFloatType(subjLLVM) {
			fmt.Fprintf(g.out, "  %s = fcmp oeq %s %s, %s\n", cond, subjLLVM, subjVal, text)
			return cond, nil
		}
		if subjLLVM == "i1" {
			fmt.Fprintf(g.out, "  %s = icmp eq i1 %s, %s\n", cond, subjVal, text)
			return cond, nil
		}
		fmt.Fprintf(g.out, "  %s = icmp eq %s %s, %s\n", cond, subjLLVM, subjVal, literalIntText(text))
		return cond, nil
	case "variant":
		info := g.enumByLLVM[subjLLVM]
		if info == nil || subjPtr == "" {
			return "", g.errf(pat.ID, "cannot translate node #%d: the subject type %s is not an enum of this document", pat.ID, subjText)
		}
		v := g.variantByID[refField(pat, "to")]
		if v == nil {
			return "", g.errf(pat.ID, "cannot translate node #%d: the variant is not lowered yet", pat.ID)
		}
		tagPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, subjPtr)
		tag := g.load("i32", tagPtr)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, v.tag)
		return cond, nil
	}
	return "", g.errf(pat.ID, "cannot translate node #%d: pattern kind %s is not lowered yet", pat.ID, fieldText(pat, "kind"))
}

// bindPattern records the value or place each name in a pattern receives.
func (g *generator) bindPattern(pat willc.Record, subjLLVM, subjText, subjPtr, subjVal string) error {
	switch fieldText(pat, "kind") {
	case "wild", "literal":
		return nil
	case "bind":
		if isAggregateType(subjLLVM) {
			g.patternPlaces[pat.ID] = subjPtr
		} else {
			g.patternVals[pat.ID] = subjVal
		}
		g.patternTypes[pat.ID] = subjLLVM
		g.patternTexts[pat.ID] = subjText
		return nil
	case "variant":
		info := g.enumByLLVM[subjLLVM]
		if info == nil || subjPtr == "" {
			return g.errf(pat.ID, "cannot translate node #%d: the subject type is not an enum of this document", pat.ID)
		}
		v := g.variantByID[refField(pat, "to")]
		if v == nil {
			return g.errf(pat.ID, "cannot translate node #%d: the variant is not lowered yet", pat.ID)
		}
		kids := g.children(pat.ID)
		sortByID(kids)
		if len(kids) == 0 {
			return nil
		}
		if info.payloadIndex == 0 || v.payload == "" {
			return g.errf(pat.ID, "cannot translate node #%d: variant %s carries no payload", pat.ID, v.name)
		}
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, subjPtr, info.payloadIndex)
		for i, kid := range kids {
			if i >= len(v.argLLVM) {
				return g.errf(pat.ID, "cannot translate node #%d: the pattern binds more values than the variant carries", pat.ID)
			}
			addr := payload
			if len(v.argLLVM) > 1 {
				field := g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, v.payload, payload, i)
				addr = field
			}
			typ := v.argLLVM[i]
			val := ""
			if !isAggregateType(typ) {
				val = g.load(typ, addr)
			}
			if err := g.bindPattern(kid, typ, v.args[i], addr, val); err != nil {
				return err
			}
		}
		return nil
	}
	return g.errf(pat.ID, "cannot translate node #%d: pattern kind %s is not lowered yet", pat.ID, fieldText(pat, "kind"))
}

// lowerArmBody lowers the body of one arm and reports whether it ended in a
// terminator.
func (g *generator) lowerArmBody(arm willc.Record, value bool, void bool) (bool, error) {
	body, ok := g.recs[refField(arm, "body")]
	if !ok {
		return false, g.errf(arm.ID, "cannot translate node #%d: the arm has no body", arm.ID)
	}
	if !value {
		return g.stmt(body, void)
	}
	switch body.Kind {
	case "exprstmt":
		return false, nil
	case "block":
		stmts := g.children(body.ID)
		sortByID(stmts)
		if len(stmts) == 0 {
			return false, g.errf(body.ID, "cannot translate node #%d: an arm with a value has an empty block", body.ID)
		}
		for _, st := range stmts[:len(stmts)-1] {
			done, err := g.stmt(st, void)
			if err != nil {
				return false, err
			}
			if done {
				return true, nil
			}
		}
		last := stmts[len(stmts)-1]
		if last.Kind != "exprstmt" {
			return false, g.errf(last.ID, "cannot translate node #%d: the last statement of an arm block is not an expression", last.ID)
		}
		return false, nil
	}
	return g.stmt(body, void)
}

// storeMatchValue stores one arm's value in the match result slot.
func (g *generator) storeMatchValue(r willc.Record, arm willc.Record) error {
	slot := g.matchSlots[r.ID]
	typ := g.matchSlotTypes[r.ID]
	if slot == "" || typ == "" {
		return g.errf(r.ID, "cannot translate node #%d: the match has no result slot", r.ID)
	}
	exprID, ok := g.armValueExpr(arm)
	if !ok {
		return g.errf(arm.ID, "cannot translate node #%d: the arm has no value", arm.ID)
	}
	if isAggregateType(typ) {
		src, srcType, err := g.place(exprID)
		if err != nil {
			return err
		}
		if srcType != typ {
			return g.errf(exprID, "cannot translate node #%d: the arm value needs %s", exprID, typ)
		}
		g.copyValue(src, slot, typ)
		return nil
	}
	v, err := g.value(exprID)
	if err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, v, slot)
	return nil
}
