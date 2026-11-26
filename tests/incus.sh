#!/usr/bin/env bash
# @@License : WTFPL
set -eo pipefail

# Check if incus is available
if ! command -v incus &>/dev/null; then
    echo "ERROR: incus not found. Install incus or use tests/docker.sh"
    exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)
cd "$PROJECT_ROOT"

# Usage: tests/incus.sh [systemd|openrc|all]   (default: all)
# systemd runs in a Debian container, openrc in an Alpine container; each gets the full test run
MODE="${1:-all}"
case "$MODE" in
    systemd|openrc) MODES="$MODE" ;;
    all) MODES="systemd openrc" ;;
    *) echo "Usage: $0 [systemd|openrc|all]"; exit 2 ;;
esac

# Detect project info from the remote, with the checkout path as fallback.
REMOTE_URL=$(git -C "$PROJECT_ROOT" remote get-url origin 2>/dev/null || true)
REMOTE_PATH=${REMOTE_URL#*://}
if [[ "$REMOTE_URL" == *:* && "$REMOTE_URL" != *://* ]]; then REMOTE_PATH=${REMOTE_URL#*:}; fi
REMOTE_PATH=${REMOTE_PATH%.git}
PROJECT_NAME=$(basename "${REMOTE_PATH:-$PROJECT_ROOT}")
PROJECT_ORG=$(basename "$(dirname "${REMOTE_PATH:-$PROJECT_ROOT}")")
INTERNAL_NAME="${INTERNAL_NAME:-$PROJECT_NAME}"
INTERNAL_ORG="${INTERNAL_ORG:-$PROJECT_ORG}"
CONTAINER_SUFFIX=$(od -An -N4 -tx4 /dev/urandom | tr -d ' \n')
CONTAINER_PREFIX="${PROJECT_NAME}-${CONTAINER_SUFFIX}"
CONTAINERS=()
BUILD_DIR=""
BIN_DIR="${CITYLIST_BIN_DIR:-}"

# Incus images, one per init system: systemd uses the latest Debian stable, OpenRC uses the
# latest stable Alpine release (update both when new stable releases ship)
INCUS_IMAGE_SYSTEMD="images:debian/trixie"
INCUS_IMAGE_OPENRC="images:alpine/3.24"

cleanup() {
    local status=$?
    trap - EXIT INT TERM
    local c
    for c in "${CONTAINERS[@]}"; do incus delete "$c" --force 2>/dev/null || true; done
    if [[ -n "$BUILD_DIR" ]]; then rm -rf -- "$BUILD_DIR"; fi
    exit "$status"
}
trap cleanup EXIT INT TERM

if [[ -z "$BIN_DIR" ]]; then
    TEMP_ROOT="${TMPDIR:-/tmp}/${PROJECT_ORG}"
    mkdir -p "$TEMP_ROOT"
    BUILD_DIR=$(mktemp -d "$TEMP_ROOT/${INTERNAL_NAME}-XXXXXX")
    BIN_DIR="$BUILD_DIR"
    echo "Building server and client into project-scoped temp storage..."
    make build BINDIR="$BIN_DIR"
fi
for required_binary in "$BIN_DIR/$PROJECT_NAME" "$BIN_DIR/$PROJECT_NAME-cli"; do
    if [[ ! -x "$required_binary" ]]; then
        echo "ERROR: required test binary is missing or not executable: $required_binary" >&2
        exit 1
    fi
done

REMOTE_LIB=$(cat <<'REMOTE_LIB_EOF'
INTERNAL_NAME="${INTERNAL_NAME:-$PROJECT_NAME}"
INTERNAL_ORG="${INTERNAL_ORG:-$PROJECT_ORG}"
FAILURES=0
fail() { echo "✗ FAILED: $*"; FAILURES=$((FAILURES + 1)); }
pass() { echo "✓ $*"; }
check() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$d"; else fail "$d"; fi; }
check_not() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then fail "$d"; else pass "$d"; fi; }
wait_for() { local n="$1" i; shift; for i in $(seq 1 "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

# A native service listens on the port the server saved in server.yml (a random 64xxx port; 80 is only the container default), so read it there
CONFIG_FILE="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}/server.yml"
port() { sed -n -e 's/^  port: *"\{0,1\}\([0-9]\{1,5\}\).*/\1/p' -e 's/^    port: *"\{0,1\}\([0-9]\{1,5\}\).*/\1/p' "$CONFIG_FILE" 2>/dev/null | head -1; }
api() { echo "http://localhost:$(port)/api/v1/server/healthz"; }
healthz() { curl -q -LSsf --max-time 10 "$(api)"; }
healthz_up() { healthz | jq -e '.status' >/dev/null; }
overlay() { healthz | jq -r ".features.$1.$2"; }
overlay_healthy() { [ "$(overlay "$1" status)" = "healthy" ]; }

# One set of service helpers per init system; the test steps are identical for both
if [ "$INIT_MODE" = "systemd" ]; then
    UNIT_FILE="/etc/systemd/system/${INTERNAL_NAME}.service"
    svc_active()  { systemctl is-active --quiet "$INTERNAL_NAME"; }
    svc_enabled() { systemctl is-enabled --quiet "$INTERNAL_NAME"; }
    svc_pid()     { systemctl show -p MainPID --value "$INTERNAL_NAME"; }
else
    UNIT_FILE="/etc/init.d/${INTERNAL_NAME}"
    svc_active()  { rc-service "$INTERNAL_NAME" status 2>&1 | grep -q started; }
    svc_enabled() { rc-update show default | grep -qw "$INTERNAL_NAME"; }
    svc_pid()     { cat "/var/run/${INTERNAL_ORG}/${INTERNAL_NAME}/${INTERNAL_NAME}.pid"; }
fi
svc_stopped() { ! svc_active; }
REMOTE_LIB_EOF
)

REMOTE_MAIN=$(cat <<'REMOTE_MAIN_EOF'
echo "=== [$INIT_MODE] Init system identity ==="
if [ "$INIT_MODE" = "systemd" ]; then
    check "PID 1 is systemd" test "$(cat /proc/1/comm)" = "systemd"
else
    check "OpenRC is running" test -d /run/openrc
    check "no systemd in this container" test ! -d /run/systemd/system
fi
CONF_ROOT="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}"
DATA_ROOT="/var/lib/${INTERNAL_ORG}/${INTERNAL_NAME}"

echo "=== Version Check ==="
${PROJECT_NAME} --version

echo "=== Help Check ==="
${PROJECT_NAME} --help

echo "=== Binary Info ==="
ls -lh /usr/local/bin/${PROJECT_NAME}
file /usr/local/bin/${PROJECT_NAME}

echo "=== [$INIT_MODE] Service install (--service --install also enables and starts) ==="
${PROJECT_NAME} --service --install
check "service file installed at ${UNIT_FILE}" test -f "$UNIT_FILE"
if [ "$INIT_MODE" = "openrc" ]; then
    check "init script starts with #!/sbin/openrc-run" sh -c "head -1 '$UNIT_FILE' | grep -q '^#!/sbin/openrc-run'"
    check "init script is executable" test -x "$UNIT_FILE"
    check_not "init script is not a SysV script" grep -q 'BEGIN INIT INFO' "$UNIT_FILE"
fi
check "service enabled at boot" svc_enabled
check "service active after install" wait_for 30 svc_active
check "health endpoint answers after install" wait_for 60 healthz_up
check "service account exists" getent passwd "$INTERNAL_NAME"
SERVICE_PID=$(svc_pid)
check "server runs as the service account after the privilege drop" bash -c 'test "$1" -gt 1 && test "$(stat -c %U "/proc/$1")" = "$2"' _ "$SERVICE_PID" "$INTERNAL_NAME"

mkdir -p "${CONF_ROOT}/yaml.d"
printf 'server:\n  rate_limit:\n    read:\n      requests: 1000\n    write:\n      requests: 1000\n    health:\n      requests: 1000\n    global_burst: 2000\n' > "${CONF_ROOT}/yaml.d/zz-test-rate-limit.yml"
chown "${INTERNAL_NAME}:${INTERNAL_NAME}" "${CONF_ROOT}/yaml.d" "${CONF_ROOT}/yaml.d/zz-test-rate-limit.yml"
chmod 0700 "${CONF_ROOT}/yaml.d"
chmod 0600 "${CONF_ROOT}/yaml.d/zz-test-rate-limit.yml"

echo "=== [$INIT_MODE] Service restart ==="
OLD_PID=$(svc_pid)
${PROJECT_NAME} --service restart
check "service active after restart" wait_for 30 svc_active
check "health endpoint answers after restart" wait_for 60 healthz_up
check "restart replaced the process" test "$(svc_pid)" != "$OLD_PID"

echo "=== [$INIT_MODE] Service stop and start ==="
${PROJECT_NAME} --service stop
check "service stopped" wait_for 30 svc_stopped
check_not "health endpoint is down while stopped" curl -q -LSsf --max-time 3 "$(api)"
${PROJECT_NAME} --service start
check "service active after start" wait_for 30 svc_active
check "health endpoint answers after start" wait_for 60 healthz_up

echo "=== API Endpoint Tests ==="
# JSON response (default)
curl -q -LSsf http://localhost:$(port)/api/v1/server/healthz || fail "/api/v1/server/healthz"
# .txt extension (plain text)
curl -q -LSsf http://localhost:$(port)/api/v1/server/healthz.txt || fail "/api/v1/server/healthz.txt"
# Accept: application/json
curl -q -LSsf -H 'Accept: application/json' http://localhost:$(port)/server/healthz || fail "Accept JSON"
# Accept: text/plain
curl -q -LSsf -H 'Accept: text/plain' http://localhost:$(port)/server/healthz || fail "Accept text/plain"

echo "=== Project-Specific Endpoint Tests ==="
for path in / /search /coordinates /api/v1/ /api/v1/cities?limit=1 \
  /server/about /server/help /server/help/mcp /server/contact \
  /server/security /server/security/policy /server/security/thanks \
  /server/privacy /server/dpo /server/terms /server/healthz \
  /api/v1/cities/search?q=London /api/v1/cities/2643743 \
  /api/v1/cities/country/GB /api/v1/cities/coordinates?latitude=51.5072\&longitude=-0.1276 \
  /api/v1/cities/nearby?lat=51.5072\&lon=-0.1276\&radius=25 \
  /api/v1/zipcodes/10001 /api/v1/zipcodes/search?q=New%20York \
  /api/v1/zipcodes/state/NY \
  /api/v1/zipcodes/nearby?lat=40.7128\&lon=-74.0060\&radius_km=25 \
  /api/v1/random /api/v1/random.txt /api/v1/stats /api/v1/stats.txt \
  /api/v1/count /api/v1/count.txt /api/v1/server/about \
  /api/v1/server/help /api/v1/server/help/mcp /api/v1/server/security \
  /api/v1/server/privacy /api/v1/server/dpo /api/v1/server/terms \
  /api/v1/server/security/policy /api/v1/server/security/thanks \
  /api/v1/server/contact /api/v1/server/preferences \
  /api/v1/server/preferences/sync /api/v1/server/preferences/sync/import \
  /api/v1/server/healthz /api/v1/server/swagger \
  /api/healthz /api/swagger \
  /server/preferences /server/preferences/tokens /server/preferences/sync \
  /server/preferences/sync/import \
  /server/docs/swagger /server/docs/graphql /locales/en.json /static/css/common.css \
  /manifest.json /sw.js /robots.txt \
  /.well-known/security.txt /.well-known/llms.txt /llms.txt; do
    if curl -q -LSsf --max-time 15 "http://localhost:$(port)$path" >/dev/null 2>&1; then
        pass "GET $path"
    else
        fail "GET $path"
    fi
done
for route in /api/graphql /api/v1/server/graphql; do
    if curl -q -LSsf --max-time 15 -X POST -H 'Content-Type: application/json' \
        -d '{"query":"{ cities(limit: 1) { id name } }"}' \
        "http://localhost:$(port)$route" >/dev/null 2>&1; then
        pass "POST $route executes a read-only GraphQL query"
    else
        fail "POST $route executes a read-only GraphQL query"
    fi
    if curl -q -LSsf --max-time 15 \
        "http://localhost:$(port)$route?query=%7B%20cities%28limit%3A1%29%20%7Bid%20name%7D%20%7D" \
        >/dev/null 2>&1; then
        pass "GET $route executes a read-only GraphQL query"
    else
        fail "GET $route executes a read-only GraphQL query"
    fi
done
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    "http://localhost:$(port)/server/security/report/not-a-valid-tracking-id")" = 404 ]; then
    pass "GET /server/security/report/{trackingID} hides an invalid tracking link"
else
    fail "GET /server/security/report/{trackingID} does not hide an invalid tracking link"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' -I \
    "http://localhost:$(port)/server/preferences")" = 200 ]; then
    pass "HEAD /server/preferences"
else
    fail "HEAD /server/preferences"
fi
for route in /server/contact /server/preferences /server/preferences/sync \
  /server/preferences/token/create /server/preferences/token/restore \
  /server/preferences/token/forget /server/preferences/token/reveal \
  /server/preferences/token/rotate /server/preferences/tokens \
  /server/preferences/tokens/nonexistent/revoke /server/consent /server/ccpa; do
    status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X POST \
      "http://localhost:$(port)$route")
    if [ "$status" = 403 ] || [ "$status" = 400 ] || [ "$status" = 404 ]; then
        pass "POST $route reaches its CSRF, validation, or feature gate"
    else
        fail "POST $route returned unexpected HTTP $status without CSRF"
    fi
done
for route in /api/mcp /mcp /api/v1/server/mcp; do
    status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' -d '{}' \
      "http://localhost:$(port)$route")
    if [ "$status" = 401 ] || [ "$status" = 403 ]; then
        pass "POST $route rejects missing bearer credentials"
    else
        fail "POST $route returned unexpected HTTP $status without credentials"
    fi
done
for route in /api/v1/server/preferences/tokens/primary/reveal \
  /api/v1/server/preferences/tokens/primary/rotate \
  /api/v1/server/preferences/tokens/nonexistent/reveal \
  /api/v1/server/preferences/tokens/nonexistent/rotate \
  /api/v1/server/preferences/tokens/nonexistent/update \
  /api/v1/server/preferences/tokens/nonexistent/revoke; do
    status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X POST \
      "http://localhost:$(port)$route")
    if [ "$status" = 401 ] || [ "$status" = 403 ]; then
        pass "POST $route rejects missing owner credentials"
    else
        fail "POST $route returned unexpected HTTP $status without credentials"
    fi
done
status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X POST \
  "http://localhost:$(port)/api/v1/server/preferences/tokens")
if [ "$status" = 401 ] || [ "$status" = 403 ]; then
    pass "POST /api/v1/server/preferences/tokens rejects missing owner credentials"
else
    fail "POST /api/v1/server/preferences/tokens returned unexpected HTTP $status without credentials"
fi
for method in PATCH DELETE; do
    status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X "$method" \
      "http://localhost:$(port)/api/v1/server/preferences/tokens/nonexistent")
    if [ "$status" = 401 ] || [ "$status" = 403 ]; then
        pass "$method /api/v1/server/preferences/tokens/{id} rejects missing owner credentials"
    else
        fail "$method /api/v1/server/preferences/tokens/{id} returned unexpected HTTP $status without credentials"
    fi
