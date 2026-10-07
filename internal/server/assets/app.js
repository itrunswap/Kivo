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
  nodePage: 1,
  nodePageSize: 24,
  logs: [],
  nodesLoading: false,
  logsLoading: false,
  overviewLoading: false,
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

// 暂存原有子节点，处理结束恢复图标和禁用状态，不使用 innerHTML。
const busyButtons = new WeakMap();
function setBusy(button, busy, label = "处理中…") {
  if (!button) return;
  if (busy) {
    if (busyButtons.has(button)) return;
    busyButtons.set(button, {nodes:[...button.childNodes], text:button.textContent, disabled:button.disabled});
    button.textContent = label;
    button.disabled = true;
    button.setAttribute("aria-busy", "true");
  } else {
    const previous = busyButtons.get(button);
    if (!previous) return;
    if (previous.nodes.length) button.replaceChildren(...previous.nodes);
    else button.textContent = previous.text;
    button.disabled = previous.disabled;
    button.removeAttribute("aria-busy");
    busyButtons.delete(button);
  }
}

// 外部来源的节点名、订阅名与日志只写文本节点，不能成为可执行 HTML。
function textElement(tag, text, className = "") {
  const item = document.createElement(tag);
  item.textContent = text;
  if (className) item.className = className;
  return item;
}
function renderEmpty(target, title, detail, action) {
  target.replaceChildren(textElement("h3", title), textElement("p", detail));
  if (action) {
    const button = textElement("button", action.label, "button button-secondary");
    button.addEventListener("click", action.run);
    target.append(button);
  }
}
function persistPreference(storage, key, value) {
  try { storage.setItem(key, value); } catch { /* 隐私模式下保留内存偏好。 */ }
}

function toast(message, type = "success") {
  const item = document.createElement("div");
  item.className = `toast ${type}`;
  item.textContent = message;
  $("#toastRegion").append(item);
  setTimeout(() => item.remove(), 4200);
}

function showLogin(message = "") {
  closeCommandDialog(false);
  $("#loginError").textContent = message;
  state.authenticated = false;
  $(".app-shell").inert = true;
  if ($("#loginOverlay").classList.contains("active")) { $("#loginSecret").focus(); return; }
  $("#loginOverlay").classList.add("active");
  setTimeout(() => $("#loginSecret").focus(), 30);
}

function hideLogin() {
  state.authenticated = true;
  $("#loginOverlay").classList.remove("active");
  syncDialogState();
}

const viewTitles = {overview:"网络概览",nodes:"代理节点",subscriptions:"订阅管理",cores:"内核管理",routing:"路由策略",diagnostics:"环境诊断",logs:"运行日志",settings:"偏好设置"};

// 外观与导航偏好独立于服务配置，不会重载内核。无存储权限时仍保留本次会话值。
function validTheme(value) { return ["light","dark","system"].includes(value) ? value : "system"; }
function resolvedTheme(value) {
  return validTheme(value) === "system" ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : value;
}
function applyTheme(value, persist = true) {
  state.themePreference = validTheme(value);
  document.documentElement.dataset.theme = resolvedTheme(state.themePreference);
  if (persist) persistPreference(localStorage,"kivo_theme",state.themePreference);
  $$("[data-theme-choice]").forEach(button=>button.setAttribute("aria-pressed",String(button.dataset.themeChoice === state.themePreference)));
  const actual = document.documentElement.dataset.theme === "dark" ? "深色" : "浅色";
  $("#themeSummary").textContent = state.themePreference === "system" ? `跟随系统 · 当前${actual}` : `固定${actual}主题`;
  $("#themeButton").title = `当前${actual}，点击切换`;
}
function setSidebarCollapsed(collapsed, persist = true) {
  document.documentElement.dataset.sidebar = collapsed ? "collapsed" : "expanded";
  if (persist) persistPreference(localStorage,"kivo_sidebar",collapsed ? "collapsed" : "expanded");
  const label = collapsed ? "展开侧栏" : "折叠侧栏";
  $("#sidebarToggle").title = label;
  $("#sidebarToggle").setAttribute("aria-label",label);
  $("#sidebarToggle").setAttribute("aria-expanded",String(!collapsed));
}
function initializeAppearance() {
  applyTheme(renamedPreference(localStorage,"kivo_theme","proxypilot_theme"),false);
  let collapsed = false;
  try { collapsed = localStorage.getItem("kivo_sidebar") === "collapsed"; } catch { /* 使用默认展开状态。 */ }
  setSidebarCollapsed(collapsed,false);
  $$(".nav-item").forEach(item=>item.title = viewTitles[item.dataset.view]);
  $("#commandShortcut").textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘ K" : "Ctrl K";
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change",()=>{
    if (state.themePreference === "system") applyTheme("system",false);
  });
}

