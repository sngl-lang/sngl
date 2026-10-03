package optimize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// reachesHost reports whether fn's body can call sngl:x/gen's host API: a
// build-only intrinsic, directly or through any function or lambda it names.
// It errs toward yes, since a call that reaches the host outside a producer is
// refused rather than recorded.
func reachesHost(fn *ir.Func, memo map[*ir.Func]bool) bool {
	if v, ok := memo[fn]; ok {
		return v
	}
	memo[fn] = false
	found := false
	visit := func(f *ir.Func) {
		if found || f == nil {
			return
		}
		if f.BuildOnly || reachesHost(f, memo) {
			found = true
		}
	}
	ir.Walk(fn.Block, func(n ir.Node) error {
		if found {
			return ir.SkipAll
		}
		switch x := n.(type) {
		case *ir.Call:
			visit(x.Func)
		case *ir.Ident:
			if f, ok := x.Sym.(*ir.Func); ok {
				visit(f)
			}
		case *ir.Lambda:
			visit(x.Func)
		}
		return nil
	})
	memo[fn] = found
	return found
}

// produce folds a call of fn that reaches the host: answered from the store
// when every input the last run recorded still holds and the code that read
// them is the same, and otherwise run with every read recorded and stored.
//
// A host error -- a refusal, a read that failed -- stops the build: nothing
// can make the call at run time instead.
func (ctx *evalCtx) produce(fn *ir.Func, args []any) (any, bool) {
	h := ctx.host
	ow := h.ownerFor(fn)
	req, storable := producerRequest(ow, fn, args)
	memoKey := ""
	if storable {
		memoKey = "produce\x00" + req.Identity + "\x00" + strings.Join(req.Params, "\x00")
		if res, ok := ctx.evalCache().load(memoKey); ok {
			if res.err != nil {
				ctx.fail(res.err)
				return nil, false
			}
			return interp.CloneValue(res.value), true
		}
		if data, ok := ctx.evalCache().genStore().Lookup(req); ok {
			if v, err := decodeStored(data, fn.Return); err == nil {
				ctx.evalCache().store(memoKey, constResult{value: v})
				return v, true
			} else {
				slog.Debug("sngl.eval stored value", "func", fn.Name, "err", err)
			}
		}
	}

	env, err := ctx.interpEnv()
	if err != nil {
		return nil, false
	}
	rec := &recorder{procs: map[*interp.Stream]*process{}}
	outer := h.rec
	h.rec = rec
	copied := make([]any, len(args))
	for i, a := range args {
		copied[i] = interp.CloneValue(a)
	}
	v, err := env.CallUserFuncValues(fn, copied)
	inputs, ferr := rec.finish()
	h.rec = outer
	if outer != nil {
		// A producer folded inside another records into it: the outer value
		// depends on everything the inner one read.
		outer.inputs = append(outer.inputs, inputs...)
	}
	var he *hostError
	switch {
	case errors.As(err, &he):
		if storable {
			ctx.evalCache().store(memoKey, constResult{err: he})
		}
		ctx.fail(he)
		return nil, false
	case err != nil:
		return nil, false
	case ferr != nil:
		ctx.fail(ferr)
		return nil, false
	}
	if storable {
		ctx.evalCache().store(memoKey, constResult{value: v})
		if body, ok := encodeStored(v, fn.Return); ok && outer == nil {
			ctx.evalCache().genStore().Put(req, gencache.Output{Inputs: inputs, Body: body})
		}
	}
	return v, true
}

// fail records the first fatal error of the fold, as a diagnostic at the call
// that failed when it has one.
func (ctx *evalCtx) fail(err error) {
	if ctx.err != nil {
		return
	}
	var he *hostError
	if errors.As(err, &he) && he.pos.IsValid() {
		err = ir.Diagnostic{Pos: he.pos, Msg: he.err.Error(), Severity: ir.Error}
	}
	ctx.err = err
}

// producerRequest names a fold in the store: the package by where it came
// from, the function, and its arguments, behind the closure digest of the
// package's code. An argument with no stored form leaves the fold unstored.
func producerRequest(ow *owner, fn *ir.Func, args []any) (gencache.Request, bool) {
	where := ow.name
	if ow.dir != "" {
		where = "dir:" + ow.dir
	}
	params := []string{where, fn.Name}
	for i, a := range args {
		var t *ir.Type
		if i < len(fn.Params) {
			t = fn.Params[i].Type
		}
		enc, ok := encodeValue(a, t)
		if !ok {
			return gencache.Request{}, false
		}
		b, err := json.Marshal(enc)
		if err != nil {
			return gencache.Request{}, false
		}
		params = append(params, string(b))
	}
	return gencache.Request{Producer: genProducer, Params: params, Identity: ow.closure()}, true
}

// storedPrefix is how a stored value is written: the const it is, holding the
// value's JSON, which the declared return type reads back.
const storedPrefix = "const value = "

