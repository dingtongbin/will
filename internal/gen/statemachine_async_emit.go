package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// emitAsyncConstructor writes the entry that allocates the frame and starts
// the task. A caller never needs the frame layout.
func (g *generator) emitAsyncConstructor(info *asyncMachineInfo) error {
	var parts []string
	var paramRecs []willc.Record
	for _, r := range g.recs {
		if r.Kind == "param" && refField(r, "parent") == info.fn.ID {
			paramRecs = append(paramRecs, r)
		}
	}
	sortByID(paramRecs)
	pab := make([]struct {
		typ   string
		byPtr bool
		text  string
	}, 0, len(paramRecs))
	for _, p := range paramRecs {
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		pab = append(pab, struct {
			typ   string
			byPtr bool
			text  string
		}{typ, byPtr, fieldText(p, "type")})
		if byPtr {
			parts = append(parts, "ptr %arg"+fmt.Sprint(len(parts)))
		} else {
			parts = append(parts, typ+" %arg"+fmt.Sprint(len(parts)))
		}
	}
	g.needAllocDecl = true
	g.needTaskDecl = true
	fmt.Fprintf(g.out, "define internal ptr @%s(%s) {\nentry:\n",
		llvmSymbol(info.constructor), strings.Join(parts, ", "))
	size := g.sizeOf(info.frameType)
	frame := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", frame, size)
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 0\n", state, info.frameType, frame)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", state)
	op := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 1\n", op, info.frameType, frame)
	fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", op)
	for i := range paramRecs {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 %d\n",
			field, info.frameType, frame, i+2)
		arg := "%arg" + fmt.Sprint(i)
		if pab[i].byPtr {
			g.copyValue(arg, field, pab[i].typ)
			if err := g.shareValue(field, pab[i].text); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", pab[i].typ, arg, field)
		}
	}
	for i := len(paramRecs); i < len(info.fields); i++ {
		f := info.fields[i]
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 %d\n",
			field, info.frameType, frame, i+2)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", f.llvm, zeroValueText(f.llvm), field)
	}
	lane := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_lane_here()\n", lane)
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 %s, i32 0)\n",
		task, llvmSymbol(info.symbol), frame, size, lane)
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", task)
	fmt.Fprintf(g.out, "  ret ptr %s\n}\n", task)
	return nil
}

