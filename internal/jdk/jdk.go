// Package jdk locates a Java runtime whose version is compatible with SNGL's
// Android build toolchain, across macOS, Windows, and Linux. It is a leaf
// package (no SNGL dependencies) so both the android codegen platform and the
// test harness can share one discovery path without an import cycle.
package jdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// scan holds one discovered, runnable JDK.
type scan struct {
	home  string
	major int
}

// scanOnce memoizes the expensive part of discovery — exec'ing `java -version`
// on every candidate home. It is range-independent, so callers asking for
// different JDK windows (different toolchain combos) share one probe pass.
var scanOnce = sync.OnceValue(scanAll)

func scanAll() []scan {
	var out []scan
	for _, h := range candidateHomes() {
		if v, ok := majorVersion(h); ok {
			out = append(out, scan{home: h, major: v})
		}
	}
	return out
}

// CompatibleHome returns the JAVA_HOME of a JDK whose major version is within
// [minMajor, maxMajor] — the window the selected toolchain's Gradle can launch
// on and compile against (see internal/androidtc). On success reason is "";
// otherwise home is "" and reason is a plain-English explanation of what to
// install. The candidate probe is memoized. `SNGL_JAVA_HOME` overrides
// discovery entirely (still range-checked, so a mistaken override fails loudly
// rather than crashing Gradle later with a cryptic version string).
func CompatibleHome(minMajor, maxMajor int) (home string, reason string) {
	inRange := func(v int) bool { return v >= minMajor && v <= maxMajor }

	if h := os.Getenv("SNGL_JAVA_HOME"); h != "" {
		v, ok := majorVersion(h)
		if !ok {
			return "", fmt.Sprintf("SNGL_JAVA_HOME=%s has no runnable bin/java", h)
		}
		if !inRange(v) {
			return "", fmt.Sprintf("SNGL_JAVA_HOME points to JDK %d, outside the supported range %d–%d — install JDK %d", v, minMajor, maxMajor, preferredInstall(minMajor, maxMajor))
		}
		return h, ""
	}

	// Pick the newest in-range JDK; remember any out-of-range one so the
	// failure message can be specific.
	best, bestV := "", -1
	exampleV := 0
	for _, s := range scanOnce() {
		exampleV = s.major
		if inRange(s.major) && s.major > bestV {
			best, bestV = s.home, s.major
		}
	}
	if best != "" {
		return best, ""
	}
	install := preferredInstall(minMajor, maxMajor)
	if exampleV != 0 {
		return "", fmt.Sprintf("found JDK %d but this Android toolchain needs JDK %d–%d; install JDK %d or set SNGL_JAVA_HOME", exampleV, minMajor, maxMajor, install)
	}
	return "", fmt.Sprintf("no JDK found; install JDK %d, or set SNGL_JAVA_HOME (need major version %d–%d)", install, minMajor, maxMajor)
}

// preferredInstall picks a friendly "install JDK N" suggestion inside the
// window: the newest LTS (21, then 17) that fits, else the window's upper bound.
func preferredInstall(minMajor, maxMajor int) int {
	for _, lts := range []int{21, 17} {
		if lts >= minMajor && lts <= maxMajor {
			return lts
		}
	}
	return maxMajor
}

var versionRe = regexp.MustCompile(`version "([^"]+)"`)

// majorVersion runs `<home>/bin/java -version` and returns its major version.
// Handles both modern ("21.0.12" → 21) and legacy ("1.8.0_292" → 8) schemes.
func majorVersion(home string) (int, bool) {
	if home == "" {
		return 0, false
	}
	bin := filepath.Join(home, "bin", "java")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if !isFile(bin) {
		return 0, false
	}
	// `java -version` writes to stderr; CombinedOutput captures both.
	out, err := exec.Command(bin, "-version").CombinedOutput()
	if err != nil {
		return 0, false
	}
	return parseMajor(string(out))
}

