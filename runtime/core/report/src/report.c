/* Report target, panic, and the symbol registry.
 *
 * The default report target is standard error, written directly with
 * WriteFile on Windows and write on POSIX. Nothing is buffered, allocated, or
 * locked on the report path: it may run on any thread and in a fault or signal
 * context. A custom sink inherits that rule; it must not allocate, block, take
 * a lock, or buffer, because a panic cannot flush a private buffer.
 *
 * willrt_report_set_sink is called once, after willrt_init and in the main
 * task. It only assigns two words and is not safe to race with writers.
 *
 * Symbol tables are module statics and constructors register them before
 * main. The registry grows only during that constructor phase; after main
 * starts it is read-only, so lookup takes no lock and allocates nothing.
 * Lookup is one binary search per table. Tables are few, so the linear walk
 * over them is cheaper than a merged index that registration would have to
 * rebuild.
 *
 * willrt_panic writes the report and then fails the current task when the task
 * engine is linked in; outside a task, it exits the process with 101.
 */

#include "report.h"

#if defined(_WIN32)
#include <windows.h>
#else
#include <errno.h>
#include <unistd.h>
#endif

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(__GNUC__) || defined(__clang__)
/* Defined by the task engine. It records the failure on the current task and
   transfers control out of the failed step, so it never returns on that path. */
extern int32_t willrt_task_fail(const char* message, size_t size,
                                const char* site, const char* symbol)
    __attribute__((weak));
#endif

enum {
    WILLRT_REPORT_INLINE_TABLES = 16
};

static void (*willrt_report_sink_fn)(const char*, size_t, void*);
static void* willrt_report_sink_ctx;

static const willrt_symbol_table* willrt_report_inline_tables
    [WILLRT_REPORT_INLINE_TABLES];
static const willrt_symbol_table** willrt_report_tables =
    willrt_report_inline_tables;
static size_t willrt_report_table_count;
static size_t willrt_report_table_capacity = WILLRT_REPORT_INLINE_TABLES;
static int willrt_report_tables_heap;

static const char willrt_report_unknown[] = "<unknown>";

static void willrt_output_write_handle(void* raw_handle, const char* text, size_t size) {
#if defined(_WIN32)
    HANDLE handle = (HANDLE)raw_handle;
    size_t offset = 0;

    if (handle == NULL || handle == INVALID_HANDLE_VALUE) {
        return;
    }
    while (offset < size) {
        size_t remaining = size - offset;
        DWORD chunk = remaining > (size_t)0xFFFFFFFFu
                          ? 0xFFFFFFFFu
                          : (DWORD)remaining;
        DWORD written = 0;

        if (!WriteFile(handle, text + offset, chunk, &written, NULL)
            || written == 0) {
            return;
        }
        offset += (size_t)written;
    }
#else
    int fd = (int)(intptr_t)raw_handle;
    size_t offset = 0;

    while (offset < size) {
        ssize_t written = write(fd, text + offset, size - offset);

        if (written < 0) {
            if (errno == EINTR) {
                continue;
            }
            return;
        }
        if (written == 0) {
            return;
        }
        offset += (size_t)written;
    }
#endif
}

void willrt_write_stdout(const char* data, size_t size) {
#if defined(_WIN32)
    willrt_output_write_handle((void*)GetStdHandle(STD_OUTPUT_HANDLE), data, size);
#else
    willrt_output_write_handle((void*)(intptr_t)STDOUT_FILENO, data, size);
#endif
}

static void willrt_print_text(const char* text, size_t size) {
    willrt_write_stdout(text, size);
}

void willrt_print_i64(int64_t value) {
    char buffer[32];
    int size = snprintf(buffer, sizeof(buffer), "%" PRId64, value);

    if (size > 0) {
        willrt_print_text(buffer, (size_t)size < sizeof(buffer) ? (size_t)size : sizeof(buffer));
    }
}

void willrt_print_u64(uint64_t value) {
    char buffer[32];
    int size = snprintf(buffer, sizeof(buffer), "%" PRIu64, value);

    if (size > 0) {
        willrt_print_text(buffer, (size_t)size < sizeof(buffer) ? (size_t)size : sizeof(buffer));
    }
}

void willrt_print_f64(double value) {
    char buffer[64];
    int size = snprintf(buffer, sizeof(buffer), "%.17g", value);

    if (size > 0) {
        willrt_print_text(buffer, (size_t)size < sizeof(buffer) ? (size_t)size : sizeof(buffer));
    }
}

void willrt_print_bool(int32_t value) {
    if (value != 0) {
        willrt_print_text("true", 4);
    } else {
        willrt_print_text("false", 5);
    }
}

