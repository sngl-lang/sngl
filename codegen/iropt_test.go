package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func varSet(vars ...*ir.Var) map[*ir.Var]struct{} {
	s := make(map[*ir.Var]struct{}, len(vars))
	for _, v := range vars {
		s[v] = struct{}{}
	}
	return s
}

func TestOptimizeMutation_RemoveStaticUpdaters(t *testing.T) {
	name := &ir.Var{Name: "name"}
	m := &MutationModel{
		Analysis:   &CommonAnalysis{},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set text", Deps: varSet(name)},
			{Name: "$u_1_text", Body: "static", Deps: nil}, // no deps = static
		},
	}
	OptimizeMutation(m)
	if len(m.Updaters) != 1 {
		t.Fatalf("expected 1 updater, got %d", len(m.Updaters))
	}
	if m.Updaters[0].Name != "$u_0_text" {
		t.Fatalf("expected $u_0_text, got %s", m.Updaters[0].Name)
	}
}

func TestOptimizeMutation_DeduplicateUpdaters(t *testing.T) {
	x := &ir.Var{Name: "x"}
	m := &MutationModel{
		Analysis:   &CommonAnalysis{},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "old", Deps: varSet(x)},
			{Name: "$u_0_text", Body: "new", Deps: varSet(x)},
		},
	}
	OptimizeMutation(m)
	if len(m.Updaters) != 1 {
		t.Fatalf("expected 1 updater after dedup, got %d", len(m.Updaters))
	}
	if m.Updaters[0].Body != "new" {
		t.Fatalf("expected last body 'new', got %q", m.Updaters[0].Body)
	}
}

func TestOptimizeMutation_MergeUpdaters(t *testing.T) {
	count := &ir.Var{Name: "count"}
	name := &ir.Var{Name: "name"}
	deps := varSet(count)
	m := &MutationModel{
		Analysis:   &CommonAnalysis{},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set text", Deps: deps},
			{Name: "$u_0_cls", Body: "set class", Deps: deps},
			{Name: "$u_0_attr", Body: "set attr", Deps: deps},
			{Name: "$u_1_text", Body: "other", Deps: varSet(name)},
		},
	}
	OptimizeMutation(m)
	if len(m.Updaters) != 2 {
		t.Fatalf("expected 2 updaters after merge, got %d", len(m.Updaters))
	}
	// First group should be merged
	if m.Updaters[0].Kind != "merged" {
		t.Fatalf("expected merged kind, got %q", m.Updaters[0].Kind)
	}
	if m.Updaters[0].Body != "set text\nset class\nset attr" {
		t.Fatalf("unexpected merged body: %q", m.Updaters[0].Body)
	}
	// Second should pass through
	if m.Updaters[1].Name != "$u_1_text" {
		t.Fatalf("expected $u_1_text, got %s", m.Updaters[1].Name)
	}
}

func TestOptimizeMutation_KeepsHandlers(t *testing.T) {
	// Handlers are kept even if their mutations don't affect updaters,
	// because state writes are observable side effects.
	x := &ir.Var{Name: "x"}
	unused := &ir.Var{Name: "unused"}
	m := &MutationModel{
		Analysis: &CommonAnalysis{
			ModelFields:    map[string]bool{"x": true, "unused": true},
			ComputedFields: map[string]bool{},
			ComputedDeps:   map[string]map[string]bool{},
		},
		DepTracker: NewDepTracker(
			varSet(x, unused),
			map[*ir.Func]struct{}{},
			map[*ir.Func]map[*ir.Var]struct{}{},
		),
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set", Deps: varSet(x)},
		},
		Handlers: []Handler{
			{NodeID: "$0", Event: "click", Body: nil, Mutated: varSet(x)},
			{NodeID: "$1", Event: "click", Body: nil, Mutated: varSet(unused)},
		},
	}
	OptimizeMutation(m)
	if len(m.Handlers) != 2 {
		t.Fatalf("expected 2 handlers preserved, got %d", len(m.Handlers))
	}
}

// Helper/import requirements ride on each Updater (collected structurally
// during translation), so OptimizeMutation no longer text-scans bodies. A dead
// updater (empty deps, not initOnly) is dropped and its Requires go with it;
// a surviving updater keeps its Requires for the platform to union.
func TestOptimizeMutation_UpdaterRequiresSurviveDropAndDrop(t *testing.T) {
	x := &ir.Var{Name: "x"}
	m := &MutationModel{
		Analysis:   &CommonAnalysis{},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			// Live updater (has a dep) — kept; its String requirement survives.
			{Name: "$u_0_text", Body: "$0.textContent = String(state.x)", Deps: varSet(x),
				Requires: Requirement{Helpers: map[string]bool{"String": true}}},
			// Dead updater (no deps, not initOnly) — dropped; its requirement
			// must NOT reach any surviving updater.
			{Name: "$u_1_text", Body: "$1.textContent = Dead()", Deps: map[*ir.Var]struct{}{},
				Requires: Requirement{Helpers: map[string]bool{"Dead": true}}},
		},
	}
	OptimizeMutation(m)

	survivors := map[string]bool{}
	for _, u := range m.Updaters {
		survivors[u.Name] = true
	}
	if !survivors["$u_0_text"] {
		t.Fatal("expected live updater to survive")
	}
	if survivors["$u_1_text"] {
		t.Fatal("expected dead (no-dep) updater to be dropped")
	}

	// Union the surviving updaters' requirements the way a platform does.
	helpers := map[string]bool{}
	native := map[string]map[string]bool{}
	for _, u := range m.Updaters {
		u.Requires.MergeInto(helpers, native)
	}
	if !helpers["String"] {
		t.Fatal("expected String (from surviving updater) in unioned helpers")
	}
	if helpers["Dead"] {
		t.Fatal("expected Dead (from dropped updater) to be excluded")
	}
}

