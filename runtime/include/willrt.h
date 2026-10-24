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

/* willrt_os_cpu_count returns the number of processors available to the
   process, or 1 when the platform cannot report a positive count. */
int32_t willrt_os_cpu_count(void);

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
/* willrt_os_thread_exit ends the calling thread without returning. It is a
   platform primitive; the task engine does not use it to contain a panic. */
void willrt_os_thread_exit(void);

/* willrt_alloc returns a block aligned for any type, or NULL on failure. A
   zero-size request still returns a unique block, so an empty buffer is never
   mistaken for an allocation failure. */
void* willrt_alloc(size_t size);

/* willrt_free releases a block from willrt_alloc. NULL is accepted. A pointer
   that willrt_alloc did not return is rejected with one line on standard
   error, and its memory is left alone. */
void willrt_free(void* ptr);

/* willrt_alloc_count reports how many allocations were counted. Counting is
   off unless WILLRT_COUNT_ALLOC=1 was set before the first allocation; the
   zero-allocation acceptance runs use it. */
int64_t willrt_alloc_count(void);
/* willrt_alloc_live reports how many counted blocks are outstanding. It moves
   only while counting is on, and a recycling path reaches a steady value. */
int64_t willrt_alloc_live(void);

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

   A poll request carries the descriptor array, the event masks, and the caller's
   user tags for one readiness scan. The task parks with the request; the poller
   waits for the whole batch and appends the ready tags into the caller's out
   array before waking the task. Windows accepts sockets only, because WSAPoll
   has no file-handle form; POSIX accepts any pollable descriptor.
   ------------------------------------------------------------------------- */

enum {
    WILLRT_POLL_IN = 1,
    WILLRT_POLL_OUT = 2,
    WILLRT_POLL_ERR = 4
};

/* willrt_poll_init starts the poller once; willrt_shutdown calls
   willrt_poll_shutdown after all tasks have joined. */
void willrt_poll_init(void);
void willrt_poll_shutdown(void);

/* willrt_poll_request is the task-embedded wait for one readiness scan. The
   task parks with the request linked into the poller's pending list; the
   poller copies the descriptor set into its own buffer, waits once for the
   whole batch, and appends ready tags straight into the waiting task's out
   array. state moves under the poller lock: PENDING is delivered as DONE by
   the poller or retired as DONE by this task when the deadline passed. gen
   guards a reused node against a snapshot taken before the reuse. The struct
   is complete here because the task owns one by value. */
enum {
    WILLRT_POLL_REQUEST_IDLE = 0,
    WILLRT_POLL_REQUEST_PENDING = 1,
    WILLRT_POLL_REQUEST_DONE = 2
};

typedef struct willrt_poll_request willrt_poll_request;

struct willrt_poll_request {
    int32_t state;
    int32_t linked;
    int32_t status;
    int64_t deadline;
    uint64_t gen;
    const void* fds;
    const void* events;
    const void* users;
    int64_t count;
    void* out;
    struct willrt_poll_request* next;
    void* waiter;
};

/* willrt_poll_wait performs one blocking readiness scan over parallel arrays of
   descriptors, event masks, and user tags, and appends the ready tags into out
   in input order. It is the synchronous spelling of a scan; the descriptor set
   belongs to the caller, so the cost is one pass over it per call. */
int32_t willrt_poll_wait(const void* fds, const void* events, const void* users,
                         int64_t count, int64_t timeout_ns, void* out);

/* willrt_poll_wait_async is the machine-declined fallback: one synchronous
   readiness scan that blocks the calling thread. Generated state machines use
   the start/poll/finish trio below instead. */
int32_t willrt_poll_wait_async(const void* fds, const void* events,
                               const void* users, int64_t count,
                               int64_t timeout_ns, void* out);
/* willrt_poll_wait_async_start registers one readiness scan on the current
   task and returns its embedded request. A caller outside a task runs the
   scan synchronously on a detached request; a zero timeout does the same. */
willrt_poll_request* willrt_poll_wait_async_start(const void* fds,
                                                  const void* events,
                                                  const void* users,
                                                  int64_t count,
                                                  int64_t timeout_ns,
                                                  void* out);
/* willrt_poll_wait_async_poll reports 1 when the scan is done and 0 while the
   task should keep waiting. It retires the request when the request deadline
   or the enclosing block deadline has passed; the block deadline reports
   WILLRT_TIMEDOUT through finish. */
