# GraalVM builds of the Android toolchain

Work item: https://git.duckfam.us/jonathan/sngl/-/work_items/296

**Goal:** on a machine with nothing installed (no JDK, no Android SDK, no
Gradle), `sngl run --platform android` builds a simple app and deploys it.
`sngl test --platform android` may keep needing a JVM for Robolectric, so
the test-side steps below are secondary. The JVM tools are built as GraalVM
native images and published from `github.com/sngl-lang/android-tools`, and
the gradle-free build (`directBuild`) downloads them from there.

## Can it be done?

Yes. Every step of `directBuild` either has a native build or can be done in
Go. Two things stand in the way today:

- `sngl run` goes through Gradle by default (`Config.UseGradle` returns true
  when the option is unset).
- Deploying needs a device. A phone attached over USB or Wi-Fi needs only
  `adb`. With no phone, the emulator binary, a system image and an AVD are
  all fetchable from Google's SDK repository, the same way `fetchSDKPackage`
  fetches build-tools now. That works, but it is several hundred MB and needs
  KVM/HVF, so it comes last.

| Tool          | Today                                                   | Plan                                                                  | Confidence                         |
|---------------|---------------------------------------------------------|-----------------------------------------------------------------------|------------------------------------|
| `kotlinc`     | JetBrains zip, runs on a JDK                            | native-image, with the compose and serialization plugins built in     | Works: spike done, 3–6x faster     |
| `d8`          | build-tools wrapper, runs on a JDK                      | native-image of `com.android.tools:r8`                                | High: pure Java, little reflection |
| `apksigner`   | build-tools jar, runs on a JDK; `jarsigner` as fallback | Go, inside sngl (`apksig` v2/v3 scheme)                               | High                               |
| `keytool`     | system JDK                                              | Go, inside sngl (PKCS#12 debug keystore)                              | High                               |
| `zipalign`    | build-tools, native                                     | Go, inside sngl                                                       | High                               |
| `aapt2`       | build-tools, native                                     | unchanged, or from our release (see licensing)                        | already native                     |
| `adb`         | `ANDROID_HOME` platform-tools                           | build from AOSP source and publish in our release                     | High, but licensing applies        |
| `android.jar` | Google SDK repo                                         | unchanged: data, no JVM                                               | n/a                                |
| Robolectric   | Gradle `:app:testDebugUnitTest`                         | still needs a JVM; drop Gradle and run it with `java` directly        | n/a                                |
| Gradle path   | Gradle + AGP + daemon                                   | `android.aapt2FromMavenOverride` at most; the rest runs in the daemon | low value                          |

Notes on the table:

- **Some JVM tools become Go instead of native images.** Signing,
  keystore generation and alignment are a few hundred lines of Go each.
  Written in Go they need no download and have no version matrix, and that
  removes `keytool` and `jarsigner`, which only a full JDK ships.
- **kotlinc works as a native image** (the spike, step 1 below). It is
  GraalVM CE 21 with the plugins compiled in. Every risk resolved with a
  flag rather than a launcher; see `kotlinc/build.sh` in `android-tools`:
  - `-Xplugin`: kotlinc reads each jar's `META-INF/services` itself and calls
    `Class.forName`, which resolves classes inside the image. The shipped
    plugin "jars" are those service files only, 1.5 KB each.
  - `-no-jdk` against `android.jar` works, so `jrt:/` is not needed.
  - `-kotlin-home` and `-Xintellij-plugin-root` (8 KB of
    `META-INF/extensions`) replace kotlinc finding its own jar.
    `-Xdisable-default-scripting-plugin` skips the scripting plugin, which
    is not in the image.
  - The image needs `-H:+AddAllCharsets` and an agent trace (69 KB of
    reflection config, checked in per Kotlin version).
  - Results: on 8 cores, hello takes **0.84s vs 5.1s** on the JVM, and
    pokedex takes **9.7s vs 25s** (9.3s vs 1m44s of CPU). The class files
    are byte-identical. The binary is 154 MB (53 MB gzipped), and building it
    takes about 4 minutes with a 6.3 GB peak.
  - The app still needs `kotlin-stdlib.jar` on its classpath. sngl takes
    that from the kotlinc distribution today; the native path has to fetch
    it from Maven Central (or the release has to carry it).
- **Gradle gains little.** Kotlin and d8 run inside the Gradle daemon, so a
  native binary does not replace them. `aapt2` is already native. The win
  for fixtures comes from moving them off Gradle, not from feeding Gradle
  native tools.

### Licensing: decide before publishing

The Android SDK License Agreement forbids redistributing the SDK, which
covers Google's prebuilt `adb`, `aapt2` and build-tools binaries. What we may
publish:

- `r8`/`d8`: BSD-3, from Google Maven. Fine.
- `kotlinc` and its plugins: Apache-2.0. Fine.
- `apksig` (if a Java signer is ever needed): Apache-2.0, from Google Maven.
  Fine.
- `adb`: Apache-2.0 **if built from AOSP source**, as
  `nmeum/android-tools` and the distro packages do. Do not mirror the
  platform-tools zip. Fetching it from Google at run time is allowed:
  `fetchSDKPackage("platform-tools", …)` already does this for build-tools,
  so the user gets it without installing anything. That route comes first.
  Building from source only buys "everything from one place".
- `aapt2`: either keep fetching it from Google's SDK repo or Maven (what
  `directBuild` does now), or build it from AOSP source with `adb`. The
  simplest first step is to keep fetching it from Google.
