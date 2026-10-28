/* Contract tests for async whole-file IO. The process exits zero when every
   assertion holds and aborts through assert on the first failure. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

typedef struct io_array {
    void* data;
    int64_t len;
    int64_t cap;
} io_array;

static void write_bytes(const char* path, const void* data, int64_t len,
                        int32_t append) {
    int32_t status;
    willrt_io_op* op = willrt_io_write_file(path, (int64_t)strlen(path), data,
                                            len, append);

    assert(op != NULL);
    status = willrt_io_wait(op);
    assert(status == WILLRT_OK);
}

static void write_text(const char* path, const char* text, int32_t append) {
    write_bytes(path, text, (int64_t)strlen(text), append);
}

static void read_text(const char* path, io_array* out, int32_t* status) {
    willrt_io_op* op = willrt_io_read_file(path, (int64_t)strlen(path), out);

    assert(op != NULL);
    *status = willrt_io_wait(op);
}

static void test_roundtrip(void) {
    const char* path = "will-io-test-roundtrip.tmp";
    char data[1024];
    io_array out = {0};
    int32_t status;

    for (int32_t i = 0; i < 1024; i++) {
        data[i] = (char)(i * 7);
    }
    write_bytes(path, data, 1024, 0);
    read_text(path, &out, &status);
    assert(status == WILLRT_OK);
    assert(out.len == 1024);
    assert(out.cap >= 1024);
    assert(memcmp(out.data, data, 1024) == 0);
    willrt_free(out.data);
    assert(remove(path) == 0);
}

static void test_append(void) {
    const char* path = "will-io-test-append.tmp";
    io_array out = {0};
    int32_t status;

    write_text(path, "abc", 0);
    write_text(path, "def", 1);
    read_text(path, &out, &status);
    assert(status == WILLRT_OK);
    assert(out.len == 6);
    assert(memcmp(out.data, "abcdef", 6) == 0);
    willrt_free(out.data);
    assert(remove(path) == 0);
}

static void test_missing(void) {
    io_array out = {0};
    int32_t status = WILLRT_OK;

    read_text("will-io-test-missing.tmp", &out, &status);
    assert(status != WILLRT_OK);
    assert(out.data == NULL);
    assert(out.len == 0);
}

/* read_frame drives one whole-file read the way a generated state machine
   does: start once, poll, park, resume, finish. */
typedef struct read_frame {
    const char* path;
    int64_t path_len;
    io_array out;
    willrt_io_op* op;
    int32_t status;
    int32_t phase;
    int32_t scope_ns;
    int64_t previous;
} read_frame;

static int32_t step_read(void* raw) {
    read_frame* f = (read_frame*)raw;

    if (f->phase == 0) {
        f->phase = 1;
        if (f->scope_ns > 0) {
            f->previous = willrt_task_enter_deadline(f->scope_ns);
        }
        f->op = willrt_io_read_file(f->path, f->path_len, &f->out);
        assert(f->op != NULL);
    }
    if (willrt_io_poll(f->op) == 0) {
        willrt_task_mark_wait();
        return WILLRT_SUSPEND;
    }
    f->status = willrt_io_finish(f->op);
    if (f->scope_ns > 0) {
        willrt_task_exit_deadline(f->previous);
    }
    return WILLRT_DONE;
}

static willrt_task* run_task(void* step, void* frame, size_t size) {
    static int32_t next_lane;
    int32_t lanes = willrt_os_cpu_count();
    int32_t lane;
    willrt_task* task;

    if (lanes < 1) {
        lanes = 1;
    }
    lane = __atomic_fetch_add(&next_lane, 1, __ATOMIC_RELAXED) % lanes;
    task = willrt_task_new(step, frame, size, lane, 0);
    assert(task != NULL);
    willrt_task_run(task);
    return task;
}

static void test_parallel_reads(void) {
    enum { FILES = 4, SIZE = 262144 };
    static const char* names[FILES] = {
        "will-io-test-p0.tmp",
        "will-io-test-p1.tmp",
        "will-io-test-p2.tmp",
        "will-io-test-p3.tmp"
    };
    char* data = (char*)willrt_alloc(SIZE);
    read_frame frames[FILES];
    willrt_task* tasks[FILES];

    assert(data != NULL);
    for (int32_t f = 0; f < FILES; f++) {
        for (int32_t i = 0; i < SIZE; i++) {
            data[i] = (char)(i + f);
        }
        write_bytes(names[f], data, SIZE, 0);
        memset(&frames[f], 0, sizeof(frames[f]));
        frames[f].path = names[f];
        frames[f].path_len = (int64_t)strlen(names[f]);
        tasks[f] = run_task((void*)step_read, &frames[f], sizeof(frames[f]));
    }
    for (int32_t f = 0; f < FILES; f++) {
        assert(willrt_join(tasks[f], NULL, NULL) == WILLRT_OK);
        assert(frames[f].status == WILLRT_OK);
        assert(frames[f].out.len == SIZE);
        for (int32_t i = 0; i < SIZE; i++) {
            assert(((const char*)frames[f].out.data)[i] == (char)(i + f));
        }
        willrt_free(frames[f].out.data);
        assert(remove(names[f]) == 0);
    }
    willrt_free(data);
}

/* test_cancel starts a large read with a cancel flag that is already set, so
   the first poll resolves the wait without waiting for the disk. */
static void test_cancel(void) {
    const char* path = "will-io-test-cancel.tmp";
    enum { SIZE = 16 * 1024 * 1024 };
    char* data = (char*)willrt_alloc(SIZE);
    read_frame frame;
    willrt_task* task;
    int32_t canceled = 1;
    void* cancel_flag = &canceled;

    assert(data != NULL);
    memset(data, 9, SIZE);
    write_bytes(path, data, SIZE, 0);
    willrt_free(data);
    memset(&frame, 0, sizeof(frame));
    frame.path = path;
    frame.path_len = (int64_t)strlen(path);
    task = willrt_task_new((void*)step_read, &frame, sizeof(frame), 0, 0);
    assert(task != NULL);
    willrt_task_bind_cancel(task, &cancel_flag);
    willrt_task_run(task);
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_CANCELED);
    assert(frame.out.data == NULL);
    assert(remove(path) == 0);
}

static void test_deadline(void) {
    const char* path = "will-io-test-deadline.tmp";
    enum { SIZE = 16 * 1024 * 1024 };
    char* data = (char*)willrt_alloc(SIZE);
    read_frame frame;
    willrt_task* task;

    assert(data != NULL);
    memset(data, 3, SIZE);
    write_bytes(path, data, SIZE, 0);
    willrt_free(data);
    memset(&frame, 0, sizeof(frame));
    frame.path = path;
    frame.path_len = (int64_t)strlen(path);
    frame.scope_ns = 1;
    task = run_task((void*)step_read, &frame, sizeof(frame));
    assert(willrt_join(task, NULL, NULL) == WILLRT_OK);
    assert(frame.status == WILLRT_TIMEDOUT);
    assert(remove(path) == 0);
}

int main(void) {
    test_roundtrip();
    test_append();
    test_missing();
    test_parallel_reads();
    test_cancel();
    test_deadline();
    willrt_io_shutdown();
    return 0;
}