done
for route in /api/v1/server/contact /api/v1/server/preferences; do
    status=$(curl -q -LSs -o /dev/null -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' -d '{}' \
      "http://localhost:$(port)$route")
    if [ "$status" != 404 ] && [ "$status" != 405 ]; then
        pass "POST $route reaches its handler (HTTP $status)"
    else
        fail "POST $route is not registered (HTTP $status)"
    fi
done
if curl -q -LSsf -X POST -H 'Content-Type: application/json' \
    -d '{"latitude":51.5072,"longitude":-0.1276}' \
    "http://localhost:$(port)/api/v1/cities/coordinates" >/dev/null 2>&1; then
    pass "POST /api/v1/cities/coordinates"
else
    fail "POST /api/v1/cities/coordinates"
fi
if curl -q -LSsf -H 'Accept: text/plain' \
    "http://localhost:$(port)/api/v1/cities/search?q=London" >/dev/null 2>&1; then
    pass "GET /api/v1/cities/search with Accept: text/plain"
else
    fail "GET /api/v1/cities/search with Accept: text/plain"
fi
if curl -q -LSsf -H 'Accept: text/html' \
    "http://localhost:$(port)/search" | grep -qi '<html'; then
    pass "GET /search with Accept: text/html returns HTML"
else
    fail "GET /search with Accept: text/html returns HTML"
fi
if curl -q -LSsf -A 'curl/8.0' -H 'Accept: text/plain' \
    "http://localhost:$(port)/api/v1/cities/search?q=London" \
    | grep -qi 'London'; then
    pass "HTTP-tool search returns formatted text"
else
    fail "HTTP-tool search returns formatted text"
fi
if curl -q -LSsf -H 'Accept: application/json' \
    "http://localhost:$(port)/server/healthz" | jq -e '.status' >/dev/null 2>&1; then
    pass "GET /server/healthz with Accept: application/json"
else
    fail "GET /server/healthz with Accept: application/json"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' -d '{}' \
    "http://localhost:$(port)/api/graphql")" = 400 ]; then
    pass "POST /api/graphql rejects a missing query"
else
    fail "POST /api/graphql rejects a missing query"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -d '{}' \
    "http://localhost:$(port)/api/v1/server/mcp")" = 401 ]; then
    pass "POST /api/v1/server/mcp rejects missing credentials"
