#!/bin/sh
# 在对应操作系统原生构建；Linux/macOS 桌面依赖 CGO，不使用 CLI 交叉编译流程。
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
VERSION=${VERSION:-0.3.2-desktop-preview}
COMMIT=${COMMIT:-none}
BUILD_DATE=${BUILD_DATE:-unknown}
TARGET=${TARGET:-}
BUILD_TAGS=${BUILD_TAGS:-}
go test ./...
go vet ./...
node --test scripts/test-desktop.mjs scripts/test-desktop-ui.mjs scripts/test-web.mjs
go run ./scripts/desktop-icon
cd desktop
if [ -n "$BUILD_TAGS" ]; then
    GOFLAGS="${GOFLAGS:-} -tags=$BUILD_TAGS"
    export GOFLAGS
fi
go test ./...
go vet ./...
LDFLAGS="-s -w -X github.com/itrunswap/Kivo/internal/version.Version=$VERSION -X github.com/itrunswap/Kivo/internal/version.Commit=$COMMIT -X github.com/itrunswap/Kivo/internal/version.Date=$BUILD_DATE"
if [ -n "$TARGET" ]; then
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 build -s -skipbindings -trimpath -platform "$TARGET" -tags "$BUILD_TAGS" -ldflags "$LDFLAGS"
else
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 build -s -skipbindings -trimpath -tags "$BUILD_TAGS" -ldflags "$LDFLAGS"
fi
cd "$ROOT"
go run ./scripts/desktop-licenses
printf '\n桌面产物：%s/desktop/build/bin\n' "$ROOT"
