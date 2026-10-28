/* Exact roots and opaque C handles. The collector scans these registries; the
   allocation and barrier modules do not. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#define WILLRT_GC_ROOT_MAX 64u
#define WILLRT_GC_HANDLE_MAX 1024u

typedef struct gc_root {
    uint8_t active;
    willrt_gc_root_scan scan;
    void* context;
} gc_root;

typedef struct gc_handle_slot {
    uint8_t active;
    void* object;
    uint32_t generation;
} gc_handle_slot;

static willrt_os_mutex gc_root_lock;
static willrt_os_mutex gc_handle_lock;
static atomic_int gc_root_state;
static atomic_int gc_handle_state;
static gc_root gc_roots[WILLRT_GC_ROOT_MAX];
static gc_handle_slot gc_handles[WILLRT_GC_HANDLE_MAX];
static size_t gc_root_count;
static size_t gc_handle_count;

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

    if (scan == NULL) {
        return;
    }
    gc_roots_init();
    willrt_os_mutex_lock(&gc_root_lock);
    for (i = 0; i < WILLRT_GC_ROOT_MAX; i++) {
        if (!gc_roots[i].active) {
            gc_roots[i].active = 1;
            gc_roots[i].scan = scan;
            gc_roots[i].context = context;
            gc_root_count++;
            willrt_os_mutex_unlock(&gc_root_lock);
            return;
        }
    }
    willrt_os_mutex_unlock(&gc_root_lock);
    abort();
}

void willrt_gc_root_unregister(willrt_gc_root_scan scan, void* context) {
    size_t i;

    gc_roots_init();
    willrt_os_mutex_lock(&gc_root_lock);
    for (i = 0; i < WILLRT_GC_ROOT_MAX; i++) {
        if (gc_roots[i].active && gc_roots[i].scan == scan &&
            gc_roots[i].context == context) {
            gc_roots[i].active = 0;
            gc_roots[i].scan = NULL;
            gc_roots[i].context = NULL;
            gc_root_count--;
            break;
        }
    }
    willrt_os_mutex_unlock(&gc_root_lock);
}

willrt_gc_handle willrt_gc_handle_new(void* object) {
    size_t i;
    uint32_t generation;
    willrt_gc_handle handle;

    if (object == NULL) {
        return WILLRT_GC_HANDLE_NONE;
    }
    object = WILLRT_GC_UNTAG(object);
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    for (i = 0; i < WILLRT_GC_HANDLE_MAX; i++) {
        if (!gc_handles[i].active) {
            generation = gc_handles[i].generation + 1u;
            if (generation == 0) {
                generation = 1;
            }
            gc_handles[i].generation = generation;
            gc_handles[i].object = object;
            gc_handles[i].active = 1;
            gc_handle_count++;
            handle = ((willrt_gc_handle)generation << 32) |
                     (willrt_gc_handle)(i + 1u);
            willrt_os_mutex_unlock(&gc_handle_lock);
            return handle;
        }
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
    return WILLRT_GC_HANDLE_NONE;
}

void* willrt_gc_handle_get(willrt_gc_handle handle) {
    size_t index;
    uint32_t generation;
    void* object = NULL;

    if (handle == WILLRT_GC_HANDLE_NONE) {
        return NULL;
    }
    index = (size_t)(handle & 0xffffffffu);
    generation = (uint32_t)(handle >> 32);
    if (index == 0 || index > WILLRT_GC_HANDLE_MAX) {
        return NULL;
    }
    index--;
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    if (gc_handles[index].active &&
        gc_handles[index].generation == generation) {
        object = gc_handles[index].object;
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
    return object;
}

void willrt_gc_handle_release(willrt_gc_handle handle) {
    size_t index;
    uint32_t generation;

    if (handle == WILLRT_GC_HANDLE_NONE) {
        return;
    }
    index = (size_t)(handle & 0xffffffffu);
    generation = (uint32_t)(handle >> 32);
    if (index == 0 || index > WILLRT_GC_HANDLE_MAX) {
        return;
    }
    index--;
    gc_handles_init();
    willrt_os_mutex_lock(&gc_handle_lock);
    if (gc_handles[index].active &&
        gc_handles[index].generation == generation) {
        gc_handles[index].active = 0;
        gc_handles[index].object = NULL;
        gc_handle_count--;
    }
    willrt_os_mutex_unlock(&gc_handle_lock);
}

size_t willrt_gc_test_root_count(void) {
    return gc_root_count;
}

size_t willrt_gc_test_handle_count(void) {
    return gc_handle_count;
}
