package sql

import (
	"errors"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// The sql: scheme's handler (lib/x/scheme/sql) reads the configuration and
// each file it names and hands them to these, which read nothing but their
// arguments -- the handler's reads are recorded -- so they record nothing.
func init() {
	buildhost.RegisterIntrinsic("sql.inputs", inputsIntrinsic)
	buildhost.RegisterIntrinsic("sql.configFor", configForIntrinsic)
	buildhost.RegisterIntrinsic("sql.generate", generateIntrinsic)
}

func inputsIntrinsic(_ *buildhost.Recorder, _ *ir.Call, args []any) (any, error) {
	config, _ := args[0].(string)
	dir, _ := args[1].(string)
	paths, err := Inputs(config, dir)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, p := range paths {
		out = append(out, p)
	}
	return out, nil
}

// generateIntrinsic answers sql.generate(files, config, name, from).
func generateIntrinsic(_ *buildhost.Recorder, call *ir.Call, args []any) (any, error) {
	read, _ := args[0].(map[string]any)
	config, _ := args[1].(string)
	name, _ := args[2].(string)
	from, _ := args[3].(string)
	files := map[string]string{}
	for k, v := range read {
		files[k], _ = v.(string)
	}
	if call == nil || call.Func == nil || call.Func.Return == nil {
		return nil, errors.New("sql.generate: no declared return type")
	}
	def, _ := call.Func.Return.Decl.(*ir.StructDef)
	if def == nil {
		return nil, errors.New("sql.generate: the declared return type is no struct")
	}
	p, err := Generate(files, config, name, from)
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

// configForIntrinsic answers sql.configFor(queries, name, from).
func configForIntrinsic(_ *buildhost.Recorder, _ *ir.Call, args []any) (any, error) {
	queries, _ := args[0].(string)
	name, _ := args[1].(string)
	from, _ := args[2].(string)
	// The engine is the schema's, read once its files are: what is asked
	// here is only which files those are.
	return ConfigFor(queries, name, from, "sqlite")
}
