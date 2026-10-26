package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// adtInfo is the lowered form of one Option<T> or Result<T,E>. The layout is
// the enum rule: a u32 tag first, then the union of the payload types.
type adtInfo struct {
	kind    string
	text    string
	args    []string
	argLLVM []string
	llvm    string
	// payloadIndex is the field that holds the payload, and payloadSize and
	// payloadAlign size the union. A payload of size zero has no field.
	payloadIndex int
	payloadSize  int
	payloadAlign int
	size         int
	align        int
	tags         map[string]uint32
}

// parseADT splits an Option or Result type text into its arguments.
func parseADT(text string) (string, []string, bool) {
	switch {
	case strings.HasPrefix(text, "Option<") && strings.HasSuffix(text, ">"):
		args := splitTypeList(text[len("Option<") : len(text)-1])
		if len(args) != 1 {
			return "", nil, false
		}
		return "option", []string{strings.TrimSpace(args[0])}, true
	case strings.HasPrefix(text, "Result<") && strings.HasSuffix(text, ">"):
		args := splitTypeList(text[len("Result<") : len(text)-1])
		if len(args) != 2 {
			return "", nil, false
		}
		return "result", []string{strings.TrimSpace(args[0]), strings.TrimSpace(args[1])}, true
	}
	return "", nil, false
}

// typeLLVMOf maps a type text used as an Option or Result argument to its LLVM
// type. The unit type has no value and returns "".
func (g *generator) typeLLVMOf(text string) (string, bool) {
	text = g.canonicalTypeText(text)
	if text == "interface {}" {
		if g.emptyIface == nil {
			g.emptyIface = &ifaceInfo{name: "interface {}", llvm: "%interface.empty", byName: map[string]int{}}
		}
		return "%interface.empty", true
	}
	if _, ok := chanElemText(text); ok {
		return "ptr", true
	}
	if _, ok := taskElemText(text); ok {
		return "ptr", true
	}
	if typ, _, ok := llvmScalar(text); ok {
		return typ, true
	}
	if typ, ok := llvmTypeText(text); ok {
		return typ, true
	}
	if n, elem, ok := arrayTypeText(text); ok {
		if elemLLVM, ok := g.typeLLVMOf(elem); ok && elemLLVM != "" {
			return "[" + strconv.Itoa(n) + " x " + elemLLVM + "]", true
		}
	}
	if text == "()" {
		return "", true
	}
	if text == "Error" || text == "TaskError" {
		g.usedError = true
		return "%interface.Error", true
	}
	if text == "Show" {
		g.showUsed = true
		return "%interface.Show", true
	}
	if strings.HasPrefix(text, "func(") {
		return "{ ptr, ptr }", true
	}
	if typ, ok := g.structInstanceFromText(text); ok {
		return typ, true
	}
	if info, ok := g.adtType(text); ok {
		return info.llvm, true
	}
	if info := g.byName[text]; info != nil {
		return info.llvm, true
	}
	if info := g.enumByName[text]; info != nil {
		return info.llvm, true
	}
	if info := g.ifaceByName[text]; info != nil {
		return info.llvm, true
	}
	if _, ok := g.structDecls[text]; ok {
		return qualifiedTypeName(text), true
	}
	kind := g.declKinds[text]
	if kind == "" && !strings.Contains(text, ".") {
		kind = g.declKinds[qualify(g.pkg, text)]
		if kind != "" {
			text = qualify(g.pkg, text)
		}
	}
	switch kind {
	case "struct":
		return qualifiedTypeName(text), true
	case "enum":
		return qualifiedEnumName(text), true
	case "interface":
		return qualifiedIfaceName(text), true
	}
	return "", false
}

