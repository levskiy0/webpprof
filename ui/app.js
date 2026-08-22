const state={events:new Map(),kind:"request",selected:"",requestTab:"payload",responseTab:"response",relatedTab:"query",screen:"dashboard",paused:false,pending:[],socket:null,reconnect:0,reconnectTimer:0,socketStableTimer:0,runtime:[],queueStats:{sources:[]},watchedTags:new Set()};
const kinds=["request","middleware","query","cache","job","email","log","http_call","schedule","exception","event",""];
const navItems=["dashboard",...kinds];
const methodFilterKinds=new Set(["request","http_call"]);
const durationFilterKinds=new Set(["request","middleware","query","job","http_call"]);
const sqlKeywords=new Set("ADD ALL ALTER ANALYZE AND AS ASC BETWEEN BY CASE CHECK COLUMN CONSTRAINT CREATE CROSS CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT DELETE DESC DISTINCT DROP ELSE END EXISTS EXPLAIN FALSE FOREIGN FROM FULL GROUP HAVING IN INDEX INNER INSERT INTERSECT INTO IS JOIN KEY LEFT LIKE LIMIT NATURAL NOT NULL OFFSET ON OR ORDER OUTER PRIMARY REFERENCES RETURNING RIGHT SELECT SET TABLE THEN TRUE UNION UNIQUE UPDATE USING VALUES WHEN WHERE WITH".split(" "));
const ids=["login","workspace","login-form","login-error","token","socket-status","event-count","pause","clear","tag-watcher","tag-watch-count","tag-watch-clear","tag-watch-search","tag-watch-selected","tag-watch-results-count","tag-watch-options","kinds","search","method-filter","method","status-filter","status","level-filter","level","duration-filter","duration","list-heading","events","empty","detail","dashboard-screen","index-screen","detail-screen","screen-title","back"];
const elements=Object.fromEntries(ids.map(id=>[id,document.getElementById(id)]));
const base=()=>location.pathname.replace(/\/$/,"");

async function api(path,options={}){
  const response=await fetch(`${base()}${path}`,{credentials:"same-origin",...options});
  if(response.status===401)throw new Error("unauthorized");
  if(!response.ok)throw new Error(`request failed: ${response.status}`);
  return response.status===204?null:response.json();
}

async function load(){
  try{
    const result=await api("/api/events?limit=1000");
    state.events.clear();
    for(const event of result.events)state.events.set(event.id,event);
    restoreLocation();
    showWorkspace();
    renderTagWatcher();
    renderKinds();
    render();
    connect();
  }catch(error){
    showLogin(error.message==="unauthorized"?"":error.message);
  }
}

function bind(){
  elements["login-form"].addEventListener("submit",login);
  elements.pause.addEventListener("click",togglePause);
  elements.clear.addEventListener("click",clearEvents);
  elements.search.addEventListener("input",render);
  elements.method.addEventListener("change",render);
  elements.status.addEventListener("change",render);
  elements.level.addEventListener("change",render);
  elements.duration.addEventListener("change",render);
  elements["tag-watch-search"].addEventListener("input",renderTagWatcher);
  elements["tag-watcher"].addEventListener("toggle",()=>{
    if(elements["tag-watcher"].open)elements["tag-watch-search"].focus();
  });
  elements["tag-watch-options"].addEventListener("change",event=>{
    const input=event.target.closest("input[data-tag]");
    if(!input)return;
    if(input.checked)state.watchedTags.add(input.dataset.tag);
    else state.watchedTags.delete(input.dataset.tag);
    applyWatchedTags();
  });
  elements["tag-watch-clear"].addEventListener("click",()=>{
    state.watchedTags.clear();
    applyWatchedTags();
  });
  elements["tag-watch-selected"].addEventListener("click",event=>{
    const button=event.target.closest("[data-remove-tag]");
    if(!button)return;
    state.watchedTags.delete(button.dataset.removeTag);
    applyWatchedTags();
  });
  elements.back.addEventListener("click",showIndex);
  elements["dashboard-screen"].addEventListener("click",event=>{
    const target=event.target.closest("[data-event-id]");
    if(!target||!state.events.has(target.dataset.eventId))return;
    openDetail(target.dataset.eventId,"push");
  });
  elements.events.addEventListener("click",event=>{
    const row=event.target.closest("[data-id]");
    if(!row)return;
    openDetail(row.dataset.id);
  });
  elements.detail.addEventListener("click",event=>{
    const tag=event.target.closest("[data-watch-tag]");
    if(tag){
      state.watchedTags.add(tag.dataset.watchTag);
      applyWatchedTags();
      return;
    }
    const requestCopy=event.target.closest("[data-copy-request]");
    if(requestCopy){
      const request=state.events.get(state.selected);
      const format=requestCopy.dataset.copyRequest;
      copyText(requestRepresentation(request,format)).then(()=>showCopied(requestCopy,`Copy ${requestFormatLabel(format)}`)).catch(()=>requestCopy.setAttribute("aria-label","Could not copy request"));
      return;
    }
    const copy=event.target.closest("[data-copy-query]");
    if(copy){
      const query=state.events.get(state.selected);
      copyText(query?.data?.sql||"").then(()=>{
        showCopied(copy,"Copy SQL");
      }).catch(()=>copy.setAttribute("aria-label","Could not copy SQL"));
      return;
    }
    const tab=event.target.closest("[data-card-tab]");
    if(tab){
      const [group,value]=tab.dataset.cardTab.split(":");
      if(group==="request")state.requestTab=value;
      if(group==="response")state.responseTab=value;
      if(group==="related")state.relatedTab=value;
      syncLocation();
      renderDetail(state.events.get(state.selected));
      return;
    }
    const target=event.target.closest("[data-event-id]");
    if(target&&state.events.has(target.dataset.eventId)){
      openDetail(target.dataset.eventId,"push");
    }
  });
  window.addEventListener("popstate",()=>{
    restoreLocation();
    renderKinds();
    render();
  });
}

function renderKinds(){
  if(elements.kinds.children.length!==navItems.length){
    elements.kinds.replaceChildren(...navItems.map(kind=>{
      const button=document.createElement("button");
      const dashboard=kind==="dashboard";
      button.type="button";
      button.className="nav-item";
      button.dataset.kind=kind;
      button.innerHTML=`${navIcon(kind)}<span>${escapeHTML(kindLabel(kind))}</span>${dashboard?'':'<span class="nav-count"></span>'}`;
      button.addEventListener("click",()=>{
        if(dashboard)state.screen="dashboard";
        else{
          state.kind=kind;
          state.screen="index";
        }
        state.selected="";
        syncLocation("push");
        renderKinds();
        render();
      });
      return button;
    }));
  }
  for(const button of elements.kinds.children){
    const kind=button.dataset.kind;
    const dashboard=kind==="dashboard";
    button.classList.toggle("active",dashboard?state.screen==="dashboard":state.screen!=="dashboard"&&state.kind===kind);
    const badge=button.querySelector(".nav-count");
    if(!badge)continue;
    const count=kindCount(kind);
    badge.textContent=count;
    badge.classList.toggle("empty",count===0);
  }
}

async function login(event){
  event.preventDefault();
  elements["login-error"].textContent="";
  try{
    await api("/session",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token:elements.token.value})});
    elements.token.value="";
    await load();
  }catch(error){
    elements["login-error"].textContent=error.message==="unauthorized"?"Invalid token":error.message;
  }
}

function showLogin(message=""){
  stopSocket();
  elements.login.classList.remove("hidden");
  elements.workspace.classList.add("hidden");
  elements["login-error"].textContent=message;
  elements.token.focus();
}

function showWorkspace(){
  elements.login.classList.add("hidden");
  elements.workspace.classList.remove("hidden");
}

function connect(){
  if(state.reconnectTimer){
    window.clearTimeout(state.reconnectTimer);
    state.reconnectTimer=0;
  }
  if(state.socket&&(state.socket.readyState===WebSocket.CONNECTING||state.socket.readyState===WebSocket.OPEN))return;
  const protocol=location.protocol==="https:"?"wss:":"ws:";
  const socket=new WebSocket(`${protocol}//${location.host}${base()}/ws`);
  state.socket=socket;
  socket.addEventListener("open",()=>{
    if(state.socket!==socket)return;
    if(state.socketStableTimer)window.clearTimeout(state.socketStableTimer);
    state.socketStableTimer=window.setTimeout(()=>{
      if(state.socket===socket&&socket.readyState===WebSocket.OPEN)state.reconnect=0;
    },30000);
    elements["socket-status"].textContent="Live";
    elements["socket-status"].className="status online";
  });
  socket.addEventListener("message",message=>{
    if(state.socket!==socket)return;
    const update=JSON.parse(message.data);
    if(update.type==="event.created"||update.type==="event.updated")receive(update.event);
    if(update.type==="connected"||update.type==="stats.updated")receiveStats(update);
    if(update.type==="events.cleared"){
      state.events.clear();
      state.selected="";
      renderTagWatcher();
      renderKinds();
      render();
    }
  });
  socket.addEventListener("close",()=>{
    if(state.socket!==socket)return;
    state.socket=null;
    if(state.socketStableTimer){
      window.clearTimeout(state.socketStableTimer);
      state.socketStableTimer=0;
    }
    elements["socket-status"].textContent="Offline";
    elements["socket-status"].className="status offline";
    recoverSocket();
  });
}

function stopSocket(){
  if(state.reconnectTimer){
    window.clearTimeout(state.reconnectTimer);
    state.reconnectTimer=0;
  }
  if(state.socketStableTimer){
    window.clearTimeout(state.socketStableTimer);
    state.socketStableTimer=0;
  }
  const socket=state.socket;
  state.socket=null;
  if(socket&&(socket.readyState===WebSocket.CONNECTING||socket.readyState===WebSocket.OPEN))socket.close(1000,"session ended");
}

