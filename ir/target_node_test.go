package ir

import "testing"

// A build-target node is found by its family and its #[gen.name], not by the
// name `platform` or `language` the repository's own packages give it by
// convention; and the family is found by its mark, not by its name, so a
// package declaring a `platform` family of its own declares a different one.
func TestTargetNodeOfFindsTheNodeByItsFamily(t *testing.T) {
	familyOfFamilies := &Component{Name: "family", Builtin: BuiltinTreeFamily}
	familyOfFamilies.Tree = familyOfFamilies
	plat := &Component{Name: "platform", Tree: familyOfFamilies, Builtin: BuiltinTreePlatform}
	lang := &Component{Name: "language", Tree: familyOfFamilies, Builtin: BuiltinTreeLanguage}
	impostor := &Component{Name: "platform", Tree: familyOfFamilies}

	shell := &Component{Name: "shell", Tree: plat, Gen: &GenCaps{TargetName: "shell"}}
	other := &Component{Name: "platform", Tree: impostor, Gen: &GenCaps{TargetName: "fake"}}
	pkg := &Package{Components: []*Component{other, shell}}

	if got := TargetNodeOf(pkg, BuiltinPlatform); got != shell {
		t.Errorf("TargetNodeOf(platform) = %v, want the node named shell", got)
	}
	if got := TargetNodeOf(pkg, BuiltinLanguage); got != nil {
		t.Errorf("TargetNodeOf(language) = %v, want none", got)
	}
	if TargetTier(lang) != BuiltinLanguage || TargetTier(plat) != BuiltinPlatform {
		t.Error("the marked families answer their tiers")
	}
	if IsBuildTargetTree(impostor) {
		t.Error("a family named platform without the mark is not the build tier")
	}
}
