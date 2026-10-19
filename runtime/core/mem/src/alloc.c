/* General allocation and the plate allocator. A plate is one raw block whose
   header sits directly before the payload, so release needs no table lookup
   and runs in constant time.

   Plates are not sharded by lane: the runtime has no lane model yet. One
   block per plate is already correct and O(1) to release from any thread;
   sharding becomes worthwhile only when allocation must avoid contention.

   Calls are safe from several threads because this file keeps no shared
   mutable state and the platform allocator is thread-safe. No lock or atomic
   is added: either would cost the hot path without protecting anything. */

#include "alloc.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#if defined(_WIN32)
#include <malloc.h>
#endif

static const uint64_t willrt_plate_magic = UINT64_C(0x504C4154454D454D);

static int willrt_mem_is_power_of_two(size_t value) {
    return value != 0 && (value & (value - 1)) == 0;
}

/* Returns the smallest power of two that is at least value, or zero when that
   value does not fit in size_t. */
static size_t willrt_mem_power_of_two_at_least(size_t value) {
    size_t result = 1;
    while (result < value) {
        if (result > SIZE_MAX / 2) {
            return 0;
        }
        result <<= 1;
    }
    return result;
}

/* The start alignment is max(elem, alignof(max_align_t)) rounded up to a
   power of two. The round-up keeps a non-power-of-two elem usable with the
   platform allocator while never lowering the alignment. */
static size_t willrt_mem_plate_align(size_t elem) {
    size_t align = _Alignof(max_align_t);
    size_t wanted = elem > align ? elem : align;
    return willrt_mem_power_of_two_at_least(wanted);
}

void* willrt_mem_raw_alloc(size_t size, size_t align) {
    size_t padded;

    if (!willrt_mem_is_power_of_two(align) || align < sizeof(void*)) {
        return NULL;
    }
    if (size == 0) {
        size = 1;
    }
    if (size > SIZE_MAX - (align - 1)) {
        return NULL;
    }
    padded = (size + align - 1) & ~(align - 1);
#if defined(_WIN32)
    return _aligned_malloc(padded, align);
#else
    return aligned_alloc(align, padded);
#endif
}

void willrt_mem_raw_free(void* ptr) {
    if (ptr == NULL) {
        return;
    }
#if defined(_WIN32)
    _aligned_free(ptr);
#else
    free(ptr);
#endif
}

void* willrt_alloc(size_t size) {
    return willrt_mem_raw_alloc(size, _Alignof(max_align_t));
}

void willrt_free(void* ptr) {
    willrt_mem_raw_free(ptr);
}

void* willrt_plate_new(size_t count, size_t elem) {
    size_t bytes;
    size_t align;
    size_t overhead;
    unsigned char* base;
    uintptr_t payload;
    struct willrt_plate_header* header;

    if (elem == 0) {
        return NULL;
    }
    if (count != 0 && elem > SIZE_MAX / count) {
        return NULL;
    }
    bytes = count * elem;
    align = willrt_mem_plate_align(elem);
    if (align == 0) {
        return NULL;
    }
    overhead = sizeof(struct willrt_plate_header) + (align - 1);
    if (bytes > SIZE_MAX - overhead) {
        return NULL;
    }
    base = willrt_mem_raw_alloc(overhead + bytes, align);
    if (base == NULL) {
        return NULL;
    }
    payload = ((uintptr_t)base + sizeof(struct willrt_plate_header) + (align - 1))
              & ~(uintptr_t)(align - 1);
    header = (struct willrt_plate_header*)(payload - sizeof(struct willrt_plate_header));
    header->magic = willrt_plate_magic;
    header->base = base;
    header->count = count;
    header->elem = elem;
    return (void*)payload;
}

void willrt_plate_release(void* plate) {
    struct willrt_plate_header* header;
    void* base;

    if (plate == NULL) {
        return;
    }
    /* A plate pointer must address mapped memory: a wild pointer that faults
       while the header is read cannot be rejected here. */
    header = (struct willrt_plate_header*)((unsigned char*)plate - sizeof(*header));
    if (header->magic != willrt_plate_magic) {
        fputs("willrt: plate release rejected: bad header magic\n", stderr);
        return;
    }
    base = header->base;
    header->magic = 0;
    willrt_mem_raw_free(base);
}
