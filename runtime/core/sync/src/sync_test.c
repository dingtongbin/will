/* Contract tests for the sync primitives. The process exits zero when every
   assertion holds and aborts through assert on the first failure. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

/* park_frame drives one acquisition the way a generated state machine does:
   start once, poll, park, resume, finish. */
typedef struct park_frame {
    void* sync;
    int32_t mode;
    int32_t status;
    int32_t scope_ns;
    int64_t scope_previous;
    int32_t started;
} park_frame;

static int32_t step_sync_park(void* raw) {
    park_frame* f = (park_frame*)raw;
    willrt_sync_wait* wait;

    if (f->started == 0) {
        f->started = 1;
        if (f->scope_ns > 0) {
            f->scope_previous = willrt_task_enter_deadline(f->scope_ns);
        }
        wait = willrt_sync_lock_async_start(f->sync, f->mode);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
        f->status = willrt_sync_wait_finish(wait);
        if (f->scope_ns > 0) {
            willrt_task_exit_deadline(f->scope_previous);
        }
        return WILLRT_DONE;
    }
    wait = willrt_task_sync_node();
    assert(wait != NULL);
    if (willrt_sync_wait_poll(wait) == 0) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status = willrt_sync_wait_finish(wait);
    if (f->scope_ns > 0) {
        willrt_task_exit_deadline(f->scope_previous);
    }
    return WILLRT_DONE;
}

/* hold_frame takes the exclusive lock, sleeps, and releases. */
typedef struct hold_frame {
    void* sync;
    int32_t hold_ms;
    int32_t started;
} hold_frame;

static int32_t step_hold_release(void* raw) {
    hold_frame* f = (hold_frame*)raw;
    willrt_sync_wait* wait;

    if (f->started == 0) {
        f->started = 1;
        wait = willrt_sync_lock_async_start(f->sync, WILLRT_SYNC_EXCLUSIVE);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    } else {
        wait = willrt_task_sync_node();
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    }
    assert(willrt_sync_wait_finish(wait) == WILLRT_OK);
    willrt_os_sleep_ns((int64_t)f->hold_ms * INT64_C(1000000));
    willrt_sync_unlock(f->sync, WILLRT_SYNC_EXCLUSIVE);
    return WILLRT_DONE;
}

static willrt_task* run_step(void* step, void* frame, size_t size) {
    static int32_t next_lane;
    int32_t lanes = willrt_os_cpu_count();
    int32_t lane;
    willrt_task* task;

    if (lanes < 1) {
        lanes = 1;
    }
    /* Tasks spread across the lanes: a test that proves overlap needs its
       holders running at the same time, not serialized on one lane. */
    lane = __atomic_fetch_add(&next_lane, 1, __ATOMIC_RELAXED) % lanes;
    task = willrt_task_new(step, frame, size, lane, 0);
    assert(task != NULL);
    willrt_task_run(task);
    return task;
}

/* run_step_bound is run_step with a cancellation credential bound before the
   task starts, so a parked wait observes the flag. */
static willrt_task* run_step_bound(void* step, void* frame, size_t size,
                                   void** cred) {
    static int32_t next_lane;
    int32_t lanes = willrt_os_cpu_count();
    int32_t lane;
    willrt_task* task;

    if (lanes < 1) {
        lanes = 1;
    }
    lane = __atomic_fetch_add(&next_lane, 1, __ATOMIC_RELAXED) % lanes;
    task = willrt_task_new(step, frame, size, lane, 0);
    assert(task != NULL);
    willrt_task_bind_cancel(task, cred);
    willrt_task_run(task);
    return task;
}

/* bump_frame takes the exclusive lock one hundred times per task; only the
   holder touches the shared counter, so the total is exact. */
typedef struct bump_frame {
    void* sync;
    int32_t i;
    int32_t phase;
} bump_frame;

static int64_t lock_bumps;

static int32_t step_lock_bump(void* raw) {
    bump_frame* f = (bump_frame*)raw;
    willrt_sync_wait* wait = willrt_task_sync_node();

    if (f->phase == 0) {
        f->i = 0;
        f->phase = 1;
    }
    for (;;) {
        if (f->phase == 1) {
            wait = willrt_sync_lock_async_start(f->sync,
                                                WILLRT_SYNC_EXCLUSIVE);
            assert(wait != NULL);
            f->phase = 2;
        }
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
        assert(willrt_sync_wait_finish(wait) == WILLRT_OK);
        lock_bumps++;
        willrt_sync_unlock(f->sync, WILLRT_SYNC_EXCLUSIVE);
        f->i++;
        if (f->i >= 100) {
            return WILLRT_DONE;
        }
        f->phase = 1;
    }
}

