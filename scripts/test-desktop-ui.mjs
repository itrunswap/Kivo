// 原生页面 DOM 回归测试：不联网、不操作系统代理，不需要安装浏览器驱动。
import { readFileSync } from "node:fs";
import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";
import * as model from "../desktop/frontend/model.mjs";
import * as sync from "../desktop/frontend/sync.mjs";

const source = readFileSync(
  new URL("../desktop/frontend/app.js", import.meta.url),
  "utf8",
).replace(/^import[\s\S]*?from "\.\/(model|sync)\.mjs";/gm, "");
const functions = source.slice(0, source.indexOf("// 可见窗口每 5 秒刷新状态"));

function harness(handler = async () => ({ data: {}, status: 200 })) {
  const nodes = new Map();
  function make(tag = "div") {
    const classes = new Set(),
      events = new Map();
    const item = {
      tagName: tag.toUpperCase(),
      textContent: "",
      className: "",
      children: [],
      dataset: {},
      value: "",
      disabled: false,
      hidden: false,
      open: false,
      classList: {
        add(...names) {
          names.forEach((x) => classes.add(x));
        },
        toggle(name, force) {
          const add = force ?? !classes.has(name);
          if (add) classes.add(name);
          else classes.delete(name);
        },
        contains(name) {
          return classes.has(name);
        },
      },
      append(...children) {
        children.forEach((child) => this.insertBefore(child, null));
      },
      replaceChildren(...children) {
        this.children.forEach((child) => {
          child.parentNode = null;
        });
        this.children = [];
        this.append(...children);
      },
      insertBefore(child, reference) {
        child.remove?.();
        const index = reference
          ? this.children.indexOf(reference)
          : this.children.length;
        this.children.splice(index, 0, child);
        Object.defineProperty(child, "parentNode", {
          value: this,
          writable: true,
          configurable: true,
          enumerable: false,
        });
      },
      remove() {
        if (this.parentNode) {
          const siblings = this.parentNode.children;
          siblings.splice(siblings.indexOf(this), 1);
          this.parentNode = null;
        }
      },
      setAttribute(name, value) {
        this[name] = value;
      },
      removeAttribute(name) {
        delete this[name];
      },
      addEventListener(name, fn) {
        events.set(name, fn);
      },
      events,
      showModal() {
        this.open = true;
      },
      close() {
        this.open = false;
      },
      focus() {
        document.activeElement = this;
      },
      querySelector() {
        return null;
      },
      querySelectorAll() {
        return [];
      },
      elements: {
        namedItem(name) {
          return find(this.form, name);
        },
      },
    };
    item.elements.form = item;
    if (tag === "select")
      Object.defineProperty(item, "type", { get: () => "select-one" });
    else item.type = "text";
    return item;
  }
  function find(item, name) {
    if (item.name === name) return item;
    for (const child of item.children || []) {
      const hit = find(child, name);
      if (hit) return hit;
    }
    return null;
  }
  const document = {
    activeElement: null,
    createElement: make,
    createElementNS: (_, tag) => make(tag),
    createTextNode: (text) => ({ textContent: text }),
    getElementById(id) {
      if (!nodes.has(id))
        nodes.set(id, make(id === "dialog" ? "dialog" : "div"));
      return nodes.get(id);
    },
    querySelectorAll(selector) {
      if (selector === "[data-mutation]")
        return [...nodes.values()].filter((item) => item.dataset.mutation);
      return [];
    },
  };
  const context = vm.createContext({
    document,
    window: { go: { main: { Desktop: { Request: handler } } } },
    ...model,
    ...sync,
    setTimeout: () => 0,
    clearTimeout() {},
    matchMedia: () => ({ matches: false }),
    localStorage: { setItem() {} },
  });
  vm.runInContext(functions, context);
  return { nodes, run: (code) => vm.runInContext(code, context) };
}

test("原生 select 的 type 属性只读，所有下拉表单仍可初始化", () => {
  const h = harness();
  h.run(
    'globalThis.form=document.createElement("form");field(form,{name:"mode",label:"模式",options:[["rule","规则"],["global","全局"]],value:"rule"})',
  );
  assert.equal(h.run('form.elements.namedItem("mode").value'), "rule");
  h.run('state.groups=[{name:"default"}];addSubscription()');
  assert.equal(h.nodes.get("subscriptionEditor").hidden, false);
  assert.equal(h.nodes.get("editorTitle").textContent, "添加订阅");
  assert.equal(h.nodes.get("mainTabs").hidden, true);
});

