#ifndef WILLRT_IO_COMPLETION_H
#define WILLRT_IO_COMPLETION_H

#include <stdint.h>

/* One completion request. owner is the operation object; complete runs on the
   completion thread with status 0 or -1 and the result byte count. */
typedef struct willrt_io_req {
    void (*complete)(void* owner, int32_t status, int64_t result);
    void* owner;
} willrt_io_req;

#endif
