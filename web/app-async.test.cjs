const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { createRequestToken } = require('./request-token.js');
const { youtubeLoginHelp } = require('./youtube-help.js');

class MockElement {
  constructor(tag = 'div') {
    this.tagName = tag.toUpperCase(); this.children = []; this.dataset = {}; this.style = {};
    this.attributes = {}; this.hidden = false; this.value = ''; this.checked = true; this.scrollTop = 0;
    this.className = ''; this.listeners = {}; this.isConnected = true;
    this.classList = {
      add: (...names) => { const s = new Set(this.className.split(/\s+/).filter(Boolean)); names.forEach((n) => s.add(n)); this.className = [...s].join(' '); },
      remove: (...names) => { const s = new Set(this.className.split(/\s+/).filter(Boolean)); names.forEach((n) => s.delete(n)); this.className = [...s].join(' '); },
      contains: (name) => this.className.split(/\s+/).includes(name),
      toggle: (name, force) => { const has = this.classList.contains(name), next = force === undefined ? !has : !!force; next ? this.classList.add(name) : this.classList.remove(name); return next; },
    };
  }
  append(...items) { for (const item of items) this.children.push(typeof item === 'string' ? Object.assign(new MockElement('#text'), { textContent: item }) : item); }
  prepend(...items) { this.children.unshift(...items); }
  replaceChildren(...items) { this.children = []; this.append(...items); }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  setAttribute(name, value) { this.attributes[name] = String(value); if (name === 'data-tab') this.dataset.tab = String(value); if (name === 'data-pane') this.dataset.pane = String(value); }
  remove() { this.isConnected = false; }
  focus() {}
  scrollIntoView() {}
  showModal() { this.open = true; }
  close() { this.open = false; }
  get offsetWidth() { return 1; }
  get textContent() { return this.children.map((c) => c.textContent || '').join('') + (this._text || ''); }
  set textContent(v) { this.children = []; this._text = String(v); }
}

function deferred() {
  let resolve, reject;
  const promise = new Promise((a, b) => { resolve = a; reject = b; });
  return { promise, resolve, reject };
}

function makeApp(callImpl = async () => ({})) {
  const elements = new Map();
  const get = (selector) => { if (!elements.has(selector)) elements.set(selector, new MockElement(selector.startsWith('#') ? selector.slice(1) : 'div')); return elements.get(selector); };
  const document = {
    querySelector: get,
    querySelectorAll: () => [],
    createElement: (tag) => new MockElement(tag),
    addEventListener() {},
  };
  const channels = [];
  const window = { mygo: { call: (method, ...args) => callImpl(method.replace('Player.', ''), ...args), channel: (onmessage) => {
    const ch = { onmessage, closed: false, close() { this.closed = true; } };
    channels.push(ch);
    return ch;
  } } };
  const src = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
  const startup = src.lastIndexOf('\n(async () => {');
  assert.ok(startup > 0, 'find application startup block');
  const code = `${fs.readFileSync(path.join(__dirname, 'request-token.js'), 'utf8')}\n${fs.readFileSync(path.join(__dirname, 'youtube-help.js'), 'utf8')}\n${src.slice(0, startup)}\nglobalThis.app = { st, selectSite, openCategory, loadSource, search, openDetail, filtersForCategory, cards };`;
  const context = { document, window, navigator: { userAgent: 'mock' }, BrowseRequestToken: createRequestToken, youtubeLoginHelp,
    setTimeout, clearTimeout, URL, console, Promise };
  vm.runInNewContext(code, context, { filename: 'app.js' });
  return { app: context.app, el: get, channels };
}

const site = (name, api = name) => ({ name, key: name, api });
const settle = () => new Promise((resolve) => setImmediate(resolve));