static void test_lock_exclusive_serializes(void) {
    void* lock = willrt_sync_lock_new();
    bump_frame frames[8];
    willrt_task* tasks[8];

    assert(lock != NULL);
    for (int32_t i = 0; i < 8; i++) {
        frames[i].sync = lock;
        frames[i].i = 0;
        frames[i].phase = 0;
        tasks[i] = run_step((void*)step_lock_bump, &frames[i],
                            sizeof(frames[i]));
    }
    for (int32_t i = 0; i < 8; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
    }
    assert(lock_bumps == 800);
}

/* reader_frame holds the shared lock, tracks the concurrent-reader peak, and
   releases. */
static _Atomic(int32_t) readers_live;
static _Atomic(int32_t) readers_peak;

typedef struct reader_frame {
    void* sync;
    int32_t hold_ms;
    int32_t started;
} reader_frame;

static int32_t step_reader_hold(void* raw) {
    reader_frame* f = (reader_frame*)raw;
    willrt_sync_wait* wait;
    int32_t live;
    int32_t peak;

    if (f->started == 0) {
        f->started = 1;
        wait = willrt_sync_lock_async_start(f->sync, WILLRT_SYNC_SHARED);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    } else {
        wait = willrt_task_sync_node();
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    }
    assert(willrt_sync_wait_finish(wait) == WILLRT_OK);
    live = __atomic_add_fetch(&readers_live, 1, __ATOMIC_ACQ_REL);
    peak = __atomic_load_n(&readers_peak, __ATOMIC_ACQUIRE);
    while (live > peak
           && !__atomic_compare_exchange_n(&readers_peak, &peak, live, 0,
                                           __ATOMIC_ACQ_REL,
                                           __ATOMIC_ACQUIRE)) {
    }
    willrt_os_sleep_ns((int64_t)f->hold_ms * INT64_C(1000000));
    __atomic_sub_fetch(&readers_live, 1, __ATOMIC_ACQ_REL);
    willrt_sync_unlock(f->sync, WILLRT_SYNC_SHARED);
    return WILLRT_DONE;
}

static void test_shared_readers_overlap(void) {
    void* lock = willrt_sync_lock_new();
    reader_frame frames[4];
    willrt_task* tasks[4];
    int64_t begin;
    int64_t end;

    assert(lock != NULL);
    /* The main thread holds the word exclusively, so every reader queues on
       the chain; one release must hand the word to the whole run at once. */
    assert(willrt_sync_lock_async(lock, WILLRT_SYNC_EXCLUSIVE) == WILLRT_OK);
    for (int32_t i = 0; i < 4; i++) {
        frames[i].sync = lock;
        frames[i].hold_ms = 100;
        frames[i].started = 0;
        tasks[i] = run_step((void*)step_reader_hold, &frames[i],
                            sizeof(frames[i]));
    }
    willrt_os_sleep_ns(INT64_C(20) * INT64_C(1000000));
    begin = willrt_now_ns();
    willrt_sync_unlock(lock, WILLRT_SYNC_EXCLUSIVE);
    for (int32_t i = 0; i < 4; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
    }
    end = willrt_now_ns();
    /* All four held it at once; a serialized lock would need four holds, so
       the elapsed time separates overlap from serialization. */
    assert(__atomic_load_n(&readers_peak, __ATOMIC_ACQUIRE) == 4);
    assert(end - begin < INT64_C(250) * INT64_C(1000000));
}

/* A reader queued behind a waiting writer stays queued until the writer is
   done, so a writer never starves behind a stream of readers. */
static int64_t writer_note;
static int64_t reader_note;

typedef struct note_frame {
    void* sync;
    int32_t mode;
    int64_t note;
    int32_t hold_ms;
    int32_t started;
} note_frame;

/* step_note_acquire notes itself the moment it holds the word, sleeps, and
   releases. The note is written before the release, so a note proves the
   acquisition and silence proves the wait. */
static int32_t step_note_acquire(void* raw) {
    note_frame* f = (note_frame*)raw;
    willrt_sync_wait* wait;

    if (f->started == 0) {
        f->started = 1;
        wait = willrt_sync_lock_async_start(f->sync, f->mode);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    } else {
        wait = willrt_task_sync_node();
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    }
    assert(willrt_sync_wait_finish(wait) == WILLRT_OK);
    if (f->mode == WILLRT_SYNC_EXCLUSIVE) {
        writer_note = f->note;
    } else {
        reader_note = f->note;
    }
    willrt_os_sleep_ns((int64_t)f->hold_ms * INT64_C(1000000));
    willrt_sync_unlock(f->sync, f->mode);
    return WILLRT_DONE;
}

