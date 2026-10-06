# Kivo 完整使用说明书

> 适用版本：Kivo v0.2.7（原名 ProxyPilot）
> 支持平台：Windows、macOS、Linux；支持 AMD64 与 ARM64
> 当前代理内核：Mihomo

本文档集中说明 Kivo 的安装、启动、交互方式、全部 CLI 命令、Web 管理、订阅认证、
订阅分组、节点选择、路由规则、数据目录、安全事项、HTTP API 与常见故障。除开发者构建
说明外，使用已编译版本不需要安装 Go、Node.js 或数据库。

---

## 目录

改名升级、简短命令安装与旧配置沿用见文末第 28 节。

1. [Kivo 是怎样工作的](#1-kivo-是怎样工作的)
2. [选择正确的程序文件](#2-选择正确的程序文件)
3. [两种命令运行方式](#3-两种命令运行方式)
4. [第一次使用的完整流程](#4-第一次使用的完整流程)
5. [全部命令速查](#5-全部命令速查)
6. [基础状态与帮助命令](#6-基础状态与帮助命令)
7. [Mihomo Core 安装与版本管理](#7-mihomo-core-安装与版本管理)
8. [Core 下载、重试与 unexpected EOF](#8-core-下载重试与-unexpected-eof)
9. [订阅管理](#9-订阅管理)
10. [订阅分组](#10-订阅分组)
11. [节点查看、测速和选择](#11-节点查看测速和选择)
12. [运行模式与路由配置](#12-运行模式与路由配置)
13. [规则组与路由规则](#13-规则组与路由规则)
14. [代理端口、LAN 与 TUN](#14-代理端口lan-与-tun)
15. [配置、诊断和日志](#15-配置诊断和日志)
16. [Web 控制服务](#16-web-控制服务)
17. [退出命令区别](#17-退出命令区别)
18. [自定义数据目录](#18-自定义数据目录)
19. [默认数据目录与文件用途](#19-默认数据目录与文件用途)
20. [远程访问 Web 页面](#20-远程访问-web-页面)
21. [普通子命令完整示例](#21-普通子命令完整示例)
22. [HTTP API 速查](#22-http-api-速查)
23. [常见故障排查](#23-常见故障排查)
24. [安全与备份建议](#24-安全与备份建议)
25. [开发、测试和构建](#25-开发测试和构建)
26. [当前能力边界](#26-当前能力边界)
27. [自动系统代理：连接、断开与恢复](#27-自动系统代理连接断开与恢复)

---

## 1. Kivo 是怎样工作的

Kivo 包含三层：

```text
CLI / Web 页面
       │
       ▼
Kivo 本地控制服务（HTTP API）
       │
       ▼
Mihomo Core（真正处理代理流量）
```

- CLI 和 Web 页面是管理入口。
- Kivo 控制服务负责保存配置、下载内核、管理进程、生成 Mihomo 配置。
- Mihomo Core 提供 HTTP + SOCKS5 混合代理、TUN、节点和规则能力。
- 默认 Web/API 地址是 `http://127.0.0.1:9099`。
- 默认混合代理地址是 `127.0.0.1:17890`。

注意：`/core start` 或 `/proxy on` 表示启动 Mihomo，并不会自动修改操作系统的“系统代理”
开关。v0.2.6 推荐 `/connect` 自动设置系统代理并检测；`/disconnect` 恢复原设置。
仅管理内核、仅配置应用代理与 TUN 是不同操作，完整说明见第 27 节。

---

## 2. 选择正确的程序文件

### 从 GitHub Release 下载（推荐）

进入 [Kivo Releases](https://github.com/itrunswap/Kivo/releases)，选择系统与架构对应的
`kivo-系统-架构.zip`（Windows）或 `kivo-系统-架构.tar.gz`（macOS / Linux）。
**压缩包解压后的程序已经统一为短名 `kivo.exe` / `kivo`**，无需再次重命名；同时附带
本说明书、README 与第三方组件声明。Apple M2 选择 `kivo-darwin-arm64.tar.gz`。

下表是源码构建的原始产物命名。下文若使用带平台后缀的文件名，在 Release 包中直接
替换为 `kivo.exe` / `kivo` 即可，例如 Windows 执行 `.\kivo.exe`，macOS 执行 `./kivo`。
详细解压步骤见 [README](../README.md#下载)。Release 的 `SHA256SUMS` 校验的是
压缩包；源码构建目录内的同名文件校验的是原始可执行文件，不要混用两份清单。

### 源码构建的原始程序文件

| 系统 | CPU | 文件 |
| --- | --- | --- |
| Windows 10/11 | Intel/AMD 64 位 | `kivo-windows-amd64.exe` |
| Windows on ARM | ARM64 | `kivo-windows-arm64.exe` |
| macOS Intel | Intel | `kivo-darwin-amd64` |
| macOS Apple Silicon | M1/M2/M3/M4 | `kivo-darwin-arm64` |
| Linux PC/服务器 | Intel/AMD 64 位 | `kivo-linux-amd64` |
| Linux ARM 设备 | ARM64 | `kivo-linux-arm64` |

发布目录中的 `SHA256SUMS` 用于核对文件完整性。

### 2.1 Windows 启动

建议在 PowerShell 中运行，而不是双击后等待窗口：

```powershell
cd E:\你的目录
.\kivo-windows-amd64.exe
```

查看版本：

```powershell
.\kivo-windows-amd64.exe version
```

双击后窗口闪一下通常不是“进程未关闭”，而是控制台程序遇到错误后窗口被系统立即关闭。
从 PowerShell 启动可以直接看到错误信息。

### 2.2 macOS Apple Silicon 启动

M1、M2、M3、M4 使用 ARM64 文件：

```bash
chmod +x kivo-darwin-arm64
./kivo-darwin-arm64
```

如果 macOS 拦截从网络下载的文件，只在确认文件来源和 SHA-256 可信后执行：

```bash
xattr -d com.apple.quarantine kivo-darwin-arm64
./kivo-darwin-arm64
```

### 2.3 Linux 启动

```bash
chmod +x kivo-linux-amd64
./kivo-linux-amd64
```

ARM64 Linux 将文件名替换为 `kivo-linux-arm64`。

---

## 3. 两种命令运行方式

### 3.1 交互模式

不传子命令直接启动：

```text
kivo
```

进入交互界面后使用斜杠命令：

```text
/status
/core start
/sub list
```

交互编辑器支持：

输入框上方固定为 8 行：空闲时显示精简状态面板，输入 `/` 时在同一位置显示命令，
通过 `↑` / `↓` 滚动全部候选。每次命令执行后刷新状态；按键筛选不会发起状态查询。
上下边框随终端宽度变化，提交后只保留命令与执行结果，不再重复打印常用命令和边框。

| 按键 | 作用 |
| --- | --- |
| `/` | 显示命令候选 |
| 继续输入，例如 `/st` | 按前缀过滤命令 |
| `↑` / `↓` | 候选区可见时选择命令；空输入或已进入历史模式时查看上一条/下一条命令 |
| `←` / `→` | 按字符移动输入光标，支持中文 |
| `Home` / `End` | 移到输入行开头/末尾 |
| `Tab` | 补全当前候选 |
| `Enter` | 执行命令 |
| `Backspace` / `Delete` | 删除光标前/后的字符 |
| `Ctrl+A` / `Ctrl+E` | 移到输入行开头/末尾 |
| `Ctrl+C` / `Ctrl+D` | 退出当前 CLI，后台服务保持当前状态 |

执行 `/node list` 后，节点名称会进入当前会话的动态补全缓存；执行 `/sub list` 后，订阅名称
会进入订阅命令的动态补全缓存。

候选区固定保留 8 行，不会因为输入或筛选结果变化而推动输入框。输入 `/` 时，程序会加载
完整命令目录；界面一次显示其中 8 条，并在上边框显示当前范围，例如
`命令 9-16/72`。持续按 `↑` 或 `↓` 即可浏览全部命令。输入框上下边框会随终端宽度变化，
并保留一列安全边距，避免某些终端写满最后一列后自动折行。
未输入命令时，这 8 行会显示常用命令和快捷键提示，不再在上一条命令输出与输入框之间留下无含义的空白块。

命令历史最多保留当前会话的 100 条记录，相邻的重复命令只保留一条。
由于订阅 URL 可能包含 Token，历史不写入磁盘，退出 CLI 后自动清除。
过长命令会在同一行内水平滚动，不会撑开或打乱候选区。

### 3.2 一次性普通子命令

交互命令去掉开头的 `/`，即可用于 PowerShell、Shell 或脚本：

```text
kivo status
kivo core start
kivo sub list
kivo route profile list
```

下面文档主要使用交互形式。除 `/quit` 和 `/clear` 等会话命令外，大多数命令都能以普通
子命令方式执行。

### 3.3 参数书写规则

- `<值>` 表示必填参数。
- `[值]` 表示可选参数。
- `a|b|c` 表示三选一。
- 包含空格的名称或路径必须加引号。
- Windows 路径建议加双引号。

示例：

```text
/sub group create "工作订阅"
/sub move 1 "工作订阅"
/core import "D:\Downloads\mihomo-windows-amd64-v1.19.31.zip"
/node use "Hong Kong Premium 01"
```

---

## 4. 第一次使用的完整流程

```text
# 1. 启动 Kivo
kivo

# 2. 安装最新 Mihomo
/core install

# 3. 添加订阅；名称会根据域名自动生成
/sub add https://example.com/subscription

# 4. 启动 Core
/core start

# 5. 更新并检查订阅
/sub update all
/sub test all

# 6. 测速并查看节点
/node test
/node list

# 7. 按序号选择节点
/node use 3

# 8. 使用绕过中国大陆规则
/route profile use bypass-cn

# 9. 查看整体状态
/connect
/status
```

应用的手动代理配置：

```text
HTTP 代理：127.0.0.1:17890
SOCKS5 代理：127.0.0.1:17890
```

---

## 5. 全部命令速查

| 分类 | 命令 |
| --- | --- |
| 基础 | `/help`、`/?`、`/status`、`/st`、`/clear` |
| 安装 | `/install mihomo [版本]` |
| Core | `/core status|list|install|update|import|use|switch|start|stop|restart|remove|uninstall|purge` |
| 代理与联网 | `/proxy`、`/proxy status|check|setup|on|off|restart` |
| 自动接入 | `/connect [--replace|--adopt] [--no-check]`、`/disconnect` |
| 系统代理 | `/system-proxy status|on|off|recover`；on 支持 `--replace/--adopt`，recover 支持 `--force` |
| 订阅 | `/sub add|list|show|edit|enable|disable|move|update|test|remove` |
| 订阅分组 | `/sub group list|create|use|enable|disable|remove` |
| 节点 | `/node list|test|use` |
| 模式 | `/mode rule|global|direct` |
| 路由配置 | `/route profile list|use|create|attach|detach|remove` |
| 规则组 | `/route group list|create|remove` |
| 路由规则 | `/route rule list|add|remove` |
| 网络 | `/port`、`/tun`、`/lan` |
| 配置 | `/config show|validate|download-proxy|download-retry` |
| 诊断 | `/doctor`、`/logs [行数]` |
| Web | `/web`、`/web status|start|stop|restart|--show-token` |
| 退出 | `/quit`、`/q`、`/exit`、`/shutdown` |

`/subscription` 是 `/sub` 的完整名称别名。

---

## 6. 基础状态与帮助命令

### `/help`、`/?`

显示 CLI 内置快速帮助。

```text
/help
/?
```

### `/status`、`/st`

显示固定八行连接状态卡；只读取本地状态和最近检测快照，不自动请求外网。`/proxy` 和
`/proxy status` 与 `/status` 同义。

状态卡按“结论 → 服务/系统 → 联网证据 → 节点/配置 → 下一步”排列。绿色表示对应检查通过，
黄色表示未接入、待确认或待检测，红色表示失败；关闭状态使用灰色。所有状态同时提供文字，
关闭颜色后仍能判断。下面是虚构的正常状态示意，并非你的实时检测结果：

```text
╭─ KIVO · 连接概览 ─────────────────────────────────────╮
│ ● 代理已启用 · 外网检测通过                                  │
│ 服务 ● 运行中 · 正在监听  │ 系统 ● 已接入                   │
│ 联网 入口通过 · 节点通过 · 10:30:00 检测                    │
│ 节点 [订阅] 香港-优化 02                                    │
│ 入口 127.0.0.1:17890 · 规则分流 · TUN 关闭                  │
│ 订阅 1/2 已启用 · Mihomo v1.19.31                           │
╰─ 下一步 /proxy check ───────────────────────────────────────╯
```

服务关闭后，若系统仍指向本程序，会突出显示“代理已停止 · 系统仍指向本程序”，而不是绿色
“已接入”；请启动内核，或先恢复系统代理设置。服务未就绪时不会展示旧的联网成功结果。
长节点名称裁剪后保留边框；较窄窗口优先保留入口与节点的检测状态，省略检测时间。
输入 `/` 时状态卡让位给八行命令候选；清空输入后恢复，不增加高度或移动输入框。

| 面板字段 | 含义与限制 |
| --- | --- |
| 顶部结论 | 当前结论，例如“代理服务已启动 · 外网待检测”“外网部分可达”“代理已启用 · 外网检测通过” |
| 服务 / 系统 | 同行双列、各自独立标记；服务运行/监听不代表系统已接入；PAC、其他代理和未知状态分别提示 |
| 联网 | 日常代理入口、选中节点出口的最近检测结果与时间；未检测、已失效、服务未就绪分别提示 |
| 节点 | PROXY 策略组展开后的实际节点；AUTO 会尽量显示当前选中的叶子节点，不代表所有规则的出口 |
| 入口 | 混合端口地址、中文模式名称和 TUN 配置；仅作用于已接入流量。TUN 已配置/未验证不等于实际接管 |
| 订阅 | 已启用/全部订阅数量及内核版本 |
| 底部下一步 | 根据当前证据建议执行的具体命令 |

Linux 自动系统代理支持 GNOME 桌面会话，其他环境明确提示使用应用代理或 TUN，不把环境
变量误认为浏览器配置。远程 CLI/Web 显示的是 **运行 Kivo 的主机**，不是访问页面的
设备。浏览器独立代理、扩展、绕过列表和 TUN 都可能影响实际路径。`/connect` 会设置系统
代理，而 `/core start`、`/proxy check` 和 `/proxy setup` 不会自动接管。

如果“内核运行中、端口正在监听”，但“系统代理未开启”，且 TUN 关闭，浏览器不会因为启动
Mihomo 就自动使用代理。优先执行 `/connect`；不支持自动设置的平台可手动配置
`127.0.0.1:17890`（或 `/port` 显示的端口），再 `/proxy check` 验证。也可显式代理测试：

```powershell
curl.exe --proxy http://127.0.0.1:17890 --max-time 20 -I https://www.google.com
```

Linux/macOS 使用 `curl`。测试能证明本次请求通过指定代理端口的结果，不保证所有网站都可访问。

```text
/status
/st
/proxy
/proxy status
```

### `/proxy check`：现在能不能上网

```text
/proxy check
```

同时检查以下路径，不切换节点、不启动内核、不修改系统代理或路由模式：

这里的“不启动内核”指检测用例本身；CLI 首次连接后台时，仍遵循原有的控制服务自动启动及
Core 自动恢复偏好。要检测已停止状态，请在后台运行时先 `/core stop`，再执行检测。

| 检测路径 | 能证明什么 | 不能证明什么 |
| --- | --- | --- |
| 本机直连 | 不使用应用/环境 HTTP 代理访问测试目标的结果 | 不保证绕过系统 VPN/TUN；它们仍可改变操作系统路由 |
| 日常代理入口 | 经本机混合端口，遵循当前模式和规则访问目标的结果 | RULE/DIRECT 或 GLOBAL 的选择可能走直连；不能单凭此项证明远端节点正常 |
| 选中节点出口 | 经受认证的内部回环入口，固定通过 PROXY 策略组访问目标的结果 | 不代表浏览器已配置代理，也不代表所有规则均走该节点 |

每条路径检测 Google 的 HTTPS 204 端点和 Cloudflare 的 HTTPS trace 端点，逐项显示
通过/未通过、耗时和安全错误原因。严格验证 TLS、不跟随重定向、不把登录页当作成功。
Cloudflare 返回的正文和出口 IP 不保存、不展示；仅验证预期响应标记。
一次最多六个小请求，单项最多 10 秒、整体最多 20 秒；连续 5 秒内的重复请求复用快照，
同时进行的重复检测会被拒绝。内核没运行时，代理两项显示“未检测”，不会偷偷启动。

结果只有四种：**通过、部分通过、未通过、未检测**。一次检测通过不保证所有网站可达。
结果超过 2 分钟，或者节点、模式、配置、内核进程改变后，需要重新检测；旧结果不再表示
当前可用。结果只在内存保留，控制服务重启后重新检测。

判断顺序：

1. 服务未启动 → `/connect`（仅内核则 `/core start`）。
2. 端口未监听 → `/logs 200`。
3. 尚未检测/旧结果失效 → `/proxy check`。
4. 部分失败 → 查看是哪条路径、哪个目标失败；必要时 `/node list`、`/node use <序号>` 后重测。
5. 入口及节点通过，但系统未接入 → `/connect`；不支持的平台按 `/proxy setup` 指引配置。
6. 系统接入且近期代理两项通过 → 显示“系统已接入 · 外网检测通过”；仍需注意浏览器扩展和绕过规则。

`/node test` 是批量比较节点延迟，`/doctor` 是环境检查，都不等于上述端到端访问检测。
普通子命令 `kivo proxy check` 在代理两项未全部通过或结果失效时返回非零退出码；
系统未接入是单独提示，不会把网络检测通过改成失败。

### `/proxy setup`：接入系统/浏览器与安全停用

```text
/proxy setup
```

根据**服务所在机器**的 Windows/macOS/Linux 平台及当前 `/port` 生成配置步骤。
这是只读指引，不自动改系统代理，不覆盖公司 PAC 或其他客户端配置。

第一次使用优先 `/connect` → `/status`。不支持自动设置或选择手动管理时，按下述指引配置。
Windows 通常在“设置 → 网络和 Internet → 代理 → 手动设置代理”
填 `127.0.0.1` 和当前混合端口；macOS 配置当前网络服务的 HTTP 和 HTTPS 代理；
Linux 根据桌面环境或应用配置。操作系统参考：[Windows 官方说明](https://support.microsoft.com/en-us/windows/experience/connectivity-networking/use-a-proxy-server-in-windows)、
[macOS 官方说明](https://support.apple.com/en-au/guide/mac-help/mchlp2591/26/mac/26)。

`/disconnect`、`/core stop`、`/web stop` 和 `/shutdown` 会先恢复本程序管理的系统代理。
仅按指引手动配置而未 `/connect --adopt` 接管的设置不会自动还原，仍需先手动恢复，
避免指向已停止的端口。`/quit` 只退出交互 CLI，不断开当前连接。

远程 Web 页显示的是服务器的接入情况，不能把远程设备自己的 `127.0.0.1` 当作服务器。

### `/clear`

清空当前终端显示，不删除日志或配置。

```text
/clear
```

### 版本命令

版本命令在程序入口执行，不是交互斜杠命令：

```text
kivo version
kivo --version
kivo -v
```

---

## 7. Mihomo Core 安装与版本管理

### 7.1 快速安装命令

```text
/install mihomo
/install mihomo v1.19.31
/install v1.19.31
```

不指定版本时安装 Mihomo 最新稳定版。

### 7.2 查看 Core 状态

```text
/core status
```

显示状态、版本、PID、代理地址、当前节点和最近错误。

常见状态：

| 状态 | 含义 |
| --- | --- |
| `NOT_INSTALLED` | 尚未安装 Core |
| `STOPPED` | 已安装但未运行 |
| `STARTING` | 正在启动 |
| `RUNNING` | 正常运行 |
| `STOPPING` | 正在停止 |
| `FAILED` | 启动失败或异常退出 |

### 7.3 查看已安装版本

```text
/core list
```

列表中的 `ACTIVE` 是当前活动版本，`INSTALLED` 是可切换的其他版本。序号可以用于
`use` 和 `remove`。

### 7.4 安装或更新

```text
/core install
/core install v1.19.31
/core update
/core update v1.19.31
```

`update` 是安装最新或指定版本的语义别名，不会更新 Kivo 本身。安装不同版本不会
自动删除旧版本。Core 正在运行时，安装完成后会安全切换；新版本启动失败会尝试回滚。

### 7.5 导入已下载的官方安装包

```text
/core import <官方 .gz 或 .zip 文件路径>
```

示例：

```text
/core import "C:\Downloads\mihomo-windows-amd64-v1-v1.19.31.zip"
/core import "/Users/me/Downloads/mihomo-darwin-arm64-v1.19.31.gz"
```

要求：

- 使用 Mihomo 官方 `.gz` 或 `.zip` 包。
- 保留官方文件名，程序需要从文件名识别版本。
- 导入后会运行 `mihomo -v` 验证可执行文件。
- 验证成功后设为活动版本；运行中的 Core 使用安全切换流程。

### 7.6 切换活动版本

```text
/core use <序号|版本>
/core switch <序号|版本>
```

示例：

```text
/core list
/core use 2
/core use v1.19.31
/core use 1.19.31
```

`v` 前缀可省略。Core 正在运行时会自动停止、切换并重新启动；启动失败时尝试恢复旧版本。

### 7.7 启动、停止和重启

```text
/core start
/core stop
/core restart
```

- `start`：启动 Core，并记录自动恢复偏好。
- `stop`：停止 Core，同时关闭下一次控制服务启动时的 Core 自动启动。
- `restart`：先校验新配置，再重启 Core；预检失败时保留当前运行进程。

等价的快捷命令：

```text
/proxy on
/proxy off
/proxy restart
```

### 7.8 删除指定版本

```text
/core remove <序号|版本>
/core uninstall <序号|版本>
```

只能删除非活动版本。先用 `/core use` 切换，再删除原版本。

### 7.9 完全卸载所有受管 Core

```text
/core purge --yes
```

该命令会：

1. 停止当前 Core。
2. 删除 `cores/mihomo` 中全部受管版本。
3. 清除活动 Core 路径和版本。
4. 关闭 Core 自动启动。

订阅、路由、Web Token 等 Kivo 配置不会被删除。

---

## 8. Core 下载、重试与 `unexpected EOF`

Kivo 的 Core 下载器支持：

- 自动重试，默认 4 次，可设置 1–10 次。
- 指数退避。
- `.part` 临时文件。
- HTTP Range 断点续传。
- Release 声明文件大小校验。
- SHA-256 校验。
- 系统环境代理或显式 HTTP/HTTPS 下载代理。

### 设置重试次数

```text
/config download-retry 6
```

### 查看下载代理

```text
/config download-proxy
```

### 使用本地代理下载 Core

```text
/config download-proxy http://127.0.0.1:7890
```

### 恢复系统环境代理

```text
/config download-proxy system
/config download-proxy off
```

当前实现中 `system` 和 `off` 都表示“清除显式代理并恢复 Go 的系统环境代理行为”，会读取
`HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`。它们不表示强制忽略系统代理。

发生 `unexpected EOF` 时建议：

```text
/config download-retry 6
/config download-proxy http://127.0.0.1:7890
/core install v1.19.31
```

下载中断后不要手动删除 `downloads` 中相同文件的 `.part`，再次安装会尝试续传。若网络始终
无法访问 GitHub，可在浏览器下载对应平台的官方包，再使用 `/core import`。

---

## 9. 订阅管理

订阅既有自身的 `Enabled` 开关，也属于一个订阅分组。只有“订阅自身启用”且“所属分组启用”
时，它才会进入生成的 Mihomo 配置。

### 9.1 最简添加

```text
/sub add https://example.com/subscription
```

程序会：

- 从 URL 域名生成名称。
- 重名时自动追加 `-2`、`-3`。
- 放入 `default` 分组。
- 使用 `none` 认证。
- 默认更新周期 3600 秒。
- 默认健康检查周期 300 秒。
- 默认节点前缀为 `[订阅名] `。

### 9.2 指定名称

```text
/sub add 机场A https://example.com/subscription
/sub add https://example.com/subscription --name 机场A
```

名称包含空格时：

```text
/sub add "My Provider" https://example.com/subscription
```

### 9.3 完整选项

```text
/sub add [名称] <URL> [--name 名称] [--group 分组] [--auth 类型] [--user 用户名] [--prefix 前缀] [--via direct|proxy]
```

示例：

```text
/sub add 机场A https://example.com/sub --group 工作 --prefix "[工作] "
/sub add https://example.com/sub --name 机场B --auth bearer
/sub add https://example.com/sub --name 机场C --auth basic --user alice
/sub add "加密订阅" https://example.com/encrypted-sub --auth aes
```

当认证/加密方式不是 `none` 时，程序会单独提示输入密码、Token 或私钥。输入采用隐藏模式，
不建议把凭据直接写在命令行或 URL 中。

不带参数时进入交互添加：

```text
/sub add
```

### 9.4 认证方式

| 类型 | 用法 | 说明 |
| --- | --- | --- |
| `none` | `--auth none` | 无额外认证，或 Token 已包含在 URL 中 |
| `basic` | `--auth basic --user alice` | HTTP Basic，随后隐藏输入密码 |
| `bearer` | `--auth bearer` | `Authorization: Bearer <Token>` |
| `token` | `--auth token` | `Authorization: token <Token>` |
| `age` | `--auth age` | Mihomo provider 的 `age-secret-key` |
| `aes` | `--auth aes` | Base64 包装的 AES-128-CBC 加密订阅；随后隐藏输入解密密码 |

`aes` 是为兼容现有订阅平台格式提供的适配器，其数据布局是
`Base64(16 字节 IV || AES-128-CBC-PKCS#7 密文)`，密钥为密码的 16 字节 MD5 值。
Kivo 在内存中解密后，通过带独立内部密钥的本机接口交给 Mihomo，不会把明文订阅写入
`config.json`。Mihomo 仍会按 provider 机制把解密后的订阅缓存到数据目录的
`runtime/mihomo/proxy_providers/`，因此应继续保护整个 Kivo 数据目录；删除订阅后如需彻底
清理历史明文缓存，应在内核停止时删除该订阅对应的 provider 缓存文件。这个旧格式不带消息认证
能力，只用于兼容既有服务，不建议用于设计新的加密协议。

完整添加示例：

```text
/sub add "加密订阅" "https://example.com/encrypted-sub" --auth aes
AES 解密密码: （此处隐藏输入）
```

名称可以自行设置，与订阅平台名称无关；实现和文档中不包含任何特定平台品牌。

### 9.5 查看订阅

```text
/sub list
/sub list <分组>
/sub show <序号|名称>
```

示例：

```text
/sub list
/sub list 工作
/sub show 1
/sub show "My Provider"
```

列表中的 URL 会隐藏用户信息和查询参数中的敏感内容。

### 9.6 修改订阅

```text
/sub edit <序号|名称> [--name 值] [--url 地址] [--group 分组] [--prefix 前缀] [--auth 类型] [--user 用户名] [--via direct|proxy]
```

示例：

```text
/sub edit 1 --name 主线路
/sub edit 1 --url https://example.com/new-sub
/sub edit 1 --group 工作 --prefix "[Work] "
/sub edit 1 --auth bearer
/sub edit 1 --auth aes
/sub edit 1 --auth none
```

设置需要凭据的认证方式时，CLI 会提示输入新密码或 Token。没有出现在命令中的字段保持不变。

### 9.7 启用、禁用和移动

```text
/sub enable <序号|名称>
/sub disable <序号|名称>
/sub move <序号|名称> <分组>
```

示例：

```text
/sub disable 2
/sub enable "备用线路"
/sub move 2 工作
```

### 9.8 更新与健康检查

```text
/sub update
/sub update <序号|名称|all> [--direct|--proxy]
/sub update group <分组> [--direct|--proxy]
/sub update --group <分组> [--via direct|proxy]
/sub test
/sub test <序号|名称|all> [--direct|--proxy]
/sub test group <分组> [--direct|--proxy]
```

示例：

```text
/sub update 1
/sub update 2 --direct
/sub update "主线路" --proxy
/sub update
/sub update all
/sub update group 工作 --proxy
/sub update --group 备用 --via direct
/sub test "主线路"
/sub test all --direct
/sub test group 工作 --proxy
```

`/sub update` 不带参数等价于 `/sub update all`。更新目标可以是序号、名称、全部活动订阅，
也可以是指定分组。`all` 和分组更新只处理“订阅自身已启用且所属分组已启用”的 provider。

更新路径说明：

| 参数 | 行为 |
| --- | --- |
| 不传 | 保留各订阅当前的更新路径；新订阅默认 `direct` |
| `--direct` | 直连下载，等价于 `--via direct` |
| `--proxy` | 通过 Mihomo 的 `PROXY` 策略组下载，等价于 `--via proxy` |

指定的更新路径会保存到该订阅，Mihomo 后续按周期自动更新时也继续使用。普通订阅由 Mihomo
provider 的 `proxy` 字段执行；AES 兼容订阅通过仅监听本机、带随机认证密钥、固定 `PROXY`
出站的内部 HTTP 入口执行，不再复用受规则和模式影响的 mixed-port。即使全局为 DIRECT，
显式代理更新也使用 PROXY；如果 PROXY 选中了 DIRECT/REJECT 或没有实际节点，将明确报错，
不会自动退回直连。直连在应用下载层忽略环境代理，不使用上述代理入口；操作系统的 VPN/TUN
等更底层路由仍可能影响连接，这不等于绕过系统网络策略。

路径没有改变时不重启、不重载内核；AES 路径由适配器实时读取，改变时也不重启。
普通 provider 改变路径时使用控制接口热重载，保持进程 ID 不变。Provider 更新使用独立的 2 分钟控制请求超时，
不再受普通本地状态查询的 15 秒超时限制。
选择 `proxy` 前应保证 `PROXY` 组已有可用节点；全新安装、没有缓存节点或唯一订阅本身就是
代理来源时，应先使用 `--direct` 完成第一次更新，避免循环依赖。

Provider 刷新 API 只存在于运行中的 Mihomo。若内核原本停止，Kivo 会临时启动它，
批量更新结束后恢复停止状态，并且不会修改 `autoStart` 偏好。若 Mihomo 尚未安装，会直接给出
“请先执行 `/core install`”的可读错误，不再把 `connection refused` 原样暴露给用户。

更新接口返回成功后，Kivo 还会读取 provider 状态并确认至少解析出一个节点；已有更新时间
时还会确认时间前进，防止将旧缓存误报成新更新。CLI 和 Web 显示每个订阅的节点数、更新前数量、
实际下载路径和耗时。数量不是“新增数量”，也不保证全部节点在线。批量更新部分失败时，
已成功的结果仍会保留，失败项目会单独列出原因。

成功更新后 `/node list` 在内核停止时也能查看离线快照，状态明确标为“缓存”。要选择节点、测速
或实际使用代理，需要先 `/core start`。快照只保存名称、协议等元信息，不保存节点密码；
更换订阅地址、认证信息或节点前缀后，旧快照不再使用。CLI 与 Web 的订阅操作串行执行，
期间启动/停止/重启内核或手动切换节点会收到“请等待”的提示。

如果订阅站返回 Base64 通用链接、HTML 错误页或空内容，命令会明确报告“未解析出任何节点”，
不再显示误导性的更新成功。Provider 请求使用 `Clash.Meta` User-Agent，以便常见订阅面板返回
Clash/Mihomo 格式。

`/sub test` 不带参数时检查全部活动订阅，同样支持单个订阅、分组和 `--direct|--proxy`。
“检查”会先按选定路径刷新 provider，验证订阅源能够下载且内容可解析，然后触发该 provider
下全部节点的健康检查。因此直连/代理选择确实应用于订阅源验证；节点健康检查本身仍会
通过各节点访问检测地址。若内核原本停止，检查也会临时启动内核并在完成后恢复。

### 9.9 删除订阅

```text
/sub remove <序号|名称>
```

删除后，运行中的 Core 会重新加载配置。

---

## 10. 订阅分组

订阅分组适合管理多个平台、机场、用途或环境，例如“工作”“个人”“备用”。

### 查看分组

```text
/sub group list
```

### 创建分组

```text
/sub group create <名称>
```

示例：

```text
/sub group create 工作
/sub group create "海外平台"
```

新分组默认启用。

### 独占使用一个分组

```text
/sub group use <名称>
```

`use` 会启用目标分组，同时禁用其他全部分组。

### 组合启用多个分组

```text
/sub group enable <名称>
/sub group disable <名称>
```

`enable/disable` 只改变目标分组，不影响其他分组。

示例：

```text
/sub group enable 工作
/sub group enable 备用
/sub group disable 个人
```

### 删除分组

```text
/sub group remove <名称>
```

存在订阅引用时不能删除该分组。先用 `/sub move` 移走订阅，或删除相关订阅。

---

## 11. 节点查看、测速和选择

节点相关操作要求 Core 正在运行且至少有一个有效、已启用的订阅 provider。

### 查看全部节点

```text
/node list
```

显示全局序号、节点名、协议、来源订阅、延迟和可用状态。

列表按完整节点名称稳定排序，测速不会改变序号；订阅新增、删除或改名仍可能改变序号。
窄终端使用两行布局，避免中文和 Emoji 导致折行错位。状态区分“未测速”“检查通过”
“检查未通过”“离线缓存”；未经测试的 `alive` 默认值不会被当作已验证可用。

### 按关键词筛选

```text
/node list 香港
/node list premium
/node list vmess
```

关键词会同时匹配节点名称、节点类型和 provider 名称。筛选结果仍显示节点在完整列表中的
全局序号，因此可以直接用于 `/node use <序号>`。

### 测试延迟

```text
/node test
```

结果按延迟从低到高排序。延迟仅表示测试 URL 的连接表现，不完全等同于实际业务速度。

结果排除 DIRECT、AUTO 等策略项，最后给出通过/未通过数量；失败项明确显示“未通过”，
不沿用上次延迟。订阅可能包含流量/到期提醒占位节点，它们也可能被计入解析数量。
Web 节点页保留本次测速统计，窄屏也能看到延迟与检查状态。

### 选择节点

```text
/node use
/node use <序号>
/node use <完整名称>
```

示例：

```text
/node use 3
/node use "香港 Premium 01"
```

不带参数时，CLI 会先列出节点，再提示输入序号或完整名称。

---

## 12. 运行模式与路由配置

### 12.1 三种 Mihomo 基础模式

```text
/mode rule
/mode global
/mode direct
```

| 模式 | 含义 |
| --- | --- |
| `rule` | 按当前路由配置中的规则决定流量去向 |
| `global` | 全部使用代理组 `PROXY` |
| `direct` | 全部直连 |

`/mode global` 会同步选中 `global` 路由配置，`/mode direct` 会选中 `direct`。从这两种模式
切回 `/mode rule` 时会恢复基础 `rule` 配置；若当前已是 `bypass-cn` 等规则配置，则保持它。

### 12.2 内置路由配置

| 配置 | 默认动作 | 规则组 | 用途 |
| --- | --- | --- | --- |
| `global` | proxy | 无 | 全局代理，同时使用 Mihomo global 模式 |
| `direct` | direct | 无 | 全部直连，同时使用 Mihomo direct 模式 |
| `rule` | proxy | 无 | 基础规则模式，未命中时走代理 |
| `bypass-cn` | proxy | 中国大陆直连 | 中国大陆及私有网络直连，其他代理 |
| `proxy-only` | direct | 指定地址代理 | 只有指定地址走代理，其他直连 |
| `bypass-list` | proxy | 指定地址直连 | 指定地址直连，其他走代理 |

### 12.3 查看和切换路由配置

```text
/route profile list
/route profile use <名称>
```

示例：

```text
/route profile use bypass-cn
/route profile use proxy-only
```

星号 `*` 表示当前活动配置。切换后，运行中的 Core 会安全重载。

### 12.4 创建自定义路由配置

```text
/route profile create <名称> --default proxy|direct|reject [--groups 规则组1,规则组2]
```

示例：

```text
/route profile create work-only --default direct --groups 工作代理
/route profile create strict --default reject --groups "允许直连,允许代理"
```

`--default` 是所有规则都未命中时的动作：

- `proxy`：走 `PROXY` 代理组。
- `direct`：直接连接。
- `reject`：拒绝连接。

### 12.5 给路由配置挂载或移除规则组

```text
/route profile attach <配置> <规则组>
/route profile detach <配置> <规则组>
```

规则组的挂载顺序就是匹配优先级。新挂载的组追加在最后。

### 12.6 删除路由配置

```text
/route profile remove <名称>
```

当前活动配置不能删除，必须先切换到另一个配置。

---

## 13. 规则组与路由规则

### 13.1 查看规则组

```text
/route group list
```

### 13.2 创建规则组

```text
/route group create <名称>
```

示例：

```text
/route group create 工作代理
/route group create 广告拒绝
```

### 13.3 删除规则组

```text
/route group remove <名称>
```

如果规则组仍被任一路由配置引用，必须先执行 `profile detach`，否则不能删除。

### 13.4 查看组内规则

```text
/route rule list <规则组>
```

规则序号同时代表匹配优先级。Mihomo 从上到下处理，第一条命中后停止。

### 13.5 添加规则

```text
/route rule add <规则组> <proxy|direct|reject> <类型> <值>
```

支持的规则类型：

| 类型 | 示例值 | 说明 |
| --- | --- | --- |
| `domain` | `api.example.com` | 完整域名 |
| `domain-suffix` | `example.com` | 域名及其子域名 |
| `domain-keyword` | `google` | 域名包含关键词 |
| `domain-wildcard` | `*.example.com` | 域名通配符 |
| `domain-regex` | `^api\\..*` | 域名正则表达式 |
| `ip-cidr` | `192.168.0.0/16` | IPv4 网段 |
| `ip-cidr6` | `2001:db8::/32` | IPv6 网段 |
| `geoip` | `cn` | GeoIP 国家/区域代码 |
| `geosite` | `cn` | GeoSite 分类 |
| `process-name` | `chrome.exe` | 进程文件名 |
| `process-path` | `C:\\App\\app.exe` | 完整进程路径 |
| `dst-port` | `443` | 目标端口 |

动作：

| 动作 | Mihomo 目标 |
| --- | --- |
| `proxy` | `PROXY` |
| `direct` | `DIRECT` |
| `reject` | `REJECT` |

示例：

```text
/route rule add 指定地址代理 proxy domain-suffix openai.com
/route rule add 指定地址代理 proxy domain-suffix chatgpt.com
/route rule add 指定地址直连 direct domain-suffix example.cn
/route rule add 广告拒绝 reject domain-keyword ads
/route rule add 工作代理 proxy ip-cidr 203.0.113.0/24
```

规则组名称包含空格时要加引号：

```text
/route rule add "工作代理" proxy domain-suffix github.com
```

### 13.6 删除规则

```text
/route rule remove <规则组> <序号>
```

示例：

```text
/route rule list 指定地址代理
/route rule remove 指定地址代理 2
```

### 13.7 常用路由方案

只代理 OpenAI，其他全部直连：

```text
/route rule add 指定地址代理 proxy domain-suffix openai.com
/route rule add 指定地址代理 proxy domain-suffix chatgpt.com
/route profile use proxy-only
```

指定网站直连，其他全部走代理：

```text
/route rule add 指定地址直连 direct domain-suffix example.cn
/route profile use bypass-list
```

中国大陆直连，其他走代理：

```text
/route profile use bypass-cn
```

指定进程走代理，其他直连：

```text
/route rule add 指定地址代理 proxy process-name telegram.exe
/route profile use proxy-only
```

---

## 14. 代理端口、LAN 与 TUN

### 14.1 查看或修改混合代理端口

```text
/port
/port <1-65535>
```

示例：

```text
/port 7890
```

这是同一个端口上的 HTTP + SOCKS5 混合代理。Core 正在运行时会重新加载。

### 14.2 LAN 访问

```text
/lan
/lan status
/lan on
/lan off
/lan enable
/lan disable
```

开启后，Mihomo 允许局域网设备访问代理端口。还需要：

- 使用主机的局域网 IP，而不是 `127.0.0.1`。
- 放行操作系统防火墙端口。
- 只在可信局域网使用。

`/lan on` 只控制代理端口，不会自动让 Web 页面监听公网地址。

### 14.3 TUN 模式

```text
/tun
/tun status
/tun on
/tun off
/tun enable
/tun disable
```

TUN 会接管不支持手动代理的应用，并修改系统路由和 DNS。通常需要：

- Windows：管理员权限。
- macOS：系统网络扩展/管理员授权。
- Linux：root 或 `CAP_NET_ADMIN`，并确保 TUN 设备可用。

远程服务器上启用 TUN 前应准备第二条管理连接，错误路由可能中断 SSH。

---

## 15. 配置、诊断和日志

### 查看公开运行配置

```text
/config
/config show
```

显示 Web 地址、代理端口、模式、LAN、TUN、活动路由配置、下载代理和重试次数。不会显示
订阅密码、控制密钥或 Web Token。

### 验证环境

```text
/doctor
/config validate
```

检查项目包括：

- Mihomo 是否已安装、路径是否存在。
- 是否配置订阅。
- Controller 端口是否可用或由当前 Core 占用。
- 混合代理端口是否可用或由当前 Core 占用。
- TUN 权限提示。

### 查看日志

```text
/logs
/logs <行数>
```

默认显示最近 80 行：

```text
/logs 200
```

日志缓存在内存中并做长度限制；常见 URL 中的 `token=`、`password=`、`secret=` 会脱敏。

---

## 16. Web 控制服务

CLI 和 Web 页面共用本地 HTTP API。进入交互模式或执行大多数普通命令时，如果本地控制
服务没有运行，Kivo 会自动在后台启动它。

### 查看状态

```text
/web
/web status
```

### 显示登录 Token

```text
/web --show-token
/web token
```

Token 属于敏感凭据，不要发送到聊天、工单或公开截图。

### 启动、停止和重启

```text
/web start
/web on
/web stop
/web off
/web restart
```

关闭 Web 服务会先恢复受管系统代理，再停止当前 Mihomo 子进程；保留 Core/连接的自动恢复
偏好。下次启动可能恢复连接。明确取消自动恢复并完全退出时使用 `/shutdown`。

Web 停止时，其他常规管理命令无法访问 API；系统代理 `status/off/recover` 与 `/disconnect`
仍可离线操作，且不会偷偷启动后台或内核。

### Web 页面功能

默认访问：

```text
http://127.0.0.1:9099
```

新版页面使用统一侧栏、亮暗主题与手机抽屉导航。详细状态含义和设计说明见[新版 Web 控制台](WEB_DESIGN.md)。

页面包括：

- 概览：总览结论、系统接入、Core、节点、端口、模式和平台；点击“检测外网”检查三条路径，点击“接入 / 停用指引”查看平台步骤。
- “连接代理 / 断开代理”自动控制系统接入；系统代理区提供仅接入、仅恢复、异常备份恢复和展开后的仅内核操作。
- 检测结果显示测试时间、每项耗时及失败原因；过期和后台失联时不会保留绿色成功结论。刷新页面状态不会自动执行外网检测。
- 节点：按关键词、订阅、状态筛选，按延迟 / 名称排序，每页 24 个节点，测速与选择。
- 订阅：添加、启停、删除、切换分组；单个、当前分组或全部订阅均可选“直连”或“当前代理”进行更新和检查。
- 内核：安装最新版、查看版本、切换、删除。
- 路由：切换配置、添加和删除规则。
- 诊断：查看汇总结论、检查数量与逐项处理建议。
- 日志：查看最近 Core 输出，支持搜索、复制及本地导出，自动刷新不会打断旧日志阅读。
- 设置：端口、模式、LAN、TUN、下载代理、下载重试、Web Token；未保存的网络设置离开前会提醒。

### 设置或关闭 Web Token

进入“偏好设置 → 控制台安全 → Web 访问权限”：

1. 输入任意长度的新 Token。
2. 点击“保存访问设置”。
3. 新 Token 对下一次请求立即生效，无需重启。

输入框留空并保存表示关闭 Token 鉴权。关闭后，任何可以访问 Web 地址的设备都拥有完整管理
权限，只能在本机或完全可信的隔离网络使用。

浏览器只把 Token 保存到当前标签会话的 `sessionStorage`；关闭标签页后需要重新输入。

### 前台运行控制服务

```text
kivo serve
kivo serve --listen 127.0.0.1:9099
kivo serve --listen 0.0.0.0:9099
```

`serve` 会占用当前终端，适合调试或交给 systemd、launchd、Windows 服务包装器管理。
`--listen` 只覆盖本次进程的 Web 监听地址，不修改 `config.json`。

### 停止后台控制服务

```text
kivo daemon stop
```

它等价于普通子命令 `kivo web stop`。

---

## 17. 退出命令区别

| 命令/操作 | 退出 CLI | 停止 Core | 关闭 Core 自动启动 | 停止 Web/API |
| --- | ---: | ---: | ---: | ---: |
| `/quit`、`/q`、`/exit` | 是 | 否 | 否 | 否 |
| `Ctrl+C`、`Ctrl+D` | 是 | 否 | 否 | 否 |
| `/core stop` | 否 | 是 | 是 | 否 |
| `/disconnect` | 否 | 是 | 是 | 否 |
| `/web stop` | 否 | 当前子进程随服务退出 | 否 | 是 |
| `/shutdown` | 是 | 是 | 是 | 是 |

停止内核/后台前会恢复本程序管理的系统代理。`/system-proxy off` 只恢复代理，保持内核和
Web 运行，但取消下次自动接入；`/quit` 与交互 CLI 的 Ctrl+C/D 不改变后台连接。
前台 `serve` 的 Ctrl+C 是关闭后台，会尝试恢复代理。强制结束进程/断电无法即时清理，
下次启动或离线 `/system-proxy recover` 使用持久备份恢复。

日常只想关闭终端但保持代理运行，使用：

```text
/quit
```

想全部停止并退出，使用：

```text
/shutdown
```

---

## 18. 自定义数据目录

全局参数：

```text
--data-dir <目录>
--data-dir=<目录>
```

它可以放在命令任意位置：

```text
kivo --data-dir D:\KivoData
kivo status --data-dir D:\KivoData
kivo serve --data-dir /srv/kivo --listen 127.0.0.1:9099
```

不同数据目录拥有彼此独立的配置、Token、Core、端口和订阅。若同时启动多个实例，必须为
它们配置不同的 Web、Controller 和代理端口。

---

## 19. 默认数据目录与文件用途

默认根目录：

| 系统 | 默认目录 |
| --- | --- |
| Windows | `%AppData%\Kivo` |
| macOS | `~/Library/Application Support/Kivo` |
| Linux | `$XDG_CONFIG_HOME/Kivo`，通常是 `~/.config/Kivo` |

以上为新安装的位置。旧用户若仅有 `ProxyPilot/config.json`，程序继续使用原目录，
避免丢失订阅、内核路径和系统代理备份；如果两处均有配置则优先 Kivo。
`--data-dir` 始终优先，可明确选择已有实例。不会自动复制或删除原目录。

Windows 常见实际路径：

```text
%APPDATA%\Kivo
```

目录结构：

```text
Kivo/
├─ config.json
├─ cores/
│  └─ mihomo/
│     └─ <版本>/
│        └─ mihomo 或 mihomo.exe
├─ downloads/
│  └─ <官方安装包名>.part
├─ runtime/
│  ├─ mihomo/
│  │  ├─ config.yaml
│  │  └─ proxy_providers/
│  └─ node-snapshots/
└─ logs/
   ├─ controller.log
   └─ daemon.log
```

| 路径 | 何时生成 | 用途 |
| --- | --- | --- |
| `config.json` | 第一次运行 | 主配置、Web Token、订阅、凭据、路由和活动内核 |
| `cores/` | 第一次运行创建目录；安装时写入 | 保存多个受管 Mihomo 版本 |
| `downloads/` | 第一次运行创建目录；下载时写入 | 保存可断点续传的 `.part` 文件 |
| `runtime/mihomo/config.yaml` | Core 启动/重启/预检时 | 由 Kivo 生成的 Mihomo 运行配置 |
| `runtime/mihomo/proxy_providers/` | Mihomo 加载订阅时 | provider 缓存 |
| `runtime/node-snapshots/` | 成功更新订阅时 | 离线节点元信息；不含节点密码，离线时不展示过期在线状态 |
| `logs/controller.log` | Web/API 服务启动时 | 控制服务日志 |
| `logs/daemon.log` | 后台服务拉起时 | 后台进程启动输出 |

不要同时手动编辑 `runtime/mihomo/config.yaml` 和使用 Kivo。该文件会在下次生成配置时被
覆盖。需要永久修改的内容应通过 CLI、Web 或 `config.json` 的受支持字段完成。

`config.json` 包含 Web Token、Controller 密钥、订阅 URL 和认证凭据，应限制文件访问权限，
不要提交到 Git。

---

## 20. 远程访问 Web 页面

默认只监听 `127.0.0.1`，外部设备不能访问。可信局域网临时访问可以：

```text
kivo web stop
kivo serve --listen 0.0.0.0:9099
```

然后访问：

```text
http://主机局域网IP:9099
```

还需要放行防火墙的 TCP 9099 端口。

安全要求：

- 非回环监听时强烈建议设置足够长且随机的 Web Token。
- Bearer Token 不能替代 HTTPS。
- 不要把 9099 直接映射到互联网。
- 跨公网使用时，应放在 VPN、SSH 隧道或有 HTTPS 和访问控制的反向代理后面。
- Web 是否可远程访问由 `serve --listen` 决定。
- 代理端口是否允许局域网设备访问由 `/lan on` 决定。
- 这两个开关互不等价。

SSH 隧道示例：

```bash
ssh -L 9099:127.0.0.1:9099 user@server
```

随后在本机访问 `http://127.0.0.1:9099`。

---

## 21. 普通子命令完整示例

下面命令适合脚本或不进入交互界面的场景：

```text
kivo status
kivo core status
kivo core list
kivo core install v1.19.31
kivo core start
kivo core stop
kivo core restart
kivo sub add https://example.com/sub
kivo sub list
kivo sub update all
kivo node list 香港
kivo node test
kivo node use 3
kivo mode rule
kivo route profile use bypass-cn
kivo port 7890
kivo tun on
kivo lan off
kivo doctor
kivo logs 200
kivo web status
kivo web start
kivo web restart
kivo web stop
kivo shutdown
```

普通子命令的返回码：成功为 `0`，失败为非零，并在标准错误输出显示 `Kivo: ...`。

---

## 22. HTTP API 速查

API 基础地址：

```text
http://127.0.0.1:9099/api/v1
```

启用 Web Token 时，除健康检查外，请求需要：

```text
Authorization: Bearer <Token>
```

成功响应统一为：

```json
{"data": {}}
```

错误响应统一为：

```json
{"error":{"code":"operation_failed","message":"可读错误信息"}}
```

### 全部 API

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/health` | 无鉴权健康检查 |
| POST | `/session/verify` | 验证当前 Bearer Token |
| POST | `/connection/connect` | 启动内核、接入系统代理；`replace/adopt/skipCheck` |
| POST | `/connection/disconnect` | 恢复受管系统代理并停止内核 |
| GET | `/system-proxy` | 只读接入/接管/备份状态，不返回原设置 |
| POST | `/system-proxy/on` | 内核已运行时接入，支持 `replace/adopt` |
| POST | `/system-proxy/off` | 恢复受管代理，不停止内核 |
| POST | `/system-proxy/recover` | 安全恢复异常备份；可显式 `force` |
| GET | `/overview` | 聚合状态 |
| POST | `/connectivity/check` | 手动 HTTPS 连通性检测；固定目标，无自定义目标参数 |
| GET | `/proxy/setup` | 所在主机的只读接入/停用指引 |
| GET | `/subscriptions` | 订阅列表，敏感内容脱敏 |
| POST | `/subscriptions` | 添加订阅 |
| PATCH | `/subscriptions` | 修改订阅 |
| DELETE | `/subscriptions?name=<序号或名称>` | 删除订阅 |
| POST | `/subscriptions/update` | 更新单个、全部或指定分组，并可指定 `direct/proxy` |
| POST | `/subscriptions/test` | 健康检查一个订阅或 `all` |
| GET | `/subscription-groups` | 查看订阅分组 |
| POST | `/subscription-groups` | 创建订阅分组 |
| DELETE | `/subscription-groups?name=<名称>` | 删除订阅分组 |
| POST | `/subscription-groups/action` | `use`、`enable` 或 `disable` 分组 |
| GET | `/nodes` | 节点列表 |
| POST | `/nodes/test` | 测试全部节点 |
| POST | `/nodes/select` | 选择节点 |
| GET | `/core/status` | Core 状态 |
| POST | `/core/install` | 安装最新或指定版本 |
| GET | `/core/installations` | 已安装版本 |
| DELETE | `/core/installations?reference=<序号或版本>` | 删除一个版本 |
| DELETE | `/core/installations?all=true` | 完全卸载受管 Core |
| POST | `/core/import` | 导入本地官方包 |
| POST | `/core/use` | 切换活动版本 |
| POST | `/core/start` | 启动 Core |
| POST | `/core/stop` | 停止 Core |
| POST | `/core/restart` | 重启 Core |
| GET | `/routing` | 路由配置和规则组 |
| POST | `/routing/profiles/use` | 切换路由配置 |
| POST | `/routing/profiles` | 创建路由配置 |
| PATCH | `/routing/profiles` | 挂载/移除规则组 |
| DELETE | `/routing/profiles?name=<名称>` | 删除路由配置 |
| POST | `/routing/groups` | 创建规则组 |
| DELETE | `/routing/groups?name=<名称>` | 删除规则组 |
| POST | `/routing/rules` | 添加规则 |
| DELETE | `/routing/rules?group=<组>&index=<序号>` | 删除规则 |
| GET/PATCH | `/settings` | 查询或修改公开运行设置 |
| GET/PATCH | `/web/security` | 查询鉴权状态或设置/清空 Token |
| GET | `/logs?limit=300` | 最近日志，最大 2000 条 |
| GET | `/doctor` | 环境诊断 |
| POST | `/controller/shutdown` | 关闭控制服务 |

### curl 示例

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
  http://127.0.0.1:9099/api/v1/overview
```

添加订阅：

```bash
curl -X POST \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"primary","url":"https://example.com/sub","authType":"none","group":"default","updateInterval":3600,"healthInterval":300,"healthCheckURL":"https://www.gstatic.com/generate_204"}' \
  http://127.0.0.1:9099/api/v1/subscriptions
```

选择节点：

```bash
curl -X POST \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"香港 Premium 01"}' \
  http://127.0.0.1:9099/api/v1/nodes/select
```

---

## 23. 常见故障排查

### 双击 Windows 程序后闪退

使用 PowerShell 启动并保留错误信息：

```powershell
cd E:\程序目录
.\kivo-windows-amd64.exe doctor
.\kivo-windows-amd64.exe
```

检查日志：

```powershell
Get-Content "$env:APPDATA\Kivo\logs\controller.log" -Tail 200
Get-Content "$env:APPDATA\Kivo\logs\daemon.log" -Tail 200
```

### Web 页面打不开

```text
kivo web status
kivo web start
```

检查：

- `config.json` 中 `web.listen` 的端口是否被占用。
- 防火墙是否阻止端口。
- 是否启动了使用同一端口的另一个 Kivo 数据目录。
- 查看 `logs/controller.log` 和 `logs/daemon.log`。

### Web Token 不正确

在本机终端运行：

```text
kivo web --show-token
```

如果在 Web 设置页修改过 Token，旧 Token 会立即失效。Token 已关闭时无需登录。

### 安装报 HTTP 416、Windows ZIP 无法解压、看不到下载进度

旧版可能把已经完整下载的 `.part` 当成未完成文件，向服务器请求文件末尾之后的字节，
导致 HTTP 416。Windows 官方 ZIP 内部的文件名通常是 `mihomo-windows-amd64-v1.exe`，
不是固定的 `mihomo.exe`，旧版解压匹配也可能失败。v0.2.1 已修复这两处问题：

- 完整缓存与官方大小、SHA256 一致时直接复用，不重复下载。
- 缓存无效或服务器拒绝续传时回退完整下载，校验续传响应范围，避免错误拼接。
- 支持官方 Windows 可执行文件名，固定输出路径；多个候选文件会明确拒绝。
- CLI/Web 实时显示下载百分比、大小、速度，以及查询版本、重试、校验、解压和安装阶段。
- 未知总大小时只显示已下载字节；缓存命中时直接显示校验阶段，不伪造进度。
- CLI 输出重定向时降级为稀疏文本日志；中途断流不显示安装成功。

升级时必须同时更新 CLI 和后台服务。仅关闭窗口或执行 `/quit` 不会关闭旧后台。
可以先在旧 CLI 执行 `/shutdown`（会停止代理和 Web），再打开新版并运行：

```text
/install mihomo
/status
```

也可在 **新版本 CLI** 中执行 `/web restart` 更新后台，再重试安装。此操作会短暂中断代理。
Web 页面需要刷新才能加载新版脚本。仍不能访问 GitHub 时可用 `/core import <官方压缩包路径>`。

### Core 启动故障排查

```text
/core status
/doctor
/logs 200
```

常见原因：

- Core 尚未安装或文件被安全软件删除。
- 混合代理端口或 Controller 端口被占用。
- TUN 权限不足。
- 订阅或自定义路由规则生成的配置未通过 Mihomo 校验。

### 没有节点

依次执行：

```text
/core status
/sub list
/sub group list
/sub update all
/sub test all
/node list
/logs 200
```

确认订阅自身和所属分组均为 `ENABLED`。

当 `/sub update` 显示“订阅已下载，但 Mihomo 未解析出任何节点”时，说明网络请求已完成，
但返回内容不是可用的 Clash/Mihomo provider。使用 `/logs 200` 查看具体解析错误，并检查
订阅是否过期、是否返回登录页，或订阅面板是否支持 Clash.Meta 客户端。

### 更新订阅时报 Controller `connection refused`

旧版本在 Mihomo 未运行时直接调用 `127.0.0.1:19090`，因此会显示底层连接拒绝错误。当前版本的
`/sub update` 会自动识别内核状态：已停止时临时启动并在更新后恢复停止；未安装时提示先执行
`/core install`。如果升级后仍看到该错误，请确认正在运行的是本次发布目录中的新二进制，并执行：

```text
/web restart
/sub update 2 --direct
```

### 更新订阅时显示 `127.0.0.1:19090 ... deadline exceeded`

`127.0.0.1:19090` 是 Kivo 调用 Mihomo 的本地 Controller，不是订阅的出站代理地址。
该错误表示 Mihomo 在控制请求时限内没有完成 provider 刷新，常见原因是订阅服务器响应慢、
DNS 或防火墙阻断。当命令显示“当前下载路径：直连”时，它并没有通过节点代理下载。
可执行：

```text
/sub show <订阅名称>
/sub update <订阅名称> --direct
/logs 200
```

### 节点测速全部失败

- 检查订阅节点是否过期。
- 检查当前网络是否能访问健康检查 URL。
- 检查 DNS、系统时间和防火墙。
- 某些节点不允许访问默认测试地址，但仍可能能访问其他网站。

### `unexpected EOF`

参见第 8 节。优先提高重试次数并配置可用的下载代理；仍失败时下载官方包后 `/core import`。

### macOS CLI 显示错行或每输入一个字符多一行

使用 v0.2.0 或更高版本。交互编辑器会在真实 TTY 中原地重绘，并在重定向输入时自动退回
普通行读取。若仍异常，请提供终端名称、macOS 版本和 `$TERM` 值。

新版候选区固定为 8 行，输入框不会随候选数量上下移动；上下横线会自动适配当前终端宽度。
如果升级后仍显示旧布局，请确认实际启动的是刚替换的新可执行文件，而不是 PATH 中的旧版本。

如果按 `↑` / `↓` 后出现多行重复的 `/status`，说明正在使用包含旧版光标保存/恢复逻辑的
可执行文件。请替换为最新构建；新版只使用基础相对光标移动，可兼容不支持 `CSI s/u` 的
Windows 控制台和第三方终端。

### 端口已被占用

先运行：

```text
/doctor
/config show
```

修改代理端口：

```text
/port 17891
```

Web 或 Controller 端口需要停止相关服务后修改 `config.json`，确保同一数据目录没有两个控制
服务同时运行。

---

## 24. 安全与备份建议

- 不要分享 Web Token、Controller 密钥、订阅 URL、订阅密码或 Token。
- Web 默认只监听回环地址，除非明确需要，不要改为 `0.0.0.0`。
- 开放 LAN 代理端口时同时配置防火墙和可信来源范围。
- 远程 Web 接入必须优先使用 VPN、SSH 隧道或 HTTPS 反向代理。
- `config.json` 的写入采用临时文件加原子替换，避免半写入文件。
- 升级、重装或迁移前，建议在 Core/Web 都停止后备份整个数据目录。
- 恢复时把备份目录作为 `--data-dir` 启动，可先验证再替换正式目录。
- 手动编辑 `config.json` 前停止 Web 服务，并保留原文件副本；字段错误会导致下次启动拒绝加载。

---

## 25. 开发、测试和构建

开发环境要求 Go 1.26 或更高版本。项目运行时代码只依赖 Go 标准库。

```text
go test ./...
go vet ./...
go build -o bin/kivo ./cmd/kivo
```

Windows 构建全部平台：

```powershell
.\scripts\build.ps1 -Version v0.2.7
```

macOS/Linux：

```bash
VERSION=v0.2.7 ./scripts/build.sh
```

构建脚本会：

1. 检查 `gofmt`。
2. 运行全部测试。
3. 运行 `go vet`。
4. 构建 Windows、macOS、Linux 的 AMD64/ARM64 六个平台文件。
5. 生成 `dist/SHA256SUMS`。

---

## 26. 当前能力边界

- 当前内核适配器是 Mihomo；“切换内核”指切换已安装的 Mihomo 版本，不是切换到 sing-box。
- 自动系统代理支持当前用户 Windows 默认连接、macOS 已启用网络服务及 GNOME 会话；不是全系统 VPN，不支持 KDE/无桌面 Linux 的自动设置。
- TUN 能否工作依赖操作系统权限、驱动、网络环境和 Mihomo 平台能力。
- 路由规则支持本文列出的结构化类型；当前 CLI 未提供外部 Rule Provider 管理。
- Web Token 是应用层 Bearer 鉴权，不提供 TLS。
- 配置凭据目前依赖操作系统文件权限保护，尚未接入 Credential Manager、Keychain 或 Secret Service。

这些边界不会影响普通的 Mihomo 安装、订阅、节点、规则代理和本地 Web 管理使用。

---

## 27. 自动系统代理：连接、断开与恢复

v0.2.6 新增自动系统接入，无需每次到 Windows“代理设置”填写地址。CLI 与 Web 共用同一
套逻辑。自动系统代理只影响遵循系统 HTTP/HTTPS 设置的应用，不等于 TUN，也不强制
所有程序经过代理。操作对象始终是服务所在主机，不是远程访问页面的设备。

### 27.1 已配置订阅后的推荐流程

```text
/sub update all --direct
/core start
/node list
/node use 3
/connect
/status
```

`/connect` 本身也会启动内核；上面提前启动是为了先选择节点。默认执行三个步骤：

1. 启动 Mihomo，确认代理端口可连接。
2. 把当前系统设置持久备份后接入 `127.0.0.1:<当前混合端口>`。
3. 检测本机直连、日常入口、选中节点出口，并显示结论。

应同时看“服务运行/正在监听”“系统自动接入”“近期入口与节点检测通过”。只有接入成功但
未检测或检测失败时，不显示外网正常；先 `/proxy check` 或选择其他节点，不必反复安装内核。
正常需要浏览器遵循系统设置；Firefox 的独立配置、代理扩展、组织策略和绕过列表需另行检查。

### 27.2 全部新增命令与参数

| 命令 | 作用 | 是否停止内核 |
| --- | --- | --- |
| `/connect` | 启动内核、接入系统代理、检测外网 | 否 |
| `/connect --no-check` | 只连接，不发送外网检测请求 | 否 |
| `/connect --replace` | 明确允许替换其他代理/PAC；原设置先备份 | 否 |
| `/connect --adopt` | 接管已经手动指向本程序的设置；断开时关闭此手动代理 | 否 |
| `/disconnect` | 恢复受管系统代理，取消自动恢复连接并停止内核 | 是 |
| `/system-proxy` 或 `/system-proxy status` | 查询系统接入、接管及待恢复备份 | 否 |
| `/system-proxy on` | 内核/端口已就绪时只接入；不自动启动内核、不检测外网 | 否 |
| `/system-proxy on --replace` | 只接入，明确允许替换其他代理/PAC | 否 |
| `/system-proxy on --adopt` | 只接管已指向本程序的手动代理 | 否 |
| `/system-proxy off` | 恢复受管系统代理，取消下次自动接入，保留内核 | 否 |
| `/system-proxy recover` | 安全恢复异常退出/未完成事务留下的备份 | 否 |
| `/system-proxy recover --force` | 显式覆盖受管字段的外部修改，恢复备份；保留其他字段 | 否 |

`--replace` 与 `--adopt` 互斥；`--no-check` 只用于 `/connect`。例如：

```text
/connect --replace --no-check
/system-proxy recover --force
```

普通子命令完全对应，不需要交互终端：

```powershell
.\kivo-windows-amd64.exe connect
.\kivo-windows-amd64.exe system-proxy status
.\kivo-windows-amd64.exe disconnect
```

### 27.3 已有手动代理、公司代理或其他客户端

默认不覆盖其他代理或 PAC，连接会解释原因，等待显式 `--replace`。请先退出其他代理客户端
的自动接管功能，避免两个程序相互改写。覆盖前保存原设置，断开时尝试恢复。

你之前已经手动填了 `127.0.0.1:17890`，但本程序没有接管记录时，使用：

```text
/connect --adopt
```

这是明确的接管选择：由于无法知道你手动填写前是什么配置，断开时关闭当前手动代理，
而不是声称能还原未知历史。没有显式接管记录时，`/system-proxy off` 不会关闭你手工或
其他软件配置的代理；它只报告“保留现有系统设置”。

重复 `/connect` 不会用本程序自己的设置覆盖原始备份。修改受管混合端口时先恢复原设置，
内核重新启动并监听新端口后再接入。新端口/重启失败时返回错误，不继续指向已知死端口。

### 27.4 状态和网页操作

- **自动接入 / 自动管理**：系统指向本程序且有有效备份，本程序负责恢复。
- **手动接入 / 未接管**：指向本程序但没有受管备份；停止时不会擅自删除手工设置。
- **未接入**：系统关闭或指向其他代理；内核即使运行，浏览器也不一定经过它。
- **备份待恢复**：异常退出、恢复未完成或外部修改；先安全恢复，不能直接覆盖旧备份。
- **外网待检测 / 未通过**：接入配置不等于联网，须查看检测路径及失败原因。

网页主按钮是“连接代理 / 断开代理”，不再只是启动/停止内核。系统代理区提供仅接入、仅
恢复、恢复备份等按钮；存在其他代理或已手工指向本程序时弹出说明及确认框。可取消勾选
“连接后检测外网”以跳过检测，之后手动检测。展开“只管理内核”保留原内核操作入口。
强制恢复只有发生恢复冲突时显示，且有额外风险确认。

### 27.5 安全退出、异常恢复与自动恢复偏好

- `/disconnect`：恢复系统代理 → 停止内核；CLI/Web 保持运行，关闭下次自动连接/启动。
- `/system-proxy off`：只恢复系统设置，内核继续运行；关闭自动接入偏好。
- `/core stop`、`/proxy off`、完全卸载内核：先尝试恢复受管设置，再停止/删除内核。
- `/web stop` 或前台 `serve` 正常退出：恢复受管代理并停止内核，保留下次恢复偏好。
- `/web restart`：先恢复当前设置，再启动后台；按保存偏好恢复连接，不强行覆盖新外部代理。
- `/shutdown`：恢复受管代理、关闭自动启动/连接、停止 Core/Web、退出 CLI。
- `/quit`、交互 CLI 的 Ctrl+C/D：只退出 CLI；代理、内核、Web 保持现状。

恢复操作发生权限/工具/读回校验错误时，不声称成功，不删除备份，也不会因此先停掉仍
可能被系统使用的内核。其他软件修改过受管字段时，普通恢复保留其现状，返回警告并保留
备份；此时内核可以停止，因为不能擅自抢回其他软件的设置。

强制结束进程或断电不能保证即时清理。后台未运行时仍可执行：

```text
kivo system-proxy status
kivo system-proxy recover
```

这两个命令以及 `off`、`disconnect` 的离线路径不会启动 Web/Core，不会为恢复而再次接入。
有冲突且明确不再使用另一代理时才加 `recover --force`。跨用户/不同平台的备份即使强制
也拒绝恢复。离线断开不会按名称强杀其他软件启动的 Mihomo 进程。

首次成功接管后保存 `systemProxy.autoConnect=true`，下次后台启动先处理上次事务，再
尝试自动连接。自动恢复不携带 `--replace/--adopt`，发现新的其他代理时保留它并记录错误。
自动恢复只接入，不自动检测外网；需要 `/proxy check` 更新联网证据。当前不安装开机
启动项或系统服务，自动恢复是“下次启动本程序后台”，不等于自动开机启动。

### 27.6 平台支持与权限

| 平台 | 实现及范围 | 边界 |
| --- | --- | --- |
| Windows AMD64/ARM64 | WinInet 当前用户默认 LAN 连接，设置后通知系统刷新 | 不改 WinHTTP、不枚举 RAS/VPN 连接；通常无需管理员，组织策略可拒绝写入 |
| macOS Intel/Apple Silicon | `networksetup`，所有已启用网络服务的 HTTP/HTTPS、PAC 开关和自动发现 | 保留绕过列表和 SOCKS；含认证或认证状态未知的原 HTTP/HTTPS 代理拒绝覆盖，不能安全备份 Keychain 密码 |
| Linux GNOME | `gsettings`，同一用户的 GNOME 持久设置，需要有效桌面 DBus 会话 | 拒绝 KDE/无桌面/不能确认的会话；组织策略锁定字段会失败；不向父 shell 注入环境变量 |

macOS 写入需要系统允许修改网络设置。若提示权限不足，检查运行账户、管理员权限及
组织策略；不要把 CLI 与后台混用不同用户，也不要用 `sudo` 另起默认数据目录后期待能
恢复原账户备份。Linux 无桌面服务器优先为具体应用指定 HTTP/SOCKS5 代理；TUN 是否适用
需要单独验证权限、路由和 DNS，本功能不会替你开启 TUN。

### 27.7 备份与 API 安全

`config.json` 的 `systemProxy.lease` 保存连接前设置、实际应用目标、受管端口和事务阶段。
先保存 `prepared`，OS 写入并读回校验后标为 `active`；外部修改未恢复时标为 `conflict`。
备份可能包含私有 PAC 地址，应与订阅凭据一样保护；公开 API、网页和 `/config` 不返回
备份内容。配置未恢复前不要删数据目录、删 `lease` 或复制备份到另一个操作系统/账户。

本程序实例之间有同一用户的跨进程设置锁；不控制其他软件的写入。恢复采用逐设置单元的
对比，保留无关字段的修改。操作系统没有与其他客户端共享的原子比较/写入协议，因此仍
建议不要同时运行多个会自动设置系统代理的客户端。

系统代理写 API 与后台关闭 API 必须提交 `Content-Type: application/json`，即使空参数也
发 `{}`；浏览器写请求只允许同源，CLI 无 Origin 可正常调用。开启 Token 时仍需 Bearer
鉴权。关闭 Token 不等于取消来源保护，但能访问本服务的其他本地程序/网络客户端仍可
管理代理；远程管理请设置 Token、访问限制及 HTTPS。

```bash
curl -X POST -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" -d '{"skipCheck":true}' \
  http://127.0.0.1:9099/api/v1/connection/connect

curl -X POST -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" -d '{}' \
  http://127.0.0.1:9099/api/v1/connection/disconnect
```

响应为 `ProxyOperationResult`：`message`、`warning`、脱敏 `systemProxy`，及可选的
`connectivity`。联网失败返回明确警告但保留有效接入，不偷偷回滚；OS 操作失败返回 HTTP
错误，可能同时返回已完成清理的 `data`，调用方不能把部分数据误当全部成功。

### 27.8 从旧版本升级

1. 在旧 CLI 执行 `/shutdown`，确认旧 Core/Web 已退出。
2. 运行 `dist/v0.2.7/` 的对应平台文件，使用原数据目录；不要同时运行旧、新后台。
3. 已手工配置本地代理时 `/connect --adopt`；否则 `/connect`。
4. `/status` 核对自动接入与检测；以后断开用 `/disconnect`，完全退出用 `/shutdown`。

旧版未管理的手动代理不会因升级自动关闭。若旧默认 exe 正在运行而无法覆盖，使用明确
版本目录中的新文件；仅关闭 CLI 窗口不代表旧 Web/Core 已退出。

---

## 28. Kivo 改名升级与简短启动命令

v0.2.7 将 ProxyPilot 统一改名为 **Kivo**。CLI、Web、日志前缀、构建入口与发行文件使用
新名称；交互命令 `/connect`、`/sub`、`/status` 等不变。目录中的历史测试报告和旧版程序
保留原名，避免把曾经使用的版本、文件路径和实测记录误写成新版本。

### 28.1 已有配置怎样保留

- 新安装默认使用系统用户配置目录下的 `Kivo`。
- 如果仅有旧 `ProxyPilot/config.json`，继续使用该数据目录：订阅、密码、内核、缓存、
  Token 和系统代理恢复备份全部沿用，不需要重新添加或重新安装。
- 两个目录都有配置时优先 Kivo；需要另一份配置时明确使用 `--data-dir <目录>`。
- 不自动移动/复制正在使用的数据。尤其不要仅复制 `config.json` 后删除旧目录：其中
  的内核路径可能仍指向旧目录，而且受管系统代理的最初备份也需要保留。
- 与旧版共用系统代理事务锁；保留旧内部认证头的读取兼容，但仍要求原控制密钥。
- Web 在同一浏览器、同一地址下沿用已有登录和主题偏好，保存为新的 Kivo 存储键。

### 28.2 Windows 现在怎样启动

本次发行目录为 `dist/v0.2.7/`。Windows x64 附带短名 `kivo.exe`：

```powershell
cd C:\Tools\Kivo
.\kivo.exe version
.\kivo.exe
```

PowerShell 当前目录的程序要写 `.\kivo.exe`；CMD 可直接输入 `kivo`。将程序所在目录加入
PATH 后，在任意终端均可使用简短命令：

```text
kivo
kivo connect
kivo status
kivo sub update all --direct
kivo disconnect
kivo shutdown
```

Windows ARM64 使用 `kivo-windows-arm64.exe`，可在自己的安装目录将其复制为 `kivo.exe`。
发行目录中附带的短名 `kivo.exe` 对应 Windows AMD64，不能据此混用其他平台版本。

### 28.3 macOS / Linux

Apple Silicon（包括 M2）使用 `kivo-darwin-arm64`；Intel Mac 使用 `kivo-darwin-amd64`。
Linux 使用对应的 `kivo-linux-amd64` 或 `kivo-linux-arm64`。不需要安装 Go。

以 M2 为例，在保存程序的目录执行：

```bash
chmod +x kivo-darwin-arm64
./kivo-darwin-arm64

# 可选：在个人命令目录安装短名，原下载文件仍保留。
mkdir -p "$HOME/.local/bin"
cp kivo-darwin-arm64 "$HOME/.local/bin/kivo"
```

确认 `~/.local/bin` 位于 PATH 后使用 `kivo`。Linux 同样复制自己架构的程序为短名。
如 macOS 提示安全拦截，参照第 2 节核对来源并处理，不建议关闭整个系统安全保护。

### 28.4 正在运行旧后台时

改名后的 CLI 可沿用原配置连接旧控制服务，但旧 Web 页面和后台日志不会在内存中自动
替换为新版本。推荐切换步骤：

1. 在旧终端执行 `/shutdown`，等待停止成功；如果刚重启过 Web，先 `/status` 等待内核
   恢复完成再关闭，避免前次实测发现的启动/关闭互斥报错。
2. 退出旧终端，运行新 `kivo.exe`；已有配置会自动识别。
3. 执行 `/connect`，再 `/status` 确认系统自动接入与当次外网检测。
4. 浏览器重新打开或刷新管理页面，品牌显示为 Kivo。

也可在**新 Kivo CLI** 中执行 `/web restart` 切换后台；它会短暂断开代理，返回后仍需
等待恢复完成并执行 `/proxy check`。本次改名没有顺带修复该生命周期时序问题。

源码目录仍保留 `proxy-pilot`，避免移动正在使用的工作区、便携 Go 工具链和历史产物。
这不影响程序名称或命令；源码模块已经改为 `github.com/itrunswap/Kivo`，构建入口为
`./cmd/kivo`。六个平台产物由 `scripts/build.ps1` / `scripts/build.sh` 统一生成。
