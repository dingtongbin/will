/* The runtime lifecycle hooks. The compiler's entry wrapper calls willrt_init
   before the program entry and willrt_shutdown after it returns. A host that
   embeds the runtime may run several programs in one process, so both hooks
   tolerate repeated calls.

   No runtime module needs process-wide state yet: the allocator, the slot
   table, the timer wheel, and the report target all prepare themselves on
   first use. Future setup that must happen once per process (fault handlers,
   the poller, the scheduler) belongs here rather than in a constructor. */

#include "../../../include/willrt.h"

#include <stdatomic.h>

#if defined(__GNUC__) || defined(__clang__)
/* Defined by the optional modules. A program that never starts a task or waits
   on IO does not link them, so every hook is weak. */
extern void willrt_tasks_wait_all(void) __attribute__((weak));
extern void willrt_poll_shutdown(void) __attribute__((weak));
extern void willrt_blocking_shutdown(void) __attribute__((weak));
extern void willrt_timer_shutdown(void) __attribute__((weak));
extern void willrt_mem_thread_flush(void) __attribute__((weak));
#endif

static atomic_int willrt_started;

void willrt_init(void) {
    int expected = 0;
    atomic_compare_exchange_strong(&willrt_started, &expected, 1);
}

void willrt_shutdown(void) {
    if (willrt_tasks_wait_all != NULL) {
        willrt_tasks_wait_all();
    }
    if (willrt_blocking_shutdown != NULL) {
        willrt_blocking_shutdown();
    }
    if (willrt_poll_shutdown != NULL) {
        willrt_poll_shutdown();
    }
    if (willrt_timer_shutdown != NULL) {
        willrt_timer_shutdown();
    }
    if (willrt_mem_thread_flush != NULL) {
        willrt_mem_thread_flush();
    }
    atomic_store(&willrt_started, 0);
}