test("订阅更新结果弹窗保留部分成功数量，不被输入弹窗再次关闭", async () => {
  const h = harness(async () => ({
    status: 502,
    data: {
      results: [
        { name: "A", nodeCount: 4, via: "direct" },
        { name: "B", error: "timeout", via: "direct" },
      ],
      temporarilyStartedCore: true,
    },
    error: "B 更新失败",
  }));
  h.run(
    "refreshChanged=async()=>[];renderNodes=()=>{};renderOverview=()=>{};updateDialog()",
  );
  const form = h.nodes.get("dialogBody").children[0];
  await form.events.get("submit")({ preventDefault() {} });
  assert.equal(h.nodes.get("dialog").open, true);
  assert.equal(h.nodes.get("dialogTitle").textContent, "更新订阅结果");
  assert.match(
    h.nodes.get("dialogBody").children[0].textContent,
    /已解析 4 个节点/,
  );
  assert.match(
    h.nodes.get("dialogBody").children[0].textContent,
    /失败 · timeout/,
  );
});

test("编辑不把脱敏地址或空密码覆盖真实配置", async () => {
  let input;
  const h = harness(async (method, path, body) => {
    input = JSON.parse(body);
    return { status: 200, data: { updated: true } };
  });
  h.run(
    'state.online=true;state.overview={subscriptionEditorVersion:1};state.groups=[{name:"default"}];refreshChanged=async()=>[];renderNodes=()=>{};renderOverview=()=>{};editSubscription({name:"A",revision:"r1",url:"https://masked.invalid?token=***",group:"default",authType:"aes",updateVia:"direct"})',
  );
  const form = h.nodes.get("subscriptionForm");
  await form.onsubmit({ preventDefault() {} });
  assert.equal(input.reference, "A");
  assert.equal(input.url, undefined);
  assert.equal(input.secret, undefined);
  assert.equal(input.authType, undefined);
  assert.equal(input.decryption.type, "aes");
  assert.equal(input.decryption.secret, undefined);
  assert.equal(input.downloadAuth.type, "none");
  assert.equal(input.revision, "r1");
});

test("独立编辑页保存失败保留草稿，返回需确认，放弃后恢复滚动位置", async () => {
  const h = harness(async () => ({ status: 400, error: "测试保存失败" }));
  h.run(
    'state.online=true;state.overview={subscriptionEditorVersion:1};$("content").scrollTop=310;addSubscription();state.editor.fields.url.value="https://example.com/sub";state.editor.fields.decryptSecret.value=" demo password ";state.editor.fields.decryptionType.value="aes";$("subscriptionForm").oninput()',
  );
  await h.nodes.get("subscriptionForm").onsubmit({ preventDefault() {} });
  assert.equal(
    h.run("state.editor.fields.decryptSecret.value"),
    " demo password ",
  );
  assert.equal(h.nodes.get("editorError").textContent, "测试保存失败");
  h.run("leaveSubscriptionEditor()");
  assert.equal(h.nodes.get("dialog").open, true);
  assert.ok(h.run("state.editor"));
  const actions = h.nodes.get("dialogBody").children[1];
  await actions.children[1].events.get("click")();
  assert.equal(h.run("state.editor"), null);
  assert.equal(h.nodes.get("content").scrollTop, 310);
  assert.equal(h.nodes.get("subscriptionForm").children.length, 0);
});

test("下载认证与解密分别提交，密码空格不被截断，切换认证必须补全凭据", () => {
  const h = harness();
  h.run(
    'addSubscription();state.editor.fields.url.value="https://example.com";state.editor.fields.downloadAuthType.value="basic";state.editor.fields.username.value="alice";state.editor.fields.authSecret.value=" http secret ";state.editor.fields.decryptionType.value="aes";state.editor.fields.decryptSecret.value=" aes secret "',
  );
  const body = h.run("subscriptionEditorPayload(state.editor)");
  assert.equal(body.downloadAuth.secret, " http secret ");
  assert.equal(body.decryption.secret, " aes secret ");
  h.run('state.editor.fields.authSecret.value=""');
  assert.throws(
    () => h.run("subscriptionEditorPayload(state.editor)"),
    /完整的下载认证/,
  );
});

