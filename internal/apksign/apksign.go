// Package apksign aligns an APK and signs it with APK Signature Scheme v2,
// which is what zipalign and apksigner did for the gradle-free Android build.
// Doing it here means signing needs neither a JDK (keytool, apksigner,
// jarsigner) nor build-tools' zipalign.
//
// v2 alone is a valid signature from API 24 on; an app with a lower minSdk
// gets a v1 (JAR) signature too (v1.go), as apksigner does. The scheme is
// specified at https://source.android.com/docs/security/features/apksigning/v2.
package apksign

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
)

// V2MinSdk is the lowest API level a v2-only signature installs on; below
// it Sign adds v1.
const V2MinSdk = 24

const (
	blockMagic = "APK Sig Block 42"
	v2BlockID  = 0x7109871a
	// rsaPKCS1SHA256 is RSASSA-PKCS1-v1_5 with SHA2-256, over SHA2-256
	// chunked digests.
	rsaPKCS1SHA256 = 0x0103
	chunkSize      = 1 << 20

	localHeaderSig = 0x04034b50
	centralDirSig  = 0x02014b50
	eocdSig        = 0x06054b50
	// alignmentExtraID is the extra field zipalign and apksigner pad a local
	// header with: the alignment as a uint16, then zeros.
	alignmentExtraID = 0xd935
	dataDescriptor   = 0x8
)

// SignFile reads the unsigned APK at src and writes it aligned and signed to
// dst, for an app installed on minSdk and later.
func SignFile(src, dst string, k *Key, minSdk int) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	out, err := Sign(in, k, minSdk)
	if err != nil {
		return fmt.Errorf("signing %s: %w", src, err)
	}
	return os.WriteFile(dst, out, 0o644)
}

// Sign returns apk aligned and signed. Its entries are rewritten in order,
// each stored one with its data aligned (4 bytes; a page for a native
// library, which the platform maps in place), and any signature the input
// carried is dropped. Below V2MinSdk it is signed with v1 as well, which
// cannot go below V1MinSdk.
func Sign(apk []byte, k *Key, minSdk int) ([]byte, error) {
	if minSdk < V1MinSdk {
		return nil, fmt.Errorf("minSdk %d is below %d, the lowest a SHA-256 v1 signature installs on", minSdk, V1MinSdk)
	}
	if minSdk < V2MinSdk {
		var err error
		if apk, err = addV1(apk, k); err != nil {
			return nil, err
		}
	}
	zipped, cdOffset, err := align(apk)
	if err != nil {
		return nil, err
	}
	eocdOffset := len(zipped) - eocdLen
	entries := zipped[:cdOffset]
	centralDir := zipped[cdOffset:eocdOffset]
	eocd := zipped[eocdOffset:]

	// The digest reads the EOCD with its central directory offset naming the
	// signing block, which goes where the central directory starts now: the
	// EOCD align wrote already says so.
	digest := contentDigest(entries, centralDir, eocd)
	block, err := signingBlock(digest, k)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(zipped)+len(block))
	out = append(out, entries...)
	out = append(out, block...)
	out = append(out, centralDir...)
	out = append(out, eocd...)
	binary.LittleEndian.PutUint32(out[len(out)-eocdLen+16:], uint32(cdOffset+len(block)))
	return out, nil
}

const eocdLen = 22

