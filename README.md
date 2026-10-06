# Kivo

一个同时提供**交互终端、Web 面板与轻量桌面窗口**的跨平台代理管理工具。

Kivo 使用 Go 开发，通过独立的 Mihomo 内核提供代理能力，负责内核安装、订阅管理、节点选择、路由配置、系统代理接入与联网诊断。下载程序即可运行，**不需要安装 Go、Node.js 或数据库**。

原名 ProxyPilot，自 v0.2.7 起使用 Kivo 名称。已有配置可以继续使用，升级前请先停止旧后台，详见[改名与升级说明](docs/USER_MANUAL.md#28-kivo-改名升级与简短启动命令)。

当前开发版已按 LINUX DO Credit 的默认设计体系重构全部 Web 页面：统一明暗主题、本地中文字体、间距与控件尺寸，新增快捷搜索和可折叠侧栏；清晰区分服务、系统接入与联网状态。保留节点分页筛选、订阅批量路径、规则管理和日志搜索。详见[新版 Web 说明](docs/WEB_DESIGN.md)。已发布版本以 GitHub Release 为准。

## 下载

### 桌面端开发预览

桌面端采用 Go + Wails 2，使用系统 WebView，不附带 Chromium。紧凑单列提供首页、配置、数据、设置四个标签，和 CLI / Web 共用后台与配置。支持连接开关、真实联网状态、订阅及路由管理、节点选择 / 测速、带进度的内核安装和原生文件导入。

Windows 本地桌面包位于 `dist/desktop/v0.3.1/kivo-desktop-windows-amd64.zip`，解压双击 `kivo-desktop.exe`。旧版包保留，不覆盖正在运行的程序。这不是下方已发布 CLI 包；Mac / Linux 使用单独原生构建流水线，完成实机验收再发布。托盘就绪后关闭窗口会驻留，点击托盘恢复、右键打开状态及操作菜单；明确区分“退出桌面（保留代理）”与“断开并退出”。详见[桌面端说明](docs/DESKTOP.md)。

### CLI / Web 已发布版本

进入 [GitHub Releases](https://github.com/itrunswap/Kivo/releases) 下载对应平台的压缩包。首次发布完成前，下方“最新版”链接可能暂不可用。

仓库若为私有，下载需要登录具备仓库访问权限的 GitHub 账号；不会因为创建 Release 而自动公开源码或安装包。

| 平台          | 设备                          | 下载最新版                                                                                                      |
| ------------- | ----------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Windows AMD64 | 常见 Intel / AMD 64 位电脑    | [kivo-windows-amd64.zip](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-windows-amd64.zip)     |
| Windows ARM64 | Windows on ARM                | [kivo-windows-arm64.zip](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-windows-arm64.zip)     |
| macOS ARM64   | Apple Silicon，包括 M2        | [kivo-darwin-arm64.tar.gz](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-darwin-arm64.tar.gz) |
| macOS AMD64   | Intel Mac                     | [kivo-darwin-amd64.tar.gz](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-darwin-amd64.tar.gz) |
| Linux AMD64   | Intel / AMD 64 位电脑、服务器 | [kivo-linux-amd64.tar.gz](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-linux-amd64.tar.gz)   |
| Linux ARM64   | ARM64 设备、服务器            | [kivo-linux-arm64.tar.gz](https://github.com/itrunswap/Kivo/releases/latest/download/kivo-linux-arm64.tar.gz)   |

每包包含短名程序 `kivo.exe` 或 `kivo`、中文 README、完整使用说明书和第三方组件声明。Release 同时提供 `SHA256SUMS`，用于验证**压缩包**完整性。Kivo 包不包含 Mihomo，内核通过命令另行安装。

### Windows

解压到自己的目录，在 PowerShell 中运行：

```powershell
cd C:\Tools\Kivo
.\kivo.exe version
.\kivo.exe
```

在 CMD 中可以输入 `kivo.exe`；将目录加入 PATH 后，在任意终端输入 `kivo` 即可。建议从终端启动，便于看到错误信息。

### macOS / Linux

以 M2 Mac 为例，在下载目录执行：

```bash
mkdir -p kivo-app
tar -xzf kivo-darwin-arm64.tar.gz -C kivo-app
cd kivo-app
chmod +x kivo
./kivo version
./kivo
```

Linux / Intel Mac 替换对应包名。程序尚未签名或公证；macOS 安全提示见[说明书](docs/USER_MANUAL.md#23-常见故障排查)，不要全局关闭系统安全保护。

## 第一次使用

启动 `kivo` 后进入交互终端。输入 `/` 查看命令，输入 `/st` 筛选命令；候选显示时用 `↑↓` 选择，空输入时用 `↑↓` 查看本次会话历史，`←→` 移动光标，`Tab` 补全。

```text
/install mihomo
/sub add "我的订阅" "https://example.com/subscribe"
/sub update all --direct
/core start
/node list
/node test
/node use 3
/connect
/status
```

替换为自己的订阅地址。节点序号以当前列表为准。密码加密订阅使用 `/sub add "我的订阅" "https://example.com/encrypted" --auth aes`，随后在隐藏输入中填写密码，不建议将密码写在命令行。

`--direct` 表示直连拉取订阅；`--proxy` 表示通过 Mihomo 的 PROXY 出站拉取。该选项会保存为目标订阅的下载偏好，**不是仅本次生效**。详细要求和分组见[订阅管理](docs/USER_MANUAL.md#9-订阅管理)。

安装内核需要访问 GitHub Release。如果直连失败，先配置一个**已经可用**的 HTTP 代理，或导入官方离线包：

```text
/config download-proxy http://127.0.0.1:7890
/install mihomo
```

```text
/core import "C:\Downloads\mihomo-windows-amd64-v1-v1.19.31.zip"
```

代理地址和安装包名只是示例，必须替换为实际值；尚未安装内核时，不能依赖 Kivo 自己提供的代理下载内核。

## 怎么判断代理是否正常

**内核运行 ≠ 系统已接入 ≠ 外网可访问。** 看 `/status` 的三层信息：

| 信息                      | 含义                             | 下一步                     |
| ------------------------- | -------------------------------- | -------------------------- |
| 内核运行、端口监听        | 本地代理服务可接收请求           | 还需系统或应用接入         |
| 系统自动接入 / 指向本程序 | 支持系统代理的应用会使用该入口   | 用 `/proxy check` 检测出口 |
| 最近联网检测通过          | 当时的代理入口、节点出口检测成功 | 切换节点或配置后重新检测   |

推荐 `/connect`：启动内核、备份并设置系统代理、检测外网。Windows 支持当前用户系统代理，macOS 使用系统网络服务；Linux 自动设置目前仅覆盖具备会话 D-Bus 的 GNOME 桌面。其他 Linux 环境按 `/proxy setup` 为应用手动配置 HTTP / SOCKS5 代理。它不是全流量 VPN，部分应用不读取系统代理。

默认代理入口 `127.0.0.1:17890`，HTTP / SOCKS5 共用端口；实际端口以 `/port` 为准。联网检测是服务主机的检测结果，不保证所有网站、所有节点或所有浏览器请求都可用。

## 常用命令

| 命令                                                          | 用途                                            |
| ------------------------------------------------------------- | ----------------------------------------------- |
| `/status`、`/proxy check`                                     | 状态和真实联网检测                              |
| `/connect`、`/disconnect`                                     | 自动接入；恢复受管系统代理并停止内核            |
| `/system-proxy status\|on\|off\|recover`                      | 独立查询、设置与恢复系统代理                    |
| `/core status\|start\|stop\|restart\|list`                    | 内核状态、启停和版本列表                        |
| `/core install\|import\|use\|remove\|purge`                   | 下载、离线导入、切换、卸载                      |
| `/sub add\|list\|show\|edit\|remove`                          | 添加、查询、编辑、删除订阅                      |
| `/sub update [目标] [--direct\|--proxy]`                      | 更新指定订阅；省略目标或 `all` 更新全部活动订阅 |
| `/sub update group <分组> [--direct\|--proxy]`                | 更新指定分组的活动订阅                          |
| `/sub test ...`、`/sub group ...`                             | 订阅检查与分组管理                              |
| `/node list [关键词]`、`/node test`、`/node use <序号或名称>` | 列表、延迟测试、选择节点                        |
| `/mode rule\|global\|direct`、`/route ...`                    | 代理模式、路由配置与规则组                      |
| `/port`、`/tun ...`、`/lan ...`                               | 端口、TUN、局域网接入                           |
| `/web --show-token`、`/web status\|start\|stop\|restart`      | 页面地址、登录 Token、服务生命周期              |
| `/doctor`、`/logs`、`/help`                                   | 诊断、日志、完整帮助                            |
| `/quit`                                                       | 只退出当前终端，后台代理继续运行                |
| `/shutdown`                                                   | 停止内核、恢复受管系统代理、关闭 Web 并退出     |

直接运行子命令时去掉 `/`，例如 `kivo status`、`kivo sub update all --direct`。**关闭终端窗口不等于关闭代理**。高级命令、参数和退出区别见[完整说明书](docs/USER_MANUAL.md)。

## Web 面板

交互 CLI 首次启动自动拉起后台控制服务，默认地址 `http://127.0.0.1:9099`。运行 `/web --show-token` 获取实际地址和登录 Token，页面可管理状态、节点、订阅、路由、内核、日志与设置。

`/web stop` 关闭 CLI 共用的控制 API，当前会话可用 `/web start` 恢复。Web 设置页可立即修改或清空 Token；**清空意味着所有能够访问该地址的人都拥有管理权限**。

局域网访问：停止旧服务后显式执行 `kivo serve --listen 0.0.0.0:9099`，同时配置防火墙与强 Token。该命令以前台方式运行，默认不提供 TLS；远程访问需使用 HTTPS 反向代理、VPN 或 SSH 隧道，不要将无认证面板直接暴露到公网。Web 监听地址与代理的 LAN 开关是两个独立设置。

## 配置与升级

| 系统    | 新安装默认数据目录                              |
| ------- | ----------------------------------------------- |
| Windows | `%APPDATA%\Kivo`                                |
| macOS   | `~/Library/Application Support/Kivo`            |
| Linux   | 通常为 `~/.config/Kivo`，遵循 `XDG_CONFIG_HOME` |

目录保存配置、订阅凭据、内核、缓存、日志与系统代理恢复备份。`--data-dir <目录>` 可指定位置。若新目录尚无配置、旧 `ProxyPilot` 目录已有配置，则继续使用旧目录；不会自动搬移数据。

升级前先 `/shutdown`，替换程序后启动；不要同时运行旧、新后台。配置中有密钥，**不要上传到 GitHub、工单或聊天**。仓库不会收录本地配置、订阅、缓存、日志、工具链或构建产物。

## 文档

- [完整使用说明书](docs/USER_MANUAL.md)：安装、全部命令、认证、分组、路由、Web、排障。
- [命令速查](docs/COMMANDS.md)：常用命令与参数。
- [架构设计](docs/ARCHITECTURE.md)、[代码规范](docs/CODE_STYLE.md)、[HTTP API](docs/API.md)：开发和扩展。
- [构建与发布](docs/RELEASE.md)：六平台打包、校验、GitHub 自动发布。
- [更新记录](CHANGELOG.md)、[历史测试报告](docs/TEST_REPORT_2026-10-06.md)：改动、实测结果与边界。
- [第三方组件声明](THIRD_PARTY_NOTICES.md)：内核与许可说明。

## 本地开发

要求 Go 1.26 或更高版本；Go 部分仅使用标准库。前端使用原生 HTML / CSS / JavaScript，静态资源嵌入程序，前端测试需要支持 `node:test` 的 Node.js。

```bash
git clone https://github.com/itrunswap/Kivo.git
cd Kivo
go test ./...
go vet ./...
node --check internal/server/assets/app.js
node --test scripts/test-web.mjs
go build -o bin/kivo ./cmd/kivo
```

跨平台构建和打包见[发布指南](docs/RELEASE.md)。CI 执行格式、测试、静态检查与六平台编译；版本标签触发 Release 工作流。

## 当前边界与许可

目前只接入 Mihomo；代理可用性取决于订阅、节点和网络。Windows 已做真实 CLI / 联网测试，macOS / Linux 完成交叉构建和平台适配测试，**尚不能视为对应系统实机验收**。高级管理并非全部有 Web 按钮；TUN、远程访问、长期稳定性需按实际环境验证。

已知生命周期限制：`/web restart` 返回后内核可能仍在恢复，立即 `/shutdown` 可能遇到“订阅操作正在执行”；请先 `/status` 等待内核就绪，再执行停止或联网检测。细节见测试报告。

Kivo 自身开源许可证**尚未指定**；源码公开不等于已授予无限制使用或再分发许可，需要项目所有者补充 `LICENSE`。Mihomo 是独立下载的 GPL-3.0 组件，本仓库发行包不捆绑其可执行文件；详见[第三方组件声明](THIRD_PARTY_NOTICES.md)。
