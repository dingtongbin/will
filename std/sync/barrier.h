#include <stdint.h>

void* willrt_sync_barrier_new(int64_t parties);
int32_t willrt_sync_barrier_wait_blocking(void* barrier);
int32_t willrt_sync_barrier_wait_async(void* barrier);
