#!/bin/bash
set -euo pipefail

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$DIR"

BIN="ubuntu-gpu-gui"
NEED_BUILD=0

ICON_SVG="frontend/dist/icon.svg"
ICON_PNG="appicon.png"
ICON_NAME="gpu-manager"

desktop_exec_quote() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//\$/\\$}
  value=${value//\`/\\\`}
  printf '"%s"' "$value"
}

validate_desktop_entry() {
  local desktop_file=$1
  local exec_path=$2

  if grep -Eq '\$\{[^}]+\}' "$desktop_file"; then
    echo "Desktop entry contains unresolved placeholders: $desktop_file" >&2
    return 1
  fi
  if [ ! -x "$exec_path" ]; then
    echo "Desktop Exec target is not executable: $exec_path" >&2
    return 1
  fi
  if command -v desktop-file-validate >/dev/null 2>&1; then
    desktop-file-validate "$desktop_file"
  fi
}

# Keep the embedded app icon up-to-date if ImageMagick is available.
if [ -f "$ICON_SVG" ]; then
  if [ ! -f "$ICON_PNG" ] || [ "$ICON_SVG" -nt "$ICON_PNG" ]; then
    if command -v convert >/dev/null 2>&1; then
      echo "Generating $ICON_PNG from $ICON_SVG..."
      convert -background none "$ICON_SVG" "$ICON_PNG"
    fi
  fi
fi

if [ ! -f "$BIN" ]; then
  NEED_BUILD=1
elif find . -name '*.go' -newer "$BIN" -print -quit | grep -q .; then
  NEED_BUILD=1
elif find frontend -type f -newer "$BIN" -print -quit | grep -q .; then
  NEED_BUILD=1
elif [ -f "$ICON_PNG" ] && [ "$ICON_PNG" -nt "$BIN" ]; then
  NEED_BUILD=1
fi

if [ "$NEED_BUILD" -eq 1 ]; then
  echo "Building $BIN..."
  go build -tags "desktop,production,webkit2_41" -o "$BIN" .
fi

BIN_PATH="$DIR/$BIN"

# Best-effort: launch via a desktop entry so GNOME/Ubuntu picks up the custom icon.
DESKTOP_ID="$ICON_NAME"
DATA_HOME="${XDG_DATA_HOME:-${HOME:?HOME is not set}/.local/share}"
DESKTOP_DIR="$DATA_HOME/applications"
DESKTOP_FILE="$DESKTOP_DIR/$DESKTOP_ID.desktop"

# Install app icon into the user's icon theme so GNOME can resolve it reliably.
ICON_THEME_DIR="$DATA_HOME/icons/hicolor"
if [ -f "$ICON_SVG" ]; then
  mkdir -p "$ICON_THEME_DIR/scalable/apps"
  cp -f "$ICON_SVG" "$ICON_THEME_DIR/scalable/apps/$ICON_NAME.svg"
fi
if [ -f "$ICON_PNG" ]; then
  mkdir -p "$ICON_THEME_DIR/256x256/apps"
  cp -f "$ICON_PNG" "$ICON_THEME_DIR/256x256/apps/$ICON_NAME.png"
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -f -t "$ICON_THEME_DIR" >/dev/null 2>&1 || true
fi

mkdir -p "$DESKTOP_DIR"
cat >"$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=GPU Manager
Exec=$(desktop_exec_quote "$BIN_PATH")
Icon=$ICON_NAME
Terminal=false
Categories=System;Monitor;
StartupNotify=true
StartupWMClass=$ICON_NAME
EOF

if ! validate_desktop_entry "$DESKTOP_FILE" "$BIN_PATH"; then
  echo "Desktop entry validation failed; falling back to direct execution." >&2
  ./"$BIN"
  exit $?
fi

if command -v gtk-launch >/dev/null 2>&1; then
  echo "Launching via desktop entry: $DESKTOP_ID"
  if gtk-launch "$DESKTOP_ID" >/dev/null 2>&1; then
    exit 0
  fi
  echo "gtk-launch failed; falling back to direct execution."
fi

./"$BIN"
