#!/usr/bin/env bash
# Runs the app on a connected device or emulator against a real hub started
# here, and collects the screenshots the tests take.
#
#   ./gradlew assembleDebug assembleDebugAndroidTest && ci/device-test.sh
#
# An emulator reaches this machine at 10.0.2.2; for a phone on the same
# network, set HUB_HOST to this machine's address.
set -euo pipefail
cd "$(dirname "$0")/.."

token=device-test-token
work=$(mktemp -d)
(cd ../hub && go build -o "$work/fetch" .)
"$work/fetch" -addr :8787 -data "$work/data" -token "$token" &
hub=$!
trap 'kill $hub; rm -rf "$work"' EXIT

adb install -r app/build/outputs/apk/debug/app-debug.apk
adb install -r app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk

# `am instrument` exits 0 even when tests fail, so the verdict is read from
# what it prints.
adb shell am instrument -w \
  -e hubUrl "http://${HUB_HOST:-10.0.2.2}:8787" -e hubToken "$token" \
  com.horsehound.fetch.test/androidx.test.runner.AndroidJUnitRunner | tee instrument.txt
status=0
grep -q '^OK (' instrument.txt || status=1

# The screenshots sit in the app's private files; a debug build lets adb in.
rm -rf screens
adb exec-out run-as com.horsehound.fetch tar c -C files screens | tar x || true
exit $status
