/* Internal GC protocol shared by the collector modules and their tests.

   The public ABI is willrt_gc.h. This header names the region allocator,
   object-state helpers, forwarding, root/lane handshake, and concurrent work
   queues. No module keeps a second copy of a fixed table or a fallback path. */

#ifndef WILLRT_GC_INTERNAL_H
#define WILLRT_GC_INTERNAL_H

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#define WILLRT_GC_HEAP_DEFAULT (UINT64_C(1024) * 1024u * 1024u)
#define WILLRT_GC_GRANULE_SIZE (UINT64_C(1024) * 1024u)
#define WILLRT_GC_TLAB_SIZE (UINT64_C(64) * 1024u)
#define WILLRT_GC_LARGE_THRESHOLD (UINT64_C(32) * 1024u)
#define WILLRT_GC_CARD_SIZE 512u
#define WILLRT_GC_GRANULE_SHIFT 20u
#define WILLRT_GC_GRANULE_MASK (WILLRT_GC_GRANULE_SIZE - 1u)
#define WILLRT_GC_GRANULE_MAX 65535u
#define WILLRT_GC_REGION_ORDER_MIN 0u
#define WILLRT_GC_REGION_ORDER_MAX 16u
#define WILLRT_GC_WORK_CHUNK 1024u
#define WILLRT_GC_MARK_COLOR_MASK WILLRT_GC_STATE_MARK_MASK
#define WILLRT_GC_MARK_UNMARKED 0u
#define WILLRT_GC_MARK_ZERO 1u
#define WILLRT_GC_MARK_ONE 2u

enum gc_region_kind {
    GC_REGION_YOUNG = WILLRT_GC_REGION_YOUNG,
    GC_REGION_OLD = WILLRT_GC_REGION_OLD,
    GC_REGION_LARGE = WILLRT_GC_REGION_LARGE,
    GC_REGION_PINNED = WILLRT_GC_REGION_PINNED,
    GC_REGION_FREE = 4
};

enum gc_region_flags {
    GC_REGION_SURVIVOR = 1u << 0,
    GC_REGION_EVACUATING = 1u << 1,
    GC_REGION_REMAPPED = 1u << 2,
    GC_REGION_LARGE_FLAG = 1u << 3
};

typedef struct gc_region {
    uint8_t* base;
    size_t size;
    size_t order;
    _Atomic size_t top;
    uint32_t id;
    uint32_t kind;
    uint32_t age;
    uint32_t flags;
    _Atomic uint32_t pin_count;
    _Atomic uint32_t evac_state;
    _Atomic size_t live_bytes;
    _Atomic uint64_t* start_bits;
    _Atomic uint64_t* end_bits;
    _Atomic uint64_t* mark_bits;
    _Atomic uint64_t* card_bits;
    _Atomic uint32_t* card_objects;
    _Atomic uint32_t mark_epoch;
    size_t bitmap_words;
    size_t card_count;
    uint32_t free_next;
} gc_region;

typedef struct gc_tlab {
    gc_region* region;
    uint8_t* cur;
    uint8_t* limit;
    uint8_t* start;
    int32_t lane;
    uint32_t epoch;
} gc_tlab;

typedef struct gc_mark_chunk {
    _Atomic(struct gc_mark_chunk*) next;
    size_t count;
    void* objects[WILLRT_GC_WORK_CHUNK];
} gc_mark_chunk;

typedef struct gc_satb_chunk {
    _Atomic(struct gc_satb_chunk*) next;
    size_t count;
    void* objects[WILLRT_GC_WORK_CHUNK];
} gc_satb_chunk;

typedef struct gc_lane_record gc_lane_record;

extern _Atomic uint32_t gc_good_color;
extern _Atomic uint32_t gc_mark_color;
extern _Atomic uint32_t gc_mark_epoch;
extern _Atomic int gc_marking;
extern _Atomic int gc_evacuating;

extern gc_region** gc_region_table;
extern gc_region** gc_granule_table;
extern uint8_t* gc_heap_base;
extern size_t gc_heap_size;
extern size_t gc_heap_granule_count;

