# Kivo 构建与发布指南

本指南面向维护者。普通用户请阅读[下载安装指南](INSTALL.md)。正式版本标签触发同一工作流：Linux 构建六平台 CLI/Web、Windows 原生构建桌面和 NSIS 安装器、macOS 原生构建 Universal 桌面、CLI、ZIP 与未公证 PKG。只有全部构建和校验通过，Release 才会公开。首次发布完成前下载页可能为空。

## 1. 发行文件

| 平台 | 原始构建产物 | Release 压缩包 | 包内程序 |
| --- | --- | --- | --- |
| Windows AMD64 | kivo-windows-amd64.exe | kivo-windows-amd64.zip | kivo.exe |
| Windows ARM64 | kivo-windows-arm64.exe | kivo-windows-arm64.zip | kivo.exe |
| macOS AMD64 | kivo-darwin-amd64 | kivo-darwin-amd64.tar.gz | kivo |
| macOS ARM64 | kivo-darwin-arm64 | kivo-darwin-arm64.tar.gz | kivo |
| Linux AMD64 | kivo-linux-amd64 | kivo-linux-amd64.tar.gz | kivo |
| Linux ARM64 | kivo-linux-arm64 | kivo-linux-arm64.tar.gz | kivo |

每个 CLI 包附带 `LICENSE`、README、`docs/USER_MANUAL.md` 和第三方组件声明。macOS / Linux 程序在
tar 中设置 `0755` 可执行权限。包名不带版本以便“最新版”链接使用；具体版本由
Release 标签和程序 `version` 命令确定。

另有 Windows AMD64/ARM64 的 `Kivo-Setup-windows-*.exe` 和 `kivo-desktop-windows-*.zip`，以及 macOS 的 `Kivo-Installer-darwin-universal.pkg` 和 `kivo-desktop-darwin-universal.zip`。这些桌面包附带 CLI，CLI 内嵌 Web。共 12 个资产，另有统一 `SHA256SUMS`；GitHub 自动附带标签对应的源码压缩包。

发行包不包含 Mihomo、开发工具、真实订阅、Token、密码、运行缓存或系统代理备份。
打包器只收录固定白名单，并拒绝覆盖已有文件；重复打包请使用新的输出目录。

## 2. 发布前检查

使用 Go 1.26 或更高版本；前端回归需要 Node.js，Go 编译本身不依赖 Node.js。

```bash
gofmt -l cmd internal scripts
go test ./...
go vet ./...
node --check internal/server/assets/app.js
node --test scripts/test-web.mjs
```

格式命令应无输出。具备 C 编译工具链的环境建议再运行 `go test -race ./...`；
GitHub Actions 的 Ubuntu 环境强制执行竞态检测。

## 3. 本地构建

### Windows PowerShell

```powershell
$releaseCommit = git rev-parse HEAD
$releaseDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
./scripts/build.ps1 -Version v0.4.0 -Commit $releaseCommit -BuildDate $releaseDate -OutputDirectory dist/v0.4.0
go run ./scripts/package.go -input dist/v0.4.0 -output dist/releases/v0.4.0
```

### macOS / Linux

```bash
VERSION=v0.4.0 COMMIT="$(git rev-parse HEAD)" BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" sh ./scripts/build.sh
go run ./scripts/package.go -input dist -output dist/releases/v0.4.0
```

脚本执行格式、测试、静态检查，以 `CGO_ENABLED=0` 编译六个平台。Go 标准库打包器
在三种系统均可运行，无需额外 zip 工具。交叉编译仅证明可构建，不等于平台实机验收。

### 本地最新版目录约定

为避免误启动历史产物，本地整理后的目录约定如下：

- `dist/kivo-windows-amd64.exe` 等六个规范名称：最新提交构建的原始程序。
- `dist/kivo.exe`：与最新 Windows AMD64 程序完全相同的短名入口。
- `dist/SHA256SUMS`：校验六个规范名称的原始程序；短名入口与 Windows AMD64 文件哈希相同。
- `dist/latest/`：最新六个压缩包及压缩包校验清单，普通用户优先使用这里的安装包。
- `dist/v<版本>-<提交短哈希>/` 与 `dist/releases/v<版本>-<提交短哈希>/`：保存版本快照。
- `dist/history/root-before-<提交短哈希>/`：保留整理前的根目录旧程序与旧校验清单，方便回查。

先完成代码提交，再将完整提交哈希注入构建。编译后校验六个平台及包内中文文档，
随后更新本地最新入口；不要覆盖已经公开的 Release 或正在运行的旧后台。
新包与旧包均保留，`latest` 指针目录不表示已推送 GitHub 或已经正式发布。

`dist` 是被 Git 忽略的生成目录；代码提交包含源码、文档和构建脚本，不把二进制文件、
测试数据、真实订阅或密钥提交到仓库。需要远程下载时，另行推送提交并运行正式发布流程。

## 4. 文件校验

Release 的 `SHA256SUMS` 校验六个**压缩包**；原始构建目录的同名清单则校验六个
**程序**，两份清单不可混用。

Windows：

```powershell
Get-FileHash .\kivo-windows-amd64.zip -Algorithm SHA256
Get-Content .\SHA256SUMS
```

Linux（所有资产都已下载时）：

```bash
sha256sum --check SHA256SUMS
```

macOS：`shasum -a 256 kivo-darwin-arm64.tar.gz`，与清单对应行比较。
校验值验证完整性；清单未签名，不能替代可信来源验证。

## 5. GitHub 自动发布

远程地址：`https://github.com/itrunswap/Kivo.git`。推送到 `main` 后 CI 验证格式、
Go / 前端测试、交叉构建和打包。

准备新版本时，更新默认开发版本、说明书和 CHANGELOG。确认提交已推送后，创建
尚未使用的正式版本标签：

```bash
git push -u origin main
git tag -a v0.4.0 -m "发布 Kivo v0.4.0"
git push origin v0.4.0
```

后续使用尚未占用的版本号，不要覆盖已有标签。上方 `v0.4.0` 只是示例，发布前需先检查远程已有标签。标签流程只接受 `v数字.数字.数字`，
从对应提交构建，注入版本、提交哈希和 UTC 时间。全部检查通过后创建草稿、上传
十二个包和清单，确认十三个资产齐全后公开为最新版。Windows/macOS 安装器必须通过对应操作系统的原生 CI 验证；在 Windows 本机不能宣称 Mac 包已构建成功。

只使用 GitHub 自带令牌，无需在仓库中保存个人 Token。失败后可重跑未公开草稿的
上传，但不能覆盖已发布版本。本地文件已存在时换输出目录，不要删除整个 dist。

需要 GitHub 登录和仓库推送权限；推送工作流的凭据必须允许修改工作流。
网络代理不可用时先修复网络。不要把 Token、密码或真实订阅地址贴到 Issue。

## 6. 许可与边界

Kivo 自身采用 MIT，根目录 `LICENSE` 必须随源码与程序包分发。
Mihomo 使用 GPL-3.0，发行包不捆绑其二进制，详见[第三方声明](../THIRD_PARTY_NOTICES.md)。
Windows 未做 Authenticode 签名，macOS 未签名或公证；多平台实机与长期联网稳定性需另行验收。
