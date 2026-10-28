package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

const classMainDoc = "#1 module path=\"m\"\n" +
	"#2 class parent=#1 text=\"Node\"\n" +
	"#3 field parent=#2 text=\"next\" type=\"Node\"\n" +
	"#4 field parent=#2 text=\"value\" type=\"i32\"\n" +
	"#5 func parent=#1 text=\"main\" type=\"i32\"\n" +
	"#6 type parent=#5 text=\"i32\" kind=scalar\n" +
	"#7 block parent=#5\n" +
	"#8 var parent=#7 text=\"n\" type=\"Node\"\n" +
	"#9 type parent=#8 text=\"Node\" kind=class to=#2\n" +
	"#10 new parent=#8 type=\"Node\"\n" +
	"#11 assign parent=#7 op=\"=\" target=#12 value=#15\n" +
	"#12 field parent=#11 text=\"value\" recv=#13\n" +
	"#13 name parent=#12 text=\"n\" to=#8\n" +
	"#14 type parent=#13 text=\"Node\" kind=class to=#2\n" +
	"#15 lit parent=#11 kind=int text=\"42\" type=\"i32\"\n" +
	"#16 return parent=#7 value=#17\n" +
	"#17 field parent=#16 text=\"value\" recv=#18\n" +
	"#18 name parent=#17 text=\"n\" to=#8\n" +
	"#19 type parent=#18 text=\"Node\" kind=class to=#2\n" +
	"#20 end\n"

const classDoc = "#1 module path=\"m\"\n" +
	"#2 class parent=#1 text=\"Node\"\n" +
	"#3 field parent=#2 text=\"next\" type=\"Node\"\n" +
	"#4 field parent=#2 text=\"value\" type=\"i64\"\n" +
	"#5 func parent=#1 text=\"run\" type=\"i64\"\n" +
	"#6 type parent=#5 text=\"i64\" kind=scalar\n" +
	"#7 block parent=#5\n" +
	"#8 var parent=#7 text=\"n\" type=\"Node\"\n" +
	"#9 type parent=#8 text=\"Node\" kind=class to=#2\n" +
	"#10 new parent=#8 type=\"Node\"\n" +
	"#11 assign parent=#7 op=\"=\" target=#12 value=#15\n" +
	"#12 field parent=#11 text=\"value\" recv=#13\n" +
	"#13 name parent=#12 text=\"n\" to=#8\n" +
	"#14 type parent=#13 text=\"Node\" kind=class to=#2\n" +
	"#15 lit parent=#11 kind=int text=\"42\" type=\"i64\"\n" +
	"#16 return parent=#7 value=#17\n" +
	"#17 field parent=#16 text=\"value\" recv=#18\n" +
	"#18 name parent=#17 text=\"n\" to=#8\n" +
	"#19 type parent=#18 text=\"Node\" kind=class to=#2\n" +
	"#20 end\n"

const classMethodDoc = "#1 module path=\"m\"\n" +
	"#2 class parent=#1 text=\"Node\"\n" +
	"#3 field parent=#2 text=\"value\" type=\"i32\"\n" +
	"#4 func parent=#1 text=\"get\" type=\"i32\"\n" +
	"#5 receiver parent=#4 text=\"self\" type=\"Node\"\n" +
	"#6 type parent=#5 text=\"Node\" kind=class to=#2\n" +
	"#7 block parent=#4\n" +
	"#8 return parent=#7 value=#9\n" +
	"#9 field parent=#8 text=\"value\" recv=#10\n" +
	"#10 name parent=#9 text=\"self\" to=#5\n" +
	"#11 type parent=#10 text=\"Node\" kind=class to=#2\n" +
	"#12 func parent=#1 text=\"main\" type=\"i32\"\n" +
	"#13 type parent=#12 text=\"i32\" kind=scalar\n" +
	"#14 block parent=#12\n" +
	"#15 var parent=#14 text=\"n\" type=\"Node\"\n" +
	"#16 type parent=#15 text=\"Node\" kind=class to=#2\n" +
	"#17 new parent=#15 type=\"Node\"\n" +
	"#18 return parent=#14 value=#19\n" +
	"#19 mcall parent=#18 text=\"get\" recv=#20\n" +
	"#20 name parent=#19 text=\"n\" to=#15\n" +
	"#21 type parent=#20 text=\"Node\" kind=class to=#2\n" +
	"#22 type parent=#4 text=\"i32\" kind=scalar\n" +
	"#23 end\n"

