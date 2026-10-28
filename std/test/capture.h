#ifndef WILL_TEST_CAPTURE_H
#define WILL_TEST_CAPTURE_H

#include <stdint.h>

/* willtest_capture_begin redirects standard output and standard error to a
   temporary file and returns 0, or -1 when a capture is already active. */
int32_t willtest_capture_begin(void);

/* willtest_capture_end restores the original streams and keeps the temporary
   file readable. It returns 0, or -1 when no capture is active. */
int32_t willtest_capture_end(void);

/* willtest_capture_read fills a will str with everything captured so far. It
   returns 0, or -1 when no capture is active or the read fails. */
int32_t willtest_capture_read(void* out_raw);

/* willtest_capture_close releases the temporary file. */
void willtest_capture_close(void);

/* willtest_now_ns returns the monotonic clock in nanoseconds. */
int64_t willtest_now_ns(void);

#endif
