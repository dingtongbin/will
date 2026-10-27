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

func isFuncText(text string) bool {
	return strings.HasPrefix(text, "func(")
}

// constructRc builds new rc<T>(value): the value is copied into the handle
// table's object storage and the u64 handle is returned.
func (g *generator) constructRc(r willc.Record, elem string) (string, string, error) {
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: rc value %s is not lowered yet", r.ID, elem)
	}
	var source string
	if g.hasInit(elem) {
		// new rc<T>(args) constructs T first, exactly as new T(args).
		inner := r
		found := false
		for i := range inner.Fields {
			if inner.Fields[i].Name == "type" {
				inner.Fields[i].Value = willc.Text(elem)
				found = true
			}
		}
		if !found {
			inner.Fields = append(inner.Fields, willc.Field{Name: "type", Value: willc.Text(elem)})
		}
		slot, typ, err := g.constructNew(inner)
		if err != nil {
			return "", "", err
		}
		if typ != elemLLVM {
			return "", "", g.errf(r.ID, "cannot translate node #%d: the value needs %s", r.ID, elemLLVM)
		}
		source = slot
	} else {
		args := g.callArgs(r)
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: rc takes one value", r.ID)
		}
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

// hasInit reports whether T has an init method in this or another document.
func (g *generator) hasInit(elem string) bool {
	if g.cur.methods[elem+".init"] != "" {
		return true
	}
	if g.methodsQualified[elem+".init"] != "" {
		return true
	}
	canonical := g.canonicalTypeText(elem)
	if g.methodsQualified[canonical+".init"] != "" {
		return true
	}
	return g.methodsQualified[qualify(g.pkg, elem)+".init"] != ""
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

// dropValue releases everything one value owns at ptr. It is the same walk as
// an rc release, including Array elements and the block itself.
func (g *generator) dropValue(ptr, text string, depth int) error {
	return g.releaseValue(ptr, text, depth)
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
	case "weak":
		g.needRcDecl = true
		weak := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i64 @willrt_weak_new(i64 %s)\n", weak, handle)
		return weak, "weak<" + elem + ">", nil
	case "clone":
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
		return "", "", g.errf(r.ID, "cannot translate node #%d: upgrade needs a weak handle", r.ID)
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
		if fieldText(r, "text") == "upgrade" {
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
	case "weak":
		return "weak<" + elem + ">", true
	case "clone":
		return recvText, true
	}
	sym := g.methodForType(elem, fieldText(r, "text"))
	if sym == "" {
		return "", false
	}
	return g.sigs[sym], true
}

// isCopySource reports whether a value is an already owned place, so copying it
// must add references for the rc handles it holds. A fresh object, call result,
// or unwrap transfers its references instead.
func (g *generator) isCopySource(id int) bool {
	r, ok := g.recs[id]
	if !ok {
		return false
	}
	switch r.Kind {
	case "name", "field", "index":
		text, err := g.willTypeText(id)
		return err == nil && g.needsReleaseText(text)
	case "await":
		return g.isCopySource(refField(r, "expr"))
	case "call":
		if arg, ok := g.waitArg(r); ok {
			return g.isCopySource(arg)
		}
		return false
	case "mcall":
		// Array.Get and view Get return a borrow of the element. The array
		// still owns its reference, so storing the result adds one.
		if fieldText(r, "text") != "get" {
			return false
		}
		recvText, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return false
		}
		recvText = stripPredeclaredPackage(recvText)
		elem, ok := arrayElemText(recvText)
		if !ok {
			elem, ok = viewElemText(recvText)
		}
		return ok && g.needsReleaseText(elem)
	}
	return false
}

// retainValue adds one reference for every rc handle reachable in a value. It
// is the copy half of the deep-copy rule for structs and Options.
func (g *generator) retainValue(ptr, text string) error {
	return g.retainValueDepth(ptr, text, 0)
}

