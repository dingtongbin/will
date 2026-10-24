package gen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// generalMachine reports whether a function must be lowered as a general CFG
// state machine. A function that waits on a channel, a task, or a select
// waits, and so does a function that calls a waiting function: the caller
// runs the callee as a child task and waits for it.
func (g *generator) generalMachine(fnID int) bool {
	if fnID == 0 {
		return false
	}
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" {
		return false
	}
	if fnID == g.entryID && g.cur.path == g.entryPkg {
		return g.entryAsync
	}
	if fieldText(fn, "async") == "true" {
		return true
	}
	return g.channelWaitBody(fnID) || g.markedBody(fnID)
}

// enqueueGeneral schedules one function for general state-machine emission.
func (g *generator) enqueueGeneral(fnID int) {
	for _, id := range g.pendingGeneral {
		if id == fnID {
			return
		}
	}
	g.pendingGeneral = append(g.pendingGeneral, fnID)
}

// smAlloca reserves one frame slot. The step's allocas are moved to the heap
// frame after the body is emitted, so the returned name is a normal SSA place.
func (g *generator) smAlloca(typ, prefix string) string {
	g.tmp++
	name := fmt.Sprintf("%%v.sm.%s.%d", prefix, g.tmp)
	fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
	return name
}

// smReserve allocates the next resume state and returns its id.
func (g *generator) smReserve() int {
	g.smStateSeq++
	id := g.smStateSeq
	g.smStates = append(g.smStates, fmt.Sprintf("sm.op.call.%d", id))
	return id
}

// smStoreState records the state that the next dispatch must resume.
func (g *generator) smStoreState(id int) {
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", id, g.smStateSlot)
	fmt.Fprintf(g.out, "  br label %%sm.op.call.%d\n", id)
	fmt.Fprintf(g.out, "sm.op.call.%d:\n", id)
}

// smCheckPark branches to the park block on WILLRT_SUSPEND and leaves the
// continuation at sm.op.cont.<id>.
func (g *generator) smCheckPark(status string, id int) {
	susp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -1\n", susp, status)
	fmt.Fprintf(g.out, "  br i1 %s, label %%sm.op.park.%d, label %%sm.op.cont.%d\n", susp, id, id)
	fmt.Fprintf(g.out, "sm.op.park.%d:\n", id)
	g.out.WriteString("  call void @willrt_task_mark_wait()\n")
	g.out.WriteString("  ret i32 -1\n")
	fmt.Fprintf(g.out, "sm.op.cont.%d:\n", id)
	g.needWaitDecl = true
}

// smValueSlot stores a scalar or pointer that must survive a suspension.
func (g *generator) smValueSlot(typ string, prefix string) string {
	return g.smAlloca(typ, prefix)
}

