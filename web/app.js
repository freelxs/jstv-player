// Plain JS frontend; calls Go through window.mygo.call("Player.Method", …).
const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];
const isChannelArg = (v) => Boolean(v && typeof v === 'object' && typeof v.close === 'function' && 'onmessage' in v);
const call = (m, ...a) => window.mygo.call('Player.' + m, ...a);
const callWith = (signal, m, ...a) => {
  // mygo v0.10.0 channel methods cancel via ch.close(); passing { signal }
  // (cancel: true) wraps w.pageCtx in handleCall and causes w.newChannel's
  // w.pageCtx == page check to mark the channel closed immediately.
  if (a.some(isChannelArg)) {
    const chs = a.filter(isChannelArg);
    const closeAll = () => chs.forEach((ch) => { try { ch.close(); } catch (e) { /* already closed */ } });
    if (signal && signal.aborted) {
      closeAll();
      return Promise.reject(signal.reason || new Error('Aborted'));
    }
    const p = window.mygo.call('Player.' + m, ...a);
    if (signal && typeof signal.addEventListener === 'function') {
      signal.addEventListener('abort', closeAll, { once: true });
      return Promise.resolve(p).finally(() => signal.removeEventListener('abort', closeAll));
    }
    return p;
  }
  return (signal && window.mygo && typeof window.mygo.callWith === 'function')
    ? window.mygo.callWith('Player.' + m, a, { signal })
    : window.mygo.call('Player.' + m, ...a);
};
const st = { settings: null, sites: [], site: null, home: null, tid: null, page: 1, filters: {}, stack: [], loadSeq: 0, libTab: 'all', libSort: 'recent' };
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
function normalizePosterURL(url) {
  const raw = String(url || '').trim();
  if (!raw) return raw;
  try {
    const u = new URL(raw);
    if (/(?:^|\.)baidu\.com$/i.test(u.hostname) && u.pathname.includes('/gimg/')) {
      const idx = raw.indexOf('src=');
      if (idx >= 0) {
        let inner = raw.slice(idx + 4);
        const amp = inner.indexOf('&');
        if (amp >= 0 && !inner.slice(0, amp).includes('?')) inner = inner.slice(0, amp);
        try {
          const decoded = decodeURIComponent(inner);
          if (/^https?:\/\//i.test(decoded)) inner = decoded;
        } catch (e) {}
        if (/^https?:\/\//i.test(inner)) return inner;
      }
    }
  } catch (e) {}
  return raw;
}
function posterURL(url) {
  const clean = normalizePosterURL(url);
  if (!window.__JSTV_WEB__ || !clean) return clean;
  return '/poster?u=' + encodeURIComponent(clean);
}

function attachPosterImage(container, url, { fade = true } = {}) {
  const img = el('img', { loading: 'lazy', referrerpolicy: 'no-referrer', alt: '' });
  const markLoaded = () => {
    if (img.naturalWidth && img.naturalWidth <= 2 && img.naturalHeight && img.naturalHeight <= 2) {
      img.remove();
      container.classList.add('noimg');
      return;
    }
    if (fade) img.classList.add('loaded');
  };
  img.onload = markLoaded;
  img.onerror = () => { img.remove(); container.classList.add('noimg'); };
  img.src = posterURL(url);
  container.append(img);
  if (img.complete && img.naturalWidth) markLoaded();
  return img;
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
function formatClock(sec) {
  const total = Math.floor(Number(sec) || 0);
  if (total <= 0) return '';
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`;
}
function formatShortDate(unixSec) {
  if (!unixSec) return '';
  try {
    return new Date(unixSec * 1000).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
  } catch (e) {
    return '';
  }
}

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
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    closeMenu();
    if (document.activeElement === $('#q')) $('#q').blur();
  }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    const q = $('#q');
    if (q) {
      q.focus();
      q.select();
    }
  }
});

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
    if (r.repoPick) {
      st.settings = await call('GetSettings');
      renderSwitcher();
      showRepoPicker(r.repoLines || []);
      return;
    }
    if (r.trustRequired) {
      trustReload = fn;
      st.settings = await call('GetSettings');
      renderSwitcher();
      const tvbox = r.subscription && (r.subscription.type === 'tvbox' || /\.json(?:$|[?#])/i.test(r.subscription.url || ''));
      $('#trustTitle').textContent = tvbox ? (r.trustScriptSHA256 ? '确认 JSON 配置与远程脚本' : '确认 JSON 配置') : (r.trustSHA256 ? '确认插件版本' : '插件更新需要确认');
      $('#trustMessage').textContent = `来源：${r.trustURL || '（地址不可解析）'}\n版本清单 SHA-256：${r.trustSHA256}${r.trustScriptSHA256 ? `\n主脚本：${r.trustScriptSHA256}\n配置脚本：${r.trustConfigSHA256 || "无"}` : ""}${r.mismatch ? `\n警告：发布者 MD5 不一致（本地 ${r.md5} / 发布 ${r.expectedMd5}）` : ''}`;
      $('#sourceTrust').dataset.pending = '1';
      if (!$('#sourceTrust').open) $('#sourceTrust').showModal();
      setDot(''); $('#status').textContent = '等待确认';
      setView(stateBox({ title: '插件尚未启动', text: '首次使用或检测到内容变化。请确认 SHA-256 后再运行。' }));
      return;
    }
    if (r.warnings && r.warnings.length) toast(`部分规则或源未加载：${r.warnings.join('；')}`, { err: true, ms: 8000 });
    const reloadedSettings = await call('GetSettings');
    if (!browseRequests.current(request) || seq !== st.loadSeq) return;
    st.settings = reloadedSettings;
    renderSwitcher();
    st.sites = r.sites || [];
    const usable = st.sites.filter((s) => !s.unsupported);
    setDot('ok'); $('#status').textContent = usable.length === st.sites.length ? `${st.sites.length}` : `${usable.length}/${st.sites.length}`;
    if (r.mismatch) toast('提示：index.js 与发布的 md5 不一致（文件可能已更新）');
    renderSites();
    if (usable.length) selectSite(usable[0]);
    else if (st.sites.length) showUnsupported(st.sites[0]);
    else setView(stateBox({ title: '这个订阅没有站点', text: '插件的 /config 没有返回任何站点。' }));
  } catch (e) {
    if (!browseRequests.current(request) || seq !== st.loadSeq) return;
    $('#sites').replaceChildren(); setDot('err'); $('#status').textContent = '';
    setView(stateBox({ ico: 'alert', error: true, title: `「${activeSub()?.name || ''}」加载失败`, text: errMsg(e),
      actions: [el('button', { class: 'btn', onclick: () => loadSource(fn) }, icon('reload'), '重试'), el('button', { class: 'btn', onclick: () => openSettings('subs') }, '管理订阅'), el('button', { class: 'btn', onclick: () => openSettings('logs') }, '查看日志')] }));
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
function showRepoPicker(lines) {
  setDot(''); $('#status').textContent = '选择线路';
  const actions = (lines || []).map((line) => el('button', { class: 'btn', onclick: () => loadSource(() => call('SelectRepoLine', line.name)) }, line.name));
  if (activeSub()?.line) actions.push(el('button', { class: 'ghost', onclick: () => loadSource(() => call('SelectRepoLine', '')) }, '重新选择'));
  setView(stateBox({ ico: 'plug', title: '这个地址是多仓配置', text: '选择一条线路后再加载其中的点播配置。', actions }));
}
function showUnsupported(s) {
  st.site = s; st.stack = []; st.tid = null; st.page = 1; st.filters = {};
  $('#back').hidden = true; $('#classes').replaceChildren(); $('#filters').replaceChildren(); $('#filters').hidden = true;
  renderSites();
  setView(stateBox({ title: s.name || s.key || '不支持的站点', text: s.reason || '这个站点不能在桌面或网页模式运行。' }));
}
const noSubState = () => stateBox({ ico: 'plug', title: '添加你的第一个订阅', text: '支持 JS 插件、M3U/TXT 直播、TVBox 配置和 Kazumi 规则仓库，可添加多个并随时切换。',
  actions: [el('button', { class: 'primary', onclick: () => openSettings('subs') }, icon('plus'), '添加订阅')] });

const hue = (s) => { let h = 0; for (const c of String(s)) h = (h * 31 + c.codePointAt(0)) % 360; return h; };
function renderSites() {
  const box = $('#sites'); box.replaceChildren();
  for (const s of st.sites) {
    const name = s.name || s.key;
    const off = !!s.unsupported;
    box.append(el('button', {
      class: 'site' + (st.site && st.site.api === s.api ? ' active' : '') + (off ? ' unsupported' : ''),
      title: off ? `${name}\n${s.reason || '这个站点不能在这里运行'}` : name,
      onclick: () => off ? showUnsupported(s) : selectSite(s),
    },
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
    const signal = browseRequests.signal();
    const home = await callWith(signal, 'Home', s.api);
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
          const r = await callWith(signal, 'Category', s.api, String(c.type_id), 1, defaultFilters(c.type_id));
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
          if ((r.list || []).length) { openCategory(c.type_id, 1); return; }
        } catch (e) {
          if (!browseRequests.current(request) || seq !== st.loadSeq || st.site !== s) return;
        }
      }
      openCategory(cls[0].type_id, 1);
    } else if (/liveSource/i.test(s.key || '') || /直播/.test(s.name || '')) {
      setView(stateBox({ico:'search',title:'暂无直播源',text:'请在插件配置页添加直播源，然后刷新站点。也可在订阅管理中直接添加 M3U/TXT。',actions:[el('button',{class:'btn',onclick:()=>call('OpenConfigPage',s.api).then((u)=>{if(window.__JSTV_WEB__)openWebPage(u,'直播配置');}).catch((e)=>toast(errMsg(e),{err:true}))},'配置直播源'),el('button',{class:'btn',onclick:()=>selectSite(s)},'刷新站点')]}));
    } else if (home.kazumi) {
      const k = home.kazumi;
      const mode = `搜索 ${String(k.searchMode || 'xpath').toUpperCase()} · 剧集 ${String(k.chapterMode || 'xpath').toUpperCase()}`;
      const ver = k.version ? ` v${k.version}` : '';
      const note = k.antiCrawler ? '（该规则开启了反爬验证，搜索可能触发人机验证）' : '';
      setView(stateBox({
        ico: 'search',
        title: `Kazumi 规则：${k.name || s.name}${ver}`,
        text: `${mode}${note}。Kazumi 规则通过关键词搜索解析剧集并嗅探播放。`,
        actions: [
          el('button', { class: 'primary', onclick: () => { setSiteOnlySearch(true); $('#q').focus(); } }, icon('search'), '搜索当前规则'),
          el('button', { class: 'btn', onclick: () => { setSiteOnlySearch(false); $('#q').focus(); } }, '搜索全部规则'),
        ],
      }));
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
  const signal = browseRequests.signal();
  if (String(st.tid) !== String(tid)) st.filters = defaultFilters(tid);
  else if (selected) st.filters = { ...selected };
  st.tid = tid; st.page = page;
  renderFilters(tid);
  [...$('#classes').children].forEach((b, i) => b.classList.toggle('active', String((st.home.class[i] || {}).type_id) === String(tid)));
  setView(skeletonGrid());
  const site = st.site;
  try {
    const r = await callWith(signal, 'Category', site.api, String(tid), page, { ...st.filters });
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
  const signal = browseRequests.signal();
  const id = String(vod.vod_id);
  const title = vod.vod_name || '';
  setView(skeletonGrid());
  try {
    const r = await callWith(signal, 'Category', site.api, id, 1, {});
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
      attachPosterImage(poster, v.vod_pic);
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
      if (String(v.vod_id).startsWith('ssearch:')) {
        st.site = site;
        setSiteOnlySearch(true);
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
  const signal = browseRequests.signal();
  setView(skeletonGrid(12));
  if ($('#onlySite').checked && st.site) {
    if (st.site.unsupported) {
      setView(stateBox({ title: st.site.name || '不支持的站点', text: st.site.reason || '这个站点不能搜索。' }));
      return;
    }
    try {
      const r = await callWith(signal, 'Search', st.site.api, wd, 1);
      if (!browseRequests.current(request)) return;
      st.stack = [];
      show(() => setView(el('div', { class: 'section-h' }, `“${wd}”`, el('span', { class: 'n' }, st.site.name)),
        (r.list || []).length ? cards(r.list, st.site) : stateBox({
          ico: 'search',
          title: '没有找到结果',
          text: '可以切换到全部站点继续搜索。',
          actions: [el('button', { class: 'primary', onclick: () => { setSiteOnlySearch(false); search(wd); } }, icon('search'), '搜索全部站点')],
        })));
    } catch (e) { if (browseRequests.current(request)) setView(errorState(e, () => search(wd))); }
    return;
  }
  const results = []; let done = false;
  const render = () => {
    const kids = [];
    for (const r of results) {
      if (!r._head) {
        r._head = el('div', { class: 'section-h' }, r.site.name, r.error ? el('span', { class: 'e' }, '出错') : el('span', { class: 'n' }, `${r.list.length} 个结果`));
        r._body = r.error ? el('p', { class: 'hint' }, r.error) : cards(r.list, r.site);
      }
      kids.push(r._head, r._body);
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
  try { await callWith(signal, 'SearchAll', wd, ch); } catch (e) { if (browseRequests.current(request)) toast(errMsg(e), { err: true }); }
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
  const signal = browseRequests.signal();
  setView(el('div', { class: 'hero' }, el('div', { class: 'cover skel' }), el('div', { class: 'info', style: 'flex:1' },
    el('div', { class: 'skel skel-line', style: 'height:26px;width:40%' }), el('div', { class: 'skel skel-line short', style: 'width:30%' }),
    el('div', { class: 'skel skel-line' }), el('div', { class: 'skel skel-line' }), el('div', { class: 'skel skel-line short' }))));
  let d;
  try { d = await callWith(signal, 'Detail', site.api, String(id)); if (!browseRequests.current(request)) return; } catch (e) { if (browseRequests.current(request)) setView(errorState(e, () => openDetail(site, id))); return; }
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
    let watched = !!(hist && hist.watched);
    const hero = el('div', { class: 'hero' }, bg, cover, el('div', { class: 'info' },
      el('h2', {}, v.vod_name || ''), meta.length ? el('div', { class: 'meta' }, meta) : null,
      v.vod_actor ? el('p', { class: 'cast' }, '主演：' + v.vod_actor) : null,
      v.vod_director ? el('p', { class: 'cast' }, '导演：' + v.vod_director) : null,
      desc ? el('p', { class: 'desc', title: desc }, desc) : null,
      el('div', { class: 'detail-actions' },
        el('button',{class:'btn',onclick:async(e)=>{const button=e.currentTarget;try{favorite=await call('ToggleFavorite',site.api,vodId,String(v.vod_name||vodId),String(v.vod_pic||''));button.textContent=favorite?'取消收藏':'收藏';}catch(error){toast(errMsg(error),{err:true});}}},favorite?'取消收藏':'收藏'),
        el('button',{class:'btn',onclick:async(e)=>{const button=e.currentTarget;try{await call('SetWatched',site.api,vodId,!watched);watched=!watched;button.textContent=watched?'已看完':'标为已看';}catch(error){toast(errMsg(error),{err:true});}}},watched?'已看完':'标为已看')),
      resumeButton()));
    function resumeButton() {
      const l = (d.lines || []).find((x) => hist && x.flag === hist.flag);
      if (!l || !l.episodes[hist.index]) return null;
      const next = hist.index;
      const pos = hist && hist.position > 0 ? ' · ' + formatClock(hist.position) : '';
      return el('div', { class: 'resume' }, el('button', { class: 'primary', onclick: (e) => play(site, l, next, vodId, v.vod_name, null) }, icon('play'), `继续播放 ${l.episodes[next].name}${pos}`));
    }
    const lines = d.lines || [];
    const body = el('div', { id: 'lines' });
    if (lines.length) {
      const tabButtons = lines.length > 1
        ? lines.map((l, i) => el('button', { class: i === lineIdx ? 'active' : '', onclick: () => { lineIdx = i; drawLine(true); } }, l.flag))
        : [];
      const tabs = lines.length > 1 ? el('div', { class: 'line-tabs' }, tabButtons) : el('div', { class: 'section-h' }, lines[0].flag);
      const eps = el('div', { class: 'eps' });
      body.append(tabs, eps);
      const focusActiveTab = (clicked) => {
        const activeBtn = tabButtons[lineIdx];
        if (!activeBtn) return;
        if (clicked && typeof activeBtn.focus === 'function') {
          try { activeBtn.focus({ preventScroll: true }); } catch (e) { activeBtn.focus(); }
        }
        if (typeof activeBtn.scrollIntoView === 'function') {
          activeBtn.scrollIntoView({ block: 'nearest', inline: clicked ? 'nearest' : 'center' });
        }
      };
      const drawLine = (clicked = false) => {
        const prevScroll = tabs.scrollLeft || 0;
        tabButtons.forEach((btn, i) => btn.classList.toggle('active', i === lineIdx));
        body.querySelector?.('.quality-pick')?.remove();
        const line = lines[lineIdx];
        eps.replaceChildren(...line.episodes.map((ep, i) => el('button', { class: 'ep' + (hist && hist.flag === line.flag && hist.index === i ? ' last' : ''), title: ep.name, onclick: (e) => play(site, line, i, vodId, v.vod_name, e.currentTarget) }, ep.name)));
        tabs.scrollLeft = prevScroll;
        focusActiveTab(clicked);
      };
      drawLine(false);
      setView(hero, body);
      focusActiveTab(false);
    } else {
      body.append(stateBox({ title: '没有播放列表', text: '插件没有返回任何剧集。' }));
      setView(hero, body);
    }
  });
}

async function play(site, line, idx, vodId, title, btn, quality) {
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
    const streams = (await call('PlayEpisodes', site.api, vodId, title, flag, line.episodes, idx, quality || '')) || [];
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
        el('div', { class: 'eps' }, streams.map((s) => el('button', { class: 'ep', onclick: () => { box.remove(); play(site, line, idx, vodId, title, btn, s.name || '').catch((e) => toast(errMsg(e), { err: true, ms: 6000 })); } }, s.name || '播放'))));
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
  $('#sSkipIntro').value = s.skipIntro || 0;
  $('#sSkipOutro').value = s.skipOutro || 0;
  $('#sStrict').checked = !!s.strictMd5; $('#sContinuous').checked = !s.disableContinuous; $('#sBridge').value = s.bridgeTemplate || '';
  $('#sBangumiToken').value = s.bangumiToken || '';
  $('#sAllowLan').checked = !!s.allowLan;
  $('#sAllowLanMedia').checked = !!s.allowLanMedia;
  $('#sInheritNodeEnv').checked = !!s.inheritNodeEnv;
  $('#sInheritProxyEnv').checked = !!s.inheritProxyEnv;
  $('#sPersistLogs').checked = !!s.persistLogs;
  $('#sHistoryEnabled').checked = !s.disableHistory;
  $('#sAdBlock').checked = !s.disableAdBlock;
  $('#sHlsAdFilter').checked = !s.disableHlsAdFilter;
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
    await call('SaveSettings', { nodePath: $('#sNode').value.trim(), player: $('#sPlayer').value, customPlayer: $('#sCustomPlayer').value.trim(), customArgs: $('#sCustomArgs').value.trim(), skipIntro: parseInt($('#sSkipIntro').value, 10) || 0, skipOutro: parseInt($('#sSkipOutro').value, 10) || 0, strictMd5: $('#sStrict').checked, disableContinuous: !$('#sContinuous').checked, bridgeTemplate: $('#sBridge').value.trim(), bangumiToken: $('#sBangumiToken').value.trim(), allowLan: $('#sAllowLan').checked, allowLanMedia: $('#sAllowLanMedia').checked, inheritProxyEnv: $('#sInheritProxyEnv').checked, inheritNodeEnv: $('#sInheritNodeEnv').checked, persistLogs: $('#sPersistLogs').checked, disableHistory: !$('#sHistoryEnabled').checked, disableAdBlock: !$('#sAdBlock').checked, disableHlsAdFilter: !$('#sHlsAdFilter').checked });
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
const btnCheckUpdate = $('#btnCheckUpdate');
const btnInstallUpdate = $('#btnInstallUpdate');
let latestUpdateInfo = null;
let checkingUpdate = false;
async function runCheckUpdate() {
  if (!btnCheckUpdate || !btnInstallUpdate || checkingUpdate) return;
  const status = $('#updateStatus');
  const notes = $('#updateNotes');
  checkingUpdate = true;
  btnCheckUpdate.disabled = true;
  btnCheckUpdate.textContent = '检查中…';
  btnInstallUpdate.hidden = true;
  if (notes) { notes.hidden = true; notes.textContent = ''; }
  if (status) { status.hidden = false; status.textContent = '正在检查最新版本…'; }
  try {
    const info = await call('CheckUpdate');
    latestUpdateInfo = info;
    if (info && info.available) {
      const label = info.canAutoInstall
        ? `发现新版本 v${info.latestVersion}（当前 v${info.currentVersion}），可一键更新并重启。`
        : `发现新版本 v${info.latestVersion}（当前 v${info.currentVersion}）${info.assetName ? '：' + info.assetName : ''}。`;
      if (status) status.textContent = label;
      if (notes && info.notes) { notes.textContent = info.notes; notes.hidden = false; }
      btnInstallUpdate.textContent = info.canAutoInstall ? '立即更新并重启' : (info.downloadUrl ? '一键下载更新' : '打开发布页');
      btnInstallUpdate.hidden = false;
    } else {
      const ver = (info && (info.currentVersion || info.latestVersion)) || '';
      if (status) status.textContent = ver ? `已是最新版本（v${ver}）。` : '已是最新版本。';
    }
  } catch (e) {
    latestUpdateInfo = null;
    if (status) status.textContent = errMsg(e);
    toast(errMsg(e), { err: true });
  } finally {
    checkingUpdate = false;
    btnCheckUpdate.disabled = false;
    btnCheckUpdate.textContent = '检查更新';
  }
}
async function openUpdateDialog(autoCheck = true) {
  await openSettings('about');
  if (autoCheck) await runCheckUpdate();
}
if (btnCheckUpdate && btnInstallUpdate) {
  btnCheckUpdate.onclick = () => runCheckUpdate();
  btnInstallUpdate.onclick = async () => {
    const status = $('#updateStatus');
    const auto = !!(latestUpdateInfo && latestUpdateInfo.canAutoInstall);
    btnInstallUpdate.disabled = true;
    btnCheckUpdate.disabled = true;
    btnInstallUpdate.textContent = auto ? '正在下载并更新…' : '正在打开…';
    if (status && auto) status.textContent = '正在下载并校验更新，完成后将自动重启…';
    try {
      const result = await call('InstallUpdate');
      if (result === 'installed') {
        toast('更新已安装，正在重启…');
      } else if (result && window.__JSTV_WEB__) {
        const opened = window.open(result, '_blank', 'noopener,noreferrer');
        if (!opened) toast('请允许弹出窗口以打开更新链接', { err: true });
      } else if (result) {
        toast('已在浏览器中打开更新下载');
      }
    } catch (e) {
      if (status) status.textContent = errMsg(e);
      toast(errMsg(e), { err: true });
    } finally {
      btnInstallUpdate.disabled = false;
      btnCheckUpdate.disabled = false;
      btnInstallUpdate.textContent = auto ? '立即更新并重启' : (latestUpdateInfo && latestUpdateInfo.downloadUrl ? '一键下载更新' : '打开发布页');
    }
  };
}

$('#btnSettings').onclick = () => openSettings('subs');
const btnSidebarUpdate = $('#btnSidebarUpdate');
if (btnSidebarUpdate) btnSidebarUpdate.onclick = () => openUpdateDialog(true);
window.__jstvOpenUpdate = () => openUpdateDialog(true);
window.__jstvOpenSettings = () => openSettings('subs');
window.__jstvReloadSource = () => loadSource(() => call('ForceRefreshSource'));
function siteForAPI(api, fallbackName) {
  return st.sites.find((x) => x.api === api) || { api, name: fallbackName || '媒体库' };
}

function continueCards(items) {
  const grid = el('div', { class: 'grid' });
  for (const item of items) {
    const site = siteForAPI(item.api, '继续观看');
    const poster = el('div', { class: 'poster' + (item.picture ? '' : ' noimg') });
    if (item.picture) {
      attachPosterImage(poster, item.picture);
    }
    const pct = item.duration > 0 ? Math.max(0, Math.min(100, Math.round((item.position / item.duration) * 100))) : 0;
    if (item.episodeName) poster.append(el('span', { class: 'badge' }, item.episodeName));
    if (pct > 0) poster.append(el('div', { class: 'prog-bar' }, el('div', { class: 'prog-fill', style: `width:${pct}%` })));
    poster.append(el('span', { class: 'play' }, icon('play')));
    const timeInfo = item.position > 0 && item.duration > 0
      ? `${formatClock(item.position)} / ${formatClock(item.duration)}`
      : (site.name || '');
    grid.append(el('button', { class: 'card', title: item.showName || item.id, onclick: () => openDetail(site, item.id) },
      poster,
      el('div', { class: 't' }, item.showName || item.id),
      el('div', { class: 's' }, timeInfo || '\u00a0')));
  }
  return grid;
}

async function exportLibraryFile() {
  try {
    const backup = await call('ExportLibrary');
    const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '');
    const blob = new Blob([JSON.stringify(backup, null, 2)], { type: 'application/json' });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `jstv-library-${stamp}.json`;
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(link.href), 2000);
    toast('媒体库备份已导出');
  } catch (e) {
    toast(errMsg(e), { err: true });
  }
}

function pickImportLibraryFile(onDone) {
  const input = el('input', { type: 'file', accept: '.json,application/json', hidden: true });
  input.addEventListener('change', async () => {
    const file = input.files && input.files[0];
    input.remove();
    if (!file) return;
    try {
      const parsed = JSON.parse(await file.text());
      const res = await call('ImportLibrary', parsed);
      toast(`导入完成：收藏 +${res.favoritesAdded} / 更新 ${res.favoritesUpdated}，历史 +${res.historyAdded} / 更新 ${res.historyUpdated}`);
      onDone();
    } catch (e) {
      toast('导入失败：' + errMsg(e), { err: true });
    }
  });
  document.body.append(input);
  input.click();
}

async function openLibrary() {
  const request = browseRequests.begin();
  try {
    const [favoritesRaw, historyRaw] = await Promise.all([call('GetFavorites'), call('ListHistory')]);
    if (!browseRequests.current(request)) return;
    const favorites = Array.isArray(favoritesRaw) ? favoritesRaw : [];
    const history = Array.isArray(historyRaw) ? historyRaw : [];
    const inProgress = history.filter((item) => !item.watched);
    st.stack = [];
    $('#classes').replaceChildren();
    $('#filters').hidden = true;
    const render = () => {
      const clearBtn = history.length ? el('button', {
        class: 'btn',
        onclick: async (e) => {
          const btn = e.currentTarget;
          if (!btn.dataset.confirm) {
            btn.dataset.confirm = '1';
            btn.textContent = '确认清空';
            btn.classList.add('confirm');
            setTimeout(() => { if (btn.isConnected) { delete btn.dataset.confirm; btn.textContent = '清空历史'; btn.classList.remove('confirm'); } }, 3000);
            return;
          }
          try {
            await call('ClearHistory');
            toast('观看历史已清空');
            openLibrary();
          } catch (err) { toast(errMsg(err), { err: true }); }
        },
      }, '清空历史') : null;
      const header = el('div', { class: 'lib-head' },
        el('div', {},
          el('h2', { class: 'lib-title' }, '媒体库'),
          el('div', { class: 'hint small' }, `继续观看 ${inProgress.length} · 收藏 ${favorites.length} · 历史 ${history.length}${st.settings && st.settings.disableHistory ? '（已暂停记录历史）' : ''}`)),
        el('div', { class: 'lib-actions' },
          el('button', { class: 'btn', onclick: exportLibraryFile }, '导出备份'),
          el('button', { class: 'btn', onclick: () => pickImportLibraryFile(openLibrary) }, '导入备份'),
          clearBtn));

      const tabs = [
        ['all', '全部'],
        ['continue', `继续观看 (${inProgress.length})`],
        ['saved', `收藏想看 (${favorites.length})`],
        ['history', `观看历史 (${history.length})`],
      ];
      const sortSelect = el('select', {
        'aria-label': '收藏排序',
        onchange: (e) => { st.libSort = e.target.value; render(); },
      },
        el('option', { value: 'recent' }, '收藏：最近添加'),
        el('option', { value: 'title' }, '收藏：名称 A-Z'));
      sortSelect.value = st.libSort;
      const tabBar = el('div', { class: 'lib-tabs' },
        el('div', { class: 'lib-tab-group' }, tabs.map(([id, label]) =>
          el('button', { class: 'chip' + (st.libTab === id ? ' active' : ''), onclick: () => { st.libTab = id; render(); } }, label))),
        (st.libTab === 'all' || st.libTab === 'saved') && favorites.length > 1 ? sortSelect : null);

      const parts = [header, tabBar];
      let shown = 0;

      if ((st.libTab === 'all' || st.libTab === 'continue') && inProgress.length) {
        parts.push(el('div', { 'data-lib-section': 'continue' }, el('div', { class: 'section-h' }, '继续观看'), continueCards(inProgress)));
        shown += inProgress.length;
      }

      if ((st.libTab === 'all' || st.libTab === 'saved') && favorites.length) {
        const sortedFavs = [...favorites];
        if (st.libSort === 'title') sortedFavs.sort((a, b) => String(a.name || '').localeCompare(String(b.name || ''), 'zh-CN'));
        const savedSection = el('div', { 'data-lib-section': 'saved' });
        if (st.libSort === 'recent') {
          const groups = new Map();
          for (const item of sortedFavs) {
            if (!groups.has(item.api)) groups.set(item.api, []);
            groups.get(item.api).push({ vod_id: item.id, vod_name: item.name, vod_pic: item.picture });
          }
          savedSection.append(el('div', { class: 'section-h' }, `收藏 / 想看 (${sortedFavs.length})`));
          for (const [api, items] of groups) {
            const site = siteForAPI(api, '收藏');
            savedSection.append(el('div', { class: 'lib-sub-h' }, site.name), cards(items, site));
          }
        } else {
          savedSection.append(el('div', { class: 'section-h' }, `收藏 / 想看 (${sortedFavs.length})`));
          const grid = el('div', { class: 'grid' });
          for (const item of sortedFavs) {
            const site = siteForAPI(item.api, '收藏');
            const poster = el('div', { class: 'poster' + (item.picture ? '' : ' noimg') });
            if (item.picture) {
              attachPosterImage(poster, item.picture);
            }
            poster.append(el('span', { class: 'play' }, icon('play')));
            grid.append(el('button', { class: 'card', title: item.name || item.id, onclick: () => openDetail(site, item.id) },
              poster, el('div', { class: 't' }, item.name || item.id), el('div', { class: 's' }, site.name || '\u00a0')));
          }
          savedSection.append(grid);
        }
        parts.push(savedSection);
        shown += sortedFavs.length;
      }

      if ((st.libTab === 'all' || st.libTab === 'history') && history.length) {
        const list = el('div', { class: 'hist-list' });
        for (const item of history) {
          const site = siteForAPI(item.api, '历史');
          const thumb = el('div', { class: 'hist-thumb' + (item.picture ? '' : ' noimg') });
          if (item.picture) {
            attachPosterImage(thumb, item.picture, { fade: false });
          }
          const metaParts = [site.name, item.episodeName];
          if (item.position > 0 && item.duration > 0) metaParts.push(`${formatClock(item.position)} / ${formatClock(item.duration)}`);
          if (item.time) metaParts.push(formatShortDate(item.time));
          list.append(el('div', { class: 'hist-row', onclick: () => openDetail(site, item.id) },
            thumb,
            el('div', { class: 'hist-meta' },
              el('div', { class: 'hist-title' },
                el('span', {}, item.showName || item.id),
                item.watched ? el('span', { class: 'badge-watched' }, '已看') : null),
              el('div', { class: 'hist-sub' }, metaParts.filter(Boolean).join(' · '))),
            el('div', { class: 'hist-actions', onclick: (e) => e.stopPropagation() },
              el('button', {
                class: 'btn',
                onclick: async () => {
                  try { await call('SetWatched', item.api, item.id, !item.watched); openLibrary(); }
                  catch (err) { toast(errMsg(err), { err: true }); }
                },
              }, item.watched ? '标为未看' : '标为已看'),
              el('button', {
                class: 'btn',
                title: '从观看历史移除',
                onclick: async () => {
                  try { await call('RemoveHistory', item.api, item.id); openLibrary(); }
                  catch (err) { toast(errMsg(err), { err: true }); }
                },
              }, '移除'))));
        }
        parts.push(el('div', { 'data-lib-section': 'history' }, el('div', { class: 'section-h' }, `观看历史 (${history.length})`), list));
        shown += history.length;
      }

      if (!shown) {
        parts.push(stateBox({ title: '媒体库还是空的', text: '播放节目或点击“收藏”后，这里会汇总继续观看、想看清单和观看历史。' }));
      }
      setView(...parts);
    };
    show(render);
  } catch (e) {
    if (browseRequests.current(request)) setView(errorState(e));
  }
}
$('#btnFavorites').onclick = () => openLibrary();
$('#btnForceReload').onclick = () => { $('#settings').close(); loadSource(() => call('ForceRefreshSource')); };
$('#btnReload').onclick = () => loadSource(() => call('ForceRefreshSource'));
$('#back').onclick = back;
$('#q').addEventListener('keydown', (e) => { if (e.key === 'Enter') search(e.target.value.trim()); });
function setSiteOnlySearch(on) {
  $('#onlySite').checked = !!on;
  $('#q').placeholder = on ? '搜索当前站点' : '搜索全部站点';
}
$('#onlySite').addEventListener('change', (e) => setSiteOnlySearch(e.target.checked));

const isMac = typeof navigator !== 'undefined' && (/Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent));
const searchKbd = $('#searchKbd');
if (searchKbd) searchKbd.textContent = isMac ? '⌘K' : 'Ctrl+K';

(async () => {
  st.settings = await call('GetSettings'); renderSwitcher();
  if (activeSub()) loadSource(() => call('StartCachedSource')); else { setView(noSubState()); openSettings('subs'); }
})();