async function recoverSocket(){
  try{
    await api("/api/stats");
  }catch(error){
    if(state.socket)return;
    if(error.message==="unauthorized"){
      showLogin();
      return;
    }
  }
  if(!state.socket)scheduleReconnect();
}

function scheduleReconnect(){
  if(state.reconnectTimer||state.socket)return;
  const delay=Math.min(10000,500*2**Math.min(state.reconnect,5));
  state.reconnect++;
  state.reconnectTimer=window.setTimeout(()=>{
    state.reconnectTimer=0;
    connect();
  },delay);
}

function receiveStats(update){
  if(update.runtime)recordRuntime(update.runtime);
  if(update.queues)state.queueStats=update.queues;
  if(state.screen==="dashboard")updateDashboard();
}

function recordRuntime(sample){
  const previous=state.runtime.at(-1);
  const total=previous?sample.cpu_seconds-previous.cpu_seconds:sample.cpu_seconds;
  const idle=previous?sample.cpu_idle_seconds-previous.cpu_idle_seconds:sample.cpu_idle_seconds;
  sample.cpu_percent=total>0?Math.max(0,Math.min(100,(total-idle)/total*100)):0;
  state.runtime.push(sample);
  if(state.runtime.length>60)state.runtime.shift();
}

function receive(event){
  if(state.paused){
    state.pending.push(event);
    updatePauseButton();
    return;
  }
  state.events.set(event.id,event);
  renderTagWatcher();
  scheduleRender();
}

let frame=0;

function scheduleRender(){
  if(frame)return;
  frame=requestAnimationFrame(()=>{
    frame=0;
    renderKinds();
    updateEventCount();
    if(state.screen==="index")render();
    if(state.screen==="dashboard")updateDashboard();
  });
}

function togglePause(){
  state.paused=!state.paused;
  updatePauseButton();
  if(!state.paused){
    for(const event of state.pending)state.events.set(event.id,event);
    state.pending=[];
    renderTagWatcher();
    renderKinds();
    render();
  }
}

function updatePauseButton(){
  const label=state.paused?`Resume recording (${state.pending.length} pending)`:"Pause recording";
  elements.pause.title=label;
  elements.pause.setAttribute("aria-label",label);
  elements.pause.classList.toggle("active",state.paused);
  elements.pause.querySelector(".sr-only").textContent=label;
}

async function clearEvents(){
  if(!confirm("Clear all profiler events?"))return;
  await api("/api/events",{method:"DELETE"});
  state.events.clear();
  state.selected="";
  renderTagWatcher();
  renderKinds();
  render();
}

function filtered(){
  const search=elements.search.value.trim().toLowerCase();
  const method=elements.method.value;
  const status=elements.status.value;
  const level=elements.level.value;
  const minimum=durationFilterKinds.has(state.kind)?Number(elements.duration.value)*1000000:0;
  return visibleEvents().filter(event=>(!state.kind||event.kind===state.kind)&&eventDuration(event)>=minimum&&matchesContextFilters(event,method,status,level)&&(!search||`${event.kind} ${JSON.stringify(event.data)}`.toLowerCase().includes(search))).sort((a,b)=>b.cursor-a.cursor);
}

function visibleEvents(){
  return[...state.events.values()].filter(matchesWatchedTags);
}

function matchesWatchedTags(event){
  if(!state.watchedTags.size)return true;
  const tags=event.tags||{};
  for(const token of state.watchedTags){
    const separator=token.indexOf("=");
    const key=separator<0?token:token.slice(0,separator);
    const value=separator<0?null:token.slice(separator+1);
    if(!Object.prototype.hasOwnProperty.call(tags,key)||value!==null&&String(tags[key])!==value)return false;
  }
  return true;
}

function applyWatchedTags(){
  if(state.selected&&!matchesWatchedTags(state.events.get(state.selected)||{})){
    state.selected="";
    if(state.screen==="detail")state.screen="index";
  }
  syncLocation();
  renderTagWatcher();
  renderKinds();
  render();
}

function renderTagWatcher(){
  const counts=new Map();
  for(const event of state.events.values()){
    for(const [key,value] of Object.entries(event.tags||{})){
      const token=`${key}=${value}`;
      counts.set(token,(counts.get(token)||0)+1);
    }
  }
  for(const token of state.watchedTags)if(!counts.has(token))counts.set(token,0);
  const query=elements["tag-watch-search"].value.trim().toLowerCase();
  const matches=[...counts].filter(([token])=>!query||token.toLowerCase().includes(query)).sort((left,right)=>{
    const leftStarts=query&&left[0].toLowerCase().startsWith(query);
    const rightStarts=query&&right[0].toLowerCase().startsWith(query);
    return Number(rightStarts)-Number(leftStarts)||right[1]-left[1]||left[0].localeCompare(right[0]);
  });
  const tags=matches.slice(0,5);
  elements["tag-watch-selected"].innerHTML=state.watchedTags.size?[...state.watchedTags].sort().map(token=>`<button type="button" data-remove-tag="${escapeHTML(token)}" title="Stop watching ${escapeHTML(token)}"><span>${escapeHTML(token)}</span><b aria-hidden="true">×</b></button>`).join(""):"";
  elements["tag-watch-selected"].classList.toggle("hidden",state.watchedTags.size===0);
  elements["tag-watch-results-count"].textContent=matches.length>5?`5 of ${matches.length}`:String(matches.length);
  elements["tag-watch-options"].innerHTML=tags.length?tags.map(([token,count])=>`<label><input type="checkbox" data-tag="${escapeHTML(token)}"${state.watchedTags.has(token)?" checked":""}><span>${escapeHTML(token)}</span><b>${count}</b></label>`).join(""):`<p>${query?"No matching tags.":"No tags recorded yet."}</p>`;
  elements["tag-watch-count"].textContent=state.watchedTags.size;
  elements["tag-watcher"].classList.toggle("active",state.watchedTags.size>0);
  elements["tag-watch-clear"].disabled=state.watchedTags.size===0;
}

function updateEventCount(){
  const visible=visibleEvents().length;
  elements["event-count"].textContent=state.watchedTags.size?`${visible}/${state.events.size} events`:`${state.events.size} events`;
}

function matchesContextFilters(event,method,status,level){
  const data=event.data||{};
  if(method&&methodFilterKinds.has(state.kind)&&String(data.method||"").toUpperCase()!==method)return false;
  if(status&&state.kind==="request"){
    const code=Number(data.status)||0;
    if(status.startsWith("class:")&&Math.floor(code/100)!==Number(status.slice(6)))return false;
    if(status.startsWith("code:")&&code!==Number(status.slice(5)))return false;
  }
  if(level&&state.kind==="log"&&normalizedLogLevel(data.level)!==level)return false;
  return true;
}

function render(){
  renderRequestStatusOptions();
  const events=state.screen==="index"?filtered():[];
  updateEventCount();
  elements["screen-title"].textContent=kindLabel(state.kind);
  renderListHeading();
  elements["method-filter"].classList.toggle("hidden",!methodFilterKinds.has(state.kind));
  elements["status-filter"].classList.toggle("hidden",state.kind!=="request");
  elements["level-filter"].classList.toggle("hidden",state.kind!=="log");
  elements["duration-filter"].classList.toggle("hidden",!durationFilterKinds.has(state.kind));
  elements.empty.classList.toggle("hidden",events.length>0);
  elements.events.replaceChildren(...events.map(row));
  if(state.selected&&!state.events.has(state.selected))state.selected="";
  elements["dashboard-screen"].classList.toggle("hidden",state.screen!=="dashboard");
  elements["index-screen"].classList.toggle("hidden",state.screen!=="index");
  elements["detail-screen"].classList.toggle("hidden",state.screen!=="detail");
  if(state.screen==="dashboard")renderDashboard();
  if(state.screen==="detail")renderDetail(state.events.get(state.selected));
}

function renderRequestStatusOptions(){
  const selected=elements.status.value;
  const counts=new Map();
  for(const event of visibleEvents()){
    if(event.kind!=="request")continue;
    const code=Number((event.data||{}).status)||0;
    if(code>=100&&code<=599)counts.set(code,(counts.get(code)||0)+1);
  }
  if(selected.startsWith("code:")){
    const code=Number(selected.slice(5));
    if(code>=100&&code<=599&&!counts.has(code))counts.set(code,0);
  }
  const statuses=[...counts].sort((left,right)=>left[0]-right[0]);
  const signature=JSON.stringify(statuses);
  if(elements.status.dataset.signature===signature)return;
  elements.status.dataset.signature=signature;
  elements.status.innerHTML=`<option value="">All</option><optgroup label="Status class"><option value="class:2">2xx · Success</option><option value="class:3">3xx · Redirect</option><option value="class:4">4xx · Client error</option><option value="class:5">5xx · Server error</option></optgroup>${statuses.length?`<optgroup label="Recorded codes">${statuses.map(([code,count])=>`<option value="code:${code}">${code} · ${count}</option>`).join("")}</optgroup>`:""}`;
  elements.status.value=selected;
}

function renderListHeading(){
  const layout=listLayout(state.kind);
  elements["list-heading"].className=`list-heading${listLayoutClasses(layout)}`;
  elements["list-heading"].innerHTML=`${layout.badge?`<span>${escapeHTML(layout.badge)}</span>`:""}<span>${escapeHTML(layout.entry)}</span>${layout.status?`<span>${escapeHTML(layout.status)}</span>`:""}${layout.duration?'<span class="list-duration">Duration</span>':""}<span class="list-time">Happened</span><span></span>`;
}

