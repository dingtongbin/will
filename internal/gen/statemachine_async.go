package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// asyncField is one parameter or local stored in an async state-machine frame.
// The frame keeps values that must survive a suspension; the step function
// addresses them through GEPs instead of stack slots.
type asyncField struct {
	id        int
	text      string
	willText  string
	llvm      string
	aggregate bool
	param     bool
}

// asyncMachineInfo describes one function whose only wait is a direct async C
// call. The frame is state, operation handle, parameters, and locals. The
// constructor starts a task; the step starts the operation once and polls it
// after each wakeup.
type asyncMachineInfo struct {
	fn          willc.Record
	symbol      string
	constructor string
	frameType   string
	fields      []asyncField
	statusID    int
	statusIndex int
	resultIndex int
	kind        string
	targetSym   string
	prelude     []willc.Record
	postlude    []willc.Record
	call        willc.Record
	startName   string
	resultText  string
	resultLLVM  string
	callText    string
	callLLVM    string
}

// asyncStartName is the C entry that submits one asynchronous operation. The
// blocking form stays available for callers outside a state machine.
func asyncStartName(name string) string {
	return name + "_start"
}

// asyncMachine recognizes a function with one direct async C call in a top
// level declaration. The statements before it run once; the call starts a
// blocking-pool operation; the statements after it run after the operation is
// finished. A function outside this shape keeps its ordinary lowering.
func (g *generator) asyncMachine(fnID int) (*asyncMachineInfo, bool) {
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
	callID := 0
	callCount := 0
	for _, r := range g.recs {
		if !g.insideBlock(r.ID, body.ID) || r.Kind != "mcall" {
			continue
		}
		name := fieldText(r, "text")
		if !strings.HasPrefix(name, "willrt_") || !strings.Contains(name, "_async") {
			continue
		}
		callCount++
		callID = r.ID
	}
	if callCount != 1 || callID == 0 {
		return nil, false
	}
	call, ok := g.recs[callID]
	if !ok {
		return nil, false
	}
	statusID := refField(call, "parent")
	statusRec, ok := g.recs[statusID]
	if !ok || (statusRec.Kind != "var" && statusRec.Kind != "const") ||
		fieldText(statusRec, "type") != "i32" {
		return nil, false
	}
	suspendIndex := -1
	for i, st := range stmts {
		if st.ID == statusID {
			suspendIndex = i
			break
		}
	}
	if suspendIndex < 0 {
		return nil, false
	}
	prelude := stmts[:suspendIndex]
	for _, st := range prelude {
		if !preludeRecordSafe(g.recs, st) {
			return nil, false
		}
	}
	postlude := stmts[suspendIndex+1:]
	if len(postlude) == 0 {
		return nil, false
	}
	for _, st := range postlude {
		if !supportedAsyncPostlude(st) {
			return nil, false
		}
	}
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
	fields := make([]asyncField, 0, len(params)+len(locals)+1)
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
	fieldTypes := make([]string, 0, len(fields))
	for _, f := range fields {
		fieldTypes = append(fieldTypes, f.llvm)
	}
	frameType := "{ i32, ptr"
	if len(fieldTypes) != 0 {
		frameType += ", " + strings.Join(fieldTypes, ", ")
	}
	frameType += " }"
	resultText := fieldText(fn, "type")
	resultLLVM := ""
	if resultText != "" {
		resultLLVM = g.resultLLVM(resultText)
		if resultLLVM == "" {
			return nil, false
		}
	}
	statusIndex, ok := indexes[statusID]
	if !ok {
		return nil, false
	}
	return &asyncMachineInfo{
		fn:          fn,
		symbol:      qualify(g.pkg, fieldText(fn, "text")+".sm"),
		constructor: qualify(g.pkg, fieldText(fn, "text")+".task"),
		frameType:   frameType,
		fields:      fields,
		statusID:    statusID,
		statusIndex: statusIndex,
		resultIndex: -1,
		kind:        "c",
		prelude:     prelude,
		postlude:    postlude,
		call:        call,
		startName:   asyncStartName(fieldText(call, "text")),
		resultText:  resultText,
		resultLLVM:  resultLLVM,
	}, true
}

