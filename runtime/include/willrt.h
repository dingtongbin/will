/* Public runtime interface. Every entry point a compiled program calls from
   the runtime carries the willrt_ prefix; this header is the only place that
   names them. */

#ifndef WILLRT_WILLRT_H
#define WILLRT_WILLRT_H

#include <stddef.h>
#include <stdint.h>

/* Status values for willrt_ functions that report int32_t. Negative values
   mean the operation did not complete, except WILLRT_SUSPEND, which means the
   caller is expected to try again after the task runs. */
enum {
    WILLRT_OK = 0,
    WILLRT_DONE = 1,
    WILLRT_SUSPEND = -1,
    WILLRT_EMPTY = -2,
    WILLRT_FULL = -3,
    WILLRT_CLOSED = -4,
    WILLRT_CANCELED = -5,
    WILLRT_TIMEDOUT = -6,
    WILLRT_FAILED = -7
};

/* willrt_os_alloc_pages returns at least size bytes of page-aligned,
   zero-filled memory, or NULL on failure. size is rounded up to the platform
   page size, so callers may ask for any byte count. */
void* willrt_os_alloc_pages(size_t size);

/* willrt_os_free_pages releases a block from willrt_os_alloc_pages. size is
   the value passed to that call. NULL is accepted. */
void willrt_os_free_pages(void* ptr, size_t size);

/* willrt_os_now_ns returns the platform's monotonic clock in nanoseconds. It
   never goes backwards. Zero is returned when the platform call fails. */
int64_t willrt_os_now_ns(void);

/* willrt_os_random_bytes fills out with size bytes from the platform's
   cryptographically secure source. It returns 0 on success and -1 when the
   source is unavailable; a partial fill is never reported as success. */
int32_t willrt_os_random_bytes(void* out, size_t size);

/* willrt_random_bytes is the core-facing form of the platform random source. */
int32_t willrt_random_bytes(void* out, size_t size);

/* Platform threads, locks, and waits. The storage is opaque to the scheduler;
   the platform implementation casts it to its own handle. A timeout below zero
   waits without a limit, and a timeout of zero does not wait. */
typedef struct willrt_os_thread {
    _Alignas(16) unsigned char bytes[128];
} willrt_os_thread;

typedef struct willrt_os_mutex {
    _Alignas(16) unsigned char bytes[128];
} willrt_os_mutex;

typedef struct willrt_os_cond {
    _Alignas(16) unsigned char bytes[128];
} willrt_os_cond;

/* willrt_os_thread_start runs fn(arg) on a new thread. It returns 0 on success
   and -1 when the platform refuses to start it. */
int32_t willrt_os_thread_start(willrt_os_thread* thread, void (*fn)(void*),
                               void* arg);
void willrt_os_thread_join(willrt_os_thread* thread);
void willrt_os_thread_yield(void);
void willrt_os_sleep_ns(int64_t ns);

void willrt_os_mutex_init(willrt_os_mutex* mutex);
void willrt_os_mutex_destroy(willrt_os_mutex* mutex);
void willrt_os_mutex_lock(willrt_os_mutex* mutex);
void willrt_os_mutex_unlock(willrt_os_mutex* mutex);

void willrt_os_cond_init(willrt_os_cond* cond);
void willrt_os_cond_destroy(willrt_os_cond* cond);
void willrt_os_cond_wait(willrt_os_cond* cond, willrt_os_mutex* mutex);
/* willrt_os_cond_timedwait returns 0 when it was signalled and 1 on timeout. */
int32_t willrt_os_cond_timedwait(willrt_os_cond* cond, willrt_os_mutex* mutex,
                                 int64_t timeout_ns);
void willrt_os_cond_signal(willrt_os_cond* cond);
void willrt_os_cond_broadcast(willrt_os_cond* cond);

/* Address waits park until *addr differs from expected or the timeout passes.
   The result is 0 when the value changed and 1 on timeout. A platform without
   a native primitive may poll; the comment in that platform file says so. */
int32_t willrt_os_wait_on_address(void* addr, uint32_t expected, int64_t timeout_ns);
void willrt_os_wake_address(void* addr, int32_t count);
/* willrt_os_thread_exit ends the calling thread without returning. The task
   engine uses it to contain a panic at the task boundary. */
