// 原生页面 DOM 回归测试：不联网、不操作系统代理，不需要安装浏览器驱动。
import { readFileSync } from "node:fs";
import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";
import * as model from "../desktop/frontend/model.mjs";

const source = readFileSync(
  new URL("../desktop/frontend/app.js", import.meta.url),
  "utf8",
).replace(/^import[\s\S]*?from "\.\/model\.mjs";/, "");
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
        this.children.push(...children);
      },
      replaceChildren(...children) {
        this.children = children;
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
    "refreshAll=async()=>{};renderNodes=()=>{};renderOverview=()=>{};updateDialog()",
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
  const h = harness(async (path, method, body) => {
    input = JSON.parse(body);
    return { status: 200, data: { updated: true } };
  });
  h.run(
    'state.groups=[{name:"default"}];refreshAll=async()=>{};renderNodes=()=>{};renderOverview=()=>{};editSubscription({name:"A",url:"https://masked.invalid?token=***",group:"default",authType:"aes",updateVia:"direct"})',
  );
  const form = h.nodes.get("dialogBody").children[0];
  await form.events.get("submit")({ preventDefault() {} });
  assert.equal(input.reference, "A");
  assert.equal(input.url, undefined);
  assert.equal(input.secret, undefined);
  assert.equal(input.authType, undefined);
});

test("事务结束重新派生节点禁用条件", () => {
  const h = harness();
  h.run(
    "globalThis.renders=0;renderNodes=()=>renders++;renderOverview=()=>{};state.busy=true;endJob()",
  );
  assert.equal(h.run("state.busy"), false);
  assert.equal(h.run("renders"), 1);
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
