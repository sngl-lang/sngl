package android

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Snapshot implements codegen.Snapshotter. It uses the same flow as sngl run
// (generate → build → install → launch) then captures a screenshot via ADB.
func (g *Generator) Snapshot(irPkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	adb, err := androidTool("adb")
	if err != nil {
		return nil, fmt.Errorf("adb not found — Android SDK required for screenshots")
	}

	if err := ensureDevice(); err != nil {
		return nil, err
	}

	const appPkg = "sngl.snapshot.app"

	tmpDir, err := os.MkdirTemp("", "sngl-android-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := map[string]string{
		"main":    "true",
		"package": appPkg,
		"gradle":  "false",
	}

	resp, err := g.Generate(&codegen.Request{
		Pkg:     irPkg,
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
		exec.Command(adb, "shell", "am", "force-stop", appPkg).Run()
		exec.Command(adb, "uninstall", appPkg).Run()
	}()

	return launchAndCapture(adb, appPkg, appPkg+"/.MainActivity")
}

// activityHintFromFQCN extracts the activity short name from "pkg/.Activity"
// or "pkg/pkg.Activity" forms so it can be matched against dumpsys focus.
func activityHintFromFQCN(fqcn string) string {
	if i := strings.Index(fqcn, "/"); i >= 0 {
		rest := fqcn[i+1:]
		if strings.HasPrefix(rest, ".") {
			return rest[1:]
		}
		if j := strings.LastIndex(rest, "."); j >= 0 {
			return rest[j+1:]
		}
		return rest
	}
	return ""
}

// launchAndCapture starts the activity at activityFQCN, waits for the package
// to gain window focus, sanity-checks that the app process didn't crash, then
// captures a PNG via screencap. Used by both single Snapshot and BatchSnapshot.
func launchAndCapture(adb, pkg, activityFQCN string) ([]byte, error) {
	// Clear logcat so any crash we surface below is from this run.
	exec.Command(adb, "logcat", "-c").Run()

	slog.Info("exec", "cmd", "adb launch", "activity", activityFQCN)
	launch := exec.Command(adb, "shell", "am", "start", "-n", activityFQCN)
	launch.Stdout = os.Stdout
	launch.Stderr = os.Stderr
	if err := launch.Run(); err != nil {
		return nil, fmt.Errorf("am start %s: %w", activityFQCN, err)
	}

	// Wait for our activity to reach the foreground, then give Compose
	// a moment to finish its first frame. The activity hint disambiguates
	// successive same-package launches in batch mode (the previous
	// activity's window can linger in dumpsys for a tick).
	hint := activityHintFromFQCN(activityFQCN)
	focusErr := waitForFocus(pkg, hint, 15*time.Second)

	// Verify the app process is still alive — without this, a crashed app
	// would screenshot the home screen and we'd silently capture garbage.
	if !isProcessAlive(adb, pkg) {
		trace := fetchCrashLog(adb, pkg)
		if trace == "" {
			trace = "(no AndroidRuntime crash trace found in logcat)"
		}
		return nil, fmt.Errorf("app %s crashed before snapshot:\n%s", pkg, trace)
	}
	if focusErr != nil {
		return nil, focusErr
	}
	time.Sleep(2 * time.Second)

	png, err := exec.Command(adb, "exec-out", "screencap", "-p").Output()
	if err != nil {
		return nil, fmt.Errorf("capturing screenshot: %w", err)
	}
	return png, nil
}

// isProcessAlive reports whether the given package's process is currently
// running on the connected device.
func isProcessAlive(adb, pkg string) bool {
	out, err := exec.Command(adb, "shell", "pidof", pkg).Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// fetchCrashLog returns the most recent AndroidRuntime FATAL EXCEPTION block
// from logcat. Caller should clear logcat before launch so this returns only
// crashes from the current run.
func fetchCrashLog(adb, pkg string) string {
	out, err := exec.Command(adb, "logcat", "-d", "-v", "time").Output()
	if err != nil {
		return ""
	}
	var trace []string
	capture := false
	for _, line := range strings.Split(string(out), "\n") {
		isRuntime := strings.Contains(line, "AndroidRuntime")
		if isRuntime && strings.Contains(line, "FATAL EXCEPTION") {
			capture = true
			trace = trace[:0]
		}
		if !capture {
			continue
		}
		if !isRuntime {
			break
		}
		trace = append(trace, line)
	}
	return strings.Join(trace, "\n")
}
