package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// simpleAsyncBody reports whether a function has one supported suspension site
// and can be split into a prelude and a postlude. The check is deliberately
// structural; the lowering repeats it with the exact types in hand.
func simpleAsyncBody(d *docState, fn int, marked map[string]bool) bool {
	recs := d.recs
	body := 0
	for _, r := range recs {
		if r.Kind == "block" && refField(r, "parent") == fn {
			body = r.ID
			break
		}
	}
	if body == 0 {
		return false
	}
	stmts := childrenOf(recs, body)
	seen := map[int]bool{}
	count := 0
	for _, r := range recs {
		if !recordUnder(recs, r.ID, body) {
			continue
		}
		if r.Kind == "defer" {
			return false
		}
		if r.Kind != "mcall" && r.Kind != "call" {
			continue
		}
		if !isAsyncCRecord(r) && !callTargetsMarked(d, r, marked) {
			continue
		}
		count++
		call := r
		if isAsyncCRecord(call) {
			parent := recs[refField(call, "parent")]
			if (parent.Kind != "var" && parent.Kind != "const") ||
				fieldText(parent, "type") != "i32" {
				return false
			}
		}
		index := asyncTopStatement(recs, stmts, call.ID)
		if index < 0 || seen[index] || !asyncCallPathAllowed(recs, call.ID, stmts[index].ID) {
			return false
		}
		seen[index] = true
	}
	return count != 0
}

func childrenOf(recs map[int]willc.Record, parent int) []willc.Record {
	var out []willc.Record
	for _, r := range recs {
		if refField(r, "parent") == parent {
			out = append(out, r)
		}
	}
	sortByID(out)
	return out
}

func recordUnder(recs map[int]willc.Record, id, parent int) bool {
	for id != 0 {
		if id == parent {
			return true
		}
		r, ok := recs[id]
		if !ok {
			return false
		}
		id = refField(r, "parent")
	}
	return false
}

func isAsyncCRecord(r willc.Record) bool {
	if r.Kind != "mcall" {
		return false
	}
	name := fieldText(r, "text")
	return strings.HasPrefix(name, "willrt_") && strings.Contains(name, "_async")
}

func callTargetsMarked(d *docState, r willc.Record, marked map[string]bool) bool {
	sym := ""
	if q := textField(r, "to"); q != "" {
		sym = q
	} else if target := refField(r, "to"); target != 0 {
		sym = d.symbols[target]
	}
	if sym == "" {
		return false
	}
	if marked[sym] {
		return true
	}
	base := stripTypeArgs(sym)
	return base != sym && marked[base]
}

// asyncCallRecs lists the hidden receiver first, then the declared parameters
// in source order. The constructor and the step share this order.
func (g *generator) asyncCallRecs(fnID int) []willc.Record {
	var out []willc.Record
	if recv := receiverOfIn(g.cur, fnID); recv != nil {
		out = append(out, *recv)
	}
	var params []willc.Record
	for _, r := range g.recs {
		if r.Kind == "param" && refField(r, "parent") == fnID {
			params = append(params, r)
		}
	}
	sortByID(params)
	return append(out, params...)
}

func (g *generator) asyncParamABI(p willc.Record) (string, bool, error) {
	text := fieldText(p, "type")
	if fieldText(p, "variadic") == "true" {
		return "", false, g.errf(p.ID, "cannot translate node #%d: a state machine takes no variadic parameter", p.ID)
	}
	if isSingleBorrow(text) {
		return "ptr", true, nil
	}
	if base, kind := borrowText(text); kind == "mutview" {
		text = base
	}
	if isRcText(text) || isWeakText(text) {
		return "i64", false, nil
	}
	if _, ok := taskElemText(text); ok {
		return "ptr", false, nil
	}
	if _, ok := chanElemText(text); ok {
		return "ptr", false, nil
	}
	if typ, _, ok := llvmScalar(text); ok {
		if isMutText(fieldText(p, "type")) {
			return "ptr", true, nil
		}
		return typ, false, nil
	}
	if text == "Duration" {
		if isMutText(fieldText(p, "type")) {
			return "ptr", true, nil
		}
		return "i64", false, nil
	}
	typ, err := g.declLLVMType(p.ID)
	if err != nil || typ == "" {
		return "", false, g.errf(p.ID, "cannot translate node #%d: parameter %s is not lowered", p.ID, text)
	}
	if isMutText(fieldText(p, "type")) {
		// A mutable aggregate is a pointer into the caller's place, so the
		// frame reserves one pointer, not a second copy of the aggregate.
		return "ptr", true, nil
	}
	return typ, true, nil
}
