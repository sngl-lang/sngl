// Package androidtc defines the known-good Android toolchain combinations SNGL
// can build against — the mutually-compatible set of Gradle, Android Gradle
// Plugin, Kotlin, Compose, build-tools, and SDK versions, plus the JDK window
// each Gradle can launch on. It is a leaf package (no SNGL dependencies) so the
// android codegen platform and the test harness share one source of truth
// without an import cycle.
//
// Both build paths read versions from the selected Combo: the Gradle scaffold
// templates and the internal direct-build toolchain. Adding or bumping a combo
// here updates every version reference at once.
package androidtc

// Combo is one vetted set of interdependent Android build-tool versions.
type Combo struct {
	Name string // stable identifier used by the `toolchain=` build option

	// Gradle build path.
	Gradle        string // Gradle distribution version (wrapper)
	AGP           string // com.android.application plugin version
	Kotlin        string // org.jetbrains.kotlin.android plugin version
	ComposePlugin string // org.jetbrains.kotlin.plugin.compose version (K2)
	ComposeBOM    string // androidx.compose:compose-bom platform version

	// Direct-build path (kotlinc + aapt2 + d8, no Gradle).
	ComposeRuntime string // androidx.compose runtime/ui artifact version
	BuildTools     string // Android build-tools version

	// Shared SDK + AndroidX.
	CompileSdk      int    // compileSdk / default targetSdk (Android API level)
	ActivityCompose string // androidx.activity:activity-compose version
	Coil            string // io.coil-kt:coil-compose version

	// JDKMin/JDKMax bound the JDK major version this combo's Gradle can launch
	// on and compile against. The lower bound is the compile target; the upper
	// bound is the newest JDK the pinned Gradle accepts at startup.
	JDKMin int
	JDKMax int
}

// combos is ordered newest-first. combos[0] is the default — the latest entry
// we consider known-good. A newer, less-proven combo may sit above the default
// only once it build-verifies; until then the proven one stays first.
var combos = []Combo{
	// Verified end-to-end on Linux + JDK 21. compileSdk 35 (Android 15),
	// Gradle 8.11.1 (launches on JDK 8–23).
	{
		Name:            "stable",
		Gradle:          "8.11.1",
		AGP:             "8.7.3",
		Kotlin:          "2.1.0",
		ComposePlugin:   "2.1.0",
		ComposeBOM:      "2025.03.00",
		ComposeRuntime:  "1.7.6",
		BuildTools:      "35.0.0",
		CompileSdk:      35,
		ActivityCompose: "1.9.3",
		Coil:            "2.7.0",
		JDKMin:          17,
		JDKMax:          23,
	},
	// Newer combo: compileSdk 36 (Android 16), Gradle 9.1, whose window covers
	// JDK 24–25 so an Android Studio JBR-25 works without a separate LTS JDK.
	// Verified: `sngl build`/assembleDebug succeeds on JDK 25 (Gradle
	// auto-installs platform/build-tools 36). NOT the default because the
	// default Robolectric test runner can't emulate API 36 yet — JVM unit tests
	// fail at AndroidTestEnvironment. Use it for building and device tests; for
	// Robolectric on this combo, pair with `sdk=35`. Kept opt-in via
	// `toolchain=next` until Robolectric supports API 36.
	{
		Name:            "next",
		Gradle:          "9.1.0",
		AGP:             "8.13.0",
		Kotlin:          "2.1.20",
		ComposePlugin:   "2.1.20",
		ComposeBOM:      "2025.06.01",
		ComposeRuntime:  "1.8.2",
		BuildTools:      "36.0.0",
		CompileSdk:      36,
		ActivityCompose: "1.10.1",
		Coil:            "2.7.0",
		JDKMin:          17,
		JDKMax:          25,
	},
}

// defaultName is the combo used when the `toolchain=` option is unset. It is
// the newest entry we've confirmed known-good; keep it pointing at a verified
// combo, not merely the newest in the table.
const defaultName = "stable"

// Default returns the default toolchain combo.
func Default() Combo {
	if c, ok := ByName(defaultName); ok {
		return c
	}
	return combos[0]
}

// ByName returns the combo with the given name.
func ByName(name string) (Combo, bool) {
	for _, c := range combos {
		if c.Name == name {
			return c, true
		}
	}
	return Combo{}, false
}

// Names returns every known combo name, newest-first, for diagnostics.
func Names() []string {
	out := make([]string, len(combos))
	for i, c := range combos {
		out[i] = c.Name
	}
	return out
}
