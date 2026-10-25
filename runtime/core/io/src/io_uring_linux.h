#ifndef WILLRT_IO_URING_LINUX_H
#define WILLRT_IO_URING_LINUX_H

#include <stdint.h>

#include "io_completion.h"

/* start creates one completion ring and its polling thread. It returns 0 when
   the ring is available. */
int32_t willrt_io_ring_start(void);
int32_t willrt_io_ring_ready(void);

/* submit queues one read or write. kind is 0 read, 1 write, 2 append. The
   completion owns the file descriptor once submit returns 0. */
int32_t willrt_io_ring_submit(int fd, int32_t kind, void* buffer, int64_t len,
                              willrt_io_req* req);
void willrt_io_ring_stop(void);

#endif
