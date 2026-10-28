package gen

import (
	"fmt"
	"sort"

	"github.com/will-lang/will/internal/willc"
)

// panicStmt lowers `panic(message)`. The message is a str value, so the call
// carries its bytes and length and the runtime does not need a terminator.
func (g *generator) panicStmt(r willc.Record) (bool, error) {
	args := refsOf(r, "arg")
	if len(args) == 0 {
		return false, g.errf(r.ID, "cannot translate node #%d: panic needs a message", r.ID)
	}
	data, length, err := g.strParts(args[0])
	if err != nil {
		return false, err
	}
	ids := make([]int, 0, len(g.liveValues))
	for id := range g.liveValues {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		slot := g.slots[id]
		text := g.liveValues[id]
		if slot == "" || !g.needsReleaseText(text) {
			continue
		}
		if err := g.releaseSlot(slot, text); err != nil {
			return false, err
		}
	}
	g.needPanicDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 %s, ptr %s, ptr %s)\n",
		data, length, g.globalString(fieldText(r, "pos")), g.globalString(g.fnSymbol))
	g.out.WriteString("  unreachable\n")
	return true, nil
}

// assertStmt lowers `assert(cond, message)`: the message is reported only when
// the condition is false.
func (g *generator) assertStmt(r willc.Record) (bool, error) {
	args := refsOf(r, "arg")
	if len(args) != 2 {
		return false, g.errf(r.ID, "cannot translate node #%d: assert needs a condition and a message", r.ID)
	}
	cond, err := g.value(args[0])
	if err != nil {
		return false, err
	}
	g.labels++
	n := g.labels
	bad := fmt.Sprintf("assert.bad.%d", n)
	ok := fmt.Sprintf("assert.ok.%d", n)
	not := g.newTmp()
	fmt.Fprintf(g.out, "  %s = xor i1 %s, true\n", not, cond)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", not, bad, ok)
	g.out.WriteString(bad + ":\n")
	data, length, err := g.strParts(args[1])
	if err != nil {
		return false, err
	}
	g.needPanicDecl = true
	fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 %s, ptr %s, ptr %s)\n",
		data, length, g.globalString(fieldText(r, "pos")), g.globalString(g.fnSymbol))
	g.out.WriteString("  unreachable\n")
	g.out.WriteString(ok + ":\n")
	return false, nil
}
