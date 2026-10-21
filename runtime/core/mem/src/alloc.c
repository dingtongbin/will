/* Lane-sharded arena allocator for willrt_alloc and plates.
 *
 * Every lane owns its partial chunks and free lists. A chunk is a whole number
 * of 64 KiB units and starts on a 64 KiB boundary, so a payload address masks
 * to its chunk header without walking a list. A registry of chunk bases proves
 * that a pointer really came from this arena before any header is read; a
 * foreign or interior pointer is rejected with the same line as before.
 *
 * A block freed on another lane goes back to the free list of the chunk's owner
 * under that lane's lock. A dedicated chunk for a large block also belongs to
 * the lane that allocated it and is returned to the system under that lock.
 *
 * The arena asks the platform for pages through willrt_os_alloc_pages and never
 * touches malloc or aligned_alloc. The scheduler tells the allocator which lane
 * a thread runs on with willrt_mem_set_lane; a thread that never calls it uses
 * lane 0, which keeps the single-threaded tests and the entry wrapper working.
 */

#include "alloc.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>

/* Small classes are powers of two from 32 bytes through 16 KiB. A 64 KiB
   chunk is a multiple of the page size on every target platform, and it holds
   at least one block of the largest class. Large requests are rounded to
   64 KiB, which keeps the chunk base mask valid. */
enum {
    WILLRT_MEM_CLASS_COUNT = 10,
    WILLRT_MEM_CLASS_MIN = 32,
    WILLRT_MEM_CHUNK_BYTES = 64 * 1024,
    WILLRT_MEM_LANES = 64,
    WILLRT_MEM_REGISTRY_BUCKETS = 4096,
    /* A thread keeps this many freed small blocks per class. The refill
       batch amortizes one lane lock over several allocations. */
    WILLRT_MEM_CACHE_LIMIT = 64,
    WILLRT_MEM_CACHE_REFILL = 16
};

static const uint64_t willrt_mem_block_live = UINT64_C(0x4C495645424C4F43);
static const uint64_t willrt_mem_block_dead = UINT64_C(0x44454144424C4F43);
static const uint64_t willrt_plate_magic = UINT64_C(0x504C4154454D454D);
static const uint64_t willrt_mem_chunk_magic = UINT64_C(0x4348554E4B4D454D);

static const size_t willrt_mem_class_size[WILLRT_MEM_CLASS_COUNT] = {
    32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384
};

/* A spin lock is enough for the short critical sections here: a free list pop
   or push, a bump of one chunk, and a registry update. */
typedef struct willrt_mem_lock {
    atomic_int held;
} willrt_mem_lock;

struct willrt_mem_chunk {
    uint64_t magic;
    void* os_base;
    size_t os_bytes;
    size_t bytes;
    size_t bump;
    int32_t owner;
    int32_t class_index;
};

struct willrt_mem_block {
    uint64_t magic;
    size_t capacity;
    struct willrt_mem_chunk* chunk;
    int32_t class_index;
};

