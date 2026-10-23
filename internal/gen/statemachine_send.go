package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// sendMachineInfo describes one loop that sends a scalar index value. It is the
// send-side counterpart of the W08 receive loop: the frame keeps the state,
// channel, current index, and limit, so a full channel only parks the task.
type sendMachineInfo struct {
	fn          willc.Record
	symbol      string
	frameType   string
	chanID      int
	loID        int
	hiID        int
	indexLLVM   string
	indexSigned bool
}

// sendMachine recognizes `for i in Range(lo, hi) { _ = ch.Send(i) }` with a
// scalar element type. The loop body may not contain another wait or branch;
// the first slice only needs the producer shape of an MPMC pipeline.
func (g *generator) sendMachine(fnID int) (*sendMachineInfo, bool) {
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" || receiverOfIn(g.cur, fnID) != nil {
		return nil, false
	}
	if fieldText(fn, "type") != "" {
		return nil, false
	}
	body, err := g.blockOf(fnID)
	if err != nil {
		return nil, false
	}
	stmts := g.children(body.ID)
	sortByID(stmts)
	var loop willc.Record
	for _, st := range stmts {
		switch st.Kind {
		case "for":
			if loop.ID != 0 {
				return nil, false
			}
			loop = st
		case "return":
			if refField(st, "value") != 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	if loop.ID == 0 || refField(loop, "iter") == 0 {
		return nil, false
	}
	iter, ok := g.recs[refField(loop, "iter")]
	if !ok || iter.Kind != "range" {
		return nil, false
	}
	bodyID := refField(loop, "body")
	if bodyID == 0 {
		return nil, false
	}
	var send willc.Record
	for _, r := range g.recs {
		if !g.insideBlock(r.ID, bodyID) {
			continue
		}
		switch r.Kind {
		case "send":
			if send.ID != 0 {
				return nil, false
			}
			send = r
		case "recv", "trysend", "tryrecv", "join", "select", "defer",
			"for", "if", "match":
			return nil, false
		}
	}
	if send.ID == 0 {
		return nil, false
	}
	valueID := refField(send, "value")
	value, ok := g.recs[valueID]
	if !ok || value.Kind != "name" || refField(value, "to") != loop.ID {
		return nil, false
	}
	chanNode, ok := g.recs[refField(send, "chan")]
	if !ok || chanNode.Kind != "name" {
		return nil, false
	}
	chanText, err := g.willTypeText(chanNode.ID)
	if err != nil {
		return nil, false
	}
	elem, ok := chanElemText(chanText)
	if !ok {
		return nil, false
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" || isAggregateType(elemLLVM) {
		return nil, false
	}
	loID := refField(iter, "lo")
	hiID := refField(iter, "hi")
	loText, err := g.willTypeText(loID)
	if err != nil {
		return nil, false
	}
	hiText, err := g.willTypeText(hiID)
	if err != nil || loText != hiText {
		return nil, false
	}
	indexLLVM, ok := g.typeLLVMOf(loText)
	if !ok || indexLLVM == "" || isAggregateType(indexLLVM) ||
		indexLLVM != elemLLVM {
		return nil, false
	}
	return &sendMachineInfo{
		fn:          fn,
		symbol:      qualify(g.pkg, fieldText(fn, "text")+".sm"),
		frameType:   fmt.Sprintf("{ i32, ptr, %s, %s }", indexLLVM, indexLLVM),
		chanID:      chanNode.ID,
		loID:        loID,
		hiID:        hiID,
		indexLLVM:   indexLLVM,
		indexSigned: !isUnsigned(loText),
	}, true
}

// lowerSendMachineSpawn allocates the producer frame and queues its step. The
// channel and range bounds are evaluated in the caller; bound names that point
// at target parameters are replaced by the matching call argument.
func (g *generator) lowerSendMachineSpawn(r willc.Record, info *sendMachineInfo, call willc.Record) (string, error) {
	args := g.callArgs(call)
	lo, err := g.sendMachineBound(info, args, info.loID)
	if err != nil {
		return "", err
	}
	hi, err := g.sendMachineBound(info, args, info.hiID)
	if err != nil {
		return "", err
	}
	chanNode, ok := g.recs[info.chanID]
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: the send channel is missing", r.ID)
	}
	chanValue, err := g.sendMachineBound(info, args, chanNode.ID)
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
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", chanValue, channel)
	current := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 2\n", current, info.frameType, frame)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.indexLLVM, lo, current)
	limit := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 3\n", limit, info.frameType, frame)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.indexLLVM, hi, limit)
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 0, i32 0)\n",
		task, llvmSymbol(info.symbol), frame, size)
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", task)
	return task, nil
}

