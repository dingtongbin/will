/* Mutex/RwLock, Semaphore, Count, and Gate over one waiter chain.
 *
 * Every primitive owns one mutex, one condition variable for callers outside
 * the task engine, and a FIFO chain of wait nodes. A releaser decides the
 * next owner under the primitive lock - one writer, or a run of consecutive
 * readers - marks the granted nodes, and wakes their tasks after unlocking,
 * so a woken task never races for the resource. A waiter observes
 * cancellation and the scoped deadline when its step re-runs and unlinks
 * itself without taking the resource, which keeps the cancel-safety
 * contract: permits, counts, and tokens never change on a canceled wait.
 */

#include "../../../include/willrt.h"

#include <stddef.h>
#include <stdint.h>
#include <string.h>

/* One primitive. kind selects what the counts mean; the wait chain is the
   same for all four. */
struct willrt_sync {
    willrt_os_mutex lock;
    willrt_os_cond cond;
    willrt_sync_wait* head;
    willrt_sync_wait* tail;
    int64_t count;
    /* Lock state: an exclusive holder counts one writer; shared holders
       count readers. A queued waiter forces later acquirers to queue behind
       it, so a writer cannot starve behind a stream of readers. */
    int32_t kind;
    int32_t writers;
    int32_t readers;
    int32_t signaled;
};

#define WILLRT_SYNC_LOCK 0
#define WILLRT_SYNC_SEMA 1
#define WILLRT_SYNC_COUNT 2
#define WILLRT_SYNC_GATE 3

/* The blocking fallback waits in slices so a scoped deadline is honored
   without a wake-up path of its own. */
static int64_t willrt_sync_step(void) {
    return INT64_C(10) * INT64_C(1000000);
}

static void willrt_sync_link(struct willrt_sync* s, willrt_sync_wait* wait) {
    wait->linked = 1;
    wait->prev = s->tail;
    wait->next = NULL;
    if (s->tail != NULL) {
        s->tail->next = wait;
    } else {
        s->head = wait;
    }
    s->tail = wait;
}

static void willrt_sync_unlink(struct willrt_sync* s, willrt_sync_wait* wait) {
    if (!wait->linked) {
        return;
    }
    if (wait->prev != NULL) {
        wait->prev->next = wait->next;
    } else {
        s->head = wait->next;
    }
    if (wait->next != NULL) {
        wait->next->prev = wait->prev;
    } else {
        s->tail = wait->prev;
    }
    wait->prev = NULL;
    wait->next = NULL;
    wait->linked = 0;
}

/* willrt_sync_grant pops the satisfied head waiters into wake and returns how
   many. It runs under the primitive lock and never wakes: the caller
   broadcasts and wakes after unlocking. Kind rules: a lock transfers the word
   to one writer or a run of readers, a semaphore spends one permit per
   granted waiter, and count and gate satisfy every waiter at once. */
static int64_t willrt_sync_grant(struct willrt_sync* s, void** wake,
                                 int64_t max) {
    int64_t n = 0;

    while (s->head != NULL) {
        willrt_sync_wait* wait = s->head;
        if (s->kind == WILLRT_SYNC_LOCK) {
            if (wait->mode == WILLRT_SYNC_SHARED) {
                if (s->writers != 0) {
                    break;
                }
                s->readers++;
            } else {
                if (s->writers != 0 || s->readers != 0) {
                    break;
                }
                s->writers = 1;
            }
        } else if (s->kind == WILLRT_SYNC_SEMA) {
            if (s->count <= 0) {
                break;
            }
            s->count--;
        }
        if (wait->task != NULL && n >= max) {
            break;
        }
        willrt_sync_unlink(s, wait);
        wait->status = WILLRT_OK;
        if (wait->task != NULL) {
            wake[n] = wait->task;
            n++;
        }
    }
    return n;
}

/* willrt_sync_flush grants in chunks of the wake table and wakes what it
   granted, so a chain longer than the table still drains. */
