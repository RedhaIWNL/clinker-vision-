package web

import "net/http"

const indexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Clinker Vision</title><style>
body{font:16px system-ui,sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;background:#f4f5f7;color:#20242a}
h1{margin-bottom:.25rem}.muted{color:#68717c}.filters{display:flex;gap:.5rem;flex-wrap:wrap;margin:1rem 0}select,button{font:inherit;padding:.45rem .7rem}button{cursor:pointer}.card{background:white;border:1px solid #d8dce2;border-radius:8px;padding:1rem;margin:.75rem 0;display:grid;grid-template-columns:240px 1fr;gap:1rem}.card img{width:240px;max-height:180px;object-fit:contain;background:#111}.unseen{border-left:5px solid #d97706}.meta{display:grid;grid-template-columns:max-content 1fr;gap:.3rem 1rem}.meta dt{font-weight:600}.meta dd{margin:0}.error{color:#b42318}@media(max-width:650px){.card{grid-template-columns:1fr}.card img{width:100%;max-height:none}}
</style></head><body><h1>Clinker Vision</h1><p class="muted">Local alert viewer</p>
<div class="filters"><label>Camera <select id="camera"><option value="">All</option><option>CAM-1</option><option>CAM-2</option><option>CAM-3</option><option>CAM-4</option><option>CAM-5</option><option>CAM-6</option></select></label><label><input id="unseen" type="checkbox"> Unseen only</label><button id="refresh">Refresh</button></div>
<p id="status" class="muted">Loading…</p><main id="alerts"></main>
<script>
const statusEl=document.getElementById('status'),listEl=document.getElementById('alerts');
function esc(value){return String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
async function load(){statusEl.textContent='Loading…';statusEl.className='muted';const q=new URLSearchParams();const camera=document.getElementById('camera').value;if(camera)q.set('camera_id',camera);if(document.getElementById('unseen').checked)q.set('unseen_only','true');q.set('limit','50');try{const response=await fetch('/api/v1/alerts?'+q);if(!response.ok)throw new Error('HTTP '+response.status);const data=await response.json();listEl.innerHTML=data.items.map(a=>{const score=a.confidence==null?'':(' — '+Math.round(a.confidence*100)+'%');const state=a.state?(' · '+a.state):'';return '<article class="card '+(a.seen_at?'':'unseen')+'"><a href="'+esc(a.evidence_url)+'"><img src="'+esc(a.evidence_url)+'" alt="'+esc(a.fault_type)+' evidence"></a><div><h2>'+esc(a.fault_type)+score+esc(state)+'</h2><dl class="meta"><dt>Camera</dt><dd>'+esc(a.camera_id)+'</dd><dt>Target</dt><dd>'+esc(a.observation_target)+'</dd><dt>Godet</dt><dd>'+esc(a.godet_id||'—')+'</dd><dt>Loop</dt><dd>'+esc(a.loop_no||'—')+'</dd><dt>Detected</dt><dd>'+esc(new Date(a.detected_at).toLocaleString())+'</dd><dt>Model</dt><dd>'+esc(a.model_version)+'</dd></dl><p>'+(a.seen_at?'Seen':'Unseen')+' <button data-id="'+esc(a.alert_id)+'" '+(a.seen_at?'disabled':'')+'>Mark seen</button></p></div></article>';}).join('');if(!data.items.length)listEl.innerHTML='<p class="muted">No alerts found.</p>';statusEl.textContent=data.items.length+' alert(s)';listEl.querySelectorAll('button[data-id]').forEach(button=>button.addEventListener('click',async()=>{await fetch('/api/v1/alerts/'+button.dataset.id+'/seen',{method:'POST'});load();}));}catch(error){statusEl.textContent='Could not load alerts: '+error.message;statusEl.className='error';}}
document.getElementById('refresh').addEventListener('click',load);document.getElementById('camera').addEventListener('change',load);document.getElementById('unseen').addEventListener('change',load);load();
</script></body></html>`

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
