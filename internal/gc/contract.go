// Package gc holds the compiler-side constants shared with willrt_gc.h. The
// header is the runtime side of the contract; this package is the side the
// compiler and decision records use. Contract tests compare both.
package gc

// Object layout constants. A class object has one state word followed by its
// payload; a struct has no GC header.
const (
	// ObjectHeaderSize is the size of the state word in bytes.
	ObjectHeaderSize = 8
	// ObjectPayloadOffset is the first payload byte.
	ObjectPayloadOffset = 8
	// ObjectAlign is the required alignment of every GC object.
	ObjectAlign = 8
)

// Color is the low-bit state carried by a GC reference.
type Color uint8

const (
	// ColorYoung is a reference to a young-generation object.
	ColorYoung Color = iota
	// ColorMarked0 is a reference observed during mark phase zero.
	ColorMarked0
	// ColorMarked1 is a reference observed during mark phase one.
	ColorMarked1
	// ColorRemapped is a reference whose address view is current.
	ColorRemapped
)

// ColorMask selects the color bits of a tagged reference.
const ColorMask = 3

// Region selects the collector area that owns an object.
type Region uint8

const (
	// RegionYoung holds Eden and survivor objects.
	RegionYoung Region = iota
	// RegionOld holds promoted objects.
	RegionOld
	// RegionLarge holds non-moving large objects.
	RegionLarge
	// RegionPinned holds objects that C may access directly.
	RegionPinned
)

// SafepointKind names the reason a stack map is scanned.
type SafepointKind uint8

const (
	// SafepointAlloc is an allocation slow path.
	SafepointAlloc SafepointKind = iota
	// SafepointAwait is a task wait point.
	SafepointAwait
	// SafepointBackedge is a loop back edge.
	SafepointBackedge
	// SafepointCall is a function entry or exit.
	SafepointCall
)

// BarrierKind names the barrier a lowered reference access needs.
type BarrierKind uint8

const (
	// BarrierRead is a read barrier.
	BarrierRead BarrierKind = iota
	// BarrierWrite is a write barrier.
	BarrierWrite
	// BarrierSATB is a snapshot-at-the-beginning barrier.
	BarrierSATB
	// BarrierCard is a generational card barrier.
	BarrierCard
)

// Type flags describe the static metadata of one allocated type.
const (
	// TypeReferences marks a type with at least one GC reference field.
	TypeReferences = 1 << 0
	// TypeLarge marks a type allocated in the large-object region.
	TypeLarge = 1 << 1
	// TypePinned marks a type allocated in the pinned region.
	TypePinned = 1 << 2
)

// Slot flags describe one precise stack-map entry.
const (
	// SlotReference marks a slot that holds a GC reference.
	SlotReference = 1 << 0
	// SlotPinned marks a slot whose object must not move.
	SlotPinned = 1 << 1
)
