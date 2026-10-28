package gen

import (
	"fmt"
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

// packVariadic copies the tail arguments of one call into a plate and returns
// the slot holding the view the callee reads. The callee's last parameter is a
// view of the declared element type.
func (g *generator) packVariadic(r willc.Record, name string, tail []int) (string, error) {
	params := g.paramTypes[name]
	if len(params) == 0 {
		return "", g.errf(r.ID, "cannot translate node #%d: the variadic parameter is missing", r.ID)
	}
	elemText := params[len(params)-1]
	elemLLVM, ok := g.typeLLVMOf(elemText)
	if !ok || elemLLVM == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: variadic element %s is not lowered yet", r.ID, elemText)
	}
	plate := "null"
	if len(tail) != 0 {
		g.needPlateDecl = true
		size := g.sizeOf(elemLLVM)
		plate = g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_plate_new(i64 %d, i64 %s)\n",
			plate, len(tail), size)
	}
	for i, id := range tail {
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %s, i64 %d\n", ptr, elemLLVM, plate, i)
		typ, err := g.typeOf(id)
		if err != nil {
			return "", err
		}
		if iface, ok := g.ifaceOfText(elemText); ok {
			argText, err := g.willTypeText(id)
			if err != nil {
				return "", err
			}
			if _, already := g.ifaceOfText(argText); !already {
				// A variadic interface element carries the data word and the
				// method table, so a concrete argument is boxed here.
				if _, err := g.ifaceValue(ptr, id, iface, argText); err != nil {
					return "", err
				}
				continue
			}
		}
		if isAggregateType(typ) {
			src, srcType, err := g.place(id)
			if err != nil {
				return "", err
			}
			if srcType != elemLLVM {
				return "", g.errf(id, "cannot translate node #%d: the argument needs %s", id, elemLLVM)
			}
			value := g.load(elemLLVM, src)
			fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, ptr)
			continue
		}
		value, err := g.value(id)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, ptr)
	}
	slot := g.allocSlot(g.variadicSlots, "varargs", r.ID, "{ ptr, i64 }")
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", plate, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", strconv.Itoa(len(tail)), lenField)
	return slot, nil
}
