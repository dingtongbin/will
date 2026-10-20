package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// rcElemText returns T from rc<T> or weak<T>.
func rcElemText(text string) (string, bool) {
	for _, prefix := range []string{"rc<", "weak<"} {
		if strings.HasPrefix(text, prefix) && strings.HasSuffix(text, ">") {
			return strings.TrimSpace(text[len(prefix) : len(text)-1]), true
		}
	}
	return "", false
}

// isRcText reports whether a type text is rc<T>.
func isRcText(text string) bool {
	return strings.HasPrefix(text, "rc<") && strings.HasSuffix(text, ">")
}

// isWeakText reports whether a type text is weak<T>.
func isWeakText(text string) bool {
	return strings.HasPrefix(text, "weak<") && strings.HasSuffix(text, ">")
}

// constructRc builds new rc<T>(value): the value is copied into the handle
// table's object storage and the u64 handle is returned.
func (g *generator) constructRc(r willc.Record, elem string) (string, string, error) {
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: rc takes one value", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: rc value %s is not lowered yet", r.ID, elem)
	}
	var source string
	if isAggregateType(elemLLVM) {
		ptr, typ, err := g.place(args[0])
		if err != nil {
			return "", "", err
		}
		if typ != elemLLVM {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		source = ptr
	} else {
		value, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		source = g.allocSlot(g.callSlots, "rc", r.ID, elemLLVM)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, source)
	}
	size := g.sizeOf(elemLLVM)
	g.needRcDecl = true
	fn := "willrt_rc_new"
	callArgs := fmt.Sprintf("ptr %s, i64 %s", source, size)
	if needsReleaseText(g, elem) {
		drop := g.dropFor(elem)
		fn = "willrt_rc_new_drop"
		callArgs += ", ptr @" + llvmSymbol(drop)
	}
	handle := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @%s(%s)\n", handle, fn, callArgs)
	return handle, "rc<" + elem + ">", nil
}

// dropRequest is one generated function that releases the rc references held
// inside an object before the handle table frees its storage.
type dropRequest struct {
	name string
	text string
}

// dropFor returns the drop function of one value type, queuing it once.
func (g *generator) dropFor(text string) string {
	text = g.qualifyTypeText(g.pkg, text)
	if name := g.dropNames[text]; name != "" {
		return name
	}
	g.dropSeq++
	name := qualify(g.pkg, "drop."+strconv.Itoa(g.dropSeq))
	g.dropNames[text] = name
	g.pendingDrops = append(g.pendingDrops, dropRequest{name: name, text: text})
	return name
}

// emitDrops writes the drop functions queued by this document.
func (g *generator) emitDrops() error {
	for _, req := range g.pendingDrops {
		if g.dropEmitted[req.name] {
			continue
		}
		g.dropEmitted[req.name] = true
		fmt.Fprintf(g.out, "define void @%s(ptr %%value) {\nentry:\n", llvmSymbol(req.name))
		if err := g.dropValue("%value", req.text, 0); err != nil {
			return err
		}
		g.out.WriteString("  ret void\n}\n")
	}
	g.pendingDrops = nil
	return nil
}

