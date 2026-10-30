package apksign

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// testAPK is a zip with stored entries of assorted name lengths, a native
// library and a deflated entry, written the way addFilesToZip writes one:
// with data descriptors.
func testAPK(t *testing.T) ([]byte, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{
		"AndroidManifest.xml":    []byte("manifest"),
		"resources.arsc":         bytes.Repeat([]byte{1, 2, 3}, 1000),
		"res/a.png":              []byte("png"),
		"lib/arm64-v8a/libx.so":  bytes.Repeat([]byte{7}, 5000),
		"classes.dex":            bytes.Repeat([]byte("dex"), 300000), // over a chunk
		"assets/odd-name-length": []byte("x"),
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		method := zip.Store
		if name == "classes.dex" {
			method = zip.Deflate
		}
		fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(files[name])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), files
}

func testKey(t *testing.T) *Key {
	t.Helper()
	k, err := DebugKey(filepath.Join(t.TempDir(), "debug.pem"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSignKeepsEntriesAndAlignsStoredOnes(t *testing.T) {
	apk, files := testAPK(t)
	signed, err := Sign(apk, testKey(t), 24)
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.File) != len(files) {
		t.Fatalf("got %d entries, want %d", len(r.File), len(files))
	}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if _, err := got.ReadFrom(rc); err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if !bytes.Equal(got.Bytes(), files[f.Name]) {
			t.Errorf("%s: contents changed", f.Name)
		}
		if f.Method != zip.Store {
			continue
		}
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		want := int64(4)
		if strings.HasSuffix(f.Name, ".so") {
			want = 4096
		}
		if off%want != 0 {
			t.Errorf("%s: data at %d, not %d-aligned", f.Name, off, want)
		}
	}
}

// TestSignatureVerifies checks the v2 block the way a device does: the block
// sits right before the central directory the EOCD names, its digest is over
// the archive with the EOCD naming the block, and the signature over the
// signed data verifies with the certificate's key.
func TestSignatureVerifies(t *testing.T) {
	apk, _ := testAPK(t)
	k := testKey(t)
	signed, err := Sign(apk, k, 24)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	eocdOff := len(signed) - eocdLen
	if le.Uint32(signed[eocdOff:]) != eocdSig {
		t.Fatal("no EOCD at the end")
	}
	cdOff := int(le.Uint32(signed[eocdOff+16:]))
	if string(signed[cdOff-16:cdOff]) != blockMagic {
		t.Fatal("no signing block before the central directory")
	}
	size := int(le.Uint64(signed[cdOff-24:]))
	blockOff := cdOff - size - 8
	if int(le.Uint64(signed[blockOff:])) != size {
		t.Fatal("the block's two sizes differ")
	}

	pairs := signed[blockOff+8 : cdOff-24]
	var v2 []byte
	for len(pairs) > 0 {
		n := int(le.Uint64(pairs))
		if le.Uint32(pairs[8:]) == v2BlockID {
			v2 = pairs[12 : 8+n]
		}
		pairs = pairs[8+n:]
	}
	if v2 == nil {
		t.Fatal("no v2 block")
	}

	// take reads one length-prefixed field.
	take := func(b *[]byte) []byte {
		n := le.Uint32(*b)
		v := (*b)[4 : 4+n]
		*b = (*b)[4+n:]
		return v
	}
	signers := take(&v2)
	signer := take(&signers)
	signedData := take(&signer)
	sigs := take(&signer)
	sig := take(&sigs)
	if le.Uint32(sig) != rsaPKCS1SHA256 {
		t.Fatalf("signature algorithm %#x", le.Uint32(sig))
	}
	sig = sig[4:]
	sigBytes := take(&sig)
	hashed := sha256.Sum256(signedData)
	if err := rsa.VerifyPKCS1v15(&k.Private.PublicKey, crypto.SHA256, hashed[:], sigBytes); err != nil {
		t.Fatalf("signature: %v", err)
	}

	sd := signedData
	digests := take(&sd)
	certs := take(&sd)
	if cert := take(&certs); !bytes.Equal(cert, k.Cert.Raw) {
		t.Error("the certificate is not the key's")
	}
	d := take(&digests)
	d = d[4:]
	digest := take(&d)

	eocd := slices.Clone(signed[eocdOff:])
	le.PutUint32(eocd[16:], uint32(blockOff))
	want := contentDigest(signed[:blockOff], signed[cdOff:eocdOff], eocd)
	if !bytes.Equal(digest, want) {
		t.Error("content digest does not match the archive")
	}
}

func TestDebugKeyIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k", "debug.pem")
	a, err := DebugKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DebugKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Cert.Raw, b.Cert.Raw) || !a.Private.Equal(b.Private) {
		t.Error("a second DebugKey made a new key")
	}
}

