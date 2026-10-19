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
	return EmitModuleIn(docs, entry, "")
}

// EmitModuleIn is EmitModule with the package that may declare the entry. A
// dependency can declare a function with the entry's name, and only the entry
// package's one is the program entry.
func EmitModuleIn(docs []*willc.Doc, entry, entryPkg string) ([]byte, error) {
	return EmitModuleWithAliases(docs, entry, entryPkg, nil)
}

// EmitModuleWithAliases is EmitModuleIn with the import aliases of the batch,
// so a qualified member that uses a replace key resolves to the package's
// canonical symbol.
func EmitModuleWithAliases(docs []*willc.Doc, entry, entryPkg string, aliases map[string]string) ([]byte, error) {
	g := &generator{
		scope:         map[int]string{},
		slots:         map[int]string{},
		declared:      map[string]bool{},
		stringsByText: map[string]string{},
		byteGlobals:   map[string]string{},
		out:           &strings.Builder{},
		aliases:       aliases,
	}
	for _, doc := range docs {
		g.docs = append(g.docs, newDocState(doc))
	}
	// A void entry cannot be the C main itself, so it keeps a mangled symbol
	// and the generated wrapper calls it.
	g.findEntry(entry, entryPkg)
	g.collectSignatures()
	g.collectGlobals()
	for _, d := range g.docs {
		g.use(d)
		if err := g.emitDoc(); err != nil {
			return nil, err
		}
	}
	if g.entryFound {
		if err := g.emitWrapper(); err != nil {
			return nil, err
		}
	}
	g.emitAux()
	return []byte(g.out.String()), nil
}

// emitAux writes the module-level values the functions produced while they
// were lowered: the strings the runtime checks name and, when a check was
// emitted, the panic declaration. Both may follow the functions because LLVM
// resolves forward references to global values.
func (g *generator) emitAux() {
	for _, s := range g.strings {
		g.out.WriteString(s)
	}
	if g.needPanicDecl {
		g.out.WriteString("declare void @willrt_panic(ptr, ptr, ptr)\n")
	}
	if g.needUTF8Decl {
		g.out.WriteString("declare i32 @willrt_utf8_decode(ptr, i64, ptr)\n")
	}
	if g.needAllocDecl {
		g.out.WriteString("declare ptr @willrt_alloc(i64)\n")
		g.out.WriteString("declare void @willrt_free(ptr)\n")
	}
	if g.needMemcmpDecl {
		g.out.WriteString("declare i32 @memcmp(ptr, ptr, i64)\n")
	}
}

// docState is the per-file part of the translation: record ids and the callable
// names mean something only inside one document.
type docState struct {
	path  string
	recs  map[int]willc.Record
	funcs map[int]string
	links map[int]string
	// methods maps <type>.<method> to the symbol the method is emitted under.
	methods map[string]string
	// moduleID is the module record of this document, and globals maps each
	// top-level variable to its symbol and lowered type.
	moduleID int
	globals  map[int]*globalInfo
}

func newDocState(doc *willc.Doc) *docState {
	d := &docState{
		recs:    map[int]willc.Record{},
		funcs:   map[int]string{},
		links:   map[int]string{},
		methods: map[string]string{},
	}
	for _, r := range doc.Records {
		d.recs[r.ID] = r
		if r.Kind == "module" {
			d.path = fieldText(r, "path")
			d.moduleID = r.ID
		}
	}
	return d
}

