// Plain JS frontend; calls Go through window.mygo.call("Player.Method", …).
const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];
const call = (m, ...a) => window.mygo.call('Player.' + m, ...a);
const st = { settings: null, sites: [], site: null, home: null, tid: null, page: 1, filters: {}, stack: [], loadSeq: 0 };
const browseRequests = BrowseRequestToken();

const errMsg = (e) => (e && (e.message || e.toString())) || String(e);

function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (k === 'class') e.className = v;
    else if (k === 'style') e.style.cssText = v;
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const k of kids.flat()) if (k != null && k !== false) e.append(k);
  return e;
}
const icon = (n) => el('i', { class: 'i i-' + n });

// ---- toast ----
function toast(msg, { ms = 3500, err = false, busy = false } = {}) {
  const t = $('#toast');
  t.replaceChildren(...(busy ? [el('span', { class: 'spin' })] : []), el('span', {}, msg));
  t.className = 'toast' + (err ? ' err' : ''); t.hidden = false;
  clearTimeout(toast.h); if (ms) toast.h = setTimeout(() => (t.hidden = true), ms);
}
const hideToast = () => { clearTimeout(toast.h); $('#toast').hidden = true; };

// ---- view states ----
function setView(...kids) { const v = $('#view'); v.replaceChildren(...kids.filter((k) => k != null && k !== false)); v.classList.remove('fade'); void v.offsetWidth; v.classList.add('fade'); }
function stateBox({ ico = 'film', title, text, error = false, actions = [] }) {
  return el('div', { class: 'state' + (error ? ' error' : '') },
    el('div', { class: 'ico' }, icon(ico)), el('h4', {}, title), text ? el('p', {}, text) : null,
    actions.length ? el('div', { class: 'btns' }, actions) : null);
}
const errorState = (e, retry) => stateBox({ ico: 'alert', error: true, title: '出错了', text: errMsg(e), actions: retry ? [el('button', { class: 'btn', onclick: retry }, icon('reload'), '重试')] : [] });
function skeletonGrid(n = 18) {
  const g = el('div', { class: 'grid' });
  for (let i = 0; i < n; i++) g.append(el('div', { class: 'card' }, el('div', { class: 'poster skel' }), el('div', { class: 'skel skel-line' }), el('div', { class: 'skel skel-line short' })));
  return g;
}

// Navigation: push a render function so "返回" restores the previous view.
function show(render) { st.stack.push(render); render(); $('#back').hidden = st.stack.length < 2; }
function back() { browseRequests.invalidate(); if (st.stack.length > 1) { st.stack.pop(); st.stack[st.stack.length - 1](); } $('#back').hidden = st.stack.length < 2; }

// ---- subscriptions ----
const activeSub = () => (st.settings?.subscriptions || []).find((s) => s.id === st.settings.active);
function setDot(state) { $('.sub-dot').className = 'sub-dot ' + (state || ''); }
function renderSwitcher() {
  const a = activeSub();
  $('#subName').textContent = a ? a.name : '未添加订阅';
  $('#subBtn').title = a ? a.url : '';
}
function renderSubMenu() {
  const m = $('#subMenu'); m.replaceChildren();
  const subs = st.settings?.subscriptions || [];
  if (!subs.length) m.append(el('div', { class: 'm-empty' }, '还没有订阅'));
  for (const s of subs) {
    m.append(el('button', { role: 'menuitemradio', 'aria-checked': String(s.id === st.settings.active), onclick: () => { closeMenu(); switchSub(s.id); } },
      el('span', { class: 'm-main' }, el('span', { class: 'm-name' }, s.name), el('span', { class: 'm-url' }, s.url)),
      s.id === st.settings.active ? icon('check') : null));
  }
  m.append(el('hr'), el('button', { onclick: () => { closeMenu(); openSettings('subs'); } }, icon('plus'), el('span', { class: 'm-main' }, '添加 / 管理订阅…')));
}
function openMenu() { renderSubMenu(); $('#subMenu').hidden = false; $('#subBtn').setAttribute('aria-expanded', 'true'); }
function closeMenu() { $('#subMenu').hidden = true; $('#subBtn').setAttribute('aria-expanded', 'false'); }
$('#subBtn').onclick = (e) => { e.stopPropagation(); $('#subMenu').hidden ? openMenu() : closeMenu(); };
document.addEventListener('click', (e) => { if (!e.target.closest('.switcher')) closeMenu(); });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeMenu(); });

