package android

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/kotlin"
)

// The scaffold is written straight to its writer rather than through
// text/template: text/template looks names up with
// reflect.Value.MethodByName, and a binary that can reach that call keeps
// every exported method of every type it holds -- half the playground's wasm.

// scaffoldSources returns the scaffold's files under their names in the
// project's own layout, which scaffoldFiles remaps for Gradle. The Gradle
// build files are only there when d.Gradle, the adaptive-icon XML when
// d.HasAdaptiveIcon, and colors.xml when d.Color is set.
func scaffoldSources(d scaffoldData) []*codegen.OutputFile {
	file := func(name string, write func(io.Writer, scaffoldData)) *codegen.OutputFile {
		return codegen.WriterFile(name, func(w io.Writer) { write(w, d) })
	}
	printf := func(name, format string, args ...any) *codegen.OutputFile {
		return codegen.WriterFile(name, func(w io.Writer) { fmt.Fprintf(w, format, args...) })
	}
	static := func(name, content string) *codegen.OutputFile {
		return codegen.BytesFile(name, []byte(content))
	}
	files := []*codegen.OutputFile{
		file("AndroidManifest.xml", writeManifestXML),
		file("MainActivity.kt", writeMainActivityKt),
		file("Theme.kt", writeThemeKt),
	}
	if d.Gradle {
		files = append(files,
			file("gradle/app.build.gradle.kts", writeAppBuildGradle),
			file("gradle/build.gradle.kts", writeRootBuildGradle),
			static("gradle/gradle.properties", gradleProperties),
			printf("gradle/gradlew", gradlew, d.GradleVersion),
			file("gradle/settings.gradle.kts", writeSettingsGradle),
			printf("gradle/wrapper/gradle-wrapper.properties", gradleWrapperProperties, d.GradleVersion),
		)
	}
	if d.HasAdaptiveIcon {
		files = append(files,
			static("res/mipmap-anydpi-v26/ic_launcher.xml", adaptiveIconXML),
			static("res/mipmap-anydpi-v26/ic_launcher_round.xml", adaptiveIconXML),
		)
	}
	if d.Color != "" {
		files = append(files, printf("res/values/colors.xml", colorsXML, d.Color, d.Color))
	}
	return files
}

const launcherIntentFilter = `
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>`

