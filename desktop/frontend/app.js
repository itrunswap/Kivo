import {
  modes,
  coreStates,
  connectionView,
  fresh,
  groupNodes,
  nodeLatency,
  selectedNode,
  updateSummary,
} from "./model.mjs";

// 页面只通过受限原生桥调用后台；没有跨域网络请求，也不持有 Web Token。
const $ = (id) => document.getElementById(id);
const state = {
  tab: "home",
  info: null,
  online: false,
  busy: false,
  refreshing: false,
  overview: null,
  nodes: [],
  nodesFresh: false,
  subs: [],
  groups: [],
  routing: null,
  settings: null,
  versions: [],
  security: null,
  openGroups: new Set(),
  loadedGroups: false,
};
let toastTimer, lastFocus;
const native = () => window.go?.main?.Desktop;

function element(tag, className, text) {
  const result = document.createElement(tag);
  if (className) result.className = className;
  if (text != null) result.textContent = text;
  return result;
}
function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  svg.classList.add("icon");
  use.setAttribute("href", `#i-${name}`);
  svg.append(use);
  svg.setAttribute("aria-hidden", "true");
  return svg;
}
function button(label, action, className = "button small") {
  const result = element("button", className, label);
  result.type = "button";
  result.addEventListener("click", action);
  return result;
}
function message(text, error = false) {
  clearTimeout(toastTimer);
  $("toast").textContent = text;
  $("toast").classList.toggle("error", error);
  $("toast").hidden = false;
  toastTimer = setTimeout(
    () => ($("toast").hidden = true),
    error ? 11000 : 5000,
  );
}
async function reply(path, method = "GET", body) {
  if (!native()) throw new Error("原生连接未就绪，请在 Kivo 桌面窗口中运行");
  return native().Request(
    method,
    path,
    body == null ? "" : JSON.stringify(body),
  );
}
async function api(path, method = "GET", body) {
  const result = await reply(path, method, body);
  if (result.error || result.status >= 400) {
    const err = new Error(result.error || `操作失败（${result.status}）`);
    err.data = result.data;
    throw err;
  }
  return result.data;
}
function startJob(label) {
  state.busy = true;
  $("job").hidden = false;
  $("jobLabel").textContent = label;
  $("jobDetail").textContent = "";
  $("jobPercent").textContent = "";
  $("jobProgress").removeAttribute("value");
  document
    .querySelectorAll("[data-mutation]")
    .forEach((item) => (item.disabled = true));
  $("connect").disabled = true;
}
function endJob() {
  state.busy = false;
  $("job").hidden = true;
  document
    .querySelectorAll("[data-mutation]")
    .forEach((item) => (item.disabled = false));
  renderOverview();
  renderNodes(true); // 忙碌状态解除后仍保留离线、缓存和禁用订阅的节点限制。
}
// 所有变更串行执行；后台同样有事务锁，避免多窗口改写内核或订阅。
async function perform(
  label,
  operation,
  { refresh = true, success = "操作完成" } = {},
) {
  if (state.busy) {
    message("已有操作正在执行，请等待完成");
    return false;
  }
  startJob(label);
  try {
    await operation();
    if (refresh) await refreshAll();
    if (success) message(success);
    return true;
  } catch (error) {
    if (refresh) await refreshAll().catch(() => {});
    message(error.message, true);
    return false;
  } finally {
    endJob();
  }
}

function dialog(title) {
  lastFocus = document.activeElement;
  $("dialogTitle").textContent = title;
  $("dialogBody").replaceChildren();
  if (!$("dialog").open) $("dialog").showModal();
  return $("dialogBody");
}
function closeDialog() {
  $("dialog").close();
  $("dialogBody").replaceChildren();
  lastFocus?.focus();
}
function confirm(title, text, action, label = "确认", danger = false) {
  const body = dialog(title);
  body.append(element("p", "dialog-copy", text));
  const actions = element("div", "dialog-actions");
  actions.append(
    button("取消", closeDialog, "button"),
    button(
      label,
      async () => {
        closeDialog();
        await action();
      },
      `button ${danger ? "danger" : "primary"}`,
    ),
  );
  body.append(actions);
}
function field(form, spec) {
  const label = element(
    "label",
    spec.type === "checkbox" ? "checkbox-field" : "field",
  );
  let input;
  if (spec.options) {
    input = element("select");
    for (const option of spec.options) {
      const [value, text] = Array.isArray(option) ? option : [option, option];
      const item = element("option", "", text);
      item.value = value;
      input.append(item);
    }
  } else input = element(spec.type === "textarea" ? "textarea" : "input");
  input.name = spec.name;
  if (input.tagName === "INPUT") input.type = spec.type || "text";
  if (spec.type === "checkbox") input.checked = !!spec.value;
  else input.value = spec.value ?? "";
  if (spec.placeholder) input.placeholder = spec.placeholder;
  if (spec.required) input.required = true;
  if (spec.type === "password") input.autocomplete = "new-password";
  if (spec.type === "number") {
    input.min = spec.min ?? 1;
    input.max = spec.max ?? 65535;
  }
  if (spec.type === "checkbox")
    label.append(input, document.createTextNode(spec.label));
  else label.append(element("span", "", spec.label), input);
  if (spec.hint) label.append(element("small", "field-hint", spec.hint));
  form.append(label);
  return input;
}
function formDialog(
  title,
  specs,
  submit,
  { copy = "", submitLabel = "保存" } = {},
) {
  const body = dialog(title),
    form = element("form");
  if (copy) form.append(element("p", "dialog-copy", copy));
  specs.forEach((spec) => field(form, spec));
  const err = element("p", "dialog-error");
  err.setAttribute("role", "alert");
  form.append(err);
  const actions = element("div", "dialog-actions"),
    save = button(submitLabel, () => {}, "button primary");
  save.type = "submit";
  actions.append(button("取消", closeDialog, "button"), save);
  form.append(actions);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (state.busy) {
      err.textContent = "已有操作正在执行";
      return;
    }
    const values = {};
    specs.forEach((spec) => {
      const control = form.elements.namedItem(spec.name);
      values[spec.name] =
        spec.type === "checkbox"
          ? control.checked
          : spec.type === "number"
            ? Number(control.value)
            : control.value.trim();
    });
    save.disabled = true;
    err.textContent = "";
    try {
      const result = await submit(values);
      if (!result?.keepOpen) closeDialog();
    } catch (error) {
      err.textContent = error.message;
    } finally {
      save.disabled = false;
    }
  });
  body.append(form);
  setTimeout(() => form.querySelector("input,select,textarea")?.focus(), 0);
  return form;
}
async function mutate(label, path, method, body) {
  if (state.busy) throw new Error("已有操作正在执行");
  startJob(label);
  try {
    const result = await api(path, method, body);
    await refreshAll();
    message("已保存");
    return result;
  } finally {
    endJob();
  }
}

