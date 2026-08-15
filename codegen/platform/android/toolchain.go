package android

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/androidtc"
)

// toolchain holds paths to the minimal Android build tools plus the version
// numbers (from the selected androidtc.Combo) that drive tool discovery and
// artifact downloads.
type toolchain struct {
	Kotlinc       string   // kotlinc binary
	ComposePlugin string   // compose-compiler-plugin-embeddable.jar
	AndroidJar    string   // android.jar
	Aapt2         string   // aapt2 binary
	D8            string   // d8.jar or d8 binary wrapper script
	Zipalign      string   // zipalign binary
	ComposeJars   []string // compose runtime + dependency JARs
	KotlinLibs    []string // kotlin-stdlib etc.
	CacheDir      string   // ~/.cache/sngl/android-toolchain

	// Versions from the selected combo.
	cKotlin      string // e.g. "2.1.0"
	cCompose     string // androidx.compose runtime/ui version
	cBuildTools  string // e.g. "35.0.0"
	cPlatformAPI string // e.g. "35"
	jdkMin       int    // JDK window this combo's build can use
	jdkMax       int
}

// resolveToolchain locates all required tools for combo c, first checking
// ANDROID_HOME and PATH, then falling back to the sngl toolchain cache.
func resolveToolchain(c androidtc.Combo) (*toolchain, error) {
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".cache", "sngl", "android-toolchain")

	tc := &toolchain{
		CacheDir:     cacheDir,
		cKotlin:      c.Kotlin,
		cCompose:     c.ComposeRuntime,
		cBuildTools:  c.BuildTools,
		cPlatformAPI: strconv.Itoa(c.CompileSdk),
		jdkMin:       c.JDKMin,
		jdkMax:       c.JDKMax,
	}

	if err := tc.findAndroidJar(); err != nil {
		return nil, err
	}
	if err := tc.findBuildTools(); err != nil {
		return nil, err
	}
	if err := tc.findKotlinc(); err != nil {
		return nil, err
	}
	if err := tc.findComposePlugin(); err != nil {
		return nil, err
	}
	if err := tc.findComposeJars(); err != nil {
		return nil, err
	}

	return tc, nil
}

func (tc *toolchain) findAndroidJar() error {
	home := sdkRoot()
	if home != "" {
		exact := filepath.Join(home, "platforms", "android-"+tc.cPlatformAPI, "android.jar")
		if fileExists(exact) {
			tc.AndroidJar = exact
			return nil
		}
		entries, _ := os.ReadDir(filepath.Join(home, "platforms"))
		for i := len(entries) - 1; i >= 0; i-- {
			jar := filepath.Join(home, "platforms", entries[i].Name(), "android.jar")
			if fileExists(jar) {
				tc.AndroidJar = jar
				return nil
			}
		}
	}
	cached := filepath.Join(tc.CacheDir, "platforms", "android-"+tc.cPlatformAPI, "android.jar")
	if fileExists(cached) {
		tc.AndroidJar = cached
		return nil
	}
	return fmt.Errorf("android.jar not found; install Android SDK platform %s or set ANDROID_HOME", tc.cPlatformAPI)
}

