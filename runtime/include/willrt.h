/* Public runtime interface. Every entry point a compiled program calls from
   the runtime carries the willrt_ prefix; this header is the only place that
   names them. */

#ifndef WILLRT_WILLRT_H
#define WILLRT_WILLRT_H

#include <stddef.h>
#include <stdint.h>

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

/* willrt_os_now_ns returns the platform's monotonic clock in nanoseconds. It
   never goes backwards. Zero is returned when the platform call fails. */
int64_t willrt_os_now_ns(void);

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

/* willrt_rc_new copies size bytes from value into a new rc object and returns
   its handle, or 0 when the allocation fails. A handle carries the slot index
   in the high 32 bits and the slot generation in the low 32 bits, and 0 is
   never valid. value must point to size bytes when size is nonzero. */
uint64_t willrt_rc_new(const void* value, size_t size);

/* willrt_rc_retain adds one reference to the object named by handle. A zero,
   out-of-range, or released handle is ignored, so a stale handle never
   faults. */
void willrt_rc_retain(uint64_t handle);

/* willrt_rc_release removes one reference from the object named by handle and
   releases its storage when the count reaches zero. A zero, out-of-range, or
   released handle is ignored. */
void willrt_rc_release(uint64_t handle);

/* willrt_rc_deref returns the object named by handle after checking the slot
   generation, or NULL when the handle is zero, out of range, or released. The
   lookup is O(1). The caller must own a reference for as long as the result is
   used. */
void* willrt_rc_deref(uint64_t handle);

/* willrt_weak_new returns a weak handle for a live rc handle, or 0 when the
   handle is not live. A weak handle does not change the strong count and does
   not keep the slot from being reused; it is encoded exactly like a strong
   handle, so it must not be passed to willrt_rc_retain or
   willrt_rc_release. */
uint64_t willrt_weak_new(uint64_t handle);

/* willrt_weak_upgrade writes the strong handle to *(uint64_t*)out and returns
   WILLRT_OK when the weak handle still names a live object. It returns
   WILLRT_EMPTY when the object was released or out is NULL. On success the
   strong count is incremented, so the caller owns the returned reference. */
int32_t willrt_weak_upgrade(uint64_t weak, void* out);

/* willrt_now_ns returns the monotonic clock in nanoseconds. It never goes
   backwards and is the time base for timer deadlines. */
int64_t willrt_now_ns(void);

/* willrt_timer is a one-shot timer. The node is owned by the runtime. */
typedef struct willrt_timer willrt_timer;

/* willrt_timer_new inserts a timer for the absolute monotonic deadline
   deadline_ns. fn is a void (*)(void*) that runs with arg after the deadline
   passes. Insertion is O(1) and returns NULL when node allocation fails. */
willrt_timer* willrt_timer_new(int64_t deadline_ns, void* fn, void* arg);

/* willrt_timer_cancel removes an armed timer in O(1). NULL and a timer that
   already fired are harmless. A cancelled or fired node stays valid until the
   next advance reclaims it; callers must not use the pointer after that. */
void willrt_timer_cancel(willrt_timer* timer);

#endif
