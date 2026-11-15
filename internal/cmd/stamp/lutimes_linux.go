package main

import (
	"syscall"
	"time"
	"unsafe"
)

// lutimes sets the times of the symlink at path itself. go test compares a
// path it saw a test stat by Lstat as well as Stat, so a symlink's own time
// is an input too, and a checkout gives it the time it was checked out.
//
// utimensat by hand: syscall has no lutimes, and golang.org/x/sys/unix is
// only an indirect requirement, which importing would change go.mod for.
func lutimes(path string, t time.Time) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	ts := [2]syscall.Timespec{syscall.NsecToTimespec(t.UnixNano()), syscall.NsecToTimespec(t.UnixNano())}
	const atSymlinkNoFollow = 0x100
	atFDCWD := -0x64 // the working directory, which path is relative to
	_, _, errno := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(atFDCWD), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&ts)), atSymlinkNoFollow, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
