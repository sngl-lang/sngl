package apksign

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
)

// A v1 signature is the JAR signing scheme: META-INF/MANIFEST.MF holds a
// digest of each entry, CERT.SF a digest of the manifest and of each of its
// sections, and CERT.RSA a PKCS#7 signature of CERT.SF. Android before API 24
// verifies only this. It is written before the v2 block, which then covers
// these files too, and CERT.SF says X-Android-APK-Signed: 2 so a verifier
// that knows v2 refuses the APK if its v2 block is stripped.
//
// The digests are SHA-256, which Android verifies from API 18. Compose itself
// needs API 21, so SHA-1 is never wanted.

const (
	v1Manifest  = "META-INF/MANIFEST.MF"
	v1Signature = "META-INF/CERT.SF"
	v1Block     = "META-INF/CERT.RSA"
	createdBy   = "1.0 (Android)"
	// V1MinSdk is the lowest API level the v1 signature written here
	// installs on: the first that accepts SHA-256 in a JAR signature.
	V1MinSdk = 18
)

// addV1 returns apk with a v1 signature's three files added, dropping any
// signature files the input carried.
func addV1(apk []byte, k *Key) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(apk), int64(len(apk)))
	if err != nil {
		return nil, err
	}
	var kept []*zip.File
	for _, f := range r.File {
		if !isSignatureFile(f.Name) {
			kept = append(kept, f)
		}
	}

	var manifest, sf bytes.Buffer
	manifest.WriteString("Manifest-Version: 1.0\r\nCreated-By: " + createdBy + "\r\n\r\n")
	type section struct{ name, text string }
	var sections []section
	for _, f := range slices.SortedFunc(slices.Values(kept), func(a, b *zip.File) int { return strings.Compare(a.Name, b.Name) }) {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Name, err)
		}
		text := manifestLine("Name", f.Name) + manifestLine("SHA-256-Digest", b64(h.Sum(nil))) + "\r\n"
		manifest.WriteString(text)
		sections = append(sections, section{f.Name, text})
	}

	sf.WriteString("Signature-Version: 1.0\r\nCreated-By: " + createdBy + "\r\n")
	sf.WriteString(manifestLine("SHA-256-Digest-Manifest", b64(sha256Sum(manifest.Bytes()))))
	sf.WriteString("X-Android-APK-Signed: 2\r\n\r\n")
	for _, s := range sections {
		sf.WriteString(manifestLine("Name", s.name))
		sf.WriteString(manifestLine("SHA-256-Digest", b64(sha256Sum([]byte(s.text)))))
		sf.WriteString("\r\n")
	}

	block, err := pkcs7Sign(sf.Bytes(), k)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range kept {
		if err := w.Copy(f); err != nil {
			return nil, err
		}
	}
	for _, e := range []struct {
		name string
		data []byte
	}{{v1Manifest, manifest.Bytes()}, {v1Signature, sf.Bytes()}, {v1Block, block}} {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// isSignatureFile reports whether name is part of a JAR signature, which a
// new one replaces.
func isSignatureFile(name string) bool {
	dir, base, ok := strings.Cut(name, "/")
	if !ok || !strings.EqualFold(dir, "META-INF") || strings.Contains(base, "/") {
		return false
	}
	upper := strings.ToUpper(base)
	if upper == "MANIFEST.MF" || strings.HasPrefix(upper, "SIG-") {
		return true
	}
	for _, ext := range []string{".SF", ".RSA", ".DSA", ".EC"} {
		if strings.HasSuffix(upper, ext) {
			return true
		}
	}
	return false
}

// manifestLine is one "Key: value" attribute, wrapped as the JAR manifest
// format requires: no line over 72 bytes, each continuation behind a space.
func manifestLine(key, value string) string {
	line := key + ": " + value
	var b strings.Builder
	for first := true; ; first = false {
		limit := 72
		if !first {
			b.WriteString(" ")
			limit = 71
		}
		if len(line) <= limit {
			b.WriteString(line)
			b.WriteString("\r\n")
			return b.String()
		}
		b.WriteString(line[:limit])
		b.WriteString("\r\n")
		line = line[limit:]
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	// Content is [0] EXPLICIT, built by hand: encoding/asn1 writes a
	// RawValue's FullBytes as they are, without the tag a struct tag asks for.
	Content asn1.RawValue `asn1:"optional"`
}

type signedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	ContentInfo      contentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      []signerInfo  `asn1:"set"`
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type signerInfo struct {
	Version                   int
	IssuerAndSerialNumber     issuerAndSerial
	DigestAlgorithm           pkix.AlgorithmIdentifier
	DigestEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedDigest           []byte
}

// pkcs7Sign is a detached PKCS#7 SignedData over content, with no
// authenticated attributes, the shape apksigner writes: the RSA signature is
// over content's SHA-256 itself.
func pkcs7Sign(content []byte, k *Key) ([]byte, error) {
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA256, sha256Sum(content))
	if err != nil {
		return nil, err
	}
	sha256Alg := pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue}
	sd := signedData{
		Version:          1,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{sha256Alg},
		ContentInfo:      contentInfo{ContentType: oidData},
		Certificates:     asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: k.Cert.Raw},
		SignerInfos: []signerInfo{{
			Version:                   1,
			IssuerAndSerialNumber:     issuerAndSerial{Issuer: asn1.RawValue{FullBytes: k.Cert.RawIssuer}, Serial: k.Cert.SerialNumber},
			DigestAlgorithm:           sha256Alg,
			DigestEncryptionAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidRSAEncryption, Parameters: asn1.NullRawValue},
			EncryptedDigest:           sig,
		}},
	}
	inner, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(contentInfo{ContentType: oidSignedData, Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner}})
}
