/* Contract test for the global job queue and its fixed worker pool. The test
   enqueues more jobs than one worker could finish before shutdown, joins every
   handle, and then shuts the runtime down twice to prove that an empty queue
   and a repeated shutdown do not hang or leave queued work behind. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>

enum { JOB_COUNT = 1000 };

typedef struct job_frame {
    int32_t value;
} job_frame;

static atomic_int completed;

static int32_t job_step(void* raw) {
    job_frame* frame = (job_frame*)raw;

    assert(frame != NULL);
    atomic_fetch_add(&completed, 1);
    return WILLRT_DONE;
}

int main(void) {
    job_frame* frames;
    willrt_task** tasks;
    int32_t i;

    willrt_init();
    frames = (job_frame*)willrt_alloc(sizeof(*frames) * JOB_COUNT);
    tasks = (willrt_task**)willrt_alloc(sizeof(*tasks) * JOB_COUNT);
    assert(frames != NULL);
    assert(tasks != NULL);

    for (i = 0; i < JOB_COUNT; i++) {
        frames[i].value = i;
        tasks[i] = willrt_job_new((void*)job_step, &frames[i], sizeof(frames[i]),
                                  0);
        assert(tasks[i] != NULL);
    }
    for (i = 0; i < JOB_COUNT; i++) {
        assert(willrt_join(tasks[i], NULL, NULL) == WILLRT_OK);
    }
    assert(atomic_load(&completed) == JOB_COUNT);

    willrt_shutdown();
    willrt_shutdown();
    return 0;
}
