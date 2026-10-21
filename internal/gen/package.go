package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// externalSignature is the exact call signature observed at a cross-package
// call site. It is more precise than the declaration's generic signature.
type externalSignature struct {
	result   string
	params   []string
	argTypes []string
	recv     bool
}

// EmitPackageModule translates one package of a module into its own LLVM
// module. The documents of the other packages are still loaded so signatures,
// type layouts, globals, and generic instances can be resolved; only the
// target package's functions and globals are defined here.
func EmitPackageModule(docs []*willc.Doc, pkg, entry, entryPkg string, aliases map[string]string, lib bool) ([]byte, error) {
	g := newGenerator(lib, aliases)
	g.packageMode = true
	g.externalFuncs = map[string]bool{}
	g.externalSigs = map[string]externalSignature{}
	g.externalGlobals = map[string]bool{}
	g.emittedFuncs = map[string]bool{}
	for _, doc := range docs {
		g.docs = append(g.docs, newDocState(doc))
	}
	if !lib {
		g.findEntry(entry, entryPkg)
	}
	instanceOwner := entryPkg
	if instanceOwner == "" {
		instanceOwner = pkg
	}
	ownsInstances := pkg == instanceOwner
	g.collectSignatures()
	g.collectExternalEnums()
	g.collectGlobals()
	if ownsInstances {
		g.planInstances()
		// Cloned generic instances were added after the first signature pass.
		g.collectSignatures()
		g.collectExternalEnums()
		g.collectGlobals()
	}
	g.initOrder = g.packageInits()
	for _, name := range g.initOrder {
		if !strings.HasPrefix(name, pkg+".") {
			g.externalFuncs[name] = true
		}
	}
	if g.usesTaskRuntime() {
		g.out.WriteString("%willrt_error = type { ptr, i64, ptr, ptr }\n")
	}
	if g.usesSelect() {
		g.out.WriteString("%willrt_arm = type { ptr, ptr, ptr, i32, i64 }\n")
	}
	// A package object may mention the predeclared Error interface only through
	// an imported signature or an ADT payload, so define it unconditionally.
	g.out.WriteString("%interface.Error = type { ptr, ptr }\n")
	g.errorEmitted = true
	emits := func(d *docState) bool {
		if d.clone && pkg != instanceOwner {
			return false
		}
		return d.path == pkg || (d.clone && pkg == instanceOwner)
	}
	// Collect declarations of every document so imported names resolve, but
	// emit concrete type layouts only for documents this object lowers. A
	// package that does not own a generic instance must not pay for every
	// other package's types during an incremental build.
	for _, d := range g.docs {
		g.use(d)
		g.collect()
	}
	for _, d := range g.docs {
		g.use(d)
		if err := g.planLambdas(); err != nil {
			return nil, err
		}
		g.collect()
		g.buildStructs()
		g.buildEnums()
		g.buildInterfaces()
	}
	for _, d := range g.docs {
		if !emits(d) {
			continue
		}
		g.use(d)
		g.collect()
		for _, sym := range d.funcs {
			g.emittedFuncs[sym] = true
		}
	}
	for _, d := range g.docs {
		if !emits(d) {
			continue
		}
		g.use(d)
		if err := g.emitDoc(); err != nil {
			return nil, err
		}
	}
	if !lib && entryPkg == pkg && g.entryFound {
		if err := g.emitWrapper(); err != nil {
			return nil, err
		}
	}
	g.emitAux()
	return []byte(g.out.String()), nil
}

// newGenerator returns the module-wide lowering state. Both the monolithic and
// per-package entries start from the same tables.
func newGenerator(lib bool, aliases map[string]string) *generator {
	return &generator{
		lib:            lib,
		scope:          map[int]string{},
		slots:          map[int]string{},
		lambdaNames:    map[int]string{},
		lambdas:        map[int]*lambdaInfo{},
		libFuncs:       map[string]string{},
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
	}
}

// packageInits lists every package init symbol in document order, including
// packages whose init is defined in another object.
func (g *generator) packageInits() []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range g.docs {
		for _, r := range g.topLevelVars(d) {
			if len(g.children(r.ID)) == 0 {
				continue
			}
			sym := qualify(d.path, "will$init")
			if !seen[sym] {
				seen[sym] = true
				out = append(out, sym)
			}
		}
	}
	return out
}

// noteExternalFunc records a function symbol defined outside this package.
func (g *generator) noteExternalFunc(name string) {
	if !g.packageMode || name == "" || strings.HasPrefix(name, g.pkg+".") || g.emittedFuncs[name] {
		return
	}
	g.externalFuncs[name] = true
}

// noteExternalGlobal records a top-level variable defined outside this package.
func (g *generator) noteExternalGlobal(info *globalInfo) {
	if !g.packageMode || info == nil || info.isConst {
		return
	}
	if strings.HasPrefix(info.sym, g.pkg+".") {
		return
	}
	g.externalGlobals[info.sym] = true
}

// emitExternalDecls writes declarations for symbols other package objects
// define. Types are module-local and need no declaration.
func (g *generator) emitExternalDecls() {
	names := make([]string, 0, len(g.externalFuncs))
	for name := range g.externalFuncs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if g.emittedFuncs[name] {
			continue
		}
		sig, known := g.externalSigs[name]
		if !known {
			fmt.Fprintf(g.out, "declare void @%s()\n", llvmSymbol(name))
			continue
		}
		ret := g.resultLLVM(sig.result)
		if ret == "" {
			ret = "void"
		}
		var parts []string
		if sig.recv && len(sig.argTypes) == 0 {
			parts = append(parts, "ptr")
		}
		ok := true
		if len(sig.argTypes) > 0 {
			parts = append(parts, sig.argTypes...)
		} else {
			for _, text := range sig.params {
				typ, lowered := g.typeLLVMOf(text)
				if !lowered || typ == "" {
					ok = false
					break
				}
				parts = append(parts, typ)
			}
		}
		if !ok {
			// A signature that is not fully lowered is reported at its call.
			continue
		}
		fmt.Fprintf(g.out, "declare %s @%s(%s)\n", ret, llvmSymbol(name), strings.Join(parts, ", "))
	}
	globals := make([]string, 0, len(g.externalGlobals))
	for name := range g.externalGlobals {
		globals = append(globals, name)
	}
	sort.Strings(globals)
	for _, name := range globals {
		info := g.globalSymbols[name]
		if info == nil || info.typ == "" {
			continue
		}
		fmt.Fprintf(g.out, "@%s = external global %s\n", llvmSymbol(name), info.typ)
	}
}
