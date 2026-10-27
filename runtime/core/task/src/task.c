/* Tasks, regions, and the task environment.
 *
 * A task is a step over a compiler-built frame, not a platform thread and not
 * a C stack. A fixed lane pool calls the step while the task is ready. A step
 * that returns WILLRT_SUSPEND has already stored its state in the frame; the
 * lane parks it, and the dependency it named wakes exactly that task: a
 * channel handoff through the wait node, a join through the target's waiter
 * chain, a park deadline through the timer wheel, a poller readiness scan
 * through the task's embedded request. Jobs use the same step contract
 * through the job pool.
 *
 * A panic inside a task writes its report, records the failure, and jumps back
 * to the lane loop instead of ending the lane thread. The jump skips defer and
 * static release, as the panic contract requires; the task frame is reclaimed
 * by the task boundary. A panic outside a running step keeps the process-exit
 * path.
 */

#include "../../../include/willrt.h"

#include <setjmp.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define WILLRT_REGION_INLINE 64

enum {
    WILLRT_TASK_READY = 0,
    WILLRT_TASK_RUNNING = 1,
    WILLRT_TASK_PARKING = 2,
    WILLRT_TASK_WAITING = 3,
    WILLRT_TASK_DONE = 4
};

/* The sleep handle is complete here because the task owns one by value. */
struct willrt_sleep_op {
    int64_t deadline;
    int32_t status;
    int32_t active;
};

struct willrt_region;

struct willrt_task {
    willrt_os_mutex lock;
    willrt_os_cond cond;
    willrt_step step;
    void* frame;
    size_t frame_size;
    void (*frame_drop)(void*);
    int32_t frame_owned;
    int32_t lane;
    int32_t prio;
    int32_t started;
    int32_t finished;
    int32_t failed;
    int32_t canceled;
    int32_t state;
    int32_t queued;
    int32_t releasing;
    int32_t is_job;
    int32_t counted;
    int32_t wake_pending;
    int32_t wait_requested;
    int32_t io_wait;
    int32_t join_linked;
    int32_t joined_region;
    int32_t waiters;
    void* cancel_flag;
    int64_t deadline;
    int64_t wait_deadline;
    unsigned char* result;
    size_t result_size;
    /* Most spawn results are one scalar or one small aggregate. Keeping them
       in the task removes one allocation and one free from every task. */
    unsigned char inline_result[32];
    char* message;
    size_t message_size;
    const char* site;
    const char* symbol;
    struct willrt_task* next_region;
    struct willrt_task* wait_next;
    struct willrt_task* job_next;
    struct willrt_task* all_next;
    struct willrt_region* regions;
    int32_t region_skipped;
    struct willrt_task* join_head;
    struct willrt_task* join_next;
    struct willrt_task* join_target;
    willrt_chan_wait chan_wait;
    /* The park timers are a ring of caller-owned wheel slots. A fire leaves a
       slot on the wheel due or retired lists until the advance pass that took
       it finishes, so a rapid re-park needs the next slot; two can be in
       flight at once and the third covers the boundary. */
    willrt_timer park_timers[3];
    willrt_timer* park_timer_armed;
    /* Async operation handles live in the task, so a park never allocates:
       the poller readiness request and the sleep deadline are one per task
       and the generated step finishes one before starting the next. */
    willrt_poll_request poll_node;
    willrt_sleep_op sleep_node;
    willrt_sync_wait sync_node;
    /* A file operation is heap-owned because the kernel keeps writing into its
       OVERLAPPED after a canceled task unwinds; the task only holds the
       handle it must abandon. */
    void* io_node;
};

struct willrt_region {
    struct willrt_task* head;
    struct willrt_task* members;
    struct willrt_region* previous;
    int32_t failed;
    int32_t exiting;
};

typedef struct lane_worker {
    willrt_os_thread thread;
    willrt_os_mutex lock;
    willrt_os_cond cond;
    struct willrt_task* head;
    struct willrt_task* tail;
    struct willrt_task* runnext;
    int32_t id;
    int32_t stopping;
    _Atomic(int32_t) alive;
    /* load counts live async tasks assigned to this lane. job_running is set
       while the lane executes a CPU job; choose prefers a lane that is not
       busy with a job. */
    _Atomic(int32_t) load;
    _Atomic(int32_t) job_running;
    /* sleeping is guarded by lock. A producer signals only while the lane is
       parked, so a running lane discovers the queued task at its next probe
       instead of paying a system wake for every enqueue. */
    int32_t sleeping;
} lane_worker;

static _Thread_local struct willrt_task* willrt_task_self;
static _Thread_local jmp_buf willrt_panic_jump;
static _Thread_local int32_t willrt_panic_jump_active;
static _Thread_local int32_t willrt_lane_self = -1;
/* The region stack starts inline per thread and grows, so nesting a spawn
   scope inside a spawn scope has no fixed ceiling. A scope that could not grow
   is remembered in willrt_region_skipped and its exit joins nothing, which
   keeps enters and exits balanced under memory exhaustion. */
static _Thread_local struct willrt_region willrt_regions_inline[WILLRT_REGION_INLINE];
static _Thread_local struct willrt_region* willrt_regions;
static _Thread_local size_t willrt_regions_cap;
static _Thread_local int willrt_region_depth;
static _Thread_local int willrt_region_skipped;
static _Thread_local int64_t willrt_main_deadline;

static atomic_int willrt_task_globals_state;
static atomic_int willrt_tasks_live;
static atomic_int willrt_threads_live;
static atomic_int willrt_activity_count;
static willrt_os_mutex willrt_tasks_lock;
static willrt_os_cond willrt_tasks_cond;

static struct willrt_task* willrt_all_tasks;
static lane_worker* willrt_lane_workers;
static int32_t willrt_lane_count;
static int32_t willrt_lane_started;
static int32_t willrt_lane_reserved;
static int32_t willrt_lane_state;
static atomic_int willrt_lane_ready;

/* A run of auto spawns on one thread stays on the lane chosen for the run.
   Every task enters its lane queue immediately, but the run pays one wake for
   the whole group. The lane count is small and pick_lanes runs once per group,
   not once per task. */
enum { WILLRT_AUTO_GROUP = 8, WILLRT_AUTO_LANES = 3 };
static _Thread_local int32_t willrt_auto_group_lanes[WILLRT_AUTO_LANES];
static _Thread_local int32_t willrt_auto_group_used;
static _Thread_local int32_t willrt_auto_group_next;
static _Thread_local int32_t willrt_auto_group_left;
static atomic_int willrt_auto_cursor;

