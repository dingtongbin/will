package gen

import "fmt"

// taskErrorInto fills an Error interface slot with the join error copy the
// calling task keeps in its own frame and the shared TaskError method table.
// The copy is a %willrt.task_error slot: the join wrote the error into its
// first member and the status after it, so no accessor depends on the joined
// task outliving the join.
func (g *generator) taskErrorInto(ptr, slot string) {
	if !g.errorEmitted {
		g.out.WriteString("%interface.Error = type { ptr, ptr }\n")
		g.errorEmitted = true
	}
	g.needTaskErrorDecl = true
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, g.sizeOf("%willrt.task_error"))
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n",
		box, slot, g.sizeOf("%willrt.task_error"))
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%interface.Error, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", box, dataPtr)
	tablePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %%interface.Error, ptr %s, i32 0, i32 1\n", tablePtr, ptr)
	fmt.Fprintf(g.out, "  store ptr @will.task.error.table, ptr %s\n", tablePtr)
}

// emitTaskError writes the Message and Status accessors and their table. The
// C side owns the copy layout; these wrappers only adapt its output to the
// interface method ABI.
func (g *generator) emitTaskError() {
	if !g.needTaskErrorDecl {
		return
	}
	g.needAllocDecl = true
	g.needMemcpyDecl = true
	g.out.WriteString("declare void @willrt_task_error_message(ptr, ptr, ptr)\n")
	g.out.WriteString("declare i32 @willrt_task_error_status(ptr)\n")
	g.out.WriteString("define internal { ptr, i64 } @will.task.error.message(ptr %data) {\nentry:\n")
	data := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca ptr\n", data)
	size := g.newTmp()
	fmt.Fprintf(g.out, "  %s = alloca i64\n", size)
	fmt.Fprintf(g.out, "  call void @willrt_task_error_message(ptr %%data, ptr %s, ptr %s)\n", data, size)
	text := g.load("ptr", data)
	length := g.load("i64", size)
	first := g.newTmp()
	fmt.Fprintf(g.out, "  %s = insertvalue { ptr, i64 } undef, ptr %s, 0\n", first, text)
	second := g.newTmp()
	fmt.Fprintf(g.out, "  %s = insertvalue { ptr, i64 } %s, i64 %s, 1\n", second, first, length)
	fmt.Fprintf(g.out, "  ret { ptr, i64 } %s\n}\n", second)
	g.out.WriteString("define internal i32 @will.task.error.status(ptr %data) {\nentry:\n")
	status := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_task_error_status(ptr %%data)\n", status)
	fmt.Fprintf(g.out, "  ret i32 %s\n}\n", status)
	fmt.Fprintf(g.out, "define internal ptr @will.task.error.clone(ptr %%data) {\nentry:\n")
	cloneSize := g.sizeOf("%willrt.task_error")
	box := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", box, cloneSize)
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %%data, i64 %s)\n", box, cloneSize)
	fmt.Fprintf(g.out, "  ret ptr %s\n}\n", box)
	g.out.WriteString("define internal void @will.task.error.drop(ptr %data) {\nentry:\n  ret void\n}\n")
	g.out.WriteString("define internal void @will.task.error.share(ptr %data) {\nentry:\n  ret void\n}\n")
	g.out.WriteString("@will.task.error.id = private constant i8 0\n")
	g.out.WriteString("@will.task.error.table = private constant [6 x ptr] [ptr @will.task.error.message, ptr @will.task.error.status, ptr @will.task.error.clone, ptr @will.task.error.drop, ptr @will.task.error.share, ptr @will.task.error.id]\n")
}
