/* Contract test for the lifecycle hooks. There is no observable state yet, so
   the test pins that repeated calls are harmless and leave the process usable. */

#include "../../../include/willrt.h"

#include <assert.h>

int main(void) {
    willrt_init();
    willrt_init();
    willrt_shutdown();
    willrt_shutdown();
    willrt_init();
    willrt_shutdown();
    return 0;
}