else
    fail "POST /api/v1/server/mcp rejects missing credentials"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    "http://localhost:$(port)/api/v1/server/preferences/tokens")" = 401 ]; then
    pass "GET /api/v1/server/preferences/tokens rejects missing owner credentials"
else
    fail "GET /api/v1/server/preferences/tokens rejects missing owner credentials"
fi
OWNER_TOKEN=$(curl -q -LSsf -X POST -H 'Content-Type: application/json' -d '{}' \
    "http://localhost:$(port)/api/v1/server/preferences/tokens/primary" \
    | jq -r '.data.owner_token // empty')
if [ -n "$OWNER_TOKEN" ]; then
    pass "Primary owner token can be issued"
    if curl -q -LSsf -H "Authorization: Bearer $OWNER_TOKEN" \
        "http://localhost:$(port)/api/v1/server/preferences/tokens" \
        | jq -e '.ok == true' >/dev/null 2>&1; then
        pass "Owner bearer token is accepted for token management"
    else
        fail "Owner bearer token is accepted for token management"
    fi
else
    fail "Primary owner token can be issued"
fi

echo "=== Open API Smoke Test ==="
# No token needed for the public endpoints; MCP, metrics and token routes are checked separately (a missing token answers 401)
if curl -q -LSsf http://localhost:$(port)/api/v1/server/healthz | jq -e '.status' >/dev/null; then pass "Health endpoint works"; else fail "Health endpoint"; fi
PGP_STATUS=$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    "http://localhost:$(port)/.well-known/pgp-key.asc")
case "$PGP_STATUS" in
    200|404) pass "Optional OpenPGP route responds with 200 or 404" ;;
    *) fail "Optional OpenPGP route returned $PGP_STATUS" ;;
