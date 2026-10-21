/* One poller thread for ready-event delivery.
 *
 * A registration is one file descriptor, one event mask, and one callback. The
 * poller copies the live registrations into a private array, waits for any of
 * them, and calls the callback outside the lock. The callback may add, delete,
 * or replace a registration without deadlocking; the next pass sees the new
 * table. This is the portable fallback for the scheduler's direct delivery: the
 * task engine currently blocks inside a step, so a ready callback wakes its
 * waiter instead of resuming a state machine.
 *
 * Windows polls sockets with WSAPoll. A loopback UDP pair is the wake channel;
 * a byte sent to it makes the poll return without a timeout. Other files and
 * handles are not registered on Windows, because WSAPoll does not accept them.
 * POSIX polls descriptors with poll() and uses a pipe as the wake channel.
 *
 * The table is fixed at WILLRT_POLL_MAX. A full table returns WILLRT_FULL; the
 * caller decides whether to wait or fail. The table is not a general file
 * descriptor registry: each descriptor appears once, and a second add for the
 * same descriptor replaces the first.
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

static void willrt_poll_main(void* ignored) {
    willrt_poll_fdstruct fds[WILLRT_POLL_MAX + 1];
    struct willrt_poll_entry snapshot[WILLRT_POLL_MAX];
    uint32_t masks[WILLRT_POLL_MAX];

    (void)ignored;
    while (atomic_load(&willrt_poll_stop) == 0) {
        size_t count = 0;
        size_t live = 0;
        size_t i;
        int ready;

        willrt_os_mutex_lock(&willrt_poll_lock);
        for (i = 0; i < WILLRT_POLL_MAX; i++) {
            if (!willrt_poll_entries[i].used) {
                continue;
            }
            snapshot[live] = willrt_poll_entries[i];
            masks[live] = willrt_poll_entries[i].events;
            fds[count].fd = (willrt_poll_fd)snapshot[live].fd;
            fds[count].events = willrt_poll_native_events(snapshot[live].events);
            fds[count].revents = 0;
            live++;
            count++;
        }
        willrt_os_mutex_unlock(&willrt_poll_lock);

        fds[count].fd = (willrt_poll_fd)willrt_poll_wake_recv;
        fds[count].events = WILLRT_POLL_INPUT;
        fds[count].revents = 0;
        count++;

#ifdef _WIN32
        ready = WSAPoll(fds, (ULONG)count, 100);
#else
        ready = poll(fds, (nfds_t)count, 100);
#endif
        if (atomic_load(&willrt_poll_stop) != 0) {
            break;
        }
        if (ready <= 0) {
            continue;
        }
        if ((fds[live].revents & WILLRT_POLL_INPUT) != 0) {
            willrt_poll_drain();
        }
        for (i = 0; i < live; i++) {
            uint32_t events = willrt_poll_events(fds[i].revents);

            if (events == 0 || snapshot[i].cb == NULL) {
                continue;
            }
            snapshot[i].cb(snapshot[i].fd, events & (masks[i] | WILLRT_POLL_ERR),
                           snapshot[i].arg);
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
        willrt_os_mutex_init(&willrt_poll_lock);
        atomic_store(&willrt_poll_stop, 0);
        willrt_rc_set_shared();
        willrt_mem_set_shared();
        if (willrt_os_thread_start(&willrt_poll_thread, willrt_poll_main, NULL)
            != 0) {
            willrt_poll_wake_close();
            willrt_os_mutex_destroy(&willrt_poll_lock);
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

void willrt_poll_shutdown(void) {
    if (atomic_load(&willrt_poll_state) != 2) {
        return;
    }
    atomic_store(&willrt_poll_stop, 1);
    willrt_poll_wake();
    willrt_os_thread_join(&willrt_poll_thread);
    willrt_poll_wake_close();
    willrt_os_mutex_destroy(&willrt_poll_lock);
    memset(willrt_poll_entries, 0, sizeof(willrt_poll_entries));
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
