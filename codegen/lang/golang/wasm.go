//go:build !js

package golang

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
)

// BuildWASM compiles a Go package to WASM with JS bindings for the specified functions.
func (t *Translator) BuildWASM(projectDir, importPath string, funcs []codegen.WASMFunc) ([]byte, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	dirName := ".sngl-wasm-" + filepath.Base(importPath)
	tmpDir := filepath.Join(absProject, dirName)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating wasm temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	src := generateGoWASMBridge(importPath, funcs)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(src), 0o644); err != nil {
		return nil, fmt.Errorf("writing wasm bridge: %w", err)
	}

	wasmPath := filepath.Join(tmpDir, "output.wasm")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	slog.Info("exec", "cmd", "go build (wasm)", "pkg", importPath, "dir", absProject)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", wasmPath, "./"+dirName+"/")
	cmd.Dir = absProject
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compiling wasm for %s: %s\n%s", importPath, err, out)
	}

	return os.ReadFile(wasmPath)
}

// WASMExecJS returns Go's wasm_exec.js from GOROOT.
func (t *Translator) WASMExecJS() ([]byte, error) {
	gorootOut, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return nil, fmt.Errorf("finding GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(gorootOut))
	return os.ReadFile(filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"))
}

func generateGoWASMBridge(importPath string, funcs []codegen.WASMFunc) string {
	needsMarshal := false
	for _, fn := range funcs {
		if isComplexBridgeType(fn.ReturnType) {
			needsMarshal = true
			break
		}
		if slices.ContainsFunc(fn.ParamTypes, isComplexBridgeType) {
			needsMarshal = true
		}
		if needsMarshal {
			break
		}
	}

	var b strings.Builder
	b.WriteString("//go:build js && wasm\n\n")
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"syscall/js\"\n")
	if needsMarshal {
		b.WriteString("\t\"encoding/json\"\n")
		b.WriteString("\t\"reflect\"\n")
		b.WriteString("\t\"strings\"\n")
	}
	fmt.Fprintf(&b, "\tpkg %q\n", importPath)
	b.WriteString(")\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("\text := js.Global().Get(\"__sngl_externs\")\n")
	b.WriteString("\tif ext.IsUndefined() {\n")
	b.WriteString("\t\text = js.ValueOf(map[string]any{})\n")
	b.WriteString("\t\tjs.Global().Set(\"__sngl_externs\", ext)\n")
	b.WriteString("\t}\n\n")

	for _, fn := range funcs {
		fmt.Fprintf(&b, "\text.Set(%q, js.FuncOf(func(this js.Value, args []js.Value) any {\n", fn.Name)

		var goArgs []string
		for i, pt := range fn.ParamTypes {
			argName := fmt.Sprintf("a%d", i)
			switch pt {
			case "string":
				fmt.Fprintf(&b, "\t\t%s := args[%d].String()\n", argName, i)
			case "int":
				fmt.Fprintf(&b, "\t\t%s := args[%d].Int()\n", argName, i)
			case "float":
				fmt.Fprintf(&b, "\t\t%s := args[%d].Float()\n", argName, i)
			case "bool":
				fmt.Fprintf(&b, "\t\t%s := args[%d].Bool()\n", argName, i)
			case "":
				fmt.Fprintf(&b, "\t\t%s := args[%d]\n", argName, i)
			default:
				fmt.Fprintf(&b, "\t\tvar %s %s\n", argName, pt)
				fmt.Fprintf(&b, "\t\tsngl_fromJS(args[%d], &%s)\n", i, argName)
			}
			goArgs = append(goArgs, argName)
		}

		call := fmt.Sprintf("pkg.%s(%s)", fn.Name, strings.Join(goArgs, ", "))
		switch {
		case fn.ReturnType == "" && !fn.HasErrorReturn:
			fmt.Fprintf(&b, "\t\t%s\n", call)
			b.WriteString("\t\treturn nil\n")
		case fn.ReturnType == "" && fn.HasErrorReturn:
			fmt.Fprintf(&b, "\t\tif err := %s; err != nil {\n", call)
			b.WriteString("\t\t\tjs.Global().Get(\"console\").Call(\"error\", err.Error())\n")
			b.WriteString("\t\t}\n")
			b.WriteString("\t\treturn nil\n")
		case fn.HasErrorReturn:
			fmt.Fprintf(&b, "\t\tresult, err := %s\n", call)
			b.WriteString("\t\tif err != nil {\n")
			b.WriteString("\t\t\tjs.Global().Get(\"console\").Call(\"error\", err.Error())\n")
			b.WriteString("\t\t\treturn nil\n")
			b.WriteString("\t\t}\n")
			if isComplexBridgeType(fn.ReturnType) {
				b.WriteString("\t\treturn sngl_toJS(result)\n")
			} else {
				b.WriteString("\t\treturn result\n")
			}
		case isComplexBridgeType(fn.ReturnType):
			fmt.Fprintf(&b, "\t\tresult := %s\n", call)
			b.WriteString("\t\treturn sngl_toJS(result)\n")
		default:
			fmt.Fprintf(&b, "\t\tresult := %s\n", call)
			b.WriteString("\t\treturn result\n")
		}

		b.WriteString("\t}))\n\n")
	}

	b.WriteString("\tselect {}\n")
	b.WriteString("}\n")

	if needsMarshal {
		b.WriteString(wasmMarshalHelpers)
	}
	return b.String()
}

// isComplexBridgeType reports whether the given Go type expression needs
// JSON-based marshalling at the bridge boundary (i.e. it is neither a
// primitive scalar nor the empty "raw js.Value" sentinel).
func isComplexBridgeType(t string) bool {
	switch t {
	case "", "string", "int", "float", "bool":
		return false
	}
	return true
}

const wasmMarshalHelpers = `
// sngl_fromJS decodes a js.Value into the Go value pointed to by dst by
// round-tripping through JSON. JSON unmarshalling matches struct fields
// case-insensitively, so JS objects keyed with SNGL's lower-cased field
// names hydrate Go structs whose fields use Go's PascalCase convention.
func sngl_fromJS(v js.Value, dst any) {
	s := js.Global().Get("JSON").Call("stringify", v).String()
	_ = json.Unmarshal([]byte(s), dst)
}

// sngl_toJS converts a Go value into a js.ValueOf-compatible shape, lowering
// struct field names to match the SNGL-side naming convention so emitted JS
// can read fields as obj.fieldName rather than obj.FieldName.
func sngl_toJS(v any) any {
	return sngl_reflectToJS(reflect.ValueOf(v))
}

func sngl_reflectToJS(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return sngl_reflectToJS(rv.Elem())
	case reflect.Struct:
		m := map[string]any{}
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			m[sngl_lowerFirst(f.Name)] = sngl_reflectToJS(rv.Field(i))
		}
		return m
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = sngl_reflectToJS(rv.Index(i))
		}
		return out
	case reflect.Map:
		m := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			var key string
			if k.Kind() == reflect.String {
				key = k.String()
			} else {
				key = ""
			}
			m[key] = sngl_reflectToJS(iter.Value())
		}
		return m
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	}
	return nil
}

func sngl_lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}
`
