const state={events:new Map(),analyses:new Map(),analysisPending:new Set(),sessionMode:"live",kind:"request",selected:"",queryTab:"query",entityTab:"",requestTab:"payload",responseTab:"response",relatedTab:"query",screen:"dashboard",paused:false,pending:[],socket:null,reconnect:0,reconnectTimer:0,socketStableTimer:0,runtime:[],queueStats:{sources:[]},dashboard:{widgets:[]},dashboardHistory:new Map(),stats:{},hasMore:false,loadingMore:false,loadMoreError:"",virtualItems:[],virtualStart:-1,virtualEnd:-1,virtualFrame:0,watchedTags:new Set(),filters:{}};
const pageSize=250;
const virtualRowHeight=70;
const virtualOverscan=8;
const virtualPreloadRows=12;
const kinds=["request","middleware","query","cache","job","email","log","http_call","schedule","exception","event",""];
const navItems=["dashboard",...kinds];
const methodFilterKinds=new Set(["request","http_call"]);
const durationFilterKinds=new Set(["request","middleware","query","cache","job","email","http_call","schedule","event",""]);
const filterParamKeys=["method","status","operation","connection","database","result","store","queue","transport","host","level","state","type","kind"];
const sqlKeywords=new Set("ADD ALL ALTER ANALYZE AND AS ASC BETWEEN BY CASE CHECK COLUMN CONSTRAINT CREATE CROSS CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP DATABASE DEFAULT DELETE DESC DISTINCT DROP ELSE END EXISTS EXPLAIN FALSE FOREIGN FROM FULL GROUP HAVING IN INDEX INNER INSERT INTERSECT INTO IS JOIN KEY LEFT LIKE LIMIT NATURAL NOT NULL OFFSET ON OR ORDER OUTER PRIMARY REFERENCES RETURNING RIGHT SELECT SET TABLE THEN TRUE UNION UNIQUE UPDATE USING VALUES WHEN WHERE WITH".split(" "));
const ids=["login","workspace","login-form","login-error","token","socket-status","event-count","capacity-status","storage-note","import-session","session-file","export-session","pause","clear","tag-watcher","tag-watch-count","tag-watch-clear","tag-watch-search","tag-watch-selected","tag-watch-results-count","tag-watch-options","kinds","search","filter-toggle","filter-count","filter-clear","filters-drawer","filter-summary","entity-filters","time-filter","time-range","time-custom","time-from","time-to","duration-filter","duration","list-heading","events","empty","pagination","load-more","pagination-status","detail","dashboard-screen","index-screen","detail-screen","screen-title","back"];
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
    const [result,dashboard]=await Promise.all([api(`/api/events?limit=${pageSize}`),api("/api/dashboard")]);
    state.events.clear();
    state.analyses.clear();
    state.analysisPending.clear();
    state.sessionMode="live";
    for(const event of result.events)state.events.set(event.id,event);
    state.stats=result.stats||{};
    state.hasMore=Boolean(result.has_more);
    recordDashboardSnapshot(dashboard);
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
  elements["export-session"].addEventListener("click",exportSession);
  elements["import-session"].addEventListener("click",()=>elements["session-file"].click());
  elements["session-file"].addEventListener("change",importSession);
  elements.pause.addEventListener("click",togglePause);
  elements.clear.addEventListener("click",clearEvents);
  elements["load-more"].addEventListener("click",loadMoreEvents);
  elements.events.addEventListener("scroll",handleVirtualScroll,{passive:true});
  elements["filter-toggle"].addEventListener("click",toggleFilterDrawer);
  elements["filter-clear"].addEventListener("click",()=>{
    resetListFilters();
    applyListFilters();
  });
  elements.search.addEventListener("input",applyListFilters);
  elements.duration.addEventListener("change",applyListFilters);
  elements["time-range"].addEventListener("change",applyListFilters);
  elements["time-from"].addEventListener("change",applyListFilters);
  elements["time-to"].addEventListener("change",applyListFilters);
  elements["entity-filters"].addEventListener("change",event=>{
    const select=event.target.closest("select[data-filter-key]");
    if(!select)return;
    if(select.value)state.filters[select.dataset.filterKey]=select.value;
    else delete state.filters[select.dataset.filterKey];
    applyListFilters();
  });
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
    const harDownload=event.target.closest("[data-download-har]");
    if(harDownload){
      const request=state.events.get(state.selected);
      downloadText(`webpprof-${request?.id||"request"}.har`,requestHAR(request),"application/json");
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
    const replayCopy=event.target.closest("[data-copy-query-replay]");
    if(replayCopy){
      const query=state.events.get(state.selected);
      copyText(queryReplayCode(query)).then(()=>showCopied(replayCopy,"Copy Go replay")).catch(()=>replayCopy.setAttribute("aria-label","Could not copy replay"));
      return;
    }
    const planCopy=event.target.closest("[data-copy-query-plan]");
    if(planCopy){
      const query=state.events.get(state.selected);
      copyText(query?.data?.plan?.text||"").then(()=>showCopied(planCopy,"Copy EXPLAIN")).catch(()=>planCopy.setAttribute("aria-label","Could not copy EXPLAIN"));
      return;
    }
    const sourceCopy=event.target.closest("[data-copy-source]");
    if(sourceCopy){
      const query=state.events.get(state.selected);
      const frame=(query?.data?.callsite||[])[Number(sourceCopy.dataset.copySource)];
      const location=frame?`${frame.file}:${frame.line}`:"";
      copyText(location).then(()=>showCopied(sourceCopy,"Copy source location")).catch(()=>sourceCopy.setAttribute("aria-label","Could not copy source location"));
      return;
    }
    const tab=event.target.closest("[data-card-tab]");
    if(tab){
      const [group,value]=tab.dataset.cardTab.split(":");
      if(group==="request")state.requestTab=value;
      if(group==="response")state.responseTab=value;
      if(group==="related")state.relatedTab=value;
      if(group==="query")state.queryTab=value;
      if(group==="entity")state.entityTab=value;
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
  window.addEventListener("resize",scheduleVirtualRows,{passive:true});
  document.addEventListener("keydown",event=>{
    if(event.key==="Escape"&&!elements["filters-drawer"].classList.contains("hidden"))setFilterDrawer(false);
  });
  document.addEventListener("pointerdown",event=>{
    const drawer=elements["filters-drawer"];
    if(drawer.classList.contains("hidden")||drawer.contains(event.target)||elements["filter-toggle"].contains(event.target))return;
    setFilterDrawer(false);
  });
}

function exportSession(){
  const snapshot={
    format:"webpprof-session",
    version:1,
    exported_at:new Date().toISOString(),
    events:[...state.events.values()].sort((left,right)=>(left.cursor||0)-(right.cursor||0)),
    runtime:state.runtime,
    queue_stats:state.queueStats,
    stats:state.stats,
    dashboard:state.dashboard,
    analyses:Object.fromEntries(state.analyses)
  };
  downloadText(`webpprof-session-${fileTimestamp()}.json`,JSON.stringify(snapshot,null,2),"application/json");
}

async function importSession(event){
  const file=event.target.files?.[0];
  event.target.value="";
  if(!file)return;
  try{
    const snapshot=JSON.parse(await file.text());
    if(snapshot?.format!=="webpprof-session"||snapshot.version!==1||!Array.isArray(snapshot.events))throw new Error("unsupported session format");
    const imported=new Map();
    for(const [index,item] of snapshot.events.entries()){
      if(!item||typeof item.id!=="string"||typeof item.kind!=="string"||!item.data)continue;
      imported.set(item.id,{...item,cursor:Number(item.cursor)||index+1});
    }
    if(!imported.size)throw new Error("session contains no valid events");
    state.events=imported;
    state.analyses=new Map(Object.entries(snapshot.analyses&&typeof snapshot.analyses==="object"?snapshot.analyses:{}));
    state.analysisPending=new Set();
    state.sessionMode="imported";
    state.runtime=Array.isArray(snapshot.runtime)?snapshot.runtime:[];
    state.queueStats=snapshot.queue_stats&&typeof snapshot.queue_stats==="object"?snapshot.queue_stats:{sources:[]};
    state.dashboard=snapshot.dashboard&&typeof snapshot.dashboard==="object"?snapshot.dashboard:state.dashboard;
    state.dashboardHistory=new Map();
    recordDashboardSnapshot(state.dashboard);
    state.stats=snapshot.stats&&typeof snapshot.stats==="object"?snapshot.stats:{events:imported.size,storage:"imported"};
    state.hasMore=false;
    state.paused=true;
    state.pending=[];
    state.selected="";
    state.screen="dashboard";
    resetListFilters();
    updatePauseButton();
    renderTagWatcher();
    renderKinds();
    syncLocation();
    render();
  }catch(error){
    alert(`Could not import session: ${error.message}`);
  }
}

function downloadText(filename,value,type){
  const url=URL.createObjectURL(new Blob([value],{type}));
  const link=document.createElement("a");
  link.href=url;
  link.download=filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(()=>URL.revokeObjectURL(url),0);
}

function fileTimestamp(){
  return new Date().toISOString().replace(/[:.]/g,"-");
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
        resetListFilters();
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
      state.analyses.clear();
      state.analysisPending.clear();
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
  if(update.stats)state.stats=update.stats;
  reconcileLoadedEvents();
  if(update.runtime)recordRuntime(update.runtime);
  if(update.queues)state.queueStats=update.queues;
  if(update.dashboard)recordDashboardSnapshot(update.dashboard);
  updateCapacityStatus();
  if(state.screen==="dashboard")updateDashboard();
}

function reconcileLoadedEvents(){
  if(state.sessionMode!=="live")return;
  const retained=Number(state.stats.events);
  if(!Number.isFinite(retained)||retained<0||state.events.size<=retained)return;
  const excess=state.events.size-retained;
  const expired=[...state.events.values()].sort((left,right)=>(Number(left.cursor)||0)-(Number(right.cursor)||0)).slice(0,excess);
  for(const event of expired){
    state.events.delete(event.id);
    state.analyses.delete(event.id);
    state.analysisPending.delete(event.id);
  }
  if(state.selected&&!state.events.has(state.selected)){
    state.selected="";
    if(state.screen==="detail")state.screen="index";
  }
  renderTagWatcher();
  scheduleRender();
}

function recordDashboardSnapshot(snapshot){
  if(!snapshot||!Array.isArray(snapshot.widgets))return;
  state.dashboard=snapshot;
  const recordedAt=Date.parse(snapshot.recorded_at)||Date.now();
  for(const widget of snapshot.widgets){
    if(widget.kind==="custom_metric"&&widget.metric)recordDashboardPoint(widget.id,recordedAt,widget.metric.value,widget.metric.error);
    if(widget.kind==="custom_chart")for(const series of widget.series||[])recordDashboardPoint(`${widget.id}:${series.id}`,recordedAt,series.value,series.error);
  }
}

function recordDashboardPoint(key,recordedAt,value,error){
  const history=state.dashboardHistory.get(key)||[];
  if(history.at(-1)?.recordedAt===recordedAt)return;
  history.push({recordedAt,value:Number(value)||0,error:error||""});
  if(history.length>60)history.shift();
  state.dashboardHistory.set(key,history);
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
  invalidateRequestAnalysis(event);
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
    for(const event of state.pending)invalidateRequestAnalysis(event);
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
  state.analyses.clear();
  state.analysisPending.clear();
  state.hasMore=false;
  state.stats={...state.stats,events:0,bytes:0};
  state.selected="";
  renderTagWatcher();
  renderKinds();
  render();
}

async function loadMoreEvents(){
  if(state.loadingMore||!state.hasMore)return;
  const cursors=[...state.events.values()].map(event=>Number(event.cursor)||0).filter(Boolean);
  const before=Math.min(...cursors);
  if(!Number.isFinite(before))return;
  state.loadingMore=true;
  state.loadMoreError="";
  renderPagination();
  try{
    const result=await api(`/api/events?limit=${pageSize}&before=${before}`);
    for(const event of result.events||[])state.events.set(event.id,event);
    state.stats=result.stats||state.stats;
    state.hasMore=Boolean(result.has_more);
    renderTagWatcher();
    renderKinds();
    render();
  }catch(error){
    state.loadMoreError=error.message||"Could not load older events";
  }finally{
    state.loadingMore=false;
    renderPagination();
  }
}

function handleVirtualScroll(){
  scheduleVirtualRows();
  const remaining=elements.events.scrollHeight-elements.events.scrollTop-elements.events.clientHeight;
  if(remaining<=virtualRowHeight*virtualPreloadRows)loadMoreEvents();
}

function scheduleVirtualRows(){
  if(state.virtualFrame)return;
  state.virtualFrame=requestAnimationFrame(()=>{
    state.virtualFrame=0;
    renderVirtualRows();
  });
}

function renderVirtualList(events){
  const viewport=elements.events;
  const previous=state.virtualItems;
  const previousTop=viewport.scrollTop;
  const pinnedToTop=previousTop<virtualRowHeight/2;
  const anchorIndex=Math.min(previous.length-1,Math.max(0,Math.floor(previousTop/virtualRowHeight)));
  const anchor=previous[anchorIndex];
  const anchorOffset=previousTop-anchorIndex*virtualRowHeight;

  state.virtualItems=events;
  state.virtualStart=-1;
  state.virtualEnd=-1;
  if(pinnedToTop){
    viewport.scrollTop=0;
  }else if(anchor){
    const nextIndex=events.findIndex(event=>event.id===anchor.id);
    viewport.scrollTop=nextIndex>=0?nextIndex*virtualRowHeight+anchorOffset:0;
  }else if(!previous.length||!events.length){
    viewport.scrollTop=0;
  }
  renderVirtualRows(true);
}

function renderVirtualRows(force=false){
  if(state.screen!=="index")return;
  const viewport=elements.events;
  const total=state.virtualItems.length;
  if(!total){
    viewport.replaceChildren();
    return;
  }
  const visibleHeight=Math.max(viewport.clientHeight,virtualRowHeight);
  const start=Math.max(0,Math.floor(viewport.scrollTop/virtualRowHeight)-virtualOverscan);
  const end=Math.min(total,Math.ceil((viewport.scrollTop+visibleHeight)/virtualRowHeight)+virtualOverscan);
  if(!force&&start===state.virtualStart&&end===state.virtualEnd)return;
  state.virtualStart=start;
  state.virtualEnd=end;

  const fragment=document.createDocumentFragment();
  fragment.append(virtualSpacer(start*virtualRowHeight,"top"));
  for(let index=start;index<end;index++){
    const item=row(state.virtualItems[index]);
    fragment.append(item);
  }
  fragment.append(virtualSpacer((total-end)*virtualRowHeight,"bottom"));
  viewport.replaceChildren(fragment);
}

function virtualSpacer(height,position){
  const spacer=document.createElement("div");
  spacer.className="virtual-spacer";
  spacer.dataset.position=position;
  spacer.style.height=`${Math.max(0,height)}px`;
  spacer.setAttribute("aria-hidden","true");
  return spacer;
}

function filtered(){
  const search=elements.search.value.trim().toLowerCase();
  const minimum=durationFilterKinds.has(state.kind)?Number(elements.duration.value)*1000000:0;
  return visibleEvents().filter(event=>(!state.kind||event.kind===state.kind)&&matchesTimeRange(event)&&eventDuration(event)>=minimum&&matchesEntityFilters(event)&&(!search||`${event.kind} ${JSON.stringify(event.data)}`.toLowerCase().includes(search))).sort((a,b)=>b.cursor-a.cursor);
}

function matchesTimeRange(event){
  const range=elements["time-range"].value;
  if(!range)return true;
  const happened=Date.parse(event.started_at);
  if(!Number.isFinite(happened))return false;
  if(range==="custom"){
    const from=elements["time-from"].value?new Date(elements["time-from"].value).getTime():Number.NEGATIVE_INFINITY;
    const to=elements["time-to"].value?new Date(elements["time-to"].value).getTime():Number.POSITIVE_INFINITY;
    return happened>=from&&happened<=to;
  }
  const windows={"5m":5*60e3,"15m":15*60e3,"1h":60*60e3,"6h":6*60*60e3,"24h":24*60*60e3};
  return happened>=Date.now()-(windows[range]||0);
}

function applyListFilters(){
  syncLocation();
  render();
}

function toggleFilterDrawer(){
  setFilterDrawer(elements["filters-drawer"].classList.contains("hidden"));
}

function setFilterDrawer(isOpen){
  elements["filters-drawer"].classList.toggle("hidden",!isOpen);
  elements["filters-drawer"].setAttribute("aria-hidden",String(!isOpen));
  elements["filter-toggle"].setAttribute("aria-expanded",String(isOpen));
  elements["filter-toggle"].classList.toggle("active",isOpen);
}

function resetListFilters(){
  state.filters={};
  elements.search.value="";
  elements.duration.value="0";
  elements["time-range"].value="";
  elements["time-from"].value="";
  elements["time-to"].value="";
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
  const total=Math.max(state.events.size,Number(state.stats.events)||0);
  elements["event-count"].textContent=state.watchedTags.size?`${visible}/${state.events.size} loaded`:(total>state.events.size?`${state.events.size}/${total} events`:`${state.events.size} events`);
  updateCapacityStatus();
}

function updateCapacityStatus(){
  const eventRatio=Number(state.stats.events||0)/Math.max(1,Number(state.stats.max_events)||1);
  const byteRatio=Number(state.stats.bytes||0)/Math.max(1,Number(state.stats.max_bytes)||1);
  const ratio=Math.max(eventRatio,byteRatio);
  const level=ratio>=.9?"danger":ratio>=.7?"warning":"";
  elements["capacity-status"].textContent=`${Math.round(ratio*100)}%`;
  elements["capacity-status"].className=`metric capacity-status${level?` ${level}`:""}`;
  elements["capacity-status"].title=`Storage ${bytes(state.stats.bytes||0)} / ${bytes(state.stats.max_bytes||0)} · ${state.stats.events||0} / ${state.stats.max_events||0} events · ${state.stats.evicted_events||0} evicted · ${state.stats.dropped_events||0} dropped`;
  const storage=state.stats.storage||"memory";
  const persisted=storage==="disk"||storage==="sqlite";
  const storageLabel=storage==="sqlite"?"SQLite":storage==="disk"?"Disk journal":"In-memory";
  elements["storage-note"].innerHTML=`<strong>${storageLabel}</strong><span>${escapeHTML(state.stats.storage_error||(persisted?"Events survive process restarts.":"Events disappear when this process stops."))}</span>`;
  elements["storage-note"].classList.toggle("danger",Boolean(state.stats.storage_error));
}

function matchesEntityFilters(event){
  const data=event.data||{};
  for(const definition of filterDefinitions(state.kind)){
    const selected=state.filters[definition.key];
    if(!selected)continue;
    const value=definition.value(data,event);
    if(definition.httpStatus&&selected.startsWith("class:")){
      if(Math.floor(Number(value)/100)!==Number(selected.slice(6)))return false;
      continue;
    }
    if(String(value)!==selected)return false;
  }
  return true;
}

function render(){
  renderEntityFilters();
  const events=state.screen==="index"?filtered():[];
  elements["dashboard-screen"].classList.toggle("hidden",state.screen!=="dashboard");
  elements["index-screen"].classList.toggle("hidden",state.screen!=="index");
  elements["detail-screen"].classList.toggle("hidden",state.screen!=="detail");
  updateEventCount();
  elements["screen-title"].textContent=kindLabel(state.kind);
  renderListHeading();
  elements["time-custom"].classList.toggle("hidden",elements["time-range"].value!=="custom");
  elements["duration-filter"].classList.toggle("hidden",!durationFilterKinds.has(state.kind));
  updateFilterPanel();
  elements.empty.classList.toggle("hidden",events.length>0);
  elements.events.classList.toggle("hidden",events.length===0);
  if(state.screen==="index")renderVirtualList(events);
  renderPagination();
  if(state.selected&&!state.events.has(state.selected))state.selected="";
  if(state.screen==="dashboard")renderDashboard();
  if(state.screen==="detail")renderDetail(state.events.get(state.selected));
}

function updateFilterPanel(){
  const active=[];
  for(const definition of filterDefinitions(state.kind)){
    const value=state.filters[definition.key];
    if(value)active.push(`${definition.label}: ${value.startsWith("class:")?`${value.slice(6)}xx`:value}`);
  }
  const range=elements["time-range"].value;
  if(range){
    const label=elements["time-range"].selectedOptions[0]?.textContent||range;
    active.push(`Time: ${label}`);
  }
  if(durationFilterKinds.has(state.kind)&&elements.duration.value!=="0")active.push(`Duration: ≥ ${elements.duration.value} ms`);
  elements["filter-count"].textContent=String(active.length);
  elements["filter-count"].classList.toggle("hidden",active.length===0);
  elements["filter-clear"].disabled=active.length===0;
  elements["filter-summary"].textContent=active.length?active.join(" · "):"All recorded events";
  elements["filter-toggle"].classList.toggle("has-filters",active.length>0);
}

function renderPagination(){
  const loaded=state.events.size;
  const total=Number(state.stats.events)||0;
  elements.pagination.classList.toggle("hidden",loaded===0&&!state.hasMore&&!state.loadingMore);
  elements["load-more"].disabled=state.loadingMore;
  elements["load-more"].classList.toggle("hidden",!state.hasMore&&!state.loadingMore&&!state.loadMoreError);
  elements["load-more"].textContent=state.loadingMore?"Loading older…":state.loadMoreError?"Retry":"Load older now";
  elements["pagination-status"].textContent=state.loadMoreError?state.loadMoreError:state.hasMore?`${loaded} loaded${total?` of ${total}`:""} · scroll for older`:`${loaded} loaded${total?` of ${total}`:""} · complete`;
}

function filterDefinitions(kind){
  const text=(key,label,normalize=value=>String(value||""))=>({key,label,value:data=>normalize(data[key])});
  const httpStatus={key:"status",label:"Status",httpStatus:true,value:data=>String(Number(data.status)||0)};
  const definitions={
    request:[text("method","Method",value=>String(value||"").toUpperCase()),httpStatus],
    middleware:[text("state","State")],
    query:[text("operation","Operation",value=>String(value||"SQL").toUpperCase()),text("connection","Connection",value=>String(value||"default")),text("database","Database",value=>String(value||"default")),{key:"result",label:"Result",value:data=>data.error?"error":"ok",fixed:[["ok","OK"],["error","Error"]]}],
    cache:[text("operation","Operation",value=>String(value||"cache").toUpperCase()),text("store","Store",value=>String(value||"default")),{key:"result",label:"Result",value:data=>data.error?"error":data.hit?"hit":"miss",fixed:[["hit","Hit"],["miss","Miss"],["error","Error"]]}],
    job:[text("queue","Queue",value=>String(value||"default")),text("state","State",value=>String(value||"recorded")),text("connection","Connection",value=>String(value||"default"))],
    email:[text("transport","Transport",value=>String(value||"mail")),text("status","Status",value=>String(value||"recorded"))],
    log:[{key:"level",label:"Level",value:data=>normalizedLogLevel(data.level||"log"),fixed:[["trace","TRACE"],["debug","DEBUG"],["info","INFO"],["warn","WARN"],["error","ERROR"],["dpanic","DPANIC"],["panic","PANIC"],["fatal","FATAL"]]}],
    http_call:[text("method","Method",value=>String(value||"HTTP").toUpperCase()),{key:"host",label:"Host",value:data=>{
      try{return new URL(data.url||"").host||"unknown";}catch{return"unknown";}
    }},httpStatus],
    schedule:[text("state","State",value=>String(value||"recorded"))],
    exception:[text("type","Type",value=>String(value||"Exception"))],
    event:[text("kind","Kind",value=>String(value||"event")),text("status","Status",value=>String(value||"recorded"))]
  };
  return definitions[kind]||[];
}

function renderEntityFilters(){
  const definitions=filterDefinitions(state.kind);
  const source=visibleEvents().filter(event=>!state.kind||event.kind===state.kind);
  elements["entity-filters"].classList.toggle("hidden",definitions.length===0);
  elements["entity-filters"].innerHTML=definitions.map(definition=>{
    const counts=new Map();
    for(const event of source){
      const value=String(definition.value(event.data||{},event));
      if(value&&value!=="0")counts.set(value,(counts.get(value)||0)+1);
    }
    const selected=state.filters[definition.key]||"";
    if(selected&&!selected.startsWith("class:")&&!counts.has(selected))counts.set(selected,0);
    const recorded=[...counts].sort((left,right)=>definition.httpStatus?Number(left[0])-Number(right[0]):right[1]-left[1]||left[0].localeCompare(right[0])).slice(0,100);
    const fixed=definition.fixed||[];
    const fixedValues=new Set(fixed.map(([value])=>value));
    const fixedOptions=fixed.map(([value,label])=>`<option value="${escapeHTML(value)}"${selected===value?" selected":""}>${escapeHTML(label)}${counts.has(value)?` · ${counts.get(value)}`:""}</option>`).join("");
    const recordedOptions=recorded.filter(([value])=>!fixedValues.has(value)).map(([value,count])=>`<option value="${escapeHTML(value)}"${selected===value?" selected":""}>${escapeHTML(value)} · ${count}</option>`).join("");
    const statusClasses=definition.httpStatus?'<optgroup label="Status class"><option value="class:2"'+(selected==="class:2"?' selected':'')+'>2xx · Success</option><option value="class:3"'+(selected==="class:3"?' selected':'')+'>3xx · Redirect</option><option value="class:4"'+(selected==="class:4"?' selected':'')+'>4xx · Client error</option><option value="class:5"'+(selected==="class:5"?' selected':'')+'>5xx · Server error</option></optgroup>':"";
    return`<label><span>${escapeHTML(definition.label)}</span><select data-filter-key="${escapeHTML(definition.key)}"><option value="">All</option>${statusClasses}${fixedOptions}${recordedOptions}</select></label>`;
  }).join("");
}

function renderListHeading(){
  const layout=listLayout(state.kind);
  elements["list-heading"].className=`list-heading${listLayoutClasses(layout)}`;
  elements["list-heading"].innerHTML=`${layout.badge?`<span>${escapeHTML(layout.badge)}</span>`:""}<span>${escapeHTML(layout.entry)}</span>${layout.status?`<span>${escapeHTML(layout.status)}</span>`:""}${layout.duration?'<span class="list-duration">Duration</span>':""}<span class="list-time">Happened</span><span class="list-action-spacer" aria-hidden="true"></span>`;
}

function renderDashboard(){
  const widgets=dashboardWidgets();
  const signature=JSON.stringify(widgets.map(widget=>[widget.id,widget.kind,widget.builtin,widget.title,widget.description,widget.span,widget.metric?.sparkline,widget.metric?.mode,(widget.series||[]).map(series=>series.id),(widget.counters||[]).map(counter=>counter.id)]));
  const current=elements["dashboard-screen"].querySelector("[data-dashboard-root]");
  if(!current||current.dataset.signature!==signature){
    elements["dashboard-screen"].innerHTML=`
      <div class="dashboard-root" data-dashboard-root data-signature="${escapeHTML(signature)}">
        <header class="dashboard-heading">
          <div><h2>Dashboard</h2><p>Runtime health and recorded application activity.</p></div>
          <div class="dashboard-window"><span class="live-pulse"></span><strong>Live · 2 minute window</strong><span data-dashboard-uptime>Collecting runtime data</span></div>
        </header>
        <section class="dashboard-grid">${widgets.map(dashboardWidgetShell).join("")||'<div class="dashboard-panel-empty dashboard-empty">No dashboard widgets configured.</div>'}</section>
      </div>`;
  }
  updateDashboard();
}

function dashboardWidgets(){
  return Array.isArray(state.dashboard?.widgets)?state.dashboard.widgets:[];
}

function dashboardWidgetShell(widget){
  const span=Math.max(1,Math.min(4,Number(widget.span)||1));
  const attributes=`data-widget-id="${escapeHTML(widget.id||"")}" data-dashboard-span="${span}"`;
  if(widget.kind==="metric")return dashboardMetricShell(widget.builtin,widget.title,widget.description,attributes,true);
  if(widget.kind==="custom_metric")return dashboardMetricShell(widget.id,widget.title,widget.description,attributes,Boolean(widget.metric?.sparkline),true);
  if(widget.kind==="event_mix")return dashboardPanelShell(widget,attributes,"Recorded window",'<div class="mix-list" data-dashboard-mix></div>',"mix-panel");
  if(widget.kind==="queue_health")return dashboardPanelShell(widget,attributes,"Waiting for a stats source",'<div data-dashboard-queues></div>',"queue-panel",'data-dashboard-queue-summary');
  if(widget.kind==="slowest_operations")return dashboardPanelShell(widget,attributes,"Click to inspect",'<div class="slow-list" data-dashboard-slow></div>',"slow-panel");
  if(widget.kind==="custom_chart")return dashboardPanelShell(widget,attributes,"Live series",`<div class="custom-chart" data-dashboard-custom-chart="${escapeHTML(widget.id)}"></div>`,"custom-chart-panel");
  if(widget.kind==="counter_grid")return dashboardPanelShell(widget,attributes,"Latest sample",`<div class="custom-counter-grid" data-dashboard-counter-grid="${escapeHTML(widget.id)}"></div>`,"counter-grid-panel");
  return"";
}

function dashboardMetricShell(key,label,description,attributes,showChart,isCustom=false){
  const chart=showChart?`<div class="dashboard-metric-chart" data-dashboard-chart="${escapeHTML(key)}"></div>`:"";
  return`<article class="dashboard-widget dashboard-metric${showChart?"":" no-chart"}" ${attributes}><header><span>${escapeHTML(label||key)}</span><strong data-dashboard-value="${escapeHTML(key)}">—</strong></header>${chart}<footer data-dashboard-meta="${escapeHTML(key)}">${escapeHTML(description||"Collecting data")}</footer>${isCustom?'<span class="sr-only">Custom metric</span>':""}</article>`;
}

function dashboardPanelShell(widget,attributes,note,body,className,noteAttribute=""){
  return`<article class="dashboard-widget dashboard-panel ${className}" ${attributes}><header><div><h3>${escapeHTML(widget.title||widget.id)}</h3>${widget.description?`<span>${escapeHTML(widget.description)}</span>`:""}</div><small ${noteAttribute}>${escapeHTML(note)}</small></header>${body}</article>`;
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
  for(const widget of dashboardWidgets()){
    if(widget.kind==="custom_metric")updateCustomDashboardMetric(widget);
    if(widget.kind==="custom_chart")setDashboardHTML(`[data-dashboard-custom-chart="${cssEscape(widget.id)}"]`,customDashboardChart(widget));
    if(widget.kind==="counter_grid")setDashboardHTML(`[data-dashboard-counter-grid="${cssEscape(widget.id)}"]`,customCounterGrid(widget));
  }
}

function updateCustomDashboardMetric(widget){
  const metric=widget.metric||{};
  const values=dashboardMetricValues(widget.id,metric.mode);
  const current=values.at(-1)??0;
  const color=dashboardColor(metric.color,0);
  const graph=metric.sparkline?sparkline(values,color,180,58):"";
  const meta=metric.error||widget.description||(metric.mode==="rate"?"Change per second":"Latest sample");
  updateDashboardMetric(widget.id,metric.error?"—":formatDashboardValue(current,metric.format,metric.unit,metric.mode),meta,graph);
}

function dashboardMetricValues(key,mode="value"){
  const history=(state.dashboardHistory.get(key)||[]).filter(point=>!point.error);
  if(mode!=="rate")return history.map(point=>point.value);
  const rates=[];
  for(let index=1;index<history.length;index++){
    const elapsed=(history[index].recordedAt-history[index-1].recordedAt)/1000;
    const delta=history[index].value-history[index-1].value;
    rates.push(elapsed>0&&delta>=0?delta/elapsed:0);
  }
  return rates.length?rates:[0];
}

function customDashboardChart(widget){
  const series=(widget.series||[]).map((item,index)=>({...item,color:dashboardColor(item.color,index),values:dashboardMetricValues(`${widget.id}:${item.id}`)}));
  if(!series.length)return'<div class="dashboard-panel-empty">No chart series configured.</div>';
  const width=720;
  const height=176;
  const padding=12;
  const all=series.flatMap(item=>item.values);
  const maximum=Math.max(1,...all);
  const minimum=Math.min(0,...all);
  const range=Math.max(1,maximum-minimum);
  const paths=series.map(item=>{
    const values=item.values.length>1?item.values:[item.values[0]||0,item.values[0]||0];
    const points=values.map((value,index)=>`${(padding+index*(width-padding*2)/(values.length-1)).toFixed(2)},${(height-padding-(value-minimum)/range*(height-padding*2)).toFixed(2)}`).join(" ");
    return`<polyline points="${points}" fill="none" stroke="${item.color}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"></polyline>`;
  }).join("");
  const legend=series.map(item=>`<span><svg viewBox="0 0 8 8" aria-hidden="true"><circle cx="4" cy="4" r="4" fill="${item.color}"></circle></svg>${escapeHTML(item.label||item.id)} <strong>${escapeHTML(formatDashboardValue(item.value,widget.format,widget.unit))}</strong>${item.error?`<em title="${escapeHTML(item.error)}">Error</em>`:""}</span>`).join("");
  return`<div class="custom-chart-legend">${legend}</div><svg class="custom-chart-svg" viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" role="img" aria-label="${escapeHTML(widget.title||"Custom chart")}"><path class="custom-chart-grid" d="M12 44H708M12 88H708M12 132H708"></path>${paths}</svg>`;
}

function customCounterGrid(widget){
  const counters=widget.counters||[];
  if(!counters.length)return'<div class="dashboard-panel-empty">No counters configured.</div>';
  return counters.map(counter=>`<div class="custom-counter${counter.error?" has-error":""}"${counter.error?` title="${escapeHTML(counter.error)}"`:""}><span>${escapeHTML(counter.label||counter.id)}</span><strong>${escapeHTML(counter.error?"—":formatDashboardValue(counter.value,counter.format,counter.unit))}</strong></div>`).join("");
}

function formatDashboardValue(value,format="number",unit="",mode="value"){
  const number=Number(value);
  if(!Number.isFinite(number))return"—";
  let formatted;
  if(format==="bytes")formatted=bytes(number);
  else if(format==="percent")formatted=percent(number);
  else if(format==="duration")formatted=duration(number);
  else formatted=new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(number);
  const suffix=unit||mode==="rate"?unit||"/s":"";
  return suffix?`${formatted} ${suffix}`:formatted;
}

function dashboardColor(value,index){
  const color=String(value||"").trim();
  if(/^#[0-9a-f]{3}([0-9a-f]{3})?$/i.test(color))return color;
  return["#6266d6","#17a36d","#d58435","#ba4a52","#2689a8"][index%5];
}

function cssEscape(value){
  return CSS.escape(String(value||""));
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
  const status=String(data.status||"").toLowerCase();
  return Boolean(data.error||Number(data.status)>=500||["failed","bounced","rejected"].includes(status)||["failed","dispatch_failed","panicked"].includes(data.state));
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
  state.queryTab="query";
  state.entityTab=firstEntityTab(state.events.get(id));
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
    restoreListFilters(params);
    return;
  }
  state.kind=view==="all"?"":kinds.includes(view)?view:state.events.get(entry).kind;
  restoreListFilters(params);
  state.selected=entry;
  const tab=params.get("tab")||"";
  const selectedEvent=state.events.get(entry);
  if(selectedEvent.kind==="query")state.queryTab=["query","callsite","explain","replay"].includes(tab)?tab:"query";
  else if(selectedEvent.kind!=="request"){
    const entityTabs=entityTabDefinitions(selectedEvent);
    state.entityTab=entityTabs.some(item=>item.key===tab)?tab:(entityTabs[0]?.key||"");
  }else if(tab==="request")state.requestTab="payload";
  else if(tab==="response")state.responseTab="response";
  else if(tab)state.relatedTab=tab;
  else state.relatedTab=firstRelatedTab(groupFor(selectedEvent));
  state.screen="detail";
}

function restoreListFilters(params){
  state.filters={};
  for(const definition of filterDefinitions(state.kind)){
    const value=params.get(definition.key)||"";
    if(value)state.filters[definition.key]=value;
  }
  elements.search.value=params.get("q")||"";
  const durationValue=params.get("duration")||"0";
  elements.duration.value=[...elements.duration.options].some(option=>option.value===durationValue)?durationValue:"0";
  const range=params.get("range")||"";
  elements["time-range"].value=[...elements["time-range"].options].some(option=>option.value===range)?range:"";
  elements["time-from"].value=params.get("from")||"";
  elements["time-to"].value=params.get("to")||"";
}

function syncLocation(mode="replace"){
  const url=new URL(location.href);
  url.searchParams.delete("entry");
  url.searchParams.delete("tab");
  url.searchParams.delete("tag");
  url.searchParams.delete("q");
  url.searchParams.delete("duration");
  url.searchParams.delete("range");
  url.searchParams.delete("from");
  url.searchParams.delete("to");
  for(const key of filterParamKeys)url.searchParams.delete(key);
  for(const tag of [...state.watchedTags].sort())url.searchParams.append("tag",tag);
  if(state.screen==="detail"&&state.selected){
    url.searchParams.set("entry",state.selected);
    const selectedKind=state.events.get(state.selected)?.kind;
    const selectedTab=selectedKind==="query"?state.queryTab:selectedKind==="request"?state.relatedTab:state.entityTab;
    if(selectedTab)url.searchParams.set("tab",selectedTab);
    url.searchParams.set("view",state.kind||"all");
  }else{
    url.searchParams.set("view",state.screen==="dashboard"?"dashboard":state.kind||"all");
  }
  if(state.screen!=="dashboard"){
    const query=elements.search.value.trim();
    if(query)url.searchParams.set("q",query);
    if(elements.duration.value&&elements.duration.value!=="0")url.searchParams.set("duration",elements.duration.value);
    if(elements["time-range"].value)url.searchParams.set("range",elements["time-range"].value);
    if(elements["time-range"].value==="custom"){
      if(elements["time-from"].value)url.searchParams.set("from",elements["time-from"].value);
      if(elements["time-to"].value)url.searchParams.set("to",elements["time-to"].value);
    }
    for(const definition of filterDefinitions(state.kind)){
      const value=state.filters[definition.key];
      if(value)url.searchParams.set(definition.key,value);
    }
  }
  history[mode==="push"?"pushState":"replaceState"](null,"",url);
}

function row(event){
  const button=document.createElement("button");
  const status=statusFor(event);
  const layout=listLayout(state.kind);
  const badge=layout.badge?listBadge(event):null;
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
    event:{badge:"",entry:"Event",status:"Status",duration:true}
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
  if(state.kind==="middleware")content={title:data.name||"Middleware"};
  else if(state.kind==="query")content={title:compactQuery(data.sql||""),full:data.sql||"",code:true};
  else if(state.kind==="cache")content={title:data.key||"Cache operation",code:true};
  else if(state.kind==="job")content={title:data.name||"Job"};
  else if(state.kind==="email")content={title:data.subject||"Email"};
  if(state.kind==="log")content={title:data.message||"Log entry"};
  else if(state.kind==="http_call")content={title:data.url||"HTTP call"};
  else if(state.kind==="schedule")content={title:data.name||"Scheduled task"};
  else if(state.kind==="exception")content={title:data.message||"Exception"};
  else if(state.kind==="event")content={title:data.name||data.summary||"Event"};
  return entityPreview(content||relationContent(event.kind,event,data),"event-main",event.tags,event.kind==="event"?data.kind||"event":"");
}

function entityPreview(content,className="",tags,kindTag=""){
  return`<span class="entity-preview ${className}">${content.code?`<code>${escapeHTML(content.title)}</code>`:`<strong>${escapeHTML(content.title)}</strong>`}${inlineTags(tags,kindTag)}</span>`;
}

function renderDetail(event){
  if(!event){
    elements.detail.innerHTML='<div class="detail-empty"><span class="empty-mark">⌁</span><strong>No event selected</strong><span>Choose an entry to inspect its complete request context.</span></div>';
    return;
  }
  const group=groupFor(event);
  elements.back.innerHTML=`<span aria-hidden="true">←</span> Back to ${escapeHTML(kindLabel(state.kind).toLowerCase())}`;
  if(event.kind==="query"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${queryDetailsCard(event,group.request)}${queryCard(event)}</div>`;
    return;
  }
  if(event.kind==="cache"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${cacheDetailsCard(event,group.request)}${entityContentCard(event)}</div>`;
    return;
  }
  if(event.kind!=="request"){
    elements.detail.innerHTML=`<div class="detail-body telescope-stack">${entityDetailsCard(event,group.request)}${entityContentCard(event)}</div>`;
    return;
  }
  if(!state.analyses.has(event.id)&&state.sessionMode==="live")loadRequestAnalysis(event.id);
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

function requestFindingsPanel(group){
  const request=group.request;
  if(!request)return'<div class="diagnostic-pending">Request data is unavailable.</div>';
  const analysis=state.analyses.get(request.id);
  if(!analysis&&state.sessionMode==="live")loadRequestAnalysis(request.id);
  if(!analysis){
    const message=state.sessionMode==="imported"?"Analysis was not included in this imported session.":"Analyzing the complete request timeline…";
    return`<div class="diagnostic-pending">${escapeHTML(message)}</div>`;
  }
  if(analysis.error){
    return`<div class="diagnostic-pending">${escapeHTML(analysis.error)}</div>`;
  }
  const findings=Array.isArray(analysis.findings)?analysis.findings:[];
  const severityRank={danger:0,warning:1,info:2};
  const severityLabels={danger:"Critical",warning:"Potential",info:"Info"};
  const severityMarks={danger:"!",warning:"?",info:"i"};
  const ordered=[...findings].sort((left,right)=>(severityRank[left.severity]??1)-(severityRank[right.severity]??1));
  const content=ordered.length?ordered.map(finding=>{
    const entryID=typeof finding.entry_id==="string"&&state.events.has(finding.entry_id)?finding.entry_id:"";
    const severity=["info","warning","danger"].includes(finding.severity)?finding.severity:"warning";
    const severityLabel=severityLabels[severity];
    const supporting=[finding.detail,finding.suggestion].filter(Boolean).join(" · ");
    return`<button type="button" class="diagnostic-row ${severity}" aria-label="${escapeHTML(severityLabel)}: ${escapeHTML(finding.title||"Finding")}"${entryID?` data-event-id="${escapeHTML(entryID)}"`:""}><span class="diagnostic-mark" aria-hidden="true">${severityMarks[severity]}</span><span class="diagnostic-copy"><strong>${escapeHTML(finding.title||"Finding")}</strong>${supporting?`<small>${escapeHTML(supporting)}</small>`:""}</span><span class="diagnostic-severity">${severityLabel}</span>${entryID?rowAction():'<span class="diagnostic-action-placeholder" aria-hidden="true"></span>'}</button>`;
  }).join(""):'<div class="diagnostic-healthy"><span aria-hidden="true">✓</span><strong>No automatic findings</strong><small>The recorded request timeline passed the current diagnostic rules.</small></div>';
  return`<div class="diagnostic-list">${content}</div>`;
}

function loadRequestAnalysis(requestID){
  if(!requestID||state.sessionMode!=="live"||state.analyses.has(requestID)||state.analysisPending.has(requestID))return;
  state.analysisPending.add(requestID);
  api(`/api/requests/${encodeURIComponent(requestID)}/analysis`).then(analysis=>{
    state.analyses.set(requestID,analysis);
  }).catch(error=>{
    state.analyses.set(requestID,{error:`Could not analyze request: ${error.message}`});
  }).finally(()=>{
    state.analysisPending.delete(requestID);
    if(state.screen==="detail"&&state.selected===requestID)renderDetail(state.events.get(requestID));
  });
}

function invalidateRequestAnalysis(event){
  const requestID=event.kind==="request"?event.id:event.request_id||event.origin_request_id;
  if(requestID)state.analyses.delete(requestID);
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
  const analysis=group.request?state.analyses.get(group.request.id):null;
  const findings=Array.isArray(analysis?.findings)?analysis.findings:[];
  const findingBadge=analysis?.error?"!":analysis?findings.length:"…";
  tabs.unshift({key:"findings",label:"Findings",badge:findingBadge});
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
  if(Array.isArray(data.callsite)&&data.callsite.length)facts.push(["Callsite",sourceFrameLocation(data.callsite[0],true),true]);
  facts.push(["Request",request?requestLink(request):"Standalone",Boolean(request)],["Tags",tagList(query.tags),Boolean(query.tags&&Object.keys(query.tags).length)]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>Query Details</h3><span>${escapeHTML(query.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function queryCard(query){
  const tabs=[{key:"query",label:"Query"},{key:"callsite",label:"Callsite"},{key:"explain",label:"EXPLAIN"},{key:"replay",label:"Go replay"}];
  if(!tabs.some(tab=>tab.key===state.queryTab))state.queryTab="query";
  let panel;
  let action="";
  if(state.queryTab==="callsite")panel=queryCallsitePanel(query);
  else if(state.queryTab==="explain"){
    panel=queryPlanPanel(query);
    if(query.data?.plan?.text)action=queryCopyButton("plan");
  }else if(state.queryTab==="replay"){
    panel=codePanel(queryReplayCode(query),false,"No SQL was captured.");
    action=queryCopyButton("replay");
  }else{
    panel=sqlPanel((query.data||{}).sql||"");
    action=queryCopyButton("query");
  }
  return tabbedCard("Query",tabs,state.queryTab,panel,action);
}

function queryCallsitePanel(query){
  const frames=Array.isArray(query.data?.callsite)?query.data.callsite:[];
  return frames.length?`<div class="query-callsite">${frames.map((frame,index)=>`<div class="source-frame${index===0?" primary":""}"><span class="source-index">${index+1}</span><span class="source-copy"><strong>${sourceFrameLocation(frame,false)}</strong><code>${escapeHTML(frame.function||"unknown function")}</code></span><span class="source-actions">${safeSourceURL(frame.url)?`<a href="${escapeHTML(safeSourceURL(frame.url))}" title="Open source">Open</a>`:""}<button type="button" data-copy-source="${index}" title="Copy ${escapeHTML(`${frame.file||""}:${frame.line||0}`)}">Copy</button></span></div>`).join("")}</div>`:'<div class="panel-empty">No Go callsite was captured.</div>';
}

function firstEntityTab(event){
  return entityTabDefinitions(event)[0]?.key||"";
}

function entityTabDefinitions(event){
  if(!event)return[];
  const data=event.data||{};
  const tabs=[];
  const add=(key,label,panel)=>tabs.push({key,label,panel});
  if(event.kind==="cache"){
    const value=formatCapturedValue(data.value||"");
    const message=data.hit?"The cache integration did not capture a value for this hit.":"No value was returned for this cache operation.";
    add("value","Value",codePanel(value,data.truncated,message));
  }
  if(event.kind==="job")add("arguments","Arguments",entityValuePanel(data.arguments,"No job arguments were captured."));
  if(event.kind==="email"){
    if(data.text)add("text","Text",entityValuePanel(data.text,"No text body was captured.",false));
    if(data.html)add("html","HTML",entityValuePanel(data.html,"No HTML body was captured.",false));
  }
  if(event.kind==="log"){
    add("fields","Fields",entityValuePanel(data.fields,"No structured fields were captured."));
    if(data.stack)add("stack","Stack",entityValuePanel(data.stack,"No stack was captured.",false));
  }
  if(event.kind==="http_call"){
    add("request","Request",entityMessagePanel("Request",data.request));
    add("response","Response",entityMessagePanel("Response",data.response));
  }
  if(event.kind==="schedule"){
    add("payload","Payload",entityValuePanel(data.payload,"No schedule payload was captured."));
    if(data.panic||data.error)add(data.panic?"panic":"error",data.panic?"Panic":"Error",entityValuePanel(data.panic||data.error,"No failure details were captured.",false));
  }
  if(event.kind==="exception")add("stack","Stack",entityValuePanel(data.stack,"No stack was captured.",false));
  if(event.kind==="event")add("fields","Fields",entityValuePanel(data.fields,"No event fields were captured."));
  if(Array.isArray(data.callsite)&&data.callsite.length)add("callsite","Callsite",queryCallsitePanel(event));
  return tabs;
}

function entityContentCard(event){
  const definitions=entityTabDefinitions(event);
  if(!definitions.length)return"";
  if(!definitions.some(tab=>tab.key===state.entityTab))state.entityTab=definitions[0].key;
  const active=definitions.find(tab=>tab.key===state.entityTab)||definitions[0];
  return tabbedCard("Entity",definitions.map(({key,label})=>({key,label})),active.key,active.panel);
}

function queryPlanPanel(query){
  const plan=query.data?.plan;
  const panel=plan?.text?codePanel(plan.text,false,"No plan rows were returned."):'<div class="panel-empty">No EXPLAIN plan is stored for this query. Enable <code>Config{Explain: true}</code> before recording it; existing entries are not backfilled.</div>';
  return`${panel}${plan?.error?`<div class="danger-block">${escapeHTML(plan.error)}</div>`:""}`;
}

function queryCopyButton(kind){
  const attributes=kind==="plan"?'data-copy-query-plan aria-label="Copy EXPLAIN" title="Copy EXPLAIN"':kind==="replay"?'data-copy-query-replay aria-label="Copy Go replay" title="Copy Go replay"':'data-copy-query aria-label="Copy SQL" title="Copy SQL"';
  return`<button type="button" class="copy-button" ${attributes}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 8h9v11H9zM6 16H4V5h9v3"/></svg><span>Copied</span></button>`;
}

function queryReplayCode(query){
  const data=query?.data||{};
  const sql=String(data.sql||"");
  if(!sql)return"";
  const operation=String(data.operation||sql.trim().split(/\s+/,1)[0]||"").toUpperCase();
  const hasArguments=/\?|\$\d+|:\w+|@\w+/.test(sql);
  const args=hasArguments?'args := []any{\n\t// TODO: add values for the SQL placeholders.\n}\n\n':"";
  const suffix=hasArguments?", args...":"";
  if(operation==="SELECT"||operation==="WITH")return`${args}rows, err := db.QueryContext(ctx, ${JSON.stringify(sql)}${suffix})\nif err != nil {\n\treturn err\n}\ndefer rows.Close()\n\nfor rows.Next() {\n\t// TODO: scan one row.\n}\nreturn rows.Err()`;
  return`${args}result, err := db.ExecContext(ctx, ${JSON.stringify(sql)}${suffix})\nif err != nil {\n\treturn err\n}\n\n_, err = result.RowsAffected()\nreturn err`;
}

function sourceFrameLocation(frame,linked){
  const file=String(frame?.file||"unknown");
  const label=`${file.split(/[\\/]/).pop()}:${Number(frame?.line)||0}`;
  const url=safeSourceURL(frame?.url);
  if(linked&&url)return`<a class="source-inline-link" href="${escapeHTML(url)}">${escapeHTML(label)}</a>`;
  return escapeHTML(label);
}

function safeSourceURL(value){
  const url=String(value||"").trim();
  return /^(https?:\/\/|vscode:\/\/file\/|goland:\/\/open\?|zed:\/\/file\/)/i.test(url)?url:"";
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
  if(Array.isArray(data.callsite)&&data.callsite.length)facts.splice(facts.length-2,0,["Callsite",sourceFrameLocation(data.callsite[0],true),true]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>Cache Details</h3><span>${escapeHTML(cache.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
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

function inlineTags(tags,kindTag=""){
  const entries=Object.entries(tags||{}).slice(0,3);
  if(!kindTag&&!entries.length)return"";
  return`<span class="inline-tags">${kindTag?`<i class="event-kind-tag" title="Kind">${escapeHTML(kindTag)}</i>`:""}${entries.map(([name,value])=>`<i>${escapeHTML(name)}${value?`=${escapeHTML(value)}`:""}</i>`).join("")}</span>`;
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
  if(Array.isArray(data.callsite)&&data.callsite.length)facts.push(["Callsite",sourceFrameLocation(data.callsite[0],true),true]);
  if(request)facts.push(["Request",requestLink(request),true]);
  else facts.push(["Request","Standalone"]);
  if(event.process)facts.push(["Process",event.process]);
  if(event.instance)facts.push(["Instance",event.instance]);
  if(event.tags&&Object.keys(event.tags).length)facts.push(["Tags",tagList(event.tags),true]);
  return`<section class="detail-section telescope-card"><div class="section-heading"><h3>${escapeHTML(detailTitle(event.kind))}</h3><span>${escapeHTML(event.id)}</span></div><dl class="facts">${facts.map(([name,value,html])=>`<dt>${escapeHTML(name)}</dt><dd>${html?value:escapeHTML(value)}</dd>`).join("")}</dl>${data.error?`<div class="danger-block">${escapeHTML(data.error)}</div>`:""}</section>`;
}

function entityValuePanel(value,emptyMessage,json=true){
  const content=value&&json?JSON.stringify(value,null,2):value||"";
  return codePanel(content,false,emptyMessage);
}

function entityMessagePanel(title,message={}){
  message=message||{};
  const body=message.body||"";
  const headerPanel=headersPanel(message.headers,"No headers were captured.");
  return`<div class="entity-message-grid"><div><span>Body</span>${codePanel(body,message.truncated,`No ${title.toLowerCase()} body was captured.`)}</div><div><span>Headers</span>${headerPanel}</div></div>`;
}

function requestCard(request){
  const data=request.data||{};
  const message=data.request||{};
  let panel;
  if(state.requestTab==="headers")panel=headersPanel(message.headers,"No request headers were captured.");
  else if(state.requestTab==="raw")panel=codePanel(rawHTTPRequest(request),message.truncated,"No raw request could be generated.");
  else if(state.requestTab==="curl")panel=codePanel(curlCommand(request),message.truncated,"No cURL command could be generated.");
  else if(state.requestTab==="har")panel=codePanel(requestHAR(request),false,"No HAR entry could be generated.");
  else panel=codePanel(requestPayload(data,message),message.truncated,"No request payload was captured.");
  const tabs=[{key:"payload",label:"Payload"},{key:"headers",label:"Headers",count:headerCount(message.headers)},{key:"raw",label:"Raw"},{key:"curl",label:"cURL"},{key:"har",label:"HAR"}];
  return tabbedCard("Request",tabs,state.requestTab,panel,requestCopyButton(state.requestTab));
}

function responseCard(request){
  const message=(request.data||{}).response||{};
  const panel=state.responseTab==="headers"?headersPanel(message.headers,"No response headers were captured."):codePanel(message.body||"",message.truncated,"No textual response body was captured.");
  return tabbedCard("Response",[{key:"response",label:"Response"},{key:"headers",label:"Headers",count:headerCount(message.headers)}],state.responseTab,panel);
}

function relatedCard(group,event,tabs){
  let panel;
  if(state.relatedTab==="findings")panel=requestFindingsPanel(group);
  else if(state.relatedTab==="timeline")panel=timeline(group.events);
  else if(state.relatedTab==="raw")panel=rawBlock(event.data);
  else panel=relatedCollection(state.relatedTab,group.byKind.get(state.relatedTab)||[]);
  return tabbedCard("Related",tabs,state.relatedTab,panel);
}

function cardTabs(group,tabs,active,action=""){
  const key=group.toLowerCase();
  const panelID=`card-panel-${key}`;
  const buttons=tabs.map(tab=>{
    const selected=active===tab.key;
    const tabID=`card-tab-${key}-${tab.key}`;
    let meta="";
    if(tab.badge!==undefined)meta=`<span class="tab-badge">${escapeHTML(tab.badge)}</span>`;
    else if(tab.count!==undefined)meta=` <span class="tab-count">(${escapeHTML(tab.count)})</span>`;
    return`<button type="button" id="${escapeHTML(tabID)}" role="tab" aria-selected="${selected}" aria-controls="${escapeHTML(panelID)}" tabindex="${selected?0:-1}" data-card-tab="${escapeHTML(`${key}:${tab.key}`)}" class="card-tab${selected?" active":""}">${escapeHTML(tab.label)}${meta}</button>`;
  }).join("");
  return`<nav class="card-tabs" aria-label="${escapeHTML(group)} details"><div class="card-tab-list" role="tablist">${buttons}</div>${action?`<div class="card-tab-actions">${action}</div>`:""}</nav>`;
}

function tabbedCard(group,tabs,active,panel,action=""){
  const key=group.toLowerCase();
  const activeTab=tabs.find(tab=>tab.key===active)||tabs[0];
  const panelID=`card-panel-${key}`;
  const labelledBy=activeTab?` aria-labelledby="${escapeHTML(`card-tab-${key}-${activeTab.key}`)}"`:"";
  return`<section class="detail-section telescope-card">${cardTabs(group,tabs,active,action)}<div class="card-panel" id="${escapeHTML(panelID)}" role="tabpanel"${labelledBy}>${panel}</div></section>`;
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
  const copy=`<button type="button" class="copy-button" data-copy-request="${escapeHTML(format)}" aria-label="Copy ${escapeHTML(label)}" title="Copy ${escapeHTML(label)}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 8h9v11H9zM6 16H4V5h9v3"/></svg><span>Copied</span></button>`;
  if(format!=="har")return copy;
  return`<span class="card-actions"><button type="button" class="copy-button" data-download-har aria-label="Download HAR" title="Download HAR"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v12m-4-4 4 4 4-4M5 20h14"/></svg></button>${copy}</span>`;
}

function requestFormatLabel(format){
  return{payload:"payload",headers:"headers",raw:"raw HTTP",curl:"cURL",har:"HAR"}[format]||"request";
}

function requestRepresentation(request,format){
  if(!request)return"";
  const data=request.data||{};
  const message=data.request||{};
  if(format==="headers")return requestHeadersText(data,false);
  if(format==="raw")return rawHTTPRequest(request);
  if(format==="curl")return curlCommand(request);
  if(format==="har")return requestHAR(request);
  return requestPayload(data,message);
}

function requestHAR(request){
  const data=request?.data||{};
  const requestMessage=data.request||{};
  const responseMessage=data.response||{};
  const url=absoluteRequestURL(data);
  const query=[];
  try{
    for(const [name,value] of new URL(url).searchParams)query.push({name,value});
  }catch{}
  const headers=value=>Object.entries(value||{}).flatMap(([name,values])=>(Array.isArray(values)?values:[values]).map(item=>({name,value:String(item)})));
  const entry={
    startedDateTime:request.started_at,
    time:eventDuration(request)/1e6,
    request:{method:data.method||"GET",url,httpVersion:data.protocol||"HTTP/1.1",headers:headers(requestMessage.headers),queryString:query,cookies:[],headersSize:-1,bodySize:Number(requestMessage.size)||-1},
    response:{status:Number(data.status)||0,statusText:"",httpVersion:data.protocol||"HTTP/1.1",headers:headers(responseMessage.headers),cookies:[],content:{size:Number(responseMessage.size)||Number(data.response_size)||0,mimeType:responseMessage.content_type||"",text:responseMessage.body||""},redirectURL:"",headersSize:-1,bodySize:Number(responseMessage.size)||Number(data.response_size)||-1},
    cache:{},
    timings:{send:0,wait:eventDuration(request)/1e6,receive:0}
  };
  if(requestMessage.body)entry.request.postData={mimeType:requestMessage.content_type||"",text:requestMessage.body};
  return JSON.stringify({log:{version:"1.2",creator:{name:"webpprof",version:"dev"},entries:[entry]}},null,2);
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
  if(kind==="middleware")return{title:data.name||"Middleware"};
  if(kind==="query")return{title:compactQuery(data.sql||""),full:data.sql||"",code:true};
  if(kind==="cache")return{title:data.key||"Cache operation",code:true};
  if(kind==="log")return{title:data.message||"Log entry"};
  if(kind==="job")return{title:data.name||"Job"};
  if(kind==="email")return{title:data.subject||"Email"};
  if(kind==="http_call")return{title:`${data.method||"HTTP"} ${data.url||""}`};
  if(kind==="schedule")return{title:data.name||"Scheduled task"};
  if(kind==="exception")return{title:data.message||"Exception"};
  return{title:data.name||data.summary||kindLabel(kind)};
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
  const request=events.find(event=>event.kind==="request")||events[0];
  const starts=events.map(event=>Date.parse(event.started_at)).filter(Number.isFinite);
  const requestStart=Date.parse(request.started_at);
  const first=Number.isFinite(requestStart)?requestStart:starts.length?Math.min(...starts):Date.now();
  const last=Math.max(first+1,...events.map(event=>timelineEventEnd(event,first)));
  const windowMS=Math.max(last-first,1);
  const byID=new Map(events.map(event=>[event.id,event]));
  const ordered=timelineTree(events,request,byID);
  const critical=timelineCriticalPath(events,first,last);
  const rows=ordered.map(({event,depth,isLast,ancestorContinuations,hasChildren})=>{
    const started=Date.parse(event.started_at);
    const offset=Math.max(0,(Number.isFinite(started)?started:first)-first);
    const recorded=Math.max(eventDuration(event)/1e6,0);
    const elapsed=event.id===request.id?windowMS:Math.max(recorded,.05);
    const x=Math.min(998,Math.max(0,offset/windowMS*1000));
    const width=Math.max(2,Math.min(1000-x,elapsed/windowMS*1000));
    const criticalClass=critical.ids.has(event.id)?" critical":"";
    const bottleneckClass=critical.bottleneck?.id===event.id?" bottleneck":"";
    const durationLabel=event.id===request.id&&recorded<windowMS*.99?formatMilliseconds(windowMS):duration(event.duration_ns);
    const operation=event.kind==="request"?requestTarget(event.data||{}):title(event);
    return`<button type="button" class="gantt-row depth-${Math.min(depth,6)}${isFailure(event)?" failed":""}${criticalClass}${bottleneckClass}" data-event-id="${escapeHTML(event.id)}"><span class="gantt-operation">${timelineTreeConnector(depth,isLast,ancestorContinuations,hasChildren)}<span class="gantt-kind" data-kind="${escapeHTML(event.kind)}">${escapeHTML(timelineKindLabel(event.kind))}</span><strong title="${escapeHTML(operation)}">${escapeHTML(operation)}</strong>${critical.bottleneck?.id===event.id?'<em>Bottleneck</em>':""}</span><span class="gantt-track"><svg viewBox="0 0 1000 20" preserveAspectRatio="none" role="img" aria-label="Starts at ${escapeHTML(formatMilliseconds(offset))}, lasts ${escapeHTML(durationLabel)}"><rect class="gantt-bar" data-kind="${escapeHTML(event.kind)}" x="${x.toFixed(2)}" y="3" width="${width.toFixed(2)}" height="14" rx="3"/></svg></span><b>${escapeHTML(durationLabel)}</b>${rowAction()}</button>`;
  }).join("");
  const bottleneck=critical.bottleneck;
  const summary=`<div class="gantt-summary"><div class="gantt-stat"><span>Request window</span><strong>${escapeHTML(formatMilliseconds(windowMS))}</strong></div><div class="gantt-stat"><span>Critical path</span><strong>${escapeHTML(formatMilliseconds(critical.duration))}</strong></div><div class="gantt-stat bottleneck"><span>Bottleneck</span><strong>${escapeHTML(bottleneck?title(bottleneck):"None")}</strong></div></div>`;
  return`<div class="gantt">${summary}${timelineBreakdown(events)}<div class="gantt-table"><div class="gantt-heading"><span>Operation</span>${timelineAxis(windowMS)}<span>Duration</span><span></span></div>${rows}</div></div>`;
}

function timelineEventEnd(event,fallback){
  const started=Date.parse(event.started_at);
  return(Number.isFinite(started)?started:fallback)+Math.max(eventDuration(event)/1e6,.05);
}

function timelineTree(events,request,byID){
  const children=new Map();
  for(const event of events){
    if(event.id===request.id)continue;
    const parentID=event.parent_id&&byID.has(event.parent_id)&&event.parent_id!==event.id?event.parent_id:request.id;
    if(!children.has(parentID))children.set(parentID,[]);
    children.get(parentID).push(event);
  }
  const compare=(left,right)=>(Date.parse(left.started_at)||0)-(Date.parse(right.started_at)||0)||(left.cursor||0)-(right.cursor||0);
  for(const group of children.values())group.sort(compare);
  const ordered=[];
  const visited=new Set();
  const visit=(event,depth,isLast,ancestorContinuations=[])=>{
    if(visited.has(event.id))return;
    visited.add(event.id);
    const nested=children.get(event.id)||[];
    ordered.push({event,depth,isLast,ancestorContinuations,hasChildren:nested.length>0});
    nested.forEach((child,index)=>visit(child,depth+1,index===nested.length-1,[...ancestorContinuations,!isLast]));
  };
  visit(request,0,true,[]);
  [...events].sort(compare).forEach(event=>{
    if(!visited.has(event.id))visit(event,event.kind==="request"?0:1,true,event.kind==="request"?[]:[false]);
  });
  return ordered;
}

function timelineTreeConnector(depth,isLast,ancestorContinuations,hasChildren){
  const ancestors=(ancestorContinuations||[]).map(continues=>`<i class="${continues?"pass":"blank"}"></i>`).join("");
  const node=depth===0?`<i class="root${hasChildren?" parent":""}"></i>`:`<i class="${isLast?"end":"fork"}"></i>`;
  const child=hasChildren?'<i class="child"></i>':'<i class="blank"></i>';
  return`<span class="gantt-tree" aria-hidden="true">${ancestors}${node}${child}</span>`;
}

function timelineCriticalPath(events,first,last){
  const blockingKinds=new Set(["middleware","query","cache","http_call"]);
  const operations=events.filter(event=>blockingKinds.has(event.kind)&&eventDuration(event)>0).map(event=>{
    const start=Math.max(first,Date.parse(event.started_at)||first);
    const end=Math.min(last,start+eventDuration(event)/1e6);
    return{event,start,end,weight:Math.max(0,end-start)};
  }).filter(operation=>operation.weight>0).sort((left,right)=>left.end-right.end||left.start-right.start);
  const best=Array(operations.length+1).fill(0);
  const previous=Array(operations.length).fill(-1);
  const take=Array(operations.length+1).fill(false);
  for(let index=0;index<operations.length;index++){
    for(let candidate=index-1;candidate>=0;candidate--){
      if(operations[candidate].end<=operations[index].start){previous[index]=candidate;break;}
    }
    const withCurrent=operations[index].weight+best[previous[index]+1];
    const withoutCurrent=best[index];
    if(withCurrent>withoutCurrent){best[index+1]=withCurrent;take[index+1]=true;}
    else best[index+1]=withoutCurrent;
  }
  const selected=[];
  for(let cursor=operations.length;cursor>0;){
    if(take[cursor]){
      const operation=operations[cursor-1];
      selected.push(operation.event);
      cursor=previous[cursor-1]+1;
    }else cursor--;
  }
  selected.reverse();
  const ids=new Set(selected.map(event=>event.id));
  const request=events.find(event=>event.kind==="request");
  if(request)ids.add(request.id);
  const bottleneck=selected.reduce((largest,event)=>!largest||eventDuration(event)>eventDuration(largest)?event:largest,null);
  return{ids,bottleneck,duration:best[operations.length]};
}

function timelineBreakdown(events){
  const totals=new Map();
  for(const event of events){
    if(event.kind==="request")continue;
    const value=Math.max(eventDuration(event)/1e6,0);
    if(value)totals.set(event.kind,(totals.get(event.kind)||0)+value);
  }
  const parts=[...totals].map(([kind,value])=>({kind,value})).sort((left,right)=>right.value-left.value);
  const total=parts.reduce((sum,part)=>sum+part.value,0);
  if(!total)return"";
  let cursor=0;
  const segments=parts.map(part=>{
    const width=part.value/total*1000;
    const segment=`<rect data-kind="${escapeHTML(part.kind)}" x="${cursor.toFixed(2)}" y="0" width="${Math.max(width,1).toFixed(2)}" height="12"/>`;
    cursor+=width;
    return segment;
  }).join("");
  const legend=parts.map(part=>`<span><i data-kind="${escapeHTML(part.kind)}"></i><b>${escapeHTML(timelineKindLabel(part.kind))}</b><em>${escapeHTML(formatMilliseconds(part.value))}</em><small>${Math.round(part.value/total*100)}%</small></span>`).join("");
  return`<section class="gantt-breakdown"><div><strong>Operation breakdown</strong><span>Aggregated recorded time</span></div><svg viewBox="0 0 1000 12" preserveAspectRatio="none" role="img" aria-label="Operation time breakdown">${segments}</svg><div class="gantt-legend">${legend}</div></section>`;
}

function timelineAxis(windowMS){
  return`<span class="gantt-axis"><span>0</span><span>${escapeHTML(formatMilliseconds(windowMS*.25))}</span><span>${escapeHTML(formatMilliseconds(windowMS*.5))}</span><span>${escapeHTML(formatMilliseconds(windowMS*.75))}</span><span>${escapeHTML(formatMilliseconds(windowMS))}</span></span>`;
}

function timelineKindLabel(kind){
  const labels={request:"Request",middleware:"Middleware",query:"SQL",cache:"Cache",http_call:"HTTP",job:"Job",email:"Mail",log:"Log",schedule:"Schedule",exception:"Exception",event:"Event"};
  return labels[kind]||kindSingular(kind);
}

function formatMilliseconds(value){
  return value>=1000?`${(value/1000).toFixed(2)} s`:`${value.toFixed(value>=10?1:2)} ms`;
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
  if(event.kind==="query")return short(data.sql||"")||data.operation||"SQL";
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
