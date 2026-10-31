#!/usr/bin/env bash
set -euo pipefail

# Per-user setup for the portable archive. Never run from a package post-install hook.
source_dir=$(cd -- "$(dirname -- "$0")" && pwd)
install_dir="$HOME/.local/lib/spacebrowser"
data_dir="${XDG_DATA_HOME:-$HOME/.local/share}"
desktop="$data_dir/applications/spacebrowser.desktop"
service="$data_dir/kio/servicemenus/spacebrowser.desktop"
nautilus="$data_dir/nautilus/scripts/Scan with SpaceBrowser"

if [[ ${1:-} == --remove-shortcuts ]]; then
    rm -f -- "$desktop" "$service" "$nautilus"
    echo 'SpaceBrowser menu entries removed. The application has been kept.'
    exit 0
fi
[[ -f "$source_dir/spacebrowser" ]] || { echo 'Extract the complete setup archive first.' >&2; exit 1; }

if command -v zenity >/dev/null 2>&1; then
    choices=$(zenity --list --checklist --title='SpaceBrowser Setup' --width=580 --height=290 \
        --text='Install SpaceBrowser for your account. Choose optional shortcuts:' \
        --column='Enable' --column='Option' --separator='|' \
        TRUE 'Create an applications-menu shortcut' \
        FALSE 'Add Scan with SpaceBrowser to the file manager' \
        TRUE 'Launch SpaceBrowser when setup finishes') || exit 0
else
    [[ -t 0 ]] || { echo 'Run this setup in a terminal, or install Zenity for a graphical checklist.' >&2; exit 1; }
    choices=''
    for option in 'Create an applications-menu shortcut' 'Add Scan with SpaceBrowser to the file manager' 'Launch SpaceBrowser when setup finishes'; do
        read -r -p "$option? [y/N] " answer
        [[ $answer != [yY] ]] || choices+="|$option"
    done
fi

mkdir -p -- "$install_dir"
install -m 755 -- "$source_dir/spacebrowser" "$install_dir/spacebrowser"
install -m 644 -- "$source_dir/appicon.png" "$install_dir/appicon.png"

# Desktop entry Exec quoting has two escaping layers; percent signs are field codes.
desktop_quote() {
    local value=$1
    value=${value//\\/\\\\}; value=${value//\"/\\\"}
    value=${value//\`/\\\`}; value=${value//\$/\\\$}
    value=${value//\\/\\\\}; value=${value//%/%%}
    printf '"%s"' "$value"
}
exec_path=$(desktop_quote "$install_dir/spacebrowser")
if [[ $choices == *'Create an applications-menu shortcut'* ]]; then
    mkdir -p -- "$(dirname -- "$desktop")"
    printf '[Desktop Entry]\nType=Application\nName=SpaceBrowser\nExec=%s\nTerminal=false\nCategories=System;Utility;\n' "$exec_path" > "$desktop"
else
    rm -f -- "$desktop"
fi

if [[ $choices == *'Add Scan with SpaceBrowser to the file manager'* ]]; then
    mkdir -p -- "$(dirname -- "$service")" "$(dirname -- "$nautilus")"
    # Dolphin supplies the current directory for a background click, and the
    # selected directory for a folder click. Keep the action in the main menu.
    printf '[Desktop Entry]\nType=Service\nMimeType=inode/directory;\nActions=scan;\nX-KDE-Priority=TopLevel\nX-KDE-RequiredNumberOfUrls=1\nX-KDE-Protocols=file\n\n[Desktop Action scan]\nName=Scan with SpaceBrowser\nIcon=drive-harddisk\nExec=%s -- %%f\n' "$exec_path" > "$service"
    chmod 755 "$service"
    cat > "$nautilus" <<'SCRIPT'
#!/usr/bin/env bash
set -eu
# Nautilus passes selected local paths as arguments. Scan one folder at a time.
[[ $# == 1 && -d $1 ]] || exit 0
exec "$HOME/.local/lib/spacebrowser/spacebrowser" -- "$1"
SCRIPT
    chmod 755 "$nautilus"
    echo 'Added Dolphin actions and Nautilus Scripts entries. Restart your file manager if needed.'
else
    rm -f -- "$service" "$nautilus"
fi
echo "Installed SpaceBrowser in $install_dir"
if [[ $choices == *'Launch SpaceBrowser when setup finishes'* ]]; then
    nohup "$install_dir/spacebrowser" >/dev/null 2>&1 &
fi