// sendMachineBound evaluates one range bound or channel in the caller. A name
// that points at a target parameter is replaced by the argument at that
// parameter's index; literals and caller-local names use the ordinary value
// lowering.
func (g *generator) sendMachineBound(info *sendMachineInfo, args []int, id int) (string, error) {
	node, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: the send bound is missing", id)
	}
	if node.Kind == "name" {
		if paramIndex := g.targetParamIndex(info.fn.ID, refField(node, "to")); paramIndex >= 0 {
			if paramIndex >= len(args) {
				return "", g.errf(id, "cannot translate node #%d: the send bound argument is missing", id)
			}
			return g.value(args[paramIndex])
		}
	}
	return g.value(id)
}

// targetParamIndex returns the source-order index of a parameter of fn.
func (g *generator) targetParamIndex(fnID, id int) int {
	var params []willc.Record
	for _, r := range g.recs {
		if r.Kind == "param" && refField(r, "parent") == fnID {
			params = append(params, r)
		}
	}
	sortByID(params)
	for i, p := range params {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// emitSendMachineStep writes the producer step: state zero enters the loop,
// state two resumes the pending send. A successful send advances the index; a
// closed, canceled, or timed-out send ends the loop without writing a result.
func (g *generator) emitSendMachineStep(info *sendMachineInfo) error {
	if g.spawnEmitted[info.symbol] {
		return nil
	}
	g.spawnEmitted[info.symbol] = true
	g.needTaskDecl = true
	g.needWaitDecl = true
	g.needChanWaitDecl = true
	fmt.Fprintf(g.out, "define i32 @%s(ptr %%frame) {\nentry:\n", llvmSymbol(info.symbol))
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 0\n", state, info.frameType)
	channel := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 1\n", channel, info.frameType)
	current := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 2\n", current, info.frameType)
	limit := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 3\n", limit, info.frameType)
	stateValue := g.load("i32", state)
	fmt.Fprintf(g.out, "  switch i32 %s, label %%send.init [ i32 2, label %%send.wait ]\n", stateValue)
	g.out.WriteString("send.init:\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", state)
	g.out.WriteString("  br label %send.loop\n")
	g.out.WriteString("send.loop:\n")
	currentValue := g.load(info.indexLLVM, current)
	limitValue := g.load(info.indexLLVM, limit)
	cond := g.newTmp()
	predicate := "slt"
	if !info.indexSigned {
		predicate = "ult"
	}
	fmt.Fprintf(g.out, "  %s = icmp %s %s %s, %s\n", cond, predicate, info.indexLLVM, currentValue, limitValue)
	g.out.WriteString("  br i1 " + cond + ", label %send.body, label %send.done\n")
	g.out.WriteString("send.body:\n")
	currentNow := g.load(info.indexLLVM, current)
	fmt.Fprintf(g.out, "  store i32 2, ptr %s\n", state)
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_chan_send_wait(ptr %s, ptr %s)\n",
		status, g.load("ptr", channel), current)
	suspended := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -1\n", suspended, status)
	g.out.WriteString("  br i1 " + suspended + ", label %send.park, label %send.after\n")
	g.out.WriteString("send.park:\n")
	g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
	g.out.WriteString("send.after:\n")
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	g.out.WriteString("  br i1 " + ok + ", label %send.next, label %send.done\n")
	g.out.WriteString("send.next:\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add %s %s, 1\n", next, info.indexLLVM, currentNow)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.indexLLVM, next, current)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", state)
	g.out.WriteString("  br label %send.loop\n")
	g.out.WriteString("send.wait:\n  br label %send.body\n")
	g.out.WriteString("send.done:\n  ret i32 1\n}\n")
	return nil
}
