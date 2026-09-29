# shellcheck shell=bash disable=SC2034
# Shared by the cv-* server scripts (sourced, not run). Plain messages for whoever clicks the icon.

CV_DIR="${CV_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"   # the project folder (git checkout)
CV_BACKUPS="$CV_DIR/backups"
cd "$CV_DIR" || exit 1

ok()   { printf '\033[1;32m✔ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*"; }
bad()  { printf '\033[1;31m✘ %s\033[0m\n' "$*"; }
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

env_get() {   # value of KEY in .env (empty if missing)
  [ -f .env ] && sed -n "s/^$1=//p" .env | tail -1 | tr -d '\r'
}

env_set() {   # set KEY=VALUE in .env (replace or append)
  touch .env
  if grep -q "^$1=" .env; then sed -i "s|^$1=.*|$1=$2|" .env; else echo "$1=$2" >> .env; fi
}

web_port() { local p; p="$(env_get WEB_PORT)"; echo "${p:-8080}"; }

http_get() {  # print the body of a local URL, fail quietly
  python3 - "$1" <<'EOF'
import sys, urllib.request
try:
    print(urllib.request.urlopen(sys.argv[1], timeout=3).read().decode())
except Exception:
    sys.exit(1)
EOF
}

preflight() {
  command -v docker >/dev/null || { bad "Docker is not installed."; return 1; }
  docker info >/dev/null 2>&1 || { bad "Docker is not running (or this user may not use it: add it to the 'docker' group)."; return 1; }
  docker compose version >/dev/null 2>&1 || { bad "'docker compose' is missing (install the docker-compose-plugin package)."; return 1; }
  [ -f config/config.yaml ] || { bad "config/config.yaml is missing: copy deploy/config.example.yaml there and fill in the cameras."; return 1; }
  check_folders
}

# The two services run as user 65532 (not root): they must be able to write these folders, and the
# settings (camera passwords) must belong to that user alone, mode 600 - the pipeline refuses any
# other access (Docs/deployment.md). install.sh sets this up once.
CV_RW_DIRS="data evidence logs model-state"
check_folders() {
  local d bad_dirs=""
  for d in $CV_RW_DIRS; do
    [ -d "$d" ] && [ "$(stat -c %u "$d")" = 65532 ] || bad_dirs="$bad_dirs $d"
  done
  if [ -n "$bad_dirs" ] || [ "$(stat -c %u config/config.yaml)" != 65532 ] \
     || [ "$(( 8#$(stat -c %a config/config.yaml) & 8#077 ))" != 0 ]; then
    bad "The system may not write its folders or read its settings. Run this once in a terminal:"
    echo "   cd $CV_DIR && sudo mkdir -p $CV_RW_DIRS && sudo chown 65532:65532 $CV_RW_DIRS config/config.yaml && sudo chmod 600 config/config.yaml"
    return 1
  fi
}

read_config() {  # print config/config.yaml (mode 600, user 65532) through Docker, which may read it
  local img
  img="$(docker compose config --images 2>/dev/null | grep pipeline | head -1)"
  docker run --rm --user 0 --entrypoint cat -v "$CV_DIR/config/config.yaml:/f:ro" "$img" /f
}

images_present() {  # are the images of the current compose + .env on this server?
  local i
  for i in $(docker compose config --images 2>/dev/null); do docker image inspect "$i" >/dev/null 2>&1 || return 1; done
}

wait_healthy() {  # up to $1 seconds for the model (healthy) and the pipeline (answers on the web port)
  local limit="${1:-300}" t=0 m p
  while [ "$t" -lt "$limit" ]; do
    m="$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q model 2>/dev/null)" 2>/dev/null)"
    p=no; http_get "http://127.0.0.1:$(web_port)/health/live" >/dev/null && p=yes
    if [ "$m" = healthy ] && [ "$p" = yes ]; then return 0; fi
    printf '\r   waiting for the system to start... %3ds (model: %s, pipeline: %s)   ' "$t" "${m:-starting}" "$p"
    sleep 5; t=$((t + 5))
  done
  echo; return 1
}

show_status() {
  local v; v="$(env_get CV_VERSION)"
  echo "Version: ${v:-not set (built on this server)}"
  docker compose ps --format 'table {{.Service}}\t{{.Status}}' 2>/dev/null
  echo
  http_get "http://127.0.0.1:$(web_port)/api/v1/status" | python3 -c "$CV_STATUS_PY" \
    || echo "The pipeline does not answer yet."
  echo "Viewer: http://127.0.0.1:$(web_port)"
}

read -r -d '' CV_STATUS_PY <<'EOF' || true
import json, sys
try:
    s = json.load(sys.stdin)
except Exception:
    print("The pipeline does not answer yet.")
    sys.exit(0)
for c in s.get("cameras") or [s]:
    n = int((c.get("counters") or {}).get("frames_total", 0))
    line = "  %-6s %-10s %s" % (c.get("camera_id") or "?", c.get("status", "?"), c.get("detail", ""))
    print(line + ("  (%s frames)" % format(n, ",") if n else ""))
print("  Alerts stored: %s of %s" % (s.get("stored_alerts", "?"), s.get("max_alerts", "?")))
EOF

pause_if_icon() {  # keep the window open when started from a desktop icon
  if [ -n "${CV_FROM_ICON:-}" ]; then echo; read -r -p "Press Enter to close this window." _; fi
}
