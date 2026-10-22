package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// stateMachineInfo is the narrow loop + receive shape W08 lowers without a C
// stack. The frame's first field is the u32 state; the remaining fields are the
// live channel, the accumulator, and the last received payload.
type stateMachineInfo struct {
	fn         willc.Record
	symbol     string
	frameType  string
	resultText string
	resultLLVM string
	chanID     int
	sumID      int
	sumType    string
	sumLLVM    string
	elemType   string
	elemLLVM   string
}

// stateMachine recognizes one infinite loop whose only suspension is Recv and
// whose final return is the accumulator. A function outside this shape keeps
// the ordinary lowering; W08 reports unsupported shapes at the call site.
func (g *generator) stateMachine(fnID int) (*stateMachineInfo, bool) {
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" {
		return nil, false
	}
	if receiverOfIn(g.cur, fn.ID) != nil {
		return nil, false
	}
	bodyID := 0
	for _, r := range g.recs {
		if r.Kind == "block" && refField(r, "parent") == fn.ID {
			bodyID = r.ID
			break
		}
	}
	if bodyID == 0 {
		return nil, false
	}
	var loop willc.Record
	var returnRec willc.Record
	sumID := 0
	sumType := ""
	for _, r := range g.recs {
		if refField(r, "parent") != bodyID {
			continue
		}
		switch r.Kind {
		case "for":
			if loop.ID != 0 {
				return nil, false
			}
			loop = r
		case "return":
			returnRec = r
		case "var":
			if strings.HasPrefix(fieldText(r, "type"), "Option<") {
				continue
			}
			if sumID != 0 {
				return nil, false
			}
			sumID = r.ID
			sumType = fieldText(r, "type")
		}
	}
	if loop.ID == 0 || returnRec.ID == 0 || sumID == 0 {
		return nil, false
	}
	if refField(loop, "iter") != 0 {
		return nil, false
	}
	var channel willc.Record
	var recv willc.Record
	for _, r := range g.recs {
		if r.Kind == "recv" && g.insideBlock(r.ID, refField(loop, "body")) {
			if recv.ID != 0 {
				return nil, false
			}
			recv = r
			chanID := refField(r, "chan")
			ch, ok := g.recs[chanID]
			if !ok || ch.Kind != "name" {
				return nil, false
			}
			channel = ch
		}
	}
	if recv.ID == 0 {
		return nil, false
	}
	returnType, err := g.willTypeText(refField(returnRec, "value"))
	if err != nil || returnType != sumType {
		return nil, false
	}
	chanText, err := g.willTypeText(channel.ID)
	if err != nil {
		return nil, false
	}
	elem, ok := chanElemText(chanText)
	if !ok {
		return nil, false
	}
	sumLLVM, ok := g.typeLLVMOf(sumType)
	if !ok || (sumLLVM != "i32" && sumLLVM != "i64") {
		return nil, false
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" || isAggregateType(elemLLVM) {
		return nil, false
	}
	resultText := fieldText(fn, "type")
	resultLLVM := g.resultLLVM(resultText)
	if resultLLVM == "" {
		return nil, false
	}
	return &stateMachineInfo{
		fn:         fn,
		symbol:     qualify(g.pkg, fieldText(fn, "text")+".sm"),
		frameType:  fmt.Sprintf("{ i32, ptr, %s, %s }", sumLLVM, elemLLVM),
		resultText: resultText,
		resultLLVM: resultLLVM,
		chanID:     channel.ID,
		sumID:      sumID,
		sumType:    sumType,
		sumLLVM:    sumLLVM,
		elemType:   elem,
		elemLLVM:   elemLLVM,
	}, true
}

func (g *generator) insideBlock(id, block int) bool {
	for id != 0 {
		if id == block {
			return true
		}
		r, ok := g.recs[id]
		if !ok {
			return false
		}
		id = refField(r, "parent")
	}
	return false
}

// lowerStateMachineSpawn allocates the state-machine frame and queues the
// generated step. The caller has already checked that the target is the W08
// shape and that there is exactly one channel argument.
func (g *generator) lowerStateMachineSpawn(r willc.Record, info *stateMachineInfo, call willc.Record) (string, error) {
	args := g.callArgs(call)
	if len(args) != 1 {
		return "", g.errf(r.ID, "cannot translate node #%d: the state machine takes one channel", r.ID)
	}
	chanPtr, err := g.value(args[0])
	if err != nil {
		return "", err
	}
	size := g.sizeOf(info.frameType)
	g.needAllocDecl = true
	g.needTaskDecl = true
	frame := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", frame, size)
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 0\n", state, info.frameType, frame)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", state)
	channel := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 1\n", channel, info.frameType, frame)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", chanPtr, channel)
	sum := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 2\n", sum, info.frameType, frame)
	fmt.Fprintf(g.out, "  store %s 0, ptr %s\n", info.sumLLVM, sum)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 3\n", payload, info.frameType, frame)
	fmt.Fprintf(g.out, "  store %s 0, ptr %s\n", info.elemLLVM, payload)
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 0, i32 0)\n",
		task, llvmSymbol(info.symbol), frame, size)
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", task)
	return task, nil
}

// emitStateMachineStep writes the generated step for the W08 shape.
func (g *generator) emitStateMachineStep(info *stateMachineInfo) error {
	if g.spawnEmitted[info.symbol] {
		return nil
	}
	g.spawnEmitted[info.symbol] = true
	g.needWaitDecl = true
	recvLabel := "sm.recv"
	fmt.Fprintf(g.out, "define i32 @%s(ptr %%frame) {\nentry:\n", llvmSymbol(info.symbol))
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 0\n", state, info.frameType)
	sum := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 2\n", sum, info.frameType)
	channel := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 1\n", channel, info.frameType)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 3\n", payload, info.frameType)
	current := g.load("i32", state)
	fmt.Fprintf(g.out, "  switch i32 %s, label %%sm.init [ i32 1, label %%%s ]\n", current, recvLabel)
	g.out.WriteString("sm.init:\n")
	fmt.Fprintf(g.out, "  store %s 0, ptr %s\n", info.sumLLVM, sum)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", state)
	fmt.Fprintf(g.out, "  br label %%%s\n", recvLabel)
	g.out.WriteString(recvLabel + ":\n")
	ch := g.load("ptr", channel)
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_chan_try_recv(ptr %s, ptr %s)\n", status, ch, payload)
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	g.labels++
	n := g.labels
	some := fmt.Sprintf("sm.some.%d", n)
	done := fmt.Sprintf("sm.done.%d", n)
	park := fmt.Sprintf("sm.park.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, some, done)
	g.out.WriteString(some + ":\n")
	value := g.load(info.elemLLVM, payload)
	sumValue := g.load(info.sumLLVM, sum)
	added := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add %s %s, %s\n", added, info.sumLLVM, sumValue, value)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.sumLLVM, added, sum)
	fmt.Fprintf(g.out, "  br label %%%s\n", recvLabel)
	g.out.WriteString(done + ":\n")
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -2\n", empty, status)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, park, "sm.finish")
	g.out.WriteString(park + ":\n")
	g.out.WriteString("  call void @willrt_task_mark_wait()\n")
	g.out.WriteString("  ret i32 -1\n")
	g.out.WriteString("sm.finish:\n")
	fmt.Fprintf(g.out, "  call i32 @willrt_task_store_result(ptr %s, i64 %s)\n",
		sum, g.sizeOf(info.sumLLVM))
	g.out.WriteString("  ret i32 1\n}\n")
	return nil
}
