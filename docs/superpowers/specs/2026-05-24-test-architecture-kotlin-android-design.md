# Test architecture — Kotlin testagent + android transport

Date: 2026-05-24

## Goal

Bring android into the unified test architecture. Two runtimes:

1. **Robolectric (default)** — fast JVM simulation of Android. Catches
   composition, state-update, and most API-surface bugs. Doesn't catch
   GPU-rendering or device-specific quirks.
2. **Real device / emulator (opt-in)** — APK installed via adb, agent
   embedded in the running app. Maximum fidelity. Slower per cycle.

Both runtimes share the same kotlin testagent runtime under
`pkg/kotlin/testagent/`. Selection via a new android platform option
`testRunner` whose values are `robolectric` (default) and `device`.

## Non-goals

- Migrating away from Robolectric entirely. It's the right default for
  fast iteration; device mode is for fidelity coverage.
- Pixel-perfect parity between Robolectric and device snapshots.
  Goldens are scoped per-testRunner; the two trees do not need to
  match.
- Full Paparazzi integration as a third backend. Robolectric 4.10+'s
  native-graphics shim is sufficient for now; revisit if a fixture
  surfaces a gap.

## High-level architecture

```
sngl test --platform=android [--opt testRunner=device]
                    │
                    ▼
       android platform's TestLauncher
                    │
        ┌───────────┴───────────┐
        │                       │
testRunner=robolectric    testRunner=device
        │                       │
        ▼                       ▼
gradle build kotlin JAR    gradle assembleDebug → APK
java -jar ...             adb install + adb forward
stdio JSON-RPC            tcp:<host>:<dev>
                          am start → agent connects back
                          TCP JSON-RPC over forwarded port
```

Both transports speak the same JSON-RPC protocol established in Plan
1. The kotlin testagent's `main()` selects between `stdio` and `tcp`
transports based on an env var the launcher sets.

## testRunner option

Add to `lib/options.sngl`-equivalent for the android platform (the
existing `Options` struct in `codegen/platform/android/android.sngl`):

```sngl
struct Options {
    ...existing fields...
    // Which Android runtime to test against.
    //   robolectric: JVM simulation, fast, no emulator.
    //   device:      APK on a real emulator/device via adb.
    testRunner string = "robolectric"
}
```

Surface to the CLI as `--opt testRunner=device`, or set in source via
`output { kotlin { android(testRunner="device") } }`.

## Dual-mode emission (mirrors Plan 1's Go pattern)

| Trigger | Mode | Emits |
|---|---|---|
| `sngl generate --opt test=true` (no testMode) | **Native** | JUnit test classes in the user's gradle test cycle. testRunner=robolectric → `app/src/test/kotlin/...` (JVM unit tests). testRunner=device → `app/src/androidTest/kotlin/...` (instrumented tests). User's `./gradlew test` / `./gradlew connectedAndroidTest` runs them. |
| `sngl test --platform=android` | **Agent** (testMode=agent) | Our own binary with linked testagent + RegisterTest calls. Gradle just builds; sngl test launches the binary directly. testRunner=robolectric → JVM `main` linking Robolectric + compose-test-rule. testRunner=device → APK with the testagent as a startup hook in `MainActivity`. |

Direction matches Plan 1's bubbletea/fyne/gtk4 cutovers: dual-mode
emission so user-project test files (`--opt test=true`) are
interoperable with the user's existing gradle setup, while `sngl test`
produces a fully-controlled binary.

## Kotlin testagent runtime

Lives at `pkg/kotlin/testagent/`. Estimated ~300 LOC across:

- `TestAgent.kt` — entry point. Selects transport (stdio if no env var, TCP if `SNGL_AGENT_PORT` set). Drives the RPC loop.
- `T.kt` — per-test handle with the intrinsic surface (`log`, `fail`, `failNow`, `skip`, `error`, `fatal`, `assertTrue`, `wait`, `waitFor`, `snapshot`, `test` for subtests, `setContext`).
- `Registry.kt` — name → fn map; codegen-emitted `init` calls populate it.
- `Rpc.kt` — JSON-RPC 2.0 codec; line-delimited JSON. Mirrors `internal/testrpc` shape.
- `Snapshot.kt` — pluggable `SnapshotCapture` interface; per-testRunner implementations register one of two implementations during init (Compose-test rule for robolectric; live View capture for device).

API surface (intrinsic-level) matches `pkg/go/testagent`. Method names
adapt to Kotlin idioms (`assertTrue` not `Assert`; lowercase camelCase
throughout). The native-mode emission maps SNGL composites to JUnit
idioms (`t.error` → `Assert.fail()` after `println`; `t.assertTrue` →
`assertTrue(...)`).

## TestLauncher implementation per testRunner

`androidPlatform.LaunchTest(ctx, dir, lang, opts)` reads `testRunner`
and dispatches:

### testRunner=robolectric (default)

1. Synthesise a gradle project under `dir`. Layout: `build.gradle.kts`,
   `settings.gradle.kts`, `app/build.gradle.kts`, plus the emitted
   testagent main + user code under `app/src/main/kotlin/...`.
