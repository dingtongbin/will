/* A parked task with no timer, job, or IO must not hang the process. */

#include "../../../include/willrt.h"

#include <assert.h>

extern void willrt_tasks_wait_all(void);

static int32_t step_park(void* frame) {
    (void)frame;
    willrt_task_mark_wait();
    return WILLRT_SUSPEND;
}

int main(void) {
    willrt_task* task;

    willrt_init();
    task = willrt_task_new((void*)step_park, NULL, 0, 0, 0);
    assert(task != NULL);
    willrt_task_run(task);
    willrt_tasks_wait_all();
    return 0;
}
