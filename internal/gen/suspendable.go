package gen

import "github.com/will-lang/will/internal/willc"

// collectSuspendable marks the functions that cannot run as a normal call on
// a lane. A direct async C call marks its function; a call to a marked function
// marks the caller. The fixed point is needed because a wrapper can call
// another wrapper before it reaches the C shim.
func (g *generator) collectSuspendable() {
	for pass := 0; pass < 32; pass++ {
		changed := false
		for _, d := range g.docs {
			for id, sym := range d.symbols {
				if sym == "" || g.suspendable[sym] {
					continue
				}
				fn, ok := d.recs[id]
				if !ok || fn.Kind != "func" {
					continue
				}
				if simpleAsyncBody(d, id, g.suspendable) {
					g.suspendable[sym] = true
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

// collectTaskCtors lists the task constructors this module defines. A caller
// inside the module uses the definition; a caller in another object needs a
// declaration for it.
func (g *generator) collectTaskCtors(keep func(d *docState) bool) {
	for _, d := range g.docs {
		if keep != nil && !keep(d) {
			continue
		}
		g.use(d)
		for id, sym := range d.symbols {
			if sym == "" {
				continue
			}
			if g.generalMachine(id) {
				g.taskCtors[sym+".task"] = true
				continue
			}
			if _, ok := g.asyncSequenceMachine(id); ok {
				g.taskCtors[sym+".task"] = true
			}
		}
	}
}

// taskCtorDefined reports whether this module defines the constructor of a
// waiting function. The definition and a declaration of one name cannot sit
// in the same module.
func (g *generator) taskCtorDefined(sym string) bool {
	return g.taskCtors[sym+".task"]
}

// awaitTargetSymbol returns the symbol of the function a call names when that
// function waits. A method call names its function through the receiver.
func (g *generator) awaitTargetSymbol(r willc.Record) string {
	if sym := g.callTargetSymbol(r); sym != "" {
		return sym
	}
	if r.Kind != "mcall" {
		return ""
	}
	sym, err := g.methodSymbol(r)
	if err != nil {
		return ""
	}
	return sym
}
