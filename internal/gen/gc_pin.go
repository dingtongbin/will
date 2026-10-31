package gen

import "fmt"

// pinObjectValue pins one GC object for a scope whose interior addresses
// survive allocations. The returned pointer remains valid until the matching
// unpin; the pin also keeps the object reachable.
func (g *generator) pinObjectValue(object string) string {
	g.needGCDecl = true
	pinned := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_pin(ptr %s)\n", pinned, object)
	return pinned
}

// pinClassFieldOwner pins the class object that owns one field place. A field
// address is an interior pointer, so a moving collection during evaluation of
// the right-hand side or a call argument would leave it stale. The field ID
// may be nested through inline struct fields; the first class receiver on the
// chain is the object to pin.
func (g *generator) pinClassFieldOwner(id int) (string, string, error) {
	for {
		r, ok := g.recs[id]
		if !ok || r.Kind != "field" {
			return "", "", nil
		}
		recvID := refField(r, "recv")
		text, err := g.willTypeText(recvID)
		if err != nil {
			return "", "", err
		}
		if g.isClassText(text) {
			object, err := g.value(recvID)
			if err != nil {
				return "", "", err
			}
			pinned := g.pinObjectValue(object)
			return pinned, pinned, nil
		}
		id = recvID
	}
}
