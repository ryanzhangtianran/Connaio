package app

/*
#include <libproc.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <unistd.h>

// responsibility_get_pid_responsible_for_pid names the process macOS holds responsible for pid:
// the app for the WebKit services its web view runs in, which launchd starts, not the app.
extern pid_t responsibility_get_pid_responsible_for_pid(pid_t);

// appFootprint sums the physical footprint (Activity Monitor's Memory column) of this process and
// every process it is responsible for.
static long long appFootprint(void) {
	pid_t self = getpid();
	int n = proc_listallpids(NULL, 0);
	if (n <= 0) return 0;
	int cap = n + 64;
	pid_t *pids = malloc(sizeof(pid_t) * cap);
	if (!pids) return 0;
	n = proc_listallpids(pids, sizeof(pid_t) * cap);
	long long total = 0;
	for (int i = 0; i < n; i++) {
		if (pids[i] != self && responsibility_get_pid_responsible_for_pid(pids[i]) != self) continue;
		struct rusage_info_v2 ri;
		if (proc_pid_rusage(pids[i], RUSAGE_INFO_V2, (rusage_info_t *)&ri) == 0) total += ri.ri_phys_footprint;
	}
	free(pids);
	return total;
}
*/
import "C"

func appMemory() int64 { return int64(C.appFootprint()) }