struct willrt_mem_lane {
    willrt_mem_lock lock;
    struct willrt_mem_chunk* partial[WILLRT_MEM_CLASS_COUNT];
    void* free_list[WILLRT_MEM_CLASS_COUNT];
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

static struct willrt_mem_lane willrt_mem_lanes[WILLRT_MEM_LANES];

/* The registry has one entry per live chunk. An entry with base 0 is free and
   an entry with base 1 is a tombstone left by a removed chunk. */
struct willrt_mem_registry_entry {
    uintptr_t base;
    struct willrt_mem_chunk* chunk;
};

static struct willrt_mem_registry_entry
    willrt_mem_registry[WILLRT_MEM_REGISTRY_BUCKETS];
static willrt_mem_lock willrt_mem_registry_lock;
static atomic_size_t willrt_mem_chunks_held;
static atomic_size_t willrt_mem_page_calls_issued;
static _Atomic int willrt_mem_shared;

/* A thread that never runs under the scheduler stays on lane 0. */
static _Thread_local int32_t willrt_mem_lane_index;

/* Small blocks freed by this thread wait here before they return to a lane.
   The payload's first word is the next link, exactly as on a lane free list. */
struct willrt_mem_cache {
    void* free_list[WILLRT_MEM_CLASS_COUNT];
    unsigned count[WILLRT_MEM_CLASS_COUNT];
};

static _Thread_local struct willrt_mem_cache willrt_mem_cache;

/* The last small chunk this thread touched. Small chunks never return to the
   system, so the pointer stays valid and a hot free avoids the registry lock. */
static _Thread_local struct willrt_mem_chunk* willrt_mem_last_small;

void willrt_mem_set_shared(void) {
    atomic_store_explicit(&willrt_mem_shared, 1, memory_order_release);
}

static int willrt_mem_single_thread(void) {
    return !atomic_load_explicit(&willrt_mem_shared, memory_order_acquire);
}

static void willrt_mem_lock_acquire(willrt_mem_lock* lock) {
    /* Before the first worker exists there is no second owner. The flag is
       published before a worker starts, so this branch can only be taken while
       one thread owns the arena. */
    if (willrt_mem_single_thread()) {
        return;
    }
    while (atomic_exchange_explicit(&lock->held, 1, memory_order_acquire)) {
        while (atomic_load_explicit(&lock->held, memory_order_relaxed)) {
        }
    }
}

static void willrt_mem_lock_release(willrt_mem_lock* lock) {
    if (willrt_mem_single_thread()) {
        return;
    }
    atomic_store_explicit(&lock->held, 0, memory_order_release);
}

void willrt_mem_set_lane(int32_t lane) {
    if (lane < 0) {
        lane = 0;
    }
    willrt_mem_lane_index = lane % WILLRT_MEM_LANES;
}

/* The three helpers below move small payloads between the thread cache and
   the current lane. The first word of a cached payload is the intrusive link;
   a cached block is dead and is marked live by the allocation that pops it. */
static void* willrt_mem_cache_pop(int class_index) {
    void* payload = willrt_mem_cache.free_list[class_index];

    if (payload == NULL) {
        return NULL;
    }
    willrt_mem_cache.free_list[class_index] = *(void**)payload;
    willrt_mem_cache.count[class_index]--;
    return payload;
}

static void willrt_mem_cache_push(int class_index, void* payload) {
    *(void**)payload = willrt_mem_cache.free_list[class_index];
    willrt_mem_cache.free_list[class_index] = payload;
    willrt_mem_cache.count[class_index]++;
}

static void willrt_mem_cache_flush_class(int class_index) {
    struct willrt_mem_lane* lane = &willrt_mem_lanes[willrt_mem_lane_index];
    void* payload;

    if (willrt_mem_cache.count[class_index] == 0) {
        return;
    }
    willrt_mem_lock_acquire(&lane->lock);
    while ((payload = willrt_mem_cache_pop(class_index)) != NULL) {
        *(void**)payload = lane->free_list[class_index];
        lane->free_list[class_index] = payload;
    }
    willrt_mem_lock_release(&lane->lock);
}

/* willrt_mem_thread_flush returns this thread's cached blocks to its lane.
   Every runtime worker calls it before the thread exits; a raw C thread that
   uses the allocator and never calls it leaves only its own cache behind. */
void willrt_mem_thread_flush(void) {
    int class_index;

    for (class_index = 0; class_index < WILLRT_MEM_CLASS_COUNT; class_index++) {
        willrt_mem_cache_flush_class(class_index);
    }
    willrt_mem_last_small = NULL;
}

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

static size_t willrt_mem_registry_hash(uintptr_t base) {
    uint64_t key = (uint64_t)(base >> 16);

    key *= UINT64_C(11400714819323198485);
    return (size_t)(key >> 52) & (WILLRT_MEM_REGISTRY_BUCKETS - 1);
}

static void willrt_mem_registry_insert(uintptr_t base,
                                       struct willrt_mem_chunk* chunk) {
    size_t index;
    size_t first_tombstone = SIZE_MAX;
    size_t probes;

    willrt_mem_lock_acquire(&willrt_mem_registry_lock);
    index = willrt_mem_registry_hash(base);
    for (probes = 0; probes < WILLRT_MEM_REGISTRY_BUCKETS; probes++) {
        struct willrt_mem_registry_entry* entry = &willrt_mem_registry[index];

        if (entry->base == 0) {
            if (first_tombstone != SIZE_MAX) {
                entry = &willrt_mem_registry[first_tombstone];
            }
            entry->base = base;
            entry->chunk = chunk;
            willrt_mem_lock_release(&willrt_mem_registry_lock);
            return;
        }
        if (entry->base == 1 && first_tombstone == SIZE_MAX) {
            first_tombstone = index;
        }
        index = (index + 1) & (WILLRT_MEM_REGISTRY_BUCKETS - 1);
    }
    /* The registry is sized far above the chunk count of a real program. If it
       ever fills, the chunk stays unreachable and the allocation fails clean. */
    willrt_mem_lock_release(&willrt_mem_registry_lock);
}

static struct willrt_mem_chunk* willrt_mem_registry_find(uintptr_t base) {
    size_t index;
    size_t probes;

