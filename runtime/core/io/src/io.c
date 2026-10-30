#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#endif

/* Async whole-file IO.
 *
 * One operation is a read of a whole file into a freshly allocated buffer, an
 * overwrite with a copied blob, or an append of that blob. The operation is
 * heap-owned and reference-counted: the caller keeps one reference until
 * finish, and the worker or the kernel keeps one until the completion is
 * delivered. A canceled or expired task publishes its status and drops the
 * caller reference; the completion then retires the operation on its own, so a
 * canceled frame never waits for the disk and the kernel never writes into a
 * frame that is gone.
 *
 * Windows submits ReadFile/WriteFile on a handle opened with
 * FILE_FLAG_OVERLAPPED and waits on a completion port. The completion key is
 * the operation, so one port pass finds it without a lookup. A small fixed
 * worker set opens handles and submits; opening can touch a network share, and
 * that cost must not run on a lane.
 *
 * POSIX has no completion interface for regular files: io_uring is Linux-only
 * and kqueue cannot report file data readiness. The platform form is therefore
 * a fixed worker set that performs pread/pwrite. It is not the generic blocking
 * pool: the count is bounded here, the worker owns the operation, and a
 * canceled wait leaves the worker to finish and free it. When a platform gains
 * a completion interface, only this file changes.
 */

#include "../../../include/willrt.h"

#include "io_completion.h"

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#ifdef __linux__
#include "io_uring_linux.h"
#endif
#endif

/* The caller's list[u8] payload. The layout is fixed by the runtime ABI. */
typedef struct willrt_io_array {
    void* data;
    int64_t len;
    int64_t cap;
} willrt_io_array;

enum {
    WILLRT_IO_KIND_READ = 0,
    WILLRT_IO_KIND_WRITE = 1,
    WILLRT_IO_KIND_APPEND = 2
};

enum {
    /* One reference for the caller until finish, one for the worker or the
       kernel until the completion. */
    WILLRT_IO_REFS = 2,
    WILLRT_IO_SUBMIT_WORKERS = 4,
    WILLRT_IO_POSIX_WORKERS = 4,
    WILLRT_IO_WORKERS = 16,
    WILLRT_IO_POLL_MS = 100,
    WILLRT_IO_DRAIN_STEPS = 5000
};

struct willrt_io_op {
    willrt_io_req req;
    willrt_io_op* next;
    willrt_task* waiter;
    char* path;
    int64_t path_len;
    void* buffer;
    int64_t length;
    void* out_array;
    int32_t kind;
    int32_t status;
    int32_t published;
    int32_t abandoned;
    int32_t caller_released;
    int32_t refs;
    int32_t file_open;
#ifdef _WIN32
    OVERLAPPED ov;
    HANDLE file;
#else
    int fd;
#endif
};

static atomic_int willrt_io_state;
static atomic_int willrt_io_lock_state;
static atomic_int willrt_io_stop;
static atomic_int willrt_io_live;
static willrt_os_mutex willrt_io_lock;
static willrt_os_cond willrt_io_cond;
static willrt_io_op* willrt_io_head;
static willrt_io_op* willrt_io_tail;
static int32_t willrt_io_started;
static willrt_os_thread willrt_io_workers[WILLRT_IO_WORKERS];
#ifdef _WIN32
static HANDLE willrt_io_port;
static willrt_os_thread willrt_io_completer;
#endif

static void willrt_io_lock_init(void) {
    int expected = 0;

    if (atomic_compare_exchange_strong(&willrt_io_lock_state, &expected, 1)) {
        willrt_os_mutex_init(&willrt_io_lock);
        willrt_os_cond_init(&willrt_io_cond);
        atomic_store(&willrt_io_lock_state, 2);
        return;
    }
    while (atomic_load(&willrt_io_lock_state) != 2) {
        willrt_os_thread_yield();
    }
}

static char* willrt_io_copy_path(const void* path, int64_t len) {
    const char* data = (const char*)path;
    char* out;

    if (data == NULL || len < 0) {
        return NULL;
    }
    out = (char*)willrt_alloc((size_t)len + 1);
    if (out == NULL) {
        return NULL;
    }
    memcpy(out, data, (size_t)len);
    out[len] = '\0';
    return out;
}