static void willrt_sync_flush(struct willrt_sync* s) {
    void* wake[64];

    for (;;) {
        int64_t n;
        willrt_os_cond_broadcast(&s->cond);
        n = willrt_sync_grant(s, wake, 64);
        willrt_os_mutex_unlock(&s->lock);
        if (n > 0) {
            willrt_task_wake_batch(wake, n);
        }
        willrt_os_mutex_lock(&s->lock);
        if (n < 64) {
            break;
        }
    }
}

void* willrt_sync_lock_new(void) {
    struct willrt_sync* s = (struct willrt_sync*)willrt_alloc(sizeof(*s));

    if (s == NULL) {
        return NULL;
    }
    memset(s, 0, sizeof(*s));
    s->kind = WILLRT_SYNC_LOCK;
    willrt_os_mutex_init(&s->lock);
    willrt_os_cond_init(&s->cond);
    return s;
}

void* willrt_sync_sema_new(int64_t permits) {
    struct willrt_sync* s = (struct willrt_sync*)willrt_alloc(sizeof(*s));

    if (s == NULL) {
        return NULL;
    }
    memset(s, 0, sizeof(*s));
    s->kind = WILLRT_SYNC_SEMA;
    s->count = permits;
    willrt_os_mutex_init(&s->lock);
    willrt_os_cond_init(&s->cond);
    return s;
}

void* willrt_sync_count_new(int64_t count) {
    struct willrt_sync* s = (struct willrt_sync*)willrt_alloc(sizeof(*s));

    if (s == NULL) {
        return NULL;
    }
    memset(s, 0, sizeof(*s));
    s->kind = WILLRT_SYNC_COUNT;
    s->count = count;
    willrt_os_mutex_init(&s->lock);
    willrt_os_cond_init(&s->cond);
    return s;
}

void* willrt_sync_gate_new(void) {
    struct willrt_sync* s = (struct willrt_sync*)willrt_alloc(sizeof(*s));

    if (s == NULL) {
        return NULL;
    }
    memset(s, 0, sizeof(*s));
    s->kind = WILLRT_SYNC_GATE;
    willrt_os_mutex_init(&s->lock);
    willrt_os_cond_init(&s->cond);
    return s;
}

/* willrt_sync_try reports whether the acquisition is granted right now, and
   takes it when it is. It runs under the primitive lock. */
static int32_t willrt_sync_try(struct willrt_sync* s, int32_t mode) {
    switch (s->kind) {
    case WILLRT_SYNC_LOCK:
        if (mode == WILLRT_SYNC_SHARED) {
            if (s->writers == 0 && s->head == NULL) {
                s->readers++;
                return 1;
            }
            return 0;
        }
        if (s->writers == 0 && s->readers == 0 && s->head == NULL) {
            s->writers = 1;
            return 1;
        }
        return 0;
    case WILLRT_SYNC_SEMA:
        if (s->count > 0 && s->head == NULL) {
            s->count--;
            return 1;
        }
        return 0;
    case WILLRT_SYNC_COUNT:
        return s->count <= 0 ? 1 : 0;
    default:
        return s->signaled ? 1 : 0;
    }
}

/* willrt_sync_wait_common runs one acquisition to a decision. A task returns
   its embedded node: granted now, or linked with WILLRT_SUSPEND for the step
   to park on. A caller outside a task blocks until granted or timed out. */
static willrt_sync_wait* willrt_sync_wait_common(void* s, int32_t mode) {
    willrt_task* task = willrt_task_current();
    willrt_sync_wait* wait = task == NULL ? NULL : willrt_task_sync_node();
    struct willrt_sync* sync = (struct willrt_sync*)s;

    if (sync == NULL) {
        return NULL;
    }
    if (wait != NULL) {
        if (wait->active) {
            willrt_panic("sync wait already active", "sync.c",
                         "will.sync.wait");
            return NULL;
        }
        wait->active = 1;
        wait->task = task;
    } else {
        wait = (willrt_sync_wait*)willrt_alloc(sizeof(*wait));
        if (wait == NULL) {
            return NULL;
        }
        memset(wait, 0, sizeof(*wait));
    }
    wait->mode = mode;
    wait->owner = s;
    willrt_os_mutex_lock(&sync->lock);
    if (willrt_sync_try(sync, mode)) {
        wait->status = WILLRT_OK;
        willrt_os_mutex_unlock(&sync->lock);
        return wait;
    }
    if (task == NULL) {
        while (!willrt_sync_try(sync, mode)) {
            int64_t deadline = willrt_task_deadline();
            if (deadline != 0 && willrt_now_ns() >= deadline) {
                wait->status = WILLRT_TIMEDOUT;
                break;
            }
            willrt_os_cond_timedwait(&sync->cond, &sync->lock,
                                     willrt_sync_step());
        }
        if (wait->status != WILLRT_TIMEDOUT) {
            wait->status = WILLRT_OK;
        }
        willrt_os_mutex_unlock(&sync->lock);
        return wait;
    }
    wait->status = WILLRT_SUSPEND;
    willrt_sync_link(sync, wait);
    willrt_os_mutex_unlock(&sync->lock);
    return wait;
}

