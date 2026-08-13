// Wails v3 服务绑定：window.go.services.App.方法名()
function api() {
  if (window.go && window.go.services && window.go.services.App) return window.go.services.App;
  return null;
}

function fmtTime(t) { return t && t !== '0001-01-01T00:00:00Z' ? new Date(t).toLocaleString('zh-CN') : '-'; }
function fmtDur(s) { if (!s||s<=0) return '-'; const m=Math.floor(s/60), sec=Math.floor(s%60); return m>0?m+'m'+sec+'s':sec+'s'; }
function fmtRemaining(secs) {
  if (secs<=0) return '已过期';
  if (secs>=86400) return Math.floor(secs/3600)+'h'+Math.floor((secs%3600)/60)+'m';
  if (secs>=3600) return Math.floor(secs/3600)+'h'+Math.floor((secs%3600)/60)+'m';
  return Math.floor(secs/60)+'m'+Math.floor(secs%60)+'s';
}
function ttlClass(s) { return s<=0?'bad':s<3600?'bad':s<86400?'warn':'ok'; }

async function refresh() {
  const a = api(); if (!a) { setTimeout(refresh, 1000); return; }
  try { render(await a.GetStatus()); } catch(e) { console.error(e); }
  setTimeout(refresh, 5000);
}

function render(s) {
  const db = document.getElementById('badge-daemon');
  if (db) { db.textContent = s.daemon_state==='running'?'运行中':'已停止'; db.className = 'badge '+(s.daemon_state==='running'?'on':'off'); }

  let ca = document.getElementById('cookie-area');
  if (ca && s.cookie) {
    if (s.cookie.present) {
      const cls = ttlClass(s.cookie.remaining_seconds||0);
      ca.innerHTML = `<div><div class="kv"><span class="l">令牌</span><span class="mono">${s.cookie.masked_value||'-'}</span></div>
        <div class="kv"><span class="l">状态</span>${s.cookie.valid?'<span class="badge ok">有效</span>':'<span class="badge err">无效</span>'}</div></div>
        <div><div class="kv"><span class="l">过期时间</span><span>${fmtTime(s.cookie.expires_at)}</span></div>
        <div class="kv"><span class="l">剩余</span><span class="ttl ${cls}">${fmtRemaining(s.cookie.remaining_seconds||0)}</span></div>
        <div class="kv"><span class="l">更新于</span><span>${fmtTime(s.cookie.updated_at)}</span></div></div>`;
    } else { ca.innerHTML = '<div class="empty">Cookie 未加载</div>'; }
  }

  let ra = document.getElementById('run-area');
  if (ra) {
    if (s.current_run) {
      ra.innerHTML = `<span class="label">状态</span><span><span class="spinner"></span> 进行中 — ${s.phase}</span><span class="label">开始</span><span>${fmtTime(s.current_run.started_at)}</span>`;
    } else if (s.last_run) {
      ra.innerHTML = `<span class="label">开始</span><span>${fmtTime(s.last_run.started_at)}</span>
        <span class="label">结束</span><span>${fmtTime(s.last_run.ended_at)}</span>
        <span class="label">耗时</span><span>${fmtDur(s.last_run.duration_seconds)}</span>
        <span class="label">状态</span><span>${s.last_run.success
          ?(s.last_run.skipped_accounts>0
            ?'<span class="badge ok">成功 '+s.last_run.ok_accounts+' / 跳过 '+s.last_run.skipped_accounts+'</span>'
            :'<span class="badge ok">全部成功 · '+s.last_run.total_orders+' 条</span>')
          :'<span class="badge err">'+(s.last_run.error||'失败')+'</span>'}</span>`;
    } else { ra.innerHTML = '<div class="empty">暂无抓取记录</div>'; }
  }

  let ga = document.getElementById('games-area');
  if (ga && s.games && s.games.length) {
    let rows = s.games.map(g =>
      `<tr><td>${g.game_name}</td><td>${g.table_key}</td>
        <td>${g.success?'<span class="cell-ok">'+g.record_count+' 条</span>':'<span class="cell-err" title="'+(g.error||'')+'">抓取失败</span>'}</td>
        <td>${g.feishu_synced
          ?(g.feishu_sync_error?'<span class="cell-err">写入失败</span>':'<span class="cell-ok">新增 '+(g.feishu_new||0)+' · 更新 '+(g.feishu_updated||0)+'</span>')
          :'<span style="color:var(--muted)">等待写入</span>'}</td></tr>`).join('');
    ga.innerHTML = '<table><thead><tr><th>游戏</th><th>表格</th><th>抓取结果</th><th>飞书写入</th></tr></thead><tbody>'+rows+'</tbody></table>';
  } else if (ga) { ga.innerHTML = '<div class="empty">暂无抓取记录</div>'; }

  let ls = document.getElementById('login-status');
  if (ls) {
    if (s.login_phase==='running') ls.innerHTML='<span class="msg info"><span class="spinner"></span>正在登录中…</span>';
    else if (s.login_phase==='success') ls.innerHTML='<span class="msg ok">✅ 登录成功</span>';
    else if (s.login_phase==='failed') ls.innerHTML='<span class="msg err">❌ 登录失败: '+(s.login_error||'')+'</span>';
  }
  let st = document.getElementById('server-time');
  if (st && s.server_time) st.textContent = new Date(s.server_time).toLocaleString('zh-CN');
}

