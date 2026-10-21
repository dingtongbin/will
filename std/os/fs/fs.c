/* File, directory, and path operations for std/os/fs.
 *
 * The public API lives in fs.will. This shim converts will str, view, array,
 * and struct headers to the host C file interface. A File value owns one
 * FILE*; Close releases it. Names returned by ReadDir are allocated once and
 * live with the process, matching the standard library's no-hidden-free rule
 * for values that escape their call. */

#include "file.h"

#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

void* willrt_alloc(size_t size);
void willrt_free(void* ptr);

typedef struct willrt_blocking_op willrt_blocking_op;
willrt_blocking_op* willrt_blocking_submit(void* fn, void* arg, void* out);
int32_t willrt_blocking_wait(willrt_blocking_op* op);

typedef struct fs_str {
    const unsigned char* data;
    int64_t len;
} fs_str;

typedef struct fs_view {
    const unsigned char* data;
    int64_t len;
} fs_view;

typedef struct fs_array {
    void* data;
    int64_t len;
    int64_t cap;
} fs_array;

typedef struct fs_stat {
    int64_t size;
    uint32_t mode;
    int64_t mtime;
    uint8_t isDir;
    uint8_t isSymlink;
} fs_stat;

typedef struct fs_dir_entry {
    fs_str name;
    uint8_t isDir;
    int64_t size;
} fs_dir_entry;

typedef struct fs_flags {
    uint8_t read;
    uint8_t write;
    uint8_t create;
    uint8_t append;
    uint8_t truncate;
    uint8_t exclusive;
} fs_flags;

typedef struct fs_file {
    void* handle;
} fs_file;

#if defined(_WIN32)
#include <direct.h>
#include <io.h>
#include <sys/stat.h>
#include <windows.h>
#else
#include <dirent.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

static char* fs_copy(const fs_str* pair) {
    char* out;
    int64_t i;

    if (pair == NULL || pair->len < 0) {
        return NULL;
    }
    out = (char*)willrt_alloc((size_t)pair->len + 1);
    if (out == NULL) {
        return NULL;
    }
    for (i = 0; i < pair->len; i++) {
        out[i] = (char)pair->data[i];
    }
    out[pair->len] = '\0';
    return out;
}

static void fs_set_str(fs_str* out, const char* data, int64_t len) {
    out->data = (const unsigned char*)data;
    out->len = len;
}

static char* fs_join(const char* dir, const char* name) {
    size_t dirLen = strlen(dir);
    size_t nameLen = strlen(name);
    int needSlash = dirLen != 0 && dir[dirLen - 1] != '/' && dir[dirLen - 1] != '\\';
    char* out = (char*)willrt_alloc(dirLen + (size_t)needSlash + nameLen + 1);

    if (out == NULL) {
        return NULL;
    }
    memcpy(out, dir, dirLen);
    if (needSlash) {
        out[dirLen] = '/';
        dirLen++;
    }
    memcpy(out + dirLen, name, nameLen);
    out[dirLen + nameLen] = '\0';
    return out;
}

int32_t willrt_fs_open(const void* path_raw, const void* flags_raw, void* out_raw) {
    const fs_str* path = (const fs_str*)path_raw;
    const fs_flags* flags = (const fs_flags*)flags_raw;
    fs_file* out = (fs_file*)out_raw;
    char* name;
    char* mode;
    FILE* file;

    if (out == NULL || flags == NULL) {
        return 1;
    }
    name = fs_copy(path);
    if (name == NULL) {
        return 1;
    }
    if (flags->append) {
        mode = "ab";
    } else if (flags->write && flags->read) {
        mode = flags->truncate ? "w+b" : "r+b";
    } else if (flags->write) {
        mode = "wb";
    } else {
        mode = "rb";
    }
    if (flags->create && flags->write) {
        mode = flags->read ? "w+b" : "wb";
    }
    file = fopen(name, mode);
    willrt_free(name);
    if (file == NULL) {
        return 1;
    }
    out->handle = file;
    return 0;
}

