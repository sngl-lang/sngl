package codegen

import (
	"maps"
	"strings"
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
	m.Updaters = filterUpdaters(m.Updaters, func(u Updater) bool {
		return len(u.Deps) > 0
	})

	// Pass 2: Deduplicate updaters by name (last wins).
	m.Updaters = deduplicateUpdaters(m.Updaters)

	// Pass 3: Merge updaters that share the same target and deps.
	m.Updaters = mergeUpdaters(m.Updaters)

	// Pass 4: Collect fields referenced by updaters (the reactive targets).
	activeFields := make(map[string]bool)
	for _, u := range m.Updaters {
		maps.Copy(activeFields, u.Deps)
	}
	timerFields := make(map[string]bool)
	for _, t := range m.Timers {
		timerFields[t.ActiveVar] = true
	}

	// Pass 5: Prune dead computed fields (based on updater deps + timer vars).
	usedForComputeds := make(map[string]bool)
	maps.Copy(usedForComputeds, activeFields)
	maps.Copy(usedForComputeds, timerFields)
	m.Analysis.PruneUnusedComputeds(usedForComputeds)

	// Note: we intentionally do NOT remove handlers based on mutation
	// analysis. A handler that writes to state has observable side effects
	// even if no updater currently reads that field (extern code, tests,
	// or future updaters may depend on it).

	// Pass 7: Prune helpers not referenced in any generated code.
	pruneHelpers(m)
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
		maps.Copy(usedFields, h.Mutated)
	}
	for _, t := range m.Timers {
		usedFields[t.ActiveVar] = true
		maps.Copy(usedFields, t.Mutated)
	}

	// Pass 3: Prune dead computed fields.
	m.Analysis.PruneUnusedComputeds(usedFields)
}

// StaticFields returns model fields that are never mutated by any handler
// or timer. These are effectively constants after initialization.
func StaticFields(handlers []Handler, timers []TimerHandler, modelFields map[string]bool) map[string]bool {
	mutated := make(map[string]bool)
	for _, h := range handlers {
		maps.Copy(mutated, h.Mutated)
	}
	for _, t := range timers {
		mutated[t.ActiveVar] = true
		maps.Copy(mutated, t.Mutated)
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
		target string
		deps   string // sorted dep names joined
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

	depsKey := func(deps map[string]bool) string {
		sorted := make([]string, 0, len(deps))
		for k := range deps {
			sorted = append(sorted, k)
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
		key := groupKey{target: targetOf(u), deps: depsKey(u.Deps)}
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
			Name: first.Name,
			Kind: "merged",
			Node: first.Node,
			Expr: first.Expr,
			Body: strings.Join(bodies, "\n"),
			Deps: first.Deps,
		}
		out = append(out, merged)
	}
	return out
}

// pruneHelpers removes entries from Analysis.Helpers that don't appear
// in any updater body or handler body string.
func pruneHelpers(m *MutationModel) {
	if len(m.Analysis.Helpers) == 0 {
		return
	}

	// Collect all generated code bodies.
	var bodies []string
	for _, u := range m.Updaters {
		bodies = append(bodies, u.Body)
	}

	referenced := make(map[string]bool)
	for name := range m.Analysis.Helpers {
		for _, body := range bodies {
			if strings.Contains(body, name) {
				referenced[name] = true
				break
			}
		}
	}

	for name := range m.Analysis.Helpers {
		if !referenced[name] {
			delete(m.Analysis.Helpers, name)
		}
	}
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
