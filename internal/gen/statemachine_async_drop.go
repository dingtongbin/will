package gen

import "fmt"

// asyncFrameDropFields reports the owning locals stored in one typed async
// frame. Parameters are borrows: the caller owns them and the frame must not
// release them. Synthetic status and result fields are consumed by the state
// machine, so they are not part of the frame cleanup either.
func (g *generator) asyncFrameDropFields(info *asyncMachineInfo) []asyncField {
	if info == nil {
		return nil
	}
	released := g.releasedOwners()
	var out []asyncField
	for _, f := range info.fields {
		if f.param || f.id == 0 || !released[f.id] {
			continue
		}
		if ownsReleaseText(g, f.willText) {
			out = append(out, f)
		}
	}
	return out
}

// asyncFrameDropSymbol returns the cleanup callback for one typed async frame.
func (g *generator) asyncFrameDropSymbol(info *asyncMachineInfo) string {
	if len(g.asyncFrameDropFields(info)) == 0 {
		return ""
	}
	return info.symbol + ".drop"
}

// emitAsyncFrameDrop writes the callback the task runtime calls before it
// returns a typed async frame to the allocator. A normal completion has already
// cleared every released local, so the same callback is safe on that path too.
func (g *generator) emitAsyncFrameDrop(info *asyncMachineInfo, sym string) error {
	fields := g.asyncFrameDropFields(info)
	if len(fields) == 0 {
		return nil
	}
	fmt.Fprintf(g.out, "define internal void @%s(ptr %%frame) {\nentry:\n", llvmSymbol(sym))
	for _, f := range fields {
		index := 0
		for i := range info.fields {
			if info.fields[i].id == f.id {
				index = i + 2
				break
			}
		}
		if index == 0 {
			continue
		}
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr %%frame, i32 0, i32 %d\n",
			field, info.frameType, index)
		if err := g.releaseOwnedValue(field, f.willText); err != nil {
			return err
		}
	}
	g.out.WriteString("  ret void\n}\n")
	return nil
}
