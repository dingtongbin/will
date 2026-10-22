package gen

import "github.com/will-lang/will/internal/willc"

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
// asyncSegment is one suspension in a sequence. before runs immediately before
// the operation starts; after a child call the call's own statement still runs
// so its ?/binding sees the stored result.
type asyncSegment struct {
	call        willc.Record
	kind        string
	startName   string
	pollName    string
	finishName  string
	targetSym   string
	waitUntil   bool
	statusIndex int
	resultIndex int
	before      []willc.Record
	stmt        willc.Record
}

type asyncMachineInfo struct {
	fn          willc.Record
	symbol      string
	constructor string
	frameType   string
	fields      []asyncField
	resultText  string
	resultLLVM  string
	segments    []asyncSegment
	tail        []willc.Record
}

// asyncStartName is the C entry that submits one asynchronous operation. The
// blocking form stays available for callers outside a state machine.
func asyncStartName(name string) string {
	return name + "_start"
}

func callTargetSymbolFor(d *docState, call willc.Record) string {
	if q := textField(call, "to"); q != "" {
		return q
	}
	return d.symbols[refField(call, "to")]
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
