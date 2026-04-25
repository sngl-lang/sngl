package lookup

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// The types and functions in this file project the Go Result/DeclIndex/*Detail
// types into plain-struct shapes that SNGL components can consume directly.
// All type expressions are pre-formatted to strings.

// Summary is a name + one-line doc pair.
type Summary struct {
	Name string
	Doc  string
}

// MethodView is one method entry folded under a type.
type MethodView struct {
	Short string
	Full  string
	Doc   string
}

// TypeListEntry is a type with its methods inline, for the package index.
type TypeListEntry struct {
	Name    string
	Doc     string
	Methods []MethodView
}

// PackageRefView is a package reachable from the current workspace.
type PackageRefView struct {
	Title string
	Alias string
	Path  string
	Kind  string // "current", "stdlib", "local", "scheme"
}

// PackageView is the sngl-facing shape of DeclIndex.
type PackageView struct {
	Found            bool
	Title            string
	Description      string
	IsStdlib         bool
	IsNative         bool
	NativeImportPath string
	Components       []Summary
	Types            []TypeListEntry
	Enums            []Summary
	Constants        []Summary
	Data             []Summary
	Functions        []Summary
	Overrides        []Summary
	PlatformTypes    []Summary
}

// PropView is a component prop or event in list form.
type PropView struct {
	Name       string
	Type       string
	Doc        string
	EnumValues []string
	IsEvent    bool
	Payload    string
}

// FieldView is one struct field.
type FieldView struct {
	Name string
	Type string
	Doc  string
}

// ParamView is one function parameter.
type ParamView struct {
	Name string
	Type string
}

// ComponentDetailView is the sngl-facing shape of ComponentDetail.
type ComponentDetailView struct {
	Name     string
	Doc      string
	Children string
	Props    []PropView
	Events   []PropView
	Examples []string
}

// TypeDetailView is the sngl-facing shape of TypeDetail.
type TypeDetailView struct {
	Name       string
	Doc        string
	IsNative   bool
	NativeName string
	Fields     []FieldView
	Methods    []MethodView
}

// EnumDetailView is the sngl-facing shape of EnumDetail.
type EnumDetailView struct {
	Name     string
	Doc      string
	IsNative bool
	Members  []Summary
}

// FuncDetailView is the sngl-facing shape of FuncDetail.
type FuncDetailView struct {
	Name       string
	Doc        string
	Signature  string
	Params     []ParamView
	ReturnType string
	IsNative   bool
	NativeName string
}

// ValueDetailView is the sngl-facing shape of ValueDetail.
type ValueDetailView struct {
	Name       string
	Doc        string
	Type       string
	IsConst    bool
	IsNative   bool
	NativeName string
}

// PropDetailView is the sngl-facing shape of PropDetail.
type PropDetailView struct {
	ComponentName string
	Name          string
	Type          string
	Doc           string
	EnumValues    []string
	IsEvent       bool
	Payload       string
}

// FieldDetailView is the sngl-facing shape of FieldDetail.
type FieldDetailView struct {
	Type string
	Name string
	Expr string
	Doc  string
}

// MemberDetailView is the sngl-facing shape of MemberDetail.
type MemberDetailView struct {
	EnumName string
	Name     string
	Doc      string
}

// Entry is the unified envelope returned by Resolve. Kind indicates which
// detail sub-struct is meaningful; the rest are zero. Error carries the human
// message when Found is false. Field names are suffixed "Doc" so none collide
// with SNGL reserved words (component, enum, func, var, const, struct, unit).
type Entry struct {
	Found        bool
	Kind         string // "index" | "component" | "type" | "enum" | "func" | "value" | "prop" | "field" | "member"
	Path         string
	Ident1       string
	Ident2       string
	Error        string
	Index        PackageView
	ComponentDoc ComponentDetailView
	TypeDoc      TypeDetailView
	EnumDoc      EnumDetailView
	FuncDoc      FuncDetailView
	ValueDoc     ValueDetailView
	PropDoc      PropDetailView
	FieldDoc     FieldDetailView
	MemberDoc    MemberDetailView
}