int32_t willrt_fs_create(const void* path_raw, uint32_t mode, void* out_raw) {
    fs_file* out = (fs_file*)out_raw;
    char* path;
    FILE* file;

    (void)mode;
    if (out == NULL) {
        return 1;
    }
    path = fs_copy((const fs_str*)path_raw);
    if (path == NULL) {
        return 1;
    }
    file = fopen(path, "w+b");
    willrt_free(path);
    if (file == NULL) {
        return 1;
    }
    out->handle = file;
    return 0;
}

int32_t willrt_fs_file_read(const void* file_raw, void* count_raw,
                            void* array_raw) {
    const fs_file* file = (const fs_file*)file_raw;
    fs_array* array = (fs_array*)array_raw;
    int64_t* count = (int64_t*)count_raw;
    int64_t left;
    size_t got;

    if (file == NULL || file->handle == NULL || array == NULL || count == NULL ||
        array->len < 0 || array->cap < array->len || array->data == NULL) {
        return 1;
    }
    left = array->cap - array->len;
    if (left <= 0) {
        *count = 0;
        return 0;
    }
    got = fread((unsigned char*)array->data + array->len, 1, (size_t)left,
                (FILE*)file->handle);
    if (got == 0 && ferror((FILE*)file->handle)) {
        return 1;
    }
    array->len += (int64_t)got;
    *count = (int64_t)got;
    return 0;
}

int32_t willrt_fs_file_write(const void* file_raw, const void* data_raw) {
    const fs_file* file = (const fs_file*)file_raw;
    const fs_view* view = (const fs_view*)data_raw;
    size_t wrote;

    if (file == NULL || file->handle == NULL || view == NULL || view->len < 0) {
        return 1;
    }
    wrote = view->len == 0 ? 0 : fwrite(view->data, 1, (size_t)view->len,
                                       (FILE*)file->handle);
    return wrote == (size_t)view->len ? 0 : 1;
}

int32_t willrt_fs_file_seek(const void* file_raw, int64_t offset, int32_t whence,
                            void* out_raw) {
    const fs_file* file = (const fs_file*)file_raw;
    int64_t* out = (int64_t*)out_raw;
    long at;

    if (file == NULL || file->handle == NULL || out == NULL) {
        return 1;
    }
    if (fseek((FILE*)file->handle, (long)offset, (int)whence) != 0) {
        return 1;
    }
    at = ftell((FILE*)file->handle);
    if (at < 0) {
        return 1;
    }
    *out = (int64_t)at;
    return 0;
}

int32_t willrt_fs_file_sync(const void* file_raw) {
    const fs_file* file = (const fs_file*)file_raw;
    int fd;

    if (file == NULL || file->handle == NULL) {
        return 1;
    }
    if (fflush((FILE*)file->handle) != 0) {
        return 1;
    }
#if defined(_WIN32)
    fd = _fileno((FILE*)file->handle);
    return _commit(fd) == 0 ? 0 : 1;
#else
    fd = fileno((FILE*)file->handle);
    return fsync(fd) == 0 ? 0 : 1;
#endif
}

int32_t willrt_fs_file_close(const void* file_raw) {
    fs_file* file = (fs_file*)file_raw;

    if (file == NULL || file->handle == NULL) {
        return 0;
    }
    if (fclose((FILE*)file->handle) != 0) {
        file->handle = NULL;
        return 1;
    }
    file->handle = NULL;
    return 0;
}

int32_t willrt_fs_open(const void* path_raw, const void* flags_raw,
                       void* out_raw);
int32_t willrt_fs_create(const void* path_raw, uint32_t mode, void* out_raw);
int32_t willrt_fs_file_read(const void* file_raw, void* count_raw,
                            void* array_raw);
int32_t willrt_fs_file_write(const void* file_raw, const void* data_raw);
int32_t willrt_fs_file_seek(const void* file_raw, int64_t offset,
                            int32_t whence, void* out_raw);
