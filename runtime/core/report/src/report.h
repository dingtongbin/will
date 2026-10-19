/* Internal interface of the report and symbol layer. The public entry points
   are in willrt.h; the test-only getter is compiled only for the contract
   test and never exists in a production object. */

#ifndef WILLRT_REPORT_REPORT_H
#define WILLRT_REPORT_REPORT_H

#include "../../../include/willrt.h"

#ifdef WILLRT_REPORT_TEST
/* willrt_report_test_has_sink reports whether a custom sink is installed.
   It exists only so the contract test can check that NULL restored the
   default without writing to the real standard error. */
int willrt_report_test_has_sink(void);
#endif

#endif
