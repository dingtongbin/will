package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// viewElemText returns the element text of a read-only view []T.
func viewElemText(text string) (string, bool) {
	if !strings.HasPrefix(text, "[]") {
		return "", false
	}
	elem := text[2:]
	if elem == "" {
		return "", false
	}
	return elem, true
}

// viewCallResult returns the LLVM and will result types of a view method.
func (g *generator) viewCallResult(r willc.Record) (llvm, text string, known bool) {
	if r.Kind != "mcall" {
		return "", "", false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return "", "", false
	}
	elem, ok := viewElemText(stripPredeclaredPackage(recvText))
	if !ok {
		return "", "", false
	}
	switch fieldText(r, "text") {
	case "len":
		return "i64", "usize", true
	case "get":
		elemLLVM, ok := g.typeLLVMOf(elem)
		if !ok || elemLLVM == "" {
			return "", "", false
		}
		return elemLLVM, elem, true
	case "equal", "has_prefix", "has_suffix":
		if elem == "u8" {
			return "i1", "bool", true
		}
	case "compare":
		if elem == "u8" {
			return "i32", "i32", true
		}
	case "index":
		if elem == "u8" {
			if info, ok := g.adtType("Option<usize>"); ok {
				return info.llvm, "Option<usize>", true
			}
		}
	case "clone":
		if elem == "u8" {
			return "ptr", "list[u8]", true
		}
	}
	return "", "", false
}

// viewMethod lowers a method call on a read-only view []T.
func (g *generator) viewMethod(r willc.Record, elem string) (string, string, error) {
	base, baseType, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the receiver is not a view", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: view element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	name := fieldText(r, "text")
	if name == "len" {
		return g.load("i64", lenField), "usize", nil
	}
	if elem == "u8" {
		switch name {
		case "equal", "compare", "has_prefix", "has_suffix", "clone":
			return g.byteViewMethod(r, base, lenField, name)
		}
	}
	args := g.callArgs(r)
	if len(args) == 0 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: get needs an index", r.ID)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	length := g.load("i64", lenField)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemLLVM, data, idx)
	return g.load(elemLLVM, ptr), elem, nil
}

// viewGetPlace returns the address of one element of a view whose element is an
// aggregate, so a field or a str method can read it in place.
func (g *generator) viewGetPlace(r willc.Record, elem string) (string, string, error) {
	base, baseType, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	if baseType != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: get needs a view", r.ID)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: view element %s is not lowered yet", r.ID, elem)
	}
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	length := g.load("i64", lenField)
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: get takes one index", r.ID)
	}
	idx, err := g.indexValue(args[0], r)
	if err != nil {
		return "", "", err
	}
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", ptr, elemLLVM, data, idx)
	return ptr, elemLLVM, nil
}

// intLitValue returns the value of the first argument when it is an integer
// literal, or -1 otherwise.
func intLitValue(args []int, g *generator) int {
	if len(args) == 0 {
		return -1
	}
	r, ok := g.recs[args[0]]
	if !ok || r.Kind != "lit" || fieldText(r, "kind") != "int" {
		return -1
	}
	text := literalIntText(fieldText(r, "text"))
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return -1
		}
		n = n*10 + int(text[i]-'0')
		if n > 1<<30 {
			return -1
		}
	}
	return n
}
