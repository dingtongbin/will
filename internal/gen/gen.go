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
	return EmitModuleWithOptions(docs, entry, entryPkg, aliases, false)
}

// EmitModuleWithOptions is EmitModuleWithAliases plus the shared-library mode.
// In lib mode an exported top-level function keeps its plain C name and no
// program entry wrapper is emitted.
func EmitModuleWithOptions(docs []*willc.Doc, entry, entryPkg string, aliases map[string]string, lib bool) ([]byte, error) {
	g := &generator{
		lib:            lib,
		scope:          map[int]string{},
		slots:          map[int]string{},
		declared:       map[string]bool{},
		stringsByText:  map[string]string{},
		byteGlobals:    map[string]string{},
		emittedTypes:   map[string]bool{},
		emittedVtables: map[string]bool{},
		thunkEmitted:   map[string]bool{},
		spawnEmitted:   map[string]bool{},
		dropEmitted:    map[string]bool{},
		out:            &strings.Builder{},
		aliases:        aliases,
		declSet:        map[string]bool{},
		suspendable:    map[string]bool{},
		builtinErrors:  map[int]bool{},
		liveValues:     map[int]string{},
	}
	for _, doc := range docs {
		g.docs = append(g.docs, newDocState(doc))
	}
	// A void entry cannot be the C main itself, so it keeps a mangled symbol
	// and the generated wrapper calls it. A shared library has no program
	// entry.
	if !lib {
		g.findEntry(entry, entryPkg)
	}
	g.collectSignatures()
	g.collectSuspendable()
	g.collectExternalEnums()
	g.collectGlobals()
	g.planInstances()
	if g.usesTaskRuntime() {
		g.out.WriteString("%willrt_error = type { ptr, i64, ptr, ptr }\n")
	}
	if g.usesSelect() {
		g.out.WriteString("%willrt_arm = type { ptr, ptr, ptr, i32, i64 }\n")
	}
	for _, d := range g.docs {
		g.use(d)
		if err := g.emitDoc(); err != nil {
			return nil, err
		}
	}
	if g.entryFound && !g.lib {
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
	g.emitBuiltinErrors()
	g.emitTaskError()
	g.emitExternalDecls()
	for _, decl := range g.decls {
		g.out.WriteString(decl + "\n")
	}
	for _, s := range g.strings {
		g.out.WriteString(s)
	}
	if g.needPanicDecl {
		g.out.WriteString("declare void @willrt_panic(ptr, ptr, ptr)\n")
		g.out.WriteString("declare void @willrt_panic_n(ptr, i64, ptr, ptr)\n")
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
	if g.needMemmoveDecl {
		g.out.WriteString("declare void @memmove(ptr, ptr, i64)\n")
	}
	if g.needMemcpyDecl {
		g.out.WriteString("declare void @memcpy(ptr, ptr, i64)\n")
	}
	if g.needPrintDecl {
		g.out.WriteString("declare void @willrt_write_stdout(ptr, i64)\n")
		g.out.WriteString("declare void @willrt_print_i64(i64)\n")
		g.out.WriteString("declare void @willrt_print_u64(i64)\n")
		g.out.WriteString("declare void @willrt_print_f64(double)\n")
		g.out.WriteString("declare void @willrt_print_bool(i32)\n")
	}
	if g.needBlockDecl {
		g.out.WriteString("declare i32 @willrt_blocking_call(ptr, ptr, ptr)\n")
	}
	if g.needTakeCDecl {
		g.out.WriteString("declare i64 @willrt_take_c(ptr, ptr, ptr, ptr, ptr)\n")
	}
	if g.needTaskDecl {
		g.out.WriteString("declare ptr @willrt_task_new(ptr, ptr, i64, i32, i32)\n")
		g.out.WriteString("declare void @willrt_task_run(ptr)\n")
		g.out.WriteString("declare i32 @willrt_join(ptr, ptr, ptr)\n")
		g.out.WriteString("declare i32 @willrt_task_try_join(ptr, ptr, ptr)\n")
		g.out.WriteString("declare ptr @willrt_task_current()\n")
		g.out.WriteString("declare i32 @willrt_task_store_result(ptr, i64)\n")
		g.out.WriteString("declare ptr @willrt_chan_new(i64, i64, i32)\n")
		g.out.WriteString("declare i32 @willrt_chan_send(ptr, ptr)\n")
		g.out.WriteString("declare i32 @willrt_chan_try_send(ptr, ptr)\n")
		g.out.WriteString("declare i32 @willrt_chan_recv(ptr, ptr)\n")
		g.out.WriteString("declare i32 @willrt_chan_try_recv(ptr, ptr)\n")
		g.out.WriteString("declare void @willrt_chan_close(ptr)\n")
		g.out.WriteString("declare i32 @willrt_select(ptr, i64)\n")
		g.out.WriteString("declare i32 @willrt_select_status()\n")
		if !g.declSet["declare i64 @willrt_now_ns()"] {
			g.out.WriteString("declare i64 @willrt_now_ns()\n")
		}
		if g.needSelectSetDecl {
			g.out.WriteString("declare void @willrt_select_set_status(i32)\n")
		}
		g.out.WriteString("declare void @willrt_region_enter()\n")
		g.out.WriteString("declare i32 @willrt_region_exit()\n")
		g.out.WriteString("declare i32 @willrt_lane_here()\n")
	}
	if g.needJobDecl {
		g.out.WriteString("declare ptr @willrt_job_new(ptr, ptr, i64, i32)\n")
	}
	if g.needWaitDecl {
		g.out.WriteString("declare void @willrt_task_mark_wait()\n")
	}
	if g.needChanWaitDecl {
		g.out.WriteString("declare i32 @willrt_chan_recv_wait(ptr, ptr)\n")
		g.out.WriteString("declare i32 @willrt_chan_send_wait(ptr, ptr)\n")
	}
	if g.needCancelBind {
		g.out.WriteString("declare void @willrt_task_bind_cancel(ptr, ptr)\n")
	}
	if g.needReportDecl {
		g.out.WriteString("declare void @willrt_report_write_n(ptr, i64)\n")
	}
	if g.needDeadlineDecl {
		g.out.WriteString("declare i64 @willrt_task_enter_deadline(i64)\n")
		g.out.WriteString("declare void @willrt_task_exit_deadline(i64)\n")
	}
	if g.needRcDecl {
		g.out.WriteString("declare i64 @willrt_rc_new(ptr, i64)\n")
		g.out.WriteString("declare i64 @willrt_rc_new_drop(ptr, i64, ptr)\n")
		g.out.WriteString("declare void @willrt_rc_retain(i64)\n")
		g.out.WriteString("declare void @willrt_rc_share(i64)\n")
		g.out.WriteString("declare void @willrt_rc_release(i64)\n")
		g.out.WriteString("declare ptr @willrt_rc_deref(i64)\n")
		g.out.WriteString("declare i64 @willrt_weak_new(i64)\n")
		g.out.WriteString("declare i32 @willrt_weak_upgrade(i64, ptr)\n")
	}
	if g.needUtf8Decl {
		g.out.WriteString("declare i32 @willrt_utf8_valid(ptr, i64)\n")
		g.out.WriteString("declare i32 @willrt_utf8_encode(i32, ptr)\n")
	}
	if g.needBytesDecl {
		g.out.WriteString("declare i64 @willrt_bytes_index(ptr, i64, ptr, i64)\n")
		g.out.WriteString("declare i64 @willrt_bytes_count(ptr, i64, ptr, i64)\n")
		g.out.WriteString("declare i64 @willrt_bytes_trim(ptr, i64, i32, i32, ptr, ptr)\n")
		g.out.WriteString("declare void @willrt_bytes_repeat(ptr, ptr, i64, i64)\n")
		g.out.WriteString("declare i64 @willrt_bytes_replace_into(ptr, i64, ptr, i64, ptr, i64, ptr, i64)\n")
		g.out.WriteString("declare i64 @willrt_bytes_split(ptr, i64, ptr, i64, ptr, i64)\n")
		g.out.WriteString("declare i64 @willrt_bytes_fields(ptr, i64, ptr, i64)\n")
	}
	if g.needPlateDecl {
		g.out.WriteString("declare ptr @willrt_plate_new(i64, i64)\n")
	}
	if g.needInterpDecl {
		g.out.WriteString("declare i64 @willrt_fmt_i64_len(i64)\n")
		g.out.WriteString("declare i64 @willrt_fmt_u64_len(i64)\n")
		g.out.WriteString("declare i64 @willrt_fmt_f64_len(double)\n")
		g.out.WriteString("declare i64 @willrt_fmt_bool_len(i32)\n")
		g.out.WriteString("declare i64 @willrt_fmt_i64_into(ptr, i64, i64)\n")
		g.out.WriteString("declare i64 @willrt_fmt_u64_into(ptr, i64, i64)\n")
		g.out.WriteString("declare i64 @willrt_fmt_f64_into(ptr, i64, double)\n")
		g.out.WriteString("declare i64 @willrt_fmt_bool_into(ptr, i64, i32)\n")
	}
}

// docState is the per-file part of the translation: record ids and the callable
// names mean something only inside one document.
type docState struct {
	path    string
	clone   bool
	recs    map[int]willc.Record
	funcs   map[int]string
	symbols map[int]string
	links   map[int]string
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
		symbols: map[int]string{},
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
	g.methodSymbols = map[string]bool{}
	g.paramTypes = map[string][]string{}
	g.varargs = map[string]bool{}
	g.ifaceDecls = map[string]*ifaceInfo{}
	g.structDecls = map[string]structDeclInfo{}
	g.structByLLVM = map[string]string{}
	g.laneIndex = map[string]int32{}
	var laneSeq int32
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "lane" {
				continue
			}
			q := qualify(d.path, fieldText(r, "text"))
			if _, ok := g.laneIndex[q]; !ok {
				g.laneIndex[q] = laneSeq
				laneSeq++
			}
		}
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind == "struct" || r.Kind == "enum" || r.Kind == "interface" {
				g.declKinds[qualify(d.path, fieldText(r, "text"))] = r.Kind
			}
			if r.Kind != "struct" {
				continue
			}
			q := qualify(d.path, fieldText(r, "text"))
			decl := structDeclInfo{name: fieldText(r, "text")}
			var fields []willc.Record
			for _, f := range d.recs {
				if f.Kind == "field" && refField(f, "parent") == r.ID {
					fields = append(fields, f)
				}
			}
			sortByID(fields)
			for _, f := range fields {
				decl.names = append(decl.names, fieldText(f, "text"))
				decl.texts = append(decl.texts, fieldText(f, "type"))
			}
			g.structDecls[q] = decl
			g.structByLLVM[qualifiedTypeName(q)] = q
		}
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "interface" {
				continue
			}
			q := qualify(d.path, fieldText(r, "text"))
			info := &ifaceInfo{name: q, llvm: qualifiedIfaceName(q), byName: map[string]int{}}
			var sigs []willc.Record
			for _, c := range d.recs {
				if c.Kind == "method-sig" && refField(c, "parent") == r.ID {
					sigs = append(sigs, c)
				}
			}
			sortByID(sigs)
			for _, s := range sigs {
				m := ifaceMethod{name: fieldText(s, "text"), result: fieldText(s, "type")}
				var ps []willc.Record
				for _, c := range d.recs {
					if c.Kind == "param" && refField(c, "parent") == s.ID {
						ps = append(ps, c)
					}
				}
				sortByID(ps)
				for _, p := range ps {
					m.params = append(m.params, fieldText(p, "type"))
				}
				info.byName[m.name] = len(info.methods)
				info.methods = append(info.methods, m)
			}
			g.ifaceDecls[q] = info
		}
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "func" {
				continue
			}
			name := fieldText(r, "text")
			sym := qualify(d.path, name)
			var paramRecs []willc.Record
			for _, c := range d.recs {
				if c.Kind == "param" && refField(c, "parent") == r.ID {
					paramRecs = append(paramRecs, c)
				}
			}
			sortByID(paramRecs)
			var params []string
			variadic := false
			for _, c := range paramRecs {
				params = append(params, g.qualifyTypeText(d.path, fieldText(c, "type")))
				if fieldText(c, "variadic") == "true" {
					variadic = true
				}
			}
			for _, c := range d.recs {
				if c.Kind == "receiver" && refField(c, "parent") == r.ID {
					sym = qualify(d.path, receiverType(&c)+"."+name)
					g.methodsQualified[receiverType(&c)+"."+name] = sym
					g.methodsQualified[qualify(d.path, receiverType(&c)+"."+name)] = sym
					g.methodSymbols[sym] = true
					break
				}
			}
			d.symbols[r.ID] = sym
			g.paramTypes[sym] = params
			if variadic {
				g.varargs[sym] = true
			}
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
			res = g.qualifyTypeText(d.path, res)
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
	g.structInstances = map[string]*structInfo{}
	g.structDecl = map[string]int{}
	g.enumDecl = map[string]int{}
	g.ifaceDecl = map[string]int{}
	g.linkSigs = map[string]cLinkSig{}
	g.linkBlock = map[string]bool{}
	g.blockingNames = map[int]string{}
	g.pendingBlock = nil
	if g.blockingDone == nil {
		g.blockingDone = map[string]bool{}
	}
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
	g.newStoreSlots = map[int]string{}
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
	g.strCatSlots = map[int]string{}
	g.interpSlots = map[int]string{}
	if !g.packageMode {
		g.lambdaNames = map[int]string{}
		g.lambdas = map[int]*lambdaInfo{}
	}
	g.closureSlots = map[int]string{}
	g.captureFields = map[int]string{}
	g.captureSpills = map[int]string{}
	g.currentLambda = 0
	g.pendingThunks = nil
	g.spawnNames = map[int]string{}
	g.pendingSpawns = nil
	g.pendingGeneral = nil
	g.pendingAsync = nil
	g.pendingSelect = nil
	g.pendingDefers = nil
	g.smPlaces = map[int]smPlace{}
	g.deferSeq = 0
	g.deferSnapshots = map[int]deferSnapshot{}
	g.joinOutSlots = map[int]string{}
	g.joinErrSlots = map[int]string{}
	g.recvOutSlots = map[int]string{}
	g.recvOptSlots = map[int]string{}
	g.sendValueSlots = map[int]string{}
	g.weakOutSlots = map[int]string{}
	g.dropNames = map[string]string{}
	g.pendingDrops = nil
	g.selectSlots = map[int]string{}
	g.selectFastSlots = map[int]string{}
	g.selectOutSlots = map[int]string{}
	g.selectBindSlots = map[int]string{}
	g.selectBindTypes = map[int]string{}
	g.castSlots = map[int]string{}
	g.variadicSlots = map[int]string{}
	g.strMethodSlots = map[int]string{}
	g.trimStartSlots = map[int]string{}
	g.trimLenSlots = map[int]string{}
	g.pkgNames = map[string]string{}
	for _, r := range d.recs {
		if r.Kind == "import" {
			path := fieldText(r, "path")
			name := path
			if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
				name = path[slash+1:]
			}
			g.pkgNames[name] = path
		}
	}
	g.loopBinds = map[int]map[string]string{}
	g.loopTypes = map[int]map[string]string{}
	g.loopPtrs = map[int]map[string]string{}
}

