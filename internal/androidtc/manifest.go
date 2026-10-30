package androidtc

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var minSdkAttr = regexp.MustCompile(`android:minSdkVersion="(\d+)"`)

// ManifestMinSdk is the minSdkVersion an AndroidManifest.xml declares, or 0
// where it declares none.
func ManifestMinSdk(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	m := minSdkAttr.FindSubmatch(data)
	if m == nil {
		return 0, nil
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, fmt.Errorf("%s: minSdkVersion %q: %w", path, m[1], err)
	}
	return n, nil
}
