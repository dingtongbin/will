#include <stdint.h>

void* willrt_sync_lock_new(void);
int32_t willrt_sync_lock_async(void* s, int32_t mode);
int32_t willrt_sync_lock_blocking(void* s, int32_t mode);
int32_t willrt_sync_lock_try(void* s, int32_t mode);
void willrt_sync_unlock(void* s, int32_t mode);
