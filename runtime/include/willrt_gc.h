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
   beside the object, is the color. Object addresses stay naturally aligned;
   relocation publishes a forwarding address and the load barrier self-heals
   the reference view. */
#define WILLRT_GC_VIEW_COUNT 4u
#define WILLRT_GC_COLOR_SHIFT 36u
#define WILLRT_GC_VIEW_STRIDE 0x1000000000ull
#define WILLRT_GC_COLOR_MASK 0x3000000000ull
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

/* The state word carries mark color, age, flags, pin depth, region id, type id,
   and the forwarded bit. A forwarded object stores the untagged new address in
   the low 48 bits; type and region are recovered from the new object. */
#define WILLRT_GC_STATE_MARK_MASK 0x3ull
#define WILLRT_GC_STATE_MARK_SHIFT 0u
#define WILLRT_GC_STATE_AGE_MASK 0xcull
#define WILLRT_GC_STATE_AGE_SHIFT 2u
#define WILLRT_GC_STATE_AGE_MAX 3u
#define WILLRT_GC_STATE_PINNED (1ull << 4)
#define WILLRT_GC_STATE_LARGE (1ull << 5)
#define WILLRT_GC_STATE_PINNED_REGION (1ull << 6)
#define WILLRT_GC_STATE_BUSY (1ull << 7)
#define WILLRT_GC_STATE_PIN_SHIFT 8u
#define WILLRT_GC_STATE_PIN_MASK (0xffffull << WILLRT_GC_STATE_PIN_SHIFT)
#define WILLRT_GC_STATE_REGION_SHIFT 24u
#define WILLRT_GC_STATE_REGION_MASK (0xffffull << WILLRT_GC_STATE_REGION_SHIFT)
#define WILLRT_GC_STATE_TYPE_SHIFT 40u
#define WILLRT_GC_STATE_TYPE_MASK (0x7fffffull << WILLRT_GC_STATE_TYPE_SHIFT)
#define WILLRT_GC_STATE_TYPE_MAX WILLRT_GC_TYPE_ID_MAX
#define WILLRT_GC_STATE_FORWARDED (1ull << 63)
#define WILLRT_GC_STATE_FORWARD_MASK 0x0000ffffffffffffull

#define WILLRT_GC_STATE_MAKE(region, type, flags) \
    ((((uint64_t)(region)) << WILLRT_GC_STATE_REGION_SHIFT) | \
     (((uint64_t)(type)) << WILLRT_GC_STATE_TYPE_SHIFT) | \
     ((uint64_t)(flags)))
#define WILLRT_GC_STATE_REGION(state) \
    ((uint32_t)((((uint64_t)(state)) & WILLRT_GC_STATE_REGION_MASK) >> \
                WILLRT_GC_STATE_REGION_SHIFT))
#define WILLRT_GC_STATE_TYPE(state) \
    ((uint32_t)((((uint64_t)(state)) & WILLRT_GC_STATE_TYPE_MASK) >> \
                WILLRT_GC_STATE_TYPE_SHIFT))
#define WILLRT_GC_STATE_PIN(state) \
    ((uint32_t)((((uint64_t)(state)) & WILLRT_GC_STATE_PIN_MASK) >> \
                WILLRT_GC_STATE_PIN_SHIFT))
#define WILLRT_GC_STATE_FORWARD_ADDRESS(state) \
    ((void*)(uintptr_t)(((uint64_t)(state)) & WILLRT_GC_STATE_FORWARD_MASK))

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

/* A type descriptor is static metadata. The compiler assigns a unique nonzero
   id; the runtime validates the descriptor and indexes it without a lookup in
   the allocation fast path. reference_offsets and weak_offsets are payload
   offsets in ascending order. */
enum willrt_gc_type_flag {
    WILLRT_GC_TYPE_REFERENCES = 1u << 0,
    WILLRT_GC_TYPE_LARGE = 1u << 1,
    WILLRT_GC_TYPE_PINNED = 1u << 2
};

#define WILLRT_GC_TYPE_ID_MAX 0x7fffffu

typedef struct willrt_gc_type {
    uint32_t id;
    uint32_t size;
    uint32_t align;
    uint32_t flags;
    uint32_t reference_count;
    const uint32_t* reference_offsets;
    uint32_t weak_count;
    const uint32_t* weak_offsets;
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

/* A frame descriptor is compiler-owned storage. frame_enter links it into the
   current lane; frame_leave unlinks it. The parent pointer is filled by the
   runtime so a lane can publish an exact chain without walking C frames. */
typedef struct willrt_gc_frame {
    const willrt_gc_stack_map_data* map;
    void* base;
    struct willrt_gc_frame* parent;
} willrt_gc_frame;

/* Root scanning is exact. The runtime calls scan(context, visit, visit_context);
   scan reports each root slot through visit(visit_context, slot). */
typedef void (*willrt_gc_root_visit)(void* context, void** slot);
typedef void (*willrt_gc_root_scan)(void* context,
                                    willrt_gc_root_visit visit,
                                    void* visit_context);

/* Registration is once per compiler-emitted type, before any allocation of
   that type. It returns 0 on success and -1 when the descriptor is invalid or
   two descriptors claim the same id. */
int32_t willrt_gc_register_type(const willrt_gc_type* type);

/* Allocation returns an untagged object pointer. The caller stores the color
   view required by the current barrier state. A null type means the object has
   no references. */
void* willrt_gc_alloc(size_t size, const willrt_gc_type* type);

/* A zero memory limit means no explicit limit. A nonzero limit is a soft GC
   target: the pacer starts a cycle before the runtime reaches it. Setting the
   limit returns the previous value; it never changes the reserved address
   space. WILL_GC_MEMLIMIT supplies the startup value. */
uint64_t willrt_gc_set_memory_limit(uint64_t bytes);
uint64_t willrt_gc_memory_limit(void);

/* willrt_gc_shutdown stops the GC worker and releases the view mapping. It is
   called by willrt_shutdown after tasks have joined. */
void willrt_gc_shutdown(void);

/* read_barrier returns the current untagged address and checks the object for
   forwarding. read_barrier_slot also writes the remapped reference back to the
   slot. write_barrier publishes a new reference and records the old value for
   SATB and remembered-set work. */
void* willrt_gc_read_barrier(const void* ref);
void* willrt_gc_read_barrier_slot(void** slot);
void willrt_gc_write_barrier(void** slot, void* value);

/* Lane and frame registration. Every thread that can touch a GC object enters
   a lane before its first allocation. A safepoint polls for a GC handshake and
   publishes the current frame chain; the fast path is one atomic load. */
void willrt_gc_lane_enter(int32_t lane);
void willrt_gc_lane_leave(void);
void willrt_gc_frame_enter(willrt_gc_frame* frame,
                           const willrt_gc_stack_map_data* map, void* base);
void willrt_gc_frame_leave(willrt_gc_frame* frame);
void willrt_gc_native_enter(void);
void willrt_gc_native_leave(void);
void willrt_gc_safepoint(enum willrt_gc_safepoint_kind kind);

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
