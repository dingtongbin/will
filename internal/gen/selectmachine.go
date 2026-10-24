package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// selectMachineInfo is one top-level select lowered as a stackless step. The
// arm array and every receive/send slot live in the frame, so a waiter can
// survive a park.
type selectMachineInfo struct {
	fn          willc.Record
	symbol      string
	constructor string
	frameType   string
	fields      []asyncField
	params      []willc.Record
	arms        []willc.Record
	armsIndex   int
	ctxIndex    int
	indexIndex  int
	outIndex    map[int]int
	valueIndex  map[int]int
	pre         []willc.Record
	post        []willc.Record
	resultText  string
	resultLLVM  string
	base        *asyncMachineInfo
}

// selectMachine recognizes a function whose only suspension is one top-level
// select. A select nested in a loop or arm body keeps the ordinary lowering.
func (g *generator) selectMachine(fnID int) (*selectMachineInfo, bool) {
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" || fnID == g.entryID {
		return nil, false
	}
	body, err := g.blockOf(fnID)
	if err != nil {
		return nil, false
	}
	stmts := g.children(body.ID)
	sortByID(stmts)
	selectIndex := -1
	var selectRec willc.Record
	for i, st := range stmts {
		if st.Kind == "select" {
			if selectIndex >= 0 {
				return nil, false
			}
			selectIndex = i
			selectRec = st
		}
	}
	if selectIndex < 0 {
		return nil, false
	}
	for _, r := range g.recs {
		if !g.insideBlock(r.ID, body.ID) {
			continue
		}
		if r.Kind == "select" && r.ID != selectRec.ID {
			return nil, false
		}
		if (r.Kind == "join" || r.Kind == "recv" || r.Kind == "send") &&
			!g.insideBlock(r.ID, selectRec.ID) {
			return nil, false
		}
	}
	arms := g.selectArms(selectRec)
	if len(arms) == 0 {
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
	fields := make([]asyncField, 0, len(params)+len(locals)+len(arms)+2)
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
	outIndex := map[int]int{}
	valueIndex := map[int]int{}
	for _, arm := range arms {
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok {
			continue
		}
		switch chanNode.Kind {
		case "recv":
			chanText, err := g.willTypeText(refField(chanNode, "chan"))
			if err != nil {
				return nil, false
			}
			elem, ok := chanElemText(chanText)
			if !ok {
				return nil, false
			}
			elemLLVM, ok := g.typeLLVMOf(elem)
			if !ok || elemLLVM == "" {
				return nil, false
			}
			outIndex[arm.ID] = len(fields)
			fields = append(fields, asyncField{id: -arm.ID, llvm: elemLLVM})
		case "send":
			valueID := refField(chanNode, "value")
			typ, err := g.typeOf(valueID)
			if err != nil {
				return nil, false
			}
			if !isAggregateType(typ) {
				valueIndex[chanNode.ID] = len(fields)
				fields = append(fields, asyncField{id: -chanNode.ID, llvm: typ})
			}
		}
	}
	armsIndex := len(fields)
	fields = append(fields, asyncField{id: 0, llvm: fmt.Sprintf("[%d x %%willrt_arm]", len(arms))})
	ctxIndex := len(fields)
	fields = append(fields, asyncField{id: 0, llvm: "ptr"})
	indexIndex := len(fields)
	fields = append(fields, asyncField{id: 0, llvm: "i32"})
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
	info := &selectMachineInfo{
		fn:          fn,
		symbol:      qualify(g.pkg, fieldText(fn, "text")+".sm"),
		constructor: qualify(g.pkg, fieldText(fn, "text")+".task"),
		frameType:   frameType,
		fields:      fields,
		params:      params,
		arms:        arms,
		armsIndex:   armsIndex,
		ctxIndex:    ctxIndex,
		indexIndex:  indexIndex,
		outIndex:    outIndex,
		valueIndex:  valueIndex,
		pre:         append([]willc.Record(nil), stmts[:selectIndex]...),
		post:        append([]willc.Record(nil), stmts[selectIndex+1:]...),
		resultText:  resultText,
		resultLLVM:  resultLLVM,
	}
	info.base = &asyncMachineInfo{
		fn:          fn,
		symbol:      info.symbol,
		constructor: info.constructor,
		frameType:   info.frameType,
		fields:      info.fields,
		resultText:  info.resultText,
		resultLLVM:  info.resultLLVM,
	}
	return info, true
}

