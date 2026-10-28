/* Collector integration tests: young copying, promotion, full compaction,
   weak clearing, pinning, large objects, and concurrent allocation. */

#include "gc_internal.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static const uint32_t node_refs[] = {0};
static const uint32_t weak_refs[] = {0};
static const willrt_gc_type node_type = {
    1, sizeof(void*), sizeof(void*), WILLRT_GC_TYPE_REFERENCES, 1,
    node_refs, 0, NULL
};
static const willrt_gc_type weak_type = {
    2, sizeof(void*), sizeof(void*), 0, 0, NULL, 1, weak_refs
};
static const willrt_gc_type large_type = {
    3, 0, 8, WILLRT_GC_TYPE_LARGE, 0, NULL, 0, NULL
};
static const willrt_gc_type bulk_type = {
    4, 1024, 8, 0, 0, NULL, 0, NULL
};
static const willrt_gc_type aligned_type = {
    5, 24, 64, WILLRT_GC_TYPE_REFERENCES, 1, node_refs, 0, NULL
};
static const willrt_gc_type aligned_large_type = {
    6, 64, 4096, WILLRT_GC_TYPE_LARGE, 0, NULL, 0, NULL
};

static void* root_slot;

static void root_scan(void* context, willrt_gc_root_visit visit,
                      void* visit_context) {
    (void)context;
    visit(visit_context, &root_slot);
}

static void wait_idle(void) {
    while (!gc_cycle_idle()) {
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_BACKEDGE);
    }
}

static void collect(int full) {
    if (full) {
        gc_cycle_request_full();
    } else {
        gc_cycle_kick();
    }
    wait_idle();
}

static void* root_object(void* object) {
    root_slot = object;
    willrt_gc_root_register(root_scan, NULL);
    return object;
}

