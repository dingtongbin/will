// Package gen translates willc into LLVM text. The translation is
// mechanical: it never analyzes, and a node it does not know is reported with
// G0001 instead of being guessed.
package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Emit translates one willc document into LLVM text. Only the node subset
// needed by the first lowering experiment is implemented: integer functions
// with parameters, return, integer literals, names, and + - *.
func Emit(doc *willc.Doc) ([]byte, error) {
	return EmitModule([]*willc.Doc{doc}, "")
}

// EmitWithEntry translates one willc document and, when entry names a function
// that returns no value, wraps it in the i32-returning main the C runtime
// calls, so a program whose entry returns nothing exits with 0.
func EmitWithEntry(doc *willc.Doc, entry string) ([]byte, error) {
	return EmitModule([]*willc.Doc{doc}, entry)
}

// EmitModule translates the documents of one module into a single LLVM module.
// Each document is one source file, and its module path is the package path:
// every function symbol is <package path>.<name>, so a call written as
// to="<package path>.<name>" names the same symbol as the definition.
func EmitModule(docs []*willc.Doc, entry string) ([]byte, error) {
	g := &generator{
		scope:    map[int]string{},
		slots:    map[int]string{},
		declared: map[string]bool{},
		out:      &strings.Builder{},
	}
	for _, doc := range docs {
		g.docs = append(g.docs, newDocState(doc))
	}
	// A void entry cannot be the C main itself, so it keeps a mangled symbol
	// and the generated wrapper calls it.
	g.findEntry(entry)
	g.collectSignatures()
	for _, d := range g.docs {
		g.use(d)
		if err := g.emitDoc(); err != nil {
			return nil, err
		}
	}
	if g.wrappedEntry != "" {
		fmt.Fprintf(g.out, "define i32 @main() {\nentry:\n  call void @%s()\n  ret i32 0\n}\n",
			llvmSymbol(g.wrappedEntry))
	}
	return []byte(g.out.String()), nil
}

// docState is the per-file part of the translation: record ids and the callable
// names mean something only inside one document.
type docState struct {
	path  string
	recs  map[int]willc.Record
	funcs map[int]string
	links map[int]string
}

func newDocState(doc *willc.Doc) *docState {
	d := &docState{
		recs:  map[int]willc.Record{},
		funcs: map[int]string{},
		links: map[int]string{},
	}
	for _, r := range doc.Records {
		d.recs[r.ID] = r
		if r.Kind == "module" {
			d.path = fieldText(r, "path")
		}
	}
	return d
}

// findEntry records the package and name of the entry function. A void entry
// also gets a mangled symbol, because the generated i32 main calls it.
func (g *generator) findEntry(entry string) {
	if entry == "" {
		return
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "func" || fieldText(r, "text") != entry {
				continue
			}
			if hasChild(d.recs, r.ID, "receiver") {
				continue
			}
			g.entryName = entry
			g.entryPkg = d.path
			if fieldText(r, "type") == "" {
				g.wrappedEntry = qualify(d.path, "will$"+entry)
			}
			return
		}
	}
}

// collectSignatures records the result type text of every function of the
// module, by symbol, so a call that names its callee by symbol knows the type
// it gets back.
func (g *generator) collectSignatures() {
	g.sigs = map[string]string{}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "func" {
				continue
			}
			name := fieldText(r, "text")
			sym := qualify(d.path, name)
			for _, c := range d.recs {
				if c.Kind == "receiver" && refField(c, "parent") == r.ID {
					sym = qualify(d.path, receiverType(&c)+"."+name)
					break
				}
			}
			g.sigs[sym] = fieldText(r, "type")
		}
	}
}

// use makes one document current. The record ids, callable names, temporaries,
// and parameter scopes are per document; declarations are module-wide.
func (g *generator) use(d *docState) {
	g.cur = d
	g.pkg = d.path
	g.recs = d.recs
	g.scope = map[int]string{}
	g.tmp = 0
	g.slots = nil
	g.slotTypes = map[int]string{}
	g.borrows = map[int]bool{}
	g.structs = map[int]*structInfo{}
	g.byType = map[string]*structInfo{}
	g.byName = map[string]*structInfo{}
	g.aggSlots = map[int]string{}
	g.callSlots = map[int]string{}
	g.loopBinds = map[int]map[string]string{}
	g.loopTypes = map[int]map[string]string{}
	g.loopPtrs = map[int]map[string]string{}
}

