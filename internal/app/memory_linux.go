package app

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// appMemory sums the resident memory of this process and its descendants: WebKitGTK's web and
// network processes are its children.
func appMemory() int64 {
	children := map[int][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// after the command name, in parentheses that it may itself contain: the state, then the parent
		if i := bytes.LastIndexByte(b, ')'); i >= 0 {
			if f := strings.Fields(string(b[i+1:])); len(f) > 1 {
				ppid, _ := strconv.Atoi(f[1])
				children[ppid] = append(children[ppid], pid)
			}
		}
	}
	var total int64
	for queue := []int{os.Getpid()}; len(queue) > 0; queue = queue[1:] {
		total += residentBytes(queue[0])
		queue = append(queue, children[queue[0]]...)
	}
	return total
}

// residentBytes is a process's resident set, from /proc/<pid>/statm (its second field, in pages).
func residentBytes(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseInt(f[1], 10, 64)
	return pages * int64(os.Getpagesize())
}
