// Package lookup resolves SNGL documentation targets — the same resolution
// `sngl doc` performs on the CLI — for any front-end (CLI, TUI, web, GUI).
//
// Callers invoke Lookup(path, idents...) where path matches `sngl doc <path>`
// semantics (stdlib keyword, local dir, scheme URI, import alias) and idents
// walk into the resolved package (type.method, struct.field, component.prop,
// enum.member). The returned Result is a discriminated union keyed by Kind.
//
// The package also exposes Index, which enumerates every package reachable
// from the cwd (stdlib, current package, and each aliased import) so UIs can
// present a package picker that delegates back to Lookup.
package lookup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// Kind tags which of Result's pointer fields is populated.
type Kind int

const (
	KindIndex     Kind = iota + 1 // a package / stdlib / native-import index
	KindComponent                 // a component (stdlib or user-defined)
	KindType                      // a struct, including primitive receivers
	KindEnum
	KindFunc
	KindValue  // const or var
	KindProp   // component prop or event
	KindField  // struct field
	KindMember // enum member
)

type Result struct {
	Kind      Kind
	Index     *DeclIndex
	Component *ComponentDetail
	Type      *TypeDetail
	Enum      *EnumDetail
	Func      *FuncDetail
	Value     *ValueDetail
	Prop      *PropDetail
	Field     *FieldDetail
	Member    *MemberDetail
}

type DeclIndex struct {
	Title       string
	Description string
	Components  []DeclSummary
	Types       []TypeEntry // structs + primitive receivers; methods inline
	Enums       []DeclSummary
	Constants   []DeclSummary
	Data        []DeclSummary
	Functions   []DeclSummary // free functions (no receiver)
	Macros      []DeclSummary // `#[...]` marks: free funcs returning Macro
	Overrides   []DeclSummary // sngl.* platform overrides
	Library     bool
	Native      *ir.NativeImport // non-nil for scheme-native packages
	// Packages is set instead of the declaration sections when the target is
	// the library itself: `sngl` names the tree, not a package with members.
	Packages []PackageEntry
}

// Doc is the raw comment; FirstSentence trims it for a compact blurb.
type DeclSummary struct {
	Name string
	Doc  string
}

type TypeEntry struct {
	Name    string
	Doc     string
	Methods []MethodEntry
}

type MethodEntry struct {
	ShortName string // "darken"
	FullName  string // "color.darken"
	Doc       string
}

type ComponentDetail struct {
	Name     string
	Doc      string
	Schema   *checker.ComponentSchema // stdlib schema when known
	AST      *ast.ComponentDecl       // user-defined AST (nil for stdlib)
	Examples []string                 // raw example sources (stdlib only)
}

type TypeDetail struct {
	Name    string
	Doc     string
	Struct  *ast.StructDef // nil for primitive receivers (int, float, string, list)
	Native  *ir.StructDef  // non-nil for scheme-native structs
	Methods []MethodEntry
}

type EnumDetail struct {
	Name   string
	Doc    string
	AST    *ast.EnumDef
	Native *ir.EnumDef
}

type FuncDetail struct {
	// Pkg is the import path's URI, for the resolved return type of a body
	// that could not annotate one.
	Pkg    string
	Name   string
	Doc    string
	AST    *ast.FuncDef
	Native *ir.Func
}

type ValueDetail struct {
	Name    string
	Doc     string
	AST     ast.Stmt // *ast.ConstDecl or *ast.VarDecl
	Native  *ir.Var
	IsConst bool
}

type PropDetail struct {
	Component string
	Name      string
	Schema    *checker.PropSchema // non-nil for prop; nil for event
	Event     string              // payload type when this is an event (Schema is nil)
	// Slot is the slot's type expression when this names a slot rather than a
	// prop or an event. Nothing else distinguishes the three: a slot is a
	// parameter of the component like the other two, and `sngl doc ui vbox
	// children` has to resolve rather than report an unknown prop.
	Slot ast.TypeExpr
}

type FieldDetail struct {
	Type string
	Name string
	Expr ast.TypeExpr // the field's type expression
	Doc  string
}

type MemberDetail struct {
	Enum string
	Name string
	Doc  string
}

