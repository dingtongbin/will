#define _GNU_SOURCE

#include "io_uring_linux.h"

#ifdef __linux__

#include "../../../include/willrt.h"

#include <errno.h>
#include <linux/io_uring.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <unistd.h>

enum { WILLRT_IO_RING_ENTRIES = 256 };

typedef struct willrt_io_ring {
    int fd;
    void* sq_ring;
    size_t sq_ring_size;
    void* cq_ring;
    size_t cq_ring_size;
    struct io_uring_sqe* sqes;
    size_t sqes_size;
    unsigned* sq_head;
    unsigned* sq_tail;
    unsigned* sq_mask;
    unsigned* sq_entries;
    unsigned* sq_array;
    unsigned* cq_head;
    unsigned* cq_tail;
    unsigned* cq_mask;
    struct io_uring_cqe* cqes;
} willrt_io_ring;

static willrt_io_ring ring;
static willrt_os_mutex submit_lock;
static willrt_os_cond submit_cond;
static willrt_os_thread submitter;
static willrt_os_thread completer;
static unsigned submit_pending;
static atomic_int stop_flag;
static atomic_int ready_flag;
static void (*complete_callback)(void*, int32_t, int64_t);

static int32_t ring_setup(void) {
    struct io_uring_params params;
    size_t sq_size;
    size_t cq_size;
    size_t map_size;
    int fd;

    memset(&params, 0, sizeof(params));
    fd = (int)syscall(SYS_io_uring_setup, WILLRT_IO_RING_ENTRIES, &params);
    if (fd < 0) {
        return -1;
    }
    ring.fd = fd;
    sq_size = params.sq_off.array
              + (size_t)params.sq_entries * sizeof(unsigned);
    cq_size = params.cq_off.cqes
              + (size_t)params.cq_entries * sizeof(struct io_uring_cqe);
    map_size = sq_size > cq_size ? sq_size : cq_size;
    if ((params.features & IORING_FEAT_SINGLE_MMAP) != 0) {
        ring.sq_ring = mmap(NULL, map_size, PROT_READ | PROT_WRITE,
                            MAP_SHARED | MAP_POPULATE, fd, IORING_OFF_SQ_RING);
        if (ring.sq_ring == MAP_FAILED) {
            close(fd);
            return -1;
        }
        ring.sq_ring_size = map_size;
        ring.cq_ring = ring.sq_ring;
        ring.cq_ring_size = map_size;
    } else {
        ring.sq_ring = mmap(NULL, sq_size, PROT_READ | PROT_WRITE,
                            MAP_SHARED | MAP_POPULATE, fd, IORING_OFF_SQ_RING);
        if (ring.sq_ring == MAP_FAILED) {
            close(fd);
            return -1;
        }
        ring.sq_ring_size = sq_size;
        ring.cq_ring = mmap(NULL, cq_size, PROT_READ | PROT_WRITE,
                            MAP_SHARED | MAP_POPULATE, fd, IORING_OFF_CQ_RING);
        if (ring.cq_ring == MAP_FAILED) {
            munmap(ring.sq_ring, ring.sq_ring_size);
            close(fd);
            return -1;
        }
        ring.cq_ring_size = cq_size;
    }
    ring.sqes_size = (size_t)params.sq_entries * sizeof(struct io_uring_sqe);
    ring.sqes = mmap(NULL, ring.sqes_size, PROT_READ | PROT_WRITE,
                     MAP_SHARED | MAP_POPULATE, fd, IORING_OFF_SQES);
    if (ring.sqes == MAP_FAILED) {
        munmap(ring.cq_ring, ring.cq_ring_size);
        if (ring.cq_ring != ring.sq_ring) {
            munmap(ring.sq_ring, ring.sq_ring_size);
        }
        close(fd);
        return -1;
    }
    ring.sq_head = (unsigned*)((char*)ring.sq_ring + params.sq_off.head);
    ring.sq_tail = (unsigned*)((char*)ring.sq_ring + params.sq_off.tail);
    ring.sq_mask = (unsigned*)((char*)ring.sq_ring + params.sq_off.ring_mask);
    ring.sq_entries =
        (unsigned*)((char*)ring.sq_ring + params.sq_off.ring_entries);
    ring.sq_array = (unsigned*)((char*)ring.sq_ring + params.sq_off.array);
    ring.cq_head = (unsigned*)((char*)ring.cq_ring + params.cq_off.head);
    ring.cq_tail = (unsigned*)((char*)ring.cq_ring + params.cq_off.tail);
    ring.cq_mask = (unsigned*)((char*)ring.cq_ring + params.cq_off.ring_mask);
    ring.cqes = (struct io_uring_cqe*)((char*)ring.cq_ring
                                       + params.cq_off.cqes);
    return 0;
}

