/* Monotonic clock and the one-shot timer wheel.
 *
 * The wheel has 1024 slots of one millisecond each, so one revolution spans
 * about 1.024 seconds. A slot holds a doubly-linked list in insertion order.
 * Insertion appends to the slot for deadline / 1 ms and is O(1); cancellation
 * unlinks through prev and next and is O(1). A deadline farther away than one
 * revolution waits in its slot and is checked again on each revolution; it
 * never fires early because expiry compares the real deadline.
 *
 * The run loop does not exist yet. willrt_timer_advance is the internal step
 * it will call with the current clock; the public interface stays
 * willrt_timer_new and willrt_timer_cancel. The test build gets a separate
 * seam, willrt_timer_test_advance, and production code cannot name it.
 *
 * Expiry takes the due nodes out of the wheel under the lock, then unlocks
 * before running callbacks. A callback may allocate, cancel another timer, or
 * call cancel on itself without deadlocking. Cancelling a node that advance
 * has already detached only marks it; advance owns the node until the callback
 * pass ends. Fired and cancelled nodes wait on a retired list and are freed at
 * the start of the next advance, so a cancel after a fire, or a second cancel,
 * is safe until that reclaim.
 *
 * One spin lock guards the slots, the immediate list, the retired list, the
 * sequence counter, the wheel cursor, and the willrt_alloc/willrt_free calls
 * for timer nodes. It is never held while a callback runs. The arena itself is
 * still single-threaded, so routing timer-node allocation through this lock
 * keeps timer users from racing each other; other callers of the arena remain
 * subject to the lane model.
 */

#include "timer.h"

#include <stdatomic.h>

enum {
    WILLRT_TIMER_SLOTS = 1024,
    WILLRT_TIMER_TICK_NS = 1000000,
    WILLRT_TIMER_IMMEDIATE = -1
};

enum {
    WILLRT_TIMER_ARMED = 0,
    WILLRT_TIMER_DUE = 1,
    WILLRT_TIMER_FIRED = 2,
    WILLRT_TIMER_CANCELLED = 3
};

struct willrt_timer {
    int64_t deadline;
    uint64_t seq;
    void* fn;
    void* arg;
    struct willrt_timer* prev;
    struct willrt_timer* next;
    struct willrt_timer* retire_next;
    _Atomic(uint32_t) state;
    int slot;
};

struct willrt_timer_bucket {
    struct willrt_timer* head;
    struct willrt_timer* tail;
};

static struct willrt_timer_bucket willrt_timer_buckets[WILLRT_TIMER_SLOTS];
static struct willrt_timer* willrt_timer_immediate_head;
static struct willrt_timer* willrt_timer_immediate_tail;
static struct willrt_timer* willrt_timer_retired_head;
static uint64_t willrt_timer_next_seq;
static uint64_t willrt_timer_last_tick;
static int willrt_timer_started;
static atomic_flag willrt_timer_lock = ATOMIC_FLAG_INIT;
static willrt_os_thread willrt_timer_thread;
static atomic_int willrt_timer_loop_state;
static atomic_int willrt_timer_loop_stop;

static void willrt_timer_lock_acquire(void) {
    while (atomic_flag_test_and_set_explicit(&willrt_timer_lock,
                                             memory_order_acquire)) {
    }
}

static void willrt_timer_lock_release(void) {
    atomic_flag_clear_explicit(&willrt_timer_lock, memory_order_release);
}

static void willrt_timer_link(struct willrt_timer** head,
                              struct willrt_timer** tail,
                              struct willrt_timer* timer) {
    timer->prev = *tail;
    timer->next = NULL;
    if (*tail != NULL) {
        (*tail)->next = timer;
    } else {
        *head = timer;
    }
    *tail = timer;
}

static void willrt_timer_unlink(struct willrt_timer** head,
                                struct willrt_timer** tail,
                                struct willrt_timer* timer) {
    if (timer->prev != NULL) {
        timer->prev->next = timer->next;
    } else {
        *head = timer->next;
    }
    if (timer->next != NULL) {
        timer->next->prev = timer->prev;
    } else {
        *tail = timer->prev;
    }
    timer->prev = NULL;
    timer->next = NULL;
}

static int willrt_timer_before(const struct willrt_timer* left,
                               const struct willrt_timer* right) {
    if (left->deadline != right->deadline) {
        return left->deadline < right->deadline;
    }
    return left->seq < right->seq;
}

static struct willrt_timer* willrt_timer_merge(struct willrt_timer* left,
                                               struct willrt_timer* right) {
    struct willrt_timer head;
    struct willrt_timer* tail = &head;

    head.next = NULL;
    while (left != NULL && right != NULL) {
        if (willrt_timer_before(left, right)) {
            tail->next = left;
            left = left->next;
        } else {
            tail->next = right;
            right = right->next;
        }
        tail = tail->next;
    }
    tail->next = left != NULL ? left : right;
    return head.next;
}

