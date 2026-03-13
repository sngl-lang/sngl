package checker

import "github.com/google/cel-go/cel"

// ChildPolicy specifies how many children a component allows.
type ChildPolicy int

const (
	ChildrenNone ChildPolicy = iota
	ChildrenOne
	ChildrenMany
)

// PropSchema describes a component property's type and valid enum values.
type PropSchema struct {
	Type *cel.Type
	Enum []string
}

// ComponentSchema defines the properties, events, and child policy for a component.
type ComponentSchema struct {
	Props    map[string]PropSchema
	Events   map[string]string // event name → event type key
	Children ChildPolicy
}

// SchemaRegistry maps component names to their schemas.
type SchemaRegistry map[string]*ComponentSchema
