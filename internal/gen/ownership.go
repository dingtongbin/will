package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

func isFuncText(text string) bool {
	return strings.HasPrefix(text, "func(")
}

// predeclaredCallName returns the builtin name a call names. The callee is a
// name record in the same document.
func (g *generator) predeclaredCallName(r willc.Record) (string, bool) {
	if r.Kind != "call" {
		return "", false
	}
	callee, ok := g.recs[refField(r, "to")]
	if !ok || callee.Kind != "name" {
		return "", false
	}
	return fieldText(callee, "text"), true
}

// dropRequest is one generated function that releases the owning values held
// inside an object before its storage is freed.
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

// dropValue releases everything one value owns at ptr.
func (g *generator) dropValue(ptr, text string, depth int) error {
	return g.releaseValue(ptr, text, depth)
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

// isCopySource reports whether a value is an already owned place, so copying it
// must add references for the owning fields it holds. A fresh object, call
// result, or unwrap transfers its references instead.
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
		return false
	case "mcall":
		return false
	}
	return false
}

// retainValue adds one reference for every owning field reachable in a value.
// It is the copy half of the deep-copy rule for structs and Options.
func (g *generator) retainValue(ptr, text string) error {
	return g.retainValueDepth(ptr, text, 0)
}

func (g *generator) retainValueDepth(ptr, text string, depth int) error {
	if depth > 32 {
		return nil
	}
	text = g.qualifyTypeText(g.pkg, text)
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

// releaseSlot releases the owning values a slot holds.
func (g *generator) releaseSlot(slot, text string) error {
	return g.releaseValue(slot, text, 0)
}

func (g *generator) releaseValue(slot, text string, depth int) error {
	if depth > 32 {
		return nil
	}
	text = g.canonicalTypeText(text)
	if _, ok := g.guardElemText(text); ok {
		if err := g.guardUnlock(slot, text); err != nil {
			return err
		}
	}
	return g.releaseOwnedValue(slot, text)
}

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
	if text == "" {
		return false
	}
	if info := g.enumInfoForText(text); info != nil {
		return enumNeedsRelease(g, info)
	}
	if info := g.enumInfoForText(qualify(g.pkg, text)); info != nil {
		return enumNeedsRelease(g, info)
	}
	if text == "str" || isFuncText(text) || isChanText(text) {
		return true
	}
	if _, ok := g.ifaceOfText(text); ok {
		return true
	}
	if _, elem, ok := arrayTypeText(text); ok {
		return ownsReleaseText(g, elem)
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

// needsReleaseText reports whether a field type owns a buffer or handle.
func needsReleaseText(g *generator, text string) bool {
	text = g.qualifyTypeText(g.pkg, text)
	if isFuncText(text) || isChanText(text) {
		return true
	}
	if _, ok := g.ifaceOfText(text); ok {
		return true
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
