#!/bin/sh
# 在 macOS 保留应用包权限、符号链接与资源；不依赖开发者证书，不改变系统安全策略。
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
[ "$(uname -s)" = Darwin ] || { printf '必须在 macOS 打包桌面应用\n' >&2; exit 1; }
VERSION=${VERSION:-0.3.2-desktop-preview}
COMMIT=${COMMIT:-unknown}
BUILD_DATE=${BUILD_DATE:-unknown}
case "$VERSION" in *[!a-zA-Z0-9.-]*|'') printf '版本格式无效\n' >&2; exit 1;; esac

# Wails 输出名称由项目配置决定，仅接受唯一的原生应用，避免误打包历史文件。
set -- desktop/build/bin/*.app
[ "$#" -eq 1 ] && [ -d "$1" ] || { printf '未找到唯一的 .app 产物\n' >&2; exit 1; }
APP=$1
EXECUTABLE=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")
case "$EXECUTABLE" in ''|*/*|..|.) printf '应用入口无效\n' >&2; exit 1;; esac
BINARY="$APP/Contents/MacOS/$EXECUTABLE"
test -x "$BINARY"
# -verify_arch 后的全部参数都会被当作架构，因此输入文件必须放在前面。
lipo "$BINARY" -verify_arch x86_64 arm64
file "$BINARY"

# ad-hoc 签名只用于本地代码完整性，不等同于 Developer ID 签名或 Apple 公证。
codesign --force --deep --sign - --timestamp=none "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"
"$BINARY" version

# CLI 内嵌 Web，分别原生交叉编译两种 CPU 后合并成 Universal 程序。
LDFLAGS="-s -w -X github.com/itrunswap/Kivo/internal/version.Version=$VERSION -X github.com/itrunswap/Kivo/internal/version.Commit=$COMMIT -X github.com/itrunswap/Kivo/internal/version.Date=$BUILD_DATE"
CLI_BUILD=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/kivo-cli.XXXXXX")
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$CLI_BUILD/kivo-amd64" ./cmd/kivo
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$CLI_BUILD/kivo-arm64" ./cmd/kivo
lipo -create -output "$CLI_BUILD/kivo" "$CLI_BUILD/kivo-amd64" "$CLI_BUILD/kivo-arm64"
chmod 0755 "$CLI_BUILD/kivo"
lipo "$CLI_BUILD/kivo" -verify_arch x86_64 arm64
codesign --force --sign - --timestamp=none "$CLI_BUILD/kivo"
codesign --verify --strict "$CLI_BUILD/kivo"

STAGE=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/kivo-macos.XXXXXX")
ditto "$APP" "$STAGE/Kivo.app"
cp "$CLI_BUILD/kivo" "$STAGE/kivo"
cp docs/DESKTOP.md "$STAGE/桌面端使用说明.md"
cp docs/INSTALL.md "$STAGE/安装与升级说明.md"
cp LICENSE THIRD_PARTY_NOTICES.md desktop/THIRD_PARTY_LICENSES.txt "$STAGE/"
printf 'Kivo %s\nCommit: %s\n架构: x86_64 + arm64\n仅 ad-hoc 签名；未经 Apple 开发者签名或公证。\n' "$VERSION" "$COMMIT" > "$STAGE/构建信息.txt"
OUTPUT="$ROOT/dist/desktop/macos"
mkdir -p "$OUTPUT"
ditto -c -k --sequesterRsrc "$STAGE" "$OUTPUT/kivo-desktop-darwin-universal.zip"

# 未签名/未公证的测试 .pkg；使用系统 pkgbuild 保留 .app 与 CLI 文件权限。
PKG_ROOT=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/kivo-pkg.XXXXXX")
mkdir -p "$PKG_ROOT/Applications" "$PKG_ROOT/usr/local/bin" "$PKG_ROOT/usr/local/share/doc/kivo"
ditto "$APP" "$PKG_ROOT/Applications/Kivo.app"
cp "$CLI_BUILD/kivo" "$PKG_ROOT/usr/local/bin/kivo"
cp LICENSE THIRD_PARTY_NOTICES.md docs/DESKTOP.md docs/INSTALL.md "$PKG_ROOT/usr/local/share/doc/kivo/"
PKG_VERSION=${VERSION#v}
PKG_VERSION=${PKG_VERSION%%-*}
pkgbuild --root "$PKG_ROOT" --identifier io.github.itrunswap.kivo --version "$PKG_VERSION" --install-location / "$OUTPUT/Kivo-Installer-darwin-universal.pkg"
cd "$OUTPUT"
shasum -a 256 kivo-desktop-darwin-universal.zip Kivo-Installer-darwin-universal.pkg > SHA256SUMS
printf 'macOS 测试包：%s\n' "$OUTPUT"
