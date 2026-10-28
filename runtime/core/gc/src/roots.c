/* Exact roots and opaque C handles. Both registries grow on demand; there is
   no fixed root or handle ceiling and no background reclaim of a live slot. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct gc_root {
    willrt_gc_root_scan scan;
    void* context;
    uint32_t active;
} gc_root;

typedef struct gc_handle_slot {
    void* object;
    uint32_t generation;
    uint32_t active;
    uint32_t next_free;
} gc_handle_slot;

static willrt_os_mutex gc_root_lock;
static willrt_os_mutex gc_handle_lock;
static atomic_int gc_root_state;
static atomic_int gc_handle_state;
static gc_root* gc_roots;
static size_t gc_root_capacity;
static gc_handle_slot* gc_handles;
static size_t gc_handle_capacity;
static uint32_t gc_handle_free;

static int gc_grow_roots(void) {
    size_t capacity = gc_root_capacity == 0 ? 16u : gc_root_capacity * 2u;
    gc_root* next = (gc_root*)willrt_alloc(capacity * sizeof(*next));

    if (next == NULL) {
        return -1;
    }
    memset(next, 0, capacity * sizeof(*next));
    if (gc_roots != NULL) {
        memcpy(next, gc_roots, gc_root_capacity * sizeof(*next));
        willrt_free(gc_roots);
    }
    gc_roots = next;
    gc_root_capacity = capacity;
    return 0;
}

static int gc_grow_handles(void) {
    size_t capacity = gc_handle_capacity == 0 ? 64u : gc_handle_capacity * 2u;
    gc_handle_slot* next =
        (gc_handle_slot*)willrt_alloc(capacity * sizeof(*next));
    size_t i;

    if (next == NULL) {
        return -1;
    }
    memset(next, 0, capacity * sizeof(*next));
    if (gc_handles != NULL) {
        memcpy(next, gc_handles, gc_handle_capacity * sizeof(*next));
        willrt_free(gc_handles);
    }
    for (i = gc_handle_capacity; i < capacity; i++) {
        next[i].next_free = (uint32_t)(i + 1u);
        next[i].active = 0;
    }
    next[capacity - 1u].next_free = UINT32_MAX;
    if (gc_handle_capacity == 0) {
        gc_handle_free = 0;
    } else if (gc_handle_free == UINT32_MAX) {
        gc_handle_free = (uint32_t)gc_handle_capacity;
    }
    gc_handles = next;
    gc_handle_capacity = capacity;
    return 0;
}

static void gc_roots_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_root_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_root_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_root_lock);
        atomic_store_explicit(&gc_root_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_root_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

static void gc_handles_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_handle_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_handle_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_handle_lock);
        atomic_store_explicit(&gc_handle_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_handle_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

void willrt_gc_root_register(willrt_gc_root_scan scan, void* context) {
    size_t i;
    size_t old_capacity;

    if (scan == NULL) {
        return;
    }
    gc_roots_init();
    willrt_os_mutex_lock(&gc_root_lock);
    for (i = 0; i < gc_root_capacity; i++) {
        if (!gc_roots[i].active) {
            gc_roots[i].scan = scan;
            gc_roots[i].context = context;
            gc_roots[i].active = 1;
            willrt_os_mutex_unlock(&gc_root_lock);
            return;
        }
    }
    old_capacity = gc_root_capacity;
    if (gc_grow_roots() != 0) {
        willrt_os_mutex_unlock(&gc_root_lock);
        abort();
    }
    gc_roots[old_capacity].scan = scan;
    gc_roots[old_capacity].context = context;
    gc_roots[old_capacity].active = 1;
    willrt_os_mutex_unlock(&gc_root_lock);
}

void willrt_gc_root_unregister(willrt_gc_root_scan scan, void* context) {
    size_t i;

    gc_roots_init();
    willrt_os_mutex_lock(&gc_root_lock);
    for (i = 0; i < gc_root_capacity; i++) {
        if (gc_roots[i].active && gc_roots[i].scan == scan &&
            gc_roots[i].context == context) {
            gc_roots[i].active = 0;
            gc_roots[i].scan = NULL;
            gc_roots[i].context = NULL;
            break;
        }
    }
    willrt_os_mutex_unlock(&gc_root_lock);
}

willrt_gc_handle willrt_gc_handle_new(void* object) {
    uint32_t index;
    uint32_t generation;
    willrt_gc_handle handle;

    if (object == NULL) {
        return WILLRT_GC_HANDLE_NONE;
    }
    object = WILLRT_GC_UNTAG(object);
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    if (gc_handles == NULL || gc_handle_free == UINT32_MAX) {
        if (gc_grow_handles() != 0) {
            willrt_os_mutex_unlock(&gc_handle_lock);
            return WILLRT_GC_HANDLE_NONE;
        }
    }
    index = gc_handle_free;
    gc_handle_free = gc_handles[index].next_free;
    generation = gc_handles[index].generation + 1u;
    if (generation == 0) {
        generation = 1u;
    }
    gc_handles[index].generation = generation;
    gc_handles[index].object = object;
    gc_handles[index].active = 1;
    handle = ((willrt_gc_handle)generation << 32) |
             (willrt_gc_handle)(index + 1u);
    willrt_os_mutex_unlock(&gc_handle_lock);
    return handle;
}

static gc_handle_slot* gc_handle_lookup(willrt_gc_handle handle,
                                        uint32_t* generation) {
    size_t index;
    uint32_t high = (uint32_t)(handle >> 32);
    uint32_t low = (uint32_t)handle;

    if (low == 0) {
        return NULL;
    }
    index = (size_t)low - 1u;
    if (index >= gc_handle_capacity) {
        return NULL;
    }
    if (!gc_handles[index].active || gc_handles[index].generation != high) {
        return NULL;
    }
    if (generation != NULL) {
        *generation = high;
    }
    return &gc_handles[index];
}

void* willrt_gc_handle_get(willrt_gc_handle handle) {
    gc_handle_slot* slot;
    void* object = NULL;

    if (handle == WILLRT_GC_HANDLE_NONE) {
        return NULL;
    }
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    slot = gc_handle_lookup(handle, NULL);
    if (slot != NULL) {
        object = slot->object;
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
    return object == NULL ? NULL : gc_read_barrier(object);
}

void willrt_gc_handle_release(willrt_gc_handle handle) {
    gc_handle_slot* slot;

    if (handle == WILLRT_GC_HANDLE_NONE) {
        return;
    }
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    slot = gc_handle_lookup(handle, NULL);
    if (slot != NULL) {
        size_t index = (size_t)(slot - gc_handles);

        slot->active = 0;
        slot->object = NULL;
        slot->next_free = gc_handle_free;
        gc_handle_free = (uint32_t)index;
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
}

void gc_roots_scan(willrt_gc_root_visit visit, void* context) {
    size_t i;

    gc_roots_init();
    willrt_os_mutex_lock(&gc_root_lock);
    for (i = 0; i < gc_root_capacity; i++) {
        if (gc_roots[i].active) {
            gc_roots[i].scan(gc_roots[i].context, visit, context);
        }
    }
    willrt_os_mutex_unlock(&gc_root_lock);
}

void gc_handles_scan(willrt_gc_root_visit visit, void* context) {
    size_t i;

    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    for (i = 0; i < gc_handle_capacity; i++) {
        if (gc_handles[i].active) {
            visit(context, &gc_handles[i].object);
        }
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
}
