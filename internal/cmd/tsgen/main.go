// Command tsgen regenerates the tree-sitter SNGL parser from grammar.js.
//
// It runs tree-sitter generate, builds the shared library for editor plugins,
// copies query files to the neovim config, and writes a grammar hash file
// to invalidate the Go build cache.
//
// Usage: go run ./internal/cmd/tsgen
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	// Resolve project root relative to this source file.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	grammarDir := filepath.Join(root, "editors", "tree-sitter-sngl")
	tsparserDir := filepath.Join(root, "internal", "parser", "internal", "tsparser")

	// 1. tree-sitter generate
	run(grammarDir, "tree-sitter", "generate")

	// 2. Build shared library (for neovim/editors)
	soName := "sngl.so"
	if runtime.GOOS == "windows" {
		soName = "sngl.dll"
	} else if runtime.GOOS == "darwin" {
		soName = "sngl.dylib"
	}
	run(grammarDir, "cc", "-o", soName, "-shared", "-fPIC", "-Isrc",
		"src/parser.c", "src/scanner.c", "-Os")

	// 3. Install to neovim tree-sitter parser dir (best-effort)
	home, err := os.UserHomeDir()
	if err == nil {
		nvimParserDir := filepath.Join(home, ".local", "share", "nvim", "lazy",
			"nvim-treesitter", "parser")
		os.MkdirAll(nvimParserDir, 0o755)
		copyFile(filepath.Join(grammarDir, soName), filepath.Join(nvimParserDir, "sngl.so"))
	}

	// 4. Copy query files to neovim config
	nvimQueryDir := filepath.Join(root, "editors", "neovim", "queries", "sngl")
	os.MkdirAll(nvimQueryDir, 0o755)
	srcQueryDir := filepath.Join(grammarDir, "queries")
	filepath.WalkDir(srcQueryDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) == ".scm" {
			copyFile(path, filepath.Join(nvimQueryDir, d.Name()))
		}
		return nil
	})

	// 5. Write grammar hash to bust cgo cache
	h := sha256.New()
	for _, name := range []string{"src/parser.c", "src/scanner.c"} {
		f, err := os.Open(filepath.Join(grammarDir, name))
		if err != nil {
			fatalf("open %s: %v", name, err)
		}
		io.Copy(h, f)
		f.Close()
	}
	hashFile := filepath.Join(tsparserDir, "grammar_hash.txt")
	os.WriteFile(hashFile, []byte(fmt.Sprintf("%x\n", h.Sum(nil))), 0o644)

	fmt.Println("tsgen: done")
}

func run(dir string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatalf("%s %v: %v", name, args, err)
	}
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		return // best-effort
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer out.Close()
	io.Copy(out, in)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tsgen: "+format+"\n", args...)
	os.Exit(1)
}
