import {
  modes,
  coreStates,
  connectionView,
  checkTime,
  fresh,
  homeConnectionTitle,
  groupNodes,
  nodeLatency,
  selectedNode,
  updateSummary,
} from "./model.mjs";
import { RefreshGate, affectedResources } from "./sync.mjs";

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
  scroll: {},
  changed: new Set(),
  draft: null,
  dirty: false,
  phase: "",
  nodeRows: new Map(),
  nodeGroups: new Map(),
  editor: null,
  subscriptionResults: new Map(),
  dataEpoch: 0,
  doctorLoaded: false,
};
const gate = new RefreshGate();
let toastTimer, lastFocus;
let doctorLoading = false,
  logsLoading = false;
const native = () => window.go?.main?.Desktop;

// 原生托盘状态也需持续刷新；丢失宿主后不能继续承诺“关闭可驻留”。
function renderTray() {
  $("trayStatus").textContent = state.info?.trayReady
    ? "托盘已就绪 · 关闭窗口后驻留；点击托盘恢复，右键打开菜单。"
    : state.info?.trayStarting
      ? "正在初始化托盘，请稍候…"
      : state.info?.trayError || "托盘不可用，关闭窗口将退出界面，后台仍保留。";
  $("hideWindow").disabled = !state.info?.trayReady || state.busy;
  $("retryTray").hidden = !!state.info?.trayReady;
  $("retryTray").disabled = !!state.info?.trayStarting;
}

function element(tag, className, text) {
  const result = document.createElement(tag);
  if (className) result.className = className;
  if (text != null) result.textContent = text;
  return result;
}

// 文本未变时不替换文本节点，减少轮询对辅助阅读和文本选择的干扰。
function setText(target, value) {
  const text = String(value ?? "");
  if (target.textContent !== text) target.textContent = text;
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
  if (method !== "GET")
    affectedResources(path).forEach((key) => state.changed.add(key));
  return native().Request(
    method,
    path,
    body == null ? "" : JSON.stringify(body),
  );
}
async function api(path, method = "GET", body) {
  // 旧后台按完整对象覆盖设置，不能把新的局部 PATCH 发给旧实现。
  if (
    method === "PATCH" &&
    path === "/api/v1/settings" &&
    !state.settings?.revision
  )
    throw new Error(
      "后台版本过旧或设置尚未读取，暂不能安全保存。请退出旧后台，再启动新版桌面。",
    );
  const result = await reply(path, method, body);
  if (result.error || result.status >= 400) {
    const err = new Error(result.error || `操作失败（${result.status}）`);
    err.data = result.data;
    throw err;
  }
  return result.data;
}
function startJob(label) {
  gate.invalidate();
  state.changed.clear();
  state.busy = true;
  $("job").hidden = false;
  $("jobLabel").textContent = label;
  $("jobDetail").textContent = "";
  $("jobPercent").textContent = "";
  $("jobProgress").removeAttribute("value");
  updateAvailability();
}
function endJob() {
  state.busy = false;
  state.phase = "";
  $("job").hidden = true;
  renderOverview();
  updateAvailability();
}

// 忙碌只改变控件状态，不删除列表；原有业务禁用条件单独保存。
function updateAvailability() {
  $("editorSave").disabled = state.busy || !state.online;
  document.querySelectorAll("[data-mutation]").forEach((item) => {
    item.disabled = state.busy || item.dataset.blocked === "true";
  });
  for (const row of state.nodeRows.values()) {
    row.choose.disabled =
      state.busy ||
      !state.online ||
      !state.nodesFresh ||
      state.overview?.core?.state !== "running" ||
      row.node.cached ||
      !row.enabled;
  }
  $("connect").disabled =
    state.busy ||
    !state.online ||
    state.overview?.systemProxy?.supported === false;
  renderTray();
}

async function refreshChanged() {
  gate.invalidate();
  return refreshResources([...state.changed]);
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
    const result = await operation();
    const failures = refresh ? await refreshChanged() : [];
    const notice = result?.warning ? result.message : success;
    if (notice || failures.length)
      message(
        [
          notice,
          failures.length ? "操作已生效；部分状态暂未刷新，可点击重试" : "",
        ]
          .filter(Boolean)
          .join("。"),
        !!result?.warning || failures.length > 0,
      );
    return true;
  } catch (error) {
    if (refresh) await refreshChanged().catch(() => {});
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
  input.setAttribute("aria-label", spec.label);
  if (input.tagName === "INPUT") input.type = spec.type || "text";
  if (spec.type === "checkbox") input.checked = !!spec.value;
  else input.value = String(spec.value ?? "");
  if (spec.placeholder) input.placeholder = spec.placeholder;
  if (spec.hint) input.title = spec.hint;
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
    const failures = await refreshChanged();
    message(
      failures.length ? "已保存；部分状态暂未刷新，可点击重试" : "已保存",
      failures.length > 0,
    );
    return result;
  } finally {
    endJob();
  }
}

