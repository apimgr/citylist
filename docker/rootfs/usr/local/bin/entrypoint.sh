#!/usr/bin/env bash
set -eu

APP_NAME="citylist"
APP_BIN="/usr/local/bin/${APP_NAME}"

export TZ="${TZ:-America/New_York}"
export CONFIG_DIR="${CONFIG_DIR:-/config/${APP_NAME}}"
export DATA_DIR="${DATA_DIR:-/data/${APP_NAME}}"

APP_ARGS=(--address "${LISTEN:-[::]}" --port "${PORT:-80}")
if [ "${DEBUG:-false}" = "true" ]; then
    APP_ARGS+=(--debug)
fi

exec "$APP_BIN" "${APP_ARGS[@]}" "$@"
