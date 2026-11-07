// Plain JS frontend; calls Go through window.mygo.call("Player.Method", …).
const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];
const call = (m, ...a) => window.mygo.call('Player.' + m, ...a);
const st = { settings: null, sites: [], site: null, home: null, tid: null, page: 1, filters: {}, stack: [], loadSeq: 0 };
const browseRequests = BrowseRequestToken();
let activeSearch = null;
let trustReload = null;
function closeActiveSearch() {
  const ch = activeSearch;
  activeSearch = null;
  if (ch && typeof ch.close === 'function') {
    try { ch.close(); } catch (e) { /* already closed */ }
  }
}
const beginBrowse = browseRequests.begin.bind(browseRequests);
const invalidateBrowse = browseRequests.invalidate.bind(browseRequests);
browseRequests.begin = () => { closeActiveSearch(); return beginBrowse(); };
browseRequests.invalidate = () => { closeActiveSearch(); invalidateBrowse(); };

const errMsg = (e) => (e && (e.message || e.toString())) || String(e);
// Web deployment loads posters through the authenticated image proxy. Desktop
// keeps the original address because __JSTV_WEB__ is unset.
function posterURL(url) {
  if (!window.__JSTV_WEB__ || !url) return url;
  return '/poster?u=' + encodeURIComponent(String(url));
}

// A configuration page lives on the plugin's own /website/ route. Other http
// addresses stay ordinary catalog ids.
function websitePageURL(raw) {
  try {
    const u = new URL(String(raw));
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return '';
    const path = u.pathname.replace(/\/+$/, '') || '/';
    if (path === '/website' || path === '/website/index.html') return u.href;
  } catch (e) {
    return '';
  }
  return '';
}

function isConfigImage(url) {
  try {
    const u = new URL(String(url), 'http://127.0.0.1');
    return /\/website\/[^/]+\.(png|jpe?g|gif|webp)$/i.test(u.pathname);
  } catch (e) {
    return false;
  }
}

function isConfigSite(s) {
  return String(s?.name || s?.key || '').trim().startsWith('配置');
}

function configPageFromItem(v) {
  const direct = websitePageURL(v && v.vod_id);
  if (direct) return direct;
  if (v && isConfigImage(v.vod_pic) && /配置/.test(String(v.vod_name || ''))) {
    try {
      const u = new URL(String(v.vod_pic));
      u.pathname = '/website/index.html';
      u.search = '';
      u.hash = '';
      return u.href;
    } catch (e) {
      return '';
    }
  }
  return '';
}

