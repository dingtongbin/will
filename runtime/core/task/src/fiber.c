/* Fiber contexts for the task scheduler.
 *
 * Windows uses the native Fiber API. POSIX uses ucontext, whose stack is
 * allocated from the runtime allocator. Both forms keep the task's C stack, so
 * a blocking runtime call can park the task and resume it at the same
 * instruction without compiler-generated state.
 *
 * A task fiber is switched into by a lane scheduler fiber. The task yields with
 * willrt_os_fiber_switch(task, scheduler); the scheduler yields back with the
 * inverse switch after the task has been made ready again.
 */

#include "../../../include/willrt.h"

#include <stdint.h>
#include <stdlib.h>

#if defined(_WIN32)
#include <windows.h>

struct willrt_os_fiber {
    LPVOID handle;
    int32_t owned;
};

willrt_os_fiber* willrt_os_fiber_new(void (*fn)(void*), void* arg,
                                     size_t stack_size) {
    willrt_os_fiber* fiber;
    LPVOID handle;

    (void)stack_size;
    fiber = (willrt_os_fiber*)willrt_alloc(sizeof(*fiber));
    if (fiber == NULL) {
        return NULL;
    }
    handle = CreateFiber(0, (LPFIBER_START_ROUTINE)fn, arg);
    if (handle == NULL) {
        willrt_free(fiber);
        return NULL;
    }
    fiber->handle = handle;
    fiber->owned = 1;
    return fiber;
}

willrt_os_fiber* willrt_os_fiber_thread(void) {
    willrt_os_fiber* fiber;
    LPVOID handle;

    if (IsThreadAFiber()) {
        handle = GetCurrentFiber();
    } else {
        handle = ConvertThreadToFiber(NULL);
    }
    if (handle == NULL) {
        return NULL;
    }
    fiber = (willrt_os_fiber*)willrt_alloc(sizeof(*fiber));
    if (fiber == NULL) {
        return NULL;
    }
    fiber->handle = handle;
    fiber->owned = 0;
    return fiber;
}

void willrt_os_fiber_switch(willrt_os_fiber* from, willrt_os_fiber* to) {
    (void)from;
    if (to != NULL && to->handle != NULL) {
        SwitchToFiber(to->handle);
    }
}

void willrt_os_fiber_destroy(willrt_os_fiber* fiber) {
    if (fiber == NULL) {
        return;
    }
    if (fiber->owned && fiber->handle != NULL) {
        DeleteFiber(fiber->handle);
    }
    willrt_free(fiber);
}

#else

#include <ucontext.h>

struct willrt_os_fiber {
    ucontext_t ctx;
    void* stack;
    size_t stack_size;
};

typedef struct willrt_fiber_start {
    void (*fn)(void*);
    void* arg;
} willrt_fiber_start;

static void willrt_fiber_entry(uint32_t lo, uint32_t hi) {
    uintptr_t raw = ((uintptr_t)hi << 32) | (uintptr_t)lo;
    willrt_fiber_start* start = (willrt_fiber_start*)raw;
    void (*fn)(void*) = start->fn;
    void* arg = start->arg;

    willrt_free(start);
    fn(arg);
}

willrt_os_fiber* willrt_os_fiber_new(void (*fn)(void*), void* arg,
                                     size_t stack_size) {
    willrt_os_fiber* fiber;
    willrt_fiber_start* start;
    uintptr_t raw;

    if (stack_size < 16384) {
        stack_size = 16384;
    }
    fiber = (willrt_os_fiber*)willrt_alloc(sizeof(*fiber));
    start = (willrt_fiber_start*)willrt_alloc(sizeof(*start));
    if (fiber == NULL || start == NULL) {
        willrt_free(fiber);
        willrt_free(start);
        return NULL;
    }
    fiber->stack = willrt_alloc(stack_size);
    if (fiber->stack == NULL) {
        willrt_free(fiber);
        willrt_free(start);
        return NULL;
    }
    fiber->stack_size = stack_size;
    start->fn = fn;
    start->arg = arg;
    raw = (uintptr_t)start;
    if (getcontext(&fiber->ctx) != 0) {
        willrt_free(fiber->stack);
        willrt_free(fiber);
        willrt_free(start);
        return NULL;
    }
    fiber->ctx.uc_stack.ss_sp = fiber->stack;
    fiber->ctx.uc_stack.ss_size = stack_size;
    fiber->ctx.uc_link = NULL;
    makecontext(&fiber->ctx, (void (*)(void))willrt_fiber_entry, 2,
                (uint32_t)(raw & UINT32_MAX), (uint32_t)(raw >> 32));
    return fiber;
}

willrt_os_fiber* willrt_os_fiber_thread(void) {
    willrt_os_fiber* fiber = (willrt_os_fiber*)willrt_alloc(sizeof(*fiber));

    if (fiber == NULL) {
        return NULL;
    }
    fiber->stack = NULL;
    fiber->stack_size = 0;
    if (getcontext(&fiber->ctx) != 0) {
        willrt_free(fiber);
        return NULL;
    }
    return fiber;
}

void willrt_os_fiber_switch(willrt_os_fiber* from, willrt_os_fiber* to) {
    if (from != NULL && to != NULL) {
        swapcontext(&from->ctx, &to->ctx);
    }
}

void willrt_os_fiber_destroy(willrt_os_fiber* fiber) {
    if (fiber == NULL) {
        return;
    }
    if (fiber->stack != NULL) {
        willrt_free(fiber->stack);
    }
    willrt_free(fiber);
}

#endif
