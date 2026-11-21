package golang

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// A Go build calling C through purego (ir.CCallsPurego) has no cgo: each C
// function it calls is a func variable of the program's (puregoVar), which
// puregoFile's init binds to the function of that name in the library its
// c.link names, and a call hands it Go's own values. purego copies a string
// to C and back; a handle C holds opaquely is an unsafe.Pointer; a number is
// the Go type of the C one's size.

// puregoFile is the Go file binding a purego build's C functions.
const puregoFile = "sngl_purego.go"

// cloadPath is the package that opens a library and binds its functions.
const cloadPath = "duckfam.us/sngl/pkg/go/cload"

// puregoScalars is the Go type of each C scalar purego crosses, by its C
// name: of the size an LP64 system gives it, where `long` is 64 bits.
var puregoScalars = map[string]string{
	"char":               "int8",
	"signed char":        "int8",
	"unsigned char":      "uint8",
	"short":              "int16",
	"unsigned short":     "uint16",
	"int":                "int32",
	"unsigned":           "uint32",
	"unsigned int":       "uint32",
	"long":               "int64",
	"unsigned long":      "uint64",
	"long long":          "int64",
	"unsigned long long": "uint64",
	"size_t":             "uintptr",
	"float":              "float32",
	"double":             "float64",
	"int8_t":             "int8",
	"uint8_t":            "uint8",
	"int16_t":            "int16",
	"uint16_t":           "uint16",
	"int32_t":            "int32",
	"uint32_t":           "uint32",
	"int64_t":            "int64",
	"uint64_t":           "uint64",
}

// puregoType is the Go type a value of t crosses purego as, alias naming the
// C type a declaration writes for it, or why it cannot cross.
//
// A value C holds (sngl:x/c's held) crosses as the uintptr cload.Hold
// answers, which is no address, and a release as the C function
// cload.Release answers: puregoParamType.
func puregoType(t *ir.Type, alias *ir.Alias) (string, error) {
	if alias.IsCHeld() {
		return "uintptr", nil
	}
	if alias != nil && alias.C != nil {
		if alias.C.Ptr == 0 {
			if g, ok := puregoScalars[alias.C.Name]; ok {
				return g, nil
			}
		}
		return "", fmt.Errorf("purego cannot tell the size of a C %s", alias.C.Name)
	}
	if t == nil {
		return "", fmt.Errorf("purego crosses no value of no type")
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeBool:
		return "int32", nil
	case ir.TypeFloat:
		return "float64", nil
	case ir.TypeString:
		return "string", nil
	case ir.TypeStruct:
		if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Foreign.C != nil && !sd.IsCStruct() && sd.Foreign.C.Ptr > 0 {
			return "unsafe.Pointer", nil
		}
		if ir.CStructOf(t) != nil {
			return "", fmt.Errorf("purego crosses no C struct by value (%s)", t)
		}
	case ir.TypeRef:
		return "", fmt.Errorf("purego hands C no address to fill (a ref %s)", t.Elems[0])
	case ir.TypeFunc:
		return "", fmt.Errorf("purego's callbacks are never given back, and a program makes them without end")
	}
	return "", fmt.Errorf("purego crosses no %s", t)
}

// puregoParamType is puregoType for p, a C function's parameter: a release
// is the uintptr purego's callbacks are.
func puregoParamType(p *ir.Param) (string, error) {
	if p.CRelease {
		return "uintptr", nil
	}
	return puregoType(p.Type, p.CAlias)
}

// puregoVar is the func variable a purego build calls fn through.
func puregoVar(fn *ir.Func) string { return "snglC_" + fn.Foreign.Name }

// puregoCall is a call of fn through its variable, each argument converted
// to the Go type purego crosses it as and the result back to SNGL's.
func (gc *GoIRContext) puregoCall(fn *ir.Func, args []string) string {
	off := len(args) - len(fn.Params)
	conv := slices.Clone(args)
	for i, p := range fn.Params {
		if off+i < 0 {
			continue
		}
		g, _ := puregoParamType(p)
		a := args[off+i]
		switch {
		case p.CAlias.IsCHeld():
			gc.RequireImport(cloadPath)
			a = "cload.Hold(" + a + ")"
		case p.CRelease:
			a = "cload.Release()"
		case p.Type.Kind == ir.TypeBool:
			a = "func() int32 { if " + a + " { return 1 }; return 0 }()"
		case g != "string" && g != "unsafe.Pointer":
			a = g + "(" + a + ")"
		}
		conv[off+i] = a
	}
	call := puregoVar(fn) + "(" + strings.Join(conv[max(off, 0):], ", ") + ")"
	if fn.Return == nil {
		return call
	}
	switch fn.Return.Kind {
	case ir.TypeInt:
		return "int(" + call + ")"
	case ir.TypeFloat:
		return "float64(" + call + ")"
	case ir.TypeBool:
		return "(" + call + " != 0)"
	case ir.TypeOption:
		if fn.ReturnCAlias.IsCHeld() {
			gc.RequireImport(cloadPath)
			return gc.heldOut(fn.Return, "cload.Held("+call+")")
		}
	}
	return call
}

