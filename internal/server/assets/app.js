"use strict";

// 将同一浏览器已有的登录/主题偏好迁移到新名称，不重新生成服务端凭据。
// 浏览器禁止存储时仍可使用内存中的值；写入成功前不删除旧值。
function renamedPreference(storage, key, legacyKey) {
  let value = "";
  try {
    const current = storage.getItem(key);
    if (current !== null) {
      value = current;
      storage.removeItem(legacyKey);
    } else {
      value = storage.getItem(legacyKey) || "";
      if (value) {
        storage.setItem(key, value);
        storage.removeItem(legacyKey);
      }
    }
  } catch { /* 存储权限限制不应阻止控制台加载。 */ }
  return value;
}

const state = {
  token: renamedPreference(sessionStorage, "kivo_token", "proxypilot_token"),
  overview: null,
  nodes: [],
  delays: {},
  activeView: "overview",
  busy: false,
  installing: false,
  checkingConnectivity: false,
  connecting: false,
  authEnabled: true,
  subscriptionGroups: [],
  routing: null,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.token) headers.set("Authorization", `Bearer ${state.token}`);
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...options, headers });
  let payload = {};
  try { payload = await response.json(); } catch (_) { /* 空响应 */ }
  if (response.status === 401) {
    showLogin("密钥已失效，请重新连接。");
    throw new Error("认证失败");
  }
  if (!response.ok) {
    const error = new Error(payload.error?.message || `请求失败（HTTP ${response.status}）`);
    error.data = payload.data; // 保留批量操作已完成的项目，错误不能覆盖部分成功结果。
    throw error;
  }
  return payload.data;
}

function setBusy(button, busy, label = "处理中…") {
  if (!button) return;
  if (busy) {
    button.dataset.label = button.textContent;
    button.textContent = label;
    button.disabled = true;
  } else {
    button.textContent = button.dataset.label || button.textContent;
    button.disabled = false;
  }
}

function toast(message, type = "success") {
  const item = document.createElement("div");
  item.className = `toast ${type}`;
  item.textContent = message;
  $("#toastRegion").append(item);
  setTimeout(() => item.remove(), 4200);
}

function showLogin(message = "") {
  $("#loginError").textContent = message;
  $("#loginOverlay").classList.add("active");
  setTimeout(() => $("#loginSecret").focus(), 30);
}

function hideLogin() { $("#loginOverlay").classList.remove("active"); }

function switchView(name) {
  state.activeView = name;
  const titles = {overview:"网络概览", nodes:"节点管理", subscriptions:"订阅源", cores:"内核版本", routing:"路由策略", diagnostics:"环境诊断", logs:"内核日志", settings:"运行设置"};
  $$(".view").forEach((view) => view.classList.toggle("active", view.id === `view-${name}`));
  $$(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.view === name));
  $("#pageTitle").textContent = titles[name] || "Kivo";
  $(".sidebar").classList.remove("open");
  location.hash = name;
  if (name === "nodes") loadNodes();
  if (name === "subscriptions") loadSubscriptions();
  if (name === "cores") loadCores();
  if (name === "routing") loadRouting();
  if (name === "logs") loadLogs();
  if (name === "settings") loadSettings();
}

function coreRunning() { return state.overview?.core?.state === "running"; }

function systemConnected(data) { return data?.core?.state === "running" && data.proxyPortListening && data.systemProxy?.managed && data.systemProxy.state === "this_app"; }

// 页面访问者可能在另一台电脑；确认框明确说明实际操作的是服务主机。
function proxyConnectOptions(data) {
  const proxy = data.systemProxy || {};
  if (proxy.recoveryPending) { toast("请先安全恢复代理备份；不会自动强制覆盖", "error"); return null; }
  if (!proxy.supported) { toast(proxy.message || "当前平台不支持自动接入，请查看接入指引", "error"); return null; }
  if (proxy.managed) return {};
  if (proxy.state === "this_app") return confirm("服务主机的手动代理已指向本程序。是否接管？断开或正常关闭后台时将关闭此手动代理；此前未知设置无法还原。") ? {adopt:true} : null;
  if (proxy.state === "other" || proxy.state === "automatic") return confirm("服务主机存在其他代理或 PAC。是否暂时替换为 Kivo？原设置会先备份，断开时恢复。") ? {replace:true} : null;
  return proxy.state === "off" ? {} : null;
}

function renderSystemProxy(data) {
  const proxy = data.systemProxy || {};
  const ready = data.core.state === "running" && data.proxyPortListening;
  $("#systemProxyOwnership").textContent = proxy.recoveryPending ? "备份待恢复" : proxy.managed ? "自动管理" : proxy.state === "this_app" ? "手动配置" : "未接入";
  $("#systemProxyOwnership").className = `pill ${proxy.managed && ready ? "online" : ""}`;
  $("#systemProxyExplanation").textContent = proxy.recoveryPending ? "检测到未完成的恢复或外部修改。备份保留，先安全恢复；强制恢复需要额外确认。" : proxy.managed ? "浏览器已接入；断开、停止内核或正常关闭后台时恢复原设置。外网是否可用请查看下方检测。" : proxy.state === "this_app" ? "手动代理指向本程序，尚未接管；点击连接并确认接管后，才由本程序负责恢复。" : proxy.supported ? "点击“连接代理”：启动内核 → 接入系统代理 → 检测外网。无需再到系统设置中填写地址。" : proxy.message || "状态尚未确认，请刷新。";
  $("#systemProxyOn").disabled = state.connecting || !ready || !proxy.supported || proxy.managed || proxy.recoveryPending;
  $("#systemProxyOff").disabled = state.connecting || !proxy.managed;
  $("#systemProxyRecover").hidden = !proxy.recoveryPending;
  $("#systemProxyForce").hidden = !proxy.conflict;
  for (const id of ["#systemProxyRecover", "#systemProxyForce"]) $(id).disabled = state.connecting;
}

