package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// spawnRequest is one step function the document still has to write. The step
// evaluates the spawned call on the task thread and stores its result.
type spawnRequest struct {
	node int
	call int
	sym  string
}

// taskElemText returns the value type of Task<T>.
func taskElemText(text string) (string, bool) {
	if strings.HasPrefix(text, "Task<") && strings.HasSuffix(text, ">") {
		return strings.TrimSpace(text[len("Task<") : len(text)-1]), true
	}
	return "", false
}

// chanElemText returns the element type of chan<T>.
func chanElemText(text string) (string, bool) {
	if strings.HasPrefix(text, "chan<") && strings.HasSuffix(text, ">") {
		return strings.TrimSpace(text[len("chan<") : len(text)-1]), true
	}
	return "", false
}

// spawnStepSymbol names one spawn step in the current document.
func (g *generator) spawnStepSymbol(node int) string {
	if name := g.spawnNames[node]; name != "" {
		return name
	}
	g.spawnSeq++
	name := qualify(g.pkg, "spawn."+strconv.Itoa(g.spawnSeq))
	g.spawnNames[node] = name
	return name
}

// lowerSpawn evaluates the arguments in the parent, copies them into a heap
// frame, and starts one task. The args are captured by value, matching the move
// rule the source checker enforces for spawn.
func (g *generator) lowerSpawn(r willc.Record) (string, error) {
	laneValue := ""
	if lane := fieldText(r, "lane"); lane != "" {
		index, ok := g.laneIndex[qualify(g.pkg, lane)]
		if !ok {
			return "", g.errf(r.ID, "cannot translate node #%d: lane %s is not declared", r.ID, lane)
		}
		laneValue = strconv.Itoa(int(index))
	}
	callID := refField(r, "call")
	call, ok := g.recs[callID]
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: the spawned call is missing", r.ID)
	}
	if call.Kind != "call" {
		return "", g.errf(r.ID, "cannot translate node #%d: spawn takes a direct call", r.ID)
	}
	sym := g.callTargetSymbol(call)
	if sym == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: the spawned call has no function", r.ID)
	}
	args := g.callArgs(call)
	var frameTypes []string
	var argTypes []string
	for _, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return "", err
		}
		argTypes = append(argTypes, typ)
		frameTypes = append(frameTypes, typ)
	}
	frameType := "{ " + strings.Join(frameTypes, ", ") + " }"
	if len(frameTypes) == 0 {
		frameType = "{}"
	}
	size := g.sizeOf(frameType)
	g.needAllocDecl = true
	g.needTaskDecl = true
	frame := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", frame, size)
	for i, a := range args {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 %d\n", field, frameType, frame, i)
		if isAggregateType(argTypes[i]) {
			src, _, err := g.place(a)
			if err != nil {
				return "", err
			}
			value := g.load(argTypes[i], src)
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", argTypes[i], value, field)
			continue
		}
		value, err := g.value(a)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", argTypes[i], value, field)
	}
	step := g.spawnStepSymbol(r.ID)
	if !g.spawnEmitted[step] {
		g.spawnEmitted[step] = true
		g.pendingSpawns = append(g.pendingSpawns, spawnRequest{node: r.ID, call: callID, sym: step})
	}
	lane := laneValue
	if lane == "" {
		here := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_lane_here()\n", here)
		lane = here
	}
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 %s, i32 0)\n",
		task, llvmSymbol(step), frame, size, lane)
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", task)
	return task, nil
}

// callTargetSymbol returns the LLVM symbol a call names, whether it is a local
// function or a qualified function of another package.
func (g *generator) callTargetSymbol(call willc.Record) string {
	if qualified := textField(call, "to"); qualified != "" {
		return qualified
	}
	target := refField(call, "to")
	return g.cur.funcs[target]
}

// emitSpawns writes every step function queued by this document.
func (g *generator) emitSpawns() error {
	for _, req := range g.pendingSpawns {
		if err := g.emitSpawnStep(req); err != nil {
			return err
		}
	}
	g.pendingSpawns = nil
	return nil
}

