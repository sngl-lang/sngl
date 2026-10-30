package androidtc

import "fmt"

// The native builds of the JVM tools in an Android build, published by
// github.com/sngl-lang/android-tools: kotlinc (with the compose and
// kotlinx.serialization plugins compiled in) and d8, each a GraalVM native
// image, so the gradle-free build needs no JDK. One release holds every tool
// for every host; its SHA256SUMS is copied into nativeSums, and an asset not
// listed there is not used.

// NativeRelease is the android-tools release sngl downloads from.
const NativeRelease = "v1"

// nativeBase is where NativeRelease's assets are downloaded from; a test
// points it at a local server.
var nativeBase = "https://github.com/sngl-lang/android-tools/releases/download/" + NativeRelease + "/"

// nativeSums is NativeRelease's SHA256SUMS.
var nativeSums = map[string]string{
	"d8-8.7.18-darwin-amd64.tar.gz":     "aac688f960832ed8c8cea505c21dbf9d03a3b1ae22206d6d1edd4a4f8e95dbea",
	"d8-8.7.18-darwin-arm64.tar.gz":     "622a494dc4a02359a5729c89fa165ee7907ae317ffa19185c32b38f8d84f2d50",
	"d8-8.7.18-linux-amd64.tar.gz":      "69da7b579e5482d2e908e12477bb475fe05f6b2edcbdc4e905fc01643ca5cc47",
	"d8-8.7.18-linux-arm64.tar.gz":      "a3be2d7b6a35a688e4a84d7019582a29bbe2ab7b66cab79bdc6461af0e2d1ff4",
	"d8-8.7.18-windows-amd64.zip":       "7ffe027652b1360f6dfda2a3fc35f45dcb23b433dde7fd6e7e82670505e6fb9f",
	"kotlinc-2.1.0-darwin-amd64.tar.gz": "3e2f0e1febcfea38c970a958ffd598a035c23aa15e5488a700944cfb0d098f00",
	"kotlinc-2.1.0-darwin-arm64.tar.gz": "b6eb8ab1e09fe0970912e9c41adc07ef928c1a5bfe0cd5b05470e37fd39b5639",
	"kotlinc-2.1.0-linux-amd64.tar.gz":  "55e0a57843f0de36db05e1992ec1dcf522a1168f860234b4ec6afea677e60b64",
	"kotlinc-2.1.0-linux-arm64.tar.gz":  "42fa1e8a76c30f9e549f15457f53b89d8c92dd70403f557075fa43dd628c0126",
	"kotlinc-2.1.0-windows-amd64.zip":   "9003add22804e036542fe1a2d5854b31a21f74b5f7b390ee21428720b75f4d58",
}

// NativeAsset is the release asset of tool ("kotlinc" or "d8") at version for
// a host, its URL and SHA-256, or ok false where none is published.
func NativeAsset(tool, version, goos, goarch string) (url, sha256 string, ok bool) {
	if version == "" {
		return "", "", false
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	name := fmt.Sprintf("%s-%s-%s-%s%s", tool, version, goos, goarch, ext)
	sum, ok := nativeSums[name]
	if !ok {
		return "", "", false
	}
	return nativeBase + name, sum, true
}
