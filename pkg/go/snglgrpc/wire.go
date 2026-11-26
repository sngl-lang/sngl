package snglgrpc

import (
	"cmp"
	"encoding/base64"
	"math"
	"slices"

	"google.golang.org/protobuf/encoding/protowire"
)

// The readers a generated message reader calls: each answers the value a
// record of wire type typ holds at the start of b and how many bytes it
// took, or a negative count where typ is not the one asked for or b ends
// first.

func Varint(b []byte, typ protowire.Type) (uint64, int) {
	if typ != protowire.VarintType {
		return 0, -1
	}
	return protowire.ConsumeVarint(b)
}

func Fixed32(b []byte, typ protowire.Type) (uint32, int) {
	if typ != protowire.Fixed32Type {
		return 0, -1
	}
	return protowire.ConsumeFixed32(b)
}

func Fixed64(b []byte, typ protowire.Type) (uint64, int) {
	if typ != protowire.Fixed64Type {
		return 0, -1
	}
	return protowire.ConsumeFixed64(b)
}

func Bytes(b []byte, typ protowire.Type) ([]byte, int) {
	if typ != protowire.BytesType {
		return nil, -1
	}
	return protowire.ConsumeBytes(b)
}

// FromFloat32 and FromFloat64 are a float's bits as the program's float.
func FromFloat32(x uint32) float64 { return float64(math.Float32frombits(x)) }
func FromFloat64(x uint64) float64 { return math.Float64frombits(x) }

// Float32 and Float64 are a float's bits on the wire.
func Float32(f float64) uint32 { return math.Float32bits(float32(f)) }
func Float64(f float64) uint64 { return math.Float64bits(f) }

// FromBase64 and ToBase64 carry a `bytes` field: the program holds its
// base64, as proto3's JSON writes it.
func FromBase64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}

func ToBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// SortedKeys are a map field's keys in order, so a message is written the
// same every time.
func SortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
