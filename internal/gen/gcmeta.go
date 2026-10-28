package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

// hasGCMeta reports whether this batch carries the IR records that need the
// compiler-side GC graph. A legacy document without classes or GC decisions
// stays on the old lowering until the surface migration removes it.
// emitGCTypes writes the runtime frame and stack-map shapes before any
// function uses them.
func (g *generator) emitGCTypes() {
	if g.gcProgram == nil {
		return
	}
	g.out.WriteString("%willrt_gc_stack_slot = type { i32, i32, ptr }\n")
	g.out.WriteString("%willrt_gc_stack_map_data = type { ptr, ptr, i64 }\n")
	g.out.WriteString("%willrt_gc_frame = type { ptr, ptr, ptr }\n")
}

func (g *generator) hasGCMeta() bool {
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
	text, err := gcmeta.DescriptorBlock(program.Types())
	if err != nil {
		return err
	}
	g.out.WriteString(text)
	if len(g.gcInlineTypes) != 0 {
		names := make([]string, 0, len(g.gcInlineTypes))
		for name := range g.gcInlineTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			inline, err := gcmeta.DescriptorText(g.gcInlineTypes[name])
			if err != nil {
				return err
			}
			g.out.WriteString(inline)
		}
	}
	var classes []*gcmeta.Type
	for _, t := range program.Types() {
		if t.Kind == gcmeta.KindClass {
			classes = append(classes, t)
		}
	}
	if len(classes) != 0 {
		g.out.WriteString("declare i32 @willrt_gc_register_type(ptr)\n")
		g.out.WriteString("define internal void @willrt.gc.register_types() {\nentry:\n")
		for _, t := range classes {
			fmt.Fprintf(g.out, "  call i32 @willrt_gc_register_type(ptr %s)\n",
				gcmeta.DescriptorSymbol(t.Name))
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
			fmt.Fprintf(g.out, "{ i32 %d, i32 1, ptr %s }", slot, typeSym)
		}
		g.out.WriteString("]\n")
		fmt.Fprintf(g.out, "%s = private constant %%willrt_gc_stack_map_data { ptr @%s, ptr %s, i64 %d }\n",
			mapName, llvmSymbol(fn), slotName, len(records))
	}
	for _, frame := range append(append([]gcFrameInfo(nil), g.gcFrames...), g.gcTaskFrames...) {
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
			if i < len(frame.Types) {
				if t := program.Lookup(frame.Types[i]); t != nil {
					typeSym = gcmeta.DescriptorSymbol(t.Name)
				}
			}
			fmt.Fprintf(g.out, "{ i32 %d, i32 1, ptr %s }", offset, typeSym)
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
