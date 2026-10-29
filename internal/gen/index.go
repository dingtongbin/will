package gen

import (
	"fmt"
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

// elementText returns the element type text of a fixed array, a view, an
// Array, or one UTF-8 scalar of a str. A str index yields a u32 scalar; the
// index lowering reads and decodes it.
func elementText(text string) (string, bool) {
	if _, elem, ok := arrayTypeText(text); ok {
		return elem, true
	}
	if elem, ok := containerElem(text); ok {
		return elem, true
	}
	if text == "str" {
		return "u32", true
	}
	return "", false
}

// indexValue lowers an index expression to an i64 value. An index may have any
// integer type; a signed value below zero is out of bounds before the length is
// compared, and a narrower value is extended so one comparison covers both.
func (g *generator) indexValue(id int, r willc.Record) (string, error) {
	text, err := g.willTypeText(id)
	if err != nil {
		return "", err
	}
	typ, _, ok := llvmScalar(text)
	if !ok || typ == "i1" || typ == "ptr" {
		return "", g.errf(r.ID, "cannot translate node #%d: an index must be an integer", r.ID)
	}
	v, err := g.value(id)
	if err != nil {
		return "", err
	}
	unsigned := isUnsigned(text)
	if typ == "i64" {
		if unsigned {
			return v, nil
		}
		if !g.nonnegativeLiteral(id) {
			bad := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp slt i64 %s, 0\n", bad, v)
			g.panicCheck(bad, panicIndex, r)
		}
		return v, nil
	}
	if !unsigned && !g.nonnegativeLiteral(id) {
		bad := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp slt %s %s, 0\n", bad, typ, v)
		g.panicCheck(bad, panicIndex, r)
	}
	out := g.newTmp()
	instr := "sext"
	if unsigned {
		instr = "zext"
	}
	fmt.Fprintf(g.out, "  %s = %s %s %s to i64\n", out, instr, typ, v)
	return out, nil
}

// nonnegativeLiteral reports whether an expression is an integer literal that
// cannot be below zero. A signed index that is already known not to be negative
// needs no signedness check.
func (g *generator) nonnegativeLiteral(id int) bool {
	r, ok := g.recs[id]
	if !ok || r.Kind != "lit" || fieldText(r, "kind") != "int" {
		return false
	}
	_, err := strconv.ParseUint(literalIntText(fieldText(r, "text")), 0, 64)
	return err == nil
}

// indexAddr lowers xs[i] to the address of the element. The index is a usize;
// every access checks it against the length, because only iteration over a
// borrowed container proves the bound statically.
func (g *generator) indexAddr(r willc.Record) (string, string, error) {
	recvID := refField(r, "recv")
	text, err := g.willTypeText(recvID)
	if err != nil {
		return "", "", err
	}
	elem, ok := elementText(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: only an array, a view, or an Array is indexed yet", r.ID)
	}
	elemType, ok := g.typeLLVMOf(elem)
	if !ok || elemType == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: element type %s is not lowered yet", r.ID, elem)
	}
	idx, err := g.indexValue(refField(r, "i"), r)
	if err != nil {
		return "", "", err
	}
	base, baseType, err := g.place(recvID)
	if err != nil {
		return "", "", err
	}
	if n, _, ok := arrayTypeText(text); ok {
		bad := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %d\n", bad, idx, n)
		g.panicCheck(bad, panicIndex, r)
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 0, i64 %s\n", ptr, baseType, base, idx)
		return ptr, elemType, nil
	}

	// A view and an Array start with the data pointer and the length, so the
	// element address is the pointer plus the index.
	ptr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", ptr, baseType, base)
	data := g.load("ptr", ptr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", lenPtr, baseType, base)
	length := g.load("i64", lenPtr)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n", addr, elemType, data, idx)
	return addr, elemType, nil
}
