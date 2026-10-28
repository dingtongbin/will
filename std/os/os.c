/* Process status and environment shims for std/os.
 *
 * The shim is deliberately small: will owns the public API, the Option and
 * Result shapes, and the stable error values. This file only turns the host's
 * process state into the two-word str and view headers that the runtime uses.
 * A returned view points into storage that lives for the rest of the process;
 * Args and Environ therefore never copy or free their strings.
 */

#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void* willrt_alloc(size_t size);
void willrt_free(void* ptr);

typedef struct os_str {
    const unsigned char* data;
    int64_t len;
} os_str;

typedef struct os_view {
    os_str* data;
    int64_t len;
} os_view;

#if defined(_WIN32)
#include <direct.h>
#include <process.h>
#include <windows.h>
extern int __argc;
extern char** __argv;
#else
#include <unistd.h>
#if defined(__APPLE__)
#include <crt_externs.h>
#endif
extern char** environ;
#endif

static int os_valid_utf8(const unsigned char* data, int64_t len) {
    int64_t i = 0;

    while (i < len) {
        unsigned char c = data[i];
        int32_t need;
        int32_t j;
        uint32_t code;

        if (c < 0x80) {
            i++;
            continue;
        }
        if (c >= 0xC2 && c <= 0xDF) {
            need = 1;
            code = c & 0x1F;
        } else if (c >= 0xE0 && c <= 0xEF) {
            need = 2;
            code = c & 0x0F;
        } else if (c >= 0xF0 && c <= 0xF4) {
            need = 3;
            code = c & 0x07;
        } else {
            return 0;
        }
        if (i + need >= len) {
            return 0;
        }
        for (j = 1; j <= need; j++) {
            unsigned char next = data[i + j];
            if ((next & 0xC0) != 0x80) {
                return 0;
            }
            code = (code << 6) | (next & 0x3F);
        }
        if ((need == 2 && code < 0x800) ||
            (need == 3 && code < 0x10000) ||
            code > 0x10FFFF ||
            (code >= 0xD800 && code <= 0xDFFF)) {
            return 0;
        }
        i += need + 1;
    }
    return 1;
}

static char* os_copy(const os_str* pair, int* invalid) {
    char* out;
    int64_t i;

    *invalid = 0;
    if (pair == NULL || pair->len < 0) {
        *invalid = 1;
        return NULL;
    }
    out = (char*)willrt_alloc((size_t)pair->len + 1);
    if (out == NULL) {
        return NULL;
    }
    for (i = 0; i < pair->len; i++) {
        if (pair->data[i] == 0) {
            *invalid = 1;
            willrt_free(out);
            return NULL;
        }
        out[i] = (char)pair->data[i];
    }
    out[pair->len] = '\0';
    return out;
}

static void os_fill_str(os_str* out, const char* data, int64_t len) {
    out->data = (const unsigned char*)data;
    out->len = len;
}

#if defined(_WIN32)
static char** os_args(int32_t* count) {
    *count = __argc;
    return __argv;
}

static char** os_environ(int32_t* count) {
    static char* block;
    static char** entries;
    static int32_t entry_count;
    char* cursor;

    if (entries != NULL) {
        *count = entry_count;
        return entries;
    }
    block = GetEnvironmentStringsA();
    if (block == NULL) {
        *count = 0;
        return NULL;
    }
    cursor = block;
    entry_count = 0;
    while (*cursor != '\0') {
        entry_count++;
        cursor += strlen(cursor) + 1;
    }
    entries = (char**)willrt_alloc(sizeof(char*) * (size_t)entry_count);
    if (entries == NULL) {
        *count = 0;
        return NULL;
    }
    cursor = block;
    for (int32_t i = 0; i < entry_count; i++) {
        entries[i] = cursor;
        cursor += strlen(cursor) + 1;
    }
    *count = entry_count;
    return entries;
}
#else
#if defined(__APPLE__)
static char** os_args(int32_t* count) {
    *count = (int32_t)*_NSGetArgc();
    return *_NSGetArgv();
}
#else
static char** os_args(int32_t* count) {
    static char* data;
    static char** entries;
    static int32_t entry_count;
    FILE* file;
    long size;
    int64_t start;
    int64_t i;

    if (entries != NULL) {
        *count = entry_count;
        return entries;
    }
    file = fopen("/proc/self/cmdline", "rb");
    if (file == NULL) {
        *count = 0;
        return NULL;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        *count = 0;
        return NULL;
    }
    size = ftell(file);
    if (size <= 0 || fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        *count = 0;
        return NULL;
    }
    data = (char*)willrt_alloc((size_t)size + 1);
    if (data == NULL || fread(data, 1, (size_t)size, file) != (size_t)size) {
        if (data != NULL) {
            willrt_free(data);
        }
        fclose(file);
        *count = 0;
        return NULL;
    }
    fclose(file);
    data[size] = '\0';
    entry_count = 0;
    for (i = 0; i < size; i++) {
        if (data[i] == '\0') {
            entry_count++;
        }
    }
    entries = (char**)willrt_alloc(sizeof(char*) * (size_t)entry_count);
    if (entries == NULL) {
        *count = 0;
        return NULL;
    }
    start = 0;
    entry_count = 0;
    for (i = 0; i < size; i++) {
        if (data[i] == '\0') {
            entries[entry_count++] = data + start;
            start = i + 1;
        }
    }
    *count = entry_count;
    return entries;
}
#endif

