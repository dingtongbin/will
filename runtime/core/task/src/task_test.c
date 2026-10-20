/* Contract tests for tasks, regions, and channels. The process exits zero when
   every assertion holds and aborts through assert on the first failure. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static int32_t step_double(void* frame) {
    int32_t value = *(int32_t*)frame;
    int32_t result = value * 2;

    assert(willrt_task_store_result(&result, sizeof(result)) == WILLRT_OK);
    return WILLRT_DONE;
}

static int32_t step_panic(void* frame) {
    (void)frame;
    willrt_panic("task failed on purpose", "task_test.c:20:5", "will.test.task");
    return WILLRT_DONE;
}

static int32_t step_send(void* frame) {
    willrt_chan* chan = (willrt_chan*)frame;
    int32_t value = 41;

    assert(willrt_chan_send(chan, &value) == WILLRT_OK);
    return WILLRT_DONE;
}

static void test_task_result(void) {
    int32_t input = 21;
    int32_t output = 0;
    willrt_task* task = willrt_task_new((void*)step_double, &input, sizeof(input),
                                        willrt_lane_here(), 0);

    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, &output, NULL) == WILLRT_OK);
    assert(output == 42);
}

static void test_region_counts(void) {
    int32_t input = 3;
    willrt_task* first;
    willrt_task* second;
    int32_t output = 0;

    willrt_region_enter();
    first = willrt_task_new((void*)step_double, &input, sizeof(input), 0, 0);
    second = willrt_task_new((void*)step_double, &input, sizeof(input), 0, 0);
    assert(first != NULL && second != NULL);
    willrt_task_run(first);
    willrt_task_run(second);
    assert(willrt_join(first, &output, NULL) == WILLRT_OK);
    assert(output == 6);
    assert(willrt_region_exit() == 0);
}

static void test_region_failure(void) {
    willrt_error err;
    willrt_task* task;

    memset(&err, 0, sizeof(err));
    willrt_region_enter();
    task = willrt_task_new((void*)step_panic, NULL, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, &err) == WILLRT_FAILED);
    assert(err.size == sizeof("task failed on purpose") - 1);
    assert(memcmp(err.message, "task failed on purpose", err.size) == 0);
    assert(willrt_region_exit() == 1);
}

static void test_channel_order(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 4, 0);
    int32_t value = 0;

    assert(chan != NULL);
    for (int32_t i = 1; i <= 4; i++) {
        assert(willrt_chan_send(chan, &i) == WILLRT_OK);
    }
    assert(willrt_chan_try_send(chan, &value) == WILLRT_FULL);
    for (int32_t i = 1; i <= 4; i++) {
        assert(willrt_chan_recv(chan, &value) == WILLRT_OK);
        assert(value == i);
    }
    assert(willrt_chan_try_recv(chan, &value) == WILLRT_EMPTY);
    willrt_chan_close(chan);
    willrt_chan_close(chan);
    assert(willrt_chan_recv(chan, &value) == WILLRT_CLOSED);
    assert(willrt_chan_send(chan, &value) == WILLRT_CLOSED);
}

static void test_channel_crosses_threads(void) {
    willrt_chan* chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_task* task;
    int32_t value = 0;

    assert(chan != NULL);
    task = willrt_task_new((void*)step_send, chan, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    assert(willrt_chan_recv(chan, &value) == WILLRT_OK);
    assert(value == 41);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
}

static void test_select_order(void) {
    willrt_chan* first = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_chan* second = willrt_chan_new(sizeof(int32_t), 1, 0);
    willrt_arm arms[3];
    int32_t one = 1;
    int32_t two = 2;
    int32_t out = 0;

    assert(first != NULL && second != NULL);
    assert(willrt_chan_send(second, &two) == WILLRT_OK);
    memset(arms, 0, sizeof(arms));
    arms[0].chan = first;
    arms[0].out = &out;
    arms[0].kind = 0;
    arms[1].chan = second;
    arms[1].out = &out;
    arms[1].kind = 0;
    arms[2].kind = 3;
    assert(willrt_select(arms, 3) == 1);
    assert(out == 2);

    assert(willrt_chan_send(first, &one) == WILLRT_OK);
    arms[0].out = &out;
    assert(willrt_select(arms, 3) == 0);
    assert(out == 1);
    assert(willrt_select(arms, 3) == 2);
}

int main(void) {
    willrt_init();
    test_task_result();
    test_region_counts();
    test_region_failure();
    test_channel_order();
    test_channel_crosses_threads();
    test_select_order();
    willrt_shutdown();
    return 0;
}