function renderDashboard(){
  if(!elements["dashboard-screen"].querySelector("[data-dashboard-root]")){
    elements["dashboard-screen"].innerHTML=`
      <div class="dashboard-root" data-dashboard-root>
        <header class="dashboard-heading">
          <div><h2>Dashboard</h2><p>Runtime health and recorded application activity.</p></div>
          <div class="dashboard-window"><span class="live-pulse"></span><strong>Live · 2 minute window</strong><span data-dashboard-uptime>Collecting runtime data</span></div>
        </header>
        <section class="dashboard-metrics">
          ${dashboardMetricShell("cpu","CPU usage")}
          ${dashboardMetricShell("memory","Go memory")}
          ${dashboardMetricShell("requests","HTTP requests")}
          ${dashboardMetricShell("queries","Database queries")}
          ${dashboardMetricShell("cache","Cache hit rate")}
          ${dashboardMetricShell("goroutines","Goroutines")}
        </section>
        <section class="dashboard-charts">
          <article class="dashboard-panel mix-panel"><header><div><h3>Event mix</h3><span>Events retained in memory</span></div><small>Recorded window</small></header><div class="mix-list" data-dashboard-mix></div></article>
          <article class="dashboard-panel queue-panel"><header><div><h3>Queue health</h3><span>Backlog and worker capacity by queue</span></div><small data-dashboard-queue-summary>Waiting for a stats source</small></header><div data-dashboard-queues></div></article>
          <article class="dashboard-panel slow-panel"><header><div><h3>Slowest operations</h3><span>Requests, queries and HTTP calls</span></div><small>Click to inspect</small></header><div class="slow-list" data-dashboard-slow></div></article>
        </section>
      </div>`;
  }
  updateDashboard();
}

function dashboardMetricShell(key,label){
  return`<article class="dashboard-metric"><header><span>${escapeHTML(label)}</span><strong data-dashboard-value="${key}">—</strong></header><div class="dashboard-metric-chart" data-dashboard-chart="${key}"></div><footer data-dashboard-meta="${key}">Collecting data</footer></article>`;
}

function updateDashboard(){
  if(!elements["dashboard-screen"].querySelector("[data-dashboard-root]"))return;
  const runtime=state.runtime.at(-1)||{};
  const requests=recentEvents("request");
  const queries=recentEvents("query");
  const cache=recentEvents("cache");
  const requestSeries=bucketSeries("request");
  const querySeries=bucketSeries("query");
  const cacheSeries=cacheHitSeries();
  const cpuSeries=state.runtime.map(sample=>sample.cpu_percent||0);
  const memorySeries=state.runtime.map(sample=>sample.memory_bytes||0);
  const goroutineSeries=state.runtime.map(sample=>sample.goroutines||0);
  const cacheHits=cache.filter(event=>(event.data||{}).hit).length;
  const hitRate=cache.length?cacheHits/cache.length*100:0;
  const requestErrors=requests.filter(isFailure).length;
  const queryErrors=queries.filter(isFailure).length;
  updateDashboardMetric("cpu",percent(runtime.cpu_percent||0),`Across ${runtime.gomaxprocs||0} logical processors`,sparkline(cpuSeries,"#6266d6",180,58));
  updateDashboardMetric("memory",bytes(runtime.memory_bytes||0),`${bytes(runtime.heap_objects_bytes||0)} heap objects`,sparkline(memorySeries,"#6266d6",180,58));
  updateDashboardMetric("requests",ratePerMinute(requests.length),`${requests.length} recorded · ${duration(averageDuration(requests))} avg · ${requestErrors} failed`,sparkline(requestSeries,"#6266d6",180,58));
  updateDashboardMetric("queries",ratePerMinute(queries.length),`${queries.length} recorded · ${duration(averageDuration(queries))} avg · ${queryErrors} failed`,sparkline(querySeries,"#6266d6",180,58));
  updateDashboardMetric("cache",percent(hitRate),`${cacheHits} hits · ${Math.max(0,cache.length-cacheHits)} misses`,sparkline(cacheSeries,"#6266d6",180,58));
  updateDashboardMetric("goroutines",String(runtime.goroutines||0),`${runtime.gc_cycles||0} completed GC cycles`,sparkline(goroutineSeries,"#6266d6",180,58));
  setDashboardText("[data-dashboard-uptime]",runtime.uptime_ns?`Uptime ${formatUptime(runtime.uptime_ns)}`:"Collecting runtime data");
  setDashboardHTML("[data-dashboard-mix]",eventMixRows());
  const queues=queueHealthView();
  setDashboardText("[data-dashboard-queue-summary]",queues.summary);
  setDashboardHTML("[data-dashboard-queues]",queues.html);
  setDashboardHTML("[data-dashboard-slow]",slowOperationRows());
}

function updateDashboardMetric(key,value,meta,chart){
  setDashboardText(`[data-dashboard-value="${key}"]`,value);
  setDashboardText(`[data-dashboard-meta="${key}"]`,meta);
  setDashboardHTML(`[data-dashboard-chart="${key}"]`,chart);
}

function setDashboardText(selector,value){
  const element=elements["dashboard-screen"].querySelector(selector);
  if(element&&element.textContent!==value)element.textContent=value;
}

function setDashboardHTML(selector,value){
  const element=elements["dashboard-screen"].querySelector(selector);
  if(element&&element.innerHTML!==value)element.innerHTML=value;
}

function eventMixRows(){
  const values=kinds.filter(Boolean).map(kind=>({kind,count:recentEvents(kind).length})).filter(item=>item.count>0).sort((a,b)=>b.count-a.count).slice(0,7);
  const maximum=Math.max(1,...values.map(item=>item.count));
  const rows=values.map(item=>`<div class="mix-row"><span>${escapeHTML(kindLabel(item.kind))}</span><progress max="${maximum}" value="${item.count}">${item.count}</progress><strong>${item.count}</strong></div>`).join("");
  return rows||'<div class="dashboard-panel-empty">Waiting for recorded events…</div>';
}

function queueHealthView(){
  const sources=Array.isArray(state.queueStats?.sources)?state.queueStats.sources:[];
  if(!sources.length){
    return{summary:"No stats source",html:'<div class="dashboard-panel-empty queue-empty">Queue metrics are not connected.</div>'};
  }
  const totals=sources.reduce((result,source)=>{
    result.pending+=positiveNumber(source.pending);
    result.active+=positiveNumber(source.workers_active);
    result.workers+=positiveNumber(source.workers_total);
    result.failed+=positiveNumber(source.failed);
    return result;
  },{pending:0,active:0,workers:0,failed:0});
  const rows=[];
  for(const source of sources){
    if(source.error){
      rows.push({name:"Unavailable",source:source.source||"default",error:source.error,pending:0,workers_active:0,workers_total:0,processed:0,failed:0});
      continue;
    }
    const queues=Array.isArray(source.queues)&&source.queues.length?source.queues:[{name:"All queues",pending:source.pending,workers_active:source.workers_active,workers_total:source.workers_total,processed:source.processed,failed:source.failed}];
    for(const queue of queues)rows.push({...queue,source:source.source||"default"});
  }
  rows.sort((left,right)=>queueHealthRank(right)-queueHealthRank(left)||positiveNumber(right.pending)-positiveNumber(left.pending)||String(left.name).localeCompare(String(right.name)));
  const body=rows.map(queue=>{
    const health=queueHealth(queue);
    const failed=positiveNumber(queue.failed);
    return`<div class="queue-row"><span class="queue-name"><strong>${escapeHTML(queue.name||"default")}</strong><small>${escapeHTML(queue.error||queue.source)}</small></span><b>${formatCount(queue.pending)}</b><b>${formatCount(queue.workers_active)}</b><b>${formatCount(queue.workers_total)}</b><b>${formatCount(queue.processed)}</b><b class="${failed?"queue-failed":""}">${formatCount(failed)}</b><span class="queue-state ${health.className}">${health.label}</span></div>`;
  }).join("");
  const overview=`<div class="queue-overview"><div><span>Pending</span><strong>${formatCount(totals.pending)}</strong></div><div><span>Active</span><strong>${formatCount(totals.active)}</strong></div><div><span>Workers</span><strong>${formatCount(totals.workers)}</strong></div><div><span>Failed</span><strong class="${totals.failed?"queue-failed":""}">${formatCount(totals.failed)}</strong></div></div>`;
  const table=`<div class="queue-table"><div class="queue-heading"><span>Queue</span><span>Pending</span><span>Active</span><span>Workers</span><span>Processed</span><span>Failed</span><span>Status</span></div>${body}</div>`;
  return{summary:`${formatCount(totals.pending)} pending · ${formatCount(totals.active)}/${formatCount(totals.workers)} workers active`,html:overview+table};
}

function queueHealth(queue){
  if(queue.error)return{label:"Unavailable",className:"danger"};
  const pending=positiveNumber(queue.pending);
  const active=positiveNumber(queue.workers_active);
  const workers=positiveNumber(queue.workers_total);
  if(pending>0&&workers===0)return{label:"No workers",className:"danger"};
  if(pending>0&&active>=workers)return{label:"Saturated",className:"warning"};
  return{label:"Healthy",className:"healthy"};
}

function queueHealthRank(queue){
  const health=queueHealth(queue);
  return health.className==="danger"?3:health.className==="warning"?2:1;
}

function positiveNumber(value){
  return Math.max(0,Number(value)||0);
}

function formatCount(value){
  return positiveNumber(value).toLocaleString();
}

function slowOperationRows(){
  const operations=visibleEvents().filter(event=>["request","query","http_call"].includes(event.kind)).sort((a,b)=>eventDuration(b)-eventDuration(a)).slice(0,6);
  const rows=operations.map(event=>`<button type="button" class="slow-row" data-event-id="${escapeHTML(event.id)}"><span class="slow-kind">${escapeHTML(kindSingular(event.kind))}</span><span><strong>${escapeHTML(slowOperationTitle(event))}</strong></span><b>${escapeHTML(duration(event.duration_ns))}</b>${rowAction()}</button>`).join("");
  return rows||'<div class="dashboard-panel-empty">No timed operations yet.</div>';
}

