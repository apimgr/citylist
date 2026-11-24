#!/usr/bin/env bash
# @@License : WTFPL
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
go_test_args=("$@")
remote_url=$(git -C "$project_root" remote get-url origin 2>/dev/null || true)
remote_path=${remote_url#*://}
if [[ "$remote_url" == *:* && "$remote_url" != *://* ]]; then remote_path=${remote_url#*:}; fi
remote_path=${remote_path%.git}
project_name=$(basename "${remote_path:-$project_root}")
project_org=$(basename "$(dirname "${remote_path:-$project_root}")")
temp_root=${TMPDIR:-/tmp}/$project_org
mkdir -p "$temp_root"
e2e_dir=$(mktemp -d "$temp_root/${project_name}-XXXXXX")
chmod 700 "$e2e_dir"
suffix=${e2e_dir##*-}
suffix=${suffix,,}
compose_project="citylist-e2e-$suffix"
test_image="citylist:e2e-$suffix"
test_network="citylist-e2e-net-$suffix"
port=$((64000 + RANDOM % 1000))
volume_dir="$e2e_dir/volumes"
artifact_dir="$e2e_dir/artifacts"
compose_override="$e2e_dir/network.yml"
hosts_file="$e2e_dir/hosts"
mkdir -p "$volume_dir/config" "$volume_dir/data" "$artifact_dir"
mkdir -p "$volume_dir/config/citylist/yaml.d"
chmod 700 "$volume_dir"
# The service drops privileges after startup, so it must traverse the mounted roots.
chmod 711 "$volume_dir/config" "$volume_dir/data"
cat >"$volume_dir/config/citylist/yaml.d/security.yml" <<'EOF'
server:
  rate_limit:
    read: {requests: 1000, window: 60}
    write: {requests: 1000, window: 60}
    health: {requests: 1000, window: 60}
    strict: {requests: 1000, window: 60}
    auth_failures: {requests: 1000, window: 60}
    global_burst: 10000
EOF
cat >"$compose_override" <<'EOF'
networks:
  citylist:
    internal: true
EOF

compose() {
  CITYLIST_TEST_IMAGE="$test_image" CITYLIST_TEST_PORT="$port" CITYLIST_TEST_VOLUME_DIR="$volume_dir" CITYLIST_TEST_NETWORK="$test_network" \
    docker compose --project-name "$compose_project" --file "$project_root/docker/docker-compose.test.yml" --file "$compose_override" "$@"
}
random_suffix() {
  printf '%08x' "$(((RANDOM << 15) | RANDOM))"
}
cleanup() {
  status=$?
  if [[ "$status" -ne 0 ]]; then compose logs >"$artifact_dir/server.log" 2>&1 || true; fi
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker rm --force "citylist-e2e-browser-$suffix" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$volume_dir"
  if [[ "$status" -eq 0 ]]; then rm -rf -- "$e2e_dir"; else printf 'Failure artifacts retained at %s\n' "$artifact_dir" >&2; fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  printf '%s\n' 'Browser E2E testing requires Docker.' >&2
  exit 1
fi

docker build --tag "$test_image" --file "$project_root/docker/Dockerfile.dev" "$project_root"
compose up --detach
ready=false
for _ in {1..60}; do
  health_status=$(docker inspect --format '{{.State.Health.Status}}' citylist-app 2>/dev/null || true)
  if [[ "$health_status" == healthy ]]; then
    ready=true
    break
  fi
  sleep 2
done
if [[ "$ready" != true ]]; then
  compose logs
  printf '%s\n' 'Citylist did not become healthy in the allotted time.' >&2
  exit 1
fi
app_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' citylist-app)
cat >"$hosts_file" <<EOF
127.0.0.1 localhost
::1 localhost ip6-localhost ip6-loopback
$app_ip citylist
EOF
chmod 600 "$hosts_file"

docker run --detach --name "citylist-e2e-browser-$suffix" --network "$test_network" --memory=2g --cpus=2 \
  --add-host="citylist:$app_ip" \
  chromedp/headless-shell:latest
browser_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "citylist-e2e-browser-$suffix")
browser_ready=false
for _ in {1..30}; do
  curl_container="citylist-$(random_suffix)"
  if docker run --rm --name "$curl_container" --network "$test_network" --memory=128m --cpus=0.5 \
    curlimages/curl:latest --fail --silent "http://$browser_ip:9222/json/version" >/dev/null 2>&1; then
    browser_ready=true
    break
  fi
  sleep 1
done
if [[ "$browser_ready" != true ]]; then
  docker logs "citylist-e2e-browser-$suffix" >&2 || true
  printf '%s\n' 'Headless Chromium did not become ready.' >&2
  exit 1
fi

test_container="citylist-$(random_suffix)"
bash "$HOME/.codex/hooks/test-lint-run.sh" test -- docker run --rm --name "$test_container" \
  --network "$test_network" --memory=4g --cpus=2 --user 0:0 \
  --mount "type=bind,source=$hosts_file,target=/etc/hosts,readonly" \
  -v "$project_root:/app" -v "$artifact_dir:/artifacts" \
  -v "$HOME/go/pkg/mod:/usr/local/share/go/pkg/mod" \
  -v "$HOME/.cache/go-build/citylist:/usr/local/share/go/cache" \
  -w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  -e CITYLIST_BASE_URL="http://citylist" \
  -e CHROME_REMOTE_DEBUGGING_URL="ws://$browser_ip:9222" -e E2E_ARTIFACT_DIR=/artifacts \
  casjaysdev/go:latest go test -tags=e2e -v -timeout 5m "${go_test_args[@]}" ./tests/e2e/...
printf '%s\n' 'Browser E2E checks passed.'