type PackageKind int

const (
	PackageCurrent PackageKind = iota + 1 // cwd's own package
	PackageLibrary                        // the embedded SNGL library, addressed as "sngl"
	PackageLocal                          // ./subdir, ../other
	PackageScheme                         // go:…, file:…, etc.
)

// Path is a valid first argument to Lookup.
type PackageRef struct {
	Title string
	Alias string
	Path  string
	Kind  PackageKind
}

// The factory builds a checker.ImportResolver per cwd, for scheme-based paths
// (go:, git://, …). Callers that only query the stdlib or local directories
// don't need to register one.
//
//sngl:pure
func RegisterResolver(f func(cwd string) checker.ImportResolver) { resolverFactory = f }

var resolverFactory func(cwd string) checker.ImportResolver

var ErrNotFound = errors.New("doc target not found")

// Lookup is LookupIn scoped to the process cwd.
//
//sngl:pure
func Lookup(path string, idents ...string) (Result, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return LookupIn(cwd, path, idents...)
}

// Results are memoized by (cwd, path, idents).
//
//sngl:pure
func LookupIn(cwd, path string, idents ...string) (Result, error) {
	if res, err, ok := cachedLookup(cwd, path, idents); ok {
		return res, err
	}
	res, err := lookupInUncached(cwd, path, idents)
	storeLookup(cwd, path, idents, res, err)
	return res, err
}

func lookupInUncached(cwd, path string, idents []string) (Result, error) {
	tgt, err := resolveTarget(cwd, path)
	if err != nil {
		return Result{}, err
	}
	if len(idents) == 0 {
		return Result{Kind: KindIndex, Index: buildIndex(tgt)}, nil
	}
	// The library root declares nothing, so a name has to be asked of the
	// package that declares it. FindInLibrary is what turns a bare name into
	// that package.
	if tgt.tree {
		origins := FindInLibrary(idents[0])
		if len(origins) == 1 {
			return LookupIn(cwd, origins[0].Pkg, idents...)
		}
		return Result{}, fmt.Errorf("%w: %q is a package of the library, not a declaration in one", ErrNotFound, idents[0])
	}
	if tgt.native != nil {
		return walkNative(tgt, idents)
	}
	return walkSNGL(tgt, idents)
}

// Index is IndexIn scoped to the process cwd.
//
//sngl:pure
func Index() []PackageRef {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return IndexIn(cwd)
}

// IndexIn enumerates the stdlib, the cwd's own package, and each aliased
// import declared in the cwd's .sngl files. Memoized per cwd.
//
//sngl:pure
func IndexIn(cwd string) []PackageRef {
	if v, ok := cachedIndex(cwd); ok {
		return v
	}
	refs := indexInUncached(cwd)
	storeIndex(cwd, refs)
	return refs
}

