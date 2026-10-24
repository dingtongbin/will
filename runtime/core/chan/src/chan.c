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
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#if defined(__clang__) || defined(__GNUC__)
#define WILLRT_CHAN_SLOW __attribute__((noinline))
#else
#define WILLRT_CHAN_SLOW
#endif

int32_t willrt_select(willrt_arm* arms, size_t n);
static _Thread_local int32_t willrt_select_last = WILLRT_OK;



/* One blocked operation. A blocking caller owns a stack node; a task wait will
   use the same shape from its frame once the compiler reserves it. The node is
   linked on the channel's receive or send chain, so a matching operation hands
   the value to this node instead of touching the ring or broadcasting. */
struct willrt_select_state {
    willrt_os_mutex lock;
    willrt_os_cond cond;
    willrt_task* task;
    int64_t timeout_ns;
    int32_t timeout_index;
    int32_t ready;
    int32_t index;
    int32_t status;
};

typedef struct willrt_select_ctx {
    willrt_arm* arms;
    size_t n;
    willrt_chan_wait* waits;
    willrt_select_state state;
} willrt_select_ctx;

struct willrt_chan {
    willrt_os_mutex lock;
    unsigned char* data;
    size_t elem;
    size_t cap;
    size_t head;
    size_t count;
    willrt_chan_wait* recv_head;
    willrt_chan_wait* recv_tail;
    willrt_chan_wait* send_head;
    willrt_chan_wait* send_tail;
    /* The counts are updated under the channel lock and read after unlock by
       the select wake path. */
    _Atomic(size_t) recv_waiters;
    _Atomic(size_t) send_waiters;
    int32_t closed;
    int32_t prio;
};

static int64_t willrt_chan_step(void) {
    return INT64_C(10) * INT64_C(1000000);
}

/* A ring index is head + offset modulo cap. Both terms are below cap, so one
   subtraction replaces the division the source form would emit. */
static size_t willrt_chan_at(const willrt_chan* chan, size_t offset) {
    size_t at = chan->head + offset;
    if (at >= chan->cap) {
        at -= chan->cap;
    }
    return at;
}

static size_t willrt_chan_next(size_t at, size_t cap) {
    at++;
    return at == cap ? 0 : at;
}

/* Elements are usually 1, 2, 4, or 8 bytes, and the allocator gives the ring
   natural alignment. A fixed-width copy avoids a memcpy call in the hot path. */
static void willrt_chan_copy(void* dst, const void* src, size_t elem) {
    if (elem == 8) {
        *(uint64_t*)dst = *(const uint64_t*)src;
        return;
    }
    if (elem == 4) {
        *(uint32_t*)dst = *(const uint32_t*)src;
        return;
    }
    if (elem == 1) {
        *(uint8_t*)dst = *(const uint8_t*)src;
        return;
    }
    if (elem == 2) {
        *(uint16_t*)dst = *(const uint16_t*)src;
        return;
    }
    memcpy(dst, src, elem);
}

/* Claims the select before copying a value. The first channel that claims wins;
   a losing arm is dropped without consuming its value. */
static int32_t willrt_select_claim(willrt_chan_wait* wait, int32_t status) {
    willrt_select_state* state = wait->select;

    if (state == NULL) {
        wait->status = status;
        wait->ready = 1;
        return 1;
    }
    willrt_os_mutex_lock(&state->lock);
    if (state->ready) {
        willrt_os_mutex_unlock(&state->lock);
        return 0;
    }
    state->ready = 1;
    state->index = (int32_t)wait->arm;
    state->status = status;
    willrt_os_mutex_unlock(&state->lock);
    return 1;
}

static void willrt_select_signal(willrt_chan_wait* wait) {
    willrt_select_state* state = wait->select;

    if (state == NULL) {
        if (wait->task != NULL) {
            willrt_task_wake_chan(wait->task);
        } else {
            willrt_os_cond_signal(&wait->cond);
        }
        return;
    }
    willrt_os_mutex_lock(&state->lock);
    if (state->task != NULL) {
        willrt_task_wake_chan(state->task);
    } else {
        willrt_os_cond_signal(&state->cond);
    }
    willrt_os_mutex_unlock(&state->lock);
}

static int32_t willrt_chan_handoff_recv(willrt_chan_wait* wait,
                                        const void* value, size_t elem) {
    wait->linked = 0;
    if (!willrt_select_claim(wait, WILLRT_OK)) {
        return 0;
    }
    if (wait->out != NULL) {
        willrt_chan_copy(wait->out, value, elem);
    }
    willrt_select_signal(wait);
    return 1;
}

