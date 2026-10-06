// Web 安装流回归测试：使用标准库模拟 DOM 和分块响应，不访问网络或浏览器会话。
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";
import test from "node:test";

const source = readFileSync(new URL("../internal/server/assets/app.js", import.meta.url), "utf8");
const bootstrapOffset = source.lastIndexOf("\ndocument.documentElement.dataset.theme=");
assert.ok(bootstrapOffset > 0, "必须找到页面初始化边界，不能截断函数定义");
const functions = source.slice(0, bootstrapOffset);

function harness(response) {
  const elements = new Map();
  const element = () => ({
      textContent: "", value: undefined, dataset: {}, disabled:false,
      children: [],
      get childNodes() { return this.children; },
      get lastElementChild() { return this.children.at(-1); },
      classList: {add(){},remove(){},toggle(){},contains(){return false}},
      append(...items) { this.children.push(...items); },
      replaceChildren(...items) { this.children = items; },
      removeAttribute(name) { delete this[name]; },
      setAttribute(name,value) { this[name]=value; },
      scrollIntoView() {},
      focus() {},
	  addEventListener() {},
    });
  const document = { createElement: element, querySelector(selector) {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  }};
  const context = vm.createContext({document, sessionStorage:{getItem:()=>null}, TextDecoder, Headers, fetch:async()=>response, setTimeout:()=>0});
  vm.runInContext(functions, context);
  return { elements, run:code=>vm.runInContext(code, context) };
}

test("Kivo 改名保留浏览器偏好且不复活已清空的 Token",()=>{
  const values=new Map([["proxypilot_token","old-token"]]);
  const storage={getItem:key=>values.has(key)?values.get(key):null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)};
  const context=vm.createContext({sessionStorage:{getItem:()=>null},storage,TextDecoder,Headers,document:{querySelector(){}}});
  vm.runInContext(functions,context);
  assert.equal(vm.runInContext('renamedPreference(storage,"kivo_token","proxypilot_token")',context),"old-token");
  assert.equal(values.get("kivo_token"),"old-token");
  assert.equal(values.has("proxypilot_token"),false);
  values.set("kivo_token","");values.set("proxypilot_token","stale-token");
  assert.equal(vm.runInContext('renamedPreference(storage,"kivo_token","proxypilot_token")',context),"");
  assert.equal(values.has("proxypilot_token"),false);
  values.clear();values.set("proxypilot_token","fallback-token");
  storage.setItem=()=>{throw new Error("storage disabled")};
  assert.equal(vm.runInContext('renamedPreference(storage,"kivo_token","proxypilot_token")',context),"fallback-token");
  assert.equal(values.get("proxypilot_token"),"fallback-token");
});

test("路由配置的 null 规则组和空规则不使页面中断",()=>{
  const h=harness(null);
  h.run('state.routing={activeProfile:"global",profiles:[{name:"global",defaultAction:"proxy",groups:null}],ruleGroups:[{name:"empty",rules:null}]};renderRouting()');
  const cards=h.elements.get("#routeProfiles").children;
  assert.equal(cards.length,1);
  assert.equal(cards[0].children[1].textContent,"无规则组");
  assert.equal(h.elements.get("#routeGroups").children[0].children[0].textContent,"empty · 0 条");
});

test("自动接入、手动接入、停止和恢复冲突明确区分",()=>{
  const h=harness(null);
  h.run('globalThis.fixture={core:{state:"running",mixedPort:17890,mode:"rule"},platform:"windows",architecture:"amd64",proxyPortListening:true,systemProxy:{state:"this_app",supported:true,managed:true,message:"自动接入"}};renderOverview(fixture)');
  assert.equal(h.elements.get("#heroAction").textContent,"断开代理");
  assert.equal(h.elements.get("#systemProxyOwnership").textContent,"自动管理");
  assert.equal(h.elements.get("#systemProxyOff").disabled,false);
  h.run('fixture.systemProxy.managed=false;renderOverview(fixture)');
  assert.equal(h.elements.get("#heroAction").textContent,"连接代理");
  assert.equal(h.elements.get("#systemProxyOwnership").textContent,"手动配置");
  assert.equal(h.elements.get("#systemProxyOff").disabled,true);
  h.run('fixture.core.state="stopped";renderOverview(fixture)');
  assert.match(h.elements.get("#accessStatus").textContent,/服务未就绪/);
  assert.match(h.elements.get("#accessStatus").className,/warning/);
  h.run('fixture.systemProxy.recoveryPending=true;fixture.systemProxy.conflict=true;renderOverview(fixture)');
  assert.equal(h.elements.get("#systemProxyRecover").hidden,false);
  assert.equal(h.elements.get("#systemProxyForce").hidden,false);
  assert.equal(h.elements.get("#systemProxyOn").disabled,true);
  assert.equal(h.elements.get("#systemProxyOwnership").textContent,"备份待恢复");
});