// --- Public sngl entry points ---

// Packages lists every package reachable from the current working directory
// (stdlib, cwd package, and each aliased import).
//
//sngl:pure
func Packages() []PackageRefView {
	refs := Index()
	out := make([]PackageRefView, len(refs))
	for i, r := range refs {
		out[i] = PackageRefView{
			Title: r.Title,
			Alias: r.Alias,
			Path:  r.Path,
			Kind:  packageKindName(r.Kind),
		}
	}
	return out
}

// PackageIndex returns a package outline for the given path, or an empty
// (Found=false) view when the path doesn't resolve.
//
//sngl:pure
func PackageIndex(path string) PackageView {
	if strings.TrimSpace(path) == "" {
		return PackageView{}
	}
	res, err := Lookup(path)
	if err != nil || res.Kind != KindIndex || res.Index == nil {
		return PackageView{}
	}
	return mapIndex(res.Index)
}

// Resolve walks a path + up-to-two idents into a single Entry envelope. Empty
// idents are stripped. When lookup fails, Found is false and Error holds the
// message.
//
//sngl:pure
func Resolve(path, ident1, ident2 string) Entry {
	idents := []string{}
	if s := strings.TrimSpace(ident1); s != "" {
		idents = append(idents, s)
	}
	if s := strings.TrimSpace(ident2); s != "" {
		idents = append(idents, s)
	}
	e := Entry{Path: path, Ident1: ident1, Ident2: ident2}
	if strings.TrimSpace(path) == "" {
		return e
	}
	res, err := Lookup(path, idents...)
	if err != nil {
		e.Error = err.Error()
		return e
	}
	e.Found = true
	switch res.Kind {
	case KindIndex:
		e.Kind = "index"
		e.Index = mapIndex(res.Index)
	case KindComponent:
		e.Kind = "component"
		e.ComponentDoc = mapComponent(res.Component)
	case KindType:
		e.Kind = "type"
		e.TypeDoc = mapType(res.Type)
	case KindEnum:
		e.Kind = "enum"
		e.EnumDoc = mapEnum(res.Enum)
	case KindFunc:
		e.Kind = "func"
		e.FuncDoc = mapFunc(res.Func)
	case KindValue:
		e.Kind = "value"
		e.ValueDoc = mapValue(res.Value)
	case KindProp:
		e.Kind = "prop"
		e.PropDoc = mapProp(res.Prop)
	case KindField:
		e.Kind = "field"
		e.FieldDoc = mapField(res.Field)
	case KindMember:
		e.Kind = "member"
		e.MemberDoc = mapMember(res.Member)
	}
	return e
}

// --- Mapping helpers ---

func packageKindName(k PackageKind) string {
	switch k {
	case PackageCurrent:
		return "current"
	case PackageStdlib:
		return "stdlib"
	case PackageLocal:
		return "local"
	case PackageScheme:
		return "scheme"
	}
	return ""
}

func mapIndex(idx *DeclIndex) PackageView {
	if idx == nil {
		return PackageView{}
	}
	v := PackageView{
		Found:         true,
		Title:         idx.Title,
		Description:   idx.Description,
		IsStdlib:      idx.IsStdlib,
		Components:    mapSummaries(idx.Components),
		Enums:         mapSummaries(idx.Enums),
		Constants:     mapSummaries(idx.Constants),
		Data:          mapSummaries(idx.Data),
		Functions:     mapSummaries(idx.Functions),
		Overrides:     mapSummaries(idx.Overrides),
		PlatformTypes: mapSummaries(idx.PlatformTypes),
	}
	if idx.Native != nil {
		v.IsNative = true
		v.NativeImportPath = idx.Native.ImportPath
	}
	for _, t := range idx.Types {
		entry := TypeListEntry{Name: t.Name, Doc: t.Doc}
		for _, m := range t.Methods {
			entry.Methods = append(entry.Methods, MethodView{
				Short: m.ShortName,
				Full:  m.FullName,
				Doc:   m.Doc,
			})
		}
		v.Types = append(v.Types, entry)
	}
	return v
}

