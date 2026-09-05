package gir

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// LoadGIR returns the TypeRegistry for the GIR file at path, reading it from a
// local cache when one is present and current.
//
// Parsing Gtk-4.0.gir is 8.4MB of XML for ~270 classes — about 150ms of CPU —
// and the type-checker touches it on every `sngl` invocation, because the gtk4
// platform's stdlib extension bodies name GTK widget types whatever the build
// targets. The cache turns that into a few milliseconds of gob decode.
//
// Every cache failure falls back to a full parse, so a missing, unreadable,
// corrupt or stale entry costs correctness nothing.
func LoadGIR(path string) (*TypeRegistry, error) {
	key, ok := cacheKey(path)
	if !ok {
		return ParseGIR(path)
	}
	cachePath := filepath.Join(codegen.SnglCacheDir(), "gir", key+".gob")

	if reg, err := readCache(cachePath); err == nil {
		return reg, nil
	}

	reg, err := ParseGIR(path)
	if err != nil {
		return nil, err
	}
	writeCache(cachePath, reg)
	return reg, nil
}

// cacheKey identifies an entry by the GIR file — path, size and mtime, so an
// upgraded gtk4 install invalidates it without an explicit purge — and by the
// shape of TypeRegistry itself.
//
// The schema half matters: this cache stores the parsed registry, so a field
// added to ClassInfo or Prop makes every existing entry wrong rather than
// merely old. gob would decode such an entry happily and leave the new field
// zero, which reads downstream as "GTK has no such property". Deriving the
// fingerprint by reflection means that can't be forgotten.
func cacheKey(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d",
		schemaFingerprint(), abs, info.Size(), info.ModTime().UnixNano()))
	return hex.EncodeToString(sum[:10]), true
}

var schemaFingerprint = sync.OnceValue(func() string {
	h := sha256.New()
	writeTypeShape(h, reflect.TypeFor[TypeRegistry](), map[reflect.Type]bool{})
	return hex.EncodeToString(h.Sum(nil)[:8])
})

// writeTypeShape feeds a stable description of t — every field name, and the
// shape of every type it reaches — into h.
func writeTypeShape(h interface{ Write([]byte) (int, error) }, t reflect.Type, seen map[reflect.Type]bool) {
	if seen[t] {
		fmt.Fprintf(h, "cycle(%s)", t.String())
		return
	}
	seen[t] = true
	defer delete(seen, t)

	fmt.Fprintf(h, "%s:%s(", t.String(), t.Kind())
	switch t.Kind() {
	case reflect.Struct:
		for f := range t.Fields() {
			fmt.Fprintf(h, "%s ", f.Name)
			writeTypeShape(h, f.Type, seen)
			h.Write([]byte(";"))
		}
	case reflect.Pointer, reflect.Slice, reflect.Array:
		writeTypeShape(h, t.Elem(), seen)
	case reflect.Map:
		writeTypeShape(h, t.Key(), seen)
		h.Write([]byte("=>"))
		writeTypeShape(h, t.Elem(), seen)
	}
	h.Write([]byte(")"))
}

func readCache(path string) (*TypeRegistry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var reg TypeRegistry
	if err := gob.NewDecoder(f).Decode(&reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

// writeCache stores reg at path. Best effort: the caller already holds a valid
// registry, so a failure here only costs the next run a re-parse. The write
// goes to a temp file and is renamed, so a concurrent reader never sees a
// half-written entry, and two writers cannot interleave.
func writeCache(path string, reg *TypeRegistry) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gir-*")
	if err != nil {
		return
	}
	if err := gob.NewEncoder(tmp).Encode(reg); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}