// emitAsyncStep writes the state machine of one direct async C call.
func (g *generator) emitAsyncStep(info *asyncMachineInfo) error {
	if g.spawnEmitted[info.symbol] {
		return nil
	}
	g.spawnEmitted[info.symbol] = true
	g.needTaskDecl = true
	g.needWaitDecl = true
	g.queueCDecl("willrt_blocking_poll", "i32", []string{"ptr"})
	g.queueCDecl("willrt_blocking_finish", "i32", []string{"ptr"})
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
	g.resultType = info.resultLLVM
	g.fnResultText = info.resultText
	g.fnSymbol = info.symbol
	g.currentLambda = 0
	if err := g.emitAsyncConstructor(info); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "define internal i32 @%s(ptr %%frame) {\nentry:\n",
		llvmSymbol(info.symbol))
	if err := g.allocas(info.fn.ID); err != nil {
		return err
	}
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 0\n", state, info.frameType)
	op := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 1\n", op, info.frameType)
	statusField := ""
	if info.kind == "c" {
		statusField = g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			statusField, info.frameType, info.statusIndex+2)
	}
	resultField := ""
	if info.kind == "task" {
		resultField = g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			resultField, info.frameType, info.resultIndex+2)
	}
	// Parameters are loaded from the frame; locals use their frame fields as
	// their slots, so every value survives a park and resume.
	paramRecs := make([]willc.Record, 0, len(info.fields))
	for _, r := range g.recs {
		if r.Kind == "param" && refField(r, "parent") == info.fn.ID {
			paramRecs = append(paramRecs, r)
		}
	}
	sortByID(paramRecs)
	for i, p := range paramRecs {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, i+2)
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
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
		g.slots[f.id] = field
		g.slotTypes[f.id] = f.llvm
	}
	current := g.load("i32", state)
	fmt.Fprintf(g.out, "  switch i32 %s, label %%sm.init [ i32 1, label %%sm.wait ]\n", current)
	g.out.WriteString("sm.init:\n")
	// The prelude is deliberately straight-line: it may declare locals but it
	// cannot wait or return before the operation starts.
	if _, err := g.bodyStmts(info.prelude, info.resultLLVM == ""); err != nil {
		return err
	}
	g.labels++
	n := g.labels
	after := fmt.Sprintf("sm.after.%d", n)
	if info.kind == "c" {
		parts, argTypes, err := g.asyncStartArgs(info.call)
		if err != nil {
			return err
		}
		g.queueCDecl(info.startName, "ptr", argTypes)
		opValue := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @%s(%s)\n",
			opValue, llvmSymbol(info.startName), strings.Join(parts, ", "))
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", opValue, op)
		started := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp ne ptr %s, null\n", started, opValue)
		g.labels++
		failed := fmt.Sprintf("sm.start.failed.%d", g.labels)
		wait := fmt.Sprintf("sm.wait.start.%d", g.labels)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", started, wait, failed)
		fmt.Fprintf(g.out, "%s:\n", failed)
		fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", statusField)
		fmt.Fprintf(g.out, "  br label %%%s\n", after)
		fmt.Fprintf(g.out, "%s:\n", wait)
	} else {
		child, err := g.emitConstructorCall(info.call, info.targetSym)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", child, op)
	}
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", state)
	g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
	g.out.WriteString("sm.wait:\n")
	g.labels++
	park := fmt.Sprintf("sm.park.%d", g.labels)
	afterWait := fmt.Sprintf("sm.after.wait.%d", g.labels)
	if info.kind == "c" {
		poll := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_blocking_poll(ptr %s)\n", poll, g.load("ptr", op))
		pending := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", pending, poll)
		finish := fmt.Sprintf("sm.finish.%d", g.labels)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", pending, park, finish)
		fmt.Fprintf(g.out, "%s:\n", park)
		g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
		fmt.Fprintf(g.out, "%s:\n", finish)
		status := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_blocking_finish(ptr %s)\n", status, g.load("ptr", op))
		fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", status, statusField)
		fmt.Fprintf(g.out, "  br label %%%s\n", afterWait)
	} else {
		errSlot := g.newTmp()
		fmt.Fprintf(g.out, "  %s = alloca %%willrt_error\n", errSlot)
		g.needPanicDecl = true
		joinStatus := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_task_try_join(ptr %s, ptr %s, ptr %s)\n",
			joinStatus, g.load("ptr", op), resultField, errSlot)
		pending := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -1\n", pending, joinStatus)
		failed := fmt.Sprintf("sm.child.failed.%d", g.labels)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", pending, park, failed)
		fmt.Fprintf(g.out, "%s:\n", park)
		g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
		fmt.Fprintf(g.out, "%s:\n", failed)
		ok := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, joinStatus)
		panicLabel := failed + ".panic"
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, afterWait, panicLabel)
		fmt.Fprintf(g.out, "%s:\n", panicLabel)
		g.emitPanicFromError(errSlot)
		fmt.Fprintf(g.out, "  br label %%%s\n", afterWait)
	}
	fmt.Fprintf(g.out, "%s:\n", after)
	if info.kind == "c" {
		fmt.Fprintf(g.out, "  br label %%%s\n", afterWait)
	} else {
		// A task state machine reaches the after label only from the child
		// result; the branch is emitted at the failed label above.
		g.out.WriteString("  br label " + afterWait + "\n")
	}
	fmt.Fprintf(g.out, "%s:\n", afterWait)
	if info.kind == "task" {
		g.smPlaces[info.call.ID] = smPlace{ptr: resultField, typ: info.callLLVM}
	}
	g.smMode = true
	terminated, err := g.bodyStmts(info.postlude, info.resultLLVM == "")
	if err != nil {
		return err
	}
	g.smMode = false
	delete(g.smPlaces, info.call.ID)
	if !terminated {
		if info.resultLLVM == "" {
			g.out.WriteString("  ret i32 1\n")
		} else {
			g.out.WriteString("  unreachable\n")
		}
	}
	g.flushLateAllocas()
	g.out.WriteString("}\n")
	return nil
}

// emitPanicFromError reports the task error stored in errSlot and does not
// return. The caller keeps the site and symbol the child task recorded.
func (g *generator) emitPanicFromError(errSlot string) {
	g.needPanicDecl = true
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
}

// emitConstructorCall builds the child frame by calling the target's task
// constructor. The constructor owns the frame layout, so this document does not
// need the target's local fields.
func (g *generator) emitConstructorCall(call willc.Record, targetSym string) (string, error) {
	if targetSym == "" {
		return "", g.errf(call.ID, "cannot translate node #%d: the suspendable call has no target", call.ID)
	}
	args := g.callArgs(call)
	parts := make([]string, 0, len(args))
	paramTypes := append([]string(nil), g.paramTypes[targetSym]...)
	if len(paramTypes) != len(args) {
		return "", g.errf(call.ID, "cannot translate node #%d: %s needs %d arguments", call.ID, targetSym, len(paramTypes))
	}
	declParams := make([]string, 0, len(args))
	for _, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return "", err
		}
		if isAggregateType(typ) {
			ptr, _, err := g.place(a)
			if err != nil {
				return "", err
			}
			parts = append(parts, "ptr "+ptr)
			declParams = append(declParams, "ptr")
			continue
		}
		value, err := g.value(a)
		if err != nil {
			return "", err
		}
		parts = append(parts, typ+" "+value)
		declParams = append(declParams, typ)
	}
	g.queueCDecl(targetSym+".task", "ptr", declParams)
	g.needTaskDecl = true
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @%s(%s)\n",
		task, llvmSymbol(targetSym+".task"), strings.Join(parts, ", "))
	return task, nil
}

// asyncStartArgs evaluates the arguments of the blocking form and returns the
// value parts for the matching start entry.
func (g *generator) asyncStartArgs(call willc.Record) ([]string, []string, error) {
	args := g.callArgs(call)
	parts := make([]string, 0, len(args))
	types := make([]string, 0, len(args))
	for _, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return nil, nil, err
		}
		if typ == "" || isAggregateType(typ) {
			return nil, nil, g.errf(a, "cannot translate node #%d: an async C call takes scalar arguments", a)
		}
		value, err := g.value(a)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, typ+" "+value)
		types = append(types, typ)
	}
	return parts, types, nil
}
