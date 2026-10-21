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

enum {
    WILLRT_BLOCKING_LIMIT = 128,
    WILLRT_BLOCKING_QUEUE = 1024
};

struct willrt_blocking_job {
    willrt_blocking_fn fn;
    void* arg;
    void* out;
    int32_t status;
    int32_t done;
};

static struct willrt_blocking_job* willrt_blocking_queue[WILLRT_BLOCKING_QUEUE];
static willrt_os_thread willrt_blocking_threads[WILLRT_BLOCKING_LIMIT];
static willrt_os_mutex willrt_blocking_lock;
static willrt_os_cond willrt_blocking_work;
static willrt_os_cond willrt_blocking_space;
static atomic_int willrt_blocking_state;
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
        willrt_os_cond_init(&willrt_blocking_space);
        willrt_blocking_stop = 0;
        willrt_blocking_head = 0;
        willrt_blocking_tail = 0;
        willrt_blocking_count = 0;
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

static void willrt_blocking_worker(void* ignored) {
    (void)ignored;
    for (;;) {
        struct willrt_blocking_job* job;

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
            (willrt_blocking_head + 1) % WILLRT_BLOCKING_QUEUE;
        willrt_blocking_count--;
        willrt_os_cond_signal(&willrt_blocking_space);
        willrt_os_mutex_unlock(&willrt_blocking_lock);

        job->status = job->fn(job->arg, job->out);

        willrt_os_mutex_lock(&willrt_blocking_lock);
        job->done = 1;
        willrt_os_cond_broadcast(&willrt_blocking_work);
        willrt_os_mutex_unlock(&willrt_blocking_lock);
    }
}

int32_t willrt_blocking_call(void* fn, void* arg, void* out) {
    struct willrt_blocking_job job;
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
    while (willrt_blocking_count == WILLRT_BLOCKING_QUEUE) {
        willrt_os_cond_wait(&willrt_blocking_space, &willrt_blocking_lock);
    }
    willrt_blocking_queue[willrt_blocking_tail] = &job;
    willrt_blocking_tail =
        (willrt_blocking_tail + 1) % WILLRT_BLOCKING_QUEUE;
    willrt_blocking_count++;
    if (willrt_blocking_created < willrt_blocking_max
        && willrt_blocking_idle == 0) {
        size_t index = willrt_blocking_created;

        willrt_rc_set_shared();
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
            (willrt_blocking_tail + WILLRT_BLOCKING_QUEUE - 1)
            % WILLRT_BLOCKING_QUEUE;
        willrt_os_cond_signal(&willrt_blocking_space);
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
    willrt_os_mutex_unlock(&willrt_blocking_lock);
    willrt_os_mutex_destroy(&willrt_blocking_lock);
    willrt_os_cond_destroy(&willrt_blocking_work);
    willrt_os_cond_destroy(&willrt_blocking_space);
    atomic_store(&willrt_blocking_state, 0);
}
