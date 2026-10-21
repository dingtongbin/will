/* Process execution shim for std/os/exec.
 *
 * The shell is the portable process launcher on both targets. The command is
 * assembled from the explicit argument vector and environment, never from a
 * caller-supplied command string. Output goes through two temporary files so
 * stdout and stderr stay separate. The current task owns its thread, so the
 * wait does not stall another spawned task.
 */

#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void* willrt_alloc(size_t size);
void willrt_free(void* ptr);

typedef struct exec_str {
    const unsigned char* data;
    int64_t len;
} exec_str;

typedef struct exec_view {
    const exec_str* data;
    int64_t len;
} exec_view;

typedef struct exec_array {
    void* data;
    int64_t len;
    int64_t cap;
} exec_array;

typedef struct exec_status {
    int32_t code;
    uint8_t signaled;
} exec_status;

#if defined(_WIN32)
#include <direct.h>
#include <process.h>
#include <windows.h>
#else
#include <sys/wait.h>
#include <unistd.h>
#endif

typedef struct exec_buf {
    char* data;
    size_t len;
    size_t cap;
} exec_buf;

static char* exec_copy(const exec_str* text) {
    char* out;
    int64_t i;

    if (text == NULL || text->len < 0) {
        return NULL;
    }
    out = (char*)willrt_alloc((size_t)text->len + 1);
    if (out == NULL) {
        return NULL;
    }
    for (i = 0; i < text->len; i++) {
        out[i] = (char)text->data[i];
    }
    out[text->len] = '\0';
    return out;
}

static int exec_buf_add(exec_buf* buf, const char* text) {
    size_t n = strlen(text);
    size_t need = buf->len + n + 1;

    if (need > buf->cap) {
        size_t cap = buf->cap == 0 ? 64 : buf->cap;
        char* next;
        while (cap < need) {
            cap *= 2;
        }
        next = (char*)willrt_alloc(cap);
        if (next == NULL) {
            return 1;
        }
        if (buf->data != NULL) {
            memcpy(next, buf->data, buf->len);
            willrt_free(buf->data);
        }
        buf->data = next;
        buf->cap = cap;
    }
    memcpy(buf->data + buf->len, text, n);
    buf->len += n;
    buf->data[buf->len] = '\0';
    return 0;
}

static int exec_buf_char(exec_buf* buf, char c) {
    char text[2] = {c, '\0'};
    return exec_buf_add(buf, text);
}

static int exec_quote(exec_buf* buf, const char* text) {
#if defined(_WIN32)
    if (strpbrk(text, " \t") == NULL) {
        return exec_buf_add(buf, text);
    }
    if (exec_buf_char(buf, '"') != 0) {
        return 1;
    }
    for (; *text != '\0'; text++) {
        if (*text == '"') {
            if (exec_buf_add(buf, "\\\"") != 0) {
                return 1;
            }
        } else if (exec_buf_char(buf, *text) != 0) {
            return 1;
        }
    }
    return exec_buf_char(buf, '"');
#else
    if (exec_buf_char(buf, '\'') != 0) {
        return 1;
    }
    for (; *text != '\0'; text++) {
        if (*text == '\'') {
            if (exec_buf_add(buf, "'\\''") != 0) {
                return 1;
            }
        } else if (exec_buf_char(buf, *text) != 0) {
            return 1;
        }
    }
    return exec_buf_char(buf, '\'');
#endif
}

static char* exec_temp_path(void) {
#if defined(_WIN32)
    char* path = (char*)willrt_alloc(MAX_PATH);
    if (path == NULL) {
        return NULL;
    }
    if (GetTempFileNameA(".", "wex", 0, path) == 0) {
        willrt_free(path);
        return NULL;
    }
    return path;
#else
    char* path = (char*)willrt_alloc(32);
    int fd;
    if (path == NULL) {
        return NULL;
    }
    strcpy(path, "/tmp/will-exec-XXXXXX");
    fd = mkstemp(path);
    if (fd < 0) {
        willrt_free(path);
        return NULL;
    }
    close(fd);
    return path;
#endif
}