int32_t willrt_sync_wait_poll(willrt_sync_wait* wait) {
    struct willrt_sync* s;
    willrt_task* task;

    if (wait == NULL) {
        return 1;
    }
    s = (struct willrt_sync*)wait->owner;
    if (s == NULL) {
        return 1;
    }
    task = willrt_task_current();
    willrt_os_mutex_lock(&s->lock);
    if (!wait->linked) {
        willrt_os_mutex_unlock(&s->lock);
        return 1;
    }
    if (task != NULL && willrt_task_canceled()) {
        willrt_sync_unlink(s, wait);
        wait->status = WILLRT_CANCELED;
        willrt_os_mutex_unlock(&s->lock);
        return 1;
    }
    if (task != NULL) {
        int64_t deadline = willrt_task_deadline();
        if (deadline != 0 && willrt_now_ns() >= deadline) {
            willrt_sync_unlink(s, wait);
            wait->status = WILLRT_TIMEDOUT;
            willrt_os_mutex_unlock(&s->lock);
            return 1;
        }
    }
    willrt_os_mutex_unlock(&s->lock);
    return 0;
}

int32_t willrt_sync_wait_finish(willrt_sync_wait* wait) {
    int32_t status;

    if (wait == NULL) {
        return WILLRT_FAILED;
    }
    status = wait->status;
    wait->active = 0;
    wait->owner = NULL;
    wait->task = NULL;
    wait->status = 0;
    if (wait->linked) {
        willrt_sync_wait_abandon(wait);
    }
    return status;
}

int64_t willrt_sync_wait_deadline(willrt_sync_wait* wait) {
    (void)wait;
    return 0;
}

void willrt_sync_wait_abandon(willrt_sync_wait* wait) {
    struct willrt_sync* s;

    if (wait == NULL || !wait->linked) {
        return;
    }
    s = (struct willrt_sync*)wait->owner;
    if (s == NULL) {
        return;
    }
    willrt_os_mutex_lock(&s->lock);
    willrt_sync_unlink(s, wait);
    willrt_os_mutex_unlock(&s->lock);
    wait->active = 0;
    wait->owner = NULL;
}

/* willrt_sync_plain runs the blocking fallback and releases the heap node it
   borrowed: a caller outside the task engine has nowhere to keep it. */
static int32_t willrt_sync_plain(void* s, int32_t mode) {
    willrt_sync_wait* wait = willrt_sync_wait_common(s, mode);
    int32_t status;
    int32_t heap;

    if (wait == NULL) {
        return WILLRT_FAILED;
    }
    heap = wait->task == NULL;
    status = willrt_sync_wait_finish(wait);
    if (heap) {
        willrt_free(wait);
    }
    return status;
}

int32_t willrt_sync_lock_async(void* s, int32_t mode) {
    return willrt_sync_plain(s, mode);
}

int32_t willrt_sync_sema_acquire_async(void* s) {
    return willrt_sync_plain(s, WILLRT_SYNC_EXCLUSIVE);
}

int32_t willrt_sync_count_wait_async(void* s) {
    return willrt_sync_plain(s, WILLRT_SYNC_EXCLUSIVE);
}

