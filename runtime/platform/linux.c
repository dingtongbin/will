/* Platform services for Linux: page-backed virtual memory and the monotonic
   clock. mmap with MAP_ANONYMOUS returns zero-filled pages, so the arena
   never clears them itself. */

#define _DEFAULT_SOURCE 1

#include <sys/mman.h>
#include <time.h>

#include "../include/willrt.h"

int64_t willrt_os_now_ns(void) {
    struct timespec now;

    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0 || now.tv_sec < 0) {
        return 0;
    }
    if ((uint64_t)now.tv_sec > (uint64_t)INT64_MAX / UINT64_C(1000000000)) {
        return INT64_MAX;
    }
    return (int64_t)now.tv_sec * INT64_C(1000000000) + (int64_t)now.tv_nsec;
}

void* willrt_os_alloc_pages(size_t size) {
    void* block;

    if (size == 0) {
        size = 1;
    }
    block = mmap(NULL, size, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (block == MAP_FAILED) {
        return NULL;
    }
    return block;
}

void willrt_os_free_pages(void* ptr, size_t size) {
    if (ptr == NULL) {
        return;
    }
    if (size == 0) {
        size = 1;
    }
    munmap(ptr, size);
}
