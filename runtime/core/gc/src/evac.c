/* Concurrent evacuation. A forwarded object is published by a CAS on the busy
   bit, copied once, and linked into a dynamic copy queue. Old from-space stays
   readable until the remap handshake has updated every root and live field. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define GC_PROMOTE_AGE 2u

static _Atomic(gc_copy_chunk*) gc_copy_active;
static _Thread_local gc_copy_chunk* gc_copy_local;
static willrt_os_mutex gc_copy_lock;
static gc_region* gc_copy_young;
static gc_region* gc_copy_old;
static atomic_int gc_copy_state;

static size_t gc_align_up(size_t value, size_t align) {
    size_t rest;

    if (align <= 1u) {
        return value;
    }
    rest = value % align;
    return rest == 0u ? value : value + align - rest;
}

static void gc_copy_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_copy_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_copy_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_copy_lock);
        atomic_store_explicit(&gc_copy_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_copy_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

static void gc_copy_push_chunk(gc_copy_chunk* chunk) {
    gc_copy_chunk* head;

    if (chunk == NULL || chunk->count == 0) {
        return;
    }
    head = atomic_load_explicit(&gc_copy_active, memory_order_relaxed);
    do {
        atomic_store_explicit(&chunk->next, head, memory_order_relaxed);
    } while (!atomic_compare_exchange_weak_explicit(
        &gc_copy_active, &head, chunk, memory_order_release,
        memory_order_relaxed));
}

void gc_copy_flush_local(void) {
    if (gc_copy_local != NULL && gc_copy_local->count != 0) {
        gc_copy_chunk* chunk = gc_copy_local;

        gc_copy_local = NULL;
        gc_copy_push_chunk(chunk);
    }
}

void gc_copy_enqueue(void* object) {
    gc_copy_chunk* chunk;

    if (object == NULL) {
        return;
    }
    gc_copy_init();
    chunk = gc_copy_local;
    if (chunk == NULL || chunk->count == WILLRT_GC_WORK_CHUNK) {
        if (chunk != NULL) {
            gc_copy_push_chunk(chunk);
        }
        chunk = gc_chunk_acquire();
        gc_copy_local = chunk;
    }
    chunk->objects[chunk->count++] = object;
}

void gc_copy_reset(void) {
    gc_copy_chunk* chunk;

    gc_copy_init();
    gc_copy_flush_local();
    chunk = atomic_exchange_explicit(&gc_copy_active, NULL,
                                     memory_order_acquire);
    while (chunk != NULL) {
        gc_copy_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);

        gc_chunk_release(chunk);
        chunk = next;
    }
    willrt_os_mutex_lock(&gc_copy_lock);
    gc_copy_young = NULL;
    gc_copy_old = NULL;
    willrt_os_mutex_unlock(&gc_copy_lock);
}

static void* gc_copy_alloc(size_t total, uint32_t type_id, uint32_t kind,
                           uint32_t age, const void* source) {
    const willrt_gc_type* type = gc_type_get(type_id);
    size_t align = type == NULL || type->align < WILLRT_GC_OBJECT_ALIGN
                       ? WILLRT_GC_OBJECT_ALIGN
                       : type->align;
    gc_region** current;
    gc_region* region;
    size_t base_offset;
    size_t top;
    void* payload;

    if (total > (size_t)WILLRT_GC_GRANULE_SIZE) {
        return NULL;
    }
    gc_copy_init();
    willrt_os_mutex_lock(&gc_copy_lock);
    current = kind == GC_REGION_OLD ? &gc_copy_old : &gc_copy_young;
    region = *current;
    top = 0;
    for (;;) {
        if (region == NULL ||
            (top = atomic_load_explicit(&region->top, memory_order_relaxed)) >
                region->size) {
            region = gc_region_alloc(0, kind);
            if (region == NULL) {
                willrt_os_mutex_unlock(&gc_copy_lock);
                return NULL;
            }
            if (kind == GC_REGION_YOUNG) {
                region->flags |= GC_REGION_SURVIVOR;
            }
            *current = region;
            top = 0;
            continue;
        }
        base_offset = (gc_align_up((size_t)(uintptr_t)region->base + top +
                                       WILLRT_GC_OBJECT_PAYLOAD_OFFSET,
                                   align) -
                       WILLRT_GC_OBJECT_PAYLOAD_OFFSET) -
                      (size_t)(uintptr_t)region->base;
        if (base_offset >= top && total <= region->size - base_offset) {
            break;
        }
        region = gc_region_alloc(0, kind);
        if (region == NULL) {
            willrt_os_mutex_unlock(&gc_copy_lock);
            return NULL;
        }
        if (kind == GC_REGION_YOUNG) {
            region->flags |= GC_REGION_SURVIVOR;
        }
        *current = region;
        top = 0;
    }
    atomic_store_explicit(&region->top, base_offset + total,
                          memory_order_release);
    memcpy(region->base + base_offset + WILLRT_GC_OBJECT_PAYLOAD_OFFSET,
           source, total - WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    gc_object_publish(region, base_offset, total, type_id,
                      (age & WILLRT_GC_STATE_AGE_MAX)
                          << WILLRT_GC_STATE_AGE_SHIFT);
    payload = region->base + base_offset + WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    willrt_os_mutex_unlock(&gc_copy_lock);
    return payload;
}

int gc_object_marked(void* object) {
    willrt_gc_header* header;
    uint64_t state;
    gc_region* region;

    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
    }
    region = gc_region_by_id(WILLRT_GC_STATE_REGION(state));
    if (region == NULL) {
        return 0;
    }
    return gc_region_is_marked(
        region, (size_t)((uint8_t*)object - region->base) -
                    WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
}

void* gc_object_forward(void* object) {
    gc_region* source;
    willrt_gc_header* header;
    uint64_t state;
    size_t offset;
    size_t start;
    size_t end;
    size_t total;
    uint32_t type_id;
    uint32_t age;
    uint32_t kind;
    void* payload;
    uint64_t busy;
    uint64_t forwarded;

    if (object == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(object);
    source = gc_region_by_address(object);
    if (source == NULL || (source->flags & GC_REGION_EVACUATING) == 0 ||
        source->kind == GC_REGION_LARGE || source->kind == GC_REGION_PINNED ||
        atomic_load_explicit(&source->pin_count, memory_order_acquire) != 0) {
        return object;
    }
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        return WILLRT_GC_STATE_FORWARD_ADDRESS(state);
    }
    if ((state & WILLRT_GC_STATE_BUSY) != 0 ||
        WILLRT_GC_STATE_PIN(state) != 0) {
        return object;
    }
    if ((uint8_t*)object < source->base + WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
        return object;
    }
    offset = (size_t)((uint8_t*)object - source->base) -
             WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
    if (!gc_region_object_bounds(source, offset, &start, &end)) {
        return object;
    }
    total = end - start;
    if (total < WILLRT_GC_OBJECT_PAYLOAD_OFFSET) {
        return object;
    }
    busy = state | WILLRT_GC_STATE_BUSY;
    if (!atomic_compare_exchange_strong_explicit(
            &header->state, &state, busy, memory_order_acq_rel,
            memory_order_acquire)) {
        return (state & WILLRT_GC_STATE_FORWARDED) != 0
                   ? WILLRT_GC_STATE_FORWARD_ADDRESS(state)
                   : object;
    }
    type_id = WILLRT_GC_STATE_TYPE(state);
    age = (uint32_t)((state & WILLRT_GC_STATE_AGE_MASK) >>
                     WILLRT_GC_STATE_AGE_SHIFT);
    if (age < WILLRT_GC_STATE_AGE_MAX) {
        age++;
    }
    kind = source->kind == GC_REGION_OLD || age >= GC_PROMOTE_AGE
               ? GC_REGION_OLD
               : GC_REGION_YOUNG;
    payload = gc_copy_alloc(total, type_id, kind, age, object);
    if (payload == NULL) {
        atomic_store_explicit(&header->state, state, memory_order_release);
        return object;
    }
    forwarded = WILLRT_GC_STATE_FORWARDED | (uint64_t)(uintptr_t)payload;
    atomic_store_explicit(&header->state, forwarded, memory_order_release);
    if (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0) {
        gc_object_mark(payload, atomic_load_explicit(&gc_mark_color,
                                                     memory_order_acquire));
    }
    gc_copy_enqueue(payload);
    return payload;
}

void gc_evac_slot(void* context, void** slot) {
    void* ref;
    void* object;

    (void)context;
    if (slot == NULL) {
        return;
    }
    ref = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    if (ref == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(ref);
    if (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0) {
        gc_mark_object(object);
    }
    object = gc_object_forward(object);
    if (object == NULL) {
        return;
    }
    atomic_store_explicit((_Atomic(void*)*)slot,
                          WILLRT_GC_TAG(object,
                                        atomic_load_explicit(
                                            &gc_good_color,
                                            memory_order_acquire)),
                          memory_order_release);
}

static void gc_mark_object_cards(void* object) {
    gc_region* owner = gc_region_by_address(object);
    willrt_gc_header* header;
    uint64_t state;
    const willrt_gc_type* type;
    uint32_t i;

    if (owner == NULL ||
        (owner->kind == GC_REGION_YOUNG &&
         atomic_load_explicit(&owner->pin_count, memory_order_acquire) == 0)) {
        return;
    }
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    type = gc_type_get(WILLRT_GC_STATE_TYPE(state));
    if (type == NULL) {
        return;
    }
    for (i = 0; i < type->reference_count; i++) {
        void** slot = (void**)((uint8_t*)object +
                               type->reference_offsets[i]);
        gc_region* target;

        if (*slot == NULL) {
            continue;
        }
        target = gc_region_by_address(WILLRT_GC_UNTAG(*slot));
        if (target != NULL && target->kind == GC_REGION_YOUNG) {
            gc_region_note_card(
                owner, (size_t)((uint8_t*)slot - owner->base));
        }
    }
}

void gc_copy_drain(void) {
    for (;;) {
        gc_copy_chunk* chunk;

        gc_copy_flush_local();
        chunk = atomic_exchange_explicit(&gc_copy_active, NULL,
                                         memory_order_acquire);
        if (chunk == NULL) {
            return;
        }
        while (chunk != NULL) {
            gc_copy_chunk* next =
                atomic_load_explicit(&chunk->next, memory_order_relaxed);
            size_t i;

            for (i = 0; i < chunk->count; i++) {
                gc_scan_object_fields(chunk->objects[i], gc_evac_slot, NULL);
                gc_scan_object_weak(chunk->objects[i], gc_weak_slot, NULL);
                gc_mark_object_cards(chunk->objects[i]);
            }
            gc_chunk_release(chunk);
            chunk = next;
        }
    }
}

void gc_weak_slot(void* context, void** slot) {
    void* ref;
    void* target;

    (void)context;
    if (slot == NULL) {
        return;
    }
    ref = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    if (ref == NULL) {
        return;
    }
    target = WILLRT_GC_UNTAG(ref);
    if (!gc_object_is_forwarded(target, &target)) {
        gc_region* region = gc_region_by_address(target);

        if (region == NULL ||
            (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0 &&
             !gc_object_marked(target)) ||
            (region->kind == GC_REGION_YOUNG &&
             (region->flags & GC_REGION_EVACUATING) != 0)) {
            atomic_store_explicit((_Atomic(void*)*)slot, NULL,
                                  memory_order_release);
            return;
        }
    }
    target = gc_object_forward(target);
    if (target == NULL) {
        atomic_store_explicit((_Atomic(void*)*)slot, NULL,
                              memory_order_release);
        return;
    }
    atomic_store_explicit((_Atomic(void*)*)slot,
                          WILLRT_GC_TAG(target,
                                        atomic_load_explicit(
                                            &gc_good_color,
                                            memory_order_acquire)),
                          memory_order_release);
}

static void gc_weak_object(void* context, void* object) {
    (void)context;
    if (!gc_object_marked(object)) {
        return;
    }
    gc_scan_object_weak(object, gc_weak_slot, NULL);
}

void gc_weak_clear(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];

        if (region == NULL || region->kind == GC_REGION_FREE) {
            continue;
        }
        gc_region_scan_objects(region, gc_weak_object, NULL);
    }
}

static void gc_remap_object(void* context, void* object) {
    gc_region* region;
    willrt_gc_header* header;
    uint64_t state;

    (void)context;
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        return;
    }
    if (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0 &&
        !gc_object_marked(object)) {
        return;
    }
    region = gc_region_by_address(object);
    if (region == NULL || (region->flags & GC_REGION_EVACUATING) != 0) {
        return;
    }
    gc_scan_object_fields(object, gc_evac_slot, NULL);
    gc_scan_object_weak(object, gc_weak_slot, NULL);
    gc_mark_object_cards(object);
}

void gc_reclaim_nonmoving(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];
        size_t start;
        size_t end;
        void* object;
        int live = 0;

        if (region == NULL ||
            (region->kind != GC_REGION_LARGE &&
             region->kind != GC_REGION_PINNED) ||
            (region->flags & GC_REGION_RETIRED) != 0 ||
            atomic_load_explicit(&region->pin_count, memory_order_acquire) !=
                0) {
            continue;
        }
        if (!gc_region_object_bounds(region, 0, &start, &end)) {
            continue;
        }
        object = region->base + start + WILLRT_GC_OBJECT_PAYLOAD_OFFSET;
        live = gc_object_marked(object);
        if (!live && (end - start) != 0) {
            gc_region_free(region);
        }
    }
}

void gc_remap_heap(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];

        if (region == NULL || region->kind == GC_REGION_FREE) {
            continue;
        }
        gc_region_scan_objects(region, gc_remap_object, NULL);
    }
}


