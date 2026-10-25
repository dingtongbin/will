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
	paramRecs := g.asyncCallRecs(info.fn.ID)
	pab := make([]struct {
		typ   string
		byPtr bool
		text  string
	}, 0, len(paramRecs))
	for i, p := range paramRecs {
		typ, byPtr, err := g.asyncParamABI(p)
		if err != nil {
			return err
		}
		if i == 0 && receiverOfIn(g.cur, info.fn.ID) != nil {
			byPtr = true
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
	// The constructor is hidden, not internal: a function of another package
	// waits on this task, so the object that owns the function must export
	// the entry that starts it. The last argument is -1 for auto placement and
	// -2 for the current lane; any other value is the declared lane index.
	parts = append(parts, "i32 %start")
	fmt.Fprintf(g.out, "define hidden ptr @%s(%s) {\nentry:\n",
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
		mut := fieldText(paramRecs[i], "mut") == "true"
		if pab[i].byPtr && mut {
			// A mutable borrow stays a pointer into the caller's place; the
			// callee writes the caller's value instead of a frame copy.
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", arg, field)
		} else if pab[i].byPtr {
			g.copyValue(arg, field, pab[i].typ)
		} else {
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", pab[i].typ, arg, field)
		}
		// The capture is the crossing whether the parameter moves by value
		// or by pointer: an rc handle lands in a frame another lane runs.
		share := field
		if pab[i].byPtr && mut {
			share = arg
		}
		if err := g.shareValue(share, pab[i].text); err != nil {
			return err
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
	auto := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %%start, -1\n", auto)
	here := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %%start, -2\n", here)
	laneValue := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i32 %s, i32 %%start\n",
		laneValue, here, lane)
	g.labels++
	n := g.labels
	autoLabel := fmt.Sprintf("sm.start.auto.%d", n)
	laneLabel := fmt.Sprintf("sm.start.lane.%d", n)
	doneLabel := fmt.Sprintf("sm.start.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", auto, autoLabel, laneLabel)
	fmt.Fprintf(g.out, "%s:\n", autoLabel)
	autoTask := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 0, i32 0)\n",
		autoTask, llvmSymbol(info.symbol), frame, size)
	g.needOwnFrameDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_task_own_frame(ptr %s)\n", autoTask)
	g.needAutoTaskDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_task_run_auto(ptr %s)\n", autoTask)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	fmt.Fprintf(g.out, "%s:\n", laneLabel)
	laneTask := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_task_new(ptr @%s, ptr %s, i64 %s, i32 %s, i32 0)\n",
		laneTask, llvmSymbol(info.symbol), frame, size, laneValue)
	g.needOwnFrameDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_task_own_frame(ptr %s)\n", laneTask)
	fmt.Fprintf(g.out, "  call void @willrt_task_run(ptr %s)\n", laneTask)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	fmt.Fprintf(g.out, "%s:\n", doneLabel)
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi ptr [ %s, %%%s ], [ %s, %%%s ]\n",
		task, autoTask, autoLabel, laneTask, laneLabel)
	fmt.Fprintf(g.out, "  ret ptr %s\n}\n", task)
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

func (g *generator) localSymbol(sym string) bool {
	for _, name := range g.cur.funcs {
		if name == sym {
			return true
		}
	}
	for _, name := range g.cur.methods {
		if name == sym {
			return true
		}
	}
	return false
}

// emitConstructorCall builds the child frame by calling the target's task
// constructor. The constructor owns the frame layout, so this document does not
// need the target's local fields.
func (g *generator) emitConstructorCall(call willc.Record, targetSym string,
	start string) (string, error) {
	if targetSym == "" {
		return "", g.errf(call.ID, "cannot translate node #%d: the suspendable call has no target", call.ID)
	}
	args := g.callArgs(call)
	parts := make([]string, 0, len(args)+1)
	paramTypes := append([]string(nil), g.paramTypes[targetSym]...)
	paramMuts := g.paramMut[targetSym]
	if len(paramTypes) != len(args) {
		return "", g.errf(call.ID, "cannot translate node #%d: %s needs %d arguments", call.ID, targetSym, len(paramTypes))
	}
	declParams := make([]string, 0, len(args)+1)
	if g.methodSymbols[targetSym] {
		recv, _, err := g.place(refField(call, "recv"))
		if err != nil {
			return "", err
		}
		parts = append(parts, "ptr "+recv)
		declParams = append(declParams, "ptr")
	}
	for i, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return "", err
		}
		mut := i < len(paramMuts) && paramMuts[i]
		if isAggregateType(typ) || mut {
			ptr, ptrType, err := g.place(a)
			if err != nil {
				return "", err
			}
			if ptrType != typ {
				return "", g.errf(a, "cannot translate node #%d: the argument needs %s", a, typ)
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
	parts = append(parts, "i32 "+start)
	declParams = append(declParams, "i32")
	if !g.taskCtorDefined(targetSym) {
		g.queueCDecl(targetSym+".task", "ptr", declParams)
	}
	g.needTaskDecl = true
	task := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @%s(%s)\n",
		task, llvmSymbol(targetSym+".task"), strings.Join(parts, ", "))
	return task, nil
}

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
