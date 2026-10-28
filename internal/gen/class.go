package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// gcGlobalRoot is one top-level reference slot. A class or interface stores
// its data word at offset zero; a closure stores its environment at offset
// eight.
type gcGlobalRoot struct {
	sym    string
	offset int
}

// gcFrameInfo is the compiler-side map of one function's class roots. The
// offsets are from the root array, not from the C stack.
type gcFrameInfo struct {
	Func  string
	Slots []int
	Types []string
}

// setupGCFrame reserves one root array for the class locals and class
// parameters of the current function. The array is the frame base the runtime
// scans; every write to it goes through the write barrier.
func (g *generator) setupGCFrame(fn int) error {
	if g.gcProgram == nil || g.smGeneral || g.smMode {
		return nil
	}
	type rootDecl struct {
		id   int
		text string
	}
	var roots []rootDecl
	for _, r := range g.recs {
		if (r.Kind != "param" && r.Kind != "receiver") ||
			refField(r, "parent") != fn {
			continue
		}
		text := fieldText(r, "type")
		if g.isClassText(text) {
			roots = append(roots, rootDecl{id: r.ID, text: text})
		} else if _, ok := g.ifaceOfText(text); ok {
			roots = append(roots, rootDecl{id: r.ID, text: text})
		}
	}
	var locals []willc.Record
	for _, r := range g.recs {
		if r.Kind != "var" || g.enclosingFunc(r.ID) != fn {
			continue
		}
		text := fieldText(r, "type")
		if g.isClassText(text) {
			locals = append(locals, r)
		} else if _, ok := g.ifaceOfText(text); ok {
			locals = append(locals, r)
		} else if isFuncText(text) {
			locals = append(locals, r)
		}
	}
	sortByID(locals)
	for _, r := range locals {
		roots = append(roots, rootDecl{id: r.ID, text: fieldText(r, "type")})
	}
	for _, r := range g.recs {
		if r.Kind != "func" || g.enclosingFunc(r.ID) != fn ||
			g.lambdas[r.ID] == nil {
			continue
		}
		roots = append(roots, rootDecl{id: r.ID, text: "func"})
	}
	var news []willc.Record
	for _, r := range g.recs {
		if r.Kind == "new" && g.enclosingFunc(r.ID) == fn &&
			g.isClassText(fieldText(r, "type")) {
			news = append(news, r)
		}
	}
	sortByID(news)
	for _, r := range news {
		roots = append(roots, rootDecl{id: r.ID, text: fieldText(r, "type")})
	}
	if len(roots) == 0 {
		return nil
	}
	n := len(roots)
	rootsSlot := "%v.gcroots"
	frameSlot := "%v.gcframe"
	g.gcFrameSlot = frameSlot
	g.gcFrameMap = gcStackMapSymbol(g.fnSymbol)
	g.gcFrameActive = true
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  %s = alloca [%d x ptr]\n", rootsSlot, n)
	fmt.Fprintf(g.out, "  store [%d x ptr] zeroinitializer, ptr %s\n", n, rootsSlot)
	fmt.Fprintf(g.out, "  %s = alloca %%willrt_gc_frame\n", frameSlot)
	info := gcFrameInfo{Func: g.fnSymbol}
	for i, root := range roots {
		addr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i32 0, i32 %d\n",
			addr, n, rootsSlot, i)
		g.gcRootSlots[root.id] = addr
		info.Slots = append(info.Slots, i*8)
		if t, ok := g.classTypeOfText(root.text); ok {
			info.Types = append(info.Types, t.Name)
		} else {
			info.Types = append(info.Types, "")
		}
	}
	// A parameter or receiver already owns a root in its caller. Mirror it into
	// this frame so a nested cycle sees it even before the first assignment.
	for _, root := range roots {
		value, ok := g.scope[root.id]
		if !ok {
			continue
		}
		if iface, ok := g.ifaceOfText(root.text); ok {
			dataPtr, _ := g.ifaceWords(value, iface)
			value = g.load("ptr", dataPtr)
		}
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			g.gcRootSlots[root.id], value)
	}
	fmt.Fprintf(g.out, "  call void @willrt_gc_frame_enter(ptr %s, ptr %s, ptr %s)\n",
		frameSlot, g.gcFrameMap, rootsSlot)
	g.gcFrames = append(g.gcFrames, info)
	return nil
}

