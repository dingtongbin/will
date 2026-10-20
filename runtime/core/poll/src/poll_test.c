/* Contract test for the poller: one listening socket becomes readable and the
   callback receives the input event. The test runs on sockets for both POSIX
   and Windows, because the Windows poller accepts sockets only. */

#include "../../../include/willrt.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdint.h>
#include <string.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET test_socket;
#define TEST_INVALID INVALID_SOCKET
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
typedef int test_socket;
#define TEST_INVALID (-1)
#endif

static atomic_int ready_calls;
static atomic_int ready_events;

static void ready(int64_t fd, uint32_t events, void* arg) {
    (void)fd;
    (void)arg;
    atomic_store(&ready_events, (int)events);
    atomic_fetch_add(&ready_calls, 1);
}

static test_socket test_socket_new(void) {
#ifdef _WIN32
    return socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
#else
    return socket(AF_INET, SOCK_STREAM, 0);
#endif
}

static void test_socket_close(test_socket socket_) {
#ifdef _WIN32
    closesocket(socket_);
#else
    close(socket_);
#endif
}

int main(void) {
    test_socket listener;
    test_socket client;
    struct sockaddr_in addr;
    int length = (int)sizeof(addr);
    int waits;

#ifdef _WIN32
    WSADATA data;
    assert(WSAStartup(MAKEWORD(2, 2), &data) == 0);
#endif

    listener = test_socket_new();
    assert(listener != TEST_INVALID);
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    addr.sin_port = 0;
    assert(bind(listener, (const struct sockaddr*)&addr, sizeof(addr)) == 0);
    assert(listen(listener, 1) == 0);
    assert(getsockname(listener, (struct sockaddr*)&addr, &length) == 0);

    assert(willrt_poll_add((int64_t)listener, WILLRT_POLL_IN, (void*)ready,
                           NULL)
           == WILLRT_OK);
    client = test_socket_new();
    assert(client != TEST_INVALID);
    assert(connect(client, (const struct sockaddr*)&addr, length) == 0);

    for (waits = 0; waits < 200 && atomic_load(&ready_calls) == 0; waits++) {
        willrt_os_sleep_ns(INT64_C(10) * INT64_C(1000000));
    }
    assert(atomic_load(&ready_calls) > 0);
    assert((atomic_load(&ready_events) & WILLRT_POLL_IN) != 0);
    assert(willrt_poll_del((int64_t)listener) == WILLRT_OK);
    assert(willrt_poll_del((int64_t)listener) == WILLRT_EMPTY);

    willrt_poll_shutdown();
    test_socket_close(client);
    test_socket_close(listener);
#ifdef _WIN32
    (void)WSACleanup();
#endif
    return 0;
}
