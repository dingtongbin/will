package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// collectSuspendable marks the functions that cannot run as a normal call on a
// lane. A direct async C call marks its function; a call to a marked function
// marks the caller. The fixed point is needed because a wrapper can call
// another wrapper before it reaches the C shim.
func (g *generator) collectSuspendable() {
	for pass := 0; pass < 32; pass++ {
		changed := false
		for _, d := range g.docs {
			for id, sym := range d.symbols {
				if sym == "" || g.suspendable[sym] {
					continue
				}
				fn, ok := d.recs[id]
				if !ok || fn.Kind != "func" {
					continue
				}
				if simpleAsyncBody(d, id, g.suspendable) {
					g.suspendable[sym] = true
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

func hasReceiver(recs map[int]willc.Record, fn int) bool {
	for _, r := range recs {
		if r.Kind == "receiver" && refField(r, "parent") == fn {
			return true
		}
	}
	return false
}

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
	callID := 0
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
		if isAsyncCRecord(r) || callTargetsMarked(d, r, marked) {
			count++
			callID = r.ID
		}
	}
	if count != 1 {
		return false
	}
	call, ok := recs[callID]
	if !ok {
		return false
	}
	if isAsyncCRecord(call) {
		parent := recs[refField(call, "parent")]
		if (parent.Kind != "var" && parent.Kind != "const") ||
			fieldText(parent, "type") != "i32" {
			return false
		}
	}
	index := -1
	for i, st := range stmts {
		if recordUnder(recs, callID, st.ID) {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	for _, st := range stmts[:index] {
		if !preludeRecordSafe(recs, st) {
			return false
		}
	}
	for _, st := range stmts[index+1:] {
		if !supportedAsyncPostlude(st) {
			return false
		}
	}
	return true
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
	if q := textField(r, "to"); q != "" {
		return marked[q]
	}
	target := refField(r, "to")
	if target == 0 {
		return false
	}
	return marked[d.symbols[target]]
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
		return typ, false, nil
	}
	if text == "Duration" {
		return "i64", false, nil
	}
	typ, err := g.declLLVMType(p.ID)
	if err != nil || typ == "" {
		return "", false, g.errf(p.ID, "cannot translate node #%d: parameter %s is not lowered", p.ID, text)
	}
	return typ, true, nil
}
