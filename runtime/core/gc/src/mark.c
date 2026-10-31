/* Concurrent marking work. Mark bits live in the object header; work is held in
   dynamic chunks so neither a deep graph nor a wide graph can overflow a fixed
   stack. Mutators may mark from a read barrier; the GC worker drains the same
   lock-free queue. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

static _Atomic(gc_mark_chunk*) gc_mark_work_stack;
static _Thread_local gc_mark_chunk* gc_mark_local;
_Atomic uint32_t gc_mark_epoch;
static uint32_t gc_current_mark;

static void gc_mark_push_chunk(gc_mark_chunk* chunk) {
    gc_mark_chunk* head;

    if (chunk == NULL || chunk->count == 0) {
        return;
    }
    head = atomic_load_explicit(&gc_mark_work_stack, memory_order_relaxed);
    do {
        atomic_store_explicit(&chunk->next, head, memory_order_relaxed);
    } while (!atomic_compare_exchange_weak_explicit(
        &gc_mark_work_stack, &head, chunk, memory_order_release,
        memory_order_relaxed));
}

void gc_mark_flush_local(void) {
    if (gc_mark_local != NULL && gc_mark_local->count != 0) {
        gc_mark_chunk* chunk = gc_mark_local;

        gc_mark_local = NULL;
        gc_mark_push_chunk(chunk);
    }
}

static void gc_mark_push(void* object) {
    gc_mark_chunk* chunk = gc_mark_local;

    if (chunk == NULL || chunk->count == WILLRT_GC_WORK_CHUNK) {
        if (chunk != NULL) {
            gc_mark_push_chunk(chunk);
        }
        chunk = gc_chunk_acquire();
        gc_mark_local = chunk;
    }
    chunk->objects[chunk->count++] = object;
}

void gc_mark_object(void* object) {
    if (object == NULL) {
        return;
    }
    object = WILLRT_GC_UNTAG(object);
    for (;;) {
        willrt_gc_header* header;
        uint64_t state;

        if (gc_region_by_address(object) == NULL) {
            return;
        }
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state,
                                     memory_order_acquire);

        if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
            object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
            continue;
        }
        if (gc_object_mark(object, gc_current_mark)) {
            gc_mark_push(object);
        }
        return;
    }
}

void gc_mark_slot(void** slot) {
    if (slot != NULL) {
        gc_mark_object(atomic_load_explicit((_Atomic(void*)*)slot,
                                            memory_order_acquire));
    }
}

static void gc_mark_visit(void* context, void** slot) {
    (void)context;
    gc_mark_slot(slot);
}

static void gc_mark_scan(void* object) {
    gc_scan_object_fields(object, gc_mark_visit, NULL);
}

static void gc_mark_pinned_object(void* context, void* object) {
    willrt_gc_header* header;
    uint64_t state;

    (void)context;
    if (object == NULL || gc_region_by_address(object) == NULL) {
        return;
    }
    header = gc_header_of(object);
    state = atomic_load_explicit(&header->state, memory_order_acquire);
    if ((state & WILLRT_GC_STATE_FORWARDED) != 0) {
        object = WILLRT_GC_STATE_FORWARD_ADDRESS(state);
        header = gc_header_of(object);
        state = atomic_load_explicit(&header->state, memory_order_acquire);
    }
    if (WILLRT_GC_STATE_PIN(state) != 0) {
        gc_mark_object(object);
    }
}

void gc_mark_pinned_objects(void) {
    size_t granule;

    for (granule = 0; granule < gc_heap_granule_count; granule++) {
        gc_region* region = gc_region_table[granule];

        if (region == NULL || region->kind == GC_REGION_FREE) {
            continue;
        }
        gc_region_scan_objects(region, gc_mark_pinned_object, NULL);
    }
}

void gc_mark_drain(void) {
    for (;;) {
        gc_mark_chunk* chunk;

        gc_mark_flush_local();
        chunk = atomic_exchange_explicit(&gc_mark_work_stack, NULL,
                                         memory_order_acquire);
        if (chunk == NULL) {
            return;
        }
        while (chunk != NULL) {
            gc_mark_chunk* next =
                atomic_load_explicit(&chunk->next, memory_order_relaxed);
            size_t i;

            for (i = 0; i < chunk->count; i++) {
                gc_mark_scan(chunk->objects[i]);
            }
            gc_chunk_release(chunk);
            chunk = next;
        }
    }
}

void gc_mark_begin(uint32_t color) {
    uint32_t epoch = atomic_load_explicit(&gc_mark_epoch,
                                          memory_order_acquire) + 1u;

    if (epoch == 0u || (epoch & UINT32_C(0x80000000)) != 0) {
        epoch = 1u;
    }
    atomic_store_explicit(&gc_mark_epoch, epoch, memory_order_release);
    gc_current_mark = color & WILLRT_GC_STATE_MARK_MASK;
    atomic_store_explicit(&gc_mark_color, gc_current_mark,
                          memory_order_release);
    atomic_store_explicit(&gc_marking, 1, memory_order_release);
}

void gc_mark_end(void) {
    gc_mark_drain();
    atomic_store_explicit(&gc_marking, 0, memory_order_release);
    gc_current_mark = WILLRT_GC_MARK_UNMARKED;
    atomic_store_explicit(&gc_mark_color, gc_current_mark,
                          memory_order_release);
}

int gc_mark_active(void) {
    return atomic_load_explicit(&gc_marking, memory_order_acquire);
}

void gc_mark_stack(const willrt_gc_stack_map_data* map, void* frame) {
    size_t i;

    if (map == NULL || frame == NULL) {
        return;
    }
    for (i = 0; i < map->slot_count; i++) {
        const willrt_gc_stack_slot* slot = &map->slots[i];

        if ((slot->flags & WILLRT_GC_SLOT_REFERENCE) != 0) {
            void** address = (void**)((uint8_t*)frame + slot->slot);

            if ((slot->flags & WILLRT_GC_SLOT_INDIRECT) != 0) {
                address = *(void***)address;
            }
            if (address == NULL) {
                continue;
            }
            if ((slot->flags & WILLRT_GC_SLOT_INLINE) != 0) {
                willrt_gc_scan_value(address, slot->type, gc_mark_visit,
                                     NULL);
            } else {
                gc_mark_object(atomic_load_explicit(
                    (_Atomic(void*)*)address, memory_order_acquire));
            }
        }
    }
}

void gc_mark_init(void) {
    atomic_store_explicit(&gc_mark_work_stack, NULL, memory_order_release);
    atomic_store_explicit(&gc_mark_epoch, 1, memory_order_release);
    gc_current_mark = WILLRT_GC_MARK_UNMARKED;
}

void gc_mark_shutdown(void) {
    gc_mark_end();
    if (gc_mark_local != NULL) {
        gc_chunk_release(gc_mark_local);
        gc_mark_local = NULL;
    }
}