esac
if [ -f "/etc/${INTERNAL_ORG}/${INTERNAL_NAME}/secrets.yml" ]; then
    API_TOKEN=$(sed -n 's/^[[:space:]]*token:[[:space:]]*\([^# ]*\).*/\1/p' \
      "/etc/${INTERNAL_ORG}/${INTERNAL_NAME}/secrets.yml" | head -1)
fi
if [ -n "${API_TOKEN:-}" ]; then
    if curl -q -LSsf -H "Authorization: Bearer $API_TOKEN" \
        "http://localhost:$(port)/api/v1/server/auth/check" | jq -e '.ok == true' >/dev/null 2>&1; then
        pass "Operator bearer token is accepted"
    else
        fail "Operator bearer token is accepted"
    fi
else
    fail "Operator token is present in secrets.yml"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    "http://localhost:$(port)/api/v1/server/auth/check")" = 401 ]; then
    pass "Missing operator bearer token is rejected"
else
    fail "Missing operator bearer token is rejected"
fi
if [ "$(curl -q -LSs -o /dev/null -w '%{http_code}' \
    -H 'Authorization: Bearer invalid-citylist-token' \
    "http://localhost:$(port)/api/v1/server/auth/check")" = 401 ]; then
    pass "Invalid operator bearer token is rejected"
else
    fail "Invalid operator bearer token is rejected"
fi

echo "=== Binary Rename Tests ==="
# Binaries show the ACTUAL name in --help/--version (not hardcoded)
cp /usr/local/bin/${PROJECT_NAME} /tmp/renamed-server
chmod +x /tmp/renamed-server
if /tmp/renamed-server --help 2>&1 | grep -q 'renamed-server'; then pass "Server binary rename works (--help shows actual name)"; else fail "Server --help does not show renamed binary name"; fi

echo "=== Client Tests (if exists) ==="
if [ -f /usr/local/bin/${PROJECT_NAME}-cli ]; then
    ${PROJECT_NAME}-cli --version || fail "CLI --version"
    ${PROJECT_NAME}-cli --help || fail "CLI --help"

    cp /usr/local/bin/${PROJECT_NAME}-cli /tmp/renamed-cli
    chmod +x /tmp/renamed-cli
    if /tmp/renamed-cli --help 2>&1 | grep -q 'renamed-cli'; then pass "CLI binary rename works"; else fail "CLI --help does not show renamed binary name"; fi

    echo '--- CLI Full Functionality Tests ---'
    if [ -n "${API_TOKEN:-}" ]; then
        ${PROJECT_NAME}-cli --server http://localhost:$(port) --token "$API_TOKEN" search London || fail "CLI city search"
        ${PROJECT_NAME}-cli --server http://localhost:$(port) --token "$API_TOKEN" zipcode 10001 || fail "CLI ZIP lookup"
    else
        ${PROJECT_NAME}-cli --server http://localhost:$(port) search London || fail "CLI city search without token"
        ${PROJECT_NAME}-cli --server http://localhost:$(port) zipcode 10001 || fail "CLI ZIP lookup without token"
    fi
else
    echo 'client not installed - skipping'
fi

echo "=== Agent Tests (if exists) ==="

echo "=== [$INIT_MODE] Tor hidden service (tor is installed in this container) ==="
DATA_ROOT="/var/lib/${INTERNAL_ORG}/${INTERNAL_NAME}"
CONF_ROOT="/etc/${INTERNAL_ORG}/${INTERNAL_NAME}"
check "tor binary is installed" command -v tor
check "features.tor.enabled is true" test "$(overlay tor enabled)" = "true"
case "$(overlay tor status)" in
    disabled|starting|healthy|unavailable) pass "features.tor.status is one of the four contract words" ;;
    *) fail "features.tor.status is not one of the four contract words" ;;
