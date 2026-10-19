/* Contract tests for willrt_alloc, willrt_free, willrt_plate_new, and
   willrt_plate_release. The process exits zero when every assertion holds and
   aborts through assert on the first failure. */

#include "alloc.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static void test_alloc_round_trip(void) {
    size_t size;
    unsigned char* block;

    for (size = 1; size <= 4096; size *= 2) {
        size_t i;
        block = (unsigned char*)willrt_alloc(size);
        assert(block != NULL);
        for (i = 0; i < size; i++) {
            block[i] = (unsigned char)(i * 7u + size);
        }
        for (i = 0; i < size; i++) {
            assert(block[i] == (unsigned char)(i * 7u + size));
        }
        willrt_free(block);
    }

    block = (unsigned char*)willrt_alloc(0);
    assert(block != NULL);
    willrt_free(block);
    willrt_free(NULL);
}

static void test_alloc_alignment(void) {
    void* block = willrt_alloc(1);
    assert(block != NULL);
    assert(((uintptr_t)block % _Alignof(max_align_t)) == 0);
    willrt_free(block);
}

/* A block above the largest size class comes from its own page chunk. Writing
   every byte of it proves the chunk is mapped for the full request. */
static void test_alloc_page_backed(void) {
    const size_t size = 200 * 1024;
    size_t calls_before = willrt_mem_page_calls();
    size_t chunks_before = willrt_mem_chunk_count();
    unsigned char* block = (unsigned char*)willrt_alloc(size);
    size_t i;

    assert(block != NULL);
    assert(willrt_mem_chunk_count() == chunks_before + 1);
    for (i = 0; i < size; i++) {
        block[i] = (unsigned char)(i ^ (i >> 8));
    }
    for (i = 0; i < size; i++) {
        assert(block[i] == (unsigned char)(i ^ (i >> 8)));
    }
    assert(willrt_mem_page_calls() > calls_before);
    willrt_free(block);
    assert(willrt_mem_chunk_count() == chunks_before);
}

/* Repeated allocation and release of one size must stay on the free list and
   never ask the system for another page chunk. */
static void test_alloc_reuse(void) {
    const size_t size = 200;
    void* first = willrt_alloc(size);
    size_t calls_before;
    size_t chunks_before;
    size_t i;

    assert(first != NULL);
    willrt_free(first);
    calls_before = willrt_mem_page_calls();
    chunks_before = willrt_mem_chunk_count();
    for (i = 0; i < 1000; i++) {
        void* block = willrt_alloc(size);
        assert(block == first);
        memset(block, 0xA5, size);
        willrt_free(block);
    }
    assert(willrt_mem_page_calls() == calls_before);
    assert(willrt_mem_chunk_count() == chunks_before);
}

static void test_alloc_foreign(void) {
    unsigned char stack_block[64];
    unsigned char* block;

    willrt_free(stack_block + 8);
    block = (unsigned char*)willrt_alloc(128);
    assert(block != NULL);
    willrt_free(block + 8);
    willrt_free(block);
    willrt_free(block);
}

static void test_plate_write(void) {
    enum { kCount = 64 };
    const size_t elem = 16;
    size_t i;
    unsigned char* plate = (unsigned char*)willrt_plate_new(kCount, elem);

    assert(plate != NULL);
    memset(plate, 0xA5, (size_t)kCount * elem);
    for (i = 0; i < (size_t)kCount * elem; i++) {
        assert(plate[i] == 0xA5);
    }
    willrt_plate_release(plate);
}

static void test_plate_stride(void) {
    enum { kCount = 32 };
    const size_t elem = 12;
    size_t i;
    unsigned char* plate = (unsigned char*)willrt_plate_new(kCount, elem);

    assert(plate != NULL);
    for (i = 0; i < kCount; i++) {
        uint32_t value = (uint32_t)(0x01000000u + i);
        memcpy(plate + i * elem, &value, sizeof(value));
    }
    for (i = 0; i < kCount; i++) {
        uint32_t value = 0;
        memcpy(&value, plate + i * elem, sizeof(value));
        assert(value == (uint32_t)(0x01000000u + i));
    }
    willrt_plate_release(plate);
}

typedef struct {
    _Alignas(32) unsigned char bytes[32];
} willrt_wide_element;

static void test_plate_alignment(void) {
    void* plate = willrt_plate_new(4, sizeof(willrt_wide_element));
    assert(plate != NULL);
    assert(((uintptr_t)plate % 32) == 0);
    willrt_plate_release(plate);
}

static void test_plate_overflow(void) {
    assert(willrt_plate_new(SIZE_MAX, 2) == NULL);
    assert(willrt_plate_new(SIZE_MAX / 2 + 1, 2) == NULL);
    assert(willrt_plate_new(SIZE_MAX / 2, 2) == NULL);
    assert(willrt_plate_new(1, 0) == NULL);
    assert(willrt_alloc(SIZE_MAX) == NULL);
}

static void test_plate_reuse(void) {
    void* first = willrt_plate_new(128, 24);
    void* second;
    size_t calls_before;

    assert(first != NULL);
    willrt_plate_release(first);
    calls_before = willrt_mem_page_calls();
    second = willrt_plate_new(128, 24);
    assert(second == first);
    assert(willrt_mem_page_calls() == calls_before);
    willrt_plate_release(second);

    first = willrt_plate_new(0, 8);
    assert(first != NULL);
    willrt_plate_release(first);
    willrt_plate_release(NULL);
}

static void test_plate_misuse(void) {
    enum { kSize = 256 };
    unsigned char* foreign = (unsigned char*)willrt_alloc(kSize);
    unsigned char* plate;
    struct willrt_plate_header* header;
    uint64_t magic;

    assert(foreign != NULL);
    memset(foreign, 0, kSize);
    willrt_plate_release(foreign + 128);

    plate = (unsigned char*)willrt_plate_new(4, 8);
    assert(plate != NULL);
    header = (struct willrt_plate_header*)(plate - sizeof(*header));
    magic = header->magic;
    header->magic ^= UINT64_C(1);
    willrt_plate_release(plate);
    header->magic = magic;
    willrt_plate_release(plate);

    willrt_free(foreign);
}

static void test_plate_foreign(void) {
    unsigned char stack_block[64];

    willrt_plate_release(stack_block + 32);
}

int main(void) {
    test_alloc_round_trip();
    test_alloc_alignment();
    test_alloc_page_backed();
    test_alloc_reuse();
    test_alloc_foreign();
    test_plate_write();
    test_plate_stride();
    test_plate_alignment();
    test_plate_overflow();
    test_plate_reuse();
    test_plate_misuse();
    test_plate_foreign();
    return 0;
}
