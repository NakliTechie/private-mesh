#!/usr/bin/env bash
# Installs com.naklitechie.hub.plist into ~/Library/LaunchAgents/ with
# __USER_HOME__ replaced by $HOME. Idempotent.
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/com.naklitechie.hub.plist"
DEST="$HOME/Library/LaunchAgents/com.naklitechie.hub.plist"
LOG_DIR="$HOME/Library/Logs"

mkdir -p "$(dirname "$DEST")" "$LOG_DIR"
sed "s|__USER_HOME__|$HOME|g" "$SRC" > "$DEST"
chmod 0644 "$DEST"

echo "Installed $DEST"
echo "Next:"
echo "  launchctl bootstrap gui/\$(id -u) $DEST"
echo "  launchctl kickstart -k gui/\$(id -u)/com.naklitechie.hub"
echo "  tail -f $LOG_DIR/nakli-hub.log"
