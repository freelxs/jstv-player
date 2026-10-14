// Plain JS frontend; calls Go through window.mygo.call("Player.Method", …).
const $ = (s) => document.querySelector(s);
const call = (m, ...a) => window.mygo.call('Player.' + m, ...a);
const st = { sites: [], site: null, home: null, tid: null, page: 1, stack: [] };

function toast(msg, ms = 3500) {
  const t = $('#toast'); t.textContent = msg; t.hidden = false;
  clearTimeout(toast.h); toast.h = setTimeout(() => (t.hidden = true), ms);
}
const errMsg = (e) => (e && (e.message || e.toString())) || String(e);
const esc = (s) => String(s ?? '').replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) k.startsWith('on') ? e.addEventListener(k.slice(2), v) : e.setAttribute(k, v);
  for (const k of kids) e.append(k);
  return e;
}

// Navigation: push a render function so "返回" restores the previous view.
function show(render) { st.stack.push(render); render(); $('#back').hidden = st.stack.length < 2; }
function back() { if (st.stack.length > 1) { st.stack.pop(); st.stack[st.stack.length - 1](); } $('#back').hidden = st.stack.length < 2; }

async function loadSource() {
  $('#status').textContent = '加载中…'; $('#sites').innerHTML = ''; $('#classes').innerHTML = ''; $('#view').innerHTML = '';
  try {
    const r = await call('LoadSource');
    st.sites = r.sites || [];
    $('#status').textContent = `${st.sites.length} 个站点` + (r.mismatch ? ' · md5 不一致' : '');
    if (r.mismatch) toast('提示：index.js 与发布的 md5 不一致（文件可能已更新）');
    renderSites();
    if (st.sites.length) selectSite(st.sites[0]);
  } catch (e) { $('#status').textContent = '加载失败'; toast(errMsg(e), 8000); }
}

function renderSites() {
  const box = $('#sites'); box.innerHTML = '';
  for (const s of st.sites) {
    const b = el('button', { title: s.name, onclick: () => selectSite(s) }, s.name);
    if (st.site && st.site.api === s.api) b.className = 'active';
    box.append(b);
  }
}

async function selectSite(s) {
  st.site = s; st.stack = []; renderSites();
  $('#classes').innerHTML = ''; $('#view').innerHTML = '<p class="muted">加载中…</p>';
  try {
    st.home = await call('Home', s.api);
    const cls = st.home.class || [];
    for (const c of cls) $('#classes').append(el('button', { onclick: () => openCategory(c.type_id, 1) }, c.type_name));
    if (st.home.list && st.home.list.length) show(() => grid(st.home.list, s));
    else if (cls.length) {
      // Some sites' first class (e.g. 首页 "/") is empty: use the first non-empty one.
      for (const c of cls.slice(0, 4)) {
        try { const r = await call('Category', s.api, String(c.type_id), 1, {}); if ((r.list || []).length) { openCategory(c.type_id, 1); return; } } catch (e) {}
      }
      openCategory(cls[0].type_id, 1);
    }
    else $('#view').innerHTML = '<p class="muted">该站点没有分类，可尝试搜索。</p>';
  } catch (e) { $('#view').innerHTML = `<p>${esc(errMsg(e))}</p>`; }
}

async function openCategory(tid, page) {
  st.tid = tid; st.page = page;
  [...$('#classes').children].forEach((b, i) => b.classList.toggle('active', (st.home.class[i] || {}).type_id === tid));
  $('#view').innerHTML = '<p class="muted">加载中…</p>';
  try {
    const r = await call('Category', st.site.api, String(tid), page, {});
    st.stack = [];
    show(() => {
      grid(r.list || [], st.site);
      const pc = Number(r.pagecount || 0);
      const p = el('div', { class: 'pager' });
      if (page > 1) p.append(el('button', { onclick: () => openCategory(tid, page - 1) }, '上一页'));
      p.append(el('span', { class: 'muted' }, `第 ${page}${pc ? ' / ' + pc : ''} 页`));
      if (!pc || page < pc) p.append(el('button', { onclick: () => openCategory(tid, page + 1) }, '下一页'));
      $('#view').append(p);
    });
  } catch (e) { $('#view').innerHTML = `<p>${esc(errMsg(e))}</p>`; }
}

function cards(list, site) {
  const g = el('div', { class: 'grid' });
  for (const v of list) {
    const img = el('img', { loading: 'lazy', referrerpolicy: 'no-referrer' });
    if (v.vod_pic) img.src = v.vod_pic;
    g.append(el('div', { class: 'card', onclick: () => String(v.vod_id).startsWith('msearch:') ? ($('#onlySite').checked = false, $('#q').value = v.vod_name, search(v.vod_name)) : openDetail(site, v.vod_id) }, img,
      el('div', { class: 't', title: v.vod_name || '' }, v.vod_name || ''), el('div', { class: 'muted small' }, v.vod_remarks || '')));
  }
  return g;
}
function grid(list, site) {
  const view = $('#view'); view.innerHTML = '';
  view.append(list.length ? cards(list, site) : el('p', { class: 'muted' }, '没有内容'));
}