func parseMajor(s string) (int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	parts := strings.Split(m[1], ".")
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	if first == 1 && len(parts) > 1 { // legacy 1.N scheme
		second, err := strconv.Atoi(parts[1])
		return second, err == nil && second != 0
	}
	return first, first != 0
}

// candidateHomes returns an ordered, de-duplicated list of directories that
// might be a JAVA_HOME, across macOS/Windows/Linux plus the common
// cross-platform version managers. Nonexistent entries are harmless —
// majorVersion filters them out. Discovery never trusts the ambient JAVA_HOME
// blindly: it is merely one candidate, subject to the same version check as the
// rest.
func candidateHomes() []string {
	var homes []string
	seen := map[string]struct{}{}
	add := func(h string) {
		if h == "" {
			return
		}
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		homes = append(homes, h)
	}
	// addRoot enumerates a directory whose children are JDK homes, adding both
	// each child and its macOS-bundle Contents/Home subpath.
	addRoot := func(root string) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(root, e.Name())
			add(p)
			add(filepath.Join(p, "Contents", "Home"))
		}
	}

	// Ambient environment: JAVA_HOME, then whatever `java` on PATH resolves to.
	add(os.Getenv("JAVA_HOME"))
	if p, err := exec.LookPath("java"); err == nil {
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			p = rp
		}
		add(filepath.Dir(filepath.Dir(p))) // <home>/bin/java → <home>
	}

	userHome, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "darwin":
		addRoot("/Library/Java/JavaVirtualMachines")
		if userHome != "" {
			addRoot(filepath.Join(userHome, "Library", "Java", "JavaVirtualMachines"))
		}
		// Homebrew keg-only openjdk formulae.
		for _, v := range []string{"@21", "@17", ""} {
			add("/opt/homebrew/opt/openjdk" + v)
			add("/usr/local/opt/openjdk" + v)
		}
		addRoot("/opt/homebrew/Cellar/openjdk")
		addRoot("/usr/local/Cellar/openjdk")
	case "windows":
		for _, pf := range []string{
			os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"),
			os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData"),
		} {
			if pf == "" {
				continue
			}
			for _, vendor := range []string{
				"Eclipse Adoptium", "Java", "Microsoft", "Amazon Corretto",
				"Zulu", "BellSoft", "Semeru", "Android Studio",
			} {
				addRoot(filepath.Join(pf, vendor))
			}
		}
	default: // linux, *bsd
		for _, root := range []string{"/usr/lib/jvm", "/usr/lib64/jvm", "/usr/java", "/opt/java"} {
			addRoot(root)
		}
	}

	// Cross-platform JDK version managers.
	if userHome != "" {
		addRoot(filepath.Join(userHome, ".sdkman", "candidates", "java"))
		addRoot(filepath.Join(userHome, ".asdf", "installs", "java"))
		addRoot(filepath.Join(userHome, ".jabba", "jdk"))
		addRoot(filepath.Join(userHome, ".jenv", "versions"))
	}

	return homes
}

// Env returns os.Environ() with JAVA_HOME and PATH pointed at home so any JVM
// tool spawned with it (gradle, kotlinc, d8) uses the chosen JDK rather than
// the ambient one. Returns the environment unchanged when home is "".
func Env(home string) []string {
	env := os.Environ()
	if home == "" {
		return env
	}
	bin := filepath.Join(home, "bin")
	out := make([]string, 0, len(env)+1)
	havePath := false
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(key, "JAVA_HOME"):
			continue // re-added below
		case strings.EqualFold(key, "PATH"):
			out = append(out, key+"="+bin+string(os.PathListSeparator)+val)
			havePath = true
		default:
			out = append(out, kv)
		}
	}
	out = append(out, "JAVA_HOME="+home)
	if !havePath {
		out = append(out, "PATH="+bin)
	}
	return out
}

// Bin returns the `java` executable path under home (with a .exe suffix on
// Windows). home must be non-empty.
func Bin(home string) string {
	bin := filepath.Join(home, "bin", "java")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	return bin
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