static int32_t willrt_chan_handoff_send(willrt_chan_wait* wait, void* out,
                                        size_t elem) {
    wait->linked = 0;
    if (!willrt_select_claim(wait, WILLRT_OK)) {
        return 0;
    }
    if (out != NULL) {
        willrt_chan_copy(out, wait->value, elem);
    }
    willrt_select_signal(wait);
    return 1;
}

static void willrt_chan_wait_complete(willrt_chan_wait* wait, int32_t status) {
    if (wait->select != NULL) {
        if (willrt_select_claim(wait, status)) {
            wait->status = status;
            wait->ready = 1;
            willrt_select_signal(wait);
        }
        return;
    }
    wait->status = status;
    wait->ready = 1;
    if (wait->task != NULL) {
        willrt_task_wake_chan(wait->task);
    } else {
        willrt_os_cond_signal(&wait->cond);
    }
}

static void willrt_chan_wait_push(willrt_chan_wait** head,
                                  willrt_chan_wait** tail,
                                  willrt_chan_wait* wait, int32_t kind) {
    wait->kind = kind;
    wait->linked = 1;
    wait->next = NULL;
    if (*tail == NULL) {
        *head = wait;
    } else {
        (*tail)->next = wait;
    }
    *tail = wait;
}

static willrt_chan_wait* willrt_chan_wait_pop(willrt_chan_wait** head,
                                              willrt_chan_wait** tail) {
    willrt_chan_wait* wait = *head;

    if (wait == NULL) {
        return NULL;
    }
    *head = wait->next;
    if (*head == NULL) {
        *tail = NULL;
    }
    wait->next = NULL;
    wait->linked = 0;
    return wait;
}

static void willrt_chan_wait_unlink(willrt_chan_wait** head,
                                    willrt_chan_wait** tail,
                                    willrt_chan_wait* wait) {
    willrt_chan_wait* cursor = *head;
    willrt_chan_wait* previous = NULL;

    while (cursor != NULL && cursor != wait) {
        previous = cursor;
        cursor = cursor->next;
    }
    if (cursor == NULL) {
        return;
    }
    if (previous == NULL) {
        *head = cursor->next;
    } else {
        previous->next = cursor->next;
    }
    if (*tail == cursor) {
        *tail = previous;
    }
    cursor->next = NULL;
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
    willrt_os_mutex_init(&chan->lock);
    return chan;
}

static int32_t willrt_chan_try_send_slow(willrt_chan* chan, const void* value);

int32_t willrt_chan_try_send(willrt_chan* chan, const void* value) {
    size_t index;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->closed) {
            return WILLRT_CLOSED;
        }
        if (chan->count == chan->cap) {
            return WILLRT_FULL;
        }
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        return WILLRT_OK;
    }
    return willrt_chan_try_send_slow(chan, value);
}

WILLRT_CHAN_SLOW static int32_t willrt_chan_try_send_slow(willrt_chan* chan, const void* value) {
    willrt_chan_wait* wait;
    size_t index;
    int32_t result = WILLRT_OK;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->closed) {
            return WILLRT_CLOSED;
        }
        if (chan->count == chan->cap) {
            return WILLRT_FULL;
        }
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        return WILLRT_OK;
    }
    willrt_os_mutex_lock(&chan->lock);
    if (chan->closed) {
        result = WILLRT_CLOSED;
    } else if ((wait = willrt_chan_wait_pop(&chan->recv_head,
                                            &chan->recv_tail)) != NULL) {
        atomic_fetch_sub(&chan->recv_waiters, 1);
        (void)willrt_chan_handoff_recv(wait, value, chan->elem);
    } else if (chan->count == chan->cap) {
        result = WILLRT_FULL;
    } else {
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
    }
    willrt_os_mutex_unlock(&chan->lock);
    if (result == WILLRT_OK) {
    }
    return result;
}

static int32_t willrt_chan_send_slow(willrt_chan* chan, const void* value);

int32_t willrt_chan_send(willrt_chan* chan, const void* value) {
    size_t index;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->closed) {
            return WILLRT_CLOSED;
        }
        if (chan->count == chan->cap) {
            return WILLRT_FULL;
        }
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        return WILLRT_OK;
    }
    return willrt_chan_send_slow(chan, value);
}

