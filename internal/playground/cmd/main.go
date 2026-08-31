//go:build js && wasm

// Command playground exposes the compiler to the browser. It is only the
// syscall/js binding layer: every entry point is a package playground
// function, so what the page runs is what that package's tests cover.
package main

import (
	"encoding/json"
	"syscall/js"

	"git.duckfam.us/jonathan/sngl/internal/playground"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/android"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"
)

func main() {
	js.Global().Set("snglCompile", source(playground.Compile, errObj("html")))
	js.Global().Set("snglFormat", source(playground.Format, errObj("source")))
	js.Global().Set("snglAST", source(playground.ASTDump, errObj("ast")))
	js.Global().Set("snglTargets", js.FuncOf(func(js.Value, []js.Value) any {
		return decode(playground.Targets())
	}))
	js.Global().Set("snglGenerate", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 3 {
			return toJSValue(map[string]any{"files": nil, "error": "usage: snglGenerate(source, platform, lang)"})
		}
		return decode(playground.Generate(args[0].String(), args[1].String(), args[2].String()))
	}))
	js.Global().Set("snglDiagnostics", source(playground.Diagnostics, []any{}))
	js.Global().Set("snglComplete", position(playground.Complete, []any{}))
	js.Global().Set("snglHover", position(playground.Hover, map[string]any{"content": ""}))
	select {}
}

// source adapts a fn taking the editor buffer alone. empty is what a call
// with no buffer returns: the shape the page's script indexes into either way,
// an array where it reads .length and an object where it reads a field.
func source(fn func(string) string, empty any) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return toJSValue(empty)
		}
		return decode(fn(args[0].String()))
	})
}

// errObj is the no-source reply for a fn whose result is an object carrying
// one payload field beside "error".
func errObj(field string) map[string]any {
	return map[string]any{field: "", "error": "no source provided"}
}

// position adapts a fn taking the buffer and a line/column, returning empty on
// a call the editor made before it had a position to send.
func position(fn func(string, int, int) string, empty any) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 3 {
			return toJSValue(empty)
		}
		return decode(fn(args[0].String(), args[1].Int(), args[2].Int()))
	})
}

// decode turns one of playground's JSON replies into the live object the
// page's scripts index into.
func decode(reply string) js.Value {
	var v any
	if err := json.Unmarshal([]byte(reply), &v); err != nil {
		return toJSValue(map[string]any{"error": err.Error()})
	}
	return toJSValue(v)
}

// toJSValue recursively converts Go values to JS values.
func toJSValue(v any) js.Value {
	switch val := v.(type) {
	case nil:
		return js.Null()
	case bool:
		return js.ValueOf(val)
	case int:
		return js.ValueOf(val)
	case float64:
		return js.ValueOf(val)
	case string:
		return js.ValueOf(val)
	case map[string]any:
		obj := js.Global().Get("Object").New()
		for k, v := range val {
			obj.Set(k, toJSValue(v))
		}
		return obj
	case []any:
		arr := js.Global().Get("Array").New(len(val))
		for i, v := range val {
			arr.SetIndex(i, toJSValue(v))
		}
		return arr
	default:
		return js.ValueOf(v)
	}
}