esac
# Bootstrap needs outbound network. REQUIRE_OVERLAY=1 makes a missing network a failure instead of a SKIP.
wait_for "${TOR_WAIT:-300}" overlay_healthy tor || true
if [ "$(overlay tor status)" = "healthy" ]; then
    if [[ "$(overlay tor hostname)" =~ ^[a-z2-7]{56}\.onion$ ]]; then pass "tor hostname is a v3 onion address"; else fail "tor hostname is not a v3 onion address"; fi
    check "features.tor.running is true" test "$(overlay tor running)" = "true"
    check "checks.tor is ok" test "$(healthz | jq -r '.checks.tor')" = "ok"
    check "onion hostname file persisted" test -s "${DATA_ROOT}/tor/site/hostname"
    for d in "${CONF_ROOT}/tor" "${DATA_ROOT}/tor" "${DATA_ROOT}/tor/site"; do
        check "$d is mode 700" test "$(stat -c %a "$d")" = "700"
    done
elif [ "${REQUIRE_OVERLAY:-0}" = "1" ]; then
    fail "Tor did not become healthy within ${TOR_WAIT:-300}s"
else
    echo "SKIP: Tor did not bootstrap within ${TOR_WAIT:-300}s (no outbound network?). The contract is still checked."
    case "$(overlay tor status)" in
        starting) check "starting maps to checks.tor ok" test "$(healthz | jq -r '.checks.tor')" = "ok" ;;
        unavailable) check "unavailable maps to checks.tor degraded" test "$(healthz | jq -r '.checks.tor')" = "degraded" ;;
        *) fail "Tor is neither starting nor unavailable while not healthy" ;;
    esac
