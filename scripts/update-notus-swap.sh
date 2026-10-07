#!/usr/bin/env bash
#
# Updates notus-swap to the newest release.
#
#   ./update-notus-swap.sh             update, if a newer release exists
#   ./update-notus-swap.sh --force     reinstall the newest release anyway
#   ./update-notus-swap.sh --rollback  go back to the previous version
#
# It keeps the running binary as notus-swap.prev, restarts the service, and
# rolls back by itself if the new version does not answer within 30 seconds.
# Settings come from the env file next to it. Releases come from Gitea when
# GITEA_URL, GITEA_REPO, GITEA_USER and GITEA_TOKEN are all set, and otherwise
# from GitHub: NOTUS_GITHUB_REPO, or wispborne/notus-swap if it isn't set.
# GITHUB_TOKEN is used if set. NOTUS_LISTEN gives the port to check.

set -euo pipefail
DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
cd "$DIR"

set -a
. ./env
set +a

PORT="${NOTUS_LISTEN##*:}"
HEALTH="http://127.0.0.1:${PORT:-8080}/notus/api/health"
ASSET=notus-swap-linux-amd64

# json_field prints the first "name":"value" string in the JSON on stdin.
json_field() {
  sed -n "s/.*\"$1\" *: *\"\([^\"]*\)\".*/\1/p" | head -n 1
}

running_version() {
  curl -sf --max-time 3 "$HEALTH" | json_field version || true
}

if [ -n "${GITEA_URL:-}" ] && [ -n "${GITEA_REPO:-}" ] && [ -n "${GITEA_USER:-}" ] && [ -n "${GITEA_TOKEN:-}" ]; then
  latest_tag() {
    curl -sSf -u "$GITEA_USER:$GITEA_TOKEN" "$GITEA_URL/api/v1/repos/$GITEA_REPO/releases/latest" | json_field tag_name
  }
  download() { # file tag destination
    curl -sSfL -u "$GITEA_USER:$GITEA_TOKEN" -o "$3" "$GITEA_URL/$GITEA_REPO/releases/download/$2/$1"
  }
else
  GITHUB_REPO="${NOTUS_GITHUB_REPO-wispborne/notus-swap}"
  if [ -z "$GITHUB_REPO" ]; then
    echo "Updates are off: NOTUS_GITHUB_REPO is empty in the env file." >&2
    exit 1
  fi
  github_api() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -sSf -H "Authorization: Bearer $GITHUB_TOKEN" "https://api.github.com/repos/$GITHUB_REPO/$1"
    else
      curl -sSf "https://api.github.com/repos/$GITHUB_REPO/$1"
    fi
  }
  latest_tag() { github_api releases/latest | json_field tag_name; }
  download() { # file tag destination
    curl -sSfL -o "$3" "https://github.com/$GITHUB_REPO/releases/download/$2/$1"
  }
fi

restart() {
  # The polkit rule from docs/install.md allows this without sudo.
  systemctl restart notus-swap.service 2>/dev/null || sudo systemctl restart notus-swap.service
}

wait_healthy() {
  for _ in $(seq 1 30); do
    v=$(running_version)
    if [ -n "$v" ]; then
      echo "$v"
      return 0
    fi
    sleep 1
  done
  return 1
}

rollback() {
  if [ ! -f notus-swap.prev ]; then
    echo "No previous version to restore." >&2
    exit 1
  fi
  cp notus-swap.prev notus-swap
  restart
  echo "Rolled back. Running: $(wait_healthy || echo 'not responding')"
}

case "${1:-}" in
  --rollback) rollback; exit 0 ;;
  --force | "") ;;
  *) sed -n '3,15p' "$0"; exit 1 ;;
esac

tag=$(latest_tag)
if [ -z "$tag" ]; then
  echo "Couldn't find the newest release." >&2
  exit 1
fi
current=$(running_version)
echo "Running: ${current:-not responding}"
echo "Newest:  $tag"
if [ "$current" = "$tag" ] && [ "${1:-}" != "--force" ]; then
  echo "Already up to date."
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for f in "$ASSET" "$ASSET.sha256"; do
  download "$f" "$tag" "$tmp/$f"
done
(cd "$tmp" && sha256sum -c --quiet "$ASSET.sha256")
chmod +x "$tmp/$ASSET"

[ -f notus-swap ] && cp notus-swap notus-swap.prev
mv "$tmp/$ASSET" notus-swap
restart

if v=$(wait_healthy); then
  echo "Updated. Running: $v"
else
  echo "New version did not respond within 30 seconds; restoring the previous version." >&2
  journalctl -u notus-swap.service -n 20 --no-pager >&2 || true
  rollback
  exit 1
fi
