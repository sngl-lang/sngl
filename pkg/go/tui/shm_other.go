//go:build !linux

package tui

// A POSIX shared memory object is reachable as a file only on Linux; elsewhere
// shm_open is a syscall with no path behind it, and handing one to the terminal
// would take cgo. Those platforms take the pty path, which works everywhere.

func shmSupported() bool { return false }

func shmPut([]byte) (string, bool) { return "", false }