// Merged updaters (same target + deps) must union their requirements so the
// merged body's needs are fully represented.
func TestOptimizeMutation_MergedUpdaterUnionsRequires(t *testing.T) {
	x := &ir.Var{Name: "x"}
	m := &MutationModel{
		Analysis:   &CommonAnalysis{},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "a", Deps: varSet(x),
				Requires: Requirement{Helpers: map[string]bool{"String": true}}},
			{Name: "$u_0_attr", Body: "b", Deps: varSet(x),
				Requires: Requirement{NativeImports: map[string]map[string]bool{"js://m": {"f": true}}}},
		},
	}
	OptimizeMutation(m)
	if len(m.Updaters) != 1 {
		t.Fatalf("expected the two same-target/dep updaters to merge into 1, got %d", len(m.Updaters))
	}
	got := m.Updaters[0].Requires
	if !got.Helpers["String"] {
		t.Error("merged updater lost the String helper requirement")
	}
	if got.NativeImports["js://m"] == nil || !got.NativeImports["js://m"]["f"] {
		t.Error("merged updater lost the native import requirement")
	}
}

func TestPruneUnusedComputeds(t *testing.T) {
	a := &CommonAnalysis{
		ModelFields:    map[string]bool{"x": true, "used": true, "unused": true},
		ComputedFields: map[string]bool{"used": true, "unused": true},
		ComputedDeps: map[string]map[string]bool{
			"used":   {"x": true},
			"unused": {"x": true},
		},
	}
	a.PruneUnusedComputeds(map[string]bool{"x": true, "used": true})

	if a.ComputedFields["unused"] {
		t.Fatal("expected 'unused' computed to be pruned")
	}
	if !a.ComputedFields["used"] {
		t.Fatal("expected 'used' computed to be kept")
	}
	if a.ModelFields["unused"] {
		t.Fatal("expected 'unused' removed from ModelFields")
	}
}

func TestPruneUnusedComputeds_Transitive(t *testing.T) {
	// 'indirect' is used by 'used' which is in usedFields
	a := &CommonAnalysis{
		ModelFields:    map[string]bool{"x": true, "used": true, "indirect": true, "dead": true},
		ComputedFields: map[string]bool{"used": true, "indirect": true, "dead": true},
		ComputedDeps: map[string]map[string]bool{
			"used":     {"indirect": true},
			"indirect": {"x": true},
			"dead":     {"x": true},
		},
	}
	a.PruneUnusedComputeds(map[string]bool{"used": true})

	if !a.ComputedFields["used"] {
		t.Fatal("expected 'used' kept")
	}
	if !a.ComputedFields["indirect"] {
		t.Fatal("expected 'indirect' kept (transitively needed by 'used')")
	}
	if a.ComputedFields["dead"] {
		t.Fatal("expected 'dead' pruned (not transitively needed)")
	}
}

func TestStaticFields(t *testing.T) {
	count := &ir.Var{Name: "count"}
	modelFields := map[string]bool{"name": true, "count": true, "label": true}
	handlers := []Handler{
		{Mutated: varSet(count)},
	}
	timers := []TimerHandler{
		{TimerInfo: TimerInfo{ActiveVar: "name"}},
	}
	static := StaticFields(handlers, timers, modelFields)
	if !static["label"] {
		t.Fatal("expected 'label' to be static (never mutated)")
	}
	if static["count"] {
		t.Fatal("expected 'count' to not be static (mutated by handler)")
	}
	if static["name"] {
		t.Fatal("expected 'name' to not be static (timer active var)")
	}
}

func TestOptimizeRender_RemoveNilHandlers(t *testing.T) {
	m := &RenderModel{
		Analysis: &CommonAnalysis{
			ModelFields:    map[string]bool{},
			ComputedFields: map[string]bool{},
			ComputedDeps:   map[string]map[string]bool{},
		},
		Handlers: []Handler{
			{NodeID: "btn0", Event: "click", Body: nil},
			{NodeID: "btn1", Event: "click", Body: &ir.CallStmt{}},
		},
	}
	OptimizeRender(m)
	if len(m.Handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(m.Handlers))
	}
}