WILLRT_CHAN_SLOW static int32_t willrt_chan_send_slow(willrt_chan* chan, const void* value) {
    willrt_chan_wait wait;
    willrt_chan_wait* ready;
    int32_t result = WILLRT_OK;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        size_t index;
        if (chan->closed) {
            return WILLRT_CLOSED;
        }
        if (chan->count == chan->cap) {
            return WILLRT_FULL;
        }
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        return WILLRT_OK;
    }
    memset(&wait, 0, sizeof(wait));
    willrt_os_mutex_lock(&chan->lock);
    if (chan->closed) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_CLOSED;
    }
    ready = willrt_chan_wait_pop(&chan->recv_head, &chan->recv_tail);
    if (ready != NULL) {
        atomic_fetch_sub(&chan->recv_waiters, 1);
        (void)willrt_chan_handoff_recv(ready, value, chan->elem);
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->count < chan->cap) {
        size_t index = willrt_chan_at(chan, chan->count);

        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    memset(&wait, 0, sizeof(wait));
    wait.value = value;
    willrt_os_cond_init(&wait.cond);
    willrt_chan_wait_push(&chan->send_head, &chan->send_tail, &wait, 1);
    atomic_fetch_add(&chan->send_waiters, 1);
    while (!wait.ready) {
        willrt_os_cond_timedwait(&wait.cond, &chan->lock, willrt_chan_wait_ns());
        if (wait.ready) {
            break;
        }
        if (willrt_chan_canceled()) {
            willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail, &wait);
            atomic_fetch_sub(&chan->send_waiters, 1);
            result = WILLRT_CANCELED;
            break;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail, &wait);
            atomic_fetch_sub(&chan->send_waiters, 1);
            result = WILLRT_TIMEDOUT;
            break;
        }
    }
    if (wait.ready) {
        result = wait.status;
    }
    willrt_os_mutex_unlock(&chan->lock);
    willrt_os_cond_destroy(&wait.cond);
    if (result == WILLRT_OK) {
    }
    return result;
}

static int32_t willrt_chan_try_recv_slow(willrt_chan* chan, void* out);

int32_t willrt_chan_try_recv(willrt_chan* chan, void* out) {
    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->count != 0) {
            if (out != NULL) {
                willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
            }
            chan->head = willrt_chan_next(chan->head, chan->cap);
            chan->count--;
            return WILLRT_OK;
        }
        return chan->closed ? WILLRT_CLOSED : WILLRT_EMPTY;
    }
    return willrt_chan_try_recv_slow(chan, out);
}

WILLRT_CHAN_SLOW static int32_t willrt_chan_try_recv_slow(willrt_chan* chan, void* out) {
    willrt_chan_wait* wait;
    int32_t result = WILLRT_OK;

    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->count != 0) {
            if (out != NULL) {
                willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
            }
            chan->head = willrt_chan_next(chan->head, chan->cap);
            chan->count--;
            return WILLRT_OK;
        }
        return chan->closed ? WILLRT_CLOSED : WILLRT_EMPTY;
    }
    willrt_os_mutex_lock(&chan->lock);
    if ((wait = willrt_chan_wait_pop(&chan->send_head, &chan->send_tail)) != NULL) {
        atomic_fetch_sub(&chan->send_waiters, 1);
        (void)willrt_chan_handoff_send(wait, out, chan->elem);
    } else if (chan->count != 0) {
        if (out != NULL) {
            willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
        }
        chan->head = willrt_chan_next(chan->head, chan->cap);
        chan->count--;
    } else if (chan->closed) {
        result = WILLRT_CLOSED;
    } else {
        result = WILLRT_EMPTY;
    }
    willrt_os_mutex_unlock(&chan->lock);
    if (result == WILLRT_OK) {
    }
    return result;
}

static int32_t willrt_chan_recv_slow(willrt_chan* chan, void* out);

int32_t willrt_chan_recv(willrt_chan* chan, void* out) {
    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->count != 0) {
            if (out != NULL) {
                willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
            }
            chan->head = willrt_chan_next(chan->head, chan->cap);
            chan->count--;
            return WILLRT_OK;
        }
        return chan->closed ? WILLRT_CLOSED : WILLRT_EMPTY;
    }
    return willrt_chan_recv_slow(chan, out);
}

