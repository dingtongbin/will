/* Tasks, regions, and the task environment.
 *
 * This engine runs every task on its own platform thread and keeps the lane
 * number as an affinity tag: the thread records it for the allocator and for
 * willrt_lane_here, and the allocator keeps that lane's arenas separate. A
 * spawn therefore never migrates, and a blocked wait blocks only the thread of
 * that task.
 *
 * The state-machine contract is kept at the API edge: willrt_task_new takes a
 * step and a frame, and a step that returns WILLRT_DONE finishes the task. A
 * step that returns WILLRT_SUSPEND is called again on this thread after a
 * yield; the first compiler lowering uses that for a single Join. The lane
 * pool, work stealing, and the global job queue described in the concurrency
 * volume do not exist yet; this file is the portable fallback that keeps the
 * visible semantics exact until that scheduler lands.
 *
 * A panic inside a task writes its report at the task boundary, records the
 * failure on the task, and ends only that thread. willrt_join reports the
 * failure and region_exit counts it.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

struct willrt_task {
    willrt_os_thread thread;
    willrt_os_mutex lock;
    willrt_os_cond cond;
    willrt_step step;
    void* frame;
    size_t frame_size;
    int32_t lane;
    int32_t prio;
    int32_t started;
    int32_t finished;
    int32_t failed;
    int32_t canceled;
    int64_t deadline;
    unsigned char* result;
    size_t result_size;
    char* message;
    size_t message_size;
    const char* site;
    const char* symbol;
    struct willrt_task* next_region;
};

struct willrt_region {
    willrt_os_mutex lock;
    struct willrt_task* head;
    int32_t failed;
};

enum { WILLRT_REGION_DEPTH = 64 };

static _Thread_local struct willrt_task* willrt_task_self;
static _Thread_local struct willrt_region willrt_regions[WILLRT_REGION_DEPTH];
static _Thread_local int willrt_region_depth;

static atomic_int willrt_task_globals_state;
static atomic_int willrt_tasks_live;
static atomic_int willrt_activity_count;
static willrt_os_mutex willrt_tasks_lock;
static willrt_os_cond willrt_tasks_cond;

static void willrt_task_globals(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_task_globals_state, &expected, 1)) {
        willrt_os_mutex_init(&willrt_tasks_lock);
        willrt_os_cond_init(&willrt_tasks_cond);
        atomic_store(&willrt_task_globals_state, 2);
        return;
    }
    while (atomic_load(&willrt_task_globals_state) != 2) {
        willrt_os_thread_yield();
    }
}

static void willrt_task_finish(struct willrt_task* task) {
    willrt_os_mutex_lock(&task->lock);
    task->finished = 1;
    willrt_os_cond_broadcast(&task->cond);
    willrt_os_mutex_unlock(&task->lock);

    willrt_os_mutex_lock(&willrt_tasks_lock);
    atomic_fetch_sub(&willrt_tasks_live, 1);
    willrt_os_cond_broadcast(&willrt_tasks_cond);
    willrt_os_mutex_unlock(&willrt_tasks_lock);
}

static void willrt_task_entry(void* raw) {
    struct willrt_task* task = (struct willrt_task*)raw;
    int32_t status;

    willrt_task_self = task;
    willrt_mem_set_lane(task->lane);
    do {
        status = task->step(task->frame);
        if (status == WILLRT_SUSPEND) {
            willrt_os_thread_yield();
        }
    } while (status == WILLRT_SUSPEND);
    willrt_task_finish(task);
    willrt_mem_thread_flush();
}

willrt_task* willrt_task_new(void* step, void* frame, size_t size, int32_t lane,
                             int32_t prio) {
    struct willrt_task* task;
    struct willrt_region* region;

    willrt_task_globals();
    task = (struct willrt_task*)willrt_alloc(sizeof(*task));
    if (task == NULL) {
        return NULL;
    }
    memset(task, 0, sizeof(*task));
    task->step = (willrt_step)step;
    task->frame = frame;
    task->frame_size = size;
    task->lane = lane < 0 ? 0 : lane;
    task->prio = prio;
    willrt_os_mutex_init(&task->lock);
    willrt_os_cond_init(&task->cond);

    willrt_os_mutex_lock(&willrt_tasks_lock);
    atomic_fetch_add(&willrt_tasks_live, 1);
    willrt_os_mutex_unlock(&willrt_tasks_lock);

    if (willrt_region_depth > 0) {
        region = &willrt_regions[willrt_region_depth - 1];
        willrt_os_mutex_lock(&region->lock);
        task->next_region = region->head;
        region->head = task;
        willrt_os_mutex_unlock(&region->lock);
    }
    return task;
}

void willrt_task_run(willrt_task* task) {
    if (task == NULL || task->started) {
        return;
    }
    task->started = 1;
    willrt_rc_set_shared();
    willrt_mem_set_shared();
    if (willrt_os_thread_start(&task->thread, willrt_task_entry, task) != 0) {
        task->failed = 1;
        willrt_task_finish(task);
    }
}

willrt_task* willrt_task_current(void) {
    return willrt_task_self;
}

int32_t willrt_task_store_result(const void* value, size_t size) {
    struct willrt_task* task = willrt_task_self;
    unsigned char* copy;

    if (task == NULL || value == NULL || size == 0) {
        return WILLRT_OK;
    }
    copy = (unsigned char*)willrt_alloc(size);
    if (copy == NULL) {
        return WILLRT_FAILED;
    }
    memcpy(copy, value, size);
    willrt_os_mutex_lock(&task->lock);
    if (task->result == NULL) {
        task->result = copy;
        task->result_size = size;
    }
    willrt_os_mutex_unlock(&task->lock);
    return WILLRT_OK;
}

/* willrt_task_try_join performs one nonblocking join attempt. A suspended step
   calls it again after the dependency has made progress. */
