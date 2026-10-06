#!/usr/bin/env sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_ROOT="$PROJECT_ROOT/dist"
VERSION=${VERSION:-dev}
COMMIT=${COMMIT:-none}
BUILD_DATE=${BUILD_DATE:-unknown}
LDFLAGS="-s -w -X github.com/itrunswap/Kivo/internal/version.Version=$VERSION -X github.com/itrunswap/Kivo/internal/version.Commit=$COMMIT -X github.com/itrunswap/Kivo/internal/version.Date=$BUILD_DATE"

cd "$PROJECT_ROOT"
# 只检查项目源码，避免扫描 dist 或开发者本地的便携工具链。
UNFORMATTED=$(gofmt -l cmd internal scripts)
if [ -n "$UNFORMATTED" ]; then
  printf '以下 Go 文件尚未格式化：\n%s\n' "$UNFORMATTED" >&2
  exit 1
fi
go test ./...
go vet ./...
mkdir -p "$DIST_ROOT"

for TARGET in windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  GOOS=${TARGET%/*}
  GOARCH=${TARGET#*/}
  EXT=""
  [ "$GOOS" = "windows" ] && EXT=".exe"
  OUTPUT="$DIST_ROOT/kivo-$GOOS-$GOARCH$EXT"
  CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT" ./cmd/kivo
  printf '已构建 %s\n' "$OUTPUT"
done

cd "$DIST_ROOT"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum kivo-* > SHA256SUMS
else
  shasum -a 256 kivo-* > SHA256SUMS
fi
printf '已生成 %s\n' "$DIST_ROOT/SHA256SUMS"
