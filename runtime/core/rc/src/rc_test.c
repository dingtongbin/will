/* Contract tests for the rc slot table. The process exits zero when every
   assertion holds and stops through assert on the first failure. */

#include "rc.h"

#include <assert.h>
#include <stdint.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <pthread.h>
#endif

enum {
    WILLRT_TEST_FIRST_BATCH = 200,
    WILLRT_TEST_TOTAL = 700,
    WILLRT_TEST_THREADS = 8,
    WILLRT_TEST_ITERATIONS = 20000
};

struct willrt_test_work {
    uint64_t handle;
    int iterations;
};

/* willrt_test_work_run keeps and drops one reference per iteration. The main
   thread holds a reference for the whole run, so the object never reaches
   zero inside a worker. */
static void willrt_test_work_run(struct willrt_test_work* work) {
    int i;

    for (i = 0; i < work->iterations; i++) {
        willrt_rc_retain(work->handle);
        willrt_rc_release(work->handle);
    }
}

#if defined(_WIN32)
static DWORD WINAPI willrt_test_worker(LPVOID argument) {
    willrt_test_work_run((struct willrt_test_work*)argument);
    return 0;
}

static void willrt_test_run_threads(struct willrt_test_work* work, size_t count) {
    HANDLE threads[WILLRT_TEST_THREADS];
    size_t i;

    for (i = 0; i < count; i++) {
        threads[i] = CreateThread(NULL, 0, willrt_test_worker, &work[i], 0,
                                  NULL);
        assert(threads[i] != NULL);
    }
    for (i = 0; i < count; i++) {
        assert(WaitForSingleObject(threads[i], INFINITE) == WAIT_OBJECT_0);
        CloseHandle(threads[i]);
    }
}
#else
static void* willrt_test_worker(void* argument) {
    willrt_test_work_run((struct willrt_test_work*)argument);
    return NULL;
}

static void willrt_test_run_threads(struct willrt_test_work* work, size_t count) {
    pthread_t threads[WILLRT_TEST_THREADS];
    size_t i;

    for (i = 0; i < count; i++) {
        assert(pthread_create(&threads[i], NULL, willrt_test_worker,
                              &work[i]) == 0);
    }
    for (i = 0; i < count; i++) {
        assert(pthread_join(threads[i], NULL) == 0);
    }
}
#endif

static void test_growth_preserves_slots(void) {
    static uint64_t handles[WILLRT_TEST_TOTAL];
    static int values[WILLRT_TEST_TOTAL];
    size_t capacity;
    int i;

    for (i = 0; i < WILLRT_TEST_FIRST_BATCH; i++) {
        values[i] = (int)i;
        handles[i] = willrt_rc_new(&values[i], sizeof(values[i]));
        assert(handles[i] != 0);
    }

    for (i = 1; i < WILLRT_TEST_FIRST_BATCH; i += 2) {
        willrt_rc_release(handles[i]);
    }

    for (i = WILLRT_TEST_FIRST_BATCH; i < WILLRT_TEST_TOTAL; i++) {
        values[i] = (int)i;
        handles[i] = willrt_rc_new(&values[i], sizeof(values[i]));
        assert(handles[i] != 0);
    }

    capacity = willrt_rc_slot_capacity();
    assert(capacity >= (size_t)WILLRT_TEST_TOTAL);

    for (i = 0; i < WILLRT_TEST_FIRST_BATCH; i++) {
        void* object = willrt_rc_deref(handles[i]);

        if ((i & 1) != 0) {
            assert(object == NULL);
        } else {
            assert(object != NULL);
            assert(*(int*)object == values[i]);
        }
    }
    for (i = WILLRT_TEST_FIRST_BATCH; i < WILLRT_TEST_TOTAL; i++) {
        void* object = willrt_rc_deref(handles[i]);

        assert(object != NULL);
        assert(*(int*)object == values[i]);
    }

    for (i = 0; i < WILLRT_TEST_TOTAL; i++) {
        if (i >= WILLRT_TEST_FIRST_BATCH || (i & 1) == 0) {
            willrt_rc_release(handles[i]);
        }
    }
}

/* Pins the public handle layout: slot in the high half, generation in the low
   half. */
static void test_handle_layout(void) {
    assert(willrt_rc_handle_make(3, 5)
           == ((UINT64_C(3) << 32) | UINT64_C(5)));
    assert(willrt_rc_handle_slot(UINT64_C(0x0000000700000009)) == 7);
    assert(willrt_rc_handle_generation(UINT64_C(0x0000000700000009)) == 9);
    assert(willrt_rc_handle_make(0, 0) == 0);
}

static void test_new_deref(void) {
    int value = 42;
    uint64_t handle = willrt_rc_new(&value, sizeof(value));
    int* object;

    assert(handle != 0);
    assert(willrt_rc_handle_generation(handle) != 0);
    object = (int*)willrt_rc_deref(handle);
    assert(object != NULL);
    assert(*object == 42);

    *object = 43;
    assert(*(int*)willrt_rc_deref(handle) == 43);
    willrt_rc_release(handle);
    assert(willrt_rc_deref(handle) == NULL);
}

static void test_retain_release(void) {
    int value = 7;
    uint64_t handle = willrt_rc_new(&value, sizeof(value));

    assert(handle != 0);
    willrt_rc_retain(handle);
    willrt_rc_retain(handle);
    willrt_rc_release(handle);
    willrt_rc_release(handle);
    assert(willrt_rc_deref(handle) != NULL);

    willrt_rc_release(handle);
    assert(willrt_rc_deref(handle) == NULL);
}