// emitSpawnStep writes one step: load the frame fields, call the target, store
// the result on the task, and return WILLRT_DONE.
func (g *generator) emitSpawnStep(req spawnRequest) error {
	call, ok := g.recs[req.call]
	if !ok {
		return g.errf(req.node, "cannot translate node #%d: the spawned call is missing", req.node)
	}
	sym := g.callTargetSymbol(call)
	if sym == "" {
		return g.errf(req.node, "cannot translate node #%d: the spawned call has no function", req.node)
	}
	args := g.callArgs(call)
	var frameTypes []string
	for _, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return err
		}
		frameTypes = append(frameTypes, typ)
	}
	frameType := "{ " + strings.Join(frameTypes, ", ") + " }"
	if len(frameTypes) == 0 {
		frameType = "{}"
	}
	result, err := g.callResultText(call)
	if err != nil {
		return err
	}
	params := g.paramTypes[sym]
	if len(params) != len(args) {
		// A local function has its parameter list in the document; use it
		// when the module index did not record the symbol.
		params = g.localParamTypes(call)
	}
	fmt.Fprintf(g.out, "define i32 @%s(ptr %%frame) {\nentry:\n", llvmSymbol(req.sym))
	var callArgs []string
	for i := range args {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n", field, frameType, i)
		if isAggregateType(frameTypes[i]) {
			callArgs = append(callArgs, "ptr "+field)
			continue
		}
		value := g.load(frameTypes[i], field)
		callArgs = append(callArgs, frameTypes[i]+" "+value)
	}
	retLLVM := ""
	if result != "" {
		retLLVM = g.resultLLVM(result)
		if retLLVM == "" {
			return g.errf(req.node, "cannot translate node #%d: result type %s is not lowered yet", req.node, result)
		}
	}
	if retLLVM == "" {
		fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(sym), strings.Join(callArgs, ", "))
		g.out.WriteString("  ret i32 1\n}\n")
		return nil
	}
	value := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s @%s(%s)\n", value, retLLVM, llvmSymbol(sym), strings.Join(callArgs, ", "))
	slot := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, retLLVM)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", retLLVM, value, slot)
	size := g.sizeOf(retLLVM)
	g.needTaskDecl = true
	fmt.Fprintf(g.out, "  call i32 @willrt_task_store_result(ptr %s, i64 %s)\n", slot, size)
	g.out.WriteString("  ret i32 1\n}\n")
	return nil
}

// localParamTypes returns the parameter type texts of the call's local target.
func (g *generator) localParamTypes(call willc.Record) []string {
	target := refField(call, "to")
	var params []willc.Record
	for _, r := range g.recs {
		if r.Kind == "param" && refField(r, "parent") == target {
			params = append(params, r)
		}
	}
	sortByID(params)
	out := make([]string, 0, len(params))
	for _, p := range params {
		out = append(out, fieldText(p, "type"))
	}
	return out
}

// sizeOf emits the byte size of an LLVM type as a runtime integer.
func (g *generator) sizeOf(typ string) string {
	end := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr null, i32 1\n", end, typ)
	size := g.newTmp()
	fmt.Fprintf(g.out, "  %s = ptrtoint ptr %s to i64\n", size, end)
	return size
}

// joinUnwrap lowers task.Join()! directly: it waits, and a failed task reports
// the stored message and ends the task at this boundary. The address it returns
// is the value the caller wants.
func (g *generator) joinUnwrap(r willc.Record) (string, string, string, error) {
	taskID := refField(r, "task")
	taskText, err := g.willTypeText(taskID)
	if err != nil {
		return "", "", "", err
	}
	elem, ok := taskElemText(taskText)
	if !ok {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: Join needs a Task", r.ID)
	}
	if elem == "" || elem == "()" {
		task, err := g.value(taskID)
		if err != nil {
			return "", "", "", err
		}
		g.needTaskDecl = true
		errSlot := g.allocSlot(g.joinErrSlots, "join.err", r.ID, "%willrt_error")
		g.needPanicDecl = true
		status := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr null, ptr %s)\n", status, task, errSlot)
		isOK := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", isOK, status)
		g.labels++
		n := g.labels
		okLabel := fmt.Sprintf("task.ok.%d", n)
		failLabel := fmt.Sprintf("task.fail.%d", n)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isOK, okLabel, failLabel)
		g.out.WriteString(failLabel + ":\n")
		message := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 0\n", message, errSlot)
		msg := g.load("ptr", message)
		sizePtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 1\n", sizePtr, errSlot)
		size := g.load("i64", sizePtr)
		sitePtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 2\n", sitePtr, errSlot)
		site := g.load("ptr", sitePtr)
		symbolPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 3\n", symbolPtr, errSlot)
		symbol := g.load("ptr", symbolPtr)
		fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 %s, ptr %s, ptr %s)\n", msg, size, site, symbol)
		g.out.WriteString("  unreachable\n")
		fmt.Fprintf(g.out, "%s:\n", okLabel)
		return "", "", "()", nil
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok {
		return "", "", "", g.errf(r.ID, "cannot translate node #%d: task value %s is not lowered yet", r.ID, elem)
	}
	task, err := g.value(taskID)
	if err != nil {
		return "", "", "", err
	}
	g.needTaskDecl = true
	out := g.allocSlot(g.joinOutSlots, "join.out", r.ID, elemLLVM)
	errSlot := g.allocSlot(g.joinErrSlots, "join.err", r.ID, "%willrt_error")
	g.needPanicDecl = true
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_join(ptr %s, ptr %s, ptr %s)\n", status, task, out, errSlot)
	isOK := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", isOK, status)
	g.labels++
	n := g.labels
	okLabel := fmt.Sprintf("task.ok.%d", n)
	failLabel := fmt.Sprintf("task.fail.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isOK, okLabel, failLabel)
	g.out.WriteString(failLabel + ":\n")
	message := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 0\n", message, errSlot)
	msg := g.load("ptr", message)
	sizePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 1\n", sizePtr, errSlot)
	size := g.load("i64", sizePtr)
	sitePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 2\n", sitePtr, errSlot)
	site := g.load("ptr", sitePtr)
	symbolPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_error, ptr %s, i32 0, i32 3\n", symbolPtr, errSlot)
	symbol := g.load("ptr", symbolPtr)
	fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 %s, ptr %s, ptr %s)\n", msg, size, site, symbol)
	g.out.WriteString("  unreachable\n")
	fmt.Fprintf(g.out, "%s:\n", okLabel)
	return out, elemLLVM, elem, nil
}

