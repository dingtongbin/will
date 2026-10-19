/* Internal interface of the timer wheel. The public entry points are in
   willrt.h; the expiry step stays internal so that the run loop can drive it
   without widening the compiled-program interface. */

#ifndef WILLRT_TIMER_TIMER_H
#define WILLRT_TIMER_TIMER_H

#include "../../../include/willrt.h"

/* willrt_timer_advance expires every timer whose deadline is at or before
   now_ns, runs the callbacks in deadline order, and returns how many ran. */
size_t willrt_timer_advance(int64_t now_ns);

#ifdef WILLRT_TIMER_TEST
/* willrt_timer_test_advance is the test-only seam around the expiry step.
   Production builds do not define it. */
size_t willrt_timer_test_advance(int64_t now_ns);
#endif

#endif
