package codegen

import (
	"fmt"
	"maps"
	"slices"
)

// Generate is what plat writes for req, and beside it what req's language
// compiles into the program of its own (CCompiler.CSources): the C sources
// the program's c.links ship and the dispatchers its C callbacks cross
// through. Every caller generating a program to build goes through it, so a
// build, a test's launch and a dump hold the same files.
func Generate(plat PlatformGenerator, req *Request, sink Sink) error {
	cc, ok := req.Lang.(CCompiler)
	if !ok {
		return plat.Generate(req, sink)
	}
	mem := NewMemSink()
	if err := plat.Generate(req, mem); err != nil {
		return err
	}
	files := mem.Files()
	srcs, err := cc.CSources(req.Pkg, files)
	if err != nil {
		return err
	}
	for name, data := range srcs {
		if _, dup := files[name]; dup {
			return fmt.Errorf("the program ships %s, which the platform also generates", name)
		}
		files[name] = data
	}
	for name := range mem.Boilerplate() {
		MarkBoilerplate(sink, name)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := sink.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(files[name]); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	return nil
}