async function switchSub(id) {
  if (id === st.settings.active && st.sites.length) return;
  await loadSource(() => call('SwitchSubscription', id));
}

// ---- loading a subscription ----
function resetBrowse() {
  st.sites = []; st.site = null; st.home = null; st.stack = [];
  st.tid = null; st.page = 1; st.filters = {};
  $('#sites').replaceChildren(); $('#classes').replaceChildren(); $('#filters').replaceChildren(); $('#filters').hidden = true; $('#back').hidden = true;
}
async function loadSource(fn = () => call('LoadSource')) {
  const seq = ++st.loadSeq;
  const request = browseRequests.begin();
  resetBrowse();
  try {
    const initialSettings = await call('GetSettings');
    if (!browseRequests.current(request) || seq !== st.loadSeq) return;
    st.settings = initialSettings;
    renderSwitcher();
    if (!activeSub()) { setDot(''); $('#status').textContent = ''; setView(noSubState()); return; }
    setDot('busy'); $('#status').textContent = '加载中';
    for (let i = 0; i < 5; i++) $('#sites').append(el('div', { class: 'site-skel skel' }));
    setView(skeletonGrid());
    const r = await fn();
    if (!browseRequests.current(request) || seq !== st.loadSeq) return; // a newer navigation or switch won
    if (r.trustRequired) {
      st.settings = await call('GetSettings');
      renderSwitcher();
      $('#trustTitle').textContent = r.trustSHA256 ? '确认插件版本' : '插件更新需要确认';
      $('#trustMessage').textContent = `来源：${r.trustURL || '（地址不可解析）'}\nSHA-256：${r.trustSHA256}${r.mismatch ? `\n警告：发布者 MD5 不一致（本地 ${r.md5} / 发布 ${r.expectedMd5}）` : ''}`;
      $('#sourceTrust').dataset.pending = '1';
      if (!$('#sourceTrust').open) $('#sourceTrust').showModal();
      setDot(''); $('#status').textContent = '等待确认';
      setView(stateBox({ title: '插件尚未启动', text: '首次使用或检测到内容变化。请确认 SHA-256 后再运行。' }));
      return;
    }
    const reloadedSettings = await call('GetSettings');
    if (!browseRequests.current(request) || seq !== st.loadSeq) return;
    st.settings = reloadedSettings;
    renderSwitcher();
    st.sites = r.sites || [];
    setDot('ok'); $('#status').textContent = `${st.sites.length}`;
    if (r.mismatch) toast('提示：index.js 与发布的 md5 不一致（文件可能已更新）');
    renderSites();
    if (st.sites.length) selectSite(st.sites[0]);
    else setView(stateBox({ title: '这个订阅没有站点', text: '插件的 /config 没有返回任何站点。' }));
  } catch (e) {
    if (!browseRequests.current(request) || seq !== st.loadSeq) return;
    $('#sites').replaceChildren(); setDot('err'); $('#status').textContent = '';
    setView(stateBox({ ico: 'alert', error: true, title: `「${activeSub()?.name || ''}」加载失败`, text: errMsg(e),
      actions: [el('button', { class: 'btn', onclick: () => loadSource() }, icon('reload'), '重试'), el('button', { class: 'btn', onclick: () => openSettings('subs') }, '管理订阅'), el('button', { class: 'btn', onclick: () => openSettings('logs') }, '查看日志')] }));
  }
}
$('#trustReject').onclick = () => {
  $('#sourceTrust').close(); delete $('#sourceTrust').dataset.pending;
  toast('未批准此版本；新插件没有运行', { err: true });
};
$('#trustApprove').onclick = async () => {
  const b = $('#trustApprove'); b.disabled = true;
  try {
    await call('ApproveSource');
    $('#sourceTrust').close(); delete $('#sourceTrust').dataset.pending;
    await loadSource();
  } catch (e) { toast(errMsg(e), { err: true }); }
  finally { b.disabled = false; }
};
const noSubState = () => stateBox({ ico: 'plug', title: '添加你的第一个订阅', text: '订阅是一个插件地址（index.js 或 index.js.md5）。可以添加多个并随时切换。',
  actions: [el('button', { class: 'primary', onclick: () => openSettings('subs') }, icon('plus'), '添加订阅')] });

