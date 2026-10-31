#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
sandbox=$(mktemp -d)
# Isolate all setup changes from the real desktop and capture launches instead.
test_home="$sandbox/home with spaces % and dollar\$"
export XDG_DATA_HOME="$test_home/.local/share"
export CAPTURE="$sandbox/arguments"
mkdir -p "$sandbox/bin" "$sandbox/source" "$test_home"
cp "$root/packaging/linux/Install.sh" "$sandbox/source/Install.sh"
touch "$sandbox/source/appicon.png"
cat > "$sandbox/source/spacebrowser" <<'SCRIPT'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$CAPTURE"
SCRIPT
cat > "$sandbox/bin/zenity" <<'SCRIPT'
#!/usr/bin/env bash
printf '%s' "$CHOICES"
exit "${CANCEL:-0}"
SCRIPT
chmod +x "$sandbox/bin/zenity"
export PATH="$sandbox/bin:$PATH"
export CHOICES='Create an applications-menu shortcut|Add Scan with SpaceBrowser to the file manager'
setup() { env HOME="$test_home" bash "$sandbox/source/Install.sh" "$@"; }
setup
test -x "$test_home/.local/lib/spacebrowser/spacebrowser"
test -f "$XDG_DATA_HOME/applications/spacebrowser.desktop"
test -f "$XDG_DATA_HOME/kio/servicemenus/spacebrowser.desktop"
service="$XDG_DATA_HOME/kio/servicemenus/spacebrowser.desktop"
# A background click is represented by Dolphin as one directory URL, just
# like a selected folder. Do not bury this action in the Actions submenu.
grep -Fx 'MimeType=inode/directory;' "$service" >/dev/null
grep -Fx 'X-KDE-Priority=TopLevel' "$service" >/dev/null
grep -Fx 'X-KDE-RequiredNumberOfUrls=1' "$service" >/dev/null
grep -Fx 'X-KDE-Protocols=file' "$service" >/dev/null
grep -E '^Exec=.* -- %f$' "$service" >/dev/null
grep -F '%%' "$service" >/dev/null
folder="$test_home/a folder & (special) --no-cache"
mkdir "$folder"
env HOME="$test_home" bash "$XDG_DATA_HOME/nautilus/scripts/Scan with SpaceBrowser" "$folder"
test "$(head -n 1 "$CAPTURE")" = '--'
test "$(tail -n 1 "$CAPTURE")" = "$folder"
rm -f "$CAPTURE"
env HOME="$test_home" bash "$XDG_DATA_HOME/nautilus/scripts/Scan with SpaceBrowser" "$folder" "$folder"
test ! -f "$CAPTURE"
# Cancelling setup leaves existing entries alone.
export CANCEL=1
setup
test -f "$XDG_DATA_HOME/applications/spacebrowser.desktop"
export CANCEL=0 CHOICES=''
setup
test ! -f "$XDG_DATA_HOME/applications/spacebrowser.desktop"
test ! -f "$XDG_DATA_HOME/kio/servicemenus/spacebrowser.desktop"
test ! -f "$XDG_DATA_HOME/nautilus/scripts/Scan with SpaceBrowser"
export CHOICES='Add Scan with SpaceBrowser to the file manager'
setup
setup --remove-shortcuts
test ! -f "$XDG_DATA_HOME/kio/servicemenus/spacebrowser.desktop"
test -x "$test_home/.local/lib/spacebrowser/spacebrowser"
echo 'Installer isolation, cancellation, path forwarding and shortcut removal passed.'