void willrt_print_char(uint32_t value) {
    unsigned char bytes[4];
    size_t size;

    if (value <= 0x7f) {
        bytes[0] = (unsigned char)value;
        size = 1;
    } else if (value <= 0x7ff) {
        bytes[0] = (unsigned char)(0xc0 | (value >> 6));
        bytes[1] = (unsigned char)(0x80 | (value & 0x3f));
        size = 2;
    } else if (value <= 0xffff) {
        bytes[0] = (unsigned char)(0xe0 | (value >> 12));
        bytes[1] = (unsigned char)(0x80 | ((value >> 6) & 0x3f));
        bytes[2] = (unsigned char)(0x80 | (value & 0x3f));
        size = 3;
    } else if (value <= 0x10ffff) {
        bytes[0] = (unsigned char)(0xf0 | (value >> 18));
        bytes[1] = (unsigned char)(0x80 | ((value >> 12) & 0x3f));
        bytes[2] = (unsigned char)(0x80 | ((value >> 6) & 0x3f));
        bytes[3] = (unsigned char)(0x80 | (value & 0x3f));
        size = 4;
    } else {
        return;
    }
    willrt_print_text((const char*)bytes, size);
}

static size_t willrt_fmt_write(char* buffer, size_t capacity, const char* text, size_t size) {
    if (capacity != 0) {
        size_t copy = size < capacity ? size : capacity;

        if (copy != 0 && buffer != NULL) {
            memcpy(buffer, text, copy);
        }
    }
    return size;
}

size_t willrt_fmt_i64_len(int64_t value) {
    int size = snprintf(NULL, 0, "%" PRId64, value);
    return size > 0 ? (size_t)size : 0;
}

size_t willrt_fmt_u64_len(uint64_t value) {
    int size = snprintf(NULL, 0, "%" PRIu64, value);
    return size > 0 ? (size_t)size : 0;
}

size_t willrt_fmt_f64_len(double value) {
    int size = snprintf(NULL, 0, "%.17g", value);
    return size > 0 ? (size_t)size : 0;
}

size_t willrt_fmt_bool_len(int32_t value) {
    return value != 0 ? 4 : 5;
}

size_t willrt_fmt_char_len(uint32_t value) {
    if (value <= 0x7f) {
        return 1;
    }
    if (value <= 0x7ff) {
        return 2;
    }
    if (value <= 0xffff) {
        return 3;
    }
    return value <= 0x10ffff ? 4 : 0;
}

size_t willrt_fmt_i64_into(char* buffer, size_t capacity, int64_t value) {
    char text[32];
    int size = snprintf(text, sizeof(text), "%" PRId64, value);

    if (size <= 0) {
        return 0;
    }
    return willrt_fmt_write(buffer, capacity, text, (size_t)size);
}

size_t willrt_fmt_u64_into(char* buffer, size_t capacity, uint64_t value) {
    char text[32];
    int size = snprintf(text, sizeof(text), "%" PRIu64, value);

    if (size <= 0) {
        return 0;
    }
    return willrt_fmt_write(buffer, capacity, text, (size_t)size);
}

size_t willrt_fmt_f64_into(char* buffer, size_t capacity, double value) {
    char text[64];
    int size = snprintf(text, sizeof(text), "%.17g", value);

    if (size <= 0) {
        return 0;
    }
    return willrt_fmt_write(buffer, capacity, text, (size_t)size);
}

size_t willrt_fmt_bool_into(char* buffer, size_t capacity, int32_t value) {
    if (value != 0) {
        return willrt_fmt_write(buffer, capacity, "true", 4);
    }
    return willrt_fmt_write(buffer, capacity, "false", 5);
}

size_t willrt_fmt_char_into(char* buffer, size_t capacity, uint32_t value) {
    unsigned char bytes[4];
    size_t size;

    if (value <= 0x7f) {
        bytes[0] = (unsigned char)value;
        size = 1;
    } else if (value <= 0x7ff) {
        bytes[0] = (unsigned char)(0xc0 | (value >> 6));
        bytes[1] = (unsigned char)(0x80 | (value & 0x3f));
        size = 2;
    } else if (value <= 0xffff) {
        bytes[0] = (unsigned char)(0xe0 | (value >> 12));
        bytes[1] = (unsigned char)(0x80 | ((value >> 6) & 0x3f));
        bytes[2] = (unsigned char)(0x80 | (value & 0x3f));
        size = 3;
    } else if (value <= 0x10ffff) {
        bytes[0] = (unsigned char)(0xf0 | (value >> 18));
        bytes[1] = (unsigned char)(0x80 | ((value >> 12) & 0x3f));
        bytes[2] = (unsigned char)(0x80 | ((value >> 6) & 0x3f));
        bytes[3] = (unsigned char)(0x80 | (value & 0x3f));
        size = 4;
    } else {
        return 0;
    }
    return willrt_fmt_write(buffer, capacity, (const char*)bytes, size);
}