const hue = (s) => { let h = 0; for (const c of String(s)) h = (h * 31 + c.codePointAt(0)) % 360; return h; };
function renderSites() {
  const box = $('#sites'); box.replaceChildren();
  for (const s of st.sites) {
    const name = s.name || s.key;
    box.append(el('button', { class: 'site' + (st.site && st.site.api === s.api ? ' active' : ''), title: name, onclick: () => selectSite(s) },
      el('span', { class: 'av', style: `--h:${hue(name)}` }, [...name.replace(/[^\p{L}\p{N}]/gu, '')][0]?.toUpperCase() || '·'),
      el('span', { class: 'nm' }, name)));
  }
}

async function selectSite(s) {
  const request = browseRequests.begin();
  st.site = s; st.stack = []; st.tid = null; st.page = 1; st.filters = {}; $('#back').hidden = true; renderSites();
  $('#classes').replaceChildren(); $('#filters').replaceChildren(); $('#filters').hidden = true; setView(skeletonGrid());
  const seq = st.loadSeq;
  try {
    const home = await call('Home', s.api);
    if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
    st.home = home;
    const cls = home.class || [];
    for (const c of cls) $('#classes').append(el('button', { class: 'chip', onclick: () => openCategory(c.type_id, 1, {}) }, c.type_name));
    if (st.home.list && st.home.list.length) show(() => setView(youtubeLoginHint(s), cards(st.home.list, s)));
    else if (cls.length) {
      // Some first classes are empty: use the first non-empty one.
      for (const c of cls.slice(0, 4)) {
        if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
        try {
          const r = await call('Category', s.api, String(c.type_id), 1, {});
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
          if ((r.list || []).length) { openCategory(c.type_id, 1); return; }
        } catch (e) {
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
        }
      }
      openCategory(cls[0].type_id, 1);
    } else setView(youtubeLoginHint(s), stateBox({ ico: 'search', title: '这个站点没有分类', text: '可以在上方直接搜索。' }));
  } catch (e) { if (browseRequests.current(request) && seq === st.loadSeq && st.site === s) setView(errorState(e, () => selectSite(s))); }
}

