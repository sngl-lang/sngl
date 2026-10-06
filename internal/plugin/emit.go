package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitProducer names the files a family's @generate handler writes.
const EmitProducer = "sngl.emit"

// Member is one member of an emitted family as the walk read it: the value
// its gen.node gives, and the members it holds.
type Member struct {
	Value    any
	Children []Member
}

// Emit is one host of a family whose gen.emit handles @generate.
type Emit struct {
	// Handler is the gen.emit's @generate handler, and Pkg the package the
	// family's override is written in, or nil for a target package's, which
	// is library source.
	Handler *ir.EventHandler
	Pkg     *ir.Package
	// Program is the package being built: what the handler runs over when
	// the override is the library's.
	Program *ir.Package
	// Key names the host beyond its members: the family and the target.
	Key     []string
	Members []Member
}

// Emit runs the handler over the members, or replays what it wrote for the
// same members last time, and returns the files it wrote by path.
func (r *Runner) Emit(e Emit) (map[string][]byte, error) {
	fn := e.Handler.Func
	var host *buildhost.Host
	if e.Pkg == nil {
		host = buildhost.New(r.Trust, r.Root, e.Program, buildhost.NewOwner(&ir.Package{}, "sngl:", "", "", true))
	} else {
		host = buildhost.New(r.Trust, r.Root, e.Pkg, nil)
	}
	ow := host.OwnerFor(fn)

	var listType, memberType *ir.Type
	if len(fn.Params) > 1 {
		listType = fn.Params[1].Type
		if listType != nil && len(listType.Elems) == 1 {
			memberType = listType.Elems[0]
		}
	}
	var valueType *ir.Type
	if memberType != nil && len(memberType.Elems) == 1 {
		valueType = memberType.Elems[0]
	}

	store := r.Store
	if store == nil {
		store = gencache.Default()
	}
	req, storable := gencache.Request{}, false
	if enc, ok := encodeMembers(e.Members, valueType); ok {
		if b, err := json.Marshal(enc); err == nil {
			params := append(host.Key(ow), e.Key...)
			req = gencache.Request{Producer: EmitProducer, Params: append(params, string(b)), Identity: ow.Closure()}
			storable = true
		}
	}
	if storable {
		if data, ok := store.Lookup(req); ok {
			return filesOf(splitFiles(gencache.Body(data))), nil
		}
	}

	pkg := e.Pkg
	if pkg == nil {
		pkg = e.Program
	}
	env, err := interp.BuildEnv(pkg, "")
	if err != nil {
		return nil, err
	}
	env.SetBuildHost(host)
	out := &buildhost.Output{Paths: true}
	host.Writing(out)
	rec := buildhost.NewRecorder()
	restore := host.Recording(rec)
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
		args = append(args, memberValues(e.Members, memberType))
	}
	_, err = env.CallUserFuncValues(fn, args)
	inputs, ferr := rec.Finish()
	restore()
	if raised, ok := errors.AsType[*interp.RaisedError](err); ok {
		msg, _ := raised.Event["message"].(string)
		return nil, fmt.Errorf("gen.emit @generate: %s", msg)
	}
	if err != nil && !interp.IsReturn(err) {
		return nil, err
	}
	if ferr != nil {
		return nil, ferr
	}
	if storable {
		store.Put(req, gencache.Output{Inputs: inputs, Body: joinFiles(out)})
	} else {
		slog.Debug("sngl.emit not stored: a member's value has no stored form", "key", e.Key)
	}
	return filesOf(out), nil
}

func filesOf(out *buildhost.Output) map[string][]byte {
	files := make(map[string][]byte, len(out.Names))
	for _, name := range out.Names {
		files[name] = []byte(out.Files[name])
	}
	return files
}

// memberValues is the members as the handler is handed them: a list of
// gen.Member structs.
func memberValues(ms []Member, t *ir.Type) []any {
	var def *ir.StructDef
	if t != nil {
		def, _ = t.Decl.(*ir.StructDef)
	}
	out := make([]any, len(ms))
	for i, m := range ms {
		s := interp.NewStruct(def, t)
		s.Fields = []interp.Field{
			{Name: "value", Value: m.Value},
			{Name: "children", Value: memberValues(m.Children, t)},
		}
		out[i] = s
	}
	return out
}

// encodeMembers is the members' stored form: what the request is keyed by.
func encodeMembers(ms []Member, t *ir.Type) (any, bool) {
	out := make([]any, len(ms))
	for i, m := range ms {
		v, ok := optimize.EncodeValue(m.Value, t)
		if !ok {
			return nil, false
		}
		c, ok := encodeMembers(m.Children, t)
		if !ok {
			return nil, false
		}
		out[i] = []any{v, c}
	}
	return out, true
}