static void test_writer_waits_for_readers(void) {
    void* lock = willrt_sync_lock_new();
    note_frame writer;
    note_frame reader;
    willrt_task* writer_task;
    willrt_task* reader_task;

    assert(lock != NULL);
    /* The main thread holds the word shared, so both tasks must queue. */
    assert(willrt_sync_lock_async(lock, WILLRT_SYNC_SHARED) == WILLRT_OK);
    memset(&writer, 0, sizeof(writer));
    writer.sync = lock;
    writer.mode = WILLRT_SYNC_EXCLUSIVE;
    writer.note = 1;
    writer.hold_ms = 80;
    writer_task = run_step((void*)step_note_acquire, &writer,
                           sizeof(writer));
    /* Let the writer queue before the reader starts, or the reader would
       take the word alongside the main thread's shared hold. */
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    memset(&reader, 0, sizeof(reader));
    reader.sync = lock;
    reader.mode = WILLRT_SYNC_SHARED;
    reader.note = 2;
    reader_task = run_step((void*)step_note_acquire, &reader,
                           sizeof(reader));
    willrt_os_sleep_ns(INT64_C(20) * INT64_C(1000000));
    /* Both are queued; nobody holds the word, so nobody wrote a note. */
    assert(writer_note == 0 && reader_note == 0);
    willrt_sync_unlock(lock, WILLRT_SYNC_SHARED);
    willrt_os_sleep_ns(INT64_C(30) * INT64_C(1000000));
    /* The release handed the word to the writer, whose 80 ms hold is not
       over; the reader is still parked behind it. */
    assert(writer_note == 1 && reader_note == 0);
    assert(willrt_join(writer_task, NULL, NULL) == WILLRT_OK);
    assert(willrt_join(reader_task, NULL, NULL) == WILLRT_OK);
    assert(writer_note == 1 && reader_note == 2);
}

static void test_cancel_leaves_token_intact(void) {
    void* lock = willrt_sync_lock_new();
    hold_frame holder;
    park_frame waiter;
    park_frame late;
    willrt_task* holder_task;
    willrt_task* waiter_task;
    willrt_task* late_task;
    int32_t flag = 0;
    void* flag_ptr = &flag;

    assert(lock != NULL);
    memset(&holder, 0, sizeof(holder));
    holder.sync = lock;
    holder.hold_ms = 80;
    holder_task = run_step((void*)step_hold_release, &holder,
                           sizeof(holder));
    /* Let the holder take the word, so the waiter provably queues behind a
       held lock instead of racing it for the free word. */
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    memset(&waiter, 0, sizeof(waiter));
    waiter.sync = lock;
    waiter.mode = WILLRT_SYNC_EXCLUSIVE;
    waiter_task = willrt_task_new((void*)step_sync_park, &waiter,
                                  sizeof(waiter), willrt_lane_here(), 0);
    assert(waiter_task != NULL);
    willrt_task_bind_cancel(waiter_task, &flag_ptr);
    willrt_task_run(waiter_task);
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    flag = 1;
    willrt_sched_wake_all();
    assert(willrt_join(waiter_task, NULL, NULL) == WILLRT_OK);
    assert(waiter.status == WILLRT_CANCELED);
    /* The holder finishes and releases; a fresh waiter is granted, which
       proves the canceled wait left no half-state on the chain. */
    assert(willrt_join(holder_task, NULL, NULL) == WILLRT_OK);
    memset(&late, 0, sizeof(late));
    late.sync = lock;
    late.mode = WILLRT_SYNC_EXCLUSIVE;
    late_task = run_step((void*)step_sync_park, &late, sizeof(late));
    assert(willrt_join(late_task, NULL, NULL) == WILLRT_OK);
    assert(late.status == WILLRT_OK);
}

static void test_deadline_unlinks_waiter(void) {
    void* lock = willrt_sync_lock_new();
    hold_frame holder;
    park_frame waiter;
    willrt_task* holder_task;
    willrt_task* waiter_task;

    assert(lock != NULL);
    memset(&holder, 0, sizeof(holder));
    holder.sync = lock;
    holder.hold_ms = 120;
    holder_task = run_step((void*)step_hold_release, &holder,
                           sizeof(holder));
    /* Let the holder take the word before the waiter starts, so the scoped
       deadline provably expires inside a queued wait. */
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    memset(&waiter, 0, sizeof(waiter));
    waiter.sync = lock;
    waiter.mode = WILLRT_SYNC_EXCLUSIVE;
    waiter.scope_ns = 40;
    waiter_task = run_step((void*)step_sync_park, &waiter,
                           sizeof(waiter));
    assert(willrt_join(waiter_task, NULL, NULL) == WILLRT_OK);
    assert(waiter.status == WILLRT_TIMEDOUT);
    assert(willrt_join(holder_task, NULL, NULL) == WILLRT_OK);
}

