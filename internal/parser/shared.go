package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"sync"
	"weak"

	"duckfam.us/sngl/ast"
)

// shared holds every document ParseShared has handed out that something still
// references, by Digest.
var shared sync.Map

// ParseShared is Parse for source the caller does not edit afterwards, as an
// import resolver's is: the same name and source parsed again while the first
// document is still referenced returns that document. An AST is immutable once
// parsed, so the checks that read one file share its tree.
func ParseShared(filename string, src []byte) (*ast.Document, error) {
	key := Digest(filename, src)
	if v, ok := shared.Load(key); ok {
		if doc := v.(weak.Pointer[ast.Document]).Value(); doc != nil {
			return doc, nil
		}
	}
	doc, err := parse(filename, src, key)
	if err != nil {
		return doc, err
	}
	ptr := weak.Make(doc)
	shared.Store(key, ptr)
	runtime.AddCleanup(doc, func(key string) { shared.CompareAndDelete(key, ptr) }, key)
	return doc, nil
}

// Digest is what Parse records as a document's Digest.
func Digest(filename string, src []byte) string {
	h := sha256.New()
	h.Write([]byte(filename))
	h.Write([]byte{0})
	h.Write(src)
	return hex.EncodeToString(h.Sum(nil))
}
