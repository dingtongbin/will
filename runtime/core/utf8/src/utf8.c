/* UTF-8 scalar decoding for str indexing and iteration.
 *
 * The decoder is total: every byte sequence either yields one scalar and its
 * byte length, or returns zero. It rejects truncated sequences, overlong
 * encodings, surrogates, and values above U+10FFFF, so the lowering can map
 * zero to the fixed "invalid UTF-8" panic without a second check.
 */

#include "utf8.h"

int32_t willrt_utf8_decode(const uint8_t* data, size_t len, uint32_t* out) {
    uint32_t cp;
    int32_t n;
    int32_t i;

    if (data == NULL || out == NULL || len == 0) {
        return 0;
    }
    if (data[0] < 0x80) {
        *out = data[0];
        return 1;
    }
    if ((data[0] & 0xE0) == 0xC0) {
        n = 2;
        cp = (uint32_t)(data[0] & 0x1F);
    } else if ((data[0] & 0xF0) == 0xE0) {
        n = 3;
        cp = (uint32_t)(data[0] & 0x0F);
    } else if ((data[0] & 0xF8) == 0xF0) {
        n = 4;
        cp = (uint32_t)(data[0] & 0x07);
    } else {
        return 0;
    }
    if (len < (size_t)n) {
        return 0;
    }
    for (i = 1; i < n; i++) {
        if ((data[i] & 0xC0) != 0x80) {
            return 0;
        }
        cp = (cp << 6) | (uint32_t)(data[i] & 0x3F);
    }
    if (n == 2 && cp < 0x80) {
        return 0;
    }
    if (n == 3 && (cp < 0x800 || (cp >= 0xD800 && cp <= 0xDFFF))) {
        return 0;
    }
    if (n == 4 && (cp < 0x10000 || cp > 0x10FFFF)) {
        return 0;
    }
    *out = cp;
    return n;
}

int32_t willrt_utf8_valid(const uint8_t* data, size_t len) {
    size_t offset = 0;

    while (offset < len) {
        uint32_t scalar = 0;
        int32_t n = willrt_utf8_decode(data + offset, len - offset, &scalar);

        if (n <= 0) {
            return 0;
        }
        offset += (size_t)n;
    }
    return 1;
}

int32_t willrt_utf8_encode(uint32_t value, uint8_t* out) {
    if (out == NULL) {
        return 0;
    }
    if (value <= 0x7f) {
        out[0] = (uint8_t)value;
        return 1;
    }
    if (value <= 0x7ff) {
        out[0] = (uint8_t)(0xc0 | (value >> 6));
        out[1] = (uint8_t)(0x80 | (value & 0x3f));
        return 2;
    }
    if (value >= 0xd800 && value <= 0xdfff) {
        return 0;
    }
    if (value <= 0xffff) {
        out[0] = (uint8_t)(0xe0 | (value >> 12));
        out[1] = (uint8_t)(0x80 | ((value >> 6) & 0x3f));
        out[2] = (uint8_t)(0x80 | (value & 0x3f));
        return 3;
    }
    if (value <= 0x10ffff) {
        out[0] = (uint8_t)(0xf0 | (value >> 18));
        out[1] = (uint8_t)(0x80 | ((value >> 12) & 0x3f));
        out[2] = (uint8_t)(0x80 | ((value >> 6) & 0x3f));
        out[3] = (uint8_t)(0x80 | (value & 0x3f));
        return 4;
    }
    return 0;
}