test('recommendation card searches every site and resets the search hint', async () => {
  const harness = makeApp(async () => ({}));
  harness.app.st.sites = [site('豆瓣')];
  harness.app.st.site = harness.app.st.sites[0];
  const only = harness.el('#onlySite');
  const q = harness.el('#q');
  only.checked = true;
  only.listeners.change({ target: only });
  assert.equal(q.placeholder, '搜索当前站点');
  const grid = harness.app.cards([{ vod_id: 'msearch:1', vod_name: '韩国制造' }], harness.app.st.site);
  grid.children[0].listeners.click();
  await settle();
  assert.equal(only.checked, false);
  assert.equal(q.value, '韩国制造');
  assert.equal(q.placeholder, '搜索全部站点');
});

test('selectSite ignores stale Home success and stale Home failure', async () => {
  for (const fail of [false, true]) {
    const oldHome = deferred(), newHome = deferred();
    const app = makeApp((method, api) => method === 'Home' ? (api === 'old' ? oldHome.promise : newHome.promise) : Promise.resolve({ list: [] })).app;
    const old = site('old'), current = site('current');
    app.st.sites = [old, current]; app.st.home = { marker: 'kept' };
    const p1 = app.selectSite(old), p2 = app.selectSite(current);
    if (fail) oldHome.reject(new Error('stale failure')); else oldHome.resolve({ marker: 'stale', class: [] });
    await p1;
    assert.equal(app.st.home.marker, 'kept');
    newHome.resolve({ marker: 'current', class: [] }); await p2;
    assert.equal(app.st.home.marker, 'current');
  }
});

test('openCategory ignores stale success and stale failure, including same-site paging', async () => {
  for (const fail of [false, true]) {
    const oldResult = deferred(), newResult = deferred();
    const app = makeApp((method, _api, tid) => method === 'Category' ? (tid === 'old' ? oldResult.promise : newResult.promise) : Promise.resolve({}));
    app.app.st.site = site('one'); app.app.st.home = { class: [] };
    const p1 = app.app.openCategory('old', 1), p2 = app.app.openCategory('new', 2);
    if (fail) oldResult.reject(new Error('stale category')); else oldResult.resolve({ list: [{ vod_name: 'STALE' }] });
    await p1; assert.ok(!app.el('#view').textContent.includes('STALE'));
    newResult.resolve({ list: [{ vod_name: 'CURRENT' }] }); await p2;
    assert.ok(app.el('#view').textContent.includes('CURRENT'));
  }
});

test('catvod filter metadata uses name, n/v values, and init', () => {
  const app = makeApp().app;
  const home = { filters: { '1': [
    { key: 'area', name: '地区', init: '美国', value: [{ n: '全部', v: '' }, { n: '美国', v: '美国' }, { n: '日本', v: '日本' }] },
    { key: 'year', name: '年份', value: [{ n: '2024', v: 2024 }] },
  ] } };
  const parsed = app.filtersForCategory(home, 1);
  assert.equal(parsed.length, 2);
  assert.equal(parsed[0].label, '地区');
  assert.equal(parsed[0].init, '美国');
  assert.deepEqual([...parsed[0].options.map((o) => o.value)], ['美国', '日本']);
  assert.equal(parsed[1].options[0].value, '2024');
  assert.equal(parsed[1].options[0].label, '2024');
});

test('opening a category sends catvod init filters', async () => {
  const calls = [];
  const app = makeApp(async (method, ...args) => {
    calls.push([method, ...args]);
    if (method === 'Home') return { class: [{ type_id: '1', type_name: '电影' }], filters: {
      '1': [{ key: 'area', name: '地区', init: '美国', value: [{ n: '全部', v: '' }, { n: '美国', v: '美国' }] }],
    } };
    if (method === 'Category') return { list: [{ vod_id: 'x', vod_name: 'mock' }], pagecount: 2 };
    return {};
  });
  app.app.st.sites = [site('first')];
  await app.app.selectSite(site('first'));
  const cats = calls.filter((c) => c[0] === 'Category');
  assert.ok(cats.length >= 1);
  assert.deepEqual({ ...cats.at(-1)[4] }, { area: '美国' });
});

