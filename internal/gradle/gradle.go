// Package gradle runs a generated Android scaffold's wrapper for the
// compiler's own builds and tests: one build at a time on this machine, across
// test binaries, in one daemon the first build starts.
package gradle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// IdleTimeout spans the gaps between a test run's android launches, where
// Gradle's default holds the daemon's heap for three hours.
const IdleTimeout = 5 * time.Minute

// Command is the wrapper in dir running args in env, with the flags every
// build here takes. The caller holds Lock for as long as the build runs.
//
// The locale is pinned because the client's user.country is part of the
// daemon's identity: a caller under en_US and one under C.UTF-8 each started a
// 3GB daemon of their own, and the two together ran a rerecord out of memory.
func Command(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	gradlew := filepath.Join(dir, "gradlew")
	_ = os.Chmod(gradlew, 0o755)
	args = append(args,
		"--console=plain",
		"-Dorg.gradle.daemon.idletimeout="+strconv.FormatInt(IdleTimeout.Milliseconds(), 10),
	)
	cmd := exec.CommandContext(ctx, gradlew, args...)
	cmd.Dir = dir
	cmd.Env = append(env, "LC_ALL=C.UTF-8")
	return cmd
}

// Lock waits until no other build on this machine is running and returns the
// function that lets the next one go. A lock that cannot be taken is no lock.
func Lock() func() {
	dir, err := os.UserCacheDir()
	if err != nil {
		return func() {}
	}
	dir = filepath.Join(dir, "sngl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, "gradle.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		_ = unlockFile(f)
		f.Close()
	}
}
