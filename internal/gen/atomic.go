package gen

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// atomicElemText returns T from std/sync.Atomic<T>.
func (g *generator) atomicElemText(text string) (string, bool) {
	text = g.canonicalTypeText(text)
	for _, prefix := range []string{"std/sync.Atomic<", "sync.Atomic<", "Atomic<"} {
		if strings.HasPrefix(text, prefix) && strings.HasSuffix(text, ">") {
			elem := strings.TrimSpace(text[len(prefix) : len(text)-1])
			if elem != "" {
				return elem, true
			}
		}
	}
	return "", false
}

func (g *generator) atomicMethod(r willc.Record, elem, recvText string) (string, string, error) {
	base, _, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", "", err
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" || isAggregateType(elemLLVM) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic element %s is not a scalar", r.ID, elem)
	}
	_, align, ok := g.sizeAlign(elemLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic element %s has no layout", r.ID, elem)
	}
	name := fieldText(r, "text")
	args := g.callArgs(r)
	switch name {
	case "load":
		if len(args) != 0 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.load takes no arguments", r.ID)
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load atomic %s, ptr %s seq_cst, align %d\n",
			out, elemLLVM, base, align)
		return out, elem, nil
	case "store":
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.store takes one value", r.ID)
		}
		value, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store atomic %s %s, ptr %s seq_cst, align %d\n",
			elemLLVM, value, base, align)
		return "", "", nil
	case "add":
		if len(args) != 1 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.add takes one value", r.ID)
		}
		if !strings.HasPrefix(elemLLVM, "i") {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.add needs an integer element", r.ID)
		}
		delta, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		old := g.newTmp()
		fmt.Fprintf(g.out, "  %s = atomicrmw add ptr %s, %s %s seq_cst\n",
			old, base, elemLLVM, delta)
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = add %s %s, %s\n", out, elemLLVM, old, delta)
		return out, elem, nil
	case "cas":
		if len(args) != 2 {
			return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.cas takes old and new values", r.ID)
		}
		old, err := g.value(args[0])
		if err != nil {
			return "", "", err
		}
		next, err := g.value(args[1])
		if err != nil {
			return "", "", err
		}
		pair := g.newTmp()
		fmt.Fprintf(g.out, "  %s = cmpxchg ptr %s, %s %s, %s %s seq_cst seq_cst\n",
			pair, base, elemLLVM, old, elemLLVM, next)
		okValue := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue { %s, i1 } %s, 1\n",
			okValue, elemLLVM, pair)
		return okValue, "bool", nil
	}
	return "", "", g.errf(r.ID, "cannot translate node #%d: Atomic.%s is not lowered", r.ID, name)
}
