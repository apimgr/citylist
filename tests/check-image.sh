#!/bin/sh
# @@License : WTFPL
set -eu

IMAGE=${1:?usage: tests/check-image.sh <image>}
TIMEOUT=${TOR_BOOTSTRAP_TIMEOUT:-180}
RUN_TIMEOUT=${DOCKER_TIMEOUT:-3600}
REQUIRE_OVERLAY=${REQUIRE_OVERLAY:-0}
random_suffix() { od -An -N4 -tx1 /dev/urandom | tr -d ' \n'; }
CONTAINER="citylist-$(random_suffix)"
PACKAGE_CONTAINER="citylist-$(random_suffix)"
NETWORK="citylist-image-check-net-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)"
TOR_STATUS=""
OUTBOUND=false

case "$TIMEOUT" in
  ''|*[!0-9]*) echo "FAIL: TOR_BOOTSTRAP_TIMEOUT must be a positive integer" >&2; exit 1 ;;
esac
[ "$TIMEOUT" -gt 0 ] || { echo "FAIL: TOR_BOOTSTRAP_TIMEOUT must be a positive integer" >&2; exit 1; }
case "$RUN_TIMEOUT" in
  ''|*[!0-9]*) echo "FAIL: DOCKER_TIMEOUT must be a positive integer" >&2; exit 1 ;;
esac
[ "$RUN_TIMEOUT" -gt 0 ] || { echo "FAIL: DOCKER_TIMEOUT must be a positive integer" >&2; exit 1; }

cleanup() {
	docker stop --time 60 "$CONTAINER" >/dev/null 2>&1 || true
	docker rm "$CONTAINER" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker network create --driver bridge "$NETWORK" >/dev/null
# shellcheck disable=SC2016
timeout -k 30 "$RUN_TIMEOUT" docker run --rm --name "$PACKAGE_CONTAINER" --network "$NETWORK" --memory=128m --cpus=0.5 --entrypoint sh "$IMAGE" -c 'for c in bash curl tor i2pd tini; do command -v "$c" || exit 1; done'
timeout -k 30 "$RUN_TIMEOUT" docker run -d --name "$CONTAINER" --network "$NETWORK" --memory=2g --cpus=2 --stop-timeout=60 "$IMAGE" >/dev/null

if docker exec "$CONTAINER" curl -kfsS --max-time 5 https://1.1.1.1/cdn-cgi/trace >/dev/null 2>&1; then
  OUTBOUND=true
fi

elapsed=0
while [ "$elapsed" -le "$TIMEOUT" ]; do
  health=$(docker exec "$CONTAINER" curl -sS -H 'Accept: application/json' --max-time 5 http://127.0.0.1/server/healthz 2>/dev/null || true)
  TOR_STATUS=$(printf '%s\n' "$health" | sed -nE '/"tor"[[:space:]]*:/,/^[[:space:]]*\}/s/^[[:space:]]*"status"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p')
  case "$TOR_STATUS" in
    healthy) exit 0 ;;
    starting|unavailable) : ;;
    error|disabled)
      echo "FAIL: image Tor health status is $TOR_STATUS" >&2
      exit 1
      ;;
    "") : ;;
    *)
      echo "FAIL: unexpected image Tor health status: $TOR_STATUS" >&2
      exit 1
      ;;
  esac
  sleep 5
  elapsed=$((elapsed + 5))
done

if [ "$REQUIRE_OVERLAY" = 1 ] || [ "$OUTBOUND" = true ]; then
  echo "FAIL: Tor did not become healthy within ${TIMEOUT}s (status: ${TOR_STATUS:-unavailable})" >&2
  exit 1
fi

if [ "$TOR_STATUS" != starting ] && [ "$TOR_STATUS" != unavailable ]; then
  echo "FAIL: no-network image status must be starting or unavailable (got: ${TOR_STATUS:-empty})" >&2
  exit 1
fi
echo "SKIP: no outbound network; Tor remains $TOR_STATUS" >&2