type generator struct {
	// docs is every document of the module, and cur is the one being lowered.
	docs []*docState
	cur  *docState

	recs     map[int]willc.Record
	scope    map[int]string
	declared map[string]bool
	out      *strings.Builder
	tmp      int
	// labels numbers the basic blocks of the current function.
	labels int
	// slots maps a local declaration node to its stack slot, and slotNames
	// numbers the slots of the current function.
	slots     map[int]string
	slotTypes map[int]string
	slotNames map[string]int
	// borrows marks the parameters and receivers passed as a pointer, so a
	// name can be used as a place.
	borrows map[int]bool
	// structs holds the lowered structs of the current document by record id,
	// and byType looks one up by its LLVM type name. aggSlots maps an aggregate
	// literal to the temporary slot reserved for it in the entry block.
	structs  map[int]*structInfo
	byType   map[string]*structInfo
	byName   map[string]*structInfo
	aggSlots map[int]string
	// callSlots maps a call whose result is a struct to the temporary slot its
	// value is stored in, so a field can be read from it.
	callSlots map[int]string
	// loopBinds maps a for record and a binding name to that binding's slot,
	// and loopTypes to its will type text. Both bindings of a loop name the
	// same for record, so the name tells them apart. loopPtrs holds the element
	// pointer of a binding that borrows an element instead of copying it.
	loopBinds map[int]map[string]string
	loopTypes map[int]map[string]string
	loopPtrs  map[int]map[string]string
	// loops stacks the labels of the enclosing loops, so break and continue
	// know where to jump.
	loops []loopLabels
	// resultType is the LLVM type the current function returns, or "" for a
	// void function.
	resultType string
	// sigs holds the result type text of every function of the module, by
	// symbol, so a call to another package knows what it gets back.
	sigs map[string]string

	// pkg is the package path of the current document.
	pkg string
	// entryName, entryPkg, and wrappedEntry describe a void entry: it keeps a
	// mangled symbol because the generated main calls it.
	entryName    string
	entryPkg     string
	wrappedEntry string
}

// emitDoc lowers every function of the current document, in source order.
func (g *generator) emitDoc() error {
	g.collect()
	g.buildStructs()
	var funcs []willc.Record
	for _, r := range g.recs {
		if r.Kind == "func" {
			funcs = append(funcs, r)
		}
	}
	sortByID(funcs)
	for _, fn := range funcs {
		if err := g.funcDecl(fn); err != nil {
			return err
		}
	}
	return nil
}

// collect records the callable names of the current document: file functions
// and import c links. A method is named <package path>.<type name>.<method>.
func (g *generator) collect() {
	for _, r := range g.recs {
		switch r.Kind {
		case "func":
			name := fieldText(r, "text")
			if recv := g.receiverOf(r.ID); recv != nil {
				g.cur.funcs[r.ID] = qualify(g.pkg, receiverType(recv)+"."+name)
				continue
			}
			g.cur.funcs[r.ID] = g.symbolName(name)
		case "link":
			g.cur.links[r.ID] = fieldText(r, "text")
		}
	}
}

// receiverOf returns the receiver record of a function, or nil.
func (g *generator) receiverOf(fn int) *willc.Record {
	for _, r := range g.recs {
		if r.Kind == "receiver" && refField(r, "parent") == fn {
			rec := r
			return &rec
		}
	}
	return nil
}

// receiverType returns the type text of a receiver record.
func receiverType(r *willc.Record) string {
	return fieldText(*r, "type")
}

// symbolName returns the IR symbol of one function of the current document.
// A void entry uses its mangled symbol because the generated main calls it; a
// value-returning main keeps the C symbol, since the runtime calls it.
func (g *generator) symbolName(name string) string {
	if g.entryName != "" && g.entryPkg == g.pkg && g.entryName == name {
		if g.wrappedEntry != "" {
			return g.wrappedEntry
		}
		if name == "main" {
			return name
		}
	}
	return qualify(g.pkg, name)
}

// loopLabels names the two places a loop body can leave to: the step block
// (continue) and the block after the loop (break).
type loopLabels struct {
	step string
	end  string
}

// qualify puts a package path in front of a name. A document without a package
// path keeps the plain name.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// llvmSymbol quotes a symbol whose text is not a plain LLVM identifier.
func llvmSymbol(name string) string {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || c == '.' || c == '-' || c == '$' ||
			c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		return `"` + name + `"`
	}
	return name
}

// isScalarName reports whether a type text names a lowered scalar.
func isScalarName(name string) bool {
	_, _, ok := llvmScalar(name)
	return ok
}

func (g *generator) errf(id int, format string, args ...any) error {
	return &willc.Error{ID: id, Message: fmt.Sprintf(format, args...)}
}