// emitGeneralStep emits one function as a dispatch over resume labels. Every
// alloca in the step is relocated into a byte frame, so locals and temporaries
// persist across a return to the scheduler.
func (g *generator) emitGeneralStep(fnID int) error {
	fn, ok := g.recs[fnID]
	if !ok || fn.Kind != "func" {
		return g.errf(fnID, "cannot translate node #%d: the general state machine needs a function", fnID)
	}
	sym := g.cur.funcs[fnID]
	if sym == "" {
		sym = qualify(g.pkg, fieldText(fn, "text"))
	}
	if g.spawnEmitted[sym+".sm"] {
		return nil
	}
	g.spawnEmitted[sym+".sm"] = true

	bodyRec, err := g.blockOf(fnID)
	if err != nil {
		return err
	}
	oldOut := g.out
	oldGeneral := g.smGeneral
	oldStateSlot := g.smStateSlot
	oldStateSeq := g.smStateSeq
	oldStates := g.smStates
	oldParamSlot := g.smParamSlot
	defer func() {
		g.out = oldOut
		g.smGeneral = oldGeneral
		g.smStateSlot = oldStateSlot
		g.smStateSeq = oldStateSeq
		g.smStates = oldStates
		g.smParamSlot = oldParamSlot
	}()

	var body strings.Builder
	g.out = &body
	g.smGeneral = true
	g.smStateSlot = "%v.sm.state"
	g.smStateSeq = 0
	g.smStates = nil
	g.smParamSlot = map[int]string{}
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
	// The general step relocates every alloca it lowers into the frame, so a
	// slot must stay in the body text instead of waiting for an entry marker.
	g.lateAllowed = false
	g.smPlaces = map[int]smPlace{}
	g.liveValues = map[int]string{}
	g.resultType = g.resultLLVM(fieldText(fn, "type"))
	g.fnResultText = fieldText(fn, "type")
	g.fnSymbol = sym
	g.fnPos = fieldText(fn, "pos")
	g.crossingPos = g.fnPos
	g.currentLambda = 0

	fmt.Fprintf(g.out, "  %s = alloca i32\n", g.smStateSlot)

	params := g.asyncCallRecs(fnID)
	type scalarParam struct {
		name string
		typ  string
		slot string
	}
	var scalarParams []scalarParam
	for _, p := range params {
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		slot := g.smAlloca(typ, "param."+strconv.Itoa(p.ID))
		g.smParamSlot[p.ID] = slot
		if byPtr {
			g.scope[p.ID] = slot
			g.borrows[p.ID] = true
			continue
		}
		name := fmt.Sprintf("%%v.sm.pval.%d", p.ID)
		g.scope[p.ID] = name
		scalarParams = append(scalarParams, scalarParam{name: name, typ: typ, slot: slot})
	}

	if err := g.allocas(fnID); err != nil {
		return err
	}
	g.out.WriteString("sm.init:\n")
	g.smMode = true
	stmts := g.children(bodyRec.ID)
	sortByID(stmts)
	terminated, err := g.bodyStmts(stmts, g.resultType == "")
	if err != nil {
		return err
	}
	if !terminated {
		if g.resultType == "" {
			g.out.WriteString("  ret i32 1\n")
		} else {
			g.out.WriteString("  unreachable\n")
		}
	}
	bodyText := body.String()
	g.out = oldOut

	relocated, geps, size, offsets, err := g.relocateGeneralAllocas(bodyText)
	if err != nil {
		return g.errf(fnID, "cannot translate node #%d: %v", fnID, err)
	}
	stateOffset, ok := offsets[g.smStateSlot]
	if !ok {
		return g.errf(fnID, "cannot translate node #%d: the state slot has no frame offset", fnID)
	}
	_ = stateOffset

	fmt.Fprintf(g.out, "define internal i32 @%s(ptr %%frame) {\nentry:\n", llvmSymbol(sym+".sm"))
	g.out.WriteString(geps)
	for _, p := range scalarParams {
		fmt.Fprintf(g.out, "  %s = load %s, ptr %s\n", p.name, p.typ, p.slot)
	}
	fmt.Fprintf(g.out, "  %%v.sm.statev = load i32, ptr %s\n", g.smStateSlot)
	if len(g.smStates) == 0 {
		g.out.WriteString("  br label %sm.init\n")
	} else {
		var cases strings.Builder
		for i, label := range g.smStates {
			fmt.Fprintf(&cases, " i32 %d, label %%%s", i+1, label)
		}
		fmt.Fprintf(g.out, "  switch i32 %%v.sm.statev, label %%sm.init [%s ]\n", cases.String())
	}
	g.out.WriteString(relocated)
	g.out.WriteString("}\n")

	return g.emitGeneralConstructor(fnID, sym, params, size, offsets)
}

// smTryJoin starts a non-blocking join. The task pointer is stored in the
// frame, so a resume can call try_join again without replaying the callee.
func (g *generator) smTryJoin(task, out, errSlot string) string {
	taskSlot := g.smAlloca("ptr", "join.task")
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", task, taskSlot)
	id := g.smReserve()
	g.smStoreState(id)
	status := g.newTmp()
	if out == "" {
		out = "null"
	}
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_task_try_join(ptr %s, ptr %s, ptr %s)\n",
		status, g.load("ptr", taskSlot), out, errSlot)
	g.needTaskDecl = true
	g.smCheckPark(status, id)
	return status
}

