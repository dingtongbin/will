package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// hasGCMeta reports whether this batch carries the IR records that need the
// compiler-side GC graph. A legacy document without classes or GC decisions
// stays on the old lowering until the surface migration removes it.
// emitGCTypes writes the runtime frame and stack-map shapes before any
// function uses them.
func (g *generator) emitGCTypes() {
	// These shapes are compiler ABI, not per-program metadata. Emit them
	// unconditionally: a program can build its GC graph lazily while lowering
	// a later function, after this header has already been written.
	g.out.WriteString("%willrt_gc_stack_slot = type { i32, i32, ptr }\n")
	g.out.WriteString("%willrt_gc_stack_map_data = type { ptr, ptr, i64 }\n")
	g.out.WriteString("%willrt_gc_frame = type { ptr, ptr, ptr }\n")
}

func (g *generator) hasGCMeta() bool {
	if g.needGCMeta || g.usesGCContainers() {
		return true
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			switch r.Kind {
			case "class", "stackmap", "barrier", "alloc", "safepoint":
				return true
			}
		}
	}
	return false
}

func (g *generator) usesGCContainers() bool {
	for _, d := range g.docs {
		for _, r := range d.recs {
			for _, f := range r.Fields {
				if f.Value.Kind != willc.TextValue {
					continue
				}
				if strings.Contains(f.Value.Text, "Array<") ||
					strings.Contains(f.Value.Text, "Map<") ||
					strings.Contains(f.Value.Text, "Set<") {
					return true
				}
			}
		}
	}
	return false
}

func (g *generator) rawDocs() []*willc.Doc {
	out := make([]*willc.Doc, 0, len(g.docs))
	for _, d := range g.docs {
		out = append(out, d.raw)
	}
	return out
}