function renderOverview() {
  const view = connectionView(state.overview, state.online);
  if (state.phase) {
    const attached =
      state.overview?.systemProxy?.state === "this_app" &&
      state.overview?.proxyPortListening;
    view.title =
      state.phase === "disconnect"
        ? "正在断开…"
        : attached
          ? "已接入 · 正在检测…"
          : "正在连接…";
    view.detail =
      state.phase === "disconnect"
        ? "恢复系统代理并停止内核"
        : "验证代理入口与联网情况";
    view.tone = "busy";
  }
  $("connect").classList.toggle("pending", !!state.phase);
  $("connect").setAttribute("aria-busy", String(!!state.phase));
  $("connectionTitle").textContent = state.phase
    ? view.title
    : homeConnectionTitle(view, state.overview, state.online);
  $("connectionTitle").title = $("connectionTitle").textContent;
  $("connectionDetail").textContent = view.detail;
  $("connectionDetail").hidden = !["error", "warn", "busy"].includes(view.tone);
  $("connectionCard").dataset.tone = view.tone;
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
  $("checkLabel").textContent = checkTime(state.overview?.connectivity);
  const core = state.overview?.core;
  $("routeLabel").textContent = modes[core?.mode] || "规则模式";
  const hint = state.overview?.systemProxy?.recoveryPending
    ? "存在未恢复系统代理备份，请到状态页查看，再在设置页恢复。"
    : core?.state === "not_installed"
      ? "首次使用：安装内核 → 添加并更新订阅 → 选择节点 → 打开连接。"
      : "";
  $("connectionHint").hidden = !hint;
  $("connectionHint").textContent = hint;
}
// 只移动/更新发生变化的元素，保留 details、焦点和滚动锚点。
function reconcileChildren(parent, children) {
  const wanted = new Set(children);
  for (const child of [...parent.children])
    if (!wanted.has(child)) child.remove();
  children.forEach((child, index) => {
    if (parent.children[index] !== child)
      parent.insertBefore(child, parent.children[index] || null);
  });
}
function renderNodes() {
  const groups = groupNodes(state.nodes, state.subs, $("nodeSearch").value);
  const running =
    state.online &&
    state.nodesFresh &&
    state.overview?.core?.state === "running";
  const usedRows = new Set(),
    usedGroups = new Set(),
    groupElements = [];
  if (!state.loadedGroups && groups.length) {
    state.openGroups.add(groups[0].name);
    state.loadedGroups = true;
  }
  for (const group of groups) {
    usedGroups.add(group.name);
    let entry = state.nodeGroups.get(group.name);
    if (!entry) {
      const root = element("details", "node-group"),
        summary = element("summary"),
        name = element("strong"),
        count = element("span", "count"),
        copy = element("span", "subscription-heading-copy"),
        feedback = element("p", "subscription-feedback");
      copy.append(name, count);
      const update = button(
        "",
        (event) => {
          event.preventDefault();
          event.stopPropagation();
          updateOneSubscription(group.name);
        },
        "icon-button accent",
      );
      update.append(icon("refresh"));
      update.dataset.mutation = "true";
      update.setAttribute("aria-label", "更新订阅 " + group.name);
      const edit = button(
        "",
        (event) => {
          event.preventDefault();
          event.stopPropagation();
          const sub = state.subs.find((s) => s.name === group.name);
          if (sub) editSubscription(sub);
        },
        "icon-button accent",
      );
      edit.append(icon("info"));
      edit.dataset.mutation = "true";
      edit.setAttribute("aria-label", "编辑订阅 " + group.name);
      summary.append(icon("chevron"), copy, update, edit);
      root.append(summary);
      entry = { root, summary, name, count, feedback, update, edit, limit: 80 };
      root.addEventListener("toggle", () => {
        if (root.open) state.openGroups.add(group.name);
        else state.openGroups.delete(group.name);
      });
      state.nodeGroups.set(group.name, entry);
    }
    setText(entry.name, group.name);
    const subscription = state.subs.find((s) => s.name === group.name);
    entry.edit.hidden = entry.update.hidden = !subscription;
    entry.update.dataset.blocked = String(!state.online || !group.enabled);
    entry.edit.dataset.blocked = String(!state.online);
    entry.update.title =
      "更新订阅 · " +
      (subscription?.updateVia === "proxy" ? "通过代理" : "直连");
    setText(
      entry.count,
      group.nodes.length +
        " 个节点" +
        (group.enabled
          ? subscription?.updateVia === "proxy"
            ? " · 代理更新"
            : " · 直连更新"
          : " · 已禁用"),
    );
    const open = state.openGroups.has(group.name) || !!$("nodeSearch").value;
    if (entry.root.open !== open) entry.root.open = open;
    const rows = [entry.summary];
    const result = state.subscriptionResults.get(group.name);
    if (result) {
      setText(entry.feedback, result.text);
      entry.feedback.classList.toggle("failed", !!result.error);
      rows.push(entry.feedback);
    }
    for (const node of group.nodes.slice(0, entry.limit)) {
      const key = JSON.stringify([group.name, node.name]);
      usedRows.add(key);
      let row = state.nodeRows.get(key);
      if (!row) {
        const root = element("div", "node-row"),
          name = element("span", "node-name"),
          type = element("span", "node-type"),
          copy = element("span", "node-copy");
        row = { root, name, type, node, enabled: group.enabled };
        const item = row;
        row.choose = button(
          "",
          () =>
            perform(
              "正在切换节点…",
              () =>
                api("/api/v1/nodes/select", "POST", { name: item.node.name }),
              { success: "节点已切换" },
            ),
          "node-select",
        );
        row.choose.dataset.mutation = "true";
        copy.append(name, type);
        row.choose.append(element("span", "radio"), copy);
        row.delay = element("span", "delay");
        row.info = button("", () => nodeDetail(item.node), "icon-button");
        row.info.append(icon("info"));
        root.append(row.choose, row.delay, row.info);
        state.nodeRows.set(key, row);
      }
      row.node = node;
      row.enabled = group.enabled;
      row.root.classList.toggle("selected", selectedNode(state.overview, node));
      setText(row.name, node.name);
      setText(
        row.type,
        (node.type || "Proxy") + (node.cached ? " · 离线快照" : ""),
      );
      row.choose.setAttribute("aria-label", "选择 " + node.name);
      row.choose.setAttribute(
        "aria-pressed",
        String(selectedNode(state.overview, node)),
      );
      row.info.setAttribute("aria-label", node.name + " 的详细信息");
      const latency = nodeLatency(node, running);
      setText(row.delay, latency.text);
      row.delay.className = "delay " + latency.tone;
      rows.push(row.root);
    }
    if (group.nodes.length > entry.limit) {
      if (!entry.more)
        entry.more = button(
          "显示更多节点",
          () => {
            entry.limit += 80;
            renderNodes();
          },
          "load-more",
        );
      rows.push(entry.more);
    }
    if (!group.nodes.length) {
      if (!entry.empty) entry.empty = element("p", "empty-note");
      entry.empty.textContent = group.enabled
        ? "尚无节点，请更新订阅。"
        : "此订阅已禁用";
      rows.push(entry.empty);
    }
    reconcileChildren(entry.root, rows);
    groupElements.push(entry.root);
  }
  if (!groups.length) {
    if (!state.nodeEmpty) state.nodeEmpty = element("p", "empty-note");
    state.nodeEmpty.textContent = $("nodeSearch").value
      ? "没有匹配的节点"
      : "尚无订阅。点击右上角 + 添加订阅。";
    groupElements.push(state.nodeEmpty);
  }
  reconcileChildren($("nodeGroups"), groupElements);
  for (const key of state.nodeRows.keys())
    if (!usedRows.has(key)) state.nodeRows.delete(key);
  for (const key of state.nodeGroups.keys())
    if (!usedGroups.has(key)) state.nodeGroups.delete(key);
  $("nodesNotice").textContent = !state.online
    ? "后台已断开，旧节点仅供查看"
    : !state.nodesFresh
      ? "节点暂未刷新，旧结果仅供查看"
      : !running
        ? "内核已停止；连接后可选择节点"
        : state.nodes.length + " 个节点";
  updateAvailability();
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
  card.dataset.key = title;
  top.append(element("strong", "", title));
  if (badge)
    top.append(element("span", `badge${active ? " active" : ""}`, badge));
  card.append(top);
  if (subtitle) card.append(element("p", "", subtitle));
  return card;
}
function reconcileCards(parent, cards) {
  const previous = new Map(
    [...parent.children].map((card) => [card.dataset.key, card]),
  );
  const next = cards.map((card) => {
    const old = previous.get(card.dataset.key);
    return old && old.dataset.revision === card.dataset.revision ? old : card;
  });
  const active = document.activeElement;
  const focusKey = active?.closest?.("[data-key]")?.dataset.key;
  const slot = active?.dataset.slot;
  reconcileChildren(parent, next);
  if (focusKey && slot != null && !active.isConnected)
    next
      .find((card) => card.dataset.key === focusKey)
      ?.querySelector('[data-slot="' + slot + '"]')
      ?.focus();
}
function actionRow(card, items) {
  const actions = element("div", "actions");
  const more = element("details", "more-actions"),
    menu = element("div", "more-menu");
  more.append(element("summary", "button small", "更多"), menu);
  for (const [index, [label, action, danger, blocked]] of items.entries()) {
    const b = button(
      label,
      () => {
        more.open = false;
        action();
      },
      `button small${danger ? " danger" : ""}`,
    );
    b.dataset.mutation = "true";
    b.dataset.slot = String(index);
    b.dataset.blocked = String(!!blocked);
    b.disabled = state.busy || !!blocked;
    (items.length > 3 && !["更新", "更新组", "启用", "禁用"].includes(label)
      ? menu
      : actions
    ).append(b);
  }
  if (menu.children.length) actions.append(more);
  card.append(actions);
}
function subscriptionSecurityLabel(sub) {
  const decrypt =
    sub.decryptionType ||
    (["aes", "age"].includes(sub.authType) ? sub.authType : "none");
  const auth =
    sub.downloadAuthType ||
    (["aes", "age"].includes(sub.authType) ? "none" : sub.authType) ||
    "none";
  const labels = [];
  if (decrypt !== "none")
    labels.push(decrypt === "age" ? "age 解密" : "AES 解密");
  if (auth !== "none")
    labels.push(
      { basic: "Basic 认证", bearer: "Bearer 认证", token: "Token 认证" }[
        auth
      ] || "下载认证",
    );
  return labels.join(" · ") || "普通订阅";
}
function renderConfig() {
  const staged = {};
  staged.subscriptions = element("div");
  if (!state.subs.length)
    staged.subscriptions.append(
      element(
        "p",
        "empty-note",
        "添加一个订阅地址，支持普通订阅及 AES / age 加密。",
      ),
    );
  for (const sub of state.subs) {
    const card = record(
      sub.name,
      `${sub.group || "默认分组"} · ${subscriptionSecurityLabel(sub)} · ${sub.updateVia === "proxy" ? "代理" : "直连"}更新`,
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
    card.dataset.revision = JSON.stringify(sub);
    staged.subscriptions.append(card);
  }
  staged.subscriptionGroups = element("div");
  for (const group of state.groups) {
    const card = record(
      group.name,
      `${state.subs.filter((sub) => sub.group === group.name).length} 个订阅`,
      group.enabled ? "已启用" : "已禁用",
      group.enabled,
    );
    actionRow(card, [
      ["更新组", () => updateDialog("", group.name)],
      ["重命名", () => renameSubscriptionGroup(group), false, group.name === "default"],
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
          `删除「${group.name}」？存在订阅时后台会拒绝删除。默认分组不可删除。`,
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
        group.name === "default",
      ],
    ]);
    card.dataset.revision = JSON.stringify([group, state.subs.filter((sub) => sub.group === group.name).length]);
    staged.subscriptionGroups.append(card);
  }
  staged.profiles = element("div");
  for (const profile of state.routing?.profiles || []) {
    const active = state.routing.activeProfile === profile.name;
    const builtin = ["global", "direct", "rule", "bypass-cn", "proxy-only", "bypass-list"].includes(profile.name);
    const card = record(
      profile.name,
      `未匹配：${{ proxy: "代理", direct: "直连", reject: "拒绝" }[profile.defaultAction] || profile.defaultAction} · ${profile.groups?.length || 0} 个规则组`,
      active ? "使用中" : builtin ? "内置方案" : "",
      active,
    );
    if (profile.groups?.length) {
      const chips = element("div", "route-group-chips");
      for (const name of profile.groups) {
        const chip = button(`${name} ×`, () =>
          confirm("解除规则组", `从「${profile.name}」中解除「${name}」？规则组不会被删除。`, () =>
            perform("解除规则组…", () => api("/api/v1/routing/profiles", "PATCH", {
              profile: profile.name, group: name, attached: false,
            })), "解除关联"), "route-group-chip");
        chip.title = `从 ${profile.name} 解除 ${name}`;
        chips.append(chip);
      }
      card.append(chips);
    }
    actionRow(card, [
      [
        "使用",
        () =>
          perform("切换路由配置…", () =>
            api("/api/v1/routing/profiles/use", "POST", { name: profile.name }),
          ),
      ],
      ["关联组", () => profileGroups(profile)],
      [
        "删除",
        () =>
          confirm(
            "删除路由配置",
            `删除自定义路由配置「${profile.name}」？内置方案与当前方案不能删除。`,
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
        active || builtin,
      ],
    ]);
    card.dataset.revision = JSON.stringify(profile);
    staged.profiles.append(card);
  }
  staged.ruleGroups = element("div");
  for (const group of state.routing?.ruleGroups || []) {
    const builtin = ["中国大陆直连", "指定地址代理", "指定地址直连"].includes(group.name);
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
        builtin,
      ],
    ]);
    card.dataset.revision = JSON.stringify(group);
    staged.ruleGroups.append(card);
  }
  for (const [id, container] of Object.entries(staged))
    reconcileCards($(id), [...container.children]);
}
function renderData() {
  const view = connectionView(state.overview, state.online);
  $("statusConclusion").textContent = view.title;
  const explanation = state.online
    ? state.overview?.connection?.detail || view.detail
    : view.detail;
  // 已启用时不重复显示“内核运行不等于外网可用”的泛化提示；检测证据留在下方。
  $("statusExplanation").textContent = view.on &&
    explanation === "内核运行不等于外网可用；请执行一次联网检测。"
    ? "" : explanation;
  $("statusExplanation").hidden = !$("statusExplanation").textContent;
  $("statusSummary").dataset.tone = view.tone;
  const key = JSON.stringify([
    state.overview,
    state.online,
    fresh(state.overview?.connectivity),
  ]);
  if (state.dataKey === key) return;
  state.dataKey = key;
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

// 诊断是只读本机检查。进入状态页即运行一次；手动重试沿用相同入口。
async function loadDoctor() {
  if (doctorLoading || state.tab !== "data" || state.editor || document.hidden)
    return;
  if (!state.online) {
    $("doctorStatus").textContent = "后台未连接";
    return;
  }
  doctorLoading = true;
  const epoch = state.dataEpoch;
  $("doctor").disabled = true;
  $("doctorStatus").textContent = "正在检查…";
  try {
    const items = await api("/api/v1/doctor");
    if (epoch !== state.dataEpoch || state.tab !== "data") return;
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
    if (!items?.length)
      $("doctorResults").append(element("p", "empty-note", "暂无诊断项目"));
    state.doctorLoaded = true;
    $("doctorStatus").textContent = "已更新";
  } catch (error) {
    if (epoch !== state.dataEpoch || state.tab !== "data") return;
    $("doctorStatus").textContent = "读取失败";
    if (!state.doctorLoaded)
      $("doctorResults").replaceChildren(
        element("p", "empty-note error-text", `诊断失败：${error.message}`),
      );
  } finally {
    doctorLoading = false;
    $("doctor").disabled = false;
  }
}

// 内核已经将 stdout/stderr 写入有界内存日志；可见的状态页每秒读取最新 200 行。
async function loadLogs() {
  if (logsLoading || state.tab !== "data" || state.editor || document.hidden)
    return;
  if (!state.online) {
    $("logStatus").textContent = "后台未连接";
    return;
  }
  logsLoading = true;
  const epoch = state.dataEpoch;
  $("refreshLogs").disabled = true;
  try {
    const lines = await api("/api/v1/logs?limit=200");
    if (epoch !== state.dataEpoch || state.tab !== "data") return;
    const log = $("logs");
    const nearBottom = log.scrollHeight - log.clientHeight - log.scrollTop < 32;
    const content =
      (lines || []).join("\n") || "暂无内核日志；启动内核后会自动显示";
    if (log.textContent !== content) {
      log.textContent = content;
      if (nearBottom) log.scrollTop = log.scrollHeight;
    }
    $("logStatus").textContent = "";
  } catch (error) {
    if (epoch !== state.dataEpoch || state.tab !== "data") return;
    $("logStatus").textContent = `更新失败：${error.message}`;
  } finally {
    logsLoading = false;
    $("refreshLogs").disabled = false;
  }
}
function renderSettings(preserveForm = false) {
  renderTray();
  setText(
    $("networkSummary"),
    state.settings ? "端口 " + state.settings.mixedPort : "未读取",
  );
  $("compatibilityNotice").hidden =
    !state.settings || !!state.settings.revision;
  if (state.settings && !preserveForm) {
    state.draft = { ...state.settings };
    const form = $("settingsForm");
    for (const name of [
      "mixedPort",
      "allowLAN",
      "tunEnabled",
      "downloadProxy",
      "downloadRetry",
    ]) {
      const control = form.elements.namedItem(name);
      if (!control) continue;
      if (control.type === "checkbox") control.checked = !!state.settings[name];
      else control.value = state.settings[name] ?? "";
    }
  }
  $("settingsDraft").textContent = state.dirty
    ? state.settings?.revision !== state.draft?.revision
      ? "其他客户端已修改设置。当前草稿已保留；重新读取后再保存。"
      : "有未保存的修改"
    : "";
  $("settingsDraft").hidden = !state.dirty;
  $("reloadSettings").hidden = !state.dirty;
  $("coreStart").dataset.blocked = String(
    state.overview?.core?.state === "running",
  );
  $("coreStop").dataset.blocked = String(
    state.overview?.core?.state !== "running",
  );
  $("coreRestart").dataset.blocked = String(
    state.overview?.core?.state === "not_installed",
  );
  $("coreRunState").textContent = coreStates[state.overview?.core?.state] || "状态未知";
  $("installedCore").textContent = state.overview?.core?.version || "尚未安装";
  const coreRunning = state.overview?.core?.state === "running";
  const activeInstallation = state.versions.find((item) => item.active);
  $("activeCoreBadge").hidden = !coreRunning || !activeInstallation ||
    activeInstallation.version !== state.overview?.core?.version;
  $("coreStart").hidden = coreRunning;
  $("coreStop").hidden = !coreRunning;
  $("coreUse").dataset.blocked = String(!state.versions.some((item) => !item.active));
  $("coreDelete").dataset.blocked = String(!state.versions.some((item) => !item.active));
  $("webSecurityLabel").textContent = state.security?.authEnabled
    ? "已开启 Token"
    : "无需 Token";
  const recovery = $("coreRecovery");
  recovery.replaceChildren();
  if (state.overview?.systemProxy?.recoveryPending) {
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
    b.dataset.key = "recover";
    b.dataset.revision = "recover";
    recovery.append(b);
  }
  $("appInfo").textContent = state.info
    ? `Kivo ${state.info.version} · ${state.info.platform} / ${state.info.architecture}`
    : "Kivo Desktop";
  $("dataDirectory").textContent = state.info
    ? `数据目录：${state.info.dataDirectory}`
    : "";
}

const resources = {
  overview: "/overview",
  nodes: "/nodes",
  subs: "/subscriptions",
  groups: "/subscription-groups",
  routing: "/routing",
  settings: "/settings",
  versions: "/core/installations",
  security: "/web/security",
};
const resourceNames = {
  overview: "运行状态",
  nodes: "节点",
  subs: "订阅",
  groups: "订阅分组",
  routing: "路由",
  settings: "设置",
  versions: "内核版本",
  security: "Web 安全",
};
const resourceErrors = new Map();

function renderSyncErrors() {
  $("syncNotice").hidden = resourceErrors.size === 0;
  $("syncMessage").textContent =
    [...resourceErrors.keys()].map((key) => resourceNames[key]).join("、") +
    "暂未刷新，保留上次结果";
}
async function readResource(key) {
  const ticket = gate.begin(key);
  try {
    const value = await api("/api/v1" + resources[key]);
    if (!gate.current(ticket)) return false;
    state[key] =
      value ??
      (["nodes", "subs", "groups", "versions"].includes(key) ? [] : null);
    resourceErrors.delete(key);
    if (key === "overview") {
      state.online = true;
      $("connectionAlert").hidden = true;
    }
    if (key === "nodes") state.nodesFresh = true;
    return true;
  } catch (error) {
    if (!gate.current(ticket)) return false;
    resourceErrors.set(key, error.message);
    if (key === "overview") {
      state.online = false;
      $("connectionAlert").hidden = false;
      $("backendMessage").textContent = error.message;
    }
    if (key === "nodes") state.nodesFresh = false;
    throw error;
  }
}
// 各资源独立提交结果，局部失败不会伪装成后台断开；渲染保持当前草稿和滚动。
async function refreshResources(keys) {
  const selected = [...new Set(keys)].filter((key) => resources[key]);
  const results = await Promise.allSettled(selected.map(readResource));
  const changed = selected.filter(
    (key, index) =>
      results[index].status === "fulfilled" && results[index].value,
  );
  if (selected.includes("overview")) {
    renderOverview();
    renderData();
  }
  if (selected.some((key) => ["overview", "nodes", "subs"].includes(key)))
    renderNodes();
  if (changed.some((key) => ["subs", "groups", "routing"].includes(key)))
    renderConfig();
  if (
    selected.some((key) =>
      ["overview", "settings", "versions", "security"].includes(key),
    )
  )
    renderSettings(state.dirty);
  renderSyncErrors();
  updateAvailability();
  return selected.filter((key, index) => results[index].status === "rejected");
}
async function refreshOverview() {
  return refreshResources(["overview"]);
}
async function refreshAll() {
  return refreshResources(Object.keys(resources));
}

function setTab(tab, focus = false) {
  if (state.editor) {
    leaveSubscriptionEditor(() => setTab(tab, focus));
    return;
  }
  const content = $("content");
  const changed = tab !== state.tab;
  if (changed) {
    state.scroll[state.tab] = content.scrollTop;
  }
  state.tab = tab;
  for (const b of document.querySelectorAll("[data-tab]")) {
    const active = b.dataset.tab === tab;
    b.classList.toggle("active", active);
    b.setAttribute("aria-selected", String(active));
    b.tabIndex = active ? 0 : -1;
    $("panel-" + b.dataset.tab).hidden = !active;
    if (active && focus) b.focus();
  }
  if (changed) content.scrollTop = state.scroll[tab] || 0;
  if (changed) state.dataEpoch++;
  if (changed && tab === "data") {
    state.doctorLoaded = false;
    void loadDoctor();
    void loadLogs();
  }
  const keys =
    tab === "settings"
      ? ["settings", "versions", "security"]
      : tab === "config"
        ? ["subs", "groups", "routing"]
        : [];
  if (state.online && !state.busy && !state.refreshing && keys.length) {
    state.refreshing = true;
    refreshResources(keys).finally(() => {
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
        state.phase = "disconnect";
        renderOverview();
        const result = await api("/api/v1/connection/disconnect", "POST", {});
        return result;
      },
      { success: "已断开，已处理系统代理恢复" },
    );
  if (state.overview?.core?.state === "not_installed") {
    setTab("settings");
    $("coreSection").scrollIntoView({ behavior: "smooth", block: "start" });
    message("请先安装 Mihomo 内核，再添加并更新订阅");
    return;
  }
  const p = state.overview.systemProxy;
  const run = (options) =>
    perform(
      "正在启动内核、验证入口并接入系统代理…",
      async () => {
        state.phase = "connect";
        renderOverview();
        const result = await api("/api/v1/connection/connect", "POST", options);
        return result;
      },
      { success: "已连接，联网检测完成" },
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
    "代理模式",
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
        value: "",
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
        rememberSubscriptionResults(result.data);
        await refreshChanged();
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
function rememberSubscriptionResults(data) {
  for (const item of data?.results || []) {
    const via = item.via === "proxy" ? "代理" : "直连";
    state.subscriptionResults.set(item.name, {
      error: !!item.error,
      text: item.error
        ? `${via}更新失败：${item.error}`
        : `${via}更新完成 · ${item.nodeCount == null ? "节点数待确认" : "解析 " + item.nodeCount + " 个节点"}${data.temporarilyStartedCore ? " · 内核已恢复停止" : ""}`,
    });
  }
}
async function updateOneSubscription(name) {
  if (state.busy) return;
  state.subscriptionResults.set(name, { text: "正在更新订阅…" });
  renderNodes();
  await perform(
    "正在更新订阅…",
    async () => {
      try {
        const result = await reply("/api/v1/subscriptions/update", "POST", {
          reference: name,
        });
        rememberSubscriptionResults(result.data);
        if (result.error || result.status >= 400)
          throw new Error(result.error || "更新失败");
        if (!result.data?.results?.length)
          state.subscriptionResults.set(name, {
            text: "更新请求完成，节点统计待确认",
          });
      } catch (error) {
        state.subscriptionResults.set(name, {
          text: "更新失败：" + error.message,
          error: true,
        });
        throw error;
      } finally {
        renderNodes();
      }
    },
    { success: "" },
  );
}
const authOptions = [
  ["none", "无需认证"],
  ["basic", "Basic 用户名 / 密码"],
  ["bearer", "Bearer Token"],
  ["token", "Authorization: token 请求头"],
];
function addSubscription() {
  openSubscriptionEditor(null);
}
function editSubscription(sub) {
  openSubscriptionEditor(sub);
}

// 编辑页拥有独立的内存草稿；后台轮询不能重建表单，密码也不进入 localStorage。
function openSubscriptionEditor(sub) {
  if (state.editor || state.busy) return;
  const editor = {
    sub,
    fields: {},
    dirty: false,
    scroll: $("content").scrollTop,
    focus: document.activeElement,
  };
  state.editor = editor;
  $("mainHeader").hidden = $("mainTabs").hidden = true;
  $("panel-" + state.tab).hidden = true;
  $("editorHeader").hidden = $("subscriptionEditor").hidden = false;
  $("editorTitle").textContent = sub ? "编辑订阅" : "添加订阅";
  $("editorError").textContent = "";
  $("content").scrollTop = 0;
  const form = $("subscriptionForm");
  form.replaceChildren();
  const type = element("div", "group editor-type");
  type.append(
    element("span", "editor-type-label", "类型"),
    element("span", "editor-type-value", "订阅"),
  );
  form.append(type);
  const basic = element(
    "div",
    `group editor-fields editor-basic${sub ? " editor-edit" : ""}`,
  );
  form.append(basic);
  const add = (parent, spec) => {
    const input = field(parent, spec);
    editor.fields[spec.name] = input;
    return input;
  };
  add(basic, {
    name: "url",
    label: "订阅地址",
    type: "url",
    required: !sub,
    placeholder: sub ? "留空保留原地址" : "https://…（必填）",
    hint: sub
      ? "原地址已脱敏，不回填、不覆盖。仅需更换时输入完整新地址。"
      : "完整粘贴平台提供的链接，URL 中已有 Token 时无需额外认证。",
  });
  add(basic, {
    name: "name",
    label: "名称",
    value: sub?.name || "",
    required: !!sub,
    placeholder: "可选，默认使用订阅域名",
  });
  const secret = add(basic, {
    name: "decryptSecret",
    label: "解密密码",
    type: "password",
    placeholder: sub
      ? "留空保留已有密码或私钥"
      : "普通订阅留空；密码保护订阅填写密码",
    hint: "填写密码默认使用已支持的 AES 兼容格式；其他格式请展开高级设置。不是节点密码。",
  });
  const reveal = button(
    "显示",
    () => {
      secret.type = secret.type === "password" ? "text" : "password";
      reveal.textContent = secret.type === "password" ? "显示" : "隐藏";
      reveal.setAttribute("aria-pressed", String(secret.type === "text"));
    },
    "text-button reveal-secret",
  );
  reveal.setAttribute("aria-label", "显示或隐藏解密凭据");
  secret.parentNode.append(reveal);
  add(basic, {
    name: "updateVia",
    label: "更新方式",
    options: [
      ["direct", "直连"],
      ["proxy", "通过当前代理节点"],
    ],
    value: sub?.updateVia || "direct",
    hint: "直连忽略 HTTP 代理；系统 VPN、TUN 或网关仍可能影响实际路径。",
  });
  const advanced = element("details", "section-disclosure editor-advanced");
  advanced.append(element("summary", "", "高级设置"));
  form.append(advanced);
  const section = (title, hint) => {
    advanced.append(element("h2", "editor-section-title", title));
    if (hint) advanced.append(element("p", "footnote", hint));
    const group = element("div", "group editor-fields");
    advanced.append(group);
    return group;
  };
  const decryptGroup = section(
    "内容解密",
    "解密发生在下载之后，不会把解密密码作为登录凭据发送。",
  );
  const originalDecrypt =
    sub?.decryptionType ||
    (["aes", "age"].includes(sub?.authType) ? sub.authType : "none");
  add(decryptGroup, {
    name: "decryptionType",
    label: "解密格式",
    options: [
      ["none", "未加密"],
      ["aes", "AES-CBC 兼容格式"],
      ["age", "age 私钥（非普通口令）"],
    ],
    value: originalDecrypt,
  });
  const download = section(
    "订阅下载",
    "下载认证与内容解密相互独立，可同时使用。默认无需额外认证。",
  );
  const originalAuth =
    sub?.downloadAuthType ||
    (["aes", "age"].includes(sub?.authType) ? "none" : sub?.authType) ||
    "none";
  add(download, {
    name: "downloadAuthType",
    label: "下载认证",
    options: authOptions,
    value: originalAuth,
  });
  add(download, {
    name: "username",
    label: "Basic 用户名",
    placeholder: sub ? "留空保留已有用户名" : "Basic 认证用户名",
  });
  add(download, {
    name: "authSecret",
    label: "认证密码 / Token",
    type: "password",
    placeholder: sub ? "留空保留已有凭据" : "仅用于订阅下载认证",
  });
  add(download, {
    name: "userAgent",
    label: "客户端标识（User-Agent）",
    value: sub?.options?.userAgent || "",
    placeholder: "默认 Clash.Meta",
    hint: "不是代理服务器地址。更改后，服务商可能返回不同的订阅格式。",
  });
  add(download, {
    name: "updateInterval",
    label: "自动更新周期（秒）",
    type: "number",
    min: 60,
    max: 604800,
    required: true,
    value: sub?.updateInterval || 3600,
  });
  add(download, {
    name: "group",
    label: "订阅分组",
    options: state.groups.length
      ? state.groups.map((g) => g.name)
      : ["default"],
    value: sub?.group || state.groups[0]?.name || "default",
  });
  const filtering = section(
    "节点过滤",
    "只改变此订阅的节点列表，不是网站分流。使用 Go/RE2 正则，不支持前后查找。",
  );
  add(filtering, {
    name: "filter",
    label: "保留名称匹配",
    value: sub?.options?.filter || "",
    placeholder: "例如：香港|日本；留空保留全部",
  });
  add(filtering, {
    name: "excludeFilter",
    label: "排除名称匹配",
    value: sub?.options?.excludeFilter || "",
    placeholder: "例如：过期|剩余流量；留空不排除",
  });
  const overrides = section(
    "节点连接设置",
    "跟随订阅不覆盖原始值；仅对支持相应功能的节点生效。",
  );
  const choices = [
    ["default", "跟随订阅"],
    ["on", "开启"],
    ["off", "关闭"],
  ];
  add(overrides, {
    name: "udp",
    label: "UDP 转发",
    options: choices,
    value: sub?.options?.udp || "default",
    hint: "转发应用的 UDP 流量，不影响订阅下载方式。",
  });
  add(overrides, {
    name: "tfo",
    label: "TCP 快速打开",
    options: choices,
    value: sub?.options?.tfo || "default",
  });
  add(overrides, {
    name: "skipCertVerify",
    label: "节点证书验证",
    options: [
      ["default", "跟随订阅"],
      ["off", "严格验证"],
      ["on", "跳过验证（不安全）"],
    ],
    value: sub?.options?.skipCertVerify || "default",
    hint: "跳过验证可能遭到中间人攻击；此设置不会关闭订阅网站的 HTTPS 验证。",
  });
  const controls = editor.fields;
  const updateFields = () => {
    controls.username.parentNode.hidden =
      controls.downloadAuthType.value !== "basic";
    controls.authSecret.parentNode.hidden =
      controls.downloadAuthType.value === "none";
    secret.parentNode.children[0].textContent =
      controls.decryptionType.value === "age" ? "age 解密私钥" : "解密密码";
    secret.setAttribute(
      "aria-label",
      controls.decryptionType.value === "age" ? "age 解密私钥" : "解密密码",
    );
  };
  controls.downloadAuthType.addEventListener("change", updateFields);
  controls.decryptionType.addEventListener("change", () => {
    editor.explicitFormat = true;
    if (controls.decryptionType.value === "none") secret.value = "";
    updateFields();
  });
  secret.addEventListener("input", () => {
    if (secret.value && controls.decryptionType.value === "none")
      controls.decryptionType.value = "aes";
    if (!secret.value && originalDecrypt === "none" && !editor.explicitFormat)
      controls.decryptionType.value = "none";
  });
  form.oninput = form.onchange = () => {
    editor.dirty = true;
  };
  form.onsubmit = saveSubscriptionEditor;
  updateFields();
  updateAvailability();
  controls.url.focus();
}

// 凭据留空代表保留，不发送脱敏字符串；换认证类型时要求重新输入，避免跨协议复用密码。
function subscriptionEditorPayload(editor) {
  const sub = editor.sub,
    f = editor.fields;
  const value = (name) => f[name].value.trim();
  const auth = { type: value("downloadAuthType") },
    decrypt = { type: value("decryptionType") };
  const originalAuth =
    sub?.downloadAuthType ||
    (["aes", "age"].includes(sub?.authType) ? "none" : sub?.authType) ||
    "none";
  const originalDecrypt =
    sub?.decryptionType ||
    (["aes", "age"].includes(sub?.authType) ? sub.authType : "none");
  if (auth.type !== "none") {
    if (auth.type === "basic" && value("username"))
      auth.username = value("username");
    if (f.authSecret.value) auth.secret = f.authSecret.value;
    if (
      (!sub || originalAuth !== auth.type) &&
      (!auth.secret || (auth.type === "basic" && !auth.username))
    )
      throw new Error("请填写完整的下载认证凭据");
  }
  if (decrypt.type !== "none") {
    if (f.decryptSecret.value) decrypt.secret = f.decryptSecret.value;
    if ((!sub || originalDecrypt !== decrypt.type) && !decrypt.secret)
      throw new Error("请填写解密密码或 age 私钥");
  }
  const body = {
    name: value("name"),
    group: value("group"),
    updateVia: value("updateVia"),
    updateInterval: Number(value("updateInterval")),
    downloadAuth: auth,
    decryption: decrypt,
    options: Object.fromEntries(
      [
        "userAgent",
        "filter",
        "excludeFilter",
        "udp",
        "tfo",
        "skipCertVerify",
      ].map((name) => [name, value(name)]),
    ),
  };
  if (!sub || value("url")) body.url = value("url");
  if (sub) {
    body.reference = sub.name;
    body.revision = sub.revision;
  }
  return body;
}

async function saveSubscriptionEditor(event) {
  event.preventDefault();
  const editor = state.editor;
  if (!editor || state.busy) return;
  $("editorError").textContent = "";
  try {
    if (!state.online)
      throw new Error("后台未连接，请恢复连接后保存；当前输入会保留");
    if (
      !state.overview?.subscriptionEditorVersion ||
      (editor.sub && !editor.sub.revision)
    )
      throw new Error("后台版本较旧，请重启新版后台后再保存，避免丢失高级配置");
    const body = subscriptionEditorPayload(editor);
    // 保存期间冻结输入，避免请求发出后新增的编辑被误当成已保存。
    Object.values(editor.fields).forEach((input) => {
      input.disabled = true;
    });
    const result = await mutate(
      "保存订阅配置…",
      "/api/v1/subscriptions",
      editor.sub ? "PATCH" : "POST",
      body,
    );
    editor.dirty = false;
    closeSubscriptionEditor();
    message(
      result?.warning
        ? result.message
        : "订阅已保存；点击该订阅的更新按钮加载节点，不会自动启用系统代理",
      !!result?.warning,
    );
  } catch (error) {
    $("editorError").textContent = error.message;
    $("editorError").scrollIntoView?.({ block: "nearest" });
    $("editorError").focus();
  } finally {
    Object.values(editor.fields).forEach((input) => {
      input.disabled = false;
    });
    updateAvailability();
  }
}
function closeSubscriptionEditor() {
  const editor = state.editor;
  if (!editor) return;
  state.editor = null;
  $("subscriptionForm").replaceChildren();
  $("editorHeader").hidden = $("subscriptionEditor").hidden = true;
  $("mainHeader").hidden = $("mainTabs").hidden = false;
  $("panel-" + state.tab).hidden = false;
  $("content").scrollTop = editor.scroll;
  editor.focus?.focus();
}
function leaveSubscriptionEditor(next = () => {}) {
  if (state.busy) {
    message("正在保存，请等待完成");
    return;
  }
  const leave = () => {
    closeSubscriptionEditor();
    next();
  };
  if (state.editor?.dirty)
    confirm(
      "放弃未保存的修改？",
      "返回后当前草稿将被清除，已有订阅不会改变。",
      leave,
      "放弃修改",
      true,
    );
  else leave();
}
function profileGroups(profile) {
  const groups = (state.routing?.ruleGroups || []).filter((group) =>
    !(profile.groups || []).some((name) => name.toLowerCase() === group.name.toLowerCase()));
  if (!groups.length) {
    message("没有可关联的规则组；已关联的规则组可点击名称旁的 × 解除");
    return;
  }
  formDialog(
    "关联规则组",
    [{ name: "group", label: "选择规则组", options: groups.map((g) => g.name) }],
    (v) =>
      mutate("更新路由关联…", "/api/v1/routing/profiles", "PATCH", {
        profile: profile.name,
        ...v,
        attached: true,
      }),
    {
      copy: `当前关联：${profile.groups?.join("、") || "无"}。规则按关联顺序匹配；解除请点击配置卡片上的规则组标签。`,
    },
  );
}

function renameSubscriptionGroup(group) {
  formDialog("重命名订阅分组", [
    { name: "newName", label: "新名称", value: group.name, required: true },
  ], ({ newName }) => mutate("重命名分组…", "/api/v1/subscription-groups", "PATCH", {
    name: group.name, newName,
  }), { copy: "分组中的订阅会一起更新引用，不会删除订阅。" });
}
const routeRuleTypeOptions = [
  ["domain", "精确域名"], ["domain-suffix", "域名后缀"], ["domain-keyword", "域名关键词"],
  ["domain-wildcard", "域名通配符"], ["domain-regex", "域名正则"],
  ["ip-cidr", "IPv4 CIDR"], ["ip-cidr6", "IPv6 CIDR"],
  ["geoip", "GeoIP"], ["geosite", "GeoSite"],
  ["process-name", "进程名称"], ["process-path", "进程路径"], ["dst-port", "目标端口"],
];
function addRule(group) {
  formDialog(
    "添加路由规则",
    [
      {
        name: "type",
        label: "匹配方式",
        options: routeRuleTypeOptions,
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
function editRule(group, index, rule) {
  formDialog("修改路由规则", [
    { name: "type", label: "匹配方式", options: routeRuleTypeOptions, value: rule.type },
    { name: "value", label: "匹配值", value: rule.value, required: true },
    { name: "action", label: "匹配后动作", options: [["proxy", "走代理"], ["direct", "直连"], ["reject", "拒绝"]], value: rule.action },
  ], (value) => mutate("修改规则…", "/api/v1/routing/rules", "PATCH", {
    group, index, expected: rule, rule: value,
  }), { copy: "保存前会核对规则是否已被其他窗口修改；无效规则不会留在配置里。" });
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
    const actions = element("div", "rule-line-actions");
    actions.append(
      button("编辑", () => editRule(group.name, index + 1, rule), "text-button"),
    );
    if (index > 0) actions.append(button("↑", async () => {
      const applied = await perform("调整规则顺序…", () => api("/api/v1/routing/rules/move", "POST", {
        group: group.name, from: index + 1, to: index, expected: rule,
      }), { success: "规则已上移" });
      if (applied) closeDialog();
    }, "text-button"));
    if (index < rules.length - 1) actions.append(button("↓", async () => {
      const applied = await perform("调整规则顺序…", () => api("/api/v1/routing/rules/move", "POST", {
        group: group.name, from: index + 1, to: index + 2, expected: rule,
      }), { success: "规则已下移" });
      if (applied) closeDialog();
    }, "text-button"));
    actions.append(
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
                  "DELETE", { expected: rule },
                ),
              ),
            "删除",
            true,
          ),
        "text-button",
      ),
    );
    row.append(
      element(
        "span",
        "",
        `${index + 1}. ${rule.type} · ${rule.value} → ${rule.action}`,
      ),
      actions,
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
    { success: "联网检测已完成，请查看状态页" },
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
        state.changed.add("overview");
        state.changed.add("versions");
        const failures = await refreshChanged();
        message(
          failures.length ? "内核已安装；状态暂未刷新" : "内核安装完成",
          failures.length > 0,
        );
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
  $("editorBack").onclick = () => leaveSubscriptionEditor();
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && state.editor && !$("dialog").open) {
      event.preventDefault();
      leaveSubscriptionEditor();
    }
  });
  for (const section of ["Subscriptions", "Routing"])
    $("config" + section).onclick = () => {
      for (const name of ["Subscriptions", "Routing"]) {
        $("config" + name + "Body").hidden = name !== section;
        $("config" + name).setAttribute(
          "aria-pressed",
          String(name === section),
        );
      }
    };
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
        Object.keys(resources).forEach((key) => state.changed.add(key));
      },
      { success: "后台已连接" },
    );
  };
  $("refreshData").onclick = () =>
    perform(
      "刷新状态…",
      async () => {
        const failed = await refreshOverview();
        if (failed.length)
          throw new Error("状态读取失败，保留上次结果，请重试");
      },
      {
        refresh: false,
        success: "状态已刷新",
      },
    );
  $("refreshLogs").onclick = () => void loadLogs();
  $("doctor").onclick = () => void loadDoctor();
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
  $("restoreRouting").onclick = () =>
    confirm("恢复内置路由方案", "只补齐缺失的内置方案和规则组，不覆盖你现有的自定义规则。", () =>
      perform("恢复内置路由…", () => api("/api/v1/routing/restore", "POST", {})), "恢复");
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
  $("settingsForm").addEventListener("input", () => {
    state.dirty = true;
    renderSettings(true);
  });
  $("reloadSettings").onclick = () =>
    confirm(
      "重新读取设置",
      "放弃当前未保存的修改并读取最新设置？",
      async () => {
        try {
          if (await readResource("settings")) {
            state.dirty = false;
            renderSettings();
          }
        } catch (error) {
          message("读取失败，当前草稿已保留：" + error.message, true);
        }
        renderSyncErrors();
      },
      "重新读取",
    );
  $("retrySync").onclick = () => refreshResources([...resourceErrors.keys()]);
  $("retryTray").onclick = async () => {
    const err = await native().RetryTray();
    if (err) message(err, true);
    state.info = await native().Info();
    renderTray();
  };
  $("settingsForm").onsubmit = (event) => {
    event.preventDefault();
    if (!state.settings) return;
    const f = event.currentTarget.elements;
    const values = {
      revision: state.draft?.revision,
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
          api("/api/v1/settings", "PATCH", values).then((value) => {
            state.dirty = false;
            return value;
          }),
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
        if (result.status === 204)
          return { warning: true, message: "已取消导入" };
        affectedResources("/api/v1/core/import").forEach((key) =>
          state.changed.add(key),
        );
      },
      { success: "导入流程已完成" },
    );
  $("coreUse").onclick = () =>
    formDialog(
      "切换内核版本",
      [{ name: "reference", label: "版本", options: state.versions.filter((item) => !item.active).map((item) => [item.version, item.version]) }],
      ({ reference }) => mutate("切换内核版本…", "/api/v1/core/use", "POST", { reference }),
      { copy: "切换后若内核正在运行，将按后台规则重启。当前版本不可再次选择。", submitLabel: "切换" },
    );
  $("coreDelete").onclick = () =>
    formDialog(
      "删除本地内核版本",
      [{ name: "reference", label: "版本", options: state.versions.filter((item) => !item.active).map((item) => [item.version, item.version]) }],
      ({ reference }) => mutate("删除内核版本…", `/api/v1/core/installations?reference=${encodeURIComponent(reference)}`, "DELETE"),
      { copy: "仅删除所选非活动版本的本地文件，不影响当前运行的内核。", submitLabel: "删除版本" },
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
      "退出桌面（保留代理）",
      "退出窗口和托盘，后台与当前代理继续运行。可再次打开 Kivo，或使用 CLI / Web 管理。",
      async () => {
        const err = await native().Quit(false);
        if (err) message(err, true);
      },
      "退出桌面",
    );
  $("hideWindow").onclick = async () => {
    const err = await native().HideWindow();
    if (err) message(err, true);
  };
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
  window.runtime?.EventsOn("desktop:message", (text) => message(text, true));
  window.runtime?.EventsOn("desktop:refresh", () => {
    if (state.busy || state.refreshing) return;
    state.refreshing = true;
    // 恢复窗口仅刷新状态；不要用后台快照覆盖用户尚未保存的输入。
    refreshResources(["overview", "nodes"])
      .catch((error) => message(error.message, true))
      .finally(() => {
        state.refreshing = false;
      });
  });
  window.runtime?.EventsOn("desktop:page", (page) => {
    if (["home", "config", "data", "settings"].includes(page)) setTab(page);
  });
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
      renderTray();
      if (state.info.ready) {
        if (state.info.smokeTest && state.info.trayStarting) {
          await new Promise((resolve) => setTimeout(resolve, 100));
          continue;
        }
        await refreshAll();
        if (!state.online) throw new Error("后台状态暂不可用");
        if (state.info.smokeTest) {
          await document.fonts.ready;
          native().NativeReady(
            JSON.stringify({
              ready: true,
              version: state.info.version,
              bridge: true,
              tray: state.info.trayReady,
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
// 可见窗口每 5 秒刷新状态；事务期间仍读取概览，显示真实连接阶段。
setInterval(async () => {
  if (document.hidden || state.refreshing) return;
  state.refreshing = true;
  try {
    state.info = await native().Info();
    renderTray();
    // 托盘与后台独立：后台启动失败也要继续显示托盘恢复结果。
    if (!state.info.ready) {
      state.online = false;
      renderOverview();
      renderData();
      updateAvailability();
      return;
    }
    await refreshResources(state.busy ? ["overview"] : ["overview", "nodes"]);
    if (state.tab === "data" && !state.doctorLoaded) void loadDoctor();
  } catch (error) {
    message(error.message, true);
  } finally {
    state.refreshing = false;
  }
}, 5000);
setInterval(() => {
  if (!document.hidden && state.tab === "data") void loadLogs();
}, 1000);
document.addEventListener("visibilitychange", () => {
  if (document.hidden || state.tab !== "data") return;
  void loadLogs();
  if (!state.doctorLoaded) void loadDoctor();
});
bootstrap().catch((error) => message(error.message, true));
