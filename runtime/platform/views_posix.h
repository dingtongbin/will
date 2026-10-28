/* POSIX multi-view mapping helper shared by the four Unix platform files.

   A small temporary file is truncated to one view size, then the same file is
   mapped at four addresses selected by the GC color range. The helper never
   falls back to one view; a platform that cannot provide four aliases returns
   an error and the caller reports it. */

#ifndef WILLRT_PLATFORM_VIEWS_POSIX_H
#define WILLRT_PLATFORM_VIEWS_POSIX_H

#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#ifndef MAP_FIXED_NOREPLACE
#define MAP_FIXED_NOREPLACE 0
#endif

typedef struct willrt_posix_gc_views {
    int fd;
    void* views[WILLRT_GC_VIEW_COUNT];
} willrt_posix_gc_views;

static int32_t willrt_posix_views_open(willrt_os_gc_views* views, size_t size,
                                       size_t stride) {
    willrt_posix_gc_views* state;
    const char* temp;
    char path[256];
    uintptr_t candidate;
    int32_t color;

    if (views == NULL || size == 0 || stride < size ||
        (stride & (stride - 1u)) != 0 || size > (size_t)INT64_MAX) {
        return -1;
    }
    state = (willrt_posix_gc_views*)views->state;
    memset(state, 0, sizeof(*state));
    state->fd = -1;
    temp = getenv("TMPDIR");
    if (temp == NULL || temp[0] == '\0') {
        temp = "/tmp";
    }
    if (snprintf(path, sizeof(path), "%s/will-gc-XXXXXX", temp) < 0) {
        return -1;
    }
    state->fd = mkstemp(path);
    if (state->fd < 0) {
        return -1;
    }
    unlink(path);
    if (ftruncate(state->fd, (off_t)size) != 0) {
        close(state->fd);
        state->fd = -1;
        return -1;
    }
    for (candidate = UINT64_C(0x10000000000);
         candidate < UINT64_C(0x700000000000);
         candidate += (uintptr_t)stride * WILLRT_GC_VIEW_COUNT) {
        for (color = 0; color < (int32_t)WILLRT_GC_VIEW_COUNT; color++) {
            void* wanted =
                (void*)(candidate + (uintptr_t)color * stride);
            void* mapped = mmap(wanted, size, PROT_READ | PROT_WRITE,
                                MAP_SHARED | MAP_FIXED_NOREPLACE, state->fd, 0);

            if (mapped != wanted) {
                if (mapped != MAP_FAILED) {
                    munmap(mapped, size);
                }
                break;
            }
            state->views[color] = mapped;
        }
        if (color == (int32_t)WILLRT_GC_VIEW_COUNT) {
            views->base = (void*)candidate;
            views->size = size;
            views->stride = stride;
            return 0;
        }
        while (--color >= 0) {
            munmap(state->views[color], size);
            state->views[color] = NULL;
        }
    }
    close(state->fd);
    state->fd = -1;
    return -1;
}

static void willrt_posix_views_close(willrt_os_gc_views* views) {
    willrt_posix_gc_views* state;
    int32_t color;

    if (views == NULL) {
        return;
    }
    state = (willrt_posix_gc_views*)views->state;
    for (color = 0; color < (int32_t)WILLRT_GC_VIEW_COUNT; color++) {
        if (state->views[color] != NULL) {
            munmap(state->views[color], views->size);
            state->views[color] = NULL;
        }
    }
    if (state->fd >= 0) {
        close(state->fd);
        state->fd = -1;
    }
    views->base = NULL;
    views->size = 0;
    views->stride = 0;
}

#endif