fi
if healthz | grep -Eq '/(etc|var|home|config|data)/'; then fail "health response leaks a filesystem path"; else pass "health response carries no filesystem path"; fi

echo "=== [$INIT_MODE] Tor missing binary (must be disabled, never an error) ==="
TOR_HIDDEN=()
for TOR_BIN in /usr/bin/tor /usr/local/bin/tor /opt/homebrew/bin/tor "$(command -v tor)"; do
    if [ -n "$TOR_BIN" ] && [ -x "$TOR_BIN" ] && [ ! -e "${TOR_BIN}.hidden" ]; then
        mv "$TOR_BIN" "${TOR_BIN}.hidden"
        TOR_HIDDEN+=("$TOR_BIN")
    fi
done
${PROJECT_NAME} --service restart
wait_for 60 healthz_up
check "enabled is false without the binary" test "$(overlay tor enabled)" = "false"
check "running is false" test "$(overlay tor running)" = "false"
check "status is disabled" test "$(overlay tor status)" = "disabled"
check "hostname is empty" test -z "$(overlay tor hostname)"
check "checks.tor is omitted" test "$(healthz | jq '(.checks | has("tor"))')" = "false"
for TOR_BIN in "${TOR_HIDDEN[@]}"; do mv "${TOR_BIN}.hidden" "$TOR_BIN"; done
${PROJECT_NAME} --service restart
wait_for 60 healthz_up

echo "=== [$INIT_MODE] I2P (default OFF in a system container) ==="
check "i2pd binary is installed" command -v i2pd
check "server.yml is 0600 after start" test "$(stat -c %a "${CONF_ROOT}/server.yml")" = "600"
check "features.i2p.enabled is false by default in a system container" test "$(overlay i2p enabled)" = "false"
check "features.i2p.status is disabled by default" test "$(overlay i2p status)" = "disabled"
check "checks.i2p is omitted by default" test "$(healthz | jq '(.checks | has("i2p"))')" = "false"
mkdir -p "${CONF_ROOT}/yaml.d"
printf 'server:\n  i2p:\n    enabled: true\n' > "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chown "${INTERNAL_NAME}:${INTERNAL_NAME}" "${CONF_ROOT}/yaml.d" "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chmod 0700 "${CONF_ROOT}/yaml.d"
chmod 0600 "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
${PROJECT_NAME} --service restart
wait_for 60 healthz_up
check "features.i2p.enabled is true after explicit opt-in" test "$(overlay i2p enabled)" = "true"
check "features.i2p.provider is i2pd" test "$(overlay i2p provider)" = "i2pd"
case "$(overlay i2p status)" in
    starting|healthy|unavailable) pass "features.i2p.status is a contract word for an enabled network" ;;
    *) fail "features.i2p.status is not valid for an enabled network" ;;