async function refreshOverview(silent = false) {
  try {
    state.overview = await api("/api/v1/overview");
    renderOverview(state.overview);
    $("#lastUpdated").textContent = `刚刚同步 · ${new Date().toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"})}`;
    if (!silent) toast("状态已刷新");
  } catch (error) {
    // 后台失联时保留过期的明细供参考，但绝不能继续声称“当前外网正常”。
    if (state.overview) {
      const previous = state.overview;
      state.overview = null;
      renderOverview({...previous, core:{...previous.core,state:"unknown"}, proxyPortListening:false,
        systemProxy:{message:"状态未同步，请检查控制服务"},
        connectivity:previous.connectivity ? {...previous.connectivity,stale:true,staleReason:"控制服务未连接"} : null,
        connection:{level:"error",title:"控制服务未连接 · 当前状态未知",detail:"无法读取最新状态，不代表代理进程已经停止。请检查服务后刷新。",nextCommand:"/web status"}});
      $("#lastUpdated").textContent = "同步失败 · 显示状态未确认";
    }
    $("#heroBadge").className = "badge badge-error";
    $("#heroBadge").textContent = "状态未同步";
    $("#heroNode").textContent = "控制服务未连接 · 当前状态未知";
    $("#heroDescription").textContent = "无法读取最新状态，不代表代理进程已经停止。请检查控制服务后刷新。";
    $("#connectionNext").textContent = "下一步  /web status";
    if (!silent) toast(error.message, "error");
  }
}

function renderOverview(data) {
  const core = data.core;
  const running = core.state === "running";
  const installed = core.state !== "not_installed";
  const failed = core.state === "failed";
  $("#sidebarDot").className = `status-dot ${running ? "online" : failed ? "error" : ""}`;
  $("#sidebarStatus").textContent = core.state === "unknown" ? "状态未同步" : running ? "内核运行中" : failed ? "内核异常" : "内核未运行";
  $("#sidebarCore").textContent = installed ? `Mihomo ${core.version || ""}` : "Mihomo 未安装";
  renderConnectivity(data);
  $("#heroAction").textContent = !installed ? "安装内核" : systemConnected(data) ? "断开代理" : "连接代理";
  $("#quickStartButton").textContent = $("#heroAction").textContent;
  $("#heroAction").disabled = state.connecting || core.state === "unknown";
  $("#quickStartButton").disabled = state.connecting || core.state === "unknown";
  renderSystemProxy(data);
  $("#metricPort").textContent = core.mixedPort || "—";
  const access = data.systemProxy?.state;
  $("#accessStatus").textContent = data.systemProxy?.recoveryPending ? "备份待恢复" : access === "this_app" ? (running && data.proxyPortListening ? data.systemProxy?.managed ? "自动接入 · 已启用" : "手动接入 · 未接管" : "已配置 · 服务未就绪") : ({other:"未接入本程序",off:"系统代理已禁用",automatic:"PAC · 接入待确认"})[access] || "接入状态未确认";
  $("#accessStatus").className = access === "this_app" && running && data.proxyPortListening && !data.systemProxy?.recoveryPending ? "access-ok" : "access-warning";
  $("#accessDetail").textContent = data.systemProxy?.message || "仅检测服务所在主机，浏览器独立设置需单独确认。";
  $("#metricCore").textContent = ({running:"运行中",failed:"异常",starting:"启动中",stopping:"停止中",stopped:"已停止",not_installed:"未安装"})[core.state] || "未知";
  $("#metricVersion").textContent = core.version || "等待安装";
  $("#metricSubscriptions").textContent = data.subscriptionCount;
  $("#metricEnabled").textContent = `${data.enabledCount} 个已启用`;
  $("#metricMode").textContent = (core.mode || "rule").toUpperCase();
  $("#metricTun").textContent = "仅作用于已接入的流量";
  $("#metricPlatform").textContent = data.platform.toUpperCase();
  $("#metricArch").textContent = data.architecture;
  $("#runtimePill").className = `pill ${running ? "online" : ""}`;
  $("#runtimePill").textContent = core.state === "unknown" ? "状态未同步" : running ? "内核运行中" : "内核未运行";
  $("#runtimePID").textContent = core.pid || "—";
  $("#runtimeNode").textContent = running ? (core.effectiveNode || core.currentNode || "未确认") : "—";
  $("#runtimeAddress").textContent = data.webAddress;
  $("#runtimeTun").textContent = data.tunEnabled ? "配置开启（实际接管未验证）" : "关闭";
  $("#runtimeProxyPort").textContent = `${core.mixedPort || "—"} · ${core.state === "unknown" ? "未确认" : data.proxyPortListening ? "正在监听" : "未监听"}`;
  $("#runtimeSystemProxy").textContent = data.systemProxy?.message || "未检测，请更新后台服务";
  $("#runtimeSystemProxy").title = "仅检测运行 Kivo 的主机和用户，不代表访问此页面的设备；浏览器扩展可能覆盖系统设置。";
  if (state.installing) $("#heroAction").textContent = "正在安装…";
  if (state.connecting) { $("#heroAction").textContent = "正在处理代理…"; $("#quickStartButton").textContent = "正在处理代理…"; }
}

async function coreAction() {
  if (state.installing || state.connecting) return;
  if (!state.overview) { toast("请先恢复控制服务连接并刷新状态", "error"); return; }
  const button = $("#quickStartButton");
  const hero = $("#heroAction");
  const installed = state.overview?.core?.state !== "not_installed";
  if (!installed) return installCore(hero);
  const action = systemConnected(state.overview) ? "disconnect" : "connect";
  const options = action === "connect" ? proxyConnectOptions(state.overview) : {};
  if (!options) return;
  if (action === "connect") options.skipCheck = !$("#checkAfterConnect").checked;
  state.connecting = true;
  renderSystemProxy(state.overview);
  setBusy(button, true); setBusy(hero, true);
  try {
    const result = await api(`/api/v1/connection/${action}`, {method:"POST",body:JSON.stringify(options)});
    toast(result.message, result.warning ? "error" : "success");
  } catch (error) { toast(error.message, "error"); }
  finally { state.connecting = false; setBusy(button, false); setBusy(hero, false); await refreshOverview(true); }
}

