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

static atomic_int willrt_started;

void willrt_init(void) {
    int expected = 0;
    atomic_compare_exchange_strong(&willrt_started, &expected, 1);
}

void willrt_shutdown(void) {
    atomic_store(&willrt_started, 0);
}