WILLRT_CHAN_SLOW static int32_t willrt_chan_recv_slow(willrt_chan* chan, void* out) {
    willrt_chan_wait wait;
    willrt_chan_wait* ready;
    int32_t result = WILLRT_OK;

    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    if (!willrt_runtime_threaded()) {
        if (chan->count != 0) {
            if (out != NULL) {
                willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
            }
            chan->head = willrt_chan_next(chan->head, chan->cap);
            chan->count--;
            return WILLRT_OK;
        }
        return chan->closed ? WILLRT_CLOSED : WILLRT_EMPTY;
    }
    memset(&wait, 0, sizeof(wait));
    wait.out = out;
    willrt_os_mutex_lock(&chan->lock);
    ready = willrt_chan_wait_pop(&chan->send_head, &chan->send_tail);
    if (ready != NULL) {
        atomic_fetch_sub(&chan->send_waiters, 1);
        (void)willrt_chan_handoff_send(ready, out, chan->elem);
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->count != 0) {
        if (out != NULL) {
            willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
        }
        chan->head = willrt_chan_next(chan->head, chan->cap);
        chan->count--;
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->closed) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_CLOSED;
    }
    memset(&wait, 0, sizeof(wait));
    wait.out = out;
    willrt_os_cond_init(&wait.cond);
    willrt_chan_wait_push(&chan->recv_head, &chan->recv_tail, &wait, 0);
    atomic_fetch_add(&chan->recv_waiters, 1);
    while (!wait.ready) {
        willrt_os_cond_timedwait(&wait.cond, &chan->lock, willrt_chan_wait_ns());
        if (wait.ready) {
            break;
        }
        if (willrt_chan_canceled()) {
            willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail, &wait);
            atomic_fetch_sub(&chan->recv_waiters, 1);
            result = WILLRT_CANCELED;
            break;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail, &wait);
            atomic_fetch_sub(&chan->recv_waiters, 1);
            result = WILLRT_TIMEDOUT;
            break;
        }
    }
    if (wait.ready) {
        result = wait.status;
    }
    willrt_os_mutex_unlock(&chan->lock);
    willrt_os_cond_destroy(&wait.cond);
    if (result == WILLRT_OK) {
    }
    return result;
}
int32_t willrt_chan_recv_wait(willrt_chan* chan, void* out) {
    willrt_task* task;
    willrt_chan_wait* wait;
    willrt_chan_wait* sender;

    if (chan == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&chan->lock);
    task = willrt_task_current();
    wait = task == NULL ? NULL : willrt_task_chan_wait_node();
    if (wait != NULL) {
        if (wait->ready) {
            int32_t status = wait->status;
            wait->ready = 0;
            willrt_os_mutex_unlock(&chan->lock);
            return status;
        }
        if (wait->linked) {
            if (willrt_chan_canceled()) {
                willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail, wait);
                atomic_fetch_sub(&chan->recv_waiters, 1);
                wait->linked = 0;
                willrt_os_mutex_unlock(&chan->lock);
                return WILLRT_CANCELED;
            }
            if (willrt_chan_deadline_passed()) {
                willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail, wait);
                atomic_fetch_sub(&chan->recv_waiters, 1);
                wait->linked = 0;
                willrt_os_mutex_unlock(&chan->lock);
                return WILLRT_TIMEDOUT;
            }
            /* A wake on the IO epoch can be unrelated to this channel. Remove
               the old node and retry the fast paths before parking again. */
            willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail, wait);
            atomic_fetch_sub(&chan->recv_waiters, 1);
            wait->linked = 0;
        }
    }
    sender = willrt_chan_wait_pop(&chan->send_head, &chan->send_tail);
    if (sender != NULL) {
        atomic_fetch_sub(&chan->send_waiters, 1);
        (void)willrt_chan_handoff_send(sender, out, chan->elem);
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->count != 0) {
        if (out != NULL) {
            willrt_chan_copy(out, chan->data + chan->head * chan->elem, chan->elem);
        }
        chan->head = willrt_chan_next(chan->head, chan->cap);
        chan->count--;
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->closed) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_CLOSED;
    }
    if (task == NULL || wait == NULL) {
        willrt_os_mutex_unlock(&chan->lock);
        return willrt_chan_recv(chan, out);
    }
    if (willrt_chan_canceled()) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_CANCELED;
    }
    if (willrt_chan_deadline_passed()) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_TIMEDOUT;
    }
    memset(wait, 0, sizeof(*wait));
    wait->task = task;
    wait->out = out;
    willrt_chan_wait_push(&chan->recv_head, &chan->recv_tail, wait, 0);
    atomic_fetch_add(&chan->recv_waiters, 1);
    willrt_os_mutex_unlock(&chan->lock);
    return WILLRT_SUSPEND;
}
int32_t willrt_chan_send_wait(willrt_chan* chan, const void* value) {
    willrt_task* task;
    willrt_chan_wait* wait;
    willrt_chan_wait* ready;
    size_t index;

    if (chan == NULL || value == NULL) {
        return WILLRT_FAILED;
    }
    task = willrt_task_current();
    if (task == NULL) {
        return willrt_chan_send(chan, value);
    }
    willrt_os_mutex_lock(&chan->lock);
    wait = willrt_task_chan_wait_node();
    if (wait != NULL && wait->ready) {
        int32_t status = wait->status;
        wait->ready = 0;
        willrt_os_mutex_unlock(&chan->lock);
        return status;
    }
    if (wait != NULL && wait->linked) {
        if (willrt_chan_canceled()) {
            willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail, wait);
            atomic_fetch_sub(&chan->send_waiters, 1);
            wait->linked = 0;
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_CANCELED;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail, wait);
            atomic_fetch_sub(&chan->send_waiters, 1);
            wait->linked = 0;
            willrt_os_mutex_unlock(&chan->lock);
            return WILLRT_TIMEDOUT;
        }
        /* A wake may have come from an unrelated IO epoch. Drop the old node
           and retry the direct handoff and ring paths before parking. */
        willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail, wait);
        atomic_fetch_sub(&chan->send_waiters, 1);
        wait->linked = 0;
    }
    if (chan->closed) {
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_CLOSED;
    }
    ready = willrt_chan_wait_pop(&chan->recv_head, &chan->recv_tail);
    if (ready != NULL) {
        atomic_fetch_sub(&chan->recv_waiters, 1);
        (void)willrt_chan_handoff_recv(ready, value, chan->elem);
        willrt_os_mutex_unlock(&chan->lock);
        return WILLRT_OK;
    }
    if (chan->count < chan->cap) {
        index = willrt_chan_at(chan, chan->count);
        willrt_chan_copy(chan->data + index * chan->elem, value, chan->elem);
        chan->count++;
        willrt_os_mutex_unlock(&chan->lock);
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
    if (wait == NULL) {
        willrt_os_mutex_unlock(&chan->lock);
        return willrt_chan_send(chan, value);
    }
    memset(wait, 0, sizeof(*wait));
    wait->task = task;
    wait->value = value;
    wait->kind = 1;
    willrt_chan_wait_push(&chan->send_head, &chan->send_tail, wait, 1);
    atomic_fetch_add(&chan->send_waiters, 1);
    willrt_os_mutex_unlock(&chan->lock);
    return WILLRT_SUSPEND;
}

