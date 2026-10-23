/* One poller thread for ready-event delivery.
 *
 * The thread serves two clients. A persistent registration is one descriptor,
 * one event mask, and one callback; the callback runs outside the lock and may
 * add, delete, or replace registrations for the next pass. A poll request is
 * the task-embedded wait of std/io.Poller.AsyncNext: the task parks with its
 * request linked into a pending list, the poller copies the descriptor set
 * into its own buffer, waits once for the whole batch, and appends ready tags
 * straight into the waiting task's out array before waking the task's lane.
 * One pass therefore costs one kernel wait for every waiter, and a delivery
 * takes each lane lock once for all the tasks it completes.
 *
 * A request is one-shot. The deadline lives on the request and the task arms
 * its own park timer for it, so either side can finish the wait: the poller
 * through a delivered state change, the task by retiring the request when its
 * deadline passed. The generation counter rejects a delivery computed from a
 * snapshot of a request that was retired and reused in between.
 *
 * Windows polls sockets with WSAPoll. A loopback UDP pair is the wake channel;
 * a byte sent to it makes the poll return without a timeout. Other files and
 * handles are not registered on Windows, because WSAPoll does not accept them.
 * POSIX polls descriptors with poll() and uses a pipe as the wake channel.
 *
 * Platform downgrade, per the concurrency spec's completion-form rule: the
 * poller serves pollable descriptors only. Regular files, directory handles,
 * and child processes are not pollable with the primitives this platform
 * layer carries (no IOCP, no io_uring, no pidfd), so the async forms of
 * std/os/fs and std/os/exec run their OS call on the blocking pool as an
 * uncancelable C call, park the task, and wake the owner directly. When the
 * platform layer gains a completion interface, those moves onto the poller.
 *
 * The registration table is fixed at WILLRT_POLL_MAX. A full table returns
 * WILLRT_FULL; the caller decides whether to wait or fail. The table is not a
 * general file descriptor registry: each descriptor appears once, and a second
 * add for the same descriptor replaces the first.
 */

#include "../../../include/willrt.h"

#include <stdatomic.h>
#include <stddef.h>
#include <string.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET willrt_poll_fd;
typedef WSAPOLLFD willrt_poll_fdstruct;
#define WILLRT_POLL_INPUT POLLRDNORM
#define WILLRT_POLL_OUTPUT POLLWRNORM
#define WILLRT_POLL_ERROR (POLLERR | POLLHUP | POLLNVAL)
#else
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <unistd.h>
typedef int willrt_poll_fd;
typedef struct pollfd willrt_poll_fdstruct;
#define WILLRT_POLL_INPUT POLLIN
#define WILLRT_POLL_OUTPUT POLLOUT
#define WILLRT_POLL_ERROR (POLLERR | POLLHUP | POLLNVAL)
#endif

enum { WILLRT_POLL_MAX = 256 };

struct willrt_poll_entry {
    int32_t used;
    int64_t fd;
    uint32_t events;
    willrt_poll_fn cb;
    void* arg;
};

static struct willrt_poll_entry willrt_poll_entries[WILLRT_POLL_MAX];
static willrt_os_mutex willrt_poll_lock;
static willrt_os_thread willrt_poll_thread;
static atomic_int willrt_poll_state;
static atomic_int willrt_poll_stop;

#ifdef _WIN32
static SOCKET willrt_poll_wake_recv = INVALID_SOCKET;
static SOCKET willrt_poll_wake_send = INVALID_SOCKET;
#else
static int willrt_poll_wake_recv = -1;
static int willrt_poll_wake_send = -1;
#endif

static void willrt_poll_wake(void) {
#ifdef _WIN32
    char byte = 1;
    if (willrt_poll_wake_send != INVALID_SOCKET) {
        (void)send(willrt_poll_wake_send, &byte, 1, 0);
    }
#else
    char byte = 1;
    if (willrt_poll_wake_send >= 0) {
        ssize_t ignored = write(willrt_poll_wake_send, &byte, 1);
        (void)ignored;
    }
#endif
}

static void willrt_poll_drain(void) {
#ifdef _WIN32
    char buffer[64];
    while (recv(willrt_poll_wake_recv, buffer, (int)sizeof(buffer), 0) > 0) {
    }
#else
    char buffer[64];
    while (read(willrt_poll_wake_recv, buffer, sizeof(buffer)) > 0) {
    }
#endif
}

