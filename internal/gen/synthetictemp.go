package gen

// syntheticTemp is one compiler-created owning slot, such as a child task
// result. The slot is zero-initialized, cleared when a move consumes it, and
// released after the statement or by the frame drop.
type syntheticTemp struct {
	slot string
	text string
	llvm string
}

// addSyntheticTemp records one owning temporary in the statement list and the
// current state-machine frame. A slot is recorded once.
func (g *generator) addSyntheticTemp(slot, text, llvm string) {
	if slot == "" || text == "" || llvm == "" {
		return
	}
	if g.frameTempSeen == nil {
		g.frameTempSeen = map[string]bool{}
	}
	if g.frameTempSeen[slot] {
		return
	}
	temp := syntheticTemp{slot: slot, text: text, llvm: llvm}
	g.syntheticTemps = append(g.syntheticTemps, temp)
	if !g.smGeneral {
		return
	}
	g.frameTempSeen[slot] = true
	g.frameTemps = append(g.frameTemps, temp)
}

// releaseSyntheticTemps drops every temporary created by the statement that
// just finished. A moved temporary is already zero, so the release is a no-op.
func (g *generator) releaseSyntheticTemps() error {
	temps := g.syntheticTemps
	g.syntheticTemps = nil
	for _, temp := range temps {
		if err := g.releaseOwnedValue(temp.slot, temp.text); err != nil {
			return err
		}
		g.storeZero(temp.slot, temp.llvm)
	}
	return nil
}

// clearSyntheticCallSource clears the child-result slot a move or transfer has
// consumed. The value itself continues in the destination; releasing the slot
// would free the same owner twice.
func (g *generator) clearSyntheticCallSource(id int) {
	for {
		record, ok := g.recs[id]
		if !ok {
			return
		}
		if record.Kind == "await" {
			id = refField(record, "expr")
			continue
		}
		place, ok := g.smPlaces[id]
		if !ok {
			return
		}
		g.storeZero(place.ptr, place.typ)
		return
	}
}

// resetSyntheticTemps starts one function or state-machine frame with no
// active or stored synthetic owners.
func (g *generator) resetSyntheticTemps() {
	g.syntheticTemps = nil
	g.frameTemps = nil
	g.frameTempSeen = map[string]bool{}
}