function openWebPage(url, title) {
  if (window.__JSTV_WEB__ && websitePageURL(url)) url='/configuration/';
  const name = title || '配置';
  if (window.__JSTV_WEB__) {
    const opened = window.open(url, '_blank', 'noopener');
    if (!opened) toast('请允许弹出窗口以打开配置页面', { err: true });
    return Promise.resolve();
  }
  return call('Open', { url, parse: true, name }, name);
}

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
      trustReload = fn;
      st.settings = await call('GetSettings');
      renderSwitcher();
      $('#trustTitle').textContent = r.trustSHA256 ? '确认插件版本' : '插件更新需要确认';
      $('#trustMessage').textContent = `来源：${r.trustURL || '（地址不可解析）'}\n版本清单 SHA-256：${r.trustSHA256}${r.trustScriptSHA256 ? `\n主脚本：${r.trustScriptSHA256}\n配置脚本：${r.trustConfigSHA256 || "无"}` : ""}${r.mismatch ? `\n警告：发布者 MD5 不一致（本地 ${r.md5} / 发布 ${r.expectedMd5}）` : ''}`;
      $('#sourceTrust').dataset.pending = '1';
      if (!$('#sourceTrust').open) $('#sourceTrust').showModal();
      setDot(''); $('#status').textContent = '等待确认';
      setView(stateBox({ title: '插件尚未启动', text: '首次使用或检测到内容变化。请确认 SHA-256 后再运行。' }));
      return;
    }
    if (r.warnings && r.warnings.length) toast(`部分直播源未加载：${r.warnings.join('；')}`, { err: true, ms: 8000 });
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
  trustReload = null;
  $('#sourceTrust').close(); delete $('#sourceTrust').dataset.pending;
  toast('未批准此版本；新插件没有运行', { err: true });
};
$('#trustApprove').onclick = async () => {
  const b = $('#trustApprove'); b.disabled = true;
  try {
    await call('ApproveSource');
    $('#sourceTrust').close(); delete $('#sourceTrust').dataset.pending;
    const retry = trustReload; trustReload = null;
    await loadSource(retry || undefined);
  } catch (e) { toast(errMsg(e), { err: true }); }
  finally { b.disabled = false; }
};
const noSubState = () => stateBox({ ico: 'plug', title: '添加你的第一个订阅', text: '支持 JS 插件、M3U/TXT 直播和 TVBox lives，可添加多个并随时切换。',
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
  if (isConfigSite(s)) {
    call('OpenConfigPage', s.api).then((page) => {
      if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
      if (window.__JSTV_WEB__ && page) {
        $('#classes').append(el('a', {class:'btn',href:page,target:'_blank',rel:'noopener noreferrer'}, '打开插件配置'));
        const opened = window.open(page, '_blank', 'noopener');
        if (!opened) toast('请点击配置卡片打开，或允许弹出窗口', { err: true });
      }
    }).catch((e) => toast(errMsg(e), { err: true }));
  }
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
          const r = await call('Category', s.api, String(c.type_id), 1, defaultFilters(c.type_id));
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
          if ((r.list || []).length) { openCategory(c.type_id, 1); return; }
        } catch (e) {
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
        }
      }
      openCategory(cls[0].type_id, 1);
    } else if (/liveSource/i.test(s.key || '') || /直播/.test(s.name || '')) {
      setView(stateBox({ico:'search',title:'暂无直播源',text:'请在插件配置页添加直播源，然后刷新站点。也可在订阅管理中直接添加 M3U/TXT。',actions:[el('button',{class:'btn',onclick:()=>call('OpenConfigPage',s.api).then((u)=>{if(window.__JSTV_WEB__)openWebPage(u,'直播配置');}).catch((e)=>toast(errMsg(e),{err:true}))},'配置直播源'),el('button',{class:'btn',onclick:()=>selectSite(s)},'刷新站点')]}));
    } else setView(youtubeLoginHint(s), stateBox({ ico: 'search', title: '这个站点没有分类', text: '可以在上方直接搜索。' }));
  } catch (e) { if (browseRequests.current(request) && seq === st.loadSeq && st.site === s) setView(errorState(e, () => selectSite(s))); }
}