func (g *generator) funcDecl(fn willc.Record) error {
	name := fieldText(fn, "text")
	recv := g.receiverOf(fn.ID)
	result := ""
	var params []willc.Record
	for _, r := range g.recs {
		if refField(r, "parent") != fn.ID {
			continue
		}
		switch r.Kind {
		case "param":
			params = append(params, r)
		case "type":
			result = fieldText(r, "text")
		}
	}
	sortByID(params)
	var sig []string
	if recv != nil {
		// The receiver is the hidden first parameter and is passed as a
		// borrow, so it is a pointer.
		recvName := fieldText(*recv, "text")
		g.scope[recv.ID] = "%" + recvName
		g.borrows[recv.ID] = true
		sig = append(sig, "ptr %"+recvName)
	}
	for _, p := range params {
		pname := fieldText(p, "text")
		ptype := fieldText(p, "type")
		if typ, _, ok := llvmScalar(ptype); ok {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, typ+" %"+pname)
			continue
		}
		typ, err := g.declLLVMType(p.ID)
		if err != nil {
			return err
		}
		if !isAggregateType(typ) {
			return g.errf(p.ID, "cannot translate node #%d: parameter type %s is not lowered yet", p.ID, ptype)
		}
		// Everything else is passed as a borrow.
		g.scope[p.ID] = "%" + pname
		g.borrows[p.ID] = true
		sig = append(sig, "ptr %"+pname)
	}
	if result == "" {
		return g.voidFunc(fn, name, sig)
	}
	resLLVM := ""
	if typ, _, ok := llvmScalar(result); ok {
		resLLVM = typ
	} else if id := g.typeRef(fn.ID); id != 0 {
		info, ok := g.ensureStruct(id)
		if !ok {
			return g.errf(fn.ID, "cannot translate node #%d: the result type is not lowered yet", fn.ID)
		}
		resLLVM = info.llvm
	} else {
		return g.errf(fn.ID, "cannot translate node #%d: result type %s is not lowered yet", fn.ID, result)
	}
	g.resultType = resLLVM
	g.labels = 0
	g.slots = map[int]string{}
	g.slotTypes = map[int]string{}
	g.slotNames = map[string]int{}
	g.loops = nil
	fmt.Fprintf(g.out, "define %s @%s(%s) {\n", resLLVM, llvmSymbol(g.cur.funcs[fn.ID]), strings.Join(sig, ", "))
	g.out.WriteString("entry:\n")
	body, err := g.blockOf(fn.ID)
	if err != nil {
		return err
	}
	if err := g.allocas(fn.ID); err != nil {
		return err
	}
	terminated, err := g.body(body, false)
	if err != nil {
		return err
	}
	if !terminated {
		g.out.WriteString("  unreachable\n")
	}
	g.out.WriteString("}\n")
	return nil
}

// voidFunc lowers a function that returns no value.
func (g *generator) voidFunc(fn willc.Record, name string, sig []string) error {
	g.resultType = ""
	g.labels = 0
	g.slots = map[int]string{}
	g.slotNames = map[string]int{}
	g.loops = nil
	fmt.Fprintf(g.out, "define void @%s(%s) {\n", llvmSymbol(g.cur.funcs[fn.ID]), strings.Join(sig, ", "))
	g.out.WriteString("entry:\n")
	body, err := g.blockOf(fn.ID)
	if err != nil {
		return err
	}
	if err := g.allocas(fn.ID); err != nil {
		return err
	}
	terminated, err := g.body(body, true)
	if err != nil {
		return err
	}
	if !terminated {
		g.out.WriteString("  ret void\n")
	}
	g.out.WriteString("}\n")
	return nil
}

func (g *generator) blockOf(owner int) (willc.Record, error) {
	for _, r := range g.recs {
		if r.Kind == "block" && refField(r, "parent") == owner {
			return r, nil
		}
	}
	return willc.Record{}, g.errf(owner, "cannot translate node #%d: no body", owner)
}

// allocas reserves a stack slot in the entry block for every local of one
// function. Creating them at the entry keeps a loop from growing the stack.
func (g *generator) allocas(fn int) error {
	var locals []willc.Record
	for _, r := range g.recs {
		if r.Kind != "var" && r.Kind != "const" {
			continue
		}
		if g.enclosingFunc(r.ID) != fn {
			continue
		}
		locals = append(locals, r)
	}
	sortByID(locals)
	for _, r := range locals {
		typ, err := g.declLLVMType(r.ID)
		if err != nil {
			return err
		}
		name := g.newSlot(fieldText(r, "text"))
		g.slots[r.ID] = name
		g.slotTypes[r.ID] = typ
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
	}
	// An aggregate literal is stored in a temporary slot reserved here, so a
	// loop that evaluates it does not grow the stack.
	var aggregates []willc.Record
	for _, r := range g.recs {
		if r.Kind == "agg" && g.enclosingFunc(r.ID) == fn {
			aggregates = append(aggregates, r)
		}
	}
	sortByID(aggregates)
	for _, r := range aggregates {
		info, ok := g.ensureStruct(refField(r, "to"))
		if !ok {
			return g.errf(r.ID, "cannot translate node #%d: the aggregate type is not lowered yet", r.ID)
		}
		name := "%v.agg." + strconv.Itoa(r.ID)
		g.aggSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
	}
	// A call whose result is a struct also needs a slot: its value has to be
	// stored before a field can be read from it.
	var calls []willc.Record
	for _, r := range g.recs {
		if (r.Kind == "call" || r.Kind == "mcall") && g.enclosingFunc(r.ID) == fn {
			calls = append(calls, r)
		}
	}
	sortByID(calls)
	for _, r := range calls {
		info, err := g.callResultStruct(r)
		if err != nil {
			continue
		}
		name := "%v.ret." + strconv.Itoa(r.ID)
		g.callSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
	}
	// A range loop binds one counter, which also needs a slot.
	for _, r := range g.recs {
		if r.Kind != "for" || g.enclosingFunc(r.ID) != fn {
			continue
		}
		if g.isRangeLoop(r) {
			if len(bindNames(r)) != 1 {
				continue
			}
			name := g.newSlot(bindNames(r)[0])
			g.slots[r.ID] = name
			g.slotTypes[r.ID] = "i32"
			g.bindLoopType(r.ID, bindNames(r)[0], "i32")
			fmt.Fprintf(g.out, "  %s = alloca i32\n", name)
			continue
		}
		// A fixed-array loop keeps its index in an i64 slot; the element
		// binding borrows the element, so it needs no slot of its own.
		iterText, err := g.willTypeText(refField(r, "iter"))
		if err != nil {
			continue
		}
		n, elem, ok := arrayTypeText(iterText)
		if !ok {
			e, cok := containerElem(iterText)
			if !cok {
				continue
			}
			n, elem, ok = 0, e, true
		}
		if !ok || n < 0 {
			continue
		}
		if _, _, ok := llvmScalar(elem); !ok {
			continue
		}
		names := bindNames(r)
		if len(names) < 1 || len(names) > 2 {
			continue
		}
		if g.loopBinds[r.ID] == nil {
			g.loopBinds[r.ID] = map[string]string{}
			g.loopTypes[r.ID] = map[string]string{}
		}
		idxName := hiddenIndex
		if len(names) == 2 {
			idxName = names[0]
		}
		idx := g.newSlot(idxName)
		g.loopBinds[r.ID][hiddenIndex] = idx
		g.bindLoopType(r.ID, idxName, "usize")
		g.loopBinds[r.ID][idxName] = idx
		fmt.Fprintf(g.out, "  %s = alloca i64\n", idx)
		elemName := names[len(names)-1]
		g.loopTypes[r.ID][elemName] = elem
	}
	return nil
}

