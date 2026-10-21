package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// deferSnapshot names the frame field that holds one expression captured at
// the defer statement. The same record drives value and place while a thunk
// is lowered, so the call sees the captured value instead of the expression.
type deferSnapshot struct {
	frame     string
	frameType string
	index     int
	llvm      string
}

// deferCapture is the capture-time view of one frame field.
type deferCapture struct {
	id      int
	index   int
	llvm    string
	closure bool
	release bool
}

// deferCall is one deferred call from the current block. A call with a frame
// runs its thunk at block exit; a call without a thunk is the old direct path.
type deferCall struct {
	call      int
	frame     string
	frameType string
	thunk     string
	fields    []deferCapture
}

// deferRequest is the body of one thunk. It is queued while a function is
// lowered and written after all functions of the document exist.
type deferRequest struct {
	name      string
	call      int
	frameType string
	fields    []deferCapture
}

// deferStmt captures the call's values now and queues the call itself for the
// end of the block. A defer inside a loop therefore runs once per iteration
// with the value that iteration captured.
func (g *generator) deferStmt(st willc.Record) error {
	callID := refField(st, "call")
	call, ok := g.recs[callID]
	if !ok || (call.Kind != "call" && call.Kind != "mcall") {
		return g.errf(st.ID, "cannot translate node #%d: defer needs one call", st.ID)
	}
	if len(g.defers) == 0 {
		return g.errf(st.ID, "cannot translate node #%d: defer has no block", st.ID)
	}
	// A blocking C link queues a task step while it is lowered. That step has
	// to be emitted before the document ends, so keep this rare shape on the
	// direct path instead of moving it into a thunk written afterwards.
	if call.Kind == "mcall" && g.isCLink(call) &&
		g.linkBlock[fieldText(call, "text")] {
		last := len(g.defers) - 1
		g.defers[last] = append(g.defers[last], deferCall{call: callID})
		return nil
	}
	deferred, request, err := g.captureDefer(st, call)
	if err != nil {
		return err
	}
	last := len(g.defers) - 1
	g.defers[last] = append(g.defers[last], deferred)
	g.pendingDefers = append(g.pendingDefers, request)
	return nil
}

// captureDefer evaluates the receiver, an indirect callee, and every argument
// once, stores them in a heap frame, and returns the call plus the thunk body.
func (g *generator) captureDefer(st willc.Record, call willc.Record) (deferCall, deferRequest, error) {
	var ids []int
	closures := map[int]bool{}
	add := func(id int) {
		if id == 0 {
			return
		}
		for _, have := range ids {
			if have == id {
				return
			}
		}
		ids = append(ids, id)
	}
	if call.Kind == "mcall" && !g.isCLink(call) {
		add(refField(call, "recv"))
	}
	if target := refField(call, "to"); target != 0 {
		if decl, ok := g.recs[target]; ok && g.isFuncValue(decl) {
			add(target)
			closures[target] = true
		}
	}
	for _, arg := range g.callArgs(call) {
		add(arg)
	}

	types := make([]string, 0, len(ids))
	for _, id := range ids {
		typ := ""
		var err error
		if closures[id] {
			typ = "{ ptr, ptr }"
		} else if r, ok := g.recs[id]; ok && (r.Kind == "var" || r.Kind == "param") {
			typ, err = g.declLLVMType(id)
		} else {
			typ, err = g.typeOf(id)
		}
		if err != nil {
			return deferCall{}, deferRequest{}, err
		}
		if typ == "" {
			return deferCall{}, deferRequest{}, g.errf(st.ID, "cannot translate node #%d: a defer argument has no type", st.ID)
		}
		types = append(types, typ)
	}
	frameType := "{}"
	frame := "null"
	if len(types) > 0 {
		frameType = "{ " + strings.Join(types, ", ") + " }"
		size := g.sizeOf(frameType)
		g.needAllocDecl = true
		frame = g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", frame, size)
	}

	fields := make([]deferCapture, 0, len(ids))
	for i, id := range ids {
		typ := types[i]
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 %d\n",
			field, frameType, frame, i)
		capture := deferCapture{id: id, index: i, llvm: typ, closure: closures[id]}
		if capture.closure {
			decl := g.recs[id]
			code, env, err := g.closureCode(decl)
			if err != nil {
				return deferCall{}, deferRequest{}, err
			}
			codeField := g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codeField, field)
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", code, codeField)
			envField := g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envField, field)
			fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", env, envField)
		} else if isAggregateType(typ) {
			src, srcType, err := g.place(id)
			if err != nil {
				return deferCall{}, deferRequest{}, err
			}
			if srcType != typ {
				return deferCall{}, deferRequest{}, g.errf(id, "cannot translate node #%d: the defer argument needs %s", id, typ)
			}
			g.copyValue(src, field, typ)
		} else {
			value, err := g.value(id)
			if err != nil {
				return deferCall{}, deferRequest{}, err
			}
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, field)
			if text, err := g.willTypeText(id); err == nil && isRcText(text) && !isWeakText(text) {
				// The source may be reassigned or released before the block
				// exits, so the frame owns one reference of its own.
				g.needRcDecl = true
				fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
				capture.release = true
			}
		}
		fields = append(fields, capture)
	}

	name := qualify(g.pkg, "defer."+strconv.Itoa(g.deferSeq))
	g.deferSeq++
	return deferCall{call: call.ID, frame: frame, frameType: frameType, thunk: name, fields: fields},
		deferRequest{name: name, call: call.ID, frameType: frameType, fields: fields}, nil
}

