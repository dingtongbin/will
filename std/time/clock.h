#ifndef WILL_TIME_CLOCK_H
#define WILL_TIME_CLOCK_H

#include <stdint.h>

int64_t willrt_now_ns(void);
void willrt_os_sleep_ns(int64_t ns);
void willrt_task_sleep_ns(int64_t ns);

#endif