// align rewrites the archive with no data descriptors, no comment and every
// stored entry aligned, returning it and its central directory's offset.
func align(apk []byte) ([]byte, int, error) {
	r, err := zip.NewReader(bytes.NewReader(apk), int64(len(apk)))
	if err != nil {
		return nil, 0, err
	}
	var out, cd bytes.Buffer
	le := binary.LittleEndian
	for _, f := range r.File {
		if f.CompressedSize64 >= 1<<32-1 || f.UncompressedSize64 >= 1<<32-1 || out.Len() >= 1<<32-1 {
			return nil, 0, errors.New("zip64 archives are not supported")
		}
		raw, err := f.OpenRaw()
		if err != nil {
			return nil, 0, err
		}
		data, err := io.ReadAll(raw)
		if err != nil {
			return nil, 0, err
		}

		offset := out.Len()
		var extra []byte
		if f.Method == zip.Store {
			n := 4
			if isNativeLib(f.Name) {
				n = 4096
			}
			start := offset + 30 + len(f.Name) + 6
			pad := (n - start%n) % n
			extra = make([]byte, 6+pad)
			le.PutUint16(extra[0:], alignmentExtraID)
			le.PutUint16(extra[2:], uint16(2+pad))
			le.PutUint16(extra[4:], uint16(n))
		}
		flags := f.Flags &^ dataDescriptor

		var h [30]byte
		le.PutUint32(h[0:], localHeaderSig)
		le.PutUint16(h[4:], f.ReaderVersion)
		le.PutUint16(h[6:], flags)
		le.PutUint16(h[8:], f.Method)
		le.PutUint16(h[10:], f.ModifiedTime)
		le.PutUint16(h[12:], f.ModifiedDate)
		le.PutUint32(h[14:], f.CRC32)
		le.PutUint32(h[18:], uint32(len(data)))
		le.PutUint32(h[22:], uint32(f.UncompressedSize64))
		le.PutUint16(h[26:], uint16(len(f.Name)))
		le.PutUint16(h[28:], uint16(len(extra)))
		out.Write(h[:])
		out.WriteString(f.Name)
		out.Write(extra)
		out.Write(data)

		var c [46]byte
		le.PutUint32(c[0:], centralDirSig)
		le.PutUint16(c[4:], f.CreatorVersion)
		le.PutUint16(c[6:], f.ReaderVersion)
		le.PutUint16(c[8:], flags)
		le.PutUint16(c[10:], f.Method)
		le.PutUint16(c[12:], f.ModifiedTime)
		le.PutUint16(c[14:], f.ModifiedDate)
		le.PutUint32(c[16:], f.CRC32)
		le.PutUint32(c[20:], uint32(len(data)))
		le.PutUint32(c[24:], uint32(f.UncompressedSize64))
		le.PutUint16(c[28:], uint16(len(f.Name)))
		// Extra and comment lengths, disk number and internal attributes
		// stay zero.
		le.PutUint32(c[38:], f.ExternalAttrs)
		le.PutUint32(c[42:], uint32(offset))
		cd.Write(c[:])
		cd.WriteString(f.Name)
	}
	if len(r.File) > 0xffff {
		return nil, 0, errors.New("too many entries for a zip without zip64")
	}

	cdOffset := out.Len()
	out.Write(cd.Bytes())
	var e [eocdLen]byte
	le.PutUint32(e[0:], eocdSig)
	le.PutUint16(e[8:], uint16(len(r.File)))
	le.PutUint16(e[10:], uint16(len(r.File)))
	le.PutUint32(e[12:], uint32(cd.Len()))
	le.PutUint32(e[16:], uint32(cdOffset))
	out.Write(e[:])
	return out.Bytes(), cdOffset, nil
}

// contentDigest is v2's chunked SHA-256: each section split into 1 MiB
// chunks, each chunk hashed behind 0xa5 and its length, and those digests
// hashed behind 0x5a and their count.
func contentDigest(sections ...[]byte) []byte {
	var digests []byte
	count := 0
	for _, s := range sections {
		for len(s) > 0 {
			n := min(len(s), chunkSize)
			h := sha256.New()
			var prefix [5]byte
			prefix[0] = 0xa5
			binary.LittleEndian.PutUint32(prefix[1:], uint32(n))
			h.Write(prefix[:])
			h.Write(s[:n])
			digests = h.Sum(digests)
			s = s[n:]
			count++
		}
	}
	h := sha256.New()
	var prefix [5]byte
	prefix[0] = 0x5a
	binary.LittleEndian.PutUint32(prefix[1:], uint32(count))
	h.Write(prefix[:])
	h.Write(digests)
	return h.Sum(nil)
}

// signingBlock is the APK Signing Block holding one v2 signer.
func signingBlock(digest []byte, k *Key) ([]byte, error) {
	signedData := prefixed(concat(
		prefixed(prefixed(concat(u32(rsaPKCS1SHA256), prefixed(digest)))),
		prefixed(prefixed(k.Cert.Raw)),
		prefixed(nil), // additional attributes
	))
	// The signature is over the signed data without its own length prefix.
	hashed := sha256.Sum256(signedData[4:])
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA256, hashed[:])
	if err != nil {
		return nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.Private.PublicKey)
	if err != nil {
		return nil, err
	}
	signer := concat(
		signedData,
		prefixed(prefixed(concat(u32(rsaPKCS1SHA256), prefixed(sig)))),
		prefixed(pub),
	)
	value := prefixed(prefixed(signer))

	pair := concat(u64(uint64(4+len(value))), u32(v2BlockID), value)
	size := u64(uint64(len(pair) + 8 + len(blockMagic)))
	return concat(size, pair, size, []byte(blockMagic)), nil
}

func u32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
func u64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

// prefixed is b behind its uint32 length, the framing every v2 field uses.
func prefixed(b []byte) []byte { return append(u32(uint32(len(b))), b...) }

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// isNativeLib reports whether name is a library the platform maps from the
// APK, which wants page alignment.
func isNativeLib(name string) bool { return path.Ext(name) == ".so" }