extern void willrt_jobs_shutdown(void) __attribute__((weak));
extern int32_t willrt_jobs_pending(void) __attribute__((weak));
extern willrt_task* willrt_job_pop(int32_t lane) __attribute__((weak));
extern willrt_task* willrt_job_steal(int32_t lane) __attribute__((weak));
extern int32_t willrt_job_count(int32_t lane) __attribute__((weak));
extern int32_t willrt_timer_pending(void) __attribute__((weak));
extern void willrt_poll_request_abandon(willrt_poll_request* req)
    __attribute__((weak));
extern void willrt_sync_wait_abandon(willrt_sync_wait* wait)
    __attribute__((weak));
extern void willrt_io_abandon(void* op) __attribute__((weak));

static void willrt_lane_init(void);
static void willrt_task_check_dead(void);

/* The park timer runs on the timer thread and wakes exactly the task that
   parked with this deadline. */
static void willrt_task_timer_wake(void* arg) {
    willrt_task_wake_chan((willrt_task*)arg);
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

static lane_worker* willrt_lane_of(const struct willrt_task* task) {
    int32_t index = task->lane;

    if (willrt_lane_count <= 0 || willrt_lane_workers == NULL) {
        return NULL;
    }
    if (index < 0) {
        index = 0;
    }
    index %= willrt_lane_count;
    if (atomic_load(&willrt_lane_workers[index].alive)) {
        return &willrt_lane_workers[index];
    }
    /* A lane can still be stopped while the runtime shuts down. Hand its work
       to a live lane so a queued task is not stranded. */
    {
        int32_t i;
        for (i = 0; i < willrt_lane_count; i++) {
            int32_t candidate = (index + i) % willrt_lane_count;
            if (atomic_load(&willrt_lane_workers[candidate].alive)) {
                return &willrt_lane_workers[candidate];
            }
        }
    }
    return &willrt_lane_workers[index];
}

/* A wake goes to the target lane's runnext slot. If that slot is occupied, the
   old head moves to the local queue and the new task takes runnext. The global
   lane list is not touched. */
static void willrt_lane_enqueue_locked(lane_worker* worker,
                                       struct willrt_task* task) {
    struct willrt_task* previous;

    if (task->queued) {
        return;
    }
    task->queued = 1;
    task->state = WILLRT_TASK_READY;
    task->wait_next = NULL;
    if (!task->counted && !task->is_job) {
        task->counted = 1;
        atomic_fetch_add(&worker->load, 1);
    }
    if (worker->runnext == NULL) {
        worker->runnext = task;
        return;
    }
    previous = worker->runnext;
    worker->runnext = task;
    previous->wait_next = worker->head;
    worker->head = previous;
    if (worker->tail == NULL) {
        worker->tail = previous;
    }
}

// willrt_lane_signal_locked wakes one parked lane. The caller holds the lane
// lock; clearing sleeping here keeps a second enqueue from signalling a lane
// that the first signal already made runnable.
static void willrt_lane_signal_locked(lane_worker* worker) {
    if (worker->sleeping) {
        worker->sleeping = 0;
        willrt_os_cond_signal(&worker->cond);
    }
}

// willrt_auto_pick_lanes fills lanes with the least loaded live lanes for one
// auto group. The submitting lane is skipped when another lane is available,
// so a task that waits on this group cannot sit behind its own lane.
static int32_t willrt_auto_pick_lanes(int32_t* lanes, int32_t want) {
    int32_t start = atomic_fetch_add(&willrt_auto_cursor, 1);
    int32_t used = 0;
    int32_t here = willrt_lane_self;

    while (used < want) {
        int32_t best = -1;
        int32_t best_score = INT32_MAX;
        int32_t i;

        for (i = 0; i < willrt_lane_count; i++) {
            int32_t lane = (start + i) % willrt_lane_count;
            lane_worker* worker = &willrt_lane_workers[lane];
            int32_t score;
            int32_t chosen = 0;

            if (!atomic_load(&worker->alive)) {
                continue;
            }
            if (here >= 0 && lane == here && willrt_lane_count > 1) {
                continue;
            }
            for (int32_t j = 0; j < used; j++) {
                if (lanes[j] == lane) {
                    chosen = 1;
                }
            }
            if (chosen) {
                continue;
            }
            score = atomic_load(&worker->load);
            if (atomic_load(&worker->job_running) != 0) {
                score += 1024;
            }
            if (willrt_job_count != NULL) {
                score += willrt_job_count(lane) * 4;
            }
            if (score < best_score) {
                best_score = score;
                best = lane;
            }
        }
        if (best < 0) {
            break;
        }
        lanes[used++] = best;
    }
    return used;
}

static struct willrt_task* willrt_lane_pop_locked(lane_worker* worker) {
    struct willrt_task* task = worker->runnext;

    if (task != NULL) {
        worker->runnext = NULL;
    } else {
        task = worker->head;
        if (task == NULL) {
            return NULL;
        }
        worker->head = task->wait_next;
        if (worker->head == NULL) {
            worker->tail = NULL;
        }
    }
    task->wait_next = NULL;
    task->queued = 0;
    return task;
}

// willrt_task_wake moves one parked task to the ready queue of its own lane.
// A task that is still switching out is marked pending; the lane makes it
// ready after the switch has actually happened.
static void willrt_task_wake(struct willrt_task* task) {
    lane_worker* worker;

    if (task == NULL) {
        return;
    }
    worker = willrt_lane_of(task);
    if (worker == NULL) {
        return;
    }
    willrt_os_mutex_lock(&worker->lock);
    if (task->state == WILLRT_TASK_WAITING) {
        willrt_lane_enqueue_locked(worker, task);
        willrt_lane_signal_locked(worker);
    } else if (task->state == WILLRT_TASK_PARKING ||
               task->state == WILLRT_TASK_RUNNING) {
        task->wake_pending = 1;
    }
    willrt_os_mutex_unlock(&worker->lock);
}

/* willrt_task_snapshot parks the head of the all-task list. The list only
   grows while the runtime runs, so a walk from the snapshot stays on nodes
   that were visible when the walk began. */
static struct willrt_task* willrt_task_all_snapshot(void) {
    struct willrt_task* task;

    if (atomic_load(&willrt_task_globals_state) != 2 ||
        !atomic_load(&willrt_lane_ready)) {
        return NULL;
    }
    willrt_os_mutex_lock(&willrt_tasks_lock);
    task = willrt_all_tasks;
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    return task;
}

// willrt_sched_wake_all wakes every parked task. Cancellation uses this
// because a credential can be observed by a waiter parked on anything.
void willrt_sched_wake_all(void) {
    struct willrt_task* task = willrt_task_all_snapshot();

    while (task != NULL) {
        willrt_task_wake(task);
        task = task->all_next;
    }
}

// willrt_sched_wake_io wakes the parked tasks that marked themselves as
// waiting on an external operation. It is the bridge for a completer that
// holds no task handle; every dependency that knows its waiter wakes it
// directly instead of calling this.
void willrt_sched_wake_io(void) {
    struct willrt_task* task = willrt_task_all_snapshot();

    while (task != NULL) {
        if (task->io_wait) {
            willrt_task_wake(task);
        }
        task = task->all_next;
    }
}

/* willrt_task_disarm_timer drops the park timer of one task. An early wake
   leaves the timer armed; disarming keeps the wheel count truthful for
   deadlock detection. A heap fallback node is reclaimed by the wheel. */
/* willrt_task_disarm_timer drops the park timer of one task. An early wake
   leaves the timer armed; disarming keeps the wheel count truthful for
   deadlock detection. A fired slot still on the wheel's lists is marked
   cancelled and the advance pass that owns it returns the slot to idle. */
static void willrt_task_disarm_timer(struct willrt_task* task) {
    if (task->park_timer_armed != NULL) {
        willrt_timer_cancel(task->park_timer_armed);
        task->park_timer_armed = NULL;
    }
}

/* willrt_task_arm_park_timer arms one of the task's own wheel slots. The
   three-slot ring covers the window where an advance pass still owns a fired
   slot; if every slot is busy the runtime is broken, and the defect is
   reported instead of silently allocating. */
static int32_t willrt_task_arm_park_timer(struct willrt_task* task,
                                          int64_t wake) {
    for (int32_t i = 0; i < 3; i++) {
        if (willrt_timer_arm_try(&task->park_timers[i], wake,
                                 willrt_task_timer_wake, task) == 0) {
            task->park_timer_armed = &task->park_timers[i];
            return 0;
        }
    }
    task->park_timer_armed = NULL;
    return -1;
}

static void willrt_task_park(struct willrt_task* task) {
    int64_t wake;
    lane_worker* worker;

    wake = task->wait_deadline;
    worker = willrt_lane_of(task);

    /* The wake path takes the same lock, so a wake that arrives while the
       task switches to the waiting state is either seen here or finds the
       task waiting and queues it. Checking the flag outside the lock would
       lose a wake that lands between the check and the switch. */
    if (worker != NULL) {
        willrt_os_mutex_lock(&worker->lock);
        if (task->wake_pending) {
            task->wake_pending = 0;
            willrt_lane_enqueue_locked(worker, task);
            willrt_lane_signal_locked(worker);
            willrt_os_mutex_unlock(&worker->lock);
            return;
        }
        if (task->deadline > 0 && (wake == 0 || task->deadline < wake)) {
            wake = task->deadline;
        }
        if (wake > 0 && willrt_now_ns() >= wake) {
            willrt_lane_enqueue_locked(worker, task);
            willrt_lane_signal_locked(worker);
            willrt_os_mutex_unlock(&worker->lock);
            return;
        }
        /* Arm before the waiting state becomes visible. A deadline that
           passes right now fires into a still-running task and turns into the
           wake-pending flag on the next park, while deadlock detection must
           never observe a waiting task with no armed timer. */
        if (wake > 0 && willrt_task_arm_park_timer(task, wake) != 0) {
            willrt_os_mutex_unlock(&worker->lock);
            willrt_panic("park timer slots exhausted", "task.c",
                         "willrt.task.park");
            return;
        }
        task->wait_deadline = 0;
        task->state = WILLRT_TASK_WAITING;
        willrt_os_mutex_unlock(&worker->lock);
    } else {
        if (wake > 0 && willrt_task_arm_park_timer(task, wake) != 0) {
            willrt_panic("park timer slots exhausted", "task.c",
                         "willrt.task.park");
            return;
        }
        task->wait_deadline = 0;
        task->state = WILLRT_TASK_WAITING;
    }
}

static void willrt_task_finish(struct willrt_task* task) {
    struct willrt_task* waiter;
    struct willrt_task* walk;

    /* A task that leaves its frame while parked would otherwise leave its
       poller request pending forever, so the poller would carry the
       descriptors of a finished task. The same applies to a sync waiter: a
       granted acquisition must never be handed to a dead task. */
    if (willrt_poll_request_abandon != NULL) {
        willrt_poll_request_abandon(&task->poll_node);
    }
    if (willrt_sync_wait_abandon != NULL) {
        willrt_sync_wait_abandon(&task->sync_node);
    }
    if (willrt_io_abandon != NULL) {
        willrt_io_abandon(task->io_node);
        task->io_node = NULL;
    }
    if (task->counted) {
        lane_worker* worker = willrt_lane_of(task);

        if (worker != NULL) {
            atomic_fetch_sub(&worker->load, 1);
        }
        task->counted = 0;
    }
    willrt_os_mutex_lock(&task->lock);
    task->finished = 1;
    walk = task->join_head;
    while (walk != NULL) {
        /* Clear the flag under the lock so a waiter re-entering try_join on
           this target cannot double-unlink a node this finish detached. */
        walk->join_linked = 0;
        walk = walk->join_next;
    }
    waiter = task->join_head;
    task->join_head = NULL;
    if (task->waiters != 0) {
        willrt_os_cond_broadcast(&task->cond);
    }
    willrt_os_mutex_unlock(&task->lock);

    while (waiter != NULL) {
        struct willrt_task* next = waiter->join_next;
        waiter->join_next = NULL;
        willrt_task_wake(waiter);
        waiter = next;
    }

    if (atomic_fetch_sub(&willrt_tasks_live, 1) == 1) {
        willrt_os_mutex_lock(&willrt_tasks_lock);
        willrt_os_cond_broadcast(&willrt_tasks_cond);
        willrt_os_mutex_unlock(&willrt_tasks_lock);
    }
}

static void willrt_task_run_step(struct willrt_task* task) {
    int32_t status;

    task->state = WILLRT_TASK_RUNNING;
    task->wait_requested = 0;
    task->io_wait = 0;
    willrt_task_disarm_timer(task);
    willrt_task_self = task;
    willrt_mem_set_lane(task->lane);
    /* A panic is a defect, not an exception: it discards the task and jumps
       back here. The lane stack is reused; no task owns a C stack. */
    willrt_panic_jump_active = 1;
    if (setjmp(willrt_panic_jump) != 0) {
        willrt_panic_jump_active = 0;
        willrt_task_self = NULL;
        willrt_task_finish(task);
        task->state = WILLRT_TASK_DONE;
        return;
    }
    status = task->step(task->frame);
    willrt_panic_jump_active = 0;
    willrt_task_self = NULL;
    if (status == WILLRT_SUSPEND) {
        if (task->is_job) {
            willrt_panic("job cannot suspend", "task.c", "willrt.task.job");
            return;
        }
        if (task->wait_requested) {
            willrt_task_park(task);
        } else {
            lane_worker* worker = willrt_lane_of(task);
            if (worker != NULL) {
                willrt_os_mutex_lock(&worker->lock);
                willrt_lane_enqueue_locked(worker, task);
                willrt_lane_signal_locked(worker);
                willrt_os_mutex_unlock(&worker->lock);
            }
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

void willrt_task_mark_io(void) {
    if (willrt_task_self != NULL) {
        willrt_task_self->io_wait = 1;
    }
}

static void willrt_lane_worker(void* raw) {
    lane_worker* worker = (lane_worker*)raw;

    willrt_mem_set_lane(worker->id);
    willrt_lane_self = worker->id;
    for (;;) {
        struct willrt_task* task;
        int32_t stopping;
        int32_t ran_job = 0;

        willrt_os_mutex_lock(&worker->lock);
        for (;;) {
            task = willrt_lane_pop_locked(worker);
            if (task == NULL && !willrt_lane_is_reserved(worker->id) &&
                willrt_job_pop != NULL) {
                /* The lane lock stays held across the local job probe. A job
                   enqueue takes the job lock and then this lane lock to signal,
                   so a job cannot land after the probe and miss the wake. */
                task = willrt_job_pop(worker->id);
            }
            if (task != NULL || worker->stopping) {
                break;
            }
            worker->sleeping = 1;
            willrt_os_cond_wait(&worker->cond, &worker->lock);
            worker->sleeping = 0;
        }
        stopping = worker->stopping;
        if (task != NULL && task->is_job) {
            task->started = 1;
            task->lane = worker->id;
            ran_job = 1;
            atomic_store(&worker->job_running, 1);
        }
        willrt_os_mutex_unlock(&worker->lock);
        if (task == NULL) {
            if (stopping) {
                break;
            }
            if (!willrt_lane_is_reserved(worker->id) &&
                willrt_job_steal != NULL) {
                task = willrt_job_steal(worker->id);
            }
            if (task == NULL) {
                continue;
            }
            if (task->is_job) {
                task->started = 1;
                task->lane = worker->id;
                ran_job = 1;
                atomic_store(&worker->job_running, 1);
            }
        }
        willrt_task_run_step(task);
        if (ran_job) {
            atomic_store(&worker->job_running, 0);
        }
    }
    willrt_mem_thread_flush();
}

int32_t willrt_lane_is_reserved(int32_t lane) {
    return lane >= 0 && lane < willrt_lane_reserved;
}

int32_t willrt_lane_ensure(void) {
    willrt_lane_init();
    return willrt_lane_count;
}

void willrt_lane_wake_jobs(void) {
    int32_t i;

    if (willrt_lane_workers == NULL || atomic_load(&willrt_lane_ready) == 0) {
        return;
    }
    for (i = willrt_lane_reserved; i < willrt_lane_count; i++) {
        lane_worker* worker = &willrt_lane_workers[i];

        if (!atomic_load(&worker->alive)) {
            continue;
        }
        willrt_os_mutex_lock(&worker->lock);
        willrt_lane_signal_locked(worker);
        willrt_os_mutex_unlock(&worker->lock);
    }
}

static int32_t willrt_lane_reserved_policy(int32_t count) {
    int32_t reserved;
    const char* env;
    char* end;
    long value;

    if (count <= 1) {
        return 0;
    }
    reserved = count == 2 ? 0 : count <= 4 ? 1 : count <= 16 ? 2 : 3;
    env = getenv("WILL_RESERVED_LANES");
    if (env != NULL && *env != '\0') {
        value = strtol(env, &end, 10);
        if (end != env && *end == '\0' && value >= 0) {
            reserved = (int32_t)value;
        }
    }
    if (reserved < 0) {
        reserved = 0;
    }
    if (reserved >= count) {
        reserved = count - 1;
    }
    return reserved;
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
    count = willrt_os_cpu_count();
    if (count < 1) {
        count = 1;
    }
    {
        const char* env = getenv("WILL_LANES");
        char* end;
        long value;

        if (env != NULL && *env != '\0') {
            value = strtol(env, &end, 10);
            if (end != env && *end == '\0' && value > 0 &&
                value < count) {
                count = (int32_t)value;
            }
        }
    }
    willrt_lane_workers = (lane_worker*)willrt_alloc(
        sizeof(*willrt_lane_workers) * (size_t)count);
    if (willrt_lane_workers != NULL) {
        atomic_store(&willrt_threads_live, 1);
        willrt_mem_set_shared();
        willrt_rc_set_threaded();
        for (i = 0; i < count; i++) {
            memset(&willrt_lane_workers[i], 0, sizeof(willrt_lane_workers[i]));
            willrt_lane_workers[i].id = i;
            atomic_store(&willrt_lane_workers[i].alive, 1);
            willrt_os_mutex_init(&willrt_lane_workers[i].lock);
            willrt_os_cond_init(&willrt_lane_workers[i].cond);
            if (willrt_os_thread_start(&willrt_lane_workers[i].thread,
                                       willrt_lane_worker,
                                       &willrt_lane_workers[i]) != 0) {
                break;
            }
            willrt_lane_started++;
        }
    }
    willrt_lane_count = willrt_lane_started;
    willrt_lane_reserved = willrt_lane_reserved_policy(willrt_lane_count);
    willrt_lane_state = 2;
    atomic_store(&willrt_lane_ready, 1);
}

static void willrt_lane_shutdown(void) {
    int32_t i;

    if (willrt_lane_state != 2) {
        return;
    }
    for (i = 0; i < willrt_lane_started; i++) {
        willrt_os_mutex_lock(&willrt_lane_workers[i].lock);
        willrt_lane_workers[i].stopping = 1;
        willrt_os_cond_signal(&willrt_lane_workers[i].cond);
        willrt_os_mutex_unlock(&willrt_lane_workers[i].lock);
    }
    for (i = 0; i < willrt_lane_started; i++) {
        willrt_os_thread_join(&willrt_lane_workers[i].thread);
    }
    willrt_lane_state = 3;
}

willrt_task* willrt_task_new(void* step, void* frame, size_t size, int32_t lane,
                             int32_t prio) {
    return willrt_task_new_drop(step, frame, size, lane, prio, NULL);
}

willrt_task* willrt_task_new_drop(void* step, void* frame, size_t size,
                                  int32_t lane, int32_t prio,
                                  void (*frame_drop)(void*)) {
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
    task->frame_drop = frame_drop;
    task->lane = lane < 0 ? 0 : lane;
    task->prio = prio;
    willrt_os_mutex_init(&task->lock);
    willrt_os_cond_init(&task->cond);

    willrt_os_mutex_lock(&willrt_tasks_lock);
    atomic_fetch_add(&willrt_tasks_live, 1);
    task->all_next = willrt_all_tasks;
    willrt_all_tasks = task;
    willrt_os_mutex_unlock(&willrt_tasks_lock);

    if (willrt_task_self != NULL) {
        region = willrt_task_self->regions;
    } else {
        region = willrt_region_depth > 0
                     ? &willrt_regions[willrt_region_depth - 1]
                     : NULL;
    }
    if (region != NULL) {
        task->next_region = region->head;
        region->head = task;
    }
    return task;
}

void willrt_task_run(willrt_task* task) {
    if (task == NULL || task->started) {
        return;
    }
    task->started = 1;
    willrt_lane_init();
    if (willrt_lane_started == 0) {
        atomic_store(&willrt_threads_live, 1);
        willrt_rc_set_threaded();
        willrt_mem_set_shared();
        willrt_task_run_step(task);
        return;
    }
    {
        lane_worker* worker = willrt_lane_of(task);
        if (worker == NULL) {
            return;
        }
        willrt_os_mutex_lock(&worker->lock);
        willrt_lane_enqueue_locked(worker, task);
        willrt_lane_signal_locked(worker);
        willrt_os_mutex_unlock(&worker->lock);
    }
}

void willrt_task_run_auto(willrt_task* task) {
    if (task == NULL || task->started) {
        return;
    }
    task->started = 1;
    willrt_lane_init();
    if (willrt_lane_started == 0) {
        atomic_store(&willrt_threads_live, 1);
        willrt_rc_set_threaded();
        willrt_mem_set_shared();
        willrt_task_run_step(task);
        return;
    }
    {
        int32_t lane;
        lane_worker* worker;

        if (willrt_auto_group_left <= 0) {
            willrt_auto_group_used =
                willrt_auto_pick_lanes(willrt_auto_group_lanes,
                                       WILLRT_AUTO_LANES);
            willrt_auto_group_next = 0;
            willrt_auto_group_left = WILLRT_AUTO_GROUP;
        }
        if (willrt_auto_group_used <= 0) {
            lane = 0;
        } else {
            lane = willrt_auto_group_lanes[willrt_auto_group_next
                                           % willrt_auto_group_used];
            willrt_auto_group_next++;
        }
        willrt_auto_group_left--;
        task->lane = lane;
        worker = willrt_lane_of(task);
        if (worker == NULL) {
            return;
        }
        willrt_os_mutex_lock(&worker->lock);
        willrt_lane_enqueue_locked(worker, task);
        willrt_lane_signal_locked(worker);
        willrt_os_mutex_unlock(&worker->lock);
    }
}

void willrt_task_mark_job(willrt_task* task) {
    if (task != NULL) {
        task->is_job = 1;
    }
}

willrt_task* willrt_task_job_next(willrt_task* task) {
    return task == NULL ? NULL : task->job_next;
}

void willrt_task_set_job_next(willrt_task* task, willrt_task* next) {
    if (task != NULL) {
        task->job_next = next;
    }
}

/* willrt_task_run_job advances one task on the caller thread. Jobs contain no
   suspension point, so a returned suspend is a failed job rather than a park. */
void willrt_task_run_job(willrt_task* task) {
    struct willrt_task* previous = willrt_task_self;

    if (task == NULL) {
        return;
    }
    atomic_store(&willrt_threads_live, 1);
    task->started = 1;
    task->lane = willrt_lane_self >= 0 ? willrt_lane_self : 0;
    willrt_mem_set_lane(task->lane);
    willrt_task_run_step(task);
    willrt_task_self = previous;
    willrt_mem_set_lane(willrt_lane_self >= 0 ? willrt_lane_self : 0);
}

willrt_task* willrt_task_current(void) {
    return willrt_task_self;
}

void willrt_task_wake_chan(willrt_task* task) {
    willrt_task_wake(task);
}

willrt_poll_request* willrt_task_poll_node(void) {
    return willrt_task_self == NULL ? NULL : &willrt_task_self->poll_node;
}

willrt_sync_wait* willrt_task_sync_node(void) {
    return willrt_task_self == NULL ? NULL : &willrt_task_self->sync_node;
}

void willrt_task_set_io(void* op) {
    if (willrt_task_self != NULL) {
        willrt_task_self->io_node = op;
    }
}

void willrt_task_clear_io(void* op) {
    if (willrt_task_self != NULL && willrt_task_self->io_node == op) {
        willrt_task_self->io_node = NULL;
    }
}

/* willrt_task_wake_batch is the poller's delivery path: one pass of ready
   tasks takes each lane lock once, whatever the tasks share. The table holds
   the lanes locked so far; a pass with more lanes than the table flushes and
   keeps going. */
void willrt_task_wake_batch(void* const* tasks, int64_t count) {
    lane_worker* locked[64];
    int32_t locked_count = 0;
    int64_t i;
    int32_t j;

    for (i = 0; i < count; i++) {
        willrt_task* task = (willrt_task*)tasks[i];
        lane_worker* worker;

        if (task == NULL) {
            continue;
        }
        worker = willrt_lane_of(task);
        if (worker == NULL) {
            continue;
        }
        for (j = 0; j < locked_count && locked[j] != worker; j++) {
        }
        if (j == locked_count) {
            if (locked_count == (int32_t)(sizeof(locked) / sizeof(locked[0]))) {
                while (locked_count > 0) {
                    locked_count--;
                    willrt_lane_signal_locked(locked[locked_count]);
                    willrt_os_mutex_unlock(&locked[locked_count]->lock);
                }
            }
            willrt_os_mutex_lock(&worker->lock);
            locked[locked_count++] = worker;
        }
        if (task->state == WILLRT_TASK_WAITING) {
            willrt_lane_enqueue_locked(worker, task);
        } else if (task->state == WILLRT_TASK_PARKING
                   || task->state == WILLRT_TASK_RUNNING) {
            task->wake_pending = 1;
        }
    }
    while (locked_count > 0) {
        locked_count--;
        willrt_lane_signal_locked(locked[locked_count]);
        willrt_os_mutex_unlock(&locked[locked_count]->lock);
    }
}

willrt_chan_wait* willrt_task_chan_wait_node(void) {
    return willrt_task_self == NULL ? NULL : &willrt_task_self->chan_wait;
}

/* The TaskError accessors read the copy the joining task keeps in its own
   frame, so no accessor depends on the joined task outliving the join. The
   message bytes stay owned by the joined task until the region barrier. */
void willrt_task_error_message(void* raw, const char** data, size_t* size) {
    const willrt_task_error* copy = (const willrt_task_error*)raw;

    if (copy == NULL) {
        *data = "";
        *size = 0;
        return;
    }
    *data = copy->error.message != NULL ? copy->error.message : "";
    *size = copy->error.size;
}

int32_t willrt_task_error_status(void* raw) {
    const willrt_task_error* copy = (const willrt_task_error*)raw;

    if (copy == NULL) {
        return 0;
    }
    return copy->status == WILLRT_CANCELED ? 8 : 0;
}

int32_t willrt_task_store_result(const void* value, size_t size) {
    struct willrt_task* task = willrt_task_self;
    unsigned char* copy;

    if (task == NULL || value == NULL || size == 0) {
        return WILLRT_OK;
    }
    if (size <= sizeof(task->inline_result)) {
        willrt_os_mutex_lock(&task->lock);
        if (task->result == NULL) {
            memcpy(task->inline_result, value, size);
            task->result = task->inline_result;
            task->result_size = size;
        }
        willrt_os_mutex_unlock(&task->lock);
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
        copy = NULL;
    }
    willrt_os_mutex_unlock(&task->lock);
    willrt_free(copy);
    return WILLRT_OK;
}

/* willrt_task_join_unlink removes the current task from target's waiter
   chain. The caller holds target's lock. */
static void willrt_task_join_unlink(struct willrt_task* target,
                                    struct willrt_task* waiter) {
    struct willrt_task* cursor = target->join_head;
    struct willrt_task* previous = NULL;

    while (cursor != NULL && cursor != waiter) {
        previous = cursor;
        cursor = cursor->join_next;
    }
    if (cursor == NULL) {
        return;
    }
    if (previous == NULL) {
        target->join_head = cursor->join_next;
    } else {
        previous->join_next = cursor->join_next;
    }
    waiter->join_next = NULL;
    waiter->join_linked = 0;
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
        if (current->join_target == task && current->join_linked) {
            /* A previous run of this step already registered here. A wake
               that was not this task's finish leaves the registration in
               place; the finished check above ran under the same lock, so no
               completion can slip between the check and this branch. */
            willrt_os_mutex_unlock(&task->lock);
            if (willrt_task_canceled()) {
                willrt_os_mutex_lock(&task->lock);
                willrt_task_join_unlink(task, current);
                willrt_os_mutex_unlock(&task->lock);
                return WILLRT_CANCELED;
            }
            if (current->deadline != 0 && willrt_now_ns() >= current->deadline) {
                willrt_os_mutex_lock(&task->lock);
                willrt_task_join_unlink(task, current);
                willrt_os_mutex_unlock(&task->lock);
                return WILLRT_TIMEDOUT;
            }
            willrt_task_mark_wait();
            return WILLRT_SUSPEND;
        }
        if (current->join_linked && current->join_target != task) {
            /* The waiter moved to a different target between runs. The stale
               registration hangs on the old target's chain, under its lock. */
            struct willrt_task* stale = current->join_target;

            willrt_os_mutex_unlock(&task->lock);
            willrt_os_mutex_lock(&stale->lock);
            if (current->join_linked) {
                willrt_task_join_unlink(stale, current);
            }
            willrt_os_mutex_unlock(&stale->lock);
            current->join_target = NULL;
            willrt_os_mutex_lock(&task->lock);
        }
        if (willrt_task_canceled()) {
            willrt_os_mutex_unlock(&task->lock);
            return WILLRT_CANCELED;
        }
        if (current->deadline != 0 && willrt_now_ns() >= current->deadline) {
            willrt_os_mutex_unlock(&task->lock);
            return WILLRT_TIMEDOUT;
        }
        current->join_target = task;
        current->join_linked = 1;
        current->join_next = task->join_head;
        task->join_head = current;
        willrt_os_mutex_unlock(&task->lock);
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
        int32_t finished;
        willrt_os_mutex_lock(&task->lock);
        if (!task->finished) {
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
            task->waiters += 1;
            willrt_os_cond_timedwait(&task->cond, &task->lock, wait);
            task->waiters -= 1;
        }
        finished = task->finished;
        if (finished) {
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
        willrt_os_mutex_unlock(&task->lock);
        willrt_task_check_dead();
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
    if (willrt_panic_jump_active) {
        longjmp(willrt_panic_jump, 1);
    }
    return 0;
}

/* willrt_regions_grow doubles the region stack. The regions themselves hold
   only a mutex and a task head, so copying them to a larger block is enough;
   the old block is released unless it is the inline one. A failed growth leaves
   the stack as it was so the caller can record a skipped scope. */
static int willrt_regions_grow(void) {
    size_t next = willrt_regions_cap * 2;
    struct willrt_region* grown;

    if (next < willrt_regions_cap
        || next > SIZE_MAX / sizeof(*grown)) {
        return 0;
    }
    grown = (struct willrt_region*)willrt_alloc(next * sizeof(*grown));
    if (grown == NULL) {
        return 0;
    }
    memcpy(grown, willrt_regions, willrt_regions_cap * sizeof(*grown));
    if (willrt_regions != willrt_regions_inline) {
        willrt_free(willrt_regions);
    }
    willrt_regions = grown;
    willrt_regions_cap = next;
    return 1;
}

void willrt_region_enter(void) {
    struct willrt_region* region;

    willrt_task_globals();
    if (willrt_task_self != NULL) {
        region = (struct willrt_region*)willrt_alloc(sizeof(*region));
        if (region == NULL) {
            willrt_task_self->region_skipped += 1;
            return;
        }
        memset(region, 0, sizeof(*region));
        region->previous = willrt_task_self->regions;
        willrt_task_self->regions = region;
        return;
    }
    if (willrt_regions == NULL) {
        /* The inline array is thread-local, so its address is not a constant
           initializer; it is installed on the first scope of this thread. */
        willrt_regions = willrt_regions_inline;
        willrt_regions_cap = WILLRT_REGION_INLINE;
    }
    if ((size_t)willrt_region_depth == willrt_regions_cap
        && !willrt_regions_grow()) {
        willrt_region_skipped++;
        return;
    }
    region = &willrt_regions[willrt_region_depth];
    memset(region, 0, sizeof(*region));
    willrt_region_depth++;
}

void willrt_task_own_frame(willrt_task* task) {
    if (task != NULL) {
        task->frame_owned = 1;
    }
}

/* willrt_task_free returns one finished region member to the allocator. The
   region barrier joins every member first, so no joiner can still read this
   task's frame, result, or failure text, and no will-level handle can have
   left the region. */
static void willrt_task_free(struct willrt_task* task) {
    willrt_free(task->message);
    if (task->result != task->inline_result) {
        willrt_free(task->result);
    }
    if (task->frame_owned) {
        if (task->frame_drop != NULL) {
            task->frame_drop(task->frame);
        }
        willrt_free(task->frame);
    }
    willrt_os_mutex_destroy(&task->lock);
    willrt_os_cond_destroy(&task->cond);
    willrt_free(task);
}

/* willrt_task_release_members removes a whole region from the all-task list
   under one lock, then frees the members outside it. A per-task unlink would
   take the global lock once per spawned task. */
static void willrt_task_release_members(struct willrt_task* members) {
    struct willrt_task* task = members;
    struct willrt_task** link;

    if (members == NULL) {
        return;
    }
    while (task != NULL) {
        task->releasing = 1;
        task = task->next_region;
    }
    willrt_os_mutex_lock(&willrt_tasks_lock);
    link = &willrt_all_tasks;
    while (*link != NULL) {
        task = *link;
        if (task->releasing) {
            *link = task->all_next;
            task->releasing = 0;
        } else {
            link = &task->all_next;
        }
    }
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    task = members;
    while (task != NULL) {
        struct willrt_task* next = task->next_region;

        willrt_task_free(task);
        task = next;
    }
}

/* willrt_region_exit_try is the state-machine form of the region barrier. It
   keeps the region on the stack across a suspension, so a caller that parks
   does not hold a lane while its children run. Each member is joined once; a
   member that is still running returns WILLRT_SUSPEND and the caller resumes
   at the same call after the child wakes it. */
int32_t willrt_region_exit_try(void) {
    struct willrt_task* current = willrt_task_self;
    struct willrt_region* region;
    struct willrt_task* member;
    int32_t failed;

    if (current != NULL) {
        if (current->region_skipped > 0) {
            current->region_skipped--;
            return 0;
        }
        region = current->regions;
        if (region == NULL) {
            return 0;
        }
    } else {
        if (willrt_region_skipped > 0) {
            willrt_region_skipped--;
            return 0;
        }
        if (willrt_region_depth <= 0) {
            return 0;
        }
        region = &willrt_regions[willrt_region_depth - 1];
    }
    if (!region->exiting) {
        region->exiting = 1;
        region->members = region->head;
        region->head = NULL;
        region->failed = 0;
    }
    member = region->members;
    while (member != NULL) {
        struct willrt_task* next = member->next_region;

        if (!member->joined_region) {
            int32_t status;

            if (current != NULL) {
                status = willrt_task_try_join(member, NULL, NULL);
            } else {
                status = willrt_join(member, NULL, NULL);
            }
            if (status == WILLRT_SUSPEND) {
                return WILLRT_SUSPEND;
            }
            if (status == WILLRT_FAILED) {
                region->failed += 1;
            }
            member->joined_region = 1;
        }
        member = next;
    }
    failed = region->failed;
    if (failed != 0) {
        char line[64];
        int size = snprintf(line, sizeof(line), "region: %d tasks failed\n",
                            failed);

        if (size > 0) {
            willrt_report_write_n(line,
                                  (size_t)size < sizeof(line) ? (size_t)size
                                                              : sizeof(line));
        }
    }
    willrt_task_release_members(region->members);
    region->members = NULL;
    region->failed = 0;
    region->exiting = 0;
    if (current != NULL) {
        current->regions = region->previous;
        willrt_free(region);
    } else {
        willrt_region_depth--;
    }
    return failed;
}

int32_t willrt_region_exit(void) {
    struct willrt_task* current = willrt_task_self;
    struct willrt_region* region;
    struct willrt_task* members;
    struct willrt_task* task;
    int32_t failed = 0;
    char line[64];
    int size;

    if (current != NULL) {
        if (current->region_skipped > 0) {
            current->region_skipped--;
            return 0;
        }
        region = current->regions;
        if (region == NULL) {
            return 0;
        }
    } else {
        if (willrt_region_skipped > 0) {
            willrt_region_skipped--;
            return 0;
        }
        if (willrt_region_depth <= 0) {
            return 0;
        }
        willrt_region_depth--;
        region = &willrt_regions[willrt_region_depth];
    }
    members = region->head;
    region->head = NULL;
    task = members;
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
    /* The join pass above is the barrier: every member has finished, so the
       frames and results are dead and go back to the allocator, whose
       per-lane size classes make the next region's tasks reuse them. */
    willrt_task_release_members(members);
    if (current != NULL) {
        current->regions = region->previous;
        willrt_free(region);
    }
    return failed;
}

int32_t willrt_lane_here(void) {
    struct willrt_task* task = willrt_task_self;

    return task == NULL ? 0 : task->lane;
}

int32_t willrt_lane_choose(void) {
    static atomic_int cursor;
    int32_t start;
    int32_t best = -1;
    int32_t best_score = INT32_MAX;
    int32_t i;

    willrt_lane_init();
    if (willrt_lane_count <= 0 || willrt_lane_workers == NULL) {
        return 0;
    }
    start = atomic_fetch_add(&cursor, 1);
    for (i = 0; i < willrt_lane_count; i++) {
        int32_t lane = (start + i) % willrt_lane_count;
        lane_worker* worker = &willrt_lane_workers[lane];
        int32_t score;

        if (!atomic_load(&worker->alive)) {
            continue;
        }
        score = atomic_load(&worker->load);
        if (atomic_load(&worker->job_running) != 0) {
            score += 1024;
        }
        if (willrt_job_count != NULL) {
            score += willrt_job_count(lane) * 4;
        }
        if (score < best_score) {
            best_score = score;
            best = lane;
        }
    }
    return best < 0 ? 0 : best;
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

void willrt_task_cancel(willrt_task* task) {
    if (task == NULL) {
        return;
    }
    willrt_os_mutex_lock(&task->lock);
    if (!task->finished) {
        task->canceled = 1;
    }
    willrt_os_mutex_unlock(&task->lock);
    willrt_task_wake_chan(task);
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

int32_t willrt_task_sleep_async(int64_t ns) {
    if (ns < 0) {
        return 1;
    }
    willrt_os_sleep_ns(ns);
    return 0;
}

/* The sleep handle is the task's own node, so a park never allocates. A
   caller outside a task uses the detached node; only the main thread can be
   there, and generated async code never runs without a task. */
static willrt_sleep_op willrt_sleep_detached;

willrt_sleep_op* willrt_task_sleep_async_start(int64_t ns) {
    willrt_task* task = willrt_task_current();
    willrt_sleep_op* op = task == NULL ? &willrt_sleep_detached
                                       : &task->sleep_node;

    if (op->active) {
        willrt_panic("sleep operation already active", "task.c",
                     "will.task.sleep");
        return NULL;
    }
    op->active = 1;
    op->status = ns < 0 ? 1 : 0;
    op->deadline = ns < 0 ? willrt_now_ns() : willrt_now_ns() + ns;
    return op;
}

int32_t willrt_task_sleep_poll(willrt_sleep_op* op) {
    willrt_task* task;

    if (op == NULL || willrt_now_ns() >= op->deadline) {
        return 1;
    }
    /* The scoped deadline ends the wait before the sleep deadline does;
       otherwise the park would requeue against a past deadline forever. */
    task = willrt_task_current();
    if (task != NULL && task->deadline != 0
        && willrt_now_ns() >= task->deadline) {
        op->status = WILLRT_TIMEDOUT;
        return 1;
    }
    return 0;
}

int32_t willrt_task_sleep_finish(willrt_sleep_op* op) {
    int32_t status;

    if (op == NULL) {
        return 1;
    }
    status = op->status;
    op->active = 0;
    op->deadline = 0;
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

/* willrt_task_check_dead reports a deadlock when every started task is parked,
   no job is queued, no timer is armed, and no IO wait is pending. Callers
   invoke it after a timed wait, never while holding the task list lock. */
static void willrt_task_check_dead(void) {
    struct willrt_task* task;
    int32_t live = 0;
    int32_t active = 0;
    int32_t io = 0;
    int32_t jobs = 0;
    int32_t timers;
    int64_t now;

    if (atomic_load(&willrt_task_globals_state) != 2) {
        return;
    }
    now = willrt_now_ns();
    timers = willrt_main_deadline > now ? 1 : 0;
    willrt_os_mutex_lock(&willrt_tasks_lock);
    task = willrt_all_tasks;
    while (task != NULL) {
        if (task->started && !task->finished) {
            live++;
            if (task->state == WILLRT_TASK_READY ||
                task->state == WILLRT_TASK_RUNNING ||
                task->state == WILLRT_TASK_PARKING) {
                active++;
            }
            if (task->io_wait) {
                io++;
            }
        }
        task = task->all_next;
    }
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    if (live == 0 || active != 0) {
        return;
    }
    /* A parked task with a wake deadline keeps one timer armed, so the
       wheel's armed count is the truthful timer condition; a task parked on
       an external operation carries the IO marker. */
    if (willrt_timer_pending != NULL) {
        timers += willrt_timer_pending();
    }
    if (willrt_jobs_pending != NULL) {
        jobs = willrt_jobs_pending();
    }
    if (jobs != 0 || timers != 0 || io != 0) {
        return;
    }
    /* One snapshot can skew: a timer fire, a channel handoff, or a pool job
       completion may sit between the counters this scan read. Deadlock is
       declared only when a second scan, taken after a pause long enough for
       any pending wake to land, sees the same empty world. */
    willrt_os_sleep_ns(INT64_C(2000000));
    live = 0;
    active = 0;
    io = 0;
    jobs = 0;
    timers = willrt_main_deadline > willrt_now_ns() ? 1 : 0;
    willrt_os_mutex_lock(&willrt_tasks_lock);
    task = willrt_all_tasks;
    while (task != NULL) {
        if (task->started && !task->finished) {
            live++;
            if (task->state == WILLRT_TASK_READY ||
                task->state == WILLRT_TASK_RUNNING ||
                task->state == WILLRT_TASK_PARKING) {
                active++;
            }
            if (task->io_wait) {
                io++;
            }
        }
        task = task->all_next;
    }
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    if (live == 0 || active != 0) {
        return;
    }
    if (willrt_timer_pending != NULL) {
        timers += willrt_timer_pending();
    }
    if (willrt_jobs_pending != NULL) {
        jobs = willrt_jobs_pending();
    }
    if (jobs != 0 || timers != 0 || io != 0) {
        return;
    }
    {
        char line[160];
        int size = snprintf(line, sizeof(line),
            "deadlock: no runnable task\n  tasks: %d\n  jobs: %d\n  timers: %d\n  io: %d\n",
            (int)live, (int)jobs, (int)timers, (int)io);
        if (size > 0) {
            size_t length = (size_t)size < sizeof(line) ? (size_t)size : sizeof(line);
            willrt_report_write_n(line, length);
        }
    }
    _Exit(1);
}

void willrt_tasks_wait_all(void) {
    willrt_task_globals();
    willrt_os_mutex_lock(&willrt_tasks_lock);
    while (atomic_load(&willrt_tasks_live) != 0) {
        willrt_os_cond_timedwait(&willrt_tasks_cond, &willrt_tasks_lock,
                                 INT64_C(10) * INT64_C(1000000));
        willrt_os_mutex_unlock(&willrt_tasks_lock);
        willrt_task_check_dead();
        willrt_os_mutex_lock(&willrt_tasks_lock);
    }
    willrt_os_mutex_unlock(&willrt_tasks_lock);
    if (willrt_lane_state == 2) {
        willrt_lane_shutdown();
    }
    if (willrt_jobs_shutdown != NULL) {
        willrt_jobs_shutdown();
    }
}
