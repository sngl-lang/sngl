// Package plugin runs a gen.scheme's @generate handler: the SNGL that answers
// an import of a scheme a package declared.
//
// The handler runs in the interpreter, through the build host, so every read
// it makes is gated by the plugin's own grants and recorded. What it writes is
// the package the import resolves to, and is stored like any producer's output
// (decision 10 of SNGL_PLUGINS.md): keyed by the request and by the digest of
// the plugin's code, and replayed while every input it recorded still holds.
// A literal `cache.inputs` directive in what it writes adds inputs the
// recorder could not see.
package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/trust"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Producer names the outputs a plugin's handler writes.
const Producer = "sngl.scheme"

// Runner runs handlers for one check.
type Runner struct {
	// Trust is what the project's plugins may do. Nil refuses everything a
	// library plugin does not ask.
	Trust *trust.Policy
	// Root is the import root a relative read resolves against, and what a
	// read is unasked inside of.
	Root string
	// Store holds outputs between builds; nil asks gencache.Default.
	Store *gencache.Store
}

// fileMark separates the files of one stored output.
const fileMark = "// sngl:file "

// Generate runs s's handler for uri, or replays what it wrote last time, and
// returns the files parsed.
func (r *Runner) Generate(s *ir.Scheme, uri string) ([]*ast.Document, error) {
	var ow *buildhost.Owner
	if s.Library && s.Pkg.Origin != nil {
		ow = buildhost.NewOwner(s.Pkg, s.Pkg.Origin.URI, "", "", true)
	}
	host := buildhost.New(r.Trust, r.Root, s.Pkg, ow)
	ow = host.OwnerFor(nil)
	where := ow.Name()
	if ow.Dir() != "" {
		where = "dir:" + ow.Dir()
	}
	req := gencache.Request{Producer: Producer, Params: []string{where, s.Name, uri}, Identity: ow.Closure()}
	store := r.Store
	if store == nil {
		store = gencache.Default()
	}
	if data, ok := store.Lookup(req); ok {
		if docs, err := parseFiles(splitFiles(gencache.Body(data))); err == nil {
			return docs, nil
		} else {
			slog.Debug("sngl.scheme stored output", "scheme", s.Name, "uri", uri, "err", err)
		}
	}

	out, inputs, err := run(host, s, uri)
	if err != nil {
		return nil, err
	}
	docs, err := parseFiles(out)
	if err != nil {
		return nil, err
	}
	literal, err := r.literalInputs(store, out)
	if err != nil {
		return nil, err
	}
	store.Put(req, gencache.Output{Inputs: append(inputs, literal...), Body: joinFiles(out)})
	return docs, nil
}

// run calls the handler with the output it writes to and the import's path.
func run(host *buildhost.Host, s *ir.Scheme, uri string) (*buildhost.Output, []gencache.Input, error) {
	env, err := interp.BuildEnv(s.Pkg, "")
	if err != nil {
		return nil, nil, fmt.Errorf("scheme %q: %w", s.Name, err)
	}
	env.SetBuildHost(host)
	out := &buildhost.Output{}
	host.Writing(out)
	rec := buildhost.NewRecorder()
	restore := host.Recording(rec)

	fn := s.Handler.Func
	var args []any
	if len(fn.Params) > 0 {
		t := fn.Params[0].Type
		var def *ir.StructDef
		if t != nil {
			def, _ = t.Decl.(*ir.StructDef)
		}
		args = append(args, interp.NewStruct(def, t))
	}
	if len(fn.Params) > 1 {
		args = append(args, uri)
	}
	_, err = env.CallUserFuncValues(fn, args)
	inputs, ferr := rec.Finish()
	restore()
	if raised, ok := errors.AsType[*interp.RaisedError](err); ok {
		msg, _ := raised.Event["message"].(string)
		return nil, nil, fmt.Errorf("scheme %q: %s", s.Name, msg)
	}
	if err != nil && !interp.IsReturn(err) {
		return nil, nil, err
	}
	if ferr != nil {
		return nil, nil, ferr
	}
	return out, inputs, nil
}

// literalInputs reads the directive each written file carries, if any, with
// what each input leaves out filled in now. A relative path is the import
// root's, as a read's is.
func (r *Runner) literalInputs(store *gencache.Store, out *buildhost.Output) ([]gencache.Input, error) {
	var all []gencache.Input
	for _, name := range out.Names {
		ins, err := gencache.LiteralInputs([]byte(out.Files[name]))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, in := range ins {
			for i, p := range in.Props {
				if (p.Name == "path" || p.Name == "dir") && p.Value != "" && !filepath.IsAbs(p.Value) && r.Root != "" {
					in.Props[i].Value = filepath.Join(r.Root, filepath.FromSlash(p.Value))
				}
			}
			got, err := store.Complete(in)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			all = append(all, got)
		}
	}
	return all, nil
}

func parseFiles(out *buildhost.Output) ([]*ast.Document, error) {
	names := append([]string(nil), out.Names...)
	sort.Strings(names)
	var docs []*ast.Document
	for _, name := range names {
		doc, err := parser.Parse(name, []byte(out.Files[name]))
		if err != nil {
			return nil, fmt.Errorf("the plugin wrote %s, which does not parse: %w", name, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// joinFiles is what is stored: each file after a line naming it.
func joinFiles(out *buildhost.Output) []byte {
	names := append([]string(nil), out.Names...)
	sort.Strings(names)
	var b bytes.Buffer
	for _, name := range names {
		b.WriteString(fileMark + name + "\n")
		src := out.Files[name]
		b.WriteString(src)
		if !strings.HasSuffix(src, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

func splitFiles(body []byte) *buildhost.Output {
	out := &buildhost.Output{Files: map[string]string{}}
	var cur string
	var b strings.Builder
	flush := func() {
		if cur != "" {
			out.Names = append(out.Names, cur)
			out.Files[cur] = b.String()
		}
		b.Reset()
	}
	for line := range strings.Lines(string(body)) {
		if name, ok := strings.CutPrefix(line, fileMark); ok {
			flush()
			cur = strings.TrimSpace(name)
			continue
		}
		b.WriteString(line)
	}
	flush()
	return out
}