// puregoSources is the file binding each C function pkg calls (puregoFile),
// or why one cannot be called through purego: a package whose c.link ships
// C to compile, which purego has no compiler for; one that names no library
// to open; and a function crossing what purego cannot.
func puregoSources(pkg *ir.Package, generated map[string][]byte) (map[string][]byte, error) {
	var fns []*ir.Func
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil && c.Func.Foreign.Path == ir.CPath && !c.Func.IsCCast() && !slices.Contains(fns, c.Func) {
			fns = append(fns, c.Func)
		}
		return nil
	})
	if len(fns) == 0 {
		return nil, nil
	}
	slices.SortFunc(fns, func(a, b *ir.Func) int { return strings.Compare(a.Foreign.Name, b.Foreign.Name) })
	var vars, binds strings.Builder
	unsafe := false
	for _, fn := range fns {
		lib, err := puregoLibrary(fn)
		if err != nil {
			return nil, err
		}
		if fn.Foreign.Field {
			return nil, fmt.Errorf("%s is a member of a C struct (field): purego opens a C function by its name, and a member has none", fn.Foreign.Name)
		}
		if i := slices.IndexFunc(fn.Params, (*ir.Param).CIntPointer); i >= 0 {
			return nil, fmt.Errorf("%s takes %s as the integer %s (#[c.type]): purego casts no pointer to an integer, which would keep nothing it points to for the call", fn.Foreign.Name, fn.Params[i].Name, fn.Params[i].CFuncType)
		}
		if freed := fn.CFreed(); len(freed) > 0 {
			return nil, fmt.Errorf("%s hands over a string %s frees (#[c.frees]): purego copies a string C answers and hands back no pointer to free", fn.Foreign.Name, freed[0])
		}
		var params []string
		for _, p := range fn.Params {
			g, err := puregoParamType(p)
			if err != nil {
				return nil, fmt.Errorf("%s takes %s: %w", fn.Foreign.Name, p.Name, err)
			}
			unsafe = unsafe || g == "unsafe.Pointer"
			params = append(params, g)
		}
		ret := ""
		if fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
			g, err := puregoType(fn.Return, fn.ReturnCAlias)
			if err != nil {
				return nil, fmt.Errorf("%s returns %s: %w", fn.Foreign.Name, fn.Return, err)
			}
			unsafe = unsafe || g == "unsafe.Pointer"
			ret = " " + g
		}
		fmt.Fprintf(&vars, "var %s func(%s)%s\n", puregoVar(fn), strings.Join(params, ", "), ret)
		fmt.Fprintf(&binds, "\tcload.Bind(&%s, %q, %q)\n", puregoVar(fn), lib, fn.Foreign.Name)
	}
	name, err := generatedPackage(generated)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Code generated by sngl; DO NOT EDIT.\n\npackage " + name + "\n\n")
	if unsafe {
		b.WriteString("import (\n\t\"unsafe\"\n\n\t\"" + cloadPath + "\"\n)\n\n")
	} else {
		b.WriteString("import \"" + cloadPath + "\"\n\n")
	}
	b.WriteString(vars.String())
	b.WriteString("\nfunc init() {\n" + binds.String() + "}\n")
	return map[string][]byte{puregoFile: []byte(b.String())}, nil
}

// puregoLibrary is the library fn is opened from: the one its package's
// c.link names (`library=`).
func puregoLibrary(fn *ir.Func) (string, error) {
	if fn.Pkg == nil {
		return "", fmt.Errorf("%s: purego opens the library a c.link names, and nothing declares this function", fn.Foreign.Name)
	}
	var lib string
	var sources []string
	for _, l := range fn.Pkg.CLinks {
		for _, s := range l.Sources {
			sources = append(sources, s.Name)
		}
		if lib == "" {
			lib = l.Library
		}
	}
	if len(sources) > 0 {
		return "", fmt.Errorf("%s: purego compiles no C, and the package declaring it ships %s", fn.Foreign.Name, strings.Join(sources, ", "))
	}
	if lib == "" {
		return "", fmt.Errorf("%s: purego opens the library a c.link names (library=…), and the package declaring it names none", fn.Foreign.Name)
	}
	return lib, nil
}