type generator struct {
	// lib selects the shared-library surface: exported functions keep their
	// plain C names and there is no program entry wrapper.
	lib bool
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
	structs         map[int]*structInfo
	structInstances map[string]*structInfo
	emittedTypes    map[string]bool
	emittedVtables  map[string]bool
	structDecl      map[string]int
	enumDecl        map[string]int
	ifaceDecl       map[string]int
	byType          map[string]*structInfo
	byName          map[string]*structInfo
	aggSlots        map[int]string
	// enums holds the lowered enums of the current document by record id;
	// enumByLLVM looks one up by its LLVM type name. enumSlots maps a
	// construction to its temporary slot, and patternVals and patternPlaces
	// hold the bindings of the arm being lowered.
	enums         map[int]*enumInfo
	enumByLLVM    map[string]*enumInfo
	enumByName    map[string]*enumInfo
	variantByID   map[int]*variantInfo
	enumSlots     map[int]string
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
	// externalEnums holds every enum of the module by its package-qualified
	// name, so a document can construct a variant declared in another one.
	externalEnums map[string]*externalEnum
	// adts caches the lowered Option<T> and Result<T,E> types by their text,
	// and adtSlots maps a constructor to the slot it builds its value in.
	adts     map[string]*adtInfo
	adtSlots map[int]string
	// newSlots maps a new expression to the slot of the value it returns, and
	// newStoreSlots holds the object storage when init returns a Result.
	newSlots      map[int]string
	newStoreSlots map[int]string
	// strSlots maps a str or bytes literal to the two-word value built for it,
	// and byteGlobals shares identical literal data.
	strSlots    map[int]string
	byteGlobals map[string]string
	byteCount   int
	// utf8Slots holds the u32 output of a UTF-8 decode for a str index or a
	// str loop, and needUTF8Decl records that the runtime call is used.
	utf8Slots    map[int]string
	utf8nSlots   map[int]string
	needUTF8Decl bool
	// fnResultText is the will result type of the function being lowered, and
	// trySlots maps a try expression to the slot its error branch returns.
	fnResultText string
	trySlots     map[int]string
	// ifaces and ifaceByName hold the interfaces of the current document, and
	// ifaceTypes the qualified names of every interface of the module.
	ifaces        map[int]*ifaceInfo
	ifaceByLLVM   map[string]*ifaceInfo
	ifaceByName   map[string]*ifaceInfo
	ifaceTypes    map[string]bool
	ifaceSlots    map[string]string
	ifaceRetSlots map[int]string
	vtables       map[string]string
	paramTypes    map[string][]string
	varargs       map[string]bool
	// errorIface is the predeclared Error interface, and errorEmitted records
	// that its module-wide type definition is already written.
	errorIface   *ifaceInfo
	errorEmitted bool
	// builtinErrors records which predeclared faults this module emits.
	builtinErrors map[int]bool
	// liveValues holds the variables whose slots still need a static release
	// on the panic path, keyed by declaration id.
	liveValues map[int]string
	usedError  bool
	// General state-machine lowering keeps every alloca in the task frame and
	// splits each channel or join wait into a resume label.
	smGeneral   bool
	smStateSlot string
	smStateSeq  int
	smStates    []string
	smParamSlot map[int]string
	// needAllocDecl records that the lowering calls the allocator.
	needAllocDecl bool
	// needMemcmpDecl records that the lowering compares str or byte data.
	needMemcmpDecl bool
	// needMemmoveDecl records that a container removed an entry in place.
	needMemmoveDecl bool
	// needMemcpyDecl records that a str concatenation copied bytes.
	needMemcpyDecl bool
	// needPrintDecl records that the lowering writes to standard output, and
	// needInterpDecl that it formats a scalar into a str.
	needPrintDecl  bool
	needInterpDecl bool
	// lateAllocas holds stack slots requested after the entry block was written.
	// The marker is replaced once the function body is complete, so a fallback
	// alloca still dominates the loop that uses it instead of growing the stack
	// on every iteration.
	lateAllocas []string
	lateAllowed bool
	// needTaskDecl records that the lowering calls the task or channel engine.
	needTaskDecl bool
	// needJobDecl records that a parallel block submitted a job.
	needJobDecl bool
	// needWaitDecl records that a generated state machine marks a wait.
	needWaitDecl bool
	// needChanWaitDecl records the direct channel receive used by a state
	// machine. Programs without that shape keep the old declaration set.
	needChanWaitDecl bool
	// needSelectSetDecl records the status setter used by a no-wait select.
	needSelectSetDecl bool
	// needCancelBind records a spawn that passed a cancellation credential.
	needCancelBind bool
	// needReportDecl records that the entry wrapper reports a main Result.
	needReportDecl bool
	// needTaskErrorDecl records that Join built a TaskError interface.
	needTaskErrorDecl bool
	// needDeadlineDecl records that the lowering scopes a deadline.
	needDeadlineDecl bool
	// parallelDepth is nonzero while lowering a parallel block. A spawn inside
	// that block is a job, so it goes to the global job queue instead of
	// starting a task thread.
	parallelDepth int
	// needTakeCDecl records that the lowering adopts a foreign C pointer.
	needTakeCDecl bool
	// needRcDecl records that the lowering calls the handle table.
	needRcDecl bool
	// needUtf8Decl records a call to the UTF-8 validator or encoder,
	// needBytesDecl one to the byte search, needPlateDecl one to the plate
	// allocator, and castSlots holds the two-word result of a str cast.
	needUtf8Decl  bool
	needBytesDecl bool
	needPlateDecl bool
	castSlots     map[int]string
	// declKinds maps a qualified type name to "struct" or "enum".
	declKinds map[string]string
	// callSlots maps a call whose result is a struct to the temporary slot its
	// value is stored in, so a field can be read from it.
	callSlots map[int]string
	// sliceSlots maps a slice expression to the temporary slot holding the view
	// it builds.
	sliceSlots map[int]string
	// strCatSlots maps a str concatenation to the two-word slot it builds,
	// and interpSlots does the same for an interpolation.
	strCatSlots map[int]string
	interpSlots map[int]string
	// lambdaNames maps a lambda record to its function symbol, and lambdaSeq
	// numbers those symbols across the module.
	lambdaNames map[int]string
	lambdaSeq   int
	// lambdas holds what each lambda captures, and closureSlots the two-word
	// slot a closure value is built in. captureFields maps a captured
	// declaration to its field inside the current lambda's environment.
	lambdas       map[int]*lambdaInfo
	closureSlots  map[int]string
	captureFields map[int]string
	captureSpills map[int]string
	currentLambda int
	// pendingThunks are the wrappers that adapt a named function to the
	// closure calling convention, and thunkEmitted dedupes them by symbol.
	pendingThunks []thunkRequest
	thunkEmitted  map[string]bool
	// structDecls indexes every struct field list of the module by its
	// qualified name, so a field of a type declared in another file or
	// package still has a layout here. structByLLVM maps the lowered type name
	// back to that qualified name.
	structDecls  map[string]structDeclInfo
	structByLLVM map[string]string
	laneIndex    map[string]int32
	// spawnNames names the step function of every spawn expression, and
	// pendingSpawns holds the steps this document still has to write.
	spawnNames     map[int]string
	spawnSeq       int
	pendingSpawns  []spawnRequest
	pendingGeneral []int
	pendingAsync   []*asyncMachineInfo
	pendingSelect  []*selectMachineInfo
	spawnEmitted   map[string]bool
	// The slots below keep a loop from growing the stack on every evaluation
	// of a join, receive, or send.
	joinOutSlots    map[int]string
	joinErrSlots    map[int]string
	recvOutSlots    map[int]string
	recvOptSlots    map[int]string
	sendValueSlots  map[int]string
	weakOutSlots    map[int]string
	variadicSlots   map[int]string
	strMethodSlots  map[int]string
	trimStartSlots  map[int]string
	trimLenSlots    map[int]string
	selectSlots     map[int]string
	selectFastSlots map[int]string
	selectOutSlots  map[int]string
	selectBindSlots map[int]string
	selectBindTypes map[int]string
	// ifaceDecls holds every interface of the module by qualified name, so a
	// call into another package can build the vtable for its interface.
	ifaceDecls map[string]*ifaceInfo
	// pkgNames maps the package name a document writes to the import path.
	pkgNames map[string]string
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
	// dropNames and dropSeq name the per-type drop functions, and
	// pendingDrops holds the ones this document still has to write.
	dropNames    map[string]string
	dropSeq      int
	pendingDrops []dropRequest
	dropEmitted  map[string]bool
	// pendingReleases are the release records that precede a return in the
	// same block. They run after the return value is evaluated, so a returned
	// borrow is still alive while it is read.
	pendingReleases []willc.Record
	// pendingTryReleases are the release records before a statement that
	// contains ?. They run only on the error path of the first failing ?.
	pendingTryReleases []willc.Record
	// defers holds one frame per open block, innermost last. Each entry is the
	// deferred call site with the storage its arguments were captured in, in
	// source order.
	defers [][]deferCall
	// pendingDefers holds the thunk bodies this document still has to write.
	// deferSeq numbers those thunks across the module.
	pendingDefers []deferRequest
	deferSeq      int
	// deferSnapshots maps an argument, receiver, or callee expression to the
	// field that captured its value at the defer statement. It is non-empty
	// only while a defer thunk is being lowered.
	deferSnapshots map[int]deferSnapshot
	// deferThunk reports that a defer body is being lowered, so a conversion
	// slot belongs to the thunk rather than the original function.
	deferThunk bool
	// linkSigs holds the header prototype of each C link of the current
	// document. An untyped link keeps the i32 fallback.
	linkSigs map[string]cLinkSig
	// decls holds module-level C declarations; they cannot be written inside a
	// function body, so the call site queues them and emitAux writes them.
	decls         []string
	declSet       map[string]bool
	linkBlock     map[string]bool
	blockingNames map[int]string
	pendingBlock  []blockingRequest
	blockingDone  map[string]bool
	blockingSeq   int
	needBlockDecl bool
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
	// methodSymbols names the functions whose first ABI argument is a receiver
	// pointer. A state-machine constructor must carry that pointer too.
	methodSymbols map[string]bool

	// pkg is the package path of the current document.
	pkg string
	// packageMode emits one package into its own LLVM module. Cross-package
	// calls then need external declarations, and all non-entry definitions use
	// hidden linkage so separate objects can resolve them.
	packageMode     bool
	externalFuncs   map[string]bool
	externalSigs    map[string]externalSignature
	externalGlobals map[string]bool
	emittedFuncs    map[string]bool
	libFuncs        map[string]string
	initOrder       []string
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
	// suspendable names the functions that a spawned caller must run as a
	// state-machine task instead of calling on the lane.
	suspendable map[string]bool
	// smMode makes a return store its value on the current task and return
	// WILLRT_DONE. A state-machine step has i32 ABI even when the source
	// function returns an aggregate, so it cannot emit the ordinary ret.
	smMode bool
	// smPlaces overrides a call expression with the slot where its child task
	// stored the result. The postlude then unwraps or binds that result without
	// calling the child again.
	smPlaces map[int]smPlace
}

