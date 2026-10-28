/* Pooled work chunks for mark, SATB, and copy queues. Chunks are reused for the
   process lifetime; a cycle never returns a chunk to the native allocator while
   another lane could still hold it. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdlib.h>

static willrt_os_mutex gc_chunk_lock;
static atomic_int gc_chunk_ready;
static gc_work_chunk* gc_chunk_free;

static void gc_chunk_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong_explicit(&gc_chunk_ready, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_chunk_lock);
        atomic_store_explicit(&gc_chunk_ready, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_chunk_ready, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

gc_work_chunk* gc_chunk_acquire(void) {
    gc_work_chunk* chunk;

    gc_chunk_init();
    willrt_os_mutex_lock(&gc_chunk_lock);
    chunk = gc_chunk_free;
    if (chunk != NULL) {
        gc_chunk_free =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);
    }
    willrt_os_mutex_unlock(&gc_chunk_lock);
    if (chunk == NULL) {
        chunk = (gc_work_chunk*)willrt_alloc(sizeof(*chunk));
        if (chunk == NULL) {
            gc_oom();
        }
    }
    atomic_store_explicit(&chunk->next, NULL, memory_order_relaxed);
    chunk->count = 0;
    return chunk;
}

void gc_chunk_release(gc_work_chunk* chunk) {
    if (chunk == NULL) {
        return;
    }
    gc_chunk_init();
    willrt_os_mutex_lock(&gc_chunk_lock);
    atomic_store_explicit(&chunk->next, gc_chunk_free,
                          memory_order_relaxed);
    gc_chunk_free = chunk;
    willrt_os_mutex_unlock(&gc_chunk_lock);
}

void gc_chunk_shutdown(void) {
    gc_work_chunk* chunk;

    if (atomic_load_explicit(&gc_chunk_ready, memory_order_acquire) != 2) {
        return;
    }
    willrt_os_mutex_lock(&gc_chunk_lock);
    chunk = gc_chunk_free;
    gc_chunk_free = NULL;
    willrt_os_mutex_unlock(&gc_chunk_lock);
    while (chunk != NULL) {
        gc_work_chunk* next =
            atomic_load_explicit(&chunk->next, memory_order_relaxed);

        willrt_free(chunk);
        chunk = next;
    }
}