/* sema_frame acquires, counts the live holders, sleeps, and releases. */
typedef struct sema_frame {
    void* sema;
    int32_t hold_ms;
    int32_t started;
    int32_t status;
} sema_frame;

static _Atomic(int32_t) permits_live;
static _Atomic(int32_t) permits_peak;
static _Atomic(int32_t) permits_taken;
static _Atomic(int32_t) permits_given;

static int32_t step_sema_hold(void* raw) {
    sema_frame* f = (sema_frame*)raw;
    willrt_sync_wait* wait;
    int32_t live;
    int32_t peak;

    if (f->started == 0) {
        f->started = 1;
        wait = willrt_sync_sema_acquire_async_start(f->sema);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    } else {
        wait = willrt_task_sync_node();
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
    }
    f->status = willrt_sync_wait_finish(wait);
    if (f->status != WILLRT_OK) {
        return WILLRT_DONE;
    }
    __atomic_add_fetch(&permits_taken, 1, __ATOMIC_ACQ_REL);
    live = __atomic_add_fetch(&permits_live, 1, __ATOMIC_ACQ_REL);
    peak = __atomic_load_n(&permits_peak, __ATOMIC_ACQUIRE);
    while (live > peak
           && !__atomic_compare_exchange_n(&permits_peak, &peak, live, 0,
                                           __ATOMIC_ACQ_REL,
                                           __ATOMIC_ACQUIRE)) {
    }
    willrt_os_sleep_ns((int64_t)f->hold_ms * INT64_C(1000000));
    __atomic_sub_fetch(&permits_live, 1, __ATOMIC_ACQ_REL);
    willrt_sync_sema_release(f->sema);
    __atomic_add_fetch(&permits_given, 1, __ATOMIC_ACQ_REL);
    return WILLRT_DONE;
}

static void test_sema_permits_never_leak_on_cancel(void) {
    void* sema = willrt_sync_sema_new(2);
    sema_frame frames[6];
    willrt_task* tasks[6];
    int32_t flag = 0;
    void* flag_ptr = &flag;

    assert(sema != NULL);
    /* Six tasks race for two permits and every task carries the credential,
       so whichever four lose the race are canceled while parked. Every
       granted acquire is released exactly once, so the books close: taken ==
       given and no permit vanished. */
    for (int32_t i = 0; i < 6; i++) {
        frames[i].sema = sema;
        frames[i].hold_ms = 30;
        frames[i].started = 0;
        frames[i].status = -1;
        tasks[i] = run_step_bound((void*)step_sema_hold, &frames[i],
                                  sizeof(frames[i]), &flag_ptr);
    }
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    flag = 1;
    willrt_sched_wake_all();
    int32_t canceled_waits = 0;
    for (int32_t i = 0; i < 6; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
        if (frames[i].status == WILLRT_CANCELED) {
            canceled_waits++;
        } else {
            assert(frames[i].status == WILLRT_OK);
        }
    }
    assert(canceled_waits >= 1);
    assert(__atomic_load_n(&permits_taken, __ATOMIC_ACQUIRE)
           == __atomic_load_n(&permits_given, __ATOMIC_ACQUIRE));
    /* Both permits are back: two immediate acquisitions succeed. */
    willrt_sync_wait* first = willrt_sync_sema_acquire_async_start(sema);
    assert(first != NULL);
    assert(willrt_sync_wait_finish(first) == WILLRT_OK);
    willrt_sync_wait* second = willrt_sync_sema_acquire_async_start(sema);
    assert(second != NULL);
    assert(willrt_sync_wait_finish(second) == WILLRT_OK);
    willrt_sync_sema_release(sema);
    willrt_sync_sema_release(sema);
}

/* count_frame waits for the counter to reach zero. */
typedef struct count_frame {
    void* sync;
    int32_t status;
    int32_t started;
} count_frame;