// storeIfaceRoot mirrors the data word of one interface local into the GC
// frame. The vtable word is static metadata and never a root.
func (g *generator) storeIfaceRoot(decl int, slot string, iface *ifaceInfo) {
	if decl == 0 || g.gcRootSlots == nil || iface == nil {
		return
	}
	root := g.gcRootSlots[decl]
	if root == "" {
		return
	}
	dataPtr, _ := g.ifaceWords(slot, iface)
	data := g.load("ptr", dataPtr)
	g.storeClassRoot(decl, data)
}

func (g *generator) storeClassRoot(decl int, value string) {
	if decl == 0 || g.gcRootSlots == nil {
		return
	}
	root := g.gcRootSlots[decl]
	if root == "" {
		return
	}
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
		root, value)
}

// generalGCSlots maps class parameters, receivers, and locals at the byte
// offsets relocateGeneralAllocas assigned to their frame slots.
func (g *generator) generalGCSlots(fnID int, offsets map[string]int) ([]int, []string) {
	var slots []int
	var texts []string
	for _, r := range g.recs {
		if r.Kind != "param" && r.Kind != "receiver" && r.Kind != "var" {
			continue
		}
		if g.enclosingFunc(r.ID) != fnID {
			continue
		}
		text := fieldText(r, "type")
		slot := g.slots[r.ID]
		if slot == "" {
			slot = g.smParamSlot[r.ID]
		}
		offset, ok := offsets[slot]
		if !ok {
			continue
		}
		if t, ok := g.classTypeOfText(text); ok {
			slots = append(slots, offset)
			texts = append(texts, t.Name)
			continue
		}
		if _, ok := g.ifaceOfText(text); ok {
			slots = append(slots, offset)
			texts = append(texts, "")
			continue
		}
		if isFuncText(text) {
			slots = append(slots, offset+8)
			texts = append(texts, "")
		}
	}
	return slots, texts
}

// closureEnvType builds the GC descriptor for one lambda environment. The
// first two words are the old closure header; captures start at offset 16.
func (g *generator) closureEnvType(info *lambdaInfo) (*gcmeta.Type, error) {
	t := &gcmeta.Type{Name: "closure." + info.name, Kind: gcmeta.KindStruct}
	offset := 16
	align := 8
	var fields []gcmeta.Field
	var refs, weaks []uint32
	for _, cap := range info.captures {
		inline, ok := g.inlineGCType(cap.text)
		if !ok {
			return nil, fmt.Errorf("closure capture %s has no GC descriptor", cap.text)
		}
		offset = alignUp(offset, inline.Align)
		entry := gcmeta.Field{Name: cap.name, Type: cap.text,
			Offset: offset, Size: inline.Size}
		fields = append(fields, entry)
		for _, ref := range inline.Refs {
			refs = append(refs, uint32(offset)+ref)
		}
		for _, weak := range inline.Weaks {
			weaks = append(weaks, uint32(offset)+weak)
		}
		offset += inline.Size
		if inline.Align > align {
			align = inline.Align
		}
	}
	t.Size = alignUp(offset, align)
	t.Align = align
	t.Refs = refs
	t.Weaks = weaks
	t.Fields = fields
	g.gcInlineTypes[t.Name] = t
	return t, nil
}

// spawnGCSlots maps class arguments in a CPU job frame. frameTypes and
// argTexts are in storage order; indirect adds the closure cell first and a
// method adds the receiver before the written arguments.
func (g *generator) spawnGCSlots(frameTypes, argTexts []string, recvClass, indirect bool, recvText string) ([]int, []string) {
	var slots []int
	var texts []string
	offset := 0
	frameBase := 0
	if indirect {
		frameBase++
	}
	recvIndex := -1
	if recvClass {
		recvIndex = frameBase
		frameBase++
	}
	textBase := 0
	if recvClass {
		textBase = 1
	}
	for i, typ := range frameTypes {
		size, align, ok := g.sizeAlign(typ)
		if !ok {
			continue
		}
		offset = alignUp(offset, align)
		if i == recvIndex {
			if t, ok := g.classTypeOfText(recvText); ok {
				slots = append(slots, offset)
				texts = append(texts, t.Name)
			}
		} else if i >= frameBase {
			textIndex := i - frameBase + textBase
			if textIndex >= 0 && textIndex < len(argTexts) {
				if t, ok := g.classTypeOfText(argTexts[textIndex]); ok {
					slots = append(slots, offset)
					texts = append(texts, t.Name)
				}
			}
		}
		offset += size
	}
	return slots, texts
}