static void willrt_io_free(willrt_io_op* op) {
    if (op->buffer != NULL) {
        willrt_free(op->buffer);
    }
    if (op->path != NULL) {
        willrt_free(op->path);
    }
    atomic_fetch_sub(&willrt_io_live, 1);
    willrt_free(op);
}

/* willrt_io_publish makes the status visible and hands a successful read to
   the caller's array before the waiting task can run. It runs once; a
   completion that loses the race to a cancel changes nothing. */
static void willrt_io_publish(willrt_io_op* op, int32_t status, int64_t bytes) {
    willrt_task* waiter = NULL;

    willrt_os_mutex_lock(&willrt_io_lock);
    if (!op->published) {
        if (status == 0 && op->kind == WILLRT_IO_KIND_READ) {
            willrt_io_array* out = (willrt_io_array*)op->out_array;
            if (out != NULL) {
                out->data = op->buffer;
                out->len = bytes;
                out->cap = op->length;
            }
            op->buffer = NULL;
        }
        op->status = status;
        op->published = 1;
        waiter = op->waiter;
        op->waiter = NULL;
    }
    willrt_os_mutex_unlock(&willrt_io_lock);
    if (waiter != NULL) {
        willrt_task_wake_chan(waiter);
    }
}

/* willrt_io_release drops the completion's reference. */
static void willrt_io_release(willrt_io_op* op) {
    int32_t free_op = 0;

    willrt_os_mutex_lock(&willrt_io_lock);
    op->refs--;
    if (op->refs == 0) {
        free_op = 1;
    }
    willrt_os_mutex_unlock(&willrt_io_lock);
    if (free_op) {
        willrt_io_free(op);
    }
}

#ifdef __linux__
static int32_t willrt_io_ring_used;

static void willrt_io_complete_linux(void* raw, int32_t status,
                                     int64_t bytes) {
    willrt_io_op* op = (willrt_io_op*)raw;

    willrt_os_mutex_lock(&willrt_io_lock);
    if (op->file_open) {
        close(op->fd);
        op->file_open = 0;
    }
    willrt_os_mutex_unlock(&willrt_io_lock);
    willrt_io_publish(op, status == 0 ? WILLRT_OK : WILLRT_FAILED, bytes);
    willrt_io_release(op);
}
#endif

#ifdef _WIN32
static wchar_t* willrt_io_wide(const char* path, int64_t len) {
    wchar_t* out;
    int count;

    if (len > (int64_t)INT32_MAX) {
        return NULL;
    }
    count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, (int)len,
                                NULL, 0);
    if (count <= 0) {
        return NULL;
    }
    out = (wchar_t*)willrt_alloc(((size_t)count + 1) * sizeof(wchar_t));
    if (out == NULL) {
        return NULL;
    }
    if (MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, (int)len, out,
                            count) != count) {
        willrt_free(out);
        return NULL;
    }
    out[count] = L'\0';
    return out;
}

/* The caller holds the operation lock, so a cancel in poll cannot race the
   close and the handle cannot be reused by another open in between. */
static void willrt_io_close_locked(willrt_io_op* op) {
    if (op->file_open) {
        CloseHandle(op->file);
        op->file = INVALID_HANDLE_VALUE;
        op->file_open = 0;
    }
}

static void willrt_io_fail_submit(willrt_io_op* op) {
    willrt_os_mutex_lock(&willrt_io_lock);
    willrt_io_close_locked(op);
    willrt_os_mutex_unlock(&willrt_io_lock);
    willrt_io_publish(op, WILLRT_FAILED, 0);
    willrt_io_release(op);
}

