package gc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headerMacros reads the object-size constants from the shared C header. The
// test compares the compiler table with the runtime header, so a change on one
// side cannot leave the other stale.
func headerMacros(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "runtime", "include", "willrt_gc.h")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read GC header: %v", err)
	}
	macros := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "#define" ||
			!strings.HasPrefix(fields[1], "WILLRT_GC_") {
			continue
		}
		macros[fields[1]] = fields[2]
	}
	return macros
}

// TestSharedHeaderMatchesCompilerConstants checks every numeric constant the
// compiler needs from willrt_gc.h.
func TestSharedHeaderMatchesCompilerConstants(t *testing.T) {
	macros := headerMacros(t)
	want := map[string]string{
		"WILLRT_GC_OBJECT_HEADER_SIZE":    "8u",
		"WILLRT_GC_OBJECT_PAYLOAD_OFFSET": "8u",
		"WILLRT_GC_OBJECT_ALIGN":          "8u",
		"WILLRT_GC_COLOR_MASK":            "0xC00000000ull",
		"WILLRT_GC_COLOR_SHIFT":           "34u",
		"WILLRT_GC_VIEW_COUNT":            "4u",
		"WILLRT_GC_VIEW_STRIDE":           "0x400000000ull",
	}
	for name, value := range want {
		got, ok := macros[name]
		if !ok {
			t.Errorf("header lacks %s", name)
			continue
		}
		if got != value {
			t.Errorf("%s = %s, want %s", name, got, value)
		}
	}
	if ObjectHeaderSize != 8 || ObjectPayloadOffset != 8 || ObjectAlign != 8 {
		t.Fatalf("compiler object layout = %d/%d/%d", ObjectHeaderSize,
			ObjectPayloadOffset, ObjectAlign)
	}
	if ColorMask != 3<<ColorShift {
		t.Fatalf("compiler color mask = %#x", ColorMask)
	}
	if ViewStride != 1<<ColorShift || ViewCount != 4 {
		t.Fatalf("compiler view layout = %d/%d", ViewStride, ViewCount)
	}
	if ColorYoung != 0 || ColorMarked0 != 1 || ColorMarked1 != 2 || ColorRemapped != 3 {
		t.Fatalf("compiler color order = %d/%d/%d/%d", ColorYoung,
			ColorMarked0, ColorMarked1, ColorRemapped)
	}
	if RegionYoung != 0 || RegionOld != 1 || RegionLarge != 2 || RegionPinned != 3 {
		t.Fatalf("compiler region order = %d/%d/%d/%d", RegionYoung, RegionOld,
			RegionLarge, RegionPinned)
	}
	if SafepointAlloc != 0 || SafepointAwait != 1 || SafepointBackedge != 2 ||
		SafepointCall != 3 {
		t.Fatalf("compiler safepoint order = %d/%d/%d/%d", SafepointAlloc,
			SafepointAwait, SafepointBackedge, SafepointCall)
	}
	if BarrierRead != 0 || BarrierWrite != 1 || BarrierSATB != 2 || BarrierCard != 3 {
		t.Fatalf("compiler barrier order = %d/%d/%d/%d", BarrierRead,
			BarrierWrite, BarrierSATB, BarrierCard)
	}
}

// TestSharedHeaderNamesContractStates checks the enum and type names that the
// decision record and lowered code refer to.
func TestSharedHeaderNamesContractStates(t *testing.T) {
	path := filepath.Join("..", "..", "runtime", "include", "willrt_gc.h")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read GC header: %v", err)
	}
	text := string(data)
	for _, name := range []string{
		"WILLRT_GC_COLOR_YOUNG",
		"WILLRT_GC_COLOR_MARKED0",
		"WILLRT_GC_COLOR_MARKED1",
		"WILLRT_GC_COLOR_REMAPPED",
		"WILLRT_GC_REGION_YOUNG",
		"WILLRT_GC_REGION_OLD",
		"WILLRT_GC_REGION_LARGE",
		"WILLRT_GC_REGION_PINNED",
		"WILLRT_GC_SAFEPOINT_ALLOC",
		"WILLRT_GC_SAFEPOINT_AWAIT",
		"WILLRT_GC_SAFEPOINT_BACKEDGE",
		"WILLRT_GC_SAFEPOINT_CALL",
		"WILLRT_GC_BARRIER_READ",
		"WILLRT_GC_BARRIER_WRITE",
		"WILLRT_GC_BARRIER_SATB",
		"WILLRT_GC_BARRIER_CARD",
		"willrt_gc_stack_map_data",
		"willrt_gc_frame_map_data",
		"willrt_gc_handle",
	} {
		if !strings.Contains(text, name) {
			t.Errorf("header lacks %s", name)
		}
	}
}