- GraalVM Community (GPLv2 + Classpath Exception) puts no license on the
  images we build. Oracle GraalVM under GFTC allows redistribution too.

## Repository: `sngl-lang/android-tools`

```
android-tools/
  kotlinc/      launcher + reflect/resource config per Kotlin version
  d8/           reflect config, version pin for com.android.tools:r8
  adb/          AOSP source build (CMake, after nmeum/android-tools)
  jre/          jlink'd fallback runtime (only if kotlinc native fails)
  versions.toml one row per published tool version
  .github/workflows/release.yml
```

- **Build on GitHub Actions, not GitLab CI.** native-image cannot
  cross-compile, so each OS/arch needs its own runner. GitHub gives free
  `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-14` (arm64), `macos-13` (amd64)
  and `windows-2022` runners to a public org repo, and the releases live
  there too. A GitLab pipeline on git.duckfam.us would need a self-hosted
  runner per platform. Revisit this if the work item's GitLab preference is
  firm.
- **Targets:** `linux-amd64`, `linux-arm64`, `darwin-arm64`,
  `darwin-amd64`, `windows-amd64`.
- **One release per tool version**, all in the same place. For example, tag
  `kotlinc-2.1.0+1` carries the compose and serialization plugins for
  2.1.0, tag `d8-8.7.18+1` carries d8, and tag `adb-35.0.2+1` carries adb.
  The `+N` is our rebuild counter.
- **Assets:** `<tool>-<version>-<os>-<arch>.tar.gz` (a `.zip` on Windows),
  plus `SHA256SUMS`. Sign it with cosign keyless or minisign, whichever is
  cheaper in CI.
- **Smoke test in CI before publishing:** compile and dex a tiny Compose app
  with the new binaries, on each runner, then run it through `aapt2 link`,
  then sign and verify with `apksigner verify` once, on a JVM job.

## Changes in sngl

1. **`androidtc.Combo` pins native tools.** Add fields for the
   `android-tools` release of kotlinc and d8, with a SHA-256 per
   `GOOS/GOARCH`, so bumping a combo stays a one-place change. A combo
   without a native build on this host falls back to the JVM tools.
2. **`resolveToolchain` prefers native.** Use the cached download under
   `~/.cache/sngl/android-toolchain/native/<tool>-<ver>/`, verified against
   its pin. `PATH`/`ANDROID_HOME` still win only when a flag or env says so
   (`SNGL_ANDROID_TOOLS=system`). The download reuses `fetchSDKPackage`'s
   stage-then-rename, so an interrupted download is never read as complete.
3. **`directBuild` asks for a JDK only when a JVM tool remains.** Today it
   calls `jdk.CompatibleHome` unconditionally.
4. **Sign, align and generate the keystore in Go.** Add a new
   `internal/apksign` package with v2+v3 signing, zipalign and a PKCS#12
   debug keystore. Check it against `apksigner verify` in a test that skips
   without a JDK.
5. **Run `adb` from our release** when there is no `ANDROID_HOME`, for
   `sngl run` and the device runner. The `emulator` stays on the SDK, since
   the user has to have an AVD anyway.
