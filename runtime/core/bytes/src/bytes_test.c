/* Contract tests for byte-view search, split, fields, trim, and replace. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static void test_index(void) {
    static const uint8_t hay[] = "hello";
    static const uint8_t lo[] = "lo";
    static const uint8_t missing[] = "zz";

    assert(willrt_bytes_index(hay, 5, lo, 2) == 3);
    assert(willrt_bytes_index(hay, 5, missing, 2) == -1);
    assert(willrt_bytes_index(hay, 5, hay, 0) == 0);
}

static void test_count(void) {
    static const uint8_t hay[] = "a,b,,";
    static const uint8_t comma[] = ",";

    assert(willrt_bytes_count(hay, 5, comma, 1) == 3);
}

static void test_trim(void) {
    static const uint8_t data[] = "  a \t";
    int64_t start = 0;
    int64_t length = 0;

    assert(willrt_bytes_trim(data, 5, 1, 1, &start, &length) == 1);
    assert(start == 2 && length == 1);
    assert(willrt_bytes_trim(data, 5, 0, 1, &start, &length) == 3);
    assert(start == 0 && length == 3);
}

static void test_split(void) {
    static const uint8_t data[] = "a,b,";
    static const uint8_t comma[] = ",";
    uint64_t parts[6];

    assert(willrt_bytes_split(data, 4, comma, 1, NULL, 0) == 3);
    assert(willrt_bytes_split(data, 4, comma, 1, parts, 3) == 3);
    assert(parts[1] == 1);
    assert(parts[3] == 1);
    assert(parts[5] == 0);
}

static void test_fields(void) {
    static const uint8_t data[] = "  a  bb ";
    uint64_t parts[4];

    assert(willrt_bytes_fields(data, 8, NULL, 0) == 2);
    assert(willrt_bytes_fields(data, 8, parts, 2) == 2);
    assert(parts[1] == 1);
    assert(parts[3] == 2);
}

static void test_replace(void) {
    static const uint8_t data[] = "aXa";
    static const uint8_t old[] = "a";
    static const uint8_t new_text[] = "bb";
    uint8_t out[8];

    assert(willrt_bytes_replace_into(out, sizeof(out), data, 3, old, 1,
                                     new_text, 2)
           == 5);
    assert(memcmp(out, "bbXbb", 5) == 0);
}

static void test_repeat(void) {
    static const uint8_t src[] = "ab";
    uint8_t out[6];

    willrt_bytes_repeat(out, src, 2, 3);
    assert(memcmp(out, "ababab", 6) == 0);
}

int main(void) {
    test_index();
    test_count();
    test_trim();
    test_split();
    test_fields();
    test_replace();
    test_repeat();
    return 0;
}