int32_t willrt_fs_file_sync(const void* file_raw);
int32_t willrt_fs_file_stat(const void* file_raw, void* out_raw);
int32_t willrt_fs_stat(const void* path_raw, void* out_raw);
int32_t willrt_fs_read_dir(const void* path_raw, void* out_raw);
int32_t willrt_fs_rename(const void* old_raw, const void* new_raw);
int32_t willrt_fs_remove(const void* path_raw);
int32_t willrt_fs_temp_file(const void* dir_raw, const void* pattern_raw,
                            void* out_raw);

typedef struct fs_async_read_file {
    const fs_str* path;
    fs_array* out;
} fs_async_read_file;

typedef struct fs_async_write_file {
    const fs_str* path;
    const fs_view* data;
} fs_async_write_file;

typedef struct fs_async_open {
    const fs_str* path;
    const fs_flags* flags;
    fs_file* out;
} fs_async_open;

typedef struct fs_async_file_io {
    const fs_file* file;
    fs_array* array;
    int64_t* count;
} fs_async_file_io;

typedef struct fs_async_file_write {
    const fs_file* file;
    const fs_view* data;
} fs_async_file_write;

typedef struct fs_async_file_seek {
    const fs_file* file;
    int64_t offset;
    int32_t whence;
    int64_t* out;
} fs_async_file_seek;

typedef struct fs_async_file_stat {
    const fs_file* file;
    fs_stat* out;
} fs_async_file_stat;

typedef struct fs_async_stat {
    const fs_str* path;
    fs_stat* out;
} fs_async_stat;

typedef struct fs_async_read_dir {
    const fs_str* path;
    fs_array* out;
} fs_async_read_dir;

typedef struct fs_async_pair {
    const fs_str* first;
    const fs_str* second;
} fs_async_pair;

typedef struct fs_async_temp {
    const fs_str* dir;
    const fs_str* pattern;
    fs_file* out;
} fs_async_temp;

static int32_t fs_async_read_file_worker(void* raw, void* ignored) {
    fs_async_read_file* arg = (fs_async_read_file*)raw;
    (void)ignored;
    return willrt_fs_read_file(arg->path, arg->out);
}

static int32_t fs_async_write_file_worker(void* raw, void* ignored) {
    fs_async_write_file* arg = (fs_async_write_file*)raw;
    (void)ignored;
    return willrt_fs_write_file(arg->path, arg->data);
}

static int32_t fs_async_append_worker(void* raw, void* ignored) {
    fs_async_write_file* arg = (fs_async_write_file*)raw;
    (void)ignored;
    return willrt_fs_append_file(arg->path, arg->data);
}

static int32_t fs_async_open_worker(void* raw, void* ignored) {
    fs_async_open* arg = (fs_async_open*)raw;
    (void)ignored;
    return willrt_fs_open(arg->path, arg->flags, arg->out);
}

static int32_t fs_async_create_worker(void* raw, void* ignored) {
    fs_async_open* arg = (fs_async_open*)raw;
    (void)ignored;
    return willrt_fs_create(arg->path, 0, arg->out);
}

static int32_t fs_async_file_read_worker(void* raw, void* ignored) {
    fs_async_file_io* arg = (fs_async_file_io*)raw;
    (void)ignored;
    return willrt_fs_file_read(arg->file, arg->count, arg->array);
}

static int32_t fs_async_file_write_worker(void* raw, void* ignored) {
    fs_async_file_write* arg = (fs_async_file_write*)raw;
    (void)ignored;
    return willrt_fs_file_write(arg->file, arg->data);
}

static int32_t fs_async_file_seek_worker(void* raw, void* ignored) {
    fs_async_file_seek* arg = (fs_async_file_seek*)raw;
    (void)ignored;
    return willrt_fs_file_seek(arg->file, arg->offset, arg->whence, arg->out);
}

