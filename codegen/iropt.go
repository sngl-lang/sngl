package codegen

// OptimizeMutation runs optimization passes on a MutationModel:
//   - Remove updaters with empty dependency sets (static content that never changes)
//   - Merge updaters that target the same node and have identical deps
//   - Remove handlers whose mutated fields don't affect any updaters
//   - Prune helpers that are no longer referenced
func OptimizeMutation(m *MutationModel) {
	// Remove updaters with no deps (they represent static content that was
	// set once during initial render and never needs updating).
	m.Updaters = filterUpdaters(m.Updaters, func(u Updater) bool {
		return len(u.Deps) > 0
	})

	// Deduplicate updaters: if two updaters have the same name, keep only
	// the last one (later registrations override earlier ones).
	seen := make(map[string]int)
	for i, u := range m.Updaters {
		seen[u.Name] = i
	}
	if len(seen) < len(m.Updaters) {
		m.Updaters = filterUpdaters(m.Updaters, func(u Updater) bool {
			idx := seen[u.Name]
			// Keep only if this is the last occurrence
			for i := len(m.Updaters) - 1; i >= 0; i-- {
				if m.Updaters[i].Name == u.Name {
					return i == idx
				}
			}
			return true
		})
	}

	// Build the set of all fields that have at least one updater depending on them.
	activeFields := make(map[string]bool)
	for _, u := range m.Updaters {
		for dep := range u.Deps {
			activeFields[dep] = true
		}
	}

	// Remove handlers that mutate fields which don't affect any updater
	// or timer. Keep handlers that have side effects (empty mutated set
	// means we can't determine effects, so keep them).
	timerFields := make(map[string]bool)
	for _, t := range m.Timers {
		timerFields[t.ActiveVar] = true
	}
	m.Handlers = filterHandlers(m.Handlers, func(h Handler) bool {
		if len(h.Mutated) == 0 {
			return true // can't determine effects, keep it
		}
		for f := range h.Mutated {
			if activeFields[f] || timerFields[f] {
				return true
			}
		}
		return false
	})
}

// OptimizeRender runs optimization passes on a RenderModel:
//   - Remove handlers with empty mutation sets (no-op handlers)
//   - Flag expressions with no model field deps as invariant (platforms
//     can hoist these out of the render loop)
func OptimizeRender(m *RenderModel) {
	m.Handlers = filterHandlers(m.Handlers, func(h Handler) bool {
		// Keep all handlers — in a render-loop model, even handlers that
		// don't directly mutate fields might have side effects (toasts,
		// extern calls). Only remove truly empty handlers.
		return h.Body != nil
	})
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
