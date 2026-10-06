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
      textContent: "", value: undefined, dataset: {},
      children: [],
      append(...items) { this.children.push(...items); },
      replaceChildren(...items) { this.children = items; },
      removeAttribute(name) { delete this[name]; },
      setAttribute(name,value) { this[name]=value; },
      scrollIntoView() {},
	  addEventListener() {},
    });
  const document = { createElement: element, querySelector(selector) {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  }};
  const context = vm.createContext({document, sessionStorage:{getItem:()=>null}, TextDecoder, Headers, fetch:async()=>response});
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
