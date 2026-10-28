package gen

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

func (g *generator) builtinErrorSize() string {
	if size, _, ok := g.sizeAlign("{ ptr, i64, i32 }"); ok {
		return strconv.Itoa(size)
	}
	return "24"
}

// builtinErrorPlace returns the predeclared error as a module-level constant.
// A per-use alloca does not survive the frame relocation of a state machine,
// and Err(Canceled) is legal inside any function body.
func (g *generator) builtinErrorPlace(r willc.Record, name string) (string, string, error) {
	kind := 8
	if name == "Deadline" {
		kind = 9
	}
	if g.builtinErrors == nil {
		g.builtinErrors = map[int]bool{}
	}
	g.builtinErrors[kind] = true
	return fmt.Sprintf("@will.error.value.%d", kind), "%interface.Error", nil
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
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, g.builtinErrorSize())
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr @will.error.data.%d, i64 %s)\n",
		box, kind, g.builtinErrorSize())
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%interface.Error, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", box, dataPtr)
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
		g.needAllocDecl = true
		g.needMemcpyDecl = true
		g.needFreeStrDecl = true
		cloneSize := g.builtinErrorSize()
		fmt.Fprintf(g.out, "define internal ptr @will.error.clone.%d(ptr %%data) {\nentry:\n", kind)
		box := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, cloneSize)
		fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %%data, i64 %s)\n", box, cloneSize)
		fmt.Fprintf(g.out, "  ret ptr %s\n}\n", box)
		fmt.Fprintf(g.out, "define internal void @will.error.drop.%d(ptr %%data) {\nentry:\n", kind)
		msg := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i32 }, ptr %%data, i32 0, i32 0\n", msg)
		messageData := g.load("ptr", msg)
		fmt.Fprintf(g.out, "  call void @willrt_free_str(ptr %s)\n", messageData)
		g.out.WriteString("  ret void\n}\n")
		identity := g.typeIdentity("std/errors.Fault")
		fmt.Fprintf(g.out, "@will.error.table.%d = private constant [5 x ptr] [ptr @will.error.message.%d, ptr @will.error.status.%d, ptr @will.error.clone.%d, ptr @will.error.drop.%d, ptr @%s]\n",
			kind, kind, kind, kind, kind, llvmSymbol(identity))
		fmt.Fprintf(g.out, "@will.error.value.%d = private constant %%interface.Error { ptr @will.error.data.%d, ptr @will.error.table.%d }\n",
			kind, kind, kind)
	}
}
