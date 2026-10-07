# Kivo CLI 命令手册

交互模式中命令以 `/` 开头；脚本模式去掉斜杠，例如
`kivo core status`。输入 `/` 或命令前缀会显示候选；候选可见时用 `↑` / `↓` 选择，
空输入时用 `↑` / `↓` 查看当前会话历史，用 `←` / `→` 移动光标，用 `Tab` 补全。

## 状态与生命周期

| 命令 | 说明 |
| --- | --- |
| `/status`、`/st` | 查看 Core、当前节点、代理端口、模式和订阅数 |
| `/core status` | 查看 Core 运行状态、版本、PID 和错误 |
| `/core start\|stop\|restart` | 启动、停止或重启 Core |
| `/proxy on\|off\|restart` | Core 开关的简短别名 |
| `/web [--show-token]` | 查看 Web 地址，可选显示 Token |
| `/web status\|start\|stop\|restart` | 查询或控制 Web/API 服务 |
| `/quit`、`/exit`、`/q` | 只退出当前 CLI，后台保持运行 |
| `/shutdown` | 停止代理和 Core、关闭 Web/API，然后退出 CLI |

## 内核管理

```text
/core list
/core install [latest|v1.19.31]
/core update [latest|版本]
/core import <Mihomo 官方 .gz/.zip 文件>
/core use <序号|版本>
/core remove <序号|版本>
/core purge --yes
```

`use` 在 Core 运行时会自动停止和重启；新版本启动失败会回滚。`remove`
不允许删除当前活动版本，`purge --yes` 会停止 Core 并删除全部受管版本。

官方 Release 下载支持断点续传和自动重试。网络不稳定时可配置：

```text
/config download-retry 6
/config download-proxy http://127.0.0.1:7890
/config download-proxy system
```

`system` 恢复使用系统环境代理。下载被中断后保留 `.part` 文件，下次重试会从已有字节续传；完成后校验 Release 声明的大小和 SHA-256。

## 订阅与分组

```text
/sub add https://example.com/sub
/sub add 机场A https://example.com/sub --group 工作 --prefix "[A] " --via direct
/sub add https://example.com/sub --name 机场A --auth bearer
/sub add "加密订阅" https://example.com/encrypted-sub --auth aes
/sub list [分组]
/sub show <序号|名称>
/sub edit <序号|名称> [--name 值] [--url 地址] [--group 分组] [--prefix 前缀] [--auth 类型] [--user 用户名] [--via direct|proxy]
/sub enable|disable <序号|名称>
/sub move <序号|名称> <分组>
/sub update
/sub update <序号|名称|all> [--direct|--proxy]
/sub update group <分组> [--direct|--proxy]
/sub update --group <分组> [--via direct|proxy]
/sub test
/sub test <序号|名称|all> [--direct|--proxy]
/sub test group <分组> [--direct|--proxy]
/sub remove <序号|名称>
```

认证/加密类型为 `none`、`basic`、`bearer`、`token`、`age` 或 `aes`。密码、Token、
AGE 私钥和 AES 解密密码均使用隐藏输入，不会出现在命令历史中。只传 URL 时会根据域名生成唯一名称。

`/sub update` 不带目标时更新全部活动订阅。`--direct` 与 `--proxy` 分别表示直连下载或通过
当前 `PROXY` 策略组下载，也可写成 `--via direct|proxy`。选定路径会保存到订阅配置，后续
定时更新继续使用。内核停止时，更新命令会临时启动 Mihomo，完成后恢复停止状态。
`/sub test` 使用同样的目标、分组、路径和临时启停规则。

```text
/sub group list
/sub group create 工作
/sub group rename 工作 "工作备用"
/sub group use 工作
/sub group enable|disable 工作
/sub group remove 工作
```

`use` 是独占切换，只启用目标分组；`enable/disable` 可组合多个分组。`rename` 会一起更新组内订阅的引用；默认分组不能重命名或删除，有订阅引用时不能删除其他分组。

## 节点与运行参数

```text
/node list [关键词]
/node test
/node use
/node use <序号|完整名称>
/mode rule|global|direct
/port
/port <1-65535>
/tun status|on|off
/lan status|on|off
```

`/node use` 不带参数时进入编号选择。执行过 `/node list` 或 `/sub list` 后，相关名称会进入当前 CLI 的 Tab 补全缓存。

## 路由配置与规则

内置 `global`、`direct`、`rule`、`bypass-cn`、`proxy-only` 和 `bypass-list`。内置方案及内置规则组不可删除；自定义方案须先切换到其他方案才能删除，仍被方案引用的规则组须先解除关联。旧版本若删掉了内置项，用 `/route restore` 只补齐缺失项，不覆盖自定义规则。

```text
/route profile list
/route restore
/route profile use <名称>
/route profile create <名称> --default proxy|direct|reject [--groups 组1,组2]
/route profile attach|detach <配置> <规则组>
/route profile remove <名称>

/route group list
/route group create <名称>
/route group remove <名称>

/route rule list <规则组>
/route rule add <规则组> <proxy|direct|reject> <类型> <值>
/route rule edit <规则组> <序号> <proxy|direct|reject> <类型> <值>
/route rule move <规则组> <原序号> <目标序号>
/route rule remove <规则组> <序号>
```

可用类型：`domain`、`domain-suffix`、`domain-keyword`、`domain-wildcard`、
`domain-regex`、`ip-cidr`、`ip-cidr6`、`geoip`、`geosite`、`process-name`、
`process-path`、`dst-port`。规则按配置中的“规则组顺序 → 组内顺序”生成，首条命中后停止，
最后自动追加配置的默认动作。

编辑、移动和删除规则会先核对原规则；若 Web、桌面或另一个 CLI 已修改列表，会拒绝过期序号，请重新列出规则再操作。运行中应用新路由前先做内核预检；应用失败会恢复旧持久配置，并在错误中说明是否需要手动恢复内核。

例如“只代理 OpenAI，其他直连”：

```text
/route rule add 指定地址代理 proxy domain-suffix openai.com
/route rule add 指定地址代理 proxy domain-suffix chatgpt.com
/route profile use proxy-only
```

例如“指定站点不走代理，其他都代理”：

```text
/route rule add 指定地址直连 direct domain-suffix example.cn
/route profile use bypass-list
```

## 诊断与设置

| 命令 | 说明 |
| --- | --- |
| `/logs [行数]` | 查看最近 Core 日志 |
| `/doctor`、`/config validate` | 诊断内核、订阅、端口和数据目录 |
| `/config show` | 查看端口、模式、路由、TUN/LAN 和下载设置 |
| `/clear` | 清空终端显示 |
| `/help`、`/?` | 显示快速帮助 |