static int willrt_poll_wake_open(void) {
#ifdef _WIN32
    struct sockaddr_in addr;
    int length = (int)sizeof(addr);
    unsigned long nonblocking = 1;

    willrt_poll_wake_recv = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (willrt_poll_wake_recv == INVALID_SOCKET) {
        return -1;
    }
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = 0;
    if (bind(willrt_poll_wake_recv, (const struct sockaddr*)&addr,
             sizeof(addr)) != 0
        || getsockname(willrt_poll_wake_recv, (struct sockaddr*)&addr,
                       &length) != 0) {
        closesocket(willrt_poll_wake_recv);
        willrt_poll_wake_recv = INVALID_SOCKET;
        return -1;
    }
    willrt_poll_wake_send = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (willrt_poll_wake_send == INVALID_SOCKET
        || connect(willrt_poll_wake_send, (const struct sockaddr*)&addr,
                   length) != 0) {
        closesocket(willrt_poll_wake_recv);
        willrt_poll_wake_recv = INVALID_SOCKET;
        if (willrt_poll_wake_send != INVALID_SOCKET) {
            closesocket(willrt_poll_wake_send);
            willrt_poll_wake_send = INVALID_SOCKET;
        }
        return -1;
    }
    (void)ioctlsocket(willrt_poll_wake_recv, FIONBIO, &nonblocking);
    return 0;
#else
    int fds[2];

    if (pipe(fds) != 0) {
        return -1;
    }
    willrt_poll_wake_recv = fds[0];
    willrt_poll_wake_send = fds[1];
    (void)fcntl(willrt_poll_wake_recv, F_SETFL,
                fcntl(willrt_poll_wake_recv, F_GETFL, 0) | O_NONBLOCK);
    (void)fcntl(willrt_poll_wake_send, F_SETFL,
                fcntl(willrt_poll_wake_send, F_GETFL, 0) | O_NONBLOCK);
    return 0;
#endif
}

static void willrt_poll_wake_close(void) {
#ifdef _WIN32
    if (willrt_poll_wake_recv != INVALID_SOCKET) {
        closesocket(willrt_poll_wake_recv);
        willrt_poll_wake_recv = INVALID_SOCKET;
    }
    if (willrt_poll_wake_send != INVALID_SOCKET) {
        closesocket(willrt_poll_wake_send);
        willrt_poll_wake_send = INVALID_SOCKET;
    }
#else
    if (willrt_poll_wake_recv >= 0) {
        close(willrt_poll_wake_recv);
        willrt_poll_wake_recv = -1;
    }
    if (willrt_poll_wake_send >= 0) {
        close(willrt_poll_wake_send);
        willrt_poll_wake_send = -1;
    }
#endif
}

static uint32_t willrt_poll_events(short revents) {
    uint32_t events = 0;

    if ((revents & WILLRT_POLL_INPUT) != 0) {
        events |= WILLRT_POLL_IN;
    }
    if ((revents & WILLRT_POLL_OUTPUT) != 0) {
        events |= WILLRT_POLL_OUT;
    }
    if ((revents & WILLRT_POLL_ERROR) != 0) {
        events |= WILLRT_POLL_ERR;
    }
    return events;
}

static short willrt_poll_native_events(uint32_t events) {
    short native = 0;

    if ((events & WILLRT_POLL_IN) != 0) {
        native = (short)(native | WILLRT_POLL_INPUT);
    }
    if ((events & WILLRT_POLL_OUT) != 0) {
        native = (short)(native | WILLRT_POLL_OUTPUT);
    }
    return native;
}

/* The request batch lives in poller-owned buffers that grow on demand and are
   retained between passes, so a steady-state wait copies into memory that is
   already there. */
typedef struct willrt_poll_req_seg {
    willrt_poll_request* req;
    uint64_t gen;
    int64_t count;
    size_t base;
} willrt_poll_req_seg;

static willrt_poll_request* willrt_poll_requests;
static willrt_poll_fdstruct* willrt_poll_batch;
static willrt_poll_req_seg* willrt_poll_batch_segs;
static void** willrt_poll_batch_waiters;
static int64_t willrt_poll_batch_cap;
static int64_t willrt_poll_batch_seg_cap;
static int64_t willrt_poll_batch_waiter_cap;
static atomic_int willrt_poll_lock_state;

static void willrt_poll_request_unlink(willrt_poll_request* req);

/* willrt_poll_lock_init readies the poller lock on its own, so a request that
   completes without the poller thread never pays for the thread. */
