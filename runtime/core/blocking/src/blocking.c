/* A bounded blocking pool.
 *
 * The runtime cannot run a blocking C call on a lane, because that would hold
 * the lane's thread. This pool keeps a fixed maximum of worker threads and a
 * bounded queue. A caller submits one job and waits on the pool condition until
 * the worker records the result; the job itself lives on the caller's stack, so
 * submitting allocates nothing.
 *
 * Workers start lazily: the first submit starts one, and a new one starts only
 * while no worker is idle and the maximum is not reached. WILL_BLOCKING_MAX
 * lowers the maximum before the first job; values outside 1..128 are ignored.
 * Shutdown drains the queue, joins every worker, and may be followed by a new
 * init because willrt_shutdown and willrt_init may run repeatedly in one host.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

/* The pool can be linked without the task engine. The task hooks are weak so
   a standalone pool still works; an executable links task.c and a finishing
   worker wakes exactly the task that submitted the job. */
extern willrt_task* willrt_task_current(void) __attribute__((weak));
extern void willrt_task_wake_chan(willrt_task* task) __attribute__((weak));

enum {
    WILLRT_BLOCKING_LIMIT = 128,
    WILLRT_BLOCKING_FIRST = 64
};

struct willrt_blocking_op {
    willrt_blocking_fn fn;
    void* arg;
    void* out;
    willrt_task* owner;
    int32_t status;
    int32_t done;
};

/* The queue grows on demand: a burst of blocking operations may be deeper than
   any constant, and a submitter must never wait for room. It is a ring under
   willrt_blocking_lock. */
static struct willrt_blocking_op** willrt_blocking_queue;
static size_t willrt_blocking_cap;
static willrt_os_thread willrt_blocking_threads[WILLRT_BLOCKING_LIMIT];
static willrt_os_mutex willrt_blocking_lock;
static willrt_os_cond willrt_blocking_work;
static atomic_int willrt_blocking_state;
static atomic_llong willrt_blocking_submits;
static int willrt_blocking_stop;
static size_t willrt_blocking_head;
static size_t willrt_blocking_tail;
static size_t willrt_blocking_count;
static size_t willrt_blocking_created;
static size_t willrt_blocking_idle;
static size_t willrt_blocking_max;

static size_t willrt_blocking_limit(void) {
    const char* text = getenv("WILL_BLOCKING_MAX");
    char* end = NULL;
    long value;

    if (text == NULL || *text == '\0') {
        return WILLRT_BLOCKING_LIMIT;
    }
    value = strtol(text, &end, 10);
    if (end == text || *end != '\0' || value < 1
        || value > WILLRT_BLOCKING_LIMIT) {
        return WILLRT_BLOCKING_LIMIT;
    }
    return (size_t)value;
}

static void willrt_blocking_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_blocking_state, &expected, 1)) {
        willrt_os_mutex_init(&willrt_blocking_lock);
        willrt_os_cond_init(&willrt_blocking_work);
        willrt_blocking_stop = 0;
        willrt_blocking_head = 0;
        willrt_blocking_tail = 0;
        willrt_blocking_count = 0;
        willrt_blocking_cap = 0;
        willrt_blocking_created = 0;
        willrt_blocking_idle = 0;
        willrt_blocking_max = willrt_blocking_limit();
        atomic_store(&willrt_blocking_state, 2);
        return;
    }
    while (atomic_load(&willrt_blocking_state) == 1) {
        willrt_os_thread_yield();
    }
}

/* willrt_blocking_grow doubles the ring and unwraps it, so head becomes zero
   and the live entries keep their order. It runs under the pool lock. */
static int willrt_blocking_grow(void) {
    size_t next = willrt_blocking_cap == 0 ? WILLRT_BLOCKING_FIRST
                                           : willrt_blocking_cap * 2;
    struct willrt_blocking_op** ring;
    size_t i;

    if (next < willrt_blocking_cap || next > SIZE_MAX / sizeof(*ring)) {
        return 0;
    }
    ring = (struct willrt_blocking_op**)willrt_alloc(next * sizeof(*ring));
    if (ring == NULL) {
        return 0;
    }
    for (i = 0; i < willrt_blocking_count; i++) {
        ring[i] = willrt_blocking_queue[
            (willrt_blocking_head + i) % willrt_blocking_cap];
    }
    willrt_free(willrt_blocking_queue);
    willrt_blocking_queue = ring;
    willrt_blocking_cap = next;
    willrt_blocking_head = 0;
    willrt_blocking_tail = willrt_blocking_count;
    return 1;
}

/* willrt_blocking_push links one job at the tail, growing the ring first when
   it is full. It runs under the pool lock and reports failure only when the
   ring cannot grow. */