static int32_t submit_enter(unsigned count) {
    int result;

    do {
        result = (int)syscall(SYS_io_uring_enter, ring.fd, count, 0, 0, NULL,
                              0);
    } while (result < 0 && errno == EINTR);
    return result < 0 ? -1 : 0;
}

static void submit_thread(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    for (;;) {
        unsigned count;
        int32_t done;

        willrt_os_mutex_lock(&submit_lock);
        while (submit_pending == 0 && atomic_load(&stop_flag) == 0) {
            willrt_os_cond_wait(&submit_cond, &submit_lock);
        }
        count = submit_pending;
        submit_pending = 0;
        if (count != 0) {
            (void)submit_enter(count);
        }
        done = atomic_load(&stop_flag) != 0 && submit_pending == 0;
        willrt_os_mutex_unlock(&submit_lock);
        if (done != 0) {
            break;
        }
    }
    willrt_mem_thread_flush();
}

static void complete_thread(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    while (atomic_load(&stop_flag) == 0) {
        unsigned head;
        unsigned tail;
        int result;

        result = (int)syscall(SYS_io_uring_enter, ring.fd, 0, 1,
                              IORING_ENTER_GETEVENTS, NULL, 0);
        if (result < 0) {
            if (errno == EINTR) {
                continue;
            }
            break;
        }
        head = __atomic_load_n(ring.cq_head, __ATOMIC_ACQUIRE);
        tail = __atomic_load_n(ring.cq_tail, __ATOMIC_ACQUIRE);
        while (head != tail) {
            struct io_uring_cqe* cqe = &ring.cqes[head & *ring.cq_mask];

            if (cqe->user_data != 0 && complete_callback != NULL) {
                int32_t status = cqe->res < 0 ? -1 : 0;
                complete_callback((void*)(uintptr_t)cqe->user_data, status,
                                  cqe->res < 0 ? 0 : (int64_t)cqe->res);
            }
            head++;
        }
        __atomic_store_n(ring.cq_head, head, __ATOMIC_RELEASE);
    }
    willrt_mem_thread_flush();
}

int32_t willrt_io_ring_start(void (*complete)(void*, int32_t, int64_t)) {
    complete_callback = complete;
    atomic_store(&stop_flag, 0);
    atomic_store(&ready_flag, 0);
    if (ring_setup() != 0) {
        return -1;
    }
    willrt_os_mutex_init(&submit_lock);
    willrt_os_cond_init(&submit_cond);
    submit_pending = 0;
    if (willrt_os_thread_start(&submitter, submit_thread, NULL) != 0) {
        munmap(ring.sqes, ring.sqes_size);
        if (ring.cq_ring != ring.sq_ring) {
            munmap(ring.cq_ring, ring.cq_ring_size);
        }
        munmap(ring.sq_ring, ring.sq_ring_size);
        close(ring.fd);
        return -1;
    }
    if (willrt_os_thread_start(&completer, complete_thread, NULL) != 0) {
        atomic_store(&stop_flag, 1);
        willrt_os_mutex_lock(&submit_lock);
        willrt_os_cond_broadcast(&submit_cond);
        willrt_os_mutex_unlock(&submit_lock);
        willrt_os_thread_join(&submitter);
        munmap(ring.sqes, ring.sqes_size);
        if (ring.cq_ring != ring.sq_ring) {
            munmap(ring.cq_ring, ring.cq_ring_size);
        }
        munmap(ring.sq_ring, ring.sq_ring_size);
        close(ring.fd);
        return -1;
    }
    atomic_store(&ready_flag, 1);
    return 0;
}

