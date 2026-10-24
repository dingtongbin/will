/* Job objects on per-lane queues.
 *
 * A job is a task with no suspension point. The submitting lane pushes it on
 * its own queue; an external thread picks the non-reserved lane with the
 * shortest queue. A lane pops its local queue head and steals from the head of
 * another lane when its own queues are empty. The lane worker holds its lane
 * lock while it probes the local job queue, so an enqueue cannot land between
 * the probe and the wait without delivering the wake.
 */

#include "../../../include/willrt.h"

#include <limits.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

typedef struct job_queue {
    willrt_os_mutex lock;
    willrt_task* head;
    willrt_task* tail;
    int32_t count;
} job_queue;

static atomic_int job_state;
static job_queue* job_queues;
static int32_t job_queue_count;

/* job_ensure starts the lane pool and allocates one queue per lane. A failed
   allocation leaves the queues empty; job submission then runs on the caller
   thread so a handle still completes. */
static int32_t job_ensure(void) {
    int expected = 0;
    int32_t count;
    int32_t i;

    if (atomic_load(&job_state) == 2) {
        return 1;
    }
    if (atomic_load(&job_state) == 3) {
        return 0;
    }
    if (atomic_compare_exchange_strong(&job_state, &expected, 1)) {
        job_queue* queues;

        count = willrt_lane_ensure();
        if (count < 1) {
            atomic_store(&job_state, 3);
            return 0;
        }
        queues = (job_queue*)willrt_alloc(sizeof(*queues) * (size_t)count);
        if (queues == NULL) {
            atomic_store(&job_state, 3);
            return 0;
        }
        memset(queues, 0, sizeof(*queues) * (size_t)count);
        for (i = 0; i < count; i++) {
            willrt_os_mutex_init(&queues[i].lock);
        }
        job_queues = queues;
        job_queue_count = count;
        atomic_store(&job_state, 2);
        return 1;
    }
    while (atomic_load(&job_state) == 1) {
        willrt_os_thread_yield();
    }
    return atomic_load(&job_state) == 2;
}

/* job_pick keeps a job on the submitting lane when that lane can run it.
   Otherwise it picks the non-reserved lane with the shortest local queue. */
static int32_t job_pick(void) {
    int32_t skip = -1;
    int32_t best = -1;
    int32_t best_count = INT32_MAX;
    int32_t i;

    if (willrt_task_current() != NULL) {
        int32_t here = willrt_lane_here();

        /* The submitting task blocks in region_exit while the jobs run. It must
           not leave the jobs on its own lane: that lane cannot run them until
           the task returns. Pick another job-capable lane. */
        if (here >= 0 && here < job_queue_count &&
            !willrt_lane_is_reserved(here)) {
            skip = here;
        }
    }
    for (i = 0; i < job_queue_count; i++) {
        int32_t count;

        if (i == skip || willrt_lane_is_reserved(i)) {
            continue;
        }
        willrt_os_mutex_lock(&job_queues[i].lock);
        count = job_queues[i].count;
        willrt_os_mutex_unlock(&job_queues[i].lock);
        if (count < best_count) {
            best_count = count;
            best = i;
        }
    }
    return best;
}

static void job_push(int32_t lane, willrt_task* task) {
    job_queue* queue = &job_queues[lane];

    willrt_task_set_job_next(task, NULL);
    willrt_os_mutex_lock(&queue->lock);
    if (queue->tail == NULL) {
        queue->head = task;
    } else {
        willrt_task_set_job_next(queue->tail, task);
    }
    queue->tail = task;
    queue->count++;
    willrt_os_mutex_unlock(&queue->lock);
    willrt_lane_wake_jobs();
}

/* job_take_head removes the oldest job from one queue. The caller holds the
   queue lock. */
static willrt_task* job_take_head(job_queue* queue) {
    willrt_task* task = queue->head;

    if (task == NULL) {
        return NULL;
    }
    queue->head = willrt_task_job_next(task);
    if (queue->head == NULL) {
        queue->tail = NULL;
    }
    willrt_task_set_job_next(task, NULL);
    queue->count--;
    return task;
}

willrt_task* willrt_job_pop(int32_t lane) {
    willrt_task* task;

    if (atomic_load(&job_state) != 2 || lane < 0 || lane >= job_queue_count) {
        return NULL;
    }
    willrt_os_mutex_lock(&job_queues[lane].lock);
    task = job_take_head(&job_queues[lane]);
    willrt_os_mutex_unlock(&job_queues[lane].lock);
    return task;
}

willrt_task* willrt_job_steal(int32_t lane) {
    int32_t offset;

    if (atomic_load(&job_state) != 2 || lane < 0 || lane >= job_queue_count) {
        return NULL;
    }
    for (offset = 1; offset < job_queue_count; offset++) {
        int32_t victim = (lane + offset) % job_queue_count;
        willrt_task* task;

        if (willrt_lane_is_reserved(victim)) {
            continue;
        }
        willrt_os_mutex_lock(&job_queues[victim].lock);
        task = job_take_head(&job_queues[victim]);
        willrt_os_mutex_unlock(&job_queues[victim].lock);
        if (task != NULL) {
            return task;
        }
    }
    return NULL;
}

int32_t willrt_jobs_pending(void) {
    int32_t pending = 0;
    int32_t i;

    if (atomic_load(&job_state) != 2) {
        return 0;
    }
    for (i = 0; i < job_queue_count; i++) {
        willrt_os_mutex_lock(&job_queues[i].lock);
        pending += job_queues[i].count;
        willrt_os_mutex_unlock(&job_queues[i].lock);
    }
    return pending;
}

void willrt_jobs_shutdown(void) {
    /* The lane pool owns execution. Shutdown has already joined every live
       task, so there is no worker thread to stop here. */
    if (atomic_load(&job_state) == 2) {
        atomic_store(&job_state, 3);
    }
}

willrt_task* willrt_job_new(void* step, void* frame, size_t size,
                            int32_t prio) {
    willrt_task* task;

    task = willrt_task_new(step, frame, size, 0, prio);
    if (task == NULL) {
        return NULL;
    }
    willrt_task_mark_job(task);
    if (!job_ensure()) {
        willrt_task_run_job(task);
        return task;
    }
    {
        int32_t lane = job_pick();

        if (lane < 0) {
            willrt_task_run_job(task);
            return task;
        }
        job_push(lane, task);
    }
    return task;
}