void willrt_os_thread_exit(void);

/* willrt_alloc returns a block aligned for any type, or NULL on failure. A
   zero-size request still returns a unique block, so an empty buffer is never
   mistaken for an allocation failure. */
void* willrt_alloc(size_t size);

/* willrt_free releases a block from willrt_alloc. NULL is accepted. A pointer
   that willrt_alloc did not return is rejected with one line on standard
   error, and its memory is left alone. */
void willrt_free(void* ptr);

/* willrt_mem_set_shared tells the allocator that a second thread may now
   allocate or free. Until this is called the lane locks are skipped, which is
   the fast path for a single-threaded program. A runtime that starts a worker
   calls it before that worker can touch memory. */
void willrt_mem_set_shared(void);

/* willrt_mem_set_lane tells the allocator which lane the calling thread runs
   on. A thread that never calls it uses lane 0. The value is clamped into the
   allocator's lane table. */
void willrt_mem_set_lane(int32_t lane);

/* willrt_mem_thread_flush returns the calling thread's cached small blocks to
   its lane. A runtime worker calls it before exiting; a raw C thread that uses
   the allocator should do the same so another thread can reuse the blocks. */
void willrt_mem_thread_flush(void);

/* willrt_plate_new returns one block holding count elements of elem bytes, or
   NULL when the request overflows or memory is exhausted. The payload starts
   on a boundary of max(elem, alignof(max_align_t)), rounded up to a power of
   two so the arena can hold the address without lowering the alignment. */
void* willrt_plate_new(size_t count, size_t elem);

/* willrt_plate_release returns a whole plate from willrt_plate_new. NULL is
   accepted. A pointer outside the arena, or a header that does not carry the
   plate magic, is rejected with one line on standard error, and its memory is
   left alone. */
void willrt_plate_release(void* plate);

/* willrt_take_c adopts an object owned by C. ptr is returned unchanged by
   willrt_rc_deref; the handle carries a foreign marker instead of a slot.
   retain_code and release_code use the closure ABI: (environment, ptr).
   retain_code may be NULL for the TakeC form; release_code is required. */
uint64_t willrt_take_c(void* ptr, void* retain_code, void* retain_env,
                       void* release_code, void* release_env);

/* willrt_rc_new copies size bytes from value into a new rc object and returns
   its handle, or 0 when the allocation fails. A handle carries the slot index
   in the high 32 bits and the slot generation in the low 32 bits, and 0 is
   never valid. value must point to size bytes when size is nonzero. */
uint64_t willrt_rc_new(const void* value, size_t size);

/* willrt_rc_new_drop is willrt_rc_new with drop glue: drop(object) runs on the
   last release, before the object storage is returned. */
uint64_t willrt_rc_new_drop(const void* value, size_t size,
                            void (*drop)(void*));

/* willrt_rc_set_shared switches reference counts from the single-thread fast
   path to atomic compare-and-swap. A worker must be marked before it can touch
   an rc handle. */
void willrt_rc_set_shared(void);

/* willrt_rc_set_threaded tells the table that a second thread exists. It does
   not make every count atomic; willrt_rc_share does that per object. */
void willrt_rc_set_threaded(void);

/* willrt_rc_share marks one object as reachable from more than one thread. The
   compiler calls it before a handle enters a task frame or a channel. Until
   then this object keeps the plain count path even when other workers exist. */
void willrt_rc_share(uint64_t handle);

/* willrt_rc_retain adds one reference to the object named by handle. A zero,
   out-of-range, or released handle is ignored, so a stale handle never
   faults. */
void willrt_rc_retain(uint64_t handle);

/* willrt_rc_release removes one reference from the object named by handle and
   releases its storage when the count reaches zero. A zero, out-of-range, or
   released handle is ignored. */
void willrt_rc_release(uint64_t handle);

/* willrt_rc_deref returns the object named by handle after checking the slot
   generation, or NULL when the handle is zero, out of range, or released. The
   lookup is O(1). The caller must own a reference for as long as the result is
   used. */