void gc_region_init(void);
void gc_region_shutdown(void);
_Noreturn void gc_oom(void);
void gc_heap_init(void);
void gc_heap_shutdown(void);
void gc_heap_epoch_bump(void);
void* gc_alloc(size_t size, const willrt_gc_type* type);
gc_region* gc_region_alloc(size_t order, uint32_t kind);
void gc_region_free(gc_region* region);
gc_region* gc_region_by_address(const void* address);
gc_region* gc_region_by_id(uint32_t id);
uint32_t gc_region_id(const gc_region* region);
void gc_region_set_start(gc_region* region, size_t offset);
void gc_region_set_end(gc_region* region, size_t offset);
int gc_region_object_bounds(gc_region* region, size_t offset, size_t* start,
                            size_t* end);
void gc_region_note_card(gc_region* region, size_t offset);
void gc_region_scan_objects(gc_region* region, void (*visit)(void*, void*),
                            void* context);
void gc_region_clear_metadata(gc_region* region);
void gc_region_add_live(gc_region* region, size_t bytes);
int gc_region_mark(gc_region* region, size_t offset);
int gc_region_is_marked(gc_region* region, size_t offset);
void gc_object_publish(gc_region* region, size_t offset, size_t total,
                       uint32_t type_id, uint32_t flags);

int32_t gc_type_register(const willrt_gc_type* type);
const willrt_gc_type* gc_type_get(uint32_t id);
size_t gc_type_payload_size(const willrt_gc_type* type);

uint64_t gc_state_make(uint32_t region, uint32_t type);
uint32_t gc_state_region(uint64_t state);
uint32_t gc_state_type(uint64_t state);
uint32_t gc_state_pin(uint64_t state);

willrt_gc_header* gc_header_of(const void* object);
void* gc_payload_of(const willrt_gc_header* header);
int gc_object_is_forwarded(const void* object, void** forwarded);
void* gc_object_forward(void* object);
int gc_object_mark(void* object, uint32_t mark_color);
int gc_object_marked(void* object);
void* gc_object_forward(void* object);
void gc_mark_slot(void** slot);
void gc_mark_drain(void);
void gc_mark_flush_local(void);
void gc_copy_enqueue(void* object);
void gc_copy_drain(void);
void gc_copy_reset(void);
void gc_copy_flush_local(void);
void gc_evac_slot(void* context, void** slot);
void gc_weak_clear(void);
void gc_remap_heap(void);
void gc_reclaim_nonmoving(void);
void gc_scan_object_fields(void* object,
                           void (*visit)(void* context, void** slot),
                           void* context);
void gc_scan_object_weak(void* object,
                         void (*visit)(void* context, void** slot),
                         void* context);

void gc_roots_scan(willrt_gc_root_visit visit, void* context);
void gc_handles_scan(willrt_gc_root_visit visit, void* context);

void gc_lane_init(void);
void gc_lane_shutdown(void);
void gc_safepoint_init(void);
void gc_safepoint_shutdown(void);
void gc_lane_set_current(int32_t lane);
void gc_lane_enter(int32_t lane);
void gc_lane_leave(void);
void gc_frame_enter(willrt_gc_frame* frame,
                    const willrt_gc_stack_map_data* map, void* base);
void gc_frame_leave(willrt_gc_frame* frame);
void gc_native_enter(void);
void gc_native_leave(void);
void gc_safepoint_poll(void);
int gc_lane_stop(int32_t lane);
void gc_lane_resume(int32_t lane);
void gc_lane_scan(int32_t lane, willrt_gc_root_visit visit, void* context);
void gc_lanes_scan(willrt_gc_root_visit visit, void* context);
int32_t gc_lane_current(void);
int gc_lane_count(void);

void gc_barrier_init(void);
void gc_barrier_shutdown(void);
void gc_write_barrier(void** slot, void* value);
void* gc_read_barrier(const void* ref);
void* gc_read_barrier_slot(void** slot);
void gc_satb_flush_local(void);
void gc_satb_scan(willrt_gc_root_visit visit, void* context);
void gc_card_scan(willrt_gc_root_visit visit, void* context);
void gc_post_write(void** slot, void* value);

void gc_mark_init(void);
void gc_mark_shutdown(void);
void gc_mark_begin(uint32_t color);
void gc_mark_end(void);
void gc_mark_object(void* object);
void gc_mark_stack(const willrt_gc_stack_map_data* map, void* frame);
int gc_mark_active(void);

void gc_cycle_init(void);
void gc_cycle_shutdown(void);
void gc_cycle_kick(void);
void gc_cycle_request_full(void);
int gc_cycle_idle(void);
void gc_pacer_init(void);
void gc_pacer_shutdown(void);
void gc_pacer_on_alloc(size_t bytes);
void gc_pacer_cycle_started(void);

#endif
