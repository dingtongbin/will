package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// storeArrayLiteral writes one fixed-array literal into its entry-block slot.
// Elements are evaluated left to right; an owning element is moved, while a
// borrowed concrete interface element is boxed into the array.
func (g *generator) storeArrayLiteral(r willc.Record) error {
	text := fieldText(r, "type")
	typ, ok := g.typeLLVMOf(text)
	if !ok || typ == "" {
		return g.errf(r.ID, "cannot translate node #%d: fixed array type %s is not lowered yet", r.ID, text)
	}
	n, elem, ok := arrayTypeText(text)
	if !ok {
		return g.errf(r.ID, "cannot translate node #%d: %s is not a fixed array", r.ID, text)
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return g.errf(r.ID, "cannot translate node #%d: fixed array element %s is not lowered yet", r.ID, elem)
	}
	slot := g.arrayLitSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.arrayLitSlots, "arrlit", r.ID, typ)
	}
	fmt.Fprintf(g.out, "  store %s zeroinitializer, ptr %s\n", typ, slot)
	elems := refsOf(r, "elem")
	if len(elems) != n {
		return g.errf(r.ID, "cannot translate node #%d: fixed array literal has %d elements, want %d", r.ID, len(elems), n)
	}
	for i, id := range elems {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			field, typ, slot, i)
		if iface, ok := g.ifaceOfText(elem); ok {
			sourceText, err := g.willTypeText(id)
			if err != nil {
				return err
			}
			if _, err := g.ifaceStoreValue(field, id, iface, sourceText); err != nil {
				return err
			}
			continue
		}
		if isAggregateType(elemLLVM) {
			src, srcType, err := g.place(id)
			if err != nil {
				return err
			}
			if srcType != elemLLVM {
				return g.errf(r.ID, "cannot translate node #%d: array element needs %s", r.ID, elemLLVM)
			}
			move, _ := g.moveSource(id)
			if !move && g.needsReleaseText(elem) && g.isCopySource(id) {
				if err := g.retainValue(src, elem); err != nil {
					return err
				}
			}
			g.copyValue(src, field, elemLLVM)
			if move {
				if err := g.zeroMovedSource(id, field); err != nil {
					return err
				}
			}
			continue
		}
		value, err := g.value(id)
		if err != nil {
			return err
		}
		if isRcText(elem) && g.isCopySource(id) {
			g.needRcDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_rc_retain(i64 %s)\n", value)
		}
		if isChanText(elem) && g.isCopySource(id) {
			g.needTaskDecl = true
			g.needChanDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_chan_retain(ptr %s)\n", value)
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, field)
	}
	return nil
}
