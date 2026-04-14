package docsite

import "git.duckfam.us/jonathan/sngl/internal/checker"

// TierOrder defines the canonical ordering and grouping of component tiers.
var TierOrder = []string{
	"Core",
	"Tier 1: Core Input",
	"Tier 2: Feedback & Navigation",
	"Tier 3: Overlays & Layout",
	"Tier 4: Data & Desktop",
	"Tier 5: Mobile & Specialized",
}

// tierAssignments maps component names to their tier.
var tierAssignments = map[string]string{
	// Core
	"vbox": "Core", "hbox": "Core", "stack": "Core", "text": "Core", "spacer": "Core",
	// Tier 1
	"button": "Tier 1: Core Input", "input": "Tier 1: Core Input", "image": "Tier 1: Core Input",
	"checkbox": "Tier 1: Core Input", "toggle": "Tier 1: Core Input", "link": "Tier 1: Core Input",
	// Tier 2
	"progress": "Tier 2: Feedback & Navigation", "slider": "Tier 2: Feedback & Navigation",
	"badge": "Tier 2: Feedback & Navigation", "divider": "Tier 2: Feedback & Navigation",
	"scroll": "Tier 2: Feedback & Navigation", "radio": "Tier 2: Feedback & Navigation",
	"select": "Tier 2: Feedback & Navigation", "tabs": "Tier 2: Feedback & Navigation",
	"tab": "Tier 2: Feedback & Navigation",
	// Tier 3
	"dialog": "Tier 3: Overlays & Layout", "toast": "Tier 3: Overlays & Layout",
	"drawer": "Tier 3: Overlays & Layout", "card": "Tier 3: Overlays & Layout",
	"grid": "Tier 3: Overlays & Layout", "tooltip": "Tier 3: Overlays & Layout",
	// Tier 4
	"table": "Tier 4: Data & Desktop", "list": "Tier 4: Data & Desktop",
	"datepicker": "Tier 4: Data & Desktop", "tree": "Tier 4: Data & Desktop",
	"menu": "Tier 4: Data & Desktop", "menuitem": "Tier 4: Data & Desktop",
	// Tier 5
	"map": "Tier 5: Mobile & Specialized", "video": "Tier 5: Mobile & Specialized",
	"audio": "Tier 5: Mobile & Specialized", "canvas": "Tier 5: Mobile & Specialized",
	"webview": "Tier 5: Mobile & Specialized",
}

// AssignTiers returns a map from component name to tier for all components in the registry.
func AssignTiers(registry checker.SchemaRegistry) map[string]string {
	result := make(map[string]string, len(registry))
	for name := range registry {
		if tier, ok := tierAssignments[name]; ok {
			result[name] = tier
		} else {
			result[name] = "Core"
		}
	}
	return result
}

// ChildPolicyString returns a human-readable string for a component's children policy.
func ChildPolicyString(children *checker.Type) string {
	if children == nil {
		return "none"
	}
	return "many"
}