// lowerChanNew builds a channel with the element size and capacity written at
// the creation site.
func (g *generator) lowerChanNew(r willc.Record) (string, error) {
	text := fieldText(r, "type")
	elem, ok := chanElemText(text)
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: %s is not a channel type", r.ID, text)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: channel element %s is not lowered yet", r.ID, elem)
	}
	capValue, err := g.value(refField(r, "cap"))
	if err != nil {
		return "", err
	}
	cap64 := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sext i32 %s to i64\n", cap64, capValue)
	size := g.sizeOf(elemLLVM)
	g.needTaskDecl = true
	chanPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_chan_new(i64 %s, i64 %s, i32 0)\n", chanPtr, size, cap64)
	return chanPtr, nil
}

// lowerSend copies one value into a channel. A scalar value needs a slot to
// pass its address; an aggregate already has one.
func (g *generator) lowerSend(r willc.Record) error {
	chanPtr, err := g.value(refField(r, "chan"))
	if err != nil {
		return err
	}
	valueID := refField(r, "value")
	typ, err := g.typeOf(valueID)
	if err != nil {
		return err
	}
	ptr := ""
	if isAggregateType(typ) {
		ptr, _, err = g.place(valueID)
		if err != nil {
			return err
		}
	} else {
		ptr = g.allocSlot(g.sendValueSlots, "send", r.ID, typ)
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, ptr)
	}
	g.needTaskDecl = true
	fmt.Fprintf(g.out, "  call i32 @willrt_chan_send(ptr %s, ptr %s)\n", chanPtr, ptr)
	return nil
}

// lowerClose closes one channel.
func (g *generator) lowerClose(r willc.Record) error {
	chanPtr, err := g.value(refField(r, "chan"))
	if err != nil {
		return err
	}
	g.needTaskDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_chan_close(ptr %s)\n", chanPtr)
	return nil
}

// lowerTrySend sends only when the channel has space and returns whether the
// value was accepted.
func (g *generator) lowerTrySend(r willc.Record) (string, error) {
	chanPtr, err := g.value(refField(r, "chan"))
	if err != nil {
		return "", err
	}
	valueID := refField(r, "value")
	typ, err := g.typeOf(valueID)
	if err != nil {
		return "", err
	}
	ptr := ""
	if isAggregateType(typ) {
		ptr, _, err = g.place(valueID)
		if err != nil {
			return "", err
		}
	} else {
		ptr = g.allocSlot(g.sendValueSlots, "send", r.ID, typ)
		value, err := g.value(valueID)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, ptr)
	}
	g.needTaskDecl = true
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_chan_try_send(ptr %s, ptr %s)\n", status, chanPtr, ptr)
	ok := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, status)
	return ok, nil
}

// lowerRecv builds Option<T> from one receive. A closed channel gives None.
func (g *generator) lowerRecvPlace(r willc.Record) (string, string, error) {
	return g.lowerRecv(r, false)
}

// lowerTryRecv is the non-blocking receive.
func (g *generator) lowerTryRecv(r willc.Record) (string, string, error) {
	return g.lowerRecv(r, true)
}

func (g *generator) lowerRecv(r willc.Record, try bool) (string, string, error) {
	chanText, err := g.willTypeText(refField(r, "chan"))
	if err != nil {
		return "", "", err
	}
	elem, ok := chanElemText(chanText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Recv needs a channel", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: channel element %s is not lowered yet", r.ID, elem)
	}
	info, ok := g.adtType("Option<" + elem + ">")
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Option<%s> is not lowered yet", r.ID, elem)
	}
	chanPtr, err := g.value(refField(r, "chan"))
	if err != nil {
		return "", "", err
	}
	out := g.allocSlot(g.recvOutSlots, "recv.out", r.ID, elemLLVM)
	g.needTaskDecl = true
	fn := "willrt_chan_recv"
	if try {
		fn = "willrt_chan_try_recv"
	}
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s, ptr %s)\n", status, fn, chanPtr, out)
	slot := g.allocSlot(g.recvOptSlots, "recv", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	isOK := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", isOK, status)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("recv.some.%d", n)
	endLabel := fmt.Sprintf("recv.end.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isOK, someLabel, endLabel)
	g.out.WriteString(someLabel + ":\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	value := g.load(elemLLVM, out)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, payload)
	fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	g.out.WriteString(endLabel + ":\n")
	return slot, info.llvm, nil
}