// findEntry records the package and name of the entry function. A void entry
// also gets a mangled symbol, because the generated i32 main calls it.
func (g *generator) findEntry(entry, entryPkg string) {
	if entry == "" {
		return
	}
	for _, d := range g.docs {
		if entryPkg != "" && d.path != entryPkg {
			continue
		}
		for _, r := range d.recs {
			if r.Kind != "func" || fieldText(r, "text") != entry {
				continue
			}
			if hasChild(d.recs, r.ID, "receiver") {
				continue
			}
			g.entryName = entry
			g.entryPkg = d.path
			g.entrySymbol = qualify(d.path, "will$"+entry)
			g.entryResult = fieldText(r, "type")
			g.entryID = r.ID
			g.entryFound = true
			for _, c := range d.recs {
				if c.Kind == "param" && refField(c, "parent") == r.ID {
					g.entryHasParam = true
				}
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
	g.structTypes = map[string]bool{}
	g.enumTypes = map[string]bool{}
	g.ifaceTypes = map[string]bool{}
	g.declKinds = map[string]string{}
	g.methodsQualified = map[string]string{}
	g.paramTypes = map[string][]string{}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind == "struct" || r.Kind == "enum" || r.Kind == "interface" {
				g.declKinds[qualify(d.path, fieldText(r, "text"))] = r.Kind
			}
		}
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "func" {
				continue
			}
			name := fieldText(r, "text")
			sym := qualify(d.path, name)
			var params []string
			for _, c := range d.recs {
				if c.Kind == "param" && refField(c, "parent") == r.ID {
					params = append(params, fieldText(c, "type"))
				}
			}
			for _, c := range d.recs {
				if c.Kind == "receiver" && refField(c, "parent") == r.ID {
					sym = qualify(d.path, receiverType(&c)+"."+name)
					g.methodsQualified[receiverType(&c)+"."+name] = sym
					g.methodsQualified[qualify(d.path, receiverType(&c)+"."+name)] = sym
					break
				}
			}
			g.paramTypes[sym] = params
			res := fieldText(r, "type")
			kind := ""
			for _, c := range d.recs {
				if c.Kind != "type" || refField(c, "parent") != r.ID {
					continue
				}
				if q := textField(c, "to"); q != "" {
					res = q
					kind = g.declKinds[q]
				} else if id := refField(c, "to"); id != 0 {
					if s, ok := d.recs[id]; ok && (s.Kind == "struct" || s.Kind == "enum" || s.Kind == "interface") {
						res = qualify(d.path, fieldText(s, "text"))
						kind = s.Kind
					}
				}
			}
			g.sigs[sym] = res
			switch kind {
			case "struct":
				g.structTypes[res] = true
			case "enum":
				g.enumTypes[res] = true
			case "interface":
				g.ifaceTypes[res] = true
			}
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
	g.enums = map[int]*enumInfo{}
	g.enumByLLVM = map[string]*enumInfo{}
	g.enumByName = map[string]*enumInfo{}
	g.variantByID = map[int]*variantInfo{}
	g.enumSlots = map[int]string{}
	g.patternVals = map[int]string{}
	g.patternPlaces = map[int]string{}
	g.patternTypes = map[int]string{}
	g.patternTexts = map[int]string{}
	g.matchSlots = map[int]string{}
	g.matchSlotTypes = map[int]string{}
	g.adts = map[string]*adtInfo{}
	g.adtSlots = map[int]string{}
	g.newSlots = map[int]string{}
	g.strSlots = map[int]string{}
	g.utf8Slots = map[int]string{}
	g.utf8nSlots = map[int]string{}
	g.trySlots = map[int]string{}
	g.ifaces = map[int]*ifaceInfo{}
	g.ifaceByLLVM = map[string]*ifaceInfo{}
	g.ifaceByName = map[string]*ifaceInfo{}
	g.ifaceSlots = map[string]string{}
	g.ifaceRetSlots = map[int]string{}
	g.vtables = map[string]string{}
	g.callSlots = map[int]string{}
	g.sliceSlots = map[int]string{}
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
	// enums holds the lowered enums of the current document by record id;
	// enumByLLVM looks one up by its LLVM type name. enumSlots maps a
	// construction to its temporary slot, and patternVals and patternPlaces
	// hold the bindings of the arm being lowered.
	enums        map[int]*enumInfo
	enumByLLVM   map[string]*enumInfo
	enumByName   map[string]*enumInfo
	variantByID  map[int]*variantInfo
	enumSlots    map[int]string
	patternVals   map[int]string
	patternPlaces map[int]string
	patternTypes  map[int]string
	patternTexts  map[int]string
	// matchSlots maps a match used as an expression to its result slot, and
	// matchSlotTypes to the LLVM type stored there.
	matchSlots     map[int]string
	matchSlotTypes map[int]string
	// enumTypes holds the qualified names whose result is an enum, so a caller
	// whose document lacks the declaration still knows the type.
	enumTypes map[string]bool
	// adts caches the lowered Option<T> and Result<T,E> types by their text,
	// and adtSlots maps a constructor to the slot it builds its value in.
	adts     map[string]*adtInfo
	adtSlots map[int]string
	// newSlots maps a new expression to the slot its storage is built in.
	newSlots map[int]string
	// strSlots maps a str or bytes literal to the two-word value built for it,
	// and byteGlobals shares identical literal data.
	strSlots    map[int]string
	byteGlobals map[string]string
	byteCount   int
	// utf8Slots holds the u32 output of a UTF-8 decode for a str index or a
	// str loop, and needUTF8Decl records that the runtime call is used.
	utf8Slots     map[int]string
	utf8nSlots    map[int]string
	needUTF8Decl  bool
	// fnResultText is the will result type of the function being lowered, and
	// trySlots maps a try expression to the slot its error branch returns.
	fnResultText string
	trySlots     map[int]string
	// ifaces and ifaceByName hold the interfaces of the current document, and
	// ifaceTypes the qualified names of every interface of the module.
	ifaces       map[int]*ifaceInfo
	ifaceByLLVM  map[string]*ifaceInfo
	ifaceByName  map[string]*ifaceInfo
	ifaceTypes   map[string]bool
	ifaceSlots   map[string]string
	ifaceRetSlots map[int]string
	vtables      map[string]string
	paramTypes   map[string][]string
	// errorIface is the predeclared Error interface, and errorEmitted records
	// that its module-wide type definition is already written.
	errorIface   *ifaceInfo
	errorEmitted bool
	usedError    bool
	// needAllocDecl records that the lowering calls the allocator.
	needAllocDecl bool
	// needMemcmpDecl records that the lowering compares str or byte data.
	needMemcmpDecl bool
	// declKinds maps a qualified type name to "struct" or "enum".
	declKinds map[string]string
	// callSlots maps a call whose result is a struct to the temporary slot its
	// value is stored in, so a field can be read from it.
	callSlots map[int]string
	// sliceSlots maps a slice expression to the temporary slot holding the view
	// it builds.
	sliceSlots map[int]string
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
	// structTypes holds the qualified names whose result is a struct, so a
	// caller whose document lacks the record still knows the name is a struct.
	structTypes map[string]bool
	// methodsQualified maps <qualified type>.<method> to the method's symbol,
	// for receivers whose type is declared in another document.
	methodsQualified map[string]string

	// pkg is the package path of the current document.
	pkg string
	// entrySymbol is the mangled symbol of the program entry; entryResult is
	// its result type text, and entryFound reports that it was located.
	entryName     string
	entryPkg      string
	entrySymbol   string
	entryResult   string
	entryFound    bool
	entryID       int
	entryHasParam bool
	// globalSymbols indexes every top-level variable of the module by its
	// qualified symbol, and inits lists the package init functions in order.
	globalSymbols map[string]*globalInfo
	inits         []string
	// aliases maps an import path that aliases a package to its canonical
	// path, so a qualified member resolves to the symbol the package emits.
	aliases map[string]string
	// fnSymbol is the stable symbol of the function being lowered; a runtime
	// check names it in the report. strings holds the module-level string
	// constants the checks use, and stringsByText shares equal texts.
	fnSymbol      string
	strings       []string
	stringsByText map[string]string
	needPanicDecl bool
}

// docUsesError reports whether any record of the current document names the
// predeclared Error interface. The definition must precede the functions that
// allocate a Result holding one, so it is written at the top of the first
// document that mentions it.
func (g *generator) docUsesError() bool {
	for _, r := range g.recs {
		for _, f := range r.Fields {
			if strings.Contains(f.Value.Text, "Error") {
				return true
			}
		}
	}
	return false
}

// emitDoc lowers every function of the current document, in source order.
func (g *generator) emitDoc() error {
	if !g.errorEmitted && g.docUsesError() {
		g.out.WriteString("%interface.Error = type { ptr, ptr }\n")
		g.errorEmitted = true
	}
	g.collect()
	g.buildStructs()
	g.buildEnums()
	g.buildInterfaces()
	g.emitGlobals(g.cur)
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
	return g.emitInit(g.cur)
}

// emitWrapper writes the C entry the runtime calls: it starts the runtime,
// runs every package initializer in dependency order, calls the program entry,
// and shuts the runtime down.
func (g *generator) emitWrapper() error {
	if g.entryHasParam {
		return g.errf(g.entryID, "cannot translate node #%d: the entry takes no parameters", g.entryID)
	}
	if g.entryResult != "" && g.entryResult != "i32" {
		return g.errf(g.entryID, "cannot translate node #%d: the entry result %s is not lowered yet", g.entryID, g.entryResult)
	}
	g.out.WriteString("declare void @willrt_init()\n")
	g.out.WriteString("declare void @willrt_shutdown()\n")
	g.out.WriteString("define i32 @main() {\nentry:\n  call void @willrt_init()\n")
	for _, init := range g.inits {
		fmt.Fprintf(g.out, "  call void @%s()\n", llvmSymbol(init))
	}
	if g.entryResult == "" {
		fmt.Fprintf(g.out, "  call void @%s()\n", llvmSymbol(g.entrySymbol))
		g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 0\n}\n")
		return nil
	}
	fmt.Fprintf(g.out, "  %%r = call i32 @%s()\n", llvmSymbol(g.entrySymbol))
	g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 %r\n}\n")
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
				sym := qualify(g.pkg, receiverType(recv)+"."+name)
				g.cur.funcs[r.ID] = sym
				g.cur.methods[receiverType(recv)+"."+name] = sym
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
	if g.entryFound && g.entryPkg == g.pkg && g.entryName == name {
		return g.entrySymbol
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
	g.fnSymbol = g.cur.funcs[fn.ID]
	recv := g.receiverOf(fn.ID)
	result := ""
	g.fnResultText = ""
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
	g.fnResultText = result
	resLLVM := ""
	if typ, ok := llvmTypeText(result); ok {
		resLLVM = typ
	} else if adt, ok := g.adtType(result); ok {
		resLLVM = adt.llvm
	} else if result == "Error" {
		g.usedError = true
		resLLVM = "%interface.Error"
	} else if q := g.typeQualified(fn.ID); q != "" {
		switch {
		case g.structTypes[q]:
			resLLVM = qualifiedTypeName(q)
		case g.enumTypes[q]:
			resLLVM = qualifiedEnumName(q)
		case g.ifaceTypes[q]:
			resLLVM = qualifiedIfaceName(q)
		default:
			return g.errf(fn.ID, "cannot translate node #%d: result type %s is not lowered yet", fn.ID, result)
		}
	} else if id := g.typeRef(fn.ID); id != 0 {
		if r, ok := g.recs[id]; ok && r.Kind == "enum" {
			info := g.enums[id]
			if info == nil {
				return g.errf(fn.ID, "cannot translate node #%d: enum %s is not lowered yet", fn.ID, fieldText(r, "text"))
			}
			resLLVM = info.llvm
		} else if r, ok := g.recs[id]; ok && r.Kind == "interface" {
			info := g.ifaces[id]
			if info == nil {
				return g.errf(fn.ID, "cannot translate node #%d: interface %s is not lowered yet", fn.ID, fieldText(r, "text"))
			}
			resLLVM = info.llvm
		} else {
			info, ok := g.ensureStruct(id)
			if !ok {
				return g.errf(fn.ID, "cannot translate node #%d: the result type is not lowered yet", fn.ID)
			}
			resLLVM = info.llvm
		}
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
	g.fnResultText = ""
	g.fnSymbol = g.cur.funcs[fn.ID]
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
	// A call whose result is a struct or an enum also needs a slot: its value
	// has to be stored before a field can be read from it or before it is
	// matched.
	var calls []willc.Record
	for _, r := range g.recs {
		if (r.Kind == "call" || r.Kind == "mcall") && g.enclosingFunc(r.ID) == fn {
			calls = append(calls, r)
		}
	}
	sortByID(calls)
	for _, r := range calls {
		if info, _ := g.enumVariantCall(r); info != nil {
			continue
		}
		if _, _, _, ok := g.adtConstructor(r); ok {
			continue
		}
		text, err := g.callResultText(r)
		if err != nil || text == "" || (!g.structTypes[text] && !g.enumTypes[text]) {
			if t, ok := llvmTypeText(text); ok && isAggregateType(t) {
				name := "%v.ret." + strconv.Itoa(r.ID)
				g.callSlots[r.ID] = name
				fmt.Fprintf(g.out, "  %s = alloca %s\n", name, t)
			} else if adt, ok := g.adtType(text); ok && adt.llvm != "" {
				name := "%v.ret." + strconv.Itoa(r.ID)
				g.callSlots[r.ID] = name
				fmt.Fprintf(g.out, "  %s = alloca %s\n", name, adt.llvm)
			}
			continue
		}
		name := "%v.ret." + strconv.Itoa(r.ID)
		g.callSlots[r.ID] = name
		typ := qualifiedTypeName(text)
		if g.enumTypes[text] {
			typ = qualifiedEnumName(text)
		}
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
	}
	// An argument converted to an interface is built in a slot of its own, so
	// the callee receives a stable two-word value for the call.
	for _, r := range calls {
		params := g.callParamTypes(r)
		for i, a := range g.callArgs(r) {
			if i >= len(params) {
				break
			}
			iface, ok := g.ifaceOfText(params[i])
			if !ok {
				continue
			}
			argText, err := g.willTypeText(a)
			if err != nil {
				continue
			}
			if _, same := g.ifaceOfText(argText); same {
				continue
			}
			key := fmt.Sprintf("%d.%d", r.ID, i)
			slot := "%v.iface." + strconv.Itoa(r.ID) + "." + strconv.Itoa(i)
			g.ifaceSlots[key] = slot
			fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, iface.llvm)
		}
	}
	// A new value is built in a slot: the field defaults and init write it
	// in place before it is copied out.
	var news []willc.Record
	for _, r := range g.recs {
		if r.Kind == "new" && g.enclosingFunc(r.ID) == fn {
			news = append(news, r)
		}
	}
	sortByID(news)
	for _, r := range news {
		typ, ok := g.typeLLVMOf(fieldText(r, "type"))
		if !ok || !isAggregateType(typ) {
			return g.errf(r.ID, "cannot translate node #%d: new type %s is not lowered yet", r.ID, fieldText(r, "type"))
		}
		name := "%v.new." + strconv.Itoa(r.ID)
		g.newSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
	}
	// A str or bytes literal is a two-word value, built in a slot.
	var strs []willc.Record
	for _, r := range g.recs {
		if r.Kind != "lit" || g.enclosingFunc(r.ID) != fn {
			continue
		}
		if kind := fieldText(r, "kind"); kind == "str" || kind == "bytes" {
			strs = append(strs, r)
		}
	}
	sortByID(strs)
	for _, r := range strs {
		name := "%v.str." + strconv.Itoa(r.ID)
		g.strSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca { ptr, i64 }\n", name)
	}
	// A str index and a str loop both decode one scalar into a u32 slot.
	var decodes []willc.Record
	for _, r := range g.recs {
		if g.enclosingFunc(r.ID) != fn {
			continue
		}
		switch r.Kind {
		case "index":
			if text, err := g.willTypeText(refField(r, "recv")); err == nil && text == "str" {
				decodes = append(decodes, r)
			}
		case "for":
			if text, err := g.willTypeText(refField(r, "iter")); err == nil && text == "str" {
				decodes = append(decodes, r)
			}
		}
	}
	sortByID(decodes)
	for _, r := range decodes {
		name := "%v.utf8." + strconv.Itoa(r.ID)
		g.utf8Slots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca i32\n", name)
		if r.Kind == "for" {
			advance := "%v.utf8n." + strconv.Itoa(r.ID)
			g.utf8nSlots[r.ID] = advance
			fmt.Fprintf(g.out, "  %s = alloca i64\n", advance)
		}
	}
	// A constructed enum is built in a slot of its own.
	var enumCtors []willc.Record
	for _, r := range g.recs {
		if r.Kind == "mcall" && g.enclosingFunc(r.ID) == fn {
			enumCtors = append(enumCtors, r)
		}
	}
	sortByID(enumCtors)
	for _, r := range enumCtors {
		info, _ := g.enumVariantCall(r)
		if info == nil {
			continue
		}
		name := "%v.enum." + strconv.Itoa(r.ID)
		g.enumSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
	}
	// A constructed Option or Result, including None, also needs a slot.
	var adtCtors []willc.Record
	for _, r := range g.recs {
		if (r.Kind == "call" || r.Kind == "name") && g.enclosingFunc(r.ID) == fn {
			adtCtors = append(adtCtors, r)
		}
	}
	sortByID(adtCtors)
	for _, r := range adtCtors {
		info, _, _, ok := g.adtConstructor(r)
		if !ok {
			continue
		}
		name := "%v.adt." + strconv.Itoa(r.ID)
		g.adtSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
	}
	// A return of a concrete value as an interface is built in a slot of its
	// own, then loaded as the two-word result.
	if iface, ok := g.ifaceOfText(g.fnResultText); ok {
		var rets []willc.Record
		for _, r := range g.recs {
			if r.Kind == "return" && g.enclosingFunc(r.ID) == fn {
				rets = append(rets, r)
			}
		}
		sortByID(rets)
		for _, r := range rets {
			valueID := refField(r, "value")
			if valueID == 0 {
				continue
			}
			valueText, err := g.willTypeText(valueID)
			if err != nil {
				continue
			}
			if _, same := g.ifaceOfText(valueText); same {
				continue
			}
			name := "%v.ifaceret." + strconv.Itoa(r.ID)
			g.ifaceRetSlots[r.ID] = name
			fmt.Fprintf(g.out, "  %s = alloca %s\n", name, iface.llvm)
		}
	}
	// A try expression returns an error from the current function, so its
	// error value is built in a slot of the function's result type.
	var tries []willc.Record
	for _, r := range g.recs {
		if r.Kind == "try" && g.enclosingFunc(r.ID) == fn {
			tries = append(tries, r)
		}
	}
	sortByID(tries)
	for _, r := range tries {
		info, ok := g.adtType(g.fnResultText)
		if !ok {
			return g.errf(r.ID, "cannot translate node #%d: ? needs an Option or Result result", r.ID)
		}
		name := "%v.try." + strconv.Itoa(r.ID)
		g.trySlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
	}
	// A match used as an expression stores each arm's value in a slot and
	// loads it after the merge, so no phi needs a predecessor label.
	var matches []willc.Record
	for _, r := range g.recs {
		if r.Kind == "match" && g.enclosingFunc(r.ID) == fn {
			matches = append(matches, r)
		}
	}
	sortByID(matches)
	for _, r := range matches {
		typ, err := g.matchValueType(r)
		if err != nil {
			return err
		}
		if typ == "" {
			continue
		}
		name := "%v.match." + strconv.Itoa(r.ID)
		g.matchSlots[r.ID] = name
		g.matchSlotTypes[r.ID] = typ
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
	}
	// A slice expression builds a two-word view, which also needs a slot.
	var slices []willc.Record
	for _, r := range g.recs {
		if r.Kind == "slice" && g.enclosingFunc(r.ID) == fn {
			slices = append(slices, r)
		}
	}
	sortByID(slices)
	for _, r := range slices {
		name := "%v.slice." + strconv.Itoa(r.ID)
		g.sliceSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca { ptr, i64 }\n", name)
	}
	// A range loop binds one counter, which also needs a slot.
	var loopRecs []willc.Record
	for _, r := range g.recs {
		if r.Kind == "for" && g.enclosingFunc(r.ID) == fn {
			loopRecs = append(loopRecs, r)
		}
	}
	sortByID(loopRecs)
	for _, r := range loopRecs {
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
		if iterText == "str" {
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
			elemSlot := g.newSlot(elemName)
			g.loopBinds[r.ID][elemName] = elemSlot
			g.bindLoopType(r.ID, elemName, "u32")
			fmt.Fprintf(g.out, "  %s = alloca i32\n", elemSlot)
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
		base := strings.TrimSuffix(op, "=")
		current := g.load(typ, ptr)
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		signed := !isUnsigned(text)
		var tmp string
		if !isFloatType(typ) && isCheckedArith(base) {
			tmp, err = g.checkedArith(base, typ, signed, current, value, valueID, r)
			if err != nil {
				return err
			}
		} else {
			instr := arithInstr(base, typ, signed)
			if instr == "" {
				return g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, op)
			}
			tmp = g.newTmp()
			fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", tmp, instr, typ, current, value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, tmp, ptr)
		return nil
	}
	if iface, ok := g.ifaceOfType(typ); ok {
		valueText, err := g.willTypeText(valueID)
		if err != nil {
			return err
		}
		if _, same := g.ifaceOfText(valueText); !same {
			if _, err := g.ifaceValue(ptr, valueID, iface, valueText); err != nil {
				return err
			}
			return nil
		}
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
	if iface, ok := g.ifaceOfType(typ); ok {
		valueText, err := g.willTypeText(valueID)
		if err != nil {
			return false, err
		}
		if _, same := g.ifaceOfText(valueText); !same {
			if _, err := g.ifaceValue(slot, valueID, iface, valueText); err != nil {
				return false, err
			}
			return false, nil
		}
	}
	if isAggregateType(typ) {
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
		done, err := g.stmt(st, void)
		if err != nil {
			return false, err
		}
		terminated = done
	}
	return terminated, nil
}

// stmt lowers one statement and reports whether it ends the current block.
func (g *generator) stmt(st willc.Record, void bool) (bool, error) {
	switch st.Kind {
	case "return":
		return g.returnStmt(st, void)
	case "if":
		return g.ifStmt(st, void)
	case "for":
		return g.forStmt(st, void)
	case "match":
		return g.matchStmt(st, void)
	case "break":
		return g.jump(true)
	case "continue":
		return g.jump(false)
	case "var", "const":
		return g.declStmt(st)
	case "assign":
		return false, g.assignStmt(st)
	case "exprstmt":
		return false, g.exprStmt(st)
	case "block":
		return g.body(st, void)
	case "release":
		// Ownership release is still a boundary: the value runs to the end of
		// its scope without an explicit free. The allocator contract tests
		// cover the free path; the lowering will emit it with the ownership
		// pass.
		return false, nil
	}
	return false, g.errf(st.ID, "cannot translate node #%d: statement %s is not lowered yet", st.ID, st.Kind)
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
	if iface, ok := g.ifaceOfText(g.fnResultText); ok {
		valueText, err := g.willTypeText(valueID)
		if err != nil {
			return false, err
		}
		if _, same := g.ifaceOfText(valueText); !same {
			slot := g.ifaceRetSlots[st.ID]
			if slot == "" {
				return false, g.errf(st.ID, "cannot translate node #%d: the interface result has no slot", st.ID)
			}
			if _, err := g.ifaceValue(slot, valueID, iface, valueText); err != nil {
				return false, err
			}
			v := g.load(iface.llvm, slot)
			fmt.Fprintf(g.out, "  ret %s %s\n", iface.llvm, v)
			return true, nil
		}
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
		if iterText == "str" {
			return g.forStr(r, void, n)
		}
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
		case "str", "bytes":
			return "", g.errf(id, "cannot translate node #%d: a %s literal is a place, not a scalar", id, kind)
		}
		return "", g.errf(id, "cannot translate node #%d: literal kind %s is not lowered yet", id, fieldText(r, "kind"))
	case "name":
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if c, ok := g.recs[decl]; ok && c.Kind == "const" {
			if v, ok := g.constValueOf(decl); ok {
				return v, nil
			}
			return "", g.errf(id, "cannot translate node #%d: the constant %s is not a literal yet", id, name)
		}
		if info := g.globalRef(r); info != nil {
			return g.load(info.typ, "@"+llvmSymbol(info.sym)), nil
		}
		if v, ok := g.patternVals[decl]; ok {
			return v, nil
		}
		if _, ok := g.patternPlaces[decl]; ok {
			return "", g.errf(id, "cannot translate node #%d: %s is an aggregate binding, not a scalar", id, name)
		}
		if _, _, _, ok := g.adtConstructor(r); ok {
			return "", g.errf(id, "cannot translate node #%d: an Option or Result value is a place, not a scalar", id)
		}
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
		if info, ok := g.importMember(r); ok {
			if info.isConst {
				if info.value == "" {
					return "", g.errf(id, "cannot translate node #%d: the imported constant is not a literal yet", id)
				}
				return info.value, nil
			}
			return g.load(info.typ, "@"+llvmSymbol(info.sym)), nil
		}
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
	case "slice":
		return "", g.errf(id, "cannot translate node #%d: a view is a place, not a scalar value", id)
	case "index":
		if text, err := g.willTypeText(refField(r, "recv")); err == nil && text == "str" {
			return g.strIndex(r)
		}
		ptr, typ, err := g.indexAddr(r)
		if err != nil {
			return "", err
		}
		if isAggregateType(typ) {
			return "", g.errf(id, "cannot translate node #%d: the element is an aggregate, not a scalar", id)
		}
		return g.load(typ, ptr), nil
	case "cast":
		return g.castValue(r)
	case "match":
		return g.matchValue(r)
	case "try":
		return g.tryValue(r)
	case "new":
		return "", g.errf(id, "cannot translate node #%d: a new value is a place, not a scalar", id)
	case "un":
		return g.unary(r)
	case "bin":
		return g.binary(r)
	case "call", "mcall":
		if _, _, _, ok := g.adtConstructor(r); ok {
			return "", g.errf(id, "cannot translate node #%d: an Option or Result value is a place, not a scalar", id)
		}
		if info, _ := g.enumVariantCall(r); info != nil {
			return "", g.errf(id, "cannot translate node #%d: an enum value is a place, not a scalar", id)
		}
		return g.callScalar(r)
	}
	return "", g.errf(id, "cannot translate node #%d: node kind %s is not lowered yet", id, r.Kind)
}

// exprStmt lowers an expression used as a statement. A call is allowed to
// return nothing here, because its result is not needed.
func (g *generator) exprStmt(st willc.Record) error {
	id := refField(st, "expr")
	if r, ok := g.recs[id]; ok && r.Kind == "discard" {
		inner := refField(r, "expr")
		typ, err := g.typeOf(inner)
		if err != nil {
			return err
		}
		if isAggregateType(typ) {
			_, _, err = g.place(inner)
			return err
		}
		_, err = g.value(inner)
		return err
	}
	if r, ok := g.recs[id]; ok && (r.Kind == "call" || r.Kind == "mcall") {
		_, _, err := g.callExpr(r)
		return err
	}
	_, err := g.value(id)
	return err
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
		text, err := g.willTypeText(refField(r, "expr"))
		if err != nil {
			return "", err
		}
		width := intWidth(typ)
		if width < 8 {
			return "", g.errf(r.ID, "cannot translate node #%d: %s cannot be negated", r.ID, typ)
		}
		bad := g.newTmp()
		if !isUnsigned(text) {
			fmt.Fprintf(g.out, "  %s = icmp eq %s %s, %s\n", bad, typ, x, signedMins[width])
		} else {
			// The negative of a nonzero unsigned value does not fit, so any
			// nonzero operand overflows the type.
			fmt.Fprintf(g.out, "  %s = icmp ne %s %s, 0\n", bad, typ, x)
		}
		g.panicCheck(bad, panicOverflow, r)
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
	if op == "==" || op == "!=" {
		lt, lerr := g.willTypeText(lID)
		rt, rerr := g.willTypeText(rID)
		if lerr == nil && rerr == nil && lt == "str" && rt == "str" {
			return g.strEqual(lID, rID, op, r)
		}
	}
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
	switch op {
	case "&&", "||":
		instr := "and"
		if op == "||" {
			instr = "or"
		}
		name := g.newTmp()
		fmt.Fprintf(g.out, "  %s = %s i1 %s, %s\n", name, instr, l, right)
		return name, nil
	case "==", "!=", "<", "<=", ">", ">=":
		instr, predicate := compareInstr(typ, signed, op)
		name := g.newTmp()
		fmt.Fprintf(g.out, "  %s = %s %s %s %s, %s\n", name, instr, predicate, typ, l, right)
		return name, nil
	}
	if !isFloatType(typ) && isCheckedArith(op) {
		return g.checkedArith(op, typ, signed, l, right, rID, r)
	}
	instr := arithInstr(op, typ, signed)
	if instr == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: operator %s is not lowered yet", r.ID, op)
	}
	name := g.newTmp()
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
		return "", g.errf(r.ID, "cannot translate node #%d: an aggregate result is a place, not a scalar value", r.ID)
	}
	return name, nil
}