void* willrt_rc_deref(uint64_t handle);

/* willrt_weak_new returns a weak handle for a live rc handle, or 0 when the
   handle is not live. A weak handle does not change the strong count and does
   not keep the slot from being reused; it is encoded exactly like a strong
   handle, so it must not be passed to willrt_rc_retain or
   willrt_rc_release. */
uint64_t willrt_weak_new(uint64_t handle);

/* willrt_weak_upgrade writes the strong handle to *(uint64_t*)out and returns
   WILLRT_OK when the weak handle still names a live object. It returns
   WILLRT_EMPTY when the object was released or out is NULL. On success the
   strong count is incremented, so the caller owns the returned reference. */
int32_t willrt_weak_upgrade(uint64_t weak, void* out);

/* willrt_now_ns returns the monotonic clock in nanoseconds. It never goes
   backwards and is the time base for timer deadlines. */
int64_t willrt_now_ns(void);

/* -------------------------------------------------------------------------
   Poller and blocking pool.

   A poller registration names one descriptor, the events it waits for, and one
   callback. The poller calls the callback with the ready subset; the callback
   may add or delete registrations. Windows accepts sockets only, because
   WSAPoll has no file-handle form; POSIX accepts any pollable descriptor.
   ------------------------------------------------------------------------- */

enum {
    WILLRT_POLL_IN = 1,
    WILLRT_POLL_OUT = 2,
    WILLRT_POLL_ERR = 4
};

/* willrt_poll_fn is called on the poller thread. It must not block for long;
   a task wake or a short handoff belongs here, a request handler does not. */
typedef void (*willrt_poll_fn)(int64_t fd, uint32_t events, void* arg);

/* willrt_poll_init starts the poller once. willrt_poll_add calls it lazily;
   willrt_shutdown calls willrt_poll_shutdown after all tasks have joined. */
void willrt_poll_init(void);
void willrt_poll_shutdown(void);
int32_t willrt_poll_add(int64_t fd, uint32_t events, void* cb, void* arg);
int32_t willrt_poll_del(int64_t fd);

/* willrt_blocking_fn is one job for the blocking pool. It may block; the pool
   owns the thread, and the caller waits for the job's result. out is the same
   pointer the caller passed to willrt_blocking_call. */
typedef int32_t (*willrt_blocking_fn)(void* arg, void* out);

/* willrt_blocking_call runs one function on a pool thread and waits for it.
   The pool starts lazily and has WILL_BLOCKING_MAX threads (default 128). */
int32_t willrt_blocking_call(void* fn, void* arg, void* out);
void willrt_blocking_shutdown(void);

/* -------------------------------------------------------------------------
   Tasks, regions, and the task environment.

   A task owns one frame of size bytes. The step function advances the frame
   once: it returns WILLRT_DONE when the frame is finished or WILLRT_SUSPEND
   when it wants to run again later. This engine runs a step on its own thread
   and calls a suspended step again after a yield.
   ------------------------------------------------------------------------- */

typedef struct willrt_task willrt_task;

typedef int32_t (*willrt_step)(void* frame);

/* willrt_error is the failure a joined task reports. message is a byte string
   of size bytes (it need not be NUL-terminated); site and symbol are static
   strings from the compiler. */
typedef struct willrt_error {
    const char* message;
    size_t size;
    const char* site;
    const char* symbol;
} willrt_error;

willrt_task* willrt_task_new(void* step, void* frame, size_t size,
                             int32_t lane, int32_t prio);
void willrt_task_run(willrt_task* task);
int32_t willrt_join(willrt_task* task, void* out, willrt_error* err);
/* willrt_task_try_join is the step-facing form of a join: it returns
   WILLRT_SUSPEND instead of waiting when the dependency is not finished. */
int32_t willrt_task_try_join(willrt_task* task, void* out, willrt_error* err);

/* The current task stores its result once, before its step returns. A task
   whose value is void does not call this. */
willrt_task* willrt_task_current(void);
int32_t willrt_task_store_result(const void* value, size_t size);