function renderOverview() {
  const view = connectionView(state.overview, state.online);
  $("connectionTitle").textContent = view.title;
  $("connectionDetail").textContent = view.detail;
  $("connectionDot").className = `status-dot ${view.tone}`;
  $("connect").setAttribute("aria-checked", String(view.on));
  $("connect").setAttribute(
    "aria-label",
    state.overview?.systemProxy?.state === "this_app"
      ? "断开系统代理"
      : "连接系统代理",
  );
  $("connect").disabled =
    state.busy ||
    !state.online ||
    state.overview?.systemProxy?.supported === false;
  $("checkLabel").textContent = view.check;
  $("checkLabel").classList.toggle("good-text", view.tone === "good");
  const core = state.overview?.core;
  $("routeLabel").textContent = modes[core?.mode] || "规则模式";
  $("coreLabel").textContent = state.online
    ? `Mihomo ${coreStates[core?.state] || "状态未知"}${core?.version ? " · " + core.version : ""}`
    : "Mihomo · 状态未知";
  $("tunLabel").textContent =
    `TUN ${state.overview?.tunEnabled ? "开启" : "关闭"}`;
  $("portLabel").textContent = core?.mixedPort
    ? `HTTP / SOCKS5 · 127.0.0.1:${core.mixedPort} · ${state.online && state.overview.proxyPortListening ? "正在监听" : "未确认监听"}`
    : "HTTP / SOCKS5 · —";
  const hint = state.overview?.systemProxy?.recoveryPending
    ? "存在未恢复系统代理备份，请到数据页查看状态，再在设置页恢复。"
    : core?.state === "not_installed"
      ? "首次使用：安装内核 → 添加并更新订阅 → 选择节点 → 打开连接。"
      : core?.state === "running" && !view.on
        ? state.overview?.tunEnabled
          ? "内核运行且已配置 TUN；仅凭配置不能确认虚拟网卡接管，请查看日志和连通测试。"
          : "内核已经运行，但系统代理未接入 Kivo。打开连接开关完成接入。"
        : "";
  $("connectionHint").hidden = !hint;
  $("connectionHint").textContent = hint;
  const report = state.overview?.connectivity;
  $("lastChecked").textContent = report
    ? `最近检测 ${new Date(report.checkedAt).toLocaleTimeString("zh-CN", { hour12: false })} · ${fresh(report) ? "结果有效 2 分钟" : "结果已过期"}`
    : "连接状态不代表联网成功，请运行连通测试";
}
function renderNodes(force = false) {
  const viewKey = JSON.stringify([
    state.nodes,
    state.nodesFresh,
    state.subs,
    $("nodeSearch").value,
    state.online,
    state.busy,
    state.overview?.core?.state,
    state.overview?.core?.currentNode,
  ]);
  if (!force && state.nodeViewKey === viewKey) return; // 相同轮询结果不重建 DOM，不丢失键盘焦点或折叠状态。
  state.nodeViewKey = viewKey;
  const groups = groupNodes(state.nodes, state.subs, $("nodeSearch").value),
    container = $("nodeGroups");
  container.replaceChildren();
  const running =
    state.online &&
    state.nodesFresh &&
    state.overview?.core?.state === "running";
  if (!state.loadedGroups && groups.length) {
    if (groups[0]) state.openGroups.add(groups[0].name);
    state.loadedGroups = true;
  }
  if (!groups.length) {
    container.append(
      element(
        "p",
        "empty-note",
        $("nodeSearch").value
          ? "没有匹配的节点"
          : "尚无订阅。点击右上角 + 添加订阅。",
      ),
    );
    return;
  }
  for (const group of groups) {
    const details = element("details", "node-group"),
      summary = element("summary");
    details.open = state.openGroups.has(group.name) || !!$("nodeSearch").value;
    summary.append(
      icon("chevron"),
      element("strong", "", group.name),
      element(
        "span",
        "count",
        `${group.nodes.length} 个节点${group.enabled ? "" : " · 已禁用"}`,
      ),
    );
    details.append(summary);
    details.addEventListener("toggle", () => {
      if (details.open) state.openGroups.add(group.name);
      else state.openGroups.delete(group.name);
    });
    if (!group.nodes.length)
      details.append(
        element(
          "p",
          "empty-note",
          group.enabled
            ? "尚无节点。请更新此订阅，查看结果中的解析数量。"
            : "此订阅已禁用",
        ),
      );
    for (const node of group.nodes) {
      const row = element(
        "div",
        `node-row${selectedNode(state.overview, node) ? " selected" : ""}`,
      );
      const choose = button(
        "",
        () =>
          perform(
            "正在切换节点…",
            () => api("/api/v1/nodes/select", "POST", { name: node.name }),
            { success: "节点已切换，建议重新检测联网" },
          ),
        "node-select",
      );
      choose.disabled = !running || node.cached || !group.enabled || state.busy;
      choose.dataset.mutation = "true";
      choose.setAttribute("aria-label", `选择 ${node.name}`);
      choose.setAttribute(
        "aria-pressed",
        String(selectedNode(state.overview, node)),
      );
      const copy = element("span", "node-copy");
      copy.append(
        element("span", "node-name", node.name),
        element(
          "span",
          "node-type",
          `${node.type || "Proxy"}${node.cached ? " · 离线快照" : ""}`,
        ),
      );
      choose.append(element("span", "radio"), copy);
      const delay = nodeLatency(node, running);
      row.append(choose, element("span", `delay ${delay.tone}`, delay.text));
      const info = button("", () => nodeDetail(node), "icon-button");
      info.append(icon("info"));
      info.setAttribute("aria-label", `${node.name} 的详细信息`);
      row.append(info);
      details.append(row);
    }
    container.append(details);
  }
  $("nodesNotice").textContent = !state.online
    ? "后台已断开，节点列表是上次读取结果，不能切换。"
    : !state.nodesFresh
      ? "节点列表未能刷新，旧节点仅供查看，请重试。"
      : !running
        ? "内核已停止。缓存节点仅供查看，启动内核后才能切换和测速。"
        : `${state.nodes.length} 个节点 · ${state.subs.filter((sub) => sub.enabled).length} 个已启用订阅`;
}
function nodeDetail(node) {
  const body = dialog("节点信息");
  body.append(
    detailsList([
      ["名称", node.name],
      ["协议", node.type],
      ["订阅", node.providerName || "未分组"],
      ["UDP", node.udp ? "支持" : "未声明支持"],
      [
        "延迟",
        nodeLatency(
          node,
          state.online &&
            state.nodesFresh &&
            state.overview?.core?.state === "running",
        ).text,
      ],
      [
        "数据来源",
        node.cached || !state.nodesFresh ? "离线缓存（待刷新）" : "内核运行时",
      ],
    ]),
  );
  body.append(
    element(
      "p",
      "footnote",
      "节点测速只验证测试目标响应，不能代替系统代理入口的连通检测。",
    ),
  );
}
function detailsList(items) {
  const list = element("dl", "detail-list");
  for (const [key, value] of items) {
    const pair = element("div", "detail-pair");
    pair.append(element("dt", "", key), element("dd", "", value ?? "—"));
    list.append(pair);
  }
  return list;
}
function record(title, subtitle, badge = "", active = false) {
  const card = element("div", "record"),
    top = element("div", "record-top");
  top.append(element("strong", "", title));
  if (badge)
    top.append(element("span", `badge${active ? " active" : ""}`, badge));
  card.append(top);
  if (subtitle) card.append(element("p", "", subtitle));
  return card;
}
function actionRow(card, items) {
  const actions = element("div", "actions");
  for (const [label, action, danger] of items) {
    const b = button(label, action, `button small${danger ? " danger" : ""}`);
    b.dataset.mutation = "true";
    actions.append(b);
  }
  card.append(actions);
}
function renderConfig() {
  $("subscriptions").replaceChildren();
  if (!state.subs.length)
    $("subscriptions").append(
      element(
        "p",
        "empty-note",
        "添加一个订阅地址，支持普通订阅及 AES / age 加密。",
      ),
    );
  for (const sub of state.subs) {
    const card = record(
      sub.name,
      `${sub.group || "默认分组"} · ${sub.authType || "none"} · 默认${sub.updateVia === "proxy" ? "代理" : "直连"}更新`,
      sub.enabled ? "已启用" : "已禁用",
      sub.enabled,
    );
    actionRow(card, [
      ["更新", () => updateDialog(sub.name)],
      ["检查", () => updateDialog(sub.name, "", "test")],
      ["编辑", () => editSubscription(sub)],
      [
        sub.enabled ? "禁用" : "启用",
        () =>
          perform("更新订阅状态…", () =>
            api("/api/v1/subscriptions", "PATCH", {
              reference: sub.name,
              enabled: !sub.enabled,
            }),
          ),
      ],
      [
        "删除",
        () =>
          confirm(
            "删除订阅",
            `删除「${sub.name}」及其配置？不会删除订阅平台的账号。`,
            () =>
              perform("删除订阅…", () =>
                api(
                  `/api/v1/subscriptions?name=${encodeURIComponent(sub.name)}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        true,
      ],
    ]);
    $("subscriptions").append(card);
  }
  $("subscriptionGroups").replaceChildren();
  for (const group of state.groups) {
    const card = record(
      group.name,
      `${state.subs.filter((sub) => sub.group === group.name).length} 个订阅`,
      group.enabled ? "已启用" : "已禁用",
      group.enabled,
    );
    actionRow(card, [
      ["更新组", () => updateDialog("", group.name)],
      [
        "仅使用此组",
        () =>
          perform("切换订阅分组…", () =>
            api("/api/v1/subscription-groups/action", "POST", {
              name: group.name,
              action: "use",
            }),
          ),
      ],
      [
        group.enabled ? "禁用" : "启用",
        () =>
          perform("修改分组…", () =>
            api("/api/v1/subscription-groups/action", "POST", {
              name: group.name,
              action: group.enabled ? "disable" : "enable",
            }),
          ),
      ],
      [
        "删除",
        () =>
          confirm(
            "删除分组",
            `删除「${group.name}」？存在订阅时后台会拒绝删除。`,
            () =>
              perform("删除分组…", () =>
                api(
                  `/api/v1/subscription-groups?name=${encodeURIComponent(group.name)}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        true,
      ],
    ]);
    $("subscriptionGroups").append(card);
  }
  $("profiles").replaceChildren();
  for (const profile of state.routing?.profiles || []) {
    const active = state.routing.activeProfile === profile.name;
    const card = record(
      profile.name,
      `未匹配：${{ proxy: "代理", direct: "直连", reject: "拒绝" }[profile.defaultAction] || profile.defaultAction} · ${profile.groups?.length || 0} 个规则组`,
      active ? "使用中" : "",
      active,
    );
    actionRow(card, [
      [
        "使用",
        () =>
          perform("切换路由配置…", () =>
            api("/api/v1/routing/profiles/use", "POST", { name: profile.name }),
          ),
      ],
      ["规则组", () => profileGroups(profile)],
      [
        "删除",
        () =>
          confirm(
            "删除路由配置",
            `删除「${profile.name}」？活动配置与内置配置受到后台保护。`,
            () =>
              perform("删除路由…", () =>
                api(
                  `/api/v1/routing/profiles?name=${encodeURIComponent(profile.name)}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        true,
      ],
    ]);
    $("profiles").append(card);
  }
  $("ruleGroups").replaceChildren();
  for (const group of state.routing?.ruleGroups || []) {
    const card = record(group.name, `${group.rules?.length || 0} 条规则`);
    actionRow(card, [
      ["查看 / 编辑", () => ruleEditor(group)],
      ["添加规则", () => addRule(group.name)],
      [
        "删除",
        () =>
          confirm(
            "删除规则组",
            `删除「${group.name}」？正在被配置引用的规则组不能直接删除。`,
            () =>
              perform("删除规则组…", () =>
                api(
                  `/api/v1/routing/groups?name=${encodeURIComponent(group.name)}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        true,
      ],
    ]);
    $("ruleGroups").append(card);
  }
}
function renderData() {
  const o = state.overview,
    c = o?.core,
    p = o?.systemProxy;
  const pairs = [
    ["后台", state.online ? "已连接" : "已断开（数据可能过期）"],
    ["内核", coreStates[c?.state] || "未知"],
    ["版本", c?.version || "未安装"],
    ["进程 PID", c?.pid || "—"],
    ["代理入口", c?.mixedPort ? `127.0.0.1:${c.mixedPort}` : "—"],
    [
      "监听状态",
      state.online && o?.proxyPortListening ? "正在监听" : "未确认监听",
    ],
    [
      "系统代理",
      {
        this_app: "接入 Kivo",
        off: "未启用",
        other: "其他程序的代理",
        automatic: "自动代理（PAC）",
        unknown: "无法确认",
      }[p?.state] || "未知",
    ],
    ["当前选择", c?.currentNode || "DIRECT"],
    ["实际展开节点", c?.effectiveNode || "—"],
    ["路由模式", modes[c?.mode] || "—"],
    ["TUN", o?.tunEnabled ? "开启（不等同联网成功）" : "关闭"],
    ["订阅", `${o?.enabledCount || 0} / ${o?.subscriptionCount || 0} 已启用`],
  ];
  $("runtimeDetails").replaceChildren(...detailsList(pairs).children);
  $("connectivityResults").replaceChildren();
  const report = o?.connectivity;
  if (!report) {
    $("connectivityResults").append(
      element(
        "p",
        "empty-note",
        "尚未检测。检测会分别验证直连、代理入口与固定节点出口。",
      ),
    );
    return;
  }
  if (!fresh(report) || !state.online)
    $("connectivityResults").append(
      element(
        "p",
        "warning-box",
        report.staleReason ||
          "检测记录已过期或后台断开，不能据此判断当前可上网。",
      ),
    );
  for (const route of report.routes || []) {
    const card = record(
      route.label,
      route.message,
      { ok: "通过", partial: "部分通过", failed: "失败", skipped: "未执行" }[
        route.state
      ] || "未知",
      fresh(report) && state.online && route.state === "ok",
    );
    for (const probe of route.probes || [])
      card.append(
        element(
          "p",
          probe.state === "failed" ? "error-text" : "",
          `${probe.target} · ${probe.state === "ok" ? "通过" : probe.state === "skipped" ? "跳过" : "失败"} · ${probe.durationMs} ms · ${probe.message}`,
        ),
      );
    $("connectivityResults").append(card);
  }
}
function renderSettings() {
  if (state.settings) {
    const form = $("settingsForm");
    for (const name of [
      "mixedPort",
      "allowLAN",
      "tunEnabled",
      "downloadProxy",
      "downloadRetry",
    ]) {
      const control = form.elements.namedItem(name);
      if (control.type === "checkbox") control.checked = !!state.settings[name];
      else control.value = state.settings[name] ?? "";
    }
  }
  $("installedCore").textContent =
    `${state.overview?.core?.version || "尚未安装"} · ${coreStates[state.overview?.core?.state] || "状态未知"}`;
  $("webSecurityLabel").textContent = state.security?.authEnabled
    ? "已开启 Token"
    : "无需 Token";
  $("coreVersions").replaceChildren();
  for (const item of state.versions) {
    const row = record(
      item.version,
      "",
      item.active ? "当前版本" : "",
      item.active,
    );
    actionRow(row, [
      [
        "切换",
        () =>
          perform("切换内核…", () =>
            api("/api/v1/core/use", "POST", { reference: item.version }),
          ),
      ],
      [
        "删除",
        () =>
          confirm(
            "删除内核版本",
            `删除 ${item.version} 的本地文件？活动内核不能被删除。`,
            () =>
              perform("删除内核…", () =>
                api(
                  `/api/v1/core/installations?reference=${encodeURIComponent(item.version)}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        true,
      ],
    ]);
    $("coreVersions").append(row);
  }
  if (state.overview?.systemProxy?.recoveryPending && !$("recoverProxy")) {
    const b = button(
      "恢复系统代理备份",
      () =>
        confirm(
          "恢复系统代理",
          "只恢复 Kivo 之前接管的设置，保留其他程序的后续修改。",
          () =>
            perform("恢复系统代理…", () =>
              api("/api/v1/system-proxy/recover", "POST", {}),
            ),
        ),
      "button danger",
    );
    b.id = "recoverProxy";
    $("coreVersions").append(b);
  }
  $("appInfo").textContent = state.info
    ? `Kivo ${state.info.version} · ${state.info.platform} / ${state.info.architecture}`
    : "Kivo Desktop";
  $("dataDirectory").textContent = state.info
    ? `数据目录：${state.info.dataDirectory}`
    : "";
}

async function refreshOverview() {
  state.overview = await api("/api/v1/overview");
  state.online = true;
  $("connectionAlert").hidden = true;
  renderOverview();
  renderData();
}
async function refreshAll() {
  try {
    await refreshOverview();
  } catch (error) {
    state.online = false;
    $("connectionAlert").hidden = false;
    $("backendMessage").textContent = error.message;
    renderOverview();
    renderNodes();
    throw error;
  }
  const requests = [
    ["nodes", "/nodes"],
    ["subs", "/subscriptions"],
    ["groups", "/subscription-groups"],
    ["routing", "/routing"],
    ["settings", "/settings"],
    ["versions", "/core/installations"],
    ["security", "/web/security"],
  ];
  state.nodesFresh = false;
  const results = await Promise.allSettled(
    requests.map(async ([key, path]) => {
      state[key] =
        (await api("/api/v1" + path)) ??
        (["nodes", "subs", "groups", "versions"].includes(key) ? [] : null);
      if (key === "nodes") state.nodesFresh = true;
    }),
  );
  const failed = results.find((item) => item.status === "rejected");
  renderNodes();
  renderConfig();
  renderSettings();
  if (failed) message(`部分信息未加载：${failed.reason.message}`, true);
}
function setTab(tab, focus = false) {
  state.tab = tab;
  for (const b of document.querySelectorAll("[data-tab]")) {
    const active = b.dataset.tab === tab;
    b.classList.toggle("active", active);
    b.setAttribute("aria-selected", String(active));
    b.tabIndex = active ? 0 : -1;
    $("panel-" + b.dataset.tab).hidden = !active;
    if (active && focus) b.focus();
  }
  $("content").scrollTop = 0;
  if (
    (tab === "settings" || tab === "config") &&
    state.online &&
    !state.busy &&
    !state.refreshing
  ) {
    state.refreshing = true;
    refreshAll()
      .catch((error) => message(error.message, true))
      .finally(() => {
        state.refreshing = false;
      });
  }
}

async function connect() {
  if (!state.online || state.busy) return;
  if (state.overview?.systemProxy?.state === "this_app")
    return perform(
      "正在断开并恢复系统代理…",
      async () => {
        const result = await api("/api/v1/connection/disconnect", "POST", {});
        if (result.warning) throw new Error(result.message);
      },
      { success: "已断开，已处理系统代理恢复" },
    );
  if (state.overview?.core?.state === "not_installed") {
    setTab("settings");
    message("请先安装 Mihomo 内核，再添加并更新订阅");
    return;
  }
  const p = state.overview.systemProxy;
  const run = (options) =>
    perform(
      "正在启动内核、验证入口并接入系统代理…",
      async () => {
        const result = await api("/api/v1/connection/connect", "POST", options);
        if (result.warning) throw new Error(result.message);
      },
      { success: "系统代理已接入，建议运行连通测试" },
    );
  if (p.state === "other" || p.state === "automatic")
    return confirm(
      "替换当前系统代理？",
      "检测到其他代理或 PAC。确认后备份当前设置，交给 Kivo；断开时会尝试恢复备份。",
      () => run({ replace: true }),
      "备份并接入",
    );
  if (p.recoveryPending)
    return confirm(
      "先处理代理备份",
      "存在未恢复的设置。请到设置页恢复，避免覆盖其他程序的修改。",
      () => setTab("settings"),
      "打开设置",
    );
  return run({});
}
function routeDialog() {
  formDialog(
    "全局路由",
    [
      {
        name: "mode",
        label: "代理模式",
        options: Object.entries(modes),
        value: state.settings?.mode || "rule",
      },
    ],
    (values) =>
      mutate("切换代理模式…", "/api/v1/settings", "PATCH", {
        ...state.settings,
        mode: values.mode,
      }),
    {
      copy: "规则模式按配置分流；全局模式将可代理流量交给当前节点；直连模式不经过代理节点。",
    },
  );
}
function updateDialog(reference = "all", group = "", kind = "update") {
  const title = kind === "test" ? "检查订阅节点" : "更新订阅";
  formDialog(
    title,
    [
      {
        name: "via",
        label: "下载路径",
        options: [
          ["", "使用各订阅默认路径"],
          ["direct", "直连（不使用 HTTP 系统代理）"],
          ["proxy", "通过当前 PROXY 节点"],
        ],
        value: "direct",
      },
    ],
    async (values) => {
      if (state.busy) throw new Error("已有操作正在执行");
      startJob(`${title}…`);
      try {
        const result = await reply(`/api/v1/subscriptions/${kind}`, "POST", {
          reference: reference || "",
          group,
          via: values.via,
        });
        await refreshAll();
        closeDialog();
        const body = dialog(title + "结果");
        const summary = updateSummary(result.data);
        body.append(element("p", "dialog-copy", summary));
        if (result.data?.temporarilyStartedCore)
          body.append(
            element(
              "p",
              "footnote",
              "内核原本未运行，已临时启动并恢复停止；可在设置页启动内核后选择节点。",
            ),
          );
        if (result.error)
          body.append(element("p", "dialog-error", result.error));
        // 结果弹窗替换了输入弹窗，外层不能再次关闭它。
        return { keepOpen: true };
      } finally {
        endJob();
      }
    },
    {
      copy: `${group ? "分组「" + group + "」" : reference && reference !== "all" ? "订阅「" + reference + "」" : "全部启用订阅"}。直连只绕过 HTTP 代理；系统 VPN / TUN / 网关仍可能影响网络路由。`,
      submitLabel: kind === "test" ? "开始检查" : "开始更新",
    },
  );
}
const authOptions = [
  ["none", "无需认证"],
  ["aes", "AES 解密密码"],
  ["age", "age 解密密码"],
  ["basic", "Basic 用户名 / 密码"],
  ["bearer", "Bearer Token"],
  ["token", "Token 参数"],
];
function subscriptionSpecs(sub) {
  return [
    { name: "name", label: "订阅名称", value: sub?.name || "", required: true },
    {
      name: "url",
      label: sub ? "替换订阅地址（留空保留原值）" : "订阅地址",
      type: "url",
      required: !sub,
      placeholder: "https://…",
      hint: sub
        ? "原地址已脱敏，不会把脱敏值重新保存为真实地址。"
        : "仅接受 HTTP / HTTPS 地址",
    },
    {
      name: "group",
      label: "分组",
      options: state.groups.length
        ? state.groups.map((g) => g.name)
        : ["default"],
      value: sub?.group || "default",
    },
    {
      name: "authType",
      label: "认证 / 解密方式",
      options: authOptions,
      value: sub?.authType || "none",
    },
    {
      name: "username",
      label: "Basic 用户名",
      placeholder: sub ? "留空保留原用户名" : "仅 Basic 认证需要",
    },
    {
      name: "secret",
      label: "密码 / Token",
      type: "password",
      placeholder: sub ? "留空保留原凭据" : "AES / age 填解密密码",
    },
    {
      name: "updateVia",
      label: "默认更新路径",
      options: [
        ["direct", "直连"],
        ["proxy", "通过当前节点"],
      ],
      value: sub?.updateVia || "direct",
    },
  ];
}
function addSubscription() {
  formDialog(
    "添加订阅",
    subscriptionSpecs(),
    (v) =>
      mutate("添加订阅…", "/api/v1/subscriptions", "POST", {
        ...v,
        updateInterval: 3600,
        healthInterval: 300,
        healthCheckURL: "https://www.gstatic.com/generate_204",
      }),
    {
      copy: "订阅与 CLI / Web 共用。添加后需更新订阅，成功解析后才能看到节点。",
      submitLabel: "添加订阅",
    },
  );
}
function editSubscription(sub) {
  formDialog("编辑订阅", subscriptionSpecs(sub), (v) => {
    const body = {
      reference: sub.name,
      name: v.name,
      group: v.group,
      updateVia: v.updateVia,
    };
    if (v.url) body.url = v.url;
    if (v.authType !== sub.authType || v.secret || v.username) {
      body.authType = v.authType;
      if (v.secret) body.secret = v.secret;
      if (v.username) body.username = v.username;
    }
    return mutate("修改订阅…", "/api/v1/subscriptions", "PATCH", body);
  });
}
function profileGroups(profile) {
  const groups = state.routing?.ruleGroups || [];
  if (!groups.length) {
    message("请先创建规则组");
    return;
  }
  formDialog(
    "关联规则组",
    [
      { name: "group", label: "规则组", options: groups.map((g) => g.name) },
      {
        name: "attached",
        label: "关联到此配置（取消为解除关联）",
        type: "checkbox",
        value: true,
      },
    ],
    (v) =>
      mutate("更新路由关联…", "/api/v1/routing/profiles", "PATCH", {
        profile: profile.name,
        ...v,
      }),
    {
      copy: `配置「${profile.name}」当前关联：${profile.groups?.join("、") || "无"}`,
    },
  );
}
function addRule(group) {
  formDialog(
    "添加路由规则",
    [
      {
        name: "type",
        label: "匹配方式",
        options: [
          ["domain", "精确域名"],
          ["domain-suffix", "域名后缀"],
          ["domain-keyword", "域名关键词"],
          ["ip-cidr", "IPv4 CIDR"],
          ["ip-cidr6", "IPv6 CIDR"],
          ["geoip", "GeoIP"],
          ["process-name", "进程名称"],
        ],
      },
      {
        name: "value",
        label: "匹配值",
        required: true,
        placeholder: "example.com",
      },
      {
        name: "action",
        label: "匹配后动作",
        options: [
          ["proxy", "走代理"],
          ["direct", "不走代理"],
          ["reject", "拒绝连接"],
        ],
      },
    ],
    (v) =>
      mutate("添加规则…", "/api/v1/routing/rules", "POST", { group, rule: v }),
    {
      copy: `规则组「${group}」。规则按顺序匹配；只有关联到活动路由配置后才参与规则分流。`,
    },
  );
}
function ruleEditor(group) {
  const body = dialog(group.name + " · 规则"),
    rules = group.rules || [];
  body.append(
    element("p", "footnote", "按顺序匹配。规则组需关联到活动配置才能生效。"),
  );
  if (!rules.length) body.append(element("p", "empty-note", "尚无规则"));
  rules.forEach((rule, index) => {
    const row = element("div", "rule-line");
    row.append(
      element(
        "span",
        "",
        `${index + 1}. ${rule.type} · ${rule.value} → ${rule.action}`,
      ),
      button(
        "删除",
        () =>
          confirm(
            "删除规则",
            `删除第 ${index + 1} 条规则？`,
            () =>
              perform("删除规则…", () =>
                api(
                  `/api/v1/routing/rules?group=${encodeURIComponent(group.name)}&index=${index + 1}`,
                  "DELETE",
                ),
              ),
            "删除",
            true,
          ),
        "text-button",
      ),
    );
    body.append(row);
  });
  body.append(button("添加规则", () => addRule(group.name), "button"));
}
async function checkConnectivity() {
  await perform(
    "正在验证直连、代理入口和固定节点出口…",
    async () => {
      const report = await api("/api/v1/connectivity/check", "POST", {});
      if (state.overview) state.overview.connectivity = report;
    },
    { success: "联网检测已完成，请查看数据页的各路径结果" },
  );
  setTab("data");
}
function installCore() {
  formDialog(
    "安装 / 更新 Mihomo",
    [{ name: "version", label: "版本号", placeholder: "留空安装最新稳定版" }],
    async (v) => {
      if (state.busy) throw new Error("已有操作正在执行");
      startJob("正在安装 Mihomo…");
      try {
        const err = await native().InstallCore(v.version);
        if (err) throw new Error(err);
        await refreshAll();
        message("内核安装完成");
      } finally {
        endJob();
      }
    },
    {
      copy: "从官方 Release 下载并校验安装包。下载失败可在设置中配置下载代理，或选择本地导入。",
      submitLabel: "开始安装",
    },
  );
}
function installProgress(event) {
  if (!state.busy) return;
  $("jobLabel").textContent = event.message || "正在安装 Mihomo…";
  const total = Number(event.total || 0),
    downloaded = Number(event.downloaded || 0);
  if (total > 0) {
    const percent = Math.min(100, Math.round((downloaded / total) * 100));
    $("jobProgress").value = percent;
    $("jobPercent").textContent = percent + "%";
  }
  $("jobDetail").textContent =
    event.error ||
    `${downloaded ? (downloaded / 1048576).toFixed(1) + " MB" : ""}${total ? " / " + (total / 1048576).toFixed(1) + " MB" : ""}${event.bytesPerSecond ? " · " + (event.bytesPerSecond / 1048576).toFixed(2) + " MB/s" : ""}`;
}

function applyTheme(value) {
  const dark =
    value === "dark" ||
    (value === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  try {
    localStorage.setItem("kivo.desktop.theme", value);
  } catch {}
  native()?.SetTheme(value);
  $("theme").value = value;
}
function bind() {
  $("addSubscription").onclick = addSubscription;
  $("configAddSub").onclick = addSubscription;
  $("connect").onclick = connect;
  $("routing").onclick = routeDialog;
  $("checkConnectivity").onclick = checkConnectivity;
  $("dataCheck").onclick = checkConnectivity;
  $("updateSubscriptions").onclick = () => updateDialog();
  $("testNodes").onclick = () =>
    perform(
      "正在测速所有已加载节点…",
      async () => {
        if (state.overview?.core?.state !== "running")
          throw new Error("请先启动内核再测速");
        await api("/api/v1/nodes/test", "POST", {});
      },
      { success: "节点测速完成" },
    );
  $("nodeSearch").addEventListener("input", renderNodes);
  $("closeDialog").onclick = closeDialog;
  $("dialog").addEventListener("cancel", (event) => {
    event.preventDefault();
    closeDialog();
  });
  $("dialog").addEventListener("click", (event) => {
    if (event.target === $("dialog")) {
      const bounds = $("dialog").getBoundingClientRect();
      if (
        event.clientX < bounds.left ||
        event.clientX > bounds.right ||
        event.clientY < bounds.top ||
        event.clientY > bounds.bottom
      )
        closeDialog();
    }
  });
  $("openWeb").onclick = async () => {
    const err = await native()?.OpenWeb();
    if (err) message(err, true);
  };
  $("reconnect").onclick = async () => {
    if (state.busy) return;
    await perform(
      "重新连接后台…",
      async () => {
        const err = await native().Reconnect();
        if (err) throw new Error(err);
        state.info = await native().Info();
      },
      { success: "后台已连接" },
    );
  };
  $("refreshData").onclick = () =>
    perform("刷新状态…", () => refreshOverview(), {
      refresh: false,
      success: "状态已刷新",
    });
  $("refreshLogs").onclick = () =>
    perform(
      "读取日志…",
      async () => {
        $("logs").textContent =
          ((await api("/api/v1/logs?limit=200")) || []).join("\n") ||
          "暂无内核日志";
      },
      { refresh: false, success: "" },
    );
  $("doctor").onclick = () =>
    perform(
      "正在进行环境诊断…",
      async () => {
        const items = await api("/api/v1/doctor");
        $("doctorResults").replaceChildren();
        for (const item of items || [])
          $("doctorResults").append(
            record(
              item.name,
              item.message,
              item.status === "ok" ? "正常" : "需关注",
              item.status === "ok",
            ),
          );
      },
      { refresh: false, success: "诊断完成" },
    );
  $("addSubGroup").onclick = () =>
    formDialog(
      "新建订阅分组",
      [{ name: "name", label: "分组名称", required: true }],
      (v) => mutate("创建分组…", "/api/v1/subscription-groups", "POST", v),
    );
  $("addProfile").onclick = () =>
    formDialog(
      "新建路由配置",
      [
        { name: "name", label: "配置名称", required: true },
        {
          name: "defaultAction",
          label: "未匹配流量",
          options: [
            ["direct", "直连（只代理规则命中的地址）"],
            ["proxy", "代理（规则指定的地址除外）"],
            ["reject", "拒绝"],
          ],
        },
      ],
      (v) =>
        mutate("创建配置…", "/api/v1/routing/profiles", "POST", {
          ...v,
          groups: [],
        }),
    );
  $("addRuleGroup").onclick = () =>
    formDialog(
      "新建规则组",
      [{ name: "name", label: "规则组名称", required: true }],
      (v) => mutate("创建规则组…", "/api/v1/routing/groups", "POST", v),
    );
  $("theme").onchange = (event) => applyTheme(event.target.value);
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if ($("theme").value === "system") applyTheme("system");
  });
  $("settingsForm").onsubmit = (event) => {
    event.preventDefault();
    if (!state.settings) return;
    const f = event.currentTarget.elements;
    const values = {
      ...state.settings,
      mixedPort: Number(f.mixedPort.value),
      allowLAN: f.allowLAN.checked,
      tunEnabled: f.tunEnabled.checked,
      downloadProxy: f.downloadProxy.value.trim(),
      downloadRetry: Number(f.downloadRetry.value),
    };
    confirm(
      "应用代理设置",
      "运行中的内核可能重载；端口变更会同时尝试更新受管系统代理。TUN 需要平台权限，请勿与其他虚拟网卡代理冲突。",
      () =>
        perform("保存代理设置…", () =>
          api("/api/v1/settings", "PATCH", values),
        ),
      "应用设置",
    );
  };
  $("tokenForm").onsubmit = (event) => {
    event.preventDefault();
    const input = event.currentTarget.elements.token;
    const token = input.value.trim();
    confirm(
      token ? "更新 Web Token" : "关闭 Web 鉴权",
      token
        ? "更新后 Web 的其他会话需要重新认证；桌面端自动使用新凭据。"
        : "不设置 Token 时，能访问监听地址的人都可以管理代理。请确保仅本机或可信网络可访问。",
      () =>
        perform("更新 Web 访问安全…", async () => {
          await api("/api/v1/web/security", "PATCH", { token });
          input.value = "";
        }),
      "确认更新",
      !token,
    );
  };
  $("installCore").onclick = installCore;
  $("importCore").onclick = () =>
    perform(
      "导入官方内核文件…",
      async () => {
        const result = await native().ImportCore();
        if (result.error) throw new Error(result.error);
      },
      { success: "导入流程已完成" },
    );
  for (const [id, action] of [
    ["coreStart", "start"],
    ["coreRestart", "restart"],
    ["coreStop", "stop"],
  ])
    $(id).onclick = () =>
      confirm(
        "内核操作",
        `仅${{ start: "启动", restart: "重启", stop: "停止" }[action]}内核；启动内核不等于接入系统代理。停止可能会影响当前代理连接。`,
        () =>
          perform("正在操作内核…", () =>
            api(`/api/v1/core/${action}`, "POST", {}),
          ),
      );
  $("quit").onclick = () =>
    confirm(
      "仅关闭窗口",
      "后台与当前代理会继续运行。可再次打开 Kivo，或使用 CLI 管理。",
      async () => {
        const err = await native().Quit(false);
        if (err) message(err, true);
      },
      "关闭窗口",
    );
  $("disconnectQuit").onclick = () =>
    confirm(
      "断开并退出",
      "断开受管代理、恢复系统设置并停止内核，然后关闭窗口。后台控制服务保留供 CLI / Web 使用；恢复存在冲突时不会强制退出。",
      () =>
        perform(
          "断开并退出…",
          async () => {
            const err = await native().Quit(true);
            if (err) throw new Error(err);
          },
          { refresh: false, success: "" },
        ),
      "断开并退出",
      true,
    );
  const tabs = [...document.querySelectorAll("[data-tab]")];
  tabs.forEach((tab, index) => {
    tab.onclick = () => setTab(tab.dataset.tab);
    tab.addEventListener("keydown", (event) => {
      if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const next =
          event.key === "Home"
            ? 0
            : event.key === "End"
              ? tabs.length - 1
              : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
                tabs.length;
        setTab(tabs[next].dataset.tab, true);
      }
    });
  });
  window.runtime?.EventsOn("core:progress", installProgress);
}
async function bootstrap() {
  bind();
  let theme = "system";
  try {
    theme = localStorage.getItem("kivo.desktop.theme") || "system";
  } catch {}
  applyTheme(["light", "dark", "system"].includes(theme) ? theme : "system");
  $("nodeGroups").append(element("p", "loading", "正在连接本地后台…"));
  for (let attempt = 0; attempt < 35; attempt++) {
    try {
      if (!native()) throw new Error("等待原生连接…");
      state.info = await native().Info();
      if (state.info.ready) {
        await refreshAll();
        if (state.info.smokeTest) {
          await document.fonts.ready;
          native().NativeReady(
            JSON.stringify({
              ready: true,
              version: state.info.version,
              bridge: true,
              pages: document.querySelectorAll(".page").length,
              tabs: document.querySelectorAll("[role=tab]").length,
              heading: $("connectionTitle").textContent,
              width: window.innerWidth,
              height: window.innerHeight,
              overflow:
                document.documentElement.scrollWidth > window.innerWidth,
              font: document.fonts.check('14px "Kivo Sans"'),
            }),
          );
        }
        return;
      }
      if (state.info.error && !state.info.starting)
        throw new Error(state.info.error);
    } catch (error) {
      $("backendMessage").textContent = error.message;
      $("connectionAlert").hidden = false;
      if (state.info?.error) {
        renderOverview();
        renderNodes();
        return;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 400));
  }
  state.online = false;
  renderOverview();
  renderNodes();
  $("backendMessage").textContent =
    state.info?.error || "后台连接超时，请检查配置或点击重试";
  $("connectionAlert").hidden = false;
}
// 可见窗口每 5 秒刷新状态；隐藏和进行操作时不轮询，也不覆盖正在编辑的设置。
setInterval(async () => {
  if (document.hidden || state.busy || state.refreshing || !state.info?.ready)
    return;
  state.refreshing = true;
  try {
    await refreshOverview();
    const nodes = await api("/api/v1/nodes");
    state.nodes = nodes || [];
    state.nodesFresh = true;
    renderNodes();
  } catch (error) {
    state.online = false;
    $("connectionAlert").hidden = false;
    $("backendMessage").textContent = error.message;
    renderOverview();
    renderNodes();
  } finally {
    state.refreshing = false;
  }
}, 5000);
bootstrap().catch((error) => message(error.message, true));
