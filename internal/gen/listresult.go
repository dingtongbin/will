package gen

import "github.com/will-lang/will/internal/willc"

// listCallResult returns the LLVM and will result types of a list method.
// known is true for a method the lowering handles; llvm is empty for a method
// that returns no value.
func (g *generator) listCallResult(r willc.Record) (llvm, text string, known bool) {
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", "", false
	}
	elem, ok := listElemText(stripPredeclaredPackage(recvText))
	if !ok {
		return "", "", false
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", false
	}
	switch fieldText(r, "text") {
	case "len", "cap":
		return "i64", "usize", true
	case "get", "remove":
		return elemLLVM, elem, true
	case "pop":
		if info, ok := g.adtType("Option<" + elem + ">"); ok {
			return info.llvm, info.text, true
		}
		return "", "", false
	case "push", "append", "set", "insert", "clear", "shrink_to_fit":
		return "", "", true
	case "slice":
		return "{ ptr, i64 }", "[]" + elem, true
	case "clone":
		if llvm, ok := llvmTypeText("list[" + elem + "]"); ok {
			return llvm, "list[" + elem + "]", true
		}
		return "", "", false
	}
	return "", "", false
}
