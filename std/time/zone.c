/* Zone lookup and host clock front end for std/time.
 *
 * The IANA table itself is generated into tzdata.c; this file only searches it
 * and asks the host for its local rules. Local() is the single exception to
 * "no runtime time zone files": the host's own rules are read here, and no
 * TZif file is parsed by this project.
 */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#define _DEFAULT_SOURCE 1
#endif

#include "zone.h"

#include <limits.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <unistd.h>
#endif

/* The first word of a will str is its data pointer; the second is its length. */
typedef struct tz_str {
    const unsigned char* data;
    int64_t len;
} tz_str;

/* find_name searches the sorted table, comparing the exact byte length so a
   name with a trailing NUL cannot match a shorter table entry. */
static int32_t find_name(const char* data, int64_t len) {
    int32_t lo = 0;
    int32_t hi = tz_zone_count;

    if (data == NULL || len < 0) {
        return -1;
    }
    while (lo < hi) {
        int32_t mid = lo + (hi - lo) / 2;
        const char* name = tz_zone_table[mid].name;
        int64_t nameLen = (int64_t)strlen(name);
        int64_t common = len < nameLen ? len : nameLen;
        int cmp = common > 0 ? memcmp(data, name, (size_t)common) : 0;

        if (cmp == 0) {
            cmp = len < nameLen ? -1 : (len > nameLen ? 1 : 0);
        }
        if (cmp == 0) {
            return mid;
        }
        if (cmp < 0) {
            hi = mid;
        } else {
            lo = mid + 1;
        }
    }
    return -1;
}

int32_t willtime_zone_find(const void* name_raw, int64_t len) {
    const tz_str* name = (const tz_str*)name_raw;

    if (name == NULL) {
        return -1;
    }
    return find_name((const char*)name->data, len);
}

int32_t willtime_zone_offset(int32_t id, int64_t at) {
    const tz_zone* zone;
    int32_t lo;
    int32_t hi;

    if (id < 0 || id >= tz_zone_count) {
        return 0;
    }
    zone = &tz_zone_table[id];
    if (zone->count <= 0) {
        return zone->initial;
    }
    lo = zone->first;
    hi = zone->first + zone->count;
    while (lo < hi) {
        int32_t mid = lo + (hi - lo) / 2;
        if (tz_transition_table[mid].at <= at) {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }
    if (lo == zone->first) {
        return zone->initial;
    }
    return tz_transition_table[lo - 1].offset;
}

#if defined(_WIN32)

int64_t willtime_wall_ns(void) {
    FILETIME fileTime;
    ULARGE_INTEGER ticks;
    /* 100ns intervals between 1601-01-01 and 1970-01-01. */
    const ULONGLONG epoch = 116444736000000000ULL;

    GetSystemTimeAsFileTime(&fileTime);
    ticks.LowPart = fileTime.dwLowDateTime;
    ticks.HighPart = fileTime.dwHighDateTime;
    return ((int64_t)(ticks.QuadPart - epoch)) * 100;
}

int32_t willtime_local_offset(int64_t at) {
    __time64_t when = (__time64_t)at;
    struct tm local;

    if (_localtime64_s(&local, &when) != 0) {
        return INT32_MIN;
    }
    {
        __time64_t asUtc = _mkgmtime64(&local);
        if (asUtc == (__time64_t)-1) {
            return INT32_MIN;
        }
        return (int32_t)(asUtc - (int64_t)when);
    }
}

static int32_t local_zone_once(void) {
    const char* tz = getenv("TZ");
    int32_t id;

    if (tz != NULL && tz[0] != '\0') {
        if (tz[0] == ':') {
            tz++;
        }
        id = find_name(tz, (int64_t)strlen(tz));
        if (id >= 0) {
            return id;
        }
        return -2;
    }
    if (willtime_local_offset(willtime_wall_ns() / 1000000000LL) == INT32_MIN) {
        return -1;
    }
    return -2;
}

#else

int64_t willtime_wall_ns(void) {
    struct timespec now;

#if defined(CLOCK_REALTIME)
    if (clock_gettime(CLOCK_REALTIME, &now) == 0) {
        return (int64_t)now.tv_sec * 1000000000LL + (int64_t)now.tv_nsec;
    }
#endif
    if (timespec_get(&now, TIME_UTC) == TIME_UTC) {
        return (int64_t)now.tv_sec * 1000000000LL + (int64_t)now.tv_nsec;
    }
    return 0;
}

int32_t willtime_local_offset(int64_t at) {
    time_t when = (time_t)at;
    struct tm local;

    if (localtime_r(&when, &local) == NULL) {
        return INT32_MIN;
    }
#if defined(__linux__) || defined(__APPLE__) || defined(__ANDROID__)
    return (int32_t)local.tm_gmtoff;
#else
    {
        time_t asUtc = timegm(&local);
        if (asUtc == (time_t)-1) {
            return INT32_MIN;
        }
        return (int32_t)((int64_t)asUtc - at);
    }
#endif
}

/* trim_end removes trailing CR, LF, space, and tab from a fixed buffer. */
static void trim_end(char* text, size_t* len) {
    while (*len > 0) {
        char c = text[*len - 1];
        if (c != '\r' && c != '\n' && c != ' ' && c != '\t') {
            break;
        }
        *len -= 1;
    }
}

/* zone_from_file reads /etc/timezone, whose whole content is an IANA name. */
static int32_t zone_from_file(void) {
    FILE* file = fopen("/etc/timezone", "rb");
    char text[256];
    size_t len;

    if (file == NULL) {
        return -1;
    }
    len = fread(text, 1, sizeof(text), file);
    fclose(file);
    trim_end(text, &len);
    if (len == 0) {
        return -1;
    }
    return find_name(text, (int64_t)len);
}

/* zone_from_link reads the /etc/localtime symlink and keeps the component
   after the last "zoneinfo/" segment. */
static int32_t zone_from_link(void) {
    char target[1024];
    ssize_t len = readlink("/etc/localtime", target, sizeof(target) - 1);
    char* found;
    char* cursor;

    if (len <= 0) {
        return -1;
    }
    target[len] = '\0';
    found = NULL;
    cursor = target;
    while ((cursor = strstr(cursor, "zoneinfo/")) != NULL) {
        found = cursor + 9;
        cursor = found;
    }
    if (found == NULL || found[0] == '\0') {
        return -1;
    }
    return find_name(found, (int64_t)strlen(found));
}

static int32_t local_zone_once(void) {
    const char* tz = getenv("TZ");
    int32_t id;

    if (tz != NULL && tz[0] != '\0') {
        if (tz[0] == ':') {
            tz++;
        }
        id = find_name(tz, (int64_t)strlen(tz));
        if (id >= 0) {
            return id;
        }
        return -2;
    }
    id = zone_from_file();
    if (id >= 0) {
        return id;
    }
    id = zone_from_link();
    if (id >= 0) {
        return id;
    }
    if (willtime_local_offset(willtime_wall_ns() / 1000000000LL) == INT32_MIN) {
        return -1;
    }
    return -2;
}

#endif

int32_t willtime_local_zone(void) {
    static atomic_int cached = ATOMIC_VAR_INIT(INT32_MIN);
    int32_t value = atomic_load(&cached);

    if (value == INT32_MIN) {
        int32_t expected = INT32_MIN;
        int32_t found = local_zone_once();
        atomic_compare_exchange_strong(&cached, &expected, found);
        value = expected == INT32_MIN ? found : expected;
    }
    return value;
}