esac
wait_for "${I2P_WAIT:-600}" overlay_healthy i2p || true
if [ "$(overlay i2p status)" = "healthy" ]; then
    if [[ "$(overlay i2p hostname)" =~ ^[a-z2-7]{52}\.b32\.i2p$ ]]; then pass "i2p hostname is a .b32.i2p address"; else fail "i2p hostname is not a .b32.i2p address"; fi
    check "features.i2p.running is true" test "$(overlay i2p running)" = "true"
    check "checks.i2p is ok" test "$(healthz | jq -r '.checks.i2p')" = "ok"
    check "destination key persisted" sh -c "find '${DATA_ROOT}/i2p/site' -type f -size +0 | grep -q ."
elif [ "${REQUIRE_OVERLAY:-0}" = "1" ]; then
    fail "I2P did not become healthy within ${I2P_WAIT:-600}s"
else
    echo "SKIP: I2P did not build tunnels within ${I2P_WAIT:-600}s (no outbound network?). The contract is still checked."
fi
if healthz | grep -Eq '/(etc|var|home|config|data)/'; then fail "health response leaks a filesystem path"; else pass "health response carries no filesystem path"; fi

echo "=== [$INIT_MODE] I2P opt-out (an explicit config value disables it) ==="
printf 'server:\n  i2p:\n    enabled: false\n' > "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chown "${INTERNAL_NAME}:${INTERNAL_NAME}" "${CONF_ROOT}/yaml.d" "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chmod 0700 "${CONF_ROOT}/yaml.d"
chmod 0600 "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
${PROJECT_NAME} --service restart
wait_for 60 healthz_up
check "features.i2p.enabled is false after opt-out" test "$(overlay i2p enabled)" = "false"
check "status is disabled after opt-out" test "$(overlay i2p status)" = "disabled"
check "checks.i2p is omitted after opt-out" test "$(healthz | jq '(.checks | has("i2p"))')" = "false"
rm -f "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
${PROJECT_NAME} --service restart
wait_for 60 healthz_up

echo "=== [$INIT_MODE] I2P missing provider (default on, no i2pd: disabled, never an error) ==="
printf 'server:\n  i2p:\n    enabled: true\n' > "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chown "${INTERNAL_NAME}:${INTERNAL_NAME}" "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
chmod 0600 "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
I2PD_BIN=$(command -v i2pd)
mv "$I2PD_BIN" "${I2PD_BIN}.hidden"
${PROJECT_NAME} --service restart
wait_for 60 healthz_up
check "enabled is false without a provider" test "$(overlay i2p enabled)" = "false"
check "status is disabled" test "$(overlay i2p status)" = "disabled"
check "provider is none" test "$(overlay i2p provider)" = "none"
check "checks.i2p is omitted" test "$(healthz | jq '(.checks | has("i2p"))')" = "false"
mv "${I2PD_BIN}.hidden" "$I2PD_BIN"
rm -f "${CONF_ROOT}/yaml.d/zz-50-i2p.yml"
${PROJECT_NAME} --service restart
wait_for 60 healthz_up

[ "$FAILURES" -eq 0 ] || { echo "$FAILURES check(s) failed"; exit 1; }
echo "=== [$INIT_MODE] Main tests passed ==="
REMOTE_MAIN_EOF
)

REMOTE_AFTER=$(cat <<'REMOTE_AFTER_EOF'
echo "=== [$INIT_MODE] Boot persistence (container was restarted) ==="
check "service still enabled after reboot" svc_enabled
check "service came back after reboot" wait_for 120 svc_active
check "health endpoint answers after reboot" wait_for 120 healthz_up

echo "=== [$INIT_MODE] Service disable (keeps the service file) ==="
${PROJECT_NAME} --service --disable
check_not "service not enabled after --disable" svc_enabled
check "service not active after --disable" wait_for 30 svc_stopped
check "service file kept after --disable" test -f "$UNIT_FILE"

echo "=== [$INIT_MODE] Service re-install ==="
${PROJECT_NAME} --service --install
check "service enabled again" svc_enabled
check "service active again" wait_for 30 svc_active
check "health endpoint answers again" wait_for 60 healthz_up

echo "=== [$INIT_MODE] Service uninstall (confirmation prompt answered with y) ==="
printf 'y\n' | ${PROJECT_NAME} --service --uninstall
check_not "service file removed" test -e "$UNIT_FILE"
check_not "service not enabled" svc_enabled
check "service not active" wait_for 30 svc_stopped
check_not "data directory removed" test -d "/var/lib/${INTERNAL_ORG}/${INTERNAL_NAME}"

[ "$FAILURES" -eq 0 ] || { echo "$FAILURES check(s) failed"; exit 1; }
echo "=== [$INIT_MODE] All tests passed ==="
REMOTE_AFTER_EOF
)