async function triggerLogin() {
  const a = api(); if (!a) return;
  const btn = document.getElementById('btn-login');
  btn.disabled = true; btn.textContent = '登录中…';
  document.getElementById('login-status').innerHTML = '<span class="msg info"><span class="spinner"></span>正在登录中…</span>';
  try {
    const r = await a.TriggerLogin();
    if (r === 'already_running') document.getElementById('login-status').innerHTML = '<span class="msg info">登录已在执行中</span>';
    else if (r === 'browser_error') document.getElementById('login-status').innerHTML = '<span class="msg err">浏览器未初始化</span>';
  } catch(e) {
    document.getElementById('login-status').innerHTML = '<span class="msg err">失败: '+e.message+'</span>';
    btn.disabled = false; btn.textContent = '获取 Cookie（自动登录）';
  }
}

async function importCookies() {
  const a = api(); if (!a) return;
  const raw = document.getElementById('cookie-json').value.trim();
  if (!raw) { im('请粘贴 Cookie JSON', 'err'); return; }
  try { JSON.parse(raw); } catch(e) { im('JSON 格式错误', 'err'); return; }
  im('<span class="spinner"></span>导入中…', 'info');
  try {
    const r = await a.ImportCookies(raw);
    if (r.status === 'ok') { im('导入成功！共 '+r.count+' 个 Cookie', 'ok'); document.getElementById('cookie-json').value = ''; }
    else im('导入失败: '+r.message, 'err');
  } catch(e) { im('失败: '+e.message, 'err'); }
}
function im(msg, cls) { document.getElementById('import-msg').innerHTML = '<span class="msg '+cls+'">'+msg+'</span>'; }

async function runScrape() {
  const a = api(); if (!a) return;
  const btn = document.getElementById('btn-scrape'), msg = document.getElementById('scrape-msg');
  btn.disabled = true; msg.textContent = '抓取中…';
  try {
    const r = await a.RunScrape();
    if (r === 'login_in_progress') msg.textContent = '登录进行中，请稍后再试';
    else if (r === 'scrape_in_progress') msg.textContent = '抓取已在进行中';
    else if (r === 'browser_error') msg.textContent = '浏览器未初始化';
    else msg.textContent = '抓取已启动';
  } catch(e) { msg.textContent = '失败: '+e.message; }
  setTimeout(() => { btn.disabled = false; msg.textContent = ''; }, 3000);
}

async function checkInit() {
  const a = api(); if (!a) return;
  try {
    const s = await a.GetInitStatus();
    const cs = document.getElementById('conn-status');
    if (cs) { cs.textContent = '✅ Go 服务已连接 · 浏览器'+(s.browser_ready?'就绪':'未就绪'); cs.style.background='#dcfce7'; cs.style.color='#166534'; }
    if (!s.browser_ready) {
      document.getElementById('badge-daemon').textContent = '浏览器未初始化';
      document.getElementById('badge-daemon').className = 'badge err';
      document.getElementById('login-status').innerHTML = '<span class="msg err">❌ Chrome 未找到，请安装 Chrome 浏览器</span>';
      document.getElementById('btn-login').disabled = true;
      document.getElementById('btn-scrape').disabled = true;
    }
  } catch(e) {
    const cs = document.getElementById('conn-status');
    if (cs) { cs.textContent = '❌ Go 服务连接失败: '+e.message; cs.style.background='#fef2f2'; cs.style.color='#991b1b'; }
  }
}

document.addEventListener('DOMContentLoaded', () => {
  let tries = 0;
  const check = setInterval(() => {
    if (window.go && window.go.services && window.go.services.App) { clearInterval(check); checkInit(); refresh(); }
    if (++tries > 100) { clearInterval(check); document.body.innerHTML += '<div style="color:red;padding:20px">Go 服务未连接，请重启应用</div>'; }
  }, 200);
});
