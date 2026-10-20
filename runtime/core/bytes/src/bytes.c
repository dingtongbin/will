/* Byte-view search. The views are not terminated, so every step checks the
   remaining length before reading; an empty needle is found at offset zero. */

#include "../../../include/willrt.h"

#include <string.h>

int64_t willrt_bytes_index(const uint8_t* hay, size_t hay_len,
                           const uint8_t* needle, size_t needle_len) {
    size_t offset;

    if (needle_len == 0) {
        return 0;
    }
    if (hay == NULL || needle == NULL || needle_len > hay_len) {
        return -1;
    }
    for (offset = 0; offset + needle_len <= hay_len; offset++) {
        if (memcmp(hay + offset, needle, needle_len) == 0) {
            return (int64_t)offset;
        }
    }
    return -1;
}
