package gencache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// compilerID identifies the running compiler: the SHA-256 of its executable.
//
// It is part of every key because a producer's code is an input no file can
// record -- a change to how gtk4 derives a declaration, or to how an argument
// is spelled for the evaluator, makes every stored answer wrong while every
// recorded input still holds. A version string would be the obvious name and
// is not enough: a developer's build carries no release version, and those are
// the builds whose producers change.
//
// Hashing ninety megabytes each process would cost more than most hits save,
// so the hash is memoized in memoDir under the executable's path, size and
// mtime -- which `go install` changes every time it writes a new binary. So
// each binary leaves a memo behind, and prune removes those nobody has used
// for maxAge.
//
// "" means the compiler could not be identified, and the store is then off:
// any fixed stand-in would be shared by every compiler that failed the same
// way, and would hand one of them another's answers.
func compilerID(memoDir string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	stamp := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d", exe, info.Size(), info.ModTime().UnixNano()))
	memo := filepath.Join(memoDir, hex.EncodeToString(stamp[:16]))
	if b, err := os.ReadFile(memo); err == nil && len(b) == 64 {
		// Marked used, as an entry is on a hit, so prune ages out the memos
		// of compilers nobody runs any more and never the one reading this.
		now := time.Now()
		os.Chtimes(memo, now, now)
		return string(b)
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	id := hex.EncodeToString(h.Sum(nil))
	if os.MkdirAll(memoDir, 0o755) == nil {
		tmp := memo + ".tmp" + strings.ReplaceAll(fmt.Sprint(os.Getpid()), "-", "")
		if os.WriteFile(tmp, []byte(id), 0o644) == nil {
			os.Rename(tmp, memo)
		}
	}
	return id
}