int32_t willrt_task_try_join(willrt_task* task, void* out, willrt_error* err) {
    struct willrt_task* current = willrt_task_self;

    if (task == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&task->lock);
    if (!task->finished) {
        if (current != NULL) {
            if (current->canceled) {
                willrt_os_mutex_unlock(&task->lock);
                return WILLRT_CANCELED;
            }
            if (current->deadline != 0 && willrt_now_ns() >= current->deadline) {
                willrt_os_mutex_unlock(&task->lock);
                return WILLRT_TIMEDOUT;
            }
        }
        willrt_os_mutex_unlock(&task->lock);
        return WILLRT_SUSPEND;
    }
    if (err != NULL) {
        err->message = task->message;
        err->size = task->message_size;
        err->site = task->site;
        err->symbol = task->symbol;
    }
    if (task->failed) {
        willrt_os_mutex_unlock(&task->lock);
        return WILLRT_FAILED;
    }
    if (task->canceled) {
        willrt_os_mutex_unlock(&task->lock);
        return WILLRT_CANCELED;
    }
    if (out != NULL && task->result != NULL && task->result_size != 0) {
        memcpy(out, task->result, task->result_size);
    }
    willrt_os_mutex_unlock(&task->lock);
    return WILLRT_OK;
}

int32_t willrt_join(willrt_task* task, void* out, willrt_error* err) {
    struct willrt_task* current = willrt_task_self;

    if (task == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&task->lock);
    while (!task->finished) {
        int64_t wait = INT64_C(10) * INT64_C(1000000);

        if (current != NULL) {
            int64_t deadline = current->deadline;

            if (deadline != 0) {
                int64_t now = willrt_now_ns();
                int64_t left = deadline - now;

                if (left <= 0) {
                    willrt_os_mutex_unlock(&task->lock);
                    return WILLRT_TIMEDOUT;
                }
                if (left < wait) {
                    wait = left;
                }
            }
            if (current->canceled) {
                willrt_os_mutex_unlock(&task->lock);
                return WILLRT_CANCELED;
            }
        }
        willrt_os_cond_timedwait(&task->cond, &task->lock, wait);
    }
    if (err != NULL) {
        err->message = task->message;
        err->size = task->message_size;
        err->site = task->site;
        err->symbol = task->symbol;
    }
    if (task->failed) {
        willrt_os_mutex_unlock(&task->lock);
        return WILLRT_FAILED;
    }
    if (task->canceled) {
        willrt_os_mutex_unlock(&task->lock);
        return WILLRT_CANCELED;
    }
    if (out != NULL && task->result != NULL && task->result_size != 0) {
        memcpy(out, task->result, task->result_size);
    }
    willrt_os_mutex_unlock(&task->lock);
    return WILLRT_OK;
}