static char** os_environ(int32_t* count) {
    int32_t i = 0;

    if (environ == NULL) {
        *count = 0;
        return NULL;
    }
    while (environ[i] != NULL) {
        i++;
    }
    *count = i;
    return environ;
}
#endif

int32_t willrt_os_args(void* out_raw) {
    os_view* out = (os_view*)out_raw;
    int32_t count = 0;
    char** argv;
    int32_t i;

    if (out == NULL) {
        return 1;
    }
    argv = os_args(&count);
    out->len = count;
    out->data = NULL;
    if (count == 0 || argv == NULL) {
        return 0;
    }
    out->data = (os_str*)willrt_alloc(sizeof(os_str) * (size_t)count);
    if (out->data == NULL) {
        out->len = 0;
        return 1;
    }
    for (i = 0; i < count; i++) {
        os_fill_str(&out->data[i], argv[i], (int64_t)strlen(argv[i]));
    }
    return 0;
}

int32_t willrt_os_getenv(const void* name_raw, void* out_raw) {
    const os_str* name = (const os_str*)name_raw;
    os_str* out = (os_str*)out_raw;
    char* key;
    char* value;
    int invalid = 0;

    if (out == NULL) {
        return 0;
    }
    key = os_copy(name, &invalid);
    if (key == NULL || invalid) {
        willrt_free(key);
        return 0;
    }
    value = getenv(key);
    willrt_free(key);
    if (value == NULL) {
        return 0;
    }
    if (!os_valid_utf8((const unsigned char*)value, (int64_t)strlen(value))) {
        return 0;
    }
    os_fill_str(out, value, (int64_t)strlen(value));
    return 1;
}

int32_t willrt_os_environ(void* out_raw) {
    os_view* out = (os_view*)out_raw;
    int32_t count = 0;
    char** entries;
    int32_t valid = 0;
    int32_t i;

    if (out == NULL) {
        return 1;
    }
    entries = os_environ(&count);
    out->data = NULL;
    out->len = 0;
    if (count == 0 || entries == NULL) {
        return 0;
    }
    out->data = (os_str*)willrt_alloc(sizeof(os_str) * (size_t)count * 2);
    if (out->data == NULL) {
        return 1;
    }
    for (i = 0; i < count; i++) {
        char* entry = entries[i];
        char* eq = strchr(entry, '=');
        int64_t keyLen;
        int64_t valueLen;

        if (eq == NULL || eq == entry) {
            continue;
        }
        keyLen = (int64_t)(eq - entry);
        valueLen = (int64_t)strlen(eq + 1);
        if (!os_valid_utf8((const unsigned char*)entry, keyLen) ||
            !os_valid_utf8((const unsigned char*)(eq + 1), valueLen)) {
            continue;
        }
        os_fill_str(&out->data[valid * 2], entry, keyLen);
        os_fill_str(&out->data[valid * 2 + 1], eq + 1, valueLen);
        valid++;
    }
    out->len = valid * 2;
    return 0;
}

int32_t willrt_os_setenv(const void* name_raw, const void* value_raw) {
    const os_str* name = (const os_str*)name_raw;
    const os_str* value = (const os_str*)value_raw;
    char* key;
    char* val;
    int invalid = 0;
    int32_t status;

    key = os_copy(name, &invalid);
    if (key == NULL || invalid) {
        willrt_free(key);
        return -2;
    }
    val = os_copy(value, &invalid);
    if (val == NULL || invalid) {
        willrt_free(key);
        willrt_free(val);
        return -2;
    }
#if defined(_WIN32)
    status = _putenv_s(key, val) == 0 ? 0 : -1;
#else
    status = setenv(key, val, 1) == 0 ? 0 : -1;
#endif
    willrt_free(key);
    willrt_free(val);
    return status;
}

int32_t willrt_os_cwd(void* out_raw) {
    os_str* out = (os_str*)out_raw;
    char buffer[4096];
    char* result;
    int64_t len;

    if (out == NULL) {
        return 1;
    }
#if defined(_WIN32)
    result = _getcwd(buffer, (int)sizeof(buffer));
#else
    result = getcwd(buffer, sizeof(buffer));
#endif
    if (result == NULL) {
        return 1;
    }
    len = (int64_t)strlen(buffer);
    os_fill_str(out, buffer, len);
    /* Cwd's str must outlive this frame; copy it into process storage. */
    {
        char* copy = (char*)willrt_alloc((size_t)len);
        if (copy == NULL) {
            return 1;
        }
        memcpy(copy, buffer, (size_t)len);
        out->data = (const unsigned char*)copy;
    }
    return 0;
}

int32_t willrt_os_chdir(const void* path_raw) {
    const os_str* path = (const os_str*)path_raw;
    char* text;
    int invalid = 0;
    int32_t status;

    text = os_copy(path, &invalid);
    if (text == NULL || invalid) {
        willrt_free(text);
        return 1;
    }
#if defined(_WIN32)
    status = _chdir(text) == 0 ? 0 : 1;
#else
    status = chdir(text) == 0 ? 0 : 1;
#endif
    willrt_free(text);
    return status;
}

void willrt_os_exit(int32_t code) {
    exit((int)code);
}