func mapSummaries(in []DeclSummary) []Summary {
	out := make([]Summary, len(in))
	for i, s := range in {
		out[i] = Summary{Name: s.Name, Doc: s.Doc}
	}
	return out
}

func mapComponent(c *ComponentDetail) ComponentDetailView {
	if c == nil {
		return ComponentDetailView{}
	}
	v := ComponentDetailView{Name: c.Name, Doc: c.Doc, Examples: append([]string(nil), c.Examples...)}
	if c.Schema != nil {
		type pe struct {
			name string
			ps   any
		}
		names := make([]string, 0, len(c.Schema.Props))
		for n := range c.Schema.Props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ps := c.Schema.Props[n]
			v.Props = append(v.Props, PropView{
				Name:       n,
				Type:       (&ps.Type).String(),
				Doc:        ps.Doc,
				EnumValues: append([]string(nil), ps.Enum...),
			})
		}
		enames := make([]string, 0, len(c.Schema.Events))
		for n := range c.Schema.Events {
			enames = append(enames, n)
		}
		sort.Strings(enames)
		for _, n := range enames {
			v.Events = append(v.Events, PropView{
				Name:    n,
				IsEvent: true,
				Payload: c.Schema.Events[n],
			})
		}
		if c.Schema.Children != nil {
			v.Children = c.Schema.Children.String()
		}
	}
	if c.AST != nil {
		for _, p := range c.AST.Props.Props {
			switch pd := p.(type) {
			case ast.Param:
				if !hasProp(v.Props, pd.Name) {
					v.Props = append(v.Props, PropView{
						Name: pd.Name,
						Type: formatType(pd.Type),
					})
				}
			case ast.EventDecl:
				if !hasProp(v.Events, pd.Name) {
					v.Events = append(v.Events, PropView{
						Name:    pd.Name,
						IsEvent: true,
						Payload: formatType(pd.Type),
					})
				}
			}
		}
		if v.Children == "" && c.AST.ChildrenType != nil {
			v.Children = formatType(c.AST.ChildrenType)
		}
	}
	return v
}

func hasProp(list []PropView, name string) bool {
	for _, p := range list {
		if p.Name == name {
			return true
		}
	}
	return false
}

func mapType(t *TypeDetail) TypeDetailView {
	if t == nil {
		return TypeDetailView{}
	}
	v := TypeDetailView{Name: t.Name, Doc: t.Doc}
	for _, m := range t.Methods {
		v.Methods = append(v.Methods, MethodView{
			Short: m.ShortName,
			Full:  m.FullName,
			Doc:   m.Doc,
		})
	}
	if t.Native != nil {
		v.IsNative = true
		v.NativeName = t.Native.Native
		for _, f := range t.Native.Fields {
			v.Fields = append(v.Fields, FieldView{
				Name: f.Name,
				Type: f.Type.String(),
			})
		}
		return v
	}
	if t.Struct != nil {
		for _, f := range t.Struct.Fields {
			ftype := formatType(f.Type)
			for _, name := range f.Names {
				v.Fields = append(v.Fields, FieldView{Name: name, Type: ftype})
			}
		}
	}
	return v
}

func mapEnum(e *EnumDetail) EnumDetailView {
	if e == nil {
		return EnumDetailView{}
	}
	v := EnumDetailView{Name: e.Name, Doc: e.Doc}
	if e.Native != nil {
		v.IsNative = true
		for _, m := range e.Native.Members {
			v.Members = append(v.Members, Summary{Name: m.Name})
		}
		return v
	}
	if e.AST != nil {
		for _, m := range e.AST.Members {
			v.Members = append(v.Members, Summary{Name: m.Name})
		}
	}
	return v
}

