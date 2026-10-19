/* Contract tests for the report target, panic output, and symbol lookup. The
   process with no arguments exits zero when every assertion holds. Arguments
   select the panic child modes that the Go driver runs as a subprocess. */

#include "report.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

static char willrt_test_sink_bytes[64];
static size_t willrt_test_sink_length;
static size_t willrt_test_sink_calls;
static const void* willrt_test_sink_context;

static void willrt_test_sink(const char* text, size_t size, void* ctx) {
    assert(ctx == willrt_test_sink_context);
    assert(size <= sizeof(willrt_test_sink_bytes)
                       - willrt_test_sink_length);
    memcpy(willrt_test_sink_bytes + willrt_test_sink_length, text, size);
    willrt_test_sink_length += size;
    willrt_test_sink_calls++;
}

static void test_single_table(void) {
    static const willrt_symbol entries[] = {
        {0x1000, 0x100, "pkg.a"},
        {0x2000, 0x080, "pkg.b"},
        {0x3000, 0x040, "pkg.c"}
    };
    static const willrt_symbol_table table = {entries, 3};

    willrt_symbols_register(&table);
    assert(strcmp(willrt_symbol_name(0x1000), "pkg.a") == 0);
    assert(strcmp(willrt_symbol_name(0x1050), "pkg.a") == 0);
    assert(strcmp(willrt_symbol_name(0x10FF), "pkg.a") == 0);
    assert(strcmp(willrt_symbol_name(0x1100), "<unknown>") == 0);
    assert(strcmp(willrt_symbol_name(0x0FFF), "<unknown>") == 0);
    assert(strcmp(willrt_symbol_name(0x2080), "<unknown>") == 0);
    assert(strcmp(willrt_symbol_name(0x2500), "<unknown>") == 0);
    assert(strcmp(willrt_symbol_name(0x3020), "pkg.c") == 0);
    assert(willrt_symbol_name(0x4000) == willrt_symbol_name(0x5000));
}

static void test_same_address(void) {
    static const willrt_symbol first_entries[] = {
        {0x6000, 0x020, "pkg.first"}
    };
    static const willrt_symbol second_entries[] = {
        {0x6000, 0x020, "pkg.second"}
    };
    static const willrt_symbol_table first_table = {first_entries, 1};
    static const willrt_symbol_table second_table = {second_entries, 1};

    willrt_symbols_register(&first_table);
    willrt_symbols_register(&second_table);
    assert(strcmp(willrt_symbol_name(0x6000), "pkg.first") == 0);
    assert(strcmp(willrt_symbol_name(0x6010), "pkg.first") == 0);
}

static void test_overlap(void) {
    static const willrt_symbol outer_entries[] = {
        {0x7000, 0x100, "pkg.outer"}
    };
    static const willrt_symbol inner_entries[] = {
        {0x7050, 0x020, "pkg.inner"}
    };
    static const willrt_symbol_table outer_table = {outer_entries, 1};
    static const willrt_symbol_table inner_table = {inner_entries, 1};

    willrt_symbols_register(&outer_table);
    willrt_symbols_register(&inner_table);
    assert(strcmp(willrt_symbol_name(0x7000), "pkg.outer") == 0);
    assert(strcmp(willrt_symbol_name(0x7050), "pkg.inner") == 0);
    assert(strcmp(willrt_symbol_name(0x706F), "pkg.inner") == 0);
    assert(strcmp(willrt_symbol_name(0x7070), "pkg.outer") == 0);
    assert(strcmp(willrt_symbol_name(0x70FF), "pkg.outer") == 0);
    assert(strcmp(willrt_symbol_name(0x7100), "<unknown>") == 0);
}

static void test_empty_and_zero(void) {
    static const willrt_symbol zero_entries[] = {
        {0x8000, 0, "pkg.zero"}
    };
    static const willrt_symbol_table zero_table = {zero_entries, 1};
    static const willrt_symbol_table empty_table = {NULL, 0};

    willrt_symbols_register(&zero_table);
    willrt_symbols_register(&empty_table);
    assert(strcmp(willrt_symbol_name(0x8000), "<unknown>") == 0);
}

static void test_registry_growth(void) {
    static willrt_symbol entries[20];
    static willrt_symbol_table tables[20];
    size_t i;

    for (i = 0; i < 20; i++) {
        entries[i].address = 0x9000 + i * 0x10;
        entries[i].size = 0x10;
        entries[i].name = "pkg.grown";
        tables[i].entries = &entries[i];
        tables[i].count = 1;
        willrt_symbols_register(&tables[i]);
    }
    assert(strcmp(willrt_symbol_name(0x9000), "pkg.grown") == 0);
    assert(strcmp(willrt_symbol_name(0x9130), "pkg.grown") == 0);
    assert(strcmp(willrt_symbol_name(0x9140), "<unknown>") == 0);
    assert(strcmp(willrt_symbol_name(0x1050), "pkg.a") == 0);
}

static void test_report_sink(void) {
    int marker;

    willrt_test_sink_length = 0;
    willrt_test_sink_calls = 0;
    willrt_test_sink_context = &marker;
    willrt_report_set_sink(willrt_test_sink, &marker);
    assert(willrt_report_test_has_sink() == 1);
    willrt_report_write("alpha");
    willrt_report_write("beta");
    assert(willrt_test_sink_calls == 2);
    assert(willrt_test_sink_length == 9);
    assert(memcmp(willrt_test_sink_bytes, "alphabeta", 9) == 0);
    willrt_report_set_sink(NULL, &marker);
    assert(willrt_report_test_has_sink() == 0);
}

int main(int argc, char** argv) {
    if (argc > 1) {
        if (strcmp(argv[1], "panic-site") == 0) {
            willrt_panic("msg", "a.will:1:2", "pkg.f");
        } else if (strcmp(argv[1], "panic-nosite") == 0) {
            willrt_panic("msg", NULL, NULL);
        } else if (strcmp(argv[1], "panic-empty") == 0) {
            willrt_panic("", NULL, NULL);
        }
        return 2;
    }
    test_single_table();
    test_same_address();
    test_overlap();
    test_empty_and_zero();
    test_registry_growth();
    test_report_sink();
    return 0;
}
