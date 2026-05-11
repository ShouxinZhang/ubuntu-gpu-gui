#!/bin/bash
set -euo pipefail

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$DIR"

BIN_NAME="ubuntu-gpu-gui"
BIN_PATH="$DIR/$BIN_NAME"
ICON_PATH="$DIR/appicon.png"
ICON_NAME="gpu-manager"
DATA_HOME="${XDG_DATA_HOME:-${HOME:?HOME is not set}/.local/share}"
DESKTOP_DIR="$DATA_HOME/applications"
DESKTOP_FILE="$DESKTOP_DIR/ubuntu-gpu-gui.desktop"
ICON_THEME_DIR="$DATA_HOME/icons/hicolor"
INSTALLED_ICON="$ICON_THEME_DIR/256x256/apps/$ICON_NAME.png"

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
    local icon_file=$3

    if grep -Eq '\$\{[^}]+\}' "$desktop_file"; then
        echo "Desktop entry contains unresolved placeholders: $desktop_file" >&2
        return 1
    fi
    if [ ! -x "$exec_path" ]; then
        echo "Desktop Exec target is not executable: $exec_path" >&2
        return 1
    fi
    if [ ! -f "$icon_file" ]; then
        echo "Desktop icon was not installed: $icon_file" >&2
        return 1
    fi
    if command -v desktop-file-validate >/dev/null 2>&1; then
        desktop-file-validate "$desktop_file"
    fi
}

echo "Building the application..."
go build -tags 'desktop,production,webkit2_41' -o "$BIN_NAME" .

if [ ! -f "$ICON_PATH" ]; then
    echo "Missing application icon: $ICON_PATH" >&2
    exit 1
fi

echo "Installing icon..."
mkdir -p "$(dirname "$INSTALLED_ICON")"
cp -f "$ICON_PATH" "$INSTALLED_ICON"
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -f -t "$ICON_THEME_DIR" >/dev/null 2>&1 || true
fi

echo "Creating desktop entry..."
mkdir -p "$DESKTOP_DIR"
cat > "$DESKTOP_FILE" <<EOL
[Desktop Entry]
Type=Application
Name=GPU Manager
Comment=Monitor GPU status and set power limits
Exec=$(desktop_exec_quote "$BIN_PATH")
Icon=$ICON_NAME
Terminal=false
Categories=System;Monitor;
Keywords=gpu;nvidia;monitor;power;
StartupNotify=true
StartupWMClass=$ICON_NAME
EOL

chmod +x "$DESKTOP_FILE"
validate_desktop_entry "$DESKTOP_FILE" "$BIN_PATH" "$INSTALLED_ICON"

echo "Installation complete!"
echo "You can now find 'GPU Manager' in your application menu."