6. **Robolectric without Gradle.** Compile the test sources with native
   kotlinc, and resolve `robolectric`, `junit` and `ui-test-junit4` from
   Maven the same way `downloadComposeArtifacts` does. The android-all jar
   comes from Maven in offline mode
   (`robolectric.offline=true`, `robolectric.dependency.dir`). Then run
   `java -cp … org.junit.runner.JUnitCore` with the agent port property. A
   JVM is still needed, but the Gradle configuration, daemon start and
   `gradle.Lock()` serialization go away, and those are most of a fixture's
   time today.
7. **Device tests through an env var.** Setting
   `SNGL_ANDROID_DEVICE=<serial>` (or `ANDROID_SERIAL`) makes `LaunchTest`
   pick `launchDevice` for every fixture, without `testRunner=device` in
   the source. `launchDevice` uses `directBuild` instead of `gradlew :app:assembleDebug`, so a device run needs no JVM at all. Recorded runs
   stay recorded against Robolectric, and the device mode only reruns them.
8. **Make the gradle-free path the default** (`Config.UseGradle` returns
   true when unset today) once the fixtures pass on it. Gradle stays
   available through `gradle=true` for people who open the scaffold in
   Android Studio.
9. **Update the docs:** `design/architecture.md` (android), `design/testing.md`
   (runners, env var) and the CLAUDE.md test bullet on `ANDROID_HOME`.

## Order of work

Steps 1–6 reach the goal. The steps after that are for tests and are
optional.

1. **Spike kotlinc native-image** (decides the shape of everything else).
   Build kotlinc 2.1.0 with compose and serialization built in, run it
   `-no-jdk` against `android.jar`, and compile pokedex's generated sources.
   Compare the time against the JVM kotlinc, cold and warm. Record the result
   here.

   Done: see *kotlinc works as a native image* above. The JVM baseline
   for the whole gradle-free hello build is 22s. The SDK's build-tools
   35.0.0 d8 is older than Kotlin 2.1's metadata and prints a warning for
   every class; the r8 pinned in `android-tools` should be new enough to
   read it.
2. **Done: the Go signer, aligner and debug key** (sngl step 4).
   `internal/apksign` writes a v2 signature, which `apksigner verify` and
   `zipalign -c` both accept on a real hello build. The debug key is a PEM
   file instead of a keystore. A `minSdk` below 24 needs v1 too, so it is
   refused and the user is pointed at `gradle=true`. `adbInstall` uninstalls
   and retries an app installed with another key. kotlinc and d8 are now the
   only JVM tools left in `directBuild` (sngl step 3).
3. **Set up the `sngl-lang/android-tools` repo** with d8 first, the easy
   image, to prove the release pipeline, checksums and the sngl download
   path (sngl steps 1 and 2).
4. **Add kotlinc to the repo,** or the jlink JRE fallback if the spike
   failed.
5. **Fetch `adb` from Google's platform-tools** when there is no
   `ANDROID_HOME` (sngl step 5).
6. **Make `sngl run` build gradle-free by default** (sngl step 8, for run
   only). Then confirm the goal: in a clean container (`debian:stable`, no
   `java`, no `ANDROID_HOME`, no `~/.gradle`), with a phone attached over
   `adb connect`, `sngl run --platform android` on a hello-world deploys and
   launches. Add a CLI script test that takes the build half of this run in
   a clean environment and checks that no `java` was executed.
7. *(Optional)* **Provision an emulator with nothing installed:** fetch
   `emulator` and a system image, then create an AVD in the sngl cache.
8. *(Tests)* **Run Robolectric without Gradle** (sngl step 6). This still
   needs a JVM, which is acceptable. Measure the time for a full
   `go tool verify` before and after.
9. *(Tests)* **Add device tests through the env var** (sngl step 7).
10. *(Tests)* **Flip the default for tests too,** rerecord with
    `go tool verify -update`, and update the docs (sngl step 9).
11. *(Optional)* **Build `adb` and `aapt2` from AOSP source,** so every tool
    downloads from one place.

## Open questions

- GitHub Actions or GitLab CI for the release builds? This plan recommends
  GitHub because of the runners.
- `aapt2`: keep fetching it from Google, or build it from AOSP and ship it
  with `adb`?
- `gomobile bind` (`go+android`) still needs `javac` and the NDK. The goal
  covers `--lang kotlin` only. Should `go` on android keep requiring a JDK?
- Should the `next` combo get native tools too, or only `stable` at first?