static void test_empty_and_foreign_handles(void) {
    willrt_rc_retain(0);
    willrt_rc_release(0);
    assert(willrt_rc_deref(0) == NULL);
    willrt_rc_retain(UINT64_MAX);
    willrt_rc_release(UINT64_MAX);
    assert(willrt_rc_deref(UINT64_MAX) == NULL);
    assert(willrt_weak_new(0) == 0);
}

static void test_zero_size(void) {
    uint64_t handle = willrt_rc_new(NULL, 0);

    assert(handle != 0);
    assert(willrt_rc_deref(handle) != NULL);
    willrt_rc_release(handle);
    assert(willrt_rc_new(NULL, 1) == 0);
}

static void test_slot_reuse(void) {
    int first = 1;
    int second = 2;
    uint64_t old_handle = willrt_rc_new(&first, sizeof(first));
    uint64_t new_handle;

    assert(old_handle != 0);
    willrt_rc_release(old_handle);
    assert(willrt_rc_deref(old_handle) == NULL);

    new_handle = willrt_rc_new(&second, sizeof(second));
    assert(new_handle != 0);
    assert(willrt_rc_handle_slot(new_handle)
           == willrt_rc_handle_slot(old_handle));
    assert(willrt_rc_handle_generation(new_handle)
           != willrt_rc_handle_generation(old_handle));

    assert(willrt_rc_deref(old_handle) == NULL);
    assert(*(int*)willrt_rc_deref(new_handle) == 2);
    willrt_rc_retain(old_handle);
    willrt_rc_release(old_handle);
    assert(*(int*)willrt_rc_deref(new_handle) == 2);

    willrt_rc_release(new_handle);
    assert(willrt_rc_deref(new_handle) == NULL);
}

/* A generation that would wrap to zero retires the slot for the rest of the
   process. The seam sets the live slot to the last generation, then the test
   releases it through a handle that carries that generation. */
static void test_generation_wrap_retires_slot(void) {
    int value = 11;
    uint64_t handle = willrt_rc_new(&value, sizeof(value));
    uint64_t weak;
    uint64_t last;
    uint64_t upgraded = 0;
    uint32_t slot;
    size_t capacity;
    size_t count;
    uint64_t* handles;
    size_t i;

    assert(handle != 0);
    slot = willrt_rc_test_slot_of(handle);
    weak = willrt_weak_new(handle);
    assert(weak == handle);

    willrt_rc_test_set_generation(slot, UINT32_MAX);
    assert(willrt_rc_deref(handle) == NULL);

    last = willrt_rc_handle_make(slot, UINT32_MAX);
    assert(willrt_rc_deref(last) != NULL);
    willrt_rc_release(last);
    assert(willrt_rc_deref(last) == NULL);
    assert(willrt_rc_deref(handle) == NULL);
    assert(willrt_weak_upgrade(weak, &upgraded) == WILLRT_EMPTY);

    /* One slot is now retired, so this many new objects must exhaust the free
       list and force a growth. A slot wrongly returned to the free list would
       be handed out first because it would sit at the head. */
    capacity = willrt_rc_slot_capacity();
    count = capacity + 64;
    handles = (uint64_t*)willrt_alloc(count * sizeof(uint64_t));
    assert(handles != NULL);
    for (i = 0; i < count; i++) {
        handles[i] = willrt_rc_new(&value, sizeof(value));
        assert(handles[i] != 0);
        assert(willrt_rc_handle_slot(handles[i]) != slot);
    }
    assert(willrt_rc_slot_capacity() > capacity);

    for (i = 0; i < count; i++) {
        willrt_rc_release(handles[i]);
    }
    willrt_free(handles);
}

static void test_weak(void) {
    int value = 9;
    int replacement = 10;
    uint64_t handle = willrt_rc_new(&value, sizeof(value));
    uint64_t weak;
    uint64_t upgraded = 0;
    uint64_t reused;

    assert(handle != 0);
    weak = willrt_weak_new(handle);
    assert(weak == handle);

    assert(willrt_weak_upgrade(weak, &upgraded) == WILLRT_OK);
    assert(upgraded == handle);
    assert(*(int*)willrt_rc_deref(upgraded) == 9);
    willrt_rc_release(upgraded);

    willrt_rc_release(handle);
    assert(willrt_rc_deref(handle) == NULL);
    assert(willrt_weak_upgrade(weak, &upgraded) == WILLRT_EMPTY);
    assert(willrt_weak_new(handle) == 0);

    reused = willrt_rc_new(&replacement, sizeof(replacement));
    assert(reused != 0);
    assert(willrt_weak_upgrade(weak, &upgraded) == WILLRT_EMPTY);
    assert(willrt_weak_new(weak) == 0);
    willrt_rc_release(reused);

    assert(willrt_weak_upgrade(weak, NULL) == WILLRT_EMPTY);
}

static void test_concurrent_retain_release(void) {
    struct willrt_test_work work[WILLRT_TEST_THREADS];
    int value = 5;
    uint64_t handle = willrt_rc_new(&value, sizeof(value));
    int i;

    assert(handle != 0);
    for (i = 0; i < WILLRT_TEST_THREADS; i++) {
        work[i].handle = handle;
        work[i].iterations = WILLRT_TEST_ITERATIONS;
    }
    willrt_test_run_threads(work, WILLRT_TEST_THREADS);

    assert(*(int*)willrt_rc_deref(handle) == 5);
    willrt_rc_release(handle);
    assert(willrt_rc_deref(handle) == NULL);
}

int main(void) {
    test_growth_preserves_slots();
    test_handle_layout();
    test_new_deref();
    test_retain_release();
    test_empty_and_foreign_handles();
    test_zero_size();
    test_slot_reuse();
    test_generation_wrap_retires_slot();
    test_weak();
    test_concurrent_retain_release();
    return 0;
}
