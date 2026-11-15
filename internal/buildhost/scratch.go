package buildhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ScratchDir is where SNGL keeps what it makes inside a project while it
// works, relative to the project: a const evaluator's program, which has to
// sit in the module for its imports to resolve, and the preview worker.
//
// One directory, made once and kept, rather than a directory per program
// beside the project's sources: making and removing one changes the time of
// the directory holding it, and whatever compares that time -- go test, when
// it decides whether a result it cached still holds -- sees the project
// change on every build.
const ScratchDir = ".tmp/sngl"

// Scratch returns dir's ScratchDir, creating it hidden from git: a
// .gitignore of "*" hides the directory, itself included, so `git status`
// stays clean and nothing in it can be committed by accident.
//
// It looks at nothing it does not make. go test counts what a test stats or
// opens as an input of its result, and the scratch directory's time changes
// with every program made in it, so a look -- MkdirAll's, or one for the
// .gitignore -- would make a test that compiles a const differ on every
// run. The .gitignore is written by whoever made the directory.
func Scratch(dir string) (string, error) {
	parent := filepath.Join(dir, filepath.Dir(filepath.FromSlash(ScratchDir)))
	if err := os.Mkdir(parent, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("creating %s: %w", parent, err)
	}
	s := filepath.Join(dir, filepath.FromSlash(ScratchDir))
	err := os.Mkdir(s, 0o755)
	if errors.Is(err, fs.ErrExist) {
		return s, nil
	}
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", s, err)
	}
	if err := os.WriteFile(filepath.Join(s, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return "", err
	}
	return s, nil
}
