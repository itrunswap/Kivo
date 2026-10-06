package platform

import (
	"fmt"
	"runtime"
)

// ProxyGuide 给出所在主机的接入/停用说明，读取说明不会修改系统设置。
type ProxyGuide struct {
	Platform string   `json:"platform"`
	Address  string   `json:"address"`
	Port     int      `json:"port"`
	Steps    []string `json:"steps"`
	Disable  string   `json:"disable"`
	Warning  string   `json:"warning"`
}

// SystemProxyGuide 不尝试覆盖系统代理、公司 PAC 或其他客户端的配置。
func SystemProxyGuide(port int) ProxyGuide { return proxyGuideFor(runtime.GOOS, port) }

func proxyGuideFor(system string, port int) ProxyGuide {
	guide := ProxyGuide{Platform: system, Address: "127.0.0.1", Port: port,
		Warning: "仅适用于运行 Kivo 的这台机器。先记下现有代理/PAC 配置；公司管理的设置不要擅自覆盖。远程访问 Web 的设备不能把自己的 127.0.0.1 当成服务器。",
		Disable: "通过 /connect 接管的代理可用 /disconnect 自动恢复并停止内核；/system-proxy off 只恢复系统代理。仅按本指引手动配置而未接管的设置不会自动还原，需先手动恢复再 /core stop。/quit 保持后台；/shutdown 恢复受管代理并停止 Core / Web。",
	}
	switch system {
	case "windows":
		guide.Steps = []string{"先执行 /core start，再执行 /proxy check，确认日常代理入口检测通过。", "打开 Windows 设置 → 网络和 Internet → 代理 → 手动设置代理 → 使用代理服务器。", fmt.Sprintf("填入地址 127.0.0.1、端口 %d，保存。按需排除 localhost、127.0.0.1 和局域网地址。", port), "执行 /status，系统代理应显示已指向 Kivo；浏览器独立代理或扩展需要单独检查。"}
	case "darwin":
		guide.Steps = []string{"先执行 /core start，再执行 /proxy check，确认日常代理入口检测通过。", "打开系统设置 → 网络 → 当前使用的网络服务 → 详细信息 → 代理。", fmt.Sprintf("配置 Web 代理（HTTP）和安全 Web 代理（HTTPS）：地址均为 127.0.0.1，端口均为 %d，然后保存。", port), "执行 /status 核对系统 HTTPS 代理；更换 Wi-Fi/有线网络服务后，需要重新核对。"}
	default:
		guide.Steps = []string{"GNOME 桌面会话可优先 /connect 自动接入；其他环境先 /core start，再 /proxy check 验证。", fmt.Sprintf("KDE/其他桌面或浏览器独立代理中，手动设置 HTTP/HTTPS 代理为 127.0.0.1:%d。", port), "Linux 自动设置仅支持可确认的 GNOME 会话；无桌面/不支持的环境会提示未确认，不冒充已启用。", "仅给终端设置 HTTP_PROXY/HTTPS_PROXY 不一定影响浏览器；无桌面环境请在目标应用中指定代理。"}
	}
	return guide
}