var (
	update = flag.Bool("update", false, "run apksigner where a signed APK moved, and rewrite its testdata/apksigner_runs record")
	verify = flag.Bool("verify", false, "run apksigner on every signed APK, whatever its testdata/apksigner_runs record says")
)

// TestApksignerVerifies holds the output to the SDK's own verifier: v2 alone
// from API 24, and v1 beside it below. apksigner runs only where the APK
// moved -- it is a JVM tool, from an SDK CI does not have -- and each run that
// passed is recorded in testdata/apksigner_runs/<test> as a digest of the APK
// and apksigner's arguments. Signing is deterministic for a fixed key, so
// the digest moves only when this package's output does.
func TestApksignerVerifies(t *testing.T) {
	key, err := DebugKey(filepath.Join("testdata", "test-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		minSdk int
		v1     bool
	}{{24, false}, {21, true}} {
		t.Run(strconv.Itoa(tc.minSdk), func(t *testing.T) {
			apk, _ := testAPK(t)
			signed, err := Sign(apk, key, tc.minSdk)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"verify", "--verbose", "--min-sdk-version", strconv.Itoa(tc.minSdk)}
			h := sha256.New()
			for _, a := range args {
				fmt.Fprintf(h, "%d\x00%s", len(a), a)
			}
			h.Write(signed)
			want := "sha256:" + hex.EncodeToString(h.Sum(nil)) + "\n"

			record := filepath.Join("testdata", "apksigner_runs", strings.ReplaceAll(t.Name(), "/", "_"))
			have, _ := os.ReadFile(record)
			switch {
			case string(have) == want && !*verify:
				return
			case !*update && !*verify && have == nil:
				t.Fatalf("%s: no record of apksigner verifying this APK; rerun with -update where an Android SDK is installed", record)
			case !*update && !*verify:
				t.Fatalf("%s: the signed APK changed since apksigner verified it; rerun with -update -- `go tool verify -update` rerecords every package's", record)
			}

			apksigner := findApksigner()
			if _, err := exec.LookPath("java"); err != nil || apksigner == "" {
				t.Fatalf("%s: cannot record: apksigner needs java and an Android SDK's build-tools", record)
			}
			path := filepath.Join(t.TempDir(), "app.apk")
			if err := os.WriteFile(path, signed, 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(apksigner, append(args, path)...).CombinedOutput()
			if err != nil {
				t.Fatalf("apksigner verify: %v\n%s", err, out)
			}
			for scheme, want := range map[string]bool{
				"Verified using v1 scheme (JAR signing): ":             tc.v1,
				"Verified using v2 scheme (APK Signature Scheme v2): ": true,
			} {
				if !strings.Contains(string(out), scheme+strconv.FormatBool(want)) {
					t.Fatalf("apksigner did not report %s%t:\n%s", scheme, want, out)
				}
			}
			if *update {
				if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestV1BelowItsFloor refuses a minSdk no SHA-256 JAR signature installs on.
func TestV1BelowItsFloor(t *testing.T) {
	apk, _ := testAPK(t)
	if _, err := Sign(apk, testKey(t), V1MinSdk-1); err == nil {
		t.Errorf("Sign accepted minSdk %d", V1MinSdk-1)
	}
}

// TestManifestLineWraps holds a long entry name to the 72-byte line limit.
func TestManifestLineWraps(t *testing.T) {
	got := manifestLine("Name", strings.Repeat("x", 150))
	for line := range strings.SplitSeq(strings.TrimSuffix(got, "\r\n"), "\r\n") {
		if len(line) > 72 {
			t.Errorf("line of %d bytes: %q", len(line), line)
		}
	}
	unwrapped := strings.ReplaceAll(got, "\r\n ", "")
	if unwrapped != "Name: "+strings.Repeat("x", 150)+"\r\n" {
		t.Errorf("wrapping lost bytes: %q", got)
	}
}

func findApksigner() string {
	var roots []string
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Android", "Sdk"))
	}
	for _, root := range roots {
		matches, _ := filepath.Glob(filepath.Join(root, "build-tools", "*", "apksigner"))
		if len(matches) > 0 {
			return matches[len(matches)-1]
		}
	}
	return ""
}

// TestSignerCertsReadsTheSignature reads back the certificate Sign wrote,
// which is how `sngl run` tells a debug build it may replace from a release
// it must not.
func TestSignerCertsReadsTheSignature(t *testing.T) {
	apk, _ := testAPK(t)
	k := testKey(t)
	signed, err := Sign(apk, k, 24)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := SignerCerts(signed)
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 || !bytes.Equal(certs[0].Raw, k.Cert.Raw) {
		t.Fatalf("SignerCerts = %d certificates, want the signing one", len(certs))
	}
	if !IsDebug(certs) {
		t.Error("the debug key's certificate is not a debug one")
	}
	if _, err := SignerCerts(apk); err == nil {
		t.Error("an unsigned APK reported certificates")
	}
}
