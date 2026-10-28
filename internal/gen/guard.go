package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// guardElemText returns T from std/sync.Guard<T> or std/sync.ReadGuard<T>.
// The package path is exact, so a user type named Guard in another package is
// not treated as the built-in guard.
func (g *generator) guardElemText(text string) (string, bool) {
	text = g.canonicalTypeText(text)
	for _, prefix := range []string{"std/sync.Guard<", "std/sync.ReadGuard<"} {
		if strings.HasPrefix(text, prefix) && strings.HasSuffix(text, ">") {
			inner := strings.TrimSpace(text[len(prefix) : len(text)-1])
			if inner != "" {
				return inner, true
			}
		}
	}
	return "", false
}

// guardReadOnly reports whether the guard type is the read side.
func (g *generator) guardReadOnly(text string) bool {
	text = g.canonicalTypeText(text)
	return strings.HasPrefix(text, "std/sync.ReadGuard<") &&
		strings.HasSuffix(text, ">")
}

// guardStateInfo resolves the MutexState<T> field layout behind one guard. The
// state field text is written in the defining package, so an unqualified name
// is completed with std/sync before the instance lookup.
func (g *generator) guardStateInfo(stateText string) (*structInfo, string, bool) {
	elem, ok := rcElemText(strings.TrimSpace(stateText))
	if !ok {
		return nil, "", false
	}
	if !strings.Contains(elem, ".") {
		elem = "std/sync." + elem
	}
	llvm, ok := g.fieldLLVMType(elem)
	if !ok {
		return nil, "", false
	}
	info := g.structInfoByLLVM(llvm)
	if info == nil || !info.ok {
		return nil, "", false
	}
	return info, info.llvm, true
}

// guardValue lowers the value one guard points at. It checks the held bit
// first: a canceled Lock returns an empty guard whose every use must panic
// with the fixed empty-handle message. The returned text is T.
func (g *generator) guardValue(recvID int, recvText string, r willc.Record) (string, string, string, error) {
	elem, ok := g.guardElemText(recvText)
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: %s is not a guard", recvID, recvText)
	}
	base, _, err := g.place(recvID)
	if err != nil {
		return "", "", "", err
	}
	guardLLVM, ok := g.typeLLVMOf(recvText)
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: guard %s is not lowered yet", recvID, recvText)
	}
	guardInfo := g.structInfoByLLVM(guardLLVM)
	if guardInfo == nil || !guardInfo.ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: guard %s has no fields", recvID, recvText)
	}
	heldIndex, ok := guardInfo.index["held"]
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: guard %s has no held field", recvID, recvText)
	}
	heldPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		heldPtr, guardLLVM, base, heldIndex)
	held := g.load("i1", heldPtr)
	notHeld := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i1 %s, false\n", notHeld, held)
	g.panicCheck(notHeld, "empty handle", r)

	stateIndex, ok := guardInfo.index["state"]
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: guard %s has no state field", recvID, recvText)
	}
	statePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		statePtr, guardLLVM, base, stateIndex)
	handle := g.load("i64", statePtr)
	g.needRcDecl = true
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_rc_deref(i64 %s)\n", object, handle)
	stateInfo, stateLLVM, ok := g.guardStateInfo(guardInfo.texts[stateIndex])
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: the guard state is not lowered yet", recvID)
	}
	valueIndex, ok := stateInfo.index["value"]
	if !ok {
		return "", "", "", g.errf(recvID, "cannot translate node #%d: the guard state has no value field", recvID)
	}
	valuePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		valuePtr, stateLLVM, object, valueIndex)
	return valuePtr, stateInfo.types[valueIndex], elem, nil
}