// bindLoopType records the will type text of one loop binding.
func (g *generator) bindLoopType(loop int, name, text string) {
	if g.loopTypes[loop] == nil {
		g.loopTypes[loop] = map[string]string{}
	}
	g.loopTypes[loop][name] = text
}

// newSlot names one stack slot. A name used more than once gets a number, so a
// shadowed local has its own slot.
func (g *generator) newSlot(base string) string {
	seen := g.slotNames[base]
	g.slotNames[base] = seen + 1
	if seen == 0 {
		return "%v." + base
	}
	return "%v." + base + "." + strconv.Itoa(seen)
}

// bindNames lists the binding names of a for record, in writing order.
func bindNames(r willc.Record) []string {
	var out []string
	for _, f := range r.Fields {
		if f.Name == "bind" {
			out = append(out, f.Value.Text)
		}
	}
	return out
}

// isRangeLoop reports whether a for record iterates a Range(lo, hi).
func (g *generator) isRangeLoop(r willc.Record) bool {
	iter, ok := g.recs[refField(r, "iter")]
	return ok && iter.Kind == "range"
}

// enclosingFunc returns the function record that contains a node, or 0.
func (g *generator) enclosingFunc(id int) int {
	for i := 0; i < 64; i++ {
		r, ok := g.recs[id]
		if !ok {
			return 0
		}
		parent := refField(r, "parent")
		if parent == 0 {
			return 0
		}
		p, ok := g.recs[parent]
		if !ok {
			return 0
		}
		if p.Kind == "func" {
			return p.ID
		}
		id = parent
	}
	return 0
}

// load reads one place of a scalar type into a temporary.
func (g *generator) load(typ, slot string) string {
	g.tmp++
	name := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = load %s, ptr %s\n", name, typ, slot)
	return name
}

// assignStmt stores a value into a place, applying the compound operator when
// the assignment names one.
func (g *generator) assignStmt(r willc.Record) error {
	target := refField(r, "target")
	ptr, typ, err := g.place(target)
	if err != nil {
		return err
	}
	valueID := refField(r, "value")
	op := fieldText(r, "op")
	if op != "=" {
		text, err := g.willTypeText(target)
		if err != nil {
			return err
		}
		instr := arithInstr(strings.TrimSuffix(op, "="), typ, !isUnsigned(text))
		if instr == "" {
			return g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, op)
		}
		current := g.load(typ, ptr)
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		g.tmp++
		tmp := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", tmp, instr, typ, current, value)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, tmp, ptr)
		return nil
	}
	if isAggregateType(typ) {
		src, srcType, err := g.place(valueID)
		if err != nil {
			return err
		}
		if srcType != typ {
			return g.errf(r.ID, "cannot translate node #%d: the assignment needs %s", r.ID, typ)
		}
		g.copyValue(src, ptr, typ)
		return nil
	}
	value, err := g.value(valueID)
	if err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, ptr)
	return nil
}

