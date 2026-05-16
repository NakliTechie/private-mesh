# nakli-hub/contrib

Optional service-management templates for running `nakli-hub` as a long-lived process.

## Linux — systemd

1. Install the binary to `/usr/local/bin/nakli-hub`.
2. Create a dedicated user: `sudo useradd --system --home /var/lib/nakli-hub --shell /usr/sbin/nologin nakli` (or whichever directory you chose).
3. `sudo nakli-hub init --data-dir /var/lib/nakli-hub --config /etc/nakli-hub/config.json`. Hand ownership to the `nakli` user.
4. `sudo cp nakli-hub.service /etc/systemd/system/`.
5. `sudo systemctl daemon-reload && sudo systemctl enable --now nakli-hub`.
6. `journalctl -u nakli-hub -f` to follow logs.

## macOS — launchd

1. Install the binary to `/usr/local/bin/nakli-hub`.
2. `nakli-hub init --data-dir "$HOME/Library/Application Support/nakli-hub"`.
3. Copy `com.naklitechie.hub.plist` to `~/Library/LaunchAgents/`, replacing `__USER_HOME__` with `$HOME`. The included `install.sh` does this for you:
   ```sh
   ./install-launchd.sh
   ```
4. `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.naklitechie.hub.plist`.
5. `tail -f "$HOME/Library/Logs/nakli-hub.log"` to follow logs.

## Editing

Paths in both files are intentional defaults. Adjust them before installing rather than relying on env overrides — service managers run with restricted environments by design.
