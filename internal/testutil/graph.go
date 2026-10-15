package testutil

import (
	"encoding/binary"
	"fmt"
	"hash/maphash"
	"math"
	"reflect"
	"slices"
	"strings"
)

// Graph is every object reachable from a root, by address: a hash of the
// object's own fields, each pointer read as the address it holds, so that it
// does not depend on the order a map is walked in. Two graphs of one root
// taken at two times name every object written in between.
type Graph map[uintptr]graphNode

type graphNode struct {
	hash uint64
	ptr  reflect.Value
}

// GraphOf walks everything reachable from root. The root itself is the entry
// at address 0.
func GraphOf(root any) Graph {
	var f Fingerprinter
	return f.Graph(root)
}

// Graph is GraphOf, stopping where f.Skip says.
func (f *Fingerprinter) Graph(root any) Graph {
	g := Graph{}
	w := walker{skip: f.Skip}
	w.walk(reflect.ValueOf(root), func(addr uintptr, ptr reflect.Value, hash uint64) {
		g[addr] = graphNode{hash: hash, ptr: ptr}
	})
	return g
}

// Changed describes each object of g that after holds with different fields,
// then each it no longer reaches, then each it reaches and g did not.
func (g Graph) Changed(after Graph) []string {
	var changed, gone, added []string
	for addr, was := range g {
		now, ok := after[addr]
		switch {
		case !ok:
			gone = append(gone, fmt.Sprintf("%x %s no longer reached: %.300s", addr, was.ptr.Type(), describe(was.ptr)))
		case now.hash != was.hash:
			changed = append(changed, fmt.Sprintf("%x %s changed, now %.300s", addr, now.ptr.Type(), describe(now.ptr)))
		}
	}
	for addr, now := range after {
		if _, ok := g[addr]; !ok {
			added = append(added, fmt.Sprintf("%x %s new: %.200s", addr, now.ptr.Type(), describe(now.ptr)))
		}
	}
	slices.Sort(changed)
	slices.Sort(gone)
	slices.Sort(added)
	return slices.Concat(changed, gone, added)
}

// Fingerprinter hashes a Graph into one number, for a caller that asks often
// and needs only whether anything changed. It keeps its tables between walks,
// so asking again allocates next to nothing.
type Fingerprinter struct {
	// Skip names the objects compared by address alone and not walked into:
	// a part of the graph held immutable by other means, or one that is not
	// the caller's. It is handed the pointer.
	Skip func(reflect.Value) bool
	w    walker
}

func (f *Fingerprinter) Of(root any) uint64 {
	f.w.skip = f.Skip
	var sum uint64
	f.w.walk(reflect.ValueOf(root), func(_ uintptr, _ reflect.Value, hash uint64) { sum += hash })
	return sum
}

var graphSeed = maphash.MakeSeed()

type walker struct {
	skip  func(reflect.Value) bool
	seen  map[uintptr]bool
	queue []reflect.Value
	// hashes holds one hash per map nesting depth, since a map entry is
	// hashed apart from its owner to be summed in any order.
	hashes []*maphash.Hash
	types  map[reflect.Type]uint64
	buf    [8]byte
}

// walk hands each in every object reachable from root, the root at address 0,
// with a hash of its address, its type and its own fields.
func (w *walker) walk(root reflect.Value, each func(addr uintptr, ptr reflect.Value, hash uint64)) {
	if w.seen == nil {
		w.seen = map[uintptr]bool{}
		w.types = map[reflect.Type]uint64{}
	}
	clear(w.seen)
	w.queue = w.queue[:0]
	h := w.hash(0)
	w.write(h, root, 1)
	each(0, root, h.Sum64())
	for len(w.queue) > 0 {
		v := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		h := w.hash(0)
		w.word(h, uint64(v.Pointer()))
		w.word(h, w.typeHash(v.Type()))
		w.write(h, v.Elem(), 1)
		each(v.Pointer(), v, h.Sum64())
	}
}

