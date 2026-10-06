// 仅开发验收使用：所有操作在内存中模拟，不读取真实配置、不启动 Mihomo、不改系统代理。
import http from "node:http";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
  port = Number(process.argv[2] || 19435);
if (!Number.isInteger(port) || port < 1024 || port > 65535)
  throw new Error("预览端口无效");
const nodes = [
  {
    name: "香港 · 优化 01",
    type: "Trojan",
    providerName: "日常订阅",
    alive: true,
    tested: true,
    delay: 48,
    udp: true,
  },
  {
    name: "香港 · 优化 02",
    type: "Trojan",
    providerName: "日常订阅",
    alive: true,
    tested: true,
    delay: 62,
    udp: true,
  },
  {
    name: "日本 · 东京 01",
    type: "Shadowsocks",
    providerName: "日常订阅",
    alive: true,
    tested: true,
    delay: 89,
    udp: true,
  },
  {
    name: "新加坡 · 01",
    type: "VLESS",
    providerName: "日常订阅",
    alive: true,
    tested: true,
    delay: 107,
    udp: true,
  },
  {
    name: "美国 · 洛杉矶 01",
    type: "Trojan",
    providerName: "备用订阅",
    alive: true,
    tested: true,
    delay: 183,
    udp: true,
  },
];
let running = true,
  connected = false,
  report = null;
