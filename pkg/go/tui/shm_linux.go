//go:build linux

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// shmDir is where a POSIX shared memory object lives on Linux: shm_open(3)
// creates a file here, so a name passed to the terminal is a path here with the
// leading slash dropped. Every other platform's shm_open is a syscall with no
// path behind it, which is why this file is Linux-only.
const shmDir = "/dev/shm"

var (
	shmOnce sync.Once
	shmOK   bool
)

// shmSupported reports whether a frame can be handed over as shared memory
// rather than written down the pty.
//
// Two things have to hold and neither is visible in the graphics protocol's own
// handshake. The terminal must be on this machine -- an escape naming a memory
// object means nothing at the far end of an ssh connection, and kitty answers
// such a request with an error rather than falling back -- and the object has to
// be creatable, which a container with no /dev/shm mounted will refuse.
func shmSupported() bool {
	shmOnce.Do(func() {
		for _, v := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
			if os.Getenv(v) != "" {
				return
			}
		}
		f, err := os.CreateTemp(shmDir, ".sngl-probe-*")
		if err != nil {
			return
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		shmOK = true
	})
	return shmOK
}

// resetShmDetection clears the cached probe (test-only seam).
func resetShmDetection() { shmOnce = sync.Once{}; shmOK = false }

var (
	shmMu      sync.Mutex
	shmCounter int
	// A read is asynchronous: the escape naming the object has been written, but
	// the terminal reads it whenever it gets to it. The protocol makes the unlink
	// the terminal's job, so this is only the backstop for one that claimed kitty
	// support and never read -- removed two transmits late, by which point an
	// in-order reader is long done with it.
	shmPending []string
)

// shmPut writes payload into a fresh shared memory object and returns the name
// to hand the terminal.
func shmPut(payload []byte) (string, bool) {
	shmMu.Lock()
	shmCounter++
	name := fmt.Sprintf("/sngl-%d-%d", os.Getpid(), shmCounter)
	var stale []string
	if len(shmPending) >= 2 {
		stale, shmPending = shmPending[:len(shmPending)-1], shmPending[len(shmPending)-1:]
	}
	shmPending = append(shmPending, name)
	shmMu.Unlock()

	for _, s := range stale {
		_ = os.Remove(shmPath(s))
	}

	path := shmPath(name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", false
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", false
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", false
	}
	return name, true
}

// shmPath is the file backing the shared memory object named name.
func shmPath(name string) string {
	return filepath.Join(shmDir, strings.TrimPrefix(name, "/"))
}