func indexInUncached(cwd string) []PackageRef {
	refs := []PackageRef{
		{Title: filepath.Base(mustAbs(cwd)), Path: ".", Kind: PackageCurrent},
		{Title: "sngl", Path: "sngl", Kind: PackageLibrary},
	}
	doc, err := parseDir(cwd)
	if err != nil {
		return refs
	}
	seen := map[string]bool{}
	for _, stmt := range doc.Stmts {
		imp, ok := stmt.(*ast.Import)
		if !ok {
			continue
		}
		path := imp.Path
		if imp.Replace != "" {
			path = imp.Replace
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		alias := imp.Alias
		// A dot import binds no name, so "." is not a title. Fall back to the
		// path-derived name, same as an import with no alias at all.
		if alias == "" || alias == "." {
			alias = checker.NamespaceFromPath(path)
		}
		kind := PackageLocal
		if scheme, _ := checker.ParseScheme(path); scheme != "" {
			kind = PackageScheme
		}
		refs = append(refs, PackageRef{Title: alias, Alias: imp.Alias, Path: path, Kind: kind})
	}
	return refs
}

// Exactly one of (pd, native) is non-nil.
type target struct {
	title  string
	pd     *checker.PackageDocs
	stmts  []ast.Stmt
	native *ir.NativeImport
	// library marks a package of the embedded SNGL library, addressed by the
	// sngl scheme. It affects how the title renders, nothing else: there is no
	// "the standard library" any more, only packages under one scheme.
	library bool
	// pkg is the import path's URI -- `ui`, `ui/draw`, `platform/html` -- which
	// is what checker.PackageSchema is keyed by. Empty for a target that is not
	// a loadable package (a directory, a native import), whose declarations
	// carry no resolved schema either.
	pkg string
	// tree marks the library root, `sngl`, which lists its packages rather
	// than declaring anything itself.
	tree bool
}

func resolveTarget(cwd, path string) (*target, error) {
	scheme, uri := checker.ParseScheme(path)

	// Bare `sngl` is the library root: the packages under the scheme, each
	// addressed by its own path. It used to be every one of them merged into a
	// single listing, which could not say where a declaration came from and
	// could not hold two packages declaring one name.
	if path == "sngl" {
		return &target{title: "sngl", library: true, tree: true}, nil
	}

	if scheme == "sngl" {
		if !checker.HasPackage(uri) && len(providedPackageDocs(uri)) == 0 {
			return nil, fmt.Errorf("unknown stdlib package %q (have: %s)", uri, strings.Join(checker.Packages(), ", "))
		}
		pd, stmts := stdlibPackageDocs(uri)
		return &target{
			title:   "sngl:" + uri,
			pkg:     uri,
			pd:      pd,
			stmts:   stmts,
			library: true,
		}, nil
	}

	if scheme != "" {
		if resolverFactory == nil {
			return nil, fmt.Errorf("scheme %q requires a registered resolver", scheme)
		}
		resolver := resolverFactory(cwd)
		docs, _, err := resolver.ResolveSchemeFS(scheme, uri, cwd)
		if err != nil {
			return nil, err
		}
		if len(docs) > 0 {
			return mergeDocsTarget(path, docs), nil
		}
		native, err := resolver.ResolveScheme(scheme, uri, cwd)
		if err != nil {
			return nil, err
		}
		if native != nil {
			return &target{title: path, native: native}, nil
		}
		return nil, fmt.Errorf("scheme %q could not resolve %q", scheme, uri)
	}

	candidate := path
	if !filepath.IsAbs(path) && cwd != "" {
		candidate = filepath.Join(cwd, path)
	}
	if dir := resolvePackageDir(candidate); dir != "" {
		doc, err := parseDir(dir)
		if err != nil {
			return nil, err
		}
		title := path
		if path == "." || path == "" {
			title = filepath.Base(mustAbs(dir))
		}
		return &target{title: title, pd: checker.ExtractPackageDocs(doc), stmts: doc.Stmts}, nil
	}

	// Built-in platform / language names (android, html, fyne, bubbletea, go,
	// kotlin, ...) — resolve via the codegen registry so they share the same
	// Lookup code paths as the stdlib and scheme imports.
	//
	for _, tier := range []string{"platform", "language"} {
		uri := tier + "/" + path
		if !isRegisteredTarget(tier, path) {
			continue
		}
		if docs := checker.PackageSource(uri); len(docs) > 0 {
			t := mergeDocsTarget(path, docs)
			t.pkg = uri
			return t, nil
		}
	}

	if cwd != "" {
		if doc, err := parseDir(cwd); err == nil {
			for _, stmt := range doc.Stmts {
				imp, ok := stmt.(*ast.Import)
				if !ok {
					continue
				}
				alias := imp.Alias
				if alias == "" {
					alias = checker.NamespaceFromPath(imp.Path)
				}
				if alias != path {
					continue
				}
				resolved := imp.Path
				if imp.Replace != "" {
					resolved = imp.Replace
				}
				if resolved == path {
					break // self-loop guard
				}
				return resolveTarget(cwd, resolved)
			}
		}
	}

	return nil, ErrNotFound
}

func mergeDocsTarget(title string, docs []*ast.Document) *target {
	merged := &checker.PackageDocs{}
	var stmts []ast.Stmt
	for _, d := range docs {
		pd := checker.ExtractPackageDocs(d)
		merged.Components = append(merged.Components, pd.Components...)
		merged.Structs = append(merged.Structs, pd.Structs...)
		merged.Enums = append(merged.Enums, pd.Enums...)
		merged.Consts = append(merged.Consts, pd.Consts...)
		merged.Data = append(merged.Data, pd.Data...)
		merged.Functions = append(merged.Functions, pd.Functions...)
		stmts = append(stmts, d.Stmts...)
	}
	return &target{title: title, pd: merged, stmts: stmts}
}

func buildIndex(tgt *target) *DeclIndex {
	idx := &DeclIndex{Title: tgt.title, Library: tgt.library, Native: tgt.native}
	switch {
	case tgt.tree:
		idx.Description = "The packages of the embedded library, each imported by its own path. " +
			"Only `sngl:builtin` is in scope without an import."
		for _, p := range LibraryPackages() {
			if p.Kind == "library" {
				idx.Packages = append(idx.Packages, p)
			}
		}
		return idx
	case tgt.library && tgt.pd != nil && tgt.pd.Doc != "":
		idx.Description = tgt.pd.Doc
	case tgt.library:
		// No package comment in lib/<name>/. Say how to import it, which is
		// the one thing true of every library package.
		idx.Description = "Import it to bring its declarations into scope: `import . " + quote(tgt.title) +
			"` to write them unqualified, or `import <alias> " + quote(tgt.title) + "` to qualify them."
	}
	if tgt.native != nil {
		populateNativeIndex(idx, tgt.native)
		return idx
	}
	if tgt.pd == nil {
		return idx
	}

	// One entry per overridden component, not per override: a stdlib
	// component is overridden by every platform that implements it, and each
	// declares the override under the same `sngl.<name>`. They are the same
	// page -- the href is built from the name -- so a second one is a
	// collision rather than a second subject.
	seenOverride := map[string]bool{}
	for _, d := range tgt.pd.Components {
		s := DeclSummary{Name: d.Name, Doc: d.Doc}
		if strings.HasPrefix(d.Name, "sngl.") {
			if seenOverride[d.Name] {
				continue
			}
			seenOverride[d.Name] = true
			idx.Overrides = append(idx.Overrides, s)
		} else {
			idx.Components = append(idx.Components, s)
		}
	}

	userStructs := tgt.pd.Structs

	methods, free := groupFunctionsByReceiver(tgt.pd.Functions)
	idx.Types = buildTypeEntries(userStructs, methods)

	for _, d := range tgt.pd.Enums {
		idx.Enums = append(idx.Enums, DeclSummary{Name: d.Name, Doc: d.Doc})
	}
	for _, d := range tgt.pd.Consts {
		idx.Constants = append(idx.Constants, DeclSummary{Name: d.Name, Doc: d.Doc})
	}
	for _, d := range tgt.pd.Data {
		idx.Data = append(idx.Data, DeclSummary{Name: d.Name, Doc: d.Doc})
	}
	for _, d := range free {
		s := DeclSummary{Name: d.Name, Doc: d.Doc}
		if fd, ok := d.Decl.(*ast.FuncDef); ok && checker.IsMacroDecl(fd) {
			idx.Macros = append(idx.Macros, s)
			continue
		}
		idx.Functions = append(idx.Functions, s)
	}

	sortByName(idx.Components)
	sortByName(idx.Enums)
	sortByName(idx.Constants)
	sortByName(idx.Data)
	sortByName(idx.Functions)
	sortByName(idx.Macros)
	sortByName(idx.Overrides)
	return idx
}

func buildTypeEntries(structs []checker.DeclInfo, methods map[string][]checker.DeclInfo) []TypeEntry {
	named := map[string]DeclSummary{}
	for _, s := range structs {
		named[s.Name] = DeclSummary{Name: s.Name, Doc: s.Doc}
	}
	for recv := range methods {
		if _, ok := named[recv]; !ok {
			named[recv] = DeclSummary{Name: recv}
		}
	}
	if len(named) == 0 {
		return nil
	}
	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]TypeEntry, 0, len(names))
	for _, n := range names {
		d := named[n]
		entry := TypeEntry{Name: d.Name, Doc: d.Doc}
		ms := methods[n]
		sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
		for _, m := range ms {
			short := strings.TrimPrefix(m.Name, n+".")
			entry.Methods = append(entry.Methods, MethodEntry{
				ShortName: short,
				FullName:  m.Name,
				Doc:       m.Doc,
			})
		}
		out = append(out, entry)
	}
	return out
}