static void willrt_poll_lock_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_poll_lock_state, &expected, 1)) {
        willrt_os_mutex_init(&willrt_poll_lock);
        atomic_store(&willrt_poll_lock_state, 2);
        return;
    }
    while (atomic_load(&willrt_poll_lock_state) == 1) {
        willrt_os_thread_yield();
    }
}

/* The out array mirrors the will Array<usize> layout: buffer, length,
   capacity. Delivery appends ready tags straight into the waiting task's
   array. */
typedef struct willrt_poll_wait_array {
    void* data;
    int64_t len;
    int64_t cap;
} willrt_poll_wait_array;

static int32_t willrt_poll_wait_append(willrt_poll_wait_array* out,
                                       int64_t value) {
    if (out->len >= out->cap) {
        int64_t next = out->cap == 0 ? 4 : out->cap * 2;
        int64_t* data = (int64_t*)willrt_alloc(sizeof(int64_t) * (size_t)next);
        if (data == NULL) {
            return 1;
        }
        if (out->data != NULL && out->len != 0) {
            memcpy(data, out->data, sizeof(int64_t) * (size_t)out->len);
            willrt_free(out->data);
        }
        out->data = data;
        out->cap = next;
    }
    ((int64_t*)out->data)[out->len++] = value;
    return 0;
}

/* willrt_poll_batch_grow grows the poller-owned batch buffers. It runs under
   the poller lock and only ever allocates, so the lock order stays poller
   lock then allocator lock. */
static int32_t willrt_poll_batch_grow(int64_t fds, int64_t segs,
                                      int64_t waiters) {
    if (fds > willrt_poll_batch_cap) {
        willrt_poll_fdstruct* next =
            (willrt_poll_fdstruct*)willrt_alloc(sizeof(*next) * (size_t)fds);
        if (next == NULL) {
            return 1;
        }
        if (willrt_poll_batch != NULL) {
            memcpy(next, willrt_poll_batch,
                   sizeof(*next) * (size_t)willrt_poll_batch_cap);
            willrt_free(willrt_poll_batch);
        }
        willrt_poll_batch = next;
        willrt_poll_batch_cap = fds;
    }
    if (segs > willrt_poll_batch_seg_cap) {
        willrt_poll_req_seg* next =
            (willrt_poll_req_seg*)willrt_alloc(sizeof(*next) * (size_t)segs);
        if (next == NULL) {
            return 1;
        }
        if (willrt_poll_batch_segs != NULL) {
            memcpy(next, willrt_poll_batch_segs,
                   sizeof(*next) * (size_t)willrt_poll_batch_seg_cap);
            willrt_free(willrt_poll_batch_segs);
        }
        willrt_poll_batch_segs = next;
        willrt_poll_batch_seg_cap = segs;
    }
    if (waiters > willrt_poll_batch_waiter_cap) {
        void** next = (void**)willrt_alloc(sizeof(*next) * (size_t)waiters);
        if (next == NULL) {
            return 1;
        }
        if (willrt_poll_batch_waiters != NULL) {
            memcpy(next, willrt_poll_batch_waiters,
                   sizeof(*next) * (size_t)willrt_poll_batch_waiter_cap);
            willrt_free(willrt_poll_batch_waiters);
        }
        willrt_poll_batch_waiters = next;
        willrt_poll_batch_waiter_cap = waiters;
    }
    return 0;
}