func mapFunc(f *FuncDetail) FuncDetailView {
	if f == nil {
		return FuncDetailView{}
	}
	v := FuncDetailView{Name: f.Name, Doc: f.Doc}
	if f.Native != nil {
		v.IsNative = true
		v.NativeName = f.Native.NativeName
		for _, p := range f.Native.Params {
			v.Params = append(v.Params, ParamView{Name: p.Name, Type: p.Type.String()})
		}
		if f.Native.Return != nil {
			v.ReturnType = f.Native.Return.String()
		}
		v.Signature = nativeFuncSig(f.Native.Name, v.Params, v.ReturnType)
		return v
	}
	if f.AST != nil {
		for _, p := range f.AST.Params.Params {
			v.Params = append(v.Params, ParamView{
				Name: p.Name,
				Type: formatType(p.Type),
			})
		}
		if f.AST.ReturnType != nil {
			v.ReturnType = formatType(f.AST.ReturnType)
		}
		v.Signature = snglFuncSig(f.AST)
	}
	return v
}

func mapValue(val *ValueDetail) ValueDetailView {
	if val == nil {
		return ValueDetailView{}
	}
	v := ValueDetailView{Name: val.Name, Doc: val.Doc, IsConst: val.IsConst}
	if val.Native != nil {
		v.IsNative = true
		v.NativeName = val.Native.NativeName
		if val.Native.Type != nil {
			v.Type = val.Native.Type.String()
		}
		return v
	}
	switch d := val.AST.(type) {
	case *ast.ConstDecl:
		for _, spec := range d.Specs {
			if spec.Type != nil {
				v.Type = formatType(spec.Type)
				break
			}
		}
	case *ast.VarDecl:
		for _, spec := range d.Specs {
			if spec.Type != nil {
				v.Type = formatType(spec.Type)
				break
			}
		}
	}
	return v
}

func mapProp(p *PropDetail) PropDetailView {
	if p == nil {
		return PropDetailView{}
	}
	v := PropDetailView{ComponentName: p.Component, Name: p.Name}
	if p.Schema != nil {
		v.Type = (&p.Schema.Type).String()
		v.Doc = p.Schema.Doc
		v.EnumValues = append([]string(nil), p.Schema.Enum...)
		return v
	}
	v.IsEvent = true
	v.Payload = p.Event
	return v
}

func mapField(f *FieldDetail) FieldDetailView {
	if f == nil {
		return FieldDetailView{}
	}
	return FieldDetailView{
		Type: f.Type,
		Name: f.Name,
		Expr: formatType(f.Expr),
		Doc:  f.Doc,
	}
}

func mapMember(m *MemberDetail) MemberDetailView {
	if m == nil {
		return MemberDetailView{}
	}
	return MemberDetailView{EnumName: m.Enum, Name: m.Name, Doc: m.Doc}
}

func formatType(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	return parser.FormatType(t)
}

func nativeFuncSig(name string, params []ParamView, ret string) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = fmt.Sprintf("%s %s", p.Name, p.Type)
	}
	sig := fmt.Sprintf("func %s(%s)", name, strings.Join(parts, ", "))
	if ret != "" {
		sig += " " + ret
	}
	return sig
}

func snglFuncSig(f *ast.FuncDef) string {
	var sb strings.Builder
	sb.WriteString("func " + f.Name)
	if len(f.TypeParams) > 0 {
		sb.WriteString("<" + strings.Join(f.TypeParams, ", ") + ">")
	}
	sb.WriteString("(")
	for i, p := range f.Params.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.Name)
		if p.Type != nil {
			sb.WriteString(" " + formatType(p.Type))
		}
	}
	sb.WriteString(")")
	if f.ReturnType != nil {
		sb.WriteString(" " + formatType(f.ReturnType))
	}
	return sb.String()
}