int32_t willrt_sync_gate_wait_async(void* s) {
    return willrt_sync_plain(s, WILLRT_SYNC_EXCLUSIVE);
}

willrt_sync_wait* willrt_sync_lock_async_start(void* s, int32_t mode) {
    return willrt_sync_wait_common(s, mode);
}

willrt_sync_wait* willrt_sync_sema_acquire_async_start(void* s) {
    return willrt_sync_wait_common(s, WILLRT_SYNC_EXCLUSIVE);
}

willrt_sync_wait* willrt_sync_count_wait_async_start(void* s) {
    return willrt_sync_wait_common(s, WILLRT_SYNC_EXCLUSIVE);
}

willrt_sync_wait* willrt_sync_gate_wait_async_start(void* s) {
    return willrt_sync_wait_common(s, WILLRT_SYNC_EXCLUSIVE);
}

void willrt_sync_unlock(void* s, int32_t mode) {
    struct willrt_sync* sync = (struct willrt_sync*)s;

    if (sync == NULL) {
        return;
    }
    willrt_os_mutex_lock(&sync->lock);
    if (mode == WILLRT_SYNC_SHARED) {
        if (sync->readers > 0) {
            sync->readers--;
        }
    } else {
        sync->writers = 0;
    }
    willrt_sync_flush(sync);
    willrt_os_mutex_unlock(&sync->lock);
}

int32_t willrt_sync_lock_try(void* s, int32_t mode) {
    struct willrt_sync* sync = (struct willrt_sync*)s;
    int32_t granted;

    if (sync == NULL) {
        return 0;
    }
    willrt_os_mutex_lock(&sync->lock);
    granted = willrt_sync_try(sync, mode);
    willrt_os_mutex_unlock(&sync->lock);
    return granted;
}

int32_t willrt_sync_sema_try_acquire(void* s) {
    struct willrt_sync* sync = (struct willrt_sync*)s;
    int32_t granted;

    if (sync == NULL) {
        return 0;
    }
    willrt_os_mutex_lock(&sync->lock);
    granted = willrt_sync_try(sync, WILLRT_SYNC_EXCLUSIVE);
    willrt_os_mutex_unlock(&sync->lock);
    return granted;
}

int64_t willrt_sync_sema_release(void* s) {
    struct willrt_sync* sync = (struct willrt_sync*)s;
    int64_t permits;

    if (sync == NULL) {
        return -1;
    }
    willrt_os_mutex_lock(&sync->lock);
    sync->count++;
    permits = sync->count;
    willrt_sync_flush(sync);
    willrt_os_mutex_unlock(&sync->lock);
    return permits;
}

void willrt_sync_count_add(void* s, int64_t delta) {
    struct willrt_sync* sync = (struct willrt_sync*)s;

    if (sync == NULL) {
        return;
    }
    willrt_os_mutex_lock(&sync->lock);
    sync->count += delta;
    if (sync->count <= 0) {
        /* Every waiter watches the same condition, so one flip satisfies all
           of them; the counter holds at or below zero until raised again. */
        willrt_sync_flush(sync);
    }
    willrt_os_mutex_unlock(&sync->lock);
}

int64_t willrt_sync_count_load(void* s) {
    struct willrt_sync* sync = (struct willrt_sync*)s;
    int64_t count;

    if (sync == NULL) {
        return 0;
    }
    willrt_os_mutex_lock(&sync->lock);
    count = sync->count;
    willrt_os_mutex_unlock(&sync->lock);
    return count;
}

void willrt_sync_gate_signal(void* s) {
    struct willrt_sync* sync = (struct willrt_sync*)s;

    if (sync == NULL) {
        return;
    }
    willrt_os_mutex_lock(&sync->lock);
    sync->signaled = 1;
    willrt_sync_flush(sync);
    willrt_os_mutex_unlock(&sync->lock);
}

void willrt_sync_gate_reset(void* s) {
    struct willrt_sync* sync = (struct willrt_sync*)s;

    if (sync == NULL) {
        return;
    }
    willrt_os_mutex_lock(&sync->lock);
    sync->signaled = 0;
    willrt_os_mutex_unlock(&sync->lock);
}
