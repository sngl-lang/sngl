// Command win32gen writes lib/platform/win32/api/api.sngl, the Win32
// declarations sngl:platform/win32 calls, as `#[cnative]` SNGL.
//
// What is declared comes from win32json, a JSON rendering of Microsoft's
// win32metadata, at a pinned commit: each function, struct and constant, and
// which pointer parameters are in, out or optional. How C spells each type
// comes from MinGW's own headers, preprocessed for both architectures: the
// metadata says a parameter is a UInt32, where C says UINT or DWORD, and cgo
// refuses one for the other. So the tool needs both MinGW compilers and the
// network, and runs by hand, never in verify; a test holds only that the
// committed file checks and is formatted.
//
// It writes what the wanted lists name and what those reach, never all of
// user32, and lists at the end of the file each wanted declaration it could
// not write, with the reason: a call that needs what C interop does not cross
// stays in the platform's C runtime.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"duckfam.us/sngl"
	"duckfam.us/sngl/internal/parser"
)

// commit is the win32json commit the declarations are written from.
const commit = "071df4995290b72320010ecfc2fca51946b5c6b4"

// partitions are the win32json files the wanted declarations live in.
var partitions = []string{
	"Foundation",
	"UI.WindowsAndMessaging",
	"Graphics.Gdi",
	"UI.Controls",
	"System.LibraryLoader",
	"UI.Input.KeyboardAndMouse",
}

// functions are the C functions wanted, by their C name.
var functions = []string{
	"DestroyWindow",
	"EnableWindow",
	"GetClientRect",
	"GetFocus",
	"GetModuleHandleW",
	"GetParent",
	"GetSysColor",
	"GetSystemMetrics",
	"GetWindowRect",
	"GetWindowTextLengthW",
	"InvalidateRect",
	"IsWindowEnabled",
	"IsWindowVisible",
	"MoveWindow",
	"PostMessageW",
	"SendMessageW",
	"SetFocus",
	"SetWindowTextW",
	"ShowWindow",
	"UpdateWindow",
	"CreateSolidBrush",
	"DeleteObject",
	"SetBkColor",
	"SetBkMode",
	"SetTextColor",
}

// structs are the C structs wanted beyond those a wanted function reaches.
var structs = []string{"RECT", "POINT", "SIZE"}

// constants are the prefixes of the constants and enum members wanted.
var constants = []string{
	"BM_", "BN_", "BS_", "BST_",
	"COLOR_",
	"EM_", "EN_", "ES_",
	"GWL_", "GWLP_",
	"SM_",
	"SS_",
	"SW_", "SWP_",
	"TRANSPARENT", "OPAQUE",
	"WM_",
	"WS_", "WS_EX_",
}

func main() {
	out := flag.String("o", "lib/platform/win32/api/api.sngl", "file to write")
	cache := flag.String("json", "", "a directory holding win32json's api/*.json at the pinned commit, rather than fetching them")
	flag.Parse()
	if err := run(*out, *cache); err != nil {
		fmt.Fprintln(os.Stderr, "win32gen:", err)
		os.Exit(1)
	}
}

