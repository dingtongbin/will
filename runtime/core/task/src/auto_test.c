/* Contract tests for the auto placement group and the inline result path. The
   driver exits zero when every assertion holds. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

typedef struct flag_frame {
    _Atomic(int32_t)* flag;
} flag_frame;

static int32_t step_set_flag(void* raw) {
    flag_frame* frame = (flag_frame*)raw;

    atomic_store(frame->flag, 1);
    return WILLRT_DONE;
}

typedef struct lane_frame {
    _Atomic(uint32_t)* bits;
} lane_frame;

static int32_t step_mark_lane(void* raw) {
    lane_frame* frame = (lane_frame*)raw;
    int32_t lane = willrt_lane_here();

    assert(lane >= 0);
    atomic_fetch_or(frame->bits, 1u << (uint32_t)(lane & 31));
    return WILLRT_DONE;
}

typedef struct hold_frame {
    _Atomic(int32_t)* entered;
    _Atomic(int32_t)* release;
} hold_frame;

static int32_t step_hold(void* raw) {
    hold_frame* frame = (hold_frame*)raw;

    atomic_store(frame->entered, 1);
    while (atomic_load(frame->release) == 0) {
        willrt_os_thread_yield();
    }
    return WILLRT_DONE;
}

typedef struct try_frame {
    int32_t calls;
    int32_t child_calls;
    willrt_task* child;
} try_frame;

static int32_t step_try_child(void* raw) {
    try_frame* frame = (try_frame*)raw;

    frame->child_calls += 1;
    if (frame->child_calls < 3) {
        return WILLRT_SUSPEND;
    }
    return WILLRT_DONE;
}

static int32_t step_region_try(void* raw) {
    try_frame* frame = (try_frame*)raw;
    int32_t status;

    frame->calls += 1;
    if (frame->calls == 1) {
        willrt_region_enter();
        frame->child = willrt_task_new((void*)step_try_child, frame,
                                       sizeof(*frame), 0, 0);
        assert(frame->child != NULL);
        willrt_task_run(frame->child);
    }
    status = willrt_region_exit_try();
    if (status == WILLRT_SUSPEND) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    assert(status == 0);
    return WILLRT_DONE;
}

typedef struct big_result {
    int32_t values[16];
} big_result;

static int32_t step_big_result(void* raw) {
    big_result* result = (big_result*)raw;

    for (int32_t i = 0; i < 16; i++) {
        result->values[i] = i * 3;
    }
    assert(willrt_task_store_result(result, sizeof(*result)) == WILLRT_OK);
    return WILLRT_DONE;
}

/* The submitter keeps running CPU work after an auto spawn. The task must be
   in its lane queue before run_auto returns, so the flag changes without a
   wait point that could flush a deferred batch. */
