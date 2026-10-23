/* Global job queue and its fixed worker pool.
 *
 * A job is a task object created by willrt_task_new. The object and the step
 * contract are shared with tasks; only the delivery path differs. willrt_job_new
 * appends the object to one global FIFO and workers advance it on their own
 * threads. A worker spins briefly before docking on the queue condition, so an
 * empty queue is a normal sleeping pool, not a deadlock.
 *
 * The worker count is fixed for the life of the pool at the platform's
 * available processor count. Workers do not steal, so a job stays on the queue
 * until one of them takes it. willrt_shutdown reaches willrt_jobs_shutdown
 * through the optional hook in task.c after the live task count reaches zero.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>

typedef struct job_entry {
    willrt_task* task;
    struct job_entry* next;
} job_entry;

typedef struct job_worker {
    willrt_os_thread thread;
    int32_t id;
} job_worker;

enum {
    JOB_STATE_EMPTY = 0,
    JOB_STATE_STARTING = 1,
    JOB_STATE_READY = 2,
    JOB_STATE_STOPPED = 3,
    JOB_SPINS = 64
};

static atomic_int job_state;
static willrt_os_mutex job_lock;
static willrt_os_cond job_cond;
static job_entry* job_head;
static job_entry* job_tail;
static job_worker* job_workers;
static int32_t job_workers_started;
static int32_t job_stopping;

extern void willrt_task_run_job(willrt_task* task);

/* job_pop removes the oldest entry. The caller holds job_lock. */
static job_entry* job_pop(void) {
    job_entry* entry = job_head;

    if (entry == NULL) {
        return NULL;
    }
    job_head = entry->next;
    if (job_head == NULL) {
        job_tail = NULL;
    }
    entry->next = NULL;
    return entry;
}

/* job_worker_main takes jobs until shutdown sees an empty queue. */
static void job_worker_main(void* raw) {
    job_worker* worker = (job_worker*)raw;

    /* Job workers share no lane index with async tasks: the upper half of
       the lane table keeps a job churn from taking the lock a task needs. */
    willrt_mem_set_lane(worker->id + 32);
    for (;;) {
        job_entry* entry = NULL;
        int spin;

        for (spin = 0; spin < JOB_SPINS; spin++) {
            willrt_os_mutex_lock(&job_lock);
            entry = job_pop();
            if (entry != NULL || job_stopping) {
                willrt_os_mutex_unlock(&job_lock);
                break;
            }
            willrt_os_mutex_unlock(&job_lock);
            willrt_os_thread_yield();
        }
        if (entry == NULL) {
            willrt_os_mutex_lock(&job_lock);
            while (job_head == NULL && !job_stopping) {
                willrt_os_cond_wait(&job_cond, &job_lock);
            }
            entry = job_pop();
            if (entry == NULL && job_stopping) {
                willrt_os_mutex_unlock(&job_lock);
                break;
            }
            willrt_os_mutex_unlock(&job_lock);
        }
        if (entry != NULL) {
            willrt_task_run_job(entry->task);
            willrt_free(entry);
        }
    }
    willrt_mem_thread_flush();
}

/* job_start creates the fixed pool once. A failure to start any worker leaves
   the pool empty; job submission then runs the task on the caller thread so a
   queued handle still finishes. */
static int32_t job_start(void) {
    int expected = JOB_STATE_EMPTY;
    int32_t count;
    int32_t i;

    if (atomic_load(&job_state) == JOB_STATE_READY) {
        return 1;
    }
    if (atomic_compare_exchange_strong(&job_state, &expected,
                                       JOB_STATE_STARTING)) {
        willrt_os_mutex_init(&job_lock);
        willrt_os_cond_init(&job_cond);
        count = willrt_os_cpu_count();
        if (count < 1) {
            count = 1;
        }
        job_workers = (job_worker*)willrt_alloc(sizeof(*job_workers) *
                                                (size_t)count);
        if (job_workers != NULL) {
            willrt_mem_set_shared();
            willrt_rc_set_threaded();
            for (i = 0; i < count; i++) {
                job_workers[i].id = i;
                if (willrt_os_thread_start(&job_workers[i].thread,
                                           job_worker_main,
                                           &job_workers[i]) != 0) {
                    break;
                }
                job_workers_started++;
            }
        }
        atomic_store(&job_state, JOB_STATE_READY);
        return job_workers_started != 0;
    }
    while (atomic_load(&job_state) == JOB_STATE_STARTING) {
        willrt_os_thread_yield();
    }
    return atomic_load(&job_state) == JOB_STATE_READY &&
           job_workers_started != 0;
}

willrt_task* willrt_job_new(void* step, void* frame, size_t size,
                            int32_t prio) {
    willrt_task* task;
    job_entry* entry;

    task = willrt_task_new(step, frame, size, 0, prio);
    if (task == NULL) {
        return NULL;
    }
    if (!job_start()) {
        willrt_task_run_job(task);
        return task;
    }
    entry = (job_entry*)willrt_alloc(sizeof(*entry));
    if (entry == NULL) {
        willrt_task_run_job(task);
        return task;
    }
    entry->task = task;
    entry->next = NULL;
    willrt_os_mutex_lock(&job_lock);
    if (job_tail == NULL) {
        job_head = entry;
    } else {
        job_tail->next = entry;
    }
    job_tail = entry;
    willrt_os_cond_signal(&job_cond);
    willrt_os_mutex_unlock(&job_lock);
    return task;
}

/* willrt_jobs_pending reports whether the global queue still holds a job.
   Deadlock detection treats a queued job as runnable work even when no task is
   currently executing. */
int32_t willrt_jobs_pending(void) {
    int32_t pending;

    if (atomic_load(&job_state) != JOB_STATE_READY) {
        return 0;
    }
    willrt_os_mutex_lock(&job_lock);
    pending = job_head != NULL;
    willrt_os_mutex_unlock(&job_lock);
    return pending;
}

/* willrt_jobs_shutdown stops the pool after the live task count reaches zero.
   Workers drain any entry that is already queued before they observe the stop
   flag. Repeated shutdown calls are harmless. */
void willrt_jobs_shutdown(void) {
    int32_t i;

    if (atomic_load(&job_state) != JOB_STATE_READY) {
        return;
    }
    willrt_os_mutex_lock(&job_lock);
    job_stopping = 1;
    willrt_os_cond_broadcast(&job_cond);
    willrt_os_mutex_unlock(&job_lock);
    for (i = 0; i < job_workers_started; i++) {
        willrt_os_thread_join(&job_workers[i].thread);
    }
    atomic_store(&job_state, JOB_STATE_STOPPED);
}
