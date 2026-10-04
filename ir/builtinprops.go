package ir

import (
	"maps"
	"slices"
)

// The props, events and slots of a #[builtin] declaration that compiler code
// reads by name. A kind identifies the declaration; what the compiler then
// reads off a node of it is the declaration's own vocabulary, which lives in
// lib/ and can be renamed there. Each name is written once, here, and
// BuiltinVocabulary lists them per kind so a library test can hold each
// declaration to declaring them: renamed in lib/ alone, every reader would
// otherwise find nothing and fall back without a word.
//
// A name a lowering *writes* -- a prop it synthesizes onto a node -- is one too,
// for the same reason.

// sngl:ui/nav.
const (
	NavPageHref    = "href"
	NavPageParams  = "params"
	NavLinkTo      = "to"
	NavLinkText    = "text"
	NavLinkStyle   = "style"
	NavLinkParams  = "params"
	NavLinkClick   = "click"
	NavCurrentID   = "nav.current"
	NavGoID        = "nav.go"
	NavBackID      = "nav.back"
	NavFollowID    = "nav.follow"
	NavHrefID      = "nav.href"
	NavGoParamsArg = "params"
)

// sngl:x/gen's gen.emit and gen.node.
const (
	GenEmitFile    = "file"
	GenEmitOpen    = "open"
	GenEmitClose   = "close"
	GenEmitRender  = "render"
	GenEmitMembers = "members"
	GenNodeOpen    = "open"
	GenNodeClose   = "close"
	GenNodeValue   = "value"
)

// Vocabulary is what compiler code reads by name off a declaration of one
// kind: its props, its events, and the intrinsic ids of its methods.
type Vocabulary struct {
	Props   []string
	Events  []string
	Methods []string
}

// BuiltinVocabulary is each kind's Vocabulary.
var BuiltinVocabulary = map[BuiltinKind]Vocabulary{
	BuiltinNavPage:  {Props: []string{NavPageHref, NavPageParams}},
	BuiltinNavLink:  {Props: []string{NavLinkTo, NavLinkText, NavLinkStyle, NavLinkParams}, Events: []string{NavLinkClick}},
	BuiltinNavStack: {Methods: []string{NavCurrentID, NavGoID, NavBackID}},
	BuiltinGenEmit:  {Props: []string{GenEmitFile, GenEmitOpen, GenEmitClose, GenEmitRender, GenEmitMembers}},
	BuiltinGenNode:  {Props: []string{GenNodeOpen, GenNodeClose, GenNodeValue}},
}

// MethodByIntrinsic is c's method carrying the intrinsic id, or nil: how a
// builtin's method is found, rather than by the name it is declared under.
func (c *Component) MethodByIntrinsic(id string) *Func {
	if c == nil {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(c.Methods)) {
		if fn := c.Methods[name]; fn != nil && fn.Intrinsic == id {
			return fn
		}
	}
	return nil
}

// RestPopulation is what the call site wrote for its component's rest slot by
// name -- `component content(p) { … }` -- or nil: the population that reaches
// the slot's arguments, found by the slot's declaration rather than its name.
func (n *NodeInst) RestPopulation() *SlotContent {
	if n == nil || n.Component == nil {
		return nil
	}
	rest := n.Component.RestSlot()
	if rest == nil {
		return nil
	}
	return n.Slots[rest.Name]
}