function filtersForCategory(home, tid) {
  const raw = home && home.filters && typeof home.filters === 'object' && !Array.isArray(home.filters) ? home.filters[String(tid)] : null;
  if (!Array.isArray(raw) || raw.length > 20) return [];
  const parsed = [];
  for (const f of raw) {
    if (!f || typeof f !== 'object' || Array.isArray(f) || typeof f.key !== 'string' || !f.key.trim() || f.key.length > 80 ||
        typeof f.label !== 'string' || !f.label.trim() || f.label.length > 80 || !Array.isArray(f.options) || !f.options.length || f.options.length > 50) return [];
    const options = [];
    for (const o of f.options) {
      if (!o || typeof o !== 'object' || Array.isArray(o) || typeof o.value !== 'string' || o.value.length > 120 ||
          typeof o.label !== 'string' || !o.label.trim() || o.label.length > 80) return [];
      options.push({ value: o.value, label: o.label });
    }
    if (new Set(options.map((o) => o.value)).size !== options.length || parsed.some((x) => x.key === f.key)) return [];
    parsed.push({ key: f.key, label: f.label, options });
  }
  return parsed;
}
function renderFilters(tid) {
  const box = $('#filters'); box.replaceChildren();
  const defs = filtersForCategory(st.home, tid);
  if (!defs.length) { box.hidden = true; return; }
  box.hidden = false;
  for (const f of defs) {
    const select = el('select', { 'aria-label': f.label, onchange: (e) => {
      const next = { ...st.filters };
      if (e.target.value) next[f.key] = e.target.value; else delete next[f.key];
      openCategory(tid, 1, next);
    } });
    select.append(el('option', { value: '' }, `全部${f.label}`));
    for (const o of f.options) select.append(el('option', { value: o.value }, o.label));
    select.value = st.filters[f.key] || '';
    box.append(el('label', { class: 'filter-field' }, el('span', {}, f.label), select));
  }
}
async function openCategory(tid, page, selected = null) {
  const request = browseRequests.begin();
  if (String(st.tid) !== String(tid)) st.filters = {};
  else if (selected) st.filters = { ...selected };
  st.tid = tid; st.page = page;
  renderFilters(tid);
  [...$('#classes').children].forEach((b, i) => b.classList.toggle('active', String((st.home.class[i] || {}).type_id) === String(tid)));
  setView(skeletonGrid());
  const site = st.site;
  try {
    const r = await call('Category', site.api, String(tid), page, { ...st.filters });
    if (!browseRequests.current(request) || st.site !== site) return;
    st.stack = [];
    show(() => {
      setView(youtubeLoginHint(site), (r.list || []).length ? cards(r.list, site) : stateBox({ title: '这里还没有内容', text: '换个分类或翻页试试。' }));
      const pc = Number(r.pagecount || 0);
      if (!(r.list || []).length && page === 1) return;
      $('#view').append(el('div', { class: 'pager' },
        el('button', { class: 'btn', disabled: page <= 1, onclick: () => openCategory(tid, page - 1) }, '上一页'),
        el('span', { class: 'pg' }, `第 ${page}${pc ? ' / ' + pc : ''} 页`),
        el('button', { class: 'btn', disabled: !!pc && page >= pc, onclick: () => openCategory(tid, page + 1) }, '下一页')));
    });
  } catch (e) { if (browseRequests.current(request) && st.site === site) setView(errorState(e, () => openCategory(tid, page))); }
}

function cards(list, site) {
  const g = el('div', { class: 'grid' });
  for (const v of list) {
    const poster = el('div', { class: 'poster' + (v.vod_pic ? '' : ' noimg') });
    if (v.vod_pic) {
      const img = el('img', { loading: 'lazy', referrerpolicy: 'no-referrer', alt: '' });
      img.onload = () => img.classList.add('loaded');
      img.onerror = () => { img.remove(); poster.classList.add('noimg'); };
      img.src = v.vod_pic; poster.append(img);
    }
    if (v.vod_remarks) poster.append(el('span', { class: 'badge' }, v.vod_remarks));
    poster.append(el('span', { class: 'play' }, icon('play')));
    const open = () => String(v.vod_id).startsWith('msearch:') ? ($('#onlySite').checked = false, $('#q').value = v.vod_name, search(v.vod_name)) : openDetail(site, v.vod_id);
    g.append(el('button', { class: 'card', title: v.vod_name || '', onclick: open }, poster,
      el('div', { class: 't' }, v.vod_name || ''), el('div', { class: 's' }, [v.vod_year, v.type_name].filter(Boolean).join(' · ') || '\u00a0')));
  }
  return g;
}
async function search(wd) {
  if (!wd) return;
  if (!st.sites.length) return toast('请先加载一个订阅');
  const request = browseRequests.begin();
  setView(skeletonGrid(12));
  if ($('#onlySite').checked && st.site) {
    try {
      const r = await call('Search', st.site.api, wd, 1);
      if (!browseRequests.current(request)) return;
      st.stack = [];
      show(() => setView(el('div', { class: 'section-h' }, `“${wd}”`, el('span', { class: 'n' }, st.site.name)),
        (r.list || []).length ? cards(r.list, st.site) : stateBox({ ico: 'search', title: '没有找到结果', text: '可以关闭“仅当前站点”搜索全部站点。' })));
    } catch (e) { if (browseRequests.current(request)) setView(errorState(e, () => search(wd))); }
    return;
  }
  const results = []; let done = false;
  const render = () => {
    const kids = [];
    for (const r of results) {
      kids.push(el('div', { class: 'section-h' }, r.site.name, r.error ? el('span', { class: 'e' }, '出错') : el('span', { class: 'n' }, `${r.list.length} 个结果`)));
      kids.push(r.error ? el('p', { class: 'hint' }, r.error) : cards(r.list, r.site));
    }
    if (!done) kids.push(results.length ? el('div', { class: 'section-h' }, el('span', { class: 'n' }, '其余站点搜索中…')) : skeletonGrid(12));
    else if (!results.length) kids.push(stateBox({ ico: 'search', title: '没有找到结果', text: `所有站点都没有“${wd}”的结果。` }));
    const v = $('#view'), top = v.scrollTop; v.replaceChildren(...kids); v.scrollTop = top;
  };
  st.stack = []; show(render);
  const isTop = () => st.stack[st.stack.length - 1] === render;
  const ch = window.mygo.channel((r) => { if (!browseRequests.current(request)) return; r.list = r.list || []; results.push(r); if (isTop()) render(); });
  try { await call('SearchAll', wd, ch); } catch (e) { if (browseRequests.current(request)) toast(errMsg(e), { err: true }); }
  if (!browseRequests.current(request)) return;
  done = true; if (isTop()) render();
}

