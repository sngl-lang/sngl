package lower

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// capabilityField maps each member of sngl:x/gen's Capability enum to the
// Features field it holds, and passField does the same for Pass.
//
// Two tables rather than one because they are the two statements the marks
// keep apart: a capability is what the target emits without help, a pass is a
// rewrite it asks for. A target that names a pass where a capability belongs
// gets "unknown" from the enum before this is reached.
//
// The tables are the compiler's half of a vocabulary declared in SNGL, so the
// two can drift. TestGenVocabularyIsMapped holds them together by reading the
// enums out of the package and asking for each member here.
var capabilityField = map[string]func(*Features) *bool{
	"toggle":           func(f *Features) *bool { return &f.Toggle },
	"ternary":          func(f *Features) *bool { return &f.Ternary },
	"lambda":           func(f *Features) *bool { return &f.Lambda },
	"ref":              func(f *Features) *bool { return &f.Ref },
	"unitType":         func(f *Features) *bool { return &f.Unit },
	"enumType":         func(f *Features) *bool { return &f.Enum },
	"asyncReactive":    func(f *Features) *bool { return &f.AsyncReactive },
	"asyncCalls":       func(f *Features) *bool { return &f.AsyncCalls },
	"computed":         func(f *Features) *bool { return &f.Computed },
	"listLambdas":      func(f *Features) *bool { return &f.ListLambdas },
	"reactivity":       func(f *Features) *bool { return &f.Reactivity },
	"declarative":      func(f *Features) *bool { return &f.Declarative },
	"inlineComponents": func(f *Features) *bool { return &f.InlineComponents },
	"implicitRecv":     func(f *Features) *bool { return &f.ImplicitRecv },
	"structSpread":     func(f *Features) *bool { return &f.StructSpread },
	"viewStatements":   func(f *Features) *bool { return &f.ViewStatements },
	"instanceState":    func(f *Features) *bool { return &f.InstanceState },
	"insertBefore":     func(f *Features) *bool { return &f.InsertBefore },
	"inlineSlots":      func(f *Features) *bool { return &f.InlineSlots },
	"asyncPost":        func(f *Features) *bool { return &f.AsyncPost },
	"asyncSpawn":       func(f *Features) *bool { return &f.AsyncSpawn },
	"effects":          func(f *Features) *bool { return &f.Effects },
	"navigation":       func(f *Features) *bool { return &f.Navigation },
}

var passField = map[string]func(*Features) *bool{
	"structComponents":   func(f *Features) *bool { return &f.StructComponents },
	"stdlibContextParam": func(f *Features) *bool { return &f.StdlibContextParam },
	"focusOrder":         func(f *Features) *bool { return &f.FocusOrder },
	"canvas":             func(f *Features) *bool { return &f.Canvas },
	"reactiveCanvas":     func(f *Features) *bool { return &f.ReactiveCanvas },
}

// FeaturesFrom reads a target's capabilities off the two build-tree nodes it
// was selected by: the language's, then the platform's overruling it per
// capability.
//
// The zero Features is the starting point and there is no base to subtract
// from, which is the polarity the marks declare: a capability not written is
// not held, so a target that says nothing gets every lowering pass. That is
// also what an out-of-process plugin answering nothing gets, and the reason
// the other polarity was not taken -- a silence that claims everything emits
// code the host compiler rejects.
func FeaturesFrom(lang, plat *ir.Component) (Features, error) {
	var f Features
	for _, c := range []*ir.Component{lang, plat} {
		g := ir.GenCapsOf(c)
		if g == nil {
			continue
		}
		if err := applyGen(&f, g, c.Name); err != nil {
			return Features{}, err
		}
	}
	return f, nil
}

func applyGen(f *Features, g *ir.GenCaps, who string) error {
	for _, set := range []struct {
		names []string
		table map[string]func(*Features) *bool
		val   bool
		mark  string
	}{
		{g.Can, capabilityField, true, "can"},
		{g.Cannot, capabilityField, false, "cannot"},
		{g.Wants, passField, true, "wants"},
	} {
		for _, n := range set.names {
			field, ok := set.table[n]
			if !ok {
				return fmt.Errorf("%s: #[gen.%s(%s)] names something this compiler has no field for (known: %s)", who, set.mark, n, knownNames(set.table))
			}
			*field(f) = set.val
		}
	}
	return nil
}

func knownNames(table map[string]func(*Features) *bool) string {
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// GenNameIsMapped reports whether a member of one of sngl:x/gen's enums has a
// Features field behind it. For the test that holds the SNGL vocabulary and
// these tables together; nothing in a build asks it, since an unmapped name
// reaching FeaturesFrom is an error naming the target that wrote it.
func GenNameIsMapped(enum, member string) bool {
	switch enum {
	case "Capability":
		_, ok := capabilityField[member]
		return ok
	case "Pass":
		_, ok := passField[member]
		return ok
	}
	return false
}
