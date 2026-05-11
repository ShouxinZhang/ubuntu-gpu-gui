#!/bin/bash

# Get the absolute path of the current directory
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

echo "Building the application..."
go build -tags 'desktop,production,webkit2_41' -o ubuntu-gpu-gui .

if [ $? -ne 0 ]; then
    echo "Build failed!"
    exit 1
fi

echo "Creating desktop entry..."
DESKTOP_FILE="$HOME/.local/share/applications/ubuntu-gpu-gui.desktop"
ICON_PATH="$DIR/appicon.png"
BIN_PATH="$DIR/ubuntu-gpu-gui"

cat > "$DESKTOP_FILE" <<EOL
[Desktop Entry]
Name=GPU Manager
Comment=Monitor GPU status and set power limits
Exec=$BIN_PATH
Icon=$ICON_PATH
Terminal=false
Type=Application
Categories=System;Monitor;
Keywords=gpu;nvidia;monitor;power;
EOL

chmod +x "$DESKTOP_FILE"

echo "Installation complete!"
echo "You can now find 'GPU Manager' in your application menu."