func run(out, cache string) error {
	meta := &metadata{types: map[string]jsonType{}, funcs: map[string]jsonFunc{}}
	for _, p := range partitions {
		data, err := partition(p, cache)
		if err != nil {
			return err
		}
		if err := meta.add(data); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	headers := map[string]*header{}
	for _, cc := range []string{"x86_64-w64-mingw32-gcc", "i686-w64-mingw32-gcc"} {
		h, err := preprocess(cc)
		if err != nil {
			return err
		}
		headers[cc] = h
	}
	g := &gen{meta: meta, h64: headers["x86_64-w64-mingw32-gcc"], h32: headers["i686-w64-mingw32-gcc"], aliases: map[string]string{}, handles: map[string]bool{}, structs: map[string]bool{}}
	src, err := g.write()
	if err != nil {
		return err
	}
	doc, err := parser.Parse(filepath.Base(out), []byte(src))
	if err != nil {
		return fmt.Errorf("the written file does not parse: %w\n%s", err, src)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, []byte(sngl.Format(doc)), 0o644)
}

// partition is one win32json file, from cache or fetched at the commit.
func partition(name, cache string) ([]byte, error) {
	if cache != "" {
		return os.ReadFile(filepath.Join(cache, "api", name+".json"))
	}
	url := "https://raw.githubusercontent.com/marlersoft/win32json/" + commit + "/api/" + name + ".json"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// --- win32json ---

type typeRef struct {
	Kind  string
	Name  string
	Child *typeRef
}

type jsonParam struct {
	Name string
	Type typeRef
	// Attrs are strings ("In", "Out", "Optional", "Const") and objects
	// (a buffer's size), of which only the strings are read.
	Attrs []json.RawMessage
}

// has says the parameter carries the string attribute attr.
func (p jsonParam) has(attr string) bool {
	for _, a := range p.Attrs {
		var s string
		if json.Unmarshal(a, &s) == nil && s == attr {
			return true
		}
	}
	return false
}

type jsonFunc struct {
	Name          string
	ReturnType    typeRef
	Architectures []string
	Params        []jsonParam
}

type jsonType struct {
	Name          string
	Kind          string
	Architectures []string
	Values        []struct {
		Name  string
		Value json.Number
	}
}

type jsonConst struct {
	Name      string
	ValueType string
	Value     json.RawMessage
}

type metadata struct {
	types  map[string]jsonType
	funcs  map[string]jsonFunc
	consts []constant
}

type constant struct {
	name, value string
}

func (m *metadata) add(data []byte) error {
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	var part struct {
		Constants []jsonConst
		Types     []jsonType
		Functions []jsonFunc
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&part); err != nil {
		return err
	}
	for _, f := range part.Functions {
		m.funcs[f.Name] = f
	}
	for _, t := range part.Types {
		if _, ok := m.types[t.Name]; !ok || len(t.Architectures) == 0 {
			m.types[t.Name] = t
		}
		if t.Kind == "Enum" {
			for _, v := range t.Values {
				m.consts = append(m.consts, constant{v.Name, v.Value.String()})
			}
		}
	}
	for _, c := range part.Constants {
		switch c.ValueType {
		case "Byte", "SByte", "Int16", "UInt16", "Int32", "UInt32", "Int64", "UInt64":
			var n json.Number
			if err := json.Unmarshal(c.Value, &n); err != nil {
				continue
			}
			m.consts = append(m.consts, constant{c.Name, n.String()})
		}
	}
	return nil
}

// --- MinGW's headers ---

// header is windows.h and commctrl.h preprocessed by one MinGW compiler: each
// function's prototype and each typedef, as C spells them.
type header struct {
	protos   map[string]proto
	typedefs map[string]string
	structs  map[string][]field
}

type proto struct {
	ret    string
	params []field
}

type field struct {
	ctype, name string
}

func preprocess(cc string) (*header, error) {
	cmd := exec.Command(cc, "-E", "-P", "-x", "c", "-")
	cmd.Stdin = strings.NewReader("#include <windows.h>\n#include <commctrl.h>\n")
	text, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s -E: %w", cc, err)
	}
	h := &header{protos: map[string]proto{}, typedefs: map[string]string{}, structs: map[string][]field{}}
	src := attrs.ReplaceAllString(string(text), "")
	for _, m := range prototype.FindAllStringSubmatch(src, -1) {
		ret, name, params := strings.TrimSpace(m[1]), m[2], strings.TrimSpace(m[3])
		var p proto
		p.ret = ret
		if params != "void" && params != "" {
			for part := range strings.SplitSeq(params, ",") {
				p.params = append(p.params, declarator(part))
			}
		}
		h.protos[name] = p
	}
	for _, m := range typedefLine.FindAllStringSubmatch(src, -1) {
		decls := strings.Split(m[1], ",")
		// `typedef long LONG,*PLONG;`: each later declarator's stars are its
		// own, on the first's type with the first's stars taken off.
		first := declarator(decls[0])
		h.typedefs[first.name] = first.ctype
		base := strings.TrimSpace(strings.TrimRight(first.ctype, "* "))
		for _, d := range decls[1:] {
			d = strings.TrimSpace(d)
			name := strings.TrimSpace(strings.TrimLeft(d, "* "))
			h.typedefs[name] = strings.TrimSpace(base + " " + strings.Repeat("*", strings.Count(d, "*")))
		}
	}
	for _, m := range structDef.FindAllStringSubmatch(src, -1) {
		var fields []field
		for line := range strings.SplitSeq(m[1], ";") {
			if line = strings.TrimSpace(line); line != "" {
				fields = append(fields, declarator(line))
			}
		}
		// `} RECT,*PRECT,*LPRECT;`: the first is the struct, and a starred
		// one a pointer to it.
		names := strings.Split(m[2], ",")
		first := strings.TrimSpace(names[0])
		h.structs[first] = fields
		for _, name := range names[1:] {
			name = strings.TrimSpace(name)
			if stars := strings.Count(name, "*"); stars > 0 {
				h.typedefs[strings.TrimSpace(strings.TrimLeft(name, "* "))] = first + " " + strings.Repeat("*", stars)
			} else {
				h.structs[name] = fields
			}
		}
	}
	return h, nil
}

var (
	attrs       = regexp.MustCompile(`__attribute__\(\((?:[^()]|\([^()]*\))*\)\)|__declspec\([^)]*\)|__extension__`)
	prototype   = regexp.MustCompile(`(?m)^\s*([A-Za-z_][\w \*]*?)\s+(\w+)\s*\(([^()]*)\);`)
	typedefLine = regexp.MustCompile(`(?m)(?:^|[;}])\s*typedef\s+([^{};()]+);`)
	structDef   = regexp.MustCompile(`typedef\s+struct\s+\w*\s*\{([^{}]*)\}\s*([^;]+);`)
)

// declarator splits `const RECT *lprc` into its type and its name.
func declarator(s string) field {
	s = strings.Join(strings.Fields(s), " ")
	i := strings.LastIndexAny(s, " *")
	if i < 0 {
		return field{ctype: s}
	}
	return field{ctype: strings.TrimSpace(s[:i+1]), name: strings.TrimSpace(s[i+1:])}
}

// resolve follows typedefs to the C type a name stands for.
func (h *header) resolve(t string) string {
	for range 16 {
		t = strings.TrimSpace(strings.TrimPrefix(t, "const "))
		next, ok := h.typedefs[t]
		if !ok {
			return t
		}
		t = next
	}
	return t
}

var integers = []string{
	"char", "signed char", "unsigned char", "short", "unsigned short", "short int", "short unsigned int",
	"int", "unsigned int", "unsigned", "long", "unsigned long", "long int", "long unsigned int",
	"long long", "unsigned long long", "long long int", "long long unsigned int", "wchar_t",
}

// --- SNGL ---

type gen struct {
	meta     *metadata
	h64, h32 *header
	// aliases are the scalar typedefs written, by name, and handles the
	// opaque pointer typedefs.
	aliases map[string]string
	handles map[string]bool
	structs map[string]bool
	skipped []string
}

// kind is what a C type is to SNGL: "int", "float", "bool", "handle",
// "alias", "struct", "utf16" or "" for what does not cross.
func (g *gen) kind(ctype string) string {
	t := strings.TrimSpace(strings.TrimPrefix(ctype, "const "))
	switch t {
	case "LPCWSTR", "PCWSTR":
		return "utf16"
	case "int":
		return "int"
	case "WINBOOL", "BOOL":
		return "bool"
	case "float", "double":
		return "float"
	}
	if g.h64.structs[t] != nil && g.meta.types[t].Kind == "Struct" {
		return "struct"
	}
	r := g.h64.resolve(t)
	if slices.Contains(integers, r) {
		if t != r && isIdent(t) {
			return "alias"
		}
		return ""
	}
	if strings.HasSuffix(r, "*") && isIdent(t) && (strings.HasPrefix(r, "struct ") && strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(r, "*")), "__") || r == "void *" && g.meta.types[t].Kind == "NativeTypedef") {
		return "handle"
	}
	return ""
}

