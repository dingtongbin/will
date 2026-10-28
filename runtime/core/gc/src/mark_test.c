/* Precise marking tests: stack-map roots, registered roots, handles, SATB
   old values, and reference offsets from the type table. */

#include "gc_internal.h"

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

static const uint32_t root_reference_offsets[] = {0};
static const willrt_gc_type root_type = {
    sizeof(void*), sizeof(void*), WILLRT_GC_TYPE_REFERENCES, 1,
    root_reference_offsets
};
static const willrt_gc_type holder_type = {
    sizeof(void*), sizeof(void*), WILLRT_GC_TYPE_REFERENCES, 1,
    root_reference_offsets
};

static void* root_slot;

static void root_scan(void* context, willrt_gc_root_visit visit,
                      void* visit_context) {
    (void)context;
    visit(visit_context, &root_slot);
}

static void test_mark(void) {
    void* child = willrt_gc_alloc(8, NULL);
    void* root = willrt_gc_alloc(sizeof(void*), &root_type);
    void* unrelated = willrt_gc_alloc(8, NULL);
    void* stack_frame[1];
    willrt_gc_stack_slot slots[1] = {
        {0, WILLRT_GC_SLOT_REFERENCE, &root_type}
    };
    willrt_gc_stack_map_data map = {NULL, slots, 1};
    willrt_gc_frame_map_data frame_map = {&map, sizeof(stack_frame)};
    void* handle_object = willrt_gc_alloc(8, NULL);
    void* old_value = willrt_gc_alloc(8, NULL);
    void* new_value = willrt_gc_alloc(8, NULL);
    void* satb_slot = old_value;
    willrt_gc_handle handle;
    uint64_t root_state;

    assert(child != NULL && root != NULL && unrelated != NULL);
    *(void**)root = child;
    root_slot = root;
    stack_frame[0] = root;
    willrt_gc_root_register(root_scan, NULL);
    willrt_gc_stackmap(&map);
    willrt_gc_frame_map(&frame_map);
    handle = willrt_gc_handle_new(handle_object);
    assert(handle != WILLRT_GC_HANDLE_NONE);

    willrt_gc_test_begin_mark();
    willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_ALLOC, &map, stack_frame);
    willrt_gc_test_mark();
    root_state = atomic_load_explicit(
        &((willrt_gc_header*)((unsigned char*)root -
                               WILLRT_GC_OBJECT_PAYLOAD_OFFSET))
             ->state,
        memory_order_relaxed);
    assert(willrt_gc_state_type(root_state) != 0);
    assert(willrt_gc_test_marked(root) == 1);
    assert(willrt_gc_test_marked(child) == 1);
    assert(willrt_gc_test_marked(handle_object) == 1);
    assert(willrt_gc_test_marked(unrelated) == 0);

    willrt_gc_write_barrier(&satb_slot, new_value);
    willrt_gc_test_mark();
    assert(willrt_gc_test_marked(old_value) == 1);
    assert(willrt_gc_test_marked(new_value) == 0);

    {
        void* young = willrt_gc_alloc(8, NULL);
        void* holder = willrt_gc_alloc(sizeof(void*), &holder_type);
        void** holder_slot = (void**)holder;

        assert(young != NULL && holder != NULL);
        *holder_slot = NULL;
        root_slot = holder;
        willrt_gc_write_barrier(holder_slot, young);
        willrt_gc_test_mark();
        assert(willrt_gc_test_marked(holder) == 1);
        assert(willrt_gc_test_marked(young) == 1);
        assert(willrt_gc_test_card_count() == 0);
    }
    willrt_gc_test_end_mark();

    willrt_gc_handle_release(handle);
    willrt_gc_root_unregister(root_scan, NULL);
}

int main(void) {
    test_mark();
    assert(willrt_gc_test_mark_count() > 0);
    return 0;
}