// smPlace is a pre-built place for one expression inside a state-machine
// postlude.
type smPlace struct {
	ptr string
	typ string
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
	if err := g.planLambdas(); err != nil {
		return err
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
		if g.hasTypeParams(fn) || g.genericMethodDecl(fn) {
			continue
		}
		if err := g.funcDecl(fn); err != nil {
			return err
		}
	}
	// A defer thunk can call a blocking C link or release an owning value,
	// and a blocking step or spawn can contain another defer. Keep draining
	// every queue until none of them gained work while the others ran.
	for pass := 0; pass < 64; pass++ {
		pending := len(g.pendingThunks) + len(g.pendingSpawns) +
			len(g.pendingGeneral) + len(g.pendingAsync) +
			len(g.pendingSelect) + len(g.pendingBlock) + len(g.pendingDrops) +
			len(g.pendingDefers)
		if pending == 0 {
			break
		}
		if err := g.emitThunks(); err != nil {
			return err
		}
		if err := g.emitSpawns(); err != nil {
			return err
		}
		if err := g.emitBlockingSteps(); err != nil {
			return err
		}
		if err := g.emitDrops(); err != nil {
			return err
		}
		if err := g.emitDeferThunks(); err != nil {
			return err
		}
	}
	if pending := len(g.pendingThunks) + len(g.pendingSpawns) +
		len(g.pendingGeneral) + len(g.pendingAsync) +
		len(g.pendingSelect) + len(g.pendingBlock) + len(g.pendingDrops) +
		len(g.pendingDefers); pending != 0 {
		return fmt.Errorf("pending lowering work did not drain")
	}
	return g.emitInit(g.cur)
}

// usesTaskRuntime reports whether any document names a task or channel node.
// usesSelect reports whether any document waits in a select statement.
func (g *generator) usesSelect() bool {
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind == "select" {
				return true
			}
		}
	}
	return false
}

func (g *generator) usesTaskRuntime() bool {
	for _, d := range g.docs {
		for _, r := range d.recs {
			switch r.Kind {
			case "spawn", "join", "recv", "tryrecv", "send", "trysend", "close", "channew":
				return true
			}
		}
	}
	return false
}

