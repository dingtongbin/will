/* Internal heap layout shared by the GC modules and their C tests. The public
   ABI stays in willrt_gc.h; this header only names the region table, TLAB, and
   object-state bits that the collector owns. */

#ifndef WILLRT_GC_INTERNAL_H
#define WILLRT_GC_INTERNAL_H

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>

#define WILLRT_GC_ARENA_SIZE (UINT64_C(64) * 1024u * 1024u)
#define WILLRT_GC_REGION_SIZE (UINT64_C(1024) * 1024u)
#define WILLRT_GC_TLAB_SIZE (UINT64_C(64) * 1024u)
#define WILLRT_GC_LARGE_THRESHOLD (UINT64_C(32) * 1024u)
#define WILLRT_GC_REGION_MAX 256u

/* The state word is runtime-owned. The low bits hold age and mark state; the
   pin count sits above the flags; the region id occupies the upper half. */
#define WILLRT_GC_STATE_AGE_MASK 0x3u
#define WILLRT_GC_STATE_AGE_SHIFT 0u
#define WILLRT_GC_STATE_MARK_SHIFT 2u
#define WILLRT_GC_STATE_MARK_MASK (0x3u << WILLRT_GC_STATE_MARK_SHIFT)
#define WILLRT_GC_STATE_PINNED (1u << 4)
#define WILLRT_GC_STATE_LARGE (1u << 5)
#define WILLRT_GC_STATE_PINNED_REGION (1u << 6)
#define WILLRT_GC_STATE_PIN_SHIFT 8u
#define WILLRT_GC_STATE_PIN_MASK (0xffu << WILLRT_GC_STATE_PIN_SHIFT)
#define WILLRT_GC_STATE_REGION_SHIFT 16u
#define WILLRT_GC_STATE_REGION_MASK (0xffffu << WILLRT_GC_STATE_REGION_SHIFT)
#define WILLRT_GC_STATE_TYPE_SHIFT 32u
#define WILLRT_GC_STATE_TYPE_MASK (UINT64_C(0xffffffff) << WILLRT_GC_STATE_TYPE_SHIFT)

typedef struct gc_region {
    uint8_t* base;
    size_t size;
    size_t top;
    uint32_t kind;
    uint32_t age;
} gc_region;

typedef struct gc_tlab {
    uint8_t* cur;
    uint8_t* limit;
    gc_region* region;
} gc_tlab;

extern willrt_os_mutex willrt_gc_heap_lock;
extern gc_region gc_regions[WILLRT_GC_REGION_MAX];
extern size_t gc_region_count;
extern size_t willrt_gc_arena_used;

uint64_t willrt_gc_state_make(uint32_t region, uint32_t type, uint32_t flags);
uint32_t willrt_gc_state_region(uint64_t state);
uint32_t willrt_gc_state_type(uint64_t state);
uint32_t willrt_gc_state_pin(uint64_t state);

uint32_t gc_type_id(const willrt_gc_type* type);
const willrt_gc_type* gc_type_get(uint32_t id);

void willrt_gc_mark_object(void* object);
void willrt_gc_mark_stack(const willrt_gc_stack_map_data* map, void* frame);
void willrt_gc_root_scan_all(willrt_gc_root_visit visit, void* context);
void willrt_gc_handle_scan_all(willrt_gc_root_visit visit, void* context);
void willrt_gc_satb_drain(willrt_gc_root_visit visit, void* context);
int willrt_gc_marking_active(void);

int willrt_gc_heap_offset(const void* ptr, size_t* offset);
void willrt_gc_card_mark(void* slot);
int willrt_gc_test_card_marked(void* slot);
size_t willrt_gc_test_card_count(void);

/* Test hooks. They expose counters the stress tests need without adding public
   ABI surface. */
size_t willrt_gc_test_region_count(void);
size_t willrt_gc_test_allocated_bytes(void);
size_t willrt_gc_test_tlab_refills(void);
size_t willrt_gc_test_large_regions(void);
size_t willrt_gc_test_pinned_regions(void);
size_t willrt_gc_test_tlab_size(void);
size_t willrt_gc_test_region_size(void);
size_t willrt_gc_test_root_count(void);
size_t willrt_gc_test_handle_count(void);
size_t willrt_gc_test_satb_count(void);
size_t willrt_gc_test_satb_overflow(void);
void willrt_gc_test_begin_mark(void);
void willrt_gc_test_end_mark(void);
void willrt_gc_test_mark(void);
int willrt_gc_test_marked(void* object);
size_t willrt_gc_test_mark_count(void);

#endif
