/* Contract test for jobs on lane-local deques. The test enqueues more jobs
   than one lane could finish before shutdown, joins every handle, and proves
   that jobs run on lane threads instead of a separate fixed pool. It then
   shuts the runtime down twice to prove that an empty queue and a repeated
   shutdown do not hang or leave queued work behind. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>

enum { JOB_COUNT = 1000 };

typedef struct job_frame {
    int32_t value;
} job_frame;

static atomic_int completed;
static atomic_int started;
static atomic_uint lane_bits;

static int32_t job_step(void* raw) {
    job_frame* frame = (job_frame*)raw;
    int32_t lane = willrt_lane_here();
    int32_t ticket;
    int32_t spin;

    assert(frame != NULL);
    assert(lane >= 0);
    atomic_fetch_or(&lane_bits, 1u << (uint32_t)(lane & 31));
    ticket = atomic_fetch_add(&started, 1);
    if (ticket == 0) {
        /* The first job keeps its lane busy until a second job runs on another
           lane. A single job lane can only make progress after the timeout. */
        for (spin = 0; spin < 200000; spin++) {
            if (atomic_load(&started) >= 2) {
                break;
            }
            willrt_os_thread_yield();
        }
    }
    atomic_fetch_add(&completed, 1);
    return WILLRT_DONE;
}

static atomic_int block_started;
static atomic_int block_gate;

static int32_t blocked_job(void* raw) {
    (void)raw;
    atomic_store(&block_started, 1);
    while (atomic_load(&block_gate) == 0) {
        willrt_os_thread_yield();
    }
    return WILLRT_DONE;
}

static int32_t lane_count_seen(void) {
    uint32_t bits = atomic_load(&lane_bits);
    int32_t count = 0;
    int32_t i;

    for (i = 0; i < 32; i++) {
        if ((bits & (1u << (uint32_t)i)) != 0) {
            count++;
        }
    }
    return count;
}

int main(void) {
    job_frame* frames;
    willrt_task** tasks;
    int32_t i;

    willrt_init();
    frames = (job_frame*)willrt_alloc(sizeof(*frames) * JOB_COUNT);
    tasks = (willrt_task**)willrt_alloc(sizeof(*tasks) * JOB_COUNT);
    assert(frames != NULL);
    assert(tasks != NULL);

    for (i = 0; i < JOB_COUNT; i++) {
        frames[i].value = i;
        tasks[i] = willrt_job_new((void*)job_step, &frames[i], sizeof(frames[i]),
                                  0);
        assert(tasks[i] != NULL);
    }
    for (i = 0; i < JOB_COUNT; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
    }
    assert(atomic_load(&completed) == JOB_COUNT);
    assert(willrt_jobs_pending() == 0);
    if (willrt_os_cpu_count() >= 4) {
        assert(lane_count_seen() >= 2);
    }
    assert(atomic_load(&started) == JOB_COUNT);

    {
        willrt_task* running = willrt_job_new((void*)blocked_job, NULL, 0, 0);

        assert(running != NULL);
        while (atomic_load(&block_started) == 0) {
            willrt_os_thread_yield();
        }
        willrt_task_cancel(running);
        atomic_store(&block_gate, 1);
        assert(willrt_join(running, NULL, NULL) == WILLRT_OK);
    }

    willrt_shutdown();
    willrt_shutdown();
    return 0;
}