/* A select context block is allocated once and reused. The free list is
   thread-local: a task runs start, poll, and finish on its own lane, so the
   reuse never crosses a lock. The block carries its wait capacity; a larger
   select takes or grows another block. */
typedef struct willrt_select_block {
    struct willrt_select_block* next;
    size_t cap;
    willrt_select_ctx ctx;
} willrt_select_block;

static _Thread_local willrt_select_block* willrt_select_pool;

static willrt_select_ctx* willrt_select_ctx_take(willrt_arm* arms, size_t n) {
    willrt_select_block* previous = NULL;
    willrt_select_block* node = willrt_select_pool;

    while (node != NULL && node->cap < n) {
        previous = node;
        node = node->next;
    }
    if (node != NULL) {
        if (previous == NULL) {
            willrt_select_pool = node->next;
        } else {
            previous->next = node->next;
        }
        node->next = NULL;
    } else {
        size_t cap = n < 4 ? 4 : n;

        node = (willrt_select_block*)willrt_alloc(
            sizeof(*node) + cap * sizeof(willrt_chan_wait));
        if (node == NULL) {
            return NULL;
        }
        node->cap = cap;
        willrt_os_mutex_init(&node->ctx.state.lock);
        willrt_os_cond_init(&node->ctx.state.cond);
    }
    node->ctx.arms = arms;
    node->ctx.n = n;
    node->ctx.waits = n != 0 ? (willrt_chan_wait*)(node + 1) : NULL;
    if (n != 0) {
        memset(node->ctx.waits, 0, n * sizeof(willrt_chan_wait));
    }
    node->ctx.state.task = willrt_task_current();
    node->ctx.state.timeout_ns = 0;
    node->ctx.state.timeout_index = -1;
    node->ctx.state.ready = 0;
    node->ctx.state.index = WILLRT_SUSPEND;
    node->ctx.state.status = WILLRT_OK;
    return &node->ctx;
}

