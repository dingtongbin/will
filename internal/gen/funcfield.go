package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// functionField returns the struct layout, field index, and function type of a
// method call that is really a call of a function-valued field.
func (g *generator) functionField(r willc.Record) (*structInfo, int, string, bool) {
	if r.Kind != "mcall" {
		return nil, 0, "", false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return nil, 0, "", false
	}
	info := g.byName[recvText]
	if info == nil {
		if typ, ok := g.typeLLVMOf(recvText); ok {
			info = g.structInfoByLLVM(typ)
		}
	}
	if info == nil || !info.ok {
		return nil, 0, "", false
	}
	index, ok := info.index[fieldText(r, "text")]
	if !ok {
		return nil, 0, "", false
	}
	text := info.texts[index]
	if !strings.HasPrefix(text, "func(") {
		return nil, 0, "", false
	}
	return info, index, text, true
}

// fieldCall loads the code and environment words from a function-valued field
// and calls them with the usual closure calling convention.
func (g *generator) fieldCall(r willc.Record, info *structInfo, index int, fnText string) (string, string, error) {
	params, result, ok := funcTypeParts(fnText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a function type", r.ID, fnText)
	}
	base, _, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	field := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
		field, info.llvm, base, index)
	codePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codePtr, field)
	code := g.load("ptr", codePtr)
	envPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envPtr, field)
	env := g.load("ptr", envPtr)
	args := g.callArgs(r)
	if len(args) != len(params) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the function field takes %d arguments", r.ID, len(params))
	}
	parts := []string{"ptr " + env}
	for i, id := range args {
		parts, err = g.callArgParts(parts, r, i, id, params[i], false)
		if err != nil {
			return "", "", err
		}
	}
	if result == "" || result == "()" {
		fmt.Fprintf(g.out, "  call void %s(%s)\n", code, strings.Join(parts, ", "))
		return "", "", nil
	}
	retLLVM := g.resultLLVM(result)
	if retLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: result type %s is not lowered yet", r.ID, result)
	}
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s %s(%s)\n", out, retLLVM, code, strings.Join(parts, ", "))
	return out, result, nil
}
