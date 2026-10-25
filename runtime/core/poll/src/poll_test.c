/* Contract test for the poller's readiness scan: one listening socket becomes
   readable and its user tag is appended to the caller's out array. The scan
   takes the descriptor set from the caller, so it has no registry and no
   capacity limit of its own; a second scan watches 300 descriptors at once to
   show that the size is bounded by nothing but the call. */

#include "../../../include/willrt.h"

#include <assert.h>
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

static test_socket test_socket_new(void) {
#ifdef _WIN32
    return socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
#else
    return socket(AF_INET, SOCK_STREAM, 0);
#endif
}

/* An unbound datagram socket is pollable and has no peer state to turn into a
   hangup on POSIX. The bulk scan uses these so the contract is platform
   neutral. */
static test_socket test_socket_new_udp(void) {
#ifdef _WIN32
    return socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
#else
    return socket(AF_INET, SOCK_DGRAM, 0);
#endif
}

static void test_socket_close(test_socket socket_) {
#ifdef _WIN32
    closesocket(socket_);
#else
    close(socket_);
#endif
}

/* willrt_poll_out is the caller's out array: a data pointer, a live length, and
   a capacity, in the order the runtime appends into. */
typedef struct {
    int64_t* data;
    int64_t len;
    int64_t cap;
} test_out;

static int32_t scan_once(const int64_t* fds, const uint32_t* events,
                         const int64_t* users, int64_t count, int64_t timeout,
                         test_out* out) {
    return willrt_poll_wait(fds, events, users, count, timeout, out);
}

int main(void) {
    test_socket listener;
    test_socket client;
    struct sockaddr_in addr;
#ifdef _WIN32
    int length = (int)sizeof(addr);
#else
    socklen_t length = (socklen_t)sizeof(addr);
#endif
    int64_t fd;
    uint32_t event = WILLRT_POLL_IN;
    int64_t user = 7;
    int64_t got[300];
    test_out out;
    test_socket many[300];
    int64_t fds[300];
    uint32_t events[300];
    int64_t users[300];
    int index;

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

    client = test_socket_new();
    assert(client != TEST_INVALID);
    assert(connect(client, (const struct sockaddr*)&addr, length) == 0);

    fd = (int64_t)listener;
    out.data = got;
    out.len = 0;
    out.cap = 8;
    assert(scan_once(&fd, &event, &user, 1, INT64_C(2000) * INT64_C(1000000),
                     &out)
           == WILLRT_OK);
    assert(out.len == 1);
    assert(got[0] == user);

    /* A scan with no descriptors sleeps the timeout instead of spinning. */
    out.len = 0;
    assert(scan_once(NULL, NULL, NULL, 0, INT64_C(1000) * INT64_C(1000000),
                     &out)
           == WILLRT_OK);
    assert(out.len == 0);

    /* 300 descriptors in one call: the size comes from the caller. */
    for (index = 0; index < 300; index++) {
        many[index] = test_socket_new_udp();
        assert(many[index] != TEST_INVALID);
        fds[index] = (int64_t)many[index];
        events[index] = WILLRT_POLL_IN;
        users[index] = (int64_t)index;
    }
    out.data = got;
    out.len = 0;
    out.cap = 300;
    assert(scan_once(fds, events, users, 300, 0, &out) == WILLRT_OK);
    assert(out.len == 0);
    for (index = 0; index < 300; index++) {
        test_socket_close(many[index]);
    }

    willrt_poll_shutdown();
    test_socket_close(client);
    test_socket_close(listener);
#ifdef _WIN32
    (void)WSACleanup();
#endif
    return 0;
}
