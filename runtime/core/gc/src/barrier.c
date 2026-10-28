/* Colored load/store barriers plus SATB and card work queues. The load path
   checks forwarding; the store path publishes a good-colored value and records
   old values and card dirt without taking a global lock. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

_Atomic uint32_t gc_good_color;
_Atomic uint32_t gc_mark_color;
_Atomic int gc_marking;
_Atomic int gc_evacuating;

static _Atomic(gc_satb_chunk*) gc_satb_active;
static _Thread_local gc_satb_chunk* gc_satb_local;

static void gc_satb_push_chunk(gc_satb_chunk* chunk) {
    gc_satb_chunk* head;

    if (chunk == NULL || chunk->count == 0) {
        return;
    }
    head = atomic_load_explicit(&gc_satb_active, memory_order_relaxed);
    do {
        atomic_store_explicit(&chunk->next, head, memory_order_relaxed);
    } while (!atomic_compare_exchange_weak_explicit(
        &gc_satb_active, &head, chunk, memory_order_release,
        memory_order_relaxed));
}

void gc_satb_flush_local(void) {
    if (gc_satb_local != NULL && gc_satb_local->count != 0) {
        gc_satb_chunk* chunk = gc_satb_local;

        gc_satb_local = NULL;
        gc_satb_push_chunk(chunk);
    }
}

static int gc_satb_record(void* object) {
    gc_satb_chunk* chunk;

    if (object == NULL) {
        return 0;
    }
    object = WILLRT_GC_UNTAG(object);
    chunk = gc_satb_local;
    if (chunk == NULL || chunk->count == WILLRT_GC_WORK_CHUNK) {
        if (chunk != NULL) {
            gc_satb_push_chunk(chunk);
        }
        chunk = (gc_satb_chunk*)willrt_alloc(sizeof(*chunk));
        if (chunk == NULL) {
            abort();
        }
        atomic_init(&chunk->next, NULL);
        chunk->count = 0;
        gc_satb_local = chunk;
    }
    chunk->objects[chunk->count++] = object;
    return 1;
}

void* gc_read_barrier(const void* ref) {
    void* object;
    willrt_gc_header* header;
    uint64_t state;
    uint32_t color;
    gc_region* region;

    if (ref == NULL) {
        return NULL;
    }
    object = WILLRT_GC_UNTAG(ref);
    color = WILLRT_GC_COLOR_OF(ref);
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) == 0 &&
        color == atomic_load_explicit(&gc_good_color, memory_order_acquire)) {
        return object;
    }
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        return WILLRT_GC_STATE_FORWARD_ADDRESS(state);
    }
    if (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0) {
        gc_mark_object(object);
    }
    if (atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0) {
        region = gc_region_by_address(object);
        if (region != NULL && region->kind == GC_REGION_YOUNG &&
            (region->flags & GC_REGION_EVACUATING) != 0) {
            void* forwarded = gc_object_forward(object);

            if (forwarded != NULL) {
                return forwarded;
            }
        }
    }
    return object;
}

void* gc_read_barrier_slot(void** slot) {
    void* ref;
    void* object;
    void* current;
    uint32_t good;

    if (slot == NULL) {
        return NULL;
    }
    ref = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    object = gc_read_barrier(ref);
    if (object == NULL) {
        return NULL;
    }
    good = atomic_load_explicit(&gc_good_color, memory_order_acquire);
    current = WILLRT_GC_TAG(object, good);
    atomic_compare_exchange_strong_explicit((_Atomic(void*)*)slot, &ref,
                                            current, memory_order_acq_rel,
                                            memory_order_acquire);
    return object;
}

void gc_write_barrier(void** slot, void* value) {
    void* old;
    void* stored;
    gc_region* region;
    size_t offset;
    uint32_t good;

    if (slot == NULL) {
        return;
    }
    old = atomic_load_explicit((_Atomic(void*)*)slot, memory_order_acquire);
    if (old != NULL && old != value &&
        (atomic_load_explicit(&gc_marking, memory_order_acquire) != 0 ||
         atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0)) {
        (void)gc_satb_record(old);
    }
    if (atomic_load_explicit(&gc_evacuating, memory_order_acquire) != 0) {
        gc_region* target;

        if (value != NULL) {
            target = gc_region_by_address(WILLRT_GC_UNTAG(value));
            if (target != NULL && target->kind == GC_REGION_YOUNG &&
                (target->flags & GC_REGION_EVACUATING) != 0) {
                void* forwarded = gc_object_forward(value);

                if (forwarded != NULL) {
                    value = WILLRT_GC_TAG(forwarded,
                                          atomic_load_explicit(
                                              &gc_good_color,
                                              memory_order_acquire));
                }
            }
        }
    }
    region = gc_region_by_address(slot);
    if (region != NULL && region->kind != GC_REGION_YOUNG) {
        offset = (size_t)((uint8_t*)slot - region->base);
        gc_region_note_card(region, offset);
    }
    good = atomic_load_explicit(&gc_good_color, memory_order_acquire);
    stored = value == NULL ? NULL : WILLRT_GC_TAG(WILLRT_GC_UNTAG(value), good);
    atomic_store_explicit((_Atomic(void*)*)slot, stored, memory_order_release);
}

void* willrt_gc_read_barrier(const void* ref) {
    return gc_read_barrier(ref);
}

void* willrt_gc_read_barrier_slot(void** slot) {
    return gc_read_barrier_slot(slot);
}

void willrt_gc_write_barrier(void** slot, void* value) {
    gc_write_barrier(slot, value);
}

void gc_satb_scan(willrt_gc_root_visit visit, void* context) {
    gc_satb_chunk* chunk;

    gc_satb_flush_local();
    chunk = atomic_exchange_explicit(&gc_satb_active, NULL,
                                     memory_order_acquire);
    while (chunk != NULL) {
        gc_satb_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);
        size_t i;

        for (i = 0; i < chunk->count; i++) {
            void* object = chunk->objects[i];

            visit(context, &object);
        }
        willrt_free(chunk);
        chunk = next;
    }
}

void gc_card_scan(willrt_gc_root_visit visit, void* context) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];
        size_t card;

        if (region == NULL || region->kind == GC_REGION_FREE ||
            region->kind == GC_REGION_YOUNG || region->card_bits == NULL) {
            continue;
        }
        for (card = 0; card < region->card_count; card++) {
            uint64_t word =
                atomic_load_explicit(&region->card_bits[card >> 6],
                                     memory_order_acquire);
            uint32_t object_offset;
            void* object;

            if ((word & (UINT64_C(1) << (card & 63u))) == 0) {
                continue;
            }
            object_offset =
                atomic_load_explicit(&region->card_objects[card],
                                     memory_order_acquire);
            object = region->base + object_offset;
            gc_scan_object_fields(object, visit, context);
        }
        for (card = 0; card < (region->card_count + 63u) / 64u; card++) {
            atomic_store_explicit(&region->card_bits[card], 0,
                                  memory_order_release);
        }
    }
}

void gc_barrier_init(void) {
    atomic_store_explicit(&gc_good_color, WILLRT_GC_COLOR_YOUNG,
                          memory_order_release);
    atomic_store_explicit(&gc_mark_color, WILLRT_GC_MARK_UNMARKED,
                          memory_order_release);
    atomic_store_explicit(&gc_marking, 0, memory_order_release);
    atomic_store_explicit(&gc_evacuating, 0, memory_order_release);
}

void gc_barrier_shutdown(void) {
    gc_satb_chunk* chunk = atomic_exchange_explicit(
        &gc_satb_active, NULL, memory_order_acquire);

    while (chunk != NULL) {
        gc_satb_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);

        willrt_free(chunk);
        chunk = next;
    }
    if (gc_satb_local != NULL) {
        willrt_free(gc_satb_local);
        gc_satb_local = NULL;
    }
}
