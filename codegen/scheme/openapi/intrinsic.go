package openapi

import (
	"errors"
	"path"
	"strings"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// The openapi: scheme's handler (lib/x/scheme/openapi) hands the document it
// read to this, which answers the package it resolves to.
func init() {
	buildhost.RegisterIntrinsic("openapi.generate", generateIntrinsic)
}

// generateIntrinsic answers openapi.generate(text, from): an
// `openapi.Package` holding the file to write, its source and the notes the
// import gives. It reads nothing but its arguments -- the handler read the
// file, which recorded it -- so it records nothing.
func generateIntrinsic(_ *buildhost.Recorder, call *ir.Call, args []any) (any, error) {
	text, _ := args[0].(string)
	from, _ := args[1].(string)
	if call == nil || call.Func == nil || call.Func.Return == nil {
		return nil, errors.New("openapi.generate: no declared return type")
	}
	def, _ := call.Func.Return.Decl.(*ir.StructDef)
	if def == nil {
		return nil, errors.New("openapi.generate: the declared return type is no struct")
	}
	spec, err := Parse([]byte(text))
	if err != nil {
		return nil, err
	}
	src, err := Write(spec, from)
	if err != nil {
		return nil, err
	}
	notes := make([]any, len(spec.Notes))
	for i, n := range spec.Notes {
		notes[i] = n
	}
	pkg := interp.NewStruct(def, call.Func.Return)
	pkg.Set("file", FileName(from))
	pkg.Set("source", src)
	pkg.Set("notes", notes)
	return pkg, nil
}

// FileName is the file an import of from writes: the document's own name,
// as a .sngl file.
func FileName(from string) string {
	base := path.Base(from)
	if i := strings.IndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return base + ".sngl"
}
