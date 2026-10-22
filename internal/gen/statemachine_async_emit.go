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
	parts := make([]string, 0, len(args)+1)
	paramTypes := append([]string(nil), g.paramTypes[targetSym]...)
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
