FROM golang:1.26-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium fonts-liberation fonts-noto-color-emoji \
    libgl-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev \
    unzip wget default-jdk-headless \
    && rm -rf /var/lib/apt/lists/*

# Android SDK for snapshot tests
ENV ANDROID_HOME=/opt/android-sdk
ENV PATH="${ANDROID_HOME}/cmdline-tools/latest/bin:${ANDROID_HOME}/platform-tools:${ANDROID_HOME}/emulator:${PATH}"
RUN mkdir -p ${ANDROID_HOME}/cmdline-tools \
    && wget -q https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip -O /tmp/cmdline-tools.zip \
    && unzip -q /tmp/cmdline-tools.zip -d ${ANDROID_HOME}/cmdline-tools \
    && mv ${ANDROID_HOME}/cmdline-tools/cmdline-tools ${ANDROID_HOME}/cmdline-tools/latest \
    && rm /tmp/cmdline-tools.zip
RUN yes | sdkmanager --licenses > /dev/null 2>&1 \
    && sdkmanager "platform-tools" "emulator" "platforms;android-35" "system-images;android-35;google_apis;x86_64" "build-tools;35.0.0"
RUN echo no | avdmanager create avd -n sngl-ci -k "system-images;android-35;google_apis;x86_64" --device "pixel_6"
ENV SNGL_AVD=sngl-ci

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
