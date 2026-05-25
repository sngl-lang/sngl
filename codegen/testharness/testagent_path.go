//go:build !js

// Package testharness houses helpers shared across platform launchers
// for locating per-target-language testagent runtime sources in the
// sngl source tree.
package testharness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LangTestagentPath returns the absolute path to pkg/<lang>/testagent
// in the sngl source tree. Used by platforms whose generated test
// binaries link the testagent runtime as a local module.
//
// Lookup order:
//  1. $SNGL_HOST_GO_MOD: the script-test convention from Plan 1; its
//     parent dir is the sngl repo root.
//  2. Walk up from os.Executable() looking for a go.mod whose module
//     path is git.duckfam.us/jonathan/sngl.
//  3. Walk up from runtime.Caller(0)'s source path (works for `go
//     test` and `go run` where Executable() points at a build cache).
func LangTestagentPath(lang string) (string, error) {
	if mod := os.Getenv("SNGL_HOST_GO_MOD"); mod != "" {
		root := filepath.Dir(mod)
		p := filepath.Join(root, "pkg", lang, "testagent")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exe)
	}
	if _, here, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, here)
	}
	for _, start := range candidates {
		dir := filepath.Dir(start)
		for i := 0; i < 12; i++ {
			modPath := filepath.Join(dir, "go.mod")
			if data, err := os.ReadFile(modPath); err == nil {
				if strings.Contains(string(data), "module git.duckfam.us/jonathan/sngl") {
					p := filepath.Join(dir, "pkg", lang, "testagent")
					if _, err := os.Stat(p); err == nil {
						return p, nil
					}
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not locate pkg/%s/testagent (set SNGL_HOST_GO_MOD)", lang)
}