/* Sorts a detached list by deadline and then by insertion order. The list is
   private to one advance call, so no lock is held. */
static struct willrt_timer* willrt_timer_sort(struct willrt_timer* list) {
    struct willrt_timer* slow;
    struct willrt_timer* fast;
    struct willrt_timer* right;

    if (list == NULL || list->next == NULL) {
        return list;
    }
    slow = list;
    fast = list->next;
    while (fast != NULL && fast->next != NULL) {
        slow = slow->next;
        fast = fast->next->next;
    }
    right = slow->next;
    slow->next = NULL;
    return willrt_timer_merge(willrt_timer_sort(list),
                              willrt_timer_sort(right));
}

/* Moves every node whose deadline has passed from one slot to the due list.
   Nodes that are not due stay in the slot for the next scan. */
static void willrt_timer_take_due(struct willrt_timer_bucket* bucket,
                                  int64_t now_ns,
                                  struct willrt_timer** due_head,
                                  struct willrt_timer** due_tail) {
    struct willrt_timer* timer = bucket->head;

    while (timer != NULL) {
        struct willrt_timer* next = timer->next;

        if (timer->deadline <= now_ns) {
            willrt_timer_unlink(&bucket->head, &bucket->tail, timer);
            atomic_store_explicit(&timer->state, WILLRT_TIMER_DUE,
                                  memory_order_release);
            willrt_timer_link(due_head, due_tail, timer);
        }
        timer = next;
    }
}

int64_t willrt_now_ns(void) {
    return willrt_os_now_ns();
}

#ifndef WILLRT_TIMER_TEST
static void willrt_timer_run(void* ignored) {
    (void)ignored;
    while (!atomic_load(&willrt_timer_loop_stop)) {
        willrt_timer_advance(willrt_now_ns());
        willrt_os_sleep_ns(INT64_C(1000000));
    }
}
#endif

static void willrt_timer_loop_start(void) {
#ifdef WILLRT_TIMER_TEST
    return;
#else
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_timer_loop_state, &expected, 1)) {
        atomic_store(&willrt_timer_loop_stop, 0);
        if (willrt_os_thread_start(&willrt_timer_thread, willrt_timer_run,
                                   NULL) != 0) {
            atomic_store(&willrt_timer_loop_state, 0);
            return;
        }
        atomic_store(&willrt_timer_loop_state, 2);
    }
#endif
}

willrt_timer* willrt_timer_new(int64_t deadline_ns, void* fn, void* arg) {
    struct willrt_timer* timer;
    struct willrt_timer_bucket* bucket;
    int64_t now_ns;
    uint64_t tick;
    int slot;

    willrt_timer_lock_acquire();
    timer = (struct willrt_timer*)willrt_alloc(sizeof(*timer));
    if (timer == NULL) {
        willrt_timer_lock_release();
        return NULL;
    }
    timer->deadline = deadline_ns;
    timer->seq = willrt_timer_next_seq++;
    timer->fn = fn;
    timer->arg = arg;
    timer->prev = NULL;
    timer->next = NULL;
    timer->retire_next = NULL;
    atomic_init(&timer->state, WILLRT_TIMER_ARMED);
    now_ns = willrt_now_ns();
    if (deadline_ns <= now_ns) {
        timer->slot = WILLRT_TIMER_IMMEDIATE;
        willrt_timer_link(&willrt_timer_immediate_head,
                          &willrt_timer_immediate_tail, timer);
    } else {
        tick = (uint64_t)deadline_ns / WILLRT_TIMER_TICK_NS;
        if (willrt_timer_started && tick < willrt_timer_last_tick) {
            tick = willrt_timer_last_tick;
        }
        slot = (int)(tick & (WILLRT_TIMER_SLOTS - 1));
        timer->slot = slot;
        bucket = &willrt_timer_buckets[slot];
        willrt_timer_link(&bucket->head, &bucket->tail, timer);
    }
    willrt_timer_lock_release();
    willrt_timer_loop_start();
    return timer;
}

void willrt_timer_cancel(willrt_timer* timer) {
    struct willrt_timer_bucket* bucket;
    uint32_t state;

    if (timer == NULL) {
        return;
    }
    willrt_timer_lock_acquire();
    state = atomic_load_explicit(&timer->state, memory_order_acquire);
    if (state == WILLRT_TIMER_ARMED) {
        if (timer->slot == WILLRT_TIMER_IMMEDIATE) {
            willrt_timer_unlink(&willrt_timer_immediate_head,
                                &willrt_timer_immediate_tail, timer);
        } else {
            bucket = &willrt_timer_buckets[timer->slot];
            willrt_timer_unlink(&bucket->head, &bucket->tail, timer);
        }
        atomic_store_explicit(&timer->state, WILLRT_TIMER_CANCELLED,
                              memory_order_release);
        timer->retire_next = willrt_timer_retired_head;
        willrt_timer_retired_head = timer;
    } else if (state == WILLRT_TIMER_DUE) {
        /* The advance call that detached the node owns it. Marking it here
           makes that call skip the callback and reclaim the node itself. */
        atomic_store_explicit(&timer->state, WILLRT_TIMER_CANCELLED,
                              memory_order_release);
    }
    willrt_timer_lock_release();
}

