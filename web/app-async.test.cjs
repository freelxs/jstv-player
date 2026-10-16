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
  const window = { mygo: { call: (method, ...args) => callImpl(method.replace('Player.', ''), ...args), channel: () => 'mock-channel' } };
  const src = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
  const startup = src.lastIndexOf('\n(async () => {');
  assert.ok(startup > 0, 'find application startup block');
  const code = `${fs.readFileSync(path.join(__dirname, 'request-token.js'), 'utf8')}\n${fs.readFileSync(path.join(__dirname, 'youtube-help.js'), 'utf8')}\n${src.slice(0, startup)}\nglobalThis.app = { st, selectSite, openCategory, loadSource, search, openDetail };`;
  const context = { document, window, navigator: { userAgent: 'mock' }, BrowseRequestToken: createRequestToken, youtubeLoginHelp,
    setTimeout, clearTimeout, URL, console, Promise };
  vm.runInNewContext(code, context, { filename: 'app.js' });
  return { app: context.app, el: get };
}

const site = (name, api = name) => ({ name, key: name, api });
const settle = () => new Promise((resolve) => setImmediate(resolve));

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
