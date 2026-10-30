package apksign

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Key is a signing key and the self-signed certificate an APK carries for it.
type Key struct {
	Private *rsa.PrivateKey
	Cert    *x509.Certificate
}

// DebugKey loads the debug signing key stored at path, creating it first if
// there is none. It is a PEM file holding a PKCS#8 RSA key and its
// certificate, rather than a keystore, so no Java tool is needed to make or
// read it.
//
// Android lets an installed app be replaced only by one signed with the same
// key, so the key is made once and kept: a new one makes the next install of
// every app it signed fail with INSTALL_FAILED_UPDATE_INCOMPATIBLE.
func DebugKey(path string) (*Key, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return createDebugKey(path)
	}
	if err != nil {
		return nil, err
	}
	return parseKey(data)
}

func createDebugKey(path string) (*Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// The subject is the one Android's own debug keystore uses.
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Android Debug", Organization: []string{"Android"}, Country: []string{"US"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(30, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)

	// Written beside the destination and renamed into place, so two builds
	// racing to make the key agree on whichever lands, and neither reads half
	// a file.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return DebugKey(path)
}

func parseKey(data []byte) (*Key, error) {
	k := &Key{}
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		switch b.Type {
		case "PRIVATE KEY":
			priv, err := x509.ParsePKCS8PrivateKey(b.Bytes)
			if err != nil {
				return nil, err
			}
			rsaKey, ok := priv.(*rsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("signing key is %T, not RSA", priv)
			}
			k.Private = rsaKey
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(b.Bytes)
			if err != nil {
				return nil, err
			}
			k.Cert = cert
		}
	}
	if k.Private == nil || k.Cert == nil {
		return nil, errors.New("signing key file needs a PRIVATE KEY and a CERTIFICATE")
	}
	return k, nil
}