// pointee is the struct a pointer type points at, if it is a wanted one.
func (g *gen) pointee(ctype string) (string, bool) {
	t := strings.TrimSpace(strings.TrimPrefix(ctype, "const "))
	r := t
	if !strings.HasSuffix(r, "*") {
		r = g.h64.resolve(t)
	}
	if !strings.HasSuffix(r, "*") {
		return "", false
	}
	base := strings.TrimSpace(strings.TrimSuffix(r, "*"))
	base = strings.TrimPrefix(base, "struct ")
	for name, fields := range g.h64.structs {
		if (name == base || "tag"+name == base) && fields != nil && g.meta.types[name].Kind == "Struct" {
			return name, true
		}
	}
	return "", false
}

func isIdent(s string) bool { return regexp.MustCompile(`^[A-Za-z_]\w*$`).MatchString(s) }

// sngl is the SNGL type a C type is written as, recording what it reaches.
func (g *gen) sngl(ctype string) (string, bool) {
	t := strings.TrimSpace(strings.TrimPrefix(ctype, "const "))
	switch g.kind(t) {
	case "utf16":
		return "c.utf16", true
	case "int":
		return "int", true
	case "bool":
		return "bool", true
	case "float":
		return "float", true
	case "alias":
		g.aliases[t] = "int"
		return t, true
	case "handle":
		g.handles[t] = true
		return t, true
	case "struct":
		if g.structFields(t) {
			return t, true
		}
	}
	return "", false
}

