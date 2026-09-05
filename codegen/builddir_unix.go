//go:build unix

package codegen

import (
	"os"
	"syscall"
)

// lockBuildDirFile takes a non-blocking exclusive flock on path so two
// processes never generate into the same build directory at once.
func lockBuildDirFile(path string) (unlock func(), ok bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}