static void test_young_copy(void) {
    void* child;
    void* root;

    wait_idle();
    child = willrt_gc_alloc(sizeof(void*), &node_type);
    root = willrt_gc_alloc(sizeof(void*), &node_type);
    {
        void* old_child = child;
        void* old_root = root;
        (void)old_child;
        (void)old_root;
    }

    assert(child != NULL && root != NULL);
    *(void**)root = child;
    root_object(root);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    assert(*(void**)root_slot != NULL);
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_promotion_and_compaction(void) {
    void* root;

    wait_idle();
    root = willrt_gc_alloc(sizeof(void*), &node_type);
    assert(root != NULL);
    *(void**)root = root;
    root_object(root);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    collect(1);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    assert(WILLRT_GC_UNTAG(*(void**)root_slot) == WILLRT_GC_UNTAG(root_slot));
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_weak_clear(void) {
    void* holder;
    void* target;

    wait_idle();
    holder = willrt_gc_alloc(sizeof(void*), &weak_type);
    target = willrt_gc_alloc(sizeof(void*), &node_type);
    assert(holder != NULL && target != NULL);
    *(void**)holder = target;
    root_object(holder);
    collect(1);
    collect(1);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    assert(*(void**)root_slot == NULL);
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_pin(void) {
    void* object;
    void* pinned;
    void* before;

    wait_idle();
    object = willrt_gc_alloc(sizeof(void*), &node_type);
    before = object;
    assert(object != NULL);
    *(void**)object = NULL;
    root_object(object);
    pinned = willrt_gc_pin(object);
    assert(pinned == object);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(WILLRT_GC_UNTAG(root_slot) == before);
    willrt_gc_unpin(pinned);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    (void)before;
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_old_to_young_barrier(void) {
    void* holder;

    wait_idle();
    holder = willrt_gc_alloc(sizeof(void*), &node_type);
    assert(holder != NULL);
    root_object(holder);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    for (int i = 0; i < 4 &&
                    gc_region_by_address(WILLRT_GC_UNTAG(root_slot))->kind !=
                        GC_REGION_OLD;
         i++) {
        collect(0);
        root_slot = willrt_gc_read_barrier(root_slot);
    }
    {
        gc_region* holder_region =
            gc_region_by_address(WILLRT_GC_UNTAG(root_slot));
        void* target;
        void** field;

        assert(holder_region != NULL);
        assert(holder_region->kind == GC_REGION_OLD);
        target = willrt_gc_alloc(sizeof(void*), &node_type);
        field = (void**)root_slot;
        assert(target != NULL);
        *field = NULL;
        willrt_gc_write_barrier(field, target);
        collect(0);
        root_slot = willrt_gc_read_barrier(root_slot);
        assert(root_slot != NULL);
        field = (void**)root_slot;
        assert(*field != NULL);
    }
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_alignment(void) {
    void* aligned;
    void* large;

    wait_idle();
    aligned = willrt_gc_alloc(24, &aligned_type);
    large = willrt_gc_alloc(64, &aligned_large_type);
    assert(aligned != NULL && large != NULL);
    *(void**)aligned = NULL;
    assert(((uintptr_t)aligned % 64u) == 0);
    assert(((uintptr_t)large % 4096u) == 0);
    root_object(aligned);
    collect(0);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    assert(((uintptr_t)root_slot % 64u) == 0);
    willrt_gc_root_unregister(root_scan, NULL);
}

static void test_pinned_root(void) {
    void* parent;
    void* child;
    void** field;

    wait_idle();
    parent = willrt_gc_alloc(sizeof(void*), &node_type);
    child = willrt_gc_alloc(sizeof(void*), &node_type);
    assert(parent != NULL && child != NULL);
    *(void**)parent = child;
    *(void**)child = NULL;
    assert(willrt_gc_pin(parent) == parent);
    collect(0);
    parent = willrt_gc_read_barrier(parent);
    assert(parent != NULL);
    field = (void**)parent;
    assert(*field != NULL);
    willrt_gc_unpin(parent);
}

static void test_frame_root(void) {
    void* object;
    void* frame_root[1];
    void* before;
    willrt_gc_stack_slot slot = {0, WILLRT_GC_SLOT_REFERENCE, &node_type};
    willrt_gc_stack_map_data map = {NULL, &slot, 1};
    willrt_gc_frame frame;

    wait_idle();
    object = willrt_gc_alloc(sizeof(void*), &node_type);
    before = object;
    assert(object != NULL);
    *(void**)object = NULL;
    frame_root[0] = object;
    willrt_gc_frame_enter(&frame, &map, frame_root);
    collect(0);
    assert(frame_root[0] != NULL);
    (void)before;
    willrt_gc_frame_leave(&frame);
}

static void test_automatic_pacing(void) {
    int i;

    for (i = 0; i < 9000; i++) {
        void* object = willrt_gc_alloc(1024, &bulk_type);

        assert(object != NULL);
    }
    wait_idle();
}

static void test_large_object(void) {
    void* large;
    void* holder;
    void* before_large;

    wait_idle();
    large = willrt_gc_alloc(64u * 1024u, &large_type);
    holder = willrt_gc_alloc(sizeof(void*), &node_type);
    before_large = large;
    assert(large != NULL && holder != NULL);
    *(void**)holder = large;
    root_object(holder);
    collect(1);
    root_slot = willrt_gc_read_barrier(root_slot);
    assert(root_slot != NULL);
    assert(WILLRT_GC_UNTAG(*(void**)root_slot) == before_large);
    willrt_gc_root_unregister(root_scan, NULL);
}

#define STRESS_THREADS 4
#define STRESS_LOOPS 256

static void stress_worker(void* arg) {
    int32_t lane = (int32_t)(uintptr_t)arg;

    willrt_gc_lane_enter(lane);
    for (int i = 0; i < STRESS_LOOPS; i++) {
        void* object = willrt_gc_alloc(32, &node_type);
        willrt_gc_handle handle;

        assert(object != NULL);
        *(void**)object = NULL;
        handle = willrt_gc_handle_new(object);
        assert(handle != WILLRT_GC_HANDLE_NONE);
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_ALLOC);
        object = willrt_gc_handle_get(handle);
        assert(object != NULL);
        willrt_gc_handle_release(handle);
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_BACKEDGE);
    }
    willrt_gc_lane_leave();
}

static void test_concurrent_allocation(void) {
    willrt_os_thread threads[STRESS_THREADS];
    int i;

    for (i = 0; i < STRESS_THREADS; i++) {
        assert(willrt_os_thread_start(&threads[i], stress_worker,
                                      (void*)(uintptr_t)(i + 1)) == 0);
    }
    for (i = 0; i < 8; i++) {
        collect(i == 7);
    }
    for (i = 0; i < STRESS_THREADS; i++) {
        willrt_os_thread_join(&threads[i]);
    }
}

int main(void) {
    willrt_gc_lane_enter(0);
    assert(willrt_gc_register_type(&node_type) == 0);
    assert(willrt_gc_register_type(&weak_type) == 0);
    assert(willrt_gc_register_type(&large_type) == 0);
    assert(willrt_gc_register_type(&bulk_type) == 0);
    assert(willrt_gc_register_type(&aligned_type) == 0);
    assert(willrt_gc_register_type(&aligned_large_type) == 0);
    for (int round = 0; round < 20; round++) {
        test_young_copy();
        test_old_to_young_barrier();
        test_alignment();
        test_promotion_and_compaction();
        test_weak_clear();
        test_pin();
        test_pinned_root();
        test_frame_root();
        test_large_object();
        test_automatic_pacing();
        test_concurrent_allocation();
    }
    willrt_gc_shutdown();
    return 0;
}