func groupFunctionsByReceiver(funcs []checker.DeclInfo) (methods map[string][]checker.DeclInfo, free []checker.DeclInfo) {
	methods = map[string][]checker.DeclInfo{}
	for _, f := range funcs {
		if i := strings.Index(f.Name, "."); i > 0 {
			methods[f.Name[:i]] = append(methods[f.Name[:i]], f)
			continue
		}
		free = append(free, f)
	}
	return
}

func populateNativeIndex(idx *DeclIndex, ni *ir.NativeImport) {
	for _, s := range ni.Structs {
		idx.Types = append(idx.Types, TypeEntry{Name: s.Name, Doc: s.Doc})
	}
	for _, e := range ni.Enums {
		idx.Enums = append(idx.Enums, DeclSummary{Name: e.Name, Doc: e.Doc})
	}
	for _, f := range ni.Funcs {
		idx.Functions = append(idx.Functions, DeclSummary{Name: f.Name, Doc: f.Doc})
	}
	for _, v := range ni.Vars {
		if v.IsConst {
			idx.Constants = append(idx.Constants, DeclSummary{Name: v.Name, Doc: v.Doc})
		} else {
			idx.Data = append(idx.Data, DeclSummary{Name: v.Name, Doc: v.Doc})
		}
	}
	sort.Slice(idx.Types, func(i, j int) bool { return idx.Types[i].Name < idx.Types[j].Name })
	sortByName(idx.Enums)
	sortByName(idx.Functions)
	sortByName(idx.Constants)
	sortByName(idx.Data)
}