# Run a script inside the container with the init mode and project names
remote() {
    local container="$1" init="$2" script="$3"
    printf '%s\n' "$script" | incus exec "$container" -- env INIT_MODE="$init" PROJECT_NAME="$PROJECT_NAME" PROJECT_ORG="$PROJECT_ORG" \
        INTERNAL_NAME="$INTERNAL_NAME" INTERNAL_ORG="$INTERNAL_ORG" REQUIRE_OVERLAY="${REQUIRE_OVERLAY:-0}" \
        TOR_WAIT="${TOR_WAIT:-300}" I2P_WAIT="${I2P_WAIT:-600}" bash -s
}

# Wait until the container accepts commands, its init system finished booting and the network is up
wait_boot() {
    local container="$1" init="$2" i state
    for i in $(seq 1 60); do incus exec "$container" -- true 2>/dev/null && break; sleep 1; done
    if [ "$init" = "systemd" ]; then
        # "degraded" is normal in a container; only "starting" means boot is unfinished
        for i in $(seq 1 90); do
            state=$(incus exec "$container" -- systemctl is-system-running 2>/dev/null || true)
            [ -n "$state" ] && [ "$state" != "starting" ] && break
            sleep 1
        done
    else
        for i in $(seq 1 60); do incus exec "$container" -- test -d /run/openrc 2>/dev/null && break; sleep 1; done
    fi
    for ((i = 0; i < 60; i++)); do incus exec "$container" -- sh -c 'ip route | grep -q "^default"' 2>/dev/null && break; sleep 1; done
}

run_mode() {
    local init="$1" image container
    if [ "$init" = "systemd" ]; then image="$INCUS_IMAGE_SYSTEMD"; else image="$INCUS_IMAGE_OPENRC"; fi
    container="$CONTAINER_PREFIX"
    if [ "$init" = "openrc" ]; then container="${CONTAINER_PREFIX}-openrc"; fi

    echo "=== [$init] Launching Incus container ($image) ==="
    if incus info "$container" >/dev/null 2>&1; then
        echo "ERROR: generated Incus test name already exists: $container" >&2
        return 1
    fi
    incus launch "$image" "$container" -c limits.cpu=2 -c limits.memory=4GiB --config security.privileged=true
    CONTAINERS+=("$container")
    wait_boot "$container" "$init"

    echo "=== [$init] Installing test tools, tor and i2pd ==="
    if [ "$init" = "systemd" ]; then
        incus exec "$container" -- bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq file tor i2pd' >/dev/null
        # The distro tor/i2pd services are NOT what is tested: the server runs its own dedicated, isolated instances (PART 31)
        incus exec "$container" -- systemctl disable --now tor.service tor@default.service i2pd.service 2>/dev/null || true
    else
        incus exec "$container" -- sh -c 'apk add --no-cache bash curl jq file tor i2pd tzdata ca-certificates' >/dev/null
        incus exec "$container" -- sh -c 'rc-service tor stop; rc-service i2pd stop; rc-update del tor; rc-update del i2pd' 2>/dev/null || true
    fi

    echo "=== [$init] Copying binaries to container ==="
    incus file push "${BIN_DIR}/${PROJECT_NAME}" "$container/usr/local/bin/"
    incus exec "$container" -- chmod +x "/usr/local/bin/${PROJECT_NAME}"
    if [ -f "${BIN_DIR}/${PROJECT_NAME}-cli" ]; then
        incus file push "${BIN_DIR}/${PROJECT_NAME}-cli" "$container/usr/local/bin/"
        incus exec "$container" -- chmod +x "/usr/local/bin/${PROJECT_NAME}-cli"
    fi

    echo "=== [$init] Running tests in Incus ==="
    remote "$container" "$init" "${REMOTE_LIB}"$'\n'"${REMOTE_MAIN}"

    echo "=== [$init] Restarting the container (boot persistence) ==="
    incus restart "$container"
    wait_boot "$container" "$init"
    remote "$container" "$init" "${REMOTE_LIB}"$'\n'"${REMOTE_AFTER}"
}

# One container per init system, run one after the other (never in parallel)
run_mode_list() {
    local init
    for init in $MODES; do
        run_mode "$init"
    done
}
run_mode_list

echo "Incus tests completed successfully ($MODES)"