// declStmt stores the initial value of a local into its slot.
func (g *generator) declStmt(r willc.Record) (bool, error) {
	slot, ok := g.slots[r.ID]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: no slot for %s", r.ID, fieldText(r, "text"))
	}
	valueID := 0
	for _, c := range g.children(r.ID) {
		valueID = c.ID
	}
	typ := g.slotTypes[r.ID]
	if isStructType(typ) {
		src, srcType, err := g.place(valueID)
		if err != nil {
			return false, err
		}
		if srcType != typ {
			return false, g.errf(r.ID, "cannot translate node #%d: %s needs a %s value", r.ID, fieldText(r, "text"), typ)
		}
		g.copyValue(src, slot, typ)
		return false, nil
	}
	value, err := g.value(valueID)
	if err != nil {
		return false, err
	}
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, slot)
	return false, nil
}
func (g *generator) body(b willc.Record, void bool) (bool, error) {
	stmts := g.children(b.ID)
	sortByID(stmts)
	terminated := false
	for _, st := range stmts {
		if terminated {
			break
		}
		var err error
		switch st.Kind {
		case "return":
			terminated, err = g.returnStmt(st, void)
		case "if":
			terminated, err = g.ifStmt(st, void)
		case "for":
			terminated, err = g.forStmt(st, void)
		case "break":
			terminated, err = g.jump(true)
		case "continue":
			terminated, err = g.jump(false)
		case "var", "const":
			terminated, err = g.declStmt(st)
		case "assign":
			err = g.assignStmt(st)
		case "exprstmt":
			_, err = g.value(refField(st, "expr"))
		case "block":
			terminated, err = g.body(st, void)
		default:
			err = g.errf(st.ID, "cannot translate node #%d: statement %s is not lowered yet", st.ID, st.Kind)
		}
		if err != nil {
			return false, err
		}
	}
	return terminated, nil
}

// returnStmt lowers one return.
func (g *generator) returnStmt(st willc.Record, void bool) (bool, error) {
	if void {
		if refField(st, "value") != 0 {
			return false, g.errf(st.ID, "cannot translate node #%d: a void function returns no value", st.ID)
		}
		g.out.WriteString("  ret void\n")
		return true, nil
	}
	valueID := refField(st, "value")
	if g.resultType == "" {
		return false, g.errf(st.ID, "cannot translate node #%d: the function returns no value", st.ID)
	}
	if isAggregateType(g.resultType) {
		ptr, typ, err := g.place(valueID)
		if err != nil {
			return false, err
		}
		if typ != g.resultType {
			return false, g.errf(st.ID, "cannot translate node #%d: the return needs %s", st.ID, g.resultType)
		}
		g.tmp++
		tmp := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = load %s, ptr %s\n", tmp, typ, ptr)
		fmt.Fprintf(g.out, "  ret %s %s\n", typ, tmp)
		return true, nil
	}
	value, err := g.value(valueID)
	if err != nil {
		return false, err
	}
	fmt.Fprintf(g.out, "  ret %s %s\n", g.resultType, value)
	return true, nil
}

