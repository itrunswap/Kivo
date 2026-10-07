import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import {
  fresh,
  connectionView,
  checkTime,
  homeConnectionTitle,
  groupNodes,
  nodeLatency,
  selectedNode,
  updateSummary,
} from "../desktop/frontend/model.mjs";

const now = Date.now(),
  report = {
    checkedAt: new Date(now).toISOString(),
    stale: false,
    routes: [
      { id: "entry", state: "ok" },
      { id: "node", state: "ok" },
    ],
  };
const overview = {
  core: { state: "running", currentNode: "Hong Kong", mode: "rule" },
  proxyPortListening: true,
  systemProxy: { state: "this_app", supported: true },
  connectivity: report,
};
test("只有入口接入、端口监听且入口及节点检测有效才显示绿色", () => {
  assert.equal(connectionView(overview, true, now).tone, "good");
  assert.equal(
    connectionView({ ...overview, connectivity: null }, true, now).tone,
    "active",
  );
  assert.equal(
    connectionView({ ...overview, systemProxy: { state: "off" } }, true, now)
      .title,
    "仅内核运行",
  );
});
test("入口通过但节点未通过不显示绿色", () => {
  const result = connectionView(
    {
      ...overview,
      connectivity: {
        ...report,
        routes: [
          { id: "entry", state: "ok" },
          { id: "node", state: "failed" },
        ],
      },
    },
    true,
    now,
  );
  assert.equal(result.tone, "warn");
  assert.match(result.title, /节点待确认/);
});

test("后台断开禁止误显示已连接", () => {
  assert.equal(connectionView(overview, false).on, false);
  assert.equal(connectionView(overview, false).tone, "error");
});
test("异常代理入口醒目显示，不掩盖潜在断网", () => {
  const result = connectionView({ ...overview, proxyPortListening: false });
  assert.equal(result.on, false);
  assert.equal(result.title, "代理入口异常");
});
test("过期、未来、标记过期的检测不是联网证据", () => {
  assert.equal(fresh(report, now), true);
  assert.equal(fresh(report, now + 120001), false);
  assert.equal(fresh({ ...report, stale: true }, now), false);
  assert.equal(
    fresh({ ...report, checkedAt: new Date(now + 20000).toISOString() }, now),
    false,
  );
});

test("首页连接标题直接展示节点，检测只显示时间且不复用过期成功状态", () => {
  const checked = new Date(2026, 9, 7, 10, 9);
  const at = new Date(2026, 9, 7, 10, 12);
  const old = { ...report, checkedAt: checked.toISOString() };
  const active = { ...overview, connectivity: old };
  assert.equal(fresh(old, at.getTime()), false);
  assert.equal(
    homeConnectionTitle(connectionView(active, true, at.getTime()), active),
    "Hong Kong",
  );
  assert.equal(checkTime(old, at.getTime()), "10:09");
  assert.equal(checkTime(null, at.getTime()), "尚未检测");
  const off = { ...active, systemProxy: { state: "off", supported: true } };
  assert.equal(
    homeConnectionTitle(connectionView(off, true, at.getTime()), off),
    "未连接",
  );
  const direct = { ...active, core: { ...active.core, mode: "direct" } };
  assert.equal(
    homeConnectionTitle(connectionView(direct, true, at.getTime()), direct),
    "直连模式",
  );
});
test("节点离线快照与未测速不显示绿色", () => {
  assert.equal(
    nodeLatency({ cached: true, delay: 45, tested: true, alive: true }, true)
      .tone,
    "",
  );
  assert.equal(
    nodeLatency({ tested: false, delay: 45, alive: true }, true).text,
    "未测速",
  );
  assert.equal(
    nodeLatency({ tested: true, delay: 45, alive: true }, true).tone,
    "good",
  );
  assert.equal(nodeLatency({ tested: true, alive: false }, true).text, "超时");
});
test("按订阅组织节点，保留空和禁用订阅", () => {
  const groups = groupNodes(
    [{ name: "HK", providerName: "A" }],
    [
      { name: "A", enabled: true },
      { name: "B", enabled: false },
    ],
  );
  assert.equal(groups.length, 2);
  assert.equal(groups[1].nodes.length, 0);
  assert.equal(
    groupNodes([{ name: "HK", providerName: "A" }], [], "hk").length,
    1,
  );
});
test("选择节点与代理连接分别展示", () => {
  assert.equal(
    selectedNode(
      { ...overview, core: { state: "stopped", currentNode: "Hong Kong" } },
      { name: "Hong Kong" },
    ),
    true,
  );
});
test("批量更新部分失败保留已解析节点数与路径", () => {
  const text = updateSummary({
    results: [
      { name: "A", nodeCount: 4, via: "direct" },
      { name: "B", error: "failed", via: "proxy" },
      { name: "C", via: "direct" },
    ],
  });
  assert.match(text, /已解析 4 个节点/);
  assert.match(text, /失败 · failed/);
  assert.match(text, /数量未知/);
  assert.match(text, /（代理）/);
});
test("四标签语义、离线字体、无第三方脚本", () => {
  const html = fs.readFileSync(
      new URL("../desktop/frontend/index.html", import.meta.url),
      "utf8",
    ),
    css = fs.readFileSync(
      new URL("../desktop/frontend/app.css", import.meta.url),
      "utf8",
    ),
    js = fs.readFileSync(
      new URL("../desktop/frontend/app.js", import.meta.url),
      "utf8",
    );
  assert.equal((html.match(/role="tab"/g) || []).length, 4);
  assert.match(html, /role="switch"/);
  assert.match(css, /noto-sans-sc-ui.woff2/);
  assert.match(css, /prefers-reduced-motion/);
  assert.doesNotMatch(html, /(?:src|href)="https?:\/\//);
  assert.doesNotMatch(js, /innerHTML|outerHTML|fetch\(/);
  assert.doesNotMatch(js, /secret\s*:\s*state\.info/);
});