static int32_t fs_async_file_sync_worker(void* raw, void* ignored) {
    fs_async_file_io* arg = (fs_async_file_io*)raw;
    (void)ignored;
    return willrt_fs_file_sync(arg->file);
}

static int32_t fs_async_file_stat_worker(void* raw, void* ignored) {
    fs_async_file_stat* arg = (fs_async_file_stat*)raw;
    (void)ignored;
    return willrt_fs_file_stat(arg->file, arg->out);
}

static int32_t fs_async_stat_worker(void* raw, void* ignored) {
    fs_async_stat* arg = (fs_async_stat*)raw;
    (void)ignored;
    return willrt_fs_stat(arg->path, arg->out);
}

static int32_t fs_async_read_dir_worker(void* raw, void* ignored) {
    fs_async_read_dir* arg = (fs_async_read_dir*)raw;
    (void)ignored;
    return willrt_fs_read_dir(arg->path, arg->out);
}

static int32_t fs_async_rename_worker(void* raw, void* ignored) {
    fs_async_pair* arg = (fs_async_pair*)raw;
    (void)ignored;
    return willrt_fs_rename(arg->first, arg->second);
}

static int32_t fs_async_remove_worker(void* raw, void* ignored) {
    fs_async_pair* arg = (fs_async_pair*)raw;
    (void)ignored;
    return willrt_fs_remove(arg->first);
}

static int32_t fs_async_temp_worker(void* raw, void* ignored) {
    fs_async_temp* arg = (fs_async_temp*)raw;
    (void)ignored;
    return willrt_fs_temp_file(arg->dir, arg->pattern, arg->out);
}

static int32_t fs_async_wait(void* fn, void* arg) {
    willrt_blocking_op* op = willrt_blocking_submit(fn, arg, NULL);
    if (op == NULL) {
        return 1;
    }
    return willrt_blocking_wait(op);
}

int32_t willrt_fs_async_read_file(const void* path, void* out) {
    fs_async_read_file arg = {(const fs_str*)path, (fs_array*)out};
    return fs_async_wait(fs_async_read_file_worker, &arg);
}

int32_t willrt_fs_async_write_file(const void* path, const void* data) {
    fs_async_write_file arg = {(const fs_str*)path, (const fs_view*)data};
    return fs_async_wait(fs_async_write_file_worker, &arg);
}

int32_t willrt_fs_async_append(const void* path, const void* data) {
    fs_async_write_file arg = {(const fs_str*)path, (const fs_view*)data};
    return fs_async_wait(fs_async_append_worker, &arg);
}

int32_t willrt_fs_async_open(const void* path, const void* flags, void* out) {
    fs_async_open arg = {(const fs_str*)path, (const fs_flags*)flags,
                         (fs_file*)out};
    return fs_async_wait(fs_async_open_worker, &arg);
}

int32_t willrt_fs_async_create(const void* path, uint32_t mode, void* out) {
    fs_async_open arg = {(const fs_str*)path, NULL, (fs_file*)out};
    (void)mode;
    return fs_async_wait(fs_async_create_worker, &arg);
}

int32_t willrt_fs_async_file_read(const void* file, void* count, void* array) {
    fs_async_file_io arg = {(const fs_file*)file, (fs_array*)array,
                            (int64_t*)count};
    return fs_async_wait(fs_async_file_read_worker, &arg);
}

int32_t willrt_fs_async_file_write(const void* file, const void* data) {
    fs_async_file_write arg = {(const fs_file*)file, (const fs_view*)data};
    return fs_async_wait(fs_async_file_write_worker, &arg);
}

int32_t willrt_fs_async_file_seek(const void* file, int64_t offset,
                                  int32_t whence, void* out) {
    fs_async_file_seek arg = {(const fs_file*)file, offset, whence,
                              (int64_t*)out};
    return fs_async_wait(fs_async_file_seek_worker, &arg);
}