static void willrt_poll_main(void* ignored) {
    struct willrt_poll_entry snapshot[WILLRT_POLL_MAX];
    uint32_t masks[WILLRT_POLL_MAX];

    (void)ignored;
    willrt_mem_set_lane(0);
    (void)willrt_poll_batch_grow(64, 8, 8);
    while (atomic_load(&willrt_poll_stop) == 0) {
        size_t count = 0;
        size_t live = 0;
        int64_t seg_count = 0;
        int64_t delivered = 0;
        int64_t i;
        int64_t s;
        int ready;

        willrt_os_mutex_lock(&willrt_poll_lock);
        for (i = 0; i < WILLRT_POLL_MAX; i++) {
            if (!willrt_poll_entries[i].used) {
                continue;
            }
            snapshot[live] = willrt_poll_entries[i];
            masks[live] = willrt_poll_entries[i].events;
            live++;
        }
        {
            int64_t needed = (int64_t)live + 1;

            for (willrt_poll_request* req = willrt_poll_requests; req != NULL;
                 req = req->next) {
                if (req->state == WILLRT_POLL_REQUEST_PENDING
                    && req->count > 0) {
                    needed += req->count;
                    seg_count++;
                }
            }
            if (willrt_poll_batch_grow(needed, seg_count, seg_count) != 0) {
                willrt_os_mutex_unlock(&willrt_poll_lock);
                willrt_os_sleep_ns(1000000);
                continue;
            }
            for (i = 0; i < (int64_t)live; i++) {
                willrt_poll_batch[count].fd = (willrt_poll_fd)snapshot[i].fd;
                willrt_poll_batch[count].events =
                    willrt_poll_native_events(masks[i]);
                willrt_poll_batch[count].revents = 0;
                count++;
            }
            s = 0;
            for (willrt_poll_request* req = willrt_poll_requests; req != NULL;
                 req = req->next) {
                int64_t j;

                if (req->state != WILLRT_POLL_REQUEST_PENDING
                    || req->count <= 0) {
                    continue;
                }
                willrt_poll_batch_segs[s].req = req;
                willrt_poll_batch_segs[s].gen = req->gen;
                willrt_poll_batch_segs[s].count = req->count;
                willrt_poll_batch_segs[s].base = count;
                s++;
                for (j = 0; j < req->count; j++) {
                    willrt_poll_batch[count].fd =
                        (willrt_poll_fd)((const int64_t*)req->fds)[j];
                    willrt_poll_batch[count].events = willrt_poll_native_events(
                        ((const uint32_t*)req->events)[j]);
                    willrt_poll_batch[count].revents = 0;
                    count++;
                }
            }
        }
        willrt_os_mutex_unlock(&willrt_poll_lock);

        willrt_poll_batch[count].fd = (willrt_poll_fd)willrt_poll_wake_recv;
        willrt_poll_batch[count].events = WILLRT_POLL_INPUT;
        willrt_poll_batch[count].revents = 0;
        count++;

#ifdef _WIN32
        ready = WSAPoll(willrt_poll_batch, (ULONG)count, 100);
#else
        ready = poll(willrt_poll_batch, (nfds_t)count, 100);
#endif
        if (atomic_load(&willrt_poll_stop) != 0) {
            break;
        }
        if (ready > 0) {
            /* A request is delivered under the lock while the state check
               still holds: the waiting task cannot resume, so its out array
               and users table stay valid. A stale generation means the task
               retired and reused the node; the readiness is dropped. */
            willrt_os_mutex_lock(&willrt_poll_lock);
            for (s = 0; s < seg_count; s++) {
                willrt_poll_request* req = willrt_poll_batch_segs[s].req;
                int64_t j;

                if (req->gen != willrt_poll_batch_segs[s].gen
                    || req->state != WILLRT_POLL_REQUEST_PENDING
                    || !req->linked) {
                    continue;
                }
                req->state = WILLRT_POLL_REQUEST_DONE;
                req->status = WILLRT_OK;
                req->linked = 0;
                willrt_poll_request_unlink(req);
                for (j = 0; j < willrt_poll_batch_segs[s].count; j++) {
                    if (willrt_poll_batch[willrt_poll_batch_segs[s].base + j]
                            .revents
                        == 0) {
                        continue;
                    }
                    if (willrt_poll_wait_append(
                            (willrt_poll_wait_array*)req->out,
                            ((const int64_t*)req->users)[j])
                        != 0) {
                        req->status = WILLRT_FAILED;
                        break;
                    }
                }
                willrt_poll_batch_waiters[delivered++] = req->waiter;
            }
            willrt_os_mutex_unlock(&willrt_poll_lock);
            if (delivered > 0) {
                willrt_task_wake_batch(willrt_poll_batch_waiters, delivered);
            }
            for (i = 0; i < (int64_t)live; i++) {
                uint32_t events = willrt_poll_events(willrt_poll_batch[i].revents);

                if (events == 0 || snapshot[i].cb == NULL) {
                    continue;
                }
                snapshot[i].cb(snapshot[i].fd,
                               events & (masks[i] | WILLRT_POLL_ERR),
                               snapshot[i].arg);
            }
            if ((willrt_poll_batch[count - 1].revents & WILLRT_POLL_INPUT)
                != 0) {
                willrt_poll_drain();
            }
        }
    }
    willrt_mem_thread_flush();
}

