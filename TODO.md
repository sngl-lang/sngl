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

| Tool          | Today                                                   | Plan                                                                  | Confidence                           |
|---------------|---------------------------------------------------------|-----------------------------------------------------------------------|--------------------------------------|
| `kotlinc`     | JetBrains zip, runs on a JDK                            | native-image, with the compose and serialization plugins built in     | Works: spike done, 3–6x faster       |
| `d8`          | build-tools wrapper, runs on a JDK                      | native-image of `com.android.tools:r8`                                | Works: 1.6x (app), 6x (cold pre-dex) |
| `apksigner`   | build-tools jar, runs on a JDK; `jarsigner` as fallback | Go, inside sngl (`apksig` v2/v3 scheme)                               | High                                 |
| `keytool`     | system JDK                                              | Go, inside sngl (PKCS#12 debug keystore)                              | High                                 |
| `zipalign`    | build-tools, native                                     | Go, inside sngl                                                       | High                                 |
| `aapt2`       | build-tools, native                                     | unchanged, or from our release (see licensing)                        | already native                       |
| `adb`         | `ANDROID_HOME` platform-tools                           | build from AOSP source and publish in our release                     | High, but licensing applies          |
| `android.jar` | Google SDK repo                                         | unchanged: data, no JVM                                               | n/a                                  |
| Robolectric   | Gradle `:app:testDebugUnitTest`                         | still needs a JVM; drop Gradle and run it with `java` directly        | n/a                                  |
| Gradle path   | Gradle + AGP + daemon                                   | `android.aapt2FromMavenOverride` at most; the rest runs in the daemon | low value                            |

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
   its pin. The JVM tools win only when `SNGL_ANDROID_TOOLS=jvm` says so.
   The download unpacks beside its directory and renames into place, so an
   interrupted download is never read as complete.
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
8. **`gradle` becomes `option[bool]`, and null means "choose".** It is
   already a `*bool` in `Config`, where nil means `true` today. `true` and
   `false` still force one path. Null resolves once, from the command and
   the config, and both the generate-time file layout and the build read
   that one answer:
   - `sngl generate` writes the Gradle project, which is something to open
     in Android Studio. Nothing is built.
   - `build`, `run` and both test runners build gradle-free. Until the
     gaps in step 9 are closed, anything that hits one of them uses Gradle.
9. **Close the gradle-free gaps:**
   - **`--lang go`:** `directBuild` never reads `golib.aar`. It has to put
     the AAR's `classes.jar` on the kotlinc classpath and into the dex, and
     its `jni/<abi>/*.so` into the APK under `lib/<abi>/`, stored and
     page-aligned (`apksign` already aligns `.so`). `gomobile bind` itself
     still runs `javac` and needs the NDK, so `go` stays JDK-bound. That is
     separate from Gradle.
   - **Robolectric:** sngl step 6. It still needs a JVM, but not Gradle.
     Robolectric also wants the processed resources: the `base.apk` that
     `aapt2 link` writes, named through `com/android/tools/test_config.properties`
     on the test classpath, which is what AGP writes for it.
   - **Device runner:** `launchDevice` builds with `gradlew :app:assembleDebug`.
     It only needs an APK, so it switches to `directBuild`.
10. **Fixtures run gradle-free by default.** One or two fixtures opt into
    `gradle=true`: one golden that builds through Gradle, and one recorded
    `sngl test` under Robolectric through Gradle, so the Gradle scaffold
    stays exercised. At least one CLI script runs a gradle-free `sngl build`
    on hello. No test covers `directBuild` today: no script or fixture sets
    `gradle=false`.
11. **Drop Coil.** The Gradle scaffold declares `io.coil-kt:coil-compose`,
    which no generated code uses, and `androidtc.Combo` pins its version.
    Removing it changes `build.gradle.kts`, which invalidates the recorded
    runs of 258 android goldens. Step 10 invalidates the same set, so both
    ship with one `go tool verify -update`.
12. **Update the docs:** `design/architecture.md` (android), `design/testing.md`
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
   file instead of a keystore. Below API 24 it adds a v1 signature too,
   down to Compose's floor of 21, so no `minSdk` needs Gradle. `adbInstall` uninstalls
   and retries an app installed with another key. kotlinc and d8 are now the
   only JVM tools left in `directBuild` (sngl step 3).
3. **Done: d8 and kotlinc are in `sngl-lang/android-tools`,** with a
   release workflow (linux amd64/arm64, darwin arm64/amd64, windows amd64)
   that smoke-tests a Compose + serialization source on each host and
   publishes a `v*` tag with `SHA256SUMS`. The tags are `v<N>`, one release
   for every tool, not one per tool version. d8 is r8 8.7.18, the version
   AGP 8.7.3 ships, and its output is byte-identical to the JVM's. On
   pokedex's classes it takes 3.8s vs 6.2s on the JVM. A cold pre-dex of the
   98 library jars takes 37.6s vs 3m45s.
4. **Done: sngl downloads them** (sngl steps 1 and 2).
   `androidtc.NativeRelease` names the release, `nativeSums` holds its
   `SHA256SUMS`, and a combo's `R8` field gives the d8 version. The tools
   unpack into `android-toolchain/native/v1/<tool>-<version>/`. The
   kotlin-stdlib comes from the kotlinc asset's `lib/`. `SNGL_ANDROID_TOOLS=jvm`
   keeps the JVM tools. `directBuild` asks for a JDK only when one of them is
   still a JVM tool. A gradle-free hello build ran with no `java` on `PATH`
   and no `JAVA_HOME`. The libraries still came from `~/.gradle` there, so
   the Maven fallback is only confirmed by step 6's clean container.
5. **Done: fetch `adb` from Google's platform-tools** when neither `PATH`
   nor `ANDROID_HOME` has one (sngl step 5). `sngl run` and screenshots
   call `fetchAdb`, which unpacks the latest `platform-tools` into
   `android-toolchain/platform-tools/`. `androidTool("adb")` looks there
   too, but never downloads, so a test runner without adb still skips.
   Google publishes no linux-arm64 adb, so that host still needs its own.
   With an empty `HOME`, no `java`, no `ANDROID_HOME` and no `~/.gradle`,
   a gradle-free hello build took 45s. It downloaded the platform,
   build-tools, both native tools and the Compose libraries from Maven.
   On a phone, pokedex then crashed at launch on `androidx.savedstate.R$id`.
   The libraries had been seeded from `~/.gradle`, which mixes versions,
   puts test libraries in the APK, and can leave a jar without the AAR whose
   `R.txt` makes its R class. The seed is gone: the build uses only the
   pinned Maven set, in `android-toolchain/maven-libs/`, and always keeps
   each AAR. Whether that APK launches is still to be checked on a device.
6. **Done, except on a device: `gradle` is `option[bool]` and null
   chooses** (sngl step 8). `Config.UseGradle` uses Gradle for a generated
   project (no `executable` option), a test (`test`) and a Go program
   (`golib/`). `sngl build` and `sngl run` are gradle-free otherwise. Option
   decoding now reads `null`, and a value wrapped into an option, into a
   `*bool`. The gradle-free layout now carries the i18n runtime and manifest
   and the program's assets (`assets/`, handed to `aapt2 link -A`), which it
   dropped before. `cmd/sngl/testdata/build_android_gradle_free.txt` builds
   hello with an empty `PATH` and no `JAVA_HOME`. Still to do: the
   clean-container `sngl run` onto a phone.
7. **Close the gradle-free gaps** (sngl step 9): Go's `golib.aar`,
   Robolectric without Gradle, and the device runner on `directBuild`.
   Measure the time for a full `go tool verify` before and after.
8. **Done: fixtures are gradle-free and Coil is gone** (sngl steps 10 and
   11), with one `go tool verify -update` (2h, almost all of it the `sngl test` scripts still on Gradle). A golden without tests now generates the
   gradle-free layout, and `internal/toolchain` verifies it with
   `android.buildApk` (about 3s each, serialized because the library caches
   are shared) instead of `gradlew`. A golden with tests stays on Gradle, so
   the Gradle test path is still covered. `generate_android_i18n.txtar`
   opts into `gradle=true`, so a Gradle build without tests is covered too.
9. *(Tests)* **Add device tests through the env var** (sngl step 7).
10. **Update the docs** (sngl step 12).
11. *(Optional)* **Provision an emulator with nothing installed:** fetch
    `emulator` and a system image, then create an AVD in the sngl cache.
12. *(Optional)* **Build `adb` and `aapt2` from AOSP source,** so every tool
    downloads from one place.

## Gradle cannot use the native tools

Asked: can a Gradle build prefer the GraalVM builds, ours or anyone's? No,
apart from `aapt2`, which is native already.

- **aapt2:** AGP takes `android.aapt2FromMavenOverride=<path>` in
  `gradle.properties`. That would only swap one native binary for another.
- **d8:** AGP runs D8 and R8 in-process, in a Gradle worker, from the R8 jar
  on its own classpath. There is no setting that makes it exec an external
  binary. A pinned `com.android.tools:r8` on the buildscript classpath still
  runs on the JVM.
- **kotlinc:** the Kotlin Gradle Plugin runs the compiler in the Kotlin
  daemon, in process, or out of process
  (`kotlin.compiler.execution.strategy`). All three load
  `kotlin-compiler-embeddable` into a JVM. None takes a compiler binary.
- No one publishes a native Gradle, and a Gradle build is JVM tasks from end
  to end.

So the speedup for fixtures comes from taking them off Gradle (steps 7–8),
not from feeding Gradle native tools.

## Open questions

- GitHub Actions or GitLab CI for the release builds? This plan recommends
  GitHub because of the runners.
- `aapt2`: keep fetching it from Google, or build it from AOSP and ship it
  with `adb`?
- `gomobile bind` (`go+android`) still needs `javac` and the NDK, even
  without Gradle. Should `go` on android keep requiring a JDK, or should we
  publish a native `javac` (or ecj)?
- Should the `next` combo get native tools too, or only `stable` at first?