test("旧后台不能悄悄忽略新版订阅字段", async () => {
  let calls = 0;
  const h = harness(async () => {
    calls++;
    return { status: 200 };
  });
  h.run(
    'state.online=true;state.overview={};addSubscription();state.editor.fields.url.value="https://example.com"',
  );
  await h.nodes.get("subscriptionForm").onsubmit({ preventDefault() {} });
  assert.equal(calls, 0);
  assert.match(h.nodes.get("editorError").textContent, /后台版本较旧/);
});

test("单订阅更新就地显示节点数和真实路径，不重建编辑草稿", async () => {
  const h = harness(async () => ({
    status: 200,
    data: {
      results: [{ name: "A", nodeCount: 8, via: "proxy" }],
      temporarilyStartedCore: true,
    },
  }));
  h.run("refreshChanged=async()=>[];renderNodes=()=>{};renderOverview=()=>{}");
  await h.run('updateOneSubscription("A")');
  assert.match(
    h.run('state.subscriptionResults.get("A").text'),
    /代理更新完成.*8 个节点.*恢复停止/,
  );
  assert.equal(h.run("state.busy"), false);
});

test("事务结束解除忙碌状态但不重建节点列表", () => {
  const h = harness();
  h.run(
    "globalThis.renders=0;renderNodes=()=>renders++;renderOverview=()=>{};state.busy=true;endJob()",
  );
  assert.equal(h.run("state.busy"), false);
  assert.equal(h.run("renders"), 0);
});

test("过期节点详情不会把旧延迟误标为实时结果", () => {
  const h = harness();
  h.run(
    'state.online=true;state.nodesFresh=false;state.overview={core:{state:"running"}};nodeDetail({name:"A",type:"ss",delay:42,cached:false})',
  );
  const body = h.nodes.get("dialogBody");
  const text = JSON.stringify(body.children, (key, value) =>
    key === "form" ? undefined : value,
  );
  assert.match(text, /离线缓存（待刷新）/);
  assert.match(text, /42 ms · 缓存/);
});

test("任意业务错误不会转成成功消息，原生请求始终只发送 JSON", async () => {
  const h = harness(async () => ({
    status: 500,
    error: "failed",
    data: { results: [{ nodeCount: 2 }] },
  }));
  await assert.rejects(
    h.run('api("/api/v1/overview")'),
    (err) => err.message === "failed" && err.data.results[0].nodeCount === 2,
  );
});

test("托盘未初始化或丢失时禁止隐藏，状态文案不能承诺仍可驻留", () => {
  const h = harness();
  h.run("state.info={trayStarting:true};renderTray()");
  assert.equal(h.nodes.get("hideWindow").disabled, true);
  assert.match(h.nodes.get("trayStatus").textContent, /正在初始化/);
  h.run("state.info={trayReady:true};renderTray()");
  assert.equal(h.nodes.get("hideWindow").disabled, false);
  h.run(
    'state.info={trayReady:false,trayError:"托盘宿主不可用，主窗口已恢复"};renderTray()',
  );
  assert.equal(h.nodes.get("hideWindow").disabled, true);
  assert.match(h.nodes.get("trayStatus").textContent, /主窗口已恢复/);
  h.run("state.info={trayReady:true};state.busy=true;renderTray()");
  assert.equal(h.nodes.get("hideWindow").disabled, true);
});

test("恢复窗口刷新状态不会覆盖尚未保存的设置表单", () => {
  const h = harness();
  h.run(
    "state.settings={mixedPort:17890};state.versions=[];renderSettings(true)",
  );
  // 空表单没有控件：若恢复流程错误地重填表单，此处会抛异常。
  assert.equal(h.nodes.get("settingsForm"), undefined);
  assert.match(h.nodes.get("installedCore").textContent, /尚未安装/);
});

test("连接只刷新状态与节点，局部读取失败不会推翻操作成功", async () => {
  const calls = [];
  const h = harness(async (method, path) => {
    calls.push([method, path]);
    if (method === "POST") return { status: 200, data: {} };
    if (path.endsWith("/nodes")) return { status: 503, error: "节点暂不可读" };
    return {
      status: 200,
      data: {
        core: { state: "running" },
        systemProxy: { state: "this_app", supported: true },
        proxyPortListening: true,
      },
    };
  });
  const result = await h.run(
    'perform("连接",()=>api("/api/v1/connection/connect","POST",{}),{success:"已连接"})',
  );
  assert.equal(result, true);
  assert.deepEqual(calls, [
    ["POST", "/api/v1/connection/connect"],
    ["GET", "/api/v1/overview"],
    ["GET", "/api/v1/nodes"],
  ]);
  assert.equal(h.run("state.online"), true);
  assert.equal(h.run("state.nodesFresh"), false);
  assert.equal(h.nodes.get("syncNotice").hidden, false);
  assert.match(h.nodes.get("toast").textContent, /已连接.*部分状态暂未刷新/);
});

