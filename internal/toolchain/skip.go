package toolchain

import "strings"

// SkipReason matches the toolchain-missing messages a caller would rather
// surface as a skip than as a failure.
//
// It exists because Unavailable cannot ask every question up front. A JDK's
// major version is readable before the build; that Gradle refuses the class
// file it then produced is only readable from the build's own output. So the
// probe answers what a probe can and this reads the rest back out.
//
// The list is host gaps only. A gap in a *platform* -- a stdlib component it
// declares no implementation for -- is deliberately not here: read off one
// list, a genuinely broken toolchain would hide as an unimplemented feature.
func SkipReason(out string) (string, bool) {
	signals := []struct{ needle, reason string }{
		{"Chrome/Chromium not on PATH", "Chrome/Chromium not on PATH"},
		{"JDK 17+ not on PATH", "JDK 17+ not on PATH"},
		{"gradle not on PATH", "gradle not available"},
		{"ANDROID_HOME", "Android SDK not configured"},
		{"emulator not found", "emulator not found in Android SDK"},
		{"no AVD configured", "no AVD configured"},
		{"gtk4 dev libraries not installed", "gtk4 dev libraries not installed"},
		{"Package gtk4 was not found", "gtk4 dev libraries not installed"},
		{"No package 'gtk4' found", "gtk4 dev libraries not installed"},
		{"go not found in PATH", "go not on PATH"},
		// The JDK selector pins a supported JDK; if Gradle still rejects the
		// runtime, that is an environment mismatch rather than a regression.
		{"install JDK 21", "no compatible JDK for the Android build"},
		{"Could not determine java version", "JDK unsupported by bundled Gradle"},
		{"Unsupported class file major version", "JDK unsupported by bundled Gradle"},
	}
	for _, s := range signals {
		if strings.Contains(out, s.needle) {
			return s.reason, true
		}
	}
	return "", false
}
