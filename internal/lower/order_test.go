package lower

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPassOrderConstraints(t *testing.T) {
	at := make(map[string]int, len(passes))
	for i, p := range passes {
		at[p.name] = i
	}
	for _, c := range orderConstraints {
		i, ok := at[c.Earlier]
		if !ok {
			t.Errorf("constraint names no such pass %q\n\tconstraint: %s", c.Earlier, c)
			continue
		}
		j, ok := at[c.Later]
		if !ok {
			t.Errorf("constraint names no such pass %q\n\tconstraint: %s", c.Later, c)
			continue
		}
		if i >= j {
			t.Errorf("pass order violates a stated requirement:\n\t%s must run before %s\n\tbecause: %s\n\tregistry has %s at %d and %s at %d",
				c.Earlier, c.Later, c.Why, c.Earlier, i, c.Later, j)
		}
	}
}

func TestPassRegistryIsWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(passes))
	for i, p := range passes {
		if p.name == "" {
			t.Errorf("passes[%d] has no name", i)
		}
		if seen[p.name] {
			t.Errorf("duplicate pass name %q", p.name)
		}
		seen[p.name] = true
		if p.apply == nil {
			t.Errorf("passes[%d] (%s) has nil apply", i, p.name)
		}
		if p.enabled == nil {
			t.Errorf("passes[%d] (%s) has nil enabled", i, p.name)
		}
	}
}

// alwaysOn is the passes that answer for every target, so no capability gates
// them. It is a set and not an order: what it states is that a pass cannot
// quietly acquire a gate, or quietly lose one, without someone saying so.
var alwaysOn = []string{
	"CSE",
	"ForElse",
	"ForeignPrimitive",
	"HoistState",
	"IndexedIter",
	"InlinePure",
	"IterKind",
	"PlatformExtensionBody",
	"PropBindings",
	"Query",
	"RecursionDepth",
	"RefLoop",
	"RootWindow",
	"StampUsage",
	"ViewForElse",
	"WindowNesting",
}

func TestAlwaysOnPasses(t *testing.T) {
	// The empty Caps is the target that asks for nothing, so what survives the
	// filter is exactly the ungated set -- which states both halves at once: a
	// pass missing here has grown a gate, and an extra one has lost its.
	got := slices.Clone(EnabledPasses(Caps{}))
	slices.Sort(got)
	if !slices.Equal(got, alwaysOn) {
		t.Errorf("EnabledPasses(Caps{}) = %v\nwant %v", got, alwaysOn)
	}
}

// TestCapabilityGatesAPassOnAndOff states the other half of what the old
// filtered list said: which passes a capability turns on. It is not an
// ordering claim, and no constraint can make it -- a gate is what a pass asks
// of a target, and TestAlwaysOnPasses only covers the passes that ask nothing.
//
// NoReactivity is the interesting one: three passes gate on hasInstanceRuntime,
// which reads it and nothing else, so a target that keeps its reactivity gets
// none of them.
func TestCapabilityGatesAPassOnAndOff(t *testing.T) {
	always := make(map[string]bool, len(alwaysOn))
	for _, n := range alwaysOn {
		always[n] = true
	}
	gated := func(c Caps) []string {
		var out []string
		for _, n := range EnabledPasses(c) {
			if !always[n] {
				out = append(out, n)
			}
		}
		slices.Sort(out)
		return out
	}
	tests := []struct {
		name string
		caps Caps
		want []string
	}{
		{"nothing asked for", Caps{}, nil},
		{"a lone statement rewrite", Caps{NoToggle: true}, []string{"NoToggle"}},
		{"an instance runtime", Caps{NoReactivity: true},
			[]string{"ComponentProps", "InstanceBodies", "InstanceEvents", "NoReactivity"}},
		{"and a slot that can place a child", Caps{NoReactivity: true, InsertBefore: true},
			[]string{"ComponentProps", "InstanceBodies", "InstanceEvents", "NoReactivity", "SlotChildInstances"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := gated(tc.caps); !slices.Equal(got, tc.want) {
				t.Errorf("gated passes = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestEveryDeclaredPassIsRegistered reads the package's own source for
// `var passX = pass{name: "..."}` and requires each to be in the registry.
//
// This is the half of the old full-order list worth keeping: it caught a pass
// dropping out of `passes` while its file stayed put, which is a silent change
// of what every target compiles. Derived from the source rather than written
// down, so adding a pass needs no edit here.
func TestEveryDeclaredPassIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	registered := make(map[string]bool, len(passes))
	for _, p := range passes {
		registered[p.name] = true
	}
	declared := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, d := range file.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.CompositeLit)
					if !ok {
						continue
					}
					if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "pass" {
						continue
					}
					declared++
					name := passNameField(t, lit)
					if !registered[name] {
						t.Errorf("%s declares a pass named %q that %s does not register",
							path, name, "passes")
					}
				}
			}
		}
	}
	if declared != len(passes) {
		t.Errorf("source declares %d passes; registry holds %d", declared, len(passes))
	}
}

func passNameField(t *testing.T, lit *ast.CompositeLit) string {
	t.Helper()
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "name" {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			t.Fatalf("pass name is not a string literal at %v", kv.Value)
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			t.Fatalf("pass name %s: %v", bl.Value, err)
		}
		return s
	}
	t.Fatal("pass literal has no name field")
	return ""
}
