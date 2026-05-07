package gtk4

import (
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Config holds per-output configuration options for the gtk4 platform.
// Fields mirror the Options struct in gtk4.sngl.
type Config struct {
	Package string `sngl:"package"`
	Main    bool   `sngl:"main"`
	GIR     string `sngl:"gir"`
}

type compilation struct {
	gen *Generator
	cfg Config
}

// Stub: replaced in Task 6 with full MutationModel emitter.
func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	return nil, nil
}

// Stub: replaced in Task 6 with full MutationModel emitter.
func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, _ *codegen.Request) (*codegen.Response, error) {
	return &codegen.Response{}, nil
}
