// main.c is the program part of the compile corpus. Together with the
// library it is the program the compile scenarios build.
#include "lib.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

// LEVELS is the number of levels a record can have.
#define LEVELS 4

// count is the number of records the corpus makes.
#define RECORD_COUNT 2000

// Level is the level of one record.
typedef enum {
    LEVEL_DEBUG = 0,
    LEVEL_INFO = 1,
    LEVEL_WARN = 2,
    LEVEL_ERROR = 3
} Level;

// Record is one record of the log.
typedef struct {
    Level level;
    int64_t latency;
    int64_t bytes;
} Record;

// Reader reads records out of a buffer of numbers.
typedef struct {
    uint64_t state;
    int32_t count;
} Reader;

// next returns the next latency of the reader.
static int64_t next(Reader* reader) {
    reader->state = reader->state * 6364136223846793005ULL + 1442695040888963407ULL;
    int64_t value = (int64_t)(reader->state >> 12) % 1000;
    reader->count += 1;
    return value;
}

// make_records builds the records of the corpus.
static void make_records(Record* records, int32_t count) {
    Reader reader;
    reader.state = 0x1234;
    reader.count = 0;
    for (int32_t i = 0; i < count; i++) {
        int64_t latency = next(&reader);
        records[i].level = (Level)(i % LEVELS);
        records[i].latency = latency;
        records[i].bytes = latency * 64;
    }
}

// latencies returns the latencies of the records.
static void latencies(const Record* records, int32_t count, int64_t* out) {
    for (int32_t i = 0; i < count; i++) {
        out[i] = records[i].latency;
    }
}

// bytes_of returns the sizes of the records.
static void bytes_of(const Record* records, int32_t count, int64_t* out) {
    for (int32_t i = 0; i < count; i++) {
        out[i] = records[i].bytes;
    }
}

// Report holds the numbers the corpus prints.
typedef struct {
    int64_t records;
    int64_t positive;
    int64_t total;
    int64_t slowest;
    int64_t fastest;
    int64_t mean;
    int64_t buckets;
    int64_t rolling;
} Report;

// build computes the report of the records.
static Report build(const Record* records, int32_t count) {
    int64_t* values = (int64_t*)malloc(sizeof(int64_t) * (size_t)count);
    int64_t* sizes = (int64_t*)malloc(sizeof(int64_t) * (size_t)count);
    int64_t* buckets = (int64_t*)malloc(sizeof(int64_t) * (size_t)count);
    int64_t* rolling = (int64_t*)malloc(sizeof(int64_t) * (size_t)count);
    Report report;
    latencies(records, count, values);
    bytes_of(records, count, sizes);
    size_t used = lib_histogram(values, (size_t)count, 100, buckets, (size_t)count);
    size_t rolled = lib_rolling(values, (size_t)count, 16, rolling, (size_t)count);
    int64_t rollingTotal = 0;
    for (size_t i = 0; i < rolled; i++) {
        rollingTotal += rolling[i];
    }
    report.records = count;
    report.positive = lib_count(values, (size_t)count);
    report.total = lib_sum(sizes, (size_t)count);
    report.slowest = lib_max(values, (size_t)count);
    report.fastest = lib_min(values, (size_t)count);
    report.mean = lib_mean(values, (size_t)count);
    report.buckets = (int64_t)used;
    report.rolling = rollingTotal;
    free(values);
    free(sizes);
    free(buckets);
    free(rolling);
    return report;
}

// print_report writes the report.
static void print_report(const Report* report) {
    printf("records %lld\n", (long long)report->records);
    printf("positive %lld\n", (long long)report->positive);
    printf("total %lld\n", (long long)report->total);
    printf("slowest %lld\n", (long long)report->slowest);
    printf("fastest %lld\n", (long long)report->fastest);
    printf("mean %lld\n", (long long)report->mean);
    printf("buckets %lld\n", (long long)report->buckets);
    printf("rolling %lld\n", (long long)report->rolling);
}

int main(void) {
    Record* records = (Record*)malloc(sizeof(Record) * RECORD_COUNT);
    if (records == NULL) {
        return 1;
    }
    make_records(records, RECORD_COUNT);
    Report report = build(records, RECORD_COUNT);
    print_report(&report);
    free(records);
    return 0;
}