static void willrt_report_default_write(const char* text, size_t size) {
#if defined(_WIN32)
    HANDLE handle = GetStdHandle(STD_ERROR_HANDLE);
    size_t offset = 0;

    if (handle == NULL || handle == INVALID_HANDLE_VALUE) {
        return;
    }
    while (offset < size) {
        size_t remaining = size - offset;
        DWORD chunk = remaining > (size_t)0xFFFFFFFFu
                          ? 0xFFFFFFFFu
                          : (DWORD)remaining;
        DWORD written = 0;

        if (!WriteFile(handle, text + offset, chunk, &written, NULL)
            || written == 0) {
            return;
        }
        offset += (size_t)written;
    }
#else
    size_t offset = 0;

    while (offset < size) {
        ssize_t written = write(STDERR_FILENO, text + offset, size - offset);

        if (written < 0) {
            if (errno == EINTR) {
                continue;
            }
            return;
        }
        if (written == 0) {
            return;
        }
        offset += (size_t)written;
    }
#endif
}

void willrt_report_write_n(const char* text, size_t size) {
    if (text == NULL || size == 0) {
        return;
    }
    if (willrt_report_sink_fn != NULL) {
        willrt_report_sink_fn(text, size, willrt_report_sink_ctx);
        return;
    }
    willrt_report_default_write(text, size);
}

void willrt_report_write(const char* text) {
    if (text == NULL) {
        return;
    }
    willrt_report_write_n(text, strlen(text));
}

void willrt_report_set_sink(void (*fn)(const char*, size_t, void*), void* ctx) {
    willrt_report_sink_fn = fn;
    willrt_report_sink_ctx = fn != NULL ? ctx : NULL;
}

void willrt_panic_n(const char* message, size_t size, const char* site, const char* symbol) {
    if (message == NULL) {
        message = "";
        size = 0;
    }
    willrt_report_write("panic: ");
    willrt_report_write_n(message, size);
    willrt_report_write("\n");
    if (site != NULL && site[0] != '\0') {
        willrt_report_write("  at ");
        willrt_report_write(site);
        willrt_report_write(" ");
        willrt_report_write(symbol != NULL ? symbol : willrt_report_unknown);
        willrt_report_write("\n");
    }
    /* A panic inside a task stops at that task's boundary. The task engine is
       linked into a program that uses tasks; a program that does not is free of
       it, so the symbol is weak and the process exit remains the fallback. */
    if (willrt_task_fail != NULL) {
        willrt_task_fail(message, size, site, symbol);
    }
    _Exit(101);
}

void willrt_panic(const char* message, const char* site, const char* symbol) {
    willrt_panic_n(message, message != NULL ? strlen(message) : 0, site, symbol);
}

/* Returns the first entry whose address is the greatest one not above
   address, or NULL when that entry does not cover address. Duplicate
   addresses resolve to the first entry so a table has one deterministic
   answer. */
static const willrt_symbol* willrt_report_find(
    const willrt_symbol_table* table, uintptr_t address) {
    size_t low = 0;
    size_t high = table->count;
    size_t first;
    size_t last;
    const willrt_symbol* entry;

    while (low < high) {
        size_t middle = low + (high - low) / 2;

        if (table->entries[middle].address <= address) {
            low = middle + 1;
        } else {
            high = middle;
        }
    }
    if (low == 0) {
        return NULL;
    }
    entry = &table->entries[low - 1];
    if (address - entry->address >= entry->size) {
        return NULL;
    }
    first = 0;
    last = low - 1;
    while (first < last) {
        size_t middle = first + (last - first) / 2;

        if (table->entries[middle].address < entry->address) {
            first = middle + 1;
        } else {
            last = middle;
        }
    }
    return &table->entries[first];
}

void willrt_symbols_register(const willrt_symbol_table* table) {
    const willrt_symbol_table** grown;
    size_t grown_capacity;
    size_t index;

    if (table == NULL || table->entries == NULL || table->count == 0) {
        return;
    }
    if (willrt_report_table_count == willrt_report_table_capacity) {
        grown_capacity = willrt_report_table_capacity * 2;
        if (grown_capacity < willrt_report_table_capacity
            || grown_capacity
                   > SIZE_MAX / sizeof(*willrt_report_tables)) {
            return;
        }
        grown = (const willrt_symbol_table**)willrt_alloc(
            grown_capacity * sizeof(*willrt_report_tables));
        if (grown == NULL) {
            return;
        }
        for (index = 0; index < willrt_report_table_count; index++) {
            grown[index] = willrt_report_tables[index];
        }
        if (willrt_report_tables_heap) {
            willrt_free((void*)willrt_report_tables);
        }
        willrt_report_tables = grown;
        willrt_report_table_capacity = grown_capacity;
        willrt_report_tables_heap = 1;
    }
    willrt_report_tables[willrt_report_table_count++] = table;
}

const char* willrt_symbol_name(uintptr_t address) {
    const willrt_symbol* best = NULL;
    size_t index;

    for (index = 0; index < willrt_report_table_count; index++) {
        const willrt_symbol* candidate =
            willrt_report_find(willrt_report_tables[index], address);

        if (candidate != NULL
            && (best == NULL || candidate->address > best->address)) {
            best = candidate;
        }
    }
    if (best == NULL || best->name == NULL) {
        return willrt_report_unknown;
    }
    return best->name;
}

#ifdef WILLRT_REPORT_TEST
int willrt_report_test_has_sink(void) {
    return willrt_report_sink_fn != NULL;
}
#endif