async function systemProxyAction(action, force = false) {
  if (state.connecting || !state.overview) return;
  const options = action === "on" ? proxyConnectOptions(state.overview) : {force};
  if (!options) return;
  if (force && !confirm("强制恢复会覆盖服务主机上其他程序对受管代理字段的修改，恢复为连接前备份。仅在确认不再使用其他代理时继续。")) return;
  state.connecting = true;
  renderSystemProxy(state.overview);
  try { const result = await api(`/api/v1/system-proxy/${action}`, {method:"POST",body:JSON.stringify(options)}); toast(result.message, result.warning ? "error" : "success"); }
  catch (error) { toast(error.message, "error"); }
  finally { state.connecting = false; await refreshOverview(true); }
}

async function coreOnlyAction(action, button) {
  if (state.connecting) return;
  state.connecting = true; setBusy(button,true);
  try { await api(`/api/v1/core/${action}`,{method:"POST",body:"{}"}); toast("内核操作完成；只有受管系统代理会在停止时恢复"); }
  catch (error) { toast(error.message,"error"); }
  finally { state.connecting = false; setBusy(button,false); await refreshOverview(true); }
}

// 三条检测路径独立呈现。旧结果一律降为中性色，不沿用上一次的绿色结论。
function renderConnectivity(data) {
  const report = data.connectivity;
  const stale = report && (report.stale || !report.checkedAt || Date.now() - Date.parse(report.checkedAt) > 120000);
  const summary = stale && data.core?.state === "running" && data.proxyPortListening
    ? {level:"warning", title:"代理服务已启动 · 外网待检测", detail:"旧结果已失效，请重新检测。", nextCommand:"/proxy check"}
    : data.connection || {level:"warning",title:"外网尚未确认",detail:"请更新后台服务后检测。",nextCommand:"/proxy check"};
  $("#heroBadge").className = `badge badge-${summary.level === "ok" ? "running" : summary.level === "error" ? "error" : summary.level === "warning" ? "warning" : "muted"}`;
  $("#heroBadge").textContent = ({ok:"最近检测通过",error:"需要处理",warning:"需要确认",idle:"尚未就绪"})[summary.level] || "需要确认";
  $("#heroNode").textContent = summary.title;
  $("#heroDescription").textContent = summary.detail;
  $("#connectionNext").textContent = `下一步  ${summary.nextCommand}`;
  $("#connectivityTime").textContent = report
    ? `${stale ? "结果已失效" : "最近检测"} · ${new Date(report.checkedAt).toLocaleString()}${stale ? ` · ${report.staleReason || "超过 2 分钟，请重新检测"}` : " · 2 分钟内有效"}`
    : "尚未检测 · 不会自动发送外网请求";
  const container = $("#connectivityRoutes");
  container.replaceChildren();
  const routes = [{id:"direct",label:"本机直连",hint:"作为对照，不使用 HTTP 代理；系统 VPN / TUN 仍可能影响路由。"}, {id:"entry",label:"日常代理入口",hint:"通过本地代理端口，验证当前模式和规则下的访问。"}, {id:"node",label:"选中节点出口",hint:"固定通过 PROXY 策略组，单独验证远端节点。"}];
  const labels = {ok:"通过",partial:"部分通过",failed:"未通过",skipped:"未检测"};
  for (const plan of routes) {
    const result = report?.routes?.find(route => route.id === plan.id);
    const card = document.createElement("section");
    const resultState = stale ? "stale" : result?.state || "skipped";
    card.className = `connectivity-route is-${resultState}`;
    const title = document.createElement("h4"); title.textContent = plan.label;
    const status = document.createElement("strong"); status.textContent = stale ? "需要重测" : labels[resultState] || "未检测";
    const note = document.createElement("p"); note.textContent = result?.message || plan.hint;
    card.append(title, status, note);
    if (!stale) for (const probe of result?.probes || []) {
      const line = document.createElement("p"); line.className = `probe-line is-${probe.state}`;
      line.textContent = `${probe.target} · ${labels[probe.state] || "未确认"} · ${probe.durationMs} ms${probe.state === "ok" ? "" : ` — ${probe.message}`}`;
      card.append(line);
    }
    container.append(card);
  }
}

async function checkConnectivity() {
  if (state.checkingConnectivity) return;
  state.checkingConnectivity = true;
  const button = $("#checkConnectivityButton");
  setBusy(button, true, "正在检测，约 10–20 秒…");
  try {
    const report = await api("/api/v1/connectivity/check", {method:"POST"});
    const passed = !report.stale && report.routes.filter(route => route.id !== "direct").every(route => route.state === "ok");
    toast(passed ? "检测完成，请查看接入状态与各路径结果" : "检测完成：有未通过或未检测的路径，请查看详情", passed ? "success" : "error");
  } catch (error) { toast(`检测未完成：${error.message}`, "error"); }
  finally { state.checkingConnectivity = false; setBusy(button, false); await refreshOverview(true); }
}

async function showProxyGuide() {
  const button = $("#proxySetupButton");
  setBusy(button, true);
  try {
    const guide = await api("/api/v1/proxy/setup");
    $("#proxyGuideTitle").textContent = `接入指引 · ${guide.platform} · ${guide.address}:${guide.port}`;
    const steps = $("#proxyGuideSteps"); steps.replaceChildren();
    for (const step of guide.steps) { const li = document.createElement("li"); li.textContent = step; steps.append(li); }
    $("#proxyGuideDisable").textContent = `如何停用：${guide.disable}`;
    $("#proxyGuideWarning").textContent = `${guide.warning} 此指引不会修改系统设置。`;
    $("#proxyGuide").hidden = false;
    button.setAttribute("aria-expanded", "true");
    $("#proxyGuide").scrollIntoView({block:"nearest"});
  } catch (error) { toast(error.message, "error"); }
  finally { setBusy(button, false); }
}