// emitSelectMachineStep writes state 0 (build arms and start), state 1 (poll),
// and the arm dispatch. The arm bodies and postlude run without further parks.
func (g *generator) emitSelectMachineStep(info *selectMachineInfo) error {
	if g.spawnEmitted[info.symbol] {
		return nil
	}
	g.spawnEmitted[info.symbol] = true
	g.needTaskDecl = true
	g.needWaitDecl = true
	g.smMode = false
	g.labels = 0
	g.slots = map[int]string{}
	g.slotTypes = map[int]string{}
	g.slotNames = map[string]int{}
	g.scope = map[int]string{}
	g.borrows = map[int]bool{}
	g.loops = nil
	g.defers = nil
	g.pendingReleases = nil
	g.pendingTryReleases = nil
	g.lateAllocas = nil
	g.lateAllowed = true
	g.smPlaces = map[int]smPlace{}
	g.resultType = info.resultLLVM
	g.fnResultText = info.resultText
	g.fnSymbol = info.symbol
	g.fnPos = fieldText(info.fn, "pos")
	g.crossingPos = g.fnPos
	g.currentLambda = 0
	if err := g.emitAsyncConstructor(info.base); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "define internal i32 @%s(ptr %%frame) {\nentry:\n",
		llvmSymbol(info.symbol))
	if err := g.allocas(info.fn.ID); err != nil {
		return err
	}
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 0\n", state, info.frameType)
	ctx := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
		ctx, info.frameType, info.ctxIndex+2)
	armsPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
		armsPtr, info.frameType, info.armsIndex+2)
	indexField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
		indexField, info.frameType, info.indexIndex+2)
	paramRecs := g.asyncCallRecs(info.fn.ID)
	recv := receiverOfIn(g.cur, info.fn.ID)
	for i, p := range paramRecs {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, i+2)
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		if recv != nil && i == 0 {
			g.scope[p.ID] = field
			g.borrows[p.ID] = true
			continue
		}
		if byPtr {
			g.scope[p.ID] = field
			g.borrows[p.ID] = true
		} else {
			g.scope[p.ID] = g.load(typ, field)
		}
	}
	for i := len(paramRecs); i < len(info.fields); i++ {
		f := info.fields[i]
		if f.id == 0 {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, i+2)
		if f.id > 0 {
			g.slots[f.id] = field
			g.slotTypes[f.id] = f.llvm
		}
	}
	for armID, index := range info.outIndex {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, index+2)
		g.selectOutSlots[armID] = field
	}
	for chanID, index := range info.valueIndex {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, index+2)
		g.sendValueSlots[chanID] = field
	}
	current := g.load("i32", state)
	fmt.Fprintf(g.out, "  switch i32 %s, label %%sm.init [ i32 1, label %%sm.wait ]\n", current)
	g.out.WriteString("sm.init:\n")
	g.smMode = true
	terminated, err := g.bodyStmts(info.pre, info.resultLLVM == "")
	if err != nil {
		return err
	}
	if !terminated {
		if err := g.emitSelectArmsTo(info, armsPtr); err != nil {
			return err
		}
		g.queueCDecl("willrt_select_start", "ptr", []string{"ptr", "i64"})
		g.queueCDecl("willrt_select_poll", "i32", []string{"ptr"})
		g.queueCDecl("willrt_select_finish", "void", []string{"ptr"})
		ctxValue := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_select_start(ptr %s, i64 %d)\n",
			ctxValue, armsPtr, len(info.arms))
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ctxValue, ctx)
		isNull := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, null\n", isNull, ctxValue)
		fmt.Fprintf(g.out, "  br i1 %s, label %%sm.after.failed, label %%sm.started\n", isNull)
		g.out.WriteString("sm.after.failed:\n")
		fmt.Fprintf(g.out, "  store i32 -7, ptr %s\n", indexField)
		g.out.WriteString("  br label %sm.after\nsm.started:\n")
		index := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_select_poll(ptr %s)\n", index, ctxValue)
		fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", index, indexField)
		ready := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp sge i32 %s, 0\n", ready, index)
		fmt.Fprintf(g.out, "  br i1 %s, label %%sm.after, label %%sm.park\n", ready)
		g.out.WriteString("sm.park:\n")
		fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", state)
		g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
	}
	g.out.WriteString("sm.wait:\n")
	ctxLoaded := g.load("ptr", ctx)
	polled := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_select_poll(ptr %s)\n", polled, ctxLoaded)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", polled, indexField)
	ready := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp sge i32 %s, 0\n", ready, polled)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.after, label %%sm.wait.park\n", ready)
	g.out.WriteString("sm.wait.park:\n")
	g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
	g.out.WriteString("sm.after:\n")
	ctxValue := g.load("ptr", ctx)
	fmt.Fprintf(g.out, "  call void @willrt_select_finish(ptr %s)\n", ctxValue)
	indexValue := g.load("i32", indexField)
	negative := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp slt i32 %s, 0\n", negative, indexValue)
	fmt.Fprintf(g.out, "  br i1 %s, label %%select.machine.post, label %%select.machine.dispatch\n", negative)
	g.out.WriteString("select.machine.dispatch:\n")
	g.labels++
	n := g.labels
	var cases []string
	for i := range info.arms {
		cases = append(cases, fmt.Sprintf("i32 %d, label %%select.machine.arm.%d.%d", i, n, i))
	}
	fmt.Fprintf(g.out, "  switch i32 %s, label %%select.machine.post [ %s ]\n",
		indexValue, strings.Join(cases, " "))
	for i, arm := range info.arms {
		fmt.Fprintf(g.out, "select.machine.arm.%d.%d:\n", n, i)
		chanNode, hasChan := g.recs[refField(arm, "chan")]
		if hasChan && chanNode.Kind == "recv" {
			if bind := fieldText(arm, "bind"); bind != "" && bind != "_" {
				if err := g.selectBind(arm); err != nil {
					return err
				}
			}
		}
		body, ok := g.recs[refField(arm, "body")]
		if !ok {
			return g.errf(arm.ID, "cannot translate node #%d: the arm body is missing", arm.ID)
		}
		done, err := g.body(body, info.resultLLVM == "")
		if err != nil {
			return err
		}
		if !done {
			g.out.WriteString("  br label %select.machine.post\n")
		}
	}
	g.out.WriteString("select.machine.post:\n")
	g.smMode = true
	postTerminated, err := g.bodyStmts(info.post, info.resultLLVM == "")
	if err != nil {
		return err
	}
	if !postTerminated {
		if info.resultLLVM == "" {
			g.out.WriteString("  ret i32 1\n")
		} else {
			g.out.WriteString("  unreachable\n")
		}
	}
	g.smMode = false
	g.flushLateAllocas()
	g.out.WriteString("}\n")
	return nil
}

