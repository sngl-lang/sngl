// Gradle build for the SNGL Kotlin testagent runtime.
//
// This module ships as a plain Kotlin/JVM library — it has no Android
// dependencies of its own. The android platform's codegen will link it
// into the generated test binary (Robolectric or device path).
//
// Run unit tests with:
//   gradle :test
//
// Note: this module is not yet wired into a multi-module gradle build;
// it stands alone and is intended to be either built independently or
// included as a project dependency by the generated android app.

plugins {
    kotlin("jvm") version "1.9.22"
    `java-library`
}

// Coordinates for gradle composite-build (includeBuild) consumers.
// The android device-test launcher synthesises a generated app project
// that depends on "us.duckfam.git.jonathan.sngl:testagent"; declaring
// these here lets composite-build substitute the dep against this module.
group = "us.duckfam.git.jonathan.sngl"
version = "0.1.0"

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

// No jvmToolchain — let the surrounding build (composite-build root or
// standalone gradle invocation) pick a JDK. Composite-build consumers
// (the android device-test launcher) run under the Android Studio JBR
// which is JDK 21; declaring a strict 17 toolchain there causes
// gradle to try (and fail) to download a matching JDK.
tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    kotlinOptions { jvmTarget = "17" }
}

repositories {
    mavenCentral()
}

dependencies {
    implementation("org.json:json:20240303")

    testImplementation(platform("org.junit:junit-bom:5.10.2"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.test {
    useJUnitPlatform()
}