// callExpr lowers one call or method call and returns the value and its result
// type text. A void call returns an empty name and an empty text.
func (g *generator) callExpr(r willc.Record) (string, string, error) {
	if info, _ := g.enumVariantCall(r); info != nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: an enum value is a place, not a scalar", r.ID)
	}
	if qualified := textField(r, "to"); qualified != "" {
		return g.emitCall(r, qualified, false)
	}
	if r.Kind == "mcall" {
		recvText, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		if elem, ok := arrayElemText(recvText); ok {
			return g.arrayMethod(r, elem)
		}
		if iface, ok := g.ifaceOfText(recvText); ok {
			return g.interfaceCall(r, iface)
		}
		name := fieldText(r, "text")
		recv := g.recs[refField(r, "recv")]
		cimport := refField(recv, "to")
		if recv.Kind == "name" && cimport != 0 {
			if link := g.linkNamed(cimport, name); link != "" {
				return g.emitCall(r, link, true)
			}
		}
		symbol, err := g.methodSymbol(r)
		if err != nil {
			return "", "", err
		}
		return g.emitCall(r, symbol, false)
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

// callResultStruct is not used; a struct result is named by its qualified type.

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
	// A method call passes its receiver first, as the borrow pointer the
	// method's hidden first parameter takes.
	if r.Kind == "mcall" && !link && textField(r, "to") == "" {
		ptr, _, err := g.place(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		parts = append(parts, "ptr "+ptr)
	}
	for i, a := range g.callArgs(r) {
		paramType := ""
		if !link && i < len(g.paramTypes[name]) {
			paramType = g.paramTypes[name][i]
		}
		var err error
		parts, err = g.callArgParts(parts, r, i, a, paramType, link)
		if err != nil {
			return "", "", err
		}
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
	result, scalar := llvmTypeText(resultText)
	if !scalar {
		if resultText == "Error" {
			g.usedError = true
			result = "%interface.Error"
		} else if adt, ok := g.adtType(resultText); ok {
			result = adt.llvm
		} else {
			switch {
			case g.structTypes[resultText]:
				result = qualifiedTypeName(resultText)
			case g.enumTypes[resultText]:
				result = qualifiedEnumName(resultText)
			case g.ifaceTypes[resultText]:
				result = qualifiedIfaceName(resultText)
			default:
				return "", "", g.errf(r.ID, "cannot translate node #%d: result type %s is not lowered yet", r.ID, resultText)
			}
		}
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
	if r.Kind == "call" {
		// A predeclared callee is written as a child name record; it is not an
		// argument.
		skip = refField(r, "to")
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
