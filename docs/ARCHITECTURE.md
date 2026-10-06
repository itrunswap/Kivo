# Kivo 架构设计

## 1. 设计目标

Kivo 的核心目标是让同一套代理能力同时服务于交互终端、自动化脚本和 Web 页面，
且不把任何 UI 绑定到特定代理内核。实现优先级依次为安全、正确、可恢复、可观测和易扩展。

## 2. 分层与依赖方向

```text
cmd/kivo             组合根、信号处理、后台服务拉起
        │
        ├── internal/cli   斜杠命令和普通子命令
        ├── internal/server Web 静态资源、HTTP API、鉴权
        │          │
        │          └── internal/app 统一业务用例
        │                       │
        │                       └── internal/core.Adapter
        │                                      │
        │                                      └── internal/core/mihomo
        ├── internal/client  CLI 到本地 API 的客户端
        ├── internal/config  配置校验、并发访问、原子持久化
        └── internal/platform 平台相关进程和终端能力
```

依赖只能向下。`cli` 与 `server` 不拼装 Mihomo 配置，`app` 不导入具体内核包，平台差异
不泄漏到业务层。`cmd` 是唯一负责实例组装的包。

## 3. 进程模型

首次执行 CLI 时，主进程先探测本地健康接口。若控制服务不存在，就以脱离终端的方式
启动同一可执行文件的 `serve` 模式。控制服务持有 Mihomo 子进程并通过 Controller API
读取状态或执行节点操作。

退出 CLI 不影响控制服务；`kivo daemon stop` 会关闭控制服务及其当前子进程。
交互终端中的 `/shutdown` 还会先关闭 Mihomo 自动启动并停止代理，再关闭控制服务和 CLI。
用户主动启动代理后 `autoStart` 被保存，下一次控制服务启动会尝试恢复代理；用户主动
停止后该偏好被清除。

## 4. 配置事务

订阅更新不会无条件重启 Core：未改变路径不重载，AES 从应用层动态读取路径，普通 provider
改变路径时通过 `PUT /configs` 热重载。Windows 的验证和启动统一使用无窗口进程；停止时
使用平台支持的终止方式并等待退出回调完成，避免旧回调覆盖新进程状态。

AES 代理下载使用单独的随机回环 HTTP 入口与每次启动重新生成的密钥，固定指向 PROXY。
使用前展开策略组，拒绝 DIRECT、REJECT、空组和循环引用。实现依据
[Mihomo listeners 出站字段](https://wiki.metacubex.one/config/inbound/listeners/#proxy)，
并通过真实内核的回环代理集成测试确认 DIRECT 模式下仍走指定出站。

`ProviderReporter` 提供节点统计，`SubscriptionProxy` 提供内部安全入口，`Reloader` 提供热重载，
都作为内核可选能力，不把 Mihomo 控制 API 泄漏到 CLI/Web。离线快照以来源指纹隔离，
只存元信息；更新结果与节点健康状态分开表达。

`config.Store` 是配置的唯一写入口：

1. 在写锁内复制当前配置。
2. 对副本执行修改。
3. 完整校验下一版本。
4. 写入同目录临时文件并设置仅当前用户可读写。
5. 原子重命名替换正式文件；失败时恢复内存状态。

订阅变更或运行参数变更后，应用层仅在内核正在运行时重载，避免意外启动用户已停止的
代理。Mihomo 启动前先执行配置测试。内核版本切换按“保存旧状态 → 停止 → 切换 →
启动”执行，新版本启动失败时恢复旧配置并尝试恢复运行。

配置 Schema V2 增加订阅分组和路由领域模型。旧 Schema V1 在加载时会原子迁移：订阅归入
`default` 分组，同时生成内置路由配置，不改写现有密钥、端口或订阅凭据。

## 5. 安全边界

- 默认情况下，Web 健康检查之外的接口必须提供 `Authorization: Bearer <token>`。
- Token 使用加密安全随机源生成，以常量时间比较，并支持在设置页立即轮换。
- 用户可显式清空 Token 进入开放模式；该模式不等同于安全访问控制，非回环监听时会记录警告。
- 默认监听地址始终是 `127.0.0.1`，降低误开放风险。
- API 不返回订阅凭据，订阅 URL 的查询参数和用户信息会被隐藏。
- AES 加密订阅由应用层按需下载并在内存中解密；Mihomo 通过独立 Controller 密钥保护的
  本机内部端点读取明文。Web Token 即使关闭，也不会绕过这层内部鉴权。
- 日志缓存有长度上限，写入前对密码、Token 和控制密钥做替换。
- HTTP 服务设置头部读取、请求读取、写入和空闲超时，并限制请求体大小。
- 前端不加载 CDN，不用 `innerHTML` 渲染用户数据，并设置严格 CSP。

当前配置凭据由文件权限保护，不等同于硬件或系统密钥库。企业部署应增加 OS 密钥库
适配，同时为远程 Web 接入提供 TLS 终止、来源限制和审计。

## 6. Web 前端

前端资源通过 `embed.FS` 编译进二进制，无需 Node 构建链。页面使用语义化 HTML、原生
CSS 和 JavaScript，覆盖桌面与 390px 移动视口，支持键盘操作、亮暗主题、减少动画偏好、
错误反馈和空状态。Session Token 仅保存在 `sessionStorage`，关闭标签页后自动清除。

## 7. 扩展新内核

接入新内核需要：

1. 在 `internal/core/<name>` 实现 `core.Adapter`。
2. 将内核配置生成、安装和控制 API 全部限制在该包内。
3. 为配置生成器、状态解析和敏感信息脱敏添加表驱动测试。
4. 在组合根选择适配器，或增加一个 `EngineFactory` 由配置决定实现。

不得让新内核的专有字段进入 CLI/Web API；优先扩展中立的领域模型。如果确实需要特有
能力，应通过 capability 查询暴露，避免 UI 猜测实现类型。

## 8. 后续生产化建议

- 使用 Windows Credential Manager、macOS Keychain、Linux Secret Service 存储凭据。
- 增加签名发布、SBOM、可复现构建和可配置 Release 镜像。
- 将长任务改造成异步 Job API，以便显示实时安装和测速进度。
- 增加配置 schema 迁移器、自动更新、崩溃恢复与操作审计。
- TUN 安装需要平台权限引导和更完整的路由/DNS 回滚测试。