function youtubeLoginHint(site) {
  const help = youtubeLoginHelp(site, st.sites);
  if (!help) return null;
  return el('aside', { class: 'auth-hint' },
    el('strong', {}, 'YouTube 登录由当前插件管理'),
    el('span', {}, help.message),
    help.configSite ? el('button', { class: 'btn', onclick: () => selectSite(help.configSite) }, '打开 ' + (help.configSite.name || help.configSite.key)) : null);
}

async function openDetail(site, id) {
  const request = browseRequests.begin();
  setView(el('div', { class: 'hero' }, el('div', { class: 'cover skel' }), el('div', { class: 'info', style: 'flex:1' },
    el('div', { class: 'skel skel-line', style: 'height:26px;width:40%' }), el('div', { class: 'skel skel-line short', style: 'width:30%' }),
    el('div', { class: 'skel skel-line' }), el('div', { class: 'skel skel-line' }), el('div', { class: 'skel skel-line short' }))));
  let d;
  try { d = await call('Detail', site.api, String(id)); if (!browseRequests.current(request)) return; } catch (e) { if (browseRequests.current(request)) setView(errorState(e, () => openDetail(site, id))); return; }
  let lineIdx = 0;
  const vodId = String(id);
  let hist = null;
  try { hist = await call('GetHistory', site.api, vodId); } catch (e) {}
  if (!browseRequests.current(request)) return;
  if (hist) { const i = (d.lines || []).findIndex((l) => l.flag === hist.flag); if (i >= 0) lineIdx = i; }
  show(() => {
    const v = d.vod;
    const cover = el('div', { class: 'cover' });
    const bg = el('div', { class: 'bg' });
    if (v.vod_pic) { cover.append(el('img', { src: v.vod_pic, referrerpolicy: 'no-referrer', alt: '' })); bg.style.backgroundImage = `url("${String(v.vod_pic).replace(/"/g, '%22')}")`; }
    const meta = [v.vod_year, v.vod_area, v.type_name, v.vod_remarks].filter(Boolean).map((x) => el('span', {}, String(x)));
    const desc = String(v.vod_content || '').replace(/<[^>]+>/g, '').trim();
    const hero = el('div', { class: 'hero' }, bg, cover, el('div', { class: 'info' },
      el('h2', {}, v.vod_name || ''), meta.length ? el('div', { class: 'meta' }, meta) : null,
      v.vod_actor ? el('p', { class: 'cast' }, '主演：' + v.vod_actor) : null,
      v.vod_director ? el('p', { class: 'cast' }, '导演：' + v.vod_director) : null,
      desc ? el('p', { class: 'desc', title: desc }, desc) : null,
      resumeButton()));
    function resumeButton() {
      const l = (d.lines || []).find((x) => hist && x.flag === hist.flag);
      if (!l || !l.episodes[hist.index]) return null;
      const next = hist.index;
      return el('div', { class: 'resume' }, el('button', { class: 'primary', onclick: (e) => play(site, l, next, vodId, v.vod_name, null) }, icon('play'), `继续播放 ${l.episodes[next].name}`));
    }
    const lines = d.lines || [];
    const body = el('div', { id: 'lines' });
    const drawLine = () => {
      const tabs = lines.length > 1 ? el('div', { class: 'line-tabs' }, lines.map((l, i) => el('button', { class: i === lineIdx ? 'active' : '', onclick: () => { lineIdx = i; drawLine(); } }, l.flag))) : el('div', { class: 'section-h' }, lines[0].flag);
      const line = lines[lineIdx];
      const eps = el('div', { class: 'eps' }, line.episodes.map((ep, i) => el('button', { class: 'ep' + (hist && hist.flag === line.flag && hist.index === i ? ' last' : ''), title: ep.name, onclick: (e) => play(site, line, i, vodId, v.vod_name, e.currentTarget) }, ep.name)));
      body.replaceChildren(tabs, eps);
    };
    if (lines.length) drawLine(); else body.append(stateBox({ title: '没有播放列表', text: '插件没有返回任何剧集。' }));
    setView(hero, body);
  });
}

