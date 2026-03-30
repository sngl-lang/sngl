package android

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// androidTool locates an Android SDK tool by name. It checks PATH first,
// then falls back to known subdirectories under ANDROID_HOME.
var androidToolDirs = map[string]string{
	"adb":      "platform-tools",
	"emulator": "emulator",
}

func androidTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home := os.Getenv("ANDROID_HOME")
	if home == "" {
		home = os.Getenv("ANDROID_SDK_ROOT")
	}
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

// Run implements codegen.Runner. It builds the Android project with Gradle,
// ensures an ADB device is available (starting an emulator if needed),
// installs the APK, and launches the main activity.
func (g *Generator) Run(dir string, args []string) error {
	if _, err := androidTool("adb"); err != nil {
		return err
	}

	if err := ensureDevice(); err != nil {
		return err
	}

	pkg, err := readPackage(dir)
	if err != nil {
		return err
	}

	if err := gradleBuild(dir); err != nil {
		return err
	}

	apk := filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk")
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
	if _, err := os.Stat(gradle); err != nil {
		var lookErr error
		gradle, lookErr = exec.LookPath("gradle")
		if lookErr != nil {
			return fmt.Errorf("neither gradlew nor gradle found (install Gradle or use Android Studio)")
		}
	}

	fmt.Fprintln(os.Stderr, "sngl: building APK...")
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
	fmt.Fprintln(os.Stderr, "sngl: installing APK...")
	install := exec.Command(adb, "install", "-r", apk)
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("adb install failed: %w", err)
	}
	return nil
}

func adbLaunch(pkg string) error {
	adb, _ := androidTool("adb")
	fmt.Fprintf(os.Stderr, "sngl: launching %s/.MainActivity\n", pkg)
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

	emu := exec.Command(emulatorPath, "-avd", avd)
	emu.Stdout = nil
	emu.Stderr = nil
	if err := emu.Start(); err != nil {
		return fmt.Errorf("starting emulator: %w", err)
	}

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
	for _, line := range strings.Split(string(out), "\n") {
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

func pickAVD() (string, error) {
	emulatorPath, err := androidTool("emulator")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(emulatorPath, "-list-avds").Output()
	if err != nil {
		return "", fmt.Errorf("listing AVDs: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("no Android Virtual Devices configured; create one with Android Studio or avdmanager")
}