/* Internal expiry step for the future run loop. It runs callbacks in deadline
   order, with insertion order breaking ties, and returns how many callbacks
   ran. The public header does not name it. */
size_t willrt_timer_advance(int64_t now_ns) {
    struct willrt_timer* due_head = NULL;
    struct willrt_timer* due_tail = NULL;
    struct willrt_timer* processed = NULL;
    struct willrt_timer* timer;
    struct willrt_timer* retired;
    int64_t now;
    uint64_t now_tick;
    size_t fired = 0;
    int slot;

    if (now_ns > 0) {
        now = now_ns;
    } else {
        now = 0;
    }
    now_tick = (uint64_t)now / WILLRT_TIMER_TICK_NS;

    willrt_timer_lock_acquire();

    /* Nodes fired or cancelled before this call are no longer reachable from
       any user contract, so this is the point where their storage returns. */
    retired = willrt_timer_retired_head;
    willrt_timer_retired_head = NULL;
    while (retired != NULL) {
        timer = retired;
        retired = timer->retire_next;
        willrt_free(timer);
    }

    /* A deadline that had already passed when the timer was created never
       enters the wheel; the next advance drains it. */
    while (willrt_timer_immediate_head != NULL) {
        timer = willrt_timer_immediate_head;
        willrt_timer_unlink(&willrt_timer_immediate_head,
                            &willrt_timer_immediate_tail, timer);
        atomic_store_explicit(&timer->state, WILLRT_TIMER_DUE,
                              memory_order_release);
        willrt_timer_link(&due_head, &due_tail, timer);
    }

    if (!willrt_timer_started) {
        for (slot = 0; slot < WILLRT_TIMER_SLOTS; slot++) {
            willrt_timer_take_due(&willrt_timer_buckets[slot], now,
                                  &due_head, &due_tail);
        }
        willrt_timer_started = 1;
        willrt_timer_last_tick = now_tick + 1;
    } else if (now_tick >= willrt_timer_last_tick) {
        uint64_t span = now_tick - willrt_timer_last_tick + 1;

        if (span >= WILLRT_TIMER_SLOTS) {
            for (slot = 0; slot < WILLRT_TIMER_SLOTS; slot++) {
                willrt_timer_take_due(&willrt_timer_buckets[slot], now,
                                      &due_head, &due_tail);
            }
        } else {
            uint64_t tick = willrt_timer_last_tick > 0
                                ? willrt_timer_last_tick - 1
                                : 0;

            for (; tick <= now_tick; tick++) {
                slot = (int)(tick & (WILLRT_TIMER_SLOTS - 1));
                willrt_timer_take_due(&willrt_timer_buckets[slot], now,
                                      &due_head, &due_tail);
            }
        }
        willrt_timer_last_tick = now_tick + 1;
    } else {
        /* The clock did not reach the next tick, but a deadline inside the
           current tick may have passed since the previous call. */
        slot = (int)(now_tick & (WILLRT_TIMER_SLOTS - 1));
        willrt_timer_take_due(&willrt_timer_buckets[slot], now,
                              &due_head, &due_tail);
    }
    willrt_timer_lock_release();

    due_head = willrt_timer_sort(due_head);
    while (due_head != NULL) {
        timer = due_head;
        due_head = timer->next;
        timer->next = NULL;
        if (atomic_load_explicit(&timer->state, memory_order_acquire)
            != WILLRT_TIMER_CANCELLED) {
            if (timer->fn != NULL) {
                ((void (*)(void*))timer->fn)(timer->arg);
            }
            atomic_store_explicit(&timer->state, WILLRT_TIMER_FIRED,
                                  memory_order_release);
            fired++;
        }
        timer->prev = NULL;
        timer->retire_next = processed;
        processed = timer;
    }

    willrt_timer_lock_acquire();
    while (processed != NULL) {
        timer = processed;
        processed = timer->retire_next;
        timer->retire_next = willrt_timer_retired_head;
        willrt_timer_retired_head = timer;
    }
    willrt_timer_lock_release();
    return fired;
}

void willrt_timer_shutdown(void) {
    if (atomic_load(&willrt_timer_loop_state) != 2) {
        return;
    }
    atomic_store(&willrt_timer_loop_stop, 1);
    willrt_os_thread_join(&willrt_timer_thread);
    atomic_store(&willrt_timer_loop_state, 0);
}

#ifdef WILLRT_TIMER_TEST
size_t willrt_timer_test_advance(int64_t now_ns) {
    return willrt_timer_advance(now_ns);
}
#endif