// adtType lowers and caches one Option or Result type.
func (g *generator) adtType(text string) (*adtInfo, bool) {
	if info, ok := g.adts[text]; ok {
		return info, info.llvm != ""
	}
	kind, args, ok := parseADT(text)
	if !ok {
		return nil, false
	}
	info := &adtInfo{kind: kind, text: text, args: args, tags: map[string]uint32{}}
	g.adts[text] = info
	info.argLLVM = make([]string, len(args))
	for i, arg := range args {
		typ, ok := g.typeLLVMOf(arg)
		if !ok {
			return info, false
		}
		info.argLLVM[i] = typ
	}
	if kind == "option" {
		info.tags["None"] = 0
		info.tags["Some"] = 1
	} else {
		info.tags["Ok"] = 0
		info.tags["Err"] = 1
	}
	type payloadField struct {
		typ   string
		size  int
		align int
	}
	var fields []payloadField
	for _, typ := range info.argLLVM {
		if typ == "" {
			continue
		}
		size, align, ok := g.sizeAlign(typ)
		if !ok {
			return info, false
		}
		fields = append(fields, payloadField{typ: typ, size: size, align: align})
	}
	if len(fields) == 0 {
		info.llvm = "{ i32 }"
		info.size = 4
		info.align = 4
		return info, true
	}
	chosen := fields[0]
	for _, f := range fields[1:] {
		if f.align > chosen.align || (f.align == chosen.align && f.size > chosen.size) {
			chosen = f
		}
	}
	payloadSize, payloadAlign := 0, 1
	for _, f := range fields {
		if f.size > payloadSize {
			payloadSize = f.size
		}
		if f.align > payloadAlign {
			payloadAlign = f.align
		}
	}
	offset := alignUp(4, payloadAlign)
	parts := []string{"i32"}
	if gap := offset - 4; gap > 0 {
		parts = append(parts, fmt.Sprintf("[%d x i8]", gap))
	}
	info.payloadIndex = len(parts)
	parts = append(parts, chosen.typ)
	if chosen.size < payloadSize {
		parts = append(parts, fmt.Sprintf("[%d x i8]", payloadSize-chosen.size))
	}
	info.payloadSize = payloadSize
	info.payloadAlign = payloadAlign
	info.llvm = "{ " + strings.Join(parts, ", ") + " }"
	info.align = payloadAlign
	if info.align < 4 {
		info.align = 4
	}
	info.size = alignUp(offset+payloadSize, info.align)
	return info, true
}

// adtTypeOf returns the lowered ADT a type text names, if any.
func (g *generator) adtTypeOf(text string) (*adtInfo, bool) {
	return g.adtType(text)
}

// adtConstructor returns the ADT and variant a call or name constructs, or nil
// when the record is not a predeclared constructor.
func (g *generator) adtConstructor(r willc.Record) (*adtInfo, string, int, bool) {
	text := fieldText(r, "type")
	if text == "" {
		return nil, "", 0, false
	}
	info, ok := g.adtType(text)
	if !ok {
		return nil, "", 0, false
	}
	switch r.Kind {
	case "name":
		if fieldText(r, "text") == "None" {
			return info, "None", 0, true
		}
	case "call":
		to, ok := g.recs[refField(r, "to")]
		if !ok || to.Kind != "name" {
			return nil, "", 0, false
		}
		switch fieldText(to, "text") {
		case "Some", "Ok", "Err":
			args := g.callArgs(r)
			if len(args) == 0 {
				return info, fieldText(to, "text"), 0, true
			}
			return info, fieldText(to, "text"), args[0], true
		}
	}
	return nil, "", 0, false
}

// adtConstruct stores one Option or Result variant in the slot reserved for it.
func (g *generator) adtConstruct(r willc.Record, info *adtInfo, variant string, argID int) (string, string, error) {
	slot := g.allocSlot(g.adtSlots, "adt", r.ID, info.llvm)
	tag, ok := info.tags[variant]
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a variant of %s", r.ID, variant, info.text)
	}
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", tag, tagPtr)
	if argID == 0 || info.payloadIndex == 0 {
		return slot, info.llvm, nil
	}
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	argIndex := 0
	if info.kind == "result" && variant == "Err" {
		argIndex = 1
	}
	if argIndex < len(info.argLLVM) && info.argLLVM[argIndex] == "" {
		// A unit payload has no LLVM value; the tag is the whole value.
		return slot, info.llvm, nil
	}
	argType, err := g.typeOf(argID)
	if err != nil {
		return "", "", err
	}
	if argIndex < len(info.argLLVM) {
		want := info.argLLVM[argIndex]
		if iface, ok := g.ifaceOfType(want); ok && argType != want {
			argText, err := g.willTypeText(argID)
			if err != nil {
				return "", "", err
			}
			if _, err := g.ifaceStoreValue(payload, argID, iface, argText); err != nil {
				return "", "", err
			}
			return slot, info.llvm, nil
		}
	}
	if isAggregateType(argType) {
		src, srcType, err := g.place(argID)
		if err != nil {
			return "", "", err
		}
		if srcType != argType {
			return "", "", g.errf(argID, "cannot translate node #%d: the argument needs %s", argID, argType)
		}
		move, _ := g.moveSource(argID)
		if !move {
			if text, err := g.willTypeText(argID); err == nil && isFuncText(text) && g.isCopySource(argID) {
				if err := g.retainValue(src, text); err != nil {
					return "", "", err
				}
			}
		}
		g.copyValue(src, payload, argType)
		if move {
			if err := g.zeroMovedSource(argID, payload); err != nil {
				return "", "", err
			}
		}
		return slot, info.llvm, nil
	}
	value, err := g.value(argID)
	if err != nil {
		return "", "", err
	}
	if text, err := g.willTypeText(argID); err == nil && isRcText(text) && g.isCopySource(argID) {
		g.needRcDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", argType, value, payload)
	return slot, info.llvm, nil
}
