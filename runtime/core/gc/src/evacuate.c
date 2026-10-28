/* Single-threaded young evacuation for the GC development path. The object
   table enumerates marked young objects, a forwarding map remembers old to new
   payload addresses, and exact roots, handles, and object fields are rewritten
   after the copy. The final collector replaces this map with the colored
   multi-view protocol; this module does not implement a fallback runtime. */

#include "gc_internal.h"

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define WILLRT_GC_FORWARD_MAX 65536u

typedef struct gc_forward_entry {
    void* old_object;
    void* new_object;
} gc_forward_entry;

static gc_forward_entry gc_forward_entries[WILLRT_GC_FORWARD_MAX];
static size_t gc_forward_count;
static size_t gc_evacuated_count;

static void gc_forward_add(void* old_object, void* new_object) {
    if (gc_forward_count >= WILLRT_GC_FORWARD_MAX) {
        abort();
    }
    gc_forward_entries[gc_forward_count].old_object = old_object;
    gc_forward_entries[gc_forward_count].new_object = new_object;
    gc_forward_count++;
}

static void* gc_forward(void* object) {
    size_t i;

    if (object == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(object);
    for (i = 0; i < gc_forward_count; i++) {
        if (gc_forward_entries[i].old_object == object) {
            return gc_forward_entries[i].new_object;
        }
    }
    return object;
}

static void gc_forward_slot(void* context, void** slot) {
    (void)context;
    if (slot != NULL && *slot != NULL) {
        *slot = gc_forward(*slot);
    }
}

static int gc_is_marked(uint64_t state) {
    return (state & WILLRT_GC_STATE_MARK_MASK) != 0;
}

size_t willrt_gc_test_evacuate(void) {
    size_t objects = willrt_gc_object_count();
    size_t i;

    gc_forward_count = 0;
    gc_evacuated_count = 0;
    willrt_gc_tlab_new_young();
    for (i = 0; i < objects; i++) {
        void* base = NULL;
        size_t size = 0;
        willrt_gc_header* header;
        uint64_t state;
        uint32_t region;
        const willrt_gc_type* type;
        void* copy;

        if (!willrt_gc_object_at(i, &base, &size) ||
            size <= WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
            continue;
        }
        header = (willrt_gc_header*)base;
        state = atomic_load_explicit(&header->state, memory_order_relaxed);
        region = willrt_gc_state_region(state);
        if (region >= WILLRT_GC_REGION_MAX ||
            gc_regions[region].kind != WILLRT_GC_REGION_YOUNG ||
            !gc_is_marked(state) || willrt_gc_state_pin(state) != 0 ||
            (state & WILLRT_GC_STATE_LARGE) != 0) {
            continue;
        }
        type = gc_type_get(willrt_gc_state_type(state));
        if (type != NULL &&
            (type->flags & (WILLRT_GC_TYPE_LARGE | WILLRT_GC_TYPE_PINNED)) !=
                0) {
            continue;
        }
        copy = willrt_gc_alloc(size - WILLRT_GC_OBJECT_PAYLOAD_OFFSET, type);
        if (copy == NULL) {
            abort();
        }
        memcpy(copy, base + WILLRT_GC_OBJECT_PAYLOAD_OFFSET,
               size - WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
        gc_forward_add(base + WILLRT_GC_OBJECT_PAYLOAD_OFFSET, copy);
        gc_evacuated_count++;
    }
    willrt_gc_root_scan_all(gc_forward_slot, NULL);
    willrt_gc_handle_scan_all(gc_forward_slot, NULL);
    {
        size_t after = willrt_gc_object_count();

        for (i = 0; i < after; i++) {
            void* base = NULL;
            size_t size = 0;
            willrt_gc_header* header;
            uint64_t state;
            const willrt_gc_type* type;
            uint32_t field;

            if (!willrt_gc_object_at(i, &base, &size) ||
                size <= WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
                continue;
            }
            header = (willrt_gc_header*)base;
            state = atomic_load_explicit(&header->state, memory_order_relaxed);
            type = gc_type_get(willrt_gc_state_type(state));
            if (type == NULL) {
                continue;
            }
            for (field = 0; field < type->reference_count; field++) {
                uint32_t offset = type->reference_offsets[field];
                void** slot;

                if (offset > type->size ||
                    sizeof(void*) > (size_t)type->size - (size_t)offset) {
                    continue;
                }
                slot = (void**)((uint8_t*)base +
                                WILLRT_GC_OBJECT_PAYLOAD_OFFSET + offset);
                if (*slot != NULL) {
                    *slot = gc_forward(*slot);
                }
            }
        }
    }
    return gc_evacuated_count;
}

size_t willrt_gc_test_evacuated_count(void) {
    return gc_evacuated_count;
}