func (g *generator) emitSelectArmsTo(info *selectMachineInfo, armsPtr string) error {
	armType := fmt.Sprintf("[%d x %%willrt_arm]", len(info.arms))
	for i, arm := range info.arms {
		base := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			base, armType, armsPtr, i)
		chanField := g.newTmp()
		outField := g.newTmp()
		valueField := g.newTmp()
		kindField := g.newTmp()
		nsField := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 0\n", chanField, base)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 1\n", outField, base)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 2\n", valueField, base)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 3\n", kindField, base)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%willrt_arm, ptr %s, i32 0, i32 4\n", nsField, base)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", chanField)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", outField)
		fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", valueField)
		fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", nsField)
		if fieldText(arm, "default") == "true" {
			fmt.Fprintf(g.out, "  store i32 3, ptr %s\n", kindField)
			continue
		}
		if timeoutID := refField(arm, "timeout"); timeoutID != 0 {
			duration, err := g.value(timeoutID)
			if err != nil {
				return err
			}
			now := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i64 @willrt_now_ns()\n", now)
			deadline := g.newTmp()
			fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", deadline, now, duration)
			fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", deadline, nsField)
			fmt.Fprintf(g.out, "  store i32 2, ptr %s\n", kindField)
			continue
		}
		chanNode, ok := g.recs[refField(arm, "chan")]
		if !ok {
			return g.errf(arm.ID, "cannot translate node #%d: the arm has no channel operation", arm.ID)
		}
		ptr, err := g.value(refField(chanNode, "chan"))
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", ptr, chanField)
		if chanNode.Kind == "recv" {
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", g.selectOutSlots[arm.ID], outField)
			fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", kindField)
			continue
		}
		valueID := refField(chanNode, "value")
		typ, err := g.typeOf(valueID)
		if err != nil {
			return err
		}
		valueText, err := g.willTypeText(valueID)
		if err != nil {
			return err
		}
		valuePtr := ""
		if isAggregateType(typ) {
			valuePtr, _, err = g.place(valueID)
			if err != nil {
				return err
			}
		} else {
			valuePtr = g.sendValueSlots[chanNode.ID]
			value, err := g.value(valueID)
			if err != nil {
				return err
			}
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, valuePtr)
		}
		if err := g.shareValue(valuePtr, valueText); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", valuePtr, valueField)
		fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", kindField)
	}
	return nil
}
