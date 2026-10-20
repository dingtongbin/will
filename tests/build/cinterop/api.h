#ifndef API_H
#define API_H

typedef unsigned long size_t;

int putchar(int c);
void* malloc(size_t n);
void free(void* p);
int add(int a, int b);
double scale(double x, int n);
int read_int(int* p);
int sum(int n, ...);

#endif
