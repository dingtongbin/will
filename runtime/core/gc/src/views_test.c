/* Multi-view mapping test: four virtual views of one backing store must
   contain the same bytes and carry the four color values in their addresses. */

#include "willrt.h"

#include <assert.h>
#include <stdint.h>

int main(void) {
    willrt_os_gc_views views;
    size_t size = 64u * 1024u;
    uintptr_t base;
    int32_t color;

    assert(willrt_os_gc_views_open(&views, size,
                                   (size_t)WILLRT_GC_VIEW_STRIDE) == 0);
    base = (uintptr_t)views.base;
    assert(base != 0);
    assert((base & WILLRT_GC_COLOR_MASK) == WILLRT_GC_COLOR_YOUNG);
    for (color = 0; color < (int32_t)WILLRT_GC_VIEW_COUNT; color++) {
        uintptr_t address = base + (uintptr_t)color * views.stride;

        assert(WILLRT_GC_COLOR_OF((void*)address) == (uint32_t)color);
    }
    ((unsigned char*)views.base)[0] = 0x5a;
    assert(((unsigned char*)views.base + views.stride)[0] == 0x5a);
    ((unsigned char*)views.base + views.stride)[1] = 0xa5;
    assert(((unsigned char*)views.base)[1] == 0xa5);
    willrt_os_gc_views_close(&views);
    assert(views.base == NULL);
    return 0;
}