func walkSNGL(tgt *target, idents []string) (Result, error) {
	if len(idents) > 2 {
		return Result{}, fmt.Errorf("too many identifiers: %v", idents)
	}
	declName := idents[0]
	info := tgt.pd.FindDecl(declName)

	// Primitive receivers (int, float, string, list) have no decl but carry
	// methods via dot-prefixed function names.
	if info == nil {
		if res, ok := synthesizedReceiver(tgt, declName, idents[1:]); ok {
			return res, nil
		}
	}

	// Support dotted leaf form: "color.darken" passed as a single ident.
	if info == nil && len(idents) == 1 {
		if before, after, ok := strings.Cut(declName, "."); ok {
			if parent := tgt.pd.FindDecl(before); parent != nil {
				return narrow(tgt, parent, after)
			}
			if res, ok := synthesizedReceiver(tgt, before, []string{after}); ok {
				return res, nil
			}
		}
	}
	if info == nil {
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, declName)
	}
	if len(idents) == 1 {
		return primary(tgt, info)
	}
	return narrow(tgt, info, idents[1])
}

// Returns (_, false) when the receiver has no methods.
func synthesizedReceiver(tgt *target, recv string, rest []string) (Result, bool) {
	methods, _ := groupFunctionsByReceiver(tgt.pd.Functions)
	ms, ok := methods[recv]
	if !ok {
		return Result{}, false
	}
	if len(rest) == 0 {
		td := &TypeDetail{Name: recv}
		sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
		for _, m := range ms {
			short := strings.TrimPrefix(m.Name, recv+".")
			td.Methods = append(td.Methods, MethodEntry{ShortName: short, FullName: m.Name, Doc: m.Doc})
		}
		return Result{Kind: KindType, Type: td}, true
	}
	want := recv + "." + rest[0]
	if fn := tgt.pd.FindDecl(want); fn != nil {
		res, err := primary(tgt, fn)
		if err != nil {
			return Result{}, false
		}
		return res, true
	}
	return Result{}, false
}

