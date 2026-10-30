package androidtc

import (
	"regexp"
	"strings"
	"testing"
)

// TestNativeSumsNameEveryHost holds the table to the release's own naming
// and to every host it publishes for, so a SHA256SUMS pasted in part shows.
func TestNativeSumsNameEveryHost(t *testing.T) {
	name := regexp.MustCompile(`^(kotlinc|d8)-[0-9.]+-(linux|darwin|windows)-(amd64|arm64)\.(tar\.gz|zip)$`)
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for asset, sum := range nativeSums {
		if !name.MatchString(asset) {
			t.Errorf("asset %q is not named <tool>-<version>-<os>-<arch>.<ext>", asset)
		}
		if !hex.MatchString(sum) {
			t.Errorf("asset %q has sum %q, not a SHA-256", asset, sum)
		}
	}
	hosts := [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}}
	for _, c := range combos {
		if c.R8 == "" {
			continue
		}
		for _, h := range hosts {
			for tool, version := range map[string]string{"kotlinc": c.Kotlin, "d8": c.R8} {
				if _, _, ok := NativeAsset(tool, version, h[0], h[1]); !ok {
					t.Errorf("combo %q: no %s %s for %s/%s in nativeSums", c.Name, tool, version, h[0], h[1])
				}
			}
		}
	}
}

func TestNativeAsset(t *testing.T) {
	url, _, ok := NativeAsset("d8", "8.7.18", "windows", "amd64")
	if !ok || !strings.HasSuffix(url, "/"+NativeRelease+"/d8-8.7.18-windows-amd64.zip") {
		t.Errorf("windows d8 = %q, %v", url, ok)
	}
	if _, _, ok := NativeAsset("d8", "", "linux", "amd64"); ok {
		t.Error("a combo with no R8 found a native d8")
	}
	if _, _, ok := NativeAsset("kotlinc", "0.0.0", "linux", "amd64"); ok {
		t.Error("an unpublished kotlinc was found")
	}
}