int32_t willrt_fs_async_file_sync(const void* file) {
    fs_async_file_io arg = {(const fs_file*)file, NULL, NULL};
    return fs_async_wait(fs_async_file_sync_worker, &arg);
}

int32_t willrt_fs_async_file_stat(const void* file, void* out) {
    fs_async_file_stat arg = {(const fs_file*)file, (fs_stat*)out};
    return fs_async_wait(fs_async_file_stat_worker, &arg);
}

int32_t willrt_fs_async_stat(const void* path, void* out) {
    fs_async_stat arg = {(const fs_str*)path, (fs_stat*)out};
    return fs_async_wait(fs_async_stat_worker, &arg);
}

int32_t willrt_fs_async_read_dir(const void* path, void* out) {
    fs_async_read_dir arg = {(const fs_str*)path, (fs_array*)out};
    return fs_async_wait(fs_async_read_dir_worker, &arg);
}

int32_t willrt_fs_async_rename(const void* old_path, const void* new_path) {
    fs_async_pair arg = {(const fs_str*)old_path, (const fs_str*)new_path};
    return fs_async_wait(fs_async_rename_worker, &arg);
}

int32_t willrt_fs_async_remove(const void* path) {
    fs_async_pair arg = {(const fs_str*)path, NULL};
    return fs_async_wait(fs_async_remove_worker, &arg);
}

int32_t willrt_fs_async_temp_file(const void* dir, const void* pattern,
                                  void* out) {
    fs_async_temp arg = {(const fs_str*)dir, (const fs_str*)pattern,
                         (fs_file*)out};
    return fs_async_wait(fs_async_temp_worker, &arg);
}

int32_t willrt_fs_file_stat(const void* file_raw, void* out_raw) {
    const fs_file* file = (const fs_file*)file_raw;
    fs_stat* out = (fs_stat*)out_raw;
    int fd;

    if (file == NULL || file->handle == NULL || out == NULL) {
        return 1;
    }
    fd = _fileno((FILE*)file->handle);
#if defined(_WIN32)
    {
        struct _stat64 st;
        if (_fstat64(fd, &st) == 0) {
            out->size = (int64_t)st.st_size;
            out->mode = (uint32_t)st.st_mode;
            out->mtime = (int64_t)st.st_mtime;
            out->isDir = (_S_IFDIR & st.st_mode) != 0;
            out->isSymlink = 0;
            return 0;
        }
    }
#else
    {
        struct stat st;
        if (fstat(fd, &st) == 0) {
            out->size = (int64_t)st.st_size;
            out->mode = (uint32_t)st.st_mode;
            out->mtime = (int64_t)st.st_mtime;
            out->isDir = S_ISDIR(st.st_mode) ? 1 : 0;
            out->isSymlink = S_ISLNK(st.st_mode) ? 1 : 0;
            return 0;
        }
    }
#endif
    return 1;
}

int32_t willrt_fs_stat(const void* path_raw, void* out_raw) {
    const fs_str* path = (const fs_str*)path_raw;
    fs_stat* out = (fs_stat*)out_raw;
    char* name;
    int32_t status = 1;

    if (out == NULL) {
        return 1;
    }
    name = fs_copy(path);
    if (name == NULL) {
        return 1;
    }
#if defined(_WIN32)
    {
        struct _stat64 st;
        if (_stat64(name, &st) == 0) {
            out->size = (int64_t)st.st_size;
            out->mode = (uint32_t)st.st_mode;
            out->mtime = (int64_t)st.st_mtime;
            out->isDir = (_S_IFDIR & st.st_mode) != 0;
            out->isSymlink = 0;
            status = 0;
        }
    }
#else
    {
        struct stat st;
        if (lstat(name, &st) == 0) {
            out->size = (int64_t)st.st_size;
            out->mode = (uint32_t)st.st_mode;
            out->mtime = (int64_t)st.st_mtime;
            out->isDir = S_ISDIR(st.st_mode) ? 1 : 0;
            out->isSymlink = S_ISLNK(st.st_mode) ? 1 : 0;
            status = 0;
        }
    }
#endif
    willrt_free(name);
    return status;
}

