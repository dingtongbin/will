package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// emitAsyncSequenceStep writes a step that can suspend at every segment in
// order. State 0 enters the first segment; state k (1..n) waits for segment
// k-1. A child result is copied into its synthetic frame field and bound by the
// original statement in the after block.
func (g *generator) emitAsyncSequenceStep(info *asyncMachineInfo) error {
	if g.spawnEmitted[info.symbol] {
		return nil
	}
	g.spawnEmitted[info.symbol] = true
	oldZeroReleases := g.zeroReleases
	g.zeroReleases = true
	defer func() { g.zeroReleases = oldZeroReleases }()
	g.needTaskDecl = true
	g.needWaitDecl = true
	for _, seg := range info.segments {
		if seg.kind == "c" {
			g.queueCDecl(seg.pollName, "i32", []string{"ptr"})
			g.queueCDecl(seg.finishName, "i32", []string{"ptr"})
			if seg.deadline != "" {
				g.queueCDecl(seg.deadline, "i64", []string{"ptr"})
				g.queueCDecl("willrt_task_wait_until", "void", []string{"i64"})
			}
		}
	}
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
	g.resetSyntheticTemps()
	g.resultType = info.resultLLVM
	g.fnResultText = info.resultText
	g.fnSymbol = info.symbol
	g.fnPos = fieldText(info.fn, "pos")
	g.crossingPos = g.fnPos
	g.currentLambda = 0
	g.currentFunc = info.fn.ID
	if err := g.emitAsyncConstructor(info); err != nil {
		return err
	}
	fmt.Fprintf(g.out, "define internal i32 @%s(ptr %%frame) {\nentry:\n",
		llvmSymbol(info.symbol))
	// The constructor is an ordinary function and flushes its own entry
	// marker, so the step must request the late-alloca path again.
	g.lateAllowed = true
	g.lateAllocas = nil
	if err := g.allocas(info.fn.ID); err != nil {
		return err
	}
	state := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 0\n", state, info.frameType)
	op := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 1\n", op, info.frameType)
	statusFields := make([]string, len(info.segments))
	resultFields := make([]string, len(info.segments))
	for i, seg := range info.segments {
		if seg.kind == "c" {
			statusFields[i] = g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
				statusFields[i], info.frameType, seg.statusIndex+2)
		} else {
			resultFields[i] = g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
				resultFields[i], info.frameType, seg.resultIndex+2)
			text := g.callResultTextFor(seg.call)
			if llvm := g.resultLLVM(text); llvm != "" {
				g.storeZero(resultFields[i], llvm)
				g.addSyntheticTemp(resultFields[i], text, llvm)
			}
		}
	}
	paramRecs := g.asyncCallRecs(info.fn.ID)
	for i, p := range paramRecs {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, i+2)
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		if byPtr {
			if fieldText(p, "mut") == "true" {
				g.scope[p.ID] = g.load("ptr", field)
			} else {
				g.scope[p.ID] = field
			}
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
	var cases strings.Builder
	for i := range info.segments {
		fmt.Fprintf(&cases, " i32 %d, label %%sm.wait.%d", i+1, i)
	}
	fmt.Fprintf(g.out, "  switch i32 %s, label %%sm.init [%s ]\n", current, cases.String())
	g.out.WriteString("sm.init:\n")
	// All body segments run in state-machine mode: a return stores the task
	// result instead of emitting the source function's ordinary return.
	g.smMode = true
	terminated, err := g.bodyStmts(info.segments[0].before, info.resultLLVM == "")
	if err != nil {
		return err
	}
	if !terminated {
		g.out.WriteString("  br label %sm.start.0\n")
	}
	for i := range info.segments {
		seg := &info.segments[i]
		fmt.Fprintf(g.out, "sm.start.%d:\n", i)
		if i == 0 {
			// The first before block already ran in sm.init.
		}
		if err := g.emitAsyncStart(info, seg, i, state, op, statusFields[i], resultFields[i]); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "sm.wait.%d:\n", i)
		if seg.kind == "c" {
			poll := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s)\n", poll, llvmSymbol(seg.pollName), g.load("ptr", op))
			pending := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", pending, poll)
			park := fmt.Sprintf("sm.park.%d", i)
			finish := fmt.Sprintf("sm.finish.%d", i)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", pending, park, finish)
			fmt.Fprintf(g.out, "%s:\n", park)
			g.emitSequenceWaitDeadline(op, seg.deadline)
			g.needIOMarkDecl = true
			g.out.WriteString("  call void @willrt_task_mark_io()\n")
			g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
			fmt.Fprintf(g.out, "%s:\n", finish)
			status := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s)\n", status, llvmSymbol(seg.finishName), g.load("ptr", op))
			fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", status, statusFields[i])
			fmt.Fprintf(g.out, "  br label %%sm.after.%d\n", i)
		} else {
			errSlot := g.newTmp()
			fmt.Fprintf(g.out, "  %s = alloca %%willrt_error\n", errSlot)
			g.needPanicDecl = true
			joinStatus := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call i32 @willrt_task_try_join(ptr %s, ptr %s, ptr %s)\n",
				joinStatus, g.load("ptr", op), resultFields[i], errSlot)
			pending := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, -1\n", pending, joinStatus)
			park := fmt.Sprintf("sm.park.%d", i)
			done := fmt.Sprintf("sm.child.done.%d", i)
			failLabel := fmt.Sprintf("sm.child.fail.%d", i)
			after := fmt.Sprintf("sm.after.%d", i)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", pending, park, done)
			fmt.Fprintf(g.out, "%s:\n", park)
			g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
			fmt.Fprintf(g.out, "%s:\n", done)
			ok := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", ok, joinStatus)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", ok, after, failLabel)
			fmt.Fprintf(g.out, "%s:\n", failLabel)
			if err := g.deadlineJoinFailure(joinStatus, resultFields[i],
				g.callResultTextFor(seg.call), errSlot, after, i); err != nil {
				return err
			}
		}
	}
	for i := range info.segments {
		seg := &info.segments[i]
		fmt.Fprintf(g.out, "sm.after.%d:\n", i)
		terminated = false
		if seg.kind == "task" {
			g.smPlaces[seg.call.ID] = smPlace{ptr: resultFields[i], typ: g.resultLLVM(g.callResultTextFor(seg.call))}
			terminated, err = g.bodyStmts([]willc.Record{seg.stmt}, info.resultLLVM == "")
			delete(g.smPlaces, seg.call.ID)
			if err != nil {
				return err
			}
		}
		if terminated {
			continue
		}
		if i+1 < len(info.segments) {
			terminated, err = g.bodyStmts(info.segments[i+1].before, info.resultLLVM == "")
			if err != nil {
				return err
			}
			if !terminated {
				fmt.Fprintf(g.out, "  br label %%sm.start.%d\n", i+1)
			}
			continue
		}
		terminated, err = g.bodyStmts(info.tail, info.resultLLVM == "")
		if err != nil {
			return err
		}
		if !terminated {
			if info.resultLLVM == "" {
				g.out.WriteString("  ret i32 1\n")
			} else {
				g.out.WriteString("  unreachable\n")
			}
		}
	}
	g.smMode = false
	g.flushLateAllocas()
	g.out.WriteString("}\n")
	borrowDrop := g.asyncFrameDropSymbol(info, false)
	ownDrop := g.asyncFrameDropSymbol(info, true)
	if borrowDrop != "" {
		if err := g.emitAsyncFrameDrop(info, borrowDrop, false); err != nil {
			return err
		}
	}
	if ownDrop != "" && ownDrop != borrowDrop {
		if err := g.emitAsyncFrameDrop(info, ownDrop, true); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) callResultTextFor(call willc.Record) string {
	if text, err := g.callResultText(call); err == nil {
		return text
	}
	return ""
}