test("连接覆盖需确认，已有备份和未知平台不能悄悄覆盖",()=>{
  const h=harness(null);
  h.run('globalThis.confirmations=[];globalThis.confirm=(message)=>{confirmations.push(message);return false};globalThis.toast=()=>{}');
  assert.equal(h.run('proxyConnectOptions({systemProxy:{supported:true,state:"other"}})'),null);
  assert.match(h.run('confirmations[0]'),/原设置会先备份/);
  h.run('confirm=()=>true');
  assert.equal(h.run('proxyConnectOptions({systemProxy:{supported:true,state:"this_app"}}).adopt'),true);
  assert.equal(h.run('proxyConnectOptions({systemProxy:{supported:true,state:"other"}}).replace'),true);
  assert.equal(h.run('proxyConnectOptions({systemProxy:{supported:true,state:"this_app",managed:true,recoveryPending:true}})'),null);
  assert.equal(h.run('proxyConnectOptions({systemProxy:{supported:false,state:"off"}})'),null);
});

function streaming(text, splitAt = 1) {
  const bytes = new TextEncoder().encode(text);
  return new Response(new ReadableStream({ start(controller) {
    // 按单字节切块，覆盖中文 UTF-8 和 JSON 行跨网络分块的情况。
    for (let i=0;i<bytes.length;i+=splitAt) controller.enqueue(bytes.slice(i,i+splitAt));
    controller.close();
  }}), {headers:{"Content-Type":"application/x-ndjson"}});
}

test("分块安装事件正确完成，支持中文和无末尾换行", async()=>{
  const h = harness(streaming('{"stage":"download","total":100,"downloaded":35}\n{"stage":"complete","message":"安装完成"}'));
  await h.run("streamInstall()");
  assert.equal(h.elements.get("#installDetail").textContent,"安装完成");
  assert.equal(h.elements.get("#installPercent").textContent,"完成");
});

test("联网结果区分三条路径，过期结果不保留成功颜色",()=>{
  const h=harness(null);
  h.run('globalThis.fixture={core:{state:"running"},proxyPortListening:true,connection:{level:"warning",title:"代理可达 · 系统接入待确认",detail:"请接入系统",nextCommand:"/proxy setup"},connectivity:{checkedAt:new Date().toISOString(),routes:[{id:"direct",state:"failed",probes:[{target:"Google",state:"failed",durationMs:10,message:"超时"}]},{id:"entry",state:"ok",probes:[]},{id:"node",state:"partial",probes:[]}]}}; renderConnectivity(fixture)');
  assert.match(h.elements.get("#heroNode").textContent,/系统接入待确认/);
  let cards=h.elements.get("#connectivityRoutes").children;
  assert.equal(cards.length,3);
  assert.match(cards[0].className,/is-failed/);
  assert.match(cards[1].className,/is-ok/);
  assert.equal(cards[2].children[1].textContent,"部分通过");
  h.run('fixture.connectivity.checkedAt=new Date(Date.now()-180000).toISOString();renderConnectivity(fixture)');
  cards=h.elements.get("#connectivityRoutes").children;
  for(const card of cards){assert.match(card.className,/is-stale/);assert.equal(card.children[1].textContent,"需要重测");}
  assert.match(h.elements.get("#heroNode").textContent,/待检测/);
});

test("未检测和内核停止不能显示已联网；接入指引渲染为文本",async()=>{
  const h=harness(new Response(JSON.stringify({data:{platform:"windows",address:"127.0.0.1",port:17890,steps:["<img src=x onerror=alert(1)>"],disable:"先恢复系统代理",warning:"只影响服务主机"}})));
  h.run('renderConnectivity({core:{state:"stopped"},connection:{level:"idle",title:"代理服务未启动",detail:"先启动",nextCommand:"/core start"}})');
  assert.equal(h.elements.get("#heroNode").textContent,"代理服务未启动");
  for(const card of h.elements.get("#connectivityRoutes").children)assert.equal(card.children[1].textContent,"未检测");
  await h.run("showProxyGuide()");
  assert.equal(h.elements.get("#proxyGuide").hidden,false);
  assert.equal(h.elements.get("#proxyGuideSteps").children[0].textContent,"<img src=x onerror=alert(1)>");
  assert.match(h.elements.get("#proxyGuideWarning").textContent,/不会修改系统设置/);
});