int32_t willrt_io_ring_ready(void) {
    return atomic_load(&ready_flag);
}

int32_t willrt_io_ring_submit(int fd, int32_t kind, void* buffer, int64_t len,
                              void* op) {
    struct io_uring_sqe* sqe;
    unsigned tail;
    unsigned head;
    unsigned index;

    if (atomic_load(&ready_flag) == 0 || fd < 0 || len < 0) {
        return -1;
    }
    for (;;) {
        willrt_os_mutex_lock(&submit_lock);
        tail = __atomic_load_n(ring.sq_tail, __ATOMIC_RELAXED);
        head = __atomic_load_n(ring.sq_head, __ATOMIC_ACQUIRE);
        if (tail - head < *ring.sq_entries) {
            break;
        }
        willrt_os_mutex_unlock(&submit_lock);
        willrt_os_thread_yield();
    }
    index = tail & *ring.sq_mask;
    sqe = &ring.sqes[index];
    memset(sqe, 0, sizeof(*sqe));
    sqe->opcode = kind == 0 ? IORING_OP_READ : IORING_OP_WRITE;
    sqe->fd = fd;
    sqe->addr = (uint64_t)(uintptr_t)buffer;
    sqe->len = (uint32_t)len;
    sqe->off = kind == 2 ? (uint64_t)-1 : 0;
    sqe->user_data = (uint64_t)(uintptr_t)op;
    ring.sq_array[index] = index;
    __atomic_store_n(ring.sq_tail, tail + 1, __ATOMIC_RELEASE);
    submit_pending += 1;
    willrt_os_cond_signal(&submit_cond);
    willrt_os_mutex_unlock(&submit_lock);
    return 0;
}

void willrt_io_ring_stop(void) {
    struct io_uring_sqe* sqe;
    unsigned tail;
    unsigned head;
    unsigned index;

    if (atomic_load(&ready_flag) == 0) {
        return;
    }
    atomic_store(&stop_flag, 1);
    willrt_os_mutex_lock(&submit_lock);
    willrt_os_cond_broadcast(&submit_cond);
    willrt_os_mutex_unlock(&submit_lock);
    willrt_os_thread_join(&submitter);
    willrt_os_mutex_lock(&submit_lock);
    tail = __atomic_load_n(ring.sq_tail, __ATOMIC_RELAXED);
    head = __atomic_load_n(ring.sq_head, __ATOMIC_ACQUIRE);
    if (tail - head < *ring.sq_entries) {
        index = tail & *ring.sq_mask;
        sqe = &ring.sqes[index];
        memset(sqe, 0, sizeof(*sqe));
        sqe->opcode = IORING_OP_NOP;
        sqe->user_data = 0;
        ring.sq_array[index] = index;
        __atomic_store_n(ring.sq_tail, tail + 1, __ATOMIC_RELEASE);
        (void)submit_enter(1);
    }
    willrt_os_mutex_unlock(&submit_lock);
    willrt_os_thread_join(&completer);
    munmap(ring.sqes, ring.sqes_size);
    if (ring.cq_ring != ring.sq_ring) {
        munmap(ring.cq_ring, ring.cq_ring_size);
    }
    munmap(ring.sq_ring, ring.sq_ring_size);
    close(ring.fd);
    atomic_store(&ready_flag, 0);
}

#endif