// asyncCallMachine recognizes one call to a suspendable will function. The
// call becomes a child task: the parent frame keeps the child handle and the
// child result, and the parent step parks on try-join.
func (g *generator) asyncCallMachine(fnID int) (*asyncMachineInfo, bool) {
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
	callID := 0
	count := 0
	for _, r := range g.recs {
		if !g.insideBlock(r.ID, body.ID) {
			continue
		}
		if r.Kind != "mcall" && r.Kind != "call" {
			continue
		}
		if callTargetsMarked(g.cur, r, g.suspendable) {
			count++
			callID = r.ID
		}
	}
	if count != 1 {
		return nil, false
	}
	call, ok := g.recs[callID]
	if !ok {
		return nil, false
	}
	suspendIndex := -1
	for i, st := range stmts {
		if g.insideBlock(callID, st.ID) {
			suspendIndex = i
			break
		}
	}
	if suspendIndex < 0 {
		return nil, false
	}
	prelude := stmts[:suspendIndex]
	for _, st := range prelude {
		if !preludeRecordSafe(g.recs, st) {
			return nil, false
		}
	}
	postlude := stmts[suspendIndex:]
	for _, st := range postlude {
		if !supportedAsyncPostlude(st) {
			return nil, false
		}
	}
	callText, err := g.callResultText(call)
	if err != nil || callText == "" {
		return nil, false
	}
	callLLVM := g.resultLLVM(callText)
	if callLLVM == "" {
		return nil, false
	}
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
	fields := make([]asyncField, 0, len(params)+len(locals)+2)
	if recv := receiverOfIn(g.cur, fnID); recv != nil {
		f, ok := g.asyncFieldOf(*recv, true)
		if !ok {
			return nil, false
		}
		fields = append(fields, f)
	}
	for _, p := range params {
		f, ok := g.asyncFieldOf(p, true)
		if !ok {
			return nil, false
		}
		fields = append(fields, f)
	}
	for _, l := range locals {
		f, ok := g.asyncFieldOf(l, false)
		if !ok {
			return nil, false
		}
		fields = append(fields, f)
	}
	resultIndex := len(fields)
	fields = append(fields, asyncField{
		text:      "",
		willText:  callText,
		llvm:      callLLVM,
		aggregate: isAggregateType(callLLVM),
	})
	fieldTypes := make([]string, 0, len(fields))
	for _, f := range fields {
		fieldTypes = append(fieldTypes, f.llvm)
	}
	frameType := "{ i32, ptr, " + strings.Join(fieldTypes, ", ") + " }"
	resultText := fieldText(fn, "type")
	resultLLVM := ""
	if resultText != "" {
		resultLLVM = g.resultLLVM(resultText)
		if resultLLVM == "" {
			return nil, false
		}
	}
	return &asyncMachineInfo{
		fn:          fn,
		symbol:      qualify(g.pkg, fieldText(fn, "text")+".sm"),
		constructor: qualify(g.pkg, fieldText(fn, "text")+".task"),
		frameType:   frameType,
		fields:      fields,
		statusID:    0,
		statusIndex: -1,
		resultIndex: resultIndex,
		kind:        "task",
		targetSym:   callTargetSymbolFor(g.cur, call),
		prelude:     prelude,
		postlude:    postlude,
		call:        call,
		resultText:  resultText,
		resultLLVM:  resultLLVM,
		callText:    callText,
		callLLVM:    callLLVM,
	}, true
}

func callTargetSymbolFor(d *docState, call willc.Record) string {
	if q := textField(call, "to"); q != "" {
		return q
	}
	return d.symbols[refField(call, "to")]
}

func simpleAsyncPrelude(st willc.Record) bool {
	switch st.Kind {
	case "var", "const", "assign", "exprstmt", "release":
		return true
	}
	return false
}

// preludeRecordSafe accepts control flow that finishes before the suspension.
// It rejects a return or defer because either would need a frame transition
// that this one-wait lowering does not emit.
func preludeRecordSafe(recs map[int]willc.Record, st willc.Record) bool {
	switch st.Kind {
	case "return", "defer", "deadline", "select":
		return false
	case "var", "const", "assign", "exprstmt", "release", "if", "match",
		"block", "for":
	default:
		return false
	}
	for _, r := range recs {
		if r.ID == st.ID || !recordUnder(recs, r.ID, st.ID) {
			continue
		}
		if r.Kind == "return" || r.Kind == "defer" || r.Kind == "deadline" ||
			r.Kind == "select" {
			return false
		}
	}
	return true
}

func supportedAsyncPostlude(st willc.Record) bool {
	switch st.Kind {
	case "return", "release", "assign", "exprstmt", "var", "const", "if",
		"match", "block", "panic", "assert":
		return true
	}
	return false
}

func (g *generator) asyncFieldOf(r willc.Record, param bool) (asyncField, bool) {
	llvm, err := g.declLLVMType(r.ID)
	if err != nil || llvm == "" {
		return asyncField{}, false
	}
	willText, err := g.willTypeText(r.ID)
	if err != nil {
		return asyncField{}, false
	}
	return asyncField{
		id:        r.ID,
		text:      fieldText(r, "text"),
		willText:  willText,
		llvm:      llvm,
		aggregate: isAggregateType(llvm),
		param:     param,
	}, true
}