// taskGCMap records one async frame's class roots and returns the map symbol
// the task constructor passes to the runtime.
func (g *generator) taskGCMap(symbol string, slots []int, texts []string) string {
	if len(slots) == 0 {
		return "null"
	}
	for _, frame := range g.gcTaskFrames {
		if frame.Func == symbol {
			return gcStackMapSymbol(symbol)
		}
	}
	g.gcTaskFrames = append(g.gcTaskFrames, gcFrameInfo{
		Func: symbol, Slots: append([]int(nil), slots...),
		Types: append([]string(nil), texts...),
	})
	return gcStackMapSymbol(symbol)
}

func (g *generator) taskResultDescriptor(text string) string {
	if text == "" || text == "()" {
		return "null"
	}
	if t, ok := g.inlineGCType(text); ok &&
		(len(t.Refs) != 0 || len(t.Weaks) != 0) {
		return gcmeta.DescriptorSymbol(t.Name)
	}
	return "null"
}

// gcSafepoint emits one poll at an allocation, wait, back edge, or call. The
// runtime keeps this to one atomic load on the fast path.
func (g *generator) gcSafepoint(kind int) {
	if g.gcProgram == nil {
		return
	}
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_safepoint(i32 %d)\n", kind)
}

func (g *generator) leaveGCFrame() {
	if !g.gcFrameActive || g.gcFrameSlot == "" {
		return
	}
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_gc_frame_leave(ptr %s)\n",
		g.gcFrameSlot)
}

// classTypeOfText resolves one class declaration in the module. The text may
// be short inside its declaring package or qualified across packages.
func (g *generator) classTypeOfText(text string) (*gcmeta.Type, bool) {
	if g.gcProgram == nil {
		return nil, false
	}
	base, kind := borrowText(text)
	if kind == "ref" || kind == "mut" {
		return nil, false
	}
	name, ok := g.gcProgram.TypeText(g.pkg, g.canonicalTypeText(base))
	if !ok {
		return nil, false
	}
	t := g.gcProgram.Lookup(name)
	if t == nil || t.Kind != gcmeta.KindClass {
		return nil, false
	}
	return t, true
}

// asyncGCSlots computes the byte offsets of class references in a sequence
// frame. The frame starts with the state word, padding, and the wait node
// pointer; the fields follow in declaration order.
func (g *generator) asyncGCSlots(fields []asyncField) ([]int, []string) {
	offset := 16
	var slots []int
	var texts []string
	for _, f := range fields {
		size, align, ok := g.sizeAlign(f.llvm)
		if !ok {
			continue
		}
		offset = alignUp(offset, align)
		if t, ok := g.classTypeOfText(f.willText); ok {
			slots = append(slots, offset)
			texts = append(texts, t.Name)
		} else if _, ok := g.ifaceOfText(f.willText); ok {
			slots = append(slots, offset)
			texts = append(texts, "")
		} else if isFuncText(f.willText) && size >= 16 {
			slots = append(slots, offset+8)
			texts = append(texts, "")
		}
		offset += size
	}
	return slots, texts
}

func (g *generator) isClassText(text string) bool {
	_, ok := g.classTypeOfText(text)
	return ok
}

// inlineGCType returns and records the descriptor for one inline value. The
// descriptor is emitted after lowering because a channel or task can mention
// a type that has no object allocation site.
func (g *generator) inlineGCType(text string) (*gcmeta.Type, bool) {
	if g.gcProgram == nil {
		return nil, false
	}
	t, err := g.gcProgram.InlineType(g.pkg, text)
	if err != nil {
		return nil, false
	}
	g.gcInlineTypes[t.Name] = t
	return t, true
}

// classPayloadLLVM names the LLVM type of a class payload. The runtime object
// pointer points at this payload; the eight-byte header sits before it.
func classPayloadLLVM(name string) string {
	return "%" + llvmSymbol("class."+name)
}