func TestClassMethodCall(t *testing.T) {
	doc, err := willc.Read([]byte(classMethodDoc))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`define hidden i32 @m.Node.get(ptr %self)`,
		`call i32 @m.Node.get(ptr`,
		`call ptr @willrt_gc_read_barrier_slot(ptr %v.n`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestClassChannelUsesElementDescriptor(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 class parent=#1 text=\"Node\"\n" +
		"#3 field parent=#2 text=\"value\" type=\"i32\"\n" +
		"#4 func parent=#1 text=\"run\"\n" +
		"#5 block parent=#4\n" +
		"#6 var parent=#5 text=\"ch\" type=\"chan<Node>\"\n" +
		"#7 type parent=#6 text=\"chan<Node>\" kind=chan arg=#8\n" +
		"#8 type parent=#7 text=\"Node\" kind=class to=#2\n" +
		"#9 channew parent=#6 type=\"chan<Node>\" cap=#10\n" +
		"#10 lit parent=#9 kind=int text=\"4\" type=\"usize\"\n" +
		"#11 exprstmt parent=#5 expr=#12\n" +
		"#12 name parent=#11 text=\"ch\" to=#6\n" +
		"#13 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`call ptr @willrt_chan_new_gc(i64 8,`,
		`ptr @"willrt.gc.type.value.Node")`,
		`@"willrt.gc.type.value.Node" = private constant`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
}

func TestClassInitConstructor(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 class parent=#1 text=\"Node\"\n" +
		"#3 field parent=#2 text=\"value\" type=\"i32\"\n" +
		"#4 func parent=#1 text=\"init\"\n" +
		"#5 receiver parent=#4 text=\"self\" type=\"Node\"\n" +
		"#6 type parent=#5 text=\"Node\" kind=class to=#2\n" +
		"#7 param parent=#4 text=\"v\" type=\"i32\"\n" +
		"#8 type parent=#7 text=\"i32\" kind=scalar\n" +
		"#9 block parent=#4\n" +
		"#10 assign parent=#9 op=\"=\" target=#11 value=#14\n" +
		"#11 field parent=#10 text=\"value\" recv=#12\n" +
		"#12 name parent=#11 text=\"self\" to=#5\n" +
		"#13 type parent=#12 text=\"Node\" kind=class to=#2\n" +
		"#14 name parent=#10 text=\"v\" to=#7\n" +
		"#15 func parent=#1 text=\"main\" type=\"i32\"\n" +
		"#16 type parent=#15 text=\"i32\" kind=scalar\n" +
		"#17 block parent=#15\n" +
		"#18 var parent=#17 text=\"n\" type=\"Node\"\n" +
		"#19 type parent=#18 text=\"Node\" kind=class to=#2\n" +
		"#20 new parent=#18 type=\"Node\" arg=#23\n" +
		"#21 assign parent=#17 op=\"=\" target=#22 value=#25\n" +
		"#22 field parent=#21 text=\"value\" recv=#24\n" +
		"#23 lit parent=#20 kind=int text=\"7\" type=\"i32\"\n" +
		"#24 name parent=#22 text=\"n\" to=#18\n" +
		"#25 lit parent=#21 kind=int text=\"7\" type=\"i32\"\n" +
		"#26 return parent=#17 value=#27\n" +
		"#27 field parent=#26 text=\"value\" recv=#28\n" +
		"#28 name parent=#27 text=\"n\" to=#18\n" +
		"#29 type parent=#28 text=\"Node\" kind=class to=#2\n" +
		"#30 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	if !strings.Contains(text, `call void @m.Node.init(ptr`) {
		t.Fatalf("constructor does not call init:\n%s", text)
	}
	if !strings.Contains(text, `@m.Node.init(ptr %self, i32 %v)`) {
		t.Fatalf("init signature is not the receiver form:\n%s", text)
	}
}