func (g *generator) emitAsyncStart(info *asyncMachineInfo, seg *asyncSegment, index int, state, op, statusField, resultField string) error {
	if seg.kind == "c" {
		parts, argTypes, err := g.asyncStartArgs(seg.call)
		if err != nil {
			return err
		}
		g.queueCDecl(seg.startName, "ptr", argTypes)
		g.queueCDecl(seg.pollName, "i32", []string{"ptr"})
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @%s(%s)\n",
			value, llvmSymbol(seg.startName), strings.Join(parts, ", "))
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", value, op)
		started := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp ne ptr %s, null\n", started, value)
		failed := fmt.Sprintf("sm.start.failed.%d", index)
		ok := fmt.Sprintf("sm.start.ok.%d", index)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", started, ok, failed)
		fmt.Fprintf(g.out, "%s:\n", failed)
		fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", statusField)
		fmt.Fprintf(g.out, "  br label %%sm.after.%d\n", index)
		fmt.Fprintf(g.out, "%s:\n", ok)
		// An operation that completed before the first park has no completer
		// to wake the task, so the machine polls before it parks. Only a
		// pending poll reaches the wait below.
		polled := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @%s(ptr %s)\n", polled,
			llvmSymbol(seg.pollName), g.load("ptr", op))
		done := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp ne i32 %s, 0\n", done, polled)
		fmt.Fprintf(g.out, "  br i1 %s, label %%sm.finish.%d, label %%sm.start.wait.%d\n",
			done, index, index)
		fmt.Fprintf(g.out, "sm.start.wait.%d:\n", index)
	} else {
		child, err := g.emitConstructorCall(seg.call, seg.targetSym, "-2", false)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", child, op)
	}
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", index+1, state)
	if seg.kind == "c" {
		g.emitSequenceWaitDeadline(op, seg.deadline)
	}
	g.needIOMarkDecl = true
	g.out.WriteString("  call void @willrt_task_mark_io()\n")
	g.out.WriteString("  call void @willrt_task_mark_wait()\n  ret i32 -1\n")
	return nil
}

// emitSequenceWaitDeadline names the next park deadline for one segment. The
// deadline entry belongs to the segment, not the machine, because a sequence
// can mix sleeps and poller waits.
func (g *generator) emitSequenceWaitDeadline(op string, deadlineName string) {
	if deadlineName == "" {
		return
	}
	deadline := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @%s(ptr %s)\n",
		deadline, llvmSymbol(deadlineName), g.load("ptr", op))
	fmt.Fprintf(g.out, "  call void @willrt_task_wait_until(i64 %s)\n", deadline)
}