async function installCore(button) {
  if (state.installing) return;
  if (!confirm("将从 MetaCubeX 官方 GitHub Release 下载适用于当前平台的最新稳定版 Mihomo。继续吗？")) return;
  state.installing = true;
  setBusy(button, true, "正在安装…");
  $("#installProgress").classList.remove("hidden");
  renderInstallEvent({stage:"prepare", message:"正在连接安装服务…"});
  try {
    await streamInstall();
    toast("Mihomo 安装完成");
    if (state.activeView === "cores") await loadCores();
  } catch (error) {
    renderInstallEvent({stage:"error", message:error.message});
    toast(error.message, "error");
  } finally {
    state.installing = false;
    setBusy(button, false);
    await refreshOverview(true);
  }
}

function formatBytes(value) {
  if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MiB`;
  return `${(value / 1024).toFixed(1)} KiB`;
}

function renderInstallEvent(event) {
  const labels = {prepare:"准备安装", resolve:"查询官方版本", download:"下载安装包", retry:"恢复下载", verify:"校验安装包", extract:"解压安装", activate:"切换版本", done:"安装完成", complete:"安装完成", error:"安装失败"};
  $("#installStage").textContent = labels[event.stage] || "安装内核";
  $("#installDetail").textContent = event.message || "";
  const bar = $("#installBar");
  const percent = $("#installPercent");
  if (event.stage === "download" && event.total > 0) {
    const value = Math.min(100, Math.floor((event.downloaded || 0) * 100 / event.total));
    bar.value = value;
    percent.textContent = `${value}%`;
    $("#installDetail").textContent = `${formatBytes(event.downloaded || 0)} / ${formatBytes(event.total)}${event.bytesPerSecond ? ` · ${formatBytes(event.bytesPerSecond)}/s` : ""}`;
  } else if (["complete", "done"].includes(event.stage)) {
    bar.value = 100;
    percent.textContent = "完成";
  } else if (event.stage === "error") {
    bar.value = 0;
    percent.textContent = "失败";
  } else {
    bar.removeAttribute("value");
    percent.textContent = "处理中";
    if (event.stage === "download") {
      $("#installDetail").textContent = `${formatBytes(event.downloaded || 0)} · 总大小未知${event.bytesPerSecond ? ` · ${formatBytes(event.bytesPerSecond)}/s` : ""}`;
    }
  }
}

// 响应按行解码；fetch 收到头部不代表安装完成，必须收到 complete 事件。
async function streamInstall() {
  const response = await fetch("/api/v1/core/install", {
    method:"POST",
    headers:{"Content-Type":"application/json", "Accept":"application/x-ndjson", "Authorization":`Bearer ${state.token}`},
    body:JSON.stringify({version:"latest"}),
  });
  if (!response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
    const payload = await response.json();
    if (!response.ok || payload.error) throw new Error(payload.error?.message || `安装失败：HTTP ${response.status}`);
    renderInstallEvent({stage:"complete", message:"安装完成；后台服务更新后可显示实时进度。"});
    return;
  }
  if (!response.ok) throw new Error(`安装失败：HTTP ${response.status}`);
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (true) {
      const {done, value} = await reader.read();
      buffer += decoder.decode(value, {stream:!done});
      const lines = buffer.split("\n");
      buffer = lines.pop();
      if (done && buffer.trim()) { lines.push(buffer); buffer = ""; }
      for (const line of lines) {
        if (!line.trim()) continue;
        const event = JSON.parse(line);
        if (event.error) throw new Error(event.error);
        renderInstallEvent(event);
        if (event.stage === "complete") return;
      }
      if (done) throw new Error("安装进度连接已中断，未收到完成确认；请重试以续传。");
      if (buffer.length > 1048576) throw new Error("安装进度数据异常。");
    }
  } finally { await reader.cancel(); reader.releaseLock(); }
}

async function loadNodes() {
  const list = $("#nodeList");
  list.className = "table-body empty-state";
  list.textContent = "正在读取节点…";
  try {
    state.nodes = await api("/api/v1/nodes");
	state.delays = {}; // 重新读取节点时以最新内核历史为准，不沿用页面内旧测速值。
    $("#navNodeCount").textContent = state.nodes.length;
    renderNodes();
  } catch (error) {
    list.textContent = error.message;
  }
}

function renderNodes() {
  const query = $("#nodeSearch").value.trim().toLowerCase();
  const filter = $("#nodeFilter").value;
  const nodes = state.nodes.filter((node) => (!query || `${node.name} ${node.type} ${node.providerName}`.toLowerCase().includes(query)) && (filter !== "alive" || (!node.cached && (state.delays[node.name] ?? node.delay) > 0 && node.alive)));
  const list = $("#nodeList");
  list.replaceChildren();
  list.className = nodes.length ? "table-body" : "table-body empty-state";
  if (!nodes.length) { const p=document.createElement("p");p.textContent="没有符合条件的节点。";list.append(p);return; }
  nodes.forEach((node) => {
    const row = document.createElement("div"); row.className="node-row";
    const name = document.createElement("div"); name.className="node-name";
    const avatar=document.createElement("span");avatar.className="node-avatar";avatar.textContent=node.name.trim().slice(0,1).toUpperCase()||"N";
    const strong=document.createElement("strong");strong.textContent=node.name;name.append(avatar,strong);
    const type=document.createElement("span");type.textContent=node.type;
    const provider=document.createElement("span");provider.textContent=node.providerName||"—";
    const delay=document.createElement("span");const value=node.cached?0:(state.delays[node.name]??node.delay);delay.className=`latency ${value&&value<120?"good":value&&value<250?"medium":value?"bad":""}`;delay.textContent=value?`${value} ms`:`—`;
    const tested=node.tested||node.delay>0||Object.hasOwn(state.delays,node.name);
    const passed=value>0&&(Object.hasOwn(state.delays,node.name)||node.alive);
    const status=document.createElement("span");status.className=`node-status ${!node.cached&&passed?"alive":""}`;const dot=document.createElement("i");const st=document.createElement("span");st.textContent=node.cached?"离线缓存":!tested?"未测速":passed?"检查通过":"检查未通过";status.append(dot,st);
    const action=document.createElement("button");action.className="button button-secondary node-action";action.textContent=node.cached?"请先启动内核":state.overview?.core?.currentNode===node.name?"使用中":"选择";action.disabled=node.cached||state.overview?.core?.currentNode===node.name;action.addEventListener("click",()=>selectNode(node.name,action));
    row.append(name,type,provider,delay,status,action);list.append(row);
  });
}

async function testNodes(button = $("#testNodesButton")) {
  setBusy(button,true,"测速中…");
  try { const delays=await api("/api/v1/nodes/test",{method:"POST"});await loadNodes();state.delays=delays;renderNodes();const values=Object.values(delays);const passed=values.filter(delay=>delay>0).length;const summary=`测速 ${values.length} 个真实节点：${passed} 个通过，${values.length-passed} 个未通过`;$("#nodeTestSummary").textContent=summary;toast(summary); }
  catch(error){toast(error.message,"error");}
  finally{setBusy(button,false);}
}

async function selectNode(name,button){setBusy(button,true,"切换中…");try{await api("/api/v1/nodes/select",{method:"POST",body:JSON.stringify({name})});toast(`已切换到 ${name}`);await refreshOverview(true);renderNodes();}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}

async function loadSubscriptions(){try{const [items,groups]=await Promise.all([api("/api/v1/subscriptions"),api("/api/v1/subscription-groups")]);state.subscriptionGroups=groups;renderSubscriptionGroups();renderSubscriptions(items);}catch(error){toast(error.message,"error");}}
function renderSubscriptionGroups() {
  ["#subscriptionGroupSelect", "#subGroup"].forEach(selector => {
    const select = $(selector);
    const previous = select.value;
    select.replaceChildren();
    state.subscriptionGroups.forEach(group => {
      const option = document.createElement("option");
      option.value = group.name;
      option.textContent = `${group.enabled ? "●" : "○"} ${group.name}`;
      select.append(option);
    });
    if (state.subscriptionGroups.some(group => group.name === previous)) select.value = previous;
  });
}
function renderSubscriptions(items) {
  const grid = $("#subscriptionGrid");
  grid.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "empty-card";
    empty.textContent = "还没有订阅，添加一个 Clash/Mihomo 兼容地址。";
    grid.append(empty);
    return;
  }
  items.forEach(sub => {
    const card = document.createElement("article");
    card.className = "subscription-card";
    const title = document.createElement("div");
    title.className = "subscription-title";
    const heading = document.createElement("h3");
    heading.textContent = `#${sub.index} ${sub.name}`;
    const status = document.createElement("span");
    status.className = `pill ${sub.enabled ? "online" : ""}`;
    status.textContent = sub.enabled ? "已启用" : "已禁用";
    title.append(heading, status);
    const url = document.createElement("p");
    url.className = "subscription-url";
    url.textContent = sub.url;
    const meta = document.createElement("div");
    meta.className = "subscription-meta";
    const authLabel = sub.authType.toLowerCase() === "aes" ? "AES 解密" : `${sub.authType.toUpperCase()} 认证`;
    const updateLabel = (sub.updateVia || "direct").toLowerCase() === "proxy" ? "PROXY 出站" : "直连下载";
    [sub.group, authLabel, updateLabel, `${sub.updateInterval}s 更新`, `${sub.healthInterval}s 检查`].forEach(text => {
      const tag = document.createElement("span");
      tag.className = "tag";
      tag.textContent = text;
      meta.append(tag);
    });
    const actions = document.createElement("div");
    actions.className = "subscription-actions";
    const route = document.createElement("select");
    route.className = "select subscription-route";
    route.setAttribute("aria-label", `${sub.name} 的订阅下载路径`);
    [["direct", "直连"], ["proxy", "PROXY 出站"]].forEach(([value, text]) => {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = text;
      route.append(option);
    });
    route.value = (sub.updateVia || "direct").toLowerCase();
    actions.append(route);
    const addAction = (label, callback) => {
      const button = document.createElement("button");
      button.className = "button button-secondary";
      button.textContent = label;
      button.addEventListener("click", () => callback(button));
      actions.append(button);
    };
    addAction(sub.enabled ? "禁用" : "启用", button => toggleSubscription(sub, button));
    addAction("更新", button => updateSubscription(sub.name, button, route.value));
    addAction("检查", button => testSubscription(sub.name, button, route.value));
    addAction("删除", () => removeSubscription(sub.name));
    card.append(title, url, meta, actions);
    grid.append(card);
  });
}
async function toggleSubscription(sub,button){setBusy(button,true);try{await api("/api/v1/subscriptions",{method:"PATCH",body:JSON.stringify({reference:sub.name,enabled:!sub.enabled})});toast(`${sub.name} 已${sub.enabled?"禁用":"启用"}`);await loadSubscriptions();await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function updateSubscription(name, button, via = "") {
  return runSubscriptionAction("update", {reference: name, via}, button);
}
async function updateSubscriptionBatch(group, button) {
  return runSubscriptionAction("update", {reference: "all", group, via: $("#subscriptionUpdateVia").value}, button);
}
async function testSubscription(name, button, via = "") {
  return runSubscriptionAction("test", {reference: name, via}, button);
}
async function testSubscriptionBatch(group, button) {
  return runSubscriptionAction("test", {reference: "all", group, via: $("#subscriptionUpdateVia").value}, button);
}

// 更新结果是持久可读的页面内容，不再只用几秒后消失的 Toast 提示。
function renderSubscriptionResult(result, action, errorMessage = "") {
  const panel = $("#subscriptionResult");
  panel.hidden = false;
  const items = result?.results || [];
  const completed = action === "test" ? result?.tested : result?.updated;
  $("#subscriptionResultTitle").textContent = `${action === "test" ? "检查" : "更新"}结果 · ${completed?.length || 0} 个成功${items.some(item => item.error) ? " · 存在失败" : ""}`;
  const summary = [];
  if (errorMessage) summary.push(errorMessage);
  if (result?.temporarilyStartedCore) summary.push(errorMessage ? "本次临时启动过内核，已尝试恢复停止；如恢复失败请查看错误。" : "内核已恢复停止。可查看缓存节点；选择节点或使用代理前请启动内核。");
  summary.push("数量为订阅解析出的节点数，不代表节点全部在线。检查路径仅控制订阅下载，节点健康检查始终通过各节点进行。");
  $("#subscriptionResultSummary").textContent = summary.join(" ");
  const list = $("#subscriptionResultList");
  list.replaceChildren();
  items.forEach(item => {
    const row = document.createElement("div");
    row.className = `subscription-result-row${item.error ? " failed" : ""}`;
    const name = document.createElement("strong");
    name.textContent = `${item.error ? "×" : "✓"} ${item.name}`;
    const detail = document.createElement("span");
    const count = Number.isInteger(item.nodeCount) ? `${item.nodeCount} 个节点${Number.isInteger(item.previousCount) ? `（更新前 ${item.previousCount}）` : ""}` : "节点数未确认";
    detail.textContent = `${item.via === "proxy" ? "PROXY 出站" : "直连"} · ${count} · ${(item.durationMs / 1000).toFixed(1)}s`;
    row.append(name, detail);
    if (item.error || item.warning) {
      const message = document.createElement("p");
      message.textContent = item.error || item.warning;
      row.append(message);
    }
    list.append(row);
  });
}

async function runSubscriptionAction(action, input, button) {
  if (state.subscriptionBusy) { toast("已有订阅操作正在执行，请等待完成。", "error"); return; }
  state.subscriptionBusy = true;
  setBusy(button, true, action === "test" ? "检查中…" : "更新中…");
  try {
    const result = await api(`/api/v1/subscriptions/${action}`, {method: "POST", body: JSON.stringify(input)});
    renderSubscriptionResult(result, action);
    toast("操作完成，节点数量与明细已显示在订阅结果中。");
  } catch (error) {
    renderSubscriptionResult(error.data, action, error.message);
    toast("操作未全部完成，请查看订阅结果中的原因。", "error");
  } finally {
    state.subscriptionBusy = false;
    setBusy(button, false);
    await Promise.all([loadSubscriptions(), loadNodes(), refreshOverview(true)]);
  }
}
async function removeSubscription(name){if(!confirm(`确认删除订阅“${name}”？缓存节点也会从配置中移除。`))return;try{await api(`/api/v1/subscriptions?name=${encodeURIComponent(name)}`,{method:"DELETE"});toast(`${name} 已删除`);await loadSubscriptions();await refreshOverview(true);}catch(error){toast(error.message,"error");}}

async function loadCores(){const list=$("#coreList");try{const items=await api("/api/v1/core/installations");list.replaceChildren();list.className=items.length?"table-body":"table-body empty-state";if(!items.length){list.textContent="尚未安装任何 Mihomo 版本。";return;}items.forEach((item,index)=>{const row=document.createElement("div");row.className="core-row";const version=document.createElement("strong");version.textContent=`#${index+1} ${item.version}`;const engine=document.createElement("span");engine.textContent=item.engine;const status=document.createElement("span");status.className=`pill ${item.active?"online":""}`;status.textContent=item.active?"ACTIVE":"INSTALLED";const path=document.createElement("code");path.textContent=item.path;const actions=document.createElement("div");actions.className="inline-actions";if(!item.active){const use=document.createElement("button");use.className="button button-secondary";use.textContent="使用";use.addEventListener("click",()=>useCore(item.version,use));const remove=document.createElement("button");remove.className="button button-secondary";remove.textContent="删除";remove.addEventListener("click",()=>removeCore(item.version));actions.append(use,remove);}row.append(version,engine,status,path,actions);list.append(row);});}catch(error){list.textContent=error.message;}}
async function useCore(version,button){setBusy(button,true,"切换中…");try{await api("/api/v1/core/use",{method:"POST",body:JSON.stringify({reference:version})});toast(`已切换到 ${version}`);await loadCores();await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function removeCore(version){if(!confirm(`确认删除 Mihomo ${version}？`))return;try{await api(`/api/v1/core/installations?reference=${encodeURIComponent(version)}`,{method:"DELETE"});toast(`${version} 已删除`);await loadCores();}catch(error){toast(error.message,"error");}}

async function loadRouting(){try{state.routing=await api("/api/v1/routing");renderRouting();}catch(error){toast(error.message,"error");}}
function renderRouting() {
  const routing = state.routing;
  const profiles = $("#routeProfiles");
  profiles.replaceChildren();
  // Go 的空切片可能编码成 null；无规则组的 Global/Direct 是有效配置，不能使整页渲染中断。
  (routing.profiles || []).forEach(profile => {
    const card = document.createElement("article");
    const active = profile.name === routing.activeProfile;
    card.className = `subscription-card ${active ? "profile-active" : ""}`;
    const title = document.createElement("div");
    title.className = "subscription-title";
    const heading = document.createElement("h3"); heading.textContent = profile.name;
    const badge = document.createElement("span"); badge.className = `pill ${active ? "online" : ""}`;
    badge.textContent = active ? "ACTIVE" : profile.defaultAction.toUpperCase();
    title.append(heading, badge);
    const description = document.createElement("p"); description.className = "subscription-url";
    const names = profile.groups || [];
    description.textContent = names.length ? names.join(" → ") : "无规则组";
    const button = document.createElement("button"); button.className = "button button-secondary";
    button.textContent = "应用配置"; button.disabled = active;
    button.addEventListener("click", () => useRouteProfile(profile.name, button));
    card.append(title, description, button); profiles.append(card);
  });
  const select = $("#routeRuleGroup"); select.replaceChildren();
  const groups = $("#routeGroups"); groups.replaceChildren();
  (routing.ruleGroups || []).forEach(group => {
    const rules = group.rules || [];
    const option = document.createElement("option"); option.value = group.name; option.textContent = group.name;
    select.append(option);
    const card = document.createElement("article"); card.className = "subscription-card";
    const heading = document.createElement("h3"); heading.textContent = `${group.name} · ${rules.length} 条`;
    card.append(heading);
    const list = document.createElement("div"); list.className = "rule-list";
    rules.forEach((rule, index) => {
      const row = document.createElement("div"); row.className = "rule-item";
      [`${index + 1}`, rule.type, rule.value, rule.action.toUpperCase()].forEach(text => {
        const cell = document.createElement("span"); cell.textContent = text; row.append(cell);
      });
      const remove = document.createElement("button"); remove.className = "text-button"; remove.textContent = "删除";
      remove.addEventListener("click", () => removeRouteRule(group.name, index + 1));
      row.append(remove); list.append(row);
    });
    if (!rules.length) { const hint = document.createElement("p"); hint.textContent = "尚无规则，可使用上方表单添加。"; list.append(hint); }
    card.append(list); groups.append(card);
  });
}
async function useRouteProfile(name,button){setBusy(button,true,"应用中…");try{await api("/api/v1/routing/profiles/use",{method:"POST",body:JSON.stringify({name})});toast(`已应用 ${name}`);await loadRouting();await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function addRouteRule(){const button=$("#addRouteRule");setBusy(button,true,"添加中…");const input={group:$("#routeRuleGroup").value,rule:{action:$("#routeRuleAction").value,type:$("#routeRuleType").value,value:$("#routeRuleValue").value.trim()}};try{if(!input.rule.value)throw new Error("请输入匹配值");await api("/api/v1/routing/rules",{method:"POST",body:JSON.stringify(input)});$("#routeRuleValue").value="";toast("路由规则已添加");await loadRouting();}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function removeRouteRule(group,index){try{await api(`/api/v1/routing/rules?group=${encodeURIComponent(group)}&index=${index}`,{method:"DELETE"});toast("规则已删除");await loadRouting();}catch(error){toast(error.message,"error");}}

async function runDoctor(){const button=$("#runDoctorButton");setBusy(button,true,"检查中…");try{const items=await api("/api/v1/doctor");const list=$("#doctorList");list.replaceChildren();items.forEach(item=>{const row=document.createElement("div");row.className=`doctor-item ${item.status}`;const icon=document.createElement("span");icon.className="doctor-icon";icon.textContent=item.status==="ok"?"✓":item.status==="warning"?"!":"×";const name=document.createElement("strong");name.textContent=item.name;const message=document.createElement("span");message.className="doctor-message";message.textContent=item.message;row.append(icon,name,message);list.append(row);});}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function loadLogs(){try{const lines=await api("/api/v1/logs?limit=300");const output=$("#logOutput");output.textContent=lines.length?lines.join("\n"):"暂时没有日志。";output.scrollTop=output.scrollHeight;}catch(error){$("#logOutput").textContent=error.message;}}
async function loadSettings(){try{const [settings,security]=await Promise.all([api("/api/v1/settings"),api("/api/v1/web/security")]);$("#settingPort").value=settings.mixedPort;$("#settingMode").value=settings.mode;$("#settingLAN").checked=settings.allowLAN;$("#settingTun").checked=settings.tunEnabled;$("#settingDownloadProxy").value=settings.downloadProxy||"";$("#settingDownloadRetry").value=settings.downloadRetry||4;renderWebSecurity(security);}catch(error){toast(error.message,"error");}}
function renderWebSecurity(security){state.authEnabled=security.authEnabled;const badge=$("#webAuthBadge");badge.className=`pill ${security.authEnabled?"online":""}`;badge.textContent=security.authEnabled?"TOKEN ENABLED":"OPEN ACCESS";$("#webSecurityWarning").classList.toggle("danger",!security.authEnabled);if(!security.authEnabled){state.token="";sessionStorage.removeItem("kivo_token");}}

function openSubscriptionModal(){const modal=$("#subscriptionModal");modal.classList.add("active");modal.setAttribute("aria-hidden","false");setTimeout(()=>$("#subName").focus(),20);}
function closeSubscriptionModal(){const modal=$("#subscriptionModal");modal.classList.remove("active");modal.setAttribute("aria-hidden","true");$("#subscriptionError").textContent="";}

function bindEvents(){
  $$(".nav-item").forEach(item=>item.addEventListener("click",()=>switchView(item.dataset.view)));
  $$('[data-goto]').forEach(item=>item.addEventListener("click",()=>switchView(item.dataset.goto)));
  $("#mobileMenu").addEventListener("click",()=>$(".sidebar").classList.toggle("open"));
  $("#refreshButton").addEventListener("click",async()=>{await refreshOverview();switchView(state.activeView);});
  $("#quickStartButton").addEventListener("click",coreAction);$("#heroAction").addEventListener("click",coreAction);
  $("#systemProxyOn").addEventListener("click",()=>systemProxyAction("on"));
  $("#systemProxyOff").addEventListener("click",()=>systemProxyAction("off"));
  $("#systemProxyRecover").addEventListener("click",()=>systemProxyAction("recover"));
  $("#systemProxyForce").addEventListener("click",()=>systemProxyAction("recover",true));
  $$('[data-core-action]').forEach(button=>button.addEventListener("click",()=>coreOnlyAction(button.dataset.coreAction,button)));
  $("#checkConnectivityButton").addEventListener("click",checkConnectivity);
  $("#proxySetupButton").addEventListener("click",showProxyGuide);
  $("#closeProxyGuide").addEventListener("click",()=>{$("#proxyGuide").hidden=true;$("#proxySetupButton").setAttribute("aria-expanded","false");$("#proxySetupButton").focus();});
  $("#quickTest").addEventListener("click",async()=>{switchView("nodes");await testNodes();});
  $("#copyProxyButton").addEventListener("click",async()=>{const port=state.overview?.core?.mixedPort||17890;await navigator.clipboard.writeText(`http://127.0.0.1:${port}`);toast("代理地址已复制");});
  $("#nodeSearch").addEventListener("input",renderNodes);$("#nodeFilter").addEventListener("change",renderNodes);$("#testNodesButton").addEventListener("click",()=>testNodes());
  $("#addSubscriptionButton").addEventListener("click",openSubscriptionModal);$$('[data-close-modal]').forEach(item=>item.addEventListener("click",closeSubscriptionModal));
  $("#useSubscriptionGroup").addEventListener("click",async()=>{const name=$("#subscriptionGroupSelect").value;try{await api("/api/v1/subscription-groups/action",{method:"POST",body:JSON.stringify({name,action:"use"})});toast(`已独占启用 ${name}`);await loadSubscriptions();await refreshOverview(true);}catch(error){toast(error.message,"error");}});
  $("#updateSubscriptionGroup").addEventListener("click",event=>updateSubscriptionBatch($("#subscriptionGroupSelect").value,event.currentTarget));
  $("#updateAllSubscriptions").addEventListener("click",event=>updateSubscriptionBatch("",event.currentTarget));
  $("#testSubscriptionGroup").addEventListener("click",event=>testSubscriptionBatch($("#subscriptionGroupSelect").value,event.currentTarget));
  $("#testAllSubscriptions").addEventListener("click",event=>testSubscriptionBatch("",event.currentTarget));
  $("#createSubscriptionGroup").addEventListener("click",async()=>{const name=$("#newSubscriptionGroup").value.trim();if(!name)return;try{await api("/api/v1/subscription-groups",{method:"POST",body:JSON.stringify({name})});$("#newSubscriptionGroup").value="";toast(`分组 ${name} 已创建`);await loadSubscriptions();}catch(error){toast(error.message,"error");}});
  $("#installLatestCore").addEventListener("click",event=>installCore(event.currentTarget));
  $("#addRouteRule").addEventListener("click",addRouteRule);
  $("#subAuth").addEventListener("change",()=>{const type=$("#subAuth").value;$("#subUsernameRow").classList.toggle("hidden",type!=="basic");$("#subSecretRow").classList.toggle("hidden",type==="none");$("#subSecretLabel").textContent=type==="aes"?"AES 解密密码":type==="age"?"AGE 私钥":"密码或 Token";});
  $("#runDoctorButton").addEventListener("click",runDoctor);$("#refreshLogsButton").addEventListener("click",loadLogs);
  $("#themeButton").addEventListener("click",()=>{const next=document.documentElement.dataset.theme==="light"?"dark":"light";document.documentElement.dataset.theme=next;localStorage.setItem("kivo_theme",next);});
  $("#loginForm").addEventListener("submit",async(event)=>{event.preventDefault();state.token=$("#loginSecret").value.trim();try{await api("/api/v1/session/verify",{method:"POST"});sessionStorage.setItem("kivo_token",state.token);hideLogin();await bootstrap();}catch(error){state.token="";$("#loginError").textContent="密钥不正确，请检查 config.json。";}});
  $("#subscriptionForm").addEventListener("submit",async(event)=>{event.preventDefault();const button=event.submitter;setBusy(button,true,"添加中…");const input={name:$("#subName").value.trim(),url:$("#subURL").value.trim(),authType:$("#subAuth").value,username:$("#subUsername").value,secret:$("#subSecret").value,updateVia:$("#subUpdateVia").value,group:$("#subGroup").value,updateInterval:Number($("#subInterval").value),healthInterval:Number($("#subHealth").value),healthCheckURL:"https://www.gstatic.com/generate_204",additionalPrefix:$("#subPrefix").value};try{await api("/api/v1/subscriptions",{method:"POST",body:JSON.stringify(input)});toast(`订阅 ${input.name} 已添加`);event.target.reset();closeSubscriptionModal();await loadSubscriptions();await refreshOverview(true);}catch(error){$("#subscriptionError").textContent=error.message;}finally{setBusy(button,false);}});
  $("#settingsForm").addEventListener("submit",async(event)=>{event.preventDefault();const button=event.submitter;setBusy(button,true,"应用中…");const settings={listen:state.overview?.webAddress||"127.0.0.1:9099",mixedPort:Number($("#settingPort").value),mode:$("#settingMode").value,allowLAN:$("#settingLAN").checked,tunEnabled:$("#settingTun").checked,downloadProxy:$("#settingDownloadProxy").value.trim(),downloadRetry:Number($("#settingDownloadRetry").value)};try{await api("/api/v1/settings",{method:"PATCH",body:JSON.stringify(settings)});toast("设置已保存并应用");await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}});
  $("#webSecurityForm").addEventListener("submit",async(event)=>{event.preventDefault();const button=event.submitter;const nextToken=$("#settingWebToken").value.trim();$("#webSecurityError").textContent="";if(!nextToken&&state.authEnabled&&!confirm("确认关闭 Web Token 鉴权？任何能访问控制台地址的设备都将拥有完整管理权限。"))return;setBusy(button,true,"保存中…");try{const security=await api("/api/v1/web/security",{method:"PATCH",body:JSON.stringify({token:nextToken})});state.token=nextToken;if(nextToken)sessionStorage.setItem("kivo_token",nextToken);else sessionStorage.removeItem("kivo_token");$("#settingWebToken").value="";renderWebSecurity(security);toast(nextToken?"新的 Web Token 已立即生效":"Web Token 鉴权已关闭","success");}catch(error){$("#webSecurityError").textContent=error.message;}finally{setBusy(button,false);}});
  window.addEventListener("hashchange",()=>{const name=location.hash.slice(1);if(document.querySelector(`#view-${name}`))switchView(name)});
}

async function bootstrap(){clearInterval(state.overviewTimer);clearInterval(state.logsTimer);await refreshOverview(true);const initial=location.hash.slice(1)||"overview";switchView(document.querySelector(`#view-${initial}`)?initial:"overview");state.overviewTimer=setInterval(()=>{if(document.visibilityState==="visible")refreshOverview(true)},5000);state.logsTimer=setInterval(()=>{if(state.activeView==="logs"&&$("#autoLogs").checked)loadLogs()},3000);}

document.documentElement.dataset.theme=renamedPreference(localStorage,"kivo_theme","proxypilot_theme")||"dark";
bindEvents();
api("/api/v1/session/verify",{method:"POST"}).then(()=>{hideLogin();bootstrap()}).catch(()=>showLogin("请输入 Web API 密钥。"));
