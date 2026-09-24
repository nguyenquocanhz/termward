#!/bin/sh
# Builds a device .ipa without a developer certificate (macOS + Xcode), ad-hoc
# signed. Sideloading tools such as SideStore / AltStore re-sign it with the
# user's own (free) Apple ID.
#
#   node scripts/mobile.mjs core ios && node scripts/mobile.mjs web
#   sh scripts/ios-ipa.sh            -> ios/App/build/Termward-<version>.ipa
set -eu
cd "$(dirname "$0")/.."
VERSION=$(node -p "require('./package.json').version")
BUILD=$(node -p "const [a,b,c]=require('./package.json').version.split('.').map(n=>parseInt(n,10));a*10000+b*100+c")
OUT=ios/App/build

xcodebuild -project ios/App/App.xcodeproj -scheme App -configuration Release \
  -sdk iphoneos -destination 'generic/platform=iOS' -derivedDataPath "$OUT" \
  MARKETING_VERSION="$VERSION" CURRENT_PROJECT_VERSION="$BUILD" \
  CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY="" build

rm -rf "$OUT/Payload"
mkdir -p "$OUT/Payload"
cp -R "$OUT/Build/Products/Release-iphoneos/App.app" "$OUT/Payload/"

# Ad-hoc ("fake") sign every binary, frameworks first. SideStore replaces the
# signature anyway, but its signer inserts LC_CODE_SIGNATURE without checking
# header padding when a binary has none, so give it one to replace.
for fw in "$OUT"/Payload/App.app/Frameworks/*.framework; do
  codesign --force --sign - --timestamp=none "$fw"
done
codesign --force --sign - --timestamp=none "$OUT/Payload/App.app"
codesign --verify --verbose=1 "$OUT/Payload/App.app"

(cd "$OUT" && rm -f "Termward-$VERSION.ipa" && zip -qry "Termward-$VERSION.ipa" Payload)
echo "built $OUT/Termward-$VERSION.ipa"
shasum -a 256 "$OUT/Termward-$VERSION.ipa"
