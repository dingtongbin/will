/* Arena allocator for willrt_alloc and plates.
 *
 * The arena asks the platform layer for page chunks through
 * willrt_os_alloc_pages and never touches malloc or aligned_alloc. Small
 * requests are carved from 64 KiB chunks into power-of-two size classes; a
 * freed small block goes on that class's free list and is handed out again.
 * A request larger than the largest class gets a dedicated chunk, which is
 * returned to the system as a whole when the block is freed.
 *
 * Every chunk is linked into one list, so willrt_free can tell a block of this
 * arena from a foreign pointer by comparing addresses. It never dereferences a
 * pointer before that check, so a stack address or an interior pointer is
 * rejected with one line on standard error instead of a fault.
 *
 * The arena is single-threaded on purpose. Lane sharding waits for the lane
 * model; willrt_alloc and willrt_plate_new keep their signatures, so the split
 * happens inside this file when lanes arrive. No lock is added here, because a
 * global lock is exactly the structure the lane split has to remove.
 */

#include "alloc.h"

#include <stdint.h>
#include <stdio.h>

/* Small classes are powers of two from 32 bytes through 16 KiB. A 64 KiB
   chunk is a multiple of the page size on every target platform, and it holds
   at least one block of the largest class. Large requests are rounded to
   16 KiB, also a multiple of the page size on every target. */
enum {
    WILLRT_MEM_CLASS_COUNT = 10,
    WILLRT_MEM_CLASS_MIN = 32,
    WILLRT_MEM_CHUNK_BYTES = 64 * 1024,
    WILLRT_MEM_LARGE_ALIGN = 16 * 1024
};

static const uint64_t willrt_mem_block_live = UINT64_C(0x4C495645424C4F43);
static const uint64_t willrt_mem_block_dead = UINT64_C(0x44454144424C4F43);
static const uint64_t willrt_plate_magic = UINT64_C(0x504C4154454D454D);

static const size_t willrt_mem_class_size[WILLRT_MEM_CLASS_COUNT] = {
    32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384
};

struct willrt_mem_chunk {
    struct willrt_mem_chunk* next;
    struct willrt_mem_chunk* prev;
    size_t bytes;
    size_t bump;
};

struct willrt_mem_block {
    uint64_t magic;
    size_t capacity;
    struct willrt_mem_chunk* chunk;
    int32_t class_index;
};

/* A block header must end on a boundary of max_align_t so every payload is
   aligned for any type, and the chunk header likewise so the first block
   starts aligned. */
static const size_t willrt_mem_block_offset =
    (sizeof(struct willrt_mem_block) + _Alignof(max_align_t) - 1)
    / _Alignof(max_align_t) * _Alignof(max_align_t);
static const size_t willrt_mem_chunk_start =
    (sizeof(struct willrt_mem_chunk) + _Alignof(max_align_t) - 1)
    / _Alignof(max_align_t) * _Alignof(max_align_t);

static struct willrt_mem_chunk* willrt_mem_chunks;
static struct willrt_mem_chunk* willrt_mem_partial[WILLRT_MEM_CLASS_COUNT];
static void* willrt_mem_free_list[WILLRT_MEM_CLASS_COUNT];
static size_t willrt_mem_chunks_held;
static size_t willrt_mem_page_calls_issued;

/* Returns value rounded up to align, or zero when the round-up overflows.
   align must be a power of two. */
