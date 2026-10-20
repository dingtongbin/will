package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// predeclaredCallName returns the builtin name a call names, such as take_c.
// The callee is a name record in the same document.
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

// lowerTakeC adopts a C pointer. The two function values use the closure ABI
// (environment, pointer); the runtime keeps them beside the pointer and calls
// them on retain and final release.
func (g *generator) lowerTakeC(r willc.Record, name string) (string, error) {
	args := g.callArgs(r)
	need := 2
	if name == "take_c_rc" {
		need = 3
	}
	if len(args) != need {
		return "", g.errf(r.ID, "cannot translate node #%d: %s takes %d arguments", r.ID, name, need)
	}
	ptr, err := g.value(args[0])
	if err != nil {
		return "", err
	}
	retainCode := "null"
	retainEnv := "null"
	releaseArg := 1
	if name == "take_c_rc" {
		retainCode, retainEnv, err = g.functionValueParts(args[1])
		if err != nil {
			return "", err
		}
		releaseArg = 2
	}
	releaseCode, releaseEnv, err := g.functionValueParts(args[releaseArg])
	if err != nil {
		return "", err
	}
	g.needTakeCDecl = true
	handle := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @willrt_take_c(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)\n",
		handle, ptr, retainCode, retainEnv, releaseCode, releaseEnv)
	return handle, nil
}

// functionValueParts loads the code and environment words of a function value
// placed in the current frame.
func (g *generator) functionValueParts(id int) (string, string, error) {
	slot, typ, err := g.place(id)
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, ptr }" {
		return "", "", g.errf(id, "cannot translate node #%d: the callback is not a function value", id)
	}
	codeField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codeField, slot)
	code := g.load("ptr", codeField)
	envField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envField, slot)
	env := g.load("ptr", envField)
	return code, env, nil
}