func primary(tgt *target, info *checker.DeclInfo) (Result, error) {
	switch decl := info.Decl.(type) {
	case *ast.ComponentDecl:
		cd := &ComponentDetail{Name: info.Name, Doc: info.Doc, AST: decl}
		if schema, ok := checker.PackageSchema(tgt.pkg)[info.Name]; ok {
			cd.Schema = schema
		}
		if srcs, ok := checker.PackageExamples(tgt.pkg)[info.Name]; ok {
			cd.Examples = srcs
		}
		return Result{Kind: KindComponent, Component: cd}, nil
	case *ast.StructDef:
		td := &TypeDetail{Name: info.Name, Doc: info.Doc, Struct: decl}
		methods, _ := groupFunctionsByReceiver(tgt.pd.Functions)
		for _, m := range methods[info.Name] {
			short := strings.TrimPrefix(m.Name, info.Name+".")
			td.Methods = append(td.Methods, MethodEntry{ShortName: short, FullName: m.Name, Doc: m.Doc})
		}
		sort.Slice(td.Methods, func(i, j int) bool { return td.Methods[i].ShortName < td.Methods[j].ShortName })
		return Result{Kind: KindType, Type: td}, nil
	case *ast.EnumDef:
		return Result{Kind: KindEnum, Enum: &EnumDetail{Name: info.Name, Doc: info.Doc, AST: decl}}, nil
	case *ast.FuncDef:
		return Result{Kind: KindFunc, Func: &FuncDetail{Pkg: tgt.pkg, Name: info.Name, Doc: info.Doc, AST: decl}}, nil
	case *ast.ConstDecl:
		return Result{Kind: KindValue, Value: &ValueDetail{Name: info.Name, Doc: info.Doc, AST: decl, IsConst: true}}, nil
	case *ast.VarDecl:
		return Result{Kind: KindValue, Value: &ValueDetail{Name: info.Name, Doc: info.Doc, AST: decl}}, nil
	default:
		return Result{}, fmt.Errorf("unsupported decl kind %T for %s", info.Decl, info.Name)
	}
}

// narrow walks one ident deeper into a top-level decl.
func narrow(tgt *target, info *checker.DeclInfo, ident string) (Result, error) {
	switch decl := info.Decl.(type) {
	case *ast.ComponentDecl:
		if schema, ok := checker.PackageSchema(tgt.pkg)[info.Name]; ok {
			if ps, ok := schema.Props[ident]; ok {
				return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident, Schema: &ps}}, nil
			}
			if payload, ok := schema.Events[ident]; ok {
				return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident, Event: payload}}, nil
			}
			for _, sl := range schema.Slots {
				if sl.Name == ident {
					return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident, Slot: sl.Type}}, nil
				}
			}
			return Result{}, fmt.Errorf("%w: prop %q on component %s", ErrNotFound, ident, info.Name)
		}
		for _, p := range decl.Props.Props {
			switch pp := p.(type) {
			case ast.Param:
				if pp.Name != ident {
					continue
				}
				if pp.IsSlot() {
					return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident, Slot: pp.Type}}, nil
				}
				return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident}}, nil
			case ast.EventDecl:
				if pp.Name == ident {
					payload := ""
					if pp.Type != nil {
						payload = parser.FormatType(pp.Type)
					}
					return Result{Kind: KindProp, Prop: &PropDetail{Component: info.Name, Name: ident, Event: payload}}, nil
				}
			}
		}
		return Result{}, fmt.Errorf("%w: prop %q on component %s", ErrNotFound, ident, info.Name)
	case *ast.StructDef:
		for _, f := range decl.Fields() {
			if slices.Contains(f.Names, ident) {
				return Result{Kind: KindField, Field: &FieldDetail{Type: info.Name, Name: ident, Expr: f.Type}}, nil
			}
		}
		// Fall back: struct.method resolves to a FuncDef named "Struct.method".
		if fn := tgt.pd.FindDecl(info.Name + "." + ident); fn != nil {
			return primary(tgt, fn)
		}
		return Result{}, fmt.Errorf("%w: field %q on struct %s", ErrNotFound, ident, info.Name)
	case *ast.EnumDef:
		for _, m := range decl.Members() {
			if m.Name == ident {
				return Result{Kind: KindMember, Member: &MemberDetail{Enum: info.Name, Name: ident}}, nil
			}
		}
		return Result{}, fmt.Errorf("%w: member %q on enum %s", ErrNotFound, ident, info.Name)
	}
	return Result{}, fmt.Errorf("cannot narrow into %T (%s)", info.Decl, info.Name)
}

