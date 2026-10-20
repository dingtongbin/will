/* Contract tests for the UTF-8 decoder. */

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "utf8.h"

static int failures;

static void expect(const char* name, const uint8_t* data, size_t len, int32_t want_len, uint32_t want_cp) {
    uint32_t cp = 0xFFFFFFFFu;
    int32_t got = willrt_utf8_decode(data, len, &cp);
    if (got != want_len || (got != 0 && cp != want_cp)) {
        fprintf(stderr, "%s: got len=%d cp=U+%04X, want len=%d cp=U+%04X\n",
                name, (int)got, (unsigned)cp, (int)want_len, (unsigned)want_cp);
        failures++;
    }
}

int main(void) {
    static const uint8_t ascii[] = {'A'};
    static const uint8_t two[] = {0xC3, 0xA9};
    static const uint8_t three[] = {0xE2, 0x82, 0xAC};
    static const uint8_t four[] = {0xF0, 0x9F, 0x98, 0x80};
    static const uint8_t truncated[] = {0xE2, 0x82};
    static const uint8_t overlong[] = {0xC0, 0x80};
    static const uint8_t surrogate[] = {0xED, 0xA0, 0x80};
    static const uint8_t too_high[] = {0xF4, 0x90, 0x80, 0x80};
    static const uint8_t bad_continuation[] = {0xE2, 0x28, 0xA1};

    expect("ascii", ascii, sizeof(ascii), 1, 0x41);
    expect("two", two, sizeof(two), 2, 0xE9);
    expect("three", three, sizeof(three), 3, 0x20AC);
    expect("four", four, sizeof(four), 4, 0x1F600);
    expect("truncated", truncated, sizeof(truncated), 0, 0);
    expect("overlong", overlong, sizeof(overlong), 0, 0);
    expect("surrogate", surrogate, sizeof(surrogate), 0, 0);
    expect("too-high", too_high, sizeof(too_high), 0, 0);
    expect("bad-continuation", bad_continuation, sizeof(bad_continuation), 0, 0);
    expect("empty", ascii, 0, 0, 0);
    if (willrt_utf8_decode(ascii, sizeof(ascii), NULL) != 0) {
        fprintf(stderr, "null-out: accepted a NULL output\n");
        failures++;
    }
    if (!willrt_utf8_valid(three, sizeof(three))
        || !willrt_utf8_valid(four, sizeof(four))
        || willrt_utf8_valid(bad_continuation, sizeof(bad_continuation))
        || willrt_utf8_valid(truncated, sizeof(truncated))) {
        fprintf(stderr, "valid: wrong verdict\n");
        failures++;
    }
    {
        uint8_t buffer[4];
        int32_t size = willrt_utf8_encode(0x20ACu, buffer);

        if (size != 3 || memcmp(buffer, three, 3) != 0) {
            fprintf(stderr, "encode: euro sign is not E2 82 AC\n");
            failures++;
        }
        if (willrt_utf8_encode(0xD800u, buffer) != 0) {
            fprintf(stderr, "encode: accepted a surrogate\n");
            failures++;
        }
    }

    return failures == 0 ? 0 : 1;
}
