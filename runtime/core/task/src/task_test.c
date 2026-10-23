/* Contract tests for tasks, regions, and channels. The process exits zero when
   every assertion holds and aborts through assert on the first failure. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int32_t step_double(void* frame) {
    int32_t value = *(int32_t*)frame;
    int32_t result = value * 2;

    assert(willrt_task_store_result(&result, sizeof(result)) == WILLRT_OK);
    return WILLRT_DONE;
}

typedef struct suspend_frame {
    int32_t calls;
    int32_t value;
} suspend_frame;

static int32_t step_suspend(void* raw) {
    suspend_frame* frame = (suspend_frame*)raw;
    int32_t result;

    frame->calls += 1;
    if (frame->calls < 3) {
        return WILLRT_SUSPEND;
    }
    result = frame->value;
    assert(willrt_task_store_result(&result, sizeof(result)) == WILLRT_OK);
    return WILLRT_DONE;
}

static int32_t step_panic(void* frame) {
    (void)frame;
    willrt_panic("task failed on purpose", "task_test.c:20:5", "will.test.task");
    return WILLRT_DONE;
}

static int32_t step_send(void* frame) {
    willrt_chan* chan = (willrt_chan*)frame;
    int32_t value = 41;

    assert(willrt_chan_send(chan, &value) == WILLRT_OK);
    return WILLRT_DONE;
}

static int32_t step_recv(void* frame) {
    willrt_chan* chan = (willrt_chan*)frame;
    int32_t value = 0;

    assert(willrt_chan_recv(chan, &value) == WILLRT_OK);
    assert(willrt_task_store_result(&value, sizeof(value)) == WILLRT_OK);
    return WILLRT_DONE;
}

static void test_task_result(void) {
    int32_t input = 21;
    int32_t output = 0;
    willrt_task* task = willrt_task_new((void*)step_double, &input, sizeof(input),
                                        willrt_lane_here(), 0);

    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &output, NULL) == WILLRT_OK);
    assert(output == 42);
}

static void test_task_suspend_resume(void) {
    suspend_frame frame = {0, 9};
    int32_t output = 0;
    willrt_task* task = willrt_task_new((void*)step_suspend, &frame,
                                        sizeof(frame), 0, 0);

    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &output, NULL) == WILLRT_OK);
    assert(output == 9);
    assert(frame.calls == 3);
}

static void test_region_counts(void) {
    int32_t input = 3;
    willrt_task* first;
    willrt_task* second;
    int32_t output = 0;

    willrt_region_enter();
    first = willrt_task_new((void*)step_double, &input, sizeof(input), 0, 0);
    second = willrt_task_new((void*)step_double, &input, sizeof(input), 0, 0);
    assert(first != NULL && second != NULL);
    willrt_task_run(first);
    willrt_task_run(second);
    assert(willrt_join(first, &output, NULL) == WILLRT_OK);
    assert(output == 6);
    assert(willrt_region_exit() == 0);
}

static void test_region_failure(void) {
    willrt_error err;
    willrt_task* task;

    memset(&err, 0, sizeof(err));
    willrt_region_enter();
    task = willrt_task_new((void*)step_panic, NULL, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, &err) == WILLRT_FAILED);
    assert(err.size == sizeof("task failed on purpose") - 1);
    assert(memcmp(err.message, "task failed on purpose", err.size) == 0);
    assert(willrt_region_exit() == 1);
}

static void test_panic_keeps_lanes(void) {
    int32_t lanes = willrt_os_cpu_count();
    int32_t input = 21;
    int32_t output = 0;
    willrt_task* task;

    if (lanes < 1) {
        lanes = 1;
    }
    /* Panic on every lane first. Before the setjmp boundary this killed each
       lane thread, so the ordinary task below never ran. */
    for (int32_t lane = 0; lane < lanes; lane++) {
        task = willrt_task_new((void*)step_panic, NULL, 0, lane, 0);
        assert(task != NULL);
        willrt_task_run(task);
        assert(willrt_join(task, NULL, NULL) == WILLRT_FAILED);
    }
    task = willrt_task_new((void*)step_double, &input, sizeof(input), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    for (int32_t attempt = 0; attempt < 1000; attempt++) {
        int32_t status = willrt_task_try_join(task, &output, NULL);

        if (status != WILLRT_SUSPEND) {
            assert(status == WILLRT_OK);
            assert(output == 42);
            return;
        }
        willrt_os_sleep_ns(INT64_C(1000000));
    }
    assert(0 && "a task after panics did not run");
}

