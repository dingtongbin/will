// Fixed formatting needs the exact decimal expansion of a binary double, so it
// is delegated to the C library's printf conversion. The shim returns the
// magnitude text and reports whether the value was nan or inf; the will side
// keeps the sign apart so fill bytes can sit next to it.
#include <math.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

void* willrt_alloc(size_t size);

typedef struct fmt_str {
    unsigned char* data;
    int64_t len;
} fmt_str;

// willfmt_f64_fixed writes the magnitude of value in fixed notation with prec
// digits after the decimal point and returns 0 for a finite value, 1 for nan,
// 2 for an infinity, and 3 when no text could be produced. The returned text
// has no sign.
int32_t willfmt_f64_fixed(double value, int32_t prec, void* out_raw) {
    fmt_str* out = (fmt_str*)out_raw;
    const char* special = NULL;
    double magnitude = fabs(value);
    int32_t kind = 0;
    int size;

    if (isnan(value)) {
        special = "nan";
        kind = 1;
    } else if (isinf(value)) {
        special = "inf";
        kind = 2;
    }
    if (special != NULL) {
        size = 3;
    } else {
        // A negative precision is rejected in will; clamping here keeps the
        // C library contract valid if the shim is ever called directly.
        if (prec < 0) {
            prec = 0;
        }
        size = snprintf(NULL, 0, "%.*f", (int)prec, magnitude);
        if (size < 0) {
            out->data = NULL;
            out->len = 0;
            return 3;
        }
    }
    out->data = (unsigned char*)willrt_alloc((size_t)size + 1u);
    if (out->data == NULL) {
        out->len = 0;
        return 3;
    }
    if (special != NULL) {
        memcpy(out->data, special, 3u);
    } else {
        snprintf((char*)out->data, (size_t)size + 1u, "%.*f", (int)prec,
                 magnitude);
    }
    out->len = (int64_t)size;
    return kind;
}
