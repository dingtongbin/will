#include "file.h"

#include "willrt.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The two-word path and data headers are the will str and view layouts. The
   standard library shim reads them here so the public API can take str and
   []u8 directly. */
typedef struct willrt_fs_str {
    const unsigned char* data;
    int64_t len;
} willrt_fs_str;

typedef struct willrt_fs_view {
    const unsigned char* data;
    int64_t len;
} willrt_fs_view;

typedef struct willrt_fs_array {
    void* data;
    int64_t len;
    int64_t cap;
} willrt_fs_array;

static char* willrt_fs_path(const void* raw) {
    const willrt_fs_str* pair = (const willrt_fs_str*)raw;
    char* path;
    int64_t i;

    if (pair == NULL || pair->len < 0) {
        return NULL;
    }
    path = (char*)willrt_alloc((size_t)pair->len + 1);
    if (path == NULL) {
        return NULL;
    }
    for (i = 0; i < pair->len; i++) {
        path[i] = (char)pair->data[i];
    }
    path[pair->len] = '\0';
    return path;
}

int32_t willrt_fs_read_file(const void* path_pair, void* out_array) {
    willrt_fs_array* out = (willrt_fs_array*)out_array;
    char* path;
    FILE* file;
    long size;
    unsigned char* data = NULL;
    size_t read;

    if (out == NULL) {
        return 1;
    }
    path = willrt_fs_path(path_pair);
    if (path == NULL) {
        return 1;
    }
    file = fopen(path, "rb");
    willrt_free(path);
    if (file == NULL) {
        return 1;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        return 1;
    }
    size = ftell(file);
    if (size < 0 || fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        return 1;
    }
    if (size > 0) {
        data = (unsigned char*)willrt_alloc((size_t)size);
        if (data == NULL) {
            fclose(file);
            return 1;
        }
        read = fread(data, 1, (size_t)size, file);
        if (read != (size_t)size) {
            willrt_free(data);
            fclose(file);
            return 1;
        }
    }
    fclose(file);
    out->data = data;
    out->len = size;
    out->cap = size;
    return 0;
}

int32_t willrt_fs_write_file(const void* path_pair, const void* data_view) {
    const willrt_fs_view* view = (const willrt_fs_view*)data_view;
    char* path;
    FILE* file;
    size_t written;

    if (view == NULL || view->len < 0) {
        return 1;
    }
    path = willrt_fs_path(path_pair);
    if (path == NULL) {
        return 1;
    }
    file = fopen(path, "wb");
    willrt_free(path);
    if (file == NULL) {
        return 1;
    }
    written = view->len == 0 ? 0 : fwrite(view->data, 1, (size_t)view->len, file);
    if (written != (size_t)view->len || fclose(file) != 0) {
        return 1;
    }
    return 0;
}

int32_t willrt_fs_append_file(const void* path_pair, const void* data_view) {
    const willrt_fs_view* view = (const willrt_fs_view*)data_view;
    char* path;
    FILE* file;
    size_t written;

    if (view == NULL || view->len < 0) {
        return 1;
    }
    path = willrt_fs_path(path_pair);
    if (path == NULL) {
        return 1;
    }
    file = fopen(path, "ab");
    willrt_free(path);
    if (file == NULL) {
        return 1;
    }
    written = view->len == 0 ? 0 : fwrite(view->data, 1, (size_t)view->len, file);
    if (written != (size_t)view->len || fclose(file) != 0) {
        return 1;
    }
    return 0;
}