// jump leaves the innermost loop: break jumps past it, continue to its step
// block.
func (g *generator) jump(isBreak bool) (bool, error) {
	if len(g.loops) == 0 {
		return false, g.errf(0, "cannot translate a break or continue outside a loop")
	}
	top := g.loops[len(g.loops)-1]
	target := top.step
	if isBreak {
		target = top.end
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", target)
	return true, nil
}

// forStmt lowers a loop. Two shapes are lowered: an endless loop, and a loop
// that counts one binding from Range(lo, hi). The bounds are computed once,
// before the loop, so a step expression cannot run twice.
func (g *generator) forStmt(r willc.Record, void bool) (bool, error) {
	g.labels++
	n := g.labels
	bodyLabel := fmt.Sprintf("for.body.%d", n)
	stepLabel := fmt.Sprintf("for.step.%d", n)
	endLabel := fmt.Sprintf("for.end.%d", n)

	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	binds := bindNames(r)
	iterID := refField(r, "iter")
	if iterID == 0 {
		if len(binds) != 0 {
			return false, g.errf(r.ID, "cannot translate node #%d: a loop without an iterator binds nothing", r.ID)
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", bodyLabel)
		g.out.WriteString(bodyLabel + ":\n")
		g.loops = append(g.loops, loopLabels{step: bodyLabel, end: endLabel})
		done, err := g.body(body, void)
		g.loops = g.loops[:len(g.loops)-1]
		if err != nil {
			return false, err
		}
		if !done {
			fmt.Fprintf(g.out, "  br label %%%s\n", bodyLabel)
		}
		fmt.Fprintf(g.out, "%s:\n", endLabel)
		return false, nil
	}

	if iterText, err := g.willTypeText(iterID); err == nil {
		if an, elem, ok := arrayTypeText(iterText); ok {
			return g.forArray(r, an, elem, void, n)
		}
		if elem, ok := containerElem(iterText); ok {
			return g.forContainer(r, elem, void, n)
		}
	}
	if len(binds) != 1 {
		return false, g.errf(r.ID, "cannot translate node #%d: only a loop over Range with one binding, or a fixed array, is lowered yet", r.ID)
	}
	iter, ok := g.recs[iterID]
	if !ok || iter.Kind != "range" {
		return false, g.errf(r.ID, "cannot translate node #%d: only a loop over Range is lowered yet", r.ID)
	}
	slot := g.slots[r.ID]
	if slot == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop counter has no slot", r.ID)
	}
	lo, err := g.value(refField(iter, "lo"))
	if err != nil {
		return false, err
	}
	hi, err := g.value(refField(iter, "hi"))
	if err != nil {
		return false, err
	}
	for _, id := range []int{refField(iter, "lo"), refField(iter, "hi")} {
		if t, err := g.typeOf(id); err != nil {
			return false, err
		} else if t != "i32" {
			return false, g.errf(r.ID, "cannot translate node #%d: only an i32 range is lowered yet", r.ID)
		}
	}
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", lo, slot)
	condLabel := fmt.Sprintf("for.cond.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i32", slot)
	g.tmp++
	cond := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = icmp slt i32 %s, %s\n", cond, current, hi)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel})
	done, err := g.body(body, void)
	g.loops = g.loops[:len(g.loops)-1]
	if err != nil {
		return false, err
	}
	if !done {
		fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	}
	g.out.WriteString(stepLabel + ":\n")
	step := g.load("i32", slot)
	g.tmp++
	next := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = add i32 %s, 1\n", next, step)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", next, slot)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

// ifStmt lowers one if. The result says whether every path of the if ends in a
// terminator; when one path falls through, it is branched to the merge label.
func (g *generator) ifStmt(r willc.Record, void bool) (bool, error) {
	g.labels++
	n := g.labels
	thenLabel := fmt.Sprintf("if.then.%d", n)
	endLabel := fmt.Sprintf("if.end.%d", n)
	cond, err := g.value(refField(r, "cond"))
	if err != nil {
		return false, err
	}
	thenBlock, ok := g.recs[refField(r, "then")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the then branch is missing", r.ID)
	}
	elseID := refField(r, "else")
	if elseID == 0 {
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLabel, endLabel)
		g.out.WriteString(thenLabel + ":\n")
		thenDone, err := g.body(thenBlock, void)
		if err != nil {
			return false, err
		}
		if !thenDone {
			fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
		}
		g.out.WriteString(endLabel + ":\n")
		return false, nil
	}

	elseLabel := fmt.Sprintf("if.else.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLabel, elseLabel)
	g.out.WriteString(thenLabel + ":\n")
	thenDone, err := g.body(thenBlock, void)
	if err != nil {
		return false, err
	}
	if !thenDone {
		fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	}
	g.out.WriteString(elseLabel + ":\n")
	elseDone, err := g.branch(elseID, void)
	if err != nil {
		return false, err
	}
	if !elseDone {
		fmt.Fprintf(g.out, "  br label %%%s\n", endLabel)
	}
	if thenDone && elseDone {
		// Both branches returned, so nothing reaches the merge label.
		return true, nil
	}
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

// branch lowers an if's else part, which is either a block or another if.
func (g *generator) branch(id int, void bool) (bool, error) {
	r, ok := g.recs[id]
	if !ok {
		return false, g.errf(id, "cannot translate node #%d: no such node", id)
	}
	if r.Kind == "if" {
		return g.ifStmt(r, void)
	}
	return g.body(r, void)
}

// value emits the instructions for one expression and returns its SSA name.
func (g *generator) value(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "lit":
		switch kind := fieldText(r, "kind"); kind {
		case "int":
			return literalIntText(fieldText(r, "text")), nil
		case "float":
			return literalFloatText(fieldText(r, "text")), nil
		case "bool":
			return fieldText(r, "text"), nil
		}
		return "", g.errf(id, "cannot translate node #%d: literal kind %s is not lowered yet", id, fieldText(r, "kind"))
	case "name":
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if ptrs, ok := g.loopPtrs[decl]; ok {
			if ptr, ok := ptrs[name]; ok {
				text, ok := g.loopBindType(decl, name)
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: the binding has no type", id)
				}
				typ, ok := llvmTypeText(text)
				if !ok {
					return "", g.errf(id, "cannot translate node #%d: type %s is not lowered yet", id, text)
				}
				if isAggregateType(typ) {
					return "", g.errf(id, "cannot translate node #%d: %s is a borrowed aggregate, not a scalar", id, name)
				}
				return g.load(typ, ptr), nil
			}
		}
		if slot, typ, ok := g.nameSlot(r); ok {
			if isAggregateType(typ) {
				return "", g.errf(id, "cannot translate node #%d: %s is an aggregate, not a scalar", id, fieldText(r, "text"))
			}
			return g.load(typ, slot), nil
		}
		decl = refField(r, "to")
		if name, ok := g.scope[decl]; ok {
			if g.borrows[decl] {
				return "", g.errf(id, "cannot translate node #%d: %s is a borrow, not a scalar value", id, fieldText(r, "text"))
			}
			return name, nil
		}
		return "", g.errf(id, "cannot translate node #%d: the name is not a parameter or a local", id)
	case "field":
		ptr, typ, err := g.place(id)
		if err != nil {
			return "", err
		}
		if isAggregateType(typ) {
			return "", g.errf(id, "cannot translate node #%d: %s is a struct field, not a scalar", id, fieldText(r, "text"))
		}
		return g.load(typ, ptr), nil
	case "agg":
		return "", g.errf(id, "cannot translate node #%d: an aggregate is a place, not a scalar value", id)
	case "un":
		return g.unary(r)
	case "bin":
		return g.binary(r)
	case "call", "mcall":
		return g.callScalar(r)
	}
	return "", g.errf(id, "cannot translate node #%d: node kind %s is not lowered yet", id, r.Kind)
}

