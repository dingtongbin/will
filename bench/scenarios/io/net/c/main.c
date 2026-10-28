// main.c is the C baseline of the io/net scenario. It runs a TCP echo
// server on the loopback interface and measures the throughput of one
// connection that sends and receives a stream of bytes.
//
// The will side waits for the network package of the standard library, so
// only the C numbers exist today. The numbers are a reference: the method
// volume asks for the numbers of a library comparison, not for a ratio.
#include "bench.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#include <winsock2.h>
#include <ws2tcpip.h>
#define BENCH_SOCKET SOCKET
#define BENCH_CLOSE closesocket
#define BENCH_INVALID INVALID_SOCKET
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <pthread.h>
#include <sys/socket.h>
#include <unistd.h>
#define BENCH_SOCKET int
#define BENCH_CLOSE close
#define BENCH_INVALID (-1)
#endif

#if defined(_WIN32)
#include <windows.h>
#else
#include <pthread.h>
#endif

// payload is the number of bytes one connection moves.
static const size_t payload = 8 * 1024 * 1024;

// repetitions is the number of measured repetitions of the workload.
static const int32_t repetitions = 5;

static uint8_t* buffer = NULL;
static BENCH_SOCKET listener = BENCH_INVALID;

// echo_server accepts one connection and echoes what it receives.
#if defined(_WIN32)
static DWORD WINAPI echo_server(LPVOID arg) {
    (void)arg;
    BENCH_SOCKET client = accept(listener, NULL, NULL);
    if (client == BENCH_INVALID) {
        return 0;
    }
    size_t done = 0;
    while (done < payload) {
        int got = recv(client, (char*)buffer, (int)(payload - done), 0);
        if (got <= 0) {
            break;
        }
        done += (size_t)got;
        if (send(client, (const char*)buffer, got, 0) != got) {
            break;
        }
    }
    BENCH_CLOSE(client);
    return 0;
}
#else
static void* echo_server(void* arg) {
    (void)arg;
    int client = accept(listener, NULL, NULL);
    if (client < 0) {
        return NULL;
    }
    size_t done = 0;
    while (done < payload) {
        ssize_t got = recv(client, buffer, payload - done, 0);
        if (got <= 0) {
            break;
        }
        done += (size_t)got;
        if (send(client, buffer, got, 0) != got) {
            break;
        }
    }
    close(client);
    return NULL;
}
#endif

int main(void) {
    uint64_t* samples = (uint64_t*)malloc(sizeof(uint64_t) * (size_t)repetitions);
    buffer = (uint8_t*)malloc(payload);
    if (samples == NULL || buffer == NULL) {
        return 1;
    }
    bench_fill_bytes(buffer, payload, 0xC0FFEE);

#if defined(_WIN32)
    WSADATA wsa;
    if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
        return 1;
    }
#endif

    listener = socket(AF_INET, SOCK_STREAM, 0);
    if (listener == BENCH_INVALID) {
        return 1;
    }
    int reuse = 1;
    setsockopt(listener, SOL_SOCKET, SO_REUSEADDR, (const char*)&reuse, sizeof(reuse));
    struct sockaddr_in address;
    memset(&address, 0, sizeof(address));
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    address.sin_port = 0;
    if (bind(listener, (struct sockaddr*)&address, sizeof(address)) != 0) {
        return 1;
    }
    if (listen(listener, 4) != 0) {
        return 1;
    }
    socklen_t addressLength = (socklen_t)sizeof(address);
    if (getsockname(listener, (struct sockaddr*)&address, &addressLength) != 0) {
        return 1;
    }

    uint64_t checksum = 0;
    for (int32_t r = 0; r < repetitions; r++) {
#if defined(_WIN32)
        HANDLE server = CreateThread(NULL, 0, echo_server, NULL, 0, NULL);
#else
        pthread_t server;
        pthread_create(&server, NULL, echo_server, NULL);
#endif
        BENCH_SOCKET client = socket(AF_INET, SOCK_STREAM, 0);
        if (client == BENCH_INVALID) {
            return 1;
        }
        if (connect(client, (struct sockaddr*)&address, sizeof(address)) != 0) {
            return 1;
        }
        uint64_t start = bench_now_ns();
        size_t done = 0;
        while (done < payload) {
            int put = send(client, (const char*)buffer + done, (int)(payload - done), 0);
            if (put <= 0) {
                break;
            }
            int got = recv(client, (char*)buffer + done, put, 0);
            if (got <= 0) {
                break;
            }
            done += (size_t)got;
        }
        uint64_t stop = bench_now_ns();
        samples[r] = stop - start;
        checksum += buffer[0] + buffer[payload - 1];
        BENCH_CLOSE(client);
#if defined(_WIN32)
        WaitForSingleObject(server, INFINITE);
        CloseHandle(server);
#else
        pthread_join(server, NULL);
#endif
    }

    BENCH_CLOSE(listener);
#if defined(_WIN32)
    WSACleanup();
#endif

    bench_sort_u64(samples, (size_t)repetitions);
    bench_metric("p50", bench_percentile(samples, (size_t)repetitions, 50, 100), "ns");
    bench_metric("max", samples[repetitions - 1], "ns");
    bench_metric("bytes", (uint64_t)payload, "bytes");
    bench_checksum(checksum);
    free(samples);
    free(buffer);
    return 0;
}
