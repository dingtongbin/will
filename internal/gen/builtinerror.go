package gen

import (
	"fmt"
	"sort"

	"github.com/will-lang/will/internal/willc"
)

// builtinErrorPlace stores one predeclared Error value in an interface slot.
// The data and method-table globals are emitted by emitBuiltinErrors.
func (g *generator) builtinErrorPlace(r willc.Record, name string) (string, string, error) {
	kind := 8
	if name == "Deadline" {
		kind = 9
	}
	slot := g.allocSlot(g.adtSlots, "builtin.error", r.ID, "%interface.Error")
	g.builtinErrorInto(slot, kind)
	return slot, "%interface.Error", nil
}

// builtinErrorInto fills an interface slot with one predeclared or internal
// fault. Four is the stable Closed status used by a failed Send.
func (g *generator) builtinErrorInto(ptr string, kind int) {
	if !g.errorEmitted {
		g.out.WriteString("%interface.Error = type { ptr, ptr }\n")
		g.errorEmitted = true
	}
	if g.builtinErrors == nil {
		g.builtinErrors = map[int]bool{}
	}
	g.builtinErrors[kind] = true
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%interface.Error, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	fmt.Fprintf(g.out, "  store ptr @will.error.data.%d, ptr %s\n", kind, dataPtr)
	tablePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%interface.Error, ptr %s, i32 0, i32 1\n", tablePtr, ptr)
	fmt.Fprintf(g.out, "  store ptr @will.error.table.%d, ptr %s\n", kind, tablePtr)
}

// emitBuiltinErrors writes the data, Message, Status, and method table for
// each predeclared fault this module used. The functions use the same ABI as
// every other interface method, so no C struct-return boundary is involved.
func (g *generator) emitBuiltinErrors() {
	if len(g.builtinErrors) == 0 {
		return
	}
	kinds := make([]int, 0, len(g.builtinErrors))
	for kind := range g.builtinErrors {
		kinds = append(kinds, kind)
	}
	sort.Ints(kinds)
	for _, kind := range kinds {
		message := "canceled"
		switch kind {
		case 4:
			message = "closed"
		case 9:
			message = "deadline"
		}
		text := g.globalString(message)
		fmt.Fprintf(g.out, "@will.error.data.%d = private constant { ptr, i64, i32 } { ptr %s, i64 %d, i32 %d }\n",
			kind, text, len(message), kind)
		fmt.Fprintf(g.out, "define internal { ptr, i64 } @will.error.message.%d(ptr %%data) {\n", kind)
		g.out.WriteString("entry:\n")
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i32 }, ptr %%data, i32 0, i32 0\n", ptr)
		data := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load ptr, ptr %s\n", data, ptr)
		sizePtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i32 }, ptr %%data, i32 0, i32 1\n", sizePtr)
		size := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load i64, ptr %s\n", size, sizePtr)
		first := g.newTmp()
		fmt.Fprintf(g.out, "  %s = insertvalue { ptr, i64 } undef, ptr %s, 0\n", first, data)
		second := g.newTmp()
		fmt.Fprintf(g.out, "  %s = insertvalue { ptr, i64 } %s, i64 %s, 1\n", second, first, size)
		fmt.Fprintf(g.out, "  ret { ptr, i64 } %s\n}\n", second)
		fmt.Fprintf(g.out, "define internal i32 @will.error.status.%d(ptr %%data) {\n", kind)
		g.out.WriteString("entry:\n")
		statusPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i32 }, ptr %%data, i32 0, i32 2\n", statusPtr)
		status := g.newTmp()
		fmt.Fprintf(g.out, "  %s = load i32, ptr %s\n", status, statusPtr)
		fmt.Fprintf(g.out, "  ret i32 %s\n}\n", status)
		fmt.Fprintf(g.out, "@will.error.table.%d = private constant [2 x ptr] [ptr @will.error.message.%d, ptr @will.error.status.%d]\n",
			kind, kind, kind)
	}
}
