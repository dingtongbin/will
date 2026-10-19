/* Internal interface of the memory allocator. The public entry points are in
   willrt.h; this header carries the raw allocation seam and the plate header
   layout shared by the allocator and its contract test. */

#ifndef WILLRT_MEM_ALLOC_H
#define WILLRT_MEM_ALLOC_H

#include <stddef.h>
#include <stdint.h>

#include "../../../include/willrt.h"

/* willrt_plate_header sits immediately before a plate payload. The base field
   is required because the payload is aligned after the header, so the payload
   address is not the address to return to the raw allocator. */
struct willrt_plate_header {
    uint64_t magic;
    void* base;
    size_t count;
    size_t elem;
};

/* willrt_mem_raw_alloc returns a block of at least size bytes aligned to
   align, or NULL on failure. align must be a power of two and at least
   sizeof(void*). A zero size still yields a unique block. */
void* willrt_mem_raw_alloc(size_t size, size_t align);

/* willrt_mem_raw_free releases a block from willrt_mem_raw_alloc. NULL is
   accepted. */
void willrt_mem_raw_free(void* ptr);

#endif