// dropValue releases the rc references inside one value at ptr.
func (g *generator) dropValue(ptr, text string, depth int) error {
	if depth > 32 {
		return nil
	}
	text = g.qualifyTypeText(g.pkg, text)
	if isWeakText(text) {
		return nil
	}
	if isRcText(text) {
		g.needRcDecl = true
		handle := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", handle)
		return nil
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		info, ok := g.adtType(text)
		if !ok {
			return nil
		}
		tag := g.load("i32", ptr)
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("drop.keep.%d", n)
		done := fmt.Sprintf("drop.done.%d", n)
		cond := g.newTmp()
		if kind == "option" {
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 1\n", cond, tag)
		} else {
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", cond, tag)
		}
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, ptr, info.payloadIndex)
		if err := g.dropValue(payload, args[0], depth+1); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
		return nil
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return nil
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return nil
	}
	for i, ft := range info.texts {
		if !needsReleaseText(g, ft) {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, info.llvm, ptr, i)
		if err := g.dropValue(field, ft, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// rcMethodCall lowers Weak, Clone, and every method reached through the handle.
// A method of T receives the dereferenced object pointer; the check is O(1).
func (g *generator) rcMethodCall(r willc.Record, recvText string) (string, string, error) {
	elem, ok := rcElemText(recvText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not rc<T>", r.ID)
	}
	handle, err := g.value(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	switch fieldText(r, "text") {
	case "Weak":
		g.needRcDecl = true
		weak := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i64 @willrt_weak_new(i64 %s)\n", weak, handle)
		return weak, "weak<" + elem + ">", nil
	case "Clone":
		g.needRcDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", handle)
		return handle, recvText, nil
	}
	g.needRcDecl = true
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_rc_deref(i64 %s)\n", object, handle)
	symbol := g.methodForType(elem, fieldText(r, "text"))
	if symbol == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no method %s", r.ID, elem, fieldText(r, "text"))
	}
	return g.emitCallFrom(r, symbol, false, object)
}

// upgradeCall lowers weak<T>.Upgrade() to Option<rc<T>>.
func (g *generator) upgradeCall(r willc.Record, weakText string) (string, string, error) {
	elem, ok := rcElemText(weakText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Upgrade needs a weak handle", r.ID)
	}
	weak, err := g.value(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	text := "Option<rc<" + elem + ">>"
	info, ok := g.adtType(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, text)
	}
	out := g.weakOutSlots[r.ID]
	if out == "" {
		out = g.allocSlot(g.weakOutSlots, "weakout", r.ID, "i64")
	}
	g.needRcDecl = true
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_weak_upgrade(i64 %s, ptr %s)\n", status, weak, out)
	slot := g.allocSlot(g.callSlots, "ret", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	okStatus := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", okStatus, status)
	g.labels++
	n := g.labels
	someLabel := fmt.Sprintf("upgrade.some.%d", n)
	endLabel := fmt.Sprintf("upgrade.end.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", okStatus, someLabel, endLabel)
	g.out.WriteString(someLabel + ":\n")
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	value := g.load("i64", out)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", value, payload)
	fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	g.out.WriteString(endLabel + ":\n")
	return g.load(info.llvm, slot), text, nil
}

// methodForType resolves one method of a named receiver, whether the receiver
// belongs to this document or another one.
func (g *generator) methodForType(elem, name string) string {
	if sym := g.cur.methods[elem+"."+name]; sym != "" {
		return sym
	}
	if sym := g.methodsQualified[elem+"."+name]; sym != "" {
		return sym
	}
	if !strings.Contains(elem, ".") {
		if sym := g.methodsQualified[qualify(g.pkg, elem+"."+name)]; sym != "" {
			return sym
		}
	}
	return ""
}

// rcCallResult returns the result text of a method call through rc/weak.
func (g *generator) rcCallResult(r willc.Record) (string, bool) {
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", false
	}
	if isWeakText(recvText) {
		if fieldText(r, "text") == "Upgrade" {
			elem, _ := rcElemText(recvText)
			return "Option<rc<" + elem + ">>", true
		}
		return "", false
	}
	elem, ok := rcElemText(recvText)
	if !ok || !isRcText(recvText) {
		return "", false
	}
	switch fieldText(r, "text") {
	case "Weak":
		return "weak<" + elem + ">", true
	case "Clone":
		return recvText, true
	}
	sym := g.methodForType(elem, fieldText(r, "text"))
	if sym == "" {
		return "", false
	}
	return g.sigs[sym], true
}

// rcCopySource reports whether a value is an already owned handle, so assigning
// it must add a reference. A freshly returned handle or a new object transfers
// its reference instead.
func (g *generator) rcCopySource(id int) bool {
	r, ok := g.recs[id]
	if !ok {
		return false
	}
	switch r.Kind {
	case "name", "field", "index":
		text, err := g.willTypeText(id)
		return err == nil && isRcText(text)
	}
	return false
}

// releaseSlot releases the rc references a value holds. A weak handle and a
// value without rc fields release nothing.
func (g *generator) releaseSlot(slot, text string) error {
	return g.releaseValue(slot, text, 0)
}

func (g *generator) releaseValue(slot, text string, depth int) error {
	if depth > 32 {
		return nil
	}
	text = g.canonicalTypeText(text)
	if isWeakText(text) {
		return nil
	}
	if isRcText(text) {
		// The object's own fields are not walked here: the handle table owns
		// the object storage, and a T with strong fields needs drop glue.
		g.needRcDecl = true
		handle := g.load("i64", slot)
		fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", handle)
		return nil
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		tag := g.load("i32", slot)
		emptyTag := 0
		if kind == "result" {
			emptyTag = 1
		}
		_ = emptyTag
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("release.keep.%d", n)
		done := fmt.Sprintf("release.done.%d", n)
		if kind == "option" {
			// None has tag 0; Some has tag 1.
			isNone := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", isNone, tag)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isNone, done, keep)
		} else {
			// Only Ok carries the first payload.
			isOk := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", isOk, tag)
			fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", isOk, keep, done)
		}
		g.out.WriteString(keep + ":\n")
		info, _ := g.adtType(text)
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
		if err := g.releaseValue(payload, args[0], depth+1); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
		return nil
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return nil
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return nil
	}
	for i, ft := range info.texts {
		if !needsReleaseText(g, ft) {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, info.llvm, slot, i)
		if err := g.releaseValue(field, ft, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// needsReleaseText reports whether a field type may hold an rc reference.
func needsReleaseText(g *generator, text string) bool {
	if isRcText(text) {
		return true
	}
	if isWeakText(text) {
		return false
	}
	if _, args, ok := parseADT(text); ok {
		return len(args) != 0 && needsReleaseText(g, args[0])
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return false
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return false
	}
	for _, ft := range info.texts {
		if needsReleaseText(g, ft) {
			return true
		}
	}
	return false
}
