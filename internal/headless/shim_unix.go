//go:build unix

package headless

import (
	"os"
	"strconv"
	"syscall"
)

// closeInheritedOnExec marks every descriptor past stderr close-on-exec, so a
// command this process starts inherits none of what it was handed. This
// process keeps them.
func closeInheritedOnExec() {
	// Listing /proc/self/fd opens a descriptor of its own, which is marked
	// along with the rest; it is closed by the time anything is started.
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		for _, e := range entries {
			if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
				syscall.CloseOnExec(fd)
			}
		}
		return
	}
	for fd := 3; fd < 1024; fd++ {
		syscall.CloseOnExec(fd)
	}
}
