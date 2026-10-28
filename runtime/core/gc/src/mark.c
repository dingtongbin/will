/* Precise marking. The marker starts from exact roots and handles, consumes
   the SATB buffer, and walks reference offsets from the static type table.
   Sweeping and copying are separate modules, so this file never frees or
   moves an object. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#define WILLRT_GC_MARK_STACK_MAX 65536u

static void* gc_mark_stack[WILLRT_GC_MARK_STACK_MAX];
static size_t gc_mark_top;
static size_t gc_mark_count;
static atomic_int gc_marking;

static void gc_mark_visit(void* context, void** slot) {
    (void)context;
    if (slot != NULL) {
        willrt_gc_mark_object(*slot);
    }
}

static void gc_scan_object(void* object) {
    willrt_gc_header* header;
    uint64_t state;
    const willrt_gc_type* type;
    uint32_t type_id;
    uint32_t i;

    header = (willrt_gc_header*)((uint8_t*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    state = atomic_load_explicit(&header->state, memory_order_relaxed);
    type_id = willrt_gc_state_type(state);
    type = gc_type_get(type_id);
    if (type == NULL || (type->flags & WILLRT_GC_TYPE_REFERENCES) == 0) {
        return;
    }
    for (i = 0; i < type->reference_count; i++) {
        uint32_t offset = type->reference_offsets[i];
        void** slot;

        if (offset > type->size ||
            sizeof(void*) > (size_t)type->size - (size_t)offset) {
            abort();
        }
        slot = (void**)((uint8_t*)object + offset);
        willrt_gc_mark_object(*slot);
    }
}

static void gc_drain_mark_stack(void) {
    while (gc_mark_top > 0) {
        void* object = gc_mark_stack[--gc_mark_top];

        gc_scan_object(object);
    }
}

void willrt_gc_mark_object(void* object) {
    willrt_gc_header* header;
    uint64_t old;

    if (object == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    header = (willrt_gc_header*)((uint8_t*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    old = atomic_fetch_or_explicit(&header->state,
                                   (uint64_t)1 << WILLRT_GC_STATE_MARK_SHIFT,
                                   memory_order_acq_rel);
    if ((old & WILLRT_GC_STATE_MARK_MASK) != 0) {
        return;
    }
    if (gc_mark_top >= WILLRT_GC_MARK_STACK_MAX) {
        abort();
    }
    gc_mark_stack[gc_mark_top++] = object;
    gc_mark_count++;
}

void willrt_gc_mark_stack(const willrt_gc_stack_map_data* map, void* frame) {
    size_t i;

    if (map == NULL || frame == NULL) {
        return;
    }
    for (i = 0; i < map->slot_count; i++) {
        const willrt_gc_stack_slot* slot = &map->slots[i];

        if ((slot->flags & WILLRT_GC_SLOT_REFERENCE) != 0) {
            void** address = (void**)((uint8_t*)frame + slot->slot);
            willrt_gc_mark_object(*address);
        }
    }
}

int willrt_gc_marking_active(void) {
    return atomic_load_explicit(&gc_marking, memory_order_acquire);
}

void willrt_gc_test_begin_mark(void) {
    gc_mark_top = 0;
    gc_mark_count = 0;
    atomic_store_explicit(&gc_marking, 1, memory_order_release);
}

void willrt_gc_test_end_mark(void) {
    atomic_store_explicit(&gc_marking, 0, memory_order_release);
}

void willrt_gc_test_mark(void) {
    willrt_gc_root_scan_all(gc_mark_visit, NULL);
    willrt_gc_handle_scan_all(gc_mark_visit, NULL);
    willrt_gc_satb_drain(gc_mark_visit, NULL);
    gc_drain_mark_stack();
}

int willrt_gc_test_marked(void* object) {
    willrt_gc_header* header;
    uint64_t state;

    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    header = (willrt_gc_header*)((uint8_t*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    state = atomic_load_explicit(&header->state, memory_order_relaxed);
    return (state & WILLRT_GC_STATE_MARK_MASK) != 0;
}

size_t willrt_gc_test_mark_count(void) {
    return gc_mark_count;
}
