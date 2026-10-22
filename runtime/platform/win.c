/* Platform services for Windows: virtual memory, the monotonic clock, threads,
   locks, condition variables, and address waits.

   VirtualAlloc with MEM_COMMIT returns zero-filled pages, and MEM_RELEASE
   returns the whole reservation, so the size argument is not needed again.

   QueryPerformanceCounter counts at a frequency fixed at boot (100 ns on
   common x86 machines). The conversion below truncates to whole nanoseconds,
   so two calls within the same tick report the same value.

   Threads use CreateThread rather than the CRT wrapper because the runtime
   keeps no CRT per-thread state. A thread's entry and argument live in the
   caller's opaque storage, so starting a thread allocates nothing. */

#define _WIN32_WINNT 0x0602
#define _CRT_RAND_S

#include <windows.h>
#include <stdlib.h>
#include <string.h>

#include "../include/willrt.h"

int32_t willrt_os_cpu_count(void) {
    DWORD count = GetActiveProcessorCount(ALL_PROCESSOR_GROUPS);

    return count == 0 ? 1 : (int32_t)count;
}

int64_t willrt_os_now_ns(void) {
    LARGE_INTEGER counter;
    LARGE_INTEGER frequency;
    uint64_t ticks;
    uint64_t freq;
    uint64_t seconds;
    uint64_t rest;

    if (!QueryPerformanceCounter(&counter)
        || !QueryPerformanceFrequency(&frequency)) {
        return 0;
    }
    if (frequency.QuadPart <= 0) {
        return 0;
    }
    ticks = (uint64_t)counter.QuadPart;
    freq = (uint64_t)frequency.QuadPart;
    seconds = ticks / freq;
    rest = ticks % freq;
    if (seconds > (uint64_t)INT64_MAX / UINT64_C(1000000000)) {
        return INT64_MAX;
    }
    /* The counter frequency on the Windows targets stays below 1 GHz, so the
       fractional product fits in 64 bits. */
    return (int64_t)(seconds * UINT64_C(1000000000)
                     + (rest * UINT64_C(1000000000)) / freq);
}

void* willrt_os_alloc_pages(size_t size) {
    if (size == 0) {
        size = 1;
    }
    return VirtualAlloc(NULL, size, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE);
}

void willrt_os_free_pages(void* ptr, size_t size) {
    (void)size;
    if (ptr == NULL) {
        return;
    }
    VirtualFree(ptr, 0, MEM_RELEASE);
}

typedef struct willrt_win_thread {
    HANDLE handle;
    void (*fn)(void*);
    void* arg;
} willrt_win_thread;

static DWORD WINAPI willrt_win_thread_entry(LPVOID raw) {
    willrt_win_thread* thread = (willrt_win_thread*)raw;

    thread->fn(thread->arg);
    return 0;
}

static DWORD willrt_win_timeout_ms(int64_t timeout_ns) {
    uint64_t ms;

    if (timeout_ns < 0) {
        return INFINITE;
    }
    if (timeout_ns == 0) {
        return 0;
    }
    ms = (uint64_t)timeout_ns / UINT64_C(1000000);
    if ((uint64_t)timeout_ns % UINT64_C(1000000) != 0) {
        ms++;
    }
    if (ms >= (uint64_t)INFINITE) {
        return INFINITE - 1;
    }
    return (DWORD)ms;
}

int32_t willrt_os_thread_start(willrt_os_thread* thread, void (*fn)(void*),
                               void* arg) {
    willrt_win_thread* state = (willrt_win_thread*)thread->bytes;

    state->fn = fn;
    state->arg = arg;
    state->handle = CreateThread(NULL, 0, willrt_win_thread_entry, state, 0, NULL);
    return state->handle == NULL ? -1 : 0;
}

void willrt_os_thread_join(willrt_os_thread* thread) {
    willrt_win_thread* state = (willrt_win_thread*)thread->bytes;

    if (state->handle == NULL) {
        return;
    }
    WaitForSingleObject(state->handle, INFINITE);
    CloseHandle(state->handle);
    state->handle = NULL;
}

void willrt_os_thread_yield(void) {
    SwitchToThread();
}

void willrt_os_sleep_ns(int64_t ns) {
    DWORD ms;

    if (ns <= 0) {
        SwitchToThread();
        return;
    }
    ms = willrt_win_timeout_ms(ns);
    if (ms == INFINITE) {
        ms = INFINITE - 1;
    }
    Sleep(ms);
}

void willrt_os_mutex_init(willrt_os_mutex* mutex) {
    InitializeCriticalSection((CRITICAL_SECTION*)mutex->bytes);
}

void willrt_os_mutex_destroy(willrt_os_mutex* mutex) {
    DeleteCriticalSection((CRITICAL_SECTION*)mutex->bytes);
}

void willrt_os_mutex_lock(willrt_os_mutex* mutex) {
    EnterCriticalSection((CRITICAL_SECTION*)mutex->bytes);
}

void willrt_os_mutex_unlock(willrt_os_mutex* mutex) {
    LeaveCriticalSection((CRITICAL_SECTION*)mutex->bytes);
}

void willrt_os_cond_init(willrt_os_cond* cond) {
    InitializeConditionVariable((CONDITION_VARIABLE*)cond->bytes);
}

void willrt_os_cond_destroy(willrt_os_cond* cond) {
    (void)cond;
}

void willrt_os_cond_wait(willrt_os_cond* cond, willrt_os_mutex* mutex) {
    SleepConditionVariableCS((CONDITION_VARIABLE*)cond->bytes,
                             (CRITICAL_SECTION*)mutex->bytes, INFINITE);
}

int32_t willrt_os_cond_timedwait(willrt_os_cond* cond, willrt_os_mutex* mutex,
                                 int64_t timeout_ns) {
    if (SleepConditionVariableCS((CONDITION_VARIABLE*)cond->bytes,
                                 (CRITICAL_SECTION*)mutex->bytes,
                                 willrt_win_timeout_ms(timeout_ns))) {
        return 0;
    }
    return 1;
}

void willrt_os_cond_signal(willrt_os_cond* cond) {
    WakeConditionVariable((CONDITION_VARIABLE*)cond->bytes);
}

void willrt_os_cond_broadcast(willrt_os_cond* cond) {
    WakeAllConditionVariable((CONDITION_VARIABLE*)cond->bytes);
}

/* WaitOnAddress lives in a DLL that the default MinGW import set does not
   carry on every target, so the fallback polls one millisecond. The scheduler
   still makes progress; it pays one wake-up per millisecond while idle. */
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
    ExitThread(0);
}