let settings = {
  revision: "preview-1",
  listen: "127.0.0.1:19435",
  mixedPort: 17890,
  mode: "rule",
  allowLAN: false,
  tunEnabled: false,
  downloadProxy: "",
  downloadRetry: 4,
};
let current = nodes[0].name;
let groups = [
  { name: "日常", enabled: true },
  { name: "备用", enabled: true },
];
let subs = [
  {
    index: 1,
    name: "日常订阅",
    group: "日常",
    enabled: true,
    authType: "none",
    updateVia: "direct",
    url: "https://example.com/sub?token=***",
  },
  {
    index: 2,
    name: "备用订阅",
    group: "备用",
    enabled: true,
    authType: "none",
    updateVia: "proxy",
    url: "https://example.com/backup?token=***",
  },
];
const routing = {
  activeProfile: "rule",
  profiles: [
    { name: "rule", defaultAction: "proxy", groups: [] },
    { name: "bypass-cn", defaultAction: "proxy", groups: ["中国大陆直连"] },
    { name: "proxy-only", defaultAction: "direct", groups: ["指定地址代理"] },
  ],
  ruleGroups: [
    {
      name: "中国大陆直连",
      rules: [{ type: "geoip", value: "cn", action: "direct" }],
    },
    { name: "指定地址代理", rules: [] },
  ],
};
function snapshot() {
  return {
    core: {
      name: "Mihomo",
      state: running ? "running" : "stopped",
      version: "v1.19.31",
      mixedPort: settings.mixedPort,
      mode: settings.mode,
      currentNode: current,
      effectiveNode: current,
      pid: running ? 1234 : 0,
    },
    systemProxy: {
      state: connected ? "this_app" : "off",
      supported: true,
      managed: connected,
      recoveryPending: false,
    },
    proxyPortListening: running,
    tunEnabled: settings.tunEnabled,
    subscriptionCount: subs.length,
    enabledCount: subs.filter((s) => s.enabled).length,
    connectivity: report,
  };
}
function api(method, uri, input) {
  const url = new URL(uri, "http://localhost"),
    p = url.pathname;
  if (p === "/api/v1/overview") return snapshot();
  if (p === "/api/v1/nodes")
    return nodes.map((n) => ({ ...n, cached: !running }));
  if (p === "/api/v1/nodes/select") {
    current = input.name;
    report = null;
    return {};
  }
  if (p === "/api/v1/nodes/test")
    return Object.fromEntries(nodes.map((n) => [n.name, n.delay]));
  if (p === "/api/v1/connection/connect") {
    running = true;
    connected = true;
    return { message: "演示已连接" };
  }
  if (p === "/api/v1/connection/disconnect") {
    running = false;
    connected = false;
    return { message: "演示已断开", warning: false };
  }
  if (p === "/api/v1/connectivity/check") {
    report = {
      checkedAt: new Date().toISOString(),
      stale: false,
      node: current,
      mode: settings.mode,
      routes: ["direct", "entry", "node"].map((id, i) => ({
        id,
        label: ["本机直连", "日常代理入口", "选中节点出口"][i],
        state: running || id === "direct" ? "ok" : "skipped",
        message: "仅模拟检测结果，不访问真实网络",
        probes: [
          {
            target: "演示目标",
            state: running || id === "direct" ? "ok" : "skipped",
            durationMs: 48,
            message: "模拟响应",
          },
        ],
      })),
    };
    return report;
  }
  if (p === "/api/v1/subscriptions") {
    if (method === "POST")
      subs.push({
        ...input,
        index: subs.length + 1,
        enabled: true,
        url: "https://example.com/redacted",
      });
    if (method === "PATCH") {
      const item = subs.find((s) => s.name === input.reference);
      if (!item) throw new Error("订阅不存在");
      Object.assign(item, input);
    }
    if (method === "DELETE")
      subs = subs.filter((s) => s.name !== url.searchParams.get("name"));
    return subs;
  }
  if (
    p === "/api/v1/subscriptions/update" ||
    p === "/api/v1/subscriptions/test"
  )
    return {
      temporarilyStartedCore: !running,
      results: subs
        .filter(
          (s) =>
            s.enabled &&
            (!input.group || s.group === input.group) &&
            (!input.reference ||
              input.reference === "all" ||
              s.name === input.reference),
        )
        .map((s) => ({
          name: s.name,
          nodeCount: nodes.filter((n) => n.providerName === s.name).length,
          via: input.via || s.updateVia,
          durationMs: 200,
        })),
    };
  if (p === "/api/v1/subscription-groups") {
    if (method === "POST") groups.push({ name: input.name, enabled: true });
    if (method === "DELETE")
      groups = groups.filter((g) => g.name !== url.searchParams.get("name"));
    return groups;
  }
  if (p === "/api/v1/subscription-groups/action") {
    groups.forEach((g) => {
      if (input.action === "use") g.enabled = g.name === input.name;
      else if (g.name === input.name) g.enabled = input.action === "enable";
    });
    return groups;
  }
  if (p === "/api/v1/routing") return routing;
  if (p === "/api/v1/routing/profiles/use") {
    routing.activeProfile = input.name;
    return routing;
  }
  if (p === "/api/v1/routing/profiles") {
    if (method === "POST") routing.profiles.push(input);
    if (method === "PATCH") {
      const item = routing.profiles.find((p) => p.name === input.profile);
      if (!item) throw new Error("配置不存在");
      item.groups = (item.groups || []).filter((g) => g !== input.group);
      if (input.attached) item.groups.push(input.group);
    }
    if (method === "DELETE")
      routing.profiles = routing.profiles.filter(
        (p) => p.name !== url.searchParams.get("name"),
      );
    return routing;
  }
  if (p === "/api/v1/routing/groups") {
    if (method === "POST") routing.ruleGroups.push({ ...input, rules: [] });
    if (method === "DELETE")
      routing.ruleGroups = routing.ruleGroups.filter(
        (g) => g.name !== url.searchParams.get("name"),
      );
    return routing;
  }
  if (p === "/api/v1/routing/rules") {
    const group = routing.ruleGroups.find(
      (g) => g.name === (input.group || url.searchParams.get("group")),
    );
    if (!group) throw new Error("组不存在");
    if (method === "POST") group.rules.push(input.rule);
    if (method === "DELETE")
      group.rules.splice(Number(url.searchParams.get("index")) - 1, 1);
    return routing;
  }
  if (p === "/api/v1/settings") {
    if (method === "PATCH") {
      if (input.revision && input.revision !== settings.revision)
        throw new Error("设置版本冲突，草稿已保留");
      settings = { ...settings, ...input, revision: "preview-" + Date.now() };
      report = null;
    }
    return settings;
  }
  if (p === "/api/v1/core/installations")
    return [
      {
        engine: "mihomo",
        version: "v1.19.31",
        active: true,
        path: "演示安装目录",
      },
    ];
  if (p.startsWith("/api/v1/core/")) {
    running = !p.endsWith("stop");
    return snapshot().core;
  }
  if (p === "/api/v1/web/security") return { authEnabled: true };
  if (p === "/api/v1/doctor")
    return [
      {
        name: "预览环境",
        status: "ok",
        message: "只使用模拟数据，不更改真实代理",
      },
    ];
  if (p === "/api/v1/logs")
    return [
      "[演示] 内核状态已载入",
      "[演示] 节点列表已加载；此预览不提供真实代理",
    ];
  throw new Error("未实现的模拟接口");
}
const bridge = `const events=new Map();window.runtime={EventsOn:(name,fn)=>events.set(name,fn)};window.go={main:{Desktop:{Info:async()=>({ready:true,version:'桌面交互演示',platform:'preview',architecture:'mock',dataDirectory:'内存数据，不读取真实配置',webURL:'',smokeTest:false,trayReady:false,trayStarting:false,trayError:'浏览器演示不提供系统托盘'}),RetryTray:async()=> '浏览器演示不提供原生托盘',HideWindow:async()=> '浏览器演示不能隐藏到系统托盘',Request:async(method,path,body)=>{const r=await fetch('/__preview/request',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({method,path,body:body?JSON.parse(body):{}})});return r.json();},SetTheme:()=>{},Reconnect:async()=>'',OpenWeb:async()=>'',Quit:async()=> '这是浏览器演示，不会退出真实后台',ImportCore:async()=>({status:204}),InstallCore:async()=>{for(let i=1;i<=5;i++){await new Promise(r=>setTimeout(r,200));events.get('core:progress')?.({stage:'download',message:'演示下载进度（不下载真实内核）',downloaded:i*200,total:1000,bytesPerSecond:1000});}return '';}}}};document.addEventListener('DOMContentLoaded',()=>{document.querySelector('.wordmark').append(document.createTextNode(' · 演示'));});`;
http
  .createServer(async (req, res) => {
    try {
      if (req.url === "/__preview/request" && req.method === "POST") {
        let chunks = "";
        for await (const chunk of req) {
          chunks += chunk;
          if (chunks.length > 65536) throw new Error("请求过大");
        }
        const input = JSON.parse(chunks);
        res.setHeader("Content-Type", "application/json");
        try {
          res.end(
            JSON.stringify({
              status: 200,
              data: api(input.method, input.path, input.body),
            }),
          );
        } catch (e) {
          res.end(JSON.stringify({ status: 400, error: e.message }));
        }
        return;
      }
      const uri = new URL(req.url, "http://localhost").pathname;
      if (uri === "/preview-bridge.js") {
        res.setHeader("Content-Type", "text/javascript");
        res.end(bridge);
        return;
      }
      const files = {
        "/": "desktop/frontend/index.html",
        "/app.js": "desktop/frontend/app.js",
        "/model.mjs": "desktop/frontend/model.mjs",
        "/sync.mjs": "desktop/frontend/sync.mjs",
        "/app.css": "desktop/frontend/app.css",
        "/fonts/noto-sans-sc-ui.woff2":
          "internal/server/assets/fonts/noto-sans-sc-ui.woff2",
      };
      const file = files[uri];
      if (!file) {
        res.writeHead(404);
        res.end();
        return;
      }
      let data = await readFile(path.join(root, file));
      if (uri === "/")
        data = Buffer.from(
          data
            .toString()
            .replace(
              '<script type="module"',
              '<script src="preview-bridge.js"></script><script type="module"',
            ),
        );
      res.setHeader(
        "Content-Type",
        file.endsWith("html")
          ? "text/html; charset=utf-8"
          : file.endsWith("css")
            ? "text/css"
            : file.endsWith("woff2")
              ? "font/woff2"
              : "text/javascript",
      );
      res.end(data);
    } catch {
      res.writeHead(500);
      res.end("预览请求失败");
    }
  })
  .listen(port, "127.0.0.1", () =>
    console.log(`Kivo 桌面演示（仅模拟数据） http://127.0.0.1:${port}`),
  );