2. App's `build.gradle.kts` declares the kotlin JVM plugin plus
   Robolectric, compose-test-rule, and the kotlin testagent runtime
   as dependencies. The `mainClass` is the codegen-emitted entry
   point (`us.duckfam.sngl.app.TestAgentMainKt`).
3. `./gradlew :app:installDist` → produces a runnable JVM script in
   `app/build/install/app/bin/app`.
4. `exec` the script. stdio JSON-RPC — sngl test owns the spawn,
   owns the streams.
5. Cleanup: process termination; gradle daemon left running (warm
   cache).

### testRunner=device

1. Synthesise a gradle Android project (the existing scaffold logic).
2. `./gradlew :app:assembleDebug` → APK at
   `app/build/outputs/apk/debug/app-debug.apk`.
3. **Ensure an adb device is available** — reuse `ensureAdbDevice`
   (today in `codegen/platform/android/run.go`). The helper checks
   `adb devices`; if none attached, picks an AVD (honouring `SNGL_AVD`
   env), launches `emulator -avd <avd> -no-snapshot-load`, polls
   adb until boot completes (120s timeout). This is the same path
   `sngl run --platform=android` already exercises in CI.
4. `adb install -r <apk>`. Verify install success.
5. Pick a free localhost port; `adb forward tcp:<host> tcp:<device>`.
6. `adb shell am start -n <pkg>/.MainActivity --es SNGL_AGENT_PORT <port>`.
7. Connect to `localhost:<host-port>` (retry loop with timeout
   ~5s; agent may take a moment to open its socket after launch).
8. Return the connected `net.Conn` as `codegen.RPCChannel`.
9. Cleanup: `adb shell am force-stop <pkg>`, `adb uninstall <pkg>`,
   `adb forward --remove tcp:<host>`. The emulator started in step 3
   is **left running** for subsequent test invocations — matches
   today's `sngl run` behaviour and amortises emulator-boot cost
   across multiple fixtures in one `sngl test` invocation.