// ensureClassPayload emits the payload type once. Its fields are the same
// declaration order as gcmeta.Fields, so field indices and descriptor offsets
// cannot drift apart.
func (g *generator) ensureClassPayload(t *gcmeta.Type) (string, error) {
	if t == nil || t.Kind != gcmeta.KindClass {
		return "", fmt.Errorf("not a class type")
	}
	name := classPayloadLLVM(t.Name)
	if g.emittedTypes[name] {
		return name, nil
	}
	g.emittedTypes[name] = true
	pkg := packageOfQualified(t.Name)
	var fields []string
	for _, f := range t.Fields {
		text := g.qualifyTypeText(pkg, f.Type)
		llvm, ok := g.typeLLVMOf(text)
		if !ok || llvm == "" {
			return "", g.errf(0, "cannot lower class %s field %s type %s", t.Name,
				f.Name, f.Type)
		}
		fields = append(fields, llvm)
	}
	if len(fields) == 0 {
		fmt.Fprintf(g.out, "%s = type {}\n", name)
		return name, nil
	}
	fmt.Fprintf(g.out, "%s = type { %s }\n", name, strings.Join(fields, ", "))
	return name, nil
}

// buildClasses emits every class payload before any function of the first
// document that carries GC metadata.
func (g *generator) buildClasses() error {
	if g.gcProgram == nil {
		return nil
	}
	for _, t := range g.gcProgram.Types() {
		if t.Kind != gcmeta.KindClass {
			continue
		}
		if _, err := g.ensureClassPayload(t); err != nil {
			return err
		}
	}
	return nil
}

// classField returns the field index and LLVM type of one class field.
func (g *generator) classField(t *gcmeta.Type, name string) (int, string, string, bool, error) {
	for i, f := range t.Fields {
		if f.Name != name {
			continue
		}
		pkg := packageOfQualified(t.Name)
		text := g.qualifyTypeText(pkg, f.Type)
		llvm, ok := g.typeLLVMOf(text)
		if !ok || llvm == "" {
			return 0, "", "", false, g.errf(0, "cannot lower field %s of %s",
				name, t.Name)
		}
		return i, text, llvm, g.isClassText(text), nil
	}
	return 0, "", "", false, nil
}

// classFieldAddress loads the object pointer and returns the address of one
// payload field. The receiver is evaluated once, so a field chain never loads
// a colored pointer twice.
func (g *generator) classFieldAddress(recvID int, name string, id int) (string, string, bool, error) {
	recvText, err := g.willTypeText(recvID)
	if err != nil {
		return "", "", false, err
	}
	t, ok := g.classTypeOfText(recvText)
	if !ok {
		return "", "", false, nil
	}
	index, fieldText, fieldLLVM, isRef, err := g.classField(t, name)
	if err != nil {
		return "", "", false, err
	}
	if fieldText == "" && index == 0 {
		return "", "", false, g.errf(id, "cannot translate node #%d: class %s has no field %s",
			id, t.Name, name)
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", false, err
	}
	object, err := g.value(recvID)
	if err != nil {
		return "", "", false, err
	}
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		ptr, payload, object, index)
	return ptr, fieldLLVM, isRef, nil
}

// classNew allocates one class payload and applies field defaults. An init
// method receives the payload pointer and runs after allocation.
func (g *generator) classNew(r willc.Record) (string, string, error) {
	text := fieldText(r, "type")
	t, ok := g.classTypeOfText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a class", r.ID, text)
	}
	if _, err := g.ensureClassPayload(t); err != nil {
		return "", "", err
	}
	g.needGCDecl = true
	g.gcDecisions = append(g.gcDecisions, decisions.Alloc(g.fnSymbol,
		fieldText(r, "pos"), t.Name, "tlab"))
	slot := g.allocSlot(g.newSlots, "class", r.ID, "ptr")
	descriptor := gcmeta.DescriptorSymbol(t.Name)
	object := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %d, ptr %s)\n",
		object, t.Size, descriptor)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", object, slot)
	// The new expression is a root before the first safepoint. A cycle can
	// start between the allocation and the assignment to a local.
	if root := g.gcRootSlots[r.ID]; root != "" {
		g.needGCDecl = true
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			root, object)
	}
	if err := g.classDefaults(r, t, object); err != nil {
		return "", "", err
	}
	initSym := g.classInitSymbol(t.Name)
	args := g.callArgs(r)
	if initSym != "" {
		params := g.paramTypes[initSym]
		muts := g.paramMut[initSym]
		parts := []string{"ptr " + object}
		for i, arg := range args {
			paramType := ""
			if i < len(params) {
				paramType = params[i]
			}
			mut := i < len(muts) && muts[i]
			var err error
			parts, err = g.callArgParts(parts, r, i, arg, paramType, mut, false)
			if err != nil {
				return "", "", err
			}
		}
		if g.sigs[initSym] != "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: class %s init returns a value", r.ID, t.Name)
		}
		fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(initSym),
			strings.Join(parts, ", "))
	} else if len(args) != 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: class %s has no init for its arguments",
			r.ID, t.Name)
	}
	if !g.gcGlobalInit {
		g.gcSafepoint(0)
	}
	return slot, "ptr", nil
}