test('duplicate catvod filter options do not hide the rest of the category', () => {
  const app = makeApp().app;
  const parsed = app.filtersForCategory({ filters: { '1': [
    { key: 'cid', name: '类型', value: [{ n: '全部', v: '-1' }, { n: '全部', v: '-1' }, { n: '青春校园', v: '5' }] },
  ] } }, '1');
  assert.equal(parsed.length, 1);
  assert.deepEqual([...parsed[0].options.map((o) => o.value)], ['-1', '5']);
  assert.deepEqual([...parsed[0].options.map((o) => o.label)], ['全部', '青春校园']);
});

test('filter metadata is strict, safe on malformed input, and scoped to the selected category', () => {
  const app = makeApp().app;
  const supported = { filters: { '1': [{ key: 'area', label: '地区', options: [{ value: 'hk', label: '香港' }] }], '2': [] } };
  assert.equal(app.filtersForCategory(supported, '1').length, 1);
  assert.equal(app.filtersForCategory(supported, '2').length, 0);
  for (const bad of [null, [], { '1': {} }, { '1': [{ key: 'x', label: 'X', options: [{ value: 1, label: 'bad' }] }] }]) {
    assert.equal(app.filtersForCategory({ filters: bad }, '1').length, 0);
  }
});

test('filters are sent on category selection, reset page on change, persist across paging, and clear on category/site change', async () => {
  const calls = [];
  const app = makeApp(async (method, ...args) => {
    calls.push([method, ...args]);
    if (method === 'Home' && args[0] === 'second') return { class: [{ type_id: '3', type_name: '其他' }] };
    if (method === 'Home') return { class: [{ type_id: '1', type_name: '电影' }, { type_id: '2', type_name: '剧集' }], filters: {
      '1': [{ key: 'area', label: '地区', options: [{ value: 'hk', label: '香港' }, { value: 'us', label: '美国' }] }],
    } };
    if (method === 'Category') return { list: [{ vod_id: 'x', vod_name: 'mock' }], pagecount: 3 };
    return {};
  });
  const s = site('first'); app.app.st.sites = [s];
  await app.app.selectSite(s);
  await app.app.openCategory('1', 3);
  const select = app.el('#filters').children[0].children[1];
  select.listeners.change({ target: { value: 'hk' } });
  await settle();
  assert.deepEqual({ ...app.app.st.filters }, { area: 'hk' });
  assert.equal(calls.at(-1)[3], 1);
  assert.deepEqual({ ...calls.at(-1)[4] }, { area: 'hk' });
  await app.app.openCategory('1', 2);
  assert.deepEqual({ ...calls.at(-1)[4] }, { area: 'hk' });
  await app.app.openCategory('2', 1);
  assert.deepEqual({ ...app.app.st.filters }, {});
  await app.app.selectSite(site('second'));
  assert.deepEqual({ ...app.app.st.filters }, {});
  assert.equal(app.el('#filters').hidden, true);
});

test('loadSource does not assign stale first or second GetSettings results', async () => {
  const first = deferred();
  const app1 = makeApp((method) => method === 'GetSettings' ? first.promise : Promise.resolve({})).app;
  app1.st.settings = { marker: 'kept' };
  const pendingFirst = app1.loadSource();
  await app1.selectSite(site('other'));
  first.resolve({ marker: 'stale-first' }); await pendingFirst;
  assert.equal(app1.st.settings.marker, 'kept');

  const load = deferred(), second = deferred(); let settingsCalls = 0;
  const app2 = makeApp((method) => {
    if (method === 'GetSettings') return ++settingsCalls === 1 ? Promise.resolve({ active: 'sub', subscriptions: [{ id: 'sub', name: 'mock' }] }) : second.promise;
    return Promise.resolve({ sites: [] });
  }).app;
  const pendingSecond = app2.loadSource(() => load.promise);
  await settle(); load.resolve({ sites: [] }); await settle();
  assert.equal(settingsCalls, 2);
  await app2.selectSite(site('other'));
  second.resolve({ marker: 'stale-second' }); await pendingSecond;
  assert.equal(app2.st.settings.active, 'sub');
});