func TestClassGlobalRoot(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 class parent=#1 text=\"Node\"\n" +
		"#3 field parent=#2 text=\"value\" type=\"i32\"\n" +
		"#4 var parent=#1 text=\"g\" type=\"Node\"\n" +
		"#5 type parent=#4 text=\"Node\" kind=class to=#2\n" +
		"#6 new parent=#4 type=\"Node\"\n" +
		"#7 func parent=#1 text=\"main\" type=\"i32\"\n" +
		"#8 type parent=#7 text=\"i32\" kind=scalar\n" +
		"#9 block parent=#7\n" +
		"#10 return parent=#9 value=#11\n" +
		"#11 lit parent=#10 kind=int text=\"0\" type=\"i32\"\n" +
		"#12 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`define internal void @willrt.gc.scan_globals`,
		`call void @willrt_gc_root_register(ptr @willrt.gc.scan_globals, ptr null)`,
		`call void @willrt_gc_write_barrier(ptr @m.g, ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
}

const classCycleDoc = "#1 module path=\"m\"\n" +
	"#2 class parent=#1 text=\"Node\"\n" +
	"#3 field parent=#2 text=\"next\" type=\"Node\"\n" +
	"#4 field parent=#2 text=\"value\" type=\"i32\"\n" +
	"#5 func parent=#1 text=\"main\" type=\"i32\"\n" +
	"#6 type parent=#5 text=\"i32\" kind=scalar\n" +
	"#7 block parent=#5\n" +
	"#8 var parent=#7 text=\"n\" type=\"Node\"\n" +
	"#9 type parent=#8 text=\"Node\" kind=class to=#2\n" +
	"#10 new parent=#8 type=\"Node\"\n" +
	"#11 assign parent=#7 op=\"=\" target=#12 value=#15\n" +
	"#12 field parent=#11 text=\"value\" recv=#13\n" +
	"#13 name parent=#12 text=\"n\" to=#8\n" +
	"#14 type parent=#13 text=\"Node\" kind=class to=#2\n" +
	"#15 lit parent=#11 kind=int text=\"42\" type=\"i32\"\n" +
	"#16 assign parent=#7 op=\"=\" target=#17 value=#20\n" +
	"#17 field parent=#16 text=\"next\" recv=#18\n" +
	"#18 name parent=#17 text=\"n\" to=#8\n" +
	"#19 type parent=#18 text=\"Node\" kind=class to=#2\n" +
	"#20 name parent=#16 text=\"n\" to=#8\n" +
	"#21 type parent=#20 text=\"Node\" kind=class to=#2\n" +
	"#22 var parent=#7 text=\"m\" type=\"Node\"\n" +
	"#23 type parent=#22 text=\"Node\" kind=class to=#2\n" +
	"#24 new parent=#22 type=\"Node\"\n" +
	"#25 return parent=#7 value=#26\n" +
	"#26 field parent=#25 text=\"value\" recv=#27\n" +
	"#27 name parent=#26 text=\"n\" to=#8\n" +
	"#28 type parent=#27 text=\"Node\" kind=class to=#2\n" +
	"#29 end\n"

func TestClassCycleFieldRuns(t *testing.T) {
	doc, err := willc.Read([]byte(classCycleDoc))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is not on PATH")
	}
	dir := t.TempDir()
	irPath := filepath.Join(dir, "cycle.ll")
	if err := os.WriteFile(irPath, ir, 0o644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	exe := filepath.Join(dir, "cycle")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-O2", "-I", filepath.Join("..", "..", "runtime", "include"),
		"-I", filepath.Join("..", "..", "runtime", "core", "gc", "src"),
		"-o", exe, irPath}
	for _, name := range []string{"types.c", "region.c", "chunks.c", "heap.c", "object.c",
		"roots.c", "safepoint.c", "barrier.c", "mark.c", "evac.c", "cycle.c", "pacer.c"} {
		args = append(args, filepath.Join("..", "..", "runtime", "core", "gc", "src", name))
	}
	args = append(args,
		filepath.Join("..", "..", "runtime", "core", "mem", "src", "alloc.c"),
		filepath.Join("..", "..", "runtime", "core", "startup", "src", "startup.c"),
		filepath.Join("..", "..", "runtime", "platform", "win.c"))
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	out, err := exec.Command(clang, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("link: %v\n%s\n%s", err, out, ir)
	}
	run := exec.Command(exe)
	run.Env = append(os.Environ(), "WILL_GC_MEMLIMIT=1")
	out, err = run.CombinedOutput()
	if err == nil {
		t.Fatalf("cycle program exited zero, want 42:\n%s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
		t.Fatalf("cycle program: %v\n%s", err, out)
	}
}

func TestClassInterfaceUsesDirectReference(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 interface parent=#1 text=\"Any\"\n" +
		"#3 class parent=#1 text=\"Node\"\n" +
		"#4 field parent=#3 text=\"value\" type=\"i32\"\n" +
		"#5 func parent=#1 text=\"run\" type=\"i32\"\n" +
		"#6 type parent=#5 text=\"i32\" kind=scalar\n" +
		"#7 block parent=#5\n" +
		"#8 var parent=#7 text=\"x\" type=\"Any\"\n" +
		"#9 type parent=#8 text=\"Any\" kind=named to=#2\n" +
		"#10 var parent=#7 text=\"n\" type=\"Node\"\n" +
		"#11 type parent=#10 text=\"Node\" kind=class to=#3\n" +
		"#12 new parent=#10 type=\"Node\"\n" +
		"#13 assign parent=#7 op=\"=\" target=#14 value=#16\n" +
		"#14 name parent=#13 text=\"x\" to=#8\n" +
		"#15 type parent=#14 text=\"Any\" kind=named to=#2\n" +
		"#16 name parent=#13 text=\"n\" to=#10\n" +
		"#17 type parent=#16 text=\"Node\" kind=class to=#3\n" +
		"#18 return parent=#7 value=#19\n" +
		"#19 lit parent=#18 kind=int text=\"7\" type=\"i32\"\n" +
		"#20 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	if !strings.Contains(text, `@"willrt.gc.stack.m.run"`) {
		t.Fatalf("class interface root map is missing:\n%s", text)
	}
	if strings.Contains(text, `call void @willrt_free(ptr %data)`) {
		t.Fatalf("class interface drop still frees a collector object:\n%s", text)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is not on PATH")
	}
	path := filepath.Join(t.TempDir(), "iface.ll")
	if err := os.WriteFile(path, ir, 0o644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	if out, err := exec.Command(clang, "-x", "ir", "-c", "-o",
		filepath.Join(t.TempDir(), "iface.o"), path).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s\n%s", err, out, text)
	}
}

func TestClassGlobalInitUsesRoot(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 class parent=#1 text=\"Node\"\n" +
		"#3 field parent=#2 text=\"value\" type=\"i32\"\n" +
		"#4 func parent=#1 text=\"init\"\n" +
		"#5 receiver parent=#4 text=\"self\" type=\"Node\"\n" +
		"#6 type parent=#5 text=\"Node\" kind=class to=#2\n" +
		"#7 param parent=#4 text=\"v\" type=\"i32\"\n" +
		"#8 type parent=#7 text=\"i32\" kind=scalar\n" +
		"#9 block parent=#4\n" +
		"#10 assign parent=#9 op=\"=\" target=#11 value=#14\n" +
		"#11 field parent=#10 text=\"value\" recv=#12\n" +
		"#12 name parent=#11 text=\"self\" to=#5\n" +
		"#13 type parent=#12 text=\"Node\" kind=class to=#2\n" +
		"#14 name parent=#10 text=\"v\" to=#7\n" +
		"#15 var parent=#1 text=\"g\" type=\"Node\"\n" +
		"#16 type parent=#15 text=\"Node\" kind=class to=#2\n" +
		"#17 new parent=#15 type=\"Node\" arg=#20\n" +
		"#18 func parent=#1 text=\"main\" type=\"i32\"\n" +
		"#19 type parent=#18 text=\"i32\" kind=scalar\n" +
		"#20 lit parent=#17 kind=int text=\"7\" type=\"i32\"\n" +
		"#21 block parent=#18\n" +
		"#22 return parent=#21 value=#23\n" +
		"#23 field parent=#22 text=\"value\" recv=#24\n" +
		"#24 name parent=#23 text=\"g\" to=#15\n" +
		"#25 type parent=#24 text=\"Node\" kind=class to=#2\n" +
		"#26 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`call void @willrt_gc_root_register(ptr @willrt.gc.scan_globals, ptr null)`,
		`call void @m.Node.init(ptr`,
		`call void @willrt_gc_write_barrier(ptr @m.g, ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is not on PATH")
	}
	dir := t.TempDir()
	irPath := filepath.Join(dir, "global.ll")
	if err := os.WriteFile(irPath, ir, 0o644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	exe := filepath.Join(dir, "global")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-O2", "-I", filepath.Join("..", "..", "runtime", "include"),
		"-I", filepath.Join("..", "..", "runtime", "core", "gc", "src"),
		"-o", exe, irPath}
	for _, name := range []string{"types.c", "region.c", "chunks.c", "heap.c", "object.c",
		"roots.c", "safepoint.c", "barrier.c", "mark.c", "evac.c", "cycle.c", "pacer.c"} {
		args = append(args, filepath.Join("..", "..", "runtime", "core", "gc", "src", name))
	}
	args = append(args,
		filepath.Join("..", "..", "runtime", "core", "mem", "src", "alloc.c"),
		filepath.Join("..", "..", "runtime", "core", "startup", "src", "startup.c"),
		filepath.Join("..", "..", "runtime", "platform", "win.c"))
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(clang, args...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s\n%s", err, out, ir)
	}
	run := exec.Command(exe)
	run.Env = append(os.Environ(), "WILL_GC_MEMLIMIT=1")
	out, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("global class program exited zero, want 7:\n%s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
		t.Fatalf("global class program: %v\n%s", err, out)
	}
}

func TestClassProgramRuns(t *testing.T) {
	doc, err := willc.Read([]byte(classMainDoc))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "main")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is not on PATH")
	}
	dir := t.TempDir()
	irPath := filepath.Join(dir, "main.ll")
	if err := os.WriteFile(irPath, ir, 0o644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	exe := filepath.Join(dir, "class-main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-O2", "-I", filepath.Join("..", "..", "runtime", "include"),
		"-I", filepath.Join("..", "..", "runtime", "core", "gc", "src"),
		"-o", exe, irPath}
	for _, name := range []string{"types.c", "region.c", "chunks.c", "heap.c", "object.c",
		"roots.c", "safepoint.c", "barrier.c", "mark.c", "evac.c", "cycle.c", "pacer.c"} {
		args = append(args, filepath.Join("..", "..", "runtime", "core", "gc", "src", name))
	}
	args = append(args,
		filepath.Join("..", "..", "runtime", "core", "mem", "src", "alloc.c"),
		filepath.Join("..", "..", "runtime", "core", "startup", "src", "startup.c"),
		filepath.Join("..", "..", "runtime", "platform", "win.c"))
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	out, err := exec.Command(clang, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("link: %v\n%s\n%s", err, out, ir)
	}
	run := exec.Command(exe)
	run.Env = append(os.Environ(), "WILL_GC_MEMLIMIT=1")
	out, err = run.CombinedOutput()
	if err == nil {
		t.Fatalf("class program exited zero, want 42:\n%s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
		t.Fatalf("class program: %v\n%s", err, out)
	}
}

func TestClassAllocationAndFieldBarriers(t *testing.T) {
	doc, err := willc.Read([]byte(classDoc))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		"%class.m.Node = type { ptr, i64 }",
		"call ptr @willrt_gc_alloc(i64 16, ptr @\"willrt.gc.type.m.Node\")",
		"call void @willrt_gc_write_barrier(ptr %v.n",
		"call ptr @willrt_gc_read_barrier_slot(ptr %v.n",
		"call void @willrt_gc_frame_enter",
		"call void @willrt_gc_frame_leave",
		"@\"willrt.gc.stack.m.run\" = private constant %willrt_gc_stack_map_data",
		"@llvm.global_ctors",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang is not on PATH")
	}
	path := filepath.Join(t.TempDir(), "class.ll")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	out, err := exec.Command(clang, "-x", "ir", "-c", "-o",
		filepath.Join(t.TempDir(), "class.o"), path).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s\n%s", err, out, text)
	}
}