void willrt_poll_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_poll_state, &expected, 1)) {
#ifdef _WIN32
        {
            WSADATA data;
            if (WSAStartup(MAKEWORD(2, 2), &data) != 0) {
                atomic_store(&willrt_poll_state, 0);
                return;
            }
        }
#endif
        if (willrt_poll_wake_open() != 0) {
#ifdef _WIN32
            (void)WSACleanup();
#endif
            atomic_store(&willrt_poll_state, 0);
            return;
        }
        memset(willrt_poll_entries, 0, sizeof(willrt_poll_entries));
        willrt_poll_lock_init();
        atomic_store(&willrt_poll_stop, 0);
        willrt_rc_set_threaded();
        willrt_mem_set_shared();
        if (willrt_os_thread_start(&willrt_poll_thread, willrt_poll_main, NULL)
            != 0) {
            willrt_poll_wake_close();
            willrt_os_mutex_destroy(&willrt_poll_lock);
            atomic_store(&willrt_poll_lock_state, 0);
#ifdef _WIN32
            (void)WSACleanup();
#endif
            atomic_store(&willrt_poll_state, 0);
            return;
        }
        atomic_store(&willrt_poll_state, 2);
        return;
    }
    while (atomic_load(&willrt_poll_state) == 1) {
        willrt_os_thread_yield();
    }
}

/* willrt_poll_wait performs one blocking readiness check over parallel arrays
   of descriptors, event masks, and user tags. It is the synchronous form used
   by std/io.Poller.Next and by a zero-timeout async request. Ready tags are
   appended in input order. */
int32_t willrt_poll_wait(const void* fds_raw, const void* events_raw,
                         const void* users_raw, int64_t count,
                         int64_t timeout_ns, void* out_raw) {
    const int64_t* fds = (const int64_t*)fds_raw;
    const uint32_t* events = (const uint32_t*)events_raw;
    const int64_t* users = (const int64_t*)users_raw;
    willrt_poll_wait_array* out = (willrt_poll_wait_array*)out_raw;
    willrt_poll_fdstruct* native;
    int32_t timeout_ms;
    int ready;
    int64_t i;

    if (out == NULL || count < 0 || (count != 0 && (fds == NULL || events == NULL || users == NULL))) {
        return WILLRT_FAILED;
    }
    if (timeout_ns < 0) {
        timeout_ms = -1;
    } else if (timeout_ns == 0) {
        timeout_ms = 0;
    } else {
        timeout_ms = (int32_t)((timeout_ns + 999999) / 1000000);
    }
    if (count == 0) {
        if (timeout_ns > 0) {
            willrt_os_sleep_ns(timeout_ns);
        }
        return WILLRT_OK;
    }
    native = (willrt_poll_fdstruct*)willrt_alloc(
        sizeof(*native) * (size_t)count);
    if (native == NULL) {
        return WILLRT_FAILED;
    }
    for (i = 0; i < count; i++) {
        native[i].fd = (willrt_poll_fd)fds[i];
        native[i].events = willrt_poll_native_events(events[i]);
        native[i].revents = 0;
    }
#ifdef _WIN32
    ready = WSAPoll(native, (ULONG)count, timeout_ms);
#else
    ready = poll(native, (nfds_t)count, timeout_ms);
#endif
    if (ready < 0) {
        willrt_free(native);
        return WILLRT_FAILED;
    }
    if (ready > 0) {
        for (i = 0; i < count; i++) {
            if (native[i].revents == 0) {
                continue;
            }
            if (willrt_poll_wait_append(out, users[i]) != 0) {
                willrt_free(native);
                return WILLRT_FAILED;
            }
        }
    }
    willrt_free(native);
    return WILLRT_OK;
}

/* willrt_poll_wait_async is the machine-declined fallback: a call the async
   lowering does not cover yet lowers to it and blocks the calling thread for
   the scan, the same contract the synchronous form keeps. */
int32_t willrt_poll_wait_async(const void* fds, const void* events,
                               const void* users, int64_t count,
                               int64_t timeout_ns, void* out) {
    return willrt_poll_wait(fds, events, users, count, timeout_ns, out);
}

/* The async request waits through the poller thread instead of a worker: the
   task parks, the poller delivers, nothing is allocated for the wait itself.
   A caller outside a task shares one detached node and runs synchronously;
   generated async code always runs inside a task. */
static willrt_poll_request willrt_poll_detached;

