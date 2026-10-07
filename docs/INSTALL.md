# Kivo 下载、安装与源码构建

Kivo 的桌面、CLI 和 Web 共用一份配置和同一套后台。Windows/macOS 桌面包附带 `kivo` CLI，CLI 本身已内嵌 Web 页面；Linux 服务器只需要 CLI 包即可使用 CLI 与 Web。**Mihomo 代理内核不包含在 Kivo 安装包中**，首次运行后再通过 Kivo 安装或导入。

下载入口：[GitHub Releases](https://github.com/itrunswap/Kivo/releases/latest)。只有版本发布工作流成功且仓库公开后，下面的最新版链接才可供所有人直接下载。源码可从 [仓库主页](https://github.com/itrunswap/Kivo) 克隆；Release 自动附带该版本的 Source code ZIP/TAR.GZ。

## 1. 选择安装包

| 系统 | 推荐文件 | 备用文件 | 内容 |
| --- | --- | --- | --- |
| Windows 64 位 Intel/AMD | `Kivo-Setup-windows-amd64.exe` | `kivo-desktop-windows-amd64.zip` | 桌面 + CLI + Web |
| Windows ARM64 | `Kivo-Setup-windows-arm64.exe` | `kivo-desktop-windows-arm64.zip` | 桌面 + CLI + Web |
| macOS Apple Silicon（M1/M2/M3/M4 等）或 Intel | `Kivo-Installer-darwin-universal.pkg` **测试包** | `kivo-desktop-darwin-universal.zip` | Universal 桌面 + CLI + Web |
| Linux 64 位 Intel/AMD | `kivo-linux-amd64.tar.gz` | 无 | CLI + Web |
| Linux ARM64 | `kivo-linux-arm64.tar.gz` | 无 | CLI + Web |

六个平台的独立 CLI 包也保留，适合便携使用。Windows 和 macOS 若只需要命令行，可以选择 `kivo-windows-*.zip` 或 `kivo-darwin-*.tar.gz`。所有包均不需要用户安装 Go、Node.js 或数据库。

### 校验下载

同一 Release 中下载 `SHA256SUMS`，对照对应文件的 SHA-256。Linux 在全部资产下载齐时可运行 `sha256sum -c SHA256SUMS`；Windows 用 `Get-FileHash .\文件名 -Algorithm SHA256`；macOS 用 `shasum -a 256 文件名`。清单未签名，仅能检查传输完整性，不能代替来源认证。

## 2. Windows

下载与架构一致的 `Kivo-Setup-windows-*.exe`，双击安装，开始菜单可启动 Kivo。CLI 位于安装目录（默认 `%LOCALAPPDATA%\Programs\Kivo\kivo.exe`），PowerShell 中可以执行：

```powershell
& "$env:LOCALAPPDATA\Programs\Kivo\kivo.exe" version
& "$env:LOCALAPPDATA\Programs\Kivo\kivo.exe"
```

安装器不会改系统 PATH，也不会随卸载清除用户配置和另行下载的 Mihomo 内核。若不想安装，下载 `kivo-desktop-windows-*.zip`，解压后直接运行 `kivo-desktop.exe` 或 `kivo.exe`。由于当前安装包没有 Authenticode 代码签名，Windows 可能显示来源警告；请核对仓库来源和校验值，不要从第三方站点下载。

## 3. macOS

下载 Universal `.pkg` 测试包，可安装 `Kivo.app` 至 `/Applications`、CLI 至 `/usr/local/bin/kivo`；或下载 ZIP，解压后把 `Kivo.app` 拖到“应用程序”，在解压目录中执行 `./kivo`。M2 等 Apple Silicon 与 Intel 使用同一个 Universal 包。

```bash
kivo version
kivo
```

**重要：目前没有 Apple Developer ID 签名或 Apple 公证。** `.pkg` 是测试包，macOS Gatekeeper 可能阻止打开；建议优先在自有测试设备上使用 ZIP。若系统提示阻止，请先在 Finder 中确认文件来自本仓库，再按 macOS“隐私与安全性”提供的当前应用放行操作。不要全局关闭 Gatekeeper，不要执行来源不明的绕过安全策略命令。正式面向公众无警告分发需要开发者签名与公证。

## 4. Linux（服务器推荐）

AMD64 示例；ARM64 将文件名中的 `amd64` 换成 `arm64`。请先在 Release 页面核对版本与 SHA-256，再安装到用户目录或受管的系统目录：

```bash
curl -fL -o kivo-linux-amd64.tar.gz https://github.com/itrunswap/Kivo/releases/latest/download/kivo-linux-amd64.tar.gz
tar -xzf kivo-linux-amd64.tar.gz
./kivo version
./kivo
```

如需全局调用，可由管理员审查后执行 `sudo install -m 0755 kivo /usr/local/bin/kivo`。Linux CLI 没有桌面窗口；`/web start` 后可以在浏览器访问 Web 页面。默认 Web 地址仅监听 `127.0.0.1`，服务器远程访问建议通过 SSH 端口转发或其他受保护的入口，**不要直接把无认证页面暴露到公网**。Web Token 可在 CLI 中按需配置，详见[使用说明书](USER_MANUAL.md)。

## 5. 首次使用和日常操作

运行 `kivo` 进入交互模式，输入 `/` 可浏览全部命令。建议顺序：

```text
/core install
/sub add "我的订阅" https://example.com/subscription
/sub update all --direct
/node list
/node use 1
/connect
/proxy check
/status
```

订阅密码、AES 解密、直连/代理更新、分组和路由设置都有独立参数，见[完整命令说明](USER_MANUAL.md)。`/connect` 用于启动内核并接入系统代理；单独 `/core start` 不会自动配置浏览器或系统代理。结束时使用 `/disconnect` 恢复系统代理；`/shutdown` 同时关闭 Web、内核并退出。桌面端首页的连接开关承担对应的主要操作。

Web：CLI 中输入 `/web start`、`/web status`、`/web stop` 或 `/web restart`；`/web --show-token` 查看地址及当前 Token。CLI 已嵌入 Web 资源，**无需另行安装 Web 服务器**。

## 6. 升级与卸载

升级前先 `/disconnect` 或在桌面端断开连接，再退出桌面与后台，避免旧后台继续占用端口或使用旧版本配置。Windows 安装新版安装器；ZIP 版本请解压到新目录再运行。macOS 安装新 `.pkg` 或替换 `.app`/CLI。Linux 用新发行包中的 `kivo` 替换旧程序。不要同时运行多个不同版本的 Kivo 后台。

Windows 卸载器只移除程序文件和快捷方式；macOS 和 Linux 删除程序也不会自动删除配置、订阅或 Mihomo。用户数据目录、备份和恢复方法见[使用说明书](USER_MANUAL.md)，删除用户数据前请自行备份。

## 7. 从源码构建

仓库采用 MIT 许可证，见[LICENSE](../LICENSE)；Mihomo 是独立组件，许可见[第三方声明](../THIRD_PARTY_NOTICES.md)。开发需 Go 1.26+；Web 前端资源已纳入源码，测试需 Node.js 24。桌面构建还需对应平台的 Wails 2.15.0 依赖：Windows WebView2 / NSIS，macOS Xcode Command Line Tools，Linux GTK/WebKitGTK。

```bash
git clone https://github.com/itrunswap/Kivo.git
cd Kivo
go test ./...
go vet ./...
go build -o kivo ./cmd/kivo
```

Windows PowerShell 构建桌面和便携包：

```powershell
./scripts/build-desktop.ps1 -Architecture amd64 -OutputDirectory dist/desktop/windows
./scripts/package-windows.ps1 -Architecture amd64 -OutputDirectory dist/desktop/windows
```

macOS 原生构建桌面、Universal CLI、ZIP 和未公证测试 PKG：

```bash
TARGET=darwin/universal sh scripts/build-desktop.sh
sh scripts/package-macos.sh
```

Linux 及跨平台纯 CLI 构建：`sh scripts/build.sh`，随后 `go run scripts/package.go -input dist -output dist/releases`。完整发布检查与 GitHub Actions 流程见[维护者发布指南](RELEASE.md)。`dist` 是本地产物目录，不纳入源码提交。
