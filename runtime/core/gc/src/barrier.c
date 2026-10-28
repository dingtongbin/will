/* Read and write barriers. The fast path removes the color bits; the write
   barrier publishes the new reference and records the old one for SATB. The
   marker consumes the SATB buffer in its own module. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>

#define WILLRT_GC_SATB_CAP 1024u
#define WILLRT_GC_CARD_SIZE 512u
#define WILLRT_GC_CARD_COUNT (WILLRT_GC_ARENA_SIZE / WILLRT_GC_CARD_SIZE)

static _Atomic unsigned char gc_cards[WILLRT_GC_CARD_COUNT];
static atomic_size_t gc_card_count;
static willrt_os_mutex gc_satb_lock;
static atomic_int gc_satb_state;
static void* gc_satb[WILLRT_GC_SATB_CAP];
static size_t gc_satb_count;
static size_t gc_satb_overflow;

static void gc_satb_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_satb_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_satb_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_satb_lock);
        atomic_store_explicit(&gc_satb_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_satb_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

void* willrt_gc_read_barrier(const void* ref) {
    return WILLRT_GC_UNTAG(ref);
}

void willrt_gc_card_mark(void* slot) {
    size_t offset;

    if (!willrt_gc_heap_offset(slot, &offset)) {
        return;
    }
    offset /= WILLRT_GC_CARD_SIZE;
    if (offset < WILLRT_GC_CARD_COUNT &&
        atomic_exchange_explicit(&gc_cards[offset], 1,
                                 memory_order_relaxed) == 0) {
        atomic_fetch_add_explicit(&gc_card_count, 1, memory_order_relaxed);
    }
}

void willrt_gc_write_barrier(void** slot, void* value) {
    void* old;

    if (slot == NULL) {
        return;
    }
    old = *slot;
    if (old != NULL && old != value) {
        gc_satb_init();
        willrt_os_mutex_lock(&gc_satb_lock);
        if (gc_satb_count < WILLRT_GC_SATB_CAP) {
            gc_satb[gc_satb_count++] = old;
        } else {
            gc_satb_overflow++;
        }
        willrt_os_mutex_unlock(&gc_satb_lock);
    }
    willrt_gc_card_mark(slot);
    *slot = value;
}

void willrt_gc_satb_drain(willrt_gc_root_visit visit, void* context) {
    size_t i;

    gc_satb_init();
    willrt_os_mutex_lock(&gc_satb_lock);
    for (i = 0; i < gc_satb_count; i++) {
        visit(context, &gc_satb[i]);
    }
    gc_satb_count = 0;
    willrt_os_mutex_unlock(&gc_satb_lock);
}

int willrt_gc_test_card_marked(void* slot) {
    size_t offset;

    if (!willrt_gc_heap_offset(slot, &offset)) {
        return 0;
    }
    offset /= WILLRT_GC_CARD_SIZE;
    return offset < WILLRT_GC_CARD_COUNT
               ? atomic_load_explicit(&gc_cards[offset], memory_order_relaxed)
               : 0;
}

size_t willrt_gc_test_card_count(void) {
    return atomic_load_explicit(&gc_card_count, memory_order_relaxed);
}

size_t willrt_gc_test_satb_count(void) {
    return gc_satb_count;
}

size_t willrt_gc_test_satb_overflow(void) {
    return gc_satb_overflow;
}