static int willrt_blocking_push(struct willrt_blocking_op* job) {
    if (willrt_blocking_count == willrt_blocking_cap
        && !willrt_blocking_grow()) {
        return 0;
    }
    willrt_blocking_queue[willrt_blocking_tail] = job;
    willrt_blocking_tail = (willrt_blocking_tail + 1) % willrt_blocking_cap;
    willrt_blocking_count++;
    return 1;
}

static void willrt_blocking_worker(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    for (;;) {
        struct willrt_blocking_op* job;

        willrt_os_mutex_lock(&willrt_blocking_lock);
        while (willrt_blocking_count == 0 && !willrt_blocking_stop) {
            willrt_blocking_idle++;
            willrt_os_cond_wait(&willrt_blocking_work, &willrt_blocking_lock);
            willrt_blocking_idle--;
        }
        if (willrt_blocking_stop && willrt_blocking_count == 0) {
            willrt_os_mutex_unlock(&willrt_blocking_lock);
            break;
        }
        job = willrt_blocking_queue[willrt_blocking_head];
        willrt_blocking_head =
            (willrt_blocking_head + 1) % willrt_blocking_cap;
        willrt_blocking_count--;
        willrt_os_mutex_unlock(&willrt_blocking_lock);

        job->status = job->fn(job->arg, job->out);

        willrt_os_mutex_lock(&willrt_blocking_lock);
        job->done = 1;
        willrt_os_cond_broadcast(&willrt_blocking_work);
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        if (job->owner != NULL && willrt_task_wake_chan != NULL) {
            willrt_task_wake_chan(job->owner);
        }
    }
    willrt_mem_thread_flush();
}

int32_t willrt_blocking_call(void* fn, void* arg, void* out) {
    struct willrt_blocking_op job;
    int32_t started = 0;
    int32_t status;

    if (fn == NULL) {
        return WILLRT_FAILED;
    }
    willrt_blocking_init();
    if (atomic_load(&willrt_blocking_state) != 2) {
        return WILLRT_FAILED;
    }
    memset(&job, 0, sizeof(job));
    job.fn = (willrt_blocking_fn)fn;
    job.arg = arg;
    job.out = out;

    willrt_os_mutex_lock(&willrt_blocking_lock);
    if (willrt_blocking_stop) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        return WILLRT_CANCELED;
    }
    if (!willrt_blocking_push(&job)) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        return WILLRT_FAILED;
    }
    if (willrt_blocking_created < willrt_blocking_max
        && willrt_blocking_idle == 0) {
        size_t index = willrt_blocking_created;

        willrt_mem_set_shared();
        if (willrt_os_thread_start(&willrt_blocking_threads[index],
                                   willrt_blocking_worker, NULL)
            == 0) {
            willrt_blocking_created++;
            started = 1;
        }
    }
    willrt_os_cond_signal(&willrt_blocking_work);
    if (!started && willrt_blocking_created == 0) {
        willrt_blocking_count--;
        willrt_blocking_tail =
            (willrt_blocking_tail + willrt_blocking_cap - 1)
            % willrt_blocking_cap;
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        return WILLRT_FAILED;
    }
    while (!job.done) {
        willrt_os_cond_wait(&willrt_blocking_work, &willrt_blocking_lock);
    }
    status = job.status;
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    return status;
}

