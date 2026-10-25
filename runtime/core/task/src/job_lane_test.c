/* Contract for jobs submitted from a task when only one lane exists. The
   parent must remain the current task, queue the jobs on its own lane, and park
   at the region barrier until every job ran. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { JOB_COUNT = 32 };

static atomic_int completed;

typedef struct parent_frame {
    int32_t calls;
    int32_t status;
} parent_frame;

static int32_t step_child(void* raw) {
    (void)raw;
    atomic_fetch_add(&completed, 1);
    return WILLRT_DONE;
}

static int32_t step_parent(void* raw) {
    parent_frame* frame = (parent_frame*)raw;
    int32_t status;

    frame->calls += 1;
    if (frame->calls == 1) {
        willrt_region_enter();
        for (int32_t i = 0; i < JOB_COUNT; i++) {
            assert(willrt_job_new((void*)step_child, NULL, 0, 0) != NULL);
        }
    }
    status = willrt_region_exit_try();
    if (status == WILLRT_SUSPEND) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    frame->status = status;
    return WILLRT_DONE;
}

int main(void) {
#ifdef _WIN32
    assert(_putenv_s("WILL_LANES", "1") == 0);
#else
    assert(setenv("WILL_LANES", "1", 1) == 0);
#endif
    willrt_init();
    {
        parent_frame frame;
        willrt_task* parent;

        memset(&frame, 0, sizeof(frame));
        parent = willrt_task_new((void*)step_parent, &frame, sizeof(frame), 0,
                                 0);
        assert(parent != NULL);
        willrt_task_run(parent);
        assert(willrt_join(parent, NULL, NULL) == WILLRT_OK);
        assert(frame.status == 0);
        assert(frame.calls >= 2);
        assert(atomic_load(&completed) == JOB_COUNT);
    }
    willrt_shutdown();
    return 0;
}
