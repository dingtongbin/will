package gen

import "fmt"

// asyncFrameDropFields reports the owning locals stored in one typed async
// frame. Parameters are borrows: the caller owns them and the frame must not
// release them. Synthetic status and result fields are consumed by the state
// machine, so they are not part of the frame cleanup either.
func (g *generator) asyncFrameDropFields(info *asyncMachineInfo, includeParams bool) []asyncField {
	if info == nil {
		return nil
	}
	released := g.releasedOwners()
	var out []asyncField
	for _, f := range info.fields {
		if f.id == 0 {
			continue
		}
		if f.param {
			if includeParams && !f.paramMut && ownsReleaseText(g, f.willText) {
				out = append(out, f)
			}
			continue
		}
		if !released[f.id] {
			continue
		}
		if ownsReleaseText(g, f.willText) {
			out = append(out, f)
		}
	}
	return out
}

// asyncFrameDropSymbol returns the cleanup callback for one typed async frame.
// A spawned frame owns its by-value parameters; an awaited frame borrows them.
func (g *generator) asyncFrameDropSymbol(info *asyncMachineInfo, ownsParams bool) string {
	if info == nil {
		return ""
	}
	ownFields := g.asyncFrameDropFields(info, true)
	borrowFields := g.asyncFrameDropFields(info, false)
	if ownsParams {
		if len(ownFields) == 0 {
			return ""
		}
		if len(ownFields) != len(borrowFields) {
			return info.symbol + ".drop.own"
		}
		return info.symbol + ".drop"
	}
	if len(ownFields) == 0 && len(borrowFields) == 0 {
		return ""
	}
	return info.symbol + ".drop"
}

// emitAsyncFrameDrop writes the callback the task runtime calls before it
// returns a typed async frame to the allocator. A normal completion has already
// cleared every released local, so the same callback is safe on that path too.
func (g *generator) emitAsyncFrameDrop(info *asyncMachineInfo, sym string, includeParams bool) error {
	fields := g.asyncFrameDropFields(info, includeParams)
	if len(fields) == 0 && sym == "" {
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