async function play(site, line, idx, vodId, title, btn) {
  const flag = line.flag, ep = line.episodes[idx];
  $$('.ep.busy').forEach((b) => b.classList.remove('busy'));
  btn?.classList.add('busy');
  toast('解析播放地址…', { ms: 0, busy: true });
  try {
    // With 连播 the Go side opens the player with this and the following
    // episodes and returns nothing; otherwise it returns this episode's streams.
    const streams = (await call('PlayEpisodes', site.api, vodId, title, flag, line.episodes, idx)) || [];
    const name = `${title} - ${ep.name}`;
    $('.quality')?.remove();
    $$('.ep.last').forEach((b) => b.classList.remove('last'));
    btn?.classList.add('last');
    if (!streams.length) {
      toast(idx < line.episodes.length - 1 ? `正在连播：从 ${ep.name} 起共 ${line.episodes.length - idx} 集` : '正在打开播放器…');
    } else if (streams.length === 1) {
      toast('正在打开播放器…', { busy: true });
      await call('Open', streams[0], name);
      hideToast();
    } else {
      hideToast();
      // Several qualities: let the user pick.
      const box = el('div', { class: 'quality' }, el('h4', {}, `选择清晰度 · ${ep.name}`),
        el('div', { class: 'eps' }, streams.map((s) => el('button', { class: 'ep', onclick: () => call('Open', s, name).catch((e) => toast(errMsg(e), { err: true, ms: 6000 })) }, s.name || '播放'))));
      $('#lines').prepend(box); box.scrollIntoView({ block: 'nearest' });
    }
    btn?.classList.add('played');
  } catch (e) { toast(errMsg(e), { err: true, ms: 6000 }); }
  btn?.classList.remove('busy');
}

// ---- settings sheet ----
function setTab(name) {
  $$('.tab').forEach((t) => t.classList.toggle('active', t.dataset.tab === name));
  $$('.pane').forEach((p) => (p.hidden = p.dataset.pane !== name));
  if (name === 'logs') call('Logs').then((l) => { const p = $('#logs'); p.textContent = l.slice(-300).join('\n') || '（暂无日志）'; p.scrollTop = p.scrollHeight; });
}
$$('.tab').forEach((t) => (t.onclick = () => setTab(t.dataset.tab)));