    willrt_mem_lock_acquire(&willrt_mem_registry_lock);
    index = willrt_mem_registry_hash(base);
    for (probes = 0; probes < WILLRT_MEM_REGISTRY_BUCKETS; probes++) {
        struct willrt_mem_registry_entry* entry = &willrt_mem_registry[index];

        if (entry->base == 0) {
            break;
        }
        if (entry->base == base) {
            struct willrt_mem_chunk* chunk = entry->chunk;

            willrt_mem_lock_release(&willrt_mem_registry_lock);
            return chunk;
        }
        index = (index + 1) & (WILLRT_MEM_REGISTRY_BUCKETS - 1);
    }
    willrt_mem_lock_release(&willrt_mem_registry_lock);
    return NULL;
}

static void willrt_mem_registry_remove(uintptr_t base) {
    size_t index;
    size_t probes;

    willrt_mem_lock_acquire(&willrt_mem_registry_lock);
    index = willrt_mem_registry_hash(base);
    for (probes = 0; probes < WILLRT_MEM_REGISTRY_BUCKETS; probes++) {
        struct willrt_mem_registry_entry* entry = &willrt_mem_registry[index];

        if (entry->base == 0) {
            break;
        }
        if (entry->base == base) {
            entry->base = 1;
            entry->chunk = NULL;
            break;
        }
        index = (index + 1) & (WILLRT_MEM_REGISTRY_BUCKETS - 1);
    }
    willrt_mem_lock_release(&willrt_mem_registry_lock);
}

static struct willrt_mem_chunk* willrt_mem_chunk_containing(const void* ptr) {
    uintptr_t base = (uintptr_t)ptr & ~(uintptr_t)(WILLRT_MEM_CHUNK_BYTES - 1);
    struct willrt_mem_chunk* chunk;

