package web

import "net/http"

// Alarm monitoring desk. Layout follows the Security Desk pattern:
// filter pane + alarm list (report pane) + canvas tile with overlay +
// acknowledge commands. Flat colors, 3px radius, no gradients, no emojis;
// all icons are inline SVG strokes.
const indexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Clinker Vision — Alarm monitoring</title><style>
:root{--bg0:#F3F4F4;--bg1:#FFFFFF;--bg2:#E7EDEE;--row:#FFFFFF;--rowhov:#ECF2F3;--rowsel:#D9E7EB;--line:#D7DDDF;--line2:#B7C4C9;--text:#061E29;--mut:#54676F;--dim:#8AA0AA;--red:#BE353F;--amber:#A96F14;--green:#2E7D46;--link:#1D546D;--ink:#061E29;--steel:#1D546D;--steel-dk:#13465C;--sage:#5F9598;--mono:Consolas,"SF Mono","Liberation Mono",monospace}
*{box-sizing:border-box}html,body{height:100%}
::selection{background:var(--steel);color:#fff}
body{margin:0;background:var(--bg0);color:var(--text);font:13px/1.45 "Segoe UI",system-ui,-apple-system,sans-serif}
button,select,input{font:inherit;color:var(--text)}
:focus-visible{outline:1px solid var(--link);outline-offset:1px}
/* ---- top command bar ---- */
.cmdbar{display:flex;align-items:center;gap:12px;height:48px;padding:0 14px;background:var(--ink);border-bottom:2px solid var(--steel);color:#F3F4F4}
.cmdbar .app{display:flex;align-items:center;gap:9px;font-weight:650;font-size:14px;letter-spacing:.04em;white-space:nowrap}
.cmdbar .app svg{flex:none}
.cmdbar .task{color:#93A9B2;font-size:13px;border-left:1px solid #23424F;padding-left:12px;white-space:nowrap}
.cmdbar .sp{flex:1}
.badge{display:inline-flex;align-items:center;gap:7px;padding:3px 10px;border:1px solid #23424F;border-radius:3px;font-size:12px;font-weight:650;letter-spacing:.05em;background:#0B2836;color:#C7D5DA;white-space:nowrap}
.badge .n{font-family:var(--mono);font-size:13px}
.badge.alarm{border-color:#D66060;color:#FFB4B6;background:#3A1518}
.badge.clear{color:#8AA0AA}
.sys{display:flex;align-items:center;gap:8px;font-size:12px;color:#93A9B2;white-space:nowrap}
.sys .dot{width:8px;height:8px;border-radius:50%;background:var(--dim)}
.sys.ok .dot{background:var(--green)}.sys.warn .dot{background:var(--amber)}.sys.bad .dot{background:var(--red)}
.sys .mono{font-family:var(--mono)}
.clock{font-family:var(--mono);font-size:13px;color:#F3F4F4}
.livebtn{display:inline-flex;align-items:center;gap:7px;background:#0B2836;border:1px solid #23424F;border-radius:3px;padding:4px 10px;cursor:pointer;font-size:12px;color:#E7EEF0}
.livebtn .pip{width:7px;height:7px;border-radius:50%;background:var(--green)}
.livebtn[aria-pressed=false] .pip{background:#5B6E77}
.livebtn:hover{border-color:var(--sage)}
/* ---- work area ---- */
.desk{display:grid;grid-template-columns:230px minmax(340px,460px) 1fr;height:calc(100vh - 48px);min-height:0}
/* filter pane */
.pane{border-right:1px solid var(--line);background:var(--bg1);padding:12px;overflow-y:auto;min-height:0}
.pane h3{margin:2px 0 8px;font-size:11px;font-weight:650;letter-spacing:.09em;color:var(--mut);text-transform:uppercase}
.fgroup{margin-bottom:14px}
.seg{display:flex;flex-direction:column;gap:2px}
.seg button{display:flex;align-items:center;gap:8px;background:none;border:none;border-left:2px solid transparent;border-radius:0;padding:6px 8px;cursor:pointer;color:var(--mut);font-size:13px;text-align:left;width:100%}
.seg button:hover{color:var(--text);background:var(--bg2)}
.seg button[aria-selected=true]{color:var(--text);border-left-color:var(--link);background:var(--bg2)}
.seg button .cnt{margin-left:auto;font-family:var(--mono);font-size:12px;color:var(--dim)}
.fgroup label{display:block;font-size:11px;letter-spacing:.07em;text-transform:uppercase;color:var(--mut);margin:0 0 4px}
.fgroup select,.fgroup input{width:100%;background:#fff;border:1px solid var(--line2);border-radius:3px;padding:6px 8px}
.fgroup input::placeholder{color:var(--dim)}
.pane .note{font-size:12px;color:var(--dim);margin-top:14px}
/* alarm list */
.alarms{border-right:1px solid var(--line);background:var(--bg0);display:flex;flex-direction:column;min-height:0;min-width:0}
.listhead{display:flex;align-items:center;gap:10px;padding:9px 12px;border-bottom:1px solid var(--line);font-size:11px;letter-spacing:.08em;text-transform:uppercase;color:var(--mut);flex:none}
.listhead .sp{flex:1}
.listhead button{background:none;border:1px solid var(--line2);border-radius:3px;color:var(--mut);padding:3px 9px;cursor:pointer;font-size:12px}
.listhead button:hover{color:var(--text)}
.rows{overflow-y:auto;flex:1;min-height:0}
.arow{display:grid;grid-template-columns:4px 1fr auto;gap:0;width:100%;background:none;border:none;border-bottom:1px solid var(--line);padding:0;cursor:pointer;text-align:left;color:var(--text)}
.arow .sev{width:4px}
.arow.sev-critical .sev{background:var(--red)}.arow.sev-major .sev{background:var(--amber)}.arow.sev-acked .sev{background:var(--dim)}
.arow .body{padding:8px 10px;min-width:0}
.arow .l1{display:flex;align-items:baseline;gap:8px}
.arow .src{font-weight:650;font-size:13px;white-space:nowrap}
.arow .inst{font-family:var(--mono);font-size:12px;color:var(--mut);white-space:nowrap}
.arow .l2{display:flex;gap:8px;margin-top:2px;font-size:12px;color:var(--mut)}
.arow .l2 .mono{font-family:var(--mono)}
.arow .st{padding:10px 12px 10px 0;font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:var(--mut);white-space:nowrap;align-self:start}
.arow.st-active .st{color:var(--amber)}.arow.st-active.sev-critical .st{color:var(--red)}
.arow:hover{background:var(--rowhov)}
.arow[aria-selected=true]{background:var(--rowsel);box-shadow:inset 2px 0 0 var(--link)}
.arow.acked{opacity:.62}
.norows{padding:28px 16px;text-align:center;color:var(--dim)}
.norows svg{margin-bottom:8px}
/* canvas */
.canvas{background:var(--bg0);overflow-y:auto;min-height:0;min-width:0;padding:14px}
.tile{background:#000;border:1px solid var(--ink);border-radius:3px;overflow:hidden;max-width:860px}
.tile .overlay{display:flex;align-items:center;gap:10px;padding:8px 12px;background:var(--ink);border-bottom:1px solid var(--steel);color:#F3F4F4}
.tile .overlay .aname{font-weight:700;font-size:13px;letter-spacing:.05em}
.tile .overlay.critical .aname{color:#FF9D9F}.tile .overlay.major .aname{color:#F0BE5F}
.tile .overlay .asrc{color:#93A9B2;font-size:12px}
.tile .overlay .sp{flex:1}
.tile .overlay .ats{font-family:var(--mono);font-size:12px;color:#93A9B2}
.tile img{display:block;width:100%;max-height:52vh;object-fit:contain;background:#000}
.tile .cmdbar2{display:flex;align-items:center;gap:8px;padding:9px 12px;background:var(--bg1);border-top:1px solid var(--line)}
.btn{display:inline-flex;align-items:center;gap:7px;border:1px solid var(--line2);background:var(--bg2);border-radius:3px;padding:6px 13px;cursor:pointer;font-size:13px}
.btn:hover:not(:disabled){border-color:var(--dim)}
.btn:disabled{opacity:.45;cursor:default}
.btn.ack{background:var(--steel);border-color:var(--steel);color:#fff;font-weight:600}
.btn.ack:hover:not(:disabled){background:var(--steel-dk);border-color:var(--steel-dk)}
.btn.ghost{background:none}
.cmdbar2 .meta{margin-left:auto;font-family:var(--mono);font-size:12px;color:var(--dim)}
.detail{max-width:860px;margin-top:12px;border:1px solid var(--line);border-radius:3px;background:var(--bg1)}
.detail h3{margin:0;padding:9px 12px;font-size:11px;font-weight:650;letter-spacing:.09em;text-transform:uppercase;color:var(--mut);border-bottom:1px solid var(--line)}
.dgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:1px;background:var(--line)}
.dgrid>div{background:var(--bg1);padding:8px 12px}
.dgrid .k{font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:var(--dim)}
.dgrid .v{font-family:var(--mono);font-size:13px;margin-top:1px;word-break:break-all}
.dgrid .v.plain{font-family:inherit}
.mtable{width:100%;border-collapse:collapse;font-size:13px}
.mtable th{font-size:11px;letter-spacing:.07em;text-transform:uppercase;color:var(--dim);font-weight:600;text-align:left;padding:8px 12px;border-bottom:1px solid var(--line)}
.mtable td{padding:7px 12px;border-bottom:1px solid var(--line);font-family:var(--mono)}
.mtable tr:last-child td{border-bottom:none}
.timeline{padding:10px 12px;display:flex;flex-direction:column;gap:0}
.tl{display:grid;grid-template-columns:14px 1fr;gap:10px}
.tl .rail{display:flex;flex-direction:column;align-items:center}
.tl .pt{width:9px;height:9px;border-radius:50%;border:2px solid var(--dim);margin-top:4px;background:var(--bg1)}
.tl.done .pt{border-color:var(--green);background:var(--green)}
.tl .ln{width:2px;flex:1;background:var(--line2);min-height:14px}
.tl:last-child .ln{display:none}
.tl .tx{padding:1px 0 12px;font-size:13px}.tl .tx .t{font-family:var(--mono);font-size:12px;color:var(--mut)}
.foot{max-width:860px;color:var(--dim);font-size:12px;margin:10px 2px}
#toasts{position:fixed;bottom:14px;right:14px;display:flex;flex-direction:column;gap:8px;z-index:30}
.toast{background:#fff;border:1px solid var(--line2);border-left:3px solid var(--link);border-radius:3px;padding:8px 12px;max-width:min(92vw,360px);font-size:13px;box-shadow:0 4px 16px rgba(6,30,41,.14)}
.toast.new{border-left-color:var(--amber)}.toast.error{border-left-color:var(--red)}
.empty-canvas{max-width:860px;border:1px dashed var(--line2);border-radius:3px;padding:48px 20px;text-align:center;color:var(--dim);background:var(--bg1)}
::-webkit-scrollbar{width:10px;height:10px}::-webkit-scrollbar-thumb{background:#B7C4C9;border:3px solid var(--bg0);border-radius:6px}::-webkit-scrollbar-track{background:transparent}
@media(max-width:1080px){.desk{grid-template-columns:210px 1fr}.canvas{display:none}.desk.focus .alarms{display:none}.desk.focus .canvas{display:block}}
@media(prefers-reduced-motion:no-preference){.arow.flash{animation:rowin 1.2s}.toast{animation:rowin .25s}}
@keyframes rowin{from{background:var(--rowsel)}}
</style></head><body>
<div class="cmdbar">
<span class="app"><svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="#e5484d" stroke-width="1.8"><path d="M9 2a5 5 0 0 1 5 5v3l1.4 2.6H2.6L4 10V7a5 5 0 0 1 5-5z"/><path d="M7 14.5a2 2 0 0 0 4 0"/></svg>CLINKER VISION</span>
<span class="task">Alarm monitoring</span><span class="sp"></span>
<span id="activebadge" class="badge clear"><span class="n" id="activen">0</span><span id="activet">CLEAR</span></span>
<span id="syshealth" class="sys"><span class="dot"></span><span id="syslabel">Connecting</span><span class="mono" id="sysver"></span></span>
<span class="clock" id="clock">--:--:--</span>
<button class="livebtn" id="livebtn" aria-pressed="true"><span class="pip"></span><span>Live</span></button>
</div>
<div class="desk" id="desk">
<nav class="pane" aria-label="Alarm filters">
<h3>Show</h3><div class="fgroup seg" role="tablist" aria-label="Alarm state filter">
<button data-show="active" aria-selected="true">Active<span class="cnt" id="cnt-active"></span></button>
<button data-show="acknowledged" aria-selected="false">Acknowledged<span class="cnt" id="cnt-acked"></span></button>
<button data-show="all" aria-selected="false">All<span class="cnt" id="cnt-all"></span></button>
</div>
<div class="fgroup"><label for="camera">Camera</label>
<select id="camera"><option value="">All cameras</option><option>CAM-1</option><option>CAM-2</option><option>CAM-3</option><option>CAM-4</option><option>CAM-5</option><option>CAM-6</option></select></div>
<div class="fgroup"><label for="godet">Godet number</label>
<input id="godet" type="search" placeholder="e.g. 812" autocomplete="off"></div>
<p class="note" id="healthnote"></p>
</nav>
<section class="alarms" aria-label="Alarm list">
<div class="listhead"><span id="listtitle">Active alarms</span><span class="sp"></span><span id="listcount"></span><button id="refresh">Refresh</button></div>
<div class="rows" id="rows" role="listbox" aria-label="Alarms"></div>
</section>
<main class="canvas" id="canvas" aria-label="Selected alarm"></main>
</div>
<div id="toasts" aria-live="polite"></div>
<script>
"use strict";
const $=id=>document.getElementById(id);
const esc=v=>String(v==null?"":v).replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;","&gt;":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
const store={items:new Map(),order:[],cursor:null,show:"active",camera:"",godet:"",selected:null,live:true,first:true};
const fmtT=iso=>{try{return new Date(iso).toLocaleString(void 0,{day:"2-digit",month:"short",hour:"2-digit",minute:"2-digit",second:"2-digit"});}catch(e){return String(iso||"—");}};
const fmtAge=iso=>{if(!iso)return "—";const s=Math.max(0,Math.round((Date.now()-new Date(iso).getTime())/1000));if(s<5)return "just now";if(s<60)return s+"s ago";const m=Math.floor(s/60);return m<60?m+"m ago":Math.floor(m/60)+"h ago";};
function toast(msg,kind){const d=document.createElement("div");d.className="toast "+(kind||"");d.textContent=msg;$("toasts").appendChild(d);setTimeout(()=>d.remove(),6500);}
function sevOf(a){if(a.seen_at)return "acked";return (a.state==="confirmed")?"critical":"major";}
/* ---- clock ---- */
function tickClock(){const d=new Date();$("clock").textContent=[d.getHours(),d.getMinutes(),d.getSeconds()].map(x=>String(x).padStart(2,"0")).join(":");}
tickClock();setInterval(tickClock,1000);
/* ---- system health ---- */
async function pollStatus(){try{const r=await fetch("/api/v1/status");if(!r.ok)throw new Error("HTTP "+r.status);const st=await r.json();
const sys=$("syshealth");sys.className="sys "+(!st.last_poll_at?"":!st.ready?(st.status==="template_lost"?"bad":"warn"):(st.status==="ok"?"ok":"warn"));
$("syslabel").textContent=!st.last_poll_at?"Starting":!st.ready?(st.status||"not ready").replace(/_/g," "):(st.status==="ok"?"Running":"Running — "+String(st.status).replace(/_/g," "));
$("sysver").textContent=st.model_version||"";
const det=[];det.push(st.loop_locked?"Loop locked":"Loop open");if(st.last_poll_at)det.push("poll "+fmtAge(st.last_poll_at));
if(st.counters&&st.counters.frames_total!=null)det.push(Number(st.counters.frames_total).toLocaleString("en-US")+" frames");
$("healthnote").textContent=det.join(" · ")+(st.detail?". "+st.detail:"")+(st.last_error?". Last poll error: "+st.last_error:"");
}catch(e){$("syshealth").className="sys bad";$("syslabel").textContent="Unreachable";$("healthnote").textContent=String(e.message||e);}}
/* ---- alarm list ---- */
function visible(){const out=[];for(const id of store.order){const a=store.items.get(id);if(!a)continue;
if(store.show==="active"&&a.seen_at)continue;if(store.show==="acknowledged"&&!a.seen_at)continue;
if(store.camera&&a.camera_id!==store.camera)continue;
if(store.godet&&String(a.godet_id||"").indexOf(store.godet)<0)continue;out.push(a);}return out;}
function counts(){let act=0,ack=0;for(const a of store.items.values()){if(store.camera&&a.camera_id!==store.camera)continue;
if(store.godet&&String(a.godet_id||"").indexOf(store.godet)<0)continue;if(a.seen_at)ack++;else act++;}
$("cnt-active").textContent=act||"";$("cnt-acked").textContent=ack||"";$("cnt-all").textContent=(act+ack)||"";
const b=$("activebadge");$("activen").textContent=act;$("activet").textContent=act?"ACTIVE":"CLEAR";b.className="badge "+(act?"alarm":"clear");}
async function load(append){try{const q=new URLSearchParams();if(store.camera)q.set("camera_id",store.camera);q.set("limit","50");if(append&&store.cursor)q.set("cursor",store.cursor);
const r=await fetch("/api/v1/alerts?"+q);if(!r.ok)throw new Error("HTTP "+r.status);const d=await r.json();let fresh=0;
for(const a of d.items||[]){if(!store.items.has(a.alert_id)&&!append&&!store.first)fresh++;store.items.set(a.alert_id,a);if(store.order.indexOf(a.alert_id)<0)store.order.push(a.alert_id);}
store.order.sort((x,y)=>new Date(store.items.get(y).detected_at)-new Date(store.items.get(x).detected_at));
store.cursor=d.next_cursor||null;store.first=false;
if(!store.selected||!store.items.has(store.selected)){const v=visible();store.selected=v.length?v[0].alert_id:null;}
renderList();renderCanvas();counts();
$("listcount").textContent=visible().length+" shown";
if(fresh>0)toast(fresh+" new alarm"+(fresh===1?"":"s"),"new");
}catch(e){toast("Alarm list: "+(e.message||e),"error");}}
function renderList(){const rows=$("rows"),vis=visible();
$("listtitle").textContent=store.show==="active"?"Active alarms":store.show==="acknowledged"?"Acknowledged alarms":"All alarms";
if(!vis.length){rows.innerHTML='<div class="norows"><svg width="30" height="30" viewBox="0 0 30 30" fill="none" stroke="#4a525c" stroke-width="1.6"><circle cx="15" cy="15" r="9"/><path d="M15 9v6l4 2"/></svg><div>No alarms in this view.</div></div>';return;}
rows.innerHTML=vis.map(a=>{const sev=sevOf(a),acked=!!a.seen_at;
return '<button class="arow sev-'+sev+(acked?" st-acked":" st-active")+'" role="option" aria-selected="'+(a.alert_id===store.selected)+'" data-id="'+esc(a.alert_id)+'">'
+'<span class="sev"></span><span class="body"><span class="l1"><span class="src">Godet #'+esc(a.godet_id||"—")+'</span><span class="inst">LOOP '+esc(a.loop_no||"—")+' · '+esc(a.camera_id)+'</span></span>'
+'<span class="l2"><span>'+esc((a.state||"event").toUpperCase())+' · '+esc(a.fault_type)+'</span><span class="mono">'+esc(fmtT(a.detected_at))+'</span></span></span>'
+'<span class="st">'+(acked?"Acked":"Active")+'</span></button>';}).join("");
rows.querySelectorAll(".arow").forEach(b=>b.addEventListener("click",()=>{store.selected=b.getAttribute("data-id");renderList();renderCanvas();if(window.innerWidth<=1080)$("desk").classList.add("focus");}));}
/* ---- canvas tile + details ---- */
function measurements(a){try{const m=JSON.parse(a.measurements_json||"null");if(!m||typeof m!=="object")return null;
return [["Lip before",m.lip_before],["Lip now",m.lip_now],["Drop",m.drop]].filter(x=>x[1]!==void 0&&x[1]!==null);}catch(e){return null;}}
function renderCanvas(){const c=$("canvas"),a=store.items.get(store.selected);
if(!a){c.innerHTML='<div class="empty-canvas">Select an alarm from the list to inspect its evidence and details.</div>';return;}
const sev=sevOf(a),ms=measurements(a);
c.innerHTML='<div class="tile"><div class="overlay '+(sev==="acked"?"":sev)+'"><span class="aname">'+esc(a.fault_type)+' — Godet #'+esc(a.godet_id||"—")+'</span>'
+'<span class="asrc">'+esc(a.camera_id)+' · Loop '+esc(a.loop_no||"—")+' · '+(a.state||"event").toUpperCase()+'</span><span class="sp"></span><span class="ats">'+esc(fmtT(a.detected_at))+'</span></div>'
+'<a href="'+esc(a.evidence_url)+'"><img src="'+esc(a.evidence_url)+'" alt="Evidence frame, godet '+esc(a.godet_id||"")+'"></a>'
+'<div class="cmdbar2">'+(a.seen_at?'<span style="color:var(--mut);font-size:13px">Acknowledged '+esc(fmtT(a.seen_at))+'</span>':'<button class="btn ack" id="ackbtn"><svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M2 7.5l3.2 3L12 3.5"/></svg>Acknowledge</button>')
+'<button class="btn ghost" id="fullbtn">Open full image</button><span class="meta">'+esc(a.alert_id.slice(0,8))+' · '+esc(a.model_version)+'</span></div></div>'
+'<div class="detail"><h3>Alarm details</h3><div class="dgrid">'
+'<div><div class="k">Event key</div><div class="v">'+esc(a.event_key||(a.godet_id!=null?("DAMAGE:"+a.godet_id+":"+a.loop_no):"—"))+'</div></div>'
+'<div><div class="k">Rule</div><div class="v">'+esc(a.rule_id||"—")+'</div></div>'
+'<div><div class="k">Evidence frame</div><div class="v">'+esc((a.evidence_frame_id||"").slice(0,13)||"—")+'</div></div>'
+'</div>'
+'<div style="padding:8px 12px;border-top:1px solid var(--line);font-size:12px;color:var(--dim)">Detection: <span style="color:var(--text)">fixed ROI indicator, full frame</span> — marks the inspected region, not localized damage.</div>'
+(ms?'<h3>Lip measurements (rows)</h3><table class="mtable"><tr><th>Signal</th><th>Value</th></tr>'+ms.map(x=>'<tr><td>'+esc(x[0])+'</td><td>'+esc(x[1])+'</td></tr>').join("")+'</table>':"")
+'<h3>Timeline</h3><div class="timeline">'
+'<div class="tl done"><div class="rail"><span class="pt"></span><span class="ln"></span></div><div class="tx">Triggered <span class="t">'+esc(fmtT(a.detected_at))+'</span></div></div>'
+'<div class="tl '+(a.seen_at?"done":"")+'"><div class="rail"><span class="pt"></span><span class="ln"></span></div><div class="tx">'+(a.seen_at?('Acknowledged <span class="t">'+esc(fmtT(a.seen_at))+'</span>'):"Awaiting acknowledgment")+'</div></div>'
+'</div></div>'
+'<p class="foot">Bounding box shown is the fixed compatibility ROI, identical on every detection.</p>';
const ack=$("ackbtn");if(ack)ack.addEventListener("click",()=>acknowledge(a.alert_id,ack));
$("fullbtn").addEventListener("click",()=>window.open(a.evidence_url,"_blank"));}
async function acknowledge(id,btn){btn.disabled=true;try{const r=await fetch("/api/v1/alerts/"+encodeURIComponent(id)+"/seen",{method:"POST"});if(!r.ok)throw new Error("HTTP "+r.status);
const d=await r.json(),a=store.items.get(id);if(a)a.seen_at=d.seen_at;renderList();renderCanvas();counts();toast("Alarm acknowledged.");}catch(e){btn.disabled=false;toast("Acknowledge failed: "+(e.message||e),"error");}}
/* ---- controls ---- */
document.querySelectorAll(".seg button").forEach(b=>b.addEventListener("click",()=>{document.querySelectorAll(".seg button").forEach(x=>x.setAttribute("aria-selected","false"));b.setAttribute("aria-selected","true");store.show=b.getAttribute("data-show");store.cursor=null;load(false);}));
$("camera").addEventListener("change",e=>{store.camera=e.target.value;store.cursor=null;load(false);});
let gT=null;$("godet").addEventListener("input",e=>{clearTimeout(gT);gT=setTimeout(()=>{store.godet=e.target.value.trim();renderList();counts();},160);});
$("refresh").addEventListener("click",()=>load(false));
$("livebtn").addEventListener("click",()=>{store.live=!store.live;$("livebtn").setAttribute("aria-pressed",String(store.live));arm();});
document.addEventListener("keydown",e=>{if(e.target.matches("input,select"))return;const vis=visible();if(!vis.length)return;
let i=vis.findIndex(a=>a.alert_id===store.selected);
if(e.key==="ArrowDown"){store.selected=vis[Math.min(vis.length-1,i+1)].alert_id;renderList();renderCanvas();e.preventDefault();}
else if(e.key==="ArrowUp"){store.selected=vis[Math.max(0,i-1)].alert_id;renderList();renderCanvas();e.preventDefault();}});
let t1=null;function arm(){clearInterval(t1);if(store.live)t1=setInterval(()=>{if(!document.hidden){load(false);pollStatus();}},5000);}
document.addEventListener("visibilitychange",()=>{if(!document.hidden&&store.live){load(false);pollStatus();}});
pollStatus();load(false);arm();setInterval(pollStatus,7000);
</script></body></html>`

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
