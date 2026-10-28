/* GC contract shared by the compiler and the runtime.

   The compiler emits object layouts, stack maps, safepoints, and barriers
   against the sizes and constants in this header. The runtime implements the
   functions below in runtime/core/gc. Both sides include the same definitions;
   a compiler-side table that disagrees with this header is a build error. */

#ifndef WILLRT_WILLRT_GC_H
#define WILLRT_WILLRT_GC_H

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#if UINTPTR_MAX != UINT64_MAX
#error "the will GC contract requires a 64-bit target"
#endif

/* A class object starts with one 8-byte state word. The payload starts at
   offset 8 and has the alignment of the widest field, never below 8. The state
   word is runtime-owned; the compiler only needs the size and offset. */
#define WILLRT_GC_OBJECT_HEADER_SIZE 8u
#define WILLRT_GC_OBJECT_PAYLOAD_OFFSET 8u
#define WILLRT_GC_OBJECT_ALIGN 8u

typedef struct willrt_gc_header {
    _Atomic uint64_t state;
} willrt_gc_header;

_Static_assert(sizeof(willrt_gc_header) == WILLRT_GC_OBJECT_HEADER_SIZE,
               "the GC object header must stay one word");
_Static_assert(_Alignof(willrt_gc_header) == WILLRT_GC_OBJECT_ALIGN,
               "the GC object header must be 8-byte aligned");

/* References select one of four virtual-memory views in bits 34 and 35.
   Each view maps the same physical pages; the view address, not a tag stored
   beside the object, is the color. Object addresses therefore stay naturally
   aligned and page remapping can change a color without rewriting every
   reference. */
#define WILLRT_GC_VIEW_COUNT 4u
#define WILLRT_GC_COLOR_SHIFT 34u
#define WILLRT_GC_VIEW_STRIDE 0x400000000ull
#define WILLRT_GC_COLOR_MASK 0xC00000000ull
#define WILLRT_GC_POINTER_MASK (~(uintptr_t)WILLRT_GC_COLOR_MASK)

enum willrt_gc_color {
    WILLRT_GC_COLOR_YOUNG = 0,
    WILLRT_GC_COLOR_MARKED0 = 1,
    WILLRT_GC_COLOR_MARKED1 = 2,
    WILLRT_GC_COLOR_REMAPPED = 3
};

#define WILLRT_GC_COLOR_OF(ptr) \
    ((uint32_t)((((uintptr_t)(ptr)) & WILLRT_GC_COLOR_MASK) >> \
                WILLRT_GC_COLOR_SHIFT))
#define WILLRT_GC_TAG(ptr, color) \
    ((void*)(((uintptr_t)(ptr)) | \
             ((uintptr_t)(color) << WILLRT_GC_COLOR_SHIFT)))
#define WILLRT_GC_UNTAG(ptr) \
    ((void*)(((uintptr_t)(ptr)) & WILLRT_GC_POINTER_MASK))

/* Regions are fixed by the collector. A large or pinned object never moves;
   a young object may move, and an old object moves only when its region is
   selected for evacuation. */
enum willrt_gc_region {
    WILLRT_GC_REGION_YOUNG = 0,
    WILLRT_GC_REGION_OLD = 1,
    WILLRT_GC_REGION_LARGE = 2,
    WILLRT_GC_REGION_PINNED = 3
};

/* Safepoints and barriers use the words the decision record writes. */
enum willrt_gc_safepoint_kind {
    WILLRT_GC_SAFEPOINT_ALLOC = 0,
    WILLRT_GC_SAFEPOINT_AWAIT = 1,
    WILLRT_GC_SAFEPOINT_BACKEDGE = 2,
    WILLRT_GC_SAFEPOINT_CALL = 3
};

enum willrt_gc_barrier_kind {
    WILLRT_GC_BARRIER_READ = 0,
    WILLRT_GC_BARRIER_WRITE = 1,
    WILLRT_GC_BARRIER_SATB = 2,
    WILLRT_GC_BARRIER_CARD = 3
};