int32_t willrt_poll_wait_async_poll(willrt_poll_request* req);
/* willrt_poll_wait_async_finish returns the scan status and resets the
   request to idle. The caller must have observed a finished poll. */
int32_t willrt_poll_wait_async_finish(willrt_poll_request* req);
/* willrt_poll_wait_async_deadline returns the absolute deadline the next park
   should arm, or zero for an unbounded wait. */
int64_t willrt_poll_wait_async_deadline(willrt_poll_request* req);
/* willrt_poll_request_abandon retires a still-pending request without
   delivering. A task that leaves its frame while parked must call it, so the
   poller stops carrying the descriptors. */
void willrt_poll_request_abandon(willrt_poll_request* req);

/* -------------------------------------------------------------------------
   Sync primitives. One primitive serves Mutex and RwLock: exclusive and
   shared acquisition share one FIFO waiter chain, so a later writer waits
   behind queued readers instead of barging. Semaphore counts permits, Count
   waits for a counter to reach zero, and Gate signals one generation flip.
   Each acquire waits as a state-machine operation whose node embeds in the
   task, so a contended wait allocates nothing. Cancellation and the scoped
   deadline unlink the waiter without taking the resource, which keeps the
   cancel-safety contract: permits, counts, and tokens never change on a
   canceled wait. Like a channel, the object lives as long as the value that
   owns it and is not freed.
   ------------------------------------------------------------------------- */

typedef struct willrt_sync_wait willrt_sync_wait;

struct willrt_sync_wait {
    int32_t active;
    int32_t linked;
    int32_t status;
    int32_t mode;
    void* owner;
    struct willrt_task* task;
    willrt_sync_wait* prev;
    willrt_sync_wait* next;
};

/* Mode of one acquisition: 0 takes the resource exclusively, 1 shares it. */
#define WILLRT_SYNC_EXCLUSIVE 0
#define WILLRT_SYNC_SHARED 1

void* willrt_sync_lock_new(void);
void* willrt_sync_sema_new(int64_t permits);
void* willrt_sync_count_new(int64_t count);
void* willrt_sync_gate_new(void);
/* willrt_sync_unlock releases one held acquisition. A null handle is a no-op,
   which is how an empty guard releases nothing. */
void willrt_sync_unlock(void* s, int32_t mode);
int64_t willrt_sync_sema_release(void* s);
/* willrt_sync_count_add moves the counter and wakes every waiter when it
   reaches zero. A later add that raises the count re-arms future waits. */
void willrt_sync_count_add(void* s, int64_t delta);
int64_t willrt_sync_count_load(void* s);
/* willrt_sync_gate_signal wakes every current and future waiter of this
   generation; willrt_sync_gate_reset arms the next one. */
void willrt_sync_gate_signal(void* s);
void willrt_sync_gate_reset(void* s);

/* willrt_sync_lock_async is the machine-declined fallback: it blocks the
   calling thread until the acquisition is granted or the caller's deadline
   passes. Generated state machines use the start form below. */
int32_t willrt_sync_lock_async(void* s, int32_t mode);
int32_t willrt_sync_sema_acquire_async(void* s);
int32_t willrt_sync_count_wait_async(void* s);
int32_t willrt_sync_gate_wait_async(void* s);
/* The start forms register the current task's embedded node and return it. A
   caller outside a task blocks in the start form and returns a granted or
   timed-out node. */
willrt_sync_wait* willrt_sync_lock_async_start(void* s, int32_t mode);
willrt_sync_wait* willrt_sync_sema_acquire_async_start(void* s);
willrt_sync_wait* willrt_sync_count_wait_async_start(void* s);
willrt_sync_wait* willrt_sync_gate_wait_async_start(void* s);
/* poll reports 1 when the wait resolved and 0 while the task should keep
   waiting. It observes cancellation and the scoped deadline on every re-run
   and unlinks the node before reporting either. */
int32_t willrt_sync_wait_poll(willrt_sync_wait* wait);
/* finish returns the resolved status and resets the node to idle. */
int32_t willrt_sync_wait_finish(willrt_sync_wait* wait);
/* deadline returns zero: these waits have no deadline of their own, the
   enclosing scope governs. */