func writeManifestXML(w io.Writer, d scaffoldData) {
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="%s"
    android:versionCode="1"
    android:versionName="1.0">
    <uses-sdk android:minSdkVersion="%d" android:targetSdkVersion="%d" />`, d.Package, d.MinSdk, d.TargetSdk)
	for _, p := range d.Permissions {
		fmt.Fprintf(w, `
    <uses-permission android:name="%s" />`, p)
	}
	fmt.Fprintf(w, `
    <application
        android:allowBackup="true"
        android:label="%s"`, d.AppName)
	if d.HasIcon {
		io.WriteString(w, `
        android:icon="@mipmap/ic_launcher"
        android:roundIcon="@mipmap/ic_launcher_round"`)
	}
	io.WriteString(w, `
        android:supportsRtl="true"
        android:theme="@android:style/Theme.Material.Light.NoActionBar">`)
	activities := d.Activities
	if len(activities) == 0 {
		activities = []ManifestActivity{{Name: "MainActivity", IsLauncher: true}}
	}
	for _, a := range activities {
		fmt.Fprintf(w, `
        <activity
            android:name=".%s"
            android:exported="true">`, a.Name)
		if a.IsLauncher {
			io.WriteString(w, launcherIntentFilter)
		}
		io.WriteString(w, `
        </activity>`)
	}
	io.WriteString(w, `
    </application>
</manifest>
`)
}

// kotlinFile is a Kotlin FileEmitter writing package pkg to w.
func kotlinFile(w io.Writer, pkg string) codegen.FileEmitter {
	return (&kotlin.Translator{}).NewFileEmitter(writerSink{w}, codegen.FileOptions{PackageName: pkg})
}

// writerSink is a Sink whose every file is w, for an emitter whose one file
// an OutputFile is writing.
type writerSink struct{ w io.Writer }

func (s writerSink) Create(string) (io.WriteCloser, error) { return nopCloser{s.w}, nil }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func writeMainActivityKt(w io.Writer, d scaffoldData) {
	writeActivityKt(w, d, "MainActivity", "")
}

// writeActivityKt writes the Activity class that shows a screen: MainActivity
// showing MainScreen, or for a batch snapshot one Activity per doc, showing
// the MainScreen screenImport names in the doc's own package (`fqn as Alias`).
func writeActivityKt(w io.Writer, d scaffoldData, class, screenImport string) {
	e := kotlinFile(w, d.Package)
	e.RequireImport("android.os.Bundle")
	e.RequireImport("androidx.activity.ComponentActivity")
	e.RequireImport("androidx.activity.compose.setContent")
	e.RequireImport("androidx.activity.enableEdgeToEdge")
	e.RequireImport("androidx.activity.SystemBarStyle")
	e.RequireImport(d.Package + ".ui.theme.AppTheme")
	screen := "MainScreen"
	if screenImport != "" {
		screen = e.RequireImport(screenImport)
	}
	if d.HasI18n {
		e.RequireImport("us.duckfam.git.jonathan.sngl.i18n.I18n")
	}
	fmt.Fprintf(e, `class %s : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The theme is always light, so the bars' icons are always dark.
        // The default follows the device's dark mode, which on a dark device
        // drew white icons over the app's white.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.light(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.light(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT),
        )`, class)
	if d.HasI18n {
		io.WriteString(e, `
        I18n.init(this)`)
	}
	if d.HasAssets {
		io.WriteString(e, `
        SnglAssets.init(this)`)
	}
	if d.HasSettings {
		io.WriteString(e, `
        SnglSettings.init(this)`)
	}
	if d.HasTestAgentBootstrap {
		io.WriteString(e, `
        if ((intent?.getIntExtra("SNGL_AGENT_PORT", 0) ?: 0) > 0) {
            TestAgentBootstrap.start(this)
        }`)
	}
	fmt.Fprintf(e, `
        setContent {
            AppTheme {
                %s()
            }
        }
    }
}
`, screen)
	e.Close()
}

// writeThemeKt writes AppTheme: Material's light scheme with the program's
// accent as its primary where `color` sets one, and otherwise the device's
// own colors where it has them (Android 12 on), Material's baseline before.
func writeThemeKt(w io.Writer, d scaffoldData) {
	e := kotlinFile(w, d.Package+".ui.theme")
	e.RequireImport("androidx.compose.material3.MaterialTheme")
	e.RequireImport("androidx.compose.material3.lightColorScheme")
	e.RequireImport("androidx.compose.runtime.Composable")
	if a, ok := accentARGB(d.Accent); ok {
		e.RequireImport("androidx.compose.ui.graphics.Color")
		fmt.Fprintf(e, `private val Accent = Color(0x%s)

private val AccentColorScheme = lightColorScheme(
    primary = Accent,
    onPrimary = Color.White,
    secondary = Accent,
    onSecondary = Color.White,
    tertiary = Accent,
)

@Composable
fun AppTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = AccentColorScheme,
        content = content
    )
}
`, a)
		e.Close()
		return
	}
	e.RequireImport("androidx.compose.material3.dynamicLightColorScheme")
	e.RequireImport("androidx.compose.ui.platform.LocalContext")
	io.WriteString(e, `private val DefaultColorScheme = lightColorScheme()