void willrt_region_enter(void);
int32_t willrt_region_exit(void);
int32_t willrt_lane_here(void);
void willrt_yield(void);
int32_t willrt_task_canceled(void);
int64_t willrt_task_deadline(void);
void willrt_activity(int32_t delta);

/* -------------------------------------------------------------------------
   Channels. A value is copied into the ring on send and copied out on receive;
   the compiler has already enforced the move rule at the source. Capacity is
   the backpressure bound and must be greater than zero.
   ------------------------------------------------------------------------- */

typedef struct willrt_chan willrt_chan;

willrt_chan* willrt_chan_new(size_t elem, size_t cap, int32_t prio);
int32_t willrt_chan_send(willrt_chan* chan, const void* value);
int32_t willrt_chan_try_send(willrt_chan* chan, const void* value);
int32_t willrt_chan_recv(willrt_chan* chan, void* out);
int32_t willrt_chan_try_recv(willrt_chan* chan, void* out);
void willrt_chan_close(willrt_chan* chan);

/* One select arm. kind 0 receives into out, 1 sends value, 2 waits until ns,
   and 3 is the default arm. A timeout or default arm has a null channel. */
typedef struct willrt_arm {
    willrt_chan* chan;
    void* out;
    const void* value;
    int32_t kind;
    int64_t ns;
} willrt_arm;

int32_t willrt_select(willrt_arm* arms, size_t n);

/* willrt_select_status reports the runtime status of the arm the last select on
   this thread chose. It is WILLRT_OK for a successful receive or send and
   WILLRT_CLOSED when a receive selected a closed channel. */
int32_t willrt_select_status(void);

/* willrt_timer is a one-shot timer. The node is owned by the runtime. */
typedef struct willrt_timer willrt_timer;

/* willrt_timer_new inserts a timer for the absolute monotonic deadline
   deadline_ns. fn is a void (*)(void*) that runs with arg after the deadline
   passes. Insertion is O(1) and returns NULL when node allocation fails. */
willrt_timer* willrt_timer_new(int64_t deadline_ns, void* fn, void* arg);

/* willrt_timer_cancel removes an armed timer in O(1). NULL and a timer that
   already fired are harmless. A cancelled or fired node stays valid until the
   next advance reclaims it; callers must not use the pointer after that. */
void willrt_timer_cancel(willrt_timer* timer);

/* The compiler's entry wrapper calls willrt_init once, before the program
   entry, and willrt_shutdown once after it returns. Both are safe to call more
   than once, so a host embedding the runtime may start and stop it repeatedly. */
void willrt_init(void);
void willrt_shutdown(void);

/* willrt_panic writes "panic: <message>" and, when site is nonempty, one
   "  at <site> <symbol>" line, then terminates the process with exit code 101.
   Task containment is not implemented yet, so every panic ends the process.
   site and symbol are static strings supplied by the compiler. */
void willrt_panic(const char* message, const char* site, const char* symbol);

/* willrt_panic_n is willrt_panic for a message that is bytes and a length, so
   a will str does not have to be copied or terminated. Task containment is not
   implemented yet, so it also ends the process. */
void willrt_panic_n(const char* message, size_t size, const char* site, const char* symbol);

/* willrt_report_write_n writes bytes that need not be NUL-terminated. */
void willrt_report_write_n(const char* text, size_t size);

/* print writes its parts to standard output without buffering. A str part is
   bytes and a length; the scalar parts are formatted by the runtime. */
void willrt_write_stdout(const char* data, size_t size);
void willrt_print_i64(int64_t value);
void willrt_print_u64(uint64_t value);
void willrt_print_f64(double value);
void willrt_print_bool(int32_t value);
void willrt_print_char(uint32_t value);

/* The interpolation helpers measure a formatted scalar and then write it into
   a buffer the caller allocates, so one interpolation allocates once. */
size_t willrt_fmt_i64_len(int64_t value);
size_t willrt_fmt_u64_len(uint64_t value);
size_t willrt_fmt_f64_len(double value);
size_t willrt_fmt_bool_len(int32_t value);
size_t willrt_fmt_char_len(uint32_t value);
size_t willrt_fmt_i64_into(char* buffer, size_t capacity, int64_t value);
size_t willrt_fmt_u64_into(char* buffer, size_t capacity, uint64_t value);
size_t willrt_fmt_f64_into(char* buffer, size_t capacity, double value);
size_t willrt_fmt_bool_into(char* buffer, size_t capacity, int32_t value);
size_t willrt_fmt_char_into(char* buffer, size_t capacity, uint32_t value);