/* willrt_task_fail records a panic on the current task and ends that thread.
   The report path calls it before its process exit, so a failure never leaves
   the task boundary. */
int32_t willrt_task_fail(const char* message, size_t size, const char* site,
                         const char* symbol) {
    struct willrt_task* task = willrt_task_self;
    char* copy;

    if (task == NULL) {
        return 0;
    }
    copy = (char*)willrt_alloc(size == 0 ? 1 : size);
    if (copy != NULL && message != NULL && size != 0) {
        memcpy(copy, message, size);
    }
    willrt_os_mutex_lock(&task->lock);
    task->failed = 1;
    task->message = copy;
    task->message_size = size;
    task->site = site;
    task->symbol = symbol;
    willrt_os_mutex_unlock(&task->lock);
    willrt_task_finish(task);
    willrt_os_thread_exit();
    return 1;
}

void willrt_region_enter(void) {
    struct willrt_region* region;

    willrt_task_globals();
    if (willrt_region_depth >= WILLRT_REGION_DEPTH) {
        return;
    }
    region = &willrt_regions[willrt_region_depth];
    memset(region, 0, sizeof(*region));
    willrt_os_mutex_init(&region->lock);
    willrt_region_depth++;
}

int32_t willrt_region_exit(void) {
    struct willrt_region* region;
    struct willrt_task* task;
    int32_t failed = 0;
    char line[64];
    int size;

    if (willrt_region_depth <= 0) {
        return 0;
    }
    willrt_region_depth--;
    region = &willrt_regions[willrt_region_depth];
    willrt_os_mutex_lock(&region->lock);
    task = region->head;
    region->head = NULL;
    willrt_os_mutex_unlock(&region->lock);
    while (task != NULL) {
        struct willrt_task* next = task->next_region;

        if (willrt_join(task, NULL, NULL) == WILLRT_FAILED) {
            failed++;
        }
        task = next;
    }
    if (failed != 0) {
        size = snprintf(line, sizeof(line), "region: %d tasks failed\n", failed);
        if (size > 0) {
            willrt_report_write_n(line,
                                  (size_t)size < sizeof(line) ? (size_t)size
                                                             : sizeof(line));
        }
    }
    willrt_os_mutex_destroy(&region->lock);
    return failed;
}

int32_t willrt_lane_here(void) {
    struct willrt_task* task = willrt_task_self;

    return task == NULL ? 0 : task->lane;
}

void willrt_yield(void) {
    willrt_os_thread_yield();
}

int32_t willrt_task_canceled(void) {
    struct willrt_task* task = willrt_task_self;

    return task == NULL ? 0 : task->canceled;
}

int64_t willrt_task_deadline(void) {
    struct willrt_task* task = willrt_task_self;

    return task == NULL ? 0 : task->deadline;
}

void willrt_activity(int32_t delta) {
    atomic_fetch_add(&willrt_activity_count, delta);
}

void willrt_tasks_wait_all(void) {
    willrt_task_globals();
    willrt_os_mutex_lock(&willrt_tasks_lock);
    while (atomic_load(&willrt_tasks_live) != 0) {
        willrt_os_cond_wait(&willrt_tasks_cond, &willrt_tasks_lock);
    }
    willrt_os_mutex_unlock(&willrt_tasks_lock);
}