async function search(wd) {
  if (!wd) return;
  $('#view').innerHTML = '<p class="muted">搜索中…</p>';
  if ($('#onlySite').checked && st.site) {
    try {
      const r = await call('Search', st.site.api, wd, 1);
      show(() => grid(r.list || [], st.site));
    } catch (e) { $('#view').innerHTML = `<p>${esc(errMsg(e))}</p>`; }
    return;
  }
  const results = [];
  const render = () => {
    const view = $('#view'); view.innerHTML = '';
    if (!results.length) view.innerHTML = '<p class="muted">搜索中…</p>';
    for (const r of results) {
      view.append(el('div', { class: 'siteh' }, r.site.name + (r.error ? '（出错）' : ` · ${r.list.length}`)));
      if (r.error) view.append(el('div', { class: 'muted small' }, r.error));
      else view.append(cards(r.list, r.site));
    }
  };
  st.stack = []; show(render);
  const ch = window.mygo.channel((r) => { r.list = r.list || []; results.push(r); if (st.stack[st.stack.length - 1] === render) render(); });
  try { await call('SearchAll', wd, ch); } catch (e) { toast(errMsg(e)); }
  if (!results.length && st.stack[st.stack.length - 1] === render) $('#view').innerHTML = '<p class="muted">没有搜索结果</p>';
}

async function openDetail(site, id) {
  $('#view').innerHTML = '<p class="muted">加载详情…</p>';
  let d;
  try { d = await call('Detail', site.api, String(id)); } catch (e) { $('#view').innerHTML = `<p>${esc(errMsg(e))}</p>`; return; }
  show(() => {
    const v = d.vod, view = $('#view'); view.innerHTML = '';
    const img = el('img', { referrerpolicy: 'no-referrer' }); if (v.vod_pic) img.src = v.vod_pic;
    const info = el('div', {});
    info.innerHTML = `<h2>${esc(v.vod_name)}</h2>
      <div class="muted">${esc([v.vod_year, v.vod_area, v.type_name, v.vod_remarks].filter(Boolean).join(' · '))}</div>
      <p class="small">${esc(v.vod_actor ? '主演：' + v.vod_actor : '')}</p>
      <p>${esc(String(v.vod_content || '').replace(/<[^>]+>/g, ''))}</p>`;
    view.append(el('div', { class: 'detail' }, img, info));
    if (!d.lines || !d.lines.length) view.append(el('p', { class: 'muted' }, '没有播放列表'));
    for (const line of d.lines || []) {
      view.append(el('h4', {}, line.flag));
      const eps = el('div', { class: 'eps' });
      for (const ep of line.episodes) eps.append(el('button', { onclick: () => play(site, line.flag, ep, v.vod_name) }, ep.name));
      view.append(eps);
    }
  });
}

async function play(site, flag, ep, title) {
  toast('解析播放地址…');
  try {
    const streams = await call('Play', site.api, flag, ep.id);
    const name = `${title} - ${ep.name}`;
    if (streams.length === 1) { toast('打开播放器…'); return void (await call('Open', streams[0], name)); }
    // Several qualities: let the user pick.
    const box = el('div', { class: 'eps' });
    for (const s of streams) box.append(el('button', { onclick: () => call('Open', s, name).catch((e) => toast(errMsg(e))) }, s.name || '播放'));
    const view = $('#view'); view.prepend(box); view.prepend(el('h4', {}, '选择清晰度：' + ep.name));
    view.scrollTop = 0;
  } catch (e) { toast(errMsg(e), 6000); }
}

async function openSettings() {
  const s = await call('GetSettings');
  $('#sURL').value = s.sourceUrl || ''; $('#sNode').value = s.nodePath || '';
  $('#sPlayer').value = s.player || 'builtin'; $('#sStrict').checked = !!s.strictMd5;
  $('#sBridge').value = s.bridgeTemplate || '';
  $('#logs').textContent = (await call('Logs')).slice(-200).join('\n');
  $('#settings').showModal();
}

$('#settings').addEventListener('close', async () => {
  if ($('#settings').returnValue !== 'save') return;
  try {
    await call('SaveSettings', { sourceUrl: $('#sURL').value, nodePath: $('#sNode').value, player: $('#sPlayer').value, strictMd5: $('#sStrict').checked, bridgeTemplate: $('#sBridge').value });
    loadSource();
  } catch (e) { toast(errMsg(e)); }
});
$('#btnSettings').onclick = openSettings;
$('#btnReload').onclick = loadSource;
$('#back').onclick = back;
$('#q').addEventListener('keydown', (e) => { if (e.key === 'Enter') search(e.target.value.trim()); });

(async () => {
  const s = await call('GetSettings');
  if (s.sourceUrl) loadSource(); else openSettings();
})();
