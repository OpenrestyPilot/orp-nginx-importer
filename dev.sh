#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT"
if [ ! -x web/node_modules/.bin/vite ]; then
  echo "Install frontend dependencies first: cd $ROOT/web && npm install" >&2
  exit 1
fi
export ORP_BACKEND_URL=${ORP_BACKEND_URL:-http://127.0.0.1:8081}
export NGINX_IMPORTER_STORAGE=${NGINX_IMPORTER_STORAGE:-$ROOT/data}
go run ./cmd/server &
API_PID=$!
trap 'kill "$API_PID" 2>/dev/null || true' EXIT INT TERM
cd "$ROOT/web"
npm run dev -- --strictPort