function renderSubList() {
  const ul = $('#subList'); ul.replaceChildren();
  for (const s of st.settings.subscriptions || []) {
    const on = s.id === st.settings.active;
    ul.append(el('li', { class: on ? 'on' : '' },
      el('div', { class: 'main' }, el('div', { class: 'nm' }, s.name, on ? el('span', { class: 'tag' }, '当前') : null), el('div', { class: 'url' }, s.url)),
      on ? null : el('button', { class: 'btn', onclick: () => { $('#settings').close(); switchSub(s.id); } }, '切换'),
      el('button', { class: 'icon-btn', title: '编辑', onclick: () => editSub(s) }, icon('edit')),
      el('button', { class: 'icon-btn danger', title: '删除', onclick: (e) => removeSub(s, e.currentTarget) }, icon('trash'))));
  }
}
function editSub(s) {
  $('#subFormId').value = s ? s.id : ''; $('#subFormName').value = s ? s.name : ''; $('#subFormURL').value = s ? s.url : '';
  $('#subFormBtn').textContent = s ? '保存' : '添加'; $('#subFormCancel').hidden = !s;
  if (s) $('#subFormURL').focus();
}
async function removeSub(s, btn) {
  // Two-step confirm inside the row (native confirm() is not available in every webview).
  if (!btn.classList.contains('confirm')) {
    btn.classList.add('confirm'); btn.replaceChildren('确认删除'); btn.className = 'btn confirm';
    setTimeout(() => { if (btn.isConnected) renderSubList(); }, 3000);
    return;
  }
  const wasActive = s.id === st.settings.active;
  try {
    st.settings = await call('RemoveSubscription', s.id); renderSubList(); renderSwitcher();
    if (wasActive) loadSource();
  } catch (e) { toast(errMsg(e), { err: true }); }
}
$('#subFormCancel').onclick = () => editSub(null);
$('#subForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const id = $('#subFormId').value, name = $('#subFormName').value, url = $('#subFormURL').value;
  const first = !(st.settings.subscriptions || []).length;
  try {
    st.settings = id ? await call('UpdateSubscription', id, name, url) : await call('AddSubscription', name, url);
    editSub(null); renderSubList(); renderSwitcher();
    if (first || id === st.settings.active) { $('#settings').close(); loadSource(); }
    else toast(id ? '已保存' : '已添加，可在左上角切换');
  } catch (err) { toast(errMsg(err), { err: true }); }
});

async function openSettings(tab = 'subs') {
  st.settings = await call('GetSettings');
  const s = st.settings;
  // Only offer the OS-specific players on their OS (keep a saved one visible).
  const os = /Windows/i.test(navigator.userAgent) ? 'win' : /Mac/i.test(navigator.userAgent) ? 'mac' : 'linux';
  for (const o of $$('#sPlayer option[data-os]')) o.hidden = o.dataset.os !== os && o.value !== s.player;
  $('#sNode').value = s.nodePath || ''; $('#sPlayer').value = s.player || 'builtin';
  $('#sStrict').checked = !!s.strictMd5; $('#sContinuous').checked = !s.disableContinuous; $('#sBridge').value = s.bridgeTemplate || '';
  $('#sAllowLan').checked = !!s.allowLan;
  $('#sAllowLanMedia').checked = !!s.allowLanMedia;
  $('#sInheritNodeEnv').checked = !!s.inheritNodeEnv;
  editSub(null); renderSubList(); setTab(tab);
  if (!$('#settings').open) $('#settings').showModal();
}
$('#prefs').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    const oldAllowLAN = !!st.settings?.allowLan;
    const nextAllowLAN = $('#sAllowLan').checked;
    await call('SaveSettings', { nodePath: $('#sNode').value.trim(), player: $('#sPlayer').value, strictMd5: $('#sStrict').checked, disableContinuous: !$('#sContinuous').checked, bridgeTemplate: $('#sBridge').value.trim(), allowLan: $('#sAllowLan').checked, allowLanMedia: $('#sAllowLanMedia').checked, inheritNodeEnv: $('#sInheritNodeEnv').checked });
    st.settings = await call('GetSettings');
    toast('设置已保存');
    if (oldAllowLAN !== nextAllowLAN && st.settings.active) {
      $('#settings').close();
      await loadSource();
    }
  } catch (err) { toast(errMsg(err), { err: true }); }
});
$('#sClose').onclick = () => $('#settings').close();
$('#probeNodePermissions').onclick = async () => {
  const out = $('#nodePermissionResult'); out.textContent = '检测中…';
  try { out.textContent = await call('ProbeNodePermissions'); }
  catch (e) { out.textContent = errMsg(e); }
};

$('#btnSettings').onclick = () => openSettings('subs');
$('#btnReload').onclick = () => loadSource();
$('#back').onclick = back;
$('#q').addEventListener('keydown', (e) => { if (e.key === 'Enter') search(e.target.value.trim()); });
$('#onlySite').addEventListener('change', (e) => ($('#q').placeholder = e.target.checked ? '搜索当前站点' : '搜索全部站点'));

(async () => {
  st.settings = await call('GetSettings'); renderSwitcher();
  if (activeSub()) loadSource(); else { setView(noSubState()); openSettings('subs'); }
})();