// 快捷搜索只展示页面标题和已加载节点的名称，不索引订阅地址、密码或 Token。
// Enter 在节点列表中定位，真正切换节点仍需要用户点击原来的选择按钮。
function commandItems(query) {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const matches = text => terms.every(term=>text.toLocaleLowerCase().includes(term));
  const pages = Object.entries(viewTitles).filter(([key,title])=>matches(`${key} ${title}`))
    .map(([view,label])=>({view,label,detail:"页面"}));
  const nodes = query.trim() ? state.nodes.filter(node=>matches(`${node.name} ${node.providerName || ""} ${node.type || ""}`))
    .map(node=>({view:"nodes",node:node.name,label:node.name,detail:`节点 · ${node.providerName || "未分组"}`})) : [];
  return [...pages,...nodes].slice(0,50);
}
function syncDialogState() {
  const active = ["#loginOverlay","#subscriptionModal","#commandOverlay"].some(id=>$(id).classList.contains("active"));
  $(".app-shell").inert = active;
  document.body.classList.toggle("modal-open",active);
}
function renderCommandResults() {
  state.commandResults = commandItems($("#commandInput").value);
  state.commandIndex = 0;
  const list = $("#commandList");
  list.replaceChildren();
  state.commandResults.forEach((result,index)=>{
    const option = textElement("button","","command-option");
    option.id = `command-option-${index}`;
    option.type = "button"; option.tabIndex = -1;
    option.setAttribute("role","option");
    option.append(textElement("span",result.label),textElement("small",result.detail));
    option.addEventListener("click",()=>chooseCommandResult(index));
    list.append(option);
  });
  if (!state.commandResults.length) list.append(textElement("p","没有匹配结果。尝试页面名称或已加载节点的关键词。","command-empty"));
  $("#commandCount").textContent = state.commandResults.length === 50 ? "最多显示 50 项，请细化关键词" : `${state.commandResults.length} 项 · 节点搜索仅含已加载数据`;
  selectCommandResult(0);
}
function selectCommandResult(index) {
  const count = state.commandResults?.length || 0;
  state.commandIndex = count ? (index + count) % count : 0;
  [...$("#commandList").children].forEach((item,i)=>item.setAttribute("aria-selected",String(count > 0 && i === state.commandIndex)));
  if (count) {
    $("#commandInput").setAttribute("aria-activedescendant",`command-option-${state.commandIndex}`);
    $("#commandList").children[state.commandIndex].scrollIntoView({block:"nearest"});
  } else $("#commandInput").removeAttribute("aria-activedescendant");
}
function openCommandDialog() {
  if (!state.authenticated || $("#subscriptionModal").classList.contains("active") || $("#loginOverlay").classList.contains("active")) return;
  setSidebarOpen(false);
  state.commandReturnFocus = document.activeElement;
  $("#commandInput").value = "";
  $("#commandOverlay").classList.add("active");
  $("#commandOverlay").setAttribute("aria-hidden","false");
  syncDialogState(); renderCommandResults(); $("#commandInput").focus();
}
function closeCommandDialog(restoreFocus = true) {
  const active = $("#commandOverlay").classList.contains("active");
  $("#commandOverlay").classList.remove("active");
  $("#commandOverlay").setAttribute("aria-hidden","true");
  syncDialogState();
  if (active && restoreFocus) state.commandReturnFocus?.focus();
}
function chooseCommandResult(index) {
  const result = state.commandResults?.[index];
  if (!result) return;
  closeCommandDialog();
  switchView(result.view,{load:!result.node});
  if (result.node && state.activeView === "nodes") {
    $("#nodeSearch").value = result.node;
    $("#nodeProvider").value = "all"; $("#nodeFilter").value = "all";
    state.nodePage = 1; renderNodes(); $("#nodeSearch").focus();
  }
}
function isEditing(target) { return Boolean(target?.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target?.tagName || "")); }
function handleShortcuts(event) {
  if (event.defaultPrevented || event.isComposing || !state.authenticated) return;
  if ($("#loginOverlay").classList.contains("active") || $("#subscriptionModal").classList.contains("active")) return;
  if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === "k") {
    event.preventDefault();
    if ($("#commandOverlay").classList.contains("active")) closeCommandDialog(); else openCommandDialog();
  } else if (!event.ctrlKey && !event.metaKey && !event.altKey && event.key === "/" && !isEditing(event.target)) {
    event.preventDefault(); openCommandDialog();
  }
}
function setSidebarOpen(open) {
  $(".sidebar").classList.toggle("open", open);
  $(".sidebar").inert = window.matchMedia("(max-width:760px)").matches && !open;
  $("#sidebarBackdrop").hidden = !open;
  $("#mobileMenu").setAttribute("aria-expanded", String(open));
  if (open) $(".nav-item.active").focus();
}
function loadActiveView(name = state.activeView) {
  const loaders = {nodes:loadNodes, subscriptions:loadSubscriptions, cores:loadCores, routing:loadRouting, diagnostics:runDoctor, logs:loadLogs, settings:loadSettings};
  return loaders[name]?.();
}
function switchView(name, {reload = false, load = true} = {}) {
  // hash 使用白名单；相同页面的 hashchange 不重复请求，也不拼接未经验证的选择器。
  if (!Object.hasOwn(viewTitles, name)) name = "overview";
  const changed = name !== state.activeView || !state.viewInitialized;
  if (changed && state.activeView === "settings" && state.settingsDirty) {
    if (!confirm("网络设置尚未保存，离开页面会丢弃这些修改。继续吗？")) {
      location.hash = "settings";
      return;
    }
    state.settingsDirty = false;
  }
  state.activeView = name;
  state.viewInitialized = true;
  $$(".view").forEach((view) => view.classList.toggle("active", view.id === `view-${name}`));
  $$(".nav-item").forEach(item => {
    const active = item.dataset.view === name;
    item.classList.toggle("active", active);
    if (active) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  });
  $("#pageTitle").textContent = viewTitles[name];
  document.title = `Kivo · ${viewTitles[name]}`;
  setSidebarOpen(false);
  if (location.hash !== `#${name}`) location.hash = name;
  if (changed) $("#contentScroll").scrollTo?.({top:0,behavior:"instant"});
  if (load && (changed || reload)) return loadActiveView(name);
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
  const ready = data.core?.state === "running" && data.proxyPortListening;
  $("#systemProxyOwnership").textContent = proxy.recoveryPending ? "备份待恢复" : proxy.managed ? "自动管理" : proxy.state === "this_app" ? "手动配置" : "未接入";
  $("#systemProxyOwnership").className = `pill ${proxy.managed && ready ? "online" : ""}`;
  $("#systemProxyExplanation").textContent = proxy.recoveryPending ? "检测到未完成的恢复或外部修改。备份保留，先安全恢复；强制恢复需要额外确认。" : proxy.managed ? (ready ? "系统代理已自动接入；断开时恢复原设置。是否能上网请查看连接健康度。" : "系统代理仍指向本程序，但服务未就绪。请启动内核或恢复原设置。") : proxy.state === "this_app" ? "手动代理指向本程序，尚未接管；点击连接并确认接管后，才由本程序负责恢复。" : proxy.supported ? "点击“连接代理”：启动内核 → 接入系统代理 → 检测外网。无需再到系统设置中填写地址。" : proxy.message || "状态尚未确认，请刷新。";
  $("#systemProxyOn").disabled = state.connecting || !ready || !proxy.supported || proxy.managed || proxy.recoveryPending;
  $("#systemProxyOff").disabled = state.connecting || !proxy.managed;
  $("#systemProxyRecover").hidden = !proxy.recoveryPending;
  $("#systemProxyForce").hidden = !proxy.conflict;
  for (const id of ["#systemProxyRecover", "#systemProxyForce"]) $(id).disabled = state.connecting;
}

async function refreshOverview(silent = false) {
  if (state.overviewLoading) return;
  state.overviewLoading = true;
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
    if (!state.overview) renderOverview({core:{state:"unknown"},systemProxy:{},connection:{level:"error",title:"控制服务未连接 · 当前状态未知",detail:"请检查控制服务后刷新。",nextCommand:"/web status"}});
    $("#heroBadge").className = "badge badge-error";
    $("#heroBadge").textContent = "状态未同步";
    $("#heroNode").textContent = "控制服务未连接 · 当前状态未知";
    $("#heroDescription").textContent = "无法读取最新状态，不代表代理进程已经停止。请检查控制服务后刷新。";
    $("#connectionNext").textContent = "下一步  /web status";
    if (!silent) toast(error.message, "error");
  } finally { state.overviewLoading = false; }
}

function renderOverview(data) {
  const core = data.core || {state:"unknown"};
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
  $("#metricSubscriptions").textContent = data.subscriptionCount ?? "—";
  $("#metricEnabled").textContent = `${data.enabledCount ?? 0} 个已启用`;
  $("#metricMode").textContent = ({rule:"规则模式",global:"全局代理",direct:"全部直连"})[core.mode] || "待确认";
  $("#metricTun").textContent = "仅作用于已接入的流量";
  $("#metricPlatform").textContent = ({windows:"Windows",darwin:"macOS",linux:"Linux"})[data.platform] || data.platform || "—";
  $("#metricArch").textContent = data.architecture || "—";
  $("#runtimePill").className = `pill ${running ? "online" : ""}`;
  $("#runtimePill").textContent = core.state === "unknown" ? "状态未同步" : running ? "内核运行中" : "内核未运行";
  $("#runtimePID").textContent = core.pid || "—";
  $("#runtimeNode").textContent = running ? (core.effectiveNode || core.currentNode || "未确认") : "—";
  $("#runtimeAddress").textContent = data.webAddress;
  $("#runtimeTun").textContent = data.tunEnabled ? "配置开启（实际接管未验证）" : "关闭";
  $("#runtimeProxyPort").textContent = `${core.mixedPort || "—"} · ${core.state === "unknown" ? "未确认" : data.proxyPortListening ? "正在监听" : "未监听"}`;
  $("#runtimeSystemProxy").textContent = data.systemProxy?.message || "未检测，请更新后台服务";
  $("#runtimeSystemProxy").title = "仅检测运行 Kivo 的主机和用户，不代表访问此页面的设备；浏览器扩展可能覆盖系统设置。";
  renderConnectionChain(data);
  if (state.installing) $("#heroAction").textContent = "正在安装…";
  if (state.connecting) { $("#heroAction").textContent = "正在处理代理…"; $("#quickStartButton").textContent = "正在处理代理…"; }
}

