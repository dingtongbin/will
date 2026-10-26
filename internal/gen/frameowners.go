package gen

// releasedOwners returns the local owners that have at least one release point
// in this document. A frame cleanup uses this set instead of every owning local:
// a leaked local has no release record and must keep its explicit leak, while a
// moved or returned local has no record and is already cleared at the transfer.
func (g *generator) releasedOwners() map[int]bool {
	out := make(map[int]bool)
	for _, r := range g.recs {
		if r.Kind != "release" {
			continue
		}
		if owner := refField(r, "owner"); owner != 0 {
			out[owner] = true
		}
	}
	return out
}
