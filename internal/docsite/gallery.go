package docsite

// TierOrder defines the canonical ordering and grouping of component tiers.
var TierOrder = []string{
	"Core",
	"Tier 1: Core Input",
	"Tier 2: Feedback & Navigation",
	"Tier 3: Overlays & Layout",
	"Tier 4: Data & Desktop",
	"Tier 5: Mobile & Specialized",
}

// TODO: AssignTiers and ChildPolicyString need porting once v2 checker has stdlib support.
