# Kivo HTTP API

基础地址默认为 `http://127.0.0.1:9099/api/v1`。默认情况下，除 `GET /health` 外请求必须携带：

```text
Authorization: Bearer <config.json 中的 web.secret>
```

如果用户在 Web 设置页清空 Token，鉴权会立即关闭，后续请求无需此请求头。无 Token 模式
会开放全部管理能力，只适用于本机或可信隔离网络。

普通成功响应统一为 `{"data": ...}`，错误响应统一为（安装的可选流式响应见下）：

```json
{"error":{"code":"invalid_request","message":"可读错误信息"}}
```

## 接口清单

订阅 `update/test` 返回 `results` 数组，每项包含 `name`、`via`、`durationMs`，以及可选的
`nodeCount`、`previousCount`、`updatedAt`、`error`、`warning`。数量省略表示未知，不能当作 0。
部分失败仍使用非 2xx 状态，但同时返回 `data` 与 `error`；客户端必须先读取 `data.results`，
保留已成功的项目，再展示错误。`updated/tested` 仅包含成功项目。

`GET /nodes` 在内核停止时可返回 `cached: true` 的离线节点，`alive` 为 false，延迟被清空。
缓存只供查看，选择和测速需要先启动内核。更新路径不变时不重载；AES 动态应用路径；普通
provider 使用热重载。显式 `proxy` 不依赖全局模式，不可用时返回错误，不隐式退回直连。

`GET /nodes` 的 `tested` 表示存在健康检查历史；仅 `alive: true` 不能证明节点已经测试。
节点按完整名称稳定排序。`POST /nodes/test` 返回名称到延迟毫秒数的映射，排除
DIRECT/AUTO 等策略项；0 表示本次检查未通过，失败项不会被省略。
公开订阅 URL 同时隐藏查询参数、用户信息及常见路径内凭据，不能作为原始订阅 URL 复制使用。

`POST /core/install` 携带 `Accept: application/x-ndjson` 时返回逐行安装事件，每行独立 JSON：

```json
{"stage":"download","message":"正在下载","downloaded":1048576,"total":22460787,"bytesPerSecond":524288}
{"stage":"complete","message":"安装完成"}
```

阶段包括 `prepare/resolve/download/retry/verify/extract/activate/done/complete/error`。
仅收到 `complete` 表示整项操作成功；中途 EOF 不算成功。失败事件包含 `error` 字符串。
在 HTTP 头已经发出后，操作错误通过事件传递，不再修改 HTTP 状态。鉴权失败仍为 401 JSON，
已有安装任务时返回 409 JSON。同一服务串行安装，防止 CLI/Web 同时写入安装缓存。
不发送此 Accept 头时保留 JSON 事件数组响应。

`GET /overview` 新增 `proxyPortListening` 和 `systemProxy: {state, message}`。
`state` 可为 `this_app/other/off/automatic/unknown`；这些字段仅描述服务主机的监听和配置，
不证明互联网、节点或浏览器连通性。TUN 字段仍是配置开关。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 无鉴权存活检查 |
| POST | `/session/verify` | 校验登录密钥 |
| GET | `/overview` | 首页聚合状态 |
| GET/POST/PATCH/DELETE | `/subscriptions` | 查询、新增、修改或按 `?name=` 删除订阅 |
| POST | `/subscriptions/update` | 刷新单个、全部或指定分组，可选择直连或代理更新 |
| POST | `/subscriptions/test` | 执行指定 provider 健康检查；名称可为 `all` |
| GET/POST/DELETE | `/subscription-groups` | 查询、新建或删除订阅分组 |
| POST | `/subscription-groups/action` | `use`、`enable` 或 `disable` 订阅分组 |
| GET | `/nodes` | 节点列表 |
| POST | `/nodes/test` | 测试全部节点延迟 |
| POST | `/nodes/select` | 选择节点 |
| POST | `/core/install` | 安装 Mihomo，可指定版本 |
| GET | `/core/status` | 内核状态、版本、PID、当前节点和端口 |
| GET/DELETE | `/core/installations` | 查询或删除受管版本；`?all=true` 为完全卸载 |
| POST | `/core/import` | 从本地官方 `.gz/.zip` 包导入内核 |
| POST | `/core/use` | 按序号或版本切换活动内核 |
| POST | `/core/start` | 启动内核 |
| POST | `/core/stop` | 停止内核 |
| POST | `/core/restart` | 重启内核 |
| GET | `/routing` | 获取当前路由配置、配置列表和规则组 |
| POST/PATCH/DELETE | `/routing/profiles` | 新建、关联规则组或删除路由配置 |
| POST | `/routing/profiles/use` | 切换并应用路由配置 |
| POST/DELETE | `/routing/groups` | 新建或删除规则组 |
| POST/DELETE | `/routing/rules` | 向规则组添加或按序号删除规则 |
| GET/PATCH | `/settings` | 查询或更新运行设置 |
| GET/PATCH | `/web/security` | 查询鉴权状态，或立即设置/清空 Token |
| GET | `/logs?limit=300` | 查询最近日志，最大 2000 条 |
| GET | `/doctor` | 环境诊断 |
| POST | `/controller/shutdown` | 优雅关闭控制服务 |

## 常见请求

新增无认证订阅：

```json
{
  "name": "primary",
  "url": "https://example.com/subscription",
  "group": "default",
  "authType": "none",
  "updateVia": "direct",
  "updateInterval": 3600,
  "healthInterval": 300,
  "healthCheckURL": "https://www.gstatic.com/generate_204"
}
```

`authType` 可为 `none`、`basic`、`bearer`、`token`、`age` 或 `aes`。Basic 认证还需
`username`；`aes` 的 `secret` 是解密密码，其余方式在 `secret` 中提供密码、Token 或 AGE 私钥。
服务不会在查询接口中回传这些值。AES 解密内容使用仅供 Mihomo 调用的内部端点传递，该端点
不属于公共管理 API，Web Token 也不能代替它的内部密钥。

更新全部活动订阅并通过当前代理下载：

```json
{
  "reference": "all",
  "via": "proxy"
}
```

只更新指定分组并强制直连：

```json
{
  "group": "工作",
  "via": "direct"
}
```

`reference` 可为序号、名称或 `all`，省略时也表示全部；`group` 与单个订阅引用不能同时使用。
`via` 可为 `direct` 或 `proxy`，省略时保留各订阅当前设置。成功响应包含 `updated` 名称数组和
`temporarilyStartedCore`。旧客户端发送的 `{"name":"..."}` 仍然兼容。

选择节点：

```json
{"name":"节点完整名称"}
```

更新设置：

```json
{
  "mixedPort": 17890,
  "mode": "rule",
  "allowLAN": false,
  "tunEnabled": false,
  "downloadProxy": "http://127.0.0.1:7890",
  "downloadRetry": 4
}
```

修改端口、LAN、TUN 或模式时，正在运行的内核会安全重载；未运行的内核不会被自动启动。