static int32_t step_count_wait(void* raw) {
    count_frame* f = (count_frame*)raw;
    willrt_sync_wait* wait;

    if (f->started == 0) {
        f->started = 1;
        wait = willrt_sync_count_wait_async_start(f->sync);
        assert(wait != NULL);
        if (willrt_sync_wait_poll(wait) == 0) {
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
        f->status = willrt_sync_wait_finish(wait);
        return WILLRT_DONE;
    }
    wait = willrt_task_sync_node();
    assert(wait != NULL);
    if (willrt_sync_wait_poll(wait) == 0) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status = willrt_sync_wait_finish(wait);
    return WILLRT_DONE;
}

static void test_count_waits_for_zero_and_rearms(void) {
    void* count = willrt_sync_count_new(3);
    count_frame first;
    count_frame second;
    willrt_task* first_task;
    willrt_task* second_task;

    assert(count != NULL);
    memset(&first, 0, sizeof(first));
    first.sync = count;
    first.status = -77;
    first_task = run_step((void*)step_count_wait, &first, sizeof(first));
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    /* Still parked: two decrements are not enough, and the frame only gets
       its final status when the wait resolves. */
    assert(first.status == -77);
    willrt_sync_count_add(count, -1);
    willrt_os_sleep_ns(INT64_C(5) * INT64_C(1000000));
    assert(first.status == -77);
    willrt_sync_count_add(count, -1);
    willrt_os_sleep_ns(INT64_C(5) * INT64_C(1000000));
    assert(first.status == -77);
    willrt_sync_count_add(count, -1);
    assert(willrt_join(first_task, NULL, NULL) == WILLRT_OK);
    assert(first.status == WILLRT_OK);
    /* Raising the count re-arms: a new waiter waits again. */
    willrt_sync_count_add(count, 2);
    memset(&second, 0, sizeof(second));
    second.sync = count;
    second.status = -77;
    second_task = run_step((void*)step_count_wait, &second,
                           sizeof(second));
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    assert(second.status == -77);
    willrt_sync_count_add(count, -2);
    assert(willrt_join(second_task, NULL, NULL) == WILLRT_OK);
    assert(second.status == WILLRT_OK);
}

static void test_gate_signals_one_generation(void) {
    void* gate = willrt_sync_gate_new();
    count_frame waiters[3];
    willrt_task* tasks[3];

    assert(gate != NULL);
    for (int32_t i = 0; i < 3; i++) {
        memset(&waiters[i], 0, sizeof(waiters[i]));
        waiters[i].sync = gate;
        tasks[i] = run_step((void*)step_count_wait, &waiters[i],
                            sizeof(waiters[i]));
    }
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    willrt_sync_gate_signal(gate);
    for (int32_t i = 0; i < 3; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
        assert(waiters[i].status == WILLRT_OK);
    }
    /* After a reset the gate waits again. */
    willrt_sync_gate_reset(gate);
    count_frame next;
    willrt_task* next_task;
    memset(&next, 0, sizeof(next));
    next.sync = gate;
    next.status = -77;
    next_task = run_step((void*)step_count_wait, &next, sizeof(next));
    willrt_os_sleep_ns(INT64_C(5) * INT64_C(1000000));
    assert(next.status == -77);
    willrt_sync_gate_signal(gate);
    assert(willrt_join(next_task, NULL, NULL) == WILLRT_OK);
    assert(next.status == WILLRT_OK);
}

static void test_blocking_fallback_honors_deadline(void) {
    void* lock = willrt_sync_lock_new();
    hold_frame holder;
    willrt_task* holder_task;
    int64_t previous;
    int32_t status;

    assert(lock != NULL);
    memset(&holder, 0, sizeof(holder));
    holder.sync = lock;
    holder.hold_ms = 120;
    holder_task = run_step((void*)step_hold_release, &holder,
                           sizeof(holder));
    willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    previous = willrt_task_enter_deadline(30);
    status = willrt_sync_lock_async(lock, WILLRT_SYNC_EXCLUSIVE);
    willrt_task_exit_deadline(previous);
    assert(status == WILLRT_TIMEDOUT);
    assert(willrt_join(holder_task, NULL, NULL) == WILLRT_OK);
    /* The holder released, so the blocking form is granted. */
    status = willrt_sync_lock_async(lock, WILLRT_SYNC_EXCLUSIVE);
    assert(status == WILLRT_OK);
    willrt_sync_unlock(lock, WILLRT_SYNC_EXCLUSIVE);
}

int main(void) {
    willrt_init();
    test_lock_exclusive_serializes();
    test_shared_readers_overlap();
    test_writer_waits_for_readers();
    test_cancel_leaves_token_intact();
    test_deadline_unlinks_waiter();
    test_sema_permits_never_leak_on_cancel();
    test_count_waits_for_zero_and_rearms();
    test_gate_signals_one_generation();
    test_blocking_fallback_honors_deadline();
    willrt_shutdown();
    return 0;
}
