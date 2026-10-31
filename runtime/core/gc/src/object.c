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

size_t gc_object_payload_size(void* object) {
    gc_region* region;
    size_t offset;
    size_t start;
    size_t end;

    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    region = gc_region_by_address(object);
    if (region == NULL) {
        return 0;
    }
    offset = (size_t)((uint8_t*)object - region->base) -
             WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    if (!gc_region_object_bounds(region, offset, &start, &end)) {
        return 0;
    }
    if (end < start + WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
        return 0;
    }
    return end - start - WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
}

static _Thread_local unsigned gc_sequence_scan_depth;

int gc_scan_in_sequence(void) {
    return gc_sequence_scan_depth != 0;
}

/* gc_scan_value_mode walks one inline value. Nested field tables and enum
   variant tables are recursive; flat offset lists are the leaf form. */
static void gc_scan_value_mode(void* base, const willrt_gc_type* type,
                               willrt_gc_root_visit visit, void* visit_context,
                               int weak) {
    uint32_t i;

    if (base == NULL || type == NULL || visit == NULL) {
        return;
    }
    if ((type->flags & WILLRT_GC_TYPE_ENUM) != 0) {
        const willrt_gc_variant* variants =
            (const willrt_gc_variant*)type->reference_offsets;
        int32_t tag = *(const int32_t*)base;

        for (i = 0; i < type->reference_count; i++) {
            if (variants[i].tag == tag) {
                if (variants[i].payload != NULL) {
                    gc_scan_value_mode((uint8_t*)base +
                                           variants[i].payload_offset,
                                       variants[i].payload, visit, visit_context,
                                       weak);
                }
                return;
            }
        }
        return;
    }
    if ((type->flags & WILLRT_GC_TYPE_NESTED) != 0) {
        const willrt_gc_field* fields =
            (const willrt_gc_field*)type->reference_offsets;

        for (i = 0; i < type->reference_count; i++) {
            int fieldWeak =
                (fields[i].flags & WILLRT_GC_FIELD_WEAK) != 0;

            if (fieldWeak != weak || fields[i].type == NULL) {
                continue;
            }
            gc_scan_value_mode((uint8_t*)base + fields[i].offset,
                               fields[i].type, visit, visit_context, weak);
        }
        return;
    }
    if (weak) {
        for (i = 0; i < type->weak_count; i++) {
            visit(visit_context,
                  (void**)((uint8_t*)base + type->weak_offsets[i]));
        }
    } else {
        for (i = 0; i < type->reference_count; i++) {
            visit(visit_context,
                  (void**)((uint8_t*)base + type->reference_offsets[i]));
        }
    }
}

/* gc_scan_sequence_fields walks a variable-length object. The region bitmap
   owns the size, so each capacity uses one immutable descriptor. */
static void gc_scan_sequence_fields(void* object, const willrt_gc_type* type,
                                    willrt_gc_root_visit visit,
                                    void* visit_context) {
    size_t payload;
    size_t count;
    size_t i;

    if (type == NULL || type->element_type == NULL ||
        type->element_size == 0) {
        return;
    }
    payload = gc_object_payload_size(object);
    count = payload / type->element_size;
    gc_sequence_scan_depth++;
    for (i = 0; i < count; i++) {
        void* element = (uint8_t*)object + i * type->element_size;

        gc_scan_value_mode(element, type->element_type, visit,
                           visit_context, 0);
    }
    gc_sequence_scan_depth--;
}

static void gc_scan_sequence_weak(void* object, const willrt_gc_type* type,
                                  willrt_gc_root_visit visit,
                                  void* visit_context) {
    const willrt_gc_type* element_type;
    size_t payload;
    size_t count;
    size_t i;

    if (type == NULL || type->element_type == NULL ||
        type->element_size == 0) {
        return;
    }
    element_type = type->element_type;
    if (element_type->weak_count == 0 &&
        (element_type->flags &
         (WILLRT_GC_TYPE_NESTED | WILLRT_GC_TYPE_ENUM)) == 0) {
        return;
    }
    payload = gc_object_payload_size(object);
    count = payload / type->element_size;
    gc_sequence_scan_depth++;
    for (i = 0; i < count; i++) {
        uint8_t* element = (uint8_t*)object + i * type->element_size;

        gc_scan_value_mode(element, element_type, visit, visit_context, 1);
    }
    gc_sequence_scan_depth--;
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
        region = gc_region_by_address(object);
        if (region == NULL) {
            return 0;
        }
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

void willrt_gc_scan_value(void* base, const willrt_gc_type* type,
                          willrt_gc_root_visit visit, void* visit_context) {
    gc_scan_value_mode(base, type, visit, visit_context, 0);
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
    if (type == NULL) {
        return;
    }
    if ((type->flags & WILLRT_GC_TYPE_SEQUENCE) != 0) {
        gc_scan_sequence_fields(object, type, visit, context);
        return;
    }
    if ((type->flags & (WILLRT_GC_TYPE_NESTED | WILLRT_GC_TYPE_ENUM)) != 0) {
        gc_scan_value_mode(object, type, visit, context, 0);
        return;
    }
    if (type->reference_count == 0) {
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
    if (type == NULL) {
        return;
    }
    if ((type->flags & WILLRT_GC_TYPE_SEQUENCE) != 0) {
        gc_scan_sequence_weak(object, type, visit, context);
        return;
    }
    if ((type->flags & (WILLRT_GC_TYPE_NESTED | WILLRT_GC_TYPE_ENUM)) != 0) {
        gc_scan_value_mode(object, type, visit, context, 1);
        return;
    }
    if (type->weak_count == 0) {
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
