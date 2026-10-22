/* Cancel is one shared flag. The flag is a small atomic integer; a Cancel
   value carries the pointer and is moved into a task like any other handle. */

#include <stdint.h>
#include <stddef.h>

void* willrt_alloc(size_t size);
void willrt_free(void* ptr);

void* willrt_cancel_new(void) {
    int32_t* flag = (int32_t*)willrt_alloc(sizeof(int32_t));
    if (flag == NULL) {
        return NULL;
    }
    *flag = 0;
    return flag;
}

extern void willrt_sched_wake_io(void) __attribute__((weak));

void willrt_cancel_trigger(int32_t* flag) {
    if (flag != NULL) {
        __atomic_store_n(flag, 1, __ATOMIC_RELEASE);
        if (willrt_sched_wake_io != NULL) {
            willrt_sched_wake_io();
        }
    }
}

int32_t willrt_cancel_canceled(const int32_t* flag) {
    return flag != NULL && __atomic_load_n(flag, __ATOMIC_ACQUIRE) != 0;
}
