# 第三方组件与许可声明

## Kivo

项目地址：https://github.com/itrunswap/Kivo

Kivo 自身的开源许可证尚未由项目所有者指定。本文件不是 Kivo 的授权协议，也不会自动将 Kivo 置于下列第三方许可证之下。正式授予使用、修改或再分发权限时，请由所有者补充根目录 `LICENSE`。

## Mihomo

- 项目：https://github.com/MetaCubeX/mihomo
- 许可证：GPL-3.0，原文见 https://github.com/MetaCubeX/mihomo/blob/Meta/LICENSE
- 源码与对应版本：https://github.com/MetaCubeX/mihomo/releases
- 集成方式：Kivo 通过独立进程与本地控制 API 管理 Mihomo。

Kivo 的发行压缩包仅包含 Kivo 程序和中文文档，不捆绑 Mihomo 二进制、用户订阅或用户配置。运行安装命令时，会从 Mihomo 官方 Release 下载所选版本。若自行捆绑或分发 Mihomo，应保留其许可、版权声明及对应源码获取方式，并按实际分发情况履行许可证义务。

## 构建工具

Kivo 使用 Go 标准库与工具链构建；Go 的许可与第三方声明见 https://go.dev/LICENSE 。Go、Node.js 与本地构建缓存不会随 Kivo 发布包分发。使用已编译的 Kivo 不需要安装这些开发工具。