willrt_blocking_op* willrt_blocking_submit(void* fn, void* arg, void* out) {
    struct willrt_blocking_op* job;
    int32_t started = 0;

    if (fn == NULL) {
        return NULL;
    }
    willrt_blocking_init();
    if (atomic_load(&willrt_blocking_state) != 2) {
        return NULL;
    }
    job = (struct willrt_blocking_op*)willrt_alloc(sizeof(*job));
    if (job == NULL) {
        return NULL;
    }
    memset(job, 0, sizeof(*job));
    job->fn = (willrt_blocking_fn)fn;
    job->arg = arg;
    job->out = out;
    job->owner = willrt_task_current != NULL ? willrt_task_current() : NULL;

    willrt_os_mutex_lock(&willrt_blocking_lock);
    if (willrt_blocking_stop) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    if (!willrt_blocking_push(job)) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    if (willrt_blocking_created < willrt_blocking_max &&
        willrt_blocking_idle == 0) {
        size_t index = willrt_blocking_created;

        willrt_mem_set_shared();
        if (willrt_os_thread_start(&willrt_blocking_threads[index],
                                   willrt_blocking_worker, NULL) == 0) {
            willrt_blocking_created++;
            started = 1;
        }
    }
    willrt_os_cond_signal(&willrt_blocking_work);
    if (!started && willrt_blocking_created == 0) {
        willrt_blocking_count--;
        willrt_blocking_tail = (willrt_blocking_tail + willrt_blocking_cap - 1)
                               % willrt_blocking_cap;
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    atomic_fetch_add(&willrt_blocking_submits, 1);
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    return (willrt_blocking_op*)job;
}

willrt_blocking_op* willrt_blocking_submit_copy(void* fn, const void* arg,
                                                size_t size, void* out) {
    struct willrt_blocking_op* job;
    int32_t started = 0;

    if (fn == NULL) {
        return NULL;
    }
    willrt_blocking_init();
    if (atomic_load(&willrt_blocking_state) != 2) {
        return NULL;
    }
    job = (struct willrt_blocking_op*)willrt_alloc(sizeof(*job) + size);
    if (job == NULL) {
        return NULL;
    }
    memset(job, 0, sizeof(*job));
    job->fn = (willrt_blocking_fn)fn;
    job->arg = size == 0 ? NULL : (void*)(job + 1);
    job->out = out;
    job->owner = willrt_task_current != NULL ? willrt_task_current() : NULL;
    if (size != 0) {
        memcpy(job->arg, arg, size);
    }

    willrt_os_mutex_lock(&willrt_blocking_lock);
    if (willrt_blocking_stop) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    if (!willrt_blocking_push(job)) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    if (willrt_blocking_created < willrt_blocking_max &&
        willrt_blocking_idle == 0) {
        size_t index = willrt_blocking_created;

        willrt_mem_set_shared();
        if (willrt_os_thread_start(&willrt_blocking_threads[index],
                                   willrt_blocking_worker, NULL) == 0) {
            willrt_blocking_created++;
            started = 1;
        }
    }
    willrt_os_cond_signal(&willrt_blocking_work);
    if (!started && willrt_blocking_created == 0) {
        willrt_blocking_count--;
        willrt_blocking_tail = (willrt_blocking_tail + willrt_blocking_cap - 1)
                               % willrt_blocking_cap;
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        willrt_free(job);
        return NULL;
    }
    atomic_fetch_add(&willrt_blocking_submits, 1);
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    return (willrt_blocking_op*)job;
}

int64_t willrt_blocking_submitted(void) {
    return atomic_load(&willrt_blocking_submits);
}

int32_t willrt_blocking_poll(willrt_blocking_op* op_raw) {
    struct willrt_blocking_op* op = (struct willrt_blocking_op*)op_raw;
    int32_t done;

    if (op == NULL) {
        return 0;
    }
    willrt_os_mutex_lock(&willrt_blocking_lock);
    done = op->done;
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    return done;
}

int32_t willrt_blocking_finish(willrt_blocking_op* op_raw) {
    struct willrt_blocking_op* op = (struct willrt_blocking_op*)op_raw;
    int32_t status;

    if (op == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&willrt_blocking_lock);
    if (!op->done) {
        willrt_os_mutex_unlock(&willrt_blocking_lock);
        return WILLRT_SUSPEND;
    }
    status = op->status;
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    willrt_free(op);
    return status;
}

int32_t willrt_blocking_wait(willrt_blocking_op* op_raw) {
    struct willrt_blocking_op* op = (struct willrt_blocking_op*)op_raw;
    int32_t status;

    if (op == NULL) {
        return WILLRT_FAILED;
    }
    /* This is the synchronous bridge for code that still has a thread to
       block. It waits on the pool condition rather than spinning. A generated
       state machine never calls it; it parks the frame and later polls. */
    willrt_os_mutex_lock(&willrt_blocking_lock);
    while (!op->done) {
        willrt_os_cond_wait(&willrt_blocking_work, &willrt_blocking_lock);
    }
    status = op->status;
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    willrt_free(op);
    return status;
}

void willrt_blocking_shutdown(void) {
    size_t i;

    if (atomic_load(&willrt_blocking_state) != 2) {
        return;
    }
    willrt_os_mutex_lock(&willrt_blocking_lock);
    willrt_blocking_stop = 1;
    willrt_os_cond_broadcast(&willrt_blocking_work);
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    for (i = 0; i < willrt_blocking_created; i++) {
        willrt_os_thread_join(&willrt_blocking_threads[i]);
    }
    willrt_os_mutex_lock(&willrt_blocking_lock);
    willrt_blocking_created = 0;
    willrt_blocking_count = 0;
    willrt_blocking_head = 0;
    willrt_blocking_tail = 0;
    willrt_free(willrt_blocking_queue);
    willrt_blocking_queue = NULL;
    willrt_blocking_cap = 0;
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    willrt_os_mutex_destroy(&willrt_blocking_lock);
    willrt_os_cond_destroy(&willrt_blocking_work);
    atomic_store(&willrt_blocking_state, 0);
}
