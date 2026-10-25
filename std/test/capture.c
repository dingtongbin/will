/* Output capture for std/test.
 *
 * The runtime writes standard output and standard error through host handles,
 * not through stdio, so capture has to redirect those handles. A temporary file
 * makes the captured bytes readable without a pipe that could fill and block.
 */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#define _DEFAULT_SOURCE 1
#endif

#include "capture.h"

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

#if defined(_WIN32)
#include <io.h>
#include <windows.h>
#else
#include <unistd.h>
#endif

void* willrt_alloc(size_t size);
int64_t willrt_now_ns(void);

/* The will str header: a data pointer followed by a byte length. */
typedef struct test_str {
    unsigned char* data;
    int64_t len;
} test_str;

static FILE* capture_file = NULL;
static int capture_active = 0;
#if defined(_WIN32)
static HANDLE saved_out = INVALID_HANDLE_VALUE;
static HANDLE saved_err = INVALID_HANDLE_VALUE;
#else
static int saved_out = -1;
static int saved_err = -1;
#endif

int32_t willtest_capture_begin(void) {
    if (capture_active) {
        return -1;
    }
    capture_file = tmpfile();
    if (capture_file == NULL) {
        return -1;
    }
    fflush(NULL);
#if defined(_WIN32)
    {
        intptr_t raw = _get_osfhandle(_fileno(capture_file));
        HANDLE handle = (HANDLE)raw;
        saved_out = GetStdHandle(STD_OUTPUT_HANDLE);
        saved_err = GetStdHandle(STD_ERROR_HANDLE);
        if (handle == INVALID_HANDLE_VALUE) {
            fclose(capture_file);
            capture_file = NULL;
            return -1;
        }
        SetStdHandle(STD_OUTPUT_HANDLE, handle);
        SetStdHandle(STD_ERROR_HANDLE, handle);
    }
#else
    saved_out = dup(STDOUT_FILENO);
    saved_err = dup(STDERR_FILENO);
    if (saved_out < 0 || saved_err < 0 || dup2(fileno(capture_file), STDOUT_FILENO) < 0 ||
        dup2(fileno(capture_file), STDERR_FILENO) < 0) {
        if (saved_out >= 0) {
            close(saved_out);
        }
        if (saved_err >= 0) {
            close(saved_err);
        }
        fclose(capture_file);
        capture_file = NULL;
        return -1;
    }
#endif
    capture_active = 1;
    return 0;
}

int32_t willtest_capture_end(void) {
    if (!capture_active) {
        return -1;
    }
    fflush(NULL);
#if defined(_WIN32)
    SetStdHandle(STD_OUTPUT_HANDLE, saved_out);
    SetStdHandle(STD_ERROR_HANDLE, saved_err);
    saved_out = INVALID_HANDLE_VALUE;
    saved_err = INVALID_HANDLE_VALUE;
#else
    dup2(saved_out, STDOUT_FILENO);
    dup2(saved_err, STDERR_FILENO);
    close(saved_out);
    close(saved_err);
    saved_out = -1;
    saved_err = -1;
#endif
    capture_active = 0;
    return 0;
}

int32_t willtest_capture_read(void* out_raw) {
    test_str* out = (test_str*)out_raw;
    long long size;
    long long got;

    if (out == NULL || capture_file == NULL) {
        return -1;
    }
    fflush(NULL);
#if defined(_WIN32)
    if (_fseeki64(capture_file, 0, SEEK_END) != 0) {
        return -1;
    }
    size = _ftelli64(capture_file);
    if (size < 0 || _fseeki64(capture_file, 0, SEEK_SET) != 0) {
        return -1;
    }
#else
    if (fseeko(capture_file, 0, SEEK_END) != 0) {
        return -1;
    }
    size = (long long)ftello(capture_file);
    if (size < 0 || fseeko(capture_file, 0, SEEK_SET) != 0) {
        return -1;
    }
#endif
    out->data = (unsigned char*)willrt_alloc((size_t)size + 1u);
    if (out->data == NULL) {
        out->len = 0;
        return -1;
    }
    got = (long long)fread(out->data, 1, (size_t)size, capture_file);
    out->len = got < 0 ? 0 : got;
    out->data[out->len] = '\0';
#if defined(_WIN32)
    _fseeki64(capture_file, 0, SEEK_END);
#else
    fseeko(capture_file, 0, SEEK_END);
#endif
    return 0;
}

void willtest_capture_close(void) {
    if (capture_file != NULL) {
        fclose(capture_file);
        capture_file = NULL;
    }
}

int64_t willtest_now_ns(void) {
    return willrt_now_ns();
}