@Composable
fun AppTheme(content: @Composable () -> Unit) {
    val colorScheme = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.S) {
        dynamicLightColorScheme(LocalContext.current)
    } else {
        DefaultColorScheme
    }
    MaterialTheme(
        colorScheme = colorScheme,
        content = content
    )
}
`)
	e.Close()
}

// accentARGB is a `#RRGGBB` color as the hex of an opaque ARGB Long, which
// Compose's Color takes; false for anything else.
func accentARGB(hex string) (string, bool) {
	h, ok := strings.CutPrefix(hex, "#")
	if !ok || len(h) != 6 {
		return "", false
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil {
		return "", false
	}
	return "FF" + strings.ToUpper(h), true
}

func writeAppBuildGradle(w io.Writer, d scaffoldData) {
	io.WriteString(w, `plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")`)
	if d.HasNav {
		io.WriteString(w, `
    id("org.jetbrains.kotlin.plugin.serialization")`)
	}
	fmt.Fprintf(w, `
}

android {
    namespace = "%[1]s"
    compileSdk = %[2]d

    defaultConfig {
        applicationId = "%[1]s"
        minSdk = %[3]d
        targetSdk = %[4]d
        versionCode = 1
        versionName = "1.0"
    }

    buildFeatures {
        compose = true
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }`, d.Package, d.CompileSdk, d.MinSdk, d.TargetSdk)
	if d.TestMode {
		io.WriteString(w, `

    // Robolectric needs real Android resources packaged into the
    // unit-test classpath; otherwise Compose tests fail at
    // Resources.NotFound on the first inflate.
    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.all {
            // Forward sngl.agent.port from gradle's JVM to the forked
            // test JVM so MainScreenAgentTest can dial back to the
            // driver-side listener. Gradle does NOT propagate -D
            // automatically — has to be wired explicitly.
            it.systemProperty(
                "sngl.agent.port",
                System.getProperty("sngl.agent.port") ?: ""
            )
        }
    }`)
	}
	fmt.Fprintf(w, `
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:%s"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.activity:activity-compose:%s")`, d.ComposeBOM, d.ActivityCompose)
	if d.HasNav {
		fmt.Fprintf(w, `
    implementation("androidx.navigation:navigation-compose:%s")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:%s")`, d.Navigation, d.Serialization)
	}
	io.WriteString(w, `
    debugImplementation("androidx.compose.ui:ui-tooling")`)
	if d.HasGoLib {
		io.WriteString(w, `
    implementation(files("golib.aar"))`)
	}
	if d.HasTestAgent {
		io.WriteString(w, `
    implementation("us.duckfam.git.jonathan.sngl:testagent")`)
	}
	if d.TestMode {
		io.WriteString(w, `

    // Compose UI tests run via Robolectric on the JVM — no emulator
    // needed. ui-test-manifest is debug-scoped because it injects a
    // ComponentActivity into the test manifest; ui-test-junit4 brings
    // createComposeRule() and the finder/asserter DSL.
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.13")
    testImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-test-manifest")`)
	}
	io.WriteString(w, `
}
`)
}

func writeRootBuildGradle(w io.Writer, d scaffoldData) {
	fmt.Fprintf(w, `plugins {
    id("com.android.application") version "%s" apply false
    id("org.jetbrains.kotlin.android") version "%s" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "%s" apply false`, d.AGPVersion, d.KotlinVersion, d.ComposePluginV)
	if d.HasNav {
		fmt.Fprintf(w, `
    id("org.jetbrains.kotlin.plugin.serialization") version "%s" apply false`, d.KotlinVersion)
	}
	io.WriteString(w, `
}
`)
}

const gradleProperties = `android.useAndroidX=true
kotlin.code.style=official
# A generated composable is one long function, and the Compose plugin's IR
# lowering of one runs out of the default heap -- examples/showcase died with
# "GC overhead limit exceeded" rather than a diagnostic. Kotlin compiles in
# Gradle's daemon, whose heap holds both, rather than in a daemon of its own
# that outlives the build.
org.gradle.jvmargs=-Xmx3g
kotlin.compiler.execution.strategy=in-process
`

// gradlew takes the Gradle version.
const gradlew = `#!/bin/sh
# Lightweight Gradle bootstrap — downloads and caches a Gradle distribution.
set -e

GRADLE_VERSION="%s"
GRADLE_URL="https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip"
GRADLE_CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/wrapper/dists/gradle-${GRADLE_VERSION}-bin"
GRADLE_BIN="$GRADLE_CACHE/gradle-${GRADLE_VERSION}/bin/gradle"

if [ ! -x "$GRADLE_BIN" ]; then
    echo "Downloading Gradle $GRADLE_VERSION..." >&2
    mkdir -p "$GRADLE_CACHE"
    DIST_ZIP="$GRADLE_CACHE/gradle-${GRADLE_VERSION}-bin.zip"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$DIST_ZIP" "$GRADLE_URL"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$DIST_ZIP" "$GRADLE_URL"
    else
        echo "Error: curl or wget required to download Gradle" >&2
        exit 1
    fi
    unzip -q -o "$DIST_ZIP" -d "$GRADLE_CACHE"
    rm -f "$DIST_ZIP"
fi

exec "$GRADLE_BIN" "$@"
`

func writeSettingsGradle(w io.Writer, d scaffoldData) {
	fmt.Fprintf(w, `pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.PREFER_SETTINGS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "%s"
include(":app")`, d.AppName)
	if d.HasTestAgent {
		fmt.Fprintf(w, `
includeBuild("%s")`, d.TestAgentInclude)
	}
	io.WriteString(w, "\n")
}

// gradleWrapperProperties takes the Gradle version.
const gradleWrapperProperties = `distributionBase=GRADLE_USER_HOME
distributionPath=wrapper/dists
distributionUrl=https\://services.gradle.org/distributions/gradle-%s-bin.zip
zipStoreBase=GRADLE_USER_HOME
zipStorePath=wrapper/dists
`

const adaptiveIconXML = `<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background"/>
    <foreground android:drawable="@drawable/ic_launcher_foreground"/>
</adaptive-icon>
`

// colorsXML takes the color twice.
const colorsXML = `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="ic_launcher_background">%s</color>
    <color name="theme_primary">%s</color>
</resources>
`