func encodeStored(v any, t *ir.Type) ([]byte, bool) {
	enc, ok := encodeValue(v, t)
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(enc)
	if err != nil {
		return nil, false
	}
	return []byte(storedPrefix + strconv.Quote(string(b)) + "\n"), true
}

func decodeStored(data []byte, t *ir.Type) (any, error) {
	line, _, _ := strings.Cut(string(gencache.Body(data)), "\n")
	lit, ok := strings.CutPrefix(line, storedPrefix)
	if !ok {
		return nil, errors.New("not a stored value")
	}
	raw, err := strconv.Unquote(lit)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var j any
	if err := dec.Decode(&j); err != nil {
		return nil, err
	}
	return decodeValue(j, t)
}

// encodeValue is v as JSON data, read by its type. Only what decodeValue can
// read back is encoded; anything else -- a stream, a function, a unit -- says
// no, and the value is computed every build.
func encodeValue(v any, t *ir.Type) (any, bool) {
	if t == nil {
		return nil, false
	}
	switch t.Kind {
	case ir.TypeOption:
		if v == nil {
			return nil, true
		}
		if len(t.Elems) == 0 {
			return nil, false
		}
		inner, ok := encodeValue(v, t.Elems[0])
		return []any{inner}, ok
	case ir.TypeString, ir.TypeEnum:
		s, ok := v.(string)
		return s, ok
	case ir.TypeBool:
		b, ok := v.(bool)
		return b, ok
	case ir.TypeInt:
		switch n := v.(type) {
		case int:
			return strconv.Itoa(n), true
		case uint64:
			return strconv.FormatUint(n, 10), true
		}
		return nil, false
	case ir.TypeFloat:
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		return strconv.FormatFloat(f, 'g', -1, 64), true
	case ir.TypeList:
		xs, ok := v.([]any)
		if !ok || len(t.Elems) == 0 {
			return nil, false
		}
		out := make([]any, len(xs))
		for i, x := range xs {
			if out[i], ok = encodeValue(x, t.Elems[0]); !ok {
				return nil, false
			}
		}
		return out, true
	case ir.TypeMap:
		m, ok := v.(map[string]any)
		if !ok || len(t.Elems) != 2 || t.Elems[0].Kind != ir.TypeString {
			return nil, false
		}
		out := make(map[string]any, len(m))
		for k, x := range m {
			if out[k], ok = encodeValue(x, t.Elems[1]); !ok {
				return nil, false
			}
		}
		return out, true
	case ir.TypeStruct:
		s, ok := v.(*interp.Struct)
		def, _ := t.Decl.(*ir.StructDef)
		if !ok || s == nil || def == nil || len(def.TypeParams) > 0 {
			return nil, false
		}
		out := make(map[string]any, len(def.Fields))
		for _, f := range def.Fields {
			fv, _ := s.Get(f.Name)
			if out[f.Name], ok = encodeValue(fv, f.Type); !ok {
				return nil, false
			}
		}
		return out, true
	}
	return nil, false
}

func decodeValue(j any, t *ir.Type) (any, error) {
	bad := func() (any, error) { return nil, fmt.Errorf("stored %T is not a %s", j, t) }
	switch t.Kind {
	case ir.TypeOption:
		if j == nil {
			return nil, nil
		}
		xs, ok := j.([]any)
		if !ok || len(xs) != 1 {
			return bad()
		}
		return decodeValue(xs[0], t.Elems[0])
	case ir.TypeString, ir.TypeEnum:
		if s, ok := j.(string); ok {
			return s, nil
		}
	case ir.TypeBool:
		if b, ok := j.(bool); ok {
			return b, nil
		}
	case ir.TypeInt:
		if s, ok := j.(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				return n, nil
			}
			if n, err := strconv.ParseUint(s, 10, 64); err == nil {
				return n, nil
			}
		}
	case ir.TypeFloat:
		if s, ok := j.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, nil
			}
		}
	case ir.TypeList:
		xs, ok := j.([]any)
		if !ok {
			return bad()
		}
		out := make([]any, len(xs))
		for i, x := range xs {
			v, err := decodeValue(x, t.Elems[0])
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case ir.TypeMap:
		m, ok := j.(map[string]any)
		if !ok {
			return bad()
		}
		out := make(map[string]any, len(m))
		for k, x := range m {
			v, err := decodeValue(x, t.Elems[1])
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case ir.TypeStruct:
		m, ok := j.(map[string]any)
		def, _ := t.Decl.(*ir.StructDef)
		if !ok || def == nil {
			return bad()
		}
		s := interp.NewStruct(def, t)
		for _, f := range def.Fields {
			x, ok := m[f.Name]
			if !ok {
				return nil, fmt.Errorf("stored %s has no field %s", def.Name, f.Name)
			}
			v, err := decodeValue(x, f.Type)
			if err != nil {
				return nil, err
			}
			s.Fields = append(s.Fields, interp.Field{Name: f.Name, Value: v})
		}
		return s, nil
	}
	return bad()
}
