#!/usr/bin/env bash
# Deploy (or roll back) the production stack on the VM.
#
#   deploy/deploy.sh             the newest image CI built from main
#   deploy/deploy.sh <commit>    the image of that commit of main (also a
#                                short id, e.g. c26b5be); use an earlier one
#                                to roll back
#
# Steps: find the commit, move this copy of the repository to it (compose
# files, SearXNG settings), download its image, restart the stack, wait
# until the server is healthy. Every deploy is written to
# /opt/ofd/deploys.log. .env is never changed.
#
# CI builds an image only for commits that change the server
# (Owis_Find_Deal_Engine/server); for other commits use no argument.
set -euo pipefail

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
die() { printf '\n\033[31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

# All in one function: bash reads it completely before running, so the
# checkout below can change this file safely.
main() {
IMAGE=${OFD_IMAGE:-ghcr.io/owisdev/owis_find_deal_engine}
export OFD_IMAGE=$IMAGE
SERVER_DIR=$(cd "$(dirname "$0")/.." && pwd)
REPO_DIR=$(git -C "$SERVER_DIR" rev-parse --show-toplevel)
LOG=${DEPLOY_LOG:-/opt/ofd/deploys.log}

cd "$SERVER_DIR"
[ -f .env ] || die ".env missing in $SERVER_DIR (see deploy/bootstrap-vm.sh)"
[ "$(stat -c %a .env)" = 600 ] || { chmod 600 .env; echo "made .env readable by you only (600)"; }
docker info >/dev/null 2>&1 || die "cannot run docker (log out and in again after bootstrap-vm.sh?)"

say "1/5 Which commit"
git -C "$REPO_DIR" fetch -q origin main
if [ $# -ge 1 ]; then
  COMMIT=$(git -C "$REPO_DIR" rev-parse --verify --quiet "$1^{commit}") || die "unknown commit: $1 (only commits of main can be deployed)"
  git -C "$REPO_DIR" merge-base --is-ancestor "$COMMIT" origin/main || die "$1 is not on main"
  docker pull -q "$IMAGE:$COMMIT" >/dev/null 2>&1 ||
    die "no image for $1: CI builds images only for commits that change the server and pass CI (see the Actions tab)"
else
  docker pull -q "$IMAGE:main" >/dev/null || die "cannot download $IMAGE:main (is the package public? see the server README, Production)"
  COMMIT=$(docker image inspect "$IMAGE:main" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}')
  [ -n "$COMMIT" ] || die "$IMAGE:main has no revision label"
fi
echo "commit: $(git -C "$REPO_DIR" log --oneline -1 "$COMMIT")"
PREVIOUS=$(docker inspect owis_find_deal_engine-server-1 --format '{{ .Config.Image }}' 2>/dev/null | sed 's/.*://' || true)
[ -n "$PREVIOUS" ] && echo "running now: ${PREVIOUS:0:12}"

say "2/5 Repository files of that commit"
if ! git -C "$REPO_DIR" diff --quiet || ! git -C "$REPO_DIR" diff --cached --quiet; then
  git -C "$REPO_DIR" status --short
  die "files in $REPO_DIR were changed by hand; undo that first (git -C $REPO_DIR checkout -- .)"
fi
git -C "$REPO_DIR" checkout -q --detach "$COMMIT"

export IMAGE_TAG=$COMMIT VERSION=$COMMIT
# cloudflared (the tunnel) only once its token is in .env.
if grep -qE '^TUNNEL_TOKEN=.+' .env; then
  export COMPOSE_PROFILES=tunnel
  echo "tunnel: on"
else
  echo "tunnel: off (no TUNNEL_TOKEN in .env; reach the server with ssh -L 3002:127.0.0.1:3002 ...)"
fi
compose() { docker compose -f docker-compose.yml -f compose.prod.yml "$@"; }
compose config -q

say "3/5 Download images"
compose pull -q

say "4/5 Start"
compose up -d --remove-orphans

say "5/5 Wait until healthy"
state=starting
for _ in $(seq 1 40); do
  state=$(docker inspect owis_find_deal_engine-server-1 --format '{{ .State.Health.Status }}' 2>/dev/null || echo starting)
  [ "$state" = healthy ] && break
  [ "$state" = unhealthy ] && break
  sleep 3
done
compose ps --format 'table {{.Service}}\t{{.Status}}'
if [ "$state" != healthy ]; then
  compose logs --tail 30 server
  die "server is $state. Roll back: deploy/deploy.sh ${PREVIOUS:-<previous commit>}"
fi
if [ "${COMPOSE_PROFILES:-}" = tunnel ]; then
  tunnel=starting
  for _ in $(seq 1 20); do
    tunnel=$(docker inspect owis_find_deal_engine-cloudflared-1 --format '{{ if .State.Health }}{{ .State.Health.Status }}{{ else }}{{ .State.Status }}{{ end }}' 2>/dev/null || echo missing)
    [ "$tunnel" = healthy ] && break
    sleep 3
  done
  if [ "$tunnel" = healthy ]; then
    echo "tunnel: connected"
  else
    docker logs --tail 5 owis_find_deal_engine-cloudflared-1 2>&1 || true
    printf '\n\033[33mWARNING: the tunnel is %s (wrong TUNNEL_TOKEN?). The server runs; only https://globalprodsearch.dawirli.com is down.\033[0m\n' "$tunnel"
  fi
fi
compose logs server 2>/dev/null | grep '"server started"' | tail -1 | grep -o '"version":"[^"]*"' || true
curl -fsS http://127.0.0.1:${API_PORT:-3002}/api/v1/health && echo

mkdir -p "$(dirname "$LOG")" 2>/dev/null || true
printf '%s %s %s (was %s)\n' "$(date -u +%FT%TZ)" "$(id -un)" "$COMMIT" "${PREVIOUS:-none}" >> "$LOG" 2>/dev/null || true
docker image prune -f >/dev/null
say "Deployed ${COMMIT:0:12}"
}

main "$@"
exit
