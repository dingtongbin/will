/* Internal interface of the rc slot table. The public entry points are in
   willrt.h; this header exposes the handle layout, the table capacity, and the
   test seam the contract test uses. */

#ifndef WILLRT_RC_RC_H
#define WILLRT_RC_RC_H

#include <stddef.h>
#include <stdint.h>

#include "../../../include/willrt.h"

/* A handle packs the slot index in the high 32 bits and the slot generation in
   the low 32 bits. Zero is never a valid handle because a slot starts at
   generation 1 and a released slot advances the generation before it can be
   reused. A slot whose generation would wrap to zero is retired instead, so
   generation zero never names a live object. */
static inline uint64_t willrt_rc_handle_make(uint32_t slot, uint32_t generation) {
    return ((uint64_t)slot << 32) | (uint64_t)generation;
}

/* willrt_rc_handle_slot returns the high 32 bits of handle. */
static inline uint32_t willrt_rc_handle_slot(uint64_t handle) {
    return (uint32_t)(handle >> 32);
}

/* willrt_rc_handle_generation returns the low 32 bits of handle. */
static inline uint32_t willrt_rc_handle_generation(uint64_t handle) {
    return (uint32_t)(handle & UINT64_C(0xFFFFFFFF));
}

/* willrt_rc_slot_capacity reports the slot count of the current table. The
   table doubles when the free list runs out. Old tables stay mapped, so this
   value never falls. */
size_t willrt_rc_slot_capacity(void);

#ifdef WILLRT_RC_TEST
/* willrt_rc_test_set_generation replaces the generation of one slot and keeps
   its strong count. The test seam exists so a contract test can reach the
   generation wrap without performing 2^32 releases. It must not be called
   while another thread operates on the slot. */
void willrt_rc_test_set_generation(uint32_t slot, uint32_t generation);

/* willrt_rc_test_slot_of returns the slot index encoded in handle. */
uint32_t willrt_rc_test_slot_of(uint64_t handle);
#endif

#endif
