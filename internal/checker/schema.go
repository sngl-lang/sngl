package checker

import "git.duckfam.us/jonathan/sngl/ast"

// ChildPolicy specifies how many children a component allows.
type ChildPolicy int

const (
	ChildrenNone     ChildPolicy = iota
	ChildrenOne                  // component
	ChildrenMany                 // list<component>
	ChildrenOptional             // option<component>
)

// PropSchema describes a component property's type and valid enum values.
type PropSchema struct {
	Type Type
	Enum []string
	Doc  string
}

// ComponentSchema defines the properties, events, and child policy for a component.
type ComponentSchema struct {
	Props          map[string]PropSchema
	Events         map[string]string // event name → event type key
	Children       ChildPolicy
	Doc            string
	Body           []*ast.VisualNode            // default body (nil = pure abstract)
	PlatformBodies map[string][]*ast.VisualNode // platform-conditional bodies
	Permissive     bool                         // true for dynamically resolved elements (accept any props/events)
}

// StylePropSchema describes a style property's type and valid enum values.
type StylePropSchema struct {
	Type Type
	Enum []string
}

// childrenFromType converts a ChildrenType string to a ChildPolicy.
// childrenFromType converts a ChildrenType string (internal format from parseTypeString)
// to a ChildPolicy. Internal format uses : separator (e.g., "list:component").
func childrenFromType(ct string) ChildPolicy {
	switch ct {
	case "":
		return ChildrenNone
	case "component":
		return ChildrenOne
	case "option:component":
		return ChildrenOptional
	default:
		if len(ct) > 5 && ct[:5] == "list:" {
			return ChildrenMany
		}
		return ChildrenNone
	}
}

// SchemaRegistry maps component names to their schemas.
type SchemaRegistry map[string]*ComponentSchema