// emitDeferThunks writes every thunk queued by the current document.
func (g *generator) emitDeferThunks() error {
	requests := g.pendingDefers
	g.pendingDefers = nil
	for _, request := range requests {
		if err := g.emitDeferThunk(request); err != nil {
			return err
		}
	}
	return nil
}

// emitDeferThunk lowers the deferred call in its own function. The call is
// lowered once, against frame loads, so every argument kind the normal call
// path supports keeps working.
func (g *generator) emitDeferThunk(request deferRequest) error {
	call, ok := g.recs[request.call]
	if !ok {
		return g.errf(request.call, "cannot translate node #%d: the deferred call is missing", request.call)
	}
	savedOut := g.out
	savedTmp := g.tmp
	savedLabels := g.labels
	savedSnapshots := g.deferSnapshots
	savedFnSymbol := g.fnSymbol
	savedFnResult := g.fnResultText
	savedDeferThunk := g.deferThunk
	savedStrMethodSlots := g.strMethodSlots
	savedTrimStartSlots := g.trimStartSlots
	savedTrimLenSlots := g.trimLenSlots
	savedCallSlots := g.callSlots
	savedNewSlots := g.newSlots
	savedAggSlots := g.aggSlots
	savedStrSlots := g.strSlots
	savedEnumSlots := g.enumSlots
	savedADTSlots := g.adtSlots
	savedSliceSlots := g.sliceSlots
	savedStrCatSlots := g.strCatSlots
	savedInterpSlots := g.interpSlots

	g.out = &strings.Builder{}
	g.tmp = 0
	g.labels = 0
	g.fnSymbol = request.name
	g.fnResultText = ""
	g.deferThunk = true
	g.deferSnapshots = map[int]deferSnapshot{}
	// Results built by a method call belong to the thunk, not to the caller's
	// entry block. A fresh map makes the existing allocSlot path emit its
	// storage inside the thunk body.
	g.strMethodSlots = map[int]string{}
	g.trimStartSlots = map[int]string{}
	g.trimLenSlots = map[int]string{}
	g.callSlots = map[int]string{}
	g.newSlots = map[int]string{}
	g.aggSlots = map[int]string{}
	g.strSlots = map[int]string{}
	g.enumSlots = map[int]string{}
	g.adtSlots = map[int]string{}
	g.sliceSlots = map[int]string{}
	g.strCatSlots = map[int]string{}
	g.interpSlots = map[int]string{}
	for _, field := range request.fields {
		g.deferSnapshots[field.id] = deferSnapshot{
			frame:     "%frame",
			frameType: request.frameType,
			index:     field.index,
			llvm:      field.llvm,
		}
	}
	fmt.Fprintf(g.out, "define void @%s(ptr %%frame) {\nentry:\n", llvmSymbol(request.name))
	_, _, err := g.callExpr(call)
	if err == nil {
		g.out.WriteString("  ret void\n}\n")
	}

	body := g.out
	g.out = savedOut
	g.tmp = savedTmp
	g.labels = savedLabels
	g.deferSnapshots = savedSnapshots
	g.fnSymbol = savedFnSymbol
	g.fnResultText = savedFnResult
	g.deferThunk = savedDeferThunk
	g.strMethodSlots = savedStrMethodSlots
	g.trimStartSlots = savedTrimStartSlots
	g.trimLenSlots = savedTrimLenSlots
	g.callSlots = savedCallSlots
	g.newSlots = savedNewSlots
	g.aggSlots = savedAggSlots
	g.strSlots = savedStrSlots
	g.enumSlots = savedEnumSlots
	g.adtSlots = savedADTSlots
	g.sliceSlots = savedSliceSlots
	g.strCatSlots = savedStrCatSlots
	g.interpSlots = savedInterpSlots
	if err != nil {
		return err
	}
	savedOut.WriteString(body.String())
	return nil
}

