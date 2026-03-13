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

// NewStandardRegistry returns the registry of standard SNGL components.
func NewStandardRegistry() SchemaRegistry {
	return SchemaRegistry{
		"vbox": {
			Props:    map[string]PropSchema{},
			Events:   map[string]string{},
			Children: ChildrenMany,
		},
		"hbox": {
			Props:    map[string]PropSchema{},
			Events:   map[string]string{},
			Children: ChildrenMany,
		},
		"stack": {
			Props:    map[string]PropSchema{},
			Events:   map[string]string{},
			Children: ChildrenMany,
		},
		"text": {
			Props: map[string]PropSchema{
				"value":      {Type: cel.StringType},
				"selectable": {Type: cel.BoolType},
			},
			Events:   map[string]string{"click": "ClickEvent"},
			Children: ChildrenNone,
		},
		"button": {
			Props: map[string]PropSchema{
				"text":     {Type: cel.StringType},
				"disabled": {Type: cel.BoolType},
			},
			Events:   map[string]string{"click": "ClickEvent", "long-press": "LongPressEvent"},
			Children: ChildrenMany,
		},
		"input": {
			Props: map[string]PropSchema{
				"value":       {Type: cel.StringType},
				"placeholder": {Type: cel.StringType},
				"disabled":    {Type: cel.BoolType},
				"readonly":    {Type: cel.BoolType},
				"type":        {Type: cel.StringType, Enum: []string{"text", "password", "number", "email", "url", "tel", "search"}},
				"max-length":  {Type: cel.IntType},
			},
			Events: map[string]string{
				"input":  "InputEvent",
				"change": "ChangeEvent",
				"focus":  "FocusEvent",
				"blur":   "FocusEvent",
				"submit": "SubmitEvent",
			},
			Children: ChildrenNone,
		},
		"image": {
			Props: map[string]PropSchema{
				"src": {Type: cel.StringType},
				"alt": {Type: cel.StringType},
				"fit": {Type: cel.StringType, Enum: []string{"contain", "cover", "fill", "none", "scale-down"}},
			},
			Events: map[string]string{
				"click": "ClickEvent",
				"load":  "LoadEvent",
				"error": "ErrorEvent",
			},
			Children: ChildrenNone,
		},
		"scroll": {
			Props: map[string]PropSchema{
				"direction":      {Type: cel.StringType, Enum: []string{"vertical", "horizontal", "both"}},
				"scroll-x":       {Type: cel.DoubleType},
				"scroll-y":       {Type: cel.DoubleType},
				"show-scrollbar": {Type: cel.StringType, Enum: []string{"auto", "always", "never"}},
			},
			Events: map[string]string{
				"scroll":     "ScrollEvent",
				"scroll-end": "ScrollEvent",
			},
			Children: ChildrenOne,
		},
		"spacer": {
			Props: map[string]PropSchema{
				"size": {Type: cel.DynType},
			},
			Events:   map[string]string{},
			Children: ChildrenNone,
		},
		"checkbox": {
			Props: map[string]PropSchema{
				"checked":  {Type: cel.BoolType},
				"label":    {Type: cel.StringType},
				"disabled": {Type: cel.BoolType},
			},
			Events:   map[string]string{"change": "ChangeEvent"},
			Children: ChildrenNone,
		},
		"slot": {
			Props:    map[string]PropSchema{},
			Events:   map[string]string{},
			Children: ChildrenNone,
		},
	}
}
