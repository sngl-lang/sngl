package apksign

import (
	"bytes"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
)

// v3BlockID is APK Signature Scheme v3's pair in the signing block.
const v3BlockID = 0xf05368c0

// DebugSubject is the common name Android's debug keystore signs with, and
// so DebugKey too. A release key never carries it.
const DebugSubject = "Android Debug"

// SignerCerts reads the certificates an APK is signed with, from its v3 or
// v2 signature. An APK signed only with v1 is reported as unsigned here.
func SignerCerts(apk []byte) ([]*x509.Certificate, error) {
	block, err := signingBlockOf(apk)
	if err != nil {
		return nil, err
	}
	for _, id := range []uint32{v3BlockID, v2BlockID} {
		value, ok := pairValue(block, id)
		if !ok {
			continue
		}
		return signedCerts(value)
	}
	return nil, errors.New("no v2 or v3 signature")
}

// IsDebug reports whether any of certs is a debug certificate.
func IsDebug(certs []*x509.Certificate) bool {
	for _, c := range certs {
		if c.Subject.CommonName == DebugSubject {
			return true
		}
	}
	return false
}

// signingBlockOf is the pairs of the APK Signing Block, which sits just
// before the central directory the end record points at.
func signingBlockOf(apk []byte) ([]byte, error) {
	eocd := bytes.LastIndex(apk, []byte{0x50, 0x4b, 0x05, 0x06})
	if eocd < 0 || eocd+22 > len(apk) {
		return nil, errors.New("not a zip")
	}
	cd := int(binary.LittleEndian.Uint32(apk[eocd+16:]))
	if cd < 24 || cd > eocd || string(apk[cd-16:cd]) != blockMagic {
		return nil, errors.New("no APK Signing Block")
	}
	size := binary.LittleEndian.Uint64(apk[cd-24:])
	start := uint64(cd) - size - 8
	if size < 24 || size > uint64(cd) || start > uint64(cd) {
		return nil, errors.New("malformed APK Signing Block")
	}
	return apk[start+8 : cd-24], nil
}

func pairValue(pairs []byte, id uint32) ([]byte, bool) {
	for len(pairs) >= 12 {
		n := binary.LittleEndian.Uint64(pairs)
		if n < 4 || n > uint64(len(pairs)-8) {
			return nil, false
		}
		pair := pairs[8 : 8+n]
		if binary.LittleEndian.Uint32(pair) == id {
			return pair[4:], true
		}
		pairs = pairs[8+n:]
	}
	return nil, false
}

// signedCerts walks signers → signer → signed data → certificates, each a
// length-prefixed field of the one before.
func signedCerts(value []byte) ([]*x509.Certificate, error) {
	signers, _, err := lengthPrefixed(value)
	if err != nil {
		return nil, err
	}
	var certs []*x509.Certificate
	for len(signers) > 0 {
		var signer []byte
		if signer, signers, err = lengthPrefixed(signers); err != nil {
			return nil, err
		}
		signedData, _, err := lengthPrefixed(signer)
		if err != nil {
			return nil, err
		}
		_, rest, err := lengthPrefixed(signedData) // digests
		if err != nil {
			return nil, err
		}
		list, _, err := lengthPrefixed(rest)
		if err != nil {
			return nil, err
		}
		for len(list) > 0 {
			var der []byte
			if der, list, err = lengthPrefixed(list); err != nil {
				return nil, err
			}
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, err
			}
			certs = append(certs, c)
		}
	}
	return certs, nil
}

func lengthPrefixed(b []byte) (field, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, fmt.Errorf("truncated signature")
	}
	n := binary.LittleEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, fmt.Errorf("truncated signature")
	}
	return b[4 : 4+n], b[4+n:], nil
}