// structFields records struct name as written, if each of its fields
// crosses.
func (g *gen) structFields(name string) bool {
	if g.structs[name] {
		return true
	}
	fields := g.h64.structs[name]
	if fields == nil {
		return false
	}
	for _, f := range fields {
		switch g.kind(f.ctype) {
		case "int", "alias", "float":
		default:
			return false
		}
	}
	for _, f := range fields {
		if g.kind(f.ctype) == "alias" {
			g.aliases[f.ctype] = "int"
		}
	}
	g.structs[name] = true
	return true
}

func (g *gen) write() (string, error) {
	var funcs strings.Builder
	for _, name := range functions {
		text, why := g.function(name)
		if why != "" {
			g.skipped = append(g.skipped, name+": "+why)
			continue
		}
		funcs.WriteString(text)
	}
	for _, name := range structs {
		if !g.structFields(name) {
			g.skipped = append(g.skipped, name+": a field does not cross")
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by win32gen from win32json %s; DO NOT EDIT.\n\n", commit[:12])
	b.WriteString("import c \"sngl:x/c\"\nimport macro \"sngl:macro\"\n\n")
	b.WriteString("c.link(include=\"windows.h\", system=true, library=\"user32\", ldflags=[\"-luser32\", \"-lgdi32\", \"-lcomctl32\"])\n\n")

	b.WriteString("// --- Handles ---\n\n")
	for _, name := range sorted(g.handles) {
		fmt.Fprintf(&b, "#[macro.cnative(%q)]\nstruct %s {}\n\n", name, name)
	}
	b.WriteString("// --- Scalars ---\n\n")
	for _, name := range sorted(g.aliases) {
		fmt.Fprintf(&b, "#[macro.cnative(%q)]\nstruct %s = %s\n\n", name, name, g.aliases[name])
	}
	b.WriteString("// --- Structs ---\n\n")
	for _, name := range sorted(g.structs) {
		fmt.Fprintf(&b, "#[macro.cnative(%q)]\nstruct %s {\n", name, name)
		for _, f := range g.h64.structs[name] {
			t, _ := g.sngl(f.ctype)
			fmt.Fprintf(&b, "\t%s %s\n", param(f.name), t)
		}
		b.WriteString("}\n\n")
	}
	b.WriteString("// --- Constants ---\n\n")
	seen := map[string]bool{}
	consts := slices.Clone(g.meta.consts)
	sort.SliceStable(consts, func(i, j int) bool { return consts[i].name < consts[j].name })
	for _, c := range consts {
		if seen[c.name] || !wantedConst(c.name) || !fitsInt(c.value) {
			continue
		}
		seen[c.name] = true
		fmt.Fprintf(&b, "const %s = %s\n", c.name, c.value)
	}
	b.WriteString("\n// --- Functions ---\n\n")
	b.WriteString(funcs.String())
	if len(g.skipped) > 0 {
		b.WriteString("// Not written, each staying in the platform's C runtime where it is called:\n//\n")
		for _, s := range g.skipped {
			b.WriteString("//   - " + s + "\n")
		}
	}
	return b.String(), nil
}

// function is name's declaration, or why it is not written.
func (g *gen) function(name string) (string, string) {
	meta, ok := g.meta.funcs[name]
	if !ok {
		return "", "not in win32json's partitions"
	}
	if len(meta.Architectures) > 0 && !(slices.Contains(meta.Architectures, "X64") && slices.Contains(meta.Architectures, "X86")) {
		return "", "not declared on both architectures (" + strings.Join(meta.Architectures, ", ") + ")"
	}
	p64, ok := g.h64.protos[name]
	if !ok {
		return "", "no prototype in MinGW's x86_64 headers"
	}
	p32, ok := g.h32.protos[name]
	if !ok {
		return "", "no prototype in MinGW's i686 headers, where it is a macro"
	}
	if !slices.Equal(p64.params, p32.params) || p64.ret != p32.ret {
		return "", "spelled differently on the two architectures"
	}
	if len(p64.params) != len(meta.Params) {
		return "", "MinGW and win32json disagree on its parameters"
	}
	var params []string
	for i, p := range p64.params {
		m := meta.Params[i]
		optional, out := m.has("Optional"), m.has("Out")
		if s, ok := g.pointee(p.ctype); ok && out && !optional {
			if !g.structFields(s) {
				return "", "parameter " + p.name + "'s struct does not cross"
			}
			params = append(params, param(p.name)+" ref["+s+"]")
			continue
		}
		t, ok := g.sngl(p.ctype)
		if !ok {
			return "", "parameter " + p.name + " is " + p.ctype + ", which does not cross"
		}
		// An optional handle is written as a handle: C takes one there, and
		// only NULL, which a SNGL handle cannot be, is lost.
		if optional && t == "c.utf16" {
			t = "option[c.utf16]"
		}
		params = append(params, param(p.name)+" "+t)
	}
	ret := ""
	if p64.ret != "void" {
		t, ok := g.sngl(p64.ret)
		if !ok || t == "c.utf16" {
			return "", "it returns " + p64.ret + ", which does not cross"
		}
		ret = " " + t
		if t == "bool" {
			// C's BOOL is an int the program tests against 0.
			g.aliases[p64.ret] = "int"
			ret = " " + p64.ret
		}
	}
	snglName := strings.TrimSuffix(name, "W")
	if _, ok := g.meta.funcs[snglName+"A"]; !ok || snglName == name {
		snglName = name
	}
	return fmt.Sprintf("#[macro.cnative(%q)]\nfunc %s(%s)%s\n\n", name, snglName, strings.Join(params, ", "), ret), ""
}

// param is a C parameter's name as SNGL may write it.
func param(name string) string {
	if parser.LookupIdent(name) != parser.IDENT {
		return name + "_"
	}
	return name
}

func wantedConst(name string) bool {
	for _, p := range constants {
		if strings.HasPrefix(name, p) && (strings.HasSuffix(p, "_") || name == p) {
			return true
		}
	}
	return false
}

// fitsInt says value is an integer a SNGL int holds.
func fitsInt(value string) bool {
	var n int64
	_, err := fmt.Sscan(value, &n)
	return err == nil && fmt.Sprint(n) == value
}

func sorted[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
