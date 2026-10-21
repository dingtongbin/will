/* Bounded channels with a mutex and two condition variables.
 *
 * A channel stores copies of one fixed-size element. The ring keeps the send
 * order: head is the oldest element and count is how many are stored. Send and
 * receive wait in short timed slices so they observe cancellation and the
 * deadline of the current task without a separate wake-up path.
 *
 * The runtime copies elements; the compiler's move analysis is what makes a
 * handoff safe at the source. The priority argument is kept on the channel for
 * the scheduler, which this fallback engine does not schedule by priority yet.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

/* A select waits on one process-wide condition. Every channel state change
   bumps the epoch before it broadcasts, so a select cannot miss a wake between
   its arm scan and its wait. */
static atomic_int willrt_chan_select_state;
static atomic_uint willrt_chan_select_epoch;
static atomic_int willrt_chan_select_waiters;
static willrt_os_mutex willrt_chan_select_lock;
static willrt_os_cond willrt_chan_select_cond;

struct willrt_chan {
    willrt_os_mutex lock;
    willrt_os_cond not_empty;
    willrt_os_cond not_full;
    unsigned char* data;
    size_t elem;
    size_t cap;
    size_t head;
    size_t count;
    int32_t closed;
    int32_t prio;
};

static int64_t willrt_chan_step(void) {
    return INT64_C(10) * INT64_C(1000000);
}

static void willrt_chan_select_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_chan_select_state, &expected, 1)) {
        willrt_os_mutex_init(&willrt_chan_select_lock);
        willrt_os_cond_init(&willrt_chan_select_cond);
        atomic_store(&willrt_chan_select_state, 2);
        return;
    }
    while (atomic_load(&willrt_chan_select_state) != 2) {
        willrt_os_thread_yield();
    }
}

static void willrt_chan_wake_select(void) {
    if (atomic_load(&willrt_chan_select_state) != 2) {
        return;
    }
    atomic_fetch_add(&willrt_chan_select_epoch, 1);
    if (atomic_load(&willrt_chan_select_waiters) == 0) {
        return;
    }
    willrt_os_mutex_lock(&willrt_chan_select_lock);
    willrt_os_cond_broadcast(&willrt_chan_select_cond);
    willrt_os_mutex_unlock(&willrt_chan_select_lock);
}

static uint32_t willrt_chan_select_epoch_now(void) {
    willrt_chan_select_init();
    return atomic_load(&willrt_chan_select_epoch);
}

/* willrt_chan_wait_ns returns the slice a channel wait may sleep. The slice is
   short enough to observe cancellation and the task deadline, but the wait is
   a condition wait, so an idle channel does not consume a processor. */
static int64_t willrt_chan_wait_ns(void) {
    int64_t slice = willrt_chan_step();
    int64_t deadline = willrt_task_deadline();

    if (deadline != 0) {
        int64_t left = deadline - willrt_now_ns();
        if (left <= 0) {
            return 0;
        }
        if (left < slice) {
            slice = left;
        }
    }
    return slice;
}

static int32_t willrt_chan_canceled(void) {
    return willrt_task_canceled();
}

static int32_t willrt_chan_deadline_passed(void) {
    int64_t deadline = willrt_task_deadline();

    return deadline != 0 && willrt_now_ns() >= deadline;
}

willrt_chan* willrt_chan_new(size_t elem, size_t cap, int32_t prio) {
    willrt_chan* chan;
    size_t bytes;

    if (cap == 0) {
        return NULL;
    }
    if (elem == 0) {
        elem = 1;
    }
    if (elem > SIZE_MAX / cap) {
        return NULL;
    }
    chan = (willrt_chan*)willrt_alloc(sizeof(*chan));
    if (chan == NULL) {
        return NULL;
    }
    memset(chan, 0, sizeof(*chan));
    bytes = elem * cap;
    chan->data = (unsigned char*)willrt_alloc(bytes);
    if (chan->data == NULL) {
        willrt_free(chan);
        return NULL;
    }
    chan->elem = elem;
    chan->cap = cap;
    chan->prio = prio;
    willrt_chan_select_init();
    willrt_os_mutex_init(&chan->lock);
    willrt_os_cond_init(&chan->not_empty);
    willrt_os_cond_init(&chan->not_full);
    return chan;
}

int32_t willrt_chan_try_send(willrt_chan* chan, const void* value) {
    int32_t result = WILLRT_OK;
    size_t index;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&chan->lock);
    if (chan->closed) {
        result = WILLRT_CLOSED;
    } else if (chan->count == chan->cap) {
        result = WILLRT_FULL;
    } else {
        index = (chan->head + chan->count) % chan->cap;
        memcpy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        willrt_os_cond_signal(&chan->not_empty);
    }
    willrt_os_mutex_unlock(&chan->lock);
    if (result == WILLRT_OK) {
        willrt_chan_wake_select();
    }
    return result;
}

