package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// The rules about the component a #[gen.protocol] function serves, which only
// a library can break since only a library declares an #[intrinsic]
// component: it is one, it is the function's package's own, and it has one of
// each operation. testdata/error_gen_protocol_create.sngl and
// error_gen_protocol_append.sngl hold the rules a program can break.
func TestGenProtocolPlacement(t *testing.T) {
	const otherSrc = "import sngl \"sngl:ui\"\nimport marks \"sngl:internal/marks\"\n\n#[marks.intrinsic(\"other:Other\")]\ncomponent Other() sngl.node\n"
	const head = "\nimport sngl \"sngl:ui\"\nimport marks \"sngl:internal/marks\"\nimport macro \"sngl:macro\"\nimport gen \"sngl:x/gen\"\nimport other \"sngl:x/protocolother\"\n\n" +
		"#[marks.intrinsic(\"extstub:Thing\")]\ncomponent Thing(x int) sngl.node\n\n" +
		"component Plain() sngl.node {}\n\n" +
		"#[macro.cnative(\"thing_new\")]\nfunc thingNew() Thing\n\n" +
		"#[macro.cnative(\"plain_new\")]\nfunc plainNew() Plain\n\n" +
		"#[macro.cnative(\"other_new\")]\nfunc otherNew() other.Other\n\n" +
		"#[macro.cnative(\"thing_add\")]\nfunc thingAdd(parent Thing, child sngl.node)\n\n"
	for _, tc := range []struct {
		name, decls, want string
	}{
		{"one", "#[gen.protocol(create)]\nfunc _create() Thing {\n    return thingNew()\n}\n", ""},
		{"not intrinsic", "#[gen.protocol(create)]\nfunc _create() Plain {\n    return plainNew()\n}\n", "and Plain is not one"},
		{"another package's", "#[gen.protocol(create)]\nfunc _create() other.Other {\n    return otherNew()\n}\n", "only the package declaring a component says how its widget is built"},
		{"twice", "#[gen.protocol(create)]\nfunc _a() Thing {\n    return thingNew()\n}\n\n#[gen.protocol(create)]\nfunc _b() Thing {\n    return thingNew()\n}\n", "Thing already has _a"},
		{"an append and a remove", "#[gen.protocol(append)]\nfunc _add(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n\n#[gen.protocol(remove)]\nfunc _drop(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n", ""},
		{"append to one not intrinsic", "#[gen.protocol(append)]\nfunc _add(parent Plain, child sngl.node) {}\n", "#[gen.protocol(append)] declares the #[intrinsic] component it adds to, and Plain is not one"},
		{"append to another package's", "#[gen.protocol(append)]\nfunc _add(parent other.Other, child sngl.node) {}\n", "#[gen.protocol(append)]: Other is declared by"},
		{"append twice", "#[gen.protocol(append)]\nfunc _a(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n\n#[gen.protocol(append)]\nfunc _b(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n", "#[gen.protocol(append)]: Thing already has _a"},
		{"remove twice", "#[gen.protocol(remove)]\nfunc _a(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n\n#[gen.protocol(remove)]\nfunc _b(parent Thing, child sngl.node) {\n    thingAdd(parent, child)\n}\n", "#[gen.protocol(remove)]: Thing already has _a"},
		{"a connect beside the target", "#[gen.protocol(connect)]\nfunc _connect(w sngl.node, event string, handler func()) {}\n", ""},
		{"connect twice", "#[gen.protocol(connect)]\nfunc _a(w sngl.node, event string, handler func()) {}\n\n#[gen.protocol(connect)]\nfunc _b(w sngl.node, event string, handler func()) {}\n", "#[gen.protocol(connect)]: extstub already has _a"},
		{"connects on an event", "#[marks.intrinsic(\"extstub:Press\")]\ncomponent Press(\n    #[gen.connects(\"button-press\")]\n    @pressed,\n) sngl.node\n", ""},
		{"connects on a prop", "#[marks.intrinsic(\"extstub:Press\")]\ncomponent Press(\n    #[gen.connects(\"press\")]\n    x int,\n) sngl.node\n", "#[gen.connects] cannot mark a parameter; it marks an event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("main.sngl", []byte(withStd("component main node {\n    text(value=\"hi\")\n}\n")))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			cfg := extStubConfig(t, head+tc.decls)
			other, err := parser.Parse("other.sngl", []byte(otherSrc))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			cfg.LibSources["x/protocolother"] = []*ast.Document{other}
			_, diags := checker.Check(doc, cfg)
			var errs []string
			for _, d := range diags {
				if d.Severity == ir.Error {
					errs = append(errs, d.Msg)
				}
			}
			got := strings.Join(errs, "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("a protocol of the package's own intrinsic was refused:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want an error containing %q, got:\n%s", tc.want, got)
			}
		})
	}
}
