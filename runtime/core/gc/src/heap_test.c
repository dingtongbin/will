/* Heap contract tests: payload alignment, region selection, TLAB refills,
   large and pinned regions, and the object pin count. */

#include "gc_internal.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static void test_small_allocations(void) {
    size_t before = willrt_gc_test_allocated_bytes();
    void* first = willrt_gc_alloc(24, NULL);
    void* second = willrt_gc_alloc(24, NULL);

    assert(first != NULL);
    assert(second != NULL);
    assert(((uintptr_t)first % WILLRT_GC_OBJECT_ALIGN) == 0);
    assert(((uintptr_t)second % WILLRT_GC_OBJECT_ALIGN) == 0);
    assert(first != second);
    memset(first, 0x5a, 24);
    memset(second, 0xa5, 24);
    assert(((unsigned char*)first)[0] == 0x5a);
    assert(((unsigned char*)second)[0] == 0xa5);
    assert(willrt_gc_test_allocated_bytes() > before);
    assert(willrt_gc_test_tlab_refills() >= 1);
}

static void test_large_region(void) {
    willrt_gc_type type = {0};
    size_t before = willrt_gc_test_large_regions();
    void* object;

    type.flags = WILLRT_GC_TYPE_LARGE;
    object = willrt_gc_alloc(WILLRT_GC_LARGE_THRESHOLD + 1, &type);
    assert(object != NULL);
    assert(willrt_gc_test_large_regions() == before + 1);
}

static void test_pinned_region_and_pin_count(void) {
    willrt_gc_type type = {0};
    willrt_gc_header* header;
    void* object;
    void* tagged;
    uint64_t state;

    type.flags = WILLRT_GC_TYPE_PINNED;
    object = willrt_gc_alloc(32, &type);
    assert(object != NULL);
    assert(willrt_gc_test_pinned_regions() >= 1);
    header = (willrt_gc_header*)((unsigned char*)object -
                                 WILLRT_GC_OBJECT_PAYLOAD_OFFSET);
    state = atomic_load_explicit(&header->state, memory_order_relaxed);
    assert((state & WILLRT_GC_STATE_PINNED_REGION) != 0);

    tagged = willrt_gc_pin(object);
    assert(tagged == object);
    state = atomic_load_explicit(&header->state, memory_order_relaxed);
    assert(willrt_gc_state_pin(state) == 1);
    willrt_gc_unpin(object);
    state = atomic_load_explicit(&header->state, memory_order_relaxed);
    assert(willrt_gc_state_pin(state) == 0);
    assert((state & WILLRT_GC_STATE_PINNED) == 0);
    assert((state & WILLRT_GC_STATE_PINNED_REGION) != 0);
}

static void test_colored_pointer_mask(void) {
    uintptr_t raw = (uintptr_t)0x4000u;
    void* colored = (void*)(raw | WILLRT_GC_COLOR_REMAPPED);

    assert(WILLRT_GC_COLOR_OF(colored) == WILLRT_GC_COLOR_REMAPPED);
    assert((uintptr_t)WILLRT_GC_UNTAG(colored) == raw);
}

#define GC_TEST_THREADS 4
#define GC_TEST_ALLOCS 256

static void worker(void* arg) {
    size_t i;

    (void)arg;
    for (i = 0; i < GC_TEST_ALLOCS; i++) {
        void* object = willrt_gc_alloc(32, NULL);
        void* pinned;

        assert(object != NULL);
        pinned = willrt_gc_pin(object);
        assert(pinned == object);
        willrt_gc_unpin(pinned);
    }
}

static void test_concurrent_allocation(void) {
    willrt_os_thread threads[GC_TEST_THREADS];
    int i;

    for (i = 0; i < GC_TEST_THREADS; i++) {
        assert(willrt_os_thread_start(&threads[i], worker, NULL) == 0);
    }
    for (i = 0; i < GC_TEST_THREADS; i++) {
        willrt_os_thread_join(&threads[i]);
    }
    assert(willrt_gc_test_allocated_bytes() >=
           (size_t)GC_TEST_THREADS * GC_TEST_ALLOCS * 40u);
}

int main(void) {
    willrt_os_page_size();
    test_small_allocations();
    test_large_region();
    test_pinned_region_and_pin_count();
    test_colored_pointer_mask();
    test_concurrent_allocation();
    assert(willrt_gc_test_region_size() >= willrt_gc_test_tlab_size());
    return 0;
}
