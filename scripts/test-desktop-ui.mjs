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
  assert.equal(h.nodes.get("dialog").open, true);
  assert.equal(h.nodes.get("dialogTitle").textContent, "添加订阅");
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
    'state.groups=[{name:"default"}];refreshChanged=async()=>[];renderNodes=()=>{};renderOverview=()=>{};editSubscription({name:"A",url:"https://masked.invalid?token=***",group:"default",authType:"aes",updateVia:"direct"})',
  );
  const form = h.nodes.get("dialogBody").children[0];
  await form.events.get("submit")({ preventDefault() {} });
  assert.equal(input.reference, "A");
  assert.equal(input.url, undefined);
  assert.equal(input.secret, undefined);
  assert.equal(input.authType, undefined);
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

test("操作返回恢复警告时不能改写为成功", async () => {
  const h = harness();
  await h.run(
    'perform("断开",async()=>({warning:true,message:"外部代理已改变，请检查备份"}),{refresh:false,success:"已断开"})',
  );
  assert.match(h.nodes.get("toast").textContent, /外部代理已改变/);
  assert.equal(h.nodes.get("toast").classList.contains("error"), true);
});
