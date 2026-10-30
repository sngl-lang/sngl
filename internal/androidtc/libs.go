package androidtc

// Lib is one library a gradle-free build compiles and dexes against: an AAR
// or a jar, where to download it, and its SHA-256.
type Lib struct {
	Group, Module, Version string
	File                   string // file name, <module>-<version>.aar or .jar
	URL                    string
	SHA256                 string
}

// Libs is the resolved closure of the combo's LibraryRoots, which
// `go run ./internal/androidtc/resolvelibs` writes into libs_gen.go. It is
// what Gradle resolves for the scaffold, without the Kotlin standard library
// the kotlinc distribution carries.
func (c Combo) Libs() []Lib { return comboLibs[c.Name] }

// LibraryRoots are the libraries the Gradle scaffold declares, as
// group:module[:version], and the BOM that versions the ones without one.
func (c Combo) LibraryRoots() (roots []string, bom string) {
	return []string{
		"androidx.compose.ui:ui",
		"androidx.compose.material3:material3",
		"androidx.compose.ui:ui-tooling-preview",
		"androidx.activity:activity-compose:" + c.ActivityCompose,
		"androidx.navigation:navigation-compose:" + c.Navigation,
		"org.jetbrains.kotlinx:kotlinx-serialization-json:" + c.Serialization,
	}, "androidx.compose:compose-bom:" + c.ComposeBOM
}

// Combos is every combo, for the generator.
func Combos() []Combo { return append([]Combo(nil), combos...) }
