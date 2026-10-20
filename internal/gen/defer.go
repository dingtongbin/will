package gen

import (
	"github.com/will-lang/will/internal/willc"
)

// deferStmt records one deferred call in the innermost open block. The call
// itself is lowered when that block exits, so a defer inside a loop runs once
// per iteration from the same emitted code.
func (g *generator) deferStmt(st willc.Record) error {
	callID := refField(st, "call")
	call, ok := g.recs[callID]
	if !ok || (call.Kind != "call" && call.Kind != "mcall") {
		return g.errf(st.ID, "cannot translate node #%d: defer needs one call", st.ID)
	}
	if len(g.defers) == 0 {
		return g.errf(st.ID, "cannot translate node #%d: defer has no block", st.ID)
	}
	last := len(g.defers) - 1
	g.defers[last] = append(g.defers[last], callID)
	return nil
}

// runDefers lowers every deferred call from the innermost frame down to
// lowest, each frame last in first out. The frames stay on the compile-time
// stack, so another branch of the same block still lowers them on its path.
func (g *generator) runDefers(lowest int) error {
	for i := len(g.defers) - 1; i >= lowest; i-- {
		frame := g.defers[i]
		for j := len(frame) - 1; j >= 0; j-- {
			if err := g.lowerDeferredCall(frame[j]); err != nil {
				return err
			}
		}
	}
	return nil
}

// lowerDeferredCall lowers one call for its side effect. A value result is
// discarded, as an expression statement does.
func (g *generator) lowerDeferredCall(id int) error {
	r, ok := g.recs[id]
	if !ok {
		return g.errf(id, "cannot translate node #%d: the deferred call is missing", id)
	}
	_, _, err := g.callExpr(r)
	return err
}

// containsTry reports whether a statement subtree holds a ? expression. The
// release group before such a statement belongs to the error path.
func (g *generator) containsTry(id int) bool {
	for _, child := range g.children(id) {
		if child.Kind == "try" || g.containsTry(child.ID) {
			return true
		}
	}
	return false
}

// flushTryReleases runs the release group of a failing ? expression.
func (g *generator) flushTryReleases() error {
	pending := g.pendingTryReleases
	g.pendingTryReleases = nil
	for _, rel := range pending {
		if err := g.releaseRecord(rel); err != nil {
			return err
		}
	}
	return nil
}
