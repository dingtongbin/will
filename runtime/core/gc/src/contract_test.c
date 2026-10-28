/* Contract tests for the shared GC header. The file exercises only the
   compile-time layout and the pointer-tag rules; it does not link a collector. */

#include "willrt_gc.h"

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

_Static_assert(sizeof(willrt_gc_header) == WILLRT_GC_OBJECT_HEADER_SIZE,
               "object header size");
_Static_assert(_Alignof(willrt_gc_header) == WILLRT_GC_OBJECT_ALIGN,
               "object header alignment");
_Static_assert(offsetof(willrt_gc_header, state) == 0,
               "object state offset");
_Static_assert(sizeof(willrt_gc_stack_slot) == 16,
               "stack slot size");
_Static_assert(offsetof(willrt_gc_stack_slot, type) == 8,
               "stack slot type offset");
_Static_assert(sizeof(willrt_gc_handle) == 8,
               "handle size");
_Static_assert(WILLRT_GC_COLOR_YOUNG == 0, "young color");
_Static_assert(WILLRT_GC_COLOR_MARKED0 == 1, "marked0 color");
_Static_assert(WILLRT_GC_COLOR_MARKED1 == 2, "marked1 color");
_Static_assert(WILLRT_GC_COLOR_REMAPPED == 3, "remapped color");
_Static_assert(WILLRT_GC_REGION_YOUNG == 0, "young region");
_Static_assert(WILLRT_GC_REGION_OLD == 1, "old region");
_Static_assert(WILLRT_GC_REGION_LARGE == 2, "large region");
_Static_assert(WILLRT_GC_REGION_PINNED == 3, "pinned region");
_Static_assert(WILLRT_GC_SAFEPOINT_ALLOC == 0, "allocation safepoint");
_Static_assert(WILLRT_GC_SAFEPOINT_AWAIT == 1, "await safepoint");
_Static_assert(WILLRT_GC_SAFEPOINT_BACKEDGE == 2, "backedge safepoint");
_Static_assert(WILLRT_GC_SAFEPOINT_CALL == 3, "call safepoint");
_Static_assert(WILLRT_GC_BARRIER_READ == 0, "read barrier");
_Static_assert(WILLRT_GC_BARRIER_WRITE == 1, "write barrier");
_Static_assert(WILLRT_GC_BARRIER_SATB == 2, "SATB barrier");
_Static_assert(WILLRT_GC_BARRIER_CARD == 3, "card barrier");

int main(void) {
    uintptr_t raw = (uintptr_t)0x1000u;
    void* tagged = (void*)(raw | WILLRT_GC_COLOR_MARKED0);

    assert(WILLRT_GC_COLOR_OF(tagged) == WILLRT_GC_COLOR_MARKED0);
    assert(WILLRT_GC_UNTAG(tagged) == (void*)raw);
    assert(WILLRT_GC_POINTER_MASK == ~(uintptr_t)WILLRT_GC_COLOR_MASK);
    assert(WILLRT_GC_OBJECT_PAYLOAD_OFFSET == sizeof(willrt_gc_header));
    assert(WILLRT_GC_HANDLE_NONE == 0);
    return 0;
}