func (g *generator) retainValueDepth(ptr, text string, depth int) error {
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
		fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", handle)
		return nil
	}
	if isFuncText(text) {
		envPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envPtr, ptr)
		env := g.load("ptr", envPtr)
		g.needClosureDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_closure_retain(ptr %s)\n", env)
		return nil
	}
	if isChanText(text) {
		g.needTaskDecl = true
		g.needChanDecl = true
		handle := g.load("ptr", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_chan_retain(ptr %s)\n", handle)
		return nil
	}
	if strings.HasPrefix(text, "Array<") && strings.HasSuffix(text, ">") {
		elem := strings.TrimSpace(text[len("Array<") : len(text)-1])
		if !needsReleaseText(g, elem) {
			return nil
		}
		return g.eachElement(ptr, elem, true, 0)
	}
	if kind, args, ok := parseADT(text); ok && len(args) != 0 {
		info, ok := g.adtType(text)
		if !ok {
			return nil
		}
		tag := g.load("i32", ptr)
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("retain.keep.%d", n)
		done := fmt.Sprintf("retain.done.%d", n)
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
		if err := g.retainValueDepth(payload, args[0], depth+1); err != nil {
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
		if err := g.retainValueDepth(field, ft, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// eachElement walks the live elements of an Array and retains or releases each
// one. ptr points at the three-word Array value.
func (g *generator) eachElement(ptr, elem string, retain bool, depth int) error {
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	length := g.load("i64", lenPtr)
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return nil
	}
	g.labels++
	n := g.labels
	cond := fmt.Sprintf("elem.cond.%d", n)
	body := fmt.Sprintf("elem.body.%d", n)
	done := fmt.Sprintf("elem.done.%d", n)
	index := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", index)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(cond + ":\n")
	at := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, at, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, body, done)
	g.out.WriteString(body + ":\n")
	element := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", element, elemLLVM, data, at)
	var err error
	if retain {
		err = g.retainValueDepth(element, elem, depth+1)
	} else {
		err = g.releaseValue(element, elem, depth+1)
	}
	if err != nil {
		return err
	}
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, at)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", cond)
	g.out.WriteString(done + ":\n")
	return nil
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
	if _, ok := g.guardElemText(text); ok {
		if err := g.guardUnlock(slot, text); err != nil {
			return err
		}
	}
	return g.releaseOwnedValue(slot, text)
}

// ownsReleaseText reports whether a value can own a buffer or handle that the
// scope must release. It is wider than needsReleaseText: the latter tracks only
// reference-counted fields, while an Array<i32> still owns its data buffer.
func enumNeedsRelease(g *generator, info *enumInfo) bool {
	if info == nil {
		return false
	}
	for i := range info.variants {
		for _, arg := range info.variants[i].args {
			if ownsReleaseText(g, arg) {
				return true
			}
		}
	}
	return false
}

func ownsReleaseText(g *generator, text string) bool {
	text = g.qualifyTypeText(g.pkg, text)
	if text == "" || isWeakText(text) {
		return false
	}
	if info := g.enumInfoForText(text); info != nil {
		return enumNeedsRelease(g, info)
	}
	if info := g.enumInfoForText(qualify(g.pkg, text)); info != nil {
		return enumNeedsRelease(g, info)
	}
	if text == "str" || isRcText(text) || isFuncText(text) || isChanText(text) {
		return true
	}
	if _, ok := g.ifaceOfText(text); ok {
		return true
	}
	if _, ok := arrayElemText(text); ok {
		return true
	}
	if _, elem, ok := arrayTypeText(text); ok {
		return ownsReleaseText(g, elem)
	}
	if _, _, ok := mapTypeText(text); ok {
		return true
	}
	if _, ok := setTypeText(text); ok {
		return true
	}
	if _, args, ok := parseADT(text); ok {
		return len(args) != 0
	}
	if _, ok := g.guardElemText(text); ok {
		return true
	}
	typ, ok := g.typeLLVMOf(text)
	if !ok || !isStructType(typ) {
		return false
	}
	info := g.structInfoByLLVM(typ)
	if info == nil || !info.ok {
		return false
	}
	for _, field := range info.texts {
		if g.fieldNeedsClone(field) {
			return true
		}
	}
	return false
}

// needsReleaseText is the method form used by the call sites.
func (g *generator) needsReleaseText(text string) bool {
	return needsReleaseText(g, text)
}

// needsReleaseText reports whether a field type may hold an rc reference.
func needsReleaseText(g *generator, text string) bool {
	text = g.qualifyTypeText(g.pkg, text)
	if isRcText(text) {
		return true
	}
	if isWeakText(text) {
		return false
	}
	if isFuncText(text) || isChanText(text) {
		return true
	}
	if _, ok := g.ifaceOfText(text); ok {
		return true
	}
	if strings.HasPrefix(text, "Array<") && strings.HasSuffix(text, ">") {
		return needsReleaseText(g, strings.TrimSpace(text[len("Array<"):len(text)-1]))
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
