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

static void test_lane_choose(void) {
    int32_t cpus = willrt_os_cpu_count();
    uint32_t bits = 0;
    int32_t lanes = 0;
    int32_t i;

    if (cpus < 4) {
        return;
    }
    for (i = 0; i < 64; i++) {
        bits |= 1u << (uint32_t)(willrt_lane_choose() & 31);
    }
    for (i = 0; i < 32; i++) {
        if ((bits & (1u << (uint32_t)i)) != 0) {
            lanes++;
        }
    }
    assert(lanes >= 2);
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

static int32_t channel_drop_count;

static void drop_counted_int(void* value) {
    (void)value;
    channel_drop_count += 1;
}

static void test_channel_release_drops_queued_values(void) {
    willrt_chan* chan = willrt_chan_new_drop(sizeof(int32_t), 2, 0,
                                             drop_counted_int);
    int32_t one = 1;
    int32_t two = 2;
    int32_t got = 0;

    channel_drop_count = 0;
    assert(chan != NULL);
    assert(willrt_chan_send(chan, &one) == WILLRT_OK);
    assert(willrt_chan_send(chan, &two) == WILLRT_OK);
    willrt_chan_retain(chan);
    willrt_chan_release(chan);
    assert(willrt_chan_recv(chan, &got) == WILLRT_OK);
    assert(got == 1);
    assert(channel_drop_count == 0);
    willrt_chan_release(chan);
    assert(channel_drop_count == 1);
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

static int32_t step_cancel_at_wait(void* raw) {
    int32_t* calls = (int32_t*)raw;

    *calls += 1;
    if (willrt_task_canceled()) {
        return WILLRT_CANCELED;
    }
    willrt_task_mark_wait();
    return WILLRT_SUSPEND;
}

static void test_task_cancel_at_wait(void) {
    int32_t calls = 0;
    willrt_task* task = willrt_task_new((void*)step_cancel_at_wait, &calls,
                                        sizeof(calls), 0, 0);

    assert(task != NULL);
    willrt_task_run(task);
    willrt_os_sleep_ns(INT64_C(1000000));
    willrt_task_cancel(task);
    willrt_task_cancel(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_CANCELED);
    assert(calls >= 2);
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

/* The async poll contract runs through the same start/poll/finish trio the
   generator emits, so these tests drive hand-written two-state steps. The
   sockets exist because the Windows poller accepts sockets only. */

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET test_sock;
#define TEST_SOCK_INVALID INVALID_SOCKET
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int test_sock;
#define TEST_SOCK_INVALID (-1)
#endif

typedef struct test_poll_out {
    void* data;
    int64_t len;
    int64_t cap;
} test_poll_out;

static test_sock test_sock_open(void) {
#ifdef _WIN32
    return socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
#else
    return socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
#endif
}

static void test_sock_close(test_sock sock) {
#ifdef _WIN32
    closesocket(sock);
#else
    close(sock);
#endif
}

static test_sock test_listener_open(struct sockaddr_in* addr) {
    test_sock listener = test_sock_open();
#ifdef _WIN32
    int length = (int)sizeof(*addr);
#else
    socklen_t length = (socklen_t)sizeof(*addr);
#endif

    if (listener == TEST_SOCK_INVALID) {
        return TEST_SOCK_INVALID;
    }
    memset(addr, 0, sizeof(*addr));
    addr->sin_family = AF_INET;
    addr->sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr->sin_port = 0;
    if (bind(listener, (const struct sockaddr*)addr, sizeof(*addr)) != 0
        || listen(listener, 1) != 0
        || getsockname(listener, (struct sockaddr*)addr, &length) != 0) {
        test_sock_close(listener);
        return TEST_SOCK_INVALID;
    }
    return listener;
}

static test_sock test_dial(const struct sockaddr_in* addr) {
    test_sock sock = test_sock_open();

    if (sock == TEST_SOCK_INVALID) {
        return TEST_SOCK_INVALID;
    }
    if (connect(sock, (const struct sockaddr*)addr, sizeof(*addr)) != 0) {
        test_sock_close(sock);
        return TEST_SOCK_INVALID;
    }
    return sock;
}

static void test_sock_write(test_sock sock, const char* bytes, int size) {
    assert(send(sock, bytes, size, 0) == size);
}

typedef struct poll_frame {
    int64_t fds[1];
    uint32_t events[1];
    int64_t users[1];
    test_poll_out out;
    int64_t timeout_ns;
    int64_t scope_ns;
    int64_t scope_previous;
    int64_t alloc_before;
    int64_t alloc_after;
    willrt_poll_request* req;
    int32_t started;
    int32_t status;
} poll_frame;

static int32_t step_async_poll(void* raw) {
    poll_frame* f = (poll_frame*)raw;

    if (f->started == 0) {
        f->started = 1;
        f->alloc_before = willrt_alloc_count();
        if (f->scope_ns > 0) {
            f->scope_previous = willrt_task_enter_deadline(f->scope_ns);
        }
        f->req = willrt_poll_wait_async_start(f->fds, f->events, f->users, 1,
                                              f->timeout_ns, &f->out);
        assert(f->req != NULL);
    }
    if (willrt_poll_wait_async_poll(f->req) == 0) {
        willrt_task_wait_until(willrt_poll_wait_async_deadline(f->req));
        willrt_task_mark_io();
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status = willrt_poll_wait_async_finish(f->req);
    if (f->scope_ns > 0) {
        willrt_task_exit_deadline(f->scope_previous);
    }
    f->alloc_after = willrt_alloc_count();
    assert(willrt_task_store_result(&f->status, sizeof(f->status))
           == WILLRT_OK);
    return WILLRT_DONE;
}

static test_sock test_poll_pair(struct sockaddr_in* addr, test_sock* client) {
    test_sock listener = test_listener_open(addr);
    test_sock server;

    if (listener == TEST_SOCK_INVALID) {
        return TEST_SOCK_INVALID;
    }
    *client = test_dial(addr);
    if (*client == TEST_SOCK_INVALID) {
        test_sock_close(listener);
        return TEST_SOCK_INVALID;
    }
    server = accept(listener, NULL, NULL);
    if (server == TEST_SOCK_INVALID) {
        test_sock_close(*client);
        test_sock_close(listener);
        return TEST_SOCK_INVALID;
    }
    return server;
}

static void test_async_poll_delivers_readiness(void) {
    struct sockaddr_in addr;
    test_sock client;
    test_sock server = test_poll_pair(&addr, &client);
    int64_t tags[4];
    int64_t submitted_before;
    poll_frame frame;
    willrt_task* task;
    int32_t result = 0;

    assert(server != TEST_SOCK_INVALID);
    test_sock_write(client, "x", 1);
    memset(&frame, 0, sizeof(frame));
    frame.fds[0] = (int64_t)server;
    frame.events[0] = WILLRT_POLL_IN;
    frame.users[0] = 77;
    frame.out.data = tags;
    frame.out.cap = 4;
    frame.timeout_ns = INT64_C(2000000000);
    submitted_before = willrt_blocking_submitted();
    task = willrt_task_new((void*)step_async_poll, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_OK);
    assert(frame.out.len == 1);
    assert(tags[0] == 77);
    assert(willrt_blocking_submitted() == submitted_before);
    /* The first async poll pays the poller's one-time batch buffers, so the
       allocation-free contract belongs to the warmed timeout test. */
    test_sock_close(server);
    test_sock_close(client);
}

static void test_async_poll_times_out_allocating_nothing(void) {
    struct sockaddr_in addr;
    test_sock client;
    test_sock server = test_poll_pair(&addr, &client);
    int64_t tags[4];
    int64_t submitted_before;
    poll_frame frame;
    willrt_task* task;
    int32_t result = 0;

    assert(server != TEST_SOCK_INVALID);
    memset(&frame, 0, sizeof(frame));
    frame.fds[0] = (int64_t)server;
    frame.events[0] = WILLRT_POLL_IN;
    frame.users[0] = 41;
    frame.out.data = tags;
    frame.out.cap = 4;
    frame.timeout_ns = INT64_C(60000000);
    submitted_before = willrt_blocking_submitted();
    task = willrt_task_new((void*)step_async_poll, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_OK);
    assert(frame.out.len == 0);
    assert(willrt_blocking_submitted() == submitted_before);
    assert(frame.alloc_after == frame.alloc_before);
    test_sock_close(server);
    test_sock_close(client);
}

static void test_async_poll_honors_block_deadline(void) {
    struct sockaddr_in addr;
    test_sock client;
    test_sock server = test_poll_pair(&addr, &client);
    int64_t tags[4];
    poll_frame frame;
    willrt_task* task;
    int32_t result = 0;

    assert(server != TEST_SOCK_INVALID);
    memset(&frame, 0, sizeof(frame));
    frame.fds[0] = (int64_t)server;
    frame.events[0] = WILLRT_POLL_IN;
    frame.users[0] = 41;
    frame.out.data = tags;
    frame.out.cap = 4;
    frame.timeout_ns = INT64_C(1500000000);
    frame.scope_ns = INT64_C(50000000);
    task = willrt_task_new((void*)step_async_poll, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_TIMEDOUT);
    assert(frame.out.len == 0);
    test_sock_close(server);
    test_sock_close(client);
}

typedef struct sleep_frame {
    int64_t ns;
    int64_t scope_ns;
    int64_t scope_previous;
    willrt_sleep_op* op;
    int32_t started;
    int32_t status;
} sleep_frame;

static int32_t step_async_sleep(void* raw) {
    sleep_frame* f = (sleep_frame*)raw;

    if (f->started == 0) {
        f->started = 1;
        if (f->scope_ns > 0) {
            f->scope_previous = willrt_task_enter_deadline(f->scope_ns);
        }
        f->op = willrt_task_sleep_async_start(f->ns);
        assert(f->op != NULL);
    }
    if (willrt_task_sleep_poll(f->op) == 0) {
        willrt_task_wait_until(willrt_task_sleep_deadline(f->op));
        willrt_task_mark_io();
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status = willrt_task_sleep_finish(f->op);
    if (f->scope_ns > 0) {
        willrt_task_exit_deadline(f->scope_previous);
    }
    assert(willrt_task_store_result(&f->status, sizeof(f->status))
           == WILLRT_OK);
    return WILLRT_DONE;
}

static void test_async_sleep_honors_block_deadline(void) {
    sleep_frame frame;
    willrt_task* task;
    int32_t result = 0;

    memset(&frame, 0, sizeof(frame));
    frame.ns = INT64_C(1500000000);
    frame.scope_ns = INT64_C(50000000);
    task = willrt_task_new((void*)step_async_sleep, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_TIMEDOUT);
}

typedef struct poll_reuse_frame {
    int64_t fds[2];
    uint32_t events[1];
    int64_t users[1];
    test_poll_out out;
    int64_t timeout_ns;
    willrt_poll_request* req;
    int32_t started;
    int32_t round;
    int32_t status[2];
} poll_reuse_frame;

static int32_t step_async_poll_reuse(void* raw) {
    poll_reuse_frame* f = (poll_reuse_frame*)raw;

    if (f->started == 0) {
        f->started = 1;
        f->req = willrt_poll_wait_async_start(f->fds + f->round, f->events,
                                              f->users, 1, f->timeout_ns,
                                              &f->out);
        assert(f->req != NULL);
    }
    if (willrt_poll_wait_async_poll(f->req) == 0) {
        willrt_task_wait_until(willrt_poll_wait_async_deadline(f->req));
        willrt_task_mark_io();
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status[f->round] = willrt_poll_wait_async_finish(f->req);
    f->round += 1;
    if (f->round == 1) {
        f->started = 0;
        f->timeout_ns = INT64_C(500000000);
        f->out.len = 0;
        return WILLRT_SUSPEND;
    }
    assert(willrt_task_store_result(&f->status[1], sizeof(f->status[1]))
           == WILLRT_OK);
    return WILLRT_DONE;
}

static void test_async_poll_survives_reuse(void) {
    struct sockaddr_in addr;
    struct sockaddr_in quiet_addr;
    test_sock client;
    test_sock quiet_client;
    test_sock ready = test_poll_pair(&addr, &client);
    test_sock quiet = test_poll_pair(&quiet_addr, &quiet_client);
    int64_t tags[4];
    int64_t submitted_before;
    poll_reuse_frame frame;
    willrt_task* task;
    int32_t result = 0;

    assert(ready != TEST_SOCK_INVALID);
    assert(quiet != TEST_SOCK_INVALID);
    test_sock_write(client, "x", 1);
    memset(&frame, 0, sizeof(frame));
    frame.fds[0] = (int64_t)ready;
    frame.fds[1] = (int64_t)quiet;
    frame.events[0] = WILLRT_POLL_IN;
    frame.users[0] = 5;
    frame.out.data = tags;
    frame.out.cap = 4;
    frame.timeout_ns = INT64_C(1000000);
    submitted_before = willrt_blocking_submitted();
    task = willrt_task_new((void*)step_async_poll_reuse, &frame,
                           sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &result, NULL) == WILLRT_OK);
    assert(frame.status[0] == WILLRT_OK);
    assert(frame.status[1] == WILLRT_OK);
    assert(frame.out.len == 0);
    assert(willrt_blocking_submitted() == submitted_before);
    test_sock_close(ready);
    test_sock_close(client);
    test_sock_close(quiet);
    test_sock_close(quiet_client);
}

static void test_task_error_copy_accessors(void) {
    willrt_task_error copy;
    const char* text = "task failed on purpose";
    const char* out = NULL;
    size_t size = 0;

    memset(&copy, 0, sizeof(copy));
    copy.error.message = text;
    copy.error.size = sizeof("task failed on purpose") - 1;
    copy.error.site = "task_test.c";
    copy.error.symbol = "will.test.task";
    copy.status = WILLRT_CANCELED;
    willrt_task_error_message(&copy, &out, &size);
    assert(size == sizeof("task failed on purpose") - 1);
    assert(memcmp(out, text, size) == 0);
    assert(willrt_task_error_status(&copy) == 8);
    copy.status = WILLRT_FAILED;
    assert(willrt_task_error_status(&copy) == 0);
}

typedef struct recycle_frame {
    int32_t round;
} recycle_frame;

static int32_t step_recycle(void* raw) {
    recycle_frame* f = (recycle_frame*)raw;

    f->round += 1;
    if (f->round < 3) {
        willrt_task_wait_until(willrt_now_ns() + INT64_C(1000000));
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    return WILLRT_DONE;
}

static void test_region_recycles_tasks(void) {
    willrt_task* tasks[8];
    int64_t warm;
    int64_t settled;
    int32_t i;
    int32_t round;

    /* Every member frame is heap memory the region owns, exactly as the
       generated constructors hand them over. */
    willrt_region_enter();
    for (i = 0; i < 8; i++) {
        recycle_frame* frame = (recycle_frame*)willrt_alloc(sizeof(*frame));

        assert(frame != NULL);
        frame->round = 0;
        tasks[i] = willrt_task_new((void*)step_recycle, frame, sizeof(*frame),
                                   0, 0);
        assert(tasks[i] != NULL);
        willrt_task_own_frame(tasks[i]);
        willrt_task_run(tasks[i]);
    }
    assert(willrt_region_exit() == 0);
    warm = willrt_alloc_live();
    for (round = 0; round < 50; round++) {
        willrt_region_enter();
        for (i = 0; i < 8; i++) {
            recycle_frame* frame =
                (recycle_frame*)willrt_alloc(sizeof(*frame));

            assert(frame != NULL);
            frame->round = 0;
            tasks[i] = willrt_task_new((void*)step_recycle, frame,
                                       sizeof(*frame), 0, 0);
            assert(tasks[i] != NULL);
            willrt_task_own_frame(tasks[i]);
            willrt_task_run(tasks[i]);
        }
        assert(willrt_region_exit() == 0);
    }
    settled = willrt_alloc_live();
    /* Without the release pass the fifty regions leak eight tasks and eight
       frames each, so the live count never comes back. */
    assert(settled == warm);
}

/* A spawn scope nested deeper than the inline region table must still own its
   own members. The inner scope exits first and reports only its own failure; a
   table that stops growing would drop the inner scope and let this exit close
   the outer one, which reports the outer failure as well. */
static void test_region_nests_past_inline(void) {
    willrt_task* inner;
    willrt_task* outer;
    int depth;

    for (depth = 0; depth < 64; depth++) {
        willrt_region_enter();
    }
    outer = willrt_task_new((void*)step_panic, NULL, 0, 0, 0);
    assert(outer != NULL);
    willrt_task_run(outer);
    willrt_region_enter();
    inner = willrt_task_new((void*)step_panic, NULL, 0, 0, 0);
    assert(inner != NULL);
    willrt_task_run(inner);
    assert(willrt_region_exit() == 1);
    /* The outer failing task belongs to the scope below, so this exit sees it
       and nothing from the scope that just closed. */
    assert(willrt_region_exit() == 1);
    for (depth = 0; depth < 63; depth++) {
        assert(willrt_region_exit() == 0);
    }
}

/* A region releases the failure text of its members with everything else, so a
   program that spawns failing tasks in a loop settles back to its baseline
   instead of growing a block per failure. */
static void test_region_releases_failure_text(void) {
    willrt_task* task;
    int64_t warm;
    int64_t settled;
    int32_t i;

    warm = willrt_alloc_live();
    for (i = 0; i < 8; i++) {
        willrt_region_enter();
        task = willrt_task_new((void*)step_panic, NULL, 0, 0, 0);
        assert(task != NULL);
        willrt_task_run(task);
        assert(willrt_join(task, NULL, NULL) == WILLRT_FAILED);
        assert(willrt_region_exit() == 1);
    }
    settled = willrt_alloc_live();
    assert(settled == warm);
}

int main(void) {
#ifdef _WIN32
    WSADATA data;

    assert(WSAStartup(MAKEWORD(2, 2), &data) == 0);
#endif
    willrt_init();
    test_lane_choose();
    test_task_result();
    test_task_suspend_resume();
    test_region_counts();
    test_region_failure();
    test_panic_keeps_lanes();
    test_channel_order();
    test_channel_release_drops_queued_values();
    test_channel_crosses_threads();
    test_channel_hands_off_to_waiting_task();
    test_channel_send_wait();
    test_channel_wait_deadline();
    test_channel_wait_cancel();
    test_task_cancel_at_wait();
    test_io_wait_is_not_deadlock();
    test_select_hands_off_from_task();
    test_select_order();
    test_wake_during_park_is_not_lost();
    test_unrelated_send_wakes_no_parked_task();
    test_timer_wakes_only_its_task();
    test_timed_park_allocates_nothing();
    test_select_reuse_allocates_nothing();
    test_async_poll_delivers_readiness();
    test_async_poll_times_out_allocating_nothing();
    test_async_poll_honors_block_deadline();
    test_async_sleep_honors_block_deadline();
    test_async_poll_survives_reuse();
    test_task_error_copy_accessors();
    test_region_recycles_tasks();
    test_region_releases_failure_text();
    test_region_nests_past_inline();
    willrt_shutdown();
#ifdef _WIN32
    (void)WSACleanup();
#endif
    return 0;
}
