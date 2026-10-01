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
.cams{display:flex;flex-direction:column;gap:2px}
.kinds{display:grid;grid-template-columns:1fr 1fr;gap:0;border:1px solid var(--line2);border-radius:3px;overflow:hidden}
.kinds button{background:var(--bg1);border:none;padding:7px 6px;cursor:pointer;color:var(--mut);font-size:13px;display:flex;justify-content:center;gap:6px}
.kinds button+button{border-left:1px solid var(--line2)}
.kinds button[aria-selected=true]{background:var(--steel);color:#fff;font-weight:600}
.kinds button .cnt{font-family:var(--mono);font-size:12px;opacity:.8}
.limits{border:1px solid var(--line);border-radius:3px;padding:10px;background:var(--bg0)}
.limits .row{display:grid;grid-template-columns:1fr 58px;gap:8px;align-items:center;margin-bottom:8px;font-size:12px;color:var(--text)}
.limits input{width:58px;text-align:center}
.limits .hint{font-size:11px;color:var(--dim);margin:2px 0 8px}
.limits .btn{width:100%;justify-content:center}
.hours{border:1px solid var(--line);border-radius:3px;background:var(--bg0);margin-top:10px}
.hours summary{cursor:pointer;padding:8px 10px;font-size:12px;font-weight:650;letter-spacing:.06em;text-transform:uppercase;color:var(--mut)}
.hours .body{padding:0 10px 10px}
.hours .cam{border-top:1px solid var(--line);padding:8px 0}
.hours .cam b{font-size:13px}
.hours select{width:100%;margin:4px 0;font-size:12px;min-width:0}
.hours .tm{display:grid;grid-template-columns:1fr 1fr;gap:6px}
.hours .tm input{width:100%;min-width:0;font-size:12px;padding:4px 2px;box-sizing:border-box}
.hours .st{font-size:11px;color:var(--dim);margin-top:3px}
.hours .warn{font-size:12px;color:var(--red);margin:6px 0}
.hours .hint{font-size:11px;color:var(--dim);margin:6px 0}
.hours .btn{width:100%;justify-content:center}
.hiddenn{padding:6px 12px;font-size:12px;color:var(--dim);border-bottom:1px solid var(--line)}
.dtype{display:inline-block;margin-left:6px;padding:0 6px;border-radius:3px;font-size:11px;font-weight:650;letter-spacing:.04em;background:#F6E3E4;color:var(--red)}
.dtype.wheel{background:#F5EBD9;color:var(--amber)}
.explain{padding:9px 12px;font-size:13px;border-top:1px solid var(--line)}
</style></head><body>
<div class="cmdbar">
<span class="app"><svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="#e5484d" stroke-width="1.8"><path d="M9 2a5 5 0 0 1 5 5v3l1.4 2.6H2.6L4 10V7a5 5 0 0 1 5-5z"/><path d="M7 14.5a2 2 0 0 0 4 0"/></svg>CLINKER VISION</span>
<span class="task" id="tasklabel">Alarm monitoring</span><span class="sp"></span>
<span id="activebadge" class="badge clear"><span class="n" id="activen">0</span><span id="activet">CLEAR</span></span>
<span id="syshealth" class="sys"><span class="dot"></span><span id="syslabel">Connecting</span><span class="mono" id="sysver"></span></span>
<span class="clock" id="clock">--:--:--</span>
<button class="livebtn" id="livebtn" aria-pressed="true"><span class="pip"></span><span>Live</span></button>
</div>
<div class="desk" id="desk">
<nav class="pane" aria-label="Alarm filters">
<h3>Camera</h3><div class="fgroup seg cams" id="cams" role="tablist" aria-label="Camera"></div>
<h3>Report</h3><div class="fgroup kinds" role="tablist" aria-label="Godets or wheels">
<button data-kind="godet" aria-selected="true">Godets<span class="cnt" id="cnt-godet"></span></button>
<button data-kind="galet" aria-selected="false">Wheels<span class="cnt" id="cnt-galet"></span></button>
</div>
<h3>Show</h3><div class="fgroup seg" id="shows" role="tablist" aria-label="Alarm state filter">
<button data-show="active" aria-selected="true">Active<span class="cnt" id="cnt-active"></span></button>
<button data-show="acknowledged" aria-selected="false">Acknowledged<span class="cnt" id="cnt-acked"></span></button>
<button data-show="all" aria-selected="false">All<span class="cnt" id="cnt-all"></span></button>
</div>
<div class="fgroup"><label for="godet">Godet number</label>
<input id="godet" type="search" placeholder="e.g. 812" autocomplete="off"></div>
<div class="fgroup limits" id="limits" hidden>
<h3 style="margin-top:0">Wheel alert limits</h3>
<div class="row"><span>Maximum godets in a row without a wheel</span><input id="lim-max" type="number" min="2" max="20"></div>
<div class="hint">Alert "too few wheels" above this number.</div>
<div class="row"><span>Minimum spacing between two wheels (godets)</span><input id="lim-min" type="number" min="1" max="6"></div>
<div class="hint">Alert "too many wheels" when two wheels are closer than this.</div>
<button class="btn ack" id="lim-save">Save limits</button>
<div class="hint" id="lim-note" style="margin-top:8px">Applies from the next chain loop (about 16 minutes). Alerts inside the limits are hidden.</div>
</div>
<details class="hours" id="hours" hidden><summary>Camera hours</summary><div class="body">
<div class="hint">When each camera runs. The server handles one camera at a time: avoid overlapping hours. Applies within 15 seconds, no restart.</div>
<div id="hours-cams"></div>
<div class="warn" id="hours-warn" hidden></div>
<button class="btn ack" id="hours-save">Save hours</button>
</div></details>
<p class="note" id="healthnote"></p>
</nav>
<section class="alarms" aria-label="Alarm list">
<div class="listhead"><span id="listtitle">Active alarms</span><span class="sp"></span><span id="listcount"></span><button id="refresh">Refresh</button></div>
<div id="hiddenn" class="hiddenn" hidden></div>
<div class="rows" id="rows" role="listbox" aria-label="Alarms"></div>
</section>
<main class="canvas" id="canvas" aria-label="Selected alarm"></main>
</div>
<div id="toasts" aria-live="polite"></div>
<script>
"use strict";
const $=id=>document.getElementById(id);
const esc=v=>String(v==null?"":v).replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
const store={items:new Map(),show:"active",camera:null,kind:"godet",godet:"",selected:null,live:true,first:true,
  limits:{wheel_max_gap_godets:4,wheel_min_spacing_godets:3},status:null};
const fmtT=iso=>{try{return new Date(iso).toLocaleString(void 0,{day:"2-digit",month:"short",hour:"2-digit",minute:"2-digit",second:"2-digit"});}catch(e){return String(iso||"—");}};
const fmtAge=iso=>{if(!iso)return "—";const s=Math.max(0,Math.round((Date.now()-new Date(iso).getTime())/1000));if(s<5)return "just now";if(s<60)return s+"s ago";const m=Math.floor(s/60);return m<60?m+"m ago":Math.floor(m/60)+"h ago";};
function toast(msg,kind){const d=document.createElement("div");d.className="toast "+(kind||"");d.textContent=msg;$("toasts").appendChild(d);setTimeout(()=>d.remove(),6500);}
function sevOf(a){if(a.seen_at)return "acked";return (a.state==="confirmed")?"critical":"major";}
/* ---- what an alarm is, in plain words ---- */
function meas(a){if(a._m===void 0){try{a._m=JSON.parse(a.measurements_json||"null")||{};}catch(e){a._m={};}}return a._m;}
const isWheel=a=>a.observation_target==="galet";
const DTYPE={1:"Cut",2:"Out of line",3:"Cut + out of line"};
function num(v){return v==null||v===""?null:Math.round(Number(v));}
function label(a){const m=meas(a);
if(isWheel(a)){if(a.fault_type==="WHEEL_GAP")return "Too few wheels";if(a.fault_type==="WHEEL_DENSITY")return "Too many wheels";if(a.fault_type==="WHEEL_MISSING")return "Wheel missing";return a.fault_type;}
if(m.damage_type!=null)return DTYPE[num(m.damage_type)]||"Damage";
if(m.lip!=null)return "Lip damage";return "Side-plate fault";}
function place(a){const m=meas(a);if(isWheel(a)){const f=num(m.first_godet),l=num(m.last_godet);
if(f!=null&&l!=null&&f!==l)return "Godets "+f+"–"+l;return "Godet "+(f!=null?f:(a.godet_id||"—"));}
return "Godet #"+(a.godet_id||"—");}
function summary(a){const m=meas(a),L=store.limits;
if(a.fault_type==="WHEEL_GAP")return num(m.godets)+" godets in a row without a wheel (limit: at most "+L.wheel_max_gap_godets+")";
if(a.fault_type==="WHEEL_DENSITY")return num(m.wheels)+" wheels within "+num(m.godets)+" godets"+(m.min_spacing!=null?"; the closest two are "+num(m.min_spacing)+" godet"+(num(m.min_spacing)===1?"":"s")+" apart (limit: at least "+L.wheel_min_spacing_godets+")":"");
if(a.fault_type==="WHEEL_MISSING")return "A wheel seen on earlier chain loops is absent on the 2 latest loops.";
if(m.damage_type!=null)return "The godet's outside (side plate / lower part) is "+String(DTYPE[num(m.damage_type)]||"damaged").toLowerCase()+".";
return "";}
/* wheel alarms that no longer break the limits set on this page are hidden */
function inLimits(a){if(!isWheel(a))return false;const m=meas(a),L=store.limits;
if(a.fault_type==="WHEEL_GAP")return num(m.godets)!=null&&num(m.godets)<=L.wheel_max_gap_godets;
if(a.fault_type==="WHEEL_DENSITY")return m.min_spacing!=null&&num(m.min_spacing)>=L.wheel_min_spacing_godets;
return false;}
/* ---- clock ---- */
function tickClock(){const d=new Date();$("clock").textContent=[d.getHours(),d.getMinutes(),d.getSeconds()].map(x=>String(x).padStart(2,"0")).join(":");}
tickClock();setInterval(tickClock,1000);
/* ---- cameras ---- */
function cameras(){const s=new Set();const st=store.status;if(st&&st.cameras)st.cameras.forEach(c=>{if(c.camera_id)s.add(c.camera_id);});else if(st&&st.camera_id)s.add(st.camera_id);
for(const a of store.items.values())s.add(a.camera_id);return Array.from(s).sort();}
function camStatus(id){const st=store.status;if(!st)return null;if(st.cameras){return st.cameras.find(c=>c.camera_id===id)||null;}return st.camera_id===id?st:null;}
function renderCams(){const list=cameras();if(list.length&&(!store.camera||list.indexOf(store.camera)<0)){
let best=null;for(const c of list){if(activeCount(c,null)>0){best=c;break;}}store.camera=best||list[0]||null;}
$("cams").innerHTML=list.length?list.map(c=>{const n=activeCount(c,null),cs=camStatus(c);
const st=cs?String(cs.status||"").replace(/_/g," "):"";
return '<button data-cam="'+esc(c)+'" aria-selected="'+(c===store.camera)+'"><span>'+esc(c)+(st?' <span style="color:var(--dim);font-size:11px">'+esc(st)+'</span>':'')+'</span><span class="cnt">'+(n||"")+'</span></button>';}).join(""):'<div class="note">No camera yet.</div>';
$("cams").querySelectorAll("button").forEach(b=>b.addEventListener("click",()=>{store.camera=b.getAttribute("data-cam");store.selected=null;renderAll();}));
$("tasklabel").textContent=store.camera?store.camera+" — "+(store.kind==="galet"?"Wheels":"Godets"):"Alarm monitoring";}
/* ---- system health ---- */
async function pollStatus(){try{const r=await fetch("/api/v1/status");if(!r.ok)throw new Error("HTTP "+r.status);const st=await r.json();store.status=st;
const sys=$("syshealth");sys.className="sys "+(!st.last_poll_at?"":!st.ready?(st.status==="template_lost"?"bad":"warn"):(st.status==="ok"?"ok":"warn"));
$("syslabel").textContent=!st.last_poll_at?"Starting":!st.ready?(st.status||"not ready").replace(/_/g," "):(st.status==="ok"?"Running":"Running — "+String(st.status).replace(/_/g," "));
$("sysver").textContent=st.model_version||"";renderCams();renderHealth();
}catch(e){$("syshealth").className="sys bad";$("syslabel").textContent="Unreachable";$("healthnote").textContent=String(e.message||e);}}
function renderHealth(){const c=store.camera&&camStatus(store.camera);if(!c){$("healthnote").textContent="";return;}
const n=c.counters&&c.counters.frames_total!=null?Number(c.counters.frames_total).toLocaleString("en-US")+" frames":"";
$("healthnote").textContent=c.camera_id+": "+String(c.status||"").replace(/_/g," ")+(n?" · "+n:"")+(c.detail?". "+c.detail:"")+(c.last_error?". Last error: "+c.last_error:"");}
/* ---- alarm list ---- */
function matches(a,cam,kind){if(cam&&a.camera_id!==cam)return false;if(kind&&(isWheel(a)?"galet":"godet")!==kind)return false;
if(inLimits(a))return false;if(store.godet&&String(a.godet_id||"").indexOf(store.godet)<0)return false;return true;}
function activeCount(cam,kind){let n=0;for(const a of store.items.values())if(!a.seen_at&&matches(a,cam,kind))n++;return n;}
function visible(){const out=[];for(const a of store.items.values()){if(!matches(a,store.camera,store.kind))continue;
if(store.show==="active"&&a.seen_at)continue;if(store.show==="acknowledged"&&!a.seen_at)continue;out.push(a);}
out.sort((x,y)=>new Date(y.detected_at)-new Date(x.detected_at));return out;}
function counts(){let act=0,ack=0;for(const a of store.items.values()){if(!matches(a,store.camera,store.kind))continue;if(a.seen_at)ack++;else act++;}
$("cnt-active").textContent=act||"";$("cnt-acked").textContent=ack||"";$("cnt-all").textContent=(act+ack)||"";
$("cnt-godet").textContent=activeCount(store.camera,"godet")||"";$("cnt-galet").textContent=activeCount(store.camera,"galet")||"";
let all=0;for(const a of store.items.values())if(!a.seen_at&&!inLimits(a))all++;
const b=$("activebadge");$("activen").textContent=all;$("activet").textContent=all?"ACTIVE":"CLEAR";b.className="badge "+(all?"alarm":"clear");
let hid=0;if(store.kind==="galet")for(const a of store.items.values())if(a.camera_id===store.camera&&inLimits(a))hid++;
$("hiddenn").hidden=!hid;$("hiddenn").textContent=hid+" wheel alarm"+(hid===1?"":"s")+" hidden: inside the limits set on this page.";}
async function fetchPage(cursor,limit){const q=new URLSearchParams();q.set("limit",String(limit));if(cursor)q.set("cursor",cursor);
const r=await fetch("/api/v1/alerts?"+q);if(!r.ok)throw new Error("HTTP "+r.status);return r.json();}
async function load(full){try{let fresh=0,cursor=null,pages=0;
do{const d=await fetchPage(cursor,full?200:50);for(const a of d.items||[]){const old=store.items.get(a.alert_id);
if(!old&&!store.first)fresh++;store.items.set(a.alert_id,a);}cursor=d.next_cursor||null;pages++;}while(full&&cursor&&pages<10);
store.first=false;renderAll();if(fresh>0)toast(fresh+" new alarm"+(fresh===1?"":"s"),"new");
}catch(e){toast("Alarm list: "+(e.message||e),"error");}}
function saveHash(){try{history.replaceState(null,"","#cam="+encodeURIComponent(store.camera||"")+"&view="+(store.kind==="galet"?"wheels":"godets"));}catch(e){}}
(function readHash(){const h=new URLSearchParams(location.hash.slice(1));if(h.get("cam"))store.camera=h.get("cam");if(h.get("view")==="wheels")store.kind="galet";})();
function renderAll(){renderCams();counts();renderHealth();renderHoursStatus();saveHash();
document.querySelectorAll(".kinds button").forEach(x=>x.setAttribute("aria-selected",String(x.getAttribute("data-kind")===store.kind)));
$("limits").hidden=store.kind!=="galet";
const v=visible();if(!store.selected||!v.some(a=>a.alert_id===store.selected))store.selected=v.length?v[0].alert_id:null;
renderList();renderCanvas();$("listcount").textContent=v.length+" shown";}
function renderList(){const rows=$("rows"),vis=visible();
$("listtitle").textContent=(store.show==="active"?"Active":store.show==="acknowledged"?"Acknowledged":"All")+" — "+(store.kind==="galet"?"wheels":"godets");
if(!vis.length){rows.innerHTML='<div class="norows"><svg width="30" height="30" viewBox="0 0 30 30" fill="none" stroke="#4a525c" stroke-width="1.6"><circle cx="15" cy="15" r="9"/><path d="M15 9v6l4 2"/></svg><div>No alarms in this view.</div></div>';return;}
rows.innerHTML=vis.map(a=>{const sev=sevOf(a),acked=!!a.seen_at;
return '<button class="arow sev-'+sev+(acked?" st-acked":" st-active")+'" role="option" aria-selected="'+(a.alert_id===store.selected)+'" data-id="'+esc(a.alert_id)+'">'
+'<span class="sev"></span><span class="body"><span class="l1"><span class="src">'+esc(place(a))+'</span><span class="dtype'+(isWheel(a)?" wheel":"")+'">'+esc(label(a))+'</span></span>'
+'<span class="l2"><span>'+esc(isWheel(a)?summary(a):"Loop "+(a.loop_no||"—"))+'</span><span class="mono">'+esc(fmtT(a.detected_at))+'</span></span></span>'
+'<span class="st">'+(acked?"Acked":"Active")+'</span></button>';}).join("");
rows.querySelectorAll(".arow").forEach(b=>b.addEventListener("click",()=>{store.selected=b.getAttribute("data-id");renderList();renderCanvas();if(window.innerWidth<=1080)$("desk").classList.add("focus");}));}
/* ---- canvas tile + details ---- */
function details(a){const m=meas(a),rows=[];const f=(k,v)=>{if(v!==void 0&&v!==null&&v!=="")rows.push([k,v]);};
if(isWheel(a)){f("Problem",label(a));f("First godet",num(m.first_godet));f("Last godet",num(m.last_godet));f("Godets",num(m.godets));f("Wheels",num(m.wheels));
if(m.min_spacing!=null)f("Closest two wheels (godets apart)",num(m.min_spacing));}
else if(m.damage_type!=null){f("Damage type",DTYPE[num(m.damage_type)]);f("Cut (model, 0–1)",m.cut!=null?Number(m.cut).toFixed(2):null);
f("Out of line (model, 0–1)",m.out_of_line!=null?Number(m.out_of_line).toFixed(2):null);f("Score",m.severity!=null?Number(m.severity).toFixed(2):null);f("Chain loops seen",num(m.passes_seen));}
else if(m.lip!=null){f("Lip",m.lip);f("Near plate",String(m.near_plate));}
else{f("Severity",m.severity!=null?Number(m.severity).toFixed(2):null);f("Chain loops seen",num(m.passes_seen));}
return rows;}
function renderCanvas(){const c=$("canvas"),a=store.items.get(store.selected);
if(!a){c.innerHTML='<div class="empty-canvas">Select an alarm from the list to inspect its evidence and details.</div>';return;}
const sev=sevOf(a),m=meas(a),rows=details(a),legacy=m.lip!=null;
c.innerHTML='<div class="tile"><div class="overlay '+(sev==="acked"?"":sev)+'"><span class="aname">'+esc(label(a))+' — '+esc(place(a))+'</span>'
+'<span class="asrc">'+esc(a.camera_id)+' · Loop '+esc(a.loop_no||"—")+' · '+(a.state||"event").toUpperCase()+'</span><span class="sp"></span><span class="ats">'+esc(fmtT(a.detected_at))+'</span></div>'
+'<a href="'+esc(a.evidence_url)+'"><img src="'+esc(a.evidence_url)+'" alt="Evidence, '+esc(place(a))+'"></a>'
+'<div class="cmdbar2">'+(a.seen_at?'<span style="color:var(--mut);font-size:13px">Acknowledged '+esc(fmtT(a.seen_at))+'</span>':'<button class="btn ack" id="ackbtn"><svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M2 7.5l3.2 3L12 3.5"/></svg>Acknowledge</button>')
+'<button class="btn ghost" id="fullbtn">Open full image</button><span class="meta">'+esc(a.alert_id.slice(0,8))+' · '+esc(a.model_version)+'</span></div></div>'
+'<div class="detail"><h3>What was found</h3>'
+(summary(a)?'<div class="explain">'+esc(summary(a))+'</div>':'')
+(m.evidence==="latest_frame"?'<div class="explain" style="color:var(--amber)">Picture: the live view at the time of the alert (the picture of the fault was no longer available).</div>':'')
+(isWheel(a)?'<div class="explain" style="color:var(--mut)">Picture: the unrolled chain around the place; the box marks the godets concerned.</div>':
 legacy?'<div class="explain" style="color:var(--mut)">Box: fixed inspection region of the old Camera 1 view, not the damage itself.</div>':
 '<div class="explain" style="color:var(--mut)">Box: the plate the model judged.</div>')
+(rows.length?'<table class="mtable"><tr><th>Measurement</th><th>Value</th></tr>'+rows.map(x=>'<tr><td style="font-family:inherit">'+esc(x[0])+'</td><td>'+esc(x[1])+'</td></tr>').join("")+'</table>':'')
+'<h3>Timeline</h3><div class="timeline">'
+'<div class="tl done"><div class="rail"><span class="pt"></span><span class="ln"></span></div><div class="tx">Triggered <span class="t">'+esc(fmtT(a.detected_at))+'</span></div></div>'
+'<div class="tl '+(a.seen_at?"done":"")+'"><div class="rail"><span class="pt"></span><span class="ln"></span></div><div class="tx">'+(a.seen_at?('Acknowledged <span class="t">'+esc(fmtT(a.seen_at))+'</span>'):"Awaiting acknowledgment")+'</div></div>'
+'</div></div>'
+'<p class="foot">Event '+esc(a.event_key||"—")+' · evidence frame '+esc((a.evidence_frame_id||"").slice(0,13)||"—")+'</p>';
const ack=$("ackbtn");if(ack)ack.addEventListener("click",()=>acknowledge(a.alert_id,ack));
$("fullbtn").addEventListener("click",()=>window.open(a.evidence_url,"_blank"));}
async function acknowledge(id,btn){btn.disabled=true;try{const r=await fetch("/api/v1/alerts/"+encodeURIComponent(id)+"/seen",{method:"POST"});if(!r.ok)throw new Error("HTTP "+r.status);
const d=await r.json(),a=store.items.get(id);if(a)a.seen_at=d.seen_at;renderAll();toast("Alarm acknowledged.");}catch(e){btn.disabled=false;toast("Acknowledge failed: "+(e.message||e),"error");}}
/* ---- wheel limits ---- */
async function loadLimits(){try{const r=await fetch("/api/v1/settings");if(!r.ok)return;store.limits=await r.json();
$("lim-max").value=store.limits.wheel_max_gap_godets;$("lim-min").value=store.limits.wheel_min_spacing_godets;renderHours(store.limits);renderAll();}catch(e){}}
$("lim-save").addEventListener("click",async()=>{const body={wheel_max_gap_godets:Number($("lim-max").value),wheel_min_spacing_godets:Number($("lim-min").value)};
try{const r=await fetch("/api/v1/settings",{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
const d=await r.json();if(!r.ok)throw new Error(d.error||d.message||("HTTP "+r.status));store.limits=d;renderAll();
toast("Limits saved: the model uses them from the next chain loop (about 16 minutes).");}catch(e){toast("Limits not saved: "+(e.message||e),"error");}});
/* ---- camera hours ---- */
function renderHours(d){if(!d||!d.cameras||!d.cameras.length){$("hours").hidden=true;return;}$("hours").hidden=false;
const set=d.camera_hours||{};
$("hours-cams").innerHTML=d.cameras.map(c=>{const h=set[c.id],m=h?h.mode:"config",cf=c.config_hours||{},src=h&&h.mode==="hours"?h:(cf.mode==="hours"?cf:{start:"",stop:""});
const cfText=cf.mode==="hours"?cf.start+" - "+cf.stop:"always on";
return '<div class="cam" data-cam="'+esc(c.id)+'"><b>'+esc(c.id)+'</b>'+
'<select class="hm"><option value="config"'+(m==="config"?" selected":"")+'>Default ('+esc(cfText)+')</option>'+
'<option value="hours"'+(m==="hours"?" selected":"")+'>These hours</option><option value="always"'+(m==="always"?" selected":"")+'>Always on</option>'+
'<option value="off"'+(m==="off"?" selected":"")+'>Off</option></select>'+
'<div class="tm"><input class="hs" type="time" value="'+esc(src.start||"")+'" aria-label="'+esc(c.id)+' start"><input class="he" type="time" value="'+esc(src.stop||"")+'" aria-label="'+esc(c.id)+' stop"></div>'+
'<div class="st"></div></div>';}).join("");
document.querySelectorAll("#hours-cams .cam").forEach(el=>{const sync=()=>{const on=el.querySelector(".hm").value==="hours";el.querySelectorAll(".tm input").forEach(i=>i.disabled=!on);};
el.querySelector(".hm").addEventListener("change",sync);sync();});
const w=d.overlaps||[];$("hours-warn").hidden=!w.length;$("hours-warn").textContent=w.length?"Overlapping hours: "+w.join(", ")+". Both cameras will run at once and may lose frames.":"";
renderHoursStatus();}
function renderHoursStatus(){document.querySelectorAll("#hours-cams .cam").forEach(el=>{const c=camStatus(el.getAttribute("data-cam"));
el.querySelector(".st").textContent=c?("Now: "+(c.status==="standby"?"off":"running")+(c.status==="standby"&&c.detail?" ("+c.detail+")":"")):"";});}
$("hours-save").addEventListener("click",async()=>{const ch={};let bad="";
document.querySelectorAll("#hours-cams .cam").forEach(el=>{const id=el.getAttribute("data-cam"),m=el.querySelector(".hm").value;
if(m==="config"){ch[id]=null;return;}const h={mode:m};if(m==="hours"){h.start=el.querySelector(".hs").value;h.stop=el.querySelector(".he").value;
if(!h.start||!h.stop)bad=id+": set a start and a stop time.";}ch[id]=h;});
if(bad){toast("Hours not saved: "+bad,"error");return;}
try{const r=await fetch("/api/v1/settings",{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({camera_hours:ch})});
const d=await r.json();if(!r.ok)throw new Error(d.error||d.message||("HTTP "+r.status));renderHours(d);
toast("Hours saved: cameras start or stop within 15 seconds.");setTimeout(pollStatus,16000);}catch(e){toast("Hours not saved: "+(e.message||e),"error");}});
/* ---- controls ---- */
document.querySelectorAll("#shows button").forEach(b=>b.addEventListener("click",()=>{document.querySelectorAll("#shows button").forEach(x=>x.setAttribute("aria-selected","false"));b.setAttribute("aria-selected","true");store.show=b.getAttribute("data-show");store.selected=null;renderAll();}));
document.querySelectorAll(".kinds button").forEach(b=>b.addEventListener("click",()=>{store.kind=b.getAttribute("data-kind");store.selected=null;renderAll();}));
let gT=null;$("godet").addEventListener("input",e=>{clearTimeout(gT);gT=setTimeout(()=>{store.godet=e.target.value.trim();renderAll();},160);});
$("refresh").addEventListener("click",()=>{load(true);pollStatus();});
$("livebtn").addEventListener("click",()=>{store.live=!store.live;$("livebtn").setAttribute("aria-pressed",String(store.live));arm();});
document.addEventListener("keydown",e=>{if(e.target.matches("input,select"))return;const vis=visible();if(!vis.length)return;
let i=vis.findIndex(a=>a.alert_id===store.selected);
if(e.key==="ArrowDown"){store.selected=vis[Math.min(vis.length-1,i+1)].alert_id;renderList();renderCanvas();e.preventDefault();}
else if(e.key==="ArrowUp"){store.selected=vis[Math.max(0,i-1)].alert_id;renderList();renderCanvas();e.preventDefault();}});
let t1=null;function arm(){clearInterval(t1);if(store.live)t1=setInterval(()=>{if(!document.hidden){load(false);pollStatus();}},5000);}
document.addEventListener("visibilitychange",()=>{if(!document.hidden&&store.live){load(false);pollStatus();}});
pollStatus();loadLimits();load(true);arm();setInterval(()=>load(true),120000);
</script></body></html>`

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
