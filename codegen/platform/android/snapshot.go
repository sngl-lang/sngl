package android

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Snapshot implements codegen.Snapshotter. It uses the same flow as sngl run
// (generate → build → install → launch) then captures a screenshot via ADB.
func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	adb, err := androidTool("adb")
	if err != nil {
		return nil, fmt.Errorf("adb not found — Android SDK required for screenshots")
	}

	if err := ensureDevice(); err != nil {
		return nil, err
	}

	const pkg = "sngl.snapshot.app"

	// Generate code into a temp directory.
	tmpDir, err := os.MkdirTemp("", "sngl-android-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := map[string]string{
		"main":    "true",
		"package": pkg,
		"gradle":  "false",
	}

	resp, err := g.Generate(&codegen.Request{
		Doc:     doc,
		Lang:    lang,
		Options: opts,
	})
	if err != nil {
		return nil, fmt.Errorf("generating android code: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("generating android code: %s", resp.Error)
	}

	// Write generated files.
	for _, file := range resp.Files {
		path := filepath.Join(tmpDir, file.Name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		f, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("creating %s: %w", file.Name, err)
		}
		_, writeErr := file.WriteTo(f)
		f.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return nil, fmt.Errorf("writing %s: %w", file.Name, writeErr)
		}
	}

	// Build → install → launch (same as sngl run)
	apk, err := g.Build(tmpDir, opts)
	if err != nil {
		return nil, fmt.Errorf("building APK: %w", err)
	}

	if err := adbInstall(apk); err != nil {
		return nil, err
	}
	defer func() {
		exec.Command(adb, "shell", "am", "force-stop", pkg).Run()
		exec.Command(adb, "uninstall", pkg).Run()
	}()

	if err := adbLaunch(pkg); err != nil {
		return nil, err
	}

	// Wait for our activity to reach the foreground, then give Compose
	// a moment to finish its first frame.
	if err := waitForFocus(pkg, 15*time.Second); err != nil {
		return nil, err
	}
	time.Sleep(2 * time.Second)

	// Capture screenshot.
	png, err := exec.Command(adb, "exec-out", "screencap", "-p").Output()
	if err != nil {
		return nil, fmt.Errorf("capturing screenshot: %w", err)
	}

	return png, nil
}
