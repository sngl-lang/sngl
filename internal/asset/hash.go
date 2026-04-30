// Package asset provides helpers for emitting static assets with content-hashed
// filenames so browsers and CDNs cache-bust automatically when content changes.
package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HashLen is the number of hex characters of the content-hash segment inserted
// into asset filenames.
const HashLen = 8

// HashedName returns name with a short content-hash segment inserted before the
// final extension. Examples:
//
//	HashedName("style.css", data)     -> "style.a3f9b1c2.css"
//	HashedName("wasm_exec.js", data)  -> "wasm_exec.deadbeef.js"
//	HashedName("Makefile", data)      -> "Makefile.cafef00d"
//
// The hash is the first HashLen hex characters of sha256(content).
func HashedName(name string, content []byte) string {
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])[:HashLen]
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 {
		return name[:i] + "." + hash + name[i:]
	}
	return name + "." + hash
}