test("截断、服务器失败和格式错误不能被当作安装成功", async()=>{
  for (const value of ['{"stage":"done"}\n','{"stage":"error","error":"校验失败"}\n','not-json\n']) {
    const h = harness(streaming(value));
    await assert.rejects(h.run("streamInstall()"));
  }
});

test("已知总量显示百分比和速度，未知总量使用不定进度", ()=>{
  const h = harness(null);
  h.run('renderInstallEvent({stage:"download",total:1024,downloaded:512,bytesPerSecond:256})');
  assert.equal(h.elements.get("#installBar").value,50);
  assert.match(h.elements.get("#installDetail").textContent,/\/s/);
  h.run('renderInstallEvent({stage:"resolve",message:"查询版本"})');
  assert.equal(h.elements.get("#installBar").value,undefined);
  h.run('renderInstallEvent({stage:"download",downloaded:1024})');
  assert.match(h.elements.get("#installDetail").textContent,/1.0 KiB.*总大小未知/);
});

test("鉴权错误不能显示成功",async()=>{
  const h = harness(new Response('{"error":{"message":"未授权"}}',{status:401,headers:{"Content-Type":"application/json"}}));
  await assert.rejects(h.run("streamInstall()"),/未授权/);
});

test("批量失败响应仍保留每项结果与节点数量", async()=>{
  const h = harness(new Response(JSON.stringify({data:{updated:["good"],results:[{name:"good",nodeCount:219}]},error:{message:"partial"}}),{status:400}));
  await assert.rejects(h.run('api("/api/v1/subscriptions/update")'),error=>error.message==="partial"&&error.data.results[0].nodeCount===219);
});

test("结果面板显示节点数、原数量、失败原因，未知数量不显示成零",()=>{
  const h=harness(null);
  h.run('renderSubscriptionResult({updated:["good"],temporarilyStartedCore:true,results:[{name:"good",via:"direct",nodeCount:219,previousCount:215,durationMs:1200},{name:"bad",via:"proxy",error:"服务器暂不可用",durationMs:500}]},"update","部分失败")');
  assert.equal(h.elements.get("#subscriptionResult").hidden,false);
  assert.match(h.elements.get("#subscriptionResultTitle").textContent,/1 个成功.*存在失败/);
  const rows=h.elements.get("#subscriptionResultList").children;
  assert.match(rows[0].children[1].textContent,/直连.*219 个节点.*215.*1.2s/);
  assert.match(rows[1].children[1].textContent,/PROXY.*节点数未确认/);
  assert.equal(rows[1].children[2].textContent,"服务器暂不可用");
  assert.match(h.elements.get("#subscriptionResultSummary").textContent,/已尝试恢复停止/);
});

test("空路径和只通过一条代理路径不能被视作外网通过",()=>{
  const h=harness(null);
  assert.equal(h.run('proxyChecksPassed({routes:[]})'),false);
  assert.equal(h.run('proxyChecksPassed({routes:[{id:"entry",state:"ok"}]})'),false);
  assert.equal(h.run('proxyChecksPassed({routes:[{id:"entry",state:"ok"},{id:"node",state:"ok"}]})'),true);
  assert.equal(h.run('proxyChecksPassed({stale:true,routes:[{id:"entry",state:"ok"},{id:"node",state:"ok"}]})'),false);
});

test("内核停止和非法检测时间使旧成功结果失效",()=>{
  const h=harness(null);
  h.run('globalThis.fixture={core:{state:"running"},proxyPortListening:true,connectivity:{checkedAt:new Date().toISOString(),routes:[{id:"entry",state:"ok"},{id:"node",state:"ok"}]},connection:{level:"ok",title:"已联网"}}');
  assert.equal(h.run('connectivityStale(fixture)'),false);
  h.run('fixture.core.state="stopped";renderConnectionChain(fixture);renderConnectivity(fixture)');
  assert.equal(h.run('connectivityStale(fixture)'),true);
  assert.doesNotMatch(h.elements.get("#chainInternetDot").className,/online/);
  for(const card of h.elements.get("#connectivityRoutes").children)assert.match(card.className,/is-stale/);
  h.run('fixture.core.state="running";fixture.connectivity.checkedAt="invalid"');
  assert.equal(h.run('connectivityStale(fixture)'),true);
});