// unary lowers one prefix operator.
func (g *generator) unary(r willc.Record) (string, error) {
	x, err := g.value(refField(r, "expr"))
	if err != nil {
		return "", err
	}
	typ, err := g.typeOf(refField(r, "expr"))
	if err != nil {
		return "", err
	}
	g.tmp++
	name := "%t" + strconv.Itoa(g.tmp)
	switch op := fieldText(r, "op"); op {
	case "-":
		if isFloatType(typ) {
			fmt.Fprintf(g.out, "  %s = fneg %s %s\n", name, typ, x)
			break
		}
		fmt.Fprintf(g.out, "  %s = sub %s 0, %s\n", name, typ, x)
	case "~":
		if isFloatType(typ) {
			return "", g.errf(r.ID, "cannot translate node #%d: ~ needs an integer", r.ID)
		}
		fmt.Fprintf(g.out, "  %s = xor %s %s, -1\n", name, typ, x)
	case "!":
		fmt.Fprintf(g.out, "  %s = xor i1 %s, true\n", name, x)
	default:
		return "", g.errf(r.ID, "cannot translate node #%d: operator %s is not lowered yet", r.ID, op)
	}
	return name, nil
}

// binary lowers one binary operator. The instruction and, for comparisons, the
// predicate follow the type of the operands: an unsigned type uses the unsigned
// form even though LLVM stores both in one integer type.
func (g *generator) binary(r willc.Record) (string, error) {
	op := fieldText(r, "op")
	lID := refField(r, "l")
	rID := refField(r, "r")
	l, err := g.value(lID)
	if err != nil {
		return "", err
	}
	right, err := g.value(rID)
	if err != nil {
		return "", err
	}
	typ, err := g.typeOf(lID)
	if err != nil {
		return "", err
	}
	other, err := g.typeOf(rID)
	if err != nil {
		return "", err
	}
	if typ != other {
		return "", g.errf(r.ID, "cannot translate node #%d: operands of %s have types %s and %s", r.ID, op, typ, other)
	}
	text, err := g.willTypeText(lID)
	if err != nil {
		return "", err
	}
	signed := !isUnsigned(text)
	g.tmp++
	name := "%t" + strconv.Itoa(g.tmp)
	switch op {
	case "&&", "||":
		instr := "and"
		if op == "||" {
			instr = "or"
		}
		fmt.Fprintf(g.out, "  %s = %s i1 %s, %s\n", name, instr, l, right)
		return name, nil
	case "==", "!=", "<", "<=", ">", ">=":
		instr, predicate := compareInstr(typ, signed, op)
		fmt.Fprintf(g.out, "  %s = %s %s %s %s, %s\n", name, instr, predicate, typ, l, right)
		return name, nil
	}
	instr := arithInstr(op, typ, signed)
	if instr == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: operator %s is not lowered yet", r.ID, op)
	}
	fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", name, instr, typ, l, right)
	return name, nil
}

// mcall and call lower a call or a method call. A call to a function of
// another package is written as mcall with the qualified name in to; a C link
// reached through the c qualifier is lowered too. A real method call is not
// lowered yet.
func (g *generator) callScalar(r willc.Record) (string, error) {
	name, text, err := g.callExpr(r)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: the call returns no value", r.ID)
	}
	if _, _, ok := llvmScalar(text); !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: a struct result is a place, not a scalar value", r.ID)
	}
	return name, nil
}

// callExpr lowers one call or method call and returns the value and its result
// type text. A void call returns an empty name and an empty text.
func (g *generator) callExpr(r willc.Record) (string, string, error) {
	if qualified := textField(r, "to"); qualified != "" {
		return g.emitCall(r, qualified, false)
	}
	if r.Kind == "mcall" {
		name := fieldText(r, "text")
		recv := g.recs[refField(r, "recv")]
		cimport := refField(recv, "to")
		if recv.Kind == "name" && cimport != 0 {
			if link := g.linkNamed(cimport, name); link != "" {
				return g.emitCall(r, link, true)
			}
		}
		return "", "", g.errf(r.ID, "cannot translate node #%d: method call %s is not lowered yet", r.ID, name)
	}
	target := refField(r, "to")
	name := g.cur.funcs[target]
	link := false
	if name == "" {
		name = g.cur.links[target]
		link = true
	}
	if name == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the callee is not a file function or a c link", r.ID)
	}
	return g.emitCall(r, name, link)
}

