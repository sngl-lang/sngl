package android

import (
	"bufio"
	"context"
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

// androidTool locates an Android SDK tool by name. It checks PATH first,
// then falls back to known subdirectories under ANDROID_HOME.
var androidToolDirs = map[string]string{
	"adb":      "platform-tools",
	"emulator": "emulator",
}

// sdkRoot returns the Android SDK root directory from ANDROID_HOME or
// ANDROID_SDK_ROOT, or "" if neither is set.
func sdkRoot() string {
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		return home
	}
	return os.Getenv("ANDROID_SDK_ROOT")
}

func androidTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home := sdkRoot()
	if home == "" {
		return "", fmt.Errorf("%s not found in PATH and ANDROID_HOME is not set", name)
	}
	if subdir, ok := androidToolDirs[name]; ok {
		p := filepath.Join(home, subdir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or ANDROID_HOME (%s)", name, home)
}

// androidEnv returns the current environment with ANDROID_SDK_ROOT set
// (needed by the emulator even when launched by absolute path).
func androidEnv() []string {
	env := os.Environ()
	home := sdkRoot()
	if home == "" {
		return env
	}
	// Ensure both vars are set so tools can find system images
	hasHome, hasRoot := false, false
	for _, e := range env {
		if strings.HasPrefix(e, "ANDROID_HOME=") {
			hasHome = true
		}
		if strings.HasPrefix(e, "ANDROID_SDK_ROOT=") {
			hasRoot = true
		}
	}
	if !hasHome {
		env = append(env, "ANDROID_HOME="+home)
	}
	if !hasRoot {
		env = append(env, "ANDROID_SDK_ROOT="+home)
	}
	return env
}

// setEnv sets key=value in env, replacing any existing entry for key.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// Run implements codegen.Runner. It builds the Android project, ensures an
// ADB device is available (starting an emulator if needed), installs the
// APK, and launches the main activity.
func (g *Generator) Run(dir string, opts *ir.StructLit, args []string) error {
	if _, err := androidTool("adb"); err != nil {
		return err
	}

	if err := ensureDevice(); err != nil {
		return err
	}

	apk, err := g.Build(dir, opts)
	if err != nil {
		return err
	}

	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return fmt.Errorf("android: %w", err)
	}
	cfg = cfg.withDefaults()
	pkg := cfg.Package
	if cfg.UseGradle() {
		if p, err := readPackage(dir); err == nil {
			pkg = p
		}
	}

	if err := adbInstall(apk); err != nil {
		return err
	}

	return adbLaunch(pkg)
}

func readPackage(dir string) (string, error) {
	path := filepath.Join(dir, "app", "build.gradle.kts")
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading build.gradle.kts: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "namespace") || strings.HasPrefix(line, "applicationId") {
			if i := strings.Index(line, `"`); i >= 0 {
				if j := strings.LastIndex(line, `"`); j > i {
					return line[i+1 : j], nil
				}
			}
		}
	}
	return "app", nil
}

func gradleBuild(dir string) error {
	gradle := filepath.Join(dir, "gradlew")
	if info, err := os.Stat(gradle); err != nil {
		var lookErr error
		gradle, lookErr = exec.LookPath("gradle")
		if lookErr != nil {
			return fmt.Errorf("neither gradlew nor gradle found (install Gradle or use Android Studio)")
		}
	} else if info.Mode()&0o111 == 0 {
		os.Chmod(gradle, 0o755)
	}

	slog.Info("exec", "cmd", "gradle assembleDebug", "dir", dir)
	build := exec.Command(gradle, "assembleDebug")
	build.Dir = dir
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("gradle build failed: %w", err)
	}
	return nil
}

func adbInstall(apk string) error {
	adb, _ := androidTool("adb")
	slog.Info("exec", "cmd", "adb install", "apk", apk)
	install := exec.Command(adb, "install", "-r", "-d", apk)
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("adb install failed: %w", err)
	}
	return nil
}

func adbLaunch(pkg string) error {
	adb, _ := androidTool("adb")
	slog.Info("exec", "cmd", "adb launch", "activity", pkg+"/.MainActivity")
	launch := exec.Command(adb, "shell", "am", "start", "-n", pkg+"/.MainActivity")
	launch.Stdout = os.Stdout
	launch.Stderr = os.Stderr
	return launch.Run()
}

