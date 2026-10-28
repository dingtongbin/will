package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// asyncCallSite is one suspension call found in a function body.
type asyncCallSite struct {
	call   willc.Record
	index  int
	kind   string
	result int
}

// asyncSequenceMachine recognizes one or more suspension calls that each sit
// directly in a top-level statement. The body is split into before, wait, and
// bind segments; a call in a loop or nested branch is left to the ordinary
// lowering until that control flow is added.
func (g *generator) asyncSequenceMachine(fnID int) (*asyncMachineInfo, bool) {
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" {
		return nil, false
	}
	body, err := g.blockOf(fnID)
	if err != nil {
		return nil, false
	}
	stmts := g.children(body.ID)
	sortByID(stmts)
	var sites []asyncCallSite
	for _, r := range g.recs {
		if !g.insideBlock(r.ID, body.ID) {
			continue
		}
		if r.Kind != "mcall" && r.Kind != "call" {
			continue
		}
		kind := ""
		if isAsyncCRecord(r) {
			kind = "c"
		} else if callTargetsMarked(g.cur, r, g.suspendable) {
			kind = "task"
		}
		if kind == "" {
			continue
		}
		index := asyncTopStatement(g.recs, stmts, r.ID)
		if index < 0 || !asyncCallPathAllowed(g.recs, r.ID, stmts[index].ID) {
			return nil, false
		}
		for _, seen := range sites {
			if seen.index == index {
				return nil, false
			}
		}
		sites = append(sites, asyncCallSite{call: r, index: index, kind: kind, result: -1})
	}
	if len(sites) == 0 {
		return nil, false
	}
	sortAsyncSites(sites)
	var params []willc.Record
	var locals []willc.Record
	for _, r := range g.recs {
		if g.enclosingFunc(r.ID) != fnID {
			continue
		}
		switch r.Kind {
		case "param":
			params = append(params, r)
		case "var", "const":
			locals = append(locals, r)
		case "defer":
			return nil, false
		}
	}
	sortByID(params)
	sortByID(locals)
	fields := make([]asyncField, 0, len(params)+len(locals)+len(sites)+1)
	indexes := map[int]int{}
	if recv := receiverOfIn(g.cur, fnID); recv != nil {
		f, ok := g.asyncFieldOf(*recv, true)
		if !ok {
			return nil, false
		}
		indexes[recv.ID] = len(fields)
		fields = append(fields, f)
	}
	for _, p := range params {
		f, ok := g.asyncFieldOf(p, true)
		if !ok {
			return nil, false
		}
		indexes[p.ID] = len(fields)
		fields = append(fields, f)
	}
	for _, l := range locals {
		f, ok := g.asyncFieldOf(l, false)
		if !ok {
			return nil, false
		}
		indexes[l.ID] = len(fields)
		fields = append(fields, f)
	}
	for i := range sites {
		if sites[i].kind == "c" {
			parent := refField(sites[i].call, "parent")
			parentRec, ok := g.recs[parent]
			if !ok || (parentRec.Kind != "var" && parentRec.Kind != "const") ||
				fieldText(parentRec, "type") != "i32" {
				return nil, false
			}
			statusIndex, ok := indexes[parent]
			if !ok {
				return nil, false
			}
			sites[i].result = statusIndex
			continue
		}
		callText, err := g.callResultText(sites[i].call)
		if err != nil || callText == "" {
			return nil, false
		}
		callLLVM := g.resultLLVM(callText)
		if callLLVM == "" {
			return nil, false
		}
		sites[i].result = len(fields)
		fields = append(fields, asyncField{
			willText:  callText,
			llvm:      callLLVM,
			aggregate: isAggregateType(callLLVM),
		})
	}
	fieldTypes := make([]string, 0, len(fields))
	for _, f := range fields {
		fieldTypes = append(fieldTypes, f.llvm)
	}
	frameType := "{ i32, ptr, " + strings.Join(fieldTypes, ", ") + " }"
	gcSlots, gcTypes := g.asyncGCSlots(fields)
	resultText := fieldText(fn, "type")
	resultLLVM := ""
	if resultText != "" {
		resultLLVM = g.resultLLVM(resultText)
		if resultLLVM == "" {
			return nil, false
		}
	}
	// A method's symbol carries its receiver type, so the step and the
	// constructor hang off that symbol; a free function's symbol is the
	// qualified name already.
	sym := g.cur.funcs[fn.ID]
	if sym == "" {
		sym = qualify(g.pkg, fieldText(fn, "text"))
	}
	info := &asyncMachineInfo{
		fn:          fn,
		symbol:      sym + ".sm",
		constructor: sym + ".task",
		frameType:   frameType,
		fields:      fields,
		gcSlots:     gcSlots,
		gcTypes:     gcTypes,
		resultText:  resultText,
		resultLLVM:  resultLLVM,
	}
	prev := 0
	for i := range sites {
		site := &sites[i]
		seg := asyncSegment{
			call:        site.call,
			kind:        site.kind,
			statusIndex: site.result,
			resultIndex: site.result,
			before:      append([]willc.Record(nil), stmts[prev:site.index]...),
			stmt:        stmts[site.index],
		}
		if site.kind == "c" {
			name := fieldText(site.call, "text")
			seg.startName = asyncStartName(name)
			seg.pollName = "willrt_blocking_poll"
			seg.finishName = "willrt_blocking_finish"
			if op, ok := asyncOps[name]; ok {
				seg.pollName = op[0]
				seg.finishName = op[1]
				seg.deadline = op[2]
			}
		} else {
			seg.targetSym = callTargetSymbolFor(g.cur, site.call)
			if seg.targetSym == "" {
				return nil, false
			}
		}
		info.segments = append(info.segments, seg)
		prev = site.index + 1
	}
	info.tail = append([]willc.Record(nil), stmts[prev:]...)
	return info, true
}

func sortAsyncSites(sites []asyncCallSite) {
	for i := 1; i < len(sites); i++ {
		for j := i; j > 0 && sites[j].index < sites[j-1].index; j-- {
			sites[j], sites[j-1] = sites[j-1], sites[j]
		}
	}
}

func asyncTopStatement(recs map[int]willc.Record, stmts []willc.Record, callID int) int {
	for i, st := range stmts {
		if recordUnder(recs, callID, st.ID) {
			return i
		}
	}
	return -1
}

// asyncCallPathAllowed accepts only wrappers that can be replayed after a
// wait. A loop, branch, select, or deadline between the statement and the call
// needs control-flow states that this lowering does not generate.
func asyncCallPathAllowed(recs map[int]willc.Record, callID, topID int) bool {
	for id := refField(recs[callID], "parent"); id != 0 && id != topID; id = refField(recs[id], "parent") {
		r, ok := recs[id]
		if !ok {
			return false
		}
		switch r.Kind {
		case "try", "unwrap", "discard", "cast", "mcall", "call", "var",
			"return", "assign", "exprstmt", "release", "field", "name",
			"bin", "un", "slice", "index":
		default:
			return false
		}
	}
	return true
}