// guardFieldPlace forwards one field access to the guarded value. It reports
// false when the receiver is not a guard.
func (g *generator) guardFieldPlace(recvID int, field string, r willc.Record) (string, string, bool, error) {
	recvText, err := g.willTypeText(recvID)
	if err != nil {
		return "", "", false, err
	}
	if _, ok := g.guardElemText(recvText); !ok {
		return "", "", false, nil
	}
	valuePtr, elemLLVM, _, err := g.guardValue(recvID, recvText, r)
	if err != nil {
		return "", "", true, err
	}
	info := g.structInfoByLLVM(elemLLVM)
	if info == nil || !info.ok {
		return "", "", true, g.errf(r.ID, "cannot translate node #%d: %s has no fields", r.ID, elemLLVM)
	}
	index, ok := info.index[field]
	if !ok {
		return "", "", true, g.errf(r.ID, "cannot translate node #%d: %s has no field %s", r.ID, elemLLVM, field)
	}
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		ptr, elemLLVM, valuePtr, index)
	return ptr, info.types[index], true, nil
}

// guardMethodCall forwards a method call to the guarded value and returns its
// normal call value. The receiver is the value pointer, not the guard.
func (g *generator) guardMethodCall(r willc.Record) (string, string, bool, error) {
	recvID := refField(r, "recv")
	recvText, err := g.willTypeText(recvID)
	if err != nil {
		return "", "", false, err
	}
	elem, ok := g.guardElemText(recvText)
	if !ok {
		return "", "", false, nil
	}
	valuePtr, _, _, err := g.guardValue(recvID, recvText, r)
	if err != nil {
		return "", "", true, err
	}
	symbol := g.methodForType(elem, fieldText(r, "text"))
	if symbol == "" {
		return "", "", true, g.errf(r.ID, "cannot translate node #%d: %s has no method %s", r.ID, elem, fieldText(r, "text"))
	}
	value, text, err := g.emitCallFrom(r, symbol, false, valuePtr)
	return value, text, true, err
}

// guardUnlock emits the release half of a guard: when held is set, load the
// lock word and the mode and return the lock, then let the ordinary struct
// release drop the state reference. An empty guard only drops the reference.
func (g *generator) guardUnlock(slot, text string) error {
	guardLLVM, ok := g.typeLLVMOf(text)
	if !ok {
		return nil
	}
	guardInfo := g.structInfoByLLVM(guardLLVM)
	if guardInfo == nil || !guardInfo.ok {
		return nil
	}
	heldIndex, ok := guardInfo.index["held"]
	if !ok {
		return nil
	}
	heldPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		heldPtr, guardLLVM, slot, heldIndex)
	held := g.load("i1", heldPtr)
	g.labels++
	n := g.labels
	unlock := fmt.Sprintf("guard.unlock.%d", n)
	done := fmt.Sprintf("guard.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", held, unlock, done)
	g.out.WriteString(unlock + ":\n")
	stateIndex, ok := guardInfo.index["state"]
	if !ok {
		g.out.WriteString("  br label %" + done + "\n")
		g.out.WriteString(done + ":\n")
		return nil
	}
	statePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		statePtr, guardLLVM, slot, stateIndex)
	handle := g.load("i64", statePtr)
	g.needRcDecl = true
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_rc_deref(i64 %s)\n", object, handle)
	stateInfo, stateLLVM, ok := g.guardStateInfo(guardInfo.texts[stateIndex])
	if !ok {
		g.out.WriteString("  br label %" + done + "\n")
		g.out.WriteString(done + ":\n")
		return nil
	}
	lockIndex, ok := stateInfo.index["lock"]
	if !ok {
		g.out.WriteString("  br label %" + done + "\n")
		g.out.WriteString(done + ":\n")
		return nil
	}
	lockPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		lockPtr, stateLLVM, object, lockIndex)
	lock := g.load("ptr", lockPtr)
	modeIndex := guardInfo.index["mode"]
	modePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		modePtr, guardLLVM, slot, modeIndex)
	mode := g.load("i32", modePtr)
	g.queueCDecl("willrt_sync_unlock", "void", []string{"ptr", "i32"})
	fmt.Fprintf(g.out, "  call void @willrt_sync_unlock(ptr %s, i32 %s)\n", lock, mode)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return nil
}