// classInitSymbol finds the init method of one class. A class without init
// keeps the zero/default field constructor.
func (g *generator) classInitSymbol(name string) string {
	short := name
	if i := strings.LastIndexByte(short, '.'); i >= 0 {
		short = short[i+1:]
	}
	for _, key := range []string{name + ".init", short + ".init",
		qualify(g.pkg, short+".init")} {
		if sym := g.cur.methods[key]; sym != "" {
			return sym
		}
		if sym := g.methodsQualified[key]; sym != "" {
			return sym
		}
	}
	return ""
}

// classDefaults writes the declared field defaults into a fresh payload. The
// runtime zeroes the payload first, so a field without a default keeps its
// zero value.
func (g *generator) classDefaults(r willc.Record, t *gcmeta.Type, object string) error {
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return err
	}
	pkg := packageOfQualified(t.Name)
	for i, field := range t.Fields {
		if field.Type == "" {
			continue
		}
		// A class declaration's default expression is attached to the field
		// record. Locate it in the current document by the field name.
		var defaultID int
		classID := g.classDecl[t.Name]
		for _, rec := range g.recs {
			if rec.Kind != "field" || refField(rec, "parent") != classID ||
				fieldText(rec, "text") != field.Name {
				continue
			}
			defaultID = refField(rec, "default")
			if defaultID != 0 {
				break
			}
		}
		if defaultID == 0 {
			continue
		}
		text := g.qualifyTypeText(pkg, field.Type)
		typ, ok := g.typeLLVMOf(text)
		if !ok {
			continue
		}
		value, err := g.value(defaultID)
		if err != nil {
			return err
		}
		addr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			addr, payload, object, i)
		if g.isClassText(text) {
			g.needGCDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
				addr, value)
		} else {
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, addr)
		}
	}
	return nil
}

// classPointerValue reads a class local, parameter, or field through the load
// barrier. A local and a field slot self-heal; a parameter has no slot of its
// own, so the value barrier returns the untagged pointer.
func (g *generator) classPointerValue(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	g.needGCDecl = true
	if r.Kind == "name" {
		decl := refField(r, "to")
		if slot, ok := g.slots[decl]; ok {
			out := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_read_barrier_slot(ptr %s)\n", out, slot)
			return out, nil
		}
		if value, ok := g.scope[decl]; ok {
			out := g.newTmp()
			fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_read_barrier(ptr %s)\n", out, value)
			return out, nil
		}
	}
	addr, typ, err := g.place(id)
	if err != nil {
		return "", err
	}
	if typ != "ptr" {
		return "", g.errf(id, "cannot translate node #%d: a class value has type %s", id, typ)
	}
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_read_barrier_slot(ptr %s)\n", out, addr)
	return out, nil
}

// classFieldRead loads one class field and applies the read barrier when the
// field itself is a reference.
func (g *generator) classFieldRead(r willc.Record) (string, string, error) {
	addr, typ, isRef, err := g.classFieldAddress(refField(r, "recv"),
		fieldText(r, "text"), r.ID)
	if err != nil {
		return "", "", err
	}
	if isRef {
		g.needGCDecl = true
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_read_barrier_slot(ptr %s)\n",
			value, addr)
		return value, typ, nil
	}
	return g.load(typ, addr), typ, nil
}
