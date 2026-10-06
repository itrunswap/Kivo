// 纯状态派生规则独立于 DOM，供 Windows/macOS/Linux 共用并进行单元测试。
export const modes = {
  rule: "规则模式",
  global: "全局代理",
  direct: "全部直连",
};
export const coreStates = {
  running: "运行中",
  stopped: "已停止",
  not_installed: "未安装",
  starting: "启动中",
  stopping: "停止中",
  failed: "启动失败",
};

export function fresh(report, now = Date.now()) {
  if (!report || report.stale) return false;
  const checked = Date.parse(report.checkedAt);
  return (
    Number.isFinite(checked) && checked <= now + 5000 && now - checked <= 120000
  );
}

export function connectionView(overview, online = true, now = Date.now()) {
  if (!online || !overview)
    return {
      title: "后台未连接",
      detail: "状态尚未确认，请重新连接后台",
      on: false,
      tone: "error",
      check: "状态未知",
    };
  const running = overview.core?.state === "running";
  const attached = overview.systemProxy?.state === "this_app";
  const port = !!overview.proxyPortListening;
  const report = overview.connectivity;
  const entry = report?.routes?.find((item) => item.id === "entry");
  const verified = fresh(report, now) && entry?.state === "ok";
  const on = running && attached && port;
  let title = on
    ? "已连接"
    : attached
      ? "代理入口异常"
      : running
        ? "仅内核运行"
        : "未连接";
  let detail = attached
    ? "系统代理已接入"
    : overview.systemProxy?.state === "other"
      ? "系统代理由其他程序配置"
      : overview.systemProxy?.state === "automatic"
        ? "系统正在使用自动代理（PAC）"
        : "系统代理未接入";
  if (on) detail += verified ? " · 最近联网检测通过" : " · 请检测联网";
  if (on && overview.core?.mode === "direct") {
    title = "已接入 · 直连模式";
    detail += " · 不经代理节点";
  }
  if (attached && (!running || !port))
    detail += " · 可能无法上网，请断开或恢复代理";
  if (overview.systemProxy?.recoveryPending) detail += " · 存在未恢复备份";
  if (overview.systemProxy?.supported === false)
    detail = "当前桌面环境不支持系统代理接入";
  else if (overview.systemProxy?.state === "unknown") {
    title = "接入未确认";
    detail = "无法确认系统代理配置，请查看环境诊断";
  }
  if (overview.tunEnabled && !attached)
    detail += " · 已配置 TUN，实际接管状态需另行验证";
  const check = !report
    ? "尚未检测"
    : !fresh(report, now)
      ? "结果已过期"
      : verified
        ? "检测通过"
        : entry?.state === "partial"
          ? "部分通过"
          : entry?.state === "skipped"
            ? "未执行入口检测"
            : "检测未通过";
  return {
    title,
    detail,
    on,
    tone:
      verified && on
        ? "good"
        : attached && !on
          ? "error"
          : on
            ? "active"
            : "idle",
    check,
  };
}

export function groupNodes(nodes, subscriptions, keyword = "") {
  const query = keyword.trim().toLocaleLowerCase();
  const groups = new Map();
  for (const sub of subscriptions || [])
    groups.set(sub.name, {
      name: sub.name,
      label: sub.group || "默认分组",
      enabled: sub.enabled,
      nodes: [],
    });
  for (const node of nodes || []) {
    const key = node.providerName || "其他节点";
    if (query && !`${node.name} ${key}`.toLocaleLowerCase().includes(query))
      continue;
    if (!groups.has(key))
      groups.set(key, { name: key, label: "", enabled: true, nodes: [] });
    groups.get(key).nodes.push(node);
  }
  return [...groups.values()].filter(
    (group) => group.nodes.length > 0 || !query,
  );
}

export function nodeLatency(node, running) {
  if (node.cached || !running)
    return {
      text: node.delay ? `${node.delay} ms · 缓存` : "离线缓存",
      tone: "",
    };
  if (!node.tested) return { text: "未测速", tone: "" };
  if (!node.alive || !node.delay) return { text: "超时", tone: "bad" };
  return { text: `${node.delay} ms`, tone: node.delay < 300 ? "good" : "" };
}

export function selectedNode(overview, node) {
  return !!overview && overview.core?.currentNode === node.name;
}

export function updateSummary(result) {
  const items = result?.results || [];
  if (!items.length) return "后台未返回订阅统计，请查看日志";
  return items
    .map(
      (item) =>
        `${item.name}：${item.error ? "失败 · " + item.error : item.nodeCount == null ? "已更新，节点数量未知" : `已解析 ${item.nodeCount} 个节点`}（${item.via === "proxy" ? "代理" : "直连"}）`,
    )
    .join("\n");
}