static void willrt_poll_request_unlink(willrt_poll_request* req) {
    willrt_poll_request** link = &willrt_poll_requests;

    while (*link != NULL) {
        if (*link == req) {
            *link = req->next;
            break;
        }
        link = &(*link)->next;
    }
    req->next = NULL;
}

willrt_poll_request* willrt_poll_wait_async_start(const void* fds,
                                                  const void* events,
                                                  const void* users,
                                                  int64_t count,
                                                  int64_t timeout_ns,
                                                  void* out) {
    willrt_task* task = willrt_task_current();
    willrt_poll_request* req = task == NULL ? &willrt_poll_detached
                                            : willrt_task_poll_node();

    if (req->state != WILLRT_POLL_REQUEST_IDLE) {
        willrt_panic("poll request already active", "poll.c",
                     "will.io.poller.request");
        return NULL;
    }
    req->fds = fds;
    req->events = events;
    req->users = users;
    req->count = count;
    req->out = out;
    req->waiter = task;
    req->linked = 0;
    req->next = NULL;
    req->status = WILLRT_OK;
    if (out == NULL || count < 0
        || (count != 0 && (fds == NULL || events == NULL || users == NULL))) {
        req->state = WILLRT_POLL_REQUEST_DONE;
        req->deadline = 0;
        req->status = WILLRT_FAILED;
        return req;
    }
    if (count == 0 && timeout_ns <= 0) {
        req->state = WILLRT_POLL_REQUEST_DONE;
        req->deadline = 0;
        return req;
    }
    if (timeout_ns == 0) {
        /* A zero timeout is one non-blocking scan, the same contract the
           synchronous form keeps; it is a check, not a wait. */
        req->state = WILLRT_POLL_REQUEST_DONE;
        req->deadline = 0;
        req->status = willrt_poll_wait(fds, events, users, count, 0, out);
        return req;
    }
    req->deadline = timeout_ns < 0 ? 0 : willrt_now_ns() + timeout_ns;
    req->gen += 1;
    if (count > 0) {
        willrt_poll_init();
        if (atomic_load(&willrt_poll_state) != 2) {
            req->state = WILLRT_POLL_REQUEST_DONE;
            req->deadline = 0;
            req->status = WILLRT_FAILED;
            return req;
        }
        willrt_os_mutex_lock(&willrt_poll_lock);
        req->state = WILLRT_POLL_REQUEST_PENDING;
        req->linked = 1;
        req->next = willrt_poll_requests;
        willrt_poll_requests = req;
        willrt_os_mutex_unlock(&willrt_poll_lock);
        willrt_poll_wake();
    } else {
        /* Nothing to scan: the request completes through the park timer at
           the deadline, so it never joins the pending list. */
        req->state = WILLRT_POLL_REQUEST_PENDING;
    }
    return req;
}

int32_t willrt_poll_wait_async_poll(willrt_poll_request* req) {
    int32_t done = 0;

    if (req == NULL) {
        return 1;
    }
    /* A zero-timeout request completes at start without ever starting the
       poller, but poll and finish still take the poller lock. */
    willrt_poll_lock_init();
    willrt_os_mutex_lock(&willrt_poll_lock);
    if (req->state == WILLRT_POLL_REQUEST_DONE) {
        willrt_os_mutex_unlock(&willrt_poll_lock);
        return 1;
    }
    if (req->state != WILLRT_POLL_REQUEST_PENDING) {
        willrt_os_mutex_unlock(&willrt_poll_lock);
        willrt_panic("poll request is not pending", "poll.c",
                     "will.io.poller.request");
        return 1;
    }
    {
        int64_t now = willrt_now_ns();
        int64_t deadline = req->deadline;
        int32_t timed_out = 0;
        int64_t scope = willrt_task_deadline();

        /* The scoped deadline wins over the request deadline: once the scope
           has expired the wait must report the timeout, whatever the request
           would have decided in the same instant. */
        if (scope != 0 && now >= scope) {
            done = 1;
            timed_out = 1;
        } else if (deadline == 0 || now >= deadline) {
            done = 1;
        }
        if (done) {
            req->state = WILLRT_POLL_REQUEST_DONE;
            req->status = timed_out ? WILLRT_TIMEDOUT : WILLRT_OK;
            if (req->linked) {
                req->linked = 0;
                willrt_poll_request_unlink(req);
            }
        }
    }
    willrt_os_mutex_unlock(&willrt_poll_lock);
    return done;
}

