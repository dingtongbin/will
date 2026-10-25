#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <pthread.h>
#include <sched.h>
#include <stdint.h>
#include <sys/mman.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

#include "../include/willrt.h"

int32_t willrt_os_cpu_count(void) {
    long count = sysconf(_SC_NPROCESSORS_ONLN);

    return count < 1 ? 1 : (int32_t)count;
}

void* willrt_os_alloc_pages(size_t size) {
    void* memory;

    if (size == 0) {
        size = 1;
    }
    memory = mmap(NULL, size, PROT_READ | PROT_WRITE,
                  MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    return memory == MAP_FAILED ? NULL : memory;
}

void willrt_os_free_pages(void* ptr, size_t size) {
    if (ptr != NULL) {
        munmap(ptr, size == 0 ? 1 : size);
    }
}

int64_t willrt_os_now_ns(void) {
    struct timespec now;

    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
        return 0;
    }
    return (int64_t)now.tv_sec * INT64_C(1000000000) + (int64_t)now.tv_nsec;
}

typedef struct willrt_posix_thread {
    pthread_t handle;
    void (*fn)(void*);
    void* arg;
} willrt_posix_thread;

static void* willrt_posix_thread_entry(void* raw) {
    willrt_posix_thread* thread = (willrt_posix_thread*)raw;

    thread->fn(thread->arg);
    return NULL;
}

static void willrt_posix_deadline(int64_t timeout_ns, struct timespec* out) {
    int64_t seconds;
    int64_t nanos;

    clock_gettime(CLOCK_REALTIME, out);
    seconds = timeout_ns / INT64_C(1000000000);
    nanos = timeout_ns % INT64_C(1000000000);
    out->tv_sec += (time_t)seconds;
    out->tv_nsec += (long)nanos;
    if (out->tv_nsec >= 1000000000L) {
        out->tv_sec += 1;
        out->tv_nsec -= 1000000000L;
    }
}

int32_t willrt_os_thread_start(willrt_os_thread* thread, void (*fn)(void*),
                               void* arg) {
    willrt_posix_thread* state = (willrt_posix_thread*)thread->bytes;

    state->fn = fn;
    state->arg = arg;
    return pthread_create(&state->handle, NULL, willrt_posix_thread_entry,
                          state) == 0
               ? 0
               : -1;
}

void willrt_os_thread_join(willrt_os_thread* thread) {
    willrt_posix_thread* state = (willrt_posix_thread*)thread->bytes;

    pthread_join(state->handle, NULL);
}

void willrt_os_thread_yield(void) {
    sched_yield();
}

void willrt_os_sleep_ns(int64_t ns) {
    struct timespec delay;

    if (ns <= 0) {
        sched_yield();
        return;
    }
    delay.tv_sec = (time_t)(ns / INT64_C(1000000000));
    delay.tv_nsec = (long)(ns % INT64_C(1000000000));
    while (nanosleep(&delay, &delay) != 0 && errno == EINTR) {
    }
}

void willrt_os_mutex_init(willrt_os_mutex* mutex) {
    pthread_mutex_init((pthread_mutex_t*)mutex->bytes, NULL);
}

void willrt_os_mutex_destroy(willrt_os_mutex* mutex) {
    pthread_mutex_destroy((pthread_mutex_t*)mutex->bytes);
}

void willrt_os_mutex_lock(willrt_os_mutex* mutex) {
    pthread_mutex_lock((pthread_mutex_t*)mutex->bytes);
}

void willrt_os_mutex_unlock(willrt_os_mutex* mutex) {
    pthread_mutex_unlock((pthread_mutex_t*)mutex->bytes);
}

void willrt_os_cond_init(willrt_os_cond* cond) {
    pthread_cond_init((pthread_cond_t*)cond->bytes, NULL);
}

void willrt_os_cond_destroy(willrt_os_cond* cond) {
    pthread_cond_destroy((pthread_cond_t*)cond->bytes);
}

void willrt_os_cond_wait(willrt_os_cond* cond, willrt_os_mutex* mutex) {
    pthread_cond_wait((pthread_cond_t*)cond->bytes,
                      (pthread_mutex_t*)mutex->bytes);
}

int32_t willrt_os_cond_timedwait(willrt_os_cond* cond, willrt_os_mutex* mutex,
                                 int64_t timeout_ns) {
    struct timespec deadline;
    int result;

    if (timeout_ns < 0) {
        pthread_cond_wait((pthread_cond_t*)cond->bytes,
                          (pthread_mutex_t*)mutex->bytes);
        return 0;
    }
    willrt_posix_deadline(timeout_ns, &deadline);
    result = pthread_cond_timedwait((pthread_cond_t*)cond->bytes,
                                    (pthread_mutex_t*)mutex->bytes, &deadline);
    return result == ETIMEDOUT ? 1 : 0;
}

void willrt_os_cond_signal(willrt_os_cond* cond) {
    pthread_cond_signal((pthread_cond_t*)cond->bytes);
}

void willrt_os_cond_broadcast(willrt_os_cond* cond) {
    pthread_cond_broadcast((pthread_cond_t*)cond->bytes);
}

/* POSIX has no portable address wait that covers every target in this tree.
   The fallback polls the word once per millisecond; the scheduler therefore
   still makes progress but pays a wake-up per millisecond while idle. */
int32_t willrt_os_wait_on_address(void* addr, uint32_t expected,
                                  int64_t timeout_ns) {
    int64_t start = willrt_os_now_ns();

    for (;;) {
        int64_t elapsed;

        if (*(volatile uint32_t*)addr != expected) {
            return 0;
        }
        if (timeout_ns >= 0) {
            elapsed = willrt_os_now_ns() - start;
            if (elapsed >= timeout_ns) {
                return 1;
            }
        }
        willrt_os_sleep_ns(INT64_C(1000000));
    }
}

void willrt_os_wake_address(void* addr, int32_t count) {
    (void)addr;
    (void)count;
}

void willrt_os_thread_exit(void) {
    pthread_exit(NULL);
}