function slowOperationTitle(event){
  const data=event.data||{};
  if(event.kind==="request")return requestTarget(data);
  if(event.kind==="http_call")return data.url||"HTTP call";
  return compactQuery(data.sql||"");
}

function recentEvents(kind){
  const cutoff=Date.now()-120000;
  return visibleEvents().filter(event=>event.kind===kind&&Date.parse(event.started_at)>=cutoff);
}

function bucketSeries(kind){
  const bucket=5000;
  const count=24;
  const end=Math.floor(Date.now()/bucket)*bucket;
  const start=end-(count-1)*bucket;
  const values=new Array(count).fill(0);
  for(const event of visibleEvents()){
    if(event.kind!==kind)continue;
    const index=Math.floor((Date.parse(event.started_at)-start)/bucket);
    if(index>=0&&index<count)values[index]++;
  }
  return values;
}

function cacheHitSeries(){
  const bucket=5000;
  const count=24;
  const end=Math.floor(Date.now()/bucket)*bucket;
  const start=end-(count-1)*bucket;
  const hits=new Array(count).fill(0);
  const totals=new Array(count).fill(0);
  for(const event of visibleEvents()){
    if(event.kind!=="cache")continue;
    const index=Math.floor((Date.parse(event.started_at)-start)/bucket);
    if(index<0||index>=count)continue;
    totals[index]++;
    if((event.data||{}).hit)hits[index]++;
  }
  return totals.map((total,index)=>total?hits[index]/total*100:0);
}

function sparkline(values,color,width,height){
  const points=chartPoints(values,width,height,5,false);
  const line=points.map(point=>point.join(",")).join(" ");
  const area=points.length?`M ${points[0][0]} ${height-3} L ${points.map(point=>point.join(" ")).join(" L ")} L ${points.at(-1)[0]} ${height-3} Z`:"";
  return`<svg class="sparkline" viewBox="0 0 ${width} ${height}" role="img" aria-label="Recent trend"><path d="${area}" fill="${color}18"></path><polyline points="${line}" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></polyline></svg>`;
}

function chartPoints(values,width,height,padding,zeroBased,forcedMaximum){
  const source=values.length>1?values:[values[0]||0,values[0]||0];
  const maximum=forcedMaximum??Math.max(...source,1);
  const minimum=zeroBased?0:Math.min(...source);
  const range=Math.max(maximum-minimum,maximum*.04,1);
  return source.map((value,index)=>[
    Number((padding+index*(width-padding*2)/(source.length-1)).toFixed(2)),
    Number((height-padding-(value-minimum)/range*(height-padding*2)).toFixed(2))
  ]);
}

function averageDuration(events){
  return events.length?events.reduce((total,event)=>total+eventDuration(event),0)/events.length:0;
}

function eventDuration(event){
  return Number(event?.duration_ns)||0;
}

function isFailure(event){
  const data=event.data||{};
  return Boolean(data.error||data.status>=500||["failed","dispatch_failed","panicked"].includes(data.state));
}

function ratePerMinute(count){
  const rate=count/2;
  return`${rate>=10?rate.toFixed(0):rate.toFixed(1)}/min`;
}

function percent(value){
  return`${value>=10?value.toFixed(0):value.toFixed(1)}%`;
}

function formatUptime(ns){
  const seconds=Math.max(0,Math.floor(ns/1e9));
  if(seconds<60)return`${seconds}s`;
  if(seconds<3600)return`${Math.floor(seconds/60)}m ${seconds%60}s`;
  if(seconds<86400)return`${Math.floor(seconds/3600)}h ${Math.floor(seconds%3600/60)}m`;
  return`${Math.floor(seconds/86400)}d ${Math.floor(seconds%86400/3600)}h`;
}

function formatDateTime(value){
  if(!value)return"—";
  const date=new Date(value);
  return Number.isNaN(date.getTime())||date.getUTCFullYear()<=1?"—":date.toLocaleString();
}

function openDetail(id,historyMode="push"){
  if(!state.events.has(id))return;
  state.kind=state.events.get(id).kind;
  state.selected=id;
  state.requestTab="payload";
  state.responseTab="response";
  state.relatedTab=firstRelatedTab(groupFor(state.events.get(id)));
  state.screen="detail";
  syncLocation(historyMode);
  renderKinds();
  render();
  elements["detail-screen"].scrollTop=0;
  window.scrollTo({top:0,behavior:"smooth"});
}

function showIndex(){
  state.screen="index";
  state.selected="";
  syncLocation("push");
  render();
}

function restoreLocation(){
  const params=new URLSearchParams(location.search);
  state.watchedTags=new Set(params.getAll("tag").filter(Boolean));
  const entry=params.get("entry")||"";
  const view=params.get("view")||"dashboard";
  if(!state.events.has(entry)||!matchesWatchedTags(state.events.get(entry))){
    state.selected="";
    if(view==="dashboard")state.screen="dashboard";
    else{
      state.kind=view==="all"?"":kinds.includes(view)?view:"request";
      state.screen="index";
    }
    return;
  }
  state.kind=view==="all"?"":kinds.includes(view)?view:state.events.get(entry).kind;
  state.selected=entry;
  const tab=params.get("tab")||"";
  if(tab==="request")state.requestTab="payload";
  else if(tab==="response")state.responseTab="response";
  else if(tab)state.relatedTab=tab;
  else state.relatedTab=firstRelatedTab(groupFor(state.events.get(entry)));
  state.screen="detail";
}

function syncLocation(mode="replace"){
  const url=new URL(location.href);
  url.searchParams.delete("entry");
  url.searchParams.delete("tab");
  url.searchParams.delete("tag");
  for(const tag of [...state.watchedTags].sort())url.searchParams.append("tag",tag);
  if(state.screen==="detail"&&state.selected){
    url.searchParams.set("entry",state.selected);
    url.searchParams.set("tab",state.relatedTab);
    url.searchParams.set("view",state.kind||"all");
  }else{
    url.searchParams.set("view",state.screen==="dashboard"?"dashboard":state.kind||"all");
  }
  history[mode==="push"?"pushState":"replaceState"](null,"",url);
}

function row(event){
  const button=document.createElement("button");
  const status=statusFor(event);
  const layout=listLayout(state.kind);
  const badge=listBadge(event);
  button.type="button";
  button.className=`event-row${listLayoutClasses(layout)}${state.selected===event.id?" active":""}`;
  button.dataset.id=event.id;
  button.dataset.kind=event.kind;
  button.innerHTML=`${badge?`<span><span class="method${badge.className?` ${escapeHTML(badge.className)}`:""}" title="${escapeHTML(badge.label)}">${escapeHTML(badge.label)}</span></span>`:""}${eventPreview(event)}${layout.status?`<span class="state ${status.className}">${escapeHTML(status.label)}</span>`:""}${layout.duration?`<span class="duration">${duration(event.duration_ns)}</span>`:""}<span class="event-time">${relativeTime(event.started_at)}</span>${rowAction()}`;
  return button;
}

function listLayout(kind){
  const layouts={
    "":{badge:"Type",entry:"Entry",status:"Status",duration:true},
    request:{badge:"Method",entry:"Path",status:"Status",duration:true},
    middleware:{badge:"",entry:"Middleware",status:"State",duration:true},
    query:{badge:"Operation",entry:"Query",status:"Status",duration:true},
    cache:{badge:"Operation",entry:"Key",status:"Result",duration:true},
    job:{badge:"Queue",entry:"Job",status:"State",duration:true},
    email:{badge:"Transport",entry:"Subject",status:"Status",duration:true},
    log:{badge:"Level",entry:"Message",status:"",duration:false},
    http_call:{badge:"Method",entry:"URL",status:"Status",duration:true},
    schedule:{badge:"",entry:"Task",status:"State",duration:true},
    exception:{badge:"Type",entry:"Message",status:"",duration:false,wideBadge:true},
    event:{badge:"Kind",entry:"Event",status:"Status",duration:true}
  };
  return layouts[kind]||layouts[""];
}

function listLayoutClasses(layout){
  return`${layout.badge?"":" without-badge"}${layout.status?"":" without-status"}${layout.duration?"":" without-duration"}${layout.wideBadge?" wide-badge":""}`;
}

function listBadge(event){
  const data=event.data||{};
  if(state.kind==="")return{label:kindSingular(event.kind),className:`method-kind-${event.kind}`};
  if(methodFilterKinds.has(state.kind)){
    const method=String(data.method||"HTTP").toUpperCase();
    return{label:method,className:`method-${method.toLowerCase().replace(/[^a-z0-9_-]/g,"")}`};
  }
  if(state.kind==="query"||state.kind==="cache"){
    const operation=String(data.operation||(state.kind==="query"?"SQL":"CACHE")).toUpperCase();
    return{label:operation,className:`method-operation method-operation-${badgeToken(operation)}`};
  }
  if(state.kind==="job")return{label:data.queue||"default",className:"method-queue"};
  if(state.kind==="email")return{label:data.transport||"mail",className:"method-transport"};
  if(state.kind==="log"){
    const level=normalizedLogLevel(data.level||"log");
    return{label:level.toUpperCase(),className:`method-level method-level-${badgeToken(level)}`};
  }
  if(state.kind==="exception")return{label:data.type||"Exception",className:"method-exception-type"};
  if(state.kind==="event")return{label:data.kind||"event",className:"method-event-kind"};
  return null;
}

function badgeToken(value){
  return String(value||"").toLowerCase().replace(/[^a-z0-9_-]/g,"");
}