int32_t willrt_poll_wait_async_finish(willrt_poll_request* req) {
    int32_t status;

    if (req == NULL) {
        return WILLRT_FAILED;
    }
    willrt_poll_lock_init();
    willrt_os_mutex_lock(&willrt_poll_lock);
    if (req->state != WILLRT_POLL_REQUEST_DONE) {
        willrt_os_mutex_unlock(&willrt_poll_lock);
        return WILLRT_SUSPEND;
    }
    status = req->status;
    req->state = WILLRT_POLL_REQUEST_IDLE;
    req->linked = 0;
    req->deadline = 0;
    req->fds = NULL;
    req->events = NULL;
    req->users = NULL;
    req->out = NULL;
    req->count = 0;
    req->waiter = NULL;
    willrt_os_mutex_unlock(&willrt_poll_lock);
    return status;
}

int64_t willrt_poll_wait_async_deadline(willrt_poll_request* req) {
    return req == NULL ? 0 : req->deadline;
}

void willrt_poll_request_abandon(willrt_poll_request* req) {
    if (req == NULL || atomic_load(&willrt_poll_state) != 2) {
        return;
    }
    willrt_os_mutex_lock(&willrt_poll_lock);
    if (req->state == WILLRT_POLL_REQUEST_PENDING) {
        req->state = WILLRT_POLL_REQUEST_DONE;
        req->status = WILLRT_FAILED;
        if (req->linked) {
            req->linked = 0;
            willrt_poll_request_unlink(req);
        }
    }
    willrt_os_mutex_unlock(&willrt_poll_lock);
}

void willrt_poll_shutdown(void) {
    if (atomic_load(&willrt_poll_state) != 2) {
        return;
    }
    atomic_store(&willrt_poll_stop, 1);
    willrt_poll_wake();
    willrt_os_thread_join(&willrt_poll_thread);
    willrt_poll_wake_close();
    willrt_os_mutex_destroy(&willrt_poll_lock);
    atomic_store(&willrt_poll_lock_state, 0);
    memset(willrt_poll_entries, 0, sizeof(willrt_poll_entries));
    willrt_poll_requests = NULL;
#ifdef _WIN32
    (void)WSACleanup();
#endif
    atomic_store(&willrt_poll_state, 0);
}

int32_t willrt_poll_add(int64_t fd, uint32_t events, void* cb, void* arg) {
    int32_t result = WILLRT_FULL;
    int32_t free_slot = -1;
    int32_t i;

    willrt_poll_init();
    if (atomic_load(&willrt_poll_state) != 2) {
        return WILLRT_FAILED;
    }
    if (cb == NULL || events == 0 || fd < 0) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&willrt_poll_lock);
    for (i = 0; i < WILLRT_POLL_MAX; i++) {
        if (willrt_poll_entries[i].used
            && willrt_poll_entries[i].fd == fd) {
            willrt_poll_entries[i].events = events;
            willrt_poll_entries[i].cb = (willrt_poll_fn)cb;
            willrt_poll_entries[i].arg = arg;
            result = WILLRT_OK;
            break;
        }
        if (!willrt_poll_entries[i].used && free_slot < 0) {
            free_slot = i;
        }
    }
    if (result != WILLRT_OK && free_slot >= 0) {
        willrt_poll_entries[free_slot].used = 1;
        willrt_poll_entries[free_slot].fd = fd;
        willrt_poll_entries[free_slot].events = events;
        willrt_poll_entries[free_slot].cb = (willrt_poll_fn)cb;
        willrt_poll_entries[free_slot].arg = arg;
        result = WILLRT_OK;
    }
    willrt_os_mutex_unlock(&willrt_poll_lock);
    willrt_poll_wake();
    return result;
}

int32_t willrt_poll_del(int64_t fd) {
    int32_t found = WILLRT_EMPTY;
    int32_t i;

    if (atomic_load(&willrt_poll_state) != 2) {
        return WILLRT_EMPTY;
    }
    willrt_os_mutex_lock(&willrt_poll_lock);
    for (i = 0; i < WILLRT_POLL_MAX; i++) {
        if (willrt_poll_entries[i].used && willrt_poll_entries[i].fd == fd) {
            willrt_poll_entries[i].used = 0;
            willrt_poll_entries[i].cb = NULL;
            found = WILLRT_OK;
            break;
        }
    }
    willrt_os_mutex_unlock(&willrt_poll_lock);
    willrt_poll_wake();
    return found;
}
