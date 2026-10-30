package android

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The SDK half of the toolchain, fetched the way the Kotlin compiler and the
// Compose artifacts already are.
//
// findAndroidJar and findBuildTools have always consulted a cache directory
// under CacheDir before giving up, but nothing ever wrote to it -- so a
// machine without ANDROID_HOME could download a compiler and every Compose
// AAR and still fail on `android.jar not found`, which is the one thing it
// could not get for itself.
//
// Archives are located through Google's own repository manifest rather than
// by constructing a URL: a platform's zip carries a revision in its name
// (`platform-35_r02.zip`) that nothing else predicts, and build-tools names
// its per-OS archives by a major version rather than the full one
// (`build-tools_r35_linux.zip` for 35.0.0).

const sdkRepoBase = "https://dl.google.com/android/repository/"

// sdkRepoManifests are the manifests to search, newest schema first. A package
// may be described by either.
var sdkRepoManifests = []string{"repository2-3.xml", "repository2-1.xml"}

// sdkRepo is the subset of the manifest this needs: for each remote package,
// the archives it is published as.
type sdkRepo struct {
	Packages []struct {
		Path     string `xml:"path,attr"`
		Archives struct {
			Archive []struct {
				HostOS string `xml:"host-os"`
				URL    string `xml:"complete>url"`
				SHA1   string `xml:"complete>checksum"`
			} `xml:"archive"`
		} `xml:"archives"`
	} `xml:"remotePackage"`
}

// sdkHostOS is the manifest's name for this machine.
func sdkHostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macosx"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// sdkArchiveURL resolves an sdkmanager package path ("platforms;android-35")
// to the archive for this host and the SHA-1 the manifest gives it. An
// archive with no host-os is platform-independent and matches anything. A
// linux archive holds x86-64 programs -- the manifest names no other arch --
// so another linux arch is refused here rather than failing to run them.
func sdkArchiveURL(pkgPath string) (url, sha1 string, err error) {
	host := sdkHostOS()
	var firstErr error
	for _, manifest := range sdkRepoManifests {
		resp, err := http.Get(sdkRepoBase + manifest)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetching %s: HTTP %d", manifest, resp.StatusCode)
			}
			continue
		}
		var repo sdkRepo
		if err := xml.Unmarshal(body, &repo); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("parsing %s: %w", manifest, err)
			}
			continue
		}
		for _, p := range repo.Packages {
			if p.Path != pkgPath {
				continue
			}
			for _, a := range p.Archives.Archive {
				if a.URL == "" {
					continue
				}
				if a.HostOS == "" || a.HostOS == host {
					if a.HostOS == "linux" && runtime.GOARCH != "amd64" {
						return "", "", fmt.Errorf("Google publishes %s for linux only as x86-64, and this is linux/%s: install it yourself and set ANDROID_HOME", pkgPath, runtime.GOARCH)
					}
					return sdkRepoBase + a.URL, a.SHA1, nil
				}
			}
		}
	}
	if firstErr != nil {
		return "", "", fmt.Errorf("locating %s: %w", pkgPath, firstErr)
	}
	return "", "", fmt.Errorf("%s is not published for %s", pkgPath, host)
}

// zipRoot is the single top-level directory every entry sits under, or "" when
// the archive has more than one. SDK archives are packed under a directory
// named for the release rather than the version asked for (`android-15` holds
// API 35), so the prefix to strip has to be read from the archive.
func zipRoot(zipPath string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	root := ""
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, "./")
		i := strings.Index(name, "/")
		if i < 0 {
			// A file at the archive root: there is no common directory.
			return "", nil
		}
		switch top := name[:i+1]; root {
		case "":
			root = top
		case top:
		default:
			return "", nil
		}
	}
	return root, nil
}

// fetchSDKPackage downloads one sdkmanager package and unpacks it into dest,
// stripping the archive's own root directory, after checking it against the
// SHA-1 Google's manifest gives it. The download is skipped when dest
// already exists.
func fetchSDKPackage(pkgPath, dest, label string) error {
	if dirExists(dest) {
		return nil
	}
	url, sum, err := sdkArchiveURL(pkgPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sngl: downloading %s...\n", label)
	err = fetchAndUnpack(url, dest, func(archive string) error {
		return checkSHA1(archive, sum)
	}, func(archive, staging string) error {
		root, err := zipRoot(archive)
		if err != nil {
			return err
		}
		return extractZip(archive, staging, root)
	})
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// checkSHA1 checks path against the SHA-1 Google's SDK manifest gives it.
// A manifest entry naming none is not taken on trust.
func checkSHA1(path, want string) error {
	if want == "" {
		return fmt.Errorf("the SDK manifest gives no checksum")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(want) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// downloadAndroidPlatform fetches the platform holding android.jar.
func (tc *toolchain) downloadAndroidPlatform() error {
	dest := filepath.Join(tc.CacheDir, "platforms", "android-"+tc.cPlatformAPI)
	return fetchSDKPackage(
		"platforms;android-"+tc.cPlatformAPI,
		dest,
		"Android platform "+tc.cPlatformAPI,
	)
}

// downloadBuildTools fetches aapt2 and d8.
func (tc *toolchain) downloadBuildTools() error {
	dest := filepath.Join(tc.CacheDir, "build-tools", tc.cBuildTools)
	if err := fetchSDKPackage(
		"build-tools;"+tc.cBuildTools,
		dest,
		"Android build-tools "+tc.cBuildTools,
	); err != nil {
		return err
	}
	// A zip carries a mode, but not every producer sets the execute bit --
	// and aapt2 and d8 are run as programs.
	for _, name := range []string{"aapt2", "d8"} {
		p := filepath.Join(dest, name)
		if fileExists(p) {
			os.Chmod(p, 0o755)
		}
	}
	return nil
}