    if (base == 0) {
        return NULL;
    }
    if (willrt_mem_last_small != NULL
        && (uintptr_t)willrt_mem_last_small == base) {
        return willrt_mem_last_small;
    }
    chunk = willrt_mem_registry_find(base);
    if (chunk != NULL && chunk->class_index >= 0) {
        willrt_mem_last_small = chunk;
    }
    return chunk;
}

/* Reports whether the whole range lies in one chunk. The check uses only
   address arithmetic, so it is safe for a pointer the arena does not own. */
static int willrt_mem_range_contained(const void* ptr, size_t size) {
    uintptr_t start = (uintptr_t)ptr;
    struct willrt_mem_chunk* chunk = willrt_mem_chunk_containing(ptr);

    if (chunk == NULL || start > UINTPTR_MAX - size) {
        return 0;
    }
    if (start < (uintptr_t)chunk || start - (uintptr_t)chunk > chunk->bytes) {
        return 0;
    }
    return size <= chunk->bytes - (start - (uintptr_t)chunk);
}

/* Creates a chunk of bytes usable bytes on one lane. The caller holds that
   lane's lock for a small chunk; a large chunk is created without it. */
static struct willrt_mem_chunk* willrt_mem_chunk_new(int32_t lane, size_t bytes,
                                                     int32_t class_index) {
    struct willrt_mem_chunk* chunk;
    void* os_base;
    uintptr_t base;

    if (bytes < willrt_mem_chunk_start + willrt_mem_block_offset
                    + WILLRT_MEM_CLASS_MIN) {
        return NULL;
    }
    if (bytes > SIZE_MAX - WILLRT_MEM_CHUNK_BYTES) {
        return NULL;
    }
    atomic_fetch_add_explicit(&willrt_mem_page_calls_issued, 1,
                              memory_order_relaxed);
    os_base = willrt_os_alloc_pages(bytes + WILLRT_MEM_CHUNK_BYTES);
    if (os_base == NULL) {
        return NULL;
    }
    base = ((uintptr_t)os_base + WILLRT_MEM_CHUNK_BYTES - 1)
           & ~(uintptr_t)(WILLRT_MEM_CHUNK_BYTES - 1);
    chunk = (struct willrt_mem_chunk*)base;
    chunk->magic = willrt_mem_chunk_magic;
    chunk->os_base = os_base;
    chunk->os_bytes = bytes + WILLRT_MEM_CHUNK_BYTES;
    chunk->bytes = bytes;
    chunk->bump = willrt_mem_chunk_start;
    chunk->owner = lane;
    chunk->class_index = class_index;
    willrt_mem_registry_insert(base, chunk);
    if (willrt_mem_registry_find(base) != chunk) {
        willrt_os_free_pages(os_base, chunk->os_bytes);
        return NULL;
    }
    atomic_fetch_add_explicit(&willrt_mem_chunks_held, 1, memory_order_relaxed);
    return chunk;
}

static void* willrt_mem_small_alloc(struct willrt_mem_lane* lane,
                                    int class_index) {
    int32_t lane_index = (int32_t)(lane - willrt_mem_lanes);
    size_t capacity = willrt_mem_class_size[class_index];
    size_t stride = willrt_mem_align_up(willrt_mem_block_offset + capacity,
                                        _Alignof(max_align_t));
    struct willrt_mem_chunk* chunk = lane->partial[class_index];
    struct willrt_mem_block* block;
    unsigned char* cursor;
    void* payload = lane->free_list[class_index];

    if (payload != NULL) {
        lane->free_list[class_index] = *(void**)payload;
        block = (struct willrt_mem_block*)((unsigned char*)payload
                                           - willrt_mem_block_offset);
        block->magic = willrt_mem_block_live;
        block->capacity = capacity;
        return payload;
    }
    if (chunk == NULL || stride > chunk->bytes - chunk->bump) {
        chunk = willrt_mem_chunk_new(lane_index, WILLRT_MEM_CHUNK_BYTES,
                                     class_index);
        if (chunk == NULL) {
            return NULL;
        }
        lane->partial[class_index] = chunk;
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

static void* willrt_mem_large_alloc(int32_t lane, size_t size) {
    struct willrt_mem_chunk* chunk;
    struct willrt_mem_block* block;
    size_t need;
    size_t bytes;

    if (size > SIZE_MAX - willrt_mem_chunk_start - willrt_mem_block_offset) {
        return NULL;
    }
    need = willrt_mem_chunk_start + willrt_mem_block_offset + size;
    bytes = willrt_mem_align_up(need, WILLRT_MEM_CHUNK_BYTES);
    if (bytes == 0) {
        return NULL;
    }
    chunk = willrt_mem_chunk_new(lane, bytes, -1);
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
    struct willrt_mem_lane* lane;
    int class_index;
    void* result;
    int refill;

    if (size == 0) {
        size = 1;
    }
    lane = &willrt_mem_lanes[willrt_mem_lane_index];
    class_index = willrt_mem_class_for(size);
    if (class_index < 0) {
        return willrt_mem_large_alloc(willrt_mem_lane_index, size);
    }
    result = willrt_mem_cache_pop(class_index);
    if (result != NULL) {
        struct willrt_mem_block* block =
            (struct willrt_mem_block*)((unsigned char*)result
                                       - willrt_mem_block_offset);

        block->magic = willrt_mem_block_live;
        block->capacity = willrt_mem_class_size[class_index];
        return result;
    }

    /* One lock covers a refill batch rather than one allocation. */
    willrt_mem_lock_acquire(&lane->lock);
    for (refill = 0; refill < WILLRT_MEM_CACHE_REFILL; refill++) {
        void* payload = lane->free_list[class_index];

        if (payload == NULL) {
            break;
        }
        lane->free_list[class_index] = *(void**)payload;
        willrt_mem_cache_push(class_index, payload);
    }
    if (willrt_mem_cache.count[class_index] == 0) {
        result = willrt_mem_small_alloc(lane, class_index);
    } else {
        result = willrt_mem_cache_pop(class_index);
        {
            struct willrt_mem_block* block =
                (struct willrt_mem_block*)((unsigned char*)result
                                           - willrt_mem_block_offset);

            block->magic = willrt_mem_block_live;
            block->capacity = willrt_mem_class_size[class_index];
        }
    }
    willrt_mem_lock_release(&lane->lock);
    return result;
}

void willrt_free(void* ptr) {
    unsigned char* payload = (unsigned char*)ptr;
    struct willrt_mem_chunk* chunk;
    struct willrt_mem_lane* lane;
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
        void* os_base = chunk->os_base;
        size_t os_bytes = chunk->os_bytes;

        lane = &willrt_mem_lanes[chunk->owner % WILLRT_MEM_LANES];
        willrt_mem_lock_acquire(&lane->lock);
        block->magic = willrt_mem_block_dead;
        willrt_mem_registry_remove((uintptr_t)chunk);
        atomic_fetch_sub_explicit(&willrt_mem_chunks_held, 1,
                                  memory_order_relaxed);
        willrt_os_free_pages(os_base, os_bytes);
        willrt_mem_lock_release(&lane->lock);
        return;
    }

    /* A small block belongs to the freeing thread's cache first, even when its
       chunk belongs to another lane. That keeps the common free path lock-free;
       the cache is flushed to a lane when it is full or the thread exits. */
    block->magic = willrt_mem_block_dead;
    willrt_mem_cache_push(block->class_index, payload);
    if (willrt_mem_cache.count[block->class_index] > WILLRT_MEM_CACHE_LIMIT) {
        willrt_mem_cache_flush_class(block->class_index);
    }
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
    return atomic_load_explicit(&willrt_mem_chunks_held, memory_order_relaxed);
}

size_t willrt_mem_page_calls(void) {
    return atomic_load_explicit(&willrt_mem_page_calls_issued,
                                memory_order_relaxed);
}
