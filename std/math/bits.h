#ifndef WILL_MATH_BITS_H
#define WILL_MATH_BITS_H

#include <stdint.h>

uint64_t willrt_f64_bits(double value);
double willrt_f64_from_bits(uint64_t bits);
uint32_t willrt_f32_bits(float value);
float willrt_f32_from_bits(uint32_t bits);

#endif
