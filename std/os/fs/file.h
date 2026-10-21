#ifndef WILL_OS_FS_FILE_H
#define WILL_OS_FS_FILE_H

#include <stdint.h>

int32_t willrt_fs_read_file(const void* path_pair, void* out_array);
int32_t willrt_fs_write_file(const void* path_pair, const void* data_view);
int32_t willrt_fs_append_file(const void* path_pair, const void* data_view);

#endif
