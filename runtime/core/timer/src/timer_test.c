/* Contract tests for willrt_now_ns, willrt_timer_new, willrt_timer_cancel,
   and the test-only expiry seam. The process exits zero when every assertion
   holds and aborts through assert on the first failure. */

#include "timer.h"

#include <assert.h>
#include <stdint.h>
#include <stddef.h>

enum {
    WILLRT_TEST_FIRED_MAX = 64
};

static int willrt_test_fired[WILLRT_TEST_FIRED_MAX];
static size_t willrt_test_fired_count;
static willrt_timer* willrt_test_self;

static int64_t willrt_test_future(int64_t seconds) {
    return willrt_now_ns() + seconds * INT64_C(1000000000);
}

static void willrt_test_record(void* arg) {
    assert(willrt_test_fired_count < WILLRT_TEST_FIRED_MAX);
    willrt_test_fired[willrt_test_fired_count++] = (int)(intptr_t)arg;
}

static void willrt_test_cancel_self(void* arg) {
    (void)arg;
    willrt_timer_cancel(willrt_test_self);
}

static void test_clock_monotonic(void) {
    int64_t previous = willrt_now_ns();
    size_t i;

    for (i = 0; i < 1000; i++) {
        int64_t now = willrt_now_ns();
        assert(now >= previous);
        previous = now;
    }
}

static void test_cancel_half(void) {
    int64_t base = willrt_test_future(1);
    willrt_timer* timers[8];
    size_t i;

    willrt_test_fired_count = 0;
    for (i = 0; i < 8; i++) {
        timers[i] = willrt_timer_new(base + (int64_t)i * 1000,
                                     willrt_test_record,
                                     (void*)(intptr_t)i);
        assert(timers[i] != NULL);
    }
    for (i = 0; i < 8; i += 2) {
        willrt_timer_cancel(timers[i]);
        willrt_timer_cancel(timers[i]);
    }
    assert(willrt_timer_test_advance(base + 1000000) == 4);
    assert(willrt_test_fired_count == 4);
    for (i = 0; i < 4; i++) {
        assert(willrt_test_fired[i] == (int)(i * 2 + 1));
    }
    willrt_timer_cancel(NULL);
}

static void test_order(void) {
    int64_t base = willrt_test_future(2);
    willrt_timer* first;
    willrt_timer* second;
    willrt_timer* third;
    willrt_timer* fourth;
    willrt_timer* fifth;

    willrt_test_fired_count = 0;
    first = willrt_timer_new(base + 30000, willrt_test_record,
                             (void*)(intptr_t)0);
    second = willrt_timer_new(base + 10000, willrt_test_record,
                              (void*)(intptr_t)1);
    third = willrt_timer_new(base + 20000, willrt_test_record,
                             (void*)(intptr_t)2);
    fourth = willrt_timer_new(base + 10000, willrt_test_record,
                              (void*)(intptr_t)3);
    fifth = willrt_timer_new(base + 5000, willrt_test_record,
                             (void*)(intptr_t)4);
    assert(first != NULL);
    assert(second != NULL);
    assert(third != NULL);
    assert(fourth != NULL);
    assert(fifth != NULL);
    assert(willrt_timer_test_advance(base + 100000) == 5);
    assert(willrt_test_fired_count == 5);
    assert(willrt_test_fired[0] == 4);
    assert(willrt_test_fired[1] == 1);
    assert(willrt_test_fired[2] == 3);
    assert(willrt_test_fired[3] == 2);
    assert(willrt_test_fired[4] == 0);
}

static void test_cancel_after_fire(void) {
    int64_t base = willrt_test_future(3);
    willrt_timer* timer;

    willrt_test_fired_count = 0;
    timer = willrt_timer_new(base, willrt_test_record, (void*)(intptr_t)42);
    assert(timer != NULL);
    assert(willrt_timer_test_advance(base + 1000000) == 1);
    assert(willrt_test_fired_count == 1);
    assert(willrt_test_fired[0] == 42);
    willrt_timer_cancel(timer);
    willrt_timer_cancel(timer);
    assert(willrt_timer_test_advance(base + 2000000) == 0);
}

static void test_cancel_during_fire(void) {
    int64_t base = willrt_test_future(4);

    willrt_test_fired_count = 0;
    willrt_test_self = willrt_timer_new(base, willrt_test_cancel_self, NULL);
    assert(willrt_test_self != NULL);
    assert(willrt_timer_test_advance(base + 1000000) == 1);
    willrt_timer_cancel(willrt_test_self);
    assert(willrt_timer_test_advance(base + 2000000) == 0);
    willrt_test_self = NULL;
}

static void test_late_insert(void) {
    int64_t base = willrt_test_future(5);
    willrt_timer* timer;

    willrt_test_fired_count = 0;
    assert(willrt_timer_test_advance(base) == 0);
    timer = willrt_timer_new(base + 500000, willrt_test_record,
                             (void*)(intptr_t)7);
    assert(timer != NULL);
    assert(willrt_timer_test_advance(base + 1000000) == 1);
    assert(willrt_test_fired_count == 1);
    assert(willrt_test_fired[0] == 7);
}

static void test_immediate(void) {
    willrt_timer* timer;

    willrt_test_fired_count = 0;
    timer = willrt_timer_new(0, willrt_test_record, (void*)(intptr_t)9);
    assert(timer != NULL);
    assert(willrt_timer_test_advance(willrt_now_ns()) == 1);
    assert(willrt_test_fired_count == 1);
    assert(willrt_test_fired[0] == 9);
}

int main(void) {
    test_clock_monotonic();
    test_cancel_half();
    test_order();
    test_cancel_after_fire();
    test_cancel_during_fire();
    test_late_insert();
    test_immediate();
    return 0;
}
