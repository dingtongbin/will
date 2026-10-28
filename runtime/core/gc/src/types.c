/* Static type descriptors. The compiler emits one descriptor per concrete GC
   type; the heap stores its id in the object state word and the marker reads
   the reference offsets back through this table. */

#include "gc_internal.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#define WILLRT_GC_TYPE_MAX 1024u

static willrt_os_mutex gc_type_lock;
static atomic_int gc_type_state;
static _Atomic(const willrt_gc_type*) gc_types[WILLRT_GC_TYPE_MAX];

static void gc_types_init(void) {
    int expected = 0;

    if (atomic_load_explicit(&gc_type_state, memory_order_acquire) == 2) {
        return;
    }
    if (atomic_compare_exchange_strong_explicit(&gc_type_state, &expected, 1,
                                                memory_order_acq_rel,
                                                memory_order_acquire)) {
        willrt_os_mutex_init(&gc_type_lock);
        atomic_store_explicit(&gc_type_state, 2, memory_order_release);
        return;
    }
    while (atomic_load_explicit(&gc_type_state, memory_order_acquire) != 2) {
        willrt_os_thread_yield();
    }
}

uint32_t gc_type_id(const willrt_gc_type* type) {
    size_t i;

    if (type == NULL) {
        return 0;
    }
    gc_types_init();
    willrt_os_mutex_lock(&gc_type_lock);
    for (i = 0; i < WILLRT_GC_TYPE_MAX; i++) {
        if (atomic_load_explicit(&gc_types[i], memory_order_relaxed) == type) {
            willrt_os_mutex_unlock(&gc_type_lock);
            return (uint32_t)(i + 1u);
        }
    }
    for (i = 0; i < WILLRT_GC_TYPE_MAX; i++) {
        if (atomic_load_explicit(&gc_types[i], memory_order_relaxed) == NULL) {
            atomic_store_explicit(&gc_types[i], type, memory_order_release);
            willrt_os_mutex_unlock(&gc_type_lock);
            return (uint32_t)(i + 1u);
        }
    }
    willrt_os_mutex_unlock(&gc_type_lock);
    abort();
}

const willrt_gc_type* gc_type_get(uint32_t id) {
    if (id == 0 || id > WILLRT_GC_TYPE_MAX) {
        return NULL;
    }
    return atomic_load_explicit(&gc_types[id - 1u], memory_order_acquire);
}
