#!/usr/bin/env bash
# @@License : WTFPL
set -eo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)
REMOTE_URL=$(git -C "$PROJECT_ROOT" remote get-url origin 2>/dev/null || true)
REMOTE_PATH=${REMOTE_URL#*://}
if [[ "$REMOTE_URL" == *:* && "$REMOTE_URL" != *://* ]]; then REMOTE_PATH=${REMOTE_URL#*:}; fi
REMOTE_PATH=${REMOTE_PATH%.git}
PROJECT_NAME=$(basename "${REMOTE_PATH:-$PROJECT_ROOT}")
PROJECT_ORG=$(basename "$(dirname "${REMOTE_PATH:-$PROJECT_ROOT}")")
INTERNAL_NAME=${INTERNAL_NAME:-$PROJECT_NAME}
TEMP_ROOT=${TMPDIR:-/tmp}/$PROJECT_ORG
mkdir -p "$TEMP_ROOT"
TEST_DIR=$(mktemp -d "$TEMP_ROOT/${INTERNAL_NAME}-docker-XXXXXX")
chmod 700 "$TEST_DIR"
SUFFIX=${TEST_DIR##*-}
TEST_IMAGE="${PROJECT_NAME}:test-${SUFFIX,,}"
TEST_NETWORK="${PROJECT_NAME}-test-net-${SUFFIX,,}"
PORT=$((64000 + RANDOM % 1000))
VOLUME_DIR="$TEST_DIR/volumes"
ARTIFACT_DIR="$TEST_DIR/artifacts"
BIN_DIR="$TEST_DIR/binaries"
mkdir -p "$VOLUME_DIR/config" "$VOLUME_DIR/data" "$ARTIFACT_DIR"
chmod 700 "$VOLUME_DIR" "$ARTIFACT_DIR"
# The service drops privileges after startup, so it must traverse the mounted roots.
chmod 711 "$VOLUME_DIR/config" "$VOLUME_DIR/data"

compose() {
  CITYLIST_TEST_IMAGE="$TEST_IMAGE" CITYLIST_TEST_PORT="$PORT" CITYLIST_TEST_VOLUME_DIR="$VOLUME_DIR" CITYLIST_TEST_NETWORK="$TEST_NETWORK" \
    docker compose --project-name "${PROJECT_NAME}-test-${SUFFIX,,}" --file "$PROJECT_ROOT/docker/docker-compose.test.yml" "$@"
}
cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then compose logs >"$ARTIFACT_DIR/compose.log" 2>&1 || true; fi
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$TEST_IMAGE" >/dev/null 2>&1 || true
  if [[ "$status" -eq 0 ]]; then rm -rf -- "$TEST_DIR"; else printf 'Docker test artifacts retained at %s\n' "$ARTIFACT_DIR" >&2; fi
  return "$status"
}
trap cleanup EXIT INT TERM

cd "$PROJECT_ROOT"
echo '=== Building server and client into project-scoped temp storage ==='
make build BINDIR="$BIN_DIR"

echo '=== Building and starting the application container ==='
docker build --tag "$TEST_IMAGE" --file docker/Dockerfile .
compose up --detach

# The test runner lives on the same isolated network as the application and Valkey.
docker run --rm --name "citylist-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
  --network "$TEST_NETWORK" --memory=4g --cpus=2 --user 0:0 \
  -v "$BIN_DIR:/app:ro" -v "$VOLUME_DIR/config:/config:ro" -v "$ARTIFACT_DIR:/artifacts" \
  -e CITYLIST_BASE_URL="http://citylist" -e CITYLIST_PORT="$PORT" \
  -e TOR_WAIT="${TOR_WAIT:-300}" -e I2P_WAIT="${I2P_WAIT:-600}" -e REQUIRE_OVERLAY="${REQUIRE_OVERLAY:-0}" \
  alpine:latest sh -ec '
    apk add --no-cache curl bash file jq tor i2pd tzdata ca-certificates >/dev/null
    FAILURES=0
    fail() { echo "FAIL: $*"; FAILURES=$((FAILURES + 1)); }
    pass() { echo "PASS: $*"; }
    status() { curl -q -LSs -o /dev/null -w "%{http_code}" "$1"; }
    BASE="$CITYLIST_BASE_URL"
    READY=false
    for _ in $(seq 1 90); do
      if curl -q -LSsf --max-time 5 "$BASE/server/healthz" > /artifacts/health.json 2>/dev/null; then READY=true; break; fi
      sleep 2
    done
    if [ "$READY" != true ]; then echo "server did not become ready" >&2; exit 1; fi

    echo "=== Binary metadata and rename checks ==="
    /app/citylist --version
    /app/citylist --help
    file /app/citylist
    cp /app/citylist /tmp/renamed-server && chmod +x /tmp/renamed-server
    /tmp/renamed-server --help 2>&1 | grep -q renamed-server || fail "server binary rename"
    /app/citylist-cli --version
    /app/citylist-cli --help
    cp /app/citylist-cli /tmp/renamed-cli && chmod +x /tmp/renamed-cli
    /tmp/renamed-cli --help 2>&1 | grep -q renamed-cli || fail "CLI binary rename"

    echo "=== Public API and content negotiation ==="
    for path in / /search /coordinates /server/about /server/help /server/help/mcp /server/contact \
      /server/security /server/security/policy /server/security/thanks /server/privacy /server/dpo \
      /server/terms /server/preferences /server/preferences/sync /server/preferences/tokens \
      /server/docs/swagger /server/docs/graphql /server/healthz /api/v1/ /api/v1/cities/search?q=London \
      /api/v1/cities/2643743 /api/v1/cities/country/GB \
      "/api/v1/cities/coordinates?latitude=51.5072&longitude=-0.1276" \
      "/api/v1/cities/nearby?lat=51.5072&lon=-0.1276&radius=25" \
      /api/v1/zipcodes/10001 "/api/v1/zipcodes/search?q=New%20York" /api/v1/zipcodes/state/NY \
      "/api/v1/zipcodes/nearby?lat=40.7128&lon=-74.0060&radius_km=25" /api/v1/random /api/v1/stats \
      /api/v1/count /api/v1/server/about /api/v1/server/help /api/v1/server/preferences \
      /api/v1/server/preferences/sync /api/v1/server/healthz /api/v1/server/swagger \
      /locales/en.json /static/css/main.css /manifest.json /sw.js /robots.txt \
      /.well-known/security.txt /.well-known/llms.txt /llms.txt; do
      code=$(status "$BASE$path")
      [ "$code" = 200 ] || fail "GET $path returned $code"
    done
    for path in /api/v1/server/healthz /api/v1/server/healthz.txt /api/v1/cities/search.txt?q=London /api/v1/stats.txt; do
      code=$(status "$BASE$path")
      [ "$code" = 200 ] || fail "GET $path returned $code"
    done
    curl -q -LSsf -H "Accept: application/json" "$BASE/server/healthz" | jq -e .status >/dev/null || fail "health JSON negotiation"
    curl -q -LSsf -H "Accept: text/plain" "$BASE/server/healthz" | grep -qi status || fail "health text negotiation"
    curl -q -LSsf -H "Accept: text/html" "$BASE/search" | grep -qi "<html" || fail "browser HTML negotiation"
    curl -q -LSsf "$BASE/api/v1/server/swagger" | jq -e .openapi >/dev/null || fail "OpenAPI JSON route"
    [ "$(status "$BASE/api/v1/server/graphql")" = 405 ] || fail "GraphQL GET must be rejected"
    [ "$(status "$BASE/api/graphql")" = 405 ] || fail "GraphQL alias GET must be rejected"
    [ "$(status "$BASE/api/v1/server/mcp")" = 405 ] || fail "MCP GET must be rejected"
    curl -q -LSsf -H "Content-Type: application/json" \
      -d "{\"query\":\"{ cities(limit: 1) { name } }\"}" "$BASE/api/v1/server/graphql" \
      | jq -e .data.cities[0].name >/dev/null || fail "GraphQL POST query"
    curl -q -LSsf -H "Accept: text/plain" "$BASE/api/v1/cities/search?q=London" | grep -qi London || fail "API text negotiation"
    [ "$(status "$BASE/__citylist_missing_route__")" = 404 ] || fail "unknown page must return 404"
    INVALID_CITY_STATUS=$(status "$BASE/api/v1/cities/not-an-id")
    [ "$INVALID_CITY_STATUS" = 400 ] || [ "$INVALID_CITY_STATUS" = 404 ] || fail "invalid city identifier returned $INVALID_CITY_STATUS"
    [ "$(curl -q -LSs -o /dev/null -w "%{http_code}" -X POST "$BASE/server/preferences")" = 403 ] || fail "CSRF must reject a form POST without token"
    curl -q -LSsf -X POST -H "Content-Type: application/json" -d "{\"latitude\":51.5072,\"longitude\":-0.1276}" "$BASE/api/v1/cities/coordinates" >/dev/null || fail "coordinate POST"

    echo "=== Operator and owner token checks ==="
    SECRETS=/config/citylist/secrets.yml
    if [ ! -f "$SECRETS" ]; then fail "server secrets.yml missing"; else
      OPERATOR_TOKEN=$(sed -n "s/^[[:space:]]*token:[[:space:]]*\\([^# ]*\\).*/\\1/p" "$SECRETS" | head -1)
    fi
    [ -n "${OPERATOR_TOKEN:-}" ] || fail "operator token missing from secrets.yml"
    [ "$(curl -q -LSs -o /dev/null -w "%{http_code}" "$BASE/api/v1/server/auth/check")" = 401 ] || fail "missing bearer token accepted"
    [ "$(curl -q -LSs -o /dev/null -w "%{http_code}" -H "Authorization: Bearer invalid-citylist-token" "$BASE/api/v1/server/auth/check")" = 401 ] || fail "invalid bearer token accepted"
    curl -q -LSsf -H "Authorization: Bearer $OPERATOR_TOKEN" "$BASE/api/v1/server/auth/check" | jq -r .data.kind | grep -qx operator || fail "operator bearer token rejected"
    [ "$(curl -q -LSs -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "{}" "$BASE/api/v1/server/mcp")" = 401 ] || fail "MCP accepted missing bearer token"
    OWNER_TOKEN=$(curl -q -LSsf -X POST -H "Content-Type: application/json" -d "{}" "$BASE/api/v1/server/preferences/tokens/primary" | jq -r ".data.owner_token // empty")
    [ -n "$OWNER_TOKEN" ] || fail "primary owner token issuance"
    if [ -n "$OWNER_TOKEN" ]; then
      curl -q -LSsf -H "Authorization: Bearer $OWNER_TOKEN" "$BASE/api/v1/server/preferences/tokens" | jq -e .ok >/dev/null || fail "owner token management authorization"
      [ "$(curl -q -LSs -o /dev/null -w "%{http_code}" -X POST -H "Authorization: Bearer $OWNER_TOKEN" -H "Content-Type: application/json" -d "{}" "$BASE/api/v1/server/preferences/tokens/primary")" = 409 ] || fail "duplicate primary token issuance should conflict"
    fi

    echo "=== CLI end-to-end requests ==="
    /app/citylist-cli --server "$BASE" --token "$OPERATOR_TOKEN" search London GB >/dev/null || fail "CLI city search"
    /app/citylist-cli --server "$BASE" --token "$OPERATOR_TOKEN" city 2643743 >/dev/null || fail "CLI city lookup"
    /app/citylist-cli --server "$BASE" --token "$OPERATOR_TOKEN" zipcode 10001 >/dev/null || fail "CLI ZIP lookup"
    /app/citylist-cli --server "$BASE" --token "$OPERATOR_TOKEN" zip-search York NY >/dev/null || fail "CLI ZIP search"
    /app/citylist-cli --server "$BASE" --token "$OPERATOR_TOKEN" stats >/dev/null || fail "CLI stats"

    echo "=== Overlay contracts ==="
    HEALTH=$(curl -q -LSsf "$BASE/api/v1/server/healthz")
    TOR_ENABLED=$(echo "$HEALTH" | jq -r .features.tor.enabled)
    TOR_STATUS=$(echo "$HEALTH" | jq -r .features.tor.status)
    [ "$TOR_ENABLED" = true ] || fail "Tor should be enabled when the image includes tor"
    case "$TOR_STATUS" in starting|healthy|unavailable) ;; *) fail "invalid Tor status $TOR_STATUS" ;; esac
    if [ "$TOR_STATUS" != healthy ]; then
      for _ in $(seq 1 "$TOR_WAIT"); do
        HEALTH=$(curl -q -LSsf "$BASE/api/v1/server/healthz")
        TOR_STATUS=$(echo "$HEALTH" | jq -r .features.tor.status)
        [ "$TOR_STATUS" = healthy ] && break
        sleep 1
      done
    fi
    if [ "$TOR_STATUS" = healthy ]; then
      [ "$(echo "$HEALTH" | jq -r .checks.tor)" = ok ] || fail "Tor check must be ok when healthy"
      echo "$HEALTH" | jq -r .features.tor.hostname | grep -Eq "^[a-z2-7]{56}\.onion$" || fail "Tor hostname format"
    elif [ "$REQUIRE_OVERLAY" = 1 ]; then
      fail "Tor bootstrap required but not healthy"
    else
      echo "SKIP: Tor did not bootstrap (status $TOR_STATUS)"
    fi
    [ "$(echo "$HEALTH" | jq -r .features.i2p.enabled)" = true ] || fail "container I2P default enabled"
    [ "$(echo "$HEALTH" | jq -r .features.i2p.provider)" = i2pd ] || fail "I2P provider"
    I2P_STATUS=$(echo "$HEALTH" | jq -r .features.i2p.status)
    case "$I2P_STATUS" in disabled|starting|healthy|unavailable) ;; *) fail "invalid I2P status $I2P_STATUS" ;; esac
    if [ "$I2P_STATUS" != healthy ]; then
      for _ in $(seq 1 "$I2P_WAIT"); do
        HEALTH=$(curl -q -LSsf "$BASE/api/v1/server/healthz")
        I2P_STATUS=$(echo "$HEALTH" | jq -r .features.i2p.status)
        [ "$I2P_STATUS" = healthy ] && break
        sleep 1
      done
    fi
    if [ "$I2P_STATUS" = healthy ]; then
      [ "$(echo "$HEALTH" | jq -r .checks.i2p)" = ok ] || fail "I2P check must be ok when healthy"
      echo "$HEALTH" | jq -r .features.i2p.hostname | grep -Eq "^[a-z2-7]{52}\.b32\.i2p$" || fail "I2P hostname format"
    elif [ "$REQUIRE_OVERLAY" = 1 ]; then
      fail "I2P bootstrap required but not healthy"
    else
      echo "SKIP: I2P did not bootstrap (status $I2P_STATUS)"
    fi
    if echo "$HEALTH" | grep -Eq "/(etc|var|home|config|data)/"; then fail "health response leaks a filesystem path"; fi
    [ "$FAILURES" -eq 0 ] || exit 1
    echo "Docker integration checks passed."
  '