// emitGCMetadata writes descriptors and stack maps after the functions. The
// descriptors are static metadata; the stack maps name the function symbol and
// the type descriptor so a frame scan never walks a type at runtime.
func (g *generator) emitGCMetadata() error {
	if !g.hasGCMeta() {
		return nil
	}
	program := g.gcProgram
	if program == nil {
		var err error
		program, err = gcmeta.Build(g.rawDocs())
		if err != nil {
			return err
		}
		g.gcProgram = program
	}
	var classes []*gcmeta.Type
	for _, t := range program.Types() {
		if t.Kind != gcmeta.KindClass {
			continue
		}
		if g.packageMode && packageOfQualified(t.Name) != g.pkg {
			continue
		}
		classes = append(classes, t)
	}
	for _, t := range program.InlineTypes() {
		if g.gcInlineTypes[t.Name] == nil {
			g.gcInlineTypes[t.Name] = t
		}
	}
	inlineNames := make([]string, 0, len(g.gcInlineTypes))
	for name := range g.gcInlineTypes {
		inlineNames = append(inlineNames, name)
	}
	sort.Strings(inlineNames)
	nextID := uint32(len(classes) + 1)
	var inlineTypes []*gcmeta.Type
	for _, name := range inlineNames {
		inline := g.gcInlineTypes[name]
		// A named declaration already owns its descriptor and ID assignment.
		if program.Lookup(name) != nil {
			continue
		}
		// An interface box or task result still needs a descriptor when the
		// inline value has no references: the allocator validates size and
		// alignment even though the scanner has nothing to visit.
		if nextID > gcmeta.TypeIDMax {
			return fmt.Errorf("GC type id table exceeds %d", gcmeta.TypeIDMax)
		}
		inline.ID = nextID
		nextID++
		inlineTypes = append(inlineTypes, inline)
	}
	var descriptors []*gcmeta.Type
	for _, t := range program.Types() {
		// Every package object may name a class declared in another package in
		// a stack map. Emit the descriptor as linkonce_odr so the linker keeps
		// one definition while each object can refer to it.
		descriptors = append(descriptors, t)
	}
	text, err := gcmeta.DescriptorBlock(descriptors)
	if err != nil {
		return err
	}
	if g.packageMode {
		text = strings.ReplaceAll(text, "private constant", "linkonce_odr constant")
	}
	g.out.WriteString(text)
	for _, inline := range inlineTypes {
		inlineText, err := gcmeta.DescriptorText(inline)
		if err != nil {
			return err
		}
		g.out.WriteString(inlineText)
	}
	if len(g.gcGlobalRoots) != 0 {
		g.out.WriteString("define internal void @willrt.gc.scan_globals(ptr %context, ptr %visit, ptr %visit_context) {\nentry:\n")
		for _, root := range g.gcGlobalRoots {
			if root.offset == 0 {
				fmt.Fprintf(g.out, "  call void %%visit(ptr %%visit_context, ptr @%s)\n",
					llvmSymbol(root.sym))
			} else {
				slot := g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr @%s, i64 %d\n",
					slot, llvmSymbol(root.sym), root.offset)
				fmt.Fprintf(g.out, "  call void %%visit(ptr %%visit_context, ptr %s)\n",
					slot)
			}
		}
		g.out.WriteString("  ret void\n}\n")
	}
	registered := append(append([]*gcmeta.Type(nil), classes...), inlineTypes...)
	if len(registered) != 0 {
		g.out.WriteString("declare i32 @willrt_gc_register_type(ptr)\n")
		g.out.WriteString("define internal void @willrt.gc.register_types() {\nentry:\n")
		for _, t := range registered {
			fmt.Fprintf(g.out, "  call i32 @willrt_gc_register_type(ptr %s)\n",
				gcmeta.DescriptorSymbol(t.Name))
		}
		if len(g.gcGlobalRoots) != 0 {
			g.out.WriteString("  call void @willrt_gc_root_register(ptr @willrt.gc.scan_globals, ptr null)\n")
		}
		g.out.WriteString("  ret void\n}\n")
		g.out.WriteString("@llvm.global_ctors = appending global [1 x { i32, ptr, ptr }] " +
			"[{ i32, ptr, ptr } { i32 65535, ptr @willrt.gc.register_types, ptr null }]\n")
	}

	maps := map[string][]willc.Record{}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "stackmap" {
				continue
			}
			fn := fieldText(r, "func")
			if fn == "" {
				return fmt.Errorf("stackmap #%d has no function", r.ID)
			}
			if g.packageMode && packageOfQualified(fn) != g.pkg {
				continue
			}
			maps[fn] = append(maps[fn], r)
		}
	}
	names := make([]string, 0, len(maps))
	for name := range maps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, fn := range names {
		records := maps[fn]
		sortByID(records)
		pkg := packageOfQualified(fn)
		slotName := gcStackSlotsSymbol(fn)
		mapName := gcStackMapSymbol(fn)
		fmt.Fprintf(g.out, "%s = private constant [%d x %%willrt_gc_stack_slot] [",
			slotName, len(records))
		for i, r := range records {
			if i != 0 {
				g.out.WriteString(", ")
			}
			slot := intField(r, "slot")
			typeText := fieldText(r, "type")
			qualified, ok := program.TypeText(pkg, typeText)
			typeSym := "null"
			if ok {
				typeSym = gcmeta.DescriptorSymbol(qualified)
			}
			fmt.Fprintf(g.out, "%%willrt_gc_stack_slot { i32 %d, i32 1, ptr %s }", slot, typeSym)
		}
		g.out.WriteString("]\n")
		fmt.Fprintf(g.out, "%s = private constant %%willrt_gc_stack_map_data { ptr @%s, ptr %s, i64 %d }\n",
			mapName, llvmSymbol(fn), slotName, len(records))
	}
	for _, frame := range append(append([]gcFrameInfo(nil), g.gcFrames...), g.gcTaskFrames...) {
		for i, offset := range frame.Slots {
			typ := "raw"
			if i < len(frame.Types) && frame.Types[i] != "" {
				typ = frame.Types[i]
			}
			g.gcDecisions = append(g.gcDecisions,
				decisions.StackMap(frame.Func, offset, typ))
		}
		if _, exists := maps[frame.Func]; exists {
			continue
		}
		slotName := gcStackSlotsSymbol(frame.Func)
		mapName := gcStackMapSymbol(frame.Func)
		fmt.Fprintf(g.out, "%s = private constant [%d x %%willrt_gc_stack_slot] [",
			slotName, len(frame.Slots))
		for i, offset := range frame.Slots {
			if i != 0 {
				g.out.WriteString(", ")
			}
			typeSym := "null"
			if i < len(frame.Types) && frame.Types[i] != "" {
				typeSym = gcmeta.DescriptorSymbol(frame.Types[i])
			}
			flags := uint32(1)
			if i < len(frame.Flags) {
				flags = frame.Flags[i]
			}
			fmt.Fprintf(g.out, "%%willrt_gc_stack_slot { i32 %d, i32 %d, ptr %s }",
				offset, flags, typeSym)
		}
		g.out.WriteString("]\n")
		fmt.Fprintf(g.out, "%s = private constant %%willrt_gc_stack_map_data { ptr @%s, ptr %s, i64 %d }\n",
			mapName, llvmSymbol(frame.Func), slotName, len(frame.Slots))
	}
	return nil
}

func escapeGCName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `"`, `\"`)
}

func gcStackMapSymbol(fn string) string {
	return `@"willrt.gc.stack.` + escapeGCName(fn) + `"`
}

func gcStackSlotsSymbol(fn string) string {
	return `@"willrt.gc.stack.slots.` + escapeGCName(fn) + `"`
}

func intField(r willc.Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.IntValue {
			return f.Value.Int
		}
	}
	return 0
}
