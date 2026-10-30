package android

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/internal/androidtc"
)

// toolchain holds paths to the minimal Android build tools plus the version
// numbers (from the selected androidtc.Combo) that drive tool discovery and
// artifact downloads.
type toolchain struct {
	Kotlinc       string // kotlinc binary
	ComposePlugin string // compose-compiler-plugin-embeddable.jar
	// SerializationPlugin is kotlinx.serialization's compiler plugin, which
	// a sngl:ui/nav stack's typed routes are written with.
	SerializationPlugin string
	AndroidJar          string   // android.jar
	Aapt2               string   // aapt2 binary
	D8                  string   // d8.jar or d8 binary wrapper script
	ComposeJars         []string // compose runtime + dependency JARs
	libAARs             []string // the AARs among them, for resources and R classes
	KotlinLibs          []string // kotlin-stdlib etc.
	CacheDir            string   // ~/.cache/sngl/android-toolchain

	// kotlincFlags are what the native kotlinc is run with besides the
	// plugins (nativeKotlincFlags); the JVM one takes none. nativeD8 is set
	// when D8 is the native d8.
	kotlincFlags []string
	nativeD8     bool

	// Versions from the selected combo.
	cKotlin      string          // e.g. "2.1.0"
	cName        string          // combo name
	cLibs        []androidtc.Lib // the libraries the app is built against
	cBuildTools  string          // e.g. "35.0.0"
	cR8          string          // the native d8's R8 version, "" where none is published
	cPlatformAPI string          // e.g. "35"
	jdkMin       int             // JDK window this combo's build can use
	jdkMax       int
}

// toolchainCacheDir is where the SDK packages, the native tools and the
// libraries a gradle-free build downloads are kept.
func toolchainCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "sngl", "android-toolchain")
}