static void willrt_select_ctx_give(willrt_select_ctx* ctx) {
    willrt_select_block* node =
        (willrt_select_block*)((unsigned char*)ctx -
                               offsetof(willrt_select_block, ctx));

    node->next = willrt_select_pool;
    willrt_select_pool = node;
}

willrt_select_ctx* willrt_select_start(willrt_arm* arms, size_t n) {
    willrt_select_ctx* ctx;
    size_t i;
    int32_t default_index = -1;

    ctx = willrt_select_ctx_take(arms, n);
    if (ctx == NULL) {
        return NULL;
    }
    if (ctx->state.task == NULL) {
        int32_t index = willrt_select(arms, n);
        ctx->state.ready = 1;
        ctx->state.index = index;
        ctx->state.status = willrt_select_last;
        return ctx;
    }
    for (i = 0; i < n; i++) {
        willrt_arm* arm = &arms[i];
        int32_t result;

        if (arm->kind == 0) {
            result = willrt_chan_try_recv(arm->chan, arm->out);
            if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                ctx->state.ready = 1;
                ctx->state.index = (int32_t)i;
                ctx->state.status = result;
                return ctx;
            }
        } else if (arm->kind == 1) {
            result = willrt_chan_try_send(arm->chan, arm->value);
            if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                ctx->state.ready = 1;
                ctx->state.index = (int32_t)i;
                ctx->state.status = result;
                return ctx;
            }
        } else if (arm->kind == 2) {
            ctx->state.timeout_index = (int32_t)i;
            ctx->state.timeout_ns = arm->ns;
        } else if (arm->kind == 3) {
            default_index = (int32_t)i;
        }
    }
    if (default_index >= 0) {
        ctx->state.ready = 1;
        ctx->state.index = default_index;
        ctx->state.status = WILLRT_OK;
        return ctx;
    }
    if (ctx->state.timeout_index >= 0 &&
        willrt_now_ns() >= ctx->state.timeout_ns) {
        ctx->state.ready = 1;
        ctx->state.index = ctx->state.timeout_index;
        ctx->state.status = WILLRT_TIMEDOUT;
        return ctx;
    }
    for (i = 0; i < n; i++) {
        willrt_arm* arm = &arms[i];
        willrt_chan_wait* wait = &ctx->waits[i];

        if (arm->kind != 0 && arm->kind != 1) {
            continue;
        }
        wait->select = &ctx->state;
        wait->arm = i;
        wait->kind = arm->kind;
        wait->out = arm->out;
        wait->value = arm->value;
        willrt_os_mutex_lock(&arm->chan->lock);
        if (arm->chan->closed) {
            willrt_os_mutex_unlock(&arm->chan->lock);
            if (willrt_select_claim(wait, WILLRT_CLOSED)) {
                willrt_select_signal(wait);
            }
            break;
        }
        if (arm->kind == 0) {
            willrt_chan_wait_push(&arm->chan->recv_head,
                                  &arm->chan->recv_tail, wait, 0);
            atomic_fetch_add(&arm->chan->recv_waiters, 1);
        } else {
            willrt_chan_wait_push(&arm->chan->send_head,
                                  &arm->chan->send_tail, wait, 1);
            atomic_fetch_add(&arm->chan->send_waiters, 1);
        }
        willrt_os_mutex_unlock(&arm->chan->lock);
    }
    if (ctx->state.timeout_index >= 0) {
        willrt_task_wait_until(ctx->state.timeout_ns);
    }
    return ctx;
}

int32_t willrt_select_poll(willrt_select_ctx* ctx) {
    int32_t index;

    if (ctx == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&ctx->state.lock);
    if (!ctx->state.ready && ctx->state.timeout_index >= 0 &&
        willrt_now_ns() >= ctx->state.timeout_ns) {
        ctx->state.ready = 1;
        ctx->state.index = ctx->state.timeout_index;
        ctx->state.status = WILLRT_TIMEDOUT;
    }
    index = ctx->state.ready ? ctx->state.index : WILLRT_SUSPEND;
    willrt_os_mutex_unlock(&ctx->state.lock);
    return index;
}

