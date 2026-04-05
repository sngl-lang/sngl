package docsite

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// TierOrder defines the canonical ordering and grouping of component tiers.
var TierOrder = []string{
	"Core",
	"Tier 1: Core Input",
	"Tier 2: Feedback & Navigation",
	"Tier 3: Overlays & Layout",
	"Tier 4: Data & Desktop",
	"Tier 5: Mobile & Specialized",
}

// AssignTiers maps component names to their tier based on the stdlib
// components.sngl file. It reads the embedded stdlib to find tier boundary
// comments.
func AssignTiers(registry checker.SchemaRegistry) map[string]string {
	tiers := map[string]string{}
	data, err := checker.StdlibFS().ReadFile("stdlib/components.sngl")
	if err != nil {
		for name := range registry {
			tiers[name] = "Core"
		}
		return tiers
	}
	lines := strings.Split(string(data), "\n")

	currentTier := "Core"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// ---") && strings.HasSuffix(trimmed, "---") {
			tierName := strings.TrimPrefix(trimmed, "// ---")
			tierName = strings.TrimSuffix(tierName, "---")
			tierName = strings.TrimSpace(tierName)
			if tierName != "" {
				currentTier = tierName
			}
			continue
		}
		if strings.HasPrefix(trimmed, "component ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				tiers[parts[1]] = currentTier
			}
		}
	}
	return tiers
}

// ChildPolicyString converts a ChildPolicy to a human-readable string.
func ChildPolicyString(cp checker.ChildPolicy) string {
	switch cp {
	case checker.ChildrenNone:
		return "none"
	case checker.ChildrenOne:
		return "one"
	case checker.ChildrenMany:
		return "many"
	default:
		return "unknown"
	}
}