// resolveToolchain locates all required tools for combo c, first checking
// ANDROID_HOME and PATH, then falling back to the sngl toolchain cache.
func resolveToolchain(c androidtc.Combo) (*toolchain, error) {
	tc := &toolchain{
		CacheDir:     toolchainCacheDir(),
		cKotlin:      c.Kotlin,
		cName:        c.Name,
		cLibs:        c.Libs(),
		cBuildTools:  c.BuildTools,
		cR8:          c.R8,
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
	if _, err := tc.findNativeD8(); err != nil {
		return nil, err
	}
	native, err := tc.findNativeKotlinc()
	if err != nil {
		return nil, err
	}
	if !native {
		if err := tc.findKotlinc(); err != nil {
			return nil, err
		}
		if err := tc.findComposePlugin(); err != nil {
			return nil, err
		}
		if err := tc.findSerializationPlugin(); err != nil {
			return nil, err
		}
	}
	if err := tc.findComposeJars(); err != nil {
		return nil, err
	}

	return tc, nil
}

// findSerializationPlugin is findComposePlugin for kotlinx.serialization: the
// Kotlin distribution ships it beside the stdlib, and Maven Central has the
// embeddable build where it does not.
func (tc *toolchain) findSerializationPlugin() error {
	kotlinHome := filepath.Dir(filepath.Dir(tc.Kotlinc))
	candidates := []string{
		filepath.Join(kotlinHome, "lib", "kotlinx-serialization-compiler-plugin.jar"),
		filepath.Join(kotlinHome, "share", "kotlin", "lib", "kotlinx-serialization-compiler-plugin.jar"),
	}
	if len(tc.KotlinLibs) > 0 {
		candidates = append(candidates, filepath.Join(filepath.Dir(tc.KotlinLibs[0]), "kotlinx-serialization-compiler-plugin.jar"))
	}
	for _, bundled := range candidates {
		if fileExists(bundled) {
			tc.SerializationPlugin = bundled
			return nil
		}
	}
	cached := filepath.Join(tc.CacheDir, "serialization-plugin",
		fmt.Sprintf("kotlin-serialization-compiler-plugin-embeddable-%s.jar", tc.cKotlin))
	if fileExists(cached) {
		tc.SerializationPlugin = cached
		return nil
	}
	fmt.Fprintf(os.Stderr, "sngl: downloading the kotlinx.serialization compiler plugin...\n")
	url := fmt.Sprintf(
		"https://repo1.maven.org/maven2/org/jetbrains/kotlin/kotlin-serialization-compiler-plugin-embeddable/%s/kotlin-serialization-compiler-plugin-embeddable-%s.jar",
		tc.cKotlin, tc.cKotlin)
	os.MkdirAll(filepath.Dir(cached), 0o755)
	if err := downloadFile(url, cached); err != nil {
		return fmt.Errorf("downloading the serialization plugin: %w", err)
	}
	tc.SerializationPlugin = cached
	return nil
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
		for _, entry := range slices.Backward(entries) {
			jar := filepath.Join(home, "platforms", entry.Name(), "android.jar")
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
	if err := tc.downloadAndroidPlatform(); err != nil {
		return fmt.Errorf("android.jar not found and could not be fetched (%w); install Android SDK platform %s or set ANDROID_HOME", err, tc.cPlatformAPI)
	}
	if !fileExists(cached) {
		return fmt.Errorf("android.jar not found in the downloaded platform %s", tc.cPlatformAPI)
	}
	tc.AndroidJar = cached
	return nil
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
		for _, entry := range slices.Backward(entries) {
			dir := filepath.Join(btDir, entry.Name())
			if dirExists(dir) {
				return tc.setBuildTools(dir)
			}
		}
	}
	cached := filepath.Join(tc.CacheDir, "build-tools", tc.cBuildTools)
	if dirExists(cached) {
		return tc.setBuildTools(cached)
	}
	if err := tc.downloadBuildTools(); err != nil {
		return fmt.Errorf("Android build-tools not found and could not be fetched (%w); install build-tools %s or set ANDROID_HOME", err, tc.cBuildTools)
	}
	return tc.setBuildTools(cached)
}

func (tc *toolchain) setBuildTools(dir string) error {
	tc.Aapt2 = filepath.Join(dir, "aapt2")

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

// libsDir holds the libraries a gradle-free build compiles and dexes against.
func (tc *toolchain) libsDir() string { return filepath.Join(tc.CacheDir, "maven-libs") }

// findComposeJars downloads the combo's libraries (androidtc.Combo.Libs) and
// sets the jars the build compiles and dexes against and the AARs whose
// resources and R classes it adds. It is exactly that list: the closure Gradle
// resolves for the scaffold, each file checked against its SHA-256, and
// nothing from ~/.gradle or from what an earlier list left in the directory.
// A hand-kept list missed lifecycle-livedata-core, and a set seeded from
// Gradle's cache varied by machine; either way the app built and then failed
// at launch on a class or an R class it did not carry.
func (tc *toolchain) findComposeJars() error {
	if len(tc.cLibs) == 0 {
		return fmt.Errorf("combo %q has no resolved libraries; run go run ./internal/androidtc/resolvelibs", tc.cName)
	}
	dir := tc.libsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tc.ComposeJars, tc.libAARs = nil, nil
	announced := false
	for _, lib := range tc.cLibs {
		// Named by module and version: several artifacts publish their file
		// under the same name (ui-release.aar).
		ext := filepath.Ext(lib.File)
		base := filepath.Join(dir, lib.Group+"."+lib.Module+"-"+lib.Version)
		dest := base + ext
		if !fileExists(dest) {
			if !announced {
				fmt.Fprintln(os.Stderr, "sngl: downloading Android libraries...")
				announced = true
			}
			if err := fetchLib(lib, dest); err != nil {
				return fmt.Errorf("%s:%s:%s: %w", lib.Group, lib.Module, lib.Version, err)
			}
		}
		if ext != ".aar" {
			tc.ComposeJars = append(tc.ComposeJars, dest)
			continue
		}
		tc.libAARs = append(tc.libAARs, dest)
		jar := base + ".jar"
		if !fileExists(jar) {
			if err := extractClassesJar(dest, jar); err != nil {
				// An AAR of resources alone has no classes.
				continue
			}
		}
		tc.ComposeJars = append(tc.ComposeJars, jar)
	}
	return nil
}

// fetchLib downloads lib to dest, checked against its SHA-256 before it is
// put in place.
func fetchLib(lib androidtc.Lib, dest string) error {
	f, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.download")
	if err != nil {
		return err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	if err := downloadFile(lib.URL, tmp); err != nil {
		return err
	}
	if err := checkSum(tmp, lib.SHA256); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// d8Key names the d8 a dex was made by, for the pre-dex cache: the native
// r8 and the build-tools d8 are different compilers, and a dex is the
// output of one.
func (tc *toolchain) d8Key() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%v\x00%s", tc.D8, tc.nativeD8, toolStamp(tc.D8))))
	return hex.EncodeToString(h[:6])
}

// toolStamp is a tool file's size and modification time: what changes when
// a package manager replaces it in place under the same path. Hashing the
// file itself would cost every build a read of a 150MB kotlinc.
func toolStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano())
}

// needsJDK reports whether a tool the build runs is still a JVM one.
func (tc *toolchain) needsJDK() bool { return tc.kotlincFlags == nil || !tc.nativeD8 }

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

// downloadClient gives up on a server that does not answer, and
// downloadFile on a body that stops arriving: neither puts a bound on a large
// download that is still moving.
var downloadClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: time.Minute,
}}

// downloadStall is how long a download may receive nothing before it fails.
var downloadStall = time.Minute

// downloadFile downloads url to dest, written beside it and renamed into
// place, so dest is either absent or whole.
func downloadFile(url, dest string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	stall := time.AfterFunc(downloadStall, cancel)
	defer stall.Stop()
	body := readerFunc(func(p []byte) (int, error) {
		n, err := resp.Body.Read(p)
		stall.Reset(downloadStall)
		return n, err
	})
	err = writeFileAtomic(dest, func(f *os.File) error {
		_, err := io.Copy(f, body)
		return err
	})
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%s: nothing received for %s", url, downloadStall)
	}
	return err
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// writeFileAtomic writes dest through write, into a file of its own beside
// dest that is renamed into place only when write succeeds: a build killed
// or failing partway, or another build writing the same file, never leaves a
// partial file that a later build would take for a whole one.
func writeFileAtomic(dest string, write func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		os.Remove(tmp)
	}
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

		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return fmt.Errorf("archive entry %q leaves its directory", f.Name)
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
		if f.Name != "classes.jar" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeFileAtomic(destJar, func(out *os.File) error {
			_, err := io.Copy(out, rc)
			return err
		})
	}
	return fmt.Errorf("classes.jar not found in %s", aarPath)
}
