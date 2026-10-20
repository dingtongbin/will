package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// cLinkSig is the prototype a C header gave one import c name.
type cLinkSig struct {
	result   string
	params   []string
	variadic bool
	typed    bool
}

// emitTypedCLink lowers a call whose declaration was read from a C header.
// Arguments are already checked to match the declared will types; a variadic
// tail keeps the type each argument already has.
func (g *generator) emitTypedCLink(r willc.Record, name string, sig cLinkSig) (string, string, error) {
	args := g.callArgs(r)
	fixed := len(sig.params)
	if len(args) < fixed || (!sig.variadic && len(args) != fixed) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s takes %d arguments", r.ID, name, fixed)
	}
	parts := make([]string, 0, len(args))
	declParams := make([]string, 0, len(sig.params))
	for i, arg := range args {
		argType, err := g.typeOf(arg)
		if err != nil {
			return "", "", err
		}
		if argType == "" || isAggregateType(argType) {
			return "", "", g.errf(arg, "cannot translate node #%d: a C call takes scalar arguments", arg)
		}
		value, err := g.value(arg)
		if err != nil {
			return "", "", err
		}
		parts = append(parts, argType+" "+value)
		if i < fixed {
			decl := sig.params[i]
			declType, ok := g.typeLLVMOf(decl)
			if !ok || declType == "" {
				return "", "", g.errf(r.ID, "cannot translate node #%d: C parameter %s is not lowered", r.ID, decl)
			}
			if declType != argType {
				return "", "", g.errf(arg, "cannot translate node #%d: C parameter needs %s", arg, decl)
			}
			declParams = append(declParams, declType)
		} else {
			declParams = append(declParams, argType)
		}
	}
	if sig.variadic {
		declParams = append(declParams, "...")
	}
	retLLVM := "void"
	if sig.result != "" && sig.result != "()" {
		typ, ok := g.typeLLVMOf(sig.result)
		if !ok || typ == "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: C result %s is not lowered", r.ID, sig.result)
		}
		retLLVM = typ
	}
	decl := "declare " + retLLVM + " @" + llvmSymbol(name) + "(" + strings.Join(declParams, ", ") + ")"
	if !g.declSet[decl] {
		g.declSet[decl] = true
		g.decls = append(g.decls, decl)
	}
	if retLLVM == "void" {
		fmt.Fprintf(g.out, "  call void @%s(%s)\n", llvmSymbol(name), strings.Join(parts, ", "))
		return "", "", nil
	}
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s @%s(%s)\n", out, retLLVM, llvmSymbol(name), strings.Join(parts, ", "))
	return out, sig.result, nil
}

// rawMethod lowers the fixed operation set of raw pointers.
func (g *generator) rawMethod(r willc.Record) (string, string, error) {
	ptr, err := g.value(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	name := fieldText(r, "text")
	loads := map[string]string{
		"LoadU8": "i8", "LoadU16": "i16", "LoadU32": "i32", "LoadU64": "i64",
		"LoadI8": "i8", "LoadI16": "i16", "LoadI32": "i32", "LoadI64": "i64",
		"LoadF32": "float", "LoadF64": "double",
	}
	if typ, ok := loads[name]; ok {
		return g.load(typ, ptr), rawResultText(name), nil
	}
	if name == "LoadRaw" {
		return g.load("ptr", ptr), "raw", nil
	}
	stores := map[string]string{
		"StoreU8": "i8", "StoreU16": "i16", "StoreU32": "i32", "StoreU64": "i64",
		"StoreI8": "i8", "StoreI16": "i16", "StoreI32": "i32", "StoreI64": "i64",
		"StoreF32": "float", "StoreF64": "double",
	}
	if typ, ok := stores[name]; ok {
		value, err := g.value(g.callArgs(r)[0])
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, ptr)
		return "", "", nil
	}
	if name == "StoreRaw" {
		value, err := g.value(g.callArgs(r)[0])
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", value, ptr)
		return "", "", nil
	}
	switch name {
	case "VolatileLoadU32":
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load volatile i32, ptr %s\n", out, ptr)
		return out, "u32", nil
	case "VolatileStoreU32":
		value, err := g.value(g.callArgs(r)[0])
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store volatile i32 %s, ptr %s\n", value, ptr)
		return "", "", nil
	case "AtomicLoadU32":
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load atomic i32, ptr %s seq_cst, align 4\n", out, ptr)
		return out, "u32", nil
	case "AtomicStoreU32":
		value, err := g.value(g.callArgs(r)[0])
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store atomic i32 %s, ptr %s seq_cst, align 4\n", value, ptr)
		return "", "", nil
	case "AtomicAddU32":
		value, err := g.value(g.callArgs(r)[0])
		if err != nil {
			return "", "", err
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = atomicrmw add ptr %s, i32 %s seq_cst\n", out, ptr, value)
		return out, "u32", nil
	case "AtomicCasU32":
		args := g.callArgs(r)
		old, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		newValue, err := g.value(args[1])
		if err != nil {
			return "", "", err
		}
		pair := g.newTmp()
		fmt.Fprintf(g.out, "  %s = cmpxchg ptr %s, i32 %s, i32 %s seq_cst seq_cst\n", pair, ptr, old, newValue)
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue { i32, i1 } %s, 1\n", out, pair)
		return out, "bool", nil
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: raw.%s is not lowered", r.ID, name)
}

func rawResultText(name string) string {
	switch name {
	case "LoadU8":
		return "u8"
	case "LoadU16":
		return "u16"
	case "LoadU32":
		return "u32"
	case "LoadU64":
		return "u64"
	case "LoadI8":
		return "i8"
	case "LoadI16":
		return "i16"
	case "LoadI32":
		return "i32"
	case "LoadI64":
		return "i64"
	case "LoadF32":
		return "f32"
	case "LoadF64":
		return "f64"
	}
	return ""
}

// rawCallResult returns the result text of a raw pointer method.
func (g *generator) rawCallResult(r willc.Record) (string, string, bool) {
	if r.Kind != "mcall" {
		return "", "", false
	}
	recv, err := g.willTypeText(refField(r, "recv"))
	if err != nil || recv != "raw" {
		return "", "", false
	}
	name := fieldText(r, "text")
	if text := rawResultText(name); text != "" {
		return "", text, true
	}
	switch name {
	case "LoadRaw":
		return "", "raw", true
	case "StoreU8", "StoreU16", "StoreU32", "StoreU64",
		"StoreI8", "StoreI16", "StoreI32", "StoreI64", "StoreF32", "StoreF64",
		"StoreRaw", "VolatileStoreU32", "AtomicStoreU32":
		return "", "", true
	case "VolatileLoadU32", "AtomicLoadU32", "AtomicAddU32":
		return "", "u32", true
	case "AtomicCasU32":
		return "", "bool", true
	}
	return "", "", false
}
