package lib_test

import (
	"strings"
	"testing"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ir"
)

// intrinsicPackages are the sngl://internal packages that declare the
// compiler's primitives, paired with the registry each was written from.
var intrinsicPackages = map[string][]ir.IntrinsicDef{
	"internal/stdlib": ir.Intrinsics,
	"internal/alert":  ir.AlertIntrinsics,
	"internal/file":   ir.FileIntrinsics,
	"internal/intl":   ir.I18nIntrinsics,
	"internal/lower":  ir.LowerIntrinsics,
	"internal/canvas": ir.CanvasIntrinsics,
}

// TestInternalPackagesDeclareEveryIntrinsic pins the declarations against the
// registry they were generated from, so the two cannot drift while both exist.
// What the compiler knows about an intrinsic is meant to be readable at the
// declaration; a registry entry with no declaration would be knowledge with
// nowhere to read it.
func TestInternalPackagesDeclareEveryIntrinsic(t *testing.T) {
	for uri, defs := range intrinsicPackages {
		src := "import . \"sngl://std\"\nimport \"sngl://" + uri + "\"\ncomponent main { text(value=\"x\") }\n"
		doc, err := sngl.Parse("t.sngl", strings.NewReader(src))
		if err != nil {
			t.Fatalf("%s: parse: %v", uri, err)
		}
		pkg, diags := sngl.Check(doc, ".")
		for _, d := range diags {
			if d.Severity == ir.Error {
				t.Fatalf("%s: check: %s", uri, d.Msg)
			}
		}
		declared := map[string]*ir.Func{}
		for _, imp := range pkg.Imports {
			if imp.Pkg == nil {
				continue
			}
			for _, fn := range imp.Pkg.Funcs {
				if fn.Intrinsic != "" {
					declared[fn.Intrinsic] = fn
				}
			}
		}
		for _, def := range defs {
			fn, ok := declared[def.Name]
			if !ok {
				t.Errorf("%s: no declaration marked #[intrinsic(%q)]", uri, def.Name)
				continue
			}
			if len(fn.Params) != len(def.Params) {
				t.Errorf("%s: %s declares %d params, registry has %d", uri, def.Name, len(fn.Params), len(def.Params))
			}
			if fn.MutatesReceiver != def.MutatesReceiver {
				t.Errorf("%s: %s mutatesReceiver=%v, registry has %v", uri, def.Name, fn.MutatesReceiver, def.MutatesReceiver)
			}
			if def.Purity != ir.PurityUnknown && fn.Purity != def.Purity {
				t.Errorf("%s: %s purity=%v, registry has %v", uri, def.Name, fn.Purity, def.Purity)
			}
		}
	}
}
