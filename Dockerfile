FROM golang:1.26-bookworm

# Everything a golden can be regenerated with.
#
# CI does not use this image to *check* the goldens. `go test -run TestGolden`
# compares committed digests and runs no compiler at all, so the ordinary
# pipeline needs nothing but Go. This is the image that runs `-update`, and the
# nightly pipeline that re-verifies what `-update` recorded.
#
# The rule it serves: a golden may not be regenerated without running the
# tooling. There is no skip-because-it-is-not-installed, so an image missing
# any of this cannot produce a record -- `-update` fails rather than quietly
# leaving a target unverified. That is why node, a JDK and the Android SDK are
# here and were not before: without them the `none/html` and `kotlin/android`
# records, two thirds of the tree, were unproducible.
#
# What each is for:
#
#   chromium      the html platform's browser tests, and its component agent
#                 mode -- the only place html's assertions actually execute,
#                 since an html record is only a `node --check`.
#   libgl/libx11  fyne's cgo.
#   libgtk-4-dev  gtk4's cgo, and gir1.2-gtk-4.0 is the introspection data the
#                 platform parses its widget vocabulary from; without the .gir
#                 it reports itself unavailable and refuses to generate.
#   cage          a headless compositor, so gtk4's window-presenting tests
#                 render without a desktop. Not a capability -- the builds run
#                 wherever a display exists -- but the only way in a container.
#   nodejs        compile-time evaluation of a pure `js:` function, and the
#                 syntax check every html record is.
#   openjdk-17    the Android toolchain launches on JDK 17-23
#                 (internal/androidtc); a default outside that window makes
#                 every android record unproducible.
RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium fonts-liberation fonts-noto-color-emoji \
        libgl-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev \
        libxi-dev libxxf86vm-dev \
        libgtk-4-dev gir1.2-gtk-4.0 \
        cage \
        nodejs \
        openjdk-17-jdk-headless unzip \
    && rm -rf /var/lib/apt/lists/*

# The Android SDK, pinned to what androidtc's `stable` combo names: compileSdk
# 35 and build-tools 35.0.0. Gradle itself is not installed -- each generated
# project ships a wrapper and downloads its own.
ENV ANDROID_HOME=/opt/android-sdk
ENV ANDROID_SDK_ROOT=/opt/android-sdk
RUN mkdir -p "$ANDROID_HOME/cmdline-tools" \
    && curl -fsSL -o /tmp/cmdline-tools.zip \
        https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip \
    && unzip -q /tmp/cmdline-tools.zip -d "$ANDROID_HOME/cmdline-tools" \
    && mv "$ANDROID_HOME/cmdline-tools/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest" \
    && rm /tmp/cmdline-tools.zip \
    && yes | "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" --licenses > /dev/null \
    && "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" \
        "platform-tools" "platforms;android-35" "build-tools;35.0.0" > /dev/null

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
