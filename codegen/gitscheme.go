package codegen

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	RegisterFSScheme(&GitImporter{})
}

// GitImporter resolves git:// scheme imports by cloning repositories into
// a local cache directory. The URI format is:
//
//	git://host/path@ref#hash
//
// where ref is a git ref (tag, branch, commit) and hash is a sha256 content
// hash for integrity verification. Use "#-" to skip hash verification.
type GitImporter struct{}

func (g *GitImporter) Scheme() string { return "git" }

func (g *GitImporter) ResolveFS(uri, dir string) (fs.FS, error) {
	parsed, err := parseGitURI(uri)
	if err != nil {
		return nil, err
	}

	cacheDir := gitCacheDir(parsed.host, parsed.path, parsed.ref)

	// Check if already cached
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		if parsed.hash != "-" {
			if err := verifyHash(cacheDir, parsed.hash); err != nil {
				return nil, fmt.Errorf("cached content hash mismatch: %w", err)
			}
		}
		return os.DirFS(cacheDir), nil
	}

	// Clone into cache
	if err := gitClone(parsed, cacheDir); err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}

	// Verify hash
	if parsed.hash != "-" {
		if err := verifyHash(cacheDir, parsed.hash); err != nil {
			os.RemoveAll(cacheDir)
			return nil, fmt.Errorf("content hash mismatch: %w", err)
		}
	}

	return os.DirFS(cacheDir), nil
}

type gitURI struct {
	host string // e.g., "github.com"
	path string // e.g., "sngl-lang/x"
	ref  string // e.g., "v1.0.0", "main"
	hash string // sha256 hex or "-"
}

func parseGitURI(uri string) (*gitURI, error) {
	rest := strings.TrimPrefix(uri, "git://")

	// Split hash
	hash := "-"
	if i := strings.LastIndex(rest, "#"); i >= 0 {
		hash = rest[i+1:]
		rest = rest[:i]
	}

	// Split ref
	ref := ""
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		ref = rest[i+1:]
		rest = rest[:i]
	}
	if ref == "" {
		return nil, fmt.Errorf("git:// URI requires @ref (e.g., git://host/repo@v1.0.0)")
	}

	// Split host/path
	if before, after, ok := strings.Cut(rest, "/"); ok {
		return &gitURI{
			host: before,
			path: after,
			ref:  ref,
			hash: hash,
		}, nil
	}

	return nil, fmt.Errorf("git:// URI requires host/path (e.g., git://github.com/user/repo)")
}

// SnglCacheDir returns the base cache directory for SNGL.
func SnglCacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "sngl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "sngl-cache")
	}
	return filepath.Join(home, ".cache", "sngl")
}

func gitCacheDir(host, repoPath, ref string) string {
	return filepath.Join(SnglCacheDir(), "git", host, repoPath, ref)
}

func gitClone(parsed *gitURI, destDir string) error {
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}

	repoURL := "https://" + parsed.host + "/" + parsed.path + ".git"
	slog.Info("exec", "cmd", "git clone", "repo", repoURL, "ref", parsed.ref, "dest", destDir)
	cmd := exec.Command("git", "clone", "--depth=1", "--branch="+parsed.ref, repoURL, destDir)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// verifyHash computes a sha256 hash of all .sngl files in the directory
// (sorted by name) and compares it to the expected hash.
func verifyHash(dir string, expected string) error {
	got, err := computeDirHash(dir)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("expected %s, got %s", expected, got)
	}
	return nil
}

// computeDirHash returns the git-scheme content hash for a cached directory:
// sha256 over the concatenation of (relative path, file bytes) for every
// .sngl file below dir, in sorted order. Used both for integrity verification
// and for the value `sngl pkg update` prints after re-cloning.
func computeDirHash(dir string) (string, error) {
	h := sha256.New()
	var names []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".sngl") {
			rel, _ := filepath.Rel(dir, path)
			names = append(names, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		h.Write([]byte(name))
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Refresh drops the cached clone, re-clones at the specified ref, and returns
// the recomputed content hash.
func (g *GitImporter) Refresh(uri, _ string) (string, error) {
	parsed, err := parseGitURI(uri)
	if err != nil {
		return "", err
	}
	cacheDir := gitCacheDir(parsed.host, parsed.path, parsed.ref)
	if err := os.RemoveAll(cacheDir); err != nil {
		return "", fmt.Errorf("clear cache: %w", err)
	}
	if err := gitClone(parsed, cacheDir); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return computeDirHash(cacheDir)
}
