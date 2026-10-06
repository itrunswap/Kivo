// 资源级请求序号：旧响应、操作前的响应都不能覆盖新状态。
export class RefreshGate {
  epoch = 0;
  serial = new Map();
  invalidate() {
    this.epoch++;
  }
  begin(key) {
    const serial = (this.serial.get(key) || 0) + 1;
    this.serial.set(key, serial);
    return { key, serial, epoch: this.epoch };
  }
  current(ticket) {
    return (
      ticket.epoch === this.epoch &&
      ticket.serial === this.serial.get(ticket.key)
    );
  }
}

// 命令只使相关资源失效，读取日志和联网检查无需加载路由、Token 或内核版本。
export function affectedResources(path) {
  if (path.includes("/web/security")) return ["security"];
  if (path.includes("/connection/") || path.includes("/system-proxy/"))
    return ["overview", "nodes"];
  if (path.includes("/connectivity/")) return ["overview"];
  if (path.includes("/nodes/select")) return ["overview"];
  if (path.includes("/nodes/test")) return ["nodes", "overview"];
  if (path.includes("/subscriptions") || path.includes("/subscription-groups"))
    return ["subs", "groups", "nodes", "overview"];
  if (path.includes("/routing")) return ["routing", "settings", "overview"];
  if (path.includes("/settings"))
    return ["settings", "overview", "nodes", "routing"];
  if (path.includes("/core/")) return ["overview", "nodes", "versions"];
  return ["overview"];
}
