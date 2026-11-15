/* sngl_win32_agent.h is the test agent's channel (sngl_win32_agent.c). */
#ifndef SNGL_WIN32_AGENT_H
#define SNGL_WIN32_AGENT_H

#include "sngl_test.h"

/* sngl_win32_serve serves tests to the driver listening on the loopback
 * port argv[1] names, until it is gone. */
int sngl_win32_serve(const sngl_test *tests, size_t n, int argc, char **argv);

#endif
