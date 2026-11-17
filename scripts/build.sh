#!/usr/bin/env bash
# One binary: the front end (web/) embedded into the Go server (server/).
#
#   scripts/build.sh                          → server/bin/hometwin (this machine)
#   GOOS=linux GOARCH=amd64 scripts/build.sh  → server/bin/hometwin-linux-amd64
set -euo pipefail
cd "$(dirname "$0")/.."

(cd web && npm run build)
rm -rf server/web/dist && mkdir -p server/web/dist && cp -R web/dist/. server/web/dist/ && touch server/web/dist/.keep

version=$(git describe --always --dirty 2>/dev/null || echo dev)
out=server/bin/hometwin
[ -n "${GOOS:-}" ] && out="$out-${GOOS}-${GOARCH:-amd64}"
(cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "../$out" ./cmd/hometwin)
ls -lh "$out"
