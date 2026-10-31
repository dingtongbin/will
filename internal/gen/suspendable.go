package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// collectSuspendable marks the functions that cannot run as a normal call on
// a lane. A function that waits on a channel, a task, or a select waits; a
// direct async C call waits; and a call to a waiting function waits as well.
// The fixed point is needed because a wrapper can call another wrapper before
// it reaches the wait.
func (g *generator) collectSuspendable() {
	for pass := 0; pass < 32; pass++ {
		changed := false
		for _, d := range g.docs {
			g.use(d)
			for id, sym := range d.symbols {
				if sym == "" || g.suspendableFor(sym) {
					continue
				}
				fn, ok := g.recs[id]
				if !ok || fn.Kind != "func" {
					continue
				}
				if id == g.entryID && d.path == g.entryPkg {
					// The entry waits with the runtime join on the program
					// thread; it has no task frame.
					continue
				}
				if fieldText(fn, "async") == "true" {
					g.markSuspendable(sym)
					changed = true
					continue
				}
				// A direct C async call still needs a state machine even when
				// the declaration forgot `async`; the type checker reports the
				// missing await at the call site. Do not propagate suspension
				// through ordinary calls: an ordinary function is synchronous
				// by definition now.
				if simpleAsyncBody(d, id, g.suspendable) {
					g.markSuspendable(sym)
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

// channelWaitBody reports whether the function waits on a channel, a task, or
// a select.
func (g *generator) channelWaitBody(fn int) bool {
	body := g.functionBody(fn)
	if body == 0 {
		return false
	}
	found := false
	for _, r := range g.recs {
		if !recordUnder(g.recs, r.ID, body) {
			continue
		}
		switch r.Kind {
		case "send", "recv", "join", "select":
			found = true
		}
	}
	return found
}

// markedBody reports whether the function calls a function that waits. The
// caller waits as well, so it is lowered as a state machine too.
func (g *generator) markedBody(fn int) bool {
	body := g.functionBody(fn)
	if body == 0 {
		return false
	}
	for _, r := range g.recs {
		if !recordUnder(g.recs, r.ID, body) {
			continue
		}
		switch r.Kind {
		case "call", "mcall":
			if g.spawnedCall(r) {
				continue
			}
			if g.callWaits(r) {
				return true
			}
		}
	}
	return false
}

// spawnedCall reports whether the call is the operand of a spawn. A spawn
// starts a task instead of waiting for it.
func (g *generator) spawnedCall(r willc.Record) bool {
	parent, ok := g.recs[refField(r, "parent")]
	return ok && parent.Kind == "spawn"
}

// callWaits reports whether the call reaches a function that waits.
func (g *generator) callWaits(r willc.Record) bool {
	if sym := g.awaitTargetSymbol(r); sym != "" {
		return g.suspendableFor(sym)
	}
	return false
}

// suspendableFor reports whether a symbol or its generic template waits. A
// call names the instance (Mutex<Counter>.Lock) while the analysis marked the
// template (Mutex.Lock); both must answer the same.
func (g *generator) suspendableFor(sym string) bool {
	if sym == "" {
		return false
	}
	if g.suspendable[sym] {
		return true
	}
	base := stripTypeArgs(sym)
	return base != sym && g.suspendable[base]
}

// markSuspendable records a symbol and its generic template, so a call that
// names an instance sees the same answer as a call that names the template.
func (g *generator) markSuspendable(sym string) {
	if sym == "" {
		return
	}
	g.suspendable[sym] = true
	if base := stripTypeArgs(sym); base != sym {
		g.suspendable[base] = true
	}
}

// stripTypeArgs removes the generic argument lists from a symbol. A method
// receiver may be generic, but the method name after the last dot never is.
func stripTypeArgs(sym string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(sym); i++ {
		switch sym[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(sym[i])
			}
		}
	}
	return b.String()
}

// containsWait reports whether an expression can suspend. A compound
// assignment reloads its target after such a value, so the load dominates the
// store in the resume label.
func (g *generator) containsWait(id int) bool {
	for _, r := range g.recs {
		if !recordUnder(g.recs, r.ID, id) {
			continue
		}
		switch r.Kind {
		case "send", "recv", "join", "select":
			return true
		case "call", "mcall":
			if g.spawnedCall(r) {
				continue
			}
			if isAsyncCRecord(r) || g.callWaits(r) {
				return true
			}
		}
	}
	return false
}

// functionBody returns the block that holds the statements of a function.
func (g *generator) functionBody(fn int) int {
	for _, r := range g.recs {
		if r.Kind == "block" && refField(r, "parent") == fn {
			return r.ID
		}
	}
	return 0
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
		// The call target lookup reads the function table this pass fills;
		// without it a free call such as Drain's Worker looks unnamed on the
		// first attempt and the constructor is declared instead of defined.
		g.collect()
		for id, sym := range d.symbols {
			if sym == "" {
				continue
			}
			if g.generalMachine(id) || g.suspendableFor(sym) {
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
// function waits. A generic call names its instance, because the declaration
// itself is never emitted; a method call names its function through the
// receiver.
func (g *generator) awaitTargetSymbol(r willc.Record) string {
	if name := g.genericCallName(r); name != "" {
		return name
	}
	if r.Kind == "mcall" {
		if name := g.genericMethodName(r); name != "" {
			return name
		}
	}
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