static int fs_name_cmp(const void* left, const void* right) {
    const char* a = *(const char* const*)left;
    const char* b = *(const char* const*)right;
    return strcmp(a, b);
}

int32_t willrt_fs_read_dir(const void* path_raw, void* out_raw) {
    const fs_str* path = (const fs_str*)path_raw;
    fs_array* out = (fs_array*)out_raw;
    char* dir;
    char** names = NULL;
    int32_t count = 0;
    int32_t capacity = 0;
    int32_t i;

    if (out == NULL) {
        return 1;
    }
    out->data = NULL;
    out->len = 0;
    out->cap = 0;
    dir = fs_copy(path);
    if (dir == NULL) {
        return 1;
    }
#if defined(_WIN32)
    {
        WIN32_FIND_DATAA data;
        HANDLE handle;
        char* pattern = fs_join(dir, "*");
        if (pattern == NULL) {
            willrt_free(dir);
            return 1;
        }
        handle = FindFirstFileA(pattern, &data);
        willrt_free(pattern);
        if (handle == INVALID_HANDLE_VALUE) {
            willrt_free(dir);
            return 1;
        }
        do {
            if (strcmp(data.cFileName, ".") == 0 || strcmp(data.cFileName, "..") == 0) {
                continue;
            }
            if (count == capacity) {
                int32_t grown = capacity == 0 ? 8 : capacity * 2;
                char** next = (char**)willrt_alloc(sizeof(char*) * (size_t)grown);
                if (next == NULL) {
                    FindClose(handle);
                    willrt_free(dir);
                    return 1;
                }
                if (names != NULL) {
                    memcpy(next, names, sizeof(char*) * (size_t)count);
                    willrt_free(names);
                }
                names = next;
                capacity = grown;
            }
            names[count] = (char*)willrt_alloc(strlen(data.cFileName) + 1);
            if (names[count] == NULL) {
                FindClose(handle);
                willrt_free(dir);
                return 1;
            }
            strcpy(names[count], data.cFileName);
            count++;
        } while (FindNextFileA(handle, &data));
        FindClose(handle);
    }
#else
    {
        DIR* handle = opendir(dir);
        struct dirent* entry;
        if (handle == NULL) {
            willrt_free(dir);
            return 1;
        }
        while ((entry = readdir(handle)) != NULL) {
            if (strcmp(entry->d_name, ".") == 0 || strcmp(entry->d_name, "..") == 0) {
                continue;
            }
            if (count == capacity) {
                int32_t grown = capacity == 0 ? 8 : capacity * 2;
                char** next = (char**)willrt_alloc(sizeof(char*) * (size_t)grown);
                if (next == NULL) {
                    closedir(handle);
                    willrt_free(dir);
                    return 1;
                }
                if (names != NULL) {
                    memcpy(next, names, sizeof(char*) * (size_t)count);
                    willrt_free(names);
                }
                names = next;
                capacity = grown;
            }
            names[count] = (char*)willrt_alloc(strlen(entry->d_name) + 1);
            if (names[count] == NULL) {
                closedir(handle);
                willrt_free(dir);
                return 1;
            }
            strcpy(names[count], entry->d_name);
            count++;
        }
        closedir(handle);
    }
#endif
    if (count == 0) {
        if (names != NULL) {
            willrt_free(names);
        }
        willrt_free(dir);
        return 0;
    }
    qsort(names, (size_t)count, sizeof(char*), fs_name_cmp);
    out->data = willrt_alloc(sizeof(fs_dir_entry) * (size_t)count);
    if (out->data == NULL) {
        willrt_free(names);
        willrt_free(dir);
        return 1;
    }
    for (i = 0; i < count; i++) {
        fs_dir_entry* entry = &((fs_dir_entry*)out->data)[i];
        fs_stat st;
        char* full;
        full = fs_join(dir, names[i]);
        memset(&st, 0, sizeof(st));
        if (full != NULL) {
            fs_str pathText;
            pathText.data = (const unsigned char*)full;
            pathText.len = (int64_t)strlen(full);
            (void)willrt_fs_stat(&pathText, &st);
            willrt_free(full);
        }
        fs_set_str(&entry->name, names[i], (int64_t)strlen(names[i]));
        entry->isDir = st.isDir;
        entry->size = st.size;
    }
    willrt_free(names);
    willrt_free(dir);
    out->len = count;
    out->cap = count;
    return 0;
}

