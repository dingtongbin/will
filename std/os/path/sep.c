/* The host path separator. Join and Clean accept both slash spellings and
 * write this one, so output follows the platform's native form. */
#include "sep.h"

uint8_t willpath_separator(void) {
#if defined(_WIN32)
    return (uint8_t)'\\';
#else
    return (uint8_t)'/';
#endif
}