// callResultStruct returns the lowered struct type a call returns. A struct
// result from another package needs that package's field layout, which this
// document does not carry.
func (g *generator) callResultStruct(r willc.Record) (*structInfo, error) {
	if textField(r, "to") != "" {
		return nil, g.errf(r.ID, "cannot translate node #%d: a struct result from another package is not lowered yet", r.ID)
	}
	fn, ok := g.recs[refField(r, "to")]
	if !ok || fn.Kind != "func" {
		return nil, g.errf(r.ID, "cannot translate node #%d: the callee is not a file function", r.ID)
	}
	info, ok := g.ensureStruct(g.typeRef(fn.ID))
	if !ok {
		return nil, g.errf(r.ID, "cannot translate node #%d: the result type is not lowered yet", r.ID)
	}
	return info, nil
}

// linkNamed returns the C symbol of name declared under one cimport record.
func (g *generator) linkNamed(cimport int, name string) string {
	for _, r := range g.recs {
		if r.Kind == "link" && refField(r, "parent") == cimport && fieldText(r, "text") == name {
			return name
		}
	}
	return ""
}

// emitCall writes one call and returns its value with its result type text. A
// scalar argument is passed by value with its own type; an aggregate argument
// is passed as the borrow the callee's pointer parameter takes.
func (g *generator) emitCall(r willc.Record, name string, link bool) (string, string, error) {
	args := g.callArgs(r)
	parts := make([]string, 0, len(args))
	for _, a := range args {
		typ, err := g.typeOf(a)
		if err != nil {
			return "", "", err
		}
		if isAggregateType(typ) {
			if link {
				return "", "", g.errf(a, "cannot translate node #%d: a C link takes only scalar arguments yet", a)
			}
			ptr, ptrType, err := g.place(a)
			if err != nil {
				return "", "", err
			}
			if ptrType != typ {
				return "", "", g.errf(a, "cannot translate node #%d: the argument needs %s", a, typ)
			}
			parts = append(parts, "ptr "+ptr)
			continue
		}
		if typ == "" {
			return "", "", g.errf(a, "cannot translate node #%d: the argument has no scalar type", a)
		}
		v, err := g.value(a)
		if err != nil {
			return "", "", err
		}
		parts = append(parts, typ+" "+v)
	}
	resultText, err := g.callResultText(r)
	if err != nil {
		return "", "", err
	}
	if link {
		if resultText == "" {
			// A C link's result type comes from its header, which is not read
			// yet; these links have always been declared as i32.
			resultText = "i32"
		}
		if resultText != "i32" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: a C link results in %s, which is not lowered yet", r.ID, resultText)
		}
		g.declare(name, len(parts))
	}
	if resultText == "" {
		fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(name), strings.Join(parts, ", "))
		return "", "", nil
	}
	result, _, scalar := llvmScalar(resultText)
	if !scalar {
		info, err := g.callResultStruct(r)
		if err != nil {
			return "", "", err
		}
		result = info.llvm
	}
	g.tmp++
	tmp := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = call %s @%s(%s)\n", tmp, result, llvmSymbol(name), strings.Join(parts, ", "))
	return tmp, resultText, nil
}

func (g *generator) declare(name string, args int) {
	if g.declared[name] {
		return
	}
	g.declared[name] = true
	fmt.Fprintf(g.out, "declare i32 @%s(%s)\n", llvmSymbol(name), i32Types(args))
}

func i32Types(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("i32, ", n-1) + "i32"
}

// callArgs lists the argument records of a call in id order. A method call
// also has a receiver child, which is not an argument.
func (g *generator) callArgs(r willc.Record) []int {
	skip := 0
	if r.Kind == "mcall" {
		skip = refField(r, "recv")
	}
	var ids []int
	for _, c := range g.recs {
		if refField(c, "parent") != r.ID || c.Kind == "type" || c.ID == skip {
			continue
		}
		ids = append(ids, c.ID)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

func (g *generator) children(parent int) []willc.Record {
	var out []willc.Record
	for _, r := range g.recs {
		if refField(r, "parent") == parent && r.Kind != "type" {
			out = append(out, r)
		}
	}
	return out
}

func refField(r willc.Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.RefValue {
			return f.Value.Ref
		}
	}
	return 0
}

// textField returns a field written as quoted text, or "" when the field holds
// a node reference.
func textField(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.TextValue {
			return f.Value.Text
		}
	}
	return ""
}

// hasChild reports whether one record has a child of the given kind.
func hasChild(recs map[int]willc.Record, parent int, kind string) bool {
	for _, r := range recs {
		if r.Kind == kind && refField(r, "parent") == parent {
			return true
		}
	}
	return false
}

// fieldText reads a text field; unquoted words such as lit kind=int are text
// too.
func fieldText(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name != name {
			continue
		}
		switch f.Value.Kind {
		case willc.TextValue, willc.WordValue:
			return f.Value.Text
		}
	}
	return ""
}

func sortByID(rs []willc.Record) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].ID < rs[j-1].ID; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}
