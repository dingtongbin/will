/* Monotonic clock for std/test. */
#include "clock.h"

#include <stdint.h>

int64_t willrt_now_ns(void);

int64_t willtest_now_ns(void) {
    return willrt_now_ns();
}
