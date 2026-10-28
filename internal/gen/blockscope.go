package gen

// insideBlock reports whether id is the block itself or one of its descendants.
func (g *generator) insideBlock(id, block int) bool {
	for id != 0 {
		if id == block {
			return true
		}
		r, ok := g.recs[id]
		if !ok {
			return false
		}
		id = refField(r, "parent")
	}
	return false
}
