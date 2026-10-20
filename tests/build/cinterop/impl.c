int add(int a, int b) {
    return a + b;
}

double scale(double x, int n) {
    return x * n;
}

int read_int(int* p) {
    return *p;
}

#include <stdarg.h>

int sum(int n, ...) {
    va_list args;
    int total = 0;
    int i;

    va_start(args, n);
    for (i = 0; i < n; i++) {
        total += va_arg(args, int);
    }
    va_end(args);
    return total;
}