// ensureDevice checks that an ADB device is connected. If none is found,
// it attempts to start an Android emulator.
func ensureDevice() error {
	if hasDevice() {
		return nil
	}

	fmt.Fprintln(os.Stderr, "sngl: no ADB device found, starting emulator...")

	avd, err := pickAVD()
	if err != nil {
		return err
	}

	emulatorPath, err := androidTool("emulator")
	if err != nil {
		return err
	}

	emu := exec.Command(emulatorPath, "-avd", avd, "-no-snapshot-load")
	emuEnv := androidEnv()
	// The emulator bundles Qt with only xcb/offscreen plugins — force xcb
	// so it works on Wayland compositors (Sway, GNOME Wayland, etc.)
	emuEnv = setEnv(emuEnv, "QT_QPA_PLATFORM", "xcb")
	emu.Env = emuEnv
	emu.Stdout = os.Stderr
	emu.Stderr = os.Stderr
	if err := emu.Start(); err != nil {
		return fmt.Errorf("starting emulator: %w", err)
	}
	go emu.Wait()

	fmt.Fprintf(os.Stderr, "sngl: waiting for emulator %q...\n", avd)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	adb, _ := androidTool("adb")
	wait := exec.CommandContext(ctx, adb, "wait-for-device")
	if err := wait.Run(); err != nil {
		return fmt.Errorf("timed out waiting for emulator (120s)")
	}

	for {
		if ctx.Err() != nil {
			return fmt.Errorf("timed out waiting for emulator boot")
		}
		out, _ := exec.Command(adb, "shell", "getprop", "sys.boot_completed").Output()
		if strings.TrimSpace(string(out)) == "1" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	fmt.Fprintln(os.Stderr, "sngl: emulator ready")
	return nil
}

func hasDevice() bool {
	adb, err := androidTool("adb")
	if err != nil {
		return false
	}
	out, err := exec.Command(adb, "devices").Output()
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			return true
		}
	}
	return false
}

// waitForFocus polls until the given package's activity holds window focus,
// or the timeout elapses. This guards against screenshots taken before the
// app has fully launched (e.g. on a cold-booted emulator). When activityHint
// is non-empty, it also requires the focused window's name to contain the
// hint — used by batch snapshot to distinguish successive activities in the
// same package, since the previous activity's window can linger in dumpsys
// for a tick after force-stop.
func waitForFocus(pkg, activityHint string, timeout time.Duration) error {
	adb, _ := androidTool("adb")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command(adb, "shell", "dumpsys", "window").Output()
		if err == nil {
			s := string(out)
			if focused := extractFocusWindow(s); strings.Contains(focused, pkg) {
				if activityHint == "" || strings.Contains(focused, activityHint) {
					return nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if activityHint != "" {
		return fmt.Errorf("timed out waiting for %s/%s to gain focus", pkg, activityHint)
	}
	return fmt.Errorf("timed out waiting for %s to gain focus", pkg)
}

// extractFocusWindow returns the contents of the first
// `mCurrentFocus=Window{...}` line in dumpsys window output. Returns "" if
// no such line is found.
func extractFocusWindow(s string) string {
	const marker = "mCurrentFocus=Window{"
	_, after, ok := strings.Cut(s, marker)
	if !ok {
		return ""
	}
	rest := after
	if before, _, ok := strings.Cut(rest, "}"); ok {
		return before
	}
	return rest
}

func pickAVD() (string, error) {
	// Allow explicit override via environment variable
	if avd := os.Getenv("SNGL_AVD"); avd != "" {
		return avd, nil
	}

	emulatorPath, err := androidTool("emulator")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(emulatorPath, "-list-avds")
	cmd.Env = androidEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("listing AVDs: %w", err)
	}
	// Pick the last AVD (typically the newest API level)
	var last string
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			last = name
		}
	}
	if last == "" {
		return "", fmt.Errorf("no Android Virtual Devices configured; create one with Android Studio or avdmanager")
	}
	return last, nil
}
