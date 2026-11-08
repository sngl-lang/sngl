package c

import (
	"slices"
	"strings"
)

// file is what a C file needs ahead of the code a renderer writes into it:
// the headers it includes and the types it defines, which C wants declared
// before their first use.
type file struct {
	includes []string
	// types are the type definitions in the order they are needed: each is
	// written after the types it holds by value.
	types []*typeDef
	// helpers are functions the code calls that no declaration of the
	// program's is: a struct's equality. Each is defined once, by name, after
	// every type.
	helpers     []string
	helperOrder []string
	helperNames map[string]bool
	// protos are every function's prototype, which C wants ahead of a call,
	// and lifted the definitions of the functions the renderer lifts out of
	// an expression: a lambda's body.
	protos []string
	lifted []string
	// seq numbers what the renderer names: each lifted function, each
	// temporary.
	seq int
	err error
}

// typeDef is one type definition: its name, the text that defines it, and
// the types that text holds by value, which C wants defined first.
type typeDef struct {
	name string
	text string
	deps []string
}

func newFile() *file { return &file{} }

// include adds a header, `<name>` where system, else `"name"`, once.
func (f *file) include(name string, system bool) {
	inc := `"` + name + `"`
	if system {
		inc = "<" + name + ">"
	}
	if !slices.Contains(f.includes, inc) {
		f.includes = append(f.includes, inc)
	}
}

// define adds a type definition, once by name.
func (f *file) define(name, text string, deps ...string) {
	if slices.ContainsFunc(f.types, func(d *typeDef) bool { return d.name == name }) {
		return
	}
	f.types = append(f.types, &typeDef{name: name, text: text, deps: deps})
}

// setHelper gives the helper name, reserved by an empty definition while a
// helper it calls was defined, its text. It keeps its place, after the helper
// it calls.
func (f *file) setHelper(name, text string) {
	for i, h := range f.helpers {
		if h == "" && f.helperOrder[i] == name {
			f.helpers[i] = text
			return
		}
	}
}

// defined reports whether a type of name has a definition.
func (f *file) defined(name string) bool {
	return slices.ContainsFunc(f.types, func(d *typeDef) bool { return d.name == name })
}

// redefine replaces name's definition, keeping its place.
func (f *file) redefine(name, text string, deps ...string) {
	for _, d := range f.types {
		if d.name == name {
			d.text, d.deps = text, deps
		}
	}
}

// helper adds a function's definition, once by name.
func (f *file) helper(name, text string) {
	if f.helperNames == nil {
		f.helperNames = map[string]bool{}
	}
	if f.helperNames[name] {
		return
	}
	f.helperNames[name] = true
	f.helpers = append(f.helpers, text)
	f.helperOrder = append(f.helperOrder, name)
}

// typeSection is every type definition, each after what it holds by value:
// the order of first definition otherwise, so that the output is stable.
func (f *file) typeSection() string {
	byName := map[string]*typeDef{}
	for _, d := range f.types {
		byName[d.name] = d
	}
	done := map[string]bool{}
	var b strings.Builder
	var visit func(d *typeDef)
	visit = func(d *typeDef) {
		if done[d.name] {
			return
		}
		done[d.name] = true
		for _, dep := range d.deps {
			if dd, ok := byName[dep]; ok {
				visit(dd)
			}
		}
		b.WriteString(d.text)
		b.WriteString("\n")
	}
	for _, d := range f.types {
		visit(d)
	}
	return b.String()
}

// next is a number no name the renderer made before has.
func (f *file) next() int {
	f.seq++
	return f.seq
}