static void willrt_io_submit_windows(willrt_io_op* op) {
    wchar_t* wide;
    DWORD access;
    DWORD create;
    HANDLE file;
    LARGE_INTEGER size;

    if (atomic_load(&willrt_io_stop) != 0 || op->abandoned) {
        willrt_io_release(op);
        return;
    }
    wide = willrt_io_wide(op->path, op->path_len);
    if (wide == NULL) {
        willrt_io_publish(op, WILLRT_FAILED, 0);
        willrt_io_release(op);
        return;
    }
    if (op->kind == WILLRT_IO_KIND_READ) {
        access = GENERIC_READ;
        create = OPEN_EXISTING;
    } else if (op->kind == WILLRT_IO_KIND_WRITE) {
        access = GENERIC_WRITE;
        create = CREATE_ALWAYS;
    } else {
        access = FILE_APPEND_DATA;
        create = OPEN_ALWAYS;
    }
    file = CreateFileW(wide, access,
                       FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                       NULL, create, FILE_FLAG_OVERLAPPED, NULL);
    willrt_free(wide);
    if (file == INVALID_HANDLE_VALUE) {
        willrt_io_publish(op, WILLRT_FAILED, 0);
        willrt_io_release(op);
        return;
    }
    if (CreateIoCompletionPort(file, willrt_io_port, (ULONG_PTR)op, 0) == NULL) {
        CloseHandle(file);
        willrt_io_publish(op, WILLRT_FAILED, 0);
        willrt_io_release(op);
        return;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    op->file = file;
    op->file_open = 1;
    willrt_os_mutex_unlock(&willrt_io_lock);

    memset(&op->ov, 0, sizeof(op->ov));
    if (op->kind == WILLRT_IO_KIND_READ) {
        if (!GetFileSizeEx(file, &size) || size.QuadPart < 0) {
            willrt_io_fail_submit(op);
            return;
        }
        if (size.QuadPart == 0) {
            willrt_os_mutex_lock(&willrt_io_lock);
            willrt_io_close_locked(op);
            willrt_os_mutex_unlock(&willrt_io_lock);
            willrt_io_publish(op, WILLRT_OK, 0);
            willrt_io_release(op);
            return;
        }
        /* ReadFile takes a DWORD byte count; a whole-file read therefore stops
           at 4 GiB on this platform. */
        if (size.QuadPart > (LONGLONG)UINT32_MAX) {
            willrt_io_fail_submit(op);
            return;
        }
        op->buffer = willrt_alloc((size_t)size.QuadPart);
        if (op->buffer == NULL) {
            willrt_io_fail_submit(op);
            return;
        }
        op->length = size.QuadPart;
        if (!ReadFile(file, op->buffer, (DWORD)size.QuadPart, NULL, &op->ov)
            && GetLastError() != ERROR_IO_PENDING) {
            willrt_io_fail_submit(op);
        }
        return;
    }
    if (op->length > (int64_t)UINT32_MAX) {
        willrt_io_fail_submit(op);
        return;
    }
    if (op->kind == WILLRT_IO_KIND_APPEND) {
        /* An all-ones offset writes at the current end of the file even when
           another writer appends at the same time. */
        op->ov.Offset = 0xFFFFFFFFu;
        op->ov.OffsetHigh = 0xFFFFFFFFu;
    }
    if (!WriteFile(file, op->buffer, (DWORD)op->length, NULL, &op->ov)
        && GetLastError() != ERROR_IO_PENDING) {
        willrt_io_fail_submit(op);
    }
}

static void willrt_io_complete_windows(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    while (atomic_load(&willrt_io_stop) == 0) {
        DWORD bytes = 0;
        ULONG_PTR key = 0;
        OVERLAPPED* ov = NULL;
        BOOL ok;

        ok = GetQueuedCompletionStatus(willrt_io_port, &bytes, &key, &ov,
                                       WILLRT_IO_POLL_MS);
        if (ov == NULL) {
            continue;
        }
        {
            willrt_io_op* op = (willrt_io_op*)key;
            willrt_os_mutex_lock(&willrt_io_lock);
            willrt_io_close_locked(op);
            willrt_os_mutex_unlock(&willrt_io_lock);
            willrt_io_publish(op, ok ? WILLRT_OK : WILLRT_FAILED,
                              (int64_t)bytes);
            willrt_io_release(op);
        }
    }
    willrt_mem_thread_flush();
}

static void willrt_io_submit_worker(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    while (atomic_load(&willrt_io_stop) == 0) {
        willrt_io_op* op;

        willrt_os_mutex_lock(&willrt_io_lock);
        while (willrt_io_head == NULL && atomic_load(&willrt_io_stop) == 0) {
            (void)willrt_os_cond_timedwait(&willrt_io_cond, &willrt_io_lock,
                                           WILLRT_IO_POLL_MS * 1000000);
        }
        op = willrt_io_head;
        if (op != NULL) {
            willrt_io_head = op->next;
            if (willrt_io_head == NULL) {
                willrt_io_tail = NULL;
            }
            op->next = NULL;
        }
        willrt_os_mutex_unlock(&willrt_io_lock);
        if (op != NULL) {
            willrt_io_submit_windows(op);
        }
    }
    willrt_mem_thread_flush();
}
#else
static void willrt_io_run_posix(willrt_io_op* op) {
    int flags;
    int fd;
    struct stat info;
    int64_t done = 0;

    if (atomic_load(&willrt_io_stop) != 0 || op->abandoned) {
        willrt_io_release(op);
        return;
    }
    if (op->kind == WILLRT_IO_KIND_READ) {
        flags = O_RDONLY;
    } else if (op->kind == WILLRT_IO_KIND_WRITE) {
        flags = O_WRONLY | O_CREAT | O_TRUNC;
    } else {
        flags = O_WRONLY | O_CREAT | O_APPEND;
    }
    fd = open(op->path, flags, 0644);
    if (fd < 0) {
        willrt_io_publish(op, WILLRT_FAILED, 0);
        willrt_io_release(op);
        return;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    op->fd = fd;
    op->file_open = 1;
    willrt_os_mutex_unlock(&willrt_io_lock);

    if (op->kind == WILLRT_IO_KIND_READ) {
        if (fstat(fd, &info) != 0 || info.st_size < 0) {
            goto fail;
        }
        op->length = (int64_t)info.st_size;
        if (op->length == 0) {
            close(fd);
            willrt_os_mutex_lock(&willrt_io_lock);
            op->file_open = 0;
            willrt_os_mutex_unlock(&willrt_io_lock);
            willrt_io_publish(op, WILLRT_OK, 0);
            willrt_io_release(op);
            return;
        }
        op->buffer = willrt_alloc((size_t)op->length);
        if (op->buffer == NULL) {
            goto fail;
        }
#ifdef __linux__
        if (willrt_io_ring_used
            && willrt_io_ring_submit(fd, op->kind, op->buffer, op->length,
                                     &op->req) == 0) {
            return;
        }
#endif
        while (done < op->length) {
            ssize_t got = pread(fd, (char*)op->buffer + done,
                                (size_t)(op->length - done), (off_t)done);
            if (got < 0 && errno == EINTR) {
                continue;
            }
            if (got < 0) {
                goto fail;
            }
            if (got == 0) {
                break;
            }
            done += got;
        }
    } else {
#ifdef __linux__
        if (willrt_io_ring_used
            && willrt_io_ring_submit(fd, op->kind, op->buffer, op->length,
                                     &op->req) == 0) {
            return;
        }
#endif
        while (done < op->length) {
            ssize_t put = write(fd, (const char*)op->buffer + done,
                                (size_t)(op->length - done));
            if (put < 0 && errno == EINTR) {
                continue;
            }
            if (put < 0) {
                goto fail;
            }
            done += put;
        }
    }
    close(fd);
    willrt_os_mutex_lock(&willrt_io_lock);
    op->file_open = 0;
    willrt_os_mutex_unlock(&willrt_io_lock);
    willrt_io_publish(op, WILLRT_OK, done);
    willrt_io_release(op);
    return;

fail:
    close(fd);
    willrt_os_mutex_lock(&willrt_io_lock);
    op->file_open = 0;
    willrt_os_mutex_unlock(&willrt_io_lock);
    willrt_io_publish(op, WILLRT_FAILED, 0);
    willrt_io_release(op);
}

static void willrt_io_worker(void* ignored) {
    (void)ignored;
    willrt_mem_set_lane(0);
    while (atomic_load(&willrt_io_stop) == 0) {
        willrt_io_op* op;

        willrt_os_mutex_lock(&willrt_io_lock);
        while (willrt_io_head == NULL && atomic_load(&willrt_io_stop) == 0) {
            (void)willrt_os_cond_timedwait(&willrt_io_cond, &willrt_io_lock,
                                           WILLRT_IO_POLL_MS * 1000000);
        }
        op = willrt_io_head;
        if (op != NULL) {
            willrt_io_head = op->next;
            if (willrt_io_head == NULL) {
                willrt_io_tail = NULL;
            }
            op->next = NULL;
        }
        willrt_os_mutex_unlock(&willrt_io_lock);
        if (op != NULL) {
            willrt_io_run_posix(op);
        }
    }
    willrt_mem_thread_flush();
}
#endif

static void willrt_io_init(void) {
    int expected = 0;
    int32_t wanted;
    int32_t i;

    willrt_io_lock_init();
    if (atomic_compare_exchange_strong(&willrt_io_state, &expected, 1)) {
        willrt_mem_set_shared();
        willrt_io_head = NULL;
        willrt_io_tail = NULL;
        willrt_io_started = 0;
        atomic_store(&willrt_io_stop, 0);
#ifdef _WIN32
        wanted = WILLRT_IO_SUBMIT_WORKERS;
        willrt_io_port = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0,
                                                0);
        if (willrt_io_port == NULL
            || willrt_os_thread_start(&willrt_io_completer,
                                      willrt_io_complete_windows, NULL)
                   != 0) {
            atomic_store(&willrt_io_state, 0);
            return;
        }
        for (i = 0; i < wanted; i++) {
            if (willrt_os_thread_start(&willrt_io_workers[i],
                                       willrt_io_submit_worker, NULL)
                != 0) {
                break;
            }
            willrt_io_started++;
        }
#else
#ifdef __linux__
        willrt_io_ring_used = willrt_io_ring_start() == 0;
#endif
        wanted = WILLRT_IO_POSIX_WORKERS;
        for (i = 0; i < wanted; i++) {
            if (willrt_os_thread_start(&willrt_io_workers[i],
                                       willrt_io_worker, NULL)
                != 0) {
                break;
            }
            willrt_io_started++;
        }
#endif
        atomic_store(&willrt_io_state, 2);
        return;
    }
    while (atomic_load(&willrt_io_state) == 1) {
        willrt_os_thread_yield();
    }
}

/* willrt_io_new builds the operation and hands it to the workers. A failure to
   start the workers publishes an error on the operation instead of returning
   NULL, so the caller still has a handle to poll and finish. */
static willrt_io_op* willrt_io_new(int32_t kind, const void* path,
                                   int64_t path_len, const void* data,
                                   int64_t len, void* out_array) {
    willrt_io_op* op;

    if (path == NULL || path_len < 0 || len < 0) {
        return NULL;
    }
    op = (willrt_io_op*)willrt_alloc(sizeof(*op));
    if (op == NULL) {
        return NULL;
    }
    memset(op, 0, sizeof(*op));
#ifdef __linux__
    op->req.complete = willrt_io_complete_linux;
    op->req.owner = op;
#endif
    op->path = willrt_io_copy_path(path, path_len);
    if (op->path == NULL) {
        willrt_free(op);
        return NULL;
    }
    op->path_len = path_len;
    op->kind = kind;
    op->out_array = out_array;
    op->refs = WILLRT_IO_REFS;
    if (kind != WILLRT_IO_KIND_READ && len > 0) {
        op->buffer = willrt_alloc((size_t)len);
        if (op->buffer == NULL) {
            willrt_free(op->path);
            willrt_free(op);
            return NULL;
        }
        memcpy(op->buffer, data, (size_t)len);
        op->length = len;
    }
    op->waiter = willrt_task_current();
    willrt_task_set_io(op);
    atomic_fetch_add(&willrt_io_live, 1);
    willrt_io_init();
    if (atomic_load(&willrt_io_state) != 2) {
        willrt_io_publish(op, WILLRT_FAILED, 0);
        /* No worker owns a reference on this path. */
        op->refs = 1;
        return op;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    op->next = NULL;
    if (willrt_io_tail != NULL) {
        willrt_io_tail->next = op;
    } else {
        willrt_io_head = op;
    }
    willrt_io_tail = op;
    willrt_os_cond_signal(&willrt_io_cond);
    willrt_os_mutex_unlock(&willrt_io_lock);
    return op;
}

willrt_io_op* willrt_io_read_file(const void* path, int64_t path_len,
                                  void* out_array) {
    return willrt_io_new(WILLRT_IO_KIND_READ, path, path_len, NULL, 0,
                         out_array);
}

willrt_io_op* willrt_io_write_file(const void* path, int64_t path_len,
                                   const void* data, int64_t len,
                                   int32_t append) {
    return willrt_io_new(append ? WILLRT_IO_KIND_APPEND : WILLRT_IO_KIND_WRITE,
                         path, path_len, data, len, NULL);
}

int32_t willrt_io_poll(willrt_io_op* op) {
    struct willrt_task* task = willrt_task_current();
    int32_t canceled;
    int32_t expired = 0;
    int64_t deadline = 0;

    if (op == NULL) {
        return 1;
    }
    canceled = task != NULL && willrt_task_canceled();
    if (task != NULL) {
        deadline = willrt_task_deadline();
        expired = deadline != 0 && willrt_now_ns() >= deadline;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    if (op->published) {
        willrt_os_mutex_unlock(&willrt_io_lock);
        return 1;
    }
    if (canceled || expired) {
        op->abandoned = 1;
        op->status = canceled ? WILLRT_CANCELED : WILLRT_TIMEDOUT;
        op->published = 1;
        op->waiter = NULL;
#ifdef _WIN32
        if (op->file_open) {
            /* Cancel under the lock so the handle cannot be closed and reused
               by another open before the cancel reaches the kernel. */
            (void)CancelIoEx(op->file, &op->ov);
        }
#endif
        willrt_os_mutex_unlock(&willrt_io_lock);
        willrt_task_clear_io(op);
        return 1;
    }
    willrt_os_mutex_unlock(&willrt_io_lock);
    return 0;
}

int32_t willrt_io_finish(willrt_io_op* op) {
    int32_t status;
    int32_t free_op = 0;

    if (op == NULL) {
        return WILLRT_FAILED;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    if (!op->published) {
        willrt_os_mutex_unlock(&willrt_io_lock);
        return WILLRT_SUSPEND;
    }
    status = op->status;
    if (!op->caller_released) {
        op->caller_released = 1;
        op->refs--;
        if (op->refs == 0) {
            free_op = 1;
        }
    }
    willrt_os_mutex_unlock(&willrt_io_lock);
    willrt_task_clear_io(op);
    if (free_op) {
        willrt_io_free(op);
    }
    return status;
}

int64_t willrt_io_deadline(willrt_io_op* op) {
    (void)op;
    return 0;
}

int32_t willrt_io_wait(willrt_io_op* op) {
    if (op == NULL) {
        return WILLRT_FAILED;
    }
    while (willrt_io_poll(op) == 0) {
        willrt_os_sleep_ns(1000000);
    }
    return willrt_io_finish(op);
}

/* willrt_io_abandon is called when the task leaves its frame while the disk is
   busy. The caller reference belongs to that frame and is dropped here; the
   completion keeps the operation alive until the kernel is done with it. */
void willrt_io_abandon(void* raw) {
    willrt_io_op* op = (willrt_io_op*)raw;
    int32_t free_op = 0;

    if (op == NULL) {
        return;
    }
    willrt_os_mutex_lock(&willrt_io_lock);
    if (!op->caller_released) {
        op->caller_released = 1;
        op->refs--;
        if (op->refs == 0) {
            free_op = 1;
        }
    }
    if (!op->published) {
        op->abandoned = 1;
        op->status = WILLRT_FAILED;
        op->published = 1;
        op->waiter = NULL;
    }
#ifdef _WIN32
    if (op->file_open) {
        (void)CancelIoEx(op->file, &op->ov);
    }
#endif
    willrt_os_mutex_unlock(&willrt_io_lock);
    if (free_op) {
        willrt_io_free(op);
    }
}

void willrt_io_shutdown(void) {
    int32_t i;

    if (atomic_load(&willrt_io_state) != 2) {
        return;
    }
    /* Tasks wait before shutdown, but a canceled operation may still be in the
       kernel; let its completion release the last reference first. */
    for (i = 0; i < WILLRT_IO_DRAIN_STEPS && atomic_load(&willrt_io_live) != 0;
         i++) {
        willrt_os_sleep_ns(1000000);
    }
    atomic_store(&willrt_io_stop, 1);
    willrt_os_mutex_lock(&willrt_io_lock);
    willrt_os_cond_broadcast(&willrt_io_cond);
    willrt_os_mutex_unlock(&willrt_io_lock);
#ifdef _WIN32
    willrt_os_thread_join(&willrt_io_completer);
    for (i = 0; i < willrt_io_started; i++) {
        willrt_os_thread_join(&willrt_io_workers[i]);
    }
    if (willrt_io_port != NULL) {
        CloseHandle(willrt_io_port);
        willrt_io_port = NULL;
    }
#else
    for (i = 0; i < willrt_io_started; i++) {
        willrt_os_thread_join(&willrt_io_workers[i]);
    }
#ifdef __linux__
    willrt_io_ring_stop();
    willrt_io_ring_used = 0;
#endif
#endif
    willrt_io_started = 0;
    atomic_store(&willrt_io_stop, 0);
    atomic_store(&willrt_io_state, 0);
}