// emitWrapper writes the C entry the runtime calls: it starts the runtime,
// runs every package initializer in dependency order, calls the program entry,
// and shuts the runtime down.
func (g *generator) emitWrapper() error {
	if g.entryHasParam {
		return g.errf(g.entryID, "cannot translate node #%d: the entry takes no parameters", g.entryID)
	}
	if g.entryResult != "" && g.entryResult != "i32" && g.entryResult != "Result<(), Error>" &&
		g.entryResult != "Result<(),Error>" {
		return g.errf(g.entryID, "cannot translate node #%d: the entry result %s is not lowered yet", g.entryID, g.entryResult)
	}
	g.out.WriteString("declare void @willrt_init()\n")
	g.out.WriteString("declare void @willrt_shutdown()\n")
	g.out.WriteString("define i32 @main() {\nentry:\n  call void @willrt_init()\n")
	inits := g.inits
	if g.packageMode {
		inits = g.initOrder
	}
	for _, init := range inits {
		fmt.Fprintf(g.out, "  call void @%s()\n", llvmSymbol(init))
	}
	if g.entryResult == "" {
		fmt.Fprintf(g.out, "  call void @%s()\n", llvmSymbol(g.entrySymbol))
		g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 0\n}\n")
		return nil
	}
	if g.entryResult == "Result<(), Error>" || g.entryResult == "Result<(),Error>" {
		return g.emitResultWrapper()
	}
	fmt.Fprintf(g.out, "  %%r = call i32 @%s()\n", llvmSymbol(g.entrySymbol))
	g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 %r\n}\n")
	return nil
}