static void test_channel_order(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 4, 0);
    int32_t value = 0;

    assert(chan != NULL);
    for (int32_t i = 1; i <= 4; i++) {
        assert(willrt_chan_send(chan, &i) == WILLRT_OK);
    }
    assert(willrt_chan_try_send(chan, &value) == WILLRT_FULL);
    for (int32_t i = 1; i <= 4; i++) {
        assert(willrt_chan_recv(chan, &value) == WILLRT_OK);
        assert(value == i);
    }
    assert(willrt_chan_try_recv(chan, &value) == WILLRT_EMPTY);
    willrt_chan_close(chan);
    willrt_chan_close(chan);
    assert(willrt_chan_recv(chan, &value) == WILLRT_CLOSED);
    assert(willrt_chan_send(chan, &value) == WILLRT_CLOSED);
}

static void test_channel_crosses_threads(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    int32_t value = 0;

    assert(chan != NULL);
    task = willrt_task_new((void*)step_send, chan, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_chan_recv(chan, &value) == WILLRT_OK);
    assert(value == 41);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
}

static void test_channel_hands_off_to_waiting_task(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    int32_t value = 17;
    int32_t output = 0;

    assert(chan != NULL);
    task = willrt_task_new((void*)step_recv, chan, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    willrt_os_sleep_ns(INT64_C(1000000));
    assert(willrt_chan_send(chan, &value) == WILLRT_OK);
    assert(willrt_join(task, &output, NULL) == WILLRT_OK);
    assert(output == 17);
}

typedef struct wait_frame {
    willrt_chan* chan;
    int32_t value;
    int32_t status;
    int32_t armed;
} wait_frame;

static int32_t step_send_wait(void* raw) {
    wait_frame* frame = (wait_frame*)raw;
    int32_t status = willrt_chan_send_wait(frame->chan, &frame->value);

    if (status == WILLRT_SUSPEND) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    frame->status = status;
    return WILLRT_DONE;
}

static int32_t step_recv_wait(void* raw) {
    wait_frame* frame = (wait_frame*)raw;
    int32_t status = willrt_chan_recv_wait(frame->chan, &frame->value);

    if (status == WILLRT_SUSPEND) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    frame->status = status;
    return WILLRT_DONE;
}

static int32_t step_recv_deadline(void* raw) {
    wait_frame* frame = (wait_frame*)raw;

    if (!frame->armed) {
        frame->armed = 1;
        (void)willrt_task_enter_deadline(INT64_C(1000000));
    }
    return step_recv_wait(raw);
}

static void test_channel_send_wait(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    wait_frame frame;
    int32_t first = 1;
    int32_t one = 0;
    int32_t two = 0;

    assert(chan != NULL);
    assert(willrt_chan_send(chan, &first) == WILLRT_OK);
    memset(&frame, 0, sizeof(frame));
    frame.chan = chan;
    frame.value = 7;
    task = willrt_task_new((void*)step_send_wait, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    willrt_os_sleep_ns(INT64_C(1000000));
    assert(willrt_chan_recv(chan, &one) == WILLRT_OK);
    assert(willrt_chan_recv(chan, &two) == WILLRT_OK);
    assert(one + two == 8);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_OK);
    willrt_chan_close(chan);
}

static void test_channel_wait_deadline(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    wait_frame frame;

    assert(chan != NULL);
    memset(&frame, 0, sizeof(frame));
    frame.chan = chan;
    task = willrt_task_new((void*)step_recv_deadline, &frame, sizeof(frame),
                           0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_TIMEDOUT);
    willrt_chan_close(chan);
}

static void test_channel_wait_cancel(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    wait_frame frame;
    int32_t flag = 0;
    void* flag_ptr = &flag;

    assert(chan != NULL);
    memset(&frame, 0, sizeof(frame));
    frame.chan = chan;
    task = willrt_task_new((void*)step_recv_wait, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_bind_cancel(task, &flag_ptr);
    willrt_task_run(task);
    willrt_os_sleep_ns(INT64_C(1000000));
    flag = 1;
    willrt_sched_wake_all();
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_CANCELED);
    willrt_chan_close(chan);
}

static void io_wake(void* arg) {
    (void)arg;
    willrt_os_sleep_ns(INT64_C(50000000));
    willrt_sched_wake_io();
}

static int32_t step_io_wait(void* raw) {
    int32_t* calls = (int32_t*)raw;

    *calls += 1;
    if (*calls == 1) {
        willrt_task_mark_io();
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    return WILLRT_DONE;
}

static void test_io_wait_is_not_deadlock(void) {
    willrt_os_thread thread;
    willrt_task* task;
    int32_t calls = 0;

    task = willrt_task_new((void*)step_io_wait, &calls, sizeof(calls), 0, 0);
    assert(task != NULL);
    assert(willrt_os_thread_start(&thread, io_wake, NULL) == 0);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    willrt_os_thread_join(&thread);
    assert(calls == 2);
}

/* A wake that lands while a task switches to the waiting state must not be
   lost. The task parks once and another thread keeps the runtime awake, so
   every task has to finish. */
typedef struct park_frame {
    int32_t calls;
} park_frame;

static volatile int32_t park_finished;
static volatile int32_t park_wake_calls;

static int32_t step_park_once(void* raw) {
    park_frame* frame = (park_frame*)raw;
    int32_t spin = 0;

    frame->calls += 1;
    if (frame->calls == 1) {
        while (park_wake_calls == 0 && spin < 1000000) {
            spin += 1;
        }
        assert(park_wake_calls > 0);
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    return WILLRT_DONE;
}

static void park_waker(void* arg) {
    (void)arg;
    while (!park_finished) {
        park_wake_calls += 1;
        willrt_sched_wake_all();
    }
}

static void test_wake_during_park_is_not_lost(void) {
    willrt_os_thread thread;
    int32_t index;

    park_finished = 0;
    assert(willrt_os_thread_start(&thread, park_waker, NULL) == 0);
    /* The waker must be observed running before the first task spins, or a
       slow start starves the spin budget and the step asserts. */
    while (park_wake_calls == 0) {
        willrt_os_thread_yield();
    }
    for (index = 0; index < 32; index++) {
        park_frame frame = {0};
        willrt_task* task = willrt_task_new((void*)step_park_once, &frame,
                                            sizeof(frame), 0, 0);
        assert(task != NULL);
        willrt_task_run(task);
        assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
        assert(frame.calls == 2);
    }
    park_finished = 1;
    willrt_os_thread_join(&thread);
}

static void test_select_hands_off_from_task(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    willrt_arm arm;
    int32_t out = 0;

    assert(chan != NULL);
    task = willrt_task_new((void*)step_send, chan, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    memset(&arm, 0, sizeof(arm));
    arm.chan = chan;
    arm.out = &out;
    arm.kind = 0;
    assert(willrt_select(&arm, 1) == 0);
    assert(out == 41);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
}

static void test_select_order(void) {
    willrt_chan* first = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_chan* second = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_arm arms[3];
    int32_t one = 1;
    int32_t two = 2;
    int32_t out = 0;

    assert(first != NULL && second != NULL);
    assert(willrt_chan_send(second, &two) == WILLRT_OK);
    memset(arms, 0, sizeof(arms));
    arms[0].chan = first;
    arms[0].out = &out;
    arms[0].kind = 0;
    arms[1].chan = second;
    arms[1].out = &out;
    arms[1].kind = 0;
    arms[2].kind = 3;
    assert(willrt_select(arms, 3) == 1);
    assert(out == 2);

    assert(willrt_chan_send(first, &one) == WILLRT_OK);
    arms[0].out = &out;
    assert(willrt_select(arms, 3) == 0);
    assert(out == 1);
    assert(willrt_select(arms, 3) == 2);
}

/* A parked task is woken only by its own dependency. Traffic on an unrelated
   channel must not re-enter the parked step; a wake that re-runs the step
   would unlink and requeue the wait node for no reason. */
typedef struct quiet_frame {
    willrt_chan* chan;
    int32_t value;
    int32_t status;
    int32_t entries;
    int64_t wake_ns;
} quiet_frame;

static int32_t step_recv_wait_counted(void* raw) {
    quiet_frame* frame = (quiet_frame*)raw;
    int32_t status = willrt_chan_recv_wait(frame->chan, &frame->value);

    frame->entries += 1;
    if (status == WILLRT_SUSPEND) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    frame->status = status;
    return WILLRT_DONE;
}

static void test_unrelated_send_wakes_no_parked_task(void) {
    willrt_chan* watched = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_chan* other = willrt_chan_new(sizeof(int32_t), 64, 0);
    quiet_frame frame;
    willrt_task* task;
    int32_t value;

    assert(watched != NULL && other != NULL);
    memset(&frame, 0, sizeof(frame));
    frame.chan = watched;
    task = willrt_task_new((void*)step_recv_wait_counted, &frame,
                           sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    willrt_os_sleep_ns(INT64_C(20000000));
    value = 0;
    for (int32_t i = 0; i < 32; i++) {
        assert(willrt_chan_send(other, &value) == WILLRT_OK);
    }
    willrt_os_sleep_ns(INT64_C(50000000));
    assert(frame.entries == 1);
    value = 7;
    assert(willrt_chan_send(watched, &value) == WILLRT_OK);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_OK);
    assert(frame.value == 7);
    willrt_chan_close(other);
}

/* The park timer wakes exactly the task whose deadline passed. Another parked
   task must not run its step again before its own timer fires. */
static int32_t step_timed_park_counted(void* raw) {
    quiet_frame* frame = (quiet_frame*)raw;

    frame->entries += 1;
    if (frame->entries == 1) {
        willrt_task_wait_until(willrt_now_ns() + frame->wake_ns);
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    return WILLRT_DONE;
}

static void test_timer_wakes_only_its_task(void) {
    quiet_frame early;
    quiet_frame late;
    willrt_task* early_task;
    willrt_task* late_task;

    memset(&early, 0, sizeof(early));
    memset(&late, 0, sizeof(late));
    early.wake_ns = INT64_C(20000000);
    late.wake_ns = INT64_C(400000000);
    early_task = willrt_task_new((void*)step_timed_park_counted, &early,
                                 sizeof(early), 0, 0);
    late_task = willrt_task_new((void*)step_timed_park_counted, &late,
                                sizeof(late), 0, 0);
    assert(early_task != NULL && late_task != NULL);
    willrt_task_run(early_task);
    willrt_task_run(late_task);
    willrt_os_sleep_ns(INT64_C(120000000));
    assert(willrt_join(early_task, NULL, NULL) == WILLRT_OK);
    assert(early.entries == 2);
    assert(late.entries == 1);
    assert(willrt_join(late_task, NULL, NULL) == WILLRT_OK);
    assert(late.entries == 2);
}

/* Suspension must not allocate once warm: a park deadline reuses the task's
   embedded timer and a select reuses the pooled context, so the counted
   allocations across the measured window are zero. */
typedef struct alloc_frame {
    int32_t entries;
    int64_t before;
    int64_t after;
    willrt_chan* chan;
    int32_t value;
} alloc_frame;

static int32_t step_park_count_allocs(void* raw) {
    alloc_frame* frame = (alloc_frame*)raw;

    frame->entries += 1;
    if (frame->entries == 1) {
        frame->before = willrt_alloc_count();
    }
    if (frame->entries <= 40) {
        willrt_task_wait_until(willrt_now_ns() + INT64_C(1000000));
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    frame->after = willrt_alloc_count();
    return WILLRT_DONE;
}

static void test_timed_park_allocates_nothing(void) {
    alloc_frame frame;
    willrt_task* task;

    memset(&frame, 0, sizeof(frame));
    task = willrt_task_new((void*)step_park_count_allocs, &frame,
                           sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.after == frame.before);
}

static int32_t step_select_count_allocs(void* raw) {
    alloc_frame* frame = (alloc_frame*)raw;
    willrt_arm arm;
    int32_t value = 5;

    memset(&arm, 0, sizeof(arm));
    arm.chan = frame->chan;
    arm.out = &frame->value;
    arm.kind = 0;
    for (int32_t round = 0; round < 2; round++) {
        if (round == 1) {
            frame->before = willrt_alloc_count();
        }
        for (int32_t i = 0; i < 40; i++) {
            willrt_select_ctx* ctx;
            int32_t index;

            assert(willrt_chan_send(frame->chan, &value) == WILLRT_OK);
            ctx = willrt_select_start(&arm, 1);
            assert(ctx != NULL);
            index = willrt_select_poll(ctx);
            assert(index == 0);
            willrt_select_finish(ctx);
        }
    }
    frame->after = willrt_alloc_count();
    return WILLRT_DONE;
}

static void test_select_reuse_allocates_nothing(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 64, 0);
    alloc_frame frame;
    willrt_task* task;

    assert(chan != NULL);
    memset(&frame, 0, sizeof(frame));
    frame.chan = chan;
    task = willrt_task_new((void*)step_select_count_allocs, &frame,
                           sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.after == frame.before);
    willrt_chan_close(chan);
}

int main(void) {
    willrt_init();
    test_task_result();
    test_task_suspend_resume();
    test_region_counts();
    test_region_failure();
    test_panic_keeps_lanes();
    test_channel_order();
    test_channel_crosses_threads();
    test_channel_hands_off_to_waiting_task();
    test_channel_send_wait();
    test_channel_wait_deadline();
    test_channel_wait_cancel();
    test_io_wait_is_not_deadlock();
    test_select_hands_off_from_task();
    test_select_order();
    test_wake_during_park_is_not_lost();
    test_unrelated_send_wakes_no_parked_task();
    test_timer_wakes_only_its_task();
    test_timed_park_allocates_nothing();
    test_select_reuse_allocates_nothing();
    willrt_shutdown();
    return 0;
}
