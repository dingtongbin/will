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
 * willrt_panic writes the report and exits with 101. Task containment belongs
 * to the task engine; until that exists, a panic always ends the process.
 */

#include "report.h"

#if defined(_WIN32)
#include <windows.h>
#else
#include <errno.h>
#include <unistd.h>
#endif

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

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

void willrt_report_write(const char* text) {
    size_t size;

    if (text == NULL) {
        return;
    }
    size = strlen(text);
    if (size == 0) {
        return;
    }
    if (willrt_report_sink_fn != NULL) {
        willrt_report_sink_fn(text, size, willrt_report_sink_ctx);
        return;
    }
    willrt_report_default_write(text, size);
}

void willrt_report_set_sink(void (*fn)(const char*, size_t, void*), void* ctx) {
    willrt_report_sink_fn = fn;
    willrt_report_sink_ctx = fn != NULL ? ctx : NULL;
}

void willrt_panic(const char* message, const char* site, const char* symbol) {
    if (message == NULL) {
        message = "";
    }
    willrt_report_write("panic: ");
    willrt_report_write(message);
    willrt_report_write("\n");
    if (site != NULL && site[0] != '\0') {
        willrt_report_write("  at ");
        willrt_report_write(site);
        willrt_report_write(" ");
        willrt_report_write(symbol != NULL ? symbol : willrt_report_unknown);
        willrt_report_write("\n");
    }
    _Exit(101);
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