test('first use and hash changes require explicit approval before source load continues', async () => {
  let sourceCalls = 0, approvals = 0;
  const settings = { active: 'sub', subscriptions: [{ id: 'sub', name: 'mock', url: 'https://source.invalid/index.js' }] };
  const app = makeApp((method) => {
    if (method === 'GetSettings') return Promise.resolve(settings);
    if (method === 'LoadSource') return Promise.resolve(++sourceCalls === 1
      ? { trustRequired: true, trustSHA256: 'abc123', trustURL: 'https://source.invalid/index.js' }
      : { sites: [] });
    if (method === 'ApproveSource') { approvals++; return Promise.resolve(); }
    return Promise.resolve({});
  });
  await app.app.loadSource();
  assert.equal(app.el('#sourceTrust').open, true);
  assert.match(app.el('#trustMessage').textContent, /abc123/);
  assert.equal(sourceCalls, 1, 'trust-required load must not continue into sites');
  await app.el('#trustApprove').onclick();
  assert.equal(approvals, 1);
  assert.equal(sourceCalls, 2, 'approval must trigger a fresh download and load');
  assert.equal(app.el('#sourceTrust').open, false);
});

test('rejecting first-use trust does not approve or retry the source', async () => {
  let approvals = 0, loads = 0;
  const app = makeApp((method) => {
    if (method === 'GetSettings') return Promise.resolve({ active: 'sub', subscriptions: [{ id: 'sub', name: 'mock' }] });
    if (method === 'LoadSource') { loads++; return Promise.resolve({ trustRequired: true, trustSHA256: 'deadbeef' }); }
    if (method === 'ApproveSource') approvals++;
    return Promise.resolve({});
  });
  await app.app.loadSource();
  app.el('#trustReject').onclick();
  assert.equal(approvals, 0);
  assert.equal(loads, 1);
  assert.match(app.el('#toast').textContent, /未批准/);
});

test('all-site search closes its channel when the user navigates away', async () => {
  const pending = deferred();
  const harness = makeApp((method) => method === 'SearchAll' ? pending.promise : Promise.resolve({ list: [] }));
  harness.el('#onlySite').checked = false;
  harness.app.st.sites = [site('one')];
  harness.app.st.site = site('one');
  harness.app.st.home = { class: [] };
  const pendingSearch = harness.app.search('万物生灵');
  await settle();
  assert.equal(harness.channels.length, 1);
  assert.equal(harness.channels[0].closed, false);
  const category = harness.app.openCategory('1', 1);
  await settle();
  assert.equal(harness.channels[0].closed, true);
  pending.resolve();
  await pendingSearch;
  await category;
});

test('search and detail ignore stale successes and failures', async () => {
  for (const fail of [false, true]) {
    const searchResult = deferred();
    const app = makeApp((method) => method === 'Search' ? searchResult.promise : Promise.resolve({}));
    app.app.st.sites = [site('one')]; app.app.st.site = site('one');
    const pendingSearch = app.app.search('old query');
    await app.app.openCategory('newer', 1);
    if (fail) searchResult.reject(new Error('stale search')); else searchResult.resolve({ list: [{ vod_name: 'STALE SEARCH' }] });
    await pendingSearch;
    assert.ok(!app.el('#view').textContent.includes('STALE SEARCH'));

    const detail = deferred();
    const app2 = makeApp((method) => method === 'Detail' ? detail.promise : Promise.resolve(null));
    const pendingDetail = app2.app.openDetail(site('one'), 'id');
    await app2.app.openCategory('newer', 1);
    if (fail) detail.reject(new Error('stale detail')); else detail.resolve({ vod: { vod_name: 'STALE DETAIL' }, lines: [] });
    await pendingDetail;
    assert.ok(!app2.el('#view').textContent.includes('STALE DETAIL'));
  }
});
