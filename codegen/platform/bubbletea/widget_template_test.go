package bubbletea

import (
	"slices"
	"testing"

	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// litNode builds a NodeInst whose props are bare string Literals (Type=nil), so
// EvalExpr returns each prop's Raw verbatim — enough to exercise token
// substitution without a full ExprCtx.
func litNode(props map[string]string) *ir.NodeInst {
	n := &ir.NodeInst{Name: "Widget"}
	for name, raw := range props {
		n.Props = append(n.Props, ir.Arg{Name: name, Value: &ir.Literal{Value: raw}})
	}
	return n
}

func TestExpandWidgetTemplate(t *testing.T) {
	tests := []struct {
		name  string
		tmpl  string
		props map[string]string
		field string
		want  string
	}{
		{
			name: "no token unchanged",
			tmpl: "textinput.New()",
			want: "textinput.New()",
		},
		{
			name: "no token view unchanged",
			tmpl: ".View()",
			want: ".View()",
		},
		{
			name:  "plain prop substitution",
			tmpl:  "m.field.ViewAs(float64(${value}) / 100)",
			props: map[string]string{"value": "m.progressValue"},
			want:  "m.field.ViewAs(float64(m.progressValue) / 100)",
		},
		{
			name:  "converter wraps expression",
			tmpl:  "list.New(${items|listItems}, d, 0, 0)",
			props: map[string]string{"items": "m.todos"},
			want:  "list.New(tui.StringItems(m.todos), d, 0, 0)",
		},
		{
			name:  "columns converter",
			tmpl:  "table.New(table.WithColumns(${cols|columns}))",
			props: map[string]string{"cols": "m.cols"},
			want:  "table.New(table.WithColumns(tui.Columns(m.cols)))",
		},
		{
			name:  "two tokens",
			tmpl:  "f(${a}, ${b|rows})",
			props: map[string]string{"a": "m.x", "b": "m.y"},
			want:  "f(m.x, tui.Rows(m.y))",
		},
		{
			name: "escape sequence",
			tmpl: "$${literal}",
			want: "${literal}",
		},
		{
			name:  "unknown prop left in place",
			tmpl:  "f(${missing})",
			props: map[string]string{"other": "m.o"},
			want:  "f(${missing})",
		},
		{
			name:  "unknown converter left in place",
			tmpl:  "f(${value|bogus})",
			props: map[string]string{"value": "m.v"},
			want:  "f(${value|bogus})",
		},
		{
			name:  "self resolves to field accessor",
			tmpl:  "${self}.Tick",
			field: "widget3",
			want:  "m.widget3.Tick",
		},
		{
			name: "w token resolves to inset terminal width helper",
			tmpl: ".SetWidth(${w})",
			want: ".SetWidth(tui.WidgetWidth(m.width))",
		},
		{
			name: "w and h tokens resolve in SetSize",
			tmpl: ".SetSize(${w}, ${h})",
			want: ".SetSize(tui.WidgetWidth(m.width), tui.WidgetHeight(m.height))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gc := golang.NewIRContext(nil)
			got := expandWidgetTemplate(gc, tt.tmpl, litNode(tt.props), tt.field)
			if got != tt.want {
				t.Errorf("expandWidgetTemplate(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

// TestExpandWidgetTemplateRegistersImport verifies a converter token registers
// the pkg/go/tui import, while a token-free / plain-prop template does not.
func TestExpandWidgetTemplateRegistersImport(t *testing.T) {
	hasTUI := func(gc *golang.GoIRContext) bool {
		return slices.Contains(gc.Imports(), tuiImportPath)
	}

	gc := golang.NewIRContext(nil)
	expandWidgetTemplate(gc, "textinput.New()", litNode(nil), "f")
	if hasTUI(gc) {
		t.Errorf("no-token template should not require tui import")
	}

	gc = golang.NewIRContext(nil)
	expandWidgetTemplate(gc, "${items|listItems}", litNode(map[string]string{"items": "m.xs"}), "f")
	if !hasTUI(gc) {
		t.Errorf("converter token should require tui import %q", tuiImportPath)
	}

	gc = golang.NewIRContext(nil)
	expandWidgetTemplate(gc, ".SetWidth(${w})", litNode(nil), "f")
	if !hasTUI(gc) {
		t.Errorf("${w} token should require tui import %q", tuiImportPath)
	}
}
