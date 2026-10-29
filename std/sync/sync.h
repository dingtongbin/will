#include <stdint.h>

void* willrt_sync_sema_new(int64_t permits);
int32_t willrt_sync_sema_acquire_async(void* s);
int32_t willrt_sync_sema_acquire_blocking(void* s);
int32_t willrt_sync_sema_try_acquire(void* s);
int64_t willrt_sync_sema_release(void* s);

void* willrt_sync_count_new(int64_t count);
void willrt_sync_count_add(void* s, int64_t delta);
int64_t willrt_sync_count_load(void* s);
int32_t willrt_sync_count_wait_async(void* s);
int32_t willrt_sync_count_wait_blocking(void* s);
