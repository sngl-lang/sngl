package androidtc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestMinSdk(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		manifest string
		want     int
	}{
		{`<manifest><uses-sdk android:minSdkVersion="21" android:targetSdkVersion="35" /></manifest>`, 21},
		{`<manifest><application /></manifest>`, 0},
	} {
		path := filepath.Join(dir, "AndroidManifest.xml")
		if err := os.WriteFile(path, []byte(c.manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ManifestMinSdk(path)
		if err != nil || got != c.want {
			t.Errorf("ManifestMinSdk(%s) = %d, %v; want %d", c.manifest, got, err, c.want)
		}
	}
}
