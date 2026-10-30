package android

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"duckfam.us/sngl/internal/androidtc"
)

// The native kotlinc and d8 (androidtc.NativeAsset) replace the JVM ones
// where this host has a build: kotlinc compiles 3-6x faster, a cold pre-dex
// of the libraries 6x, and with both native the gradle-free build runs no
// JVM at all. SNGL_ANDROID_TOOLS=jvm keeps the JVM tools, which is how an
// unpublished combo builds anyway.

// nativeKotlincFlags are the flags the native kotlinc is run with, beside the
// plugins: the image has no jrt:/ filesystem (android.jar supplies java.*),
// no scripting plugin, and no class file of its own to find its home or its
// META-INF/extensions from. sngl-lang/android-tools' kotlinc/build.sh says
// why each is there.
func nativeKotlincFlags(dir string) []string {
	return []string{
		"-no-jdk",
		"-Xdisable-default-scripting-plugin",
		"-kotlin-home", filepath.Join(dir, "home"),
		"-Xintellij-plugin-root=" + filepath.Join(dir, "root"),
	}
}

// useNative reports whether the native tools are wanted at all.
func useNative() bool { return os.Getenv("SNGL_ANDROID_TOOLS") != "jvm" }

// findNativeKotlinc sets the toolchain's kotlinc, its plugins and the stdlib
// from the native build of this combo's Kotlin, reporting false where there
// is none for this host.
func (tc *toolchain) findNativeKotlinc() (bool, error) {
	dir, ok, err := tc.fetchNative("kotlinc", tc.cKotlin)
	if !ok || err != nil {
		return false, err
	}
	tc.Kotlinc = filepath.Join(dir, "bin", "kotlinc"+exeSuffix())
	tc.KotlinLibs = []string{filepath.Join(dir, "lib", "kotlin-stdlib.jar")}
	tc.ComposePlugin = filepath.Join(dir, "plugins", "compose-compiler-plugin.jar")
	tc.SerializationPlugin = filepath.Join(dir, "plugins", "kotlin-serialization-compiler-plugin.jar")
	tc.kotlincFlags = nativeKotlincFlags(dir)
	return true, nil
}

// findNativeD8 sets the toolchain's d8 to the native build of this combo's
// R8, reporting false where there is none for this host.
func (tc *toolchain) findNativeD8() (bool, error) {
	dir, ok, err := tc.fetchNative("d8", tc.cR8)
	if !ok || err != nil {
		return false, err
	}
	tc.D8 = filepath.Join(dir, "bin", "d8"+exeSuffix())
	tc.nativeD8 = true
	return true, nil
}

// fetchNative is the directory holding tool at version for this host,
// downloaded and unpacked on first use. ok is false where no build is
// published for it.
func (tc *toolchain) fetchNative(tool, version string) (dir string, ok bool, err error) {
	if !useNative() {
		return "", false, nil
	}
	url, sum, ok := androidtc.NativeAsset(tool, version, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", false, nil
	}
	dir = filepath.Join(tc.CacheDir, "native", androidtc.NativeRelease, tool+"-"+version)
	if dirExists(dir) {
		return dir, true, nil
	}
	fmt.Fprintf(os.Stderr, "sngl: downloading native %s %s...\n", tool, version)
	if err := fetchArchive(url, sum, dir); err != nil {
		return "", false, fmt.Errorf("native %s %s: %w", tool, version, err)
	}
	return dir, true, nil
}

// fetchArchive downloads the archive at url, checks it against sum, and
// unpacks it into dest. The archive and the directory it unpacks into are
// both named for this build alone and the directory is renamed into place,
// so neither an interrupted build nor another unpacking the same archive at
// once leaves a directory a later build reads as complete.
func fetchArchive(url, sum, dest string) error {
	return fetchAndUnpack(url, dest, func(archive string) error {
		return checkSum(archive, sum)
	}, func(archive, staging string) error {
		if strings.HasSuffix(url, ".zip") {
			return extractZip(archive, staging, "")
		}
		return extractTarGz(archive, staging)
	})
}

// fetchAndUnpack downloads url, checks it, and unpacks it into dest through
// a staging directory of this build's own. A dest another build put in place
// first is kept.
func fetchAndUnpack(url, dest string, check func(archive string) error, unpack func(archive, staging string) error) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, filepath.Base(dest)+".*.download")
	if err != nil {
		return err
	}
	archive := f.Name()
	f.Close()
	defer os.Remove(archive)
	if err := downloadFile(url, archive); err != nil {
		return err
	}
	if err := check(archive); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, filepath.Base(dest)+".*.part")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := unpack(archive, staging); err != nil {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		if dirExists(dest) {
			return nil
		}
		return err
	}
	return nil
}

func checkSum(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// extractTarGz unpacks a gzipped tar into dest, keeping each file's mode, so
// the binaries stay executable.
func extractTarGz(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return fmt.Errorf("archive entry %q leaves its directory", h.Name)
		}
		target := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode).Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
		}
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
