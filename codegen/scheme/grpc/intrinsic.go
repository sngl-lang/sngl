package grpc

import (
	"errors"
	"path"
	"strings"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// The grpc: scheme's handler (lib/x/scheme/grpc) reads the files and hands
// them to these, which read nothing but their arguments -- the handler's
// reads are recorded -- so they record nothing.
func init() {
	buildhost.RegisterIntrinsic("grpc.imports", importsIntrinsic)
	buildhost.RegisterIntrinsic("grpc.generate", generateIntrinsic)
}

func importsIntrinsic(_ *buildhost.Recorder, _ *ir.Call, args []any) (any, error) {
	text, _ := args[0].(string)
	name, _ := args[1].(string)
	out := []any{}
	for _, p := range Imports(text, name) {
		out = append(out, p)
	}
	return out, nil
}

// generateIntrinsic answers grpc.generate(files, name, from).
func generateIntrinsic(_ *buildhost.Recorder, call *ir.Call, args []any) (any, error) {
	read, _ := args[0].(map[string]any)
	name, _ := args[1].(string)
	from, _ := args[2].(string)
	files := map[string]string{}
	for k, v := range read {
		files[k], _ = v.(string)
	}
	if call == nil || call.Func == nil || call.Func.Return == nil {
		return nil, errors.New("grpc.generate: no declared return type")
	}
	def, _ := call.Func.Return.Decl.(*ir.StructDef)
	if def == nil {
		return nil, errors.New("grpc.generate: the declared return type is no struct")
	}
	p, err := Generate(files, name, from)
	if err != nil {
		return nil, err
	}
	notes := make([]any, len(p.Notes))
	for i, n := range p.Notes {
		notes[i] = n
	}
	pkg := interp.NewStruct(def, call.Func.Return)
	pkg.Set("file", FileName(from))
	pkg.Set("source", p.Source)
	pkg.Set("notes", notes)
	return pkg, nil
}

// FileName is the file an import of from writes: the .proto's own name, as a
// .sngl file.
func FileName(from string) string {
	base := path.Base(from)
	if i := strings.IndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return base + ".sngl"
}
