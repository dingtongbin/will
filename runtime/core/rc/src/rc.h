/* Internal interface of the rc slot table. The public entry points are in
   willrt.h; this header exposes the handle layout and the table capacity the
   contract test observes. */

#ifndef WILLRT_RC_RC_H
#define WILLRT_RC_RC_H

#include <stddef.h>
#include <stdint.h>

#include "../../../include/willrt.h"

/* A handle packs the slot index in the low 32 bits and the slot generation in
   the high 32 bits. Zero is never a valid handle because a slot starts at
   generation 1 and a released slot advances the generation before it can be
   reused. */
static inline uint64_t willrt_rc_handle_make(uint32_t slot, uint32_t generation) {
    return ((uint64_t)generation << 32) | (uint64_t)slot;
}

/* willrt_rc_handle_slot returns the low 32 bits of handle. */
static inline uint32_t willrt_rc_handle_slot(uint64_t handle) {
    return (uint32_t)(handle & UINT64_C(0xFFFFFFFF));
}

/* willrt_rc_handle_generation returns the high 32 bits of handle. */
static inline uint32_t willrt_rc_handle_generation(uint64_t handle) {
    return (uint32_t)(handle >> 32);
}

/* willrt_rc_slot_capacity reports the slot count of the current table. The
   table doubles when the free list runs out. Old tables stay mapped, so this
   value never falls. */
size_t willrt_rc_slot_capacity(void);

#endif