func (tc *toolchain) findBuildTools() error {
	home := sdkRoot()
	if home != "" {
		btDir := filepath.Join(home, "build-tools")
		entries, _ := os.ReadDir(btDir)
		for _, ver := range []string{tc.cBuildTools} {
			dir := filepath.Join(btDir, ver)
			if dirExists(dir) {
				return tc.setBuildTools(dir)
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			dir := filepath.Join(btDir, entries[i].Name())
			if dirExists(dir) {
				return tc.setBuildTools(dir)
			}
		}
	}
	cached := filepath.Join(tc.CacheDir, "build-tools", tc.cBuildTools)
	if dirExists(cached) {
		return tc.setBuildTools(cached)
	}
	return fmt.Errorf("Android build-tools not found; install build-tools %s or set ANDROID_HOME", tc.cBuildTools)
}

func (tc *toolchain) setBuildTools(dir string) error {
	tc.Aapt2 = filepath.Join(dir, "aapt2")
	tc.Zipalign = filepath.Join(dir, "zipalign")

	// Prefer the d8 wrapper script over the jar (the jar may lack a main manifest)
	d8Script := filepath.Join(dir, "d8")
	if fileExists(d8Script) {
		tc.D8 = d8Script
	} else if fileExists(filepath.Join(dir, "lib", "d8.jar")) {
		tc.D8 = filepath.Join(dir, "lib", "d8.jar")
	} else if fileExists(filepath.Join(dir, "d8.jar")) {
		tc.D8 = filepath.Join(dir, "d8.jar")
	}
	return nil
}

func (tc *toolchain) findKotlinc() error {
	if p, err := exec.LookPath("kotlinc"); err == nil {
		tc.Kotlinc = p
		kotlinHome := filepath.Dir(filepath.Dir(p))
		// Check multiple lib locations — distro packages may use /usr/share/kotlin/lib/
		for _, libDir := range []string{
			filepath.Join(kotlinHome, "lib"),
			filepath.Join(kotlinHome, "share", "kotlin", "lib"),
		} {
			if jars := findJars(libDir, "kotlin-stdlib"); len(jars) > 0 {
				tc.KotlinLibs = jars
				break
			}
		}
		return nil
	}

	cachedBin := filepath.Join(tc.CacheDir, "kotlin-"+tc.cKotlin, "bin", "kotlinc")
	if fileExists(cachedBin) {
		tc.Kotlinc = cachedBin
		tc.KotlinLibs = findJars(filepath.Join(tc.CacheDir, "kotlin-"+tc.cKotlin, "lib"), "kotlin-stdlib")
		return nil
	}

	fmt.Fprintf(os.Stderr, "sngl: downloading Kotlin %s...\n", tc.cKotlin)
	if err := tc.downloadKotlin(); err != nil {
		return fmt.Errorf("downloading kotlinc: %w", err)
	}
	tc.Kotlinc = cachedBin
	tc.KotlinLibs = findJars(filepath.Join(tc.CacheDir, "kotlin-"+tc.cKotlin, "lib"), "kotlin-stdlib")
	return nil
}

func (tc *toolchain) downloadKotlin() error {
	url := fmt.Sprintf("https://github.com/JetBrains/kotlin/releases/download/v%s/kotlin-compiler-%s.zip", tc.cKotlin, tc.cKotlin)
	zipPath := filepath.Join(tc.CacheDir, "kotlin-compiler.zip")
	if err := downloadFile(url, zipPath); err != nil {
		return err
	}
	defer os.Remove(zipPath)
	destDir := filepath.Join(tc.CacheDir, "kotlin-"+tc.cKotlin)
	return extractZip(zipPath, destDir, "kotlinc/")
}

func (tc *toolchain) findComposePlugin() error {
	// Kotlin 2.x ships compose-compiler-plugin.jar in its lib/ directory.
	// Use the bundled version to avoid version mismatches.
	// Check multiple possible locations since package managers put files in
	// different places (e.g., /usr/share/kotlin/lib/ on some distros).
	kotlinHome := filepath.Dir(filepath.Dir(tc.Kotlinc))
	candidates := []string{
		filepath.Join(kotlinHome, "lib", "compose-compiler-plugin.jar"),
		filepath.Join(kotlinHome, "share", "kotlin", "lib", "compose-compiler-plugin.jar"),
	}
	// Also check the directory containing the kotlin stdlib jars
	if len(tc.KotlinLibs) > 0 {
		candidates = append(candidates, filepath.Join(filepath.Dir(tc.KotlinLibs[0]), "compose-compiler-plugin.jar"))
	}
	for _, bundled := range candidates {
		if fileExists(bundled) {
			tc.ComposePlugin = bundled
			return nil
		}
	}

	// Fall back to downloading from Maven Central
	cached := filepath.Join(tc.CacheDir, "compose-plugin",
		fmt.Sprintf("kotlin-compose-compiler-plugin-embeddable-%s.jar", tc.cKotlin))
	if fileExists(cached) {
		tc.ComposePlugin = cached
		return nil
	}

	fmt.Fprintf(os.Stderr, "sngl: downloading Compose compiler plugin...\n")
	url := fmt.Sprintf(
		"https://repo1.maven.org/maven2/org/jetbrains/kotlin/kotlin-compose-compiler-plugin-embeddable/%s/kotlin-compose-compiler-plugin-embeddable-%s.jar",
		tc.cKotlin, tc.cKotlin)
	os.MkdirAll(filepath.Dir(cached), 0o755)
	if err := downloadFile(url, cached); err != nil {
		return fmt.Errorf("downloading Compose plugin: %w", err)
	}
	tc.ComposePlugin = cached
	return nil
}

func (tc *toolchain) findComposeJars() error {
	cacheDir := filepath.Join(tc.CacheDir, "compose-libs")
	os.MkdirAll(cacheDir, 0o755)

	// Seed from Gradle cache if available — captures the transitive
	// dependency tree from a prior Gradle build.
	extractFromGradleCache(cacheDir)
	tc.ensureAARs(cacheDir)

	// Always run the explicit artifact download list to fill gaps. Each
	// download is a no-op if the JAR/AAR is already cached.
	if err := tc.downloadComposeArtifacts(cacheDir); err != nil {
		return err
	}

	tc.ComposeJars = findJars(cacheDir, "")
	if len(tc.ComposeJars) == 0 {
		return fmt.Errorf("no Compose libraries found after download")
	}
	return nil
}

func (tc *toolchain) downloadComposeArtifacts(cacheDir string) error {

	// Compose KMP artifacts use "-android" suffix for the Android variant.
	// The base artifact (e.g. "runtime") is a near-empty stub; the real
	// classes live in "runtime-android".
	type artifact struct {
		group, name, version string
		android              bool // true = append "-android" to artifact name for AAR
		jar                  bool // true = download as .jar directly (no AAR extraction)
	}

	arts := []artifact{
		// Compose runtime & UI (KMP — need -android suffix)
		{"androidx.compose.runtime", "runtime", tc.cCompose, true, false},
		{"androidx.compose.runtime", "runtime-saveable", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui-geometry", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui-graphics", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui-text", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui-unit", tc.cCompose, true, false},
		{"androidx.compose.ui", "ui-util", tc.cCompose, true, false},
		{"androidx.compose.foundation", "foundation", tc.cCompose, true, false},
		{"androidx.compose.foundation", "foundation-layout", tc.cCompose, true, false},
		{"androidx.compose.animation", "animation", tc.cCompose, true, false},
		{"androidx.compose.animation", "animation-core", tc.cCompose, true, false},
		{"androidx.compose.material", "material-ripple", tc.cCompose, true, false},
		{"androidx.compose.material3", "material3", "1.3.1", true, false},
		// AndroidX (not KMP — no -android suffix, but still AAR)
		{"androidx.activity", "activity-compose", "1.9.3", false, false},
		{"androidx.activity", "activity-ktx", "1.9.3", false, false},
		{"androidx.activity", "activity", "1.9.3", false, false},
		{"androidx.core", "core", "1.15.0", false, false},
		{"androidx.core", "core-ktx", "1.15.0", false, false},
		{"androidx.lifecycle", "lifecycle-common-jvm", "2.8.7", false, true},
		{"androidx.lifecycle", "lifecycle-runtime", "2.8.7", true, false},
		{"androidx.lifecycle", "lifecycle-runtime-ktx", "2.8.7", true, false},
		{"androidx.lifecycle", "lifecycle-runtime-compose", "2.8.7", true, false},
		{"androidx.lifecycle", "lifecycle-viewmodel", "2.8.7", true, false},
		{"androidx.lifecycle", "lifecycle-viewmodel-compose", "2.8.7", true, false},
		{"androidx.lifecycle", "lifecycle-viewmodel-savedstate", "2.8.7", false, false},
		{"androidx.savedstate", "savedstate", "1.2.1", false, false},
		{"androidx.savedstate", "savedstate-ktx", "1.2.1", false, false},
		{"androidx.startup", "startup-runtime", "1.2.0", false, false},
		{"androidx.emoji2", "emoji2", "1.4.0", false, false},
		{"androidx.emoji2", "emoji2-views-helper", "1.4.0", false, false},
		{"androidx.tracing", "tracing", "1.2.0", false, false},
		{"androidx.tracing", "tracing-ktx", "1.2.0", false, false},
		{"androidx.versionedparcelable", "versionedparcelable", "1.2.0", false, false},
		{"androidx.profileinstaller", "profileinstaller", "1.4.1", false, false},
		{"androidx.customview", "customview-poolingcontainer", "1.0.0", false, false},
		{"androidx.annotation", "annotation", "1.9.1", false, true},
		{"androidx.annotation", "annotation-jvm", "1.9.1", false, true},
		{"androidx.collection", "collection-jvm", "1.4.5", false, true},
		{"androidx.arch.core", "core-common", "2.2.0", false, true},
		{"androidx.arch.core", "core-runtime", "2.2.0", false, false},
		// Kotlinx
		{"org.jetbrains.kotlinx", "kotlinx-coroutines-core-jvm", "1.9.0", false, true},
		{"org.jetbrains.kotlinx", "kotlinx-coroutines-android", "1.9.0", false, true},
	}

	for _, a := range arts {
		groupPath := strings.ReplaceAll(a.group, ".", "/")

		// Determine actual artifact name (with -android suffix for KMP)
		dlName := a.name
		if a.android {
			dlName = a.name + "-android"
		}

		var jarDest string
		if a.jar {
			// Download JAR directly
			url := fmt.Sprintf("https://dl.google.com/dl/android/maven2/%s/%s/%s/%s-%s.jar",
				groupPath, dlName, a.version, dlName, a.version)
			jarDest = filepath.Join(cacheDir, fmt.Sprintf("%s-%s.jar", dlName, a.version))
			if fileExists(jarDest) {
				continue
			}
			if err := downloadFile(url, jarDest); err != nil {
				// Try Maven Central
				centralURL := fmt.Sprintf("https://repo1.maven.org/maven2/%s/%s/%s/%s-%s.jar",
					groupPath, dlName, a.version, dlName, a.version)
				if err2 := downloadFile(centralURL, jarDest); err2 != nil {
					fmt.Fprintf(os.Stderr, "sngl: warning: failed to download %s: %v\n", dlName, err)
				}
			}
		} else {
			// Download AAR, extract classes.jar
			aarDest := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.aar", dlName, a.version))
			jarDest = filepath.Join(cacheDir, fmt.Sprintf("%s-%s.jar", dlName, a.version))
			if fileExists(jarDest) {
				continue
			}
			url := fmt.Sprintf("https://dl.google.com/dl/android/maven2/%s/%s/%s/%s-%s.aar",
				groupPath, dlName, a.version, dlName, a.version)
			if err := downloadFile(url, aarDest); err != nil {
				centralURL := fmt.Sprintf("https://repo1.maven.org/maven2/%s/%s/%s/%s-%s.aar",
					groupPath, dlName, a.version, dlName, a.version)
				if err2 := downloadFile(centralURL, aarDest); err2 != nil {
					fmt.Fprintf(os.Stderr, "sngl: warning: failed to download %s: %v\n", dlName, err)
					continue
				}
			}
			if err := extractClassesJar(aarDest, jarDest); err != nil {
				fmt.Fprintf(os.Stderr, "sngl: warning: failed to extract classes.jar from %s: %v\n", dlName, err)
			}
		}
	}

	return nil
}

// extractFromGradleCache walks the Gradle module cache and extracts
// classes.jar from all AndroidX/Compose/Kotlinx AARs, copying JARs directly.
// Returns the list of extracted JAR paths, or nil if the cache isn't available.
func extractFromGradleCache(destDir string) []string {
	home, _ := os.UserHomeDir()
	cacheBase := filepath.Join(home, ".gradle", "caches", "modules-2", "files-2.1")
	if !dirExists(cacheBase) {
		return nil
	}

	os.MkdirAll(destDir, 0o755)
	// Only extract AndroidX and kotlinx runtime libraries — NOT the kotlin
	// compiler itself (kotlin-stdlib comes from the kotlinc distribution).
	prefixes := []string{"androidx.", "org.jetbrains.kotlinx"}

	var jars []string
	entries, _ := os.ReadDir(cacheBase)
	for _, groupEntry := range entries {
		groupName := groupEntry.Name()
		match := false
		for _, p := range prefixes {
			if strings.HasPrefix(groupName, p) {
				match = true
				break
			}
		}
		if !match {
			continue
		}

		groupDir := filepath.Join(cacheBase, groupName)
		artEntries, _ := os.ReadDir(groupDir)
		for _, artEntry := range artEntries {
			artDir := filepath.Join(groupDir, artEntry.Name())
			// Find highest version
			verEntries, _ := os.ReadDir(artDir)
			if len(verEntries) == 0 {
				continue
			}
			verDir := filepath.Join(artDir, verEntries[len(verEntries)-1].Name())
			// Find the AAR or JAR in hash subdirectories
			filepath.Walk(verDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				base := info.Name()
				destName := artEntry.Name() + "-" + verEntries[len(verEntries)-1].Name()

				if strings.HasSuffix(base, ".aar") {
					jarDest := filepath.Join(destDir, destName+".jar")
					if !fileExists(jarDest) {
						if err := extractClassesJar(path, jarDest); err == nil {
							jars = append(jars, jarDest)
						}
					} else {
						jars = append(jars, jarDest)
					}
				} else if strings.HasSuffix(base, ".jar") && !strings.Contains(base, "sources") && !strings.Contains(base, "javadoc") {
					jarDest := filepath.Join(destDir, destName+".jar")
					if !fileExists(jarDest) {
						copyFile(path, jarDest)
					}
					jars = append(jars, jarDest)
				}
				return nil
			})
		}
	}

	if len(jars) > 0 {
		fmt.Fprintf(os.Stderr, "sngl: extracted %d libraries from Gradle cache\n", len(jars))
	}
	return jars
}