test("晚返回的旧状态不能覆盖操作后的新状态", async () => {
  const replies = [];
  const h = harness(() => new Promise((resolve) => replies.push(resolve)));
  const old = h.run('readResource("overview")');
  h.run("gate.invalidate()");
  const latest = h.run('readResource("overview")');
  replies[1]({ status: 200, data: { marker: "latest" } });
  await latest;
  replies[0]({ status: 200, data: { marker: "obsolete" } });
  assert.equal(await old, false);
  assert.equal(h.run("state.overview.marker"), "latest");
});

test("节点局部更新保留 DOM、焦点、展开和业务禁用条件", () => {
  const h = harness();
  h.run(
    'state.online=true;state.nodesFresh=true;state.overview={core:{state:"running"},systemProxy:{supported:true}};state.nodes=[{name:"A",providerName:"S",type:"ss",tested:true,alive:true,delay:60}];state.subs=[{name:"S",enabled:true}];renderNodes();globalThis.row=[...state.nodeRows.values()][0];row.choose.focus();state.busy=true;updateAvailability()',
  );
  assert.equal(h.run("row.choose.disabled"), true);
  h.run("state.nodes[0].delay=42;renderNodes();endJob()");
  assert.equal(h.run("row === [...state.nodeRows.values()][0]"), true);
  assert.equal(h.run("document.activeElement===row.choose"), true);
  assert.equal(h.run("row.delay.textContent"), "42 ms");
  assert.equal(h.run("row.choose.disabled"), false);
  assert.equal(h.run("[...state.nodeGroups.values()][0].root.open"), true);
  h.run("state.nodesFresh=false;updateAvailability()");
  assert.equal(h.run("row.choose.disabled"), true);
});

test("设置刷新保留未保存草稿及其原版本", async () => {
  const h = harness(async () => ({
    status: 200,
    data: { revision: "new", mixedPort: 4567 },
  }));
  h.run(
    'field($("settingsForm"),{name:"mixedPort",label:"端口",value:9999});state.settings={revision:"old",mixedPort:1234};renderSettings();state.dirty=true;$("settingsForm").elements.namedItem("mixedPort").value="9999"',
  );
  await h.run('refreshResources(["settings"])');
  assert.equal(
    h.run('$("settingsForm").elements.namedItem("mixedPort").value'),
    "9999",
  );
  assert.equal(h.run("state.draft.revision"), "old");
  assert.match(h.nodes.get("settingsDraft").textContent, /当前草稿已保留/);
});

test("设置无修改时不显示同步提示，保存区位于表单尾部", () => {
  const h = harness();
  h.run('state.settings={revision:"current",mixedPort:17890};state.versions=[];renderSettings(true)');
  assert.equal(h.nodes.get("settingsDraft").textContent, "");
  assert.equal(h.nodes.get("settingsDraft").hidden, true);
  h.run("state.dirty=true;renderSettings(true)");
  assert.equal(h.nodes.get("settingsDraft").hidden, false);
  const html = readFileSync(new URL("../desktop/frontend/index.html", import.meta.url), "utf8");
  const css = readFileSync(new URL("../desktop/frontend/app.css", import.meta.url), "utf8");
  assert.match(html, /class="settings-form-footer"[\s\S]*id="settingsDraft"[\s\S]*保存代理设置/);
  assert.match(css, /scrollbar-gutter:\s*stable both-edges/);
  assert.match(css, /\.connection-main\s*\{[^}]*height:\s*66px/);
  assert.match(css, /\.connection-card > \.setting-row\s*\{[^}]*height:\s*66px/);
});

