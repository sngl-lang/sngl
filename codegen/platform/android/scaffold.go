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
    val colorScheme = try {
        dynamicLightColorScheme(LocalContext.current)
    } catch (_: Exception) {
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
        android:theme="@style/Theme.Material3.DayNight.NoActionBar">
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
dependencyResolution {
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

func appNameFromPkg(pkg string) string {
	parts := strings.Split(pkg, ".")
	if len(parts) > 0 {
		return exportName(parts[len(parts)-1])
	}
	return "App"
}