/* A type descriptor is static metadata. reference_offsets points to payload
   offsets of GC references, in ascending order, so the collector can scan an
   object without searching its fields. */
enum willrt_gc_type_flag {
    WILLRT_GC_TYPE_REFERENCES = 1u << 0,
    WILLRT_GC_TYPE_LARGE = 1u << 1,
    WILLRT_GC_TYPE_PINNED = 1u << 2
};

typedef struct willrt_gc_type {
    uint32_t size;
    uint32_t align;
    uint32_t flags;
    uint32_t reference_count;
    const uint32_t* reference_offsets;
} willrt_gc_type;

/* One precise stack slot. The slot is a byte offset from the frame base; a
   reference slot always holds a GC reference, while a pinned slot holds an
   object that must not move for the duration of the frame. */
enum willrt_gc_slot_flag {
    WILLRT_GC_SLOT_REFERENCE = 1u << 0,
    WILLRT_GC_SLOT_PINNED = 1u << 1
};

typedef struct willrt_gc_stack_slot {
    uint32_t slot;
    uint32_t flags;
    const willrt_gc_type* type;
} willrt_gc_stack_slot;

typedef struct willrt_gc_stack_map_data {
    const void* function;
    const willrt_gc_stack_slot* slots;
    size_t slot_count;
} willrt_gc_stack_map_data;

typedef struct willrt_gc_frame_map_data {
    const willrt_gc_stack_map_data* stack;
    size_t frame_size;
} willrt_gc_frame_map_data;

/* Root scanning is exact. The runtime calls scan(context, visit, visit_context);
   scan reports each root slot through visit(visit_context, slot). */
typedef void (*willrt_gc_root_visit)(void* context, void** slot);
typedef void (*willrt_gc_root_scan)(void* context,
                                    willrt_gc_root_visit visit,
                                    void* visit_context);

/* Allocation returns an untagged object pointer. The caller stores the color
   view required by the current barrier state. A null type means the object has
   no references. */
void* willrt_gc_alloc(size_t size, const willrt_gc_type* type);

/* read_barrier returns the current untagged address. write_barrier publishes a
   new reference and records the old value for SATB and remembered-set work. */
void* willrt_gc_read_barrier(const void* ref);
void willrt_gc_write_barrier(void** slot, void* value);

/* A safepoint reports the exact stack map of the current frame. A map may be
   null when the frame has no roots. */
void willrt_gc_safepoint(enum willrt_gc_safepoint_kind kind,
                         const willrt_gc_stack_map_data* map,
                         void* frame);

/* Static maps are registered once per module; asynchronous frames register
   their frame map and pass the current frame at each safepoint. */
void willrt_gc_stackmap(const willrt_gc_stack_map_data* map);
void willrt_gc_frame_map(const willrt_gc_frame_map_data* map);

/* Global and module roots. Registration is exact; there is no conservative
   scan of the C stack or of writable data. */
void willrt_gc_root_register(willrt_gc_root_scan scan, void* context);
void willrt_gc_root_unregister(willrt_gc_root_scan scan, void* context);

/* Pinning returns an untagged address that stays valid until the matching
   unpin. Pin depth is counted; a nested pin must be unpinned as many times. */
void* willrt_gc_pin(void* object);
void willrt_gc_unpin(void* object);

/* A handle is an opaque root for C. A C caller stores only the handle and
   recovers the object through willrt_gc_handle_get. */
typedef uint64_t willrt_gc_handle;

#define WILLRT_GC_HANDLE_NONE ((willrt_gc_handle)0)

willrt_gc_handle willrt_gc_handle_new(void* object);
void* willrt_gc_handle_get(willrt_gc_handle handle);
void willrt_gc_handle_release(willrt_gc_handle handle);

#endif