test("状态已启用时隐藏重复的泛化检测提示", () => {
  const h = harness();
  h.run('state.online=true;state.overview={display:{title:"已启用 · 未检测",detail:"系统代理已接入",on:true,tone:"active"},connection:{detail:"内核运行不等于外网可用；请执行一次联网检测。"}};state.dataKey=JSON.stringify([state.overview,state.online,fresh(state.overview?.connectivity)]);renderData()');
  assert.equal(h.nodes.get("statusExplanation").textContent, "");
  assert.equal(h.nodes.get("statusExplanation").hidden, true);
});

test("当前内核版本合并进状态行，非活动版本才允许切换和删除", () => {
  const h = harness();
  h.run('state.overview={core:{state:"running",version:"v1"},systemProxy:{}};state.versions=[{version:"v1",active:true},{version:"v2",active:false}];renderSettings()');
  assert.equal(h.nodes.get("coreRunState").textContent, "运行中");
  assert.equal(h.nodes.get("installedCore").textContent, "v1");
  assert.equal(h.nodes.get("activeCoreBadge").hidden, false);
  assert.equal(h.nodes.get("coreStart").hidden, true);
  assert.equal(h.nodes.get("coreStop").hidden, false);
  assert.equal(h.nodes.get("coreUse").dataset.blocked, "false");
  assert.equal(h.nodes.get("coreDelete").dataset.blocked, "false");
  h.run('state.versions=[{version:"v1",active:true}];renderSettings()');
  assert.equal(h.nodes.get("coreUse").dataset.blocked, "true");
  assert.equal(h.nodes.get("coreDelete").dataset.blocked, "true");
  h.run('state.overview.core.version="v3";renderSettings()');
  assert.equal(h.nodes.get("activeCoreBadge").hidden, true);
});

test("日志读取成功后不显示刷新频率文案", async () => {
  const h = harness(async () => ({ status: 200, data: ["[INFO] ready"] }));
  h.run('state.online=true;state.tab="data"');
  await h.run('loadLogs()');
  assert.equal(h.nodes.get("logs").textContent, "[INFO] ready");
  assert.equal(h.nodes.get("logStatus").textContent, "");
});

test("旧后台不支持局部保存时禁止发送危险 PATCH", async () => {
  let called = false;
  const h = harness(async () => {
    called = true;
    return { status: 200, data: {} };
  });
  await assert.rejects(
    h.run('api("/api/v1/settings","PATCH",{mode:"global"})'),
    /后台版本过旧/,
  );
  assert.equal(called, false);
});

test("重复点击当前标签不跳回页面顶部", () => {
  const h = harness();
  h.run('$("content").scrollTop=237;setTab("home")');
  assert.equal(h.nodes.get("content").scrollTop, 237);
});

test("进入状态页自动诊断和读取日志，离开后不继续请求", async () => {
  const calls = [];
  const h = harness(async (_method, path) => {
    calls.push(path);
    if (path.endsWith("/doctor"))
      return {
        status: 200,
        data: [{ name: "代理端口", status: "ok", message: "可用" }],
      };
    if (path.includes("/logs")) return { status: 200, data: ["[INFO] 已启动"] };
    return { status: 200, data: {} };
  });
  h.run('state.online=true;setTab("data")');
  await new Promise((resolve) => setImmediate(resolve));
  assert.ok(calls.some((path) => path.endsWith("/doctor")));
  assert.ok(calls.some((path) => path.includes("/logs")));
  assert.equal(h.nodes.get("logs").textContent, "[INFO] 已启动");
  assert.equal(h.run("state.doctorLoaded"), true);
  h.run('setTab("home")');
  const count = calls.length;
  await h.run("loadLogs()");
  assert.equal(calls.length, count);
});

test("离开状态页后旧日志响应不能覆盖当前页面", async () => {
  let finish;
  const h = harness(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const pending = h.run(
    '$("logs").textContent="旧内容";state.online=true;state.tab="data";loadLogs()',
  );
  h.run('state.tab="home";state.dataEpoch++');
  finish({ status: 200, data: ["过期日志"] });
  await pending;
  assert.equal(h.nodes.get("logs").textContent, "旧内容");
});

test("操作返回恢复警告时不能改写为成功", async () => {
  const h = harness();
  await h.run(
    'perform("断开",async()=>({warning:true,message:"外部代理已改变，请检查备份"}),{refresh:false,success:"已断开"})',
  );
  assert.match(h.nodes.get("toast").textContent, /外部代理已改变/);
  assert.equal(h.nodes.get("toast").classList.contains("error"), true);
});