int64_t willrt_sync_wait_deadline(willrt_sync_wait* wait);
/* willrt_sync_wait_abandon unlinks a still-pending node from its primitive. A
   task that leaves its frame while parked must call it, so a granted
   acquisition is never handed to a dead task. */
void willrt_sync_wait_abandon(willrt_sync_wait* wait);

/* willrt_blocking_fn is one job for the blocking pool. It may block; the pool
   owns the thread, and the caller waits for the job's result. out is the same
   pointer the caller passed to willrt_blocking_call. */
typedef int32_t (*willrt_blocking_fn)(void* arg, void* out);

/* willrt_blocking_call runs one function on a pool thread and waits for it.
   The pool starts lazily and has WILL_BLOCKING_MAX threads (default 128). */
int32_t willrt_blocking_call(void* fn, void* arg, void* out);
/* willrt_blocking_submit queues one job and returns a handle. Waiting on the
   handle parks the current task (or the main thread) until the worker finishes;
   the handle is released by willrt_blocking_wait. */
typedef struct willrt_blocking_op willrt_blocking_op;
willrt_blocking_op* willrt_blocking_submit(void* fn, void* arg, void* out);
/* willrt_blocking_submit_copy copies size bytes of arg into the handle, so a
   state machine can return to its caller while the worker still reads the
   arguments. The handle owns the copy. */
willrt_blocking_op* willrt_blocking_submit_copy(void* fn, const void* arg,
                                                size_t size, void* out);
/* willrt_blocking_poll reports 1 when the worker finished and 0 otherwise. It
   never waits. */
int32_t willrt_blocking_poll(willrt_blocking_op* op);
/* willrt_blocking_finish returns the worker status and releases the handle.
   The caller must have observed a finished poll. */
int32_t willrt_blocking_finish(willrt_blocking_op* op);
int32_t willrt_blocking_wait(willrt_blocking_op* op);
/* willrt_blocking_submitted counts the jobs the pool has accepted. It is the
   test and diagnostics view of who still leans on the pool. */
int64_t willrt_blocking_submitted(void);
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

/* willrt_task_error is the copy a joining task keeps in its own frame: the
   joined task's error with the status that join returned. The message bytes
   stay owned by the joined task; the region barrier that recycles the joined
   task joins the joiner first, so no copy is read after the barrier. */
typedef struct willrt_task_error {
    willrt_error error;
    int32_t status;
} willrt_task_error;

willrt_task* willrt_task_new(void* step, void* frame, size_t size,
                             int32_t lane, int32_t prio);
/* willrt_task_own_frame marks the frame as allocated on the heap, so the
   region barrier frees it with the task. A frame that lives anywhere else
   must not be marked. */
void willrt_task_own_frame(willrt_task* task);
/* willrt_job_new queues a task on the global job queue. The caller only uses
   it from a parallel block; the block body must not suspend. The returned
   handle is joined like a task handle. */
willrt_task* willrt_job_new(void* step, void* frame, size_t size,
                            int32_t prio);
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
/* willrt_runtime_threaded reports whether any task thread or job worker has
   started. A channel on a single-threaded program can take its uncontended
   path without a lock. */
int32_t willrt_runtime_threaded(void);
void willrt_yield(void);
int32_t willrt_task_canceled(void);
void willrt_task_bind_cancel(willrt_task* task, void* flag);
/* A step that is not ready marks the current task as waiting before returning
   WILLRT_SUSPEND. A bare suspend without this mark is retried immediately. */
void willrt_task_mark_wait(void);
/* A step that parked on an external operation sets the IO marker; checkdead
   counts it as pending work instead of reporting a deadlock. */
void willrt_task_mark_io(void);
int64_t willrt_task_deadline(void);
/* willrt_task_sleep_ns sleeps the calling thread. A generated step uses the
   async spelling and parks its frame instead. */
void willrt_task_sleep_ns(int64_t ns);
/* The async sleep handle records one deadline. poll never waits; finish frees
   the handle and returns zero for a valid sleep. */
typedef struct willrt_sleep_op willrt_sleep_op;
int32_t willrt_task_sleep_async(int64_t ns);
willrt_sleep_op* willrt_task_sleep_async_start(int64_t ns);
int32_t willrt_task_sleep_poll(willrt_sleep_op* op);
int32_t willrt_task_sleep_finish(willrt_sleep_op* op);
int64_t willrt_task_sleep_deadline(willrt_sleep_op* op);
/* willrt_task_wait_until names the deadline of the next park. The scheduler
   arms its timer and clears the field after the park. */