func (w *walker) hash(depth int) *maphash.Hash {
	for len(w.hashes) <= depth {
		h := &maphash.Hash{}
		h.SetSeed(graphSeed)
		w.hashes = append(w.hashes, h)
	}
	h := w.hashes[depth]
	h.Reset()
	return h
}

func (w *walker) typeHash(t reflect.Type) uint64 {
	if x, ok := w.types[t]; ok {
		return x
	}
	x := maphash.String(graphSeed, t.String())
	w.types[t] = x
	return x
}

func (w *walker) word(h *maphash.Hash, x uint64) {
	binary.LittleEndian.PutUint64(w.buf[:], x)
	h.Write(w.buf[:])
}

func (w *walker) write(h *maphash.Hash, v reflect.Value, depth int) {
	h.WriteByte(byte(v.Kind()))
	switch v.Kind() {
	case reflect.Pointer, reflect.UnsafePointer:
		if v.IsNil() {
			return
		}
		w.word(h, uint64(v.Pointer()))
		if v.Kind() == reflect.Pointer && !w.seen[v.Pointer()] && (w.skip == nil || !w.skip(v)) {
			w.seen[v.Pointer()] = true
			w.queue = append(w.queue, v)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		w.word(h, w.typeHash(v.Elem().Type()))
		w.write(h, v.Elem(), depth)
	case reflect.Struct:
		// Not v.Fields(): it builds a StructField per field, which was most of
		// what a walk allocated.
		for i, n := 0, v.NumField(); i < n; i++ {
			w.write(h, v.Field(i), depth)
		}
	case reflect.Slice, reflect.Array:
		w.word(h, uint64(v.Len()))
		for i := range v.Len() {
			w.write(h, v.Index(i), depth)
		}
	case reflect.Map:
		var sum uint64
		iter := v.MapRange()
		for iter.Next() {
			e := w.hash(depth)
			w.write(e, iter.Key(), depth+1)
			w.write(e, iter.Value(), depth+1)
			sum += e.Sum64()
		}
		w.word(h, uint64(v.Len()))
		w.word(h, sum)
	case reflect.String:
		w.word(h, uint64(v.Len()))
		h.WriteString(v.String())
	case reflect.Bool:
		if v.Bool() {
			h.WriteByte(1)
		} else {
			h.WriteByte(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		w.word(h, uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		w.word(h, v.Uint())
	case reflect.Float32, reflect.Float64:
		w.word(h, math.Float64bits(v.Float()))
	}
}

// describe is v's own fields as text, a pointer as its address.
func describe(v reflect.Value) string {
	var b strings.Builder
	var shallow func(v reflect.Value)
	shallow = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.UnsafePointer:
			if v.IsNil() {
				b.WriteString("nil ")
				return
			}
			fmt.Fprintf(&b, "%x ", v.Pointer())
		case reflect.Interface:
			if v.IsNil() {
				b.WriteString("nil ")
				return
			}
			shallow(v.Elem())
		case reflect.Struct:
			b.WriteString("{")
			for i := range v.NumField() {
				b.WriteString(v.Type().Field(i).Name + ":")
				shallow(v.Field(i))
			}
			b.WriteString("} ")
		case reflect.Slice, reflect.Array:
			fmt.Fprintf(&b, "[%d: ", v.Len())
			for i := range min(v.Len(), 8) {
				shallow(v.Index(i))
			}
			b.WriteString("] ")
		case reflect.Map:
			fmt.Fprintf(&b, "map[%d", v.Len())
			if v.Type().Key().Kind() == reflect.String {
				keys := make([]string, 0, v.Len())
				for _, k := range v.MapKeys() {
					keys = append(keys, k.String())
				}
				slices.Sort(keys)
				fmt.Fprintf(&b, ": %s", strings.Join(keys[:min(len(keys), 12)], " "))
			}
			b.WriteString("] ")
		case reflect.String:
			fmt.Fprintf(&b, "%q ", v.String())
		default:
			fmt.Fprintf(&b, "%v ", v)
		}
	}
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	shallow(v)
	return b.String()
}