static int exec_build(const exec_str* path, const exec_view* args,
                      const exec_view* env, const exec_str* dir,
                      const char* outPath, const char* errPath,
                      int capture, char** outCommand) {
    exec_buf buf = {0};
    char* pathText = exec_copy(path);
    char* dirText = dir == NULL || dir->len == 0 ? NULL : exec_copy(dir);
    int64_t i;

    if (pathText == NULL) {
        return 1;
    }
#if defined(_WIN32)
    if (dirText != NULL) {
        if (exec_buf_add(&buf, "cd /D ") != 0 || exec_quote(&buf, dirText) != 0 ||
            exec_buf_add(&buf, " && ") != 0) {
            goto fail;
        }
    }
#else
    if (dirText != NULL) {
        if (exec_buf_add(&buf, "cd ") != 0 || exec_quote(&buf, dirText) != 0 ||
            exec_buf_add(&buf, " && ") != 0) {
            goto fail;
        }
    }
#endif
    if (env != NULL && env->len != 0) {
        for (i = 0; i + 1 < env->len; i += 2) {
            char* key = exec_copy(&env->data[i]);
            char* value = exec_copy(&env->data[i + 1]);
            if (key == NULL || value == NULL) {
                willrt_free(key);
                willrt_free(value);
                goto fail;
            }
#if defined(_WIN32)
            if (exec_buf_add(&buf, "set \"") != 0 || exec_buf_add(&buf, key) != 0 ||
                exec_buf_add(&buf, "=") != 0 || exec_buf_add(&buf, value) != 0 ||
                exec_buf_add(&buf, "\" && ") != 0) {
                willrt_free(key);
                willrt_free(value);
                goto fail;
            }
#else
            if (exec_buf_add(&buf, "env ") != 0 || exec_quote(&buf, key) != 0 ||
                exec_buf_add(&buf, "=") != 0 || exec_quote(&buf, value) != 0 ||
                exec_buf_add(&buf, " ") != 0) {
                willrt_free(key);
                willrt_free(value);
                goto fail;
            }
#endif
            willrt_free(key);
            willrt_free(value);
        }
    }
    if (exec_quote(&buf, pathText) != 0) {
        goto fail;
    }
    if (args != NULL) {
        for (i = 0; i < args->len; i++) {
            char* arg = exec_copy(&args->data[i]);
            if (arg == NULL || exec_buf_char(&buf, ' ') != 0 || exec_quote(&buf, arg) != 0) {
                willrt_free(arg);
                goto fail;
            }
            willrt_free(arg);
        }
    }
    if (capture) {
        if (exec_buf_add(&buf, " > ") != 0 || exec_quote(&buf, outPath) != 0 ||
            exec_buf_add(&buf, " 2> ") != 0 || exec_quote(&buf, errPath) != 0) {
            goto fail;
        }
    } else {
#if defined(_WIN32)
        if (exec_buf_add(&buf, " > NUL 2> NUL") != 0) {
            goto fail;
        }
#else
        if (exec_buf_add(&buf, " > /dev/null 2>&1") != 0) {
            goto fail;
        }
#endif
    }
    willrt_free(pathText);
    willrt_free(dirText);
    *outCommand = buf.data;
    return 0;
fail:
    willrt_free(pathText);
    willrt_free(dirText);
    willrt_free(buf.data);
    return 1;
}

static void exec_read_file(const char* path, exec_array* out) {
    FILE* file;
    long size;

    out->data = NULL;
    out->len = 0;
    out->cap = 0;
    file = fopen(path, "rb");
    if (file == NULL) {
        return;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        return;
    }
    size = ftell(file);
    if (size < 0 || fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        return;
    }
    if (size != 0) {
        out->data = willrt_alloc((size_t)size);
        if (out->data != NULL && fread(out->data, 1, (size_t)size, file) == (size_t)size) {
            out->len = (int64_t)size;
            out->cap = (int64_t)size;
        }
    }
    fclose(file);
}

static int32_t exec_status_code(int status, exec_status* out) {
    if (out == NULL) {
        return 1;
    }
#if defined(_WIN32)
    out->code = status;
    out->signaled = 0;
    return 0;
#else
    if (WIFEXITED(status)) {
        out->code = WEXITSTATUS(status);
        out->signaled = 0;
        return 0;
    }
    if (WIFSIGNALED(status)) {
        out->code = WTERMSIG(status);
        out->signaled = 1;
        return 0;
    }
#endif
    out->code = status;
    out->signaled = 0;
    return 0;
}

int32_t willrt_exec_run(const void* path_raw, const void* args_raw,
                        const void* env_raw, const void* dir_raw,
                        void* status_raw) {
    const exec_str* path = (const exec_str*)path_raw;
    const exec_view* args = (const exec_view*)args_raw;
    const exec_view* env = (const exec_view*)env_raw;
    const exec_str* dir = (const exec_str*)dir_raw;
    exec_status* status = (exec_status*)status_raw;
    char* command = NULL;
    int result;

    if (exec_build(path, args, env, dir, NULL, NULL, 0, &command) != 0) {
        return 1;
    }
    result = system(command);
    willrt_free(command);
    if (result == -1) {
        return 1;
    }
    return exec_status_code(result, status);
}

int32_t willrt_exec_output(const void* path_raw, const void* args_raw,
                           const void* env_raw, const void* dir_raw,
                           void* out_raw, void* err_raw, void* status_raw) {
    const exec_str* path = (const exec_str*)path_raw;
    const exec_view* args = (const exec_view*)args_raw;
    const exec_view* env = (const exec_view*)env_raw;
    const exec_str* dir = (const exec_str*)dir_raw;
    exec_array* out = (exec_array*)out_raw;
    exec_array* err = (exec_array*)err_raw;
    exec_status* status = (exec_status*)status_raw;
    char* command = NULL;
    char* outPath;
    char* errPath;
    int result;

    outPath = exec_temp_path();
    errPath = exec_temp_path();
    if (outPath == NULL || errPath == NULL) {
        willrt_free(outPath);
        willrt_free(errPath);
        return 1;
    }
    if (exec_build(path, args, env, dir, outPath, errPath, 1, &command) != 0) {
        willrt_free(outPath);
        willrt_free(errPath);
        return 1;
    }
    result = system(command);
    willrt_free(command);
    if (result == -1) {
        willrt_free(outPath);
        willrt_free(errPath);
        return 1;
    }
    exec_read_file(outPath, out);
    exec_read_file(errPath, err);
    remove(outPath);
    remove(errPath);
    willrt_free(outPath);
    willrt_free(errPath);
    return exec_status_code(result, status);
}