static size_t willrt_mem_align_up(size_t value, size_t align) {
    if (value > SIZE_MAX - (align - 1)) {
        return 0;
    }
    return (value + align - 1) & ~(align - 1);
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

/* The start alignment is max(elem, alignof(max_align_t)) rounded up to a power
   of two. The round-up keeps a non-power-of-two elem usable while never
   lowering the alignment. */
static size_t willrt_mem_plate_align(size_t elem) {
    size_t align = _Alignof(max_align_t);
    size_t wanted = elem > align ? elem : align;

    return willrt_mem_power_of_two_at_least(wanted);
}

/* Returns the class index for size, or -1 when the request needs a dedicated
   chunk. A zero size was already turned into one byte by willrt_alloc. */
static int willrt_mem_class_for(size_t size) {
    int index;

    for (index = 0; index < WILLRT_MEM_CLASS_COUNT; index++) {
        if (size <= willrt_mem_class_size[index]) {
            return index;
        }
    }
    return -1;
}

static struct willrt_mem_chunk* willrt_mem_chunk_containing(const void* ptr) {
    uintptr_t address = (uintptr_t)ptr;
    struct willrt_mem_chunk* chunk;

    for (chunk = willrt_mem_chunks; chunk != NULL; chunk = chunk->next) {
        uintptr_t base = (uintptr_t)chunk;

        if (address >= base && address - base < chunk->bytes) {
            return chunk;
        }
    }
    return NULL;
}

/* Reports whether the whole range lies in one chunk. The check uses only
   address arithmetic, so it is safe for a pointer the arena does not own. */
static int willrt_mem_range_contained(const void* ptr, size_t size) {
    uintptr_t start = (uintptr_t)ptr;
    struct willrt_mem_chunk* chunk;

    if (size > UINTPTR_MAX - start) {
        return 0;
    }
    for (chunk = willrt_mem_chunks; chunk != NULL; chunk = chunk->next) {
        uintptr_t base = (uintptr_t)chunk;

        if (start >= base && start - base <= chunk->bytes
            && size <= chunk->bytes - (start - base)) {
            return 1;
        }
    }
    return 0;
}

static struct willrt_mem_chunk* willrt_mem_chunk_new(size_t bytes) {
    struct willrt_mem_chunk* chunk;

    if (bytes < willrt_mem_chunk_start + willrt_mem_block_offset
                    + WILLRT_MEM_CLASS_MIN) {
        return NULL;
    }
    willrt_mem_page_calls_issued++;
    chunk = (struct willrt_mem_chunk*)willrt_os_alloc_pages(bytes);
    if (chunk == NULL) {
        return NULL;
    }
    chunk->bytes = bytes;
    chunk->bump = willrt_mem_chunk_start;
    chunk->prev = NULL;
    chunk->next = willrt_mem_chunks;
    if (willrt_mem_chunks != NULL) {
        willrt_mem_chunks->prev = chunk;
    }
    willrt_mem_chunks = chunk;
    willrt_mem_chunks_held++;
    return chunk;
}

static void willrt_mem_chunk_unlink(struct willrt_mem_chunk* chunk) {
    if (chunk->prev != NULL) {
        chunk->prev->next = chunk->next;
    } else {
        willrt_mem_chunks = chunk->next;
    }
    if (chunk->next != NULL) {
        chunk->next->prev = chunk->prev;
    }
    willrt_mem_chunks_held--;
}

static void* willrt_mem_small_alloc(int class_index) {
    size_t capacity = willrt_mem_class_size[class_index];
    size_t stride = willrt_mem_align_up(willrt_mem_block_offset + capacity,
                                        _Alignof(max_align_t));
    struct willrt_mem_chunk* chunk = willrt_mem_partial[class_index];
    struct willrt_mem_block* block;
    unsigned char* cursor;
    void* payload = willrt_mem_free_list[class_index];

    if (payload != NULL) {
        willrt_mem_free_list[class_index] = *(void**)payload;
        block = (struct willrt_mem_block*)((unsigned char*)payload
                                           - willrt_mem_block_offset);
        block->magic = willrt_mem_block_live;
        block->capacity = capacity;
        return payload;
    }
    if (chunk == NULL || stride > chunk->bytes - chunk->bump) {
        chunk = willrt_mem_chunk_new(WILLRT_MEM_CHUNK_BYTES);
        if (chunk == NULL) {
            return NULL;
        }
        willrt_mem_partial[class_index] = chunk;
    }
    cursor = (unsigned char*)chunk + chunk->bump;
    chunk->bump += stride;
    block = (struct willrt_mem_block*)cursor;
    block->magic = willrt_mem_block_live;
    block->capacity = capacity;
    block->chunk = chunk;
    block->class_index = class_index;
    return cursor + willrt_mem_block_offset;
}

static void* willrt_mem_large_alloc(size_t size) {
    struct willrt_mem_chunk* chunk;
    struct willrt_mem_block* block;
    size_t need;
    size_t bytes;

    if (size > SIZE_MAX - willrt_mem_chunk_start - willrt_mem_block_offset) {
        return NULL;
    }
    need = willrt_mem_chunk_start + willrt_mem_block_offset + size;
    bytes = willrt_mem_align_up(need, WILLRT_MEM_LARGE_ALIGN);
    if (bytes == 0) {
        return NULL;
    }
    chunk = willrt_mem_chunk_new(bytes);
    if (chunk == NULL) {
        return NULL;
    }
    block = (struct willrt_mem_block*)((unsigned char*)chunk
                                       + willrt_mem_chunk_start);
    block->magic = willrt_mem_block_live;
    block->capacity = size;
    block->chunk = chunk;
    block->class_index = -1;
    return (unsigned char*)block + willrt_mem_block_offset;
}

void* willrt_alloc(size_t size) {
    int class_index;

    if (size == 0) {
        size = 1;
    }
    class_index = willrt_mem_class_for(size);
    if (class_index >= 0) {
        return willrt_mem_small_alloc(class_index);
    }
    return willrt_mem_large_alloc(size);
}

void willrt_free(void* ptr) {
    unsigned char* payload = (unsigned char*)ptr;
    struct willrt_mem_chunk* chunk;
    struct willrt_mem_block* block;

    if (ptr == NULL) {
        return;
    }
    chunk = willrt_mem_chunk_containing(ptr);
    if (chunk == NULL
        || payload < (unsigned char*)chunk + willrt_mem_chunk_start
                          + willrt_mem_block_offset) {
        fputs("willrt: free rejected: pointer was not returned by willrt_alloc\n",
              stderr);
        return;
    }
    block = (struct willrt_mem_block*)(payload - willrt_mem_block_offset);
    if (block->magic != willrt_mem_block_live || block->chunk != chunk
        || block->class_index < -1
        || block->class_index >= WILLRT_MEM_CLASS_COUNT) {
        fputs("willrt: free rejected: block is not live\n", stderr);
        return;
    }
    if (block->class_index < 0) {
        size_t bytes = chunk->bytes;

        willrt_mem_chunk_unlink(chunk);
        willrt_os_free_pages(chunk, bytes);
        return;
    }
    block->magic = willrt_mem_block_dead;
    *(void**)payload = willrt_mem_free_list[block->class_index];
    willrt_mem_free_list[block->class_index] = payload;
}

void* willrt_plate_new(size_t count, size_t elem) {
    struct willrt_plate_header* header;
    unsigned char* base;
    uintptr_t payload;
    size_t bytes;
    size_t align;
    size_t overhead;

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
    base = (unsigned char*)willrt_alloc(overhead + bytes);
    if (base == NULL) {
        return NULL;
    }
    payload = ((uintptr_t)base + sizeof(struct willrt_plate_header) + (align - 1))
              & ~(uintptr_t)(align - 1);
    header = (struct willrt_plate_header*)(payload
                                           - sizeof(struct willrt_plate_header));
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
    header = (struct willrt_plate_header*)((unsigned char*)plate
                                           - sizeof(*header));
    if (!willrt_mem_range_contained(header, sizeof(*header))) {
        fputs("willrt: plate release rejected: pointer is not in the arena\n",
              stderr);
        return;
    }
    if (header->magic != willrt_plate_magic) {
        fputs("willrt: plate release rejected: bad header magic\n", stderr);
        return;
    }
    base = header->base;
    header->magic = 0;
    willrt_free(base);
}

size_t willrt_mem_chunk_count(void) {
    return willrt_mem_chunks_held;
}

size_t willrt_mem_page_calls(void) {
    return willrt_mem_page_calls_issued;
}