// ensureAARs downloads AARs for libraries that need resource extraction.
// Only downloads AARs that contain string/id resources needed at runtime.
func (tc *toolchain) ensureAARs(cacheDir string) {
	// Key libraries that have runtime resources (strings, ids)
	resourceAARs := []struct{ group, name, version string }{
		{"androidx.compose.material3", "material3-android", "1.3.1"},
		{"androidx.compose.material", "material-android", tc.cCompose},
		{"androidx.compose.ui", "ui-android", tc.cCompose},
		{"androidx.compose.foundation", "foundation-android", tc.cCompose},
		{"androidx.customview", "customview-poolingcontainer", "1.0.0"},
		{"androidx.core", "core", "1.15.0"},
	}

	for _, a := range resourceAARs {
		aarDest := filepath.Join(cacheDir, a.name+"-"+a.version+".aar")
		if fileExists(aarDest) {
			continue
		}
		groupPath := strings.ReplaceAll(a.group, ".", "/")
		url := fmt.Sprintf("https://dl.google.com/dl/android/maven2/%s/%s/%s/%s-%s.aar",
			groupPath, a.name, a.version, a.name, a.version)
		downloadFile(url, aarDest)
	}
}

// classpath returns the full classpath string for kotlinc compilation.
func (tc *toolchain) classpath() string {
	var parts []string
	parts = append(parts, tc.AndroidJar)
	parts = append(parts, tc.ComposeJars...)
	parts = append(parts, tc.KotlinLibs...)
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	return strings.Join(parts, sep)
}

// Helper functions

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func findJars(dir string, prefix string) []string {
	var jars []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jar") {
			continue
		}
		// Skip sources and javadoc JARs
		if strings.Contains(name, "-sources") || strings.Contains(name, "-javadoc") {
			continue
		}
		if prefix == "" || strings.HasPrefix(name, prefix) {
			jars = append(jars, filepath.Join(dir, name))
		}
	}
	return jars
}

func downloadFile(url, dest string) error {
	os.MkdirAll(filepath.Dir(dest), 0o755)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func extractZip(zipPath, destDir, stripPrefix string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		name := f.Name
		if stripPrefix != "" {
			if !strings.HasPrefix(name, stripPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, stripPrefix)
		}
		if name == "" {
			continue
		}

		target := filepath.Join(destDir, name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0o755)
			continue
		}

		os.MkdirAll(filepath.Dir(target), 0o755)
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractClassesJar(aarPath, destJar string) error {
	r, err := zip.OpenReader(aarPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "classes.jar" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.Create(destJar)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("classes.jar not found in %s", aarPath)
}