function filterText(v, max) {
  if (typeof v === 'string') return v.length <= max ? v : null;
  if (typeof v === 'number' && Number.isFinite(v)) {
    const s = String(v);
    return s.length <= max ? s : null;
  }
  return null;
}
function filtersForCategory(home, tid) {
  const raw = home && home.filters && typeof home.filters === 'object' && !Array.isArray(home.filters) ? home.filters[String(tid)] : null;
  if (!Array.isArray(raw) || raw.length > 20) return [];
  const parsed = [];
  for (const f of raw) {
    if (!f || typeof f !== 'object' || Array.isArray(f) || typeof f.key !== 'string' || !f.key.trim() || f.key.length > 80) return [];
    const label = typeof f.label === 'string' ? f.label : typeof f.name === 'string' ? f.name : '';
    if (!label.trim() || label.length > 80) return [];
    const rawOptions = Array.isArray(f.options) ? f.options : Array.isArray(f.value) ? f.value : null;
    if (!rawOptions || !rawOptions.length || rawOptions.length > 50) return [];
    const options = [];
    const seen = new Set();
    for (const o of rawOptions) {
      if (!o || typeof o !== 'object' || Array.isArray(o)) return [];
      // Documented {value,label} stays strict. CatVod {n,v} also accepts numbers.
      const value = typeof o.value === 'string' ? (o.value.length <= 120 ? o.value : null) : filterText(o.v, 120);
      const olabel = typeof o.label === 'string' ? o.label : filterText(o.n, 80);
      if (value == null || olabel == null || !String(olabel).trim() || olabel.length > 80) return [];
      // Plugins repeat the blank "全部" choice. Keep the first value so the
      // rest of the filter stays usable.
      if (value !== '' && !seen.has(value)) { seen.add(value); options.push({ value, label: olabel }); }
    }
    if (!options.length || new Set(options.map((o) => o.value)).size !== options.length || parsed.some((x) => x.key === f.key)) return [];
    const init = typeof f.init === 'string' && options.some((o) => o.value === f.init) ? f.init : '';
    parsed.push({ key: f.key, label, options, init });
  }
  return parsed;
}
function defaultFilters(tid) {
  const out = {};
  for (const f of filtersForCategory(st.home, tid)) if (f.init) out[f.key] = f.init;
  return out;
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
  if (String(st.tid) !== String(tid)) st.filters = defaultFilters(tid);
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

function isFolderItem(v) {
  return String(v?.vod_tag || '').trim().toLowerCase() === 'folder' && String(v?.vod_id || '') !== '';
}

// Some sources mark a row as a folder. The next page of items is category(id),
// not detail(id). Calling detail on that id is defined to come back empty.
async function openFolder(site, vod) {
  const request = browseRequests.begin();
  const id = String(vod.vod_id);
  const title = vod.vod_name || '';
  setView(skeletonGrid());
  try {
    const r = await call('Category', site.api, id, 1, {});
    if (!browseRequests.current(request) || st.site !== site) return;
    show(() => {
      const list = r.list || [];
      setView(
        title ? el('div', { class: 'section-h' }, title) : null,
        list.length ? cards(list, site) : stateBox({ title: '这里还没有内容', text: '这个文件夹没有可打开的条目。' }),
      );
    });
  } catch (e) {
    if (browseRequests.current(request) && st.site === site) setView(errorState(e, () => openFolder(site, vod)));
  }
}

function cards(list, site) {
  const g = el('div', { class: 'grid' });
  for (const v of list) {
    const poster = el('div', { class: 'poster' + (v.vod_pic ? '' : ' noimg') + (isConfigImage(v.vod_pic) ? ' code' : '') });
    if (v.vod_pic) {
      const img = el('img', { loading: 'lazy', referrerpolicy: 'no-referrer', alt: '' });
      img.onload = () => img.classList.add('loaded');
      img.onerror = () => { img.remove(); poster.classList.add('noimg'); };
      img.src = posterURL(v.vod_pic); poster.append(img);
    }
    if (v.vod_remarks) poster.append(el('span', { class: 'badge' }, v.vod_remarks));
    poster.append(el('span', { class: 'play' }, icon('play')));
    const open = () => {
      const page = configPageFromItem(v);
      if (page) {
        openWebPage(page, v.vod_name || '配置').catch((e) => toast(errMsg(e), { err: true }));
        return;
      }
      if (isFolderItem(v)) {
        openFolder(site, v);
        return;
      }
      if (String(v.vod_id).startsWith('msearch:')) {
        // These cards are search recommendations. Searching every site must
        // also clear the "current site only" hint, which a bare checked=false
        // does not, because it does not fire the checkbox change listener.
        setSiteOnlySearch(false);
        $('#q').value = v.vod_name;
        search(v.vod_name);
        return;
      }
      openDetail(site, v.vod_id);
    };
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
    else if (results.length && results.every((r) => r.error)) kids.unshift(stateBox({ ico: 'alert', title: '所有站点搜索失败', text: '请检查网络或稍后重试。各站点的诊断信息如下。', error: true }));
    else if (!results.length) kids.push(stateBox({ ico: 'search', title: '没有找到结果', text: `所有站点都没有“${wd}”的结果。` }));
    const v = $('#view'), top = v.scrollTop; v.replaceChildren(...kids); v.scrollTop = top;
  };
  st.stack = []; show(render);
  const isTop = () => st.stack[st.stack.length - 1] === render;
  const ch = window.mygo.channel((r) => { if (!browseRequests.current(request)) return; r.list = r.list || []; results.push(r); if (isTop()) render(); });
  activeSearch = ch;
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
  let favorite = false;
  try { const entries = await call('GetFavorites'); favorite = Array.isArray(entries) && entries.some((x) => x.api === site.api && x.id === vodId); } catch (e) {}
  if (!browseRequests.current(request)) return;
  let hist = null;
  try { hist = await call('GetHistory', site.api, vodId); } catch (e) {}
  if (!browseRequests.current(request)) return;
  if (hist) { const i = (d.lines || []).findIndex((l) => l.flag === hist.flag); if (i >= 0) lineIdx = i; }
  show(() => {
    const v = d.vod;
    const cover = el('div', { class: 'cover' + (isConfigImage(v.vod_pic) ? ' code' : '') });
    const bg = el('div', { class: 'bg' });
    if (v.vod_pic) { const pic = posterURL(v.vod_pic); cover.append(el('img', { src: pic, referrerpolicy: 'no-referrer', alt: '' })); bg.style.backgroundImage = `url("${String(pic).replace(/"/g, '%22')}")`; }
    const meta = [v.vod_year, v.vod_area, v.type_name, v.vod_remarks].filter(Boolean).map((x) => el('span', {}, String(x)));
    const desc = String(v.vod_content || '').replace(/<[^>]+>/g, '').trim();
    const hero = el('div', { class: 'hero' }, bg, cover, el('div', { class: 'info' },
      el('h2', {}, v.vod_name || ''), meta.length ? el('div', { class: 'meta' }, meta) : null,
      v.vod_actor ? el('p', { class: 'cast' }, '主演：' + v.vod_actor) : null,
      v.vod_director ? el('p', { class: 'cast' }, '导演：' + v.vod_director) : null,
      desc ? el('p', { class: 'desc', title: desc }, desc) : null,
      el('button',{class:'btn',onclick:async(e)=>{const button=e.currentTarget;try{favorite=await call('ToggleFavorite',site.api,vodId,String(v.vod_name||vodId),String(v.vod_pic||''));button.textContent=favorite?'取消收藏':'收藏';}catch(error){toast(errMsg(error),{err:true});}}},favorite?'取消收藏':'收藏'),
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
  const page = websitePageURL(ep.id);
  if (page) {
    toast('正在打开配置页面…', { busy: true });
    try {
      await openWebPage(page, `${title} - ${ep.name}`);
      hideToast();
      btn?.classList.add('played');
    } catch (e) { toast(errMsg(e), { err: true, ms: 6000 }); }
    btn?.classList.remove('busy');
    return;
  }
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
  if (name === 'logs') refreshLogs();
}
async function refreshLogs() {
  try {
    const l = await call('Logs'); const p = $('#logs');
    p.textContent = l.slice(-300).join('\n') || '（暂无日志）'; p.scrollTop = p.scrollHeight;
    $('#logPersistenceState').textContent = st.settings?.persistLogs ? '持久化已开启' : '持久化已关闭';
  } catch (e) { toast(errMsg(e), { err: true }); }
}
$('#clearLogs').onclick = async () => {
  try { await call('ClearLogs'); await refreshLogs(); toast('日志已清除'); }
  catch (e) { toast(errMsg(e), { err: true }); }
};
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
  $('#subFormType').value = (s && s.type) || 'auto';
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
    st.settings = id ? await call('UpdateSubscriptionWithType', id, name, url, $('#subFormType').value || 'auto') : await call('AddSubscriptionWithType', name, url, $('#subFormType').value || 'auto');
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
  // WebKit's select popup ignores hidden and display:none on <option>, so
  // drop the node. A saved choice for another OS stays in the list.
  for (const o of $$('#sPlayer option[data-os]')) {
    if (o.dataset.os !== os && o.value !== s.player) o.remove();
  }
  $('#sNode').value = s.nodePath || ''; $('#sPlayer').value = s.player || 'builtin';
  $('#sCustomPlayer').value = s.customPlayer || ''; $('#sCustomArgs').value = s.customArgs || '';
  syncCustomPlayerFields();
  $('#sStrict').checked = !!s.strictMd5; $('#sContinuous').checked = !s.disableContinuous; $('#sBridge').value = s.bridgeTemplate || '';
  $('#sAllowLan').checked = !!s.allowLan;
  $('#sAllowLanMedia').checked = !!s.allowLanMedia;
  $('#sInheritNodeEnv').checked = !!s.inheritNodeEnv;
  $('#sInheritProxyEnv').checked = !!s.inheritProxyEnv;
  $('#sPersistLogs').checked = !!s.persistLogs;
  editSub(null); renderSubList(); setTab(tab);
  if (!$('#settings').open) $('#settings').showModal();
}
function confirmLAN() {
  return new Promise((resolve) => {
    const dialog = el('dialog', {}, el('h3', {}, '确认开放局域网访问'), el('p', {}, '这会把插件管理后台开放给整个局域网，其他设备可能修改配置和执行插件操作。'),
      el('button', {class:'btn',onclick:()=>finish(false)}, '取消'), el('button', {class:'primary',onclick:()=>finish(true)}, '确认开启'));
    const finish = (allowed) => { dialog.close(); dialog.remove(); resolve(allowed); };
    dialog.addEventListener('cancel', (e) => { e.preventDefault(); finish(false); });
    document.body.append(dialog); dialog.showModal();
  });
}
$('#prefs').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    const oldAllowLAN = !!st.settings?.allowLan;
    const oldAllowLANMedia = !!st.settings?.allowLanMedia;
    const nextAllowLAN = $('#sAllowLan').checked;
    if (!oldAllowLAN && nextAllowLAN && !(await confirmLAN())) return;
    await call('SaveSettings', { nodePath: $('#sNode').value.trim(), player: $('#sPlayer').value, customPlayer: $('#sCustomPlayer').value.trim(), customArgs: $('#sCustomArgs').value.trim(), strictMd5: $('#sStrict').checked, disableContinuous: !$('#sContinuous').checked, bridgeTemplate: $('#sBridge').value.trim(), allowLan: $('#sAllowLan').checked, allowLanMedia: $('#sAllowLanMedia').checked, inheritProxyEnv: $('#sInheritProxyEnv').checked, inheritNodeEnv: $('#sInheritNodeEnv').checked, persistLogs: $('#sPersistLogs').checked });
    st.settings = await call('GetSettings');
    toast('设置已保存');
    if ((oldAllowLAN !== nextAllowLAN || oldAllowLANMedia !== !!st.settings.allowLanMedia) && st.settings.active) {
      $('#settings').close();
      await loadSource();
    }
  } catch (err) { toast(errMsg(err), { err: true }); }
});
function syncCustomPlayerFields() {
  $('#customPlayerFields').hidden = $('#sPlayer').value !== 'custom';
}
$('#sPlayer').addEventListener('change', syncCustomPlayerFields);
$('#sBrowsePlayer').onclick = async () => {
  try {
    const path = await call('PickCustomPlayer');
    if (path) $('#sCustomPlayer').value = path;
  } catch (e) { toast(errMsg(e), { err: true }); }
};
$('#sClose').onclick = () => $('#settings').close();
$('#probeNodePermissions').onclick = async () => {
  const out = $('#nodePermissionResult'); out.textContent = '检测中…';
  try { out.textContent = await call('ProbeNodePermissions'); }
  catch (e) { out.textContent = errMsg(e); }
};

$('#btnSettings').onclick = () => openSettings('subs');
$('#btnFavorites').onclick = async () => {
  const request=browseRequests.begin();
  try {
    const entries=await call('GetFavorites');if(!browseRequests.current(request))return;
    st.stack=[];$('#classes').replaceChildren();$('#filters').hidden=true;
    show(()=>{
      if(!Array.isArray(entries)||!entries.length){setView(stateBox({title:'还没有收藏',text:'在节目或直播频道详情中点击“收藏”。'}));return;}
      const groups=new Map();for(const item of entries){if(!groups.has(item.api))groups.set(item.api,[]);groups.get(item.api).push({vod_id:item.id,vod_name:item.name,vod_pic:item.picture});}
      const elements=[];for(const [api,items] of groups){const site=st.sites.find((x)=>x.api===api)||{api,name:'收藏'};elements.push(el('div',{class:'section-h'},site.name),cards(items,site));}setView(...elements);
    });
  } catch(e){if(browseRequests.current(request))setView(errorState(e));}
};
$('#btnForceReload').onclick = () => { $('#settings').close(); loadSource(() => call('ForceRefreshSource')); };
$('#btnReload').onclick = () => loadSource();
$('#btnStartCached').onclick = () => loadSource(() => call('StartCachedSource'));
$('#back').onclick = back;
$('#q').addEventListener('keydown', (e) => { if (e.key === 'Enter') search(e.target.value.trim()); });
function setSiteOnlySearch(on) {
  $('#onlySite').checked = !!on;
  $('#q').placeholder = on ? '搜索当前站点' : '搜索全部站点';
}
$('#onlySite').addEventListener('change', (e) => setSiteOnlySearch(e.target.checked));

(async () => {
  st.settings = await call('GetSettings'); renderSwitcher();
  if (activeSub()) loadSource(() => call('StartCachedSource')); else { setView(noSubState()); openSettings('subs'); }
})();