func walkNative(tgt *target, idents []string) (Result, error) {
	if len(idents) > 1 {
		return Result{}, fmt.Errorf("native imports support at most one identifier: %v", idents)
	}
	name := idents[0]
	for _, s := range tgt.native.Structs {
		if s.Name == name {
			return Result{Kind: KindType, Type: &TypeDetail{Name: s.Name, Doc: s.Doc, Native: s}}, nil
		}
	}
	for _, e := range tgt.native.Enums {
		if e.Name == name {
			return Result{Kind: KindEnum, Enum: &EnumDetail{Name: e.Name, Doc: e.Doc, Native: e}}, nil
		}
	}
	for _, f := range tgt.native.Funcs {
		if f.Name == name {
			return Result{Kind: KindFunc, Func: &FuncDetail{Name: f.Name, Doc: f.Doc, Native: f}}, nil
		}
	}
	for _, v := range tgt.native.Vars {
		if v.Name == name {
			return Result{Kind: KindValue, Value: &ValueDetail{Name: v.Name, Doc: v.Doc, Native: v, IsConst: v.IsConst}}, nil
		}
	}
	return Result{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// FirstSentence trims a doc string at the first ". " boundary, or the first line.
func FirstSentence(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	if i := strings.Index(doc, ". "); i > 0 {
		return doc[:i+1]
	}
	if i := strings.IndexByte(doc, '\n'); i > 0 {
		return strings.TrimSpace(doc[:i])
	}
	return doc
}

// isRegisteredTarget reports whether path names a registered platform or
// language, and one usable here: an unavailable platform contributes no
// package, which is a whole-package decision made by the plugin.
func isRegisteredTarget(tier, path string) bool {
	switch tier {
	case "platform":
		return len(codegen.PlatformDocs(codegen.LookupPlatform(path))) > 0
	case "language":
		return len(codegen.LangDocs(codegen.LookupLang(path))) > 0
	}
	return false
}

// providedPackageDocs is the source a registered target provides for its own
// library package. A target carries its package rather than lib/ holding it, so
// a package that exists only because a plugin is registered has to resolve
// here the way it does in the checker.
func providedPackageDocs(pkg string) []*ast.Document {
	if name, ok := strings.CutPrefix(pkg, "platform/"); ok {
		return checker.ProvidedDocs(codegen.LookupPlatform(name))
	}
	if name, ok := strings.CutPrefix(pkg, "language/"); ok {
		return checker.ProvidedDocs(codegen.LookupLang(name))
	}
	return nil
}

// The one file a package's prose is read from. Go's semantics — every file's
// package comment counts, concatenated in load order — cannot say which order,
// and the blank line that separates a package comment from a declaration
// comment is easy to leave in by accident: three files in lib/remote opened
// with a file header and the package read as whichever the directory listed
// first. So a package holding a doc.sngl is documented by it alone.
//
// A package with no doc.sngl keeps go's semantics, which costs nothing: the
// packages in that position are the single-file ones a target serves for
// itself (`sngl:platform/html`, `sngl:language/go`), where there is no order
// to leave unpinned. lib/packagedoc_test.go holds lib/ to the stricter rule.
const packageDocFile = "doc.sngl"

// documentFile is the file a parsed document came from. An *ast.Document does
// not carry one, so it is recovered from the first statement's position —
// parseStdlibDocs and ProvidedDocs both parse under the base name.
func documentFile(doc *ast.Document) string {
	if len(doc.Stmts) == 0 {
		return ""
	}
	p := doc.Stmts[0].StmtPos()
	if p == nil {
		return ""
	}
	return filepath.Base(p.File)
}

// packageProse is one package's own description, read from packageDocFile
// alone when the package has one.
func packageProse(src []*ast.Document) string {
	fromDoc := slices.ContainsFunc(src, func(d *ast.Document) bool { return documentFile(d) == packageDocFile })
	var prose []string
	for _, d := range src {
		if fromDoc && documentFile(d) != packageDocFile {
			continue
		}
		if doc := checker.PackageDoc(d); doc != "" {
			prose = append(prose, doc)
		}
	}
	return strings.Join(prose, "\n\n")
}

func stdlibPackageDocs(pkgs ...string) (*checker.PackageDocs, []ast.Stmt) {
	merged := &checker.PackageDocs{}
	var stmts []ast.Stmt
	var docs []*ast.Document
	var prose []string
	for _, pkg := range pkgs {
		// PackageSource, not the two halves separately: a mark is read off the
		// loaded IR and its declaration is then found here by pointer, and two
		// parses of one file never share one. Reading a target's provided
		// source fresh is why the #[options] struct of every target package
		// was classified as an ordinary user type.
		src := checker.PackageSource(pkg)
		docs = append(docs, src...)
		if p := packageProse(src); p != "" {
			prose = append(prose, p)
		}
	}
	merged.Doc = strings.Join(prose, "\n\n")
	for _, doc := range docs {
		pd := checker.ExtractPackageDocs(doc)
		merged.Components = append(merged.Components, pd.Components...)
		merged.Structs = append(merged.Structs, pd.Structs...)
		merged.Enums = append(merged.Enums, pd.Enums...)
		merged.Consts = append(merged.Consts, pd.Consts...)
		merged.Data = append(merged.Data, pd.Data...)
		merged.Functions = append(merged.Functions, pd.Functions...)
		stmts = append(stmts, doc.Stmts...)
	}
	sortDecls := func(items []checker.DeclInfo) {
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	}
	sortDecls(merged.Components)
	sortDecls(merged.Structs)
	sortDecls(merged.Enums)
	sortDecls(merged.Consts)
	sortDecls(merged.Data)
	sortDecls(merged.Functions)
	return merged, stmts
}

func resolvePackageDir(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return path
	}
	if strings.HasSuffix(path, ".sngl") {
		return filepath.Dir(path)
	}
	return ""
}

func parseDir(dir string) (*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .sngl files in %s", dir)
	}
	var merged *ast.Document
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		d, err := parser.Parse(filepath.Base(path), data)
		if err != nil {
			continue
		}
		if merged == nil {
			merged = d
			continue
		}
		merged.Stmts = append(merged.Stmts, d.Stmts...)
	}
	if merged == nil {
		return nil, fmt.Errorf("no parseable .sngl files in %s", dir)
	}
	return merged, nil
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func sortByName(xs []DeclSummary) {
	sort.Slice(xs, func(i, j int) bool { return xs[i].Name < xs[j].Name })
}

