package android

import (
	"strings"

	"duckfam.us/sngl/codegen"
)

// scaffoldData is what the scaffold's files are written from.
type scaffoldData struct {
	Package         string // e.g. "test.sngl.app"
	AppName         string // e.g. "My App"
	HasIcon         bool
	HasAdaptiveIcon bool   // true when icon is SVG (produces VectorDrawable foreground)
	Color           string // e.g. "#6750A4" (empty if not set)
	Gradle          bool   // true for Gradle scaffold
	HasGoLib        bool   // true when Go module (golib.aar) is included
	// Activities, when non-empty, replaces the default MainActivity
	// declaration in AndroidManifest.xml with one entry per element.
	// Used by batch snapshot to declare N activities in a single APK.
	Activities []ManifestActivity
	// HasI18n is true when the source package uses i18n intrinsics. When set,
	// MainActivity.kt emits I18n.init(this) in onCreate so the runtime
	// is initialised before Compose content is set.
	HasI18n bool
	// HasAssets is true when the program reads pictures out of the app's
	// assets, and MainActivity hands SnglAssets the context to read them with.
	HasAssets bool
	// Permissions are what the manifest asks for: what the program reaches
	// asks (permissions), and INTERNET for the on-device test agent.
	Permissions []string
	// TestMode is true when generating the project for the test
	// runner. Adds Robolectric + Compose UI test deps and the
	// testOptions block to app/build.gradle.kts.
	TestMode bool
	// HasTestAgentBootstrap, when true, causes MainActivity.kt to
	// call TestAgentBootstrap.start(this) in onCreate. Set under
	// testMode=agent && testRunner=device so the on-device agent host
	// starts its TCP listener when launched with SNGL_AGENT_PORT.
	HasTestAgentBootstrap bool
	// HasTestAgent, when true, adds the pkg/kotlin/testagent dependency
	// to app/build.gradle.kts so the on-device agent code compiles. Set
	// alongside HasTestAgentBootstrap. TestAgentInclude is the absolute
	// path to pkg/kotlin/testagent on the host, written into the root
	// settings.gradle.kts as `includeBuild(...)`.
	HasTestAgent     bool
	TestAgentInclude string

	// Toolchain versions (from the selected androidtc.Combo) injected into the
	// Gradle scaffold so every version reference has a single source of truth.
	GradleVersion   string
	AGPVersion      string
	KotlinVersion   string
	ComposePluginV  string
	ComposeBOM      string
	ActivityCompose string
	Coil            string
	// HasNav adds what a sngl:ui/nav stack is built on: navigation-compose,
	// and the serialization plugin its typed routes are written with.
	HasNav        bool
	Navigation    string
	Serialization string
	CompileSdk    int
	TargetSdk     int
	MinSdk        int
}

// ManifestActivity describes one <activity> entry to emit into the manifest
// when batch-snapshotting. Exactly one entry should have IsLauncher=true.
type ManifestActivity struct {
	Name       string
	IsLauncher bool
}

func newScaffoldData(cfg Config) scaffoldData {
	combo := cfg.combo()
	return scaffoldData{
		Package:         cfg.Package,
		AppName:         appLabel(cfg),
		HasIcon:         cfg.Icon != "",
		HasAdaptiveIcon: cfg.Icon != "" && strings.HasSuffix(strings.ToLower(cfg.Icon), ".svg"),
		Color:           cfg.Color,
		Gradle:          cfg.UseGradle(),
		HasGoLib:        cfg.GoLib,
		GradleVersion:   combo.Gradle,
		AGPVersion:      combo.AGP,
		KotlinVersion:   combo.Kotlin,
		ComposePluginV:  combo.ComposePlugin,
		ComposeBOM:      combo.ComposeBOM,
		ActivityCompose: combo.ActivityCompose,
		Coil:            combo.Coil,
		HasNav:          cfg.Nav,
		Navigation:      combo.Navigation,
		Serialization:   combo.Serialization,
		CompileSdk:      combo.CompileSdk,
		TargetSdk:       combo.CompileSdk,
		MinSdk:          cfg.minSdk(),
	}
}

