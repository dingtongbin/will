/* The random-byte entry point. The platform layer owns the system source; the
   core only forwards the request so will code does not depend on a C library
   name that differs by platform. */

#include "../../../include/willrt.h"

int32_t willrt_random_bytes(void* out, size_t size) {
    return willrt_os_random_bytes(out, size);
}