function kindSingular(kind){
  const labels={request:"Request",middleware:"Middleware",query:"Query",cache:"Cache",job:"Job",email:"Mail",log:"Log",http_call:"HTTP",schedule:"Schedule",exception:"Exception",event:"Event"};
  return labels[kind]||kind;
}

function eventPreview(event){
  const data=event.data||{};
  if(event.kind==="request")return`<span class="event-main entity-preview"><strong>${escapeHTML(requestTarget(data))}</strong>${inlineTags(event.tags)}</span>`;
  let content;
  if(state.kind==="middleware")content={title:data.name||"Middleware",subtitle:"Inclusive timing"};
  else if(state.kind==="query")content={title:compactQuery(data.sql||""),full:data.sql||"",subtitle:data.connection||data.driver||"default",code:true};
  else if(state.kind==="cache")content={title:data.key||"Cache operation",subtitle:data.store||"default",code:true};
  else if(state.kind==="job")content={title:data.name||"Job",subtitle:[data.connection,data.attempt?`attempt ${data.attempt}`:""].filter(Boolean).join(" · ")||"Queued job"};
  else if(state.kind==="email")content={title:data.subject||"Email",subtitle:(data.to||[]).map(address).join(", ")||"No recipients"};
  if(state.kind==="log"){
    const fieldCount=Object.keys(data.fields||{}).length;
    content={title:data.message||"Log entry",subtitle:fieldCount?`${fieldCount} structured ${plural(fieldCount,"field","fields")}`:"Plain log message"};
  }
  else if(state.kind==="http_call")content={title:data.url||"HTTP call",subtitle:data.status?`Status ${data.status}`:"Outgoing request"};
  else if(state.kind==="schedule")content={title:data.name||"Scheduled task",subtitle:data.payload?"Payload captured":"No payload"};
  else if(state.kind==="exception")content={title:data.message||"Exception",subtitle:data.stack?"Stack captured":"No stack captured"};
  else if(state.kind==="event")content={title:data.name||data.summary||"Event",subtitle:data.summary&&data.summary!==data.name?data.summary:"Application event"};
  return entityPreview(content||relationContent(event.kind,event,data),"event-main",event.tags,false);
}

function entityPreview(content,className="",tags,showSubtitle=true){
  return`<span class="entity-preview ${className}">${content.code?`<code>${escapeHTML(content.title)}</code>`:`<strong>${escapeHTML(content.title)}</strong>`}${showSubtitle&&content.subtitle?`<small>${escapeHTML(content.subtitle)}</small>`:""}${inlineTags(tags)}</span>`;
}