test("节点分页覆盖全部节点，末页钳制且筛选后不残留空页",()=>{
  const h=harness(null);
  h.run('state.nodes=Array.from({length:218},(_,i)=>({name:"node-"+i,type:"Trojan",providerName:"primary",alive:true,delay:50+i}));renderNodes()');
  assert.equal(h.elements.get("#nodeList").children.length,24);
  assert.match(h.elements.get("#nodePageInfo").textContent,/1–24.*218/);
  h.run('state.nodePage=10;renderNodes()');
  assert.equal(h.elements.get("#nodeList").children.length,2);
  assert.equal(h.elements.get("#nodeNext").disabled,true);
  h.run('$("#nodeSearch").value="node-217";renderNodes()');
  assert.equal(h.elements.get("#nodeList").children.length,1);
  assert.equal(h.run('state.nodePage'),1);
});

test("节点延迟排序将失败和离线缓存放到通过检查的节点之后",()=>{
  const h=harness(null);
  h.run('state.nodes=[{name:"cached",delay:1,alive:true,cached:true},{name:"failed",delay:0,alive:false},{name:"slow",delay:150,alive:true},{name:"fast",delay:50,alive:true}];$("#nodeSort").value="latency"');
  assert.equal(h.run('filteredNodes().map(x=>x.name).join(",")'),"fast,slow,cached,failed");
  h.run('$("#nodeFilter").value="alive"');
  assert.equal(h.run('filteredNodes().map(x=>x.name).join(",")'),"fast,slow");
  h.run('state.delays.failed=30');
  assert.equal(h.run('filteredNodes().map(x=>x.name).join(",")'),"failed,fast,slow");
});

test("忙碌按钮恢复图标、原有禁用状态，重复调用不覆盖原标签",()=>{
  const h=harness(null);
  h.run('globalThis.button=$("#test");button.children=[{icon:true},{textContent:"保存"}];button.disabled=true;setBusy(button,true,"处理中");setBusy(button,true,"重复");setBusy(button,false)');
  const button=h.elements.get("#test");
  assert.equal(button.children.length,2);
  assert.equal(button.children[0].icon,true);
  assert.equal(button.disabled,true);
  assert.equal(button["aria-busy"],undefined);
});

test("节点和日志里的 HTML 保持文本，不生成可执行标签",()=>{
  const h=harness(null);
  h.run('state.nodes=[{name:"<img src=x onerror=alert(1)>",delay:0}];renderNodes();state.logs=["<script>alert(1)</script>"];renderLogs()');
  const name=h.elements.get("#nodeList").children[0].children[0].children[1];
  assert.equal(name.textContent,"<img src=x onerror=alert(1)>");
  assert.equal(h.elements.get("#logOutput").textContent,"<script>alert(1)</script>");
});

test("八个页面保留 API 交互元素且 ID 不重复",()=>{
  const html=readFileSync(new URL("../internal/server/assets/index.html",import.meta.url),"utf8");
  const ids=[...html.matchAll(/\bid="([^"]+)"/g)].map(match=>match[1]);
  assert.equal(ids.length,new Set(ids).size);
  for(const name of ["overview","nodes","subscriptions","cores","routing","diagnostics","logs","settings"])assert.ok(ids.includes("view-"+name));
  for(const match of functions.matchAll(/(?:\$|on)\("#([A-Za-z][A-Za-z0-9_-]*)[^"]*"/g))assert.ok(ids.includes(match[1]),"缺少 DOM 元素 "+match[1]);
  assert.doesNotMatch(html,/<script[^>]+src="https?:/);
});

test("设置保存保留持久监听地址，不把临时 serve 覆盖写回配置",async()=>{
  const h=harness(null);
  h.run('globalThis.requests=[];api=async(path,options)=>{requests.push(JSON.parse(options.body));return requests.at(-1)};refreshOverview=async()=>{};toast=()=>{};state.settings={listen:"127.0.0.1:9099"};state.overview={webAddress:"127.0.0.1:19400"};$("#settingPort").value="17890";$("#settingMode").value="rule";$("#settingDownloadProxy").value="";$("#settingDownloadRetry").value="4"');
  await h.run('handleSettingsSubmit({preventDefault(){},submitter:$("#save")})');
  assert.equal(h.run('requests[0].listen'),"127.0.0.1:9099");
  assert.equal(h.run('state.settingsDirty'),false);
});

test("相同 hash 导航不重复加载，非法 hash 回到安全页面",()=>{
  const h=harness(null);
  h.run('document.querySelectorAll=()=>[];globalThis.location={hash:"#nodes"};globalThis.window={scrollTo(){},matchMedia(){return {matches:false}}};globalThis.loads=0;loadNodes=()=>{loads++};switchView("nodes");switchView("nodes");');
  assert.equal(h.run('loads'),1);
  h.run('switchView("\\\"[bad-selector")');
  assert.equal(h.run('state.activeView'),"overview");
});
