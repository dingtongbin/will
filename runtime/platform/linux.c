/* Page-backed virtual memory for Linux. mmap with MAP_ANONYMOUS returns
   zero-filled pages, so the arena never clears them itself. */

#define _DEFAULT_SOURCE 1

#include <sys/mman.h>

#include "../include/willrt.h"

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
