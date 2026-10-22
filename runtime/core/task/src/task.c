/* Tasks, regions, and the task environment.
 *
 * A task is a step over a compiler-built frame, not a platform thread and not
 * a C stack. A fixed lane pool calls the step while the task is ready. A step
 * that returns WILLRT_SUSPEND has already stored its state in the frame; the
 * lane parks it until the IO epoch or a target dependency wakes it, then calls
 * the same step again. Jobs use the same step contract through the job pool.
 *
 * A panic inside a task writes its report, records the failure, and switches
 * back to the lane instead of ending the process. A panic on the main thread
 * keeps the old process-exit path.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#define WILLRT_TASK_STACK (128 * 1024)
#define WILLRT_REGION_DEPTH 64

enum {
    WILLRT_TASK_READY = 0,
    WILLRT_TASK_RUNNING = 1,
    WILLRT_TASK_PARKING = 2,
    WILLRT_TASK_WAITING = 3,
    WILLRT_TASK_DONE = 4
};

struct willrt_task {
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
    int32_t state;
    int32_t queued;
    int32_t wake_pending;
    int32_t wait_requested;
    void* cancel_flag;
    int64_t deadline;
    int64_t wait_deadline;
    unsigned char* result;
    size_t result_size;
    char* message;
    size_t message_size;
    const char* site;
    const char* symbol;
    struct willrt_task* next_region;
    struct willrt_task* wait_next;
    struct willrt_task* io_next;
    struct willrt_task* all_next;
};

struct willrt_region {
    willrt_os_mutex lock;
    struct willrt_task* head;
    int32_t failed;
};

typedef struct lane_worker {
    willrt_os_thread thread;
    int32_t id;
} lane_worker;

static _Thread_local struct willrt_task* willrt_task_self;
static _Thread_local struct willrt_region willrt_regions[WILLRT_REGION_DEPTH];
static _Thread_local int willrt_region_depth;
static _Thread_local int64_t willrt_main_deadline;

static atomic_int willrt_task_globals_state;
static atomic_int willrt_tasks_live;
static atomic_int willrt_threads_live;
static atomic_int willrt_activity_count;
static atomic_uint willrt_io_epoch;
static willrt_os_mutex willrt_tasks_lock;
static willrt_os_cond willrt_tasks_cond;

static willrt_os_mutex willrt_lane_lock;
static willrt_os_cond willrt_lane_cond;
static struct willrt_task* willrt_all_tasks;
static struct willrt_task* willrt_lane_head;
static struct willrt_task* willrt_lane_tail;
static struct willrt_task* willrt_io_head;
static struct willrt_task* willrt_io_tail;
static lane_worker* willrt_lane_workers;
static int32_t willrt_lane_count;
static int32_t willrt_lane_started;
static int32_t willrt_lane_state;
static int32_t willrt_lane_stopping;
static atomic_int willrt_lane_ready;

extern void willrt_jobs_shutdown(void) __attribute__((weak));

static void willrt_lane_init(void);

static void willrt_io_timer(void* arg) {
    (void)arg;
    willrt_sched_wake_io();
}

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

static void willrt_lane_enqueue_locked(struct willrt_task* task) {
    if (task->queued) {
        return;
    }
    task->queued = 1;
    task->state = WILLRT_TASK_READY;
    task->io_next = NULL;
    task->wait_next = NULL;
    if (willrt_lane_tail == NULL) {
        willrt_lane_head = task;
    } else {
        willrt_lane_tail->wait_next = task;
    }
    willrt_lane_tail = task;
}

static struct willrt_task* willrt_lane_pop_locked(void) {
    struct willrt_task* task = willrt_lane_head;

    if (task == NULL) {
        return NULL;
    }
    willrt_lane_head = task->wait_next;
    if (willrt_lane_head == NULL) {
        willrt_lane_tail = NULL;
    }
    task->wait_next = NULL;
    task->queued = 0;
    return task;
}

// willrt_task_wake moves one parked task to the ready queue. A task that is
// still switching out is marked pending; the lane makes it ready after the
// switch has actually happened.
static void willrt_task_wake(struct willrt_task* task) {
    if (task == NULL) {
        return;
    }
    willrt_os_mutex_lock(&willrt_lane_lock);
    if (task->state == WILLRT_TASK_WAITING) {
        willrt_lane_enqueue_locked(task);
        willrt_os_cond_broadcast(&willrt_lane_cond);
    } else if (task->state == WILLRT_TASK_PARKING ||
               task->state == WILLRT_TASK_RUNNING) {
        task->wake_pending = 1;
    }
    willrt_os_mutex_unlock(&willrt_lane_lock);
}

// willrt_sched_wake_io wakes every task parked on a channel or select. The
// epoch closes the check-then-park race in those loops.
void willrt_sched_wake_all(void) {
    struct willrt_task* task;

    if (atomic_load(&willrt_task_globals_state) != 2 ||
        !atomic_load(&willrt_lane_ready)) {
        return;
    }
    willrt_os_mutex_lock(&willrt_tasks_lock);
    task = willrt_all_tasks;
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    while (task != NULL) {
        willrt_task_wake(task);
        task = task->all_next;
    }
    willrt_sched_wake_io();
}

void willrt_sched_wake_io(void) {
    struct willrt_task* task;

    atomic_fetch_add(&willrt_io_epoch, 1);
    if (!atomic_load(&willrt_lane_ready)) {
        return;
    }
    willrt_os_mutex_lock(&willrt_lane_lock);
    task = willrt_io_head;
    willrt_io_head = NULL;
    willrt_io_tail = NULL;
    while (task != NULL) {
        struct willrt_task* next = task->io_next;
        task->io_next = NULL;
        if (task->state == WILLRT_TASK_WAITING) {
            willrt_lane_enqueue_locked(task);
        } else if (task->state == WILLRT_TASK_PARKING ||
                   task->state == WILLRT_TASK_RUNNING) {
            task->wake_pending = 1;
        }
        task = next;
    }
    willrt_os_cond_broadcast(&willrt_lane_cond);
    willrt_os_mutex_unlock(&willrt_lane_lock);
}

uint32_t willrt_sched_io_epoch(void) {
    return atomic_load(&willrt_io_epoch);
}

// willrt_task_wait_io is a compatibility no-op. A generated step parks its
// frame with WILLRT_SUSPEND and is woken by the IO epoch or a timer; a caller
// that still has a thread waits on its own condition instead of spinning here.
void willrt_task_wait_io(uint32_t epoch, int64_t deadline) {
    (void)epoch;
    (void)deadline;
}

static void willrt_task_park(struct willrt_task* task) {
    willrt_timer* timer = NULL;
    int64_t wake = task->wait_deadline;

    if (task->deadline > 0 && (wake == 0 || task->deadline < wake)) {
        wake = task->deadline;
    }
    if (wake > 0 && willrt_now_ns() < wake) {
        timer = willrt_timer_new(wake, willrt_io_timer, NULL);
    }
    task->wait_deadline = 0;
    willrt_os_mutex_lock(&willrt_lane_lock);
    task->state = WILLRT_TASK_WAITING;
    task->io_next = NULL;
    if (willrt_io_tail == NULL) {
        willrt_io_head = task;
    } else {
        willrt_io_tail->io_next = task;
    }
    willrt_io_tail = task;
    willrt_os_mutex_unlock(&willrt_lane_lock);
    (void)timer;
}

static void willrt_task_finish(struct willrt_task* task) {
    struct willrt_task* waiter;

    willrt_os_mutex_lock(&task->lock);
    task->finished = 1;
    waiter = task->wait_next;
    task->wait_next = NULL;
    willrt_os_cond_broadcast(&task->cond);
    willrt_os_mutex_unlock(&task->lock);

    while (waiter != NULL) {
        struct willrt_task* next = waiter->wait_next;
        waiter->wait_next = NULL;
        willrt_task_wake(waiter);
        waiter = next;
    }

    willrt_os_mutex_lock(&willrt_tasks_lock);
    atomic_fetch_sub(&willrt_tasks_live, 1);
    willrt_os_cond_broadcast(&willrt_tasks_cond);
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    willrt_sched_wake_io();
}

static void willrt_task_run_step(struct willrt_task* task) {
    int32_t status;

    task->state = WILLRT_TASK_RUNNING;
    task->wait_requested = 0;
    willrt_task_self = task;
    willrt_mem_set_lane(task->lane);
    status = task->step(task->frame);
    willrt_task_self = NULL;
    if (status == WILLRT_SUSPEND) {
        if (task->wait_requested) {
            willrt_task_park(task);
        } else {
            willrt_os_mutex_lock(&willrt_lane_lock);
            willrt_lane_enqueue_locked(task);
            willrt_os_cond_broadcast(&willrt_lane_cond);
            willrt_os_mutex_unlock(&willrt_lane_lock);
        }
        return;
    }
    willrt_task_finish(task);
    task->state = WILLRT_TASK_DONE;
}

void willrt_task_mark_wait(void) {
    if (willrt_task_self != NULL) {
        willrt_task_self->wait_requested = 1;
    }
}

static void willrt_lane_worker(void* raw) {
    lane_worker* worker = (lane_worker*)raw;

    willrt_mem_set_lane(worker->id);
    for (;;) {
        struct willrt_task* task;

        willrt_os_mutex_lock(&willrt_lane_lock);
        while (willrt_lane_head == NULL && !willrt_lane_stopping) {
            willrt_os_cond_wait(&willrt_lane_cond, &willrt_lane_lock);
        }
        task = willrt_lane_pop_locked();
        willrt_os_mutex_unlock(&willrt_lane_lock);
        if (task == NULL && willrt_lane_stopping) {
            break;
        }
        if (task == NULL) {
            continue;
        }
        willrt_task_run_step(task);
    }
    willrt_mem_thread_flush();
}

static void willrt_lane_init(void) {
    int32_t count;
    int32_t i;

    if (willrt_lane_state == 2) {
        return;
    }
    if (willrt_lane_state == 1) {
        while (willrt_lane_state != 2) {
            willrt_os_thread_yield();
        }
        return;
    }
    willrt_lane_state = 1;
    willrt_os_mutex_init(&willrt_lane_lock);
    willrt_os_cond_init(&willrt_lane_cond);
    count = willrt_os_cpu_count();
    if (count < 1) {
        count = 1;
    }
    willrt_lane_workers = (lane_worker*)willrt_alloc(
        sizeof(*willrt_lane_workers) * (size_t)count);
    if (willrt_lane_workers != NULL) {
        atomic_store(&willrt_threads_live, 1);
        willrt_mem_set_shared();
        willrt_rc_set_threaded();
        for (i = 0; i < count; i++) {
            willrt_lane_workers[i].id = i;
            if (willrt_os_thread_start(&willrt_lane_workers[i].thread,
                                       willrt_lane_worker,
                                       &willrt_lane_workers[i]) != 0) {
                break;
            }
            willrt_lane_started++;
        }
    }
    willrt_lane_count = willrt_lane_started;
    willrt_lane_state = 2;
    atomic_store(&willrt_lane_ready, 1);
}

static void willrt_lane_shutdown(void) {
    int32_t i;

    if (willrt_lane_state != 2) {
        return;
    }
    willrt_os_mutex_lock(&willrt_lane_lock);
    willrt_lane_stopping = 1;
    willrt_os_cond_broadcast(&willrt_lane_cond);
    willrt_os_mutex_unlock(&willrt_lane_lock);
    for (i = 0; i < willrt_lane_started; i++) {
        willrt_os_thread_join(&willrt_lane_workers[i].thread);
    }
    willrt_lane_state = 3;
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
    task->all_next = willrt_all_tasks;
    willrt_all_tasks = task;
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
    atomic_store(&willrt_threads_live, 1);
    willrt_rc_set_threaded();
    willrt_mem_set_shared();
    willrt_lane_init();
    if (willrt_lane_started == 0) {
        willrt_task_run_step(task);
        return;
    }
    willrt_os_mutex_lock(&willrt_lane_lock);
    willrt_lane_enqueue_locked(task);
    willrt_os_cond_broadcast(&willrt_lane_cond);
    willrt_os_mutex_unlock(&willrt_lane_lock);
}

/* willrt_task_run_job advances one task on the caller thread. Jobs contain no
   suspension point, so a returned suspend is a failed job rather than a park. */
