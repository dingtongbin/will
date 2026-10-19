/* Page-backed virtual memory for Windows. VirtualAlloc with MEM_COMMIT
   returns zero-filled pages, and MEM_RELEASE returns the whole reservation,
   so the size argument is not needed again. */

#include <windows.h>

#include "../include/willrt.h"

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
