#!/bin/bash
set -e

APP_NAME="Chrisper"
APP_DIR="$APP_NAME.app"
CONTENTS_DIR="$APP_DIR/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"
RESOURCES_DIR="$CONTENTS_DIR/Resources"

echo "Building $APP_NAME..."

# 1. Build Binary
# Get Project ID from env
PROJECT_ID=${GOOGLE_CLOUD_PROJECT}
if [ -z "$PROJECT_ID" ]; then
  echo "Warning: GOOGLE_CLOUD_PROJECT is not set. App will require manual config."
else
  echo "Embedding Project ID: $PROJECT_ID"
fi

CGO_ENABLED=1 go build -ldflags "-X main.embeddedProjectID=$PROJECT_ID" -o "$APP_NAME" main.go icon.go

# 2. Create App Structure
rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR"
mkdir -p "$RESOURCES_DIR"

# 3. Copy Files
cp "$APP_NAME" "$MACOS_DIR/"
cp Info.plist "$CONTENTS_DIR/"

# 3b. Process Icon
ICON_SOURCE="app_icon.png"

if [ -f "$ICON_SOURCE" ]; then
    echo "Creating AppIcon.icns..."
    
    # Ensure input is a valid PNG for sips
    cp "$ICON_SOURCE" temp_icon.png
    sips -s format png temp_icon.png --out temp_icon.png
    
    mkdir -p Chrisper.iconset
    sips -s format png -z 16 16     temp_icon.png --out Chrisper.iconset/icon_16x16.png
    sips -s format png -z 32 32     temp_icon.png --out Chrisper.iconset/icon_16x16@2x.png
    sips -s format png -z 32 32     temp_icon.png --out Chrisper.iconset/icon_32x32.png
    sips -s format png -z 64 64     temp_icon.png --out Chrisper.iconset/icon_32x32@2x.png
    sips -s format png -z 128 128   temp_icon.png --out Chrisper.iconset/icon_128x128.png
    sips -s format png -z 256 256   temp_icon.png --out Chrisper.iconset/icon_128x128@2x.png
    sips -s format png -z 256 256   temp_icon.png --out Chrisper.iconset/icon_256x256.png
    sips -s format png -z 512 512   temp_icon.png --out Chrisper.iconset/icon_256x256@2x.png
    sips -s format png -z 512 512   temp_icon.png --out Chrisper.iconset/icon_512x512.png
    sips -s format png -z 1024 1024 temp_icon.png --out Chrisper.iconset/icon_512x512@2x.png
    
    iconutil -c icns Chrisper.iconset -o AppIcon.icns
    cp AppIcon.icns "$RESOURCES_DIR/"
    rm -rf Chrisper.iconset AppIcon.icns temp_icon.png
else
    echo "Warning: Icon file not found at $ICON_SOURCE"
fi

# 4. Create PkgInfo
echo "APPL????" > "$CONTENTS_DIR/PkgInfo"

# 5. Cleanup
rm "$APP_NAME"

echo "Successfully created $APP_DIR"
echo "To install: mv $APP_DIR /Applications/"

