package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func scaffoldFiles(cfg Config) []*codegen.OutputFile {
	pkgPath := pkgToPath(cfg.Package)
	var files []*codegen.OutputFile

	// MainActivity.kt
	files = append(files, &codegen.OutputFile{
		Name:    "app/src/main/java/" + pkgPath + "/MainActivity.kt",
		Content: []byte(mainActivityKt(cfg)),
	})

	// Theme.kt
	files = append(files, &codegen.OutputFile{
		Name:    "app/src/main/java/" + pkgPath + "/ui/theme/Theme.kt",
		Content: []byte(themeKt(cfg)),
	})

	// AndroidManifest.xml
	files = append(files, &codegen.OutputFile{
		Name:    "app/src/main/AndroidManifest.xml",
		Content: []byte(androidManifest(cfg)),
	})

	// app/build.gradle.kts
	files = append(files, &codegen.OutputFile{
		Name:    "app/build.gradle.kts",
		Content: []byte(appBuildGradle(cfg)),
	})

	// Root build.gradle.kts
	files = append(files, &codegen.OutputFile{
		Name:    "build.gradle.kts",
		Content: []byte(rootBuildGradle()),
	})

	// settings.gradle.kts
	files = append(files, &codegen.OutputFile{
		Name:    "settings.gradle.kts",
		Content: []byte(settingsGradle(cfg)),
	})

	// gradle.properties
	files = append(files, &codegen.OutputFile{
		Name:    "gradle.properties",
		Content: []byte(gradleProperties()),
	})

	// Gradle wrapper
	files = append(files, &codegen.OutputFile{
		Name:    "gradle/wrapper/gradle-wrapper.properties",
		Content: []byte(gradleWrapperProperties()),
	})
	files = append(files, &codegen.OutputFile{
		Name:    "gradlew",
		Content: []byte(gradlewScript()),
	})

	return files
}

func mainActivityKt(cfg Config) string {
	return fmt.Sprintf(`package %s

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import %s.ui.theme.AppTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            AppTheme {
                MainScreen()
            }
        }
    }
}
`, cfg.Package, cfg.Package)
}

func themeKt(cfg Config) string {
	return fmt.Sprintf(`package %s.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext

private val DefaultColorScheme = lightColorScheme()

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
`, cfg.Package)
}

func androidManifest(cfg Config) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application
        android:allowBackup="true"
        android:label="%s"
        android:supportsRtl="true"
        android:theme="@android:style/Theme.Material.Light.NoActionBar">
        <activity
            android:name=".MainActivity"
            android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
    </application>
</manifest>
`, appNameFromPkg(cfg.Package))
}

func appBuildGradle(cfg Config) string {
	return fmt.Sprintf(`plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "%s"
    compileSdk = 35

    defaultConfig {
        applicationId = "%s"
        minSdk = 26
        targetSdk = 35
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
    }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2024.12.01"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("io.coil-kt:coil-compose:2.7.0")
    debugImplementation("androidx.compose.ui:ui-tooling")
}
`, cfg.Package, cfg.Package)
}

func rootBuildGradle() string {
	return `plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.1.0" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.1.0" apply false
}
`
}

func settingsGradle(cfg Config) string {
	return fmt.Sprintf(`pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "%s"
include(":app")
`, appNameFromPkg(cfg.Package))
}

func gradleProperties() string {
	return `android.useAndroidX=true
kotlin.code.style=official
`
}

func gradleWrapperProperties() string {
	return `distributionBase=GRADLE_USER_HOME
distributionPath=wrapper/dists
distributionUrl=https\://services.gradle.org/distributions/gradle-8.11.1-bin.zip
zipStoreBase=GRADLE_USER_HOME
zipStorePath=wrapper/dists
`
}

func gradlewScript() string {
	return `#!/bin/sh
# Lightweight Gradle bootstrap — downloads and caches a Gradle distribution.
set -e

GRADLE_VERSION="8.11.1"
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
}

func appNameFromPkg(pkg string) string {
	parts := strings.Split(pkg, ".")
	if len(parts) > 0 {
		return exportName(parts[len(parts)-1])
	}
	return "App"
}
