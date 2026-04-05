//go:build !js

package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
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
	var b strings.Builder
	b.WriteString("//go:build js && wasm\n\n")
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"syscall/js\"\n")
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
			default:
				fmt.Fprintf(&b, "\t\t%s := args[%d]\n", argName, i)
			}
			goArgs = append(goArgs, argName)
		}

		call := fmt.Sprintf("pkg.%s(%s)", fn.Name, strings.Join(goArgs, ", "))
		if fn.ReturnType == "" {
			fmt.Fprintf(&b, "\t\t%s\n", call)
			b.WriteString("\t\treturn nil\n")
		} else {
			fmt.Fprintf(&b, "\t\tresult := %s\n", call)
			b.WriteString("\t\treturn result\n")
		}

		b.WriteString("\t}))\n\n")
	}

	b.WriteString("\tselect {}\n")
	b.WriteString("}\n")
	return b.String()
}
