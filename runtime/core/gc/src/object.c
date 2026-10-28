/* Object header helpers and precise field walking. The header carries only
   runtime state; copying, marking, and weak handling use static type offsets. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

willrt_gc_header* gc_header_of(const void* object) {
    return (willrt_gc_header*)((uint8_t*)object -
                               WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
}

void* gc_payload_of(const willrt_gc_header* header) {
    return (void*)((uint8_t*)header + WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
}

int gc_object_is_forwarded(const void* object, void** forwarded) {
    willrt_gc_header* header;
    uint64_t state;

    if (object == NULL) {
        return 0;
    }
    header = gc_header_of(WILLRT_GC_UNTAG(object));
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) == 0) {
        return 0;
    }
    if (forwarded != NULL) {
        *forwarded = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
    }
    return 1;
}

uint64_t gc_state_make(uint32_t region, uint32_t type) {
    return WILLRT_GC_STATE_MAKE(region, type, 0);
}

uint32_t gc_state_region(uint64_t state) {
    return WILLRT_GC_STATE_REGION(state);
}

uint32_t gc_state_type(uint64_t state) {
    return WILLRT_GC_STATE_TYPE(state);
}

uint32_t gc_state_pin(uint64_t state) {
    return WILLRT_GC_STATE_PIN(state);
}

int gc_object_mark(void* object, uint32_t mark_color) {
    gc_region* region;
    willrt_gc_header* header;
    uint64_t state;

    (void)mark_color;
    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    for (;;) {
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
        if ((state & WILLRT_GC_STATE_FORWARDED) == 0) {
            break;
        }
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
    }
    region = gc_region_by_id(WILLRT_GC_STATE_REGION(state));
    if (region == NULL) {
        return 0;
    }
    return gc_region_mark(region, (size_t)((uint8_t*)object - region->base) -
                                      WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
}

void gc_scan_object_fields(void* object,
                           void (*visit)(void* context, void** slot),
                           void* context) {
    willrt_gc_header* header;
    uint64_t state;
    const willrt_gc_type* type;
    uint32_t i;

    if (object == NULL || visit == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
    }
    type = gc_type_get(WILLRT_GC_STATE_TYPE(state));
    if (type == NULL || type->reference_count == 0) {
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
        visit(context, slot);
    }
}

void gc_scan_object_weak(void* object,
                         void (*visit)(void* context, void** slot),
                         void* context) {
    willrt_gc_header* header;
    uint64_t state;
    const willrt_gc_type* type;
    uint32_t i;

    if (object == NULL || visit == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
    }
    type = gc_type_get(WILLRT_GC_STATE_TYPE(state));
    if (type == NULL || type->weak_count == 0) {
        return;
    }
    for (i = 0; i < type->weak_count; i++) {
        uint32_t offset = type->weak_offsets[i];
        void** slot;

        if (offset > type->size ||
            sizeof(void*) > (size_t)type->size - (size_t)offset) {
            abort();
        }
        slot = (void**)((uint8_t*)object + offset);
        visit(context, slot);
    }
}