// emitResultWrapper lowers the only non-integer entry shape the language
// allows: Result<(), Error>. Ok exits 0; Err writes one stable line through the
// report target and exits 1.
func (g *generator) emitResultWrapper() error {
	info, ok := g.adtType("Result<(), Error>")
	if !ok {
		return g.errf(g.entryID, "cannot translate node #%d: Result<(), Error> is not lowered", g.entryID)
	}
	iface, ok := g.ifaceOfText("Error")
	if !ok {
		return g.errf(g.entryID, "cannot translate node #%d: the Error interface is not lowered", g.entryID)
	}
	messageIndex, ok := iface.byName["Message"]
	if !ok {
		return g.errf(g.entryID, "cannot translate node #%d: Error has no Message method", g.entryID)
	}
	if !g.errorEmitted {
		g.out.WriteString("%interface.Error = type { ptr, ptr }\n")
		g.errorEmitted = true
	}
	g.needReportDecl = true
	result := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s @%s()\n", result, info.llvm, llvmSymbol(g.entrySymbol))
	slot := g.allocSlot(g.adtSlots, "main.result", g.entryID, info.llvm)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", info.llvm, result, slot)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	tag := g.load("i32", tagPtr)
	okTag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", okTag, tag, info.tags["Ok"])
	g.labels++
	n := g.labels
	okLabel := fmt.Sprintf("main.ok.%d", n)
	errLabel := fmt.Sprintf("main.err.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", okTag, okLabel, errLabel)
	g.out.WriteString(okLabel + ":\n")
	g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 0\n")
	g.out.WriteString(errLabel + ":\n")
	data, table := g.ifaceSlotValue(info, slot)
	method := g.ifaceMethod(table, messageIndex)
	message := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call { ptr, i64 } %s(ptr %s)\n", message, method, data)
	text := g.newTmp()
	fmt.Fprintf(g.out, "  %s = extractvalue { ptr, i64 } %s, 0\n", text, message)
	size := g.newTmp()
	fmt.Fprintf(g.out, "  %s = extractvalue { ptr, i64 } %s, 1\n", size, message)
	prefix := g.globalString("error: ")
	newline := g.globalString("\n")
	fmt.Fprintf(g.out, "  call void @willrt_report_write_n(ptr %s, i64 7)\n", prefix)
	fmt.Fprintf(g.out, "  call void @willrt_report_write_n(ptr %s, i64 %s)\n", text, size)
	fmt.Fprintf(g.out, "  call void @willrt_report_write_n(ptr %s, i64 1)\n", newline)
	g.out.WriteString("  call void @willrt_shutdown()\n  ret i32 1\n}\n")
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
			sym := g.symbolName(name)
			g.cur.funcs[r.ID] = sym
			if g.lib && cExported(name) {
				if g.libFuncs == nil {
					g.libFuncs = map[string]string{}
				}
				g.libFuncs[qualify(g.pkg, name)] = sym
			}
		case "link":
			g.cur.links[r.ID] = fieldText(r, "text")
			if fieldText(r, "blocking") == "true" {
				g.linkBlock[fieldText(r, "text")] = true
			}
			if fieldText(r, "typed") == "true" {
				sig := cLinkSig{
					result:   fieldText(r, "type"),
					variadic: fieldText(r, "variadic") == "true",
					typed:    true,
				}
				for _, f := range r.Fields {
					if f.Name == "param" && f.Value.Kind == willc.TextValue {
						sig.params = append(sig.params, f.Value.Text)
					}
				}
				g.linkSigs[fieldText(r, "text")] = sig
			}
		case "struct":
			g.structDecl[fieldText(r, "text")] = r.ID
		case "enum":
			g.enumDecl[fieldText(r, "text")] = r.ID
		case "interface":
			g.ifaceDecl[fieldText(r, "text")] = r.ID
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
// functionPrefix gives a will function the narrowest linkage that still keeps
// the C surface working. The executable can hide every non-entry definition;
// a shared library keeps its capitalised C exports. Generic instances and
// private functions are internal, so LLVM may inline and remove them.
func (g *generator) functionPrefix(fn willc.Record, name string) string {
	sym := g.cur.funcs[fn.ID]
	if g.entryFound && g.entryPkg == g.pkg && g.entryName == name {
		return ""
	}
	if g.packageMode {
		if g.lib && cExported(name) && !strings.Contains(sym, "$") {
			return ""
		}
		return "hidden "
	}
	instance := strings.Contains(sym, "$")
	if g.lib && cExported(name) && !instance {
		return ""
	}
	if cExported(name) && !instance {
		return "hidden "
	}
	return "internal "
}

// inlineAttr forces a small leaf function into its callers. A loop, a call,
// or a defer keeps the ordinary inline hint, because forcing those would grow
// code and can break recursive functions.
func (g *generator) inlineAttr(fn willc.Record, name string) string {
	if g.entryFound && g.entryPkg == g.pkg && g.entryName == name {
		return ""
	}
	simple := true
	var visit func(int)
	visit = func(parent int) {
		for _, child := range g.children(parent) {
			switch child.Kind {
			case "for", "call", "mcall", "spawn", "join", "defer", "try", "select":
				simple = false
				return
			}
			visit(child.ID)
			if !simple {
				return
			}
		}
	}
	visit(fn.ID)
	if simple {
		return " alwaysinline"
	}
	return " inlinehint"
}

func (g *generator) symbolName(name string) string {
	if g.lib && cExported(name) {
		return name
	}
	if g.entryFound && g.entryPkg == g.pkg && g.entryName == name {
		return g.entrySymbol
	}
	return qualify(g.pkg, name)
}

// cExported reports whether a top-level name is in the C surface: a leading
// ASCII capital, as the language's export rule already requires.
func cExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// loopLabels names the two places a loop body can leave to: the step block
// (continue) and the block after the loop (break).
type loopLabels struct {
	step string
	end  string
	// deferDepth is the number of defer frames outside the loop body. A break
	// or continue runs every frame from the innermost down to this depth.
	deferDepth int
}

// qualify puts a package path in front of a name. A document without a package
// path keeps the plain name.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// stripPredeclaredPackage removes the package prefix that qualification adds
// to a predeclared generic. Array<F> stays Array<F> in every document, while a
// user type keeps its qualified name.
func stripPredeclaredPackage(text string) string {
	for _, name := range []string{"Array<", "Map<", "Set<", "Option<", "Result<",
		"rc<", "weak<", "chan<", "Task<"} {
		if i := strings.Index(text, name); i > 0 && text[i-1] == '.' {
			return text[i:]
		}
	}
	return text
}

// zeroValueText is the LLVM constant for an uninitialized local. A named tag
// and every aggregate use zeroinitializer; floats need a decimal point.
func zeroValueText(typ string) string {
	switch typ {
	case "double", "float":
		return "0.0"
	case "ptr":
		return "null"
	}
	if strings.HasPrefix(typ, "{") || strings.HasPrefix(typ, "[") ||
		strings.HasPrefix(typ, "%") || strings.HasPrefix(typ, "<") {
		return "zeroinitializer"
	}
	return "0"
}

// qualifyTypeText qualifies every named type inside one type text with the
// package path, including the arguments of a predeclared generic. A type from
// another document then carries one canonical name in signatures and layouts.
func (g *generator) qualifyTypeText(pkg, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	if strings.HasPrefix(text, "[]") {
		return "[]" + g.qualifyTypeText(pkg, text[2:])
	}
	generics := []string{"Array<", "Option<", "Result<", "Map<", "Set<", "chan<", "Task<", "rc<", "weak<"}
	for _, prefix := range generics {
		if strings.HasPrefix(text, prefix) && strings.HasSuffix(text, ">") {
			args := splitTypeList(text[len(prefix) : len(text)-1])
			for i := range args {
				args[i] = g.qualifyTypeText(pkg, args[i])
			}
			return prefix + strings.Join(args, ",") + ">"
		}
	}
	if name, args, ok := splitGenericType(text); ok {
		for i := range args {
			args[i] = g.qualifyTypeText(pkg, args[i])
		}
		if q := qualify(pkg, name); g.declKinds[q] != "" {
			return q + "<" + strings.Join(args, ",") + ">"
		}
	}
	if strings.HasPrefix(text, "func(") || text == "Error" || text == "()" || strings.Contains(text, ".") {
		return text
	}
	if _, _, ok := llvmScalar(text); ok {
		return text
	}
	switch text {
	case "str", "Duration", "Value", "Show":
		return text
	}
	if q := qualify(pkg, text); g.declKinds[q] != "" {
		return q
	}
	return text
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
	g.liveValues = map[int]string{}
	if g.generalMachine(fn.ID) {
		g.suspendable[g.cur.funcs[fn.ID]] = true
		g.enqueueGeneral(fn.ID)
		return nil
	}
	hasAsync := false
	if info, ok := g.asyncSequenceMachine(fn.ID); ok {
		g.pendingAsync = append(g.pendingAsync, info)
		hasAsync = true
	}
	hasSelect := false
	if info, ok := g.selectMachine(fn.ID); ok {
		g.pendingSelect = append(g.pendingSelect, info)
		hasSelect = true
	}
	if g.suspendable[g.cur.funcs[fn.ID]] && !hasAsync && !hasSelect {
		return g.errf(fn.ID, "cannot translate node #%d: suspendable function %s has no supported state-machine shape", fn.ID, fieldText(fn, "text"))
	}
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
	g.currentLambda = 0
	g.captureFields = map[int]string{}
	g.captureSpills = map[int]string{}
	if _, ok := g.lambdas[fn.ID]; ok {
		// Every lambda takes its environment first, even when it captures
		// nothing, so an indirect call has one calling convention.
		g.currentLambda = fn.ID
		sig = append(sig, "ptr %closure.env")
	}
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
		if fieldText(p, "variadic") == "true" {
			// The call packs the tail arguments into one view.
			g.scope[p.ID] = "%" + pname
			g.borrows[p.ID] = true
			sig = append(sig, "ptr %"+pname)
			continue
		}
		if isRcText(ptype) || isWeakText(ptype) {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, "i64 %"+pname)
			continue
		}
		if _, ok := taskElemText(ptype); ok {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, "ptr %"+pname)
			continue
		}
		if _, ok := chanElemText(ptype); ok {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, "ptr %"+pname)
			continue
		}
		if typ, _, ok := llvmScalar(ptype); ok {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, typ+" %"+pname)
			continue
		}
		if ptype == "Duration" {
			g.scope[p.ID] = "%" + pname
			sig = append(sig, "i64 %"+pname)
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
	} else if typ, ok := g.typeLLVMOf(result); ok {
		resLLVM = typ
	} else if id := g.typeRef(fn.ID); id != 0 && g.structIsGeneric(id) {
		var args []string
		for _, r := range g.recs {
			if r.Kind == "type" && refField(r, "parent") == fn.ID {
				args = g.typeRecordArgs(r)
				break
			}
		}
		info, ok := g.ensureStructInstance(id, args)
		if !ok {
			return g.errf(fn.ID, "cannot translate node #%d: result type %s is not lowered yet", fn.ID, result)
		}
		resLLVM = info.llvm
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
	} else if id := g.structDecl[result]; id != 0 {
		info, ok := g.ensureStruct(id)
		if !ok {
			return g.errf(fn.ID, "cannot translate node #%d: result type %s is not lowered yet", fn.ID, result)
		}
		resLLVM = info.llvm
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
	fmt.Fprintf(g.out, "define %s%s @%s(%s)%s {\n",
		g.functionPrefix(fn, name), resLLVM, llvmSymbol(g.cur.funcs[fn.ID]), strings.Join(sig, ", "),
		g.inlineAttr(fn, name))
	g.out.WriteString("entry:\n")
	g.lateAllocas = nil
	g.lateAllowed = true
	if err := g.setupCaptures(fn.ID); err != nil {
		return err
	}
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
	g.flushLateAllocas()
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
	g.captureSpills = map[int]string{}
	fmt.Fprintf(g.out, "define %svoid @%s(%s)%s {\n",
		g.functionPrefix(fn, name), llvmSymbol(g.cur.funcs[fn.ID]), strings.Join(sig, ", "),
		g.inlineAttr(fn, name))
	g.out.WriteString("entry:\n")
	g.lateAllocas = nil
	g.lateAllowed = true
	if err := g.setupCaptures(fn.ID); err != nil {
		return err
	}
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
	g.flushLateAllocas()
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
	var closures []willc.Record
	for _, r := range g.recs {
		if g.isClosureRecord(r, fn) {
			closures = append(closures, r)
		}
	}
	sortByID(closures)
	for _, r := range closures {
		name := "%v.closure." + strconv.Itoa(r.ID)
		g.closureSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca { ptr, ptr }\n", name)
	}
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
		info, ok := g.aggStructInfo(r)
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
		if isRcText(fieldText(r, "type")) {
			// An rc is one handle built by the table; it needs no slot here.
			continue
		}
		baseText := fieldText(r, "type")
		resultText := g.newResultText(r)
		typ, ok := g.typeLLVMOf(resultText)
		if !ok || !isAggregateType(typ) {
			return g.errf(r.ID, "cannot translate node #%d: new type %s is not lowered yet", r.ID, resultText)
		}
		name := "%v.new." + strconv.Itoa(r.ID)
		g.newSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
		if resultText != baseText {
			baseType, ok := g.typeLLVMOf(baseText)
			if !ok || !isAggregateType(baseType) {
				return g.errf(r.ID, "cannot translate node #%d: new type %s is not lowered yet", r.ID, baseText)
			}
			store := "%v.newstore." + strconv.Itoa(r.ID)
			g.newStoreSlots[r.ID] = store
			fmt.Fprintf(g.out, "  %s = alloca %s\n", store, baseType)
		}
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
	// A str method that returns a str, a view of str, or an Option keeps its
	// result, and a trim its two bounds, in the entry block.
	var stringCalls []willc.Record
	for _, r := range g.recs {
		if r.Kind == "mcall" && g.enclosingFunc(r.ID) == fn {
			stringCalls = append(stringCalls, r)
		}
	}
	sortByID(stringCalls)
	for _, r := range stringCalls {
		recvText, err := g.willTypeText(refField(r, "recv"))
		if err != nil || recvText != "str" {
			continue
		}
		llvm, text, known := g.strCallResult(r)
		if !known {
			continue
		}
		if text == "str" || text == "[]str" || text == "Option<usize>" {
			name := "%v.strm." + strconv.Itoa(r.ID)
			g.strMethodSlots[r.ID] = name
			fmt.Fprintf(g.out, "  %s = alloca %s\n", name, llvm)
		}
		switch fieldText(r, "text") {
		case "Trim", "TrimLeft", "TrimRight":
			start := "%v.trim.start." + strconv.Itoa(r.ID)
			g.trimStartSlots[r.ID] = start
			fmt.Fprintf(g.out, "  %s = alloca i64\n", start)
			length := "%v.trim.len." + strconv.Itoa(r.ID)
			g.trimLenSlots[r.ID] = length
			fmt.Fprintf(g.out, "  %s = alloca i64\n", length)
		}
	}
	// Range counters are needed by the wait scan below (a send value can name
	// the loop binding), so reserve them before that pass.
	for _, r := range g.recs {
		if r.Kind != "for" || g.enclosingFunc(r.ID) != fn || !g.isRangeLoop(r) {
			continue
		}
		names := bindNames(r)
		if len(names) != 1 || g.slots[r.ID] != "" {
			continue
		}
		name := g.newSlot(names[0])
		g.slots[r.ID] = name
		g.slotTypes[r.ID] = "i32"
		g.bindLoopType(r.ID, names[0], "i32")
		fmt.Fprintf(g.out, "  %s = alloca i32\n", name)
	}
	// A join, receive, and send keep their temporaries in the entry block, so
	// a loop that waits repeatedly does not grow the stack.
	var waits []willc.Record
	for _, r := range g.recs {
		if g.enclosingFunc(r.ID) == fn {
			switch r.Kind {
			case "join", "recv", "tryrecv", "send", "trysend", "select":
				waits = append(waits, r)
			}
		}
	}
	sortByID(waits)
	for _, r := range waits {
		switch r.Kind {
		case "join":
			taskText, err := g.willTypeText(refField(r, "task"))
			if err != nil {
				return err
			}
			if elem, ok := taskElemText(taskText); ok && elem != "" && elem != "()" {
				if elemLLVM, ok := g.typeLLVMOf(elem); ok && elemLLVM != "" {
					name := "%v.join.out." + strconv.Itoa(r.ID)
					g.joinOutSlots[r.ID] = name
					fmt.Fprintf(g.out, "  %s = alloca %s\n", name, elemLLVM)
				}
			}
			name := "%v.join.err." + strconv.Itoa(r.ID)
			g.joinErrSlots[r.ID] = name
			fmt.Fprintf(g.out, "  %s = alloca %%willrt_error\n", name)
		case "recv", "tryrecv":
			chanText, err := g.willTypeText(refField(r, "chan"))
			if err != nil {
				return err
			}
			elem, ok := chanElemText(chanText)
			if !ok {
				continue
			}
			elemLLVM, ok := g.typeLLVMOf(elem)
			if !ok || elemLLVM == "" {
				continue
			}
			out := "%v.recv.out." + strconv.Itoa(r.ID)
			g.recvOutSlots[r.ID] = out
			fmt.Fprintf(g.out, "  %s = alloca %s\n", out, elemLLVM)
			if info, ok := g.adtType("Option<" + elem + ">"); ok {
				name := "%v.recv." + strconv.Itoa(r.ID)
				g.recvOptSlots[r.ID] = name
				fmt.Fprintf(g.out, "  %s = alloca %s\n", name, info.llvm)
			}
		case "send", "trysend":
			typ, err := g.typeOf(refField(r, "value"))
			if err != nil {
				// A pattern binding gets its type while the match arm is
				// lowered; the lazy slot in lowerSend fills it then.
				continue
			}
			if typ != "" && !isAggregateType(typ) {
				name := "%v.send." + strconv.Itoa(r.ID)
				g.sendValueSlots[r.ID] = name
				fmt.Fprintf(g.out, "  %s = alloca %s\n", name, typ)
			}
		case "select":
			if err := g.prepareSelect(r); err != nil {
				return err
			}
		}
	}
	// A cast to str and an Upgrade keep their result storage in the entry
	// block as well.
	var aux []willc.Record
	for _, r := range g.recs {
		if g.enclosingFunc(r.ID) != fn {
			continue
		}
		if r.Kind == "cast" || r.Kind == "mcall" {
			aux = append(aux, r)
		}
	}
	sortByID(aux)
	for _, r := range aux {
		switch r.Kind {
		case "cast":
			if to, ok := g.recs[refField(r, "to")]; ok {
				switch fieldText(to, "text") {
				case "str", "[]u8":
					name := "%v.cast." + strconv.Itoa(r.ID)
					g.castSlots[r.ID] = name
					fmt.Fprintf(g.out, "  %s = alloca { ptr, i64 }\n", name)
				}
			}
		case "mcall":
			if recvText, err := g.willTypeText(refField(r, "recv")); err == nil &&
				isWeakText(recvText) && fieldText(r, "text") == "Upgrade" {
				name := "%v.weakout." + strconv.Itoa(r.ID)
				g.weakOutSlots[r.ID] = name
				fmt.Fprintf(g.out, "  %s = alloca i64\n", name)
			}
		}
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
	// A str concatenation builds a two-word value, which also needs a slot.
	var cats []willc.Record
	for _, r := range g.recs {
		if r.Kind == "bin" && g.enclosingFunc(r.ID) == fn && g.isStrConcat(r) {
			cats = append(cats, r)
		}
	}
	sortByID(cats)
	for _, r := range cats {
		name := "%v.cat." + strconv.Itoa(r.ID)
		g.strCatSlots[r.ID] = name
		fmt.Fprintf(g.out, "  %s = alloca { ptr, i64 }\n", name)
	}
	var interps []willc.Record
	for _, r := range g.recs {
		if r.Kind == "interp" && g.enclosingFunc(r.ID) == fn {
			interps = append(interps, r)
		}
	}
	sortByID(interps)
	for _, r := range interps {
		name := "%v.interp." + strconv.Itoa(r.ID)
		g.interpSlots[r.ID] = name
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
			if g.slots[r.ID] != "" {
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
		if g.prepareContainerLoop(r, iterText) {
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
		if _, ok := g.typeLLVMOf(elem); !ok {
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
	g.out.WriteString(entryAllocaMarker + "\n")
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
	targetText, textErr := g.willTypeText(target)
	if textErr == nil && isRcText(targetText) {
		value, err := g.value(valueID)
		if err != nil {
			return err
		}
		g.needRcDecl = true
		if g.isCopySource(valueID) {
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		old := g.load("i64", ptr)
		fmt.Fprintf(g.out, "  call void @willrt_rc_release(i64 %s)\n", old)
		fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", value, ptr)
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
		if g.needsReleaseText(targetText) {
			if err := g.releaseSlot(ptr, targetText); err != nil {
				return err
			}
			if g.isCopySource(valueID) {
				if err := g.retainValue(src, targetText); err != nil {
					return err
				}
			}
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
		if c.Kind != "type" {
			valueID = c.ID
		}
	}
	typ := g.slotTypes[r.ID]
	if g.needsReleaseText(fieldText(r, "type")) {
		g.liveValues[r.ID] = fieldText(r, "type")
	}
	if valueID == 0 {
		// A var without an initializer keeps the zero value of its type.
		if isRcText(fieldText(r, "type")) || typ == "i64" {
			fmt.Fprintf(g.out, "  store %s 0, ptr %s\n", typ, slot)
		} else {
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, zeroValueText(typ), slot)
		}
		return false, nil
	}
	if isRcText(fieldText(r, "type")) {
		value, err := g.value(valueID)
		if err != nil {
			return false, err
		}
		if g.isCopySource(valueID) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", value, slot)
		return false, nil
	}
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
		text := fieldText(r, "type")
		if g.needsReleaseText(text) && g.isCopySource(valueID) {
			if err := g.retainValue(src, text); err != nil {
				return false, err
			}
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
	return g.bodyStmts(stmts, void)
}

// bodyStmts lowers one ordered statement list. A state-machine step uses it
// for the statements before and after one suspension point, so the ordinary
// body lowering and the split path share release and defer handling.
func (g *generator) bodyStmts(stmts []willc.Record, void bool) (bool, error) {
	entry := len(g.defers)
	savedLive := make(map[int]string, len(g.liveValues))
	for id, text := range g.liveValues {
		savedLive[id] = text
	}
	g.defers = append(g.defers, nil)
	terminated := false
	var pending []willc.Record
	for _, st := range stmts {
		if terminated {
			break
		}
		if st.Kind == "release" {
			pending = append(pending, st)
			continue
		}
		if st.Kind == "return" || st.Kind == "break" || st.Kind == "continue" {
			g.pendingReleases = append(g.pendingReleases, pending...)
			pending = nil
			done, err := g.stmt(st, void)
			if err != nil {
				return false, err
			}
			terminated = done
			continue
		}
		if len(pending) != 0 && g.containsTry(st.ID) {
			g.pendingTryReleases = append(g.pendingTryReleases, pending...)
			pending = nil
		} else {
			for _, rel := range pending {
				if err := g.releaseRecord(rel); err != nil {
					return false, err
				}
			}
			pending = nil
		}
		done, err := g.stmt(st, void)
		if err != nil {
			return false, err
		}
		g.pendingTryReleases = nil
		terminated = done
	}
	if !terminated {
		if err := g.runDefers(entry); err != nil {
			return false, err
		}
		for _, rel := range pending {
			if err := g.releaseRecord(rel); err != nil {
				return false, err
			}
		}
	}
	if len(g.defers) > entry {
		g.defers = g.defers[:entry]
	}
	g.liveValues = savedLive
	return terminated, nil
}

// releaseRecord lowers one release record for the owner still in its slot.
func (g *generator) releaseRecord(rel willc.Record) error {
	owner := refField(rel, "owner")
	slot := g.slots[owner]
	if slot == "" {
		return nil
	}
	rec, ok := g.recs[owner]
	if !ok {
		return nil
	}
	text := fieldText(rec, "type")
	if !needsReleaseText(g, text) {
		return nil
	}
	delete(g.liveValues, owner)
	return g.releaseSlot(slot, text)
}

// flushReleases runs the releases of a return after its value is ready.
func (g *generator) flushReleases() error {
	pending := g.pendingReleases
	g.pendingReleases = nil
	for _, rel := range pending {
		if err := g.releaseRecord(rel); err != nil {
			return err
		}
	}
	return nil
}

// stmt lowers one statement and reports whether it ends the current block.
func (g *generator) stmt(st willc.Record, void bool) (bool, error) {
	switch st.Kind {
	case "defer":
		return false, g.deferStmt(st)
	case "return":
		return g.returnStmt(st, void)
	case "deadline":
		duration, err := g.value(refField(st, "d"))
		if err != nil {
			return false, err
		}
		g.needTaskDecl = true
		g.needDeadlineDecl = true
		previous := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i64 @willrt_task_enter_deadline(i64 %s)\n", previous, duration)
		previousSlot := ""
		if g.smGeneral {
			previousSlot = g.smAlloca("i64", "deadline.previous")
			fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", previous, previousSlot)
		}
		body, ok := g.recs[refField(st, "body")]
		if !ok {
			return false, g.errf(st.ID, "cannot translate node #%d: the deadline block is missing", st.ID)
		}
		if _, err := g.body(body, void); err != nil {
			return false, err
		}
		if previousSlot != "" {
			previous = g.load("i64", previousSlot)
		}
		fmt.Fprintf(g.out, "  call void @willrt_task_exit_deadline(i64 %s)\n", previous)
		return false, nil
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
		if id := refField(st, "expr"); id != 0 {
			if r, ok := g.recs[id]; ok && r.Kind == "panic" {
				return g.panicStmt(r)
			}
		}
		return false, g.exprStmt(st)
	case "block":
		return g.body(st, void)
	case "release":
		owner := refField(st, "owner")
		if slot := g.slots[owner]; slot != "" {
			if err := g.releaseSlot(slot, fieldText(g.recs[owner], "type")); err != nil {
				return false, err
			}
		}
		return false, nil
	case "panic":
		return g.panicStmt(st)
	case "assert":
		return g.assertStmt(st)
	case "select":
		if g.smGeneral {
			return g.generalSelectStmt(st, void)
		}
		return g.selectStmt(st, void)
	case "region", "parallel":
		g.needTaskDecl = true
		g.out.WriteString("  call void @willrt_region_enter()\n")
		body, ok := g.recs[refField(st, "body")]
		if !ok {
			return false, g.errf(st.ID, "cannot translate node #%d: the block is missing", st.ID)
		}
		if st.Kind == "parallel" {
			g.parallelDepth++
		}
		_, err := g.body(body, void)
		if st.Kind == "parallel" {
			g.parallelDepth--
		}
		if err != nil {
			return false, err
		}
		g.out.WriteString("  call i32 @willrt_region_exit()\n")
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
		if err := g.runDefers(0); err != nil {
			return false, err
		}
		if err := g.flushReleases(); err != nil {
			return false, err
		}
		if g.smMode {
			g.out.WriteString("  ret i32 1\n")
		} else {
			g.out.WriteString("  ret void\n")
		}
		return true, nil
	}
	valueID := refField(st, "value")
	if g.resultType == "" {
		return false, g.errf(st.ID, "cannot translate node #%d: the function returns no value", st.ID)
	}
	if g.needsReleaseText(g.fnResultText) && g.isCopySource(valueID) {
		movedLocal := false
		if name, ok := g.recs[valueID]; ok && name.Kind == "name" {
			if _, ok := g.slots[refField(name, "to")]; ok {
				movedLocal = true
			}
		}
		if !movedLocal {
			if isRcText(g.fnResultText) {
				handle, err := g.value(valueID)
				if err != nil {
					return false, err
				}
				g.needRcDecl = true
				fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", handle)
			} else {
				src, _, err := g.place(valueID)
				if err != nil {
					return false, err
				}
				if err := g.retainValue(src, g.fnResultText); err != nil {
					return false, err
				}
			}
		}
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
			if err := g.runDefers(0); err != nil {
				return false, err
			}
			if err := g.flushReleases(); err != nil {
				return false, err
			}
			if g.smMode {
				g.storeTaskResult(iface.llvm, v)
			} else {
				fmt.Fprintf(g.out, "  ret %s %s\n", iface.llvm, v)
			}
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
		if err := g.runDefers(0); err != nil {
			return false, err
		}
		if err := g.flushReleases(); err != nil {
			return false, err
		}
		if g.smMode {
			g.storeTaskResult(typ, tmp)
		} else {
			fmt.Fprintf(g.out, "  ret %s %s\n", typ, tmp)
		}
		return true, nil
	}
	value, err := g.value(valueID)
	if err != nil {
		return false, err
	}
	if err := g.runDefers(0); err != nil {
		return false, err
	}
	if err := g.flushReleases(); err != nil {
		return false, err
	}
	if g.smMode {
		g.storeTaskResult(g.resultType, value)
	} else {
		fmt.Fprintf(g.out, "  ret %s %s\n", g.resultType, value)
	}
	return true, nil
}

// storeTaskResult copies one completed return value into the task result and
// ends the step. The temporary is safe: a step returns exactly once.
func (g *generator) storeTaskResult(typ, value string) {
	g.needTaskDecl = true
	slot := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, typ)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, slot)
	fmt.Fprintf(g.out, "  call i32 @willrt_task_store_result(ptr %s, i64 %s)\n",
		slot, g.sizeOf(typ))
	g.out.WriteString("  ret i32 1\n")
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
	if err := g.runDefers(top.deferDepth); err != nil {
		return false, err
	}
	if err := g.flushReleases(); err != nil {
		return false, err
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
		g.loops = append(g.loops, loopLabels{step: bodyLabel, end: endLabel, deferDepth: len(g.defers)})
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
		if k, v, ok := mapTypeText(iterText); ok {
			return g.forMap(r, k, v, void, n)
		}
		if elem, ok := setTypeText(iterText); ok {
			return g.forSet(r, elem, void, n)
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
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel, deferDepth: len(g.defers)})
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
	if snap, ok := g.deferSnapshots[id]; ok {
		if isAggregateType(snap.llvm) {
			return "", g.errf(id, "cannot translate node #%d: a deferred aggregate is a place", id)
		}
		return g.load(snap.llvm, g.deferFieldPtr(snap)), nil
	}
	if p, ok := g.smPlaces[id]; ok {
		if isAggregateType(p.typ) {
			return "", g.errf(id, "cannot translate node #%d: a state-machine result is a place", id)
		}
		return g.load(p.typ, p.ptr), nil
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
		case "char":
			return literalIntText(fieldText(r, "text")), nil
		case "duration":
			value, ok := durationNanos(fieldText(r, "text"))
			if !ok {
				return "", g.errf(id, "cannot translate node #%d: duration %s is not a number and a unit", id, fieldText(r, "text"))
			}
			return value, nil
		case "str", "bytes":
			return "", g.errf(id, "cannot translate node #%d: a %s literal is a place, not a scalar", id, kind)
		}
		return "", g.errf(id, "cannot translate node #%d: literal kind %s is not lowered yet", id, fieldText(r, "kind"))
	case "spawn":
		return g.lowerSpawn(r)
	case "channew":
		return g.lowerChanNew(r)
	case "lanehere":
		g.needTaskDecl = true
		name := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call i32 @willrt_lane_here()\n", name)
		wide := g.newTmp()
		fmt.Fprintf(g.out, "  %s = sext i32 %s to i64\n", wide, name)
		return wide, nil
	case "trysend":
		return g.lowerTrySend(r)
	case "send":
		return g.lowerSendResult(r)
	case "close":
		return "", g.errf(id, "cannot translate node #%d: Close is a statement", id)
	case "recv":
		return "", g.errf(id, "cannot translate node #%d: Recv is a place", id)
	case "join":
		return g.lowerJoinResult(r)
	case "func":
		if _, ok := g.lambdaNames[r.ID]; ok {
			return "", g.errf(id, "cannot translate node #%d: a closure is a place, not a scalar", id)
		}
		return "", g.errf(id, "cannot translate node #%d: a function declaration is not a value", id)
	case "name":
		decl := refField(r, "to")
		name := fieldText(r, "text")
		if c, ok := g.recs[decl]; ok && c.Kind == "func" {
			return "", g.errf(id, "cannot translate node #%d: a function value is a place, not a scalar", id)
		}
		if c, ok := g.recs[decl]; ok && c.Kind == "const" {
			if v, ok := g.constValueOf(decl); ok {
				return v, nil
			}
			return "", g.errf(id, "cannot translate node #%d: the constant %s is not a literal yet", id, name)
		}
		if info := g.globalRef(r); info != nil {
			if info.isConst {
				if info.value == "" {
					return "", g.errf(id, "cannot translate node #%d: the constant %s is not a literal yet", id, name)
				}
				return info.value, nil
			}
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
	case "unwrap":
		return g.unwrapValue(r)
	case "new":
		if text := fieldText(r, "type"); isRcText(text) {
			elem, _ := rcElemText(text)
			handle, _, err := g.constructRc(r, elem)
			return handle, err
		}
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

const entryAllocaMarker = ";__will_entry_allocas__"

// allocSlot returns the slot of one record, allocating it in the current block
// when the function prepass did not run for it. The prepass covers the records
// inside a function; a top-level initializer is lowered outside one, so its
// slots are made at the point of use. Inside a function the late line is held
// for the entry marker, so a loop never executes an alloca.
func (g *generator) allocSlot(slots map[int]string, prefix string, id int, llvm string) string {
	if slot := slots[id]; slot != "" {
		return slot
	}
	slot := "%v." + prefix + "." + strconv.Itoa(id)
	slots[id] = slot
	line := fmt.Sprintf("  %s = alloca %s", slot, llvm)
	if g.lateAllowed && prefix == "ret" {
		g.lateAllocas = append(g.lateAllocas, line)
	} else {
		g.out.WriteString(line + "\n")
	}
	return slot
}

// flushLateAllocas replaces the entry marker with the slots requested while
// lowering the body. The marker is always present in a function.
func (g *generator) flushLateAllocas() {
	if !g.lateAllowed {
		return
	}
	text := g.out.String()
	index := strings.Index(text, entryAllocaMarker)
	if index < 0 {
		g.lateAllowed = false
		return
	}
	end := index + len(entryAllocaMarker)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	replacement := strings.Join(g.lateAllocas, "\n")
	if replacement != "" {
		replacement += "\n"
	}
	text = text[:index] + replacement + text[end:]
	g.out.Reset()
	g.out.WriteString(text)
	g.lateAllowed = false
	g.lateAllocas = nil
}

// exprStmt lowers an expression used as a statement. A call is allowed to
// return nothing here, because its result is not needed.
func (g *generator) exprStmt(st willc.Record) error {
	id := refField(st, "expr")
	if r, ok := g.recs[id]; ok && r.Kind == "discard" {
		inner := refField(r, "expr")
		if send, ok := g.recs[inner]; ok && send.Kind == "send" {
			return g.lowerSend(send)
		}
		if u, ok := g.recs[inner]; ok && u.Kind == "unwrap" {
			if join, ok := g.recs[refField(u, "expr")]; ok && join.Kind == "join" {
				taskText, typeErr := g.willTypeText(refField(join, "task"))
				if typeErr == nil {
					if elem, ok := taskElemText(taskText); ok && (elem == "" || elem == "()") {
						_, _, _, err := g.unwrapAddr(u)
						return err
					}
				}
			}
		}
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
	if r, ok := g.recs[id]; ok && (r.Kind == "panic" || r.Kind == "assert") {
		_, err := g.stmt(r, false)
		return err
	}
	if r, ok := g.recs[id]; ok {
		switch r.Kind {
		case "send":
			return g.lowerSend(r)
		case "close":
			return g.lowerClose(r)
		case "recv":
			chanPtr, err := g.value(refField(r, "chan"))
			if err != nil {
				return err
			}
			g.needTaskDecl = true
			fmt.Fprintf(g.out, "  call i32 @willrt_chan_recv(ptr %s, ptr null)\n", chanPtr)
			return nil
		}
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
	if op == "+" {
		if lt, err := g.willTypeText(lID); err == nil && lt == "raw" {
			base, err := g.value(lID)
			if err != nil {
				return "", err
			}
			offset, err := g.value(rID)
			if err != nil {
				return "", err
			}
			out := g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", out, base, offset)
			return out, nil
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
	switch op {
	case "+%", "-%", "*%", "<<%", ">>%":
		// A wrapping operator is the machine operation: it never traps, so it
		// needs no overflow check.
		instr := arithInstr(op, typ, signed)
		if instr == "" {
			return "", g.errf(r.ID, "cannot translate node #%d: operator %s is not lowered yet", r.ID, op)
		}
		name := g.newTmp()
		fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", name, instr, typ, l, right)
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
		name, _ := g.predeclaredCallName(r)
		return "", g.errf(r.ID, "cannot translate node #%d: the call returns no value (callee=%s)", r.ID, name)
	}
	_, taskScalar := taskElemText(text)
	_, chanScalar := chanElemText(text)
	if _, _, ok := llvmScalar(text); !ok && text != "Duration" && !isRcText(text) && !isWeakText(text) && !taskScalar && !chanScalar {
		return "", g.errf(r.ID, "cannot translate node #%d: an aggregate result is a place, not a scalar value", r.ID)
	}
	return name, nil
}

// callExpr lowers one call or method call and returns the value and its result
// type text. A void call returns an empty name and an empty text.
func (g *generator) callExpr(r willc.Record) (string, string, error) {
	if g.smGeneral && !isAsyncCRecord(r) {
		if sym := g.callTargetSymbol(r); sym != "" && g.suspendable[sym] {
			value, err := g.generalCallWait(r)
			if err != nil {
				return "", "", err
			}
			text, err := g.callResultText(r)
			if err != nil {
				return "", "", err
			}
			return value, text, nil
		}
	}
	if g.smGeneral && isAsyncCRecord(r) {
		value, err := g.generalAsyncC(r)
		if err != nil {
			return "", "", err
		}
		return value, "i32", nil
	}
	if info, _ := g.enumVariantCall(r); info != nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: an enum value is a place, not a scalar", r.ID)
	}
	if g.isPrintCall(r) {
		return g.printCall(r)
	}
	if name, ok := g.predeclaredCallName(r); ok &&
		(name == "SizeOf" || name == "AlignOf" || name == "OffsetOf") {
		value, err := g.lowerLayout(r, name)
		if err != nil {
			return "", "", err
		}
		return value, "usize", nil
	}
	if name, ok := g.predeclaredCallName(r); ok && (name == "take_c" || name == "take_c_rc") {
		value, err := g.lowerTakeC(r, name)
		if err != nil {
			return "", "", err
		}
		result := ""
		if args := g.typeArgTexts(r); len(args) == 1 {
			result = "rc<" + args[0] + ">"
		}
		if result == "" {
			result, err = g.willTypeText(r.ID)
			if err != nil {
				return "", "", err
			}
		}
		return value, result, nil
	}
	if r.Kind == "mcall" && g.isCLink(r) {
		name := fieldText(r, "text")
		if g.linkBlock[name] {
			return g.lowerBlockingCLink(r, name)
		}
	}
	if r.Kind == "mcall" {
		if recvText, err := g.willTypeText(refField(r, "recv")); err == nil {
			recvText = stripPredeclaredPackage(recvText)
			if isWeakText(recvText) && fieldText(r, "text") == "Upgrade" {
				return g.upgradeCall(r, recvText)
			}
			if isRcText(recvText) {
				return g.rcMethodCall(r, recvText)
			}
			if kind, args, ok := parseADT(recvText); ok {
				switch fieldText(r, "text") {
				case "IsSome", "IsNone", "IsOk", "IsErr", "Or":
					return g.adtMethod(r, kind, args)
				}
			}
		}
	}
	if target := refField(r, "to"); target != 0 {
		if decl, ok := g.recs[target]; ok && g.isFuncValue(decl) {
			return g.indirectCall(r, decl)
		}
	}
	if name := g.genericCallName(r); name != "" {
		return g.emitCall(r, name, false)
	}
	if qualified := textField(r, "to"); qualified != "" {
		return g.emitCall(r, qualified, false)
	}
	if r.Kind == "mcall" {
		if name := g.genericMethodName(r); name != "" {
			return g.emitCall(r, name, false)
		}
		if name := g.genericCallName(r); name != "" {
			return g.emitCall(r, name, false)
		}
		if _, _, known := g.strCallResult(r); known {
			return g.strMethod(r)
		}
		if _, _, known := g.containerCallResult(r); known {
			recvText, err := g.willTypeText(refField(r, "recv"))
			if err != nil {
				return "", "", err
			}
			return g.containerMethod(r, stripPredeclaredPackage(recvText))
		}
		recvText, err := g.willTypeText(refField(r, "recv"))
		if err != nil {
			return "", "", err
		}
		recvText = stripPredeclaredPackage(recvText)
		if recvText == "raw" {
			return g.rawMethod(r)
		}
		if elem, ok := viewElemText(recvText); ok {
			return g.viewMethod(r, elem)
		}
		if elem, ok := arrayElemText(recvText); ok {
			return g.arrayMethod(r, elem)
		}
		if _, ok := chanElemText(recvText); ok && fieldText(r, "text") == "Close" {
			return "", "", g.lowerClose(r)
		}
		if iface, ok := g.ifaceOfText(recvText); ok {
			return g.interfaceCall(r, iface)
		}
		if typ, err := g.typeOf(refField(r, "recv")); err == nil {
			if iface, ok := g.ifaceOfType(typ); ok {
				return g.interfaceCall(r, iface)
			}
		}
		if info, index, fnText, ok := g.functionField(r); ok {
			return g.fieldCall(r, info, index, fnText)
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
	if name := g.genericCallName(r); name != "" {
		return g.emitCall(r, name, false)
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
	return g.emitCallFrom(r, name, link, "")
}

// emitCallFrom writes a call whose receiver pointer may already be known, as
// when an rc method call has dereferenced its handle.
func (g *generator) emitCallFrom(r willc.Record, name string, link bool, recv string) (string, string, error) {
	if link {
		if g.linkBlock[name] {
			return "", "", g.errf(r.ID, "cannot translate node #%d: a blocking C call is not lowered yet", r.ID)
		}
		if sig, ok := g.linkSigs[name]; ok && sig.typed {
			return g.emitTypedCLink(r, name, sig)
		}
	}
	if !link {
		if alias := g.libFuncs[name]; alias != "" {
			name = alias
		}
		g.noteExternalFunc(name)
	}
	args := g.callArgs(r)
	parts := make([]string, 0, len(args))
	// A method call passes its receiver first, as the borrow pointer the
	// method's hidden first parameter takes.
	if r.Kind == "mcall" && !link && textField(r, "to") == "" {
		if recv != "" {
			parts = append(parts, "ptr "+recv)
		} else {
			ptr, _, err := g.place(refField(r, "recv"))
			if err != nil {
				return "", "", err
			}
			parts = append(parts, "ptr "+ptr)
		}
	}
	fixed := len(args)
	if !link && g.varargs[name] {
		fixed = len(g.paramTypes[name]) - 1
		if fixed < 0 {
			fixed = 0
		}
		if len(args) < fixed {
			return "", "", g.errf(r.ID, "cannot translate node #%d: %s needs %d arguments", r.ID, name, fixed)
		}
	}
	for i := 0; i < fixed; i++ {
		a := args[i]
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
	if !link && g.varargs[name] {
		slot, err := g.packVariadic(r, name, args[fixed:])
		if err != nil {
			return "", "", err
		}
		parts = append(parts, "ptr "+slot)
	}
	resultText, err := g.callResultText(r)
	if err != nil {
		return "", "", err
	}
	if !link && g.packageMode && name != "" && !strings.HasPrefix(name, g.pkg+".") {
		params := g.paramTypes[name]
		if params == nil {
			params = g.callParamTypes(r)
		}
		argTypes := make([]string, 0, len(parts))
		for _, part := range parts {
			if i := strings.LastIndexByte(part, ' '); i > 0 {
				argTypes = append(argTypes, part[:i])
			}
		}
		g.externalFuncs[name] = true
		g.externalSigs[name] = externalSignature{
			result:   resultText,
			params:   append([]string(nil), params...),
			argTypes: argTypes,
			recv:     r.Kind == "mcall" && textField(r, "to") == "",
		}
	}
	if link {
		if resultText == "" {
			// An explicit link without a header keeps the historical i32
			// fallback; a typed header always takes the branch above.
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
		} else if typ, ok := g.typeLLVMOf(resultText); ok && typ != "" {
			result = typ
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
	decl := "declare i32 @" + llvmSymbol(name) + "(" + i32Types(args) + ")"
	if g.declSet[decl] {
		return
	}
	g.declSet[decl] = true
	g.decls = append(g.decls, decl)
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
		case willc.BoolValue:
			if f.Value.Bool {
				return "true"
			}
			return "false"
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

// sortedRecords returns one document's records in source order. Lowering that
// creates new documents must not use map order, or the symbol order would
// change between identical builds.
func sortedRecords(recs map[int]willc.Record) []willc.Record {
	out := make([]willc.Record, 0, len(recs))
	for _, r := range recs {
		out = append(out, r)
	}
	sortByID(out)
	return out
}