void willrt_task_wait_until(int64_t deadline);
/* willrt_sched_wake_io wakes the parked tasks that marked themselves as
   waiting on an external operation. It exists for a completer that holds no
   task handle; a dependency that knows its waiter wakes it directly. */
void willrt_sched_wake_io(void);
/* Wake every parked task. Cancellation uses this because a credential can be
   observed by a waiter parked on anything. */
void willrt_sched_wake_all(void);
/* willrt_task_enter_deadline starts one scoped deadline and returns the
   previous absolute deadline. willrt_task_exit_deadline restores it. A zero
   duration means the block has no deadline. */
int64_t willrt_task_enter_deadline(int64_t duration_ns);
void willrt_task_exit_deadline(int64_t previous);
void willrt_activity(int32_t delta);

/* -------------------------------------------------------------------------
   Channels. A value is copied into the ring on send and copied out on receive;
   the compiler has already enforced the move rule at the source. Capacity is
   the backpressure bound and must be greater than zero.
   ------------------------------------------------------------------------- */

typedef struct willrt_chan willrt_chan;
typedef struct willrt_select_state willrt_select_state;

/* One linked wait. A blocking caller owns a stack node; a task owns its node in
   the task object. A select points select at its state and keeps one node per
   arm. value points at storage that stays alive until the node completes. */
typedef struct willrt_chan_wait {
    struct willrt_chan_wait* next;
    willrt_os_cond cond;
    willrt_select_state* select;
    willrt_task* task;
    size_t arm;
    int32_t linked;
    int32_t ready;
    int32_t status;
    int32_t kind;
    void* out;
    const void* value;
} willrt_chan_wait;

willrt_chan* willrt_chan_new(size_t elem, size_t cap, int32_t prio);
int32_t willrt_chan_send(willrt_chan* chan, const void* value);
int32_t willrt_chan_try_send(willrt_chan* chan, const void* value);
int32_t willrt_chan_recv(willrt_chan* chan, void* out);
int32_t willrt_chan_try_recv(willrt_chan* chan, void* out);
/* willrt_chan_recv_wait registers the current task on recvq and returns
   WILLRT_SUSPEND; a matching send writes the task's out slot directly. On a
   resume it returns the completed status, or CANCELED/TIMEDOUT after it
   unlinks the node. */
int32_t willrt_chan_recv_wait(willrt_chan* chan, void* out);
/* willrt_chan_send_wait is the send-side counterpart. It completes directly
   into a waiting receiver or into the ring when there is room, and otherwise
   registers the current task on sendq. */
int32_t willrt_chan_send_wait(willrt_chan* chan, const void* value);
willrt_chan_wait* willrt_task_chan_wait_node(void);
/* willrt_task_poll_node returns the current task's embedded poll request, or
   NULL outside a task. The poller API stores the waiter in it. */
willrt_poll_request* willrt_task_poll_node(void);
/* willrt_task_sync_node returns the current task's embedded sync wait node,
   or NULL outside a task. The sync primitives park through it. */
willrt_sync_wait* willrt_task_sync_node(void);
void willrt_task_wake_chan(willrt_task* task);
/* willrt_task_wake_batch moves parked tasks to their lanes' ready queues,
   locking each lane once for the whole batch. The poller delivers one pass of
   ready tasks through it. */
void willrt_task_wake_batch(void* const* tasks, int64_t count);
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

/* A compiler-generated select step builds the arms in its frame, starts one
   context, and polls it after each wake. poll returns the arm index or
   WILLRT_SUSPEND; finish unlinks the remaining waits and releases the context. */
typedef struct willrt_select_ctx willrt_select_ctx;
willrt_select_ctx* willrt_select_start(willrt_arm* arms, size_t n);
int32_t willrt_select_poll(willrt_select_ctx* ctx);
void willrt_select_finish(willrt_select_ctx* ctx);

/* willrt_select_status reports the runtime status of the arm the last select on
   this thread chose. It is WILLRT_OK for a successful receive or send and
   WILLRT_CLOSED when a receive selected a closed channel. */
int32_t willrt_select_status(void);
/* The compiler's no-wait default path records the chosen arm status before it
   dispatches the body. */
void willrt_select_set_status(int32_t status);