The probe (Plan 2 Task 6's `codegen.SkipError`) reports a clean skip
when:

- JDK 17+ not on PATH (both runtimes).
- testRunner=device + Android SDK unavailable: `ANDROID_HOME` /
  `ANDROID_SDK_ROOT` unset, `adb` not on PATH, `emulator` not on
  PATH, or no AVDs configured (and `SNGL_AVD` not set). The probe
  matches `ensureAdbDevice`'s preconditions — if those don't hold,
  the device path can't bring up an emulator and the test must skip.

## Snapshot capture

Per-testRunner registration of `SnapshotCapture`:

### Robolectric

```kotlin
class RobolectricSnapshotCapture(
    private val composeRule: ComposeTestRule,
) : SnapshotCapture {
    override fun capture(): Pair<String, ByteArray> {
        val img = composeRule.onRoot().captureToImage()
        val bmp = img.asAndroidBitmap()
        val out = ByteArrayOutputStream()
        bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
        return "image/png" to out.toByteArray()
    }
}
```

Requires Robolectric 4.10+ with native graphics enabled
(`@GraphicsMode(GraphicsMode.Mode.NATIVE)` annotation on the test
class, or equivalent gradle config).

### Device

```kotlin
class DeviceSnapshotCapture(
    private val rootView: View,
) : SnapshotCapture {
    override fun capture(): Pair<String, ByteArray> {
        val bmp = Bitmap.createBitmap(rootView.width, rootView.height, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bmp)
        rootView.draw(canvas)
        val out = ByteArrayOutputStream()
        bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
        return "image/png" to out.toByteArray()
    }
}
```

For modern Android (hardware-accelerated views), prefer `PixelCopy`
for fidelity, falling back to `View.draw(Canvas)` when PixelCopy fails
(typically only on older or non-attached views).

## Per-testRunner goldens

The kotlin testagent prefixes the snapshot name with `<testRunner>/`
before submitting `snapshotAssert`. Names become:

- `robolectric/initial`
- `device/initial`

`codegen/testharness/snapshot.Store` treats them as separate names →
separate golden files:

```
<fixture-dir>/<fixture-base>.snapshots/
    robolectric/initial.png
    device/initial.png
```

Zero changes to Store machinery. The same fixture run with
testRunner=robolectric vs testRunner=device accumulates two goldens.
Mismatch failures are scoped to the testRunner being run.

## Transport per testRunner

- **stdio JSON-RPC** for robolectric. Matches bubbletea/fyne/gtk4 in
  Plan 1/2 — sngl test owns the spawn, owns the streams.
- **TCP JSON-RPC** for device. Adb-forwarded loopback port. Required
  because stdin/stdout don't reach an APK.

Two entry points into the testagent runtime:

- **`fun main()`** — used by testRunner=robolectric. Selects stdio
  transport (no env var) or TCP (when `SNGL_AGENT_PORT` set, mostly
  for local debugging).
- **`fun startTcp(port: Int)`** — used by testRunner=device. Called
  from the codegen-emitted `MainActivity.onCreate` before the user UI
  is installed. Opens a localhost TCP listener on `port`, accepts the
  driver's connection, runs the same driver loop.

```kotlin
fun main() {
    val rpc = System.getenv("SNGL_AGENT_PORT")?.toIntOrNull()
        ?.let { RpcClient.tcp(it) }
        ?: RpcClient.stdio()
    runDriver(rpc)
}

fun startTcp(port: Int) {
    runDriver(RpcClient.acceptTcp(port))
}
```

## Sequencing

1. **Kotlin testagent runtime** (`pkg/kotlin/testagent/`) — full
   intrinsic surface, JSON-RPC codec, both transports, snapshot
   plumbing. No platform integration yet. Unit-test in isolation
   (gradle test against the testagent jar).
2. **testRunner option** + native-mode emission switch:
   `--opt test=true testRunner=robolectric` → `app/src/test/kotlin/...`;
   testRunner=device → `app/src/androidTest/kotlin/...`. JUnit class
   bodies use intrinsics that map cleanly to JUnit assertions.
3. **Android Robolectric agent-mode cutover.** Delete
   `codegen/platform/android/runtests.go`. android platform's
   `Generate` emits the agent-mode binary (kotlin main + testagent +
   RegisterTest calls + Compose-test-rule snapshot capture) when
   `Options.test && testMode=agent && testRunner=robolectric`.
   `TestLauncher` builds + runs the JVM binary, talks stdio.
4. **Android device agent-mode.** Same `Generate` path with
   testRunner=device emits an APK whose `MainActivity.onCreate` calls
   `TestAgent.startTcp(intent.getIntExtra("SNGL_AGENT_PORT", 0))`
   before any user UI. `TestLauncher` builds APK, adb-installs, adb
   forwards, launches, connects. Snapshot capture uses the live View.
5. **Probe/skip wiring** via `codegen.SkipError` for: JDK 17+ missing,
   adb missing, no device attached.
6. **Fixtures**:
   `cmd/sngl/testdata/test_android_robolectric_snapshot.txt`,
   `cmd/sngl/testdata/test_android_device_snapshot.txt`, and a
   state-only fixture `test_android_state.txt` that runs against both
   testRunner values via a script-level matrix.

## Test plan

- `pkg/kotlin/testagent/` — unit tests covering RPC codec, registry,
  per-test isolation, abort/skip control flow.
- `cmd/sngl/testdata/test_android_robolectric_state.txt` — minimal
  state-assertion fixture. Skips when JDK 17+ missing. Asserts PASS
  + a couple of test names.
- `cmd/sngl/testdata/test_android_robolectric_snapshot.txt` — snapshot
  fixture; goldens under `<fixture>.snapshots/robolectric/`. First run
  with `SNGL_UPDATE_SNAPSHOTS=1` creates; second run diffs.
- `cmd/sngl/testdata/test_android_device_snapshot.txt` — same shape;
  testRunner=device. Skips when no `adb devices` returns nothing.
  Goldens under `<fixture>.snapshots/device/`.
- `cmd/sngl/testdata/test_android_state.txt` — runs same fixture
  against both testRunners (two invocations of `sngl test`), asserts
  both pass.

## Out of scope (tracked elsewhere)

- Plan 4: JS testagent + html (WebSocket transport).
- Plan 5: Fixture backfill for 22 untested stdlib components.
- iOS support — Plan 4+ or separate.
- Paparazzi integration as a third snapshot backend.
- Cross-runtime golden tooling (e.g. "promote robolectric golden to
  device golden").

## Risks

- **Robolectric native-graphics rendering quirks.** Some Compose
  features (notably scrollable content, gestures, accessibility) may
  render differently or not at all under Robolectric. Document the
  limitation; fixtures that depend on those features should use
  testRunner=device.
- **adb device acquisition in CI.** Reused from `sngl run`'s existing
  `ensureAdbDevice` helper, which auto-launches an AVD when no device
  is attached (configurable via `SNGL_AVD`). CI needs: ANDROID_HOME +
  one AVD created via `avdmanager create avd`. Beyond that, no
  per-fixture emulator setup. Skip-cleanly when `ANDROID_HOME` is
  unset or no AVDs are configured.
- **Gradle daemon and warm cache.** First test run downloads gradle
  + kotlin + Robolectric deps (~500MB, several minutes). Subsequent
  runs reuse `~/.gradle/caches/`. CI should bake the warm cache or
  accept the cold-start cost.
- **adb forward port collisions.** If `sngl test` is run concurrently
  against multiple android fixtures, port allocation must be
  thread-safe. Use a free-port helper that grabs+holds a localhost
  TCP listener until adb forward picks up.
- **Kotlin testagent dependency footprint.** Kotlin's standard
  library + JSON serialization (kotlinx.serialization) + Robolectric
  add ~50MB of compiled classes. Acceptable for `sngl test` but
  user-facing `--opt test=true` emission should only pull in
  testagent and not Robolectric (Robolectric is opt-in via the
  user's own gradle test dependencies).