// 接入和联网分开表达：内核运行不等于浏览器已接入，节点测速不等于外网检测。
function connectivityStale(data) {
  const report = data.connectivity;
  const checkedAt = Date.parse(report?.checkedAt || "");
  return !report || report.stale || !Number.isFinite(checkedAt) ||
    Date.now() - checkedAt > 120000 || data.core?.state !== "running" || !data.proxyPortListening;
}
function proxyChecksPassed(report) {
  return !report?.stale && ["entry", "node"].every(id => report?.routes?.some(route => route.id === id && route.state === "ok"));
}
function renderConnectionChain(data) {
  const core = data.core || {};
  const ready = core.state === "running" && data.proxyPortListening;
  const proxy = data.systemProxy || {};
  const access = ready && proxy.state === "this_app" && !proxy.recoveryPending;
  const fresh = !connectivityStale(data);
  const passed = fresh && proxyChecksPassed(data.connectivity);
  const service = core.state === "unknown" ? "状态未同步" : ready ? "运行中 · 端口就绪" : core.state === "not_installed" ? "尚未安装内核" : core.state === "running" ? "运行中 · 端口未就绪" : "内核未运行";
  $("#chainService").textContent = service;
  $("#chainServiceDot").className = `status-dot ${ready ? "online" : core.state === "failed" ? "error" : ""}`;
  $("#chainAccess").textContent = proxy.recoveryPending ? "备份待恢复" : access ? proxy.managed ? "已自动接入" : "手动接入 · 未接管" : proxy.state === "this_app" ? "已配置 · 服务未就绪" : core.state === "unknown" ? "状态未确认" : "未接入本程序";
  $("#chainAccessDot").className = `status-dot ${access ? "online" : proxy.recoveryPending ? "warning" : ""}`;
  $("#chainInternet").textContent = passed ? "代理路径检测通过" : fresh ? "有路径未通过" : data.connectivity ? "结果失效 · 待重测" : "尚未检测";
  $("#chainInternetDot").className = `status-dot ${passed ? "online" : fresh ? "warning" : ""}`;
  const name = core.effectiveNode || core.currentNode;
  $("#selectedNodeName").textContent = core.state === "unknown" ? "出口状态未确认" : core.state === "running" ? name || "尚未选择节点" : "代理内核未运行";
  $("#selectedNodeDetail").textContent = core.state === "running" ? "PROXY 策略组实际出口" : "启动内核后读取实际出口";
  $("#nodeCurrent").textContent = core.currentNode || "尚未选择";
  $("#coreEngineSummary").textContent = `${core.version || "尚未安装"} · ${service}`;
  // 侧栏反映系统接入，不再以“内核运行”暗示已启用代理。
  $("#sidebarStatus").textContent = core.state === "unknown" ? "控制服务未连接" : access ? "系统代理已接入" : ready ? "内核运行 · 未接入" : core.state === "not_installed" ? "尚未安装内核" : "代理服务未就绪";
  $("#sidebarDot").className = `status-dot ${access ? "online" : ready ? "warning" : ""}`;
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
  const stale = Boolean(report) && connectivityStale(data);
  const invalidSuccess = data.connection?.level === "ok" && connectivityStale(data);
  const summary = (stale || invalidSuccess) && data.core?.state === "running" && data.proxyPortListening
    ? {level:"warning", title:"代理服务已启动 · 外网待检测", detail:"旧结果已失效，请重新检测。", nextCommand:"/proxy check"}
    : invalidSuccess
      ? {level:"idle",title:"代理服务未就绪 · 当前外网未确认",detail:"旧检测不代表当前网络状态，请启动服务后重测。",nextCommand:"/connect"}
      : data.connection || {level:"warning",title:"外网尚未确认",detail:"请启动服务后检测。",nextCommand:"/proxy check"};
  $("#heroBadge").className = `badge badge-${summary.level === "ok" ? "running" : summary.level === "error" ? "error" : summary.level === "warning" ? "warning" : "muted"}`;
  $("#heroBadge").textContent = ({ok:"最近检测通过",error:"需要处理",warning:"需要确认",idle:"尚未就绪"})[summary.level] || "需要确认";
  $("#heroNode").textContent = summary.title;
  $("#heroDescription").textContent = summary.detail;
  $("#connectionNext").textContent = `下一步  ${summary.nextCommand}`;
  $("#connectivityTime").textContent = report
    ? `${stale ? "结果已失效" : "最近检测"} · ${Number.isFinite(Date.parse(report.checkedAt)) ? new Date(report.checkedAt).toLocaleString() : "检测时间未确认"}${stale ? ` · ${report.staleReason || "超过 2 分钟，请重新检测"}` : " · 2 分钟内有效"}`
    : "尚未检测 · 不会自动发送外网请求";
  $("#connectionHero").dataset.level = summary.level || "idle";
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
    const passed = proxyChecksPassed(report);
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

// 同一列表只保留一个在途读取，避免切页和测速时旧响应覆盖新数据。
async function loadNodes() {
  if (state.nodesLoading) return state.nodesLoading;
  state.nodesLoading = (async () => {
    const list = $("#nodeList");
    list.className = "table-body empty-state";
    list.textContent = "正在读取节点…";
    try {
      state.nodes = await api("/api/v1/nodes") || [];
      state.delays = {};
      $("#navNodeCount").textContent = state.nodes.length;
      const select = $("#nodeProvider");
      const previous = select.value;
      select.replaceChildren();
      const all = textElement("option", "全部订阅"); all.value = "all"; select.append(all);
      [...new Set(state.nodes.map(node => node.providerName).filter(Boolean))].sort().forEach(name => {
        const option = textElement("option", name); option.value = name; select.append(option);
      });
      select.value = state.nodes.some(node => node.providerName === previous) ? previous : "all";
      renderNodes();
    } catch (error) {
      renderEmpty(list, "节点读取失败", error.message, {label:"重新读取",run:()=>loadNodes()});
      $("#nodePageInfo").textContent = "读取失败";
    }
  })();
  try { await state.nodesLoading; } finally { state.nodesLoading = false; }
}
function nodeDelay(node) { return node.cached ? 0 : (state.delays[node.name] ?? node.delay ?? 0); }
function nodePassed(node) {
  return !node.cached && nodeDelay(node) > 0 && (Object.hasOwn(state.delays,node.name) || node.alive);
}
function nodeTested(node) { return node.tested || node.delay > 0 || Object.hasOwn(state.delays,node.name); }
function filteredNodes() {
  const query = ($("#nodeSearch").value || "").trim().toLowerCase();
  const filter = $("#nodeFilter").value || "all";
  const provider = $("#nodeProvider").value || "all";
  const nodes = state.nodes.filter(node =>
    (!query || `${node.name} ${node.type} ${node.providerName || ""}`.toLowerCase().includes(query)) &&
    (provider === "all" || node.providerName === provider) &&
    (filter !== "alive" || nodePassed(node)) && (filter !== "untested" || !nodeTested(node)));
  if ($("#nodeSort").value === "latency") nodes.sort((a,b) =>
    (nodePassed(a) ? nodeDelay(a) : Infinity) - (nodePassed(b) ? nodeDelay(b) : Infinity));
  if ($("#nodeSort").value === "name") nodes.sort((a,b) => a.name.localeCompare(b.name, "zh-CN", {numeric:true}));
  return nodes;
}
function renderNodes() {
  const nodes = filteredNodes();
  const pages = Math.max(1, Math.ceil(nodes.length / state.nodePageSize));
  state.nodePage = Math.min(pages, Math.max(1, state.nodePage));
  const start = (state.nodePage - 1) * state.nodePageSize;
  $("#nodeTotal").textContent = state.nodes.length;
  $("#nodeAvailable").textContent = state.nodes.filter(nodePassed).length;
  $("#nodeCurrent").textContent = state.overview?.core?.currentNode || "尚未选择";
  $("#nodePageInfo").textContent = nodes.length ? `第 ${start+1}–${Math.min(start+state.nodePageSize,nodes.length)} 个 · 共 ${nodes.length} 个` : "0 个符合条件的节点";
  $("#nodePrevious").disabled = state.nodePage === 1;
  $("#nodeNext").disabled = state.nodePage === pages;
  const list = $("#nodeList");
  list.replaceChildren();
  list.className = nodes.length ? "table-body" : "table-body empty-state";
  if (!nodes.length) {
    const loaded = state.nodes.length > 0;
    renderEmpty(list, loaded ? "没有匹配的节点" : "暂时没有可用节点",
      loaded ? "尝试其他关键词，或清除订阅和状态筛选。" : "先添加订阅并更新节点；内核停止时可能仅显示离线缓存。",
      {label:loaded ? "清除筛选" : "管理订阅",run:()=>loaded ? resetNodeFilters() : switchView("subscriptions")});
    return;
  }
  nodes.slice(start,start+state.nodePageSize).forEach(node => {
    const selected = !node.cached && state.overview?.core?.currentNode === node.name;
    const row = document.createElement("div"); row.className = `node-row${selected ? " selected" : ""}`;
    const name = document.createElement("div"); name.className = "node-name";
    const avatar = textElement("span",node.name.trim().slice(0,1).toUpperCase() || "N","node-avatar");
    name.append(avatar,textElement("strong",node.name));
    const type = textElement("span",node.type || "—");
    const provider = textElement("span",node.providerName || "—");
    const value = nodeDelay(node);
    const delay = textElement("span",value ? `${value} ms` : "—",`latency ${value && value<120 ? "good" : value && value<250 ? "medium" : value ? "bad" : ""}`);
    const status = document.createElement("span"); status.className = `node-status ${nodePassed(node) ? "alive" : ""}`;
    status.append(document.createElement("i"),textElement("span",node.cached ? "离线缓存" : !nodeTested(node) ? "未测速" : nodePassed(node) ? "检查通过" : "未通过"));
    const action = textElement("button",node.cached ? "离线" : selected ? "使用中" : "选择","button button-secondary node-action");
    action.disabled = node.cached || selected;
    action.setAttribute("aria-label",`${selected ? "正在使用" : "选择"}节点 ${node.name}`);
    action.addEventListener("click",()=>selectNode(node.name,action));
    row.append(name,type,provider,delay,status,action); list.append(row);
  });
}
function resetNodeFilters() {
  $("#nodeSearch").value = "";
  $("#nodeFilter").value = "all";
  $("#nodeProvider").value = "all";
  $("#nodeSort").value = "default";
  state.nodePage = 1;
  renderNodes();
}

async function testNodes(button = $("#testNodesButton")) {
  if (state.testingNodes) return;
  state.testingNodes = true;
  setBusy(button,true,"测速中…");
  try { const delays=await api("/api/v1/nodes/test",{method:"POST"});await loadNodes();state.delays=delays;renderNodes();const values=Object.values(delays);const passed=values.filter(delay=>delay>0).length;const summary=`测速 ${values.length} 个真实节点：${passed} 个通过，${values.length-passed} 个未通过`;$("#nodeTestSummary").textContent=summary;toast(summary); }
  catch(error){toast(error.message,"error");}
  finally{state.testingNodes=false;setBusy(button,false);}
}

async function selectNode(name,button){setBusy(button,true,"切换中…");try{await api("/api/v1/nodes/select",{method:"POST",body:JSON.stringify({name})});toast(`已切换到 ${name}`);await refreshOverview(true);renderNodes();}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}

async function loadSubscriptions() {
  const grid = $("#subscriptionGrid");
  try {
    const [items,groups] = await Promise.all([api("/api/v1/subscriptions"),api("/api/v1/subscription-groups")]);
    state.subscriptionGroups = groups || [];
    renderSubscriptionGroups();
    state.subscriptions = items || [];
    renderSubscriptions(items || []);
  } catch (error) {
    grid.replaceChildren();
    const empty = document.createElement("div"); empty.className = "empty-card";
    renderEmpty(empty,"订阅读取失败",error.message,{label:"重试",run:loadSubscriptions});
    grid.append(empty);
    $("#subscriptionCount").textContent = "同步失败";
  }
}

function renderSubscriptionGroups() {
  ["#subscriptionGroupSelect", "#subGroup"].forEach(selector => {
    const select = $(selector);
    const previous = select.value;
    select.replaceChildren();
    state.subscriptionGroups.forEach(group => {
      const option = document.createElement("option");
      option.value = group.name;
      option.textContent = `${group.enabled ? "已启用" : "已禁用"} · ${group.name === "default" ? "默认分组" : group.name}`;
      select.append(option);
    });
    if (state.subscriptionGroups.some(group => group.name === previous)) select.value = previous;
  });
  updateSubscriptionGroupControls();
}
function updateSubscriptionGroupControls() {
  const group = state.subscriptionGroups.find(item => item.name === $("#subscriptionGroupSelect").value);
  $("#toggleSubscriptionGroup").textContent = group?.enabled ? "禁用此组" : "启用此组";
  $("#toggleSubscriptionGroup").disabled = !group;
  $("#saveSubscriptionGroupName").disabled = !group || group.name === "default";
  $("#deleteSubscriptionGroup").disabled = !group || group.name === "default";
  $("#renameSubscriptionGroup").disabled = !group || group.name === "default";
  $("#renameSubscriptionGroup").value = group?.name === "default" ? "" : group?.name || "";
}
function renderSubscriptions(items) {
  const grid = $("#subscriptionGrid");
  grid.replaceChildren();
  $("#subscriptionCount").textContent = `${items.length} 个来源 · ${items.filter(item=>item.enabled).length} 个启用`;
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "empty-card";
    renderEmpty(empty,"还没有订阅来源","添加 Clash / Mihomo 兼容地址，统一管理你的连接。",{label:"添加第一个订阅",run:openSubscriptionModal});
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
    const auth = (sub.authType || "none").toLowerCase();
    const authNames = {none:"无需额外认证",basic:"Basic 认证",bearer:"Bearer 认证",token:"Token 请求头",aes:"AES 解密",age:"age 私钥解密"};
    const authLabel = sub.downloadAuthType
      ? [authNames[sub.downloadAuthType] || sub.downloadAuthType, sub.decryptionType && sub.decryptionType !== "none" ? authNames[sub.decryptionType] : ""].filter(Boolean).join(" · ")
      : authNames[auth] || `${auth.toUpperCase()} 认证`;
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
    addAction("编辑", () => openSubscriptionModal(sub));
    addAction("更新", button => updateSubscription(sub.name, button, route.value));
    addAction("检查", button => testSubscription(sub.name, button, route.value));
    addAction("删除", () => removeSubscription(sub.name));
    actions.lastElementChild.classList.add("subscription-delete");
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

async function loadCores() {
  const list = $("#coreList");
  list.className = "table-body empty-state";
  list.textContent = "正在读取内核版本…";
  try {
    const items = await api("/api/v1/core/installations") || [];
    list.replaceChildren();
    list.className = items.length ? "table-body" : "table-body empty-state";
    if (!items.length) {
      renderEmpty(list,"尚未安装内核","安装最新稳定版 Mihomo，即可开始配置你的代理。");
      return;
    }
    items.forEach((item,index) => {
      const row = document.createElement("div"); row.className = "core-row";
      const version = textElement("strong",`#${index+1} ${item.version}`);
      const engine = textElement("span",item.engine);
      const status = textElement("span",item.active ? "当前使用" : "已安装",`pill ${item.active ? "online" : ""}`);
      const path = textElement("code",item.path);
      const actions = document.createElement("div"); actions.className = "inline-actions";
      if (!item.active) {
        const use = textElement("button","使用","button button-secondary");
        use.addEventListener("click",()=>useCore(item.version,use));
        const remove = textElement("button","删除","button button-danger");
        remove.addEventListener("click",()=>removeCore(item.version));
        actions.append(use,remove);
      }
      row.append(version,engine,status,path,actions); list.append(row);
    });
  } catch (error) { renderEmpty(list,"内核版本读取失败",error.message,{label:"重试",run:loadCores}); }
}

async function useCore(version,button){setBusy(button,true,"切换中…");try{await api("/api/v1/core/use",{method:"POST",body:JSON.stringify({reference:version})});toast(`已切换到 ${version}`);await loadCores();await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function removeCore(version){if(!confirm(`确认删除 Mihomo ${version}？`))return;try{await api(`/api/v1/core/installations?reference=${encodeURIComponent(version)}`,{method:"DELETE"});toast(`${version} 已删除`);await loadCores();}catch(error){toast(error.message,"error");}}

async function loadRouting() {
  try {
    state.routing = await api("/api/v1/routing");
    renderRouting();
  } catch (error) {
    $("#routingActive").textContent = "读取失败";
    const profiles = $("#routeProfiles"); profiles.replaceChildren();
    const empty = document.createElement("div"); empty.className = "empty-card";
    renderEmpty(empty,"路由策略读取失败",error.message,{label:"重试",run:loadRouting});
    profiles.append(empty);
  }
}

function renderRouting() {
  const routing = state.routing;
  $("#routingActive").textContent = `当前：${profileLabel(routing.activeProfile)}`;
  const profiles = $("#routeProfiles");
  profiles.replaceChildren();
  // Go 的空切片可能编码成 null；无规则组的 Global/Direct 是有效配置，不能使整页渲染中断。
  (routing.profiles || []).forEach(profile => {
    const card = document.createElement("article");
    const active = profile.name === routing.activeProfile;
    card.className = `subscription-card ${active ? "profile-active" : ""}`;
    const title = document.createElement("div");
    title.className = "subscription-title";
    const heading = document.createElement("h3"); heading.textContent = profileLabel(profile.name);
    const badge = document.createElement("span"); badge.className = `pill ${active ? "online" : ""}`;
    badge.textContent = active ? "使用中" : ({proxy:"默认代理",direct:"默认直连",reject:"默认拒绝"})[profile.defaultAction] || profile.defaultAction;
    title.append(heading, badge);
    const description = document.createElement("p"); description.className = "subscription-url";
    const names = profile.groups || [];
    description.textContent = names.length ? "已关联规则组（按顺序匹配）" : "尚未关联规则组；未命中流量按默认动作处理";
    const button = document.createElement("button"); button.className = "button button-secondary";
    button.textContent = "应用配置"; button.disabled = active;
    button.addEventListener("click", () => useRouteProfile(profile.name, button));
    const note = textElement("p",profileDescription(profile.name), "profile-note");
    const chips = document.createElement("div"); chips.className = "route-chip-list";
    names.forEach(name => {
      const chip = document.createElement("button"); chip.className = "route-chip"; chip.type = "button";
      chip.textContent = `${name} ×`; chip.title = `从 ${profile.name} 解除 ${name}`;
      chip.addEventListener("click", () => { if (confirm(`从“${profile.name}”解除“${name}”？规则组不会被删除。`)) routeMutation("/api/v1/routing/profiles", "PATCH", {profile:profile.name,group:name,attached:false}, "规则组已解除关联"); });
      chips.append(chip);
    });
    const actions = document.createElement("div"); actions.className = "subscription-actions route-card-actions";
    actions.append(button);
    const available = (routing.ruleGroups || []).filter(group => !names.includes(group.name));
    if (available.length) {
      const select = document.createElement("select"); select.className = "select"; select.setAttribute("aria-label", `给 ${profile.name} 关联规则组`);
      available.forEach(group => {const option=document.createElement("option");option.value=group.name;option.textContent=group.name;select.append(option);});
      const attach = document.createElement("button"); attach.className = "button button-secondary"; attach.type = "button"; attach.textContent = "关联组";
      attach.addEventListener("click", () => routeMutation("/api/v1/routing/profiles", "PATCH", {profile:profile.name,group:select.value,attached:true}, "规则组已关联"));
      actions.append(select, attach);
    }
    if (!isBuiltinRouteProfile(profile.name) && !active) {
      const remove = document.createElement("button"); remove.className = "button button-secondary"; remove.type = "button"; remove.textContent = "删除方案";
      remove.addEventListener("click", () => { if (confirm(`删除自定义方案“${profile.name}”？`)) routeMutation(`/api/v1/routing/profiles?name=${encodeURIComponent(profile.name)}`, "DELETE", null, "方案已删除"); });
      actions.append(remove);
    }
    card.append(title, description, chips, note, actions); profiles.append(card);
  });
  const select = $("#routeRuleGroup"); select.replaceChildren();
  const groups = $("#routeGroups"); groups.replaceChildren();
  $("#addRouteRule").disabled = !(routing.ruleGroups || []).length;
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
      const actions = document.createElement("div"); actions.className = "rule-item-actions";
      const action = (label, run, disabled = false) => {
        const button = document.createElement("button"); button.type = "button"; button.className = "text-button";
        button.textContent = label; button.disabled = disabled; button.addEventListener("click", run); actions.append(button);
      };
      action("编辑", () => openRouteRuleEditor(row, group.name, index + 1, rule));
      action("↑", () => routeMutation("/api/v1/routing/rules/move", "POST", {group:group.name,from:index+1,to:index,expected:rule}, "规则已上移"), index === 0);
      action("↓", () => routeMutation("/api/v1/routing/rules/move", "POST", {group:group.name,from:index+1,to:index+2,expected:rule}, "规则已下移"), index === rules.length - 1);
      action("删除", () => removeRouteRule(group.name, index + 1, rule));
      row.append(actions); list.append(row);
    });
    if (!rules.length) { const hint = document.createElement("p"); hint.textContent = "尚无规则，可使用上方表单添加。"; list.append(hint); }
    card.append(list);
    if (!["中国大陆直连", "指定地址代理", "指定地址直连"].includes(group.name)) {
      const remove = document.createElement("button"); remove.className = "button button-secondary"; remove.type = "button"; remove.textContent = "删除规则组";
      remove.addEventListener("click", () => { if (confirm(`删除规则组“${group.name}”？若仍被方案引用，需先解除关联。`)) routeMutation(`/api/v1/routing/groups?name=${encodeURIComponent(group.name)}`, "DELETE", null, "规则组已删除"); });
      card.append(remove);
    }
    groups.append(card);
  });
  if (!(routing.ruleGroups || []).length) {
    const empty = document.createElement("div"); empty.className = "empty-card";
    renderEmpty(empty, "还没有规则组", "先在上方创建规则组，再添加规则并关联到方案。", {label:"定位到创建入口",run:()=>$("#newRouteGroup").focus()});
    groups.append(empty);
  }
}
function isBuiltinRouteProfile(name) { return ["global","direct","rule","bypass-cn","proxy-only","bypass-list"].includes(name); }
async function routeMutation(path, method, payload, success) {
  if (state.routeBusy) return false;
  state.routeBusy = true;
  try {
    await api(path, {method, ...(payload ? {body:JSON.stringify(payload)} : {})});
    toast(success); await loadRouting(); await refreshOverview(true);
    return true;
  } catch (error) { toast(error.message, "error"); return false; }
  finally { state.routeBusy = false; }
}
function createRouteProfile() {
  const name = $("#newRouteProfile").value.trim();
  if (!name) { toast("请输入方案名称", "error"); $("#newRouteProfile").focus(); return; }
  void routeMutation("/api/v1/routing/profiles", "POST", {name,defaultAction:$("#newRouteDefault").value,groups:[]}, "方案已创建").then(saved => { if (saved) $("#newRouteProfile").value = ""; });
}
function createRouteGroup() {
  const name = $("#newRouteGroup").value.trim();
  if (!name) { toast("请输入规则组名称", "error"); $("#newRouteGroup").focus(); return; }
  void routeMutation("/api/v1/routing/groups", "POST", {name}, "规则组已创建").then(saved => { if (saved) $("#newRouteGroup").value = ""; });
}
async function useRouteProfile(name,button){setBusy(button,true,"应用中…");try{await api("/api/v1/routing/profiles/use",{method:"POST",body:JSON.stringify({name})});toast(`已应用 ${name}`);await loadRouting();await refreshOverview(true);}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
async function addRouteRule(){const button=$("#addRouteRule");setBusy(button,true,"添加中…");const input={group:$("#routeRuleGroup").value,rule:{action:$("#routeRuleAction").value,type:$("#routeRuleType").value,value:$("#routeRuleValue").value.trim()}};try{if(!input.rule.value)throw new Error("请输入匹配值");await api("/api/v1/routing/rules",{method:"POST",body:JSON.stringify(input)});$("#routeRuleValue").value="";toast("路由规则已添加");await loadRouting();}catch(error){toast(error.message,"error");}finally{setBusy(button,false);}}
function openRouteRuleEditor(row, group, index, rule) {
  const existing = row.nextElementSibling;
  if (existing?.classList.contains("rule-edit-form")) { existing.remove(); return; }
  const editor = document.createElement("form"); editor.className = "rule-edit-form";
  const type = $("#routeRuleType").cloneNode(true); type.removeAttribute("id"); type.value = rule.type;
  const value = document.createElement("input"); value.value = rule.value; value.required = true; value.setAttribute("aria-label", "规则匹配值");
  const action = $("#routeRuleAction").cloneNode(true); action.removeAttribute("id"); action.value = rule.action;
  const save = document.createElement("button"); save.type = "submit"; save.className = "button button-primary"; save.textContent = "保存规则";
  const cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button button-secondary"; cancel.textContent = "取消";
  cancel.addEventListener("click", () => editor.remove());
  editor.append(type, value, action, save, cancel);
  editor.addEventListener("submit", async event => {
    event.preventDefault();
    const updated = {type:type.value,value:value.value.trim(),action:action.value};
    if (!updated.value) { toast("请输入规则匹配值", "error"); return; }
    setBusy(save, true, "保存中…");
    const saved = await routeMutation("/api/v1/routing/rules", "PATCH", {group,index,expected:rule,rule:updated}, "规则已修改");
    if (!saved) setBusy(save, false);
  });
  row.after(editor); value.focus();
}
async function removeRouteRule(group,index,expected){if(!confirm(`删除规则组“${group}”中的第 ${index} 条规则？此操作会重载内核。`))return;await routeMutation(`/api/v1/routing/rules?group=${encodeURIComponent(group)}&index=${index}`,"DELETE",{expected},"规则已删除");}

async function runDoctor() {
  const button = $("#runDoctorButton");
  if (state.doctorBusy) return;
  state.doctorBusy = true; setBusy(button,true,"检查中…");
  $("#doctorSummary").textContent = "正在检查当前主机的安装、配置与运行环境…";
  try {
    const items = await api("/api/v1/doctor") || [];
    const list = $("#doctorList"); list.replaceChildren();
    const issues = items.filter(item => item.status !== "ok");
    $("#doctorHeadline").textContent = !items.length ? "没有返回检查项目" : issues.length ? "有项目需要关注" : "环境检查通过";
    $("#doctorSummary").textContent = `共检查 ${items.length} 项，${items.filter(item=>item.status==="ok").length} 项通过，${issues.length} 项需要关注。诊断不代表外网一定可用。`;
    $("#doctorCheckedAt").textContent = new Date().toLocaleTimeString([], {hour:"2-digit",minute:"2-digit"});
    if (!items.length) renderEmpty(list,"暂无检查结果","可再次运行诊断，或查看内核日志。");
    items.forEach(item => {
      const row = document.createElement("div"); row.className = `doctor-item ${["ok","warning","error"].includes(item.status) ? item.status : "warning"}`;
      const icon = textElement("span",item.status==="ok"?"✓":item.status==="warning"?"!":"×","doctor-icon");
      const content = document.createElement("div");
      content.append(textElement("strong",item.name),textElement("p",item.message));
      row.append(icon,content); list.append(row);
    });
  } catch (error) {
    $("#doctorHeadline").textContent = "诊断未完成";
    $("#doctorSummary").textContent = error.message;
    toast(error.message,"error");
  } finally { state.doctorBusy = false; setBusy(button,false); }
}
// 不强制抢走用户阅读较早日志的位置；只有接近底部时才跟随新输出。
function renderLogs() {
  const output = $("#logOutput");
  const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 55;
  const position = output.scrollTop;
  const query = ($("#logSearch").value || "").trim().toLowerCase();
  const lines = state.logs.filter(line => !query || line.toLowerCase().includes(query));
  output.textContent = lines.length ? lines.join("\n") : query ? "没有匹配的日志。" : "暂时没有内核日志。启动内核后，输出将在这里显示。";
  $("#logCount").textContent = query ? `${lines.length} / ${state.logs.length} 条` : `${state.logs.length} 条`;
  output.scrollTop = atBottom ? output.scrollHeight : position;
}
async function loadLogs() {
  if (state.logsLoading) return;
  state.logsLoading = true;
  try {
    state.logs = await api("/api/v1/logs?limit=300") || [];
    renderLogs();
    $("#logUpdatedAt").textContent = `最近同步 ${new Date().toLocaleTimeString([], {hour:"2-digit",minute:"2-digit",second:"2-digit"})}`;
  } catch (error) {
    $("#logUpdatedAt").textContent = "同步失败";
    if (!state.logs.length) $("#logOutput").textContent = error.message;
    else $("#logUpdatedAt").textContent = "同步失败 · 保留上次日志";
  } finally { state.logsLoading = false; }
}
async function copyText(value, message) {
  try {
    if (!navigator.clipboard?.writeText) throw new Error("当前浏览器或连接不支持剪贴板，请手动复制。");
    await navigator.clipboard.writeText(value);
    toast(message);
  } catch (error) { toast(error.message,"error"); }
}
function downloadLogs() {
  if (!state.logs.length) { toast("暂时没有日志可导出","error"); return; }
  // 导出只在浏览器本地生成，不向外部服务器上传日志。
  const url = URL.createObjectURL(new Blob([state.logs.join("\n")],{type:"text/plain;charset=utf-8"}));
  const link = document.createElement("a"); link.href = url;
  link.download = `kivo-mihomo-${new Date().toISOString().slice(0,10)}.log`;
  link.click(); setTimeout(()=>URL.revokeObjectURL(url),1000);
}
function profileLabel(name) {
  return ({"rule":"规则分流","global":"全局代理","direct":"全部直连","bypass-cn":"绕过大陆","proxy-only":"仅指定走代理","bypass-list":"仅指定不代理"})[name] || name || "未选择";
}
function profileDescription(name) {
  return ({"rule":"按规则匹配流量，未命中时默认代理。","global":"未命中的流量默认走代理。","direct":"未命中的流量默认直连。","bypass-cn":"中国大陆相关流量直连，其余流量走代理。","proxy-only":"只让匹配规则的流量通过代理。","bypass-list":"为默认代理的流量设置直连例外。"})[name] || "按规则组顺序匹配，未命中时使用默认动作。";
}

async function loadSettings() {
  try {
    const [settings,security] = await Promise.all([api("/api/v1/settings"),api("/api/v1/web/security")]);
    state.settings = settings;
    $("#settingPort").value = settings.mixedPort;
    $("#settingMode").value = settings.mode;
    $("#settingLAN").checked = settings.allowLAN;
    $("#settingTun").checked = settings.tunEnabled;
    $("#settingDownloadProxy").value = settings.downloadProxy || "";
    $("#settingDownloadRetry").value = settings.downloadRetry || 4;
    state.settingsDirty = false;
    renderWebSecurity(security);
    $("#settingsForm button[type='submit']").disabled = false;
  } catch (error) {
    $("#settingsForm button[type='submit']").disabled = true;
    toast(`设置读取失败，保存已禁用：${error.message}`,"error");
  }
}
function renderWebSecurity(security) {
  state.authEnabled = security.authEnabled;
  const badge = $("#webAuthBadge");
  badge.className = `pill ${security.authEnabled ? "online" : ""}`;
  badge.textContent = security.authEnabled ? "已开启鉴权" : "无需 Token";
  $("#webSecurityWarning").classList.toggle("danger",!security.authEnabled);
  if (!security.authEnabled) {
    state.token = "";
    try { sessionStorage.removeItem("kivo_token"); } catch { /* 凭据已在内存清空。 */ }
  }
}

// 对话框保留打开前焦点；Esc 关闭、Tab 限制在对话框内，避免键盘进入遮罩后的页面。
function updateSubscriptionAuthFields() {
  const type = $("#subAuth").value;
  $("#subUsernameRow").classList.toggle("hidden",type !== "basic");
  $("#subSecretRow").classList.toggle("hidden",type === "none");
  $("#subDecryptSecretRow").classList.toggle("hidden",$("#subDecrypt").value === "none");
}
function openSubscriptionModal(sub = null) {
  const editing = sub && typeof sub.name === "string" ? sub : null;
  closeCommandDialog(false);
  state.modalReturnFocus = document.activeElement;
  const modal = $("#subscriptionModal");
  const form = $("#subscriptionForm");
  form.reset();
  state.editingSubscription = editing;
  $("#subscriptionTitle").textContent = editing ? `编辑订阅 · ${editing.name}` : "添加订阅";
  $("#subscriptionSubmit").textContent = editing ? "保存修改" : "添加订阅";
  $("#subURL").required = !editing;
  $("#subURL").placeholder = editing ? "留空保留原地址；不回显 URL 中的 Token" : "https://example.com/subscribe?token=…";
  $("#subURLHint").textContent = editing ? "为了保护 URL 内的 Token，原地址不回填；留空即保留。" : "支持 Clash / Mihomo 兼容订阅";
  if (editing) {
    $("#subName").value = editing.name;
    $("#subGroup").value = editing.group;
    $("#subUpdateVia").value = editing.updateVia || "direct";
    $("#subAuth").value = editing.downloadAuthType || "none";
    $("#subDecrypt").value = editing.decryptionType || "none";
    $("#subInterval").value = editing.updateInterval || 3600;
    $("#subHealth").value = editing.healthInterval || 300;
    $("#subPrefix").value = editing.additionalPrefix || "";
    $("#subUserAgent").value = editing.options?.userAgent || "";
    $("#subFilter").value = editing.options?.filter || "";
    $("#subExcludeFilter").value = editing.options?.excludeFilter || "";
    $("#subUDP").value = editing.options?.udp || "default";
    $("#subTFO").value = editing.options?.tfo || "default";
    $("#subSkipCertVerify").value = editing.options?.skipCertVerify || "default";
  }
  $("#subscriptionError").textContent = "";
  updateSubscriptionAuthFields();
  modal.classList.add("active"); modal.setAttribute("aria-hidden","false");
  document.body.classList.add("modal-open");
  $(".app-shell").inert = true;
  $("#subName").focus();
  if (!state.subscriptionGroups.length) loadSubscriptions();
}
function closeSubscriptionModal() {
  if (state.addingSubscription) return;
  const modal = $("#subscriptionModal");
  modal.classList.remove("active"); modal.setAttribute("aria-hidden","true");
  document.body.classList.remove("modal-open");
  syncDialogState();
  $("#subscriptionError").textContent = "";
  state.editingSubscription = null;
  state.modalReturnFocus?.focus();
}
function handleDialogKey(event) {
  if (event.isComposing) return;
  const login = $("#loginOverlay").classList.contains("active");
  const modal = $("#subscriptionModal").classList.contains("active");
  const command = !login && !modal && $("#commandOverlay").classList.contains("active");
  if (command && event.target === $("#commandInput") && ["ArrowDown","ArrowUp","Enter"].includes(event.key)) {
    event.preventDefault();
    if (event.key === "Enter") chooseCommandResult(state.commandIndex);
    else selectCommandResult(state.commandIndex + (event.key === "ArrowDown" ? 1 : -1));
    return;
  }
  if (event.key === "Escape") {
    if (command) { event.preventDefault(); closeCommandDialog(); }
    else if (modal && !login) { event.preventDefault(); closeSubscriptionModal(); }
    else if ($(".sidebar").classList.contains("open")) { setSidebarOpen(false); $("#mobileMenu").focus(); }
    return;
  }
  if (event.key !== "Tab" || (!login && !modal && !command)) return;
  const dialog = $(login ? "#loginForm" : modal ? "#subscriptionForm" : "#commandDialog");
  const focusable = [...dialog.querySelectorAll('button:not(:disabled),input:not(:disabled),select:not(:disabled),summary,[tabindex="0"]')].filter(item=>item.tabIndex >= 0 && item.getClientRects().length);
  if (!focusable.length) return;
  const first = focusable[0], last = focusable[focusable.length-1];
  if (event.shiftKey && (document.activeElement === first || !dialog.contains(document.activeElement))) { event.preventDefault(); last.focus(); }
  else if (!event.shiftKey && (document.activeElement === last || !dialog.contains(document.activeElement))) { event.preventDefault(); first.focus(); }
}

// 将表单事务与事件绑定分离，让页面操作可独立测试和扩展。
async function handleLoginSubmit(event) {
  event.preventDefault();
  if (state.loggingIn) return;
  state.loggingIn = true;
  const button = event.submitter;
  setBusy(button,true,"正在连接…");
  state.token = $("#loginSecret").value.trim();
  try {
    await api("/api/v1/session/verify",{method:"POST"});
    persistPreference(sessionStorage,"kivo_token",state.token);
    $("#loginSecret").value = "";
    hideLogin();
    await bootstrap();
  } catch (error) {
    state.token = "";
    $("#loginError").textContent = error.message === "认证失败" ? "Token 不正确或已失效，请确认服务主机的配置。" : `无法连接控制服务：${error.message}`;
  } finally { state.loggingIn = false; setBusy(button,false); }
}
async function handleSubscriptionSubmit(event) {
  event.preventDefault();
  if (state.addingSubscription) return;
  state.addingSubscription = true;
  const button = event.submitter;
  const editing = state.editingSubscription;
  setBusy(button,true,editing ? "保存中…" : "添加中…");
  $("#subscriptionError").textContent = "";
  const authType = $("#subAuth").value, decryptType = $("#subDecrypt").value;
  const options = {userAgent:$("#subUserAgent").value.trim(),filter:$("#subFilter").value.trim(),
    excludeFilter:$("#subExcludeFilter").value.trim(),udp:$("#subUDP").value,tfo:$("#subTFO").value,
    skipCertVerify:$("#subSkipCertVerify").value};
  const input = {
    name:$("#subName").value.trim(),url:$("#subURL").value.trim(),
    downloadAuth:{type:authType,username:authType === "basic" ? $("#subUsername").value : "",secret:$("#subSecret").value},
    decryption:{type:decryptType,secret:$("#subDecryptSecret").value},options,
    updateVia:$("#subUpdateVia").value,group:$("#subGroup").value,
    updateInterval:Number($("#subInterval").value),healthInterval:Number($("#subHealth").value),
    healthCheckURL:"https://www.gstatic.com/generate_204",additionalPrefix:$("#subPrefix").value,
  };
  try {
    let result;
    if (editing) {
      const patch = {reference:editing.name,revision:editing.revision,name:input.name,group:input.group,
        updateVia:input.updateVia,updateInterval:input.updateInterval,healthInterval:input.healthInterval,
        additionalPrefix:input.additionalPrefix,options};
      if (input.url) patch.url = input.url;
      patch.downloadAuth = {type:authType};
      if (authType === "basic" && $("#subUsername").value) patch.downloadAuth.username = $("#subUsername").value;
      if ($("#subSecret").value) patch.downloadAuth.secret = $("#subSecret").value;
      patch.decryption = {type:decryptType};
      if ($("#subDecryptSecret").value) patch.decryption.secret = $("#subDecryptSecret").value;
      result = await api("/api/v1/subscriptions",{method:"PATCH",body:JSON.stringify(patch)});
    } else {
      result = await api("/api/v1/subscriptions",{method:"POST",body:JSON.stringify(input)});
    }
    toast(result?.warning ? result.message : `订阅 ${input.name} 已${editing ? "更新" : "添加"}`, result?.warning ? "error" : "success");
    event.target.reset();
    state.addingSubscription = false;
    closeSubscriptionModal();
    await loadSubscriptions();
    await refreshOverview(true);
  } catch (error) { $("#subscriptionError").textContent = error.message; }
  finally { state.addingSubscription = false; setBusy(button,false); }
}
async function handleSettingsSubmit(event) {
  event.preventDefault();
  if (state.settingsSaving) return;
  if (!state.settings) { toast("请先刷新并成功读取设置，再保存。","error"); return; }
  state.settingsSaving = true;
  const button = event.submitter;
  setBusy(button,true,"应用中…");
  const settings = {
    // 此页面不编辑控制台监听地址；保留持久配置，而不是写入 serve --listen 的临时覆盖。
    listen:state.settings.listen,
    mixedPort:Number($("#settingPort").value),mode:$("#settingMode").value,
    allowLAN:$("#settingLAN").checked,tunEnabled:$("#settingTun").checked,
    downloadProxy:$("#settingDownloadProxy").value.trim(),downloadRetry:Number($("#settingDownloadRetry").value),
  };
  try {
    state.settings = await api("/api/v1/settings",{method:"PATCH",body:JSON.stringify(settings)});
    state.settingsDirty = false;
    toast("设置已保存并应用");
    await refreshOverview(true);
  } catch (error) { toast(error.message,"error"); }
  finally { state.settingsSaving = false; setBusy(button,false); }
}
async function handleWebSecuritySubmit(event) {
  event.preventDefault();
  if (state.securitySaving) return;
  const button = event.submitter;
  const token = $("#settingWebToken").value.trim();
  $("#webSecurityError").textContent = "";
  if (!token && state.authEnabled && !confirm("确认关闭 Web Token 鉴权？任何能访问控制台地址的设备都将拥有完整管理权限。")) return;
  state.securitySaving = true;
  setBusy(button,true,"保存中…");
  try {
    const security = await api("/api/v1/web/security",{method:"PATCH",body:JSON.stringify({token})});
    state.token = token;
    // 空字符串是明确的清空标记，避免旧名称的偏好再次迁移出旧凭据。
    persistPreference(sessionStorage,"kivo_token",token);
    $("#settingWebToken").value = "";
    renderWebSecurity(security);
    toast(token ? "新的 Web Token 已立即生效" : "Web Token 鉴权已关闭");
  } catch (error) { $("#webSecurityError").textContent = error.message; }
  finally { state.securitySaving = false; setBusy(button,false); }
}
async function useSubscriptionGroup(event) {
  const name = $("#subscriptionGroupSelect").value;
  if (!name || state.groupBusy) return;
  if (!confirm(`独占使用“${name}”组？其他分组与组外订阅会停用，运行中的内核将重载。`)) return;
  state.groupBusy = true; setBusy(event.currentTarget,true);
  try {
    await api("/api/v1/subscription-groups/action",{method:"POST",body:JSON.stringify({name,action:"use"})});
    toast(`已独占启用 ${name}`);
    await loadSubscriptions(); await refreshOverview(true);
  } catch (error) { toast(error.message,"error"); }
  finally { state.groupBusy = false; setBusy($("#useSubscriptionGroup"),false); }
}
async function createSubscriptionGroup(event) {
  const name = $("#newSubscriptionGroup").value.trim();
  if (!name || state.groupBusy) return;
  state.groupBusy = true; setBusy(event.currentTarget,true);
  try {
    await api("/api/v1/subscription-groups",{method:"POST",body:JSON.stringify({name})});
    $("#newSubscriptionGroup").value = "";
    toast(`分组 ${name} 已创建`);
    await loadSubscriptions();
  } catch (error) { toast(error.message,"error"); }
  finally { state.groupBusy = false; setBusy($("#createSubscriptionGroup"),false); }
}
async function changeSubscriptionGroup(action, button) {
  const group = state.subscriptionGroups.find(item => item.name === $("#subscriptionGroupSelect").value);
  if (!group || state.groupBusy) return;
  const newName = $("#renameSubscriptionGroup").value.trim();
  if (action === "rename" && (!newName || newName === group.name)) {
    toast("请输入不同的新分组名称", "error"); return;
  }
  if (action === "delete" && !confirm(`删除空分组“${group.name}”？有订阅的分组需要先移动订阅。`)) return;
  state.groupBusy = true; setBusy(button, true, "处理中…");
  try {
    if (action === "rename") await api("/api/v1/subscription-groups", {method:"PATCH",body:JSON.stringify({name:group.name,newName})});
    else if (action === "delete") await api(`/api/v1/subscription-groups?name=${encodeURIComponent(group.name)}`, {method:"DELETE"});
    else await api("/api/v1/subscription-groups/action", {method:"POST",body:JSON.stringify({name:group.name,action:group.enabled ? "disable" : "enable"})});
    await loadSubscriptions(); await refreshOverview(true);
    if (action === "rename") $("#subscriptionGroupSelect").value = newName;
    updateSubscriptionGroupControls();
    toast(action === "rename" ? `已重命名为 ${newName}` : action === "delete" ? "分组已删除" : "分组状态已更新");
  } catch (error) { toast(error.message, "error"); }
  finally { state.groupBusy = false; setBusy(button, false); }
}
function bindEvents() {
  const on = (selector,event,handler) => $(selector).addEventListener(event,handler);
  $$(".nav-item").forEach(item=>item.addEventListener("click",()=>switchView(item.dataset.view)));
  $$("[data-goto]").forEach(item=>item.addEventListener("click",()=>switchView(item.dataset.goto)));
  on("#mobileMenu","click",()=>setSidebarOpen(!$(".sidebar").classList.contains("open")));
  on("#sidebarBackdrop","click",()=>setSidebarOpen(false));
  window.matchMedia("(max-width:760px)").addEventListener("change",()=>setSidebarOpen(false));
  document.addEventListener("keydown",handleDialogKey);
  document.addEventListener("keydown",handleShortcuts);
  on("#commandButton","click",openCommandDialog);
  on("#commandInput","input",renderCommandResults);
  on("#closeCommandButton","click",()=>closeCommandDialog());
  on("#commandOverlay","click",event=>{if(event.target===event.currentTarget)closeCommandDialog();});
  on("#sidebarToggle","click",()=>setSidebarCollapsed(document.documentElement.dataset.sidebar !== "collapsed"));
  $$("[data-theme-choice]").forEach(button=>button.addEventListener("click",()=>applyTheme(button.dataset.themeChoice)));
  on("#subscriptionModal","click",event=>{if(event.target===event.currentTarget)closeSubscriptionModal();});
  on("#refreshButton","click",async event=>{
    if (state.activeView === "settings" && state.settingsDirty && !confirm("刷新会重新读取设置，丢弃尚未保存的修改。继续吗？")) return;
    setBusy(event.currentTarget,true,"刷新中…");
    try { await refreshOverview(); await loadActiveView(); }
    finally { setBusy($("#refreshButton"),false); }
  });
  on("#quickStartButton","click",coreAction);
  on("#heroAction","click",coreAction);
  on("#systemProxyOn","click",()=>systemProxyAction("on"));
  on("#systemProxyOff","click",()=>systemProxyAction("off"));
  on("#systemProxyRecover","click",()=>systemProxyAction("recover"));
  on("#systemProxyForce","click",()=>systemProxyAction("recover",true));
  $$("[data-core-action]").forEach(button=>button.addEventListener("click",()=>coreOnlyAction(button.dataset.coreAction,button)));
  on("#checkConnectivityButton","click",checkConnectivity);
  on("#proxySetupButton","click",showProxyGuide);
  on("#closeProxyGuide","click",()=>{
    $("#proxyGuide").hidden = true;
    $("#proxySetupButton").setAttribute("aria-expanded","false");
    $("#proxySetupButton").focus();
  });
  on("#quickTest","click",async()=>{switchView("nodes",{load:false});await testNodes();});
  on("#copyProxyButton","click",()=>{
    const port = state.overview?.core?.mixedPort;
    if (!port) { toast("代理地址尚未确认，请刷新状态。","error"); return; }
    copyText(`http://127.0.0.1:${port}`,"代理地址已复制");
  });
  on("#nodeSearch","input",()=>{state.nodePage=1;renderNodes();});
  ["#nodeFilter","#nodeProvider","#nodeSort"].forEach(selector=>on(selector,"change",()=>{state.nodePage=1;renderNodes();}));
  on("#nodePrevious","click",()=>{state.nodePage--;renderNodes();$("#nodeList").scrollIntoView({block:"start"});});
  on("#nodeNext","click",()=>{state.nodePage++;renderNodes();$("#nodeList").scrollIntoView({block:"start"});});
  on("#testNodesButton","click",()=>testNodes());
  on("#closeSubscriptionResult","click",()=>$("#subscriptionResult").hidden=true);
  on("#addSubscriptionButton","click",openSubscriptionModal);
  $$("[data-close-modal]").forEach(item=>item.addEventListener("click",closeSubscriptionModal));
  on("#useSubscriptionGroup","click",useSubscriptionGroup);
  on("#createSubscriptionGroup","click",createSubscriptionGroup);
  on("#subscriptionGroupSelect","change",updateSubscriptionGroupControls);
  on("#toggleSubscriptionGroup","click",event=>changeSubscriptionGroup("toggle",event.currentTarget));
  on("#saveSubscriptionGroupName","click",event=>changeSubscriptionGroup("rename",event.currentTarget));
  on("#deleteSubscriptionGroup","click",event=>changeSubscriptionGroup("delete",event.currentTarget));
  on("#updateSubscriptionGroup","click",event=>updateSubscriptionBatch($("#subscriptionGroupSelect").value,event.currentTarget));
  on("#updateAllSubscriptions","click",event=>updateSubscriptionBatch("",event.currentTarget));
  on("#testSubscriptionGroup","click",event=>testSubscriptionBatch($("#subscriptionGroupSelect").value,event.currentTarget));
  on("#testAllSubscriptions","click",event=>testSubscriptionBatch("",event.currentTarget));
  on("#installLatestCore","click",event=>installCore(event.currentTarget));
  on("#addRouteRule","click",addRouteRule);
  on("#createRouteProfile","click",createRouteProfile);
  on("#createRouteGroup","click",createRouteGroup);
  on("#restoreRouteDefaults","click",()=>{if(confirm("只补齐缺失的内置方案和规则组，不覆盖自定义规则。继续吗？"))routeMutation("/api/v1/routing/restore","POST",{},"缺失预设已恢复");});
  on("#subAuth","change",updateSubscriptionAuthFields);
  on("#subDecrypt","change",updateSubscriptionAuthFields);
  on("#runDoctorButton","click",runDoctor);
  on("#refreshLogsButton","click",loadLogs);
  on("#logSearch","input",renderLogs);
  on("#autoLogs","change",()=>{if($("#autoLogs").checked)loadLogs();});
  on("#copyLogsButton","click",()=>copyText($("#logOutput").textContent,"显示的日志已复制"));
  on("#downloadLogsButton","click",downloadLogs);
  on("#themeButton","click",()=>{
    const next = document.documentElement.dataset.theme==="light" ? "dark" : "light";
    applyTheme(next);
  });
  on("#loginForm","submit",handleLoginSubmit);
  on("#subscriptionForm","submit",handleSubscriptionSubmit);
  on("#settingsForm","submit",handleSettingsSubmit);
  on("#settingsForm","input",()=>{state.settingsDirty=true;});
  on("#settingsForm","change",()=>{state.settingsDirty=true;});
  on("#webSecurityForm","submit",handleWebSecuritySubmit);
  window.addEventListener("hashchange",()=>switchView(location.hash.slice(1)));
}

async function bootstrap() {
  clearInterval(state.overviewTimer);
  clearInterval(state.logsTimer);
  await refreshOverview(true);
  switchView(location.hash.slice(1) || "overview");
  // 页面不可见或凭据失效时停止轮询，避免重复请求和登录框抢焦点。
  state.overviewTimer = setInterval(() => {
    if (state.authenticated && document.visibilityState === "visible") refreshOverview(true);
  }, 5000);
  state.logsTimer = setInterval(() => {
    if (state.authenticated && document.visibilityState === "visible" && state.activeView === "logs" && $("#autoLogs").checked) loadLogs();
  }, 1000);
}

document.documentElement.dataset.theme=resolvedTheme(renamedPreference(localStorage,"kivo_theme","proxypilot_theme"));
initializeAppearance();
bindEvents();
api("/api/v1/session/verify",{method:"POST"}).then(()=>{hideLogin();bootstrap()}).catch(()=>showLogin("请输入 Web API 密钥。"));
