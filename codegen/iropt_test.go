package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestOptimizeMutation_RemoveStaticUpdaters(t *testing.T) {
	m := &MutationModel{
		Analysis:   &CommonAnalysis{Helpers: map[string]bool{}},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set text", Deps: map[string]bool{"name": true}},
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
	m := &MutationModel{
		Analysis:   &CommonAnalysis{Helpers: map[string]bool{}},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "old", Deps: map[string]bool{"x": true}},
			{Name: "$u_0_text", Body: "new", Deps: map[string]bool{"x": true}},
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
	deps := map[string]bool{"count": true}
	m := &MutationModel{
		Analysis:   &CommonAnalysis{Helpers: map[string]bool{}},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set text", Deps: deps},
			{Name: "$u_0_cls", Body: "set class", Deps: deps},
			{Name: "$u_0_attr", Body: "set attr", Deps: deps},
			{Name: "$u_1_text", Body: "other", Deps: map[string]bool{"name": true}},
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
	m := &MutationModel{
		Analysis: &CommonAnalysis{
			ModelFields:    map[string]bool{"x": true, "unused": true},
			ComputedFields: map[string]bool{},
			ComputedDeps:   map[string]map[string]bool{},
			Helpers:        map[string]bool{},
		},
		DepTracker: NewDepTracker(
			map[string]bool{"x": true, "unused": true},
			map[string]bool{},
			map[string]map[string]bool{},
		),
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "set", Deps: map[string]bool{"x": true}},
		},
		Handlers: []Handler{
			{NodeID: "$0", Event: "click", Body: nil, Mutated: map[string]bool{"x": true}},
			{NodeID: "$1", Event: "click", Body: nil, Mutated: map[string]bool{"unused": true}},
		},
	}
	OptimizeMutation(m)
	if len(m.Handlers) != 2 {
		t.Fatalf("expected 2 handlers preserved, got %d", len(m.Handlers))
	}
}

func TestOptimizeMutation_PruneHelpers(t *testing.T) {
	m := &MutationModel{
		Analysis: &CommonAnalysis{
			Helpers: map[string]bool{"String": true, "Unused": true},
		},
		DepTracker: &DepTracker{},
		Updaters: []Updater{
			{Name: "$u_0_text", Body: "$0.textContent = String(state.x)", Deps: map[string]bool{"x": true}},
		},
	}
	OptimizeMutation(m)
	if !m.Analysis.Helpers["String"] {
		t.Fatal("expected String helper to be kept")
	}
	if m.Analysis.Helpers["Unused"] {
		t.Fatal("expected Unused helper to be pruned")
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
	modelFields := map[string]bool{"name": true, "count": true, "label": true}
	handlers := []Handler{
		{Mutated: map[string]bool{"count": true}},
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