// scaffoldFiles generates all scaffold files for a full Gradle project.
// hasI18n causes MainActivity to emit I18n.init(this).
// hasAssets causes it to emit SnglAssets.init(this).
// perms are the permissions the manifest asks for.
// bootstrap causes MainActivity to call TestAgentBootstrap.start(this);
// set under testMode=agent && testRunner=device.
func scaffoldFiles(cfg Config, hasI18n, hasAssets bool, perms []string, bootstrap bool, testAgentInclude string, testMode bool) []*codegen.OutputFile {
	data := newScaffoldData(cfg)
	data.HasI18n = hasI18n
	data.HasAssets = hasAssets
	data.Permissions = perms
	if bootstrap {
		// The on-device test agent listens on a loopback ServerSocket the
		// host driver adb-forwards a TCP port to.
		data.Permissions = withPermission(perms, internetPermission)
	}
	data.HasTestAgentBootstrap = bootstrap
	data.HasTestAgent = testAgentInclude != ""
	data.TestAgentInclude = testAgentInclude
	data.TestMode = testMode
	pkgPath := pkgToPath(cfg.Package)

	files := scaffoldSources(data)

	// Remap output paths for the Gradle project layout:
	// - MainActivity.kt / Theme.kt → app/src/main/java/{pkg}/...
	// - AndroidManifest.xml → app/src/main/...
	// - res/ → app/src/main/res/...
	// - gradle/* → root (strip gradle/ prefix for build files)
	for _, f := range files {
		switch {
		case f.Name == "MainActivity.kt":
			f.Name = "app/src/main/java/" + pkgPath + "/MainActivity.kt"
		case f.Name == "Theme.kt":
			f.Name = "app/src/main/java/" + pkgPath + "/ui/theme/Theme.kt"
		case f.Name == "AndroidManifest.xml":
			f.Name = "app/src/main/" + f.Name
		case strings.HasPrefix(f.Name, "res/"):
			f.Name = "app/src/main/" + f.Name
		case f.Name == "gradle/app.build.gradle.kts":
			f.Name = "app/build.gradle.kts"
		case f.Name == "gradle/build.gradle.kts":
			f.Name = "build.gradle.kts"
		case f.Name == "gradle/settings.gradle.kts":
			f.Name = "settings.gradle.kts"
		case f.Name == "gradle/gradle.properties":
			f.Name = "gradle.properties"
		case f.Name == "gradle/gradlew":
			f.Name = "gradlew"
		case f.Name == "gradle/wrapper/gradle-wrapper.properties":
			// keep as-is
		}
	}

	return files
}

// directBuildFiles returns the minimal files for a gradle-free build.
// The same files as scaffoldFiles, with Gradle=false so the Gradle files are left out.
// hasI18n causes MainActivity to emit I18n.init(this).
// hasAssets causes it to emit SnglAssets.init(this).
// perms are the permissions the manifest asks for.
// bootstrap causes MainActivity to call TestAgentBootstrap.start(this);
// set under testMode=agent && testRunner=device.
func directBuildFiles(cfg Config, hasI18n, hasAssets bool, perms []string, bootstrap bool) []*codegen.OutputFile {
	data := newScaffoldData(cfg)
	data.Gradle = false
	data.HasI18n = hasI18n
	data.HasAssets = hasAssets
	data.Permissions = perms
	if bootstrap {
		// The on-device test agent listens on a loopback ServerSocket the
		// host driver adb-forwards a TCP port to.
		data.Permissions = withPermission(perms, internetPermission)
	}
	data.HasTestAgentBootstrap = bootstrap
	return scaffoldSources(data)
}

// directBuildFilesWithActivities is the batch-snapshot variant of
// directBuildFiles: the manifest declares the given Activities (instead of a
// single default MainActivity) and the canonical MainActivity.kt is omitted —
// callers emit per-doc Activity sources separately.
func directBuildFilesWithActivities(cfg Config, activities []ManifestActivity) []*codegen.OutputFile {
	data := newScaffoldData(cfg)
	data.Gradle = false
	data.Activities = activities
	files := scaffoldSources(data)
	out := files[:0]
	for _, f := range files {
		if f.Name == "MainActivity.kt" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func appLabel(cfg Config) string {
	if cfg.AppName != "" {
		return cfg.AppName
	}
	return appNameFromPkg(cfg.Package)
}

func appNameFromPkg(pkg string) string {
	parts := strings.Split(pkg, ".")
	if len(parts) > 0 {
		return exportName(parts[len(parts)-1])
	}
	return "App"
}
