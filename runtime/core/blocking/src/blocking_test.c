/* Contract test for the blocking pool: a job runs on a pool thread, its result
   reaches the caller, and shutdown joins the workers. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdint.h>

static int32_t add_one(void* arg, void* out) {
    int32_t value = *(const int32_t*)arg;

    *(int32_t*)out = value + 1;
    return WILLRT_OK;
}

int main(void) {
    int32_t input = 41;
    int32_t output = 0;
    int32_t status;
    willrt_blocking_op* op;

    status = willrt_blocking_call((void*)add_one, &input, &output);
    assert(status == WILLRT_OK);
    assert(output == 42);

    input = -1;
    output = 0;
    status = willrt_blocking_call((void*)add_one, &input, &output);
    assert(status == WILLRT_OK);
    assert(output == 0);

    input = 9;
    output = 0;
    op = willrt_blocking_submit_copy((void*)add_one, &input, sizeof(input),
                                     &output);
    assert(op != NULL);
    input = 100;
    while (!willrt_blocking_poll(op)) {
        willrt_os_thread_yield();
    }
    status = willrt_blocking_finish(op);
    assert(status == WILLRT_OK);
    assert(output == 10);

    willrt_blocking_shutdown();
    return 0;
}