func (g *generator) emitGeneralConstructor(fnID int, sym string, params []willc.Record, size int, offsets map[string]int) error {
	var parts []string
	var cancelPtrs []string
	type paramABI struct {
		typ   string
		byPtr bool
		text  string
		p     willc.Record
	}
	abis := make([]paramABI, 0, len(params))
	for i, p := range params {
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		abis = append(abis, paramABI{typ: typ, byPtr: byPtr, text: fieldText(p, "type"), p: p})
		if byPtr {
			parts = append(parts, fmt.Sprintf("ptr %%arg%d", i))
		} else {
			parts = append(parts, fmt.Sprintf("%s %%arg%d", typ, i))
		}
	}
	g.needAllocDecl = true
	g.needTaskDecl = true
	// The constructor is hidden, not internal: a function of another package
	// waits on this task, so the object that owns the function must export
	// the entry that starts it.
	fmt.Fprintf(g.out, "define hidden ptr @%s(%s) {\nentry:\n", llvmSymbol(sym+".task"), strings.Join(parts, ", "))
	fmt.Fprintf(g.out, "  %%v.sm.frame = call ptr @willrt_alloc(i64 %d)\n", size)
	stateOffset := offsets[g.smStateSlot]
	statePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr %%v.sm.frame, i64 %d\n", statePtr, stateOffset)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", statePtr)
	for i, a := range abis {
		slot := g.smParamSlot[a.p.ID]
		offset, ok := offsets[slot]
		if !ok {
			return g.errf(fnID, "cannot translate node #%d: parameter %d has no frame offset", fnID, a.p.ID)
		}
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr %%v.sm.frame, i64 %d\n", ptr, offset)
		if a.byPtr {
			g.copyValue(fmt.Sprintf("%%arg%d", i), ptr, a.typ)
		} else {
			fmt.Fprintf(g.out, "  store %s %%arg%d, ptr %s\n", a.typ, i, ptr)
		}
		// The capture is the crossing whether the parameter moves by value
		// or by pointer: an rc handle lands in a frame another lane runs.
		if err := g.shareValue(ptr, a.text); err != nil {
			return err
		}
		if a.text == "task.Cancel" || strings.HasSuffix(a.text, ".Cancel") {
			cancelPtrs = append(cancelPtrs, ptr)
		}
	}
	lane := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_lane_here()\n", lane)
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %%v.sm.frame, i64 %d, i32 %s, i32 0)\n",
		task, llvmSymbol(sym+".sm"), size, lane)
	g.needOwnFrameDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_task_own_frame(ptr %s)\n", task)
	for _, ptr := range cancelPtrs {
		g.needCancelBind = true
		fmt.Fprintf(g.out, "  call void @willrt_task_bind_cancel(ptr %s, ptr %s)\n", task, ptr)
	}
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", task)
	fmt.Fprintf(g.out, "  ret ptr %s\n}\n", task)
	return nil
}

// generalAlloca matches one alloca line emitted by the existing lowerer.
var generalAlloca = regexp.MustCompile(`^\s*(%[A-Za-z0-9_.$]+) = alloca (.+?)(?:, align \d+)?\s*$`)

// relocateGeneralAllocas moves every alloca in one step into the task frame.
// The frame is raw bytes; a GEP in the entry block replaces each alloca, so
// every local and temporary survives a step return.
func (g *generator) relocateGeneralAllocas(text string) (string, string, int, map[string]int, error) {
	lines := strings.Split(text, "\n")
	var body strings.Builder
	var geps strings.Builder
	offsets := map[string]int{}
	offset := 0
	for _, line := range lines {
		m := generalAlloca.FindStringSubmatch(line)
		if m == nil {
			body.WriteString(line)
			body.WriteByte('\n')
			continue
		}
		size, align, ok := g.sizeAlign(strings.TrimSpace(m[2]))
		if !ok {
			return "", "", 0, nil, fmt.Errorf("frame type %s is not sized", strings.TrimSpace(m[2]))
		}
		if align < 1 {
			align = 1
		}
		if size < 1 {
			size = 1
		}
		offset = alignUp(offset, align)
		offsets[m[1]] = offset
		fmt.Fprintf(&geps, "  %s = getelementptr i8, ptr %%frame, i64 %d\n", m[1], offset)
		offset += size
	}
	size := alignUp(offset, 16)
	if size < 16 {
		size = 16
	}
	return body.String(), geps.String(), size, offsets, nil
}