/* willrt_timer is a one-shot timer node. The wheel owns heap nodes from
   willrt_timer_new; willrt_timer_arm_try reuses caller storage instead, so a
   task parks through a small ring of slots and never allocates. A slot
   returns to WILLRT_TIMER_IDLE once the wheel has fully released it. */
typedef struct willrt_timer {
    int64_t deadline;
    uint64_t seq;
    void* fn;
    void* arg;
    struct willrt_timer* prev;
    struct willrt_timer* next;
    struct willrt_timer* retire_next;
    _Atomic(uint32_t) state;
    int slot;
    int owned;
} willrt_timer;

enum {
    WILLRT_TIMER_IDLE = 0,
    WILLRT_TIMER_ARMED = 1,
    WILLRT_TIMER_DUE = 2,
    WILLRT_TIMER_FIRED = 3,
    WILLRT_TIMER_CANCELLED = 4
};

/* willrt_timer_new inserts one heap-owned timer for the absolute monotonic
   deadline deadline_ns. fn is a void (*)(void*) that runs with arg after the
   deadline passes. Insertion is O(1) and returns NULL when node allocation
   fails. */
willrt_timer* willrt_timer_new(int64_t deadline_ns, void* fn, void* arg);

/* willrt_timer_arm_try arms caller storage (zero-initialized willrt_timer
   state). It returns 0 when the storage is armed and -1 when the wheel still
   owns it from an earlier fire; the caller tries another storage. A task
   parks through a small ring of these and never allocates. */
int32_t willrt_timer_arm_try(willrt_timer* storage, int64_t deadline_ns,
                             void* fn, void* arg);

/* willrt_timer_cancel removes an armed timer in O(1). NULL and a timer that
   already fired are harmless. A cancelled or fired node stays valid until the
   next advance reclaims it; callers must not use the pointer after that. */
void willrt_timer_cancel(willrt_timer* timer);
/* willrt_timer_pending reports how many timers are armed right now. Deadlock
   detection reads it: a wait that a timer will end is not a deadlock. */
int32_t willrt_timer_pending(void);
/* willrt_timer_shutdown stops the timer run loop after all task deadlines have
   been observed. */
void willrt_timer_shutdown(void);

/* The compiler's entry wrapper calls willrt_init once, before the program
   entry, and willrt_shutdown once after it returns. Both are safe to call more
   than once, so a host embedding the runtime may start and stop it repeatedly. */
void willrt_init(void);
void willrt_shutdown(void);

/* willrt_panic writes "panic: <message>" and, when site is nonempty, one
   "  at <site> <symbol>" line. Inside a task it fails that task and returns to
   the lane's step boundary; outside a task it exits the process with 101.
   site and symbol are static strings supplied by the compiler. */
void willrt_panic(const char* message, const char* site, const char* symbol);

/* willrt_panic_n is willrt_panic for a message that is bytes and a length, so
   a will str does not have to be copied or terminated. */
void willrt_panic_n(const char* message, size_t size, const char* site, const char* symbol);

/* willrt_report_write_n writes bytes that need not be NUL-terminated. */
void willrt_report_write_n(const char* text, size_t size);

/* TaskError reads the failure text and status from the willrt_task_error copy
   the joining task keeps in its own frame. */
void willrt_task_error_message(void* copy, const char** data, size_t* size);
int32_t willrt_task_error_status(void* copy);


/* print writes its parts to standard output without buffering. A str part is
   bytes and a length; the scalar parts are formatted by the runtime. */
void willrt_write_stdout(const char* data, size_t size);
void willrt_print_i64(int64_t value);
void willrt_print_u64(uint64_t value);
void willrt_print_f64(double value);
void willrt_print_bool(int32_t value);
void willrt_print_char(uint32_t value);

/* willrt_str is the two-word will str: the bytes and their length. */
typedef struct willrt_str {
    const char* data;
    int64_t size;
} willrt_str;

/* The Show thunks format one predeclared value into an owning str. The
   compiler emits one small thunk per scalar type; these functions do the
   formatting and write the result through out. */
void willrt_show_i64(int64_t value, willrt_str* out);
void willrt_show_u64(uint64_t value, willrt_str* out);
void willrt_show_f64(double value, willrt_str* out);
void willrt_show_bool(int32_t value, willrt_str* out);
void willrt_show_str(const char* data, int64_t size, willrt_str* out);

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
