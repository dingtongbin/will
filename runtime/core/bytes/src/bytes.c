/* Byte-view search. The views are not terminated, so every step checks the
   remaining length before reading; an empty needle is found at offset zero. */

#include "../../../include/willrt.h"

#include <string.h>

static int willrt_bytes_space(uint8_t c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v'
           || c == '\f';
}

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

int64_t willrt_bytes_count(const uint8_t* hay, size_t hay_len,
                           const uint8_t* needle, size_t needle_len) {
    size_t offset = 0;
    int64_t count = 0;

    if (needle_len == 0 || hay == NULL || needle == NULL) {
        return 0;
    }
    while (offset + needle_len <= hay_len) {
        if (memcmp(hay + offset, needle, needle_len) == 0) {
            count++;
            offset += needle_len;
        } else {
            offset++;
        }
    }
    return count;
}

int64_t willrt_bytes_trim(const uint8_t* data, size_t len, int32_t left,
                          int32_t right, int64_t* start, int64_t* out_len) {
    size_t begin = 0;
    size_t end = len;

    if (data == NULL) {
        return 0;
    }
    if (left) {
        while (begin < end && willrt_bytes_space(data[begin])) {
            begin++;
        }
    }
    if (right) {
        while (end > begin && willrt_bytes_space(data[end - 1])) {
            end--;
        }
    }
    if (start != NULL) {
        *start = (int64_t)begin;
    }
    if (out_len != NULL) {
        *out_len = (int64_t)(end - begin);
    }
    return (int64_t)(end - begin);
}

void willrt_bytes_repeat(uint8_t* dst, const uint8_t* src, size_t len,
                         size_t count) {
    size_t i;

    if (dst == NULL || src == NULL) {
        return;
    }
    for (i = 0; i < count; i++) {
        memcpy(dst + i * len, src, len);
    }
}

int64_t willrt_bytes_replace_into(uint8_t* dst, size_t cap, const uint8_t* data,
                                  size_t len, const uint8_t* old,
                                  size_t old_len, const uint8_t* new_text,
                                  size_t new_len) {
    size_t in = 0;
    size_t out = 0;

    if (old_len == 0) {
        if (cap < len) {
            return -1;
        }
        memcpy(dst, data, len);
        return (int64_t)len;
    }
    while (in < len) {
        if (in + old_len <= len && memcmp(data + in, old, old_len) == 0) {
            if (out + new_len > cap) {
                return -1;
            }
            memcpy(dst + out, new_text, new_len);
            out += new_len;
            in += old_len;
        } else {
            if (out == cap) {
                return -1;
            }
            dst[out++] = data[in++];
        }
    }
    return (int64_t)out;
}

/* Writes each part as a pointer and a length. A null out just counts. */
int64_t willrt_bytes_split(const uint8_t* data, size_t len, const uint8_t* sep,
                           size_t sep_len, uint64_t* out, size_t cap) {
    size_t start = 0;
    size_t offset = 0;
    size_t count = 0;

    if (sep_len == 0) {
        if (out != NULL && cap > 0) {
            out[0] = (uint64_t)(uintptr_t)data;
            out[1] = (uint64_t)len;
        }
        return 1;
    }
    while (offset <= len) {
        if (offset + sep_len <= len
            && memcmp(data + offset, sep, sep_len) == 0) {
            if (out != NULL && count < cap) {
                out[count * 2] = (uint64_t)(uintptr_t)(data + start);
                out[count * 2 + 1] = (uint64_t)(offset - start);
            }
            count++;
            offset += sep_len;
            start = offset;
        } else {
            offset++;
        }
    }
    if (out != NULL && count < cap) {
        out[count * 2] = (uint64_t)(uintptr_t)(data + start);
        out[count * 2 + 1] = (uint64_t)(len - start);
    }
    return (int64_t)(count + 1);
}

int64_t willrt_bytes_fields(const uint8_t* data, size_t len, uint64_t* out,
                            size_t cap) {
    size_t offset = 0;
    size_t count = 0;

    while (offset < len) {
        size_t start;

        while (offset < len && willrt_bytes_space(data[offset])) {
            offset++;
        }
        start = offset;
        while (offset < len && !willrt_bytes_space(data[offset])) {
            offset++;
        }
        if (offset > start) {
            if (out != NULL && count < cap) {
                out[count * 2] = (uint64_t)(uintptr_t)(data + start);
                out[count * 2 + 1] = (uint64_t)(offset - start);
            }
            count++;
        }
    }
    return (int64_t)count;
}