// deferFieldPtr returns the address of one captured field inside the thunk's
// frame.
func (g *generator) deferFieldPtr(s deferSnapshot) string {
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i32 0, i32 %d\n",
		ptr, s.frameType, s.frame, s.index)
	return ptr
}

// runDefers runs every deferred call from the innermost frame down to lowest,
// each frame last in first out. The frames stay on the compile-time stack, so
// another branch of the same block still runs them on its path.
func (g *generator) runDefers(lowest int) error {
	for i := len(g.defers) - 1; i >= lowest; i-- {
		frame := g.defers[i]
		for j := len(frame) - 1; j >= 0; j-- {
			if err := g.lowerDeferredCall(frame[j]); err != nil {
				return err
			}
		}
	}
	return nil
}

// lowerDeferredCall runs one captured call and releases the frame's owned
// references. A direct call record without a thunk is the old fallback.
func (g *generator) lowerDeferredCall(call deferCall) error {
	if call.thunk == "" {
		return g.lowerDirectDeferredCall(call.call)
	}
	fmt.Fprintf(g.out, "  call void @%s(ptr %s)\n", llvmSymbol(call.thunk), call.frame)
	for _, field := range call.fields {
		if !field.release {
			continue
		}
		g.needRcDecl = true
		ptr := g.deferFieldPtr(deferSnapshot{
			frame:     call.frame,
			frameType: call.frameType,
			index:     field.index,
			llvm:      field.llvm,
		})
		value := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", value)
	}
	if call.frame != "null" {
		g.needAllocDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_free(ptr %s)\n", call.frame)
	}
	return nil
}

// lowerDirectDeferredCall is the pre-snapshot path. It remains for a call
// shape whose values could not be captured.
func (g *generator) lowerDirectDeferredCall(id int) error {
	r, ok := g.recs[id]
	if !ok {
		return g.errf(id, "cannot translate node #%d: the deferred call is missing", id)
	}
	_, _, err := g.callExpr(r)
	return err
}

// containsTry reports whether a statement subtree holds a ? expression. The
// release group before such a statement belongs to the error path.
func (g *generator) containsTry(id int) bool {
	for _, child := range g.children(id) {
		if child.Kind == "try" || g.containsTry(child.ID) {
			return true
		}
	}
	return false
}

// flushTryReleases runs the release group of a failing ? expression.
func (g *generator) flushTryReleases() error {
	pending := g.pendingTryReleases
	g.pendingTryReleases = nil
	for _, rel := range pending {
		if err := g.releaseRecord(rel); err != nil {
			return err
		}
	}
	return nil
}
