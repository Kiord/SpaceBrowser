#!/bin/bash
set -euo pipefail
source_dir=$(cd -- "$(dirname -- "$0")" && pwd)
destination="$HOME/Applications/SpaceBrowser.app"
service="$HOME/Library/Services/Scan with SpaceBrowser.workflow"
if [[ ${1:-} == --remove-shortcuts ]]; then
    # Only the two files created by this setup are removed; preserve unknown contents.
    rm -f -- "$service/Contents/Info.plist" "$service/Contents/document.wflow"
    rmdir "$service/Contents" "$service" 2>/dev/null || true
    exit 0
fi
[[ -d "$source_dir/SpaceBrowser.app" ]] || { echo 'Open setup from the downloaded disk image or extracted ZIP.' >&2; exit 1; }
choices=$(/usr/bin/osascript <<'APPLESCRIPT'
set options to {"Add Scan with SpaceBrowser to Finder", "Launch SpaceBrowser when setup finishes"}
set selected to choose from list options with title "SpaceBrowser Setup" with prompt "Install in your Applications folder. Select optional features (Command-click to select several):" default items {"Launch SpaceBrowser when setup finishes"} OK button name "Install" multiple selections allowed true empty selection allowed true
if selected is false then return "cancel"
return selected as text
APPLESCRIPT
)
[[ $choices != cancel ]] || exit 0
mkdir -p "$HOME/Applications"
/usr/bin/ditto "$source_dir/SpaceBrowser.app" "$destination"
if [[ $choices == *'Add Scan with SpaceBrowser to Finder'* ]]; then
    mkdir -p "$HOME/Library/Services"
    /usr/bin/ditto "$source_dir/Scan with SpaceBrowser.workflow" "$service"
else
    rm -f -- "$service/Contents/Info.plist" "$service/Contents/document.wflow"
    rmdir "$service/Contents" "$service" 2>/dev/null || true
fi
if [[ $choices == *'Launch SpaceBrowser when setup finishes'* ]]; then
    /usr/bin/open "$destination"
fi
echo 'SpaceBrowser installed in your Applications folder.'
