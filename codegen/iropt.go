package codegen

import (
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// OptimizeMutation runs optimization passes on a MutationModel:
//   - Remove updaters with empty dependency sets (static, never changes)
//   - Deduplicate updaters by name (last registration wins)
//   - Merge updaters that share the same target element and dependency set
//   - Remove handlers whose mutations don't affect any updater or timer
//   - Prune dead computed fields not referenced by any updater/handler/timer
//   - Prune helpers not actually referenced in generated code
func OptimizeMutation(m *MutationModel) {
	// Pass 1: Remove updaters with no deps (static content).
	// Exception: initOnly updaters with empty deps stay — they're meant
	// to run once at initial sync to populate the DOM from a value the
	// static-render pass couldn't pre-compute (e.g. an i18n.tr call
	// whose result depends on the active locale).
	m.Updaters = filterUpdaters(m.Updaters, func(u Updater) bool {
		return len(u.Deps) > 0 || u.InitOnly
	})

	// Pass 2: Deduplicate updaters by name (last wins).
	m.Updaters = deduplicateUpdaters(m.Updaters)

	// Pass 3: Merge updaters that share the same target and deps.
	m.Updaters = mergeUpdaters(m.Updaters)

	// Pass 4: Collect vars referenced by updaters (the reactive targets).
	activeVars := make(map[*ir.Var]struct{})
	for _, u := range m.Updaters {
		maps.Copy(activeVars, u.Deps)
	}

	// Pass 5: Prune dead computed fields (based on updater deps + timer vars).
	usedForComputeds := make(map[string]bool)
	for v := range activeVars {
		usedForComputeds[v.Name] = true
	}
	for _, t := range m.Timers {
		if t.ActiveVar != "" {
			usedForComputeds[t.ActiveVar] = true
		}
	}
	m.Analysis.PruneUnusedComputeds(usedForComputeds)

	// Note: we intentionally do NOT remove handlers based on mutation
	// analysis. A handler that writes to state has observable side effects
	// even if no updater currently reads that field (extern code, tests,
	// or future updaters may depend on it).
	//
	// Helper/import collection is NOT done here by text-scanning bodies.
	// Each updater carries its own Requires (collected during translation);
	// the platform unions the Requires of the SURVIVING updaters (after the
	// drop/dedup/merge passes above) into its helper/import set. Dead updaters
	// therefore contribute nothing, with no string matching involved.
}

// OptimizeRender runs optimization passes on a RenderModel:
//   - Remove handlers with nil bodies (no-op)
//   - Prune dead computed fields
//   - Detect static fields (never mutated)
func OptimizeRender(m *RenderModel) {
	// Pass 1: Remove no-op handlers.
	m.Handlers = filterHandlers(m.Handlers, func(h Handler) bool {
		return h.Body != nil
	})

	// Pass 2: Collect all referenced fields.
	usedFields := make(map[string]bool)
	for _, h := range m.Handlers {
		for v := range h.Mutated {
			usedFields[v.Name] = true
		}
	}
	for _, t := range m.Timers {
		if t.ActiveVar != "" {
			usedFields[t.ActiveVar] = true
		}
		for v := range t.Mutated {
			usedFields[v.Name] = true
		}
	}

	// Pass 3: Prune dead computed fields.
	m.Analysis.PruneUnusedComputeds(usedFields)
}

// StaticFields returns model fields that are never mutated by any handler
// or timer. These are effectively constants after initialization.
func StaticFields(handlers []Handler, timers []TimerHandler, modelFields map[string]bool) map[string]bool {
	mutated := make(map[string]bool)
	for _, h := range handlers {
		for v := range h.Mutated {
			mutated[v.Name] = true
		}
	}
	for _, t := range timers {
		if t.ActiveVar != "" {
			mutated[t.ActiveVar] = true
		}
		for v := range t.Mutated {
			mutated[v.Name] = true
		}
	}
	static := make(map[string]bool)
	for f := range modelFields {
		if !mutated[f] {
			static[f] = true
		}
	}
	return static
}

// --- internal helpers ---

func deduplicateUpdaters(us []Updater) []Updater {
	last := make(map[string]int)
	for i, u := range us {
		last[u.Name] = i
	}
	if len(last) == len(us) {
		return us
	}
	var out []Updater
	for i, u := range us {
		if last[u.Name] == i {
			out = append(out, u)
		}
	}
	return out
}

// mergeUpdaters groups updaters by target element ID and dependency set,
// merging those with identical deps into a single updater with concatenated
// bodies. The target ID is extracted from the updater name prefix (e.g.,
// "$u_0_text" → "0", "updateLabel3" → "3").
func mergeUpdaters(us []Updater) []Updater {
	type groupKey struct {
		target   string
		deps     string // sorted dep names joined
		initOnly bool
	}

	targetOf := func(u Updater) string {
		// HTML pattern: "$u_0_text" → "0"
		name := u.Name
		if strings.HasPrefix(name, "$u_") {
			rest := name[3:]
			if idx := strings.Index(rest, "_"); idx > 0 {
				return rest[:idx]
			}
		}
		// Fyne pattern: "updateLabel0" → "Label0"
		for _, prefix := range []string{"update"} {
			if strings.HasPrefix(name, prefix) {
				return name[len(prefix):]
			}
		}
		return name
	}

	depsKey := func(deps map[*ir.Var]struct{}) string {
		sorted := make([]string, 0, len(deps))
		for v := range deps {
			sorted = append(sorted, v.Name)
		}
		// Simple sort for determinism
		for i := range sorted {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i] > sorted[j] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return strings.Join(sorted, ",")
	}

	// Group updaters by target + deps.
	groups := make(map[groupKey][]int)
	var order []groupKey
	for i, u := range us {
		key := groupKey{target: targetOf(u), deps: depsKey(u.Deps), initOnly: u.InitOnly}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}

	// Emit merged updaters. Groups of 1 pass through unchanged.
	var out []Updater
	for _, key := range order {
		indices := groups[key]
		if len(indices) == 1 {
			out = append(out, us[indices[0]])
			continue
		}
		// Merge: keep first updater's metadata, concatenate bodies.
		first := us[indices[0]]
		var bodies []string
		for _, idx := range indices {
			if us[idx].Body != "" {
				bodies = append(bodies, us[idx].Body)
			}
		}
		merged := Updater{
			Name:     first.Name,
			Kind:     "merged",
			Node:     first.Node,
			Expr:     first.Expr,
			Body:     strings.Join(bodies, "\n"),
			Deps:     first.Deps,
			InitOnly: first.InitOnly,
		}
		// Union helper/import requirements across the merged group so the
		// merged body's needs are fully represented.
		for _, idx := range indices {
			merged.Requires.union(us[idx].Requires)
		}
		out = append(out, merged)
	}
	return out
}

func filterUpdaters(us []Updater, keep func(Updater) bool) []Updater {
	var out []Updater
	for _, u := range us {
		if keep(u) {
			out = append(out, u)
		}
	}
	return out
}

func filterHandlers(hs []Handler, keep func(Handler) bool) []Handler {
	var out []Handler
	for _, h := range hs {
		if keep(h) {
			out = append(out, h)
		}
	}
	return out
}
