package fyne

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen/lang/golang"
)

// emitFyneModel writes the structural model file — lang helpers, unit/struct
// decls, the Model struct, New(), toast methods, computed methods,
// user functions, and getters/setters. It emits Go directly (not via a
// template) so every framework reference registers its import through gc:
// method-body references (fyne.Do, time.*, widget.*) call gc.RequireImport at
// the emit site, and field/return/param types register via
// requireTypeImports. Whitespace is left rough — the FileEmitter gofmt pass
// normalizes it.
func emitFyneModel(b *strings.Builder, td *templateData, gc *golang.GoIRContext) {
	b.WriteString(td.LangHelpers)
	b.WriteString(td.UnitDecls)

	for _, s := range td.Structs {
		fmt.Fprintf(b, "type %s struct {\n", s.Name)
		for _, f := range s.Fields {
			fmt.Fprintf(b, "\t%s %s\n", f.Name, f.Type)
			requireTypeImports(gc, f.Type)
		}
		b.WriteString("}\n\n")
	}

	if td.NeedsToast {
		b.WriteString("type snglToast struct {\n\tmessage string\n\tvariant string\n}\n\n")
	}

	// Model struct. Blank lines separate field groups so gofmt aligns each
	// group's columns independently (matching the prior template layout, which
	// downstream tests assert against).
	b.WriteString("// Model holds the state for this SNGL UI.\ntype Model struct {\n")
	for _, bd := range td.Binds {
		fmt.Fprintf(b, "\t%s %s\n", bd.Name, bd.GoType)
		requireTypeImports(gc, bd.GoType)
	}
	for _, e := range td.Externs {
		fmt.Fprintf(b, "\t%s %s // extern\n", e.Name, e.GoType)
		requireTypeImports(gc, e.GoType)
	}
	if len(td.Binds) > 0 || len(td.Externs) > 0 {
		b.WriteString("\n")
	}
	for _, w := range td.WidgetFields {
		fmt.Fprintf(b, "\t%s %s\n", w.Name, w.GoType)
		requireTypeImports(gc, w.GoType)
	}
	if len(td.WidgetFields) > 0 {
		b.WriteString("\n")
	}
	if td.NeedsToast {
		b.WriteString("\ttoasts []snglToast\n")
		b.WriteString("\ttoastLabel *widget.Label\n")
		b.WriteString("\ttoastBox *fyne.Container\n")
		gc.RequireImport("fyne.io/fyne/v2/widget")
		gc.RequireImport("fyne.io/fyne/v2")
		// BuildUI wires the toast via container.NewVBox / NewBorder.
		gc.RequireImport("fyne.io/fyne/v2/container")
	}
	b.WriteString("}\n\n")

	// New() — sequential field init so later inits can read earlier fields.
	b.WriteString("// New creates a Model with default values.\nfunc New() *Model {\n\tm := &Model{}\n")
	for _, bd := range td.Binds {
		fmt.Fprintf(b, "\tm.%s = %s\n", bd.Name, bd.InitVal)
	}
	b.WriteString("\treturn m\n}\n\n")

	if td.NeedsToast {
		// Toast methods reference fyne.Do and time.*; register both.
		gc.RequireImport("fyne.io/fyne/v2")
		gc.RequireImport("time")
		b.WriteString(`func (m *Model) showToast(msg, variant string) {
	m.toasts = append(m.toasts, snglToast{msg, variant})
	m.updateToast()
	time.AfterFunc(3*time.Second, func() {
		fyne.Do(func() {
			if len(m.toasts) > 0 {
				m.toasts = m.toasts[1:]
			}
			m.updateToast()
		})
	})
}

func (m *Model) updateToast() {
	if m.toastLabel == nil {
		return
	}
	if len(m.toasts) > 0 {
		m.toastLabel.SetText(m.toasts[0].message)
		m.toastBox.Show()
	} else {
		m.toastBox.Hide()
	}
}

`)
	}

	for _, c := range td.Computeds {
		fmt.Fprintf(b, "func (m *Model) %s() %s {\n%s\n}\n\n", c.Name, c.GoType, c.Body)
		requireTypeImports(gc, c.GoType)
	}

	b.WriteString(td.FunctionCode)

	for _, bd := range td.Binds {
		if bd.NoAccessors {
			continue
		}
		fmt.Fprintf(b, "func (m *Model) %s() %s {\n\treturn m.%s\n}\n\n", bd.Getter, bd.GoType, bd.Name)
		fmt.Fprintf(b, "func (m *Model) Set%s(v %s) {\n\tm.%s = v\n%s}\n\n", bd.Getter, bd.GoType, bd.Name, bd.SetterExtra)
	}
}

// requireTypeImports registers the imports a rendered Go type string depends
// on (e.g. "*fyne.Container" → fyne, "time.Duration" → time). Type rendering
// (IRTypeToGo) produces strings, so a type's package deps are resolved by
// matching the known framework/std selectors rather than a per-site call.
func requireTypeImports(gc *golang.GoIRContext, goType string) {
	for sel, path := range fyneFrameworkPkgs {
		if namesSelector(goType, sel) {
			gc.RequireImport(path)
		}
	}
	if namesSelector(goType, "time") {
		gc.RequireImport("time")
	}
}

// namesSelector reports whether goType qualifies a name by sel, as a whole
// identifier: `*fynelayout.Tap` names fynelayout and not layout.
func namesSelector(goType, sel string) bool {
	for i := 0; ; {
		j := strings.Index(goType[i:], sel+".")
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !isIdentByte(goType[at-1]) {
			return true
		}
		i = at + 1
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