static int fs_copy_path(const fs_str* pair, char** out) {
    *out = fs_copy(pair);
    return *out == NULL ? 1 : 0;
}

int32_t willrt_fs_rename(const void* old_raw, const void* new_raw) {
    char* oldPath;
    char* newPath;
    int32_t status;

    if (fs_copy_path((const fs_str*)old_raw, &oldPath) != 0) {
        return 1;
    }
    if (fs_copy_path((const fs_str*)new_raw, &newPath) != 0) {
        willrt_free(oldPath);
        return 1;
    }
    status = rename(oldPath, newPath) == 0 ? 0 : 1;
    willrt_free(oldPath);
    willrt_free(newPath);
    return status;
}

int32_t willrt_fs_remove(const void* path_raw) {
    char* path;
    int32_t status;

    if (fs_copy_path((const fs_str*)path_raw, &path) != 0) {
        return 1;
    }
    status = remove(path) == 0 ? 0 : 1;
    willrt_free(path);
    return status;
}

int32_t willrt_fs_temp_file(const void* dir_raw, const void* pattern_raw,
                            void* out_raw) {
    fs_file* out = (fs_file*)out_raw;
    char* dir;
    char* pattern;
    char* template;
    size_t dirLen;
    size_t patternLen;
    FILE* file = NULL;

    if (out == NULL) {
        return 1;
    }
    dir = fs_copy((const fs_str*)dir_raw);
    pattern = fs_copy((const fs_str*)pattern_raw);
    if (dir == NULL || pattern == NULL) {
        willrt_free(dir);
        willrt_free(pattern);
        return 1;
    }
    dirLen = strlen(dir);
    patternLen = strlen(pattern);
#if defined(_WIN32)
    template = (char*)willrt_alloc(MAX_PATH);
    if (template == NULL) {
        willrt_free(dir);
        willrt_free(pattern);
        return 1;
    }
    {
        char prefix[4] = "wil";
        size_t n = patternLen < 3 ? patternLen : 3;
        memcpy(prefix, pattern, n);
        prefix[n] = '\0';
        if (GetTempFileNameA(dir, prefix, 0, template) == 0) {
            willrt_free(template);
            willrt_free(dir);
            willrt_free(pattern);
            return 1;
        }
    }
    file = fopen(template, "w+b");
    willrt_free(template);
#else
    template = (char*)willrt_alloc(dirLen + patternLen + 12);
    if (template == NULL) {
        willrt_free(dir);
        willrt_free(pattern);
        return 1;
    }
    memcpy(template, dir, dirLen);
    if (dirLen != 0 && dir[dirLen - 1] != '/') {
        template[dirLen++] = '/';
    }
    memcpy(template + dirLen, patternLen == 0 ? "will" : pattern, patternLen);
    dirLen += patternLen == 0 ? 4 : patternLen;
    memcpy(template + dirLen, "XXXXXX", 6);
    template[dirLen + 6] = '\0';
    {
        int fd = mkstemp(template);
        if (fd >= 0) {
            file = fdopen(fd, "w+b");
            if (file == NULL) {
                close(fd);
            }
        }
    }
    willrt_free(template);
#endif
    willrt_free(dir);
    willrt_free(pattern);
    if (file == NULL) {
        return 1;
    }
    out->handle = file;
    return 0;
}
