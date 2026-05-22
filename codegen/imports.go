package codegen

// ImportSpec describes a single import an emitted file needs. The lang
// translator decides how to render the import line; the writer dedups by
// value and preserves insertion order.
type ImportSpec struct {
	Path  string
	Alias string
	Kind  ImportKind
}

// ImportKind tells the lang translator how to render an ImportSpec.
type ImportKind int

const (
	ImportNative        ImportKind = iota // language-native import
	ImportStdlibRuntime                   // sngl pkg/<lang>/<name>
	ImportCgo                             // C header via cgo preamble
	ImportEsModule                        // ES module (JS bundler input)
	ImportWasmExtern                      // WASM extern bridge
)

// String returns a short identifier for the import kind. Used in error
// messages and debug dumps; not part of any wire format.
func (k ImportKind) String() string {
	switch k {
	case ImportNative:
		return "native"
	case ImportStdlibRuntime:
		return "stdlib-runtime"
	case ImportCgo:
		return "cgo"
	case ImportEsModule:
		return "esmodule"
	case ImportWasmExtern:
		return "wasm-extern"
	default:
		return "unknown"
	}
}
