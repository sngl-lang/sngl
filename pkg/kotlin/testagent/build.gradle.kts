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

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    jvmToolchain(17)
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