static void test_auto_runs_without_a_wait(void) {
    _Atomic(int32_t) flag;
    flag_frame frame;
    willrt_task* task;
    int32_t spins;

    atomic_init(&flag, 0);
    frame.flag = &flag;
    willrt_region_enter();
    task = willrt_task_new((void*)step_set_flag, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run_auto(task);
    for (spins = 0; spins < 1000000 && atomic_load(&flag) == 0; spins++) {
        willrt_os_thread_yield();
    }
    assert(atomic_load(&flag) == 1);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(willrt_region_exit() == 0);
}

/* One auto group has to reach more than one lane on a multi-core machine. */
static void test_auto_group_spreads(void) {
    enum { COUNT = 128 };
    _Atomic(uint32_t) bits;
    lane_frame frames[COUNT];
    willrt_task* tasks[COUNT];
    int32_t lanes = 0;
    int32_t i;

    atomic_init(&bits, 0);
    willrt_region_enter();
    for (i = 0; i < COUNT; i++) {
        frames[i].bits = &bits;
        tasks[i] = willrt_task_new((void*)step_mark_lane, &frames[i],
                                   sizeof(frames[i]), 0, 0);
        assert(tasks[i] != NULL);
        willrt_task_run_auto(tasks[i]);
    }
    for (i = 0; i < COUNT; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
    }
    assert(willrt_region_exit() == 0);
    for (i = 0; i < 32; i++) {
        if ((atomic_load(&bits) & (1u << (uint32_t)i)) != 0) {
            lanes++;
        }
    }
    if (willrt_os_cpu_count() >= 4) {
        assert(lanes >= 2);
    }
}

/* A task queued to a lane that is already running must be seen after the
   current step returns, without relying on a wake that the running lane would
   not consume. */
static void test_enqueue_while_lane_runs(void) {
    _Atomic(int32_t) entered;
    _Atomic(int32_t) release;
    _Atomic(int32_t) ran;
    hold_frame hold;
    flag_frame flag;
    willrt_task* holder;
    willrt_task* queued;

    atomic_init(&entered, 0);
    atomic_init(&release, 0);
    atomic_init(&ran, 0);
    hold.entered = &entered;
    hold.release = &release;
    flag.flag = &ran;
    willrt_region_enter();
    holder = willrt_task_new((void*)step_hold, &hold, sizeof(hold), 0, 0);
    assert(holder != NULL);
    willrt_task_run(holder);
    while (atomic_load(&entered) == 0) {
        willrt_os_thread_yield();
    }
    queued = willrt_task_new((void*)step_set_flag, &flag, sizeof(flag), 0, 0);
    assert(queued != NULL);
    willrt_task_run(queued);
    atomic_store(&release, 1);
    assert(willrt_join(holder, NULL, NULL) == WILLRT_OK);
    assert(willrt_join(queued, NULL, NULL) == WILLRT_OK);
    assert(atomic_load(&ran) == 1);
    assert(willrt_region_exit() == 0);
}

/* A result larger than the inline cell goes through the heap path and is
   released with the region. */
static void test_large_result(void) {
    big_result result;
    willrt_task* task;

    memset(&result, 0, sizeof(result));
    willrt_region_enter();
    task = willrt_task_new((void*)step_big_result, &result, sizeof(result), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    for (int32_t i = 0; i < 16; i++) {
        assert(result.values[i] == i * 3);
    }
    assert(willrt_region_exit() == 0);
}

/* A region exit from a task parks instead of blocking the lane. The child
   finishes only after the parent has parked, and the parent resumes at the
   same barrier call. */
static void test_region_exit_parks(void) {
    try_frame frame;
    willrt_task* parent;

    memset(&frame, 0, sizeof(frame));
    parent = willrt_task_new((void*)step_region_try, &frame, sizeof(frame), 0,
                             0);
    assert(parent != NULL);
    willrt_task_run(parent);
    assert(willrt_join(parent, NULL, NULL) == WILLRT_OK);
    assert(frame.child_calls == 3);
    assert(frame.calls >= 2);
}

/* Two tasks on one lane can each have a parked region. The region stack is
   task-local, so the second task's enter/exit must not pop the first one. */
static void test_two_task_region_stacks(void) {
    try_frame first;
    try_frame second;
    willrt_task* first_task;
    willrt_task* second_task;

    memset(&first, 0, sizeof(first));
    memset(&second, 0, sizeof(second));
    first_task = willrt_task_new((void*)step_region_try, &first,
                                 sizeof(first), 0, 0);
    assert(first_task != NULL);
    willrt_task_run(first_task);
    second_task = willrt_task_new((void*)step_region_try, &second,
                                  sizeof(second), 0, 0);
    assert(second_task != NULL);
    willrt_task_run(second_task);
    assert(willrt_join(first_task, NULL, NULL) == WILLRT_OK);
    assert(willrt_join(second_task, NULL, NULL) == WILLRT_OK);
    assert(first.child_calls == 3 && second.child_calls == 3);
    assert(first.calls >= 2 && second.calls >= 2);
}

int main(void) {
    willrt_init();
    test_auto_runs_without_a_wait();
    test_auto_group_spreads();
    test_enqueue_while_lane_runs();
    test_region_exit_parks();
    test_two_task_region_stacks();
    test_large_result();
    willrt_shutdown();
    return 0;
}
