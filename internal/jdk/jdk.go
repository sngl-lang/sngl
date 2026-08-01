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

// JDK major-version window the Android build can use. The lower bound is the
// scaffold's compile target (jvmTarget/sourceCompatibility = 17); the upper
// bound is the newest JDK the pinned Gradle can *launch on* (gradle-8.11.1
// runs on JDK 8–23; JDK 24+ is rejected at startup). Keep both in sync with
// codegen/platform/android/templates/gradle/wrapper/gradle-wrapper.properties
// and app.build.gradle.kts.
//
// This is deliberately not a treadmill: pin a known-good JDK rather than chase
// each new host JDK. A rolling distro (or an Android Studio JBR bump) can move
// the ambient JDK past this window; discovery below simply looks past it for a
// compatible one, and reports a clear "install JDK 21" message if none exists.
const (
	MinMajor = 17
	MaxMajor = 23
)

// once memoizes discovery — it execs `java -version` on each candidate, so we
// do it at most once per process.
var once = sync.OnceValues(discover)

// CompatibleHome returns the JAVA_HOME of a JDK whose major version is within
// [MinMajor, MaxMajor] — the range the scaffold's Gradle can launch on and
// compile against. On success reason is ""; otherwise home is "" and reason is
// a plain-English explanation of what to install. The result is memoized.
// `SNGL_JAVA_HOME` overrides discovery entirely.
func CompatibleHome() (home string, reason string) {
	return once()
}

func discover() (string, string) {
	// Explicit override wins — trusted when it points to a runnable, in-range
	// JDK. We still range-check so a mistaken override fails loudly rather than
	// crashing Gradle later with a cryptic version string.
	if h := os.Getenv("SNGL_JAVA_HOME"); h != "" {
		v, ok := majorVersion(h)
		if !ok {
			return "", fmt.Sprintf("SNGL_JAVA_HOME=%s has no runnable bin/java", h)
		}
		if !inRange(v) {
			return "", fmt.Sprintf("SNGL_JAVA_HOME points to JDK %d, outside the supported range %d–%d — install JDK 21 (LTS)", v, MinMajor, MaxMajor)
		}
		return h, ""
	}

	// Scan every candidate; keep the newest in-range JDK, and remember any
	// out-of-range one so the failure message can be specific.
	best, bestV := "", -1
	exampleV := 0
	for _, h := range candidateHomes() {
		v, ok := majorVersion(h)
		if !ok {
			continue
		}
		exampleV = v
		if inRange(v) && v > bestV {
			best, bestV = h, v
		}
	}
	if best != "" {
		return best, ""
	}
	if exampleV != 0 {
		return "", fmt.Sprintf("found JDK %d but the Android build needs JDK %d–%d; install JDK 21 (LTS) or set SNGL_JAVA_HOME", exampleV, MinMajor, MaxMajor)
	}
	return "", fmt.Sprintf("no JDK found; install JDK 21 (LTS), or set SNGL_JAVA_HOME (need major version %d–%d)", MinMajor, MaxMajor)
}

func inRange(major int) bool { return major >= MinMajor && major <= MaxMajor }

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
