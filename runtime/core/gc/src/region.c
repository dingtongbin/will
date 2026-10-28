/* Region metadata and the buddy allocator. Regions are the only heap blocks:
   young, old, large, and pinned all come from the same view-mapped arena.
   Free regions are reused; nothing in this file calls mmap or munmap. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

gc_region** gc_region_table;
gc_region** gc_granule_table;
uint8_t* gc_heap_base;
size_t gc_heap_size;
size_t gc_heap_granule_count;

static gc_region* gc_free_lists[WILLRT_GC_REGION_ORDER_MAX + 1u];
static willrt_os_mutex gc_region_lock;

static size_t gc_bitmap_words(size_t size) {
    return (size / WILLRT_GC_OBJECT_ALIGN + 63u) / 64u;
}

static size_t gc_card_words(size_t size) {
    size_t cards = (size + WILLRT_GC_CARD_SIZE - 1u) / WILLRT_GC_CARD_SIZE;

    return (cards + 63u) / 64u;
}

static void gc_bitmap_clear(_Atomic uint64_t* bits, size_t words) {
    size_t i;

    for (i = 0; i < words; i++) {
        atomic_store_explicit(&bits[i], 0, memory_order_relaxed);
    }
}

static void gc_bitmap_set(_Atomic uint64_t* bits, size_t bit) {
    atomic_fetch_or_explicit(&bits[bit >> 6], UINT64_C(1) << (bit & 63u),
                             memory_order_release);
}

static int gc_bitmap_test(_Atomic uint64_t* bits, size_t bit) {
    return (atomic_load_explicit(&bits[bit >> 6], memory_order_acquire) &
            (UINT64_C(1) << (bit & 63u))) != 0;
}

static int gc_bitmap_next(_Atomic uint64_t* bits, size_t from, size_t limit,
                          size_t* found) {
    size_t word;
    uint64_t value;

    if (from >= limit) {
        return 0;
    }
    word = from >> 6;
    value = atomic_load_explicit(&bits[word], memory_order_acquire);
    value &= ~UINT64_C(0) << (from & 63u);
    for (;;) {
        if (value != 0) {
            size_t bit = (word << 6) + (size_t)__builtin_ctzll(value);

            if (bit >= limit) {
                return 0;
            }
            *found = bit;
            return 1;
        }
        word++;
        if ((word << 6) >= limit) {
            return 0;
        }
        value = atomic_load_explicit(&bits[word], memory_order_acquire);
    }
}

static gc_region* gc_region_new(uint32_t id, size_t order, uint32_t kind) {
    gc_region* region = (gc_region*)willrt_alloc(sizeof(*region));

    if (region == NULL) {
        return NULL;
    }
    memset(region, 0, sizeof(*region));
    region->id = id;
    region->order = order;
    region->size = (size_t)WILLRT_GC_GRANULE_SIZE << order;
    region->base = gc_heap_base + ((size_t)id << WILLRT_GC_GRANULE_SHIFT);
    region->kind = kind;
    atomic_init(&region->top, 0);
    atomic_init(&region->pin_count, 0);
    atomic_init(&region->evac_state, 0);
    atomic_init(&region->live_bytes, 0);
    region->free_next = UINT32_MAX;
    return region;
}

static void gc_region_destroy(gc_region* region) {
    if (region == NULL) {
        return;
    }
    if (region->start_bits != NULL) {
        willrt_free(region->start_bits);
    }
    if (region->end_bits != NULL) {
        willrt_free(region->end_bits);
    }
    if (region->mark_bits != NULL) {
        willrt_free(region->mark_bits);
    }
    if (region->card_bits != NULL) {
        willrt_free(region->card_bits);
    }
    if (region->card_objects != NULL) {
        willrt_free(region->card_objects);
    }
    willrt_free(region);
}

static int gc_region_prepare(gc_region* region) {
    size_t words = gc_bitmap_words(region->size);
    size_t cards = (region->size + WILLRT_GC_CARD_SIZE - 1u) /
                   WILLRT_GC_CARD_SIZE;
    size_t card_words = gc_card_words(region->size);

    region->start_bits = (_Atomic uint64_t*)willrt_alloc(
        words * sizeof(*region->start_bits));
    region->end_bits = (_Atomic uint64_t*)willrt_alloc(
        words * sizeof(*region->end_bits));
    region->mark_bits = (_Atomic uint64_t*)willrt_alloc(
        words * sizeof(*region->mark_bits));
    region->card_bits = (_Atomic uint64_t*)willrt_alloc(
        card_words * sizeof(*region->card_bits));
    region->card_objects = (_Atomic uint32_t*)willrt_alloc(
        cards * sizeof(*region->card_objects));
    if (region->start_bits == NULL || region->end_bits == NULL ||
        region->mark_bits == NULL || region->card_bits == NULL ||
        region->card_objects == NULL) {
        return -1;
    }
    region->bitmap_words = words;
    region->card_count = cards;
    gc_bitmap_clear(region->start_bits, words);
    gc_bitmap_clear(region->end_bits, words);
    gc_bitmap_clear(region->mark_bits, words);
    gc_bitmap_clear(region->card_bits, card_words);
    memset(region->card_objects, 0, cards * sizeof(*region->card_objects));
    return 0;
}

static void gc_region_map(gc_region* region) {
    size_t count = (size_t)1u << region->order;
    size_t i;

    gc_region_table[region->id] = region;
    for (i = 0; i < count; i++) {
        gc_granule_table[(size_t)region->id + i] = region;
    }
}

static void gc_region_unmap(gc_region* region) {
    size_t count = (size_t)1u << region->order;
    size_t i;

    if (gc_region_table[region->id] == region) {
        gc_region_table[region->id] = NULL;
    }
    for (i = 0; i < count; i++) {
        size_t id = (size_t)region->id + i;

        if (gc_granule_table[id] == region) {
            gc_granule_table[id] = NULL;
        }
    }
}

static void gc_free_push(gc_region* region) {
    size_t order = region->order;

    region->free_next = gc_free_lists[order] == NULL
                            ? UINT32_MAX
                            : gc_free_lists[order]->id;
    gc_free_lists[order] = region;
}

static gc_region* gc_free_pop(size_t order) {
    gc_region* region = gc_free_lists[order];

    if (region != NULL) {
        gc_free_lists[order] =
            region->free_next == UINT32_MAX
                ? NULL
                : gc_region_table[region->free_next];
    }
    return region;
}

static void gc_free_remove(gc_region* region) {
    size_t order = region->order;
    gc_region* current = gc_free_lists[order];
    gc_region* previous = NULL;

    while (current != NULL) {
        if (current == region) {
            if (previous == NULL) {
                gc_free_lists[order] =
                    current->free_next == UINT32_MAX
                        ? NULL
                        : gc_region_table[current->free_next];
            } else {
                previous->free_next = current->free_next;
            }
            return;
        }
        previous = current;
        current = current->free_next == UINT32_MAX
                      ? NULL
                      : gc_region_table[current->free_next];
    }
}

void gc_region_init(void) {
    size_t order = 0;
    size_t count;

    willrt_os_mutex_init(&gc_region_lock);
    gc_heap_granule_count = gc_heap_size / WILLRT_GC_GRANULE_SIZE;
    gc_region_table = (gc_region**)willrt_alloc(
        gc_heap_granule_count * sizeof(*gc_region_table));
    gc_granule_table = (gc_region**)willrt_alloc(
        gc_heap_granule_count * sizeof(*gc_granule_table));
    if (gc_region_table == NULL || gc_granule_table == NULL) {
        abort();
    }
    memset(gc_region_table, 0, gc_heap_granule_count * sizeof(*gc_region_table));
    memset(gc_granule_table, 0,
           gc_heap_granule_count * sizeof(*gc_granule_table));
    while (((size_t)1u << (order + 1u)) <= gc_heap_granule_count &&
           order < WILLRT_GC_REGION_ORDER_MAX) {
        order++;
    }
    if (((size_t)1u << order) != gc_heap_granule_count) {
        abort();
    }
    count = 1;
    {
        gc_region* whole = gc_region_new(0, order, GC_REGION_FREE);

        if (whole == NULL) {
            abort();
        }
        gc_free_push(whole);
    }
    (void)count;
}

void gc_region_shutdown(void) {
    size_t i;

    if (gc_region_table == NULL) {
        return;
    }
    for (i = 0; i < gc_heap_granule_count; i++) {
        gc_region* region = gc_region_table[i];

        if (region != NULL) {
            gc_region_destroy(region);
            gc_region_table[i] = NULL;
        }
    }
    willrt_free(gc_region_table);
    willrt_free(gc_granule_table);
    gc_region_table = NULL;
    gc_granule_table = NULL;
    memset(gc_free_lists, 0, sizeof(gc_free_lists));
}

gc_region* gc_region_alloc(size_t order, uint32_t kind) {
    size_t wanted;
    gc_region* region = NULL;

    if (order > WILLRT_GC_REGION_ORDER_MAX) {
        return NULL;
    }
    willrt_os_mutex_lock(&gc_region_lock);
    for (wanted = order; wanted <= WILLRT_GC_REGION_ORDER_MAX; wanted++) {
        region = gc_free_pop(wanted);
        if (region != NULL) {
            break;
        }
    }
    if (region == NULL) {
        willrt_os_mutex_unlock(&gc_region_lock);
        return NULL;
    }
    while (region->order > order) {
        size_t half = region->order - 1u;
        size_t buddy_id = region->id + (uint32_t)((size_t)1u << half);
        gc_region* buddy = gc_region_new(buddy_id, half, GC_REGION_FREE);

        if (buddy == NULL) {
            gc_free_push(region);
            willrt_os_mutex_unlock(&gc_region_lock);
            return NULL;
        }
        region->order = half;
        region->size = (size_t)WILLRT_GC_GRANULE_SIZE << half;
        gc_region_map(buddy);
        gc_free_push(buddy);
    }
    region->kind = kind;
    atomic_store_explicit(&region->top, 0, memory_order_relaxed);
    atomic_store_explicit(&region->pin_count, 0, memory_order_relaxed);
    atomic_store_explicit(&region->evac_state, 0, memory_order_relaxed);
    atomic_store_explicit(&region->live_bytes, 0, memory_order_relaxed);
    region->age = 0;
    region->flags = kind == GC_REGION_LARGE ? GC_REGION_LARGE_FLAG : 0u;
    if (gc_region_prepare(region) != 0) {
        region->kind = GC_REGION_FREE;
        gc_free_push(region);
        willrt_os_mutex_unlock(&gc_region_lock);
        return NULL;
    }
    gc_region_map(region);
    willrt_os_mutex_unlock(&gc_region_lock);
    return region;
}

void gc_region_free(gc_region* region) {
    size_t order;
    size_t buddy_id;
    gc_region* buddy;

    if (region == NULL || region->kind == GC_REGION_FREE) {
        return;
    }
    willrt_os_mutex_lock(&gc_region_lock);
    if (region->start_bits != NULL) {
        willrt_free(region->start_bits);
        region->start_bits = NULL;
    }
    if (region->end_bits != NULL) {
        willrt_free(region->end_bits);
        region->end_bits = NULL;
    }
    if (region->mark_bits != NULL) {
        willrt_free(region->mark_bits);
        region->mark_bits = NULL;
    }
    if (region->card_bits != NULL) {
        willrt_free(region->card_bits);
        region->card_bits = NULL;
    }
    if (region->card_objects != NULL) {
        willrt_free(region->card_objects);
        region->card_objects = NULL;
    }
    region->kind = GC_REGION_FREE;
    region->flags = 0;
    atomic_store_explicit(&region->top, 0, memory_order_relaxed);
    order = region->order;
    while (order < WILLRT_GC_REGION_ORDER_MAX) {
        buddy_id = region->id ^ (uint32_t)((size_t)1u << order);
        if (buddy_id >= gc_heap_granule_count) {
            break;
        }
        buddy = gc_region_table[buddy_id];
        if (buddy == NULL || buddy->kind != GC_REGION_FREE ||
            buddy->order != order) {
            break;
        }
        gc_free_remove(buddy);
        gc_region_unmap(region);
        gc_region_unmap(buddy);
        gc_region_destroy(buddy);
        if (region->id > buddy_id) {
            region->id = buddy_id;
            region->base = gc_heap_base +
                           ((size_t)buddy_id << WILLRT_GC_GRANULE_SHIFT);
        }
        order++;
        region->order = order;
        region->size = (size_t)WILLRT_GC_GRANULE_SIZE << order;
    }
    gc_region_map(region);
    gc_free_push(region);
    willrt_os_mutex_unlock(&gc_region_lock);
}

gc_region* gc_region_by_address(const void* address) {
    uintptr_t value = (uintptr_t)address;
    uintptr_t base = (uintptr_t)gc_heap_base;
    size_t granule;
    gc_region* region;

    if (gc_heap_base == NULL || value < base ||
        value >= base + gc_heap_size) {
        return NULL;
    }
    granule = (size_t)((value - base) >> WILLRT_GC_GRANULE_SHIFT);
    region = gc_granule_table[granule];
    return region != NULL && region->kind != GC_REGION_FREE ? region : NULL;
}

gc_region* gc_region_by_id(uint32_t id) {
    if (id >= gc_heap_granule_count) {
        return NULL;
    }
    return gc_region_table[id];
}

uint32_t gc_region_id(const gc_region* region) {
    return region->id;
}

void gc_region_set_start(gc_region* region, size_t offset) {
    gc_bitmap_set(region->start_bits, offset / WILLRT_GC_OBJECT_ALIGN);
}

void gc_region_set_end(gc_region* region, size_t offset) {
    if (offset < region->size) {
        gc_bitmap_set(region->end_bits, offset / WILLRT_GC_OBJECT_ALIGN);
    }
}

int gc_region_object_bounds(gc_region* region, size_t offset, size_t* start,
                            size_t* end) {
    size_t top = atomic_load_explicit(&region->top, memory_order_acquire);
    size_t bit = offset / WILLRT_GC_OBJECT_ALIGN;
    size_t limit = top / WILLRT_GC_OBJECT_ALIGN;
    size_t found;

    if (offset >= top || !gc_bitmap_test(region->start_bits, bit)) {
        return 0;
    }
    if (start != NULL) {
        *start = offset;
    }
    if (gc_bitmap_next(region->end_bits, bit + 1u, limit + 1u, &found)) {
        if (end != NULL) {
            *end = found * WILLRT_GC_OBJECT_ALIGN;
        }
    } else if (end != NULL) {
        *end = top;
    }
    return 1;
}

void gc_region_note_card(gc_region* region, size_t offset) {
    size_t card = offset / WILLRT_GC_CARD_SIZE;

    if (card < region->card_count) {
        gc_bitmap_set(region->card_bits, card);
    }
}

void gc_region_scan_objects(gc_region* region, void (*visit)(void*, void*),
                            void* context) {
    size_t top = atomic_load_explicit(&region->top, memory_order_acquire);
    size_t limit = top / WILLRT_GC_OBJECT_ALIGN;
    size_t bit = 0;
    size_t found;

    while (gc_bitmap_next(region->start_bits, bit, limit, &found)) {
        void* object = region->base + found * WILLRT_GC_OBJECT_ALIGN +
                      WILLRT_GC_OBJECT_PAYLOAD_OFFSET;

        visit(context, object);
        bit = found + 1u;
    }
}

void gc_region_clear_metadata(gc_region* region) {
    if (region->start_bits != NULL) {
        gc_bitmap_clear(region->start_bits, region->bitmap_words);
    }
    if (region->end_bits != NULL) {
        gc_bitmap_clear(region->end_bits, region->bitmap_words);
    }
    if (region->mark_bits != NULL) {
        gc_bitmap_clear(region->mark_bits, region->bitmap_words);
    }
    if (region->card_bits != NULL) {
        gc_bitmap_clear(region->card_bits, gc_card_words(region->size));
    }
    if (region->card_objects != NULL) {
        memset(region->card_objects, 0,
               region->card_count * sizeof(*region->card_objects));
    }
    atomic_store_explicit(&region->top, 0, memory_order_release);
    atomic_store_explicit(&region->live_bytes, 0, memory_order_release);
    atomic_store_explicit(&region->evac_state, 0, memory_order_release);
    region->age = 0;
    region->flags = 0;
}

void gc_region_add_live(gc_region* region, size_t bytes) {
    atomic_fetch_add_explicit(&region->live_bytes, bytes, memory_order_relaxed);
}

int gc_region_is_marked(gc_region* region, size_t offset) {
    uint32_t epoch = atomic_load_explicit(&gc_mark_epoch, memory_order_acquire);
    size_t bit;

    if (region == NULL || offset >= region->size ||
        atomic_load_explicit(&region->mark_epoch, memory_order_acquire) !=
            epoch) {
        return 0;
    }
    bit = offset / WILLRT_GC_OBJECT_ALIGN;
    return (atomic_load_explicit(&region->mark_bits[bit >> 6],
                                 memory_order_acquire) &
            (UINT64_C(1) << (bit & 63u))) != 0;
}

int gc_region_mark(gc_region* region, size_t offset) {
    uint32_t epoch;
    uint32_t state;
    size_t bit;
    uint64_t old;

    if (region == NULL || offset >= region->size) {
        return 0;
    }
    epoch = atomic_load_explicit(&gc_mark_epoch, memory_order_acquire);
    for (;;) {
        state = atomic_load_explicit(&region->mark_epoch,
                                     memory_order_acquire);
        if (state == epoch) {
            break;
        }
        if ((state & UINT32_C(0x80000000)) != 0) {
            willrt_os_thread_yield();
            continue;
        }
        if (atomic_compare_exchange_weak_explicit(
                &region->mark_epoch, &state, state | UINT32_C(0x80000000),
                memory_order_acq_rel, memory_order_acquire)) {
            gc_bitmap_clear(region->mark_bits, region->bitmap_words);
            atomic_store_explicit(&region->mark_epoch, epoch,
                                  memory_order_release);
            break;
        }
    }
    bit = offset / WILLRT_GC_OBJECT_ALIGN;
    old = atomic_fetch_or_explicit(&region->mark_bits[bit >> 6],
                                   UINT64_C(1) << (bit & 63u),
                                   memory_order_acq_rel);
    return (old & (UINT64_C(1) << (bit & 63u))) == 0;
}
