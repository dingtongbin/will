/* Parked-task wake latency probe.

   A receiver task parks on a channel; the main lane sends one value and joins.
   The measured interval is the wake path, not task construction or channel
   setup. This is an opt-in host measurement, not a release test. */

#include "../../../runtime/include/willrt.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define SAMPLES 2000

static _Atomic int64_t wake_start;
static _Atomic int64_t wake_end;

static int32_t step_recv(void* frame) {
    willrt_chan* chan = (willrt_chan*)frame;
    int32_t value = 0;

    if (willrt_chan_recv(chan, &value) != WILLRT_OK) {
        return WILLRT_FAILED;
    }
    atomic_store_explicit(&wake_end, willrt_now_ns(), memory_order_release);
    return WILLRT_DONE;
}

static int compare_i64(const void* a, const void* b) {
    int64_t x = *(const int64_t*)a;
    int64_t y = *(const int64_t*)b;

    return x < y ? -1 : x > y ? 1 : 0;
}

static int64_t percentile(int64_t* values, size_t n, int per_mille) {
    size_t index = (n * (size_t)per_mille) / 1000u;

    if (index >= n) {
        index = n - 1u;
    }
    return values[index];
}

int main(void) {
    int64_t samples[SAMPLES];
    willrt_chan* chan;
    int32_t i;

    willrt_init();
    chan = willrt_chan_new(sizeof(int32_t), 1, 0);
    if (chan == NULL) {
        return 1;
    }
    for (i = 0; i < SAMPLES; i++) {
        willrt_task* task;
        int32_t value = i;

        task = willrt_task_new((void*)step_recv, chan, 0, 0, 0);
        if (task == NULL) {
            return 1;
        }
        willrt_task_run(task);
        atomic_store_explicit(&wake_end, 0, memory_order_release);
        atomic_store_explicit(&wake_start, willrt_now_ns(), memory_order_release);
        if (willrt_chan_send(chan, &value) != WILLRT_OK) {
            return 1;
        }
        if (willrt_join(task, NULL, NULL) != WILLRT_OK) {
            return 1;
        }
        samples[i] = atomic_load_explicit(&wake_end, memory_order_acquire) -
                     atomic_load_explicit(&wake_start, memory_order_acquire);
    }
    willrt_chan_close(chan);
    willrt_shutdown();
    qsort(samples, SAMPLES, sizeof(samples[0]), compare_i64);
    printf("wake_ns p50=%lld p99=%lld p999=%lld\n",
           (long long)percentile(samples, SAMPLES, 500),
           (long long)percentile(samples, SAMPLES, 990),
           (long long)percentile(samples, SAMPLES, 999));
    return 0;
}