void willrt_task_run_job(willrt_task* task) {
    if (task == NULL) {
        return;
    }
    atomic_store(&willrt_threads_live, 1);
    task->started = 1;
    willrt_task_run_step(task);
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

int32_t willrt_task_try_join(willrt_task* task, void* out, willrt_error* err) {
    struct willrt_task* current = willrt_task_self;

    if (task == NULL) {
        return WILLRT_FAILED;
    }
    if (current != NULL) {
        willrt_os_mutex_lock(&task->lock);
        if (task->finished) {
            int32_t status = task->failed ? WILLRT_FAILED : WILLRT_OK;
            if (out != NULL && task->result != NULL && task->result_size != 0) {
                memcpy(out, task->result, task->result_size);
            }
            if (err != NULL) {
                err->message = task->message;
                err->size = task->message_size;
                err->site = task->site;
                err->symbol = task->symbol;
            }
            willrt_os_mutex_unlock(&task->lock);
            return status;
        }
        willrt_os_mutex_unlock(&task->lock);
        if (willrt_task_canceled()) {
            return WILLRT_CANCELED;
        }
        if (current->deadline != 0 && willrt_now_ns() >= current->deadline) {
            return WILLRT_TIMEDOUT;
        }
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    willrt_os_mutex_lock(&task->lock);
    if (!task->finished) {
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
    for (;;) {
        willrt_os_mutex_lock(&task->lock);
        if (!task->finished) {
            {
                int64_t wait = INT64_C(10) * INT64_C(1000000);
                if (current != NULL && current->deadline != 0) {
                    int64_t left = current->deadline - willrt_now_ns();
                    if (left <= 0) {
                        willrt_os_mutex_unlock(&task->lock);
                        return WILLRT_TIMEDOUT;
                    }
                    if (left < wait) {
                        wait = left;
                    }
                }
                willrt_os_cond_timedwait(&task->cond, &task->lock, wait);
            }
            willrt_os_mutex_unlock(&task->lock);
            continue;
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
}

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

int32_t willrt_runtime_threaded(void) {
    return atomic_load(&willrt_threads_live) != 0;
}

void willrt_yield(void) {
    willrt_os_thread_yield();
}

int32_t willrt_task_canceled(void) {
    struct willrt_task* task = willrt_task_self;

    if (task == NULL) {
        return 0;
    }
    if (task->canceled) {
        return 1;
    }
    if (task->cancel_flag != NULL) {
        return __atomic_load_n((int32_t*)task->cancel_flag, __ATOMIC_ACQUIRE) != 0;
    }
    return 0;
}

void willrt_task_bind_cancel(willrt_task* task, void* value) {
    if (task != NULL) {
        task->cancel_flag = value == NULL ? NULL : *(void**)value;
    }
}

int64_t willrt_task_deadline(void) {
    struct willrt_task* task = willrt_task_self;

    return task == NULL ? willrt_main_deadline : task->deadline;
}

void willrt_task_sleep_ns(int64_t ns) {
    struct willrt_task* task = willrt_task_self;
    int64_t deadline;

    if (task == NULL || ns <= 0) {
        willrt_os_sleep_ns(ns);
        return;
    }
    deadline = willrt_now_ns() + ns;
    (void)deadline;
    willrt_os_sleep_ns(ns);
}

struct willrt_sleep_op {
    int64_t deadline;
    int32_t status;
};

int32_t willrt_task_sleep_async(int64_t ns) {
    if (ns < 0) {
        return 1;
    }
    willrt_os_sleep_ns(ns);
    return 0;
}

willrt_sleep_op* willrt_task_sleep_async_start(int64_t ns) {
    willrt_sleep_op* op = (willrt_sleep_op*)willrt_alloc(sizeof(*op));

    if (op == NULL) {
        return NULL;
    }
    op->status = ns < 0 ? 1 : 0;
    op->deadline = ns < 0 ? willrt_now_ns() : willrt_now_ns() + ns;
    return op;
}

int32_t willrt_task_sleep_poll(willrt_sleep_op* op) {
    return op == NULL || willrt_now_ns() >= op->deadline;
}

int32_t willrt_task_sleep_finish(willrt_sleep_op* op) {
    int32_t status;

    if (op == NULL) {
        return 1;
    }
    status = op->status;
    willrt_free(op);
    return status;
}

int64_t willrt_task_sleep_deadline(willrt_sleep_op* op) {
    return op == NULL ? 0 : op->deadline;
}

void willrt_task_wait_until(int64_t deadline) {
    struct willrt_task* task = willrt_task_self;

    if (task != NULL) {
        task->wait_deadline = deadline;
    }
}

int64_t willrt_task_enter_deadline(int64_t duration_ns) {
    struct willrt_task* task = willrt_task_self;
    int64_t previous = task == NULL ? willrt_main_deadline : task->deadline;
    int64_t next = duration_ns <= 0 ? 0 : willrt_now_ns() + duration_ns;

    if (task == NULL) {
        willrt_main_deadline = next;
    } else {
        task->deadline = next;
    }
    return previous;
}

void willrt_task_exit_deadline(int64_t previous) {
    struct willrt_task* task = willrt_task_self;

    if (task == NULL) {
        willrt_main_deadline = previous;
    } else {
        task->deadline = previous;
    }
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
    if (willrt_lane_state == 2) {
        willrt_lane_shutdown();
    }
    if (willrt_jobs_shutdown != NULL) {
        willrt_jobs_shutdown();
    }
}
