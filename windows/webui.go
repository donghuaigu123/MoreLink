package main

import (
	"net/http"
)

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>MoreLink · Windows</title>
<style>
:root{--bg:#0f1420;--card:#1a2132;--accent:#6ee7b7;--fg:#e6edf7;--mut:#8b98ad;--line:#2b3550}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,"Segoe UI",Roboto,"Microsoft YaHei",sans-serif}
header{padding:18px 24px;border-bottom:1px solid var(--line);display:flex;align-items:center;gap:12px}
header h1{font-size:20px;margin:0;color:var(--accent)}
.badge{background:#243;;color:var(--accent);padding:2px 10px;border-radius:20px;font-size:12px}
.wrap{max-width:980px;margin:0 auto;padding:24px}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:18px}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:18px}
.card h2{margin:0 0 14px;font-size:15px;color:var(--accent)}
.row{display:flex;gap:8px;margin-bottom:10px;flex-wrap:wrap}
input,button,select{background:#0d1320;color:var(--fg);border:1px solid var(--line);border-radius:8px;padding:9px 12px;font:inherit}
input{flex:1;min-width:120px}
button{background:var(--accent);color:#06281c;border:0;font-weight:600;cursor:pointer}
button:hover{filter:brightness(1.1)}
button.ghost{background:transparent;color:var(--accent);border:1px solid var(--accent)}
ul{list-style:none;margin:0;padding:0}
li{border-bottom:1px solid var(--line);padding:10px 0;display:flex;justify-content:space-between;gap:10px}
li:last-child{border:0}
.mut{color:var(--mut);font-size:12px}
.online{color:#6ee7b7}.offline{color:#f87171}
.tag{font-size:11px;background:#223;color:var(--mut);padding:1px 7px;border-radius:5px}
.status{font-size:13px;color:var(--mut)}
pre{white-space:pre-wrap;word-break:break-all;background:#0d1320;border-radius:8px;padding:10px;font-size:12px;max-height:220px;overflow:auto}
@media(max-width:760px){.grid{grid-template-columns:1fr}}
</style>
</head>
<body>
<header><h1>◆ MoreLink</h1><span class="badge">Windows</span><span class="status" id="status">加载中…</span></header>
<div class="wrap">
<div class="grid">
  <div class="card">
    <h2>本机状态</h2>
    <div class="mut">设备 ID</div><pre id="myid"></pre>
    <div class="mut">接收目录</div><pre id="dl"></pre>
    <div class="row" style="margin-top:12px">
      <button class="ghost" onclick="refresh()">刷新</button>
    </div>
  </div>
  <div class="card">
    <h2>配对</h2>
    <p class="mut">在手机上生成 6 位配对码，填入下方并输入手机设备 ID（留空则配对任意请求设备）。</p>
    <div class="row">
      <input id="pin" placeholder="6 位配对码" maxlength="6">
      <input id="peerid" placeholder="手机设备 ID（可选）">
    </div>
    <button onclick="doPair()">确认配对</button>
    <div class="mut" id="pairmsg" style="margin-top:8px"></div>
  </div>
  <div class="card">
    <h2>附近设备</h2>
    <ul id="peers"></ul>
  </div>
  <div class="card">
    <h2>发送剪贴板文本</h2>
    <div class="row"><input id="targetIp" placeholder="对端 IP"></div>
    <div class="row"><input id="clipText" placeholder="要发送的文本"></div>
    <button onclick="sendClip()">发送到对端</button>
    <div class="mut" id="clipmsg" style="margin-top:8px"></div>
  </div>
  <div class="card">
    <h2>发送文件</h2>
    <div class="row"><input id="fIp" placeholder="对端 IP"></div>
    <div class="row"><input id="fPath" placeholder="本机文件完整路径"></div>
    <button onclick="sendFile()">发送文件</button>
    <div class="mut" id="filemsg" style="margin-top:8px"></div>
  </div>
  <div class="card">
    <h2>文件接收记录</h2>
    <ul id="transfers"></ul>
  </div>
  <div class="card" style="grid-column:1/-1">
    <h2>手机通知</h2>
    <ul id="notifies"></ul>
  </div>
</div>
</div>
<script>
async function refresh(){
  const s=await (await fetch('/api/state')).json();
  document.getElementById('status').textContent=s.status+(s.paired?' · 已配对':' · 未配对');
  document.getElementById('myid').textContent=s.id;
  document.getElementById('dl').textContent=s.download;
  document.getElementById('peers').innerHTML=s.peers.map(p=>
    '<li><div><b>'+esc(p.name)+'</b> <span class="tag">'+p.platform+'</span><br><span class="mut">'+p.ip+':'+p.port+'</span><br><span class="mut">'+p.id+'</span></div>'+
    '<div><span class="'+(p.online?'online':'offline')+'">'+(p.online?'在线':'离线')+'</span><br>'+
    '<button class="ghost" onclick="usePeer(\''+p.ip+'\',\''+p.port+'\')">选用</button></div></li>').join('')||'<li class="mut">暂未发现设备</li>';
  document.getElementById('transfers').innerHTML=(s.transfers||[]).map(t=>
    '<li><span>'+esc(t.name)+'</span><span class="mut">'+t.status+' · '+t.size+' B</span></li>').join('')||'<li class="mut">暂无记录</li>';
  const ns=await (await fetch('/api/notify/list')).json();
  document.getElementById('notifies').innerHTML=(ns||[]).map(n=>
    '<li><div><b>'+esc(n.title)+'</b><br><span class="mut">'+esc(n.app)+' · '+esc(n.text)+'</span></div><span class="mut">'+new Date(n.ts*1000).toLocaleString()+'</span></li>').join('')||'<li class="mut">暂无通知</li>';
}
function esc(s){return (s||'').replace(/[<>]/g,c=>c==='<'?'&lt;':'&gt;')}
function usePeer(ip,port){document.getElementById('targetIp').value=ip;document.getElementById('fIp').value=ip}
async function doPair(){
  const pin=document.getElementById('pin').value.trim(),id=document.getElementById('peerid').value.trim();
  const r=await fetch('/api/pair?pin='+encodeURIComponent(pin)+'&id='+encodeURIComponent(id));
  document.getElementById('pairmsg').textContent=r.ok?'配对成功':'配对失败: '+await r.text();
  refresh();
}
async function sendClip(){
  const t=document.getElementById('clipText').value,ip=document.getElementById('targetIp').value.trim();
  const r=await fetch('/api/clip/send?text='+encodeURIComponent(t)+'&ip='+encodeURIComponent(ip));
  document.getElementById('clipmsg').textContent=r.ok?'已发送':'失败: '+await r.text();
}
async function sendFile(){
  const p=document.getElementById('fPath').value.trim(),ip=document.getElementById('fIp').value.trim();
  const r=await fetch('/api/file/send?path='+encodeURIComponent(p)+'&ip='+encodeURIComponent(ip));
  document.getElementById('filemsg').textContent=r.ok?'已发送':'失败: '+await r.text();
  refresh();
}
refresh();setInterval(refresh,2000);
</script>
</body></html>`

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