func quote(s string) string { return "\"" + s + "\"" }

// LibraryOrigin is a library package that declares a given name.
type LibraryOrigin struct {
	Pkg     string // package path, e.g. "sngl:ui/draw"
	Ambient bool   // in scope without an import
	Kind    string // "component", "type", "func", …
}

// ImportLine is how a program brings this origin's declarations into scope,
// or "" when it needs no import.
func (o LibraryOrigin) ImportLine() string {
	if o.Ambient {
		return ""
	}
	return `import . ` + quote(o.Pkg)
}

// FindInLibrary reports every public library package declaring name. Only
// sngl:builtin is in scope without an import, so the caller has to know which
// package it found and what importing it costs; two packages may declare the
// same name, and the caller decides between them.
func FindInLibrary(name string) []LibraryOrigin {
	var out []LibraryOrigin
	for _, pkg := range lib.PublicPackages() {
		pd, _ := stdlibPackageDocs(pkg)
		info := pd.FindDecl(name)
		if info == nil {
			continue
		}
		out = append(out, LibraryOrigin{
			Pkg:     "sngl:" + pkg,
			Ambient: pkg == "builtin",
			Kind:    declKindName(pd, name),
		})
	}
	return out
}

func declKindName(pd *checker.PackageDocs, name string) string {
	for _, c := range pd.Components {
		if c.Name == name {
			return "component"
		}
	}
	for _, s := range pd.Structs {
		if s.Name == name {
			return "type"
		}
	}
	for _, e := range pd.Enums {
		if e.Name == name {
			return "enum"
		}
	}
	for _, f := range pd.Functions {
		if f.Name == name {
			return "func"
		}
	}
	return "declaration"
}
