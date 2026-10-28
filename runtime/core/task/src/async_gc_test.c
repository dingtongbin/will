/* Async roots integration: a task frame and a channel ring are the only
   strong references to a node while a full concurrent cycle runs. A weak
   holder proves that the collector actually traced the roots and, after the
   task/channel disappears, that the node is reclaimed. */

#include "gc_internal.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static const uint32_t node_refs[] = {0};
static const uint32_t weak_refs[] = {0};

static const willrt_gc_type node_type = {
    100, sizeof(void*), sizeof(void*), WILLRT_GC_TYPE_REFERENCES, 1,
    node_refs, 0, NULL
};
static const willrt_gc_type weak_type = {
    101, sizeof(void*), sizeof(void*), 0, 0, NULL, 1, weak_refs
};

static const willrt_gc_stack_slot task_slots[] = {
    {0, WILLRT_GC_SLOT_REFERENCE, NULL}
};
static const willrt_gc_stack_map_data task_map = {
    NULL, task_slots, 1
};

static void* channel_holder;
static void* task_holder;
static void* result_holder;
static void* result_node;

static void channel_root_scan(void* context, willrt_gc_root_visit visit,
                              void* visit_context) {
    (void)context;
    visit(visit_context, &channel_holder);
}

static void task_root_scan(void* context, willrt_gc_root_visit visit,
                           void* visit_context) {
    (void)context;
    visit(visit_context, &task_holder);
}

static void result_root_scan(void* context, willrt_gc_root_visit visit,
                             void* visit_context) {
    (void)context;
    visit(visit_context, &result_holder);
}

static void wait_idle(void) {
    while (!gc_cycle_idle()) {
        willrt_gc_safepoint(WILLRT_GC_SAFEPOINT_BACKEDGE);
    }
}

static void collect_full(void) {
    gc_cycle_request_full();
    wait_idle();
}

static int32_t task_step(void* frame) {
    (void)frame;
    return WILLRT_DONE;
}

static int32_t result_step(void* frame) {
    (void)frame;
    return willrt_task_store_result(&result_node, sizeof(result_node)) ==
                   WILLRT_OK
               ? WILLRT_DONE
               : WILLRT_FAILED;
}

static void* weak_value(void* holder) {
    void* value = willrt_gc_read_barrier(holder);

    return value == NULL ? NULL : *(void**)value;
}

static void test_channel_root(void) {
    void* node;
    void* holder;
    willrt_chan* chan;

    wait_idle();
    node = willrt_gc_alloc(sizeof(void*), &node_type);
    holder = willrt_gc_alloc(sizeof(void*), &weak_type);
    assert(node != NULL && holder != NULL);
    *(void**)holder = node;
    channel_holder = holder;
    willrt_gc_root_register(channel_root_scan, NULL);

    chan = willrt_chan_new_gc(sizeof(void*), 4, 0, NULL, &node_type);
    assert(chan != NULL);
    assert(willrt_chan_send(chan, &node) == WILLRT_OK);
    node = NULL;

    collect_full();
    assert(weak_value(channel_holder) != NULL);

    willrt_chan_release(chan);
    collect_full();
    assert(weak_value(channel_holder) == NULL);

    willrt_gc_root_unregister(channel_root_scan, NULL);
    channel_holder = NULL;
}

static void test_task_root(void) {
    void* node;
    void* holder;
    void* frame;
    willrt_task* task;

    wait_idle();
    node = willrt_gc_alloc(sizeof(void*), &node_type);
    holder = willrt_gc_alloc(sizeof(void*), &weak_type);
    assert(node != NULL && holder != NULL);
    *(void**)holder = node;
    task_holder = holder;
    willrt_gc_root_register(task_root_scan, NULL);

    frame = willrt_alloc(sizeof(void*));
    assert(frame != NULL);
    *(void**)frame = node;
    node = NULL;

    willrt_region_enter();
    task = willrt_task_new_gc(task_step, frame, sizeof(void*), 0, 0, &task_map,
                              NULL);
    assert(task != NULL);
    willrt_task_own_frame(task);

    collect_full();
    assert(weak_value(task_holder) != NULL);

    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(willrt_region_exit() == 0);

    collect_full();
    assert(weak_value(task_holder) == NULL);

    willrt_gc_root_unregister(task_root_scan, NULL);
    task_holder = NULL;
}

static void test_task_result_root(void) {
    void* node;
    void* holder;
    void* frame;
    willrt_task* task;

    wait_idle();
    node = willrt_gc_alloc(sizeof(void*), &node_type);
    holder = willrt_gc_alloc(sizeof(void*), &weak_type);
    assert(node != NULL && holder != NULL);
    *(void**)holder = node;
    result_holder = holder;
    willrt_gc_root_register(result_root_scan, NULL);

    result_node = node;
    node = NULL;
    frame = willrt_alloc(1);
    assert(frame != NULL);

    willrt_region_enter();
    task = willrt_task_new_gc(result_step, frame, 1, 0, 0, NULL, &node_type);
    assert(task != NULL);
    willrt_task_own_frame(task);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    result_node = NULL;

    collect_full();
    assert(weak_value(result_holder) != NULL);

    assert(willrt_region_exit() == 0);
    collect_full();
    assert(weak_value(result_holder) == NULL);

    willrt_gc_root_unregister(result_root_scan, NULL);
    result_holder = NULL;
}

int main(void) {
    willrt_gc_lane_enter(0);
    assert(willrt_gc_register_type(&node_type) == 0);
    assert(willrt_gc_register_type(&weak_type) == 0);

    test_channel_root();
    test_task_root();
    test_task_result_root();

    willrt_gc_shutdown();
    return 0;
}
