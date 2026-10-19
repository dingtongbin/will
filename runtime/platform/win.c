/* Platform services for Windows: page-backed virtual memory and the monotonic
   clock. VirtualAlloc with MEM_COMMIT returns zero-filled pages, and
   MEM_RELEASE returns the whole reservation, so the size argument is not
   needed again.

   QueryPerformanceCounter counts at a frequency fixed at boot (100 ns on
   common x86 machines). The conversion below truncates to whole nanoseconds,
   so two calls within the same tick report the same value. */

#include <windows.h>

#include "../include/willrt.h"

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
