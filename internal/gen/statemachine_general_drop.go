package gen

import (
	"fmt"
	"sort"
)

// generalFrameOwner is one owning local slot in a raw general state-machine
// frame. The offset is the byte offset assigned by relocateGeneralAllocas.
type generalFrameOwner struct {
	id     int
	offset int
	llvm   string
	text   string
}

// generalFrameOwners collects local owners in reverse declaration order. The
// frame drop and the constructor use the same list, so initialization and
// cleanup cannot disagree about which slots belong to the frame.
func (g *generator) generalFrameOwners(fnID int, offsets map[string]int) []generalFrameOwner {
	released := g.releasedOwners()
	var out []generalFrameOwner
	for _, r := range g.recs {
		if r.Kind != "var" || !released[r.ID] || g.enclosingFunc(r.ID) != fnID {
			continue
		}
		text := fieldText(r, "type")
		if text == "" {
			var err error
			text, err = g.willTypeText(r.ID)
			if err != nil {
				continue
			}
		}
		if !ownsReleaseText(g, text) {
			continue
		}
		slot := g.slots[r.ID]
		if slot == "" {
			continue
		}
		offset, ok := offsets[slot]
		if !ok {
			continue
		}
		llvm, ok := g.typeLLVMOf(text)
		if !ok || llvm == "" {
			continue
		}
		out = append(out, generalFrameOwner{id: r.ID, offset: offset, llvm: llvm, text: text})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].id > out[j].id
	})
	return out
}

// emitGeneralFrameZero clears every owning local before the task can run. An
// early drop must see null owners for locals that have not been initialized.
func (g *generator) emitGeneralFrameZero(frame string, owners []generalFrameOwner) {
	for _, owner := range owners {
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr %s, i64 %d\n", ptr, frame, owner.offset)
		fmt.Fprintf(g.out, "  store %s zeroinitializer, ptr %s\n", owner.llvm, ptr)
	}
}

// emitGeneralFrameDrop writes the callback that releases locals left in a
// general state-machine frame after cancellation, panic, or normal completion.
func (g *generator) emitGeneralFrameDrop(sym string, owners []generalFrameOwner) error {
	if len(owners) == 0 {
		return nil
	}
	fmt.Fprintf(g.out, "define internal void @%s(ptr %%frame) {\nentry:\n", llvmSymbol(sym+".drop"))
	for _, owner := range owners {
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr %%frame, i64 %d\n", ptr, owner.offset)
		if err := g.releaseOwnedValue(ptr, owner.text); err != nil {
			return err
		}
	}
	g.out.WriteString("  ret void\n}\n")
	return nil
}