int32_t willrt_chan_send(willrt_chan* chan, const void* value) {
    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    for (;;) {
        willrt_os_mutex_lock(&chan->lock);
        if (chan->closed) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_CLOSED;
        }
        if (chan->count < chan->cap) {
            size_t index = (chan->head + chan->count) % chan->cap;

            memcpy(chan->data + index * chan->elem, value, chan->elem);
            chan->count++;
            willrt_os_cond_signal(&chan->not_empty);
            willrt_os_mutex_unlock(&chan->lock);
            willrt_chan_wake_select();
            return WILLRT_OK;
        }
        if (willrt_chan_canceled()) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_CANCELED;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_TIMEDOUT;
        }
        willrt_os_cond_timedwait(&chan->not_full, &chan->lock,
                                 willrt_chan_wait_ns());
        willrt_os_mutex_unlock(&chan->lock);
    }
}

int32_t willrt_chan_try_recv(willrt_chan* chan, void* out) {
    int32_t result = WILLRT_OK;

    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&chan->lock);
    if (chan->count != 0) {
        if (out != NULL) {
            memcpy(out, chan->data + chan->head * chan->elem, chan->elem);
        }
        chan->head = (chan->head + 1) % chan->cap;
        chan->count--;
        willrt_os_cond_signal(&chan->not_full);
    } else if (chan->closed) {
        result = WILLRT_CLOSED;
    } else {
        result = WILLRT_EMPTY;
    }
    willrt_os_mutex_unlock(&chan->lock);
    if (result == WILLRT_OK) {
        willrt_chan_wake_select();
    }
    return result;
}

int32_t willrt_chan_recv(willrt_chan* chan, void* out) {
    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    for (;;) {
        willrt_os_mutex_lock(&chan->lock);
        if (chan->count != 0) {
            if (out != NULL) {
                memcpy(out, chan->data + chan->head * chan->elem, chan->elem);
            }
            chan->head = (chan->head + 1) % chan->cap;
            chan->count--;
            willrt_os_cond_signal(&chan->not_full);
            willrt_os_mutex_unlock(&chan->lock);
            willrt_chan_wake_select();
            return WILLRT_OK;
        }
        if (chan->closed) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_CLOSED;
        }
        if (willrt_chan_canceled()) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_CANCELED;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_TIMEDOUT;
        }
        willrt_os_cond_timedwait(&chan->not_empty, &chan->lock,
                                 willrt_chan_wait_ns());
        willrt_os_mutex_unlock(&chan->lock);
    }
}

void willrt_chan_close(willrt_chan* chan) {
    if (chan == NULL) {
        return;
    }
    willrt_os_mutex_lock(&chan->lock);
    chan->closed = 1;
    willrt_os_cond_broadcast(&chan->not_empty);
    willrt_os_cond_broadcast(&chan->not_full);
    willrt_os_mutex_unlock(&chan->lock);
    willrt_chan_wake_select();
}

/* Select checks the arms in array order, never at random. It first tries every
   arm without waiting, then a timeout or default arm, then waits one short slice
   and tries again. A ready receive on a closed channel counts as ready so the
   arm can observe the None it produces. */
static _Thread_local int32_t willrt_select_last = WILLRT_OK;

int32_t willrt_select_status(void) {
    return willrt_select_last;
}

int32_t willrt_select(willrt_arm* arms, size_t n) {
    size_t i;

    for (;;) {
        int32_t default_index = -1;
        int32_t timeout_index = -1;
        uint32_t epoch = willrt_chan_select_epoch_now();

        for (i = 0; i < n; i++) {
            willrt_arm* arm = &arms[i];

            if (arm->kind == 0) {
                int32_t result = willrt_chan_try_recv(arm->chan, arm->out);

                if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                    willrt_select_last = result;
                    return (int32_t)i;
                }
            } else if (arm->kind == 1) {
                int32_t result = willrt_chan_try_send(arm->chan, arm->value);

                if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                    willrt_select_last = result;
                    return (int32_t)i;
                }
            } else if (arm->kind == 2) {
                timeout_index = (int32_t)i;
            } else if (arm->kind == 3) {
                default_index = (int32_t)i;
            }
        }
        if (default_index >= 0) {
            willrt_select_last = WILLRT_OK;
            return default_index;
        }
        if (timeout_index >= 0 && willrt_now_ns() >= arms[timeout_index].ns) {
            willrt_select_last = WILLRT_TIMEDOUT;
            return timeout_index;
        }
        if (willrt_chan_canceled()) {
            willrt_select_last = WILLRT_CANCELED;
            return WILLRT_CANCELED;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_select_last = WILLRT_TIMEDOUT;
            return WILLRT_TIMEDOUT;
        }
        {
            int64_t wait = willrt_chan_step();
            int64_t now = willrt_now_ns();

            if (timeout_index >= 0) {
                int64_t left = arms[timeout_index].ns - now;
                if (left <= 0) {
                    continue;
                }
                if (left < wait) {
                    wait = left;
                }
            }
            if (willrt_task_deadline() != 0) {
                int64_t left = willrt_task_deadline() - now;
                if (left <= 0) {
                    continue;
                }
                if (left < wait) {
                    wait = left;
                }
            }
            willrt_os_mutex_lock(&willrt_chan_select_lock);
            if (atomic_load(&willrt_chan_select_epoch) == epoch) {
                atomic_fetch_add(&willrt_chan_select_waiters, 1);
                willrt_os_cond_timedwait(&willrt_chan_select_cond,
                                         &willrt_chan_select_lock, wait);
                atomic_fetch_sub(&willrt_chan_select_waiters, 1);
            }
            willrt_os_mutex_unlock(&willrt_chan_select_lock);
        }
    }
}
