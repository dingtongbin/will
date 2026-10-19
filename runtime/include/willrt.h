/* Public runtime interface. Every entry point a compiled program calls from
   the runtime carries the willrt_ prefix; this header is the only place that
   names them. */

#ifndef WILLRT_WILLRT_H
#define WILLRT_WILLRT_H

#include <stddef.h>

/* Status values for willrt_ functions that report int32_t. Negative values
   mean the operation did not complete, except WILLRT_SUSPEND, which means the
   caller is expected to try again after the task runs. */
enum {
    WILLRT_OK = 0,
    WILLRT_DONE = 1,
    WILLRT_SUSPEND = -1,
    WILLRT_EMPTY = -2,
    WILLRT_FULL = -3,
    WILLRT_CLOSED = -4,
    WILLRT_CANCELED = -5,
    WILLRT_TIMEDOUT = -6,
    WILLRT_FAILED = -7
};

/* willrt_os_alloc_pages returns at least size bytes of page-aligned,
   zero-filled memory, or NULL on failure. size is rounded up to the platform
   page size, so callers may ask for any byte count. */
void* willrt_os_alloc_pages(size_t size);

/* willrt_os_free_pages releases a block from willrt_os_alloc_pages. size is
   the value passed to that call. NULL is accepted. */
void willrt_os_free_pages(void* ptr, size_t size);

/* willrt_alloc returns a block aligned for any type, or NULL on failure. A
   zero-size request still returns a unique block, so an empty buffer is never
   mistaken for an allocation failure. */
void* willrt_alloc(size_t size);

/* willrt_free releases a block from willrt_alloc. NULL is accepted. A pointer
   that willrt_alloc did not return is rejected with one line on standard
   error, and its memory is left alone. */
void willrt_free(void* ptr);

/* willrt_plate_new returns one block holding count elements of elem bytes, or
   NULL when the request overflows or memory is exhausted. The payload starts
   on a boundary of max(elem, alignof(max_align_t)), rounded up to a power of
   two so the arena can hold the address without lowering the alignment. */
void* willrt_plate_new(size_t count, size_t elem);

/* willrt_plate_release returns a whole plate from willrt_plate_new. NULL is
   accepted. A pointer outside the arena, or a header that does not carry the
   plate magic, is rejected with one line on standard error, and its memory is
   left alone. */
void willrt_plate_release(void* plate);

#endif
