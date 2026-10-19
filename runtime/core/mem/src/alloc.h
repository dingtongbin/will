/* Internal interface of the memory allocator. The public entry points are in
   willrt.h; this header carries the plate header layout shared by the
   allocator and its contract test, plus the arena counters the test uses to
   observe page reuse. */

#ifndef WILLRT_MEM_ALLOC_H
#define WILLRT_MEM_ALLOC_H

#include <stddef.h>
#include <stdint.h>

#include "../../../include/willrt.h"

/* willrt_plate_header sits immediately before a plate payload. The base field
   is required because the payload is aligned after the header, so the payload
   address is not the address to return to the arena. */
struct willrt_plate_header {
    uint64_t magic;
    void* base;
    size_t count;
    size_t elem;
};

/* willrt_mem_chunk_count reports how many page chunks the arena holds. It
   falls when a dedicated chunk for a large block is returned to the system. */
size_t willrt_mem_chunk_count(void);

/* willrt_mem_page_calls reports how many times the arena has asked the system
   for pages. Reusing a freed small block must leave it unchanged. */
size_t willrt_mem_page_calls(void);

#endif
