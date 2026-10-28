/* Runtime contract tests for exact roots, opaque handles, and the read/write
   barriers. Allocation is covered by heap_test.c. */

#include "gc_internal.h"

#include <assert.h>
#include <stdint.h>

static void root_scan(void* context, willrt_gc_root_visit visit,
                      void* visit_context) {
    (void)context;
    (void)visit;
    (void)visit_context;
}

static void test_roots(void) {
    int marker = 0;

    assert(willrt_gc_test_root_count() == 0);
    willrt_gc_root_register(root_scan, &marker);
    assert(willrt_gc_test_root_count() == 1);
    willrt_gc_root_unregister(root_scan, &marker);
    assert(willrt_gc_test_root_count() == 0);
}

static void test_handles(void) {
    void* object = willrt_gc_alloc(16, NULL);
    willrt_gc_handle first;
    willrt_gc_handle second;

    assert(object != NULL);
    first = willrt_gc_handle_new(object);
    assert(first != WILLRT_GC_HANDLE_NONE);
    assert(willrt_gc_handle_get(first) == object);
    assert(willrt_gc_test_handle_count() == 1);
    willrt_gc_handle_release(first);
    assert(willrt_gc_handle_get(first) == NULL);
    assert(willrt_gc_test_handle_count() == 0);

    second = willrt_gc_handle_new(object);
    assert(second != WILLRT_GC_HANDLE_NONE);
    assert(second != first);
    willrt_gc_handle_release(second);
}

static void test_barriers(void) {
    void* first = willrt_gc_alloc(16, NULL);
    void* second = willrt_gc_alloc(16, NULL);
    void* slot = first;
    void* tagged = (void*)((uintptr_t)second | WILLRT_GC_COLOR_MARKED0);

    assert(willrt_gc_read_barrier(tagged) == second);
    willrt_gc_write_barrier(&slot, tagged);
    assert(slot == tagged);
    assert(willrt_gc_test_satb_count() == 1);
    assert(willrt_gc_test_satb_overflow() == 0);
    willrt_gc_write_barrier(&slot, tagged);
    assert(willrt_gc_test_satb_count() == 1);
}

int main(void) {
    test_roots();
    test_handles();
    test_barriers();
    return 0;
}
