#!/usr/bin/env bash
# Direct APK build without Gradle — uses Android build-tools directly.
# Useful on systems where the Gradle distribution cannot be downloaded.
set -euo pipefail

SDK="${ANDROID_HOME:-/opt/android-sdk}"
BT="$SDK/build-tools/34.0.0"
JAR="$SDK/platforms/android-34/android.jar"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/android/app/src/main"
OUT="$ROOT/dist"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$OUT" "$WORK/compiled" "$WORK/classes" "$WORK/dex" "$WORK/gen"

echo "[1/6] Compile resources with aapt2..."
"$BT/aapt2" compile --dir "$SRC/res" -o "$WORK/compiled/res.zip"

echo "[2/6] Link resources + manifest (generate R.java)..."
"$BT/aapt2" link \
  -I "$JAR" \
  --manifest "$SRC/AndroidManifest.xml" \
  --java "$WORK/gen" \
  --min-sdk-version 26 \
  --target-sdk-version 34 \
  --version-code 1 \
  --version-name 1.0.0 \
  -o "$WORK/base.apk" \
  "$WORK/compiled/res.zip"

echo "[3/6] Compile Java sources..."
find "$SRC/java" "$WORK/gen" -name '*.java' > "$WORK/sources.txt"
# Put android.jar on the classpath (not bootclasspath) so JDK 20 supplies
# java.lang.invoke.LambdaMetafactory for lambdas; d8 desugars them for min-api 26.
javac -source 8 -target 8 -classpath "$JAR" \
  -d "$WORK/classes" @"$WORK/sources.txt" 2>&1 | grep -vE "warning:|bootstrap class path|deprecat" || true
CLASS_COUNT=$(find "$WORK/classes" -name '*.class' | wc -l)
if [ "$CLASS_COUNT" -lt 5 ]; then
  echo "ERROR: Java compilation produced only $CLASS_COUNT classes"; exit 1
fi
echo "  compiled $CLASS_COUNT classes"

echo "[4/6] Dex with d8..."
find "$WORK/classes" -name '*.class' > "$WORK/classlist.txt"
"$BT/d8" --lib "$JAR" --min-api 26 --output "$WORK/dex" @"$WORK/classlist.txt"

echo "[5/6] Add classes.dex to APK..."
cd "$WORK"
cp base.apk unsigned.apk
zip -q -j unsigned.apk dex/classes.dex

echo "[6/6] Align and sign..."
"$BT/zipalign" -f -p 4 unsigned.apk aligned.apk
if [ ! -f "$ROOT/android/debug.keystore" ]; then
  keytool -genkeypair -v -keystore "$ROOT/android/debug.keystore" \
    -alias androiddebugkey -storepass android -kepass android \
    -dname "CN=Android Debug,O=Android,C=US" -keyalg RSA -keysize 2048 \
    -validity 10000 >/dev/null 2>&1 || \
  keytool -genkeypair -v -keystore "$ROOT/android/debug.keystore" \
    -alias androiddebugkey -storepass android -keypass android \
    -dname "CN=Android Debug,O=Android,C=US" -keyalg RSA -keysize 2048 \
    -validity 10000 >/dev/null 2>&1
fi
"$BT/apksigner" sign --ks "$ROOT/android/debug.keystore" \
  --ks-key-alias androiddebugkey --ks-pass pass:android --key-pass pass:android \
  --out "$OUT/MoreLink-Android.apk" aligned.apk

echo ""
echo "APK built: $OUT/MoreLink-Android.apk"
ls -lh "$OUT/MoreLink-Android.apk"
"$BT/apksigner" verify --print-certs "$OUT/MoreLink-Android.apk" | head -6
