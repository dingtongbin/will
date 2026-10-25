#ifndef WILL_TIME_ZONE_H
#define WILL_TIME_ZONE_H

#include <stdint.h>

/* One offset change in one zone. The offset is seconds east of UTC. */
typedef struct tz_transition {
    int64_t at;
    int32_t offset;
} tz_transition;

/* One named zone. Transitions are the half-open slice [first, first + count). */
typedef struct tz_zone {
    const char* name;
    int32_t initial;
    int32_t first;
    int32_t count;
} tz_zone;

extern const int32_t tz_zone_count;
extern const tz_zone* tz_zone_table;
extern const tz_transition* tz_transition_table;

/* willtime_zone_find returns the table index of an IANA name, or -1. */
int32_t willtime_zone_find(const void* name_raw, int64_t len);

/* willtime_zone_offset returns the offset of table zone id at Unix second at. */
int32_t willtime_zone_offset(int32_t id, int64_t at);

/* willtime_local_zone returns a table index, -2 for the host's own rules when
   the zone has no IANA name, or -1 when the host exposes no local zone. */
int32_t willtime_local_zone(void);

/* willtime_local_offset returns the host offset at at, or INT32_MIN when the
   host lookup fails. */
int32_t willtime_local_offset(int64_t at);

/* willtime_wall_ns returns the wall clock as Unix nanoseconds. */
int64_t willtime_wall_ns(void);

#endif