/* willrt_report_write sends the NUL-terminated text to the current report
   target. The text must not be NULL. The default target is standard error,
   written without buffering. */
void willrt_report_write(const char* text);

/* willrt_report_set_sink replaces the report target. fn is called
   synchronously with the text bytes and ctx. It must write or forward the
   bytes without allocating, blocking, taking a lock, or buffering, because
   reports may run on any thread or in a fault context and a panic cannot
   flush a private buffer. A NULL fn restores standard error and clears ctx.
   Call this once, after willrt_init and in the main task. */
void willrt_report_set_sink(void (*fn)(const char*, size_t, void*), void* ctx);

/* willrt_symbol describes one function in a module symbol table. address is
   the first byte of the function, size is its byte length, and name is the
   stable symbol name in static storage. */
typedef struct willrt_symbol {
    uintptr_t address;
    size_t size;
    const char* name;
} willrt_symbol;

/* willrt_symbol_table holds entries sorted by ascending address. The table and
   its entries are static storage and must not change after registration. */
typedef struct willrt_symbol_table {
    const willrt_symbol* entries;
    size_t count;
} willrt_symbol_table;

/* willrt_symbols_register adds one module table. Constructors call it once per
   module before the main task starts; registering while other threads may
   look up symbols is not supported. */
void willrt_symbols_register(const willrt_symbol_table* table);

/* willrt_symbol_name returns the name whose range contains addr. When ranges
   overlap it picks the greatest address, and for equal addresses it picks the
   earliest registered table. It returns the static string "<unknown>" when no
   range contains addr. Lookup allocates nothing and takes no lock. */
const char* willrt_symbol_name(uintptr_t addr);

/* willrt_utf8_decode reads one UTF-8 scalar from data[0..len). It returns the
   number of bytes the scalar occupies (1 to 4) and stores the scalar in *out.
   It returns 0 for an empty buffer, a truncated sequence, an overlong
   encoding, a surrogate, or a value above U+10FFFF. */
int32_t willrt_utf8_decode(const uint8_t* data, size_t len, uint32_t* out);

/* willrt_utf8_valid reports whether every byte in the range starts a complete,
   shortest-form scalar. An empty range is valid. */
int32_t willrt_utf8_valid(const uint8_t* data, size_t len);

/* willrt_utf8_encode writes one scalar in shortest form and returns its byte
   length, or 0 for a surrogate or a value above U+10FFFF. */
int32_t willrt_utf8_encode(uint32_t value, uint8_t* out);

/* willrt_bytes_index returns the first offset where needle occurs in hay, or
   -1 when it does not. An empty needle is found at offset zero. */
int64_t willrt_bytes_index(const uint8_t* hay, size_t hay_len,
                           const uint8_t* needle, size_t needle_len);
int64_t willrt_bytes_count(const uint8_t* hay, size_t hay_len,
                           const uint8_t* needle, size_t needle_len);
int64_t willrt_bytes_trim(const uint8_t* data, size_t len, int32_t left,
                          int32_t right, int64_t* start, int64_t* out_len);
void willrt_bytes_repeat(uint8_t* dst, const uint8_t* src, size_t len,
                         size_t count);
int64_t willrt_bytes_replace_into(uint8_t* dst, size_t cap, const uint8_t* data,
                                  size_t len, const uint8_t* old,
                                  size_t old_len, const uint8_t* new_text,
                                  size_t new_len);
/* willrt_bytes_split writes each part as a pointer word and a length word and
   returns the part count; a null out only counts. */
int64_t willrt_bytes_split(const uint8_t* data, size_t len, const uint8_t* sep,
                           size_t sep_len, uint64_t* out, size_t cap);
int64_t willrt_bytes_fields(const uint8_t* data, size_t len, uint64_t* out,
                            size_t cap);

#endif