function renderDetail(event){
  if(!event){
    elements.detail.innerHTML='<div class="detail-empty"><span class="empty-mark">⌁</span><strong>No event selected</strong><span>Choose an entry to inspect its complete request context.</span></div>';
    return;
  }
  const group=groupFor(event);
  elements.back.innerHTML=`<span aria-hidden="true">←</span> Back to ${escapeHTML(kindLabel(state.kind).toLowerCase())}`;
  if(event.kind==="query"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${queryDetailsCard(event,group.request)}${querySQLCard(event)}</div>`;
    return;
  }
  if(event.kind==="cache"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${cacheDetailsCard(event,group.request)}${cacheValueCard(event)}</div>`;
    return;
  }
  if(event.kind!=="request"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${entityDetailsCard(event,group.request)}${entityContentCards(event)}</div>`;
    return;
  }
  const relatedTabs=relatedDefinitions(group);
  if(!relatedTabs.some(tab=>tab.key===state.relatedTab))state.relatedTab=relatedTabs[0].key;
  elements.detail.innerHTML=`
    <div class="detail-body telescope-stack">
      ${requestDetailsCard(event)}
      ${requestCard(event)}
      ${responseCard(event)}
      ${relatedCard(group,event,relatedTabs)}
    </div>`;
}

function groupFor(event){
  const requestID=event.kind==="request"?event.id:event.request_id;
  if(!requestID)return{request:null,events:[event],byKind:new Map([[event.kind,[event]]])};
  const events=visibleEvents().filter(item=>item.id===requestID||item.request_id===requestID).sort((a,b)=>Date.parse(a.started_at)-Date.parse(b.started_at));
  const byKind=new Map();
  for(const item of events){
    if(!byKind.has(item.kind))byKind.set(item.kind,[]);
    byKind.get(item.kind).push(item);
  }
  return{request:events.find(item=>item.kind==="request")||null,events,byKind};
}

function relatedDefinitions(group){
  const definitions=[
    ["middleware","Middleware","middleware"],
    ["query","Queries","query"],
    ["cache","Cache","cache"],
    ["log","Logs","log"],
    ["job","Jobs","job"],
    ["email","Mail","email"],
    ["http_call","HTTP Client","http_call"],
    ["schedule","Schedules","schedule"],
    ["exception","Exceptions","exception"],
    ["event","Events","event"]
  ];
  const tabs=definitions.filter(([, ,kind])=>(group.byKind.get(kind)||[]).length).map(([key,label,kind])=>({key,label,count:(group.byKind.get(kind)||[]).length}));
  tabs.push({key:"timeline",label:"Timeline",count:group.events.length},{key:"raw",label:"Raw",count:undefined});
  return tabs;
}

function firstRelatedTab(group){
  return relatedDefinitions(group)[0].key;
}

function requestDetailsCard(request){
  const data=request.data||{};
  const status=statusFor(request);
  const facts=[
    ["Time",`${new Date(request.started_at).toLocaleString()} (${relativeTime(request.started_at)})`],
    ["Hostname",data.host||"—"],
    ["Method",`<span class="method method-${escapeHTML((data.method||"http").toLowerCase())}">${escapeHTML(data.method||"HTTP")}</span>`,true],
    ["Route",data.route||"—"],
    ["Path",requestTarget(data)],
    ["Status",`<span class="state ${status.className}">${escapeHTML(status.label)}</span>`,true],
    ["Duration",duration(request.duration_ns)],
    ["IP Address",data.remote_ip||"—"],
    ["Protocol",data.protocol||"—"],
    ["Request size",bytes(data.request_size)],
    ["Response size",bytes(data.response_size)]
  ];
  if(request.tags&&Object.keys(request.tags).length)facts.push(["Tags",tagList(request.tags),true]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>Request Details</h3><span>${escapeHTML(request.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function queryDetailsCard(query,request){
  const data=query.data||{};
  const facts=[
    ["Time",`${new Date(query.started_at).toLocaleString()} (${relativeTime(query.started_at)})`],
    ["Connection",data.connection||"default"],
    ["Driver",data.driver||"—"],
    ["Database",data.database||"—"],
    ["Operation",data.operation||"SQL"],
    ["Duration",duration(query.duration_ns)]
  ];
  if(data.rows_affected!==undefined&&data.rows_affected!==null)facts.push(["Rows affected",data.rows_affected]);
  facts.push(["Request",request?requestLink(request):"Standalone",Boolean(request)],["Tags",tagList(query.tags),Boolean(query.tags&&Object.keys(query.tags).length)]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>Query Details</h3><span>${escapeHTML(query.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function querySQLCard(query){
  return`<section class="detail-section telescope-card"><nav class="card-tabs query-tabs" aria-label="SQL query"><span class="card-tab active">Query</span><button type="button" class="copy-button" data-copy-query aria-label="Copy SQL" title="Copy SQL"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 8h9v11H9zM6 16H4V5h9v3"/></svg><span>Copied</span></button></nav><div class="card-panel">${sqlPanel((query.data||{}).sql||"")}</div></section>`;
}

function cacheDetailsCard(cache,request){
  const data=cache.data||{};
  const status=statusFor(cache);
  const facts=[
    ["Time",`${new Date(cache.started_at).toLocaleString()} (${relativeTime(cache.started_at)})`],
    ["Store",data.store||"default"],
    ["Operation",data.operation||"cache"],
    ["Result",`<span class="state ${status.className}">${escapeHTML(status.label)}</span>`,true],
    ["Key",`<code class="inline-code">${escapeHTML(data.key||"—")}</code>`,true],
    ["Duration",duration(cache.duration_ns)],
    ["TTL",data.ttl_ns?duration(data.ttl_ns):"—"],
    ["Size",data.size?bytes(data.size):"—"],
    ["Truncated",data.truncated?"Yes":"No"],
    ["Request",request?requestLink(request):"Standalone",Boolean(request)],
    ["Tags",tagList(cache.tags),Boolean(cache.tags&&Object.keys(cache.tags).length)]
  ];
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>Cache Details</h3><span>${escapeHTML(cache.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function cacheValueCard(cache){
  const data=cache.data||{};
  const value=formatCapturedValue(data.value||"");
  const message=data.hit?"The cache integration did not capture a value for this hit.":"No value was returned for this cache operation.";
  return`<section class="detail-section telescope-card"><nav class="card-tabs query-tabs" aria-label="Cache value"><span class="card-tab active">Value</span>${data.truncated?'<span class="value-truncated">truncated</span>':""}</nav><div class="card-panel">${codePanel(value,data.truncated,message)}</div></section>`;
}

function formatCapturedValue(value){
  if(!value)return"";
  try{
    return JSON.stringify(JSON.parse(value),null,2);
  }catch{
    return value;
  }
}

function tagList(tags){
  if(!tags||!Object.keys(tags).length)return"—";
  return`<span class="tag-list">${Object.entries(tags).map(([name,value])=>{const token=`${name}=${value}`;return`<button type="button" data-watch-tag="${escapeHTML(token)}" title="Watch ${escapeHTML(token)}">${escapeHTML(name)}${value?`: ${escapeHTML(value)}`:""}</button>`}).join("")}</span>`;
}

function inlineTags(tags){
  if(!tags||!Object.keys(tags).length)return"";
  return`<span class="inline-tags">${Object.entries(tags).slice(0,3).map(([name,value])=>`<i>${escapeHTML(name)}${value?`=${escapeHTML(value)}`:""}</i>`).join("")}</span>`;
}

function requestLink(request){
  const data=request.data||{};
  return`<button type="button" class="entity-reference" data-event-id="${escapeHTML(request.id)}"><span class="method method-${escapeHTML((data.method||"http").toLowerCase())}">${escapeHTML(data.method||"HTTP")}</span><span><strong>${escapeHTML(requestTarget(data))}</strong><small>View parent request</small></span><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2.5a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15ZM8.3 6.7l3.6 3.3-3.6 3.3M11.7 10H6.2"/></svg></button>`;
}

function detailTitle(kind){
  return{middleware:"Middleware Details",job:"Job Details",email:"Mail Details",log:"Log Details",http_call:"HTTP Client Details",schedule:"Schedule Details",exception:"Exception Details",event:"Event Details"}[kind]||`${kindLabel(kind)} Details`;
}

function entityDetailsCard(event,request){
  const data=event.data||{};
  const status=statusFor(event);
  const facts=[["Time",`${new Date(event.started_at).toLocaleString()} (${relativeTime(event.started_at)})`]];
  if(event.kind==="middleware")facts.push(["Middleware",data.name||"—"],["State",`<span class="state ${status.className}">${escapeHTML(data.state||status.label)}</span>`,true]);
  if(event.kind==="job")facts.push(["Job",data.name||"—"],["Queue",data.queue||"default"],["Connection",data.connection||"—"],["State",`<span class="state ${status.className}">${escapeHTML(data.state||status.label)}</span>`,true],["Attempt",data.attempt?`${data.attempt}${data.max_attempts?` of ${data.max_attempts}`:""}`:"—"],["Wait",data.wait_ns?duration(data.wait_ns):"—"],["Available at",formatDateTime(data.available_at)]);
  if(event.kind==="email")facts.push(["Transport",data.transport||"—"],["From",address(data.from)||"—"],["To",(data.to||[]).map(address).join(", ")||"—"],["CC",(data.cc||[]).map(address).join(", ")||"—"],["BCC",(data.bcc||[]).map(address).join(", ")||"—"],["Subject",data.subject||"—"],["Status",`<span class="state ${status.className}">${escapeHTML(data.status||status.label)}</span>`,true]);
  if(event.kind==="log")facts.push(["Level",`<span class="state ${status.className}">${escapeHTML((data.level||"log").toUpperCase())}</span>`,true],["Message",data.message||"—"]);
  if(event.kind==="http_call")facts.push(["Method",`<span class="method method-${escapeHTML((data.method||"http").toLowerCase())}">${escapeHTML(data.method||"HTTP")}</span>`,true],["URL",data.url||"—"],["Status",`<span class="state ${status.className}">${escapeHTML(status.label)}</span>`,true],["Response size",bytes(data.response_size||0)]);
  if(event.kind==="schedule")facts.push(["Task",data.name||"—"],["State",`<span class="state ${status.className}">${escapeHTML(data.state||status.label)}</span>`,true],["Planned at",formatDateTime(data.planned_at)]);
  if(event.kind==="exception")facts.push(["Type",data.type||"Exception"],["Message",data.message||"—"],["Status",`<span class="state error">Error</span>`,true]);
  if(event.kind==="event")facts.push(["Kind",data.kind||"event"],["Name",data.name||"—"],["Status",`<span class="state ${status.className}">${escapeHTML(data.status||status.label)}</span>`,true],["Summary",data.summary||"—"]);
  facts.push(["Duration",duration(event.duration_ns)]);
  if(request)facts.push(["Request",requestLink(request),true]);
  else facts.push(["Request","Standalone"]);
  if(event.process)facts.push(["Process",event.process]);
  if(event.instance)facts.push(["Instance",event.instance]);
  if(event.tags&&Object.keys(event.tags).length)facts.push(["Tags",tagList(event.tags),true]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>${escapeHTML(detailTitle(event.kind))}</h3><span>${escapeHTML(event.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function entityContentCards(event){
  const data=event.data||{};
  const cards=[];
  if(event.kind==="job")cards.push(entityCodeCard("Arguments",data.arguments,"No job arguments were captured."));
  if(event.kind==="email"){
    if(data.text)cards.push(entityCodeCard("Text",data.text,"No text body was captured.",false));
    if(data.html)cards.push(entityCodeCard("HTML",data.html,"No HTML body was captured.",false));
  }
  if(event.kind==="log"){
    cards.push(entityCodeCard("Fields",data.fields,"No structured fields were captured."));
    if(data.stack)cards.push(entityCodeCard("Stack",data.stack,"No stack was captured.",false));
  }
  if(event.kind==="http_call"){
    cards.push(entityMessageCard("Request",data.request));
    cards.push(entityMessageCard("Response",data.response));
  }
  if(event.kind==="schedule"){
    cards.push(entityCodeCard("Payload",data.payload,"No schedule payload was captured."));
    if(data.panic||data.error)cards.push(entityCodeCard(data.panic?"Panic":"Error",data.panic||data.error,"No failure details were captured.",false));
  }
  if(event.kind==="exception")cards.push(entityCodeCard("Stack",data.stack,"No stack was captured.",false));
  if(event.kind==="event")cards.push(entityCodeCard("Fields",data.fields,"No event fields were captured."));
  return cards.join("");
}

function entityCodeCard(title,value,emptyMessage,json=true){
  const content=value&&json?JSON.stringify(value,null,2):value||"";
  return`<section class="detail-section telescope-card"><nav class="card-tabs query-tabs" aria-label="${escapeHTML(title)}"><span class="card-tab active">${escapeHTML(title)}</span></nav><div class="card-panel">${codePanel(content,false,emptyMessage)}</div></section>`;
}

function entityMessageCard(title,message={}){
  const body=message.body||"";
  const headerPanel=headersPanel(message.headers,"No headers were captured.");
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>${escapeHTML(title)}</h3><span>${escapeHTML(message.content_type||"")}</span></div><div class="entity-message-grid"><div><span>Body</span>${codePanel(body,message.truncated,`No ${title.toLowerCase()} body was captured.`)}</div><div><span>Headers</span>${headerPanel}</div></div></section>`;
}

function requestCard(request){
  const data=request.data||{};
  const message=data.request||{};
  let panel;
  if(state.requestTab==="headers")panel=headersPanel(message.headers,"No request headers were captured.");
  else if(state.requestTab==="raw")panel=codePanel(rawHTTPRequest(request),message.truncated,"No raw request could be generated.");
  else if(state.requestTab==="curl")panel=codePanel(curlCommand(request),message.truncated,"No cURL command could be generated.");
  else panel=codePanel(requestPayload(data,message),message.truncated,"No request payload was captured.");
  const tabs=[{key:"payload",label:"Payload"},{key:"headers",label:"Headers",count:headerCount(message.headers)},{key:"raw",label:"Raw"},{key:"curl",label:"cURL"}];
  return tabbedCard("Request",tabs,state.requestTab,panel,requestCopyButton(state.requestTab));
}

function responseCard(request){
  const message=(request.data||{}).response||{};
  const panel=state.responseTab==="headers"?headersPanel(message.headers,"No response headers were captured."):codePanel(message.body||"",message.truncated,"No textual response body was captured.");
  return tabbedCard("Response",[{key:"response",label:"Response"},{key:"headers",label:"Headers",count:headerCount(message.headers)}],state.responseTab,panel);
}

function relatedCard(group,event,tabs){
  let panel;
  if(state.relatedTab==="timeline")panel=timeline(group.events);
  else if(state.relatedTab==="raw")panel=rawBlock(event.data);
  else panel=relatedCollection(state.relatedTab,group.byKind.get(state.relatedTab)||[]);
  return tabbedCard("Related",tabs,state.relatedTab,panel);
}

function tabbedCard(group,tabs,active,panel,action=""){
  const key=group.toLowerCase();
  return`<section class="detail-section telescope-card"><nav class="card-tabs" aria-label="${escapeHTML(group)} details">${tabs.map(tab=>`<button type="button" data-card-tab="${key}:${tab.key}" class="card-tab${active===tab.key?" active":""}">${escapeHTML(tab.label)}${tab.count===undefined?"":` <span>(${tab.count})</span>`}</button>`).join("")}${action}</nav><div class="card-panel">${panel}</div></section>`;
}

function codePanel(value,truncated,emptyMessage){
  if(!value)return`<div class="panel-empty">${escapeHTML(emptyMessage)}</div>`;
  const code=highlightedCode(value);
  return`${truncated?'<div class="truncated-strip">Captured body was truncated</div>':""}<pre${code.json?' class="json"':""}><code>${code.html}</code></pre>`;
}

function highlightedCode(value){
  const source=String(value);
  try{
    const formatted=JSON.stringify(JSON.parse(source),null,2);
    return{json:true,html:highlightJSON(formatted)};
  }catch{
    return{json:false,html:escapeHTML(source)};
  }
}

function highlightJSON(json){
  const pattern=/("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false)\b|\b(null)\b|([{}\[\],:])/g;
  let output="";
  let cursor=0;
  for(const match of json.matchAll(pattern)){
    output+=escapeHTML(json.slice(cursor,match.index));
    if(match[1]){
      output+=`<span class="${match[2]?"json-key":"json-string"}">${escapeHTML(match[1])}</span>`;
      if(match[2])output+=`<span class="json-punctuation">${escapeHTML(match[2])}</span>`;
    }else if(match[3])output+=`<span class="json-number">${escapeHTML(match[3])}</span>`;
    else if(match[4])output+=`<span class="json-boolean">${escapeHTML(match[4])}</span>`;
    else if(match[5])output+=`<span class="json-null">${escapeHTML(match[5])}</span>`;
    else output+=`<span class="json-punctuation">${escapeHTML(match[6])}</span>`;
    cursor=match.index+match[0].length;
  }
  return output+escapeHTML(json.slice(cursor));
}

function headersPanel(headers,emptyMessage){
  if(!headers||!Object.keys(headers).length)return`<div class="panel-empty">${escapeHTML(emptyMessage)}</div>`;
  const rows=Object.entries(headers).sort(([a],[b])=>a.localeCompare(b)).map(([name,values])=>`<tr><th>${escapeHTML(name)}</th><td>${escapeHTML(headerValue(values))}</td></tr>`).join("");
  return`<div class="table-wrap"><table class="headers"><tbody>${rows}</tbody></table></div>`;
}

function headerCount(headers){
  return headers?Object.keys(headers).length:0;
}

function requestPayload(data,message){
  if(message.body)return message.body;
  if(!data.query)return"";
  const params=new URLSearchParams(data.query);
  return JSON.stringify(Object.fromEntries(params.entries()),null,2);
}

function requestCopyButton(format){
  const label=requestFormatLabel(format);
  return`<button type="button" class="copy-button" data-copy-request="${escapeHTML(format)}" aria-label="Copy ${escapeHTML(label)}" title="Copy ${escapeHTML(label)}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 8h9v11H9zM6 16H4V5h9v3"/></svg><span>Copied</span></button>`;
}

function requestFormatLabel(format){
  return{payload:"payload",headers:"headers",raw:"raw HTTP",curl:"cURL"}[format]||"request";
}

function requestRepresentation(request,format){
  if(!request)return"";
  const data=request.data||{};
  const message=data.request||{};
  if(format==="headers")return requestHeadersText(data,false);
  if(format==="raw")return rawHTTPRequest(request);
  if(format==="curl")return curlCommand(request);
  return requestPayload(data,message);
}

function requestHeadersText(data,includeHost){
  const headers=data.request?.headers||{};
  const lines=[];
  if(includeHost&&data.host&&!Object.keys(headers).some(name=>name.toLowerCase()==="host"))lines.push(`Host: ${data.host}`);
  for(const [name,values] of Object.entries(headers).sort(([left],[right])=>left.localeCompare(right))){
    for(const value of Array.isArray(values)?values:[values])lines.push(`${name}: ${value}`);
  }
  return lines.join("\r\n");
}

function absoluteRequestURL(data){
  const scheme=data.scheme||"http";
  const host=data.host||"localhost";
  const target=requestTarget(data);
  return`${scheme}://${host}${target.startsWith("/")?"":"/"}${target}`;
}

function rawHTTPRequest(request){
  const data=request?.data||{};
  const message=data.request||{};
  const head=[`${data.method||"GET"} ${requestTarget(data)} ${data.protocol||"HTTP/1.1"}`,requestHeadersText(data,true)].filter(Boolean).join("\r\n");
  return message.body?`${head}\r\n\r\n${message.body}`:`${head}\r\n\r\n`;
}

function curlCommand(request){
  const data=request?.data||{};
  const message=data.request||{};
  const parts=[`curl --request ${shellQuote(data.method||"GET")}`,`--url ${shellQuote(absoluteRequestURL(data))}`];
  for(const [name,values] of Object.entries(message.headers||{}).sort(([left],[right])=>left.localeCompare(right))){
    if(["content-length","host"].includes(name.toLowerCase()))continue;
    for(const value of Array.isArray(values)?values:[values])parts.push(`--header ${shellQuote(`${name}: ${value}`)}`);
  }
  if(message.body)parts.push(`--data-raw ${shellQuote(message.body)}`);
  return parts.join(" \\\n  ");
}

function shellQuote(value){
  return`'${String(value).replace(/'/g,`'"'"'`)}'`;
}

function relatedCollection(kind,events){
  if(!events.length)return'<div class="detail-empty compact"><strong>No related entries</strong></div>';
  const summary=relationSummary(kind,events);
  const className=summary.status?" with-status":"";
  return`<section class="relation-list${className}"><header class="relation-heading"><div><strong>${escapeHTML(summary.title)}</strong><small>${escapeHTML(summary.description)}</small></div>${summary.status?`<div class="relation-heading-status"><strong>${escapeHTML(summary.status.title)}</strong><small>${escapeHTML(summary.status.description)}</small></div>`:""}<div class="relation-heading-duration"><strong>Duration</strong><small>${escapeHTML(duration(summary.duration))}</small></div><span></span></header><div class="relation-rows">${events.map(event=>relationRow(kind,event,summary.status!==null)).join("")}</div></section>`;
}

function relationSummary(kind,events){
  const totalDuration=events.reduce((total,event)=>total+eventDuration(event),0);
  if(kind==="query"){
    const unique=new Set(events.map(event=>normalizeQuery((event.data||{}).sql||""))).size;
    const duplicated=Math.max(0,events.length-unique);
    return{title:"Query",description:`${events.length} ${plural(events.length,"query","queries")}, ${duplicated} duplicated.`,status:null,duration:totalDuration};
  }
  if(kind==="cache"){
    const hits=events.filter(event=>(event.data||{}).hit).length;
    return{title:"Cache operation",description:`${events.length} ${plural(events.length,"operation","operations")}.`,status:{title:"Result",description:events.length?`${Math.round(hits/events.length*100)}% hit rate`:"—"},duration:totalDuration};
  }
  const failures=events.filter(isFailure).length;
  return{title:kindLabel(kind).replace(/s$/,""),description:`${events.length} recorded ${plural(events.length,"entry","entries")}.`,status:{title:"Status",description:failures?`${failures} failed`:"No failures"},duration:totalDuration};
}

function relationRow(kind,event,withStatus){
  const data=event.data||{};
  const status=statusFor(event);
  const content=relationContent(kind,event,data);
  return`<button type="button" class="relation-row${withStatus?" with-status":""}" data-event-id="${escapeHTML(event.id)}" title="${escapeHTML(content.full||content.title)}">${entityPreview(content,"relation-primary",event.tags)}${withStatus?`<span class="relation-result"><span class="state ${status.className}">${escapeHTML(status.label)}</span></span>`:""}<span class="relation-duration">${escapeHTML(duration(event.duration_ns))}</span>${rowAction()}</button>`;
}

function rowAction(){
  return`<span class="relation-action" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="M10 2.5a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15ZM8.3 6.7l3.6 3.3-3.6 3.3M11.7 10H6.2"/></svg></span>`;
}

function relationContent(kind,event,data){
  if(kind==="middleware")return{title:data.name||"Middleware",subtitle:`${data.state||"completed"} · inclusive timing`};
  if(kind==="query")return{title:compactQuery(data.sql||""),full:data.sql||"",subtitle:[data.operation||"SQL",data.connection||data.driver||"default"].join(" · "),code:true};
  if(kind==="cache")return{title:data.key||"Cache operation",subtitle:[data.operation||"cache",data.store||"default"].join(" · "),code:true};
  if(kind==="log")return{title:data.message||"Log entry",subtitle:(data.level||"log").toUpperCase()};
  if(kind==="job")return{title:data.name||"Job",subtitle:[data.queue||"default",data.state||"recorded"].join(" · ")};
  if(kind==="email")return{title:data.subject||"Email",subtitle:[data.transport||"mail",(data.to||[]).map(address).join(", ")||"no recipients"].join(" · ")};
  if(kind==="http_call")return{title:`${data.method||"HTTP"} ${data.url||""}`,subtitle:data.status?`Status ${data.status}`:"Outgoing request"};
  if(kind==="schedule")return{title:data.name||"Scheduled task",subtitle:data.state||"recorded"};
  if(kind==="exception")return{title:data.message||"Exception",subtitle:data.type||"Exception"};
  return{title:data.name||data.summary||kindLabel(kind),subtitle:[data.kind,data.status].filter(Boolean).join(" · ")||"Application event"};
}

function compactQuery(sql){
  const value=sql.replace(/\s+/g," ").trim();
  return value.length>150?`${value.slice(0,147)}…`:value||"SQL query";
}

function normalizeQuery(sql){
  return sql.replace(/\s+/g," ").trim().toLowerCase();
}

function plural(count,singular,pluralValue){
  return count===1?singular:pluralValue;
}

function timeline(events){
  if(!events.length)return'<div class="detail-empty compact"><strong>No timeline events</strong></div>';
  const first=Date.parse(events[0].started_at);
  return`<div class="timeline">${events.map(event=>{
    const offset=Math.max(0,Date.parse(event.started_at)-first);
    return`<button type="button" class="timeline-row" data-event-id="${escapeHTML(event.id)}"><span class="timeline-rail"><i data-kind="${escapeHTML(event.kind)}"></i></span><span class="timeline-copy"><strong>${escapeHTML(title(event))}</strong><small>${escapeHTML(kindLabel(event.kind))} · +${offset.toFixed(1)} ms</small></span><span class="duration">${duration(event.duration_ns)}</span></button>`;
  }).join("")}</div>`;
}

function rawBlock(value){
  return`<pre class="json"><code>${highlightJSON(JSON.stringify(value??{},null,2))}</code></pre>`;
}

function sqlPanel(sql){
  if(!sql)return'<div class="panel-empty">No SQL text was captured.</div>';
  return`<pre class="sql"><code>${highlightSQL(formatSQL(sql))}</code></pre>`;
}

function formatSQL(sql){
  const pattern=/(\s+|--[^\n]*|#[^\n]*|\/\*[\s\S]*?\*\/|'(?:''|\\.|[^'])*'|"(?:""|\\.|[^"])*"|`(?:``|[^`])*`|\b\d+(?:\.\d+)?\b|\b[A-Za-z_][A-Za-z0-9_$]*\b|<>|<=|>=|!=|:=|[-+*\/%=<>().,;])/g;
  const tokens=[];
  let cursor=0;
  for(const match of sql.matchAll(pattern)){
    if(match.index>cursor)tokens.push(sql.slice(cursor,match.index));
    if(!/^\s+$/.test(match[0]))tokens.push(match[0]);
    cursor=match.index+match[0].length;
  }
  if(cursor<sql.length)tokens.push(sql.slice(cursor));
  const breaks=new Set(["SELECT","FROM","WHERE","GROUP","HAVING","ORDER","LIMIT","OFFSET","RETURNING","UNION","INTERSECT","EXCEPT","VALUES","SET","JOIN","LEFT","RIGHT","FULL","INNER","CROSS","AND","OR"]);
  let output="";
  let depth=0;
  let previous="";
  const newline=()=>{
    output=output.trimEnd();
    if(output&&!output.endsWith("\n"))output+="\n";
    output+="  ".repeat(depth);
  };
  for(const token of tokens){
    const upper=token.toUpperCase();
    const isComment=token.startsWith("--")||token.startsWith("#")||token.startsWith("/*");
    if(isComment)newline();
    if(breaks.has(upper)&&output.trim()&&previous!=="("&&!(upper==="JOIN"&&["LEFT","RIGHT","FULL","INNER","CROSS"].includes(previous.toUpperCase())))newline();
    if(token===")")depth=Math.max(0,depth-1);
    const noSpace=token==="."||token===","||token===")"||token===";"||previous==="("||previous==="."||output.endsWith("\n")||!output;
    if(!noSpace&&!output.endsWith(" "))output+=" ";
    output+=token;
    if(token==="(")depth++;
    if(token===","&&depth===0)newline();
    if(isComment)newline();
    previous=token;
  }
  return output.trim();
}

function highlightSQL(sql){
  const pattern=/(\s+|--[^\n]*|#[^\n]*|\/\*[\s\S]*?\*\/|'(?:''|\\.|[^'])*'|"(?:""|\\.|[^"])*"|`(?:``|[^`])*`|\b\d+(?:\.\d+)?\b|\b[A-Za-z_][A-Za-z0-9_$]*\b|<>|<=|>=|!=|:=|[-+*\/%=<>().,;])/g;
  let output="";
  let cursor=0;
  for(const match of sql.matchAll(pattern)){
    output+=escapeHTML(sql.slice(cursor,match.index));
    output+=sqlToken(match[0]);
    cursor=match.index+match[0].length;
  }
  return output+escapeHTML(sql.slice(cursor));
}

function sqlToken(token){
  if(/^\s+$/.test(token))return escapeHTML(token);
  if(token.startsWith("--")||token.startsWith("#")||token.startsWith("/*"))return`<span class="sql-comment">${escapeHTML(token)}</span>`;
  if(token.startsWith("'"))return`<span class="sql-string">${escapeHTML(token)}</span>`;
  if(token.startsWith("`")||token.startsWith('"'))return`<span class="sql-identifier">${escapeHTML(token)}</span>`;
  if(/^\d/.test(token))return`<span class="sql-number">${escapeHTML(token)}</span>`;
  if(sqlKeywords.has(token.toUpperCase()))return`<span class="sql-keyword">${escapeHTML(token)}</span>`;
  if(/^[-+*\/%=<>().,;]+$/.test(token))return`<span class="sql-operator">${escapeHTML(token)}</span>`;
  return escapeHTML(token);
}

function copyText(value){
  if(navigator.clipboard?.writeText)return navigator.clipboard.writeText(value);
  const input=document.createElement("textarea");
  input.value=value;
  input.setAttribute("readonly","");
  input.style.position="fixed";
  input.style.opacity="0";
  document.body.append(input);
  input.select();
  const copied=document.execCommand("copy");
  input.remove();
  return copied?Promise.resolve():Promise.reject(new Error("copy failed"));
}

function showCopied(button,idleLabel){
  button.classList.add("copied");
  button.setAttribute("aria-label","Copied");
  window.setTimeout(()=>{
    button.classList.remove("copied");
    button.setAttribute("aria-label",idleLabel);
  },1200);
}

function kindCount(kind){
  const events=visibleEvents();
  if(!kind)return events.length;
  let count=0;
  for(const event of events)if(event.kind===kind)count++;
  return count;
}

function navIcon(kind){
  const paths={dashboard:'<path d="M4 13h6v7H4zM14 4h6v16h-6zM4 4h6v5H4z"/>',request:'<path d="M4 5h16v14H4zM8 9h8M8 13h5"/>',middleware:'<path d="M5 4h14v5H5zM5 15h14v5H5zM8 9v6M16 9v6"/>',query:'<ellipse cx="12" cy="6" rx="7" ry="3"/><path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6"/>',cache:'<path d="M5 8h14v11H5zM8 5h8v3M9 12h6M9 15h4"/>',job:'<path d="M4 7h16v12H4zM9 7V4h6v3M4 11h16M10 11v2h4v-2"/>',email:'<path d="M3 6h18v13H3zM3 7l9 7 9-7"/>',log:'<path d="M6 3h9l4 4v14H6zM15 3v5h4M9 12h6M9 16h6"/>',http_call:'<path d="M8 12h8M13 8l4 4-4 4M5 5h14v14H5"/>',schedule:'<circle cx="12" cy="13" r="8"/><path d="M12 9v5l3 2M9 3h6"/>',exception:'<path d="M12 3l10 18H2zM12 9v5M12 18h.01"/>',event:'<path d="M12 3l2.2 5.8L20 11l-5.8 2.2L12 19l-2.2-5.8L4 11l5.8-2.2z"/>','':'<path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"/>'};
  return`<svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true">${paths[kind]||paths[""]}</svg>`;
}

function kindLabel(kind){
  const labels={dashboard:"Dashboard",request:"Requests",middleware:"Middleware",query:"Queries",cache:"Cache",job:"Jobs",email:"Mail",log:"Logs",http_call:"HTTP calls",schedule:"Schedules",exception:"Exceptions",event:"Events","":"All events"};
  return labels[kind]||kind;
}

function requestTarget(data){
  const path=data.path||data.route||"/";
  return data.query?`${path}?${data.query}`:path;
}

function title(event){
  const data=event.data||{};
  if(event.kind==="request")return`${data.method||"HTTP"} ${requestTarget(data)}`;
  if(event.kind==="middleware")return data.name||"middleware";
  if(event.kind==="query")return`${data.operation||"SQL"} ${short(data.sql||"")}`;
  if(event.kind==="cache")return`${data.operation||"cache"} ${data.key||""}`;
  if(event.kind==="job")return data.name||"job";
  if(event.kind==="email")return data.subject||"email";
  if(event.kind==="log")return data.message||"log";
  if(event.kind==="http_call")return`${data.method||"HTTP"} ${data.url||""}`;
  if(event.kind==="schedule")return data.name||"schedule";
  if(event.kind==="exception")return data.message||"exception";
  return data.name||data.summary||event.kind;
}

function statusFor(event){
  const data=event.data||{};
  const textStatus=typeof data.status==="string"?data.status.toLowerCase():"";
  const logLevel=event.kind==="log"?String(data.level||"").toLowerCase():"";
  if(["error","fatal","panic"].includes(logLevel)||["failed","failure","bounced","rejected","panicked"].includes(textStatus))return{label:data.status||data.level||"Error",className:"error"};
  if(["warn","warning"].includes(logLevel)||["warning","retrying","degraded"].includes(textStatus))return{label:data.status||data.level||"Warning",className:"warning"};
  if(data.error||event.kind==="exception"||data.status>=500||["failed","dispatch_failed","panicked"].includes(data.state))return{label:data.status?String(data.status):"Error",className:"error"};
  if(data.status>=400)return{label:String(data.status),className:"error"};
  if(data.state)return{label:data.state,className:["succeeded","completed"].includes(data.state)?"ok":""};
  if(event.kind==="cache")return{label:data.hit?"Hit":"Miss",className:data.hit?"ok":"warning"};
  return{label:data.status?String(data.status):"OK",className:"ok"};
}

function normalizedLogLevel(value){
  const level=String(value||"").trim().toLowerCase();
  return level==="warning"?"warn":level;
}

function address(value){
  if(!value)return"";
  return value.name?`${value.name} <${value.email}>`:value.email||"";
}

function headerValue(value){
  if(Array.isArray(value))return value.join(", ");
  return value??"";
}

function duration(ns=0){
  if(!ns)return"0 ms";
  if(ns>=1e9)return`${(ns/1e9).toFixed(2)} s`;
  if(ns>=1e6)return`${(ns/1e6).toFixed(2)} ms`;
  if(ns>=1e3)return`${(ns/1e3).toFixed(1)} µs`;
  return`${ns} ns`;
}

function bytes(value=0){
  if(value<0)return"unknown";
  if(value>=1048576)return`${(value/1048576).toFixed(2)} MB`;
  if(value>=1024)return`${(value/1024).toFixed(1)} KB`;
  return`${value||0} B`;
}

function relativeTime(value){
  const elapsed=Math.max(0,Date.now()-Date.parse(value));
  if(elapsed<1000)return"now";
  if(elapsed<60000)return`${Math.floor(elapsed/1000)}s ago`;
  if(elapsed<3600000)return`${Math.floor(elapsed/60000)}m ago`;
  if(elapsed<86400000)return`${Math.floor(elapsed/3600000)}h ago`;
  return`${Math.floor(elapsed/86400000)}d ago`;
}

function short(value){
  return value.length>86?`${value.slice(0,83)}…`:value;
}

function escapeHTML(value){
  return String(value).replace(/[&<>'"]/g,character=>({"&":"&amp;","<":"&lt;",">":"&gt;","'":"&#39;",'"':"&quot;"})[character]);
}

bind();
renderKinds();
load();