void willrt_select_finish(willrt_select_ctx* ctx) {
    size_t i;

    if (ctx == NULL) {
        return;
    }
    for (i = 0; i < ctx->n; i++) {
        willrt_chan_wait* wait = &ctx->waits[i];
        willrt_chan* chan = ctx->arms[i].chan;

        if (wait->select == NULL || chan == NULL) {
            continue;
        }
        willrt_os_mutex_lock(&chan->lock);
        if (wait->linked) {
            if (wait->kind == 0) {
                willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail,
                                        wait);
                atomic_fetch_sub(&chan->recv_waiters, 1);
            } else {
                willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail,
                                        wait);
                atomic_fetch_sub(&chan->send_waiters, 1);
            }
            wait->linked = 0;
        }
        willrt_os_mutex_unlock(&chan->lock);
    }
    willrt_select_last = ctx->state.status;
    willrt_select_ctx_give(ctx);
}

void willrt_chan_close(willrt_chan* chan) {
    if (chan == NULL) {
        return;
    }
    willrt_chan_wait* wait;

    willrt_os_mutex_lock(&chan->lock);
    chan->closed = 1;
    while ((wait = willrt_chan_wait_pop(&chan->recv_head,
                                        &chan->recv_tail)) != NULL) {
        atomic_fetch_sub(&chan->recv_waiters, 1);
        if (chan->count != 0) {
            const void* value = chan->data + chan->head * chan->elem;

            if (willrt_chan_handoff_recv(wait, value, chan->elem)) {
                chan->head = willrt_chan_next(chan->head, chan->cap);
                chan->count--;
            }
        } else {
            willrt_chan_wait_complete(wait, WILLRT_CLOSED);
        }
    }
    while ((wait = willrt_chan_wait_pop(&chan->send_head,
                                        &chan->send_tail)) != NULL) {
        atomic_fetch_sub(&chan->send_waiters, 1);
        willrt_chan_wait_complete(wait, WILLRT_CLOSED);
    }
    willrt_os_mutex_unlock(&chan->lock);
}

/* Select checks the arms in array order, never at random. It first tries every
   arm without waiting, then a timeout or default arm, then waits one short slice
   and tries again. A ready receive on a closed channel counts as ready so the
   arm can observe the None it produces. */
int32_t willrt_select_status(void) {
    return willrt_select_last;
}

void willrt_select_set_status(int32_t status) {
    willrt_select_last = status;
}

