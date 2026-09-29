#!/usr/bin/env bash
# One-time setup on the plant server (Ubuntu desktop): checks the tools and puts 5 icons on the
# Desktop and in the applications menu: Update, Status, Viewer, Save logs, Roll back.
#   bash deploy/server/install.sh
source "$(dirname "${BASH_SOURCE[0]}")/cv-common.sh"
here="$CV_DIR/deploy/server"

step "Checking the tools"
fail=""
for t in git python3 docker; do command -v "$t" >/dev/null && ok "$t" || { bad "$t is missing"; fail=1; }; done
docker compose version >/dev/null 2>&1 && ok "docker compose" || { bad "docker compose is missing (sudo apt install docker-compose-plugin)"; fail=1; }
if ! docker info >/dev/null 2>&1; then
  bad "This user may not use Docker. Run:  sudo usermod -aG docker $USER   then log out and in again."; fail=1
fi
[ -f config/config.yaml ] && ok "config/config.yaml" || { bad "config/config.yaml is missing (copy deploy/config.example.yaml and fill in the cameras)"; fail=1; }
[ -z "$fail" ] || { bad "Fix the lines above, then run this again."; exit 1; }

step "Folder permissions (the services run as user 65532)"
if check_folders >/dev/null 2>&1; then ok "already set"
else
  echo "Your password is asked once, to let the services write their folders and read the settings."
  # shellcheck disable=SC2086
  sudo mkdir -p $CV_RW_DIRS && sudo chown 65532:65532 $CV_RW_DIRS config/config.yaml \
    && ok "set" || { bad "could not set them"; exit 1; }
fi
chmod +x "$here"/cv-update "$here"/cv-rollback "$here"/cv-status "$here"/cv-logs

step "Creating the icons"
term() {   # command line that opens a terminal window running a cv-* script
  if command -v gnome-terminal >/dev/null; then
    echo "gnome-terminal --title=\"Clinker Vision - $1\" -- env CV_FROM_ICON=1 bash \"$here/$2\""
  else
    echo "x-terminal-emulator -e env CV_FROM_ICON=1 bash \"$here/$2\""
  fi
}
desk="$(xdg-user-dir DESKTOP 2>/dev/null || echo "$HOME/Desktop")"
apps="$HOME/.local/share/applications"
mkdir -p "$desk" "$apps"
make_icon() {  # file, name, comment, icon, exec
  local f
  for f in "$desk/$1.desktop" "$apps/$1.desktop"; do
    cat > "$f" <<EOF
[Desktop Entry]
Type=Application
Name=$2
Comment=$3
Icon=$4
Exec=$5
Terminal=false
Categories=Utility;
EOF
    chmod +x "$f"
    gio set "$f" metadata::trusted true 2>/dev/null || true
  done
  ok "$2"
}
make_icon clinker-update   "Clinker Vision - Update"    "Install the newest version (automatic backup, goes back by itself if it fails)" system-software-update "$(term Update cv-update)"
make_icon clinker-status   "Clinker Vision - Status"    "Is it running? Version and each camera"           dialog-information "$(term Status cv-status)"
make_icon clinker-viewer   "Clinker Vision - Viewer"    "Open the alerts viewer"                            web-browser        "xdg-open http://127.0.0.1:$(web_port)"
make_icon clinker-logs     "Clinker Vision - Save logs" "One file on the Desktop to send when something is wrong" document-save "$(term 'Save logs' cv-logs)"
make_icon clinker-rollback "Clinker Vision - Roll back" "Go back to the version before the last update"     edit-undo          "$(term 'Roll back' cv-rollback)"

echo
ok "Done. If an icon shows a warning the first time, right-click it and choose 'Allow Launching'."
echo "Next: double-click 'Clinker Vision - Update'."
