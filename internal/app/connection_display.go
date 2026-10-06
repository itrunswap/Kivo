package app

import "time"

// ConnectionDisplay 是页面和托盘共用的展示契约，避免各端把检测失败解释成已联网。
type ConnectionDisplay struct {
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	Tone          string `json:"tone"`
	Check         string `json:"check"`
	On            bool   `json:"on"`
	CanConnect    bool   `json:"canConnect"`
	CanDisconnect bool   `json:"canDisconnect"`
}

// DisplayConnection 只解释已有证据，不触发外网检测；时间由调用方注入以便测试。
func DisplayConnection(o Overview, now time.Time) ConnectionDisplay {
	running, attached := o.Core.State == "running", o.SystemProxy.State == "this_app"
	v := ConnectionDisplay{Title: "未连接", Detail: "系统代理未接入", Tone: "idle", Check: "尚未检测", On: running && attached && o.ProxyPortListening}
	if running {
		v.Title = "仅内核运行"
	}
	if o.SystemProxy.State == "other" {
		v.Detail = "系统代理由其他程序配置"
	}
	if o.SystemProxy.State == "automatic" {
		v.Detail = "系统正在使用自动代理（PAC）"
	}
	if attached {
		v.Detail = "系统代理已接入"
	}
	if v.On {
		v.Title, v.Tone = "已连接 · 未检测", "active"
	}
	if attached && !v.On {
		v.Title, v.Tone, v.Detail = "代理入口异常", "error", "系统代理指向未就绪的入口，请断开或恢复"
	}
	if o.Core.State == "not_installed" {
		v.Title = "内核未安装"
	}
	if o.Core.State == "failed" {
		v.Title, v.Tone = "内核异常", "error"
	}
	if o.Core.State == "starting" || o.Core.State == "stopping" {
		v.Title, v.Tone = "内核正在切换", "warn"
	}
	if r := o.Connectivity; r != nil {
		valid := !r.Stale && !r.CheckedAt.IsZero() && r.CheckedAt.After(now.Add(-2*time.Minute)) && !r.CheckedAt.After(now.Add(5*time.Second))
		v.Check = "结果已过期"
		if valid {
			entry, node := "skipped", "skipped"
			for _, route := range r.Routes {
				if route.ID == "entry" {
					entry = route.State
				}
				if route.ID == "node" {
					node = route.State
				}
			}
			v.Check = "检测未通过"
			switch entry {
			case "ok":
				v.Check = "入口检测通过"
				if v.On {
					v.Title, v.Tone = "已接入 · 节点待确认", "warn"
				}
				if node == "ok" {
					v.Check = "检测通过"
					if v.On {
						v.Title, v.Tone = "已连接 · 检测通过", "good"
					}
				}
			case "partial":
				v.Check = "部分通过"
				if v.On {
					v.Title, v.Tone = "已接入 · 部分可达", "warn"
				}
			case "failed":
				if v.On {
					v.Title, v.Tone = "已接入 · 联网异常", "error"
				}
			case "skipped":
				v.Check = "未执行入口检测"
			}
		}
	}
	if v.On && o.Core.Mode == "direct" {
		v.Title = "已接入 · 直连模式"
		if v.Tone == "good" {
			v.Tone = "warn"
		}
		v.Detail = "当前流量不经远端代理节点"
	}
	if !o.SystemProxy.Supported {
		v.Detail = "当前环境不支持自动接入系统代理"
	}
	if o.SystemProxy.State == "unknown" {
		v.Title, v.Tone, v.Detail = "接入未确认", "error", "请查看环境诊断"
	}
	if o.SystemProxy.RecoveryPending {
		v.Title, v.Tone = "系统代理待恢复", "warn"
		v.Detail = "存在未恢复备份，请先在设置中处理"
	}
	if o.TUNEnabled && !attached {
		v.Detail += " · TUN 已配置，接管状态待确认"
	}
	v.CanConnect = o.SystemProxy.Supported && o.SystemProxy.State != "unknown" && !attached && !o.SystemProxy.RecoveryPending && o.Core.State != "not_installed" && o.Core.State != "starting" && o.Core.State != "stopping"
	v.CanDisconnect = running || attached || o.SystemProxy.Managed || o.SystemProxy.RecoveryPending
	return v
}