int32_t willrt_select(willrt_arm* arms, size_t n) {
    size_t i;

    /* The single-thread form touches the ring directly, so a select whose first
       arm is ready costs no locks and no nested channel calls. */
    if (!willrt_runtime_threaded()) {
        int32_t default_index = -1;
        int32_t timeout_index = -1;

        for (i = 0; i < n; i++) {
            willrt_arm* arm = &arms[i];
            if (arm->kind == 0) {
                willrt_chan* chan = arm->chan;
                if (chan != NULL && chan->count != 0) {
                    if (arm->out != NULL) {
                        willrt_chan_copy(arm->out,
                                          chan->data + chan->head * chan->elem,
                                          chan->elem);
                    }
                    chan->head = willrt_chan_next(chan->head, chan->cap);
                    chan->count--;
                    willrt_select_last = WILLRT_OK;
                    return (int32_t)i;
                }
                if (chan != NULL && chan->closed) {
                    willrt_select_last = WILLRT_CLOSED;
                    return (int32_t)i;
                }
            } else if (arm->kind == 1) {
                willrt_chan* chan = arm->chan;
                if (chan != NULL && chan->closed) {
                    willrt_select_last = WILLRT_CLOSED;
                    return (int32_t)i;
                }
                if (chan != NULL && chan->count < chan->cap) {
                    size_t at = willrt_chan_at(chan, chan->count);
                    willrt_chan_copy(chan->data + at * chan->elem, arm->value, chan->elem);
                    chan->count++;
                    willrt_select_last = WILLRT_OK;
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
    }

    {
        willrt_chan_wait waits[n > 0 ? n : 1];
        willrt_select_state state;
        int32_t default_index = -1;
        int32_t timeout_index = -1;
        int32_t result = -1;
        int32_t status = WILLRT_OK;
        int64_t timeout_ns = 0;
        size_t i;

        memset(waits, 0, sizeof(waits));
        memset(&state, 0, sizeof(state));
        willrt_os_mutex_init(&state.lock);
        willrt_os_cond_init(&state.cond);

        for (i = 0; i < n; i++) {
            willrt_arm* arm = &arms[i];

            if (arm->kind == 0) {
                result = willrt_chan_try_recv(arm->chan, arm->out);
                if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                    willrt_select_last = result;
                    result = (int32_t)i;
                    goto select_done;
                }
            } else if (arm->kind == 1) {
                result = willrt_chan_try_send(arm->chan, arm->value);
                if (result == WILLRT_OK || result == WILLRT_CLOSED) {
                    willrt_select_last = result;
                    result = (int32_t)i;
                    goto select_done;
                }
            } else if (arm->kind == 2) {
                timeout_index = (int32_t)i;
                timeout_ns = arm->ns;
            } else if (arm->kind == 3) {
                default_index = (int32_t)i;
            }
        }
        if (default_index >= 0) {
            willrt_select_last = WILLRT_OK;
            result = default_index;
            goto select_done;
        }
        if (timeout_index >= 0 && willrt_now_ns() >= timeout_ns) {
            willrt_select_last = WILLRT_TIMEDOUT;
            result = timeout_index;
            goto select_done;
        }
        if (willrt_chan_canceled()) {
            willrt_select_last = WILLRT_CANCELED;
            result = WILLRT_CANCELED;
            goto select_done;
        }
        if (willrt_chan_deadline_passed()) {
            willrt_select_last = WILLRT_TIMEDOUT;
            result = WILLRT_TIMEDOUT;
            goto select_done;
        }

        for (i = 0; i < n; i++) {
            willrt_arm* arm = &arms[i];
            willrt_chan_wait* wait = &waits[i];

            if (arm->kind != 0 && arm->kind != 1) {
                continue;
            }
            wait->select = &state;
            wait->arm = i;
            wait->kind = arm->kind;
            wait->out = arm->out;
            wait->value = arm->value;
            willrt_os_mutex_lock(&arm->chan->lock);
            if (arm->chan->closed) {
                willrt_os_mutex_unlock(&arm->chan->lock);
                willrt_select_claim(wait, WILLRT_CLOSED);
                break;
            }
            if (arm->kind == 0) {
                willrt_chan_wait_push(&arm->chan->recv_head,
                                      &arm->chan->recv_tail, wait, 0);
                atomic_fetch_add(&arm->chan->recv_waiters, 1);
            } else {
                willrt_chan_wait_push(&arm->chan->send_head,
                                      &arm->chan->send_tail, wait, 1);
                atomic_fetch_add(&arm->chan->send_waiters, 1);
            }
            willrt_os_mutex_unlock(&arm->chan->lock);
        }

        willrt_os_mutex_lock(&state.lock);
        while (!state.ready) {
            int64_t wait = willrt_chan_step();
            int64_t now = willrt_now_ns();

            if (timeout_index >= 0) {
                int64_t left = timeout_ns - now;
                if (left <= 0) {
                    state.ready = 1;
                    state.index = timeout_index;
                    state.status = WILLRT_TIMEDOUT;
                    break;
                }
                if (left < wait) {
                    wait = left;
                }
            }
            if (willrt_task_deadline() != 0) {
                int64_t left = willrt_task_deadline() - now;
                if (left <= 0) {
                    state.ready = 1;
                    state.index = WILLRT_TIMEDOUT;
                    state.status = WILLRT_TIMEDOUT;
                    break;
                }
                if (left < wait) {
                    wait = left;
                }
            }
            willrt_os_cond_timedwait(&state.cond, &state.lock, wait);
            if (!state.ready && willrt_chan_canceled()) {
                state.ready = 1;
                state.index = WILLRT_CANCELED;
                state.status = WILLRT_CANCELED;
            }
        }
        result = state.index;
        status = state.status;
        willrt_os_mutex_unlock(&state.lock);

    select_done:
        for (i = 0; i < n; i++) {
            willrt_chan_wait* wait = &waits[i];
            willrt_chan* chan;

            if (wait->select == NULL) {
                continue;
            }
            chan = arms[i].chan;
            if (chan == NULL) {
                continue;
            }
            willrt_os_mutex_lock(&chan->lock);
            if (wait->linked) {
                if (wait->kind == 0) {
                    willrt_chan_wait_unlink(&chan->recv_head, &chan->recv_tail,
                                            wait);
                } else {
                    willrt_chan_wait_unlink(&chan->send_head, &chan->send_tail,
                                            wait);
                }
                if (wait->kind == 0) {
                    atomic_fetch_sub(&chan->recv_waiters, 1);
                } else {
                    atomic_fetch_sub(&chan->send_waiters, 1);
                }
                wait->linked = 0;
            }
            willrt_os_mutex_unlock(&chan->lock);
        }
        willrt_os_mutex_destroy(&state.lock);
        willrt_os_cond_destroy(&state.cond);
        willrt_select_last = status;
        return result;
    }
}
